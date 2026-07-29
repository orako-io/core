// SPDX-License-Identifier: AGPL-3.0-or-later

package oauth

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// ServeToken handles both grants Orako's AS supports: authorization_code
// (code+PKCE-verifier → token pair) and refresh_token (rotate). Real Claude
// Code's DCR requests refresh_token even though nothing required it (see
// spike-findings.md) — locked decision: support it, don't silently drop it.
func (s *Server) ServeToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "malformed form body: "+err.Error())

		return
	}

	switch r.PostForm.Get("grant_type") {
	case GrantTypeAuthorizationCode:
		s.serveAuthCodeGrant(w, r)
	case GrantTypeRefreshToken:
		s.serveRefreshGrant(w, r)
	default:
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "grant_type must be authorization_code or refresh_token")
	}
}

// serveAuthCodeGrant exchanges a single-use authorization code plus its PKCE
// verifier for a fresh access+refresh token pair.
func (s *Server) serveAuthCodeGrant(w http.ResponseWriter, r *http.Request) {
	form := r.PostForm

	rawCode := form.Get(ResponseTypeCode)
	verifier := form.Get("code_verifier")
	clientID := form.Get("client_id")
	redirectURI := form.Get("redirect_uri")
	reqResource := form.Get("resource")

	ac, err := s.store.ConsumeAuthCode(r.Context(), rawCode)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "unknown, already-used, or expired code")

		return
	}

	if ac.ClientID != clientID {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "client_id does not match the code")

		return
	}

	if ac.RedirectURI != redirectURI {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "redirect_uri does not match the code")

		return
	}

	if ac.CodeChallengeMethod != PKCEMethodS256 || !VerifyPKCE(ac.CodeChallenge, verifier) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "PKCE verification failed")

		return
	}

	// RFC 8707: a resource repeated at /token must match the one bound to the
	// code at /authorize — it narrows, it never widens, the audience.
	if reqResource != "" && reqResource != ac.Resource {
		writeOAuthError(w, http.StatusBadRequest, "invalid_target", "resource does not match the one requested at authorize")

		return
	}

	access, refresh, err := mintTokenPair(r.Context(), s.store, s.now(), ac.OrgID, ac.MemberID, ac.ClientID, ac.Resource, ac.ProjectIDs, uuid.New())
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not issue tokens")

		return
	}

	writeTokenResponse(w, access, refresh)
}

// serveRefreshGrant rotates a refresh token: the presented token is revoked
// and a new access+refresh pair is issued under the same grant, so a future
// "disconnect this agent" action can invalidate every token the rotation
// chain ever produced with one Store.RevokeGrant call. A refresh token
// presented after it was already rotated away is reuse — the theft-response
// per RFC 6749 §10.4 is to revoke the entire grant, not just the reused
// token.
func (s *Server) serveRefreshGrant(w http.ResponseWriter, r *http.Request) {
	form := r.PostForm

	rawRefresh := form.Get(GrantTypeRefreshToken)
	clientID := form.Get("client_id")
	reqResource := form.Get("resource")

	tok, err := s.store.GetToken(r.Context(), rawRefresh, TokenKindRefresh)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "unknown refresh token")

		return
	}

	if tok.ClientID != clientID {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "client_id does not match the refresh token")

		return
	}

	if tok.Revoked() {
		if revokeErr := s.store.RevokeGrant(r.Context(), tok.GrantID); revokeErr != nil {
			writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not respond to refresh token reuse")

			return
		}

		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "refresh token reuse detected; the whole grant has been revoked")

		return
	}

	if tok.ExpiredAt(s.now()) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "refresh token expired")

		return
	}

	if reqResource != "" && reqResource != tok.Resource {
		writeOAuthError(w, http.StatusBadRequest, "invalid_target", "resource does not match the token's original grant")

		return
	}

	// tok.ProjectIDs is carried forward: refresh rotation must never widen (or
	// drop) the connection's project scope.
	access, refresh, claimed, err := mintRotatedTokenPair(r.Context(), s.store, s.now(), tok)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not issue tokens")

		return
	}

	if !claimed {
		if revokeErr := s.store.RevokeGrant(r.Context(), tok.GrantID); revokeErr != nil {
			writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not respond to refresh token reuse")

			return
		}

		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "refresh token reuse detected; the whole grant has been revoked")

		return
	}

	writeTokenResponse(w, access, refresh)
}

type generatedTokenPair struct {
	accessSecret  string
	accessHash    []byte
	accessToken   Token
	refreshSecret string
	refreshHash   []byte
	refreshToken  Token
}

func generateTokenPair(
	now time.Time,
	orgID uuid.UUID,
	memberID uuid.UUID,
	clientID string,
	resource string,
	projectIDs []uuid.UUID,
	grantID uuid.UUID,
) (generatedTokenPair, error) {
	accessSecret, accessHash, err := newOpaqueSecret(AccessTokenPrefix)
	if err != nil {
		return generatedTokenPair{}, fmt.Errorf("minting access token: %w", err)
	}

	refreshSecret, refreshHash, err := newOpaqueSecret(RefreshTokenPrefix)
	if err != nil {
		return generatedTokenPair{}, fmt.Errorf("minting refresh token: %w", err)
	}

	return generatedTokenPair{
		accessSecret: accessSecret,
		accessHash:   accessHash,
		accessToken: Token{
			ID:         uuid.New(),
			OrgID:      orgID,
			MemberID:   memberID,
			ClientID:   clientID,
			Resource:   resource,
			Kind:       TokenKindAccess,
			ProjectIDs: projectIDs,
			GrantID:    grantID,
			ExpiresAt:  now.Add(AccessTokenTTL),
		},
		refreshSecret: refreshSecret,
		refreshHash:   refreshHash,
		refreshToken: Token{
			ID:         uuid.New(),
			OrgID:      orgID,
			MemberID:   memberID,
			ClientID:   clientID,
			Resource:   resource,
			Kind:       TokenKindRefresh,
			ProjectIDs: projectIDs,
			GrantID:    grantID,
			ExpiresAt:  now.Add(RefreshTokenTTL),
		},
	}, nil
}

// mintTokenPair generates and persists a fresh access+refresh pair sharing
// grantID, returning the raw secrets — the only time either is available in
// the clear.
func mintTokenPair(ctx context.Context, store *Store, now time.Time, orgID, memberID uuid.UUID, clientID, resource string, projectIDs []uuid.UUID, grantID uuid.UUID) (access, refresh string, err error) {
	pair, err := generateTokenPair(now, orgID, memberID, clientID, resource, projectIDs, grantID)
	if err != nil {
		return "", "", err
	}

	if err := store.CreateTokenPair(
		ctx,
		pair.accessToken,
		pair.accessHash,
		pair.refreshToken,
		pair.refreshHash,
	); err != nil {
		return "", "", fmt.Errorf("persisting token pair: %w", err)
	}

	return pair.accessSecret, pair.refreshSecret, nil
}

func mintRotatedTokenPair(
	ctx context.Context,
	store *Store,
	now time.Time,
	presentedRefresh Token,
) (access, refresh string, claimed bool, err error) {
	pair, err := generateTokenPair(
		now,
		presentedRefresh.OrgID,
		presentedRefresh.MemberID,
		presentedRefresh.ClientID,
		presentedRefresh.Resource,
		presentedRefresh.ProjectIDs,
		presentedRefresh.GrantID,
	)
	if err != nil {
		return "", "", false, err
	}

	claimed, err = store.RotateTokenPair(
		ctx,
		presentedRefresh.ID,
		pair.accessToken,
		pair.accessHash,
		pair.refreshToken,
		pair.refreshHash,
	)
	if err != nil {
		return "", "", false, fmt.Errorf("persisting rotated token pair: %w", err)
	}

	if !claimed {
		return "", "", false, nil
	}

	return pair.accessSecret, pair.refreshSecret, true, nil
}

// MintMachineToken mints a durable, non-interactive access token for a
// headless agent (phase 1): kind='access' (fits the existing CHECK, no
// constraint change) with NO paired refresh token — unlike mintTokenPair, a
// machine token is revoked outright, never rotated — a long TTL
// (MachineTokenTTL), a fresh per-token grant_id, and the reserved
// MachineClientID standing in for a real DCR client. Exported (unlike
// mintTokenPair) because the admin-gated command that calls it lives outside
// this package, in infra/api/machine_token_service.go — application code
// never imports this transport package directly, only through that adapter's
// narrow ports.
func MintMachineToken(ctx context.Context, store *Store, now time.Time, orgID, memberID uuid.UUID, resource string, projectIDs []uuid.UUID, label string) (secret string, tok Token, err error) {
	secret, hash, err := newOpaqueSecret(AccessTokenPrefix)
	if err != nil {
		return "", Token{}, fmt.Errorf("minting machine token: %w", err)
	}

	tok = Token{
		ID:         uuid.New(),
		OrgID:      orgID,
		MemberID:   memberID,
		ClientID:   MachineClientID,
		Resource:   resource,
		Kind:       TokenKindAccess,
		ProjectIDs: projectIDs,
		GrantID:    uuid.New(),
		ExpiresAt:  now.Add(MachineTokenTTL),
	}

	createdAt, err := store.CreateMachineToken(ctx, tok, hash, label)
	if err != nil {
		return "", Token{}, fmt.Errorf("persisting machine token: %w", err)
	}

	tok.CreatedAt = createdAt

	return secret, tok, nil
}

// writeTokenResponse writes the RFC 6749 §5.1 access token response body.
func writeTokenResponse(w http.ResponseWriter, access, refresh string) {
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":        access,
		"token_type":          "Bearer",
		"expires_in":          int(AccessTokenTTL.Seconds()),
		GrantTypeRefreshToken: refresh,
		"scope":               "mcp",
	})
}

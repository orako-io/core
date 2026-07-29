// SPDX-License-Identifier: AGPL-3.0-or-later

// Package invitelink talks to the Supabase GoTrue admin API. Its single job
// is generating signed action links (invite / magiclink) so invitation emails
// authenticate the recipient directly — the click proves email ownership, no
// second verification round.
package invitelink

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// GoTrue action-link types.
const (
	linkTypeInvite    = "invite"
	linkTypeMagiclink = "magiclink"
)

// Supabase is a minimal GoTrue admin client (no SDK dependency). Safe for
// concurrent use.
type Supabase struct {
	baseURL    string // https://<project>.supabase.co
	serviceKey string // service_role key — never logged
	client     *http.Client
}

// NewSupabase builds the client. baseURL is the Supabase project base URL
// (issuer minus /auth/v1).
func NewSupabase(baseURL, serviceKey string) *Supabase {
	return &Supabase{
		baseURL:    strings.TrimRight(baseURL, "/"),
		serviceKey: serviceKey,
		client:     &http.Client{Timeout: 10 * time.Second},
	}
}

// generateLinkResponse covers both GoTrue response shapes: flat fields and
// the supabase-js style nested "properties" object.
type generateLinkResponse struct {
	HashedToken string `json:"hashed_token"`
	Properties  struct {
		HashedToken string `json:"hashed_token"`
	} `json:"properties"`
}

// GenerateInviteLink asks GoTrue for a signed invite token for email. When
// the address is already registered it retries as a magiclink (same effect:
// the click authenticates). Returns the hashed token and the link type
// actually used ("invite" or "magiclink").
func (a *Supabase) GenerateInviteLink(ctx context.Context, email string) (string, string, error) {
	token, err := a.generateLink(ctx, linkTypeInvite, email)
	if err == nil {
		return token, linkTypeInvite, nil
	}

	if !isAlreadyRegistered(err) {
		return "", "", err
	}

	token, err = a.generateLink(ctx, linkTypeMagiclink, email)
	if err != nil {
		return "", "", err
	}

	return token, linkTypeMagiclink, nil
}

// alreadyRegisteredError marks the invite-specific "user exists" rejection so
// GenerateInviteLink can fall back to a magiclink.
type alreadyRegisteredError struct{ inner error }

func (e alreadyRegisteredError) Error() string { return e.inner.Error() }

func isAlreadyRegistered(err error) bool {
	var target alreadyRegisteredError

	return errors.As(err, &target)
}

// generateLink performs one POST /auth/v1/admin/generate_link call.
func (a *Supabase) generateLink(ctx context.Context, linkType, email string) (string, error) {
	body, err := json.Marshal(map[string]string{"type": linkType, "email": email})
	if err != nil {
		return "", fmt.Errorf("encoding generate_link request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		a.baseURL+"/auth/v1/admin/generate_link", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("building generate_link request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apikey", a.serviceKey)
	req.Header.Set("Authorization", "Bearer "+a.serviceKey)

	resp, err := a.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("calling supabase admin generate_link: %w", err)
	}

	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("reading generate_link response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		// The body may quote the email but never the key; keep it for context.
		err := fmt.Errorf("supabase admin generate_link (%s): status %d: %s",
			linkType, resp.StatusCode, truncate(string(raw), 200))

		if resp.StatusCode == http.StatusUnprocessableEntity ||
			(resp.StatusCode == http.StatusBadRequest && strings.Contains(strings.ToLower(string(raw)), "already")) {
			return "", alreadyRegisteredError{inner: err}
		}

		return "", err
	}

	var parsed generateLinkResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("decoding generate_link response: %w", err)
	}

	token := parsed.HashedToken
	if token == "" {
		token = parsed.Properties.HashedToken
	}

	if token == "" {
		return "", fmt.Errorf("supabase admin generate_link (%s): response carries no hashed_token", linkType)
	}

	return token, nil
}

// truncate keeps error messages bounded.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}

	return s[:n] + "…"
}

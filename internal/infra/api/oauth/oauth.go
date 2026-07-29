// SPDX-License-Identifier: AGPL-3.0-or-later

// Package oauth implements authorization for the MCP endpoint.
package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Token lifetimes.
const (
	AccessTokenTTL  = time.Hour
	RefreshTokenTTL = 30 * 24 * time.Hour
	AuthCodeTTL     = 2 * time.Minute
	MachineTokenTTL = 365 * 24 * time.Hour
)

// OAuth protocol values.
const (
	GrantTypeAuthorizationCode = "authorization_code"
	GrantTypeRefreshToken      = "refresh_token"
	ResponseTypeCode           = "code"
	PKCEMethodS256             = "S256"
	TokenAuthMethodNone        = "none"
)

// OAuth credential prefixes.
const (
	ClientIDPrefix     = "mcp_client_"
	AccessTokenPrefix  = "mcp_at_"
	RefreshTokenPrefix = "mcp_rt_"
)

// MachineClientID identifies machine-token grants.
const MachineClientID = "mcp_client_machine_tokens"

// TokenKind distinguishes an access row from a refresh row sharing the same
// oauth_tokens table.
type TokenKind string

// Recognized token kinds.
const (
	TokenKindAccess  TokenKind = "access"
	TokenKindRefresh TokenKind = "refresh"
)

// Client is a dynamically registered public client (RFC 7591): PKCE replaces
// a client secret, so there is no secret field.
type Client struct {
	ID            string
	Name          string
	RedirectURIs  []string
	GrantTypes    []string
	ResponseTypes []string
	AuthMethod    string
	CreatedAt     time.Time `exhaustruct:"optional"`
}

// HasRedirectURI reports whether uri is one of the client's registered
// redirect URIs, checked by exact string match — no scheme/port/path
// normalization, per the OAuth security BCP.
func (c Client) HasRedirectURI(uri string) bool {
	return slices.Contains(c.RedirectURIs, uri)
}

// AuthCode is a short-lived, single-use authorization code bound to the
// client, the exact redirect_uri, the PKCE challenge, the requested resource,
// and the member who approved the consent screen.
type AuthCode struct {
	ID                  uuid.UUID
	OrgID               uuid.UUID `exhaustruct:"optional"`
	ClientID            string
	RedirectURI         string
	CodeChallenge       string
	CodeChallengeMethod string
	Resource            string
	MemberID            uuid.UUID
	// ProjectIDs is the connection's project scope chosen at consent (ordered,
	// first = primary). Empty means all the caller's projects (unscoped).
	ProjectIDs []uuid.UUID `exhaustruct:"optional"`
	CreatedAt  time.Time   `exhaustruct:"optional"`
	ExpiresAt  time.Time
	UsedAt     *time.Time `exhaustruct:"optional"`
}

// Token is an opaque access or refresh token row. Only its SHA-256 ever
// reaches the database (see HashSecret); the raw secret is returned to the
// caller once, at mint time, and never stored.
type Token struct {
	ID       uuid.UUID
	OrgID    uuid.UUID `exhaustruct:"optional"`
	MemberID uuid.UUID
	ClientID string
	// Resource is the RFC 8707 audience this token is scoped to — the
	// resource server rejects any token whose Resource doesn't exactly match
	// its own canonical MCP resource URL (the confused-deputy guard).
	Resource string
	Kind     TokenKind
	// ProjectIDs is the token's project scope (ordered, first = primary).
	// Empty means all the caller's projects. Carried from the auth code and
	// preserved across refresh rotation.
	ProjectIDs []uuid.UUID `exhaustruct:"optional"`
	// GrantID groups an access+refresh pair and every pair produced by
	// rotating that refresh token, so revoking one connected agent is a
	// single RevokeGrant call.
	GrantID    uuid.UUID
	CreatedAt  time.Time `exhaustruct:"optional"`
	ExpiresAt  time.Time
	RevokedAt  *time.Time `exhaustruct:"optional"`
	LastUsedAt *time.Time `exhaustruct:"optional"`
}

// Revoked reports whether the token has been invalidated (explicit revoke, or
// the reuse-detection response to a rotated-away refresh token).
func (t Token) Revoked() bool { return t.RevokedAt != nil }

// Grant is one row per grant_id: the dashboard-facing view of a connected MCP
// client, aggregating the access+refresh token pair (and every pair produced
// by rotating it) sharing that grant_id into a single connection the caller
// can see and revoke.
type Grant struct {
	GrantID    uuid.UUID
	ClientName string
	Resource   string
	// ProjectIDs is the grant's project scope (ordered, first = primary).
	// Empty means all the caller's projects. Constant across every token row
	// sharing the grant.
	ProjectIDs []uuid.UUID `exhaustruct:"optional"`
	// ConnectedAt is when the grant was first created (MIN(created_at) across
	// every row sharing grant_id, including tokens later rotated away).
	ConnectedAt time.Time
	// LastUsedAt is the most recent authentication across the grant's tokens;
	// nil when none has ever been used.
	LastUsedAt *time.Time `exhaustruct:"optional"`
	// ExpiresAt is the grant's refresh-token expiry — the point at which the
	// connection stops working without a human re-approving it.
	ExpiresAt time.Time
}

// ExpiredAt reports whether the token's expiry has passed as of now.
func (t Token) ExpiredAt(now time.Time) bool { return now.After(t.ExpiresAt) }

// MachineToken is the dashboard-facing machine-token metadata.
type MachineToken struct {
	ID    uuid.UUID
	Label string
	// ProjectIDs is the token's project scope (ordered, first = primary).
	// Empty means all the caller's projects.
	ProjectIDs []uuid.UUID `exhaustruct:"optional"`
	CreatedAt  time.Time
	ExpiresAt  time.Time
	// LastUsedAt is unset until the token authenticates its first call.
	LastUsedAt *time.Time `exhaustruct:"optional"`
	// RevokedAt is unset while the token is live.
	RevokedAt *time.Time `exhaustruct:"optional"`
}

// HashSecret is the single hashing rule for every opaque secret this package
// issues (auth codes, access tokens, refresh tokens): SHA-256 over the full
// secret string.
func HashSecret(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))

	return sum[:]
}

// IsAccessTokenSecret reports whether a bearer value is an Orako-issued MCP
// access token, as opposed to a raw JWT or a dev-stub token — the routing
// check every Authenticator in the chain uses to decide whether a bearer
// belongs to it.
func IsAccessTokenSecret(bearer string) bool {
	return strings.HasPrefix(bearer, AccessTokenPrefix)
}

// VerifyPKCE recomputes S256(verifier) and compares it to the stored
// challenge in constant time.
func VerifyPKCE(challenge, verifier string) bool {
	if verifier == "" {
		return false
	}

	sum := sha256.Sum256([]byte(verifier))
	computed := base64.RawURLEncoding.EncodeToString(sum[:])

	return subtle.ConstantTimeCompare([]byte(computed), []byte(challenge)) == 1
}

// newOpaqueSecret mints a fresh bearer secret: prefix + 32 random bytes in
// url-safe base64. It returns the raw secret (returned to the caller once) and
// its SHA-256 (the only thing persisted).
func newOpaqueSecret(prefix string) (secret string, hash []byte, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("generating token entropy: %w", err)
	}

	secret = prefix + base64.RawURLEncoding.EncodeToString(raw)

	return secret, HashSecret(secret), nil
}

// newClientID mints a fresh public client identifier. Client IDs are not
// secret — DCR returns them in the clear — so no hashing applies.
func newClientID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generating client id entropy: %w", err)
	}

	return ClientIDPrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

// defaultOr returns v when non-empty, otherwise d — RFC 7591's default for
// grant_types/response_types when a DCR request omits them.
func defaultOr(v, d []string) []string {
	if len(v) == 0 {
		return d
	}

	return v
}

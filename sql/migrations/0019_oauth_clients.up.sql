-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Thin MCP Authorization Server (OAuth 2.1 + PKCE + DCR): dynamically
-- registered public clients, short-lived single-use authorization codes, and
-- opaque hashed access/refresh tokens. Mirrors agent_tokens' "only the hash
-- ever lands here" design; the extra columns (client_id, resource/aud,
-- grant_id) don't belong on agent_tokens itself, hence a sibling table
-- instead of reuse (see spike-findings.md 2026_07_06-remote-mcp).

-- Dynamically registered clients (RFC 7591). Public clients only — PKCE
-- replaces a client secret, so there is no secret column.
CREATE TABLE oauth_clients (
    client_id                  text PRIMARY KEY,
    client_name                text NOT NULL DEFAULT '',
    redirect_uris               text[] NOT NULL,
    grant_types                text[] NOT NULL,
    response_types              text[] NOT NULL,
    token_endpoint_auth_method  text NOT NULL DEFAULT 'none',
    created_at                  timestamptz NOT NULL DEFAULT now()
);

-- Short-lived, single-use authorization codes issued at /authorize and
-- redeemed at /token. code_hash mirrors the token-hashing convention; a code
-- is low-value (2-minute TTL, single use) but hashing costs nothing.
CREATE TABLE oauth_authorization_codes (
    id                     uuid PRIMARY KEY,
    code_hash               bytea NOT NULL UNIQUE,
    client_id               text NOT NULL REFERENCES oauth_clients(client_id) ON DELETE CASCADE,
    redirect_uri            text NOT NULL,
    code_challenge          text NOT NULL,
    code_challenge_method   text NOT NULL,
    resource                text NOT NULL,
    member_id               uuid NOT NULL REFERENCES members(id) ON DELETE CASCADE,
    created_at              timestamptz NOT NULL DEFAULT now(),
    expires_at              timestamptz NOT NULL,
    used_at                 timestamptz
);

-- Opaque, hashed access + refresh tokens (never a JWT — see spike-findings.md
-- §1: the resource server hits Postgres for CallerIdentity regardless, so a
-- JWT's stateless benefit is moot, while opaque tokens get free, instant
-- revocation). grant_id groups an access+refresh pair issued together and
-- every pair produced by rotating that refresh token, so revoking "this
-- connected agent" is one UPDATE by grant_id. resource is the RFC 8707
-- audience the resource server (mcp_token_auth.go) must match exactly.
CREATE TABLE oauth_tokens (
    id             uuid PRIMARY KEY,
    member_id      uuid NOT NULL REFERENCES members(id) ON DELETE CASCADE,
    client_id      text NOT NULL REFERENCES oauth_clients(client_id) ON DELETE CASCADE,
    resource       text NOT NULL,
    kind           text NOT NULL CHECK (kind IN ('access', 'refresh')),
    token_hash     bytea NOT NULL UNIQUE,
    grant_id       uuid NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    expires_at     timestamptz NOT NULL,
    revoked_at     timestamptz,
    last_used_at   timestamptz
);

CREATE INDEX idx_oauth_authorization_codes_expires ON oauth_authorization_codes (expires_at);
CREATE INDEX idx_oauth_tokens_member ON oauth_tokens (member_id, created_at DESC);
CREATE INDEX idx_oauth_tokens_grant ON oauth_tokens (grant_id);

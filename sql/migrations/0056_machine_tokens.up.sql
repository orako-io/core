-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- Machine tokens (phase 1): a non-interactive, org-admin-minted long-lived
-- oauth_tokens row for a headless agent. It reuses the existing oauth_tokens
-- shape as kind='access' (fits the CHECK IN ('access','refresh') as-is) with
-- NO paired refresh row — a machine token is revoked outright, never
-- rotated. label distinguishes it in the dashboard list (an OAuth-flow token
-- has none, hence nullable/additive rather than NOT NULL).
--
-- oauth_tokens.client_id is NOT NULL REFERENCES oauth_clients, but a machine
-- token was never issued through RFC 7591 dynamic client registration — there
-- is no real DCR client behind it. The reserved pseudo-client row below
-- satisfies that FK and doubles as the stable client_id filter
-- ListMachineTokens/RevokeMachineToken use to distinguish machine tokens from
-- real OAuth-flow connections sharing the same table. It registers no
-- redirect_uris/grant_types/response_types (empty arrays) since it is never
-- driven through /authorize or /token.

ALTER TABLE oauth_tokens ADD COLUMN label text;

INSERT INTO oauth_clients (client_id, client_name, redirect_uris, grant_types, response_types, token_endpoint_auth_method)
VALUES ('mcp_client_machine_tokens', 'Machine tokens (reserved)', '{}', '{}', '{}', 'none');

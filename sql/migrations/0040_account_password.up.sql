-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Local-auth credentials. An account authenticates either via an external IdP
-- (subject IS NOT NULL) or via email+password (password_hash IS NOT NULL). The
-- hash is a bcrypt digest; NULL for IdP-only accounts. Self-host uses this;
-- the SaaS (Supabase OIDC) leaves it NULL.
ALTER TABLE accounts ADD COLUMN password_hash TEXT;

-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- 0024_project_providers_nullable_creds: with provider credentials now living
-- at the org level (org_providers, 0023), a project_providers row may exist
-- solely to hold a per-project alert-channel override for a kind — with no
-- credentials of its own. Relax the NOT NULL so a channel-only row is legal.
-- Credentials that remain here are legacy until the org-level activation lands.

ALTER TABLE project_providers ALTER COLUMN credentials DROP NOT NULL;

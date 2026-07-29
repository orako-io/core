-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Single-row table holding the self-host license key an admin pastes in the
-- dashboard (Settings → License). It replaces ORAKO_LICENSE_KEY: the key now
-- lives here and applies at runtime — the edition refresh loop re-resolves it
-- without a server restart. The singleton BOOLEAN PRIMARY KEY + CHECK guarantee
-- at most one row (an upsert always targets it). set_by is the org-admin member
-- who set it (nullable: an automatic refresh-loop renewal has no human setter).
-- The key is still verified OFFLINE against the baked-in Ed25519 public key
-- exactly as before — this table changes WHERE the key lives, never how it is
-- trusted.
CREATE TABLE instance_license (
    singleton   BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    license_key TEXT NOT NULL,
    set_by      UUID,
    set_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

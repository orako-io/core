-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Restore the retired claim-era org knobs (values are lost; NULL = product
-- default, matching 0012/0016's original semantics).
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS silence_timeout_seconds BIGINT;
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS capture_second_opinions BOOLEAN;

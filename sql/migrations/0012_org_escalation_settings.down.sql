-- SPDX-License-Identifier: AGPL-3.0-or-later
ALTER TABLE conversations
    DROP COLUMN nudged_at;

ALTER TABLE organizations
    DROP COLUMN claim_timeout_seconds,
    DROP COLUMN silence_timeout_seconds;

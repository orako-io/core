-- SPDX-License-Identifier: AGPL-3.0-or-later
-- 0043: single-use password resets (L1). The reset token embeds this version;
-- PerformReset requires a match and bumps it on success, and any password change
-- bumps it too — so a reset link is spent once and invalidated by a later change.
ALTER TABLE accounts ADD COLUMN password_reset_version INTEGER NOT NULL DEFAULT 0;

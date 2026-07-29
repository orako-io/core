-- SPDX-License-Identifier: AGPL-3.0-or-later

-- Per-member, permanent dismissal of the Get-started onboarding. NULL = not yet
-- dismissed (the page + nav item still show); a timestamp = dismissed for good,
-- surviving reloads and other devices (this is why it is server-side, not
-- localStorage). Undo clears it back to NULL.
ALTER TABLE members ADD COLUMN onboarding_dismissed_at timestamptz;

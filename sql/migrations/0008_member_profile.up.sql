-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Member profile fields for human/AI routing context: given/family name and the
-- member's git handle. Empty string means "not provided".
ALTER TABLE members
    ADD COLUMN first_name text NOT NULL DEFAULT '',
    ADD COLUMN last_name  text NOT NULL DEFAULT '',
    ADD COLUMN git_handle text NOT NULL DEFAULT '';

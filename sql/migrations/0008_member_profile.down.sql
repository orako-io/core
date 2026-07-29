-- SPDX-License-Identifier: AGPL-3.0-or-later
ALTER TABLE members
    DROP COLUMN first_name,
    DROP COLUMN last_name,
    DROP COLUMN git_handle;

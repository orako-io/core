-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Member availability: a nullable return_date reminder for the on_leave status.
-- The status column is TEXT with no CHECK constraint, so the new values
-- 'on_leave' and 'deactivated' need no schema change — only the reminder column.
ALTER TABLE members
    ADD COLUMN return_date date;

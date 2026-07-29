-- SPDX-License-Identifier: AGPL-3.0-or-later
ALTER TABLE conversations DROP COLUMN IF EXISTS entities;
ALTER TABLE conversations DROP COLUMN IF EXISTS tags;
ALTER TABLE conversations DROP COLUMN IF EXISTS summary;

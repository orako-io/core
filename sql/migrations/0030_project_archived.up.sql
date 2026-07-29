-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Project archive: a reversible freeze. An archived project keeps all its data
-- (conversations, KB, members) but drops out of the active reads, agent routing
-- and the seat count. archived_at NULL = active.
ALTER TABLE projects
    ADD COLUMN archived_at timestamptz;

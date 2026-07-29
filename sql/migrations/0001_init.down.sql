-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- Reverse of 0001_init. Drop in dependency order; child tables first.

DROP TABLE IF EXISTS presence;
DROP TABLE IF EXISTS kb_feedback;
DROP TABLE IF EXISTS kb_entries;
DROP TABLE IF EXISTS messages;
DROP TABLE IF EXISTS conversations;
DROP TABLE IF EXISTS event_log;
DROP TABLE IF EXISTS project_members;
DROP TABLE IF EXISTS projects;
DROP TABLE IF EXISTS members;

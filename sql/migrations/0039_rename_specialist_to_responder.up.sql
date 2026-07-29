-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Rename conversations.specialist_member_id → responder_member_id. The claim
-- model is gone; this column only records who first answered (KB attribution),
-- so the honest name is "responder". Pure rename, no data change.
ALTER TABLE conversations RENAME COLUMN specialist_member_id TO responder_member_id;

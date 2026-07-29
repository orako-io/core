-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- 0016_second_opinion: capture path for a losing pool candidate's reply on an
-- already-claimed conversation (see internal/application/command/follow_up.go
-- gateReply). messages.role has no DB CHECK constraint today (plain TEXT,
-- validated in the domain model only), so the new "second_opinion" role
-- needs no schema change there — model.MessageRoleSecondOpinion.String()
-- is the sole gate.
--
-- capture_second_opinions is the org-level opt-out / degradation switch:
-- NULL = product default (on), matching the existing escalation-settings
-- NULL-means-default convention (0012_org_escalation_settings). Read through
-- EscalationStore.CaptureSecondOpinionsEnabled, joined from a project via its
-- org_id — a project without an org also reads as enabled.

ALTER TABLE organizations
    ADD COLUMN capture_second_opinions BOOLEAN;

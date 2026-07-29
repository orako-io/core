-- SPDX-License-Identifier: AGPL-3.0-or-later

DROP INDEX idx_oauth_tokens_org;
DROP INDEX idx_oauth_authorization_codes_org;

ALTER TABLE oauth_tokens DROP CONSTRAINT oauth_machine_token_org_required;
ALTER TABLE oauth_tokens DROP COLUMN org_id;
ALTER TABLE oauth_authorization_codes DROP COLUMN org_id;

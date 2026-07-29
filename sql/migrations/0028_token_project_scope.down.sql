-- SPDX-License-Identifier: AGPL-3.0-or-later

ALTER TABLE agent_tokens DROP COLUMN project_ids;
ALTER TABLE oauth_tokens DROP COLUMN project_ids;
ALTER TABLE oauth_authorization_codes DROP COLUMN project_ids;

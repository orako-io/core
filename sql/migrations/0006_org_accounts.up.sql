-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- 0006_org_accounts: the organization + account model above projects.
--
-- accounts       — an authenticated human (the login identity). One per person.
--                  Distinct from members: a member is a project participant that
--                  may have NO account (an external-only specialist living in
--                  Slack/Telegram), and an account participates in projects as a
--                  member. members.account_id links the two when a human logs in.
-- organizations  — the tenant boundary above projects.
-- org_members    — account × org × role: org-scoped RBAC (admin | member).
-- projects.org_id — attaches a project to an org. Nullable for now; the create-org
--                   flow enforces it and existing rows are backfilled in a later slice.

CREATE TABLE accounts (
    id           UUID PRIMARY KEY,
    subject      TEXT,           -- external IdP subject (JWT sub); NULL for local-auth accounts
    email        TEXT NOT NULL,
    display_name TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Email is the login identity; the external subject, when present, is unique.
CREATE UNIQUE INDEX idx_accounts_email ON accounts (email);
CREATE UNIQUE INDEX idx_accounts_subject ON accounts (subject) WHERE subject IS NOT NULL;

CREATE TABLE organizations (
    id         UUID PRIMARY KEY,
    name       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE org_members (
    org_id     UUID NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    account_id UUID NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    role       TEXT NOT NULL,  -- admin | member
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (org_id, account_id)
);

CREATE INDEX idx_org_members_account ON org_members (account_id);

ALTER TABLE projects
    ADD COLUMN org_id UUID REFERENCES organizations (id) ON DELETE CASCADE;

CREATE INDEX idx_projects_org ON projects (org_id);

-- members.account_id links a project participant to the human account that logs
-- in as them (NULL for external-only specialists).
ALTER TABLE members
    ADD COLUMN account_id UUID REFERENCES accounts (id) ON DELETE SET NULL;

CREATE INDEX idx_members_account ON members (account_id);

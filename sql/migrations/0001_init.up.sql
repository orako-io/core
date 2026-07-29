-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- 0001_init (post-pivot 2026-07-01): identity/projects, the append-only event
-- log, conversations/messages, the knowledge base, and connection presence. The
-- event log is the source of truth; supersession is append-only.
--
-- Runs on stock Postgres — no non-stock extension. The pgvector requirement was
-- removed here (P5b): the kb_entries.embedding column is gone and kb_entries is
-- fully dropped in 0046. History search now uses FTS + pg_trgm (both contrib).

-- members: Orako-canonical identity. Connector ids (slack_user_id) are bindings.
-- email is the match key against a connector directory; nullable after PII purge.
CREATE TABLE members (
    id            UUID PRIMARY KEY,
    email         TEXT,
    display_name  TEXT,
    slack_user_id TEXT,
    status        TEXT NOT NULL DEFAULT 'active',  -- invited|active|suspended|removed|purged
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- projects: the room-equivalent; groups members with roles.
CREATE TABLE projects (
    id         UUID PRIMARY KEY,
    name       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- project_members: membership + role + domains. A member may hold several roles
-- in a project (a dev who also answers as a specialist).
CREATE TABLE project_members (
    project_id UUID NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    member_id  UUID NOT NULL REFERENCES members (id) ON DELETE CASCADE,
    role       TEXT NOT NULL,  -- dev|specialist|lead|admin
    domains    TEXT[] NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (project_id, member_id, role)
);

-- event_log is append-only: rows are inserted, never updated or deleted. seq is
-- a per-project monotonic position; (project_id, seq) is unique so a replay is
-- totally ordered and concurrent appends conflict rather than duplicate.
CREATE TABLE event_log (
    id          UUID PRIMARY KEY,
    project_id  UUID NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    seq         BIGINT NOT NULL,
    type        TEXT NOT NULL,
    payload     JSONB NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (project_id, seq)
);

CREATE INDEX idx_event_log_project_seq ON event_log (project_id, seq);

-- conversations: durable threads the agent manages. Maps to a Slack thread.
CREATE TABLE conversations (
    id                   UUID PRIMARY KEY,
    project_id           UUID NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    asker_member_id      UUID NOT NULL REFERENCES members (id),
    specialist_member_id UUID REFERENCES members (id),
    status               TEXT NOT NULL DEFAULT 'open',  -- open|answered|closed|timed_out
    question             TEXT NOT NULL,
    context              TEXT NOT NULL DEFAULT '',
    refs                 JSONB NOT NULL DEFAULT '{}',
    slack_channel_id     TEXT,
    slack_thread_ts      TEXT,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_conversations_project ON conversations (project_id);
CREATE INDEX idx_conversations_slack_thread ON conversations (slack_channel_id, slack_thread_ts);

-- messages: append-only per conversation. Specialist answers land here from the
-- Slack webhook (role = 'answer').
CREATE TABLE messages (
    id               UUID PRIMARY KEY,
    conversation_id  UUID NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    author_member_id UUID REFERENCES members (id),  -- null for system
    role             TEXT NOT NULL,                  -- question|answer|follow_up|system
    body             TEXT NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_messages_conversation ON messages (conversation_id, created_at);

-- kb_entries: the (legacy) knowledge base — HUMAN-authored answers. Retained
-- here only so the historical migration chain stays intact; the table (with its
-- FTS column added in 0026/0027) is dropped wholesale in 0046 (P5b). The former
-- pgvector embedding column is gone so the chain runs on stock Postgres.
CREATE TABLE kb_entries (
    id               UUID PRIMARY KEY,
    project_id       UUID NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    conversation_id  UUID REFERENCES conversations (id) ON DELETE SET NULL,
    question         TEXT NOT NULL,
    answer           TEXT NOT NULL,
    author_member_id UUID REFERENCES members (id),
    tags             TEXT[] NOT NULL DEFAULT '{}',
    refs             JSONB NOT NULL DEFAULT '{}',
    confidence       REAL NOT NULL DEFAULT 1.0,
    superseded_by    UUID REFERENCES kb_entries (id),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_kb_entries_project ON kb_entries (project_id);

-- kb_feedback: consumer usefulness/accuracy signals feeding confidence.
CREATE TABLE kb_feedback (
    id          UUID PRIMARY KEY,
    kb_entry_id UUID NOT NULL REFERENCES kb_entries (id) ON DELETE CASCADE,
    member_id   UUID NOT NULL REFERENCES members (id),
    verdict     TEXT NOT NULL,  -- useful|inaccurate
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- presence: connection heartbeat (ephemeral availability), not file activity.
CREATE TABLE presence (
    member_id  UUID PRIMARY KEY REFERENCES members (id) ON DELETE CASCADE,
    online     BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

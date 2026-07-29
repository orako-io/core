-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- 0055_knowledge_entries: curated knowledge as a first-class, searchable entry
-- type distinct from organic conversation-history (decision 2026-07-26). Its
-- searchable/returnable shape mirrors a resolved conversation 1:1 (question,
-- answer, summary, tags, entities) so search_knowledge returns curated and
-- organic hits under ONE shape the agent cannot tell apart. Same FTS ('simple',
-- no stemming) + pg_trgm fuzzy arms as conversations (migration 0045); NO
-- embeddings (upholds 2026-07-24). A dedicated table keeps responder/
-- response-time analytics honest — curated never enters the conversation KPIs.

CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- knowledge_search_labels: the fuzzy pg_trgm arm's flat text join of tags +
-- entities. array_to_string is only STABLE and is rejected inside a generated
-- column, so it is wrapped in an IMMUTABLE SQL function (deterministic for
-- text[] input) — the same shape as conversation_search_labels (0045), kept
-- separate so this migration owns its whole schema and drops cleanly.
CREATE FUNCTION knowledge_search_labels(tags text[], entities text[])
    RETURNS text
    LANGUAGE sql
    IMMUTABLE
    PARALLEL SAFE
    AS $$
        SELECT array_to_string(tags, ' ') || ' ' || array_to_string(entities, ' ')
    $$;

CREATE TABLE knowledge_entries (
    id               uuid PRIMARY KEY,
    org_id           uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    -- project_id NULL = org-wide entry (visible in every project of the org).
    project_id       uuid REFERENCES projects (id) ON DELETE CASCADE,
    question         text NOT NULL,                          -- ~ conversation.question/title
    answer           text NOT NULL,                          -- the curated answer body
    summary          text NOT NULL DEFAULT '',               -- SAME as conversations.summary
    tags             text[] NOT NULL DEFAULT '{}',           -- SAME as conversations.tags
    entities         text[] NOT NULL DEFAULT '{}',           -- SAME as conversations.entities
    -- author_member_id: who curated it (label/attribution only). SET NULL so a
    -- removed member does not delete the knowledge they authored.
    author_member_id uuid REFERENCES members (id) ON DELETE SET NULL,
    source           text NOT NULL DEFAULT 'curated'
                     CHECK (source IN ('curated', 'agent_suggested')),
    status           text NOT NULL DEFAULT 'active'
                     CHECK (status IN ('active', 'stale', 'pending')),
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    -- fts: a STORED tsvector built the SAME way as conversations.fts — 'simple'
    -- config over summary+question+answer, with tags/entities folded in as
    -- single lexemes. array_to_tsvector and to_tsvector(regconfig,text) are
    -- IMMUTABLE, so they are allowed in a generated column.
    fts tsvector GENERATED ALWAYS AS (
        to_tsvector('simple',
            coalesce(summary, '') || ' ' ||
            coalesce(question, '') || ' ' ||
            coalesce(answer, ''))
        || array_to_tsvector(tags)
        || array_to_tsvector(entities)
    ) STORED,
    -- search_labels: the flat tag/entity text for pg_trgm fuzzy matching, same
    -- as conversations.search_labels.
    search_labels text GENERATED ALWAYS AS (
        knowledge_search_labels(tags, entities)
    ) STORED
);

CREATE INDEX idx_knowledge_entries_fts
    ON knowledge_entries USING gin (fts);

CREATE INDEX idx_knowledge_entries_labels_trgm
    ON knowledge_entries USING gin (search_labels gin_trgm_ops);

CREATE INDEX idx_knowledge_entries_org
    ON knowledge_entries (org_id);

-- FK-covering index for project_id (also serves project-scoped list/search).
CREATE INDEX idx_knowledge_entries_project
    ON knowledge_entries (project_id);

-- FK-covering index for author_member_id (keeps the members ON DELETE SET NULL
-- cascade from a sequential scan).
CREATE INDEX idx_knowledge_entries_author
    ON knowledge_entries (author_member_id);

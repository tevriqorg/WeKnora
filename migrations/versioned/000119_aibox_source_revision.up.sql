ALTER TABLE resources
    ADD COLUMN IF NOT EXISTS ownership VARCHAR(16) NOT NULL DEFAULT 'managed';

ALTER TABLE resources
    DROP CONSTRAINT IF EXISTS resources_ownership_check;
ALTER TABLE resources
    ADD CONSTRAINT resources_ownership_check CHECK (ownership IN ('managed', 'external'));

CREATE TABLE IF NOT EXISTS source_revisions (
    revision_id VARCHAR(36) PRIMARY KEY,
    knowledge_id VARCHAR(36) NOT NULL REFERENCES knowledges(id) ON DELETE CASCADE,
    revision_seq BIGINT NOT NULL,
    source_hash VARCHAR(128) NOT NULL DEFAULT '',
    native_version TEXT NOT NULL DEFAULT '',
    etag TEXT NOT NULL DEFAULT '',
    source_updated_at TIMESTAMPTZ NULL,
    observed_at TIMESTAMPTZ NOT NULL,
    resource_ref TEXT NOT NULL DEFAULT '',
    locator TEXT NOT NULL DEFAULT '',
    size BIGINT NOT NULL DEFAULT 0,
    mime VARCHAR(255) NOT NULL DEFAULT '',
    is_current BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT source_revisions_knowledge_seq_unique UNIQUE (knowledge_id, revision_seq)
);

CREATE INDEX IF NOT EXISTS idx_source_revisions_knowledge
    ON source_revisions (knowledge_id, revision_seq DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_source_revisions_one_current
    ON source_revisions (knowledge_id) WHERE is_current = TRUE;

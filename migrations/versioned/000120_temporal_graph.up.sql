CREATE TABLE temporal_graph_generations (
    generation_id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    group_id VARCHAR(255) NOT NULL UNIQUE,
    graphiti_commit VARCHAR(64) NOT NULL,
    extraction_schema_version VARCHAR(128) NOT NULL,
    model_config_fingerprint VARCHAR(128) NOT NULL,
    status VARCHAR(16) NOT NULL CHECK (status IN ('building','active','retired','failed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    activated_at TIMESTAMPTZ NULL
);
CREATE INDEX idx_temporal_generation_kb ON temporal_graph_generations(tenant_id, knowledge_base_id);
CREATE UNIQUE INDEX uq_temporal_generation_active_kb
    ON temporal_graph_generations(tenant_id, knowledge_base_id)
    WHERE status = 'active';

CREATE TABLE temporal_episode_bindings (
    episode_uuid VARCHAR(36) PRIMARY KEY,
    generation_id VARCHAR(36) NOT NULL,
    group_id VARCHAR(255) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL,
    source_revision_id VARCHAR(36) NOT NULL,
    window_ordinal INTEGER NULL,
    derived_object_id VARCHAR(255) NULL,
    reference_time TIMESTAMPTZ NOT NULL,
    source_kind VARCHAR(16) NOT NULL CHECK (source_kind IN ('text','json','message')),
    authority_key VARCHAR(512) NOT NULL,
    evidence_snapshot TEXT NOT NULL,
    evidence_locator_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    snapshot_hash VARCHAR(128) NOT NULL,
    extraction_schema_version VARCHAR(128) NOT NULL,
    model_config_fingerprint VARCHAR(128) NOT NULL,
    temporal_status VARCHAR(16) NOT NULL CHECK (temporal_status IN ('pending','running','ready','failed','stale','purged')),
    temporal_attempt INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ NULL,
    last_error TEXT NOT NULL DEFAULT '',
    CHECK ((window_ordinal IS NOT NULL) <> (derived_object_id IS NOT NULL))
);
CREATE UNIQUE INDEX uq_temporal_binding_generation_episode
    ON temporal_episode_bindings(generation_id, episode_uuid);
CREATE UNIQUE INDEX uq_temporal_binding_window
    ON temporal_episode_bindings(generation_id, source_revision_id, window_ordinal)
    WHERE window_ordinal IS NOT NULL;
CREATE UNIQUE INDEX uq_temporal_binding_derived
    ON temporal_episode_bindings(generation_id, source_revision_id, derived_object_id)
    WHERE derived_object_id IS NOT NULL;
CREATE INDEX idx_temporal_binding_knowledge ON temporal_episode_bindings(knowledge_id);
CREATE INDEX idx_temporal_binding_status ON temporal_episode_bindings(temporal_status);

CREATE TABLE temporal_transition_bindings (
    generation_id VARCHAR(36) NOT NULL,
    old_fact_uuid VARCHAR(36) NOT NULL,
    new_fact_uuid VARCHAR(36) NOT NULL,
    causing_episode_uuid VARCHAR(36) NOT NULL,
    transition_type VARCHAR(16) NOT NULL CHECK (transition_type IN ('superseded','invalidated','corrected')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (generation_id, old_fact_uuid, new_fact_uuid, causing_episode_uuid)
);
CREATE INDEX idx_temporal_transition_episode ON temporal_transition_bindings(causing_episode_uuid);

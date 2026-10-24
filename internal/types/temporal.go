package types

import "time"

type TemporalGenerationStatus string

const (
	TemporalGenerationBuilding TemporalGenerationStatus = "building"
	TemporalGenerationActive   TemporalGenerationStatus = "active"
	TemporalGenerationRetired  TemporalGenerationStatus = "retired"
	TemporalGenerationFailed   TemporalGenerationStatus = "failed"
)

type TemporalStatus string

const (
	TemporalStatusPending TemporalStatus = "pending"
	TemporalStatusRunning TemporalStatus = "running"
	TemporalStatusReady   TemporalStatus = "ready"
	TemporalStatusFailed  TemporalStatus = "failed"
	TemporalStatusStale   TemporalStatus = "stale"
	TemporalStatusPurged  TemporalStatus = "purged"
)

type TemporalGraphGeneration struct {
	GenerationID            string                   `json:"generation_id" gorm:"type:varchar(36);primaryKey"`
	TenantID                uint64                   `json:"tenant_id" gorm:"not null;index:idx_temporal_generation_kb,priority:1"`
	KnowledgeBaseID         string                   `json:"knowledge_base_id" gorm:"type:varchar(36);not null;index:idx_temporal_generation_kb,priority:2"`
	GroupID                 string                   `json:"group_id" gorm:"type:varchar(255);not null;uniqueIndex"`
	GraphitiCommit          string                   `json:"graphiti_commit" gorm:"type:varchar(64);not null"`
	ExtractionSchemaVersion string                   `json:"extraction_schema_version" gorm:"type:varchar(128);not null"`
	ModelConfigFingerprint  string                   `json:"model_config_fingerprint" gorm:"type:varchar(128);not null"`
	Status                  TemporalGenerationStatus `json:"status" gorm:"type:varchar(16);not null;index"`
	CreatedAt               time.Time                `json:"created_at"`
	ActivatedAt             *time.Time               `json:"activated_at"`
}

func (TemporalGraphGeneration) TableName() string { return "temporal_graph_generations" }

type TemporalEpisodeBinding struct {
	EpisodeUUID             string         `json:"episode_uuid" gorm:"type:varchar(36);primaryKey"`
	GenerationID            string         `json:"generation_id" gorm:"type:varchar(36);not null;index"`
	GroupID                 string         `json:"group_id" gorm:"type:varchar(255);not null;index"`
	KnowledgeID             string         `json:"knowledge_id" gorm:"type:varchar(36);not null;index"`
	SourceRevisionID        string         `json:"source_revision_id" gorm:"type:varchar(36);not null;index"`
	WindowOrdinal           *int           `json:"window_ordinal"`
	DerivedObjectID         *string        `json:"derived_object_id" gorm:"type:varchar(255)"`
	ReferenceTime           time.Time      `json:"reference_time"`
	SourceKind              string         `json:"source_kind" gorm:"type:varchar(16);not null"`
	AuthorityKey            string         `json:"authority_key" gorm:"type:varchar(512);not null"`
	EvidenceSnapshot        string         `json:"evidence_snapshot" gorm:"type:text;not null"`
	EvidenceLocatorJSON     JSON           `json:"evidence_locator_json" gorm:"type:jsonb"`
	SnapshotHash            string         `json:"snapshot_hash" gorm:"type:varchar(128);not null"`
	ExtractionSchemaVersion string         `json:"extraction_schema_version" gorm:"type:varchar(128);not null"`
	ModelConfigFingerprint  string         `json:"model_config_fingerprint" gorm:"type:varchar(128);not null"`
	TemporalStatus          TemporalStatus `json:"temporal_status" gorm:"type:varchar(16);not null;index"`
	TemporalAttempt         int            `json:"temporal_attempt" gorm:"not null;default:0"`
	CreatedAt               time.Time      `json:"created_at"`
	CompletedAt             *time.Time     `json:"completed_at"`
	LastError               string         `json:"last_error" gorm:"type:text"`
}

func (TemporalEpisodeBinding) TableName() string { return "temporal_episode_bindings" }

type TemporalTransitionBinding struct {
	GenerationID       string    `json:"generation_id" gorm:"type:varchar(36);primaryKey"`
	OldFactUUID        string    `json:"old_fact_uuid" gorm:"type:varchar(36);primaryKey"`
	NewFactUUID        string    `json:"new_fact_uuid" gorm:"type:varchar(36);primaryKey"`
	CausingEpisodeUUID string    `json:"causing_episode_uuid" gorm:"type:varchar(36);primaryKey"`
	TransitionType     string    `json:"transition_type" gorm:"type:varchar(16);not null"`
	CreatedAt          time.Time `json:"created_at"`
}

func (TemporalTransitionBinding) TableName() string { return "temporal_transition_bindings" }

// One temporal slot is owned by each distinct finalizing batch.
type TemporalFinalizeReceipt struct {
	BatchID string `json:"batch_id" gorm:"type:varchar(36);primaryKey"`
	SlotKind string `json:"slot_kind" gorm:"type:varchar(32);primaryKey"`
	TenantID uint64 `json:"tenant_id" gorm:"not null"`
	KnowledgeID string `json:"knowledge_id" gorm:"type:varchar(36);not null"`
	CreatedAt time.Time `json:"created_at"`
}

func (TemporalFinalizeReceipt) TableName() string { return "temporal_finalize_receipts" }

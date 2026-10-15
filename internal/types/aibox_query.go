package types

import "time"

type AIBOXQueryMode string

const (
	AIBOXQueryModeAuto    AIBOXQueryMode = "auto"
	AIBOXQueryModeCurrent AIBOXQueryMode = "current"
	AIBOXQueryModeValidAt AIBOXQueryMode = "valid_at"
	AIBOXQueryModeKnownAt AIBOXQueryMode = "known_at"
	AIBOXQueryModeHistory AIBOXQueryMode = "history"
)

type AIBOXQueryInput struct {
	Query     string         `json:"query"`
	KBIDs     []string       `json:"knowledge_base_ids"`
	Mode      AIBOXQueryMode `json:"mode"`
	ValidTime string         `json:"valid_time,omitempty"`
	KnownTime string         `json:"known_time,omitempty"`
	Limit     int            `json:"limit,omitempty"`
}

type AIBOXDocumentEvidence struct {
	ChunkID        string  `json:"chunk_id"`
	KnowledgeID    string  `json:"knowledge_id"`
	KnowledgeTitle string  `json:"knowledge_title,omitempty"`
	Content        string  `json:"content"`
	Score          float64 `json:"score,omitempty"`
}

type AIBOXTemporalEntity struct {
	UUID       string         `json:"uuid"`
	Name       string         `json:"name,omitempty"`
	Labels     []string       `json:"labels,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

type AIBOXTemporalEvidence struct {
	FactUUID      string              `json:"fact_uuid"`
	Fact          string              `json:"fact"`
	Relation      string              `json:"relation,omitempty"`
	GroupID       string              `json:"group_id"`
	Episodes      []string            `json:"episodes,omitempty"`
	AuthorityKeys []string            `json:"authority_keys,omitempty"`
	CreatedAt     time.Time           `json:"created_at"`
	ValidAt       *time.Time          `json:"valid_at,omitempty"`
	InvalidAt     *time.Time          `json:"invalid_at,omitempty"`
	ExpiredAt     *time.Time          `json:"expired_at,omitempty"`
	ReferenceTime *time.Time          `json:"reference_time,omitempty"`
	Attributes    map[string]any      `json:"attributes,omitempty"`
	Source        AIBOXTemporalEntity `json:"source"`
	Target        AIBOXTemporalEntity `json:"target"`
}

type AIBOXConflict struct {
	Key    string                  `json:"key"`
	Claims []AIBOXTemporalEvidence `json:"claims"`
}

type AIBOXQueryResult struct {
	EffectiveMode    AIBOXQueryMode          `json:"effective_mode"`
	TemporalRequired bool                    `json:"temporal_required"`
	TemporalDegraded bool                    `json:"temporal_degraded"`
	DocumentDegraded bool                    `json:"document_degraded"`
	DegradedReasons  []string                `json:"degraded_reasons,omitempty"`
	Documents        []AIBOXDocumentEvidence `json:"documents,omitempty"`
	TemporalFacts    []AIBOXTemporalEvidence `json:"temporal_facts,omitempty"`
	Conflicts        []AIBOXConflict         `json:"conflicts,omitempty"`
	EvaluatedAt      time.Time               `json:"evaluated_at"`
	EffectiveTime    *time.Time              `json:"effective_time,omitempty"`
}

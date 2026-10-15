package types

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// SourceRevision captures a source-system reality revision independently from
// parser/model retries. Exactly one row per knowledge should be current.
type SourceRevision struct {
	RevisionID      string     `json:"revision_id" gorm:"column:revision_id;type:varchar(36);primaryKey"`
	KnowledgeID     string     `json:"knowledge_id" gorm:"column:knowledge_id;type:varchar(36);not null;uniqueIndex:idx_source_revision_seq,priority:1;index"`
	RevisionSeq     int64      `json:"revision_seq" gorm:"column:revision_seq;not null;uniqueIndex:idx_source_revision_seq,priority:2"`
	SourceHash      string     `json:"source_hash,omitempty" gorm:"column:source_hash;type:varchar(128);not null;default:''"`
	NativeVersion   string     `json:"native_version,omitempty" gorm:"column:native_version;type:text;not null;default:''"`
	ETag            string     `json:"etag,omitempty" gorm:"column:etag;type:text;not null;default:''"`
	SourceUpdatedAt *time.Time `json:"source_updated_at,omitempty" gorm:"column:source_updated_at"`
	ObservedAt      time.Time  `json:"observed_at" gorm:"column:observed_at;not null"`
	ResourceRef     string     `json:"resource_ref,omitempty" gorm:"column:resource_ref;type:text;not null;default:''"`
	Locator         string     `json:"locator,omitempty" gorm:"column:locator;type:text;not null;default:''"`
	Size            int64      `json:"size" gorm:"column:size;not null;default:0"`
	Mime            string     `json:"mime,omitempty" gorm:"column:mime;type:varchar(255);not null;default:''"`
	IsCurrent       bool       `json:"is_current" gorm:"column:is_current;not null;default:true;index"`
	CreatedAt       time.Time  `json:"created_at" gorm:"column:created_at;not null"`
}

func (SourceRevision) TableName() string { return "source_revisions" }

func (r *SourceRevision) BeforeCreate(_ *gorm.DB) error {
	if r.RevisionID == "" {
		r.RevisionID = uuid.NewString()
	}
	if r.ObservedAt.IsZero() {
		r.ObservedAt = time.Now().UTC()
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = r.ObservedAt
	}
	r.IsCurrent = true
	return nil
}

package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// UpsertSourceRevision appends a reality revision only when the source fingerprint
// changes. Same-reality observations refresh locator/resource metadata in place.
func (r *knowledgeRepository) UpsertSourceRevision(
	ctx context.Context, candidate *types.SourceRevision,
) (*types.SourceRevision, bool, error) {
	if candidate == nil || candidate.KnowledgeID == "" {
		return nil, false, errors.New("source revision requires knowledge_id")
	}
	var result *types.SourceRevision
	created := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current types.SourceRevision
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("knowledge_id = ? AND is_current = ?", candidate.KnowledgeID, true).
			Order("revision_seq DESC").First(&current).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil && sameSourceReality(&current, candidate) {
			updates := map[string]any{
				"observed_at": time.Now().UTC(), "resource_ref": candidate.ResourceRef,
				"locator": candidate.Locator, "size": candidate.Size, "mime": candidate.Mime,
				"source_updated_at": candidate.SourceUpdatedAt,
			}
			if candidate.NativeVersion != "" {
				updates["native_version"] = candidate.NativeVersion
			}
			if candidate.ETag != "" {
				updates["etag"] = candidate.ETag
			}
			if updateErr := tx.Model(&current).Updates(updates).Error; updateErr != nil {
				return updateErr
			}
			if readErr := tx.First(&current, "revision_id = ?", current.RevisionID).Error; readErr != nil {
				return readErr
			}
			result = &current
			return nil
		}

		seq := int64(1)
		if err == nil {
			seq = current.RevisionSeq + 1
			if updateErr := tx.Model(&types.SourceRevision{}).
				Where("knowledge_id = ? AND is_current = ?", candidate.KnowledgeID, true).
				Update("is_current", false).Error; updateErr != nil {
				return updateErr
			}
		}
		next := *candidate
		next.RevisionID = ""
		next.RevisionSeq = seq
		next.IsCurrent = true
		if createErr := tx.Create(&next).Error; createErr != nil {
			return createErr
		}
		result = &next
		created = true
		return nil
	})
	return result, created, err
}

// RollbackSourceRevision removes a just-staged current revision after a source
// replacement fails and restores the immediately previous revision as current.
func (r *knowledgeRepository) RollbackSourceRevision(ctx context.Context, knowledgeID, revisionID string) error {
	if knowledgeID == "" || revisionID == "" {
		return nil
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rev types.SourceRevision
		if err := tx.Where("knowledge_id = ? AND revision_id = ?", knowledgeID, revisionID).First(&rev).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if err := tx.Delete(&types.SourceRevision{}, "revision_id = ?", revisionID).Error; err != nil {
			return err
		}
		if !rev.IsCurrent || rev.RevisionSeq <= 1 {
			return nil
		}
		return tx.Model(&types.SourceRevision{}).
			Where("knowledge_id = ? AND revision_seq = ?", knowledgeID, rev.RevisionSeq-1).
			Update("is_current", true).Error
	})
}

func sameSourceReality(current, next *types.SourceRevision) bool {
	if next.SourceHash != "" {
		return current.SourceHash != "" && current.SourceHash == next.SourceHash
	}
	if next.NativeVersion != "" || next.ETag != "" {
		return current.NativeVersion == next.NativeVersion && current.ETag == next.ETag
	}
	if next.SourceUpdatedAt != nil {
		return current.SourceUpdatedAt != nil && current.SourceUpdatedAt.Equal(*next.SourceUpdatedAt)
	}
	// With no source-native fingerprint, a stable locator is the best available
	// reality key. Locator-only metadata refreshes do not create a revision.
	return current.Locator == next.Locator
}

package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type TemporalRepository struct{ db *gorm.DB }

func NewTemporalRepository(db *gorm.DB) *TemporalRepository { return &TemporalRepository{db: db} }

func (r *TemporalRepository) CreateGeneration(ctx context.Context, generation *types.TemporalGraphGeneration) error {
	if generation == nil || generation.GenerationID == "" || generation.KnowledgeBaseID == "" || generation.GroupID == "" {
		return errors.New("temporal generation requires generation_id, knowledge_base_id and group_id")
	}
	if generation.Status == "" {
		generation.Status = types.TemporalGenerationBuilding
	}
	return r.db.WithContext(ctx).Create(generation).Error
}

func (r *TemporalRepository) GetActiveGeneration(ctx context.Context, tenantID uint64, kbID string) (*types.TemporalGraphGeneration, error) {
	var generation types.TemporalGraphGeneration
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_base_id = ? AND status = ?", tenantID, kbID, types.TemporalGenerationActive).
		First(&generation).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &generation, err
}

func (r *TemporalRepository) ActivateGeneration(ctx context.Context, tenantID uint64, kbID, generationID string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var target types.TemporalGraphGeneration
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("generation_id = ? AND tenant_id = ? AND knowledge_base_id = ?", generationID, tenantID, kbID).
			First(&target).Error; err != nil {
			return err
		}
		if target.Status == types.TemporalGenerationFailed {
			return errors.New("failed temporal generation cannot be activated")
		}
		if err := tx.Model(&types.TemporalGraphGeneration{}).
			Where("tenant_id = ? AND knowledge_base_id = ? AND status = ? AND generation_id <> ?", tenantID, kbID, types.TemporalGenerationActive, generationID).
			Update("status", types.TemporalGenerationRetired).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		return tx.Model(&types.TemporalGraphGeneration{}).
			Where("generation_id = ?", generationID).
			Updates(map[string]any{"status": types.TemporalGenerationActive, "activated_at": now}).Error
	})
}

func (r *TemporalRepository) GetEpisodeBinding(ctx context.Context, generationID, episodeUUID string) (*types.TemporalEpisodeBinding, error) {
	var binding types.TemporalEpisodeBinding
	err := r.db.WithContext(ctx).
		Where("generation_id = ? AND episode_uuid = ?", generationID, episodeUUID).
		First(&binding).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &binding, err
}

func (r *TemporalRepository) UpsertEpisodeBinding(ctx context.Context, binding *types.TemporalEpisodeBinding) error {
	if binding == nil || binding.GenerationID == "" || binding.EpisodeUUID == "" || binding.SourceRevisionID == "" {
		return errors.New("temporal binding requires generation_id, episode_uuid and source_revision_id")
	}
	if binding.TemporalStatus == "" {
		binding.TemporalStatus = types.TemporalStatusPending
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "episode_uuid"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"temporal_status", "temporal_attempt", "completed_at", "last_error",
		}),
	}).Create(binding).Error
}

// ErrTemporalSnapshotSuperseded indicates that a queued/in-flight extraction
// no longer owns the Knowledge snapshot. It must not retry writes as current.
var ErrTemporalSnapshotSuperseded = errors.New("temporal derived snapshot superseded")

// withCurrentDerivedSnapshot serializes each projection write with updates to
// the Knowledge row. PostgreSQL FOR UPDATE makes the snapshot check and the
// binding/transition write atomic relative to a new derived publication.
// The old snapshot can legitimately be published again (rollback), so status
// alone is not a sufficient fence.
func (r *TemporalRepository) withCurrentDerivedSnapshot(
	ctx context.Context, tenantID uint64, knowledgeID, snapshotHash string,
	write func(*TemporalRepository) error,
) error {
	if tenantID == 0 || knowledgeID == "" || snapshotHash == "" {
		return errors.New("derived temporal write requires tenant, knowledge and snapshot")
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var knowledge types.Knowledge
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND tenant_id = ?", knowledgeID, tenantID).
			First(&knowledge).Error; err != nil {
			return err
		}
		derived, err := knowledge.DerivedMetadata()
		if err != nil {
			return err
		}
		if derived == nil || derived.SnapshotHash != snapshotHash {
			return ErrTemporalSnapshotSuperseded
		}
		return write(&TemporalRepository{db: tx})
	})
}

func (r *TemporalRepository) MarkSupersededEpisodeBindingsForDerivedSnapshot(
	ctx context.Context, tenantID uint64, knowledgeID, snapshotHash,
	generationID, revisionID string, keepEpisodeUUIDs []string,
) error {
	return r.withCurrentDerivedSnapshot(ctx, tenantID, knowledgeID, snapshotHash, func(txRepo *TemporalRepository) error {
		return txRepo.MarkSupersededEpisodeBindings(ctx, generationID, knowledgeID, revisionID, keepEpisodeUUIDs)
	})
}

func (r *TemporalRepository) UpsertEpisodeBindingForDerivedSnapshot(
	ctx context.Context, tenantID uint64, knowledgeID, snapshotHash string,
	binding *types.TemporalEpisodeBinding,
) error {
	return r.withCurrentDerivedSnapshot(ctx, tenantID, knowledgeID, snapshotHash, func(txRepo *TemporalRepository) error {
		return txRepo.UpsertEpisodeBinding(ctx, binding)
	})
}

// Commit the adapter receipt and its ready binding together, under the same
// snapshot fence. A late old worker cannot insert stale transitions and then
// resurrect its old binding after a newer publication.
func (r *TemporalRepository) CommitEpisodeForDerivedSnapshot(
	ctx context.Context, tenantID uint64, knowledgeID, snapshotHash string,
	binding *types.TemporalEpisodeBinding, transitions []types.TemporalTransitionBinding,
) error {
	return r.withCurrentDerivedSnapshot(ctx, tenantID, knowledgeID, snapshotHash, func(txRepo *TemporalRepository) error {
		if err := txRepo.InsertTransitions(ctx, transitions); err != nil {
			return err
		}
		return txRepo.UpsertEpisodeBinding(ctx, binding)
	})
}

// MarkSupersededEpisodeBindings removes old derived projections from the
// current evidence view before a corrected snapshot is extracted. The raw
// SourceRevision stays authoritative and is never rewritten by this method.
// An adapter failure leaves the new projection degraded, not silently stale.
func (r *TemporalRepository) MarkSupersededEpisodeBindings(ctx context.Context, generationID, knowledgeID, revisionID string, keepEpisodeUUIDs []string) error {
	if generationID == "" || knowledgeID == "" || revisionID == "" {
		return errors.New("temporal projection scope is required")
	}
	q := r.db.WithContext(ctx).Model(&types.TemporalEpisodeBinding{}).
		Where("generation_id = ? AND knowledge_id = ? AND source_revision_id = ?", generationID, knowledgeID, revisionID).
		Where("temporal_status IN ?", []types.TemporalStatus{
			types.TemporalStatusPending, types.TemporalStatusRunning,
			types.TemporalStatusReady, types.TemporalStatusFailed,
		})
	if len(keepEpisodeUUIDs) > 0 {
		q = q.Where("episode_uuid NOT IN ?", keepEpisodeUUIDs)
	}
	return q.Update("temporal_status", types.TemporalStatusStale).Error
}

func (r *TemporalRepository) InsertTransitions(ctx context.Context, transitions []types.TemporalTransitionBinding) error {
	if len(transitions) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&transitions).Error
}

func (r *TemporalRepository) GetCurrentSourceRevision(ctx context.Context, knowledgeID string) (*types.SourceRevision, error) {
	var revision types.SourceRevision
	err := r.db.WithContext(ctx).
		Where("knowledge_id = ? AND is_current = ?", knowledgeID, true).
		Order("revision_seq DESC").First(&revision).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &revision, err
}

func (r *TemporalRepository) ListEpisodeBindingsByUUIDs(
	ctx context.Context, generationIDs, episodeUUIDs []string, currentOnly bool,
) ([]types.TemporalEpisodeBinding, error) {
	if len(generationIDs) == 0 || len(episodeUUIDs) == 0 {
		return nil, nil
	}
	q := r.db.WithContext(ctx).Model(&types.TemporalEpisodeBinding{}).
		Where("temporal_episode_bindings.generation_id IN ?", generationIDs).
		Where("temporal_episode_bindings.episode_uuid IN ?", episodeUUIDs)
	if currentOnly {
		q = q.Joins("JOIN source_revisions sr ON sr.revision_id = temporal_episode_bindings.source_revision_id AND sr.is_current = ?", true).
			Where("temporal_episode_bindings.temporal_status = ?", types.TemporalStatusReady)
	}
	var bindings []types.TemporalEpisodeBinding
	return bindings, q.Find(&bindings).Error
}

func (r *TemporalRepository) HasIncompleteEpisodeBindings(ctx context.Context, generationIDs []string) (bool, error) {
	if len(generationIDs) == 0 {
		return false, nil
	}
	var count int64
	err := r.db.WithContext(ctx).Model(&types.TemporalEpisodeBinding{}).
		Where("generation_id IN ?", generationIDs).
		Where("temporal_status IN ?", []types.TemporalStatus{types.TemporalStatusPending, types.TemporalStatusRunning, types.TemporalStatusFailed}).
		Count(&count).Error
	return count > 0, err
}

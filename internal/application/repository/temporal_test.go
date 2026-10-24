package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newTemporalRepoForTest(t *testing.T) (*TemporalRepository, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&types.Knowledge{},
		&types.TemporalGraphGeneration{},
		&types.TemporalEpisodeBinding{},
		&types.TemporalTransitionBinding{}, &types.TemporalFinalizeReceipt{},
	))
	return NewTemporalRepository(db), db
}

func TestTemporalGenerationActivationRetiresPrevious(t *testing.T) {
	repo, _ := newTemporalRepoForTest(t)
	ctx := context.Background()
	for _, id := range []string{"g1", "g2"} {
		require.NoError(t, repo.CreateGeneration(ctx, &types.TemporalGraphGeneration{
			GenerationID: id, TenantID: 7, KnowledgeBaseID: "kb-1", GroupID: "group-" + id,
			GraphitiCommit: "graphiti-pin", ExtractionSchemaVersion: "v1", ModelConfigFingerprint: "model-a",
		}))
	}
	require.NoError(t, repo.ActivateGeneration(ctx, 7, "kb-1", "g1"))
	require.NoError(t, repo.ActivateGeneration(ctx, 7, "kb-1", "g2"))
	active, err := repo.GetActiveGeneration(ctx, 7, "kb-1")
	require.NoError(t, err)
	require.Equal(t, "g2", active.GenerationID)
	var first types.TemporalGraphGeneration
	require.NoError(t, repo.db.First(&first, "generation_id = ?", "g1").Error)
	require.Equal(t, types.TemporalGenerationRetired, first.Status)
}

func TestTemporalEpisodeBindingRetryUpdatesStatusWithoutDuplicate(t *testing.T) {
	repo, db := newTemporalRepoForTest(t)
	ctx := context.Background()
	ordinal := 0
	binding := &types.TemporalEpisodeBinding{
		EpisodeUUID: "ep-1", GenerationID: "g1", GroupID: "group-g1", KnowledgeID: "k1",
		SourceRevisionID: "r1", WindowOrdinal: &ordinal, ReferenceTime: time.Now().UTC(), SourceKind: "text",
		AuthorityKey: "source:a", EvidenceSnapshot: "Alice works at Acme", EvidenceLocatorJSON: types.JSON(`{}`),
		SnapshotHash: "hash", ExtractionSchemaVersion: "v1", ModelConfigFingerprint: "model-a",
		TemporalStatus: types.TemporalStatusPending,
	}
	require.NoError(t, repo.UpsertEpisodeBinding(ctx, binding))
	binding.TemporalStatus = types.TemporalStatusReady
	binding.TemporalAttempt = 2
	now := time.Now().UTC()
	binding.CompletedAt = &now
	require.NoError(t, repo.UpsertEpisodeBinding(ctx, binding))
	stored, err := repo.GetEpisodeBinding(ctx, "g1", "ep-1")
	require.NoError(t, err)
	require.Equal(t, types.TemporalStatusReady, stored.TemporalStatus)
	require.Equal(t, 2, stored.TemporalAttempt)
	var count int64
	require.NoError(t, db.Model(&types.TemporalEpisodeBinding{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestTemporalTransitionsAreReplayIdempotent(t *testing.T) {
	repo, db := newTemporalRepoForTest(t)
	ctx := context.Background()
	transition := types.TemporalTransitionBinding{
		GenerationID: "g1", OldFactUUID: "old", NewFactUUID: "new", CausingEpisodeUUID: "ep-1",
		TransitionType: "invalidated",
	}
	require.NoError(t, repo.InsertTransitions(ctx, []types.TemporalTransitionBinding{transition}))
	require.NoError(t, repo.InsertTransitions(ctx, []types.TemporalTransitionBinding{transition}))
	var count int64
	require.NoError(t, db.Model(&types.TemporalTransitionBinding{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
}


// A delayed adapter response must not overwrite a newer snapshot's projection.
// Re-publishing the exact old snapshot later is allowed (legitimate rollback).
func TestTemporalDerivedSnapshotFenceRejectsLateWriteAndAllowsRollback(t *testing.T) {
	repo, db := newTemporalRepoForTest(t)
	ctx := context.Background()
	knowledge := &types.Knowledge{ID: "kn-1", TenantID: 7, KnowledgeBaseID: "kb-1"}
	require.NoError(t, knowledge.SetDerivedMetadata(&types.DerivedKnowledgeMetadata{SnapshotHash: "v1"}))
	require.NoError(t, db.Create(knowledge).Error)
	binding := &types.TemporalEpisodeBinding{
		EpisodeUUID: "ep-v1", GenerationID: "gen-1", GroupID: "group-1",
		KnowledgeID: "kn-1", SourceRevisionID: "rev-1", SourceKind: "text",
		AuthorityKey: "knowledge:kn-1", SnapshotHash: "window-v1",
		TemporalStatus: types.TemporalStatusRunning,
	}
	require.NoError(t, repo.UpsertEpisodeBindingForDerivedSnapshot(ctx, 7, "kn-1", "v1", binding))
	require.NoError(t, knowledge.SetDerivedMetadata(&types.DerivedKnowledgeMetadata{SnapshotHash: "v2"}))
	require.NoError(t, db.Model(&types.Knowledge{}).Where("id = ?", "kn-1").
		Update("metadata", knowledge.Metadata).Error)
	require.NoError(t, repo.MarkSupersededEpisodeBindingsForDerivedSnapshot(
		ctx, 7, "kn-1", "v2", "gen-1", "rev-1", nil))
	binding.TemporalStatus = types.TemporalStatusReady
	require.ErrorIs(t, repo.CommitEpisodeForDerivedSnapshot(ctx, 7, "kn-1", "v1", binding,
		[]types.TemporalTransitionBinding{{
			GenerationID: "gen-1", OldFactUUID: "old", NewFactUUID: "new",
			CausingEpisodeUUID: "ep-v1", TransitionType: "invalidated",
		}}), ErrTemporalSnapshotSuperseded)
	require.ErrorIs(t, repo.MarkSupersededEpisodeBindingsForDerivedSnapshot(
		ctx, 7, "kn-1", "v1", "gen-1", "rev-1", nil), ErrTemporalSnapshotSuperseded)
	stored, err := repo.GetEpisodeBinding(ctx, "gen-1", "ep-v1")
	require.NoError(t, err)
	require.Equal(t, types.TemporalStatusStale, stored.TemporalStatus)
	var transitions int64
	require.NoError(t, db.Model(&types.TemporalTransitionBinding{}).Count(&transitions).Error)
	require.Zero(t, transitions, "superseded adapter must not commit transitions")

	// A later deliberate rollback of the source snapshot can reprocess v1.
	require.NoError(t, knowledge.SetDerivedMetadata(&types.DerivedKnowledgeMetadata{SnapshotHash: "v1"}))
	require.NoError(t, db.Model(&types.Knowledge{}).Where("id = ?", "kn-1").
		Update("metadata", knowledge.Metadata).Error)
	require.NoError(t, repo.UpsertEpisodeBindingForDerivedSnapshot(ctx, 7, "kn-1", "v1", binding))
	stored, err = repo.GetEpisodeBinding(ctx, "gen-1", "ep-v1")
	require.NoError(t, err)
	require.Equal(t, types.TemporalStatusReady, stored.TemporalStatus)
}

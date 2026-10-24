package repository

import (
    "context"
    "testing"

    "github.com/Tencent/WeKnora/internal/types"
    "github.com/stretchr/testify/require"
)

// P2 #126 RED regression: an at-least-once delivery of the SAME temporal
// finalizer must not consume another task's slot in the same derived snapshot.
// SnapshotHash is content identity, not durable batch/slot ownership.
func TestDerivedTemporalDuplicateFinalizeMustNotConsumeOtherSlot(t *testing.T) {
    repo, db := newTemporalRepoForTest(t)
    knowledge := &types.Knowledge{
        ID: "kn-duplicate-finalize", TenantID: 7, KnowledgeBaseID: "kb-1",
        ParseStatus: types.ParseStatusFinalizing, PendingSubtasksCount: 2, FinalizingBatchID: "batch-1",
    }
    require.NoError(t, knowledge.SetDerivedMetadata(&types.DerivedKnowledgeMetadata{
        SnapshotHash: "same-content-hash", SourceRevisionID: "same-revision",
    }))
    require.NoError(t, db.Create(knowledge).Error)
    ctx := context.Background()

    // First acknowledgement legitimately releases its own slot.
    require.NoError(t, repo.FinalizeSubtaskForSnapshot(ctx, 7, knowledge.ID, "same-content-hash", "batch-1"))
    require.NoError(t, db.First(knowledge, "id = ?", knowledge.ID).Error)
    require.Equal(t, 1, knowledge.PendingSubtasksCount)
    require.Equal(t, types.ParseStatusFinalizing, knowledge.ParseStatus)

    // Replay of the same acknowledgement must not release the second task's
    // still-pending slot. A durable per-slot token is required to distinguish it.
    require.NoError(t, repo.FinalizeSubtaskForSnapshot(ctx, 7, knowledge.ID, "same-content-hash", "batch-1"))
    require.NoError(t, db.First(knowledge, "id = ?", knowledge.ID).Error)
    require.Equal(t, 1, knowledge.PendingSubtasksCount, "duplicate acknowledgement drained an unrelated slot")
    require.Equal(t, types.ParseStatusFinalizing, knowledge.ParseStatus,
        "duplicate acknowledgement prematurely completed the batch")
}

// Same content reprocessed in a new batch must fence old slot owners.
func TestDerivedTemporalOldBatchCannotDrainSameSnapshotNewBatch(t *testing.T) {
    repo, db := newTemporalRepoForTest(t)
    k := &types.Knowledge{ID: "kn-rebatch", TenantID: 7, KnowledgeBaseID: "kb-1", ParseStatus: types.ParseStatusFinalizing, PendingSubtasksCount: 2, FinalizingBatchID: "batch-old"}
    require.NoError(t, k.SetDerivedMetadata(&types.DerivedKnowledgeMetadata{SnapshotHash: "unchanged", SourceRevisionID: "rev-1"}))
    require.NoError(t, db.Create(k).Error)
    ctx := context.Background()
    require.NoError(t, repo.FinalizeSubtaskForSnapshot(ctx, 7, k.ID, "unchanged", "batch-old"))
    require.NoError(t, db.Model(&types.Knowledge{}).Where("id = ?", k.ID).Updates(map[string]any{"finalizing_batch_id": "batch-new", "pending_subtasks_count": 2}).Error)
    require.ErrorIs(t, repo.FinalizeSubtaskForSnapshot(ctx, 7, k.ID, "unchanged", "batch-old"), ErrTemporalSnapshotSuperseded)
    require.NoError(t, repo.FinalizeSubtaskForSnapshot(ctx, 7, k.ID, "unchanged", "batch-new"))
    require.NoError(t, db.First(k, "id = ?", k.ID).Error)
    require.Equal(t, 1, k.PendingSubtasksCount)
    require.Equal(t, types.ParseStatusFinalizing, k.ParseStatus)
}

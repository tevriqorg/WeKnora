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
        ParseStatus: types.ParseStatusFinalizing, PendingSubtasksCount: 2,
    }
    require.NoError(t, knowledge.SetDerivedMetadata(&types.DerivedKnowledgeMetadata{
        SnapshotHash: "same-content-hash", SourceRevisionID: "same-revision",
    }))
    require.NoError(t, db.Create(knowledge).Error)
    ctx := context.Background()

    // First acknowledgement legitimately releases its own slot.
    require.NoError(t, repo.FinalizeSubtaskForSnapshot(ctx, 7, knowledge.ID, "same-content-hash"))
    require.NoError(t, db.First(knowledge, "id = ?", knowledge.ID).Error)
    require.Equal(t, 1, knowledge.PendingSubtasksCount)
    require.Equal(t, types.ParseStatusFinalizing, knowledge.ParseStatus)

    // Replay of the same acknowledgement must not release the second task's
    // still-pending slot. A durable per-slot token is required to distinguish it.
    require.NoError(t, repo.FinalizeSubtaskForSnapshot(ctx, 7, knowledge.ID, "same-content-hash"))
    require.NoError(t, db.First(knowledge, "id = ?", knowledge.ID).Error)
    require.Equal(t, 1, knowledge.PendingSubtasksCount, "duplicate acknowledgement drained an unrelated slot")
    require.Equal(t, types.ParseStatusFinalizing, knowledge.ParseStatus,
        "duplicate acknowledgement prematurely completed the batch")
}

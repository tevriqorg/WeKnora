package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// A v1 extraction may remain in flight after v2 has made its binding stale.
// Its delayed acknowledgement must not restore the old episode to CURRENT.
func TestDerivedTemporalInFlightOldWorkerCannotResurrectStaleBinding(t *testing.T) {
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	defer func() {
		select {
		case <-releaseFirst:
		default:
			close(releaseFirst)
		}
	}()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req TemporalEpisodeRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		if calls.Add(1) == 1 {
			close(firstEntered)
			<-releaseFirst
		}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(TemporalEpisodeResult{
			EpisodeUUID: req.EpisodeUUID, FactUUIDs: []string{"fact"}, Status: "committed",
		}))
	}))
	defer server.Close()

	service, db := newTemporalEnrichmentTestService(t, server.URL)
	seedTemporalEnrichmentTest(t, db)
	var knowledge types.Knowledge
	require.NoError(t, db.First(&knowledge, "id = ?", "kn-1").Error)
	require.NoError(t, knowledge.SetDerivedMetadata(&types.DerivedKnowledgeMetadata{
		SnapshotHash: "derived-v1", SourceRevisionID: "native-unchanged",
	}))
	require.NoError(t, db.Model(&types.Knowledge{}).Where("id = ?", "kn-1").Update("metadata", knowledge.Metadata).Error)

	firstDone := make(chan error, 1)
	firstTask := temporalTaskForSnapshot(t, "derived-v1")
	go func() { firstDone <- service.Handle(context.Background(), firstTask) }()
	select {
	case <-firstEntered:
	case <-time.After(10 * time.Second):
		t.Fatal("v1 temporal worker never reached adapter")
	}

	// v2 publishes corrected chunks and metadata while the old adapter is blocked.
	require.NoError(t, db.Model(&types.Chunk{}).Where("id = ?", "chunk-1").
		Update("content", "Alice now works at Contoso.").Error)
	require.NoError(t, knowledge.SetDerivedMetadata(&types.DerivedKnowledgeMetadata{
		SnapshotHash: "derived-v2", SourceRevisionID: "native-unchanged",
	}))
	require.NoError(t, db.Model(&types.Knowledge{}).Where("id = ?", "kn-1").Updates(map[string]any{
		"metadata": knowledge.Metadata, "parse_status": types.ParseStatusFinalizing, "pending_subtasks_count": 1,
	}).Error)
	require.NoError(t, service.Handle(context.Background(), temporalTaskForSnapshot(t, "derived-v2")))
	require.EqualValues(t, 2, calls.Load())

	close(releaseFirst)
	select {
	case err := <-firstDone:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("v1 temporal worker did not finish")
	}

	oldID, err := deterministicDerivedTemporalEpisodeUUID("kn-1", "rev-1", "gen-1", 0, "derived-v1")
	require.NoError(t, err)
	newID, err := deterministicDerivedTemporalEpisodeUUID("kn-1", "rev-1", "gen-1", 0, "derived-v2")
	require.NoError(t, err)
	oldBinding, err := service.temporalRepo.GetEpisodeBinding(context.Background(), "gen-1", oldID)
	require.NoError(t, err)
	require.NotNil(t, oldBinding)
	newBinding, err := service.temporalRepo.GetEpisodeBinding(context.Background(), "gen-1", newID)
	require.NoError(t, err)
	require.NotNil(t, newBinding)
	require.Equal(t, types.TemporalStatusStale, oldBinding.TemporalStatus,
		"delayed v1 acknowledgement must not resurrect a superseded projection")
	require.Equal(t, types.TemporalStatusReady, newBinding.TemporalStatus)
}


// A superseded v1 adapter acknowledgement must not consume the v2 temporal
// finalizing slot. A newer snapshot may have additional work still pending.
func TestDerivedTemporalSupersededWorkerCannotReleaseNewFinalizeSlot(t *testing.T) {
    firstEntered := make(chan struct{})
    releaseFirst := make(chan struct{})
    defer func() {
        select {
        case <-releaseFirst:
        default:
            close(releaseFirst)
        }
    }()
    var calls atomic.Int32
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        var req TemporalEpisodeRequest
        require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
        if calls.Add(1) == 1 {
            close(firstEntered)
            <-releaseFirst
        }
        w.Header().Set("Content-Type", "application/json")
        require.NoError(t, json.NewEncoder(w).Encode(TemporalEpisodeResult{
            EpisodeUUID: req.EpisodeUUID, Status: "committed",
        }))
    }))
    defer server.Close()

    service, db := newTemporalEnrichmentTestService(t, server.URL)
    seedTemporalEnrichmentTest(t, db)
    var knowledge types.Knowledge
    require.NoError(t, db.First(&knowledge, "id = ?", "kn-1").Error)
    require.NoError(t, knowledge.SetDerivedMetadata(&types.DerivedKnowledgeMetadata{
        SnapshotHash: "derived-v1", SourceRevisionID: "native-unchanged",
    }))
    require.NoError(t, db.Model(&types.Knowledge{}).Where("id = ?", "kn-1").Update("metadata", knowledge.Metadata).Error)

    firstDone := make(chan error, 1)
    go func() { firstDone <- service.Handle(context.Background(), temporalTaskForSnapshot(t, "derived-v1")) }()
    select {
    case <-firstEntered:
    case <-time.After(10 * time.Second):
        t.Fatal("v1 temporal worker never reached adapter")
    }

    require.NoError(t, db.Model(&types.Chunk{}).Where("id = ?", "chunk-1").
        Update("content", "Alice now works at Contoso.").Error)
    require.NoError(t, knowledge.SetDerivedMetadata(&types.DerivedKnowledgeMetadata{
        SnapshotHash: "derived-v2", SourceRevisionID: "native-unchanged",
    }))
    // Two v2 slots: this temporal task and an unrelated pending subtask.
    require.NoError(t, db.Model(&types.Knowledge{}).Where("id = ?", "kn-1").Updates(map[string]any{
        "metadata": knowledge.Metadata, "parse_status": types.ParseStatusFinalizing,
        "pending_subtasks_count": 2,
    }).Error)
    require.NoError(t, service.Handle(context.Background(), temporalTaskForSnapshot(t, "derived-v2")))
    require.EqualValues(t, 2, calls.Load())
    require.NoError(t, db.First(&knowledge, "id = ?", "kn-1").Error)
    require.Equal(t, types.ParseStatusFinalizing, knowledge.ParseStatus)
    require.Equal(t, 1, knowledge.PendingSubtasksCount)

    close(releaseFirst)
    select {
    case err := <-firstDone:
        require.NoError(t, err)
    case <-time.After(10 * time.Second):
        t.Fatal("v1 temporal worker did not finish")
    }
    require.NoError(t, db.First(&knowledge, "id = ?", "kn-1").Error)
    require.Equal(t, types.ParseStatusFinalizing, knowledge.ParseStatus,
        "superseded v1 must not prematurely complete the v2 parse")
    require.Equal(t, 1, knowledge.PendingSubtasksCount,
        "superseded v1 must not decrement the v2 pending slot")
}

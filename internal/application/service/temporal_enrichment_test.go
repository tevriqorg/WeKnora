package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newTemporalEnrichmentTestService(t *testing.T, adapterURL string) (*TemporalEnrichmentService, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&types.Knowledge{}, &types.Chunk{}, &types.SourceRevision{},
		&types.TemporalGraphGeneration{}, &types.TemporalEpisodeBinding{}, &types.TemporalTransitionBinding{},
	))
	knowledgeRepo := apprepo.NewKnowledgeRepository(db)
	chunkRepo := apprepo.NewChunkRepository(db)
	temporalRepo := apprepo.NewTemporalRepository(db)
	t.Setenv(temporalAdapterURLEnv, adapterURL)
	return &TemporalEnrichmentService{
		knowledgeRepo: knowledgeRepo,
		chunkRepo:     chunkRepo,
		temporalRepo:  temporalRepo,
	}, db
}

func seedTemporalEnrichmentTest(t *testing.T, db *gorm.DB) {
	t.Helper()
	now := time.Date(2026, 10, 15, 2, 3, 4, 0, time.UTC)
	require.NoError(t, db.Create(&types.Knowledge{
		ID: "kn-1", TenantID: 7, KnowledgeBaseID: "kb-1",
		ParseStatus: types.ParseStatusFinalizing, PendingSubtasksCount: 1,
	}).Error)
	require.NoError(t, db.Create(&types.SourceRevision{
		RevisionID: "rev-1", KnowledgeID: "kn-1", RevisionSeq: 1,
		SourceHash: "source-hash", SourceUpdatedAt: &now, ObservedAt: now, Locator: "source/document.txt",
	}).Error)
	require.NoError(t, db.Create(&types.TemporalGraphGeneration{
		GenerationID: "gen-1", TenantID: 7, KnowledgeBaseID: "kb-1", GroupID: "group-1",
		GraphitiCommit: "graphiti-pin", ExtractionSchemaVersion: "schema-v1",
		ModelConfigFingerprint: "model-v1", Status: types.TemporalGenerationActive,
	}).Error)
	require.NoError(t, db.Create(&types.Chunk{
		ID: "chunk-1", TenantID: 7, KnowledgeID: "kn-1", KnowledgeBaseID: "kb-1",
		Content: "Alice works at Acme.", ChunkIndex: 0, IsEnabled: true, ChunkType: types.ChunkTypeText,
	}).Error)
}

func temporalTask(t *testing.T) *asynq.Task {
	t.Helper()
	payload, err := json.Marshal(types.TemporalEnrichmentPayload{
		TenantID: 7, KnowledgeBaseID: "kb-1", KnowledgeID: "kn-1",
	})
	require.NoError(t, err)
	return asynq.NewTask(types.TypeTemporalEnrichment, payload)
}

func TestTemporalEnrichmentCommitsBindingTransitionAndFinalizingSlot(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var req TemporalEpisodeRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.Equal(t, "gen-1", req.GenerationID)
		require.Equal(t, "group-1", req.GroupID)
		expectedUUID, err := deterministicTemporalEpisodeUUID("kn-1", "rev-1", "gen-1", 0)
		require.NoError(t, err)
		require.Equal(t, expectedUUID, req.EpisodeUUID)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(TemporalEpisodeResult{
			EpisodeUUID: req.EpisodeUUID, FactUUIDs: []string{"new-fact"},
			InvalidatedBy: map[string][]string{"old-fact": {"new-fact"}}, Status: "committed",
		})
	}))
	defer server.Close()
	service, db := newTemporalEnrichmentTestService(t, server.URL)
	seedTemporalEnrichmentTest(t, db)

	require.NoError(t, service.Handle(context.Background(), temporalTask(t)))
	require.EqualValues(t, 1, calls.Load())
	var knowledge types.Knowledge
	require.NoError(t, db.First(&knowledge, "id = ?", "kn-1").Error)
	require.Equal(t, types.ParseStatusCompleted, knowledge.ParseStatus)
	require.Zero(t, knowledge.PendingSubtasksCount)
	var binding types.TemporalEpisodeBinding
	require.NoError(t, db.First(&binding).Error)
	require.Equal(t, types.TemporalStatusReady, binding.TemporalStatus)
	require.Equal(t, "rev-1", binding.SourceRevisionID)
	var transitions int64
	require.NoError(t, db.Model(&types.TemporalTransitionBinding{}).Count(&transitions).Error)
	require.EqualValues(t, 1, transitions)
}

func TestTemporalEnrichmentRecoversIncompleteBindingWithSameEpisodeIdentity(t *testing.T) {
	var calls atomic.Int32
	var seenUUID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var req TemporalEpisodeRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		if seenUUID == "" {
			seenUUID = req.EpisodeUUID
		} else {
			require.Equal(t, seenUUID, req.EpisodeUUID)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(TemporalEpisodeResult{
			EpisodeUUID: req.EpisodeUUID, Status: "committed", Reused: calls.Load() > 1,
			InvalidatedBy: map[string][]string{"old-fact": {"new-fact"}},
		})
	}))
	defer server.Close()
	service, db := newTemporalEnrichmentTestService(t, server.URL)
	seedTemporalEnrichmentTest(t, db)
	require.NoError(t, service.Handle(context.Background(), temporalTask(t)))

	// Simulate Graphiti commit succeeding while the caller lost durable transition/ready acks.
	require.NoError(t, db.Where("causing_episode_uuid = ?", seenUUID).Delete(&types.TemporalTransitionBinding{}).Error)
	require.NoError(t, db.Model(&types.TemporalEpisodeBinding{}).Where("episode_uuid = ?", seenUUID).
		Updates(map[string]any{"temporal_status": types.TemporalStatusRunning, "completed_at": nil}).Error)
	require.NoError(t, db.Model(&types.Knowledge{}).Where("id = ?", "kn-1").
		Updates(map[string]any{"parse_status": types.ParseStatusFinalizing, "pending_subtasks_count": 1}).Error)
	require.NoError(t, service.Handle(context.Background(), temporalTask(t)))
	require.EqualValues(t, 2, calls.Load())
	var binding types.TemporalEpisodeBinding
	require.NoError(t, db.First(&binding, "episode_uuid = ?", seenUUID).Error)
	require.Equal(t, types.TemporalStatusReady, binding.TemporalStatus)
	var transitionCount int64
	require.NoError(t, db.Model(&types.TemporalTransitionBinding{}).Where("causing_episode_uuid = ?", seenUUID).Count(&transitionCount).Error)
	require.EqualValues(t, 1, transitionCount)
}

// Processor corrections may change derived text and time locators while the
// authoritative raw SourceRevision stays unchanged. A ready old episode must
// not suppress a new Graphiti extraction or remain current after replacement.
func TestDerivedTemporalSnapshotRefreshesUnchangedNativeRevision(t *testing.T) {
    var calls atomic.Int32
    var episodeIDs []string
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        var req TemporalEpisodeRequest
        require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
        episodeIDs = append(episodeIDs, req.EpisodeUUID)
        calls.Add(1)
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
    require.NoError(t, knowledge.SetDerivedMetadata(&types.DerivedKnowledgeMetadata{SnapshotHash: "derived-v1", SourceRevisionID: "native-unchanged"}))
    require.NoError(t, db.Model(&types.Knowledge{}).Where("id = ?", "kn-1").Update("metadata", knowledge.Metadata).Error)

    require.NoError(t, service.Handle(context.Background(), temporalTask(t)))
    require.EqualValues(t, 1, calls.Load())
    var sourceCount int64
    require.NoError(t, db.Model(&types.SourceRevision{}).Where("knowledge_id = ?", "kn-1").Count(&sourceCount).Error)
    require.EqualValues(t, 1, sourceCount)

    require.NoError(t, db.Model(&types.Chunk{}).Where("id = ?", "chunk-1").Update("content", "Alice works at Contoso since correction.").Error)
    require.NoError(t, knowledge.SetDerivedMetadata(&types.DerivedKnowledgeMetadata{SnapshotHash: "derived-v2", SourceRevisionID: "native-unchanged"}))
    require.NoError(t, db.Model(&types.Knowledge{}).Where("id = ?", "kn-1").Updates(map[string]any{
        "metadata": knowledge.Metadata, "parse_status": types.ParseStatusFinalizing, "pending_subtasks_count": 1,
    }).Error)
    require.NoError(t, service.Handle(context.Background(), temporalTask(t)))
    require.EqualValues(t, 2, calls.Load(), "new derived snapshot must reach adapter despite unchanged native revision")
    require.Len(t, episodeIDs, 2)
    require.NotEqual(t, episodeIDs[0], episodeIDs[1], "Graphiti deduplicates identical episode UUIDs")
    var bindings []types.TemporalEpisodeBinding
    require.NoError(t, db.Order("created_at").Find(&bindings).Error)
    require.Len(t, bindings, 2)
    statuses := map[string]types.TemporalStatus{}
    for _, b := range bindings { statuses[b.EpisodeUUID] = b.TemporalStatus }
    require.Equal(t, types.TemporalStatusStale, statuses[episodeIDs[0]], "old facts must not remain current")
    require.Equal(t, types.TemporalStatusReady, statuses[episodeIDs[1]])
    require.NoError(t, db.Model(&types.SourceRevision{}).Where("knowledge_id = ?", "kn-1").Count(&sourceCount).Error)
    require.EqualValues(t, 1, sourceCount, "processor-only correction must not create a raw revision")
}

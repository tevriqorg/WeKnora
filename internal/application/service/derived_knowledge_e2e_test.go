package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

func TestG6DerivedEnvelopeReachesTemporalWithOriginalTimecode(t *testing.T) {
	mini := miniredis.RunT(t)
	client := asynq.NewClient(asynq.RedisClientOpt{Addr: mini.Addr()})
	defer client.Close()
	fixture := newDocumentWriteFixture(t)
	kbSvc := &g2OrdinaryDocKBService{kb: fixture.kbs.values["kb"]}
	fixture.svc.kbService = kbSvc
	require.NoError(t, fixture.db.AutoMigrate(
		&types.SourceRevision{}, &types.TemporalGraphGeneration{},
		&types.TemporalEpisodeBinding{}, &types.TemporalTransitionBinding{}, &types.TemporalFinalizeReceipt{},
	))
	fixture.svc.task = client
	knowledgeRepo := apprepo.NewKnowledgeRepository(fixture.db)
	revisionStore, ok := any(knowledgeRepo).(derivedSourceRevisionStore)
	require.True(t, ok)
	derived := newDerivedKnowledgeEnvelopeService(fixture.svc, revisionStore)

	var temporalRequest TemporalEpisodeRequest
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/episodes/upsert", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&temporalRequest))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(TemporalEpisodeResult{
			EpisodeUUID: temporalRequest.EpisodeUUID, FactUUIDs: []string{"fact-g6"}, Status: "committed",
		})
	}))
	defer adapter.Close()

	now := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	envelope := &types.DerivedKnowledgeEnvelope{
		SourceKey: "p16:recording-1", SourceRevisionID: "p16-rev-7", DerivedObjectID: "asset-42",
		Title: "NVR clip 42", Markdown: "A person enters the room and leaves shortly afterwards.",
		SourceResourceRef: "p16://asset/asset-42",
		SourceLocators: types.SourceLocators{{
			Type: types.SourceLocatorTime, StartMs: 1250, EndMs: 4900, SourceID: "event-9", Quote: "person enters",
		}},
		Processor: "p16-home-vision", ProcessorVersion: "consumer-api-v2",
		Capabilities: []string{"vlm", "keyframes", "timeline"}, AnalysisStatus: "ready", ProducedAt: now,
	}
	knowledge, err := derived.Upsert(fixture.ctx, "kb", envelope)
	require.NoError(t, err)
	require.NotEmpty(t, knowledge.ID)

	// Keep this E2E focused on the temporal branch rather than summary model setup.
	disabled := false
	require.NoError(t, knowledge.SetProcessOverrides(&types.KnowledgeProcessOverrides{SummaryEnabled: &disabled}))
	require.NoError(t, fixture.repo.UpdateKnowledge(fixture.ctx, knowledge))

	var revision types.SourceRevision
	require.NoError(t, fixture.db.First(&revision, "knowledge_id = ? AND is_current = ?", knowledge.ID, true).Error)
	require.Equal(t, "p16-rev-7", revision.NativeVersion)
	require.Equal(t, "p16://asset/asset-42", revision.ResourceRef)

	require.NoError(t, fixture.db.Create(&types.TemporalGraphGeneration{
		GenerationID: "gen-g6", TenantID: 7, KnowledgeBaseID: "kb", GroupID: "group-g6",
		GraphitiCommit: "graphiti-pin", ExtractionSchemaVersion: "schema-v1",
		ModelConfigFingerprint: "model-v1", Status: types.TemporalGenerationActive,
	}).Error)
	t.Setenv("WEKNORA_TEMPORAL_ENABLED", "true")
	t.Setenv(temporalAdapterURLEnv, adapter.URL)

	post := &KnowledgePostProcessService{
		knowledgeRepo: fixture.repo, kbService: kbSvc, chunkRepo: fixture.chunkRepo, taskEnqueuer: client,
	}
	temporal := &TemporalEnrichmentService{
		knowledgeRepo: fixture.repo, chunkRepo: fixture.chunkRepo,
		temporalRepo: apprepo.NewTemporalRepository(fixture.db),
	}
	core := asynq.NewServer(asynq.RedisClientOpt{Addr: mini.Addr()}, asynq.Config{Concurrency: 1, Queues: map[string]int{types.QueueDefault: 1}})
	coreMux := asynq.NewServeMux()
	coreMux.HandleFunc(types.TypeManualProcess, fixture.svc.ProcessManualUpdate)
	require.NoError(t, core.Start(coreMux))
	defer core.Shutdown()
	postServer := asynq.NewServer(asynq.RedisClientOpt{Addr: mini.Addr()}, asynq.Config{Concurrency: 1, Queues: map[string]int{types.QueuePostProcess: 1}})
	postMux := asynq.NewServeMux()
	postMux.HandleFunc(types.TypeKnowledgePostProcess, post.Handle)
	require.NoError(t, postServer.Start(postMux))
	defer postServer.Shutdown()
	enrichment := asynq.NewServer(asynq.RedisClientOpt{Addr: mini.Addr()}, asynq.Config{Concurrency: 1, Queues: map[string]int{types.QueueTemporal: 1}})
	enrichMux := asynq.NewServeMux()
	enrichMux.HandleFunc(types.TypeTemporalEnrichment, temporal.Handle)
	require.NoError(t, enrichment.Start(enrichMux))
	defer enrichment.Shutdown()

	deadline := time.Now().Add(10 * time.Second)
	for {
		current, getErr := fixture.repo.GetKnowledgeByID(fixture.ctx, 7, knowledge.ID)
		require.NoError(t, getErr)
		if current.ParseStatus == types.ParseStatusCompleted {
			break
		}
		if current.ParseStatus == types.ParseStatusFailed {
			t.Fatalf("derived knowledge failed: %s", current.ErrorMessage)
		}
		if time.Now().After(deadline) {
			t.Fatalf("derived knowledge did not complete; status=%s", current.ParseStatus)
		}
		time.Sleep(25 * time.Millisecond)
	}

	chunks, err := fixture.chunkRepo.ListChunksByKnowledgeID(fixture.ctx, 7, knowledge.ID)
	require.NoError(t, err)
	require.NotEmpty(t, chunks)
	require.NotEmpty(t, chunks[0].SourceLocators)
	require.Equal(t, int64(1250), chunks[0].SourceLocators[0].StartMs)
	require.Equal(t, int64(4900), chunks[0].SourceLocators[0].EndMs)
	require.Equal(t, "event-9", chunks[0].SourceLocators[0].SourceID)

	var binding types.TemporalEpisodeBinding
	require.NoError(t, fixture.db.First(&binding, "knowledge_id = ?", knowledge.ID).Error)
	require.Equal(t, types.TemporalStatusReady, binding.TemporalStatus)
	require.Equal(t, revision.RevisionID, binding.SourceRevisionID)
	require.Contains(t, temporalRequest.Content, "person enters")
	require.Contains(t, binding.EvidenceLocatorJSON.ToString(), "1250")
	require.Contains(t, binding.EvidenceLocatorJSON.ToString(), "event-9")
}

func TestDerivedRevisionUpdatePersistsHistoryOnStableKnowledge(t *testing.T) {
	mini := miniredis.RunT(t)
	client := asynq.NewClient(asynq.RedisClientOpt{Addr: mini.Addr()})
	defer client.Close()
	fixture := newDocumentWriteFixture(t)
	fixture.svc.kbService = &g2OrdinaryDocKBService{kb: fixture.kbs.values["kb"]}
	require.NoError(t, fixture.db.AutoMigrate(&types.SourceRevision{}))
	fixture.svc.task = client
	repo := apprepo.NewKnowledgeRepository(fixture.db)
	fixture.svc.repo = repo
	revisions, ok := any(repo).(derivedSourceRevisionStore)
	require.True(t, ok)
	svc := newDerivedKnowledgeEnvelopeService(fixture.svc, revisions)
	first := derivedEnvelope("ready")
	a, err := svc.Upsert(fixture.ctx, "kb", first)
	require.NoError(t, err)
	originalID := a.ID
	next := derivedEnvelope("ready")
	next.SourceRevisionID = "rev-2"
	next.Markdown = "person leaves"
	b, err := svc.Upsert(fixture.ctx, "kb", next)
	require.NoError(t, err)
	require.Equal(t, originalID, b.ID)
	var history []types.SourceRevision
	require.NoError(t, fixture.db.Where("knowledge_id = ?", originalID).Order("revision_seq").Find(&history).Error)
	require.Len(t, history, 2)
	require.False(t, history[0].IsCurrent)
	require.True(t, history[1].IsCurrent)
	require.Equal(t, "rev-2", history[1].NativeVersion)
	require.Equal(t, next.SourceResourceRef, history[1].ResourceRef)
	require.Equal(t, next.SourceKey, history[1].Locator)
	upgrade := *next
	upgrade.IdempotencyKey = ""
	upgrade.ProcessorVersion = "consumer-api-v3"
	c, err := svc.Upsert(fixture.ctx, "kb", &upgrade)
	require.NoError(t, err)
	require.Equal(t, originalID, c.ID)
	var count int64
	require.NoError(t, fixture.db.Model(&types.SourceRevision{}).Where("knowledge_id = ?", originalID).Count(&count).Error)
	require.EqualValues(t, 2, count, "processor replay must not create a reality revision")
}

// A queue error occurs after metadata commits; it must not undo the raw revision.
func TestDerivedPostCommitQueueFailurePreservesCurrentSourceRevision(t *testing.T) {
	mini := miniredis.RunT(t)
	client := asynq.NewClient(asynq.RedisClientOpt{Addr: mini.Addr()})
	fixture := newDocumentWriteFixture(t)
	fixture.svc.kbService = &g2OrdinaryDocKBService{kb: fixture.kbs.values["kb"]}
	require.NoError(t, fixture.db.AutoMigrate(&types.SourceRevision{}))
	fixture.svc.task = client
	repo := apprepo.NewKnowledgeRepository(fixture.db)
	fixture.svc.repo = repo
	revisions, ok := any(repo).(derivedSourceRevisionStore)
	require.True(t, ok)
	svc := newDerivedKnowledgeEnvelopeService(fixture.svc, revisions)
	first, err := svc.Upsert(fixture.ctx, "kb", derivedEnvelope("ready"))
	require.NoError(t, err)
	require.NoError(t, client.Close()) // Real queue transport failure, not a writer spy.
	next := derivedEnvelope("ready")
	next.SourceRevisionID = "rev-2"
	next.Markdown = "v2 committed before queue failure"
	_, err = svc.Upsert(fixture.ctx, "kb", next)
	require.Error(t, err)
	stored, err := repo.GetKnowledgeByID(fixture.ctx, first.TenantID, first.ID)
	require.NoError(t, err)
	meta, err := stored.DerivedMetadata()
	require.NoError(t, err)
	require.Equal(t, "rev-2", meta.SourceRevisionID)
	var current types.SourceRevision
	require.NoError(t, fixture.db.Where("knowledge_id = ? AND is_current = ?", first.ID, true).First(&current).Error)
	require.Equal(t, "rev-2", current.NativeVersion, "queue failure after committed v2 metadata must not restore v1 source")
}

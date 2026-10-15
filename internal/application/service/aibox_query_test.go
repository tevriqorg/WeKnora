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
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func testAIBOXTemporalDB(t *testing.T) (*apprepo.TemporalRepository, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.SourceRevision{}, &types.TemporalGraphGeneration{}, &types.TemporalEpisodeBinding{}))
	return apprepo.NewTemporalRepository(db), db
}

func aiboxTenantContext() context.Context {
	return context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
}

func seedAIBOXGeneration(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Create(&types.TemporalGraphGeneration{
		GenerationID: "gen-1", TenantID: 7, KnowledgeBaseID: "kb-1", GroupID: "group-1",
		GraphitiCommit: "pin", ExtractionSchemaVersion: "v1", ModelConfigFingerprint: "m1",
		Status: types.TemporalGenerationActive,
	}).Error)
}

func TestPlanAIBOXQueryForcesExplicitTemporalIntent(t *testing.T) {
	cases := []struct {
		query string
		mode  types.AIBOXQueryMode
	}{
		{"现在 Alice 在哪里？", types.AIBOXQueryModeCurrent},
		{"Alice 的历史是什么？", types.AIBOXQueryModeHistory},
		{"Where was Alice as of 2026-10-01?", types.AIBOXQueryModeValidAt},
		{"What did we know at 2026-10-01?", types.AIBOXQueryModeKnownAt},
	}
	for _, tc := range cases {
		mode, required, _, err := planAIBOXQuery(types.AIBOXQueryInput{Query: tc.query, Mode: types.AIBOXQueryModeAuto})
		require.NoError(t, err)
		require.True(t, required, tc.query)
		require.Equal(t, tc.mode, mode, tc.query)
	}
	mode, required, _, err := planAIBOXQuery(types.AIBOXQueryInput{Query: "Explain vector search", Mode: types.AIBOXQueryModeAuto})
	require.NoError(t, err)
	require.False(t, required)
	require.Equal(t, types.AIBOXQueryModeAuto, mode)
}

func TestAIBOXCurrentDropsFactsSupportedOnlyByOldSourceRevision(t *testing.T) {
	repo, db := testAIBOXTemporalDB(t)
	seedAIBOXGeneration(t, db)
	now := time.Now().UTC()
	require.NoError(t, db.Create(&types.SourceRevision{RevisionID: "rev-old", KnowledgeID: "kn-1", RevisionSeq: 1, ObservedAt: now, CreatedAt: now}).Error)
	require.NoError(t, db.Model(&types.SourceRevision{}).Where("revision_id = ?", "rev-old").Update("is_current", false).Error)
	require.NoError(t, db.Create(&types.SourceRevision{RevisionID: "rev-new", KnowledgeID: "kn-1", RevisionSeq: 2, ObservedAt: now, CreatedAt: now}).Error)
	for _, b := range []types.TemporalEpisodeBinding{
		{EpisodeUUID: "ep-old", GenerationID: "gen-1", GroupID: "group-1", KnowledgeID: "kn-1", SourceRevisionID: "rev-old", WindowOrdinal: intPtr(0), ReferenceTime: now, SourceKind: "text", AuthorityKey: "official", EvidenceSnapshot: "old", SnapshotHash: "h1", ExtractionSchemaVersion: "v1", ModelConfigFingerprint: "m1", TemporalStatus: types.TemporalStatusReady},
		{EpisodeUUID: "ep-new", GenerationID: "gen-1", GroupID: "group-1", KnowledgeID: "kn-1", SourceRevisionID: "rev-new", WindowOrdinal: intPtr(0), ReferenceTime: now, SourceKind: "text", AuthorityKey: "official", EvidenceSnapshot: "new", SnapshotHash: "h2", ExtractionSchemaVersion: "v1", ModelConfigFingerprint: "m1", TemporalStatus: types.TemporalStatusReady},
	} {
		require.NoError(t, db.Create(&b).Error)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(TemporalSearchResponse{Facts: []TemporalSearchFact{
			{UUID: "fact-old", Fact: "Alice works at OldCo", Relation: "works_at", GroupID: "group-1", Episodes: []string{"ep-old"}, CreatedAt: now, Source: TemporalSearchEntity{UUID: "alice"}, Target: TemporalSearchEntity{UUID: "oldco"}},
			{UUID: "fact-new", Fact: "Alice works at NewCo", Relation: "works_at", GroupID: "group-1", Episodes: []string{"ep-new"}, CreatedAt: now, Source: TemporalSearchEntity{UUID: "alice"}, Target: TemporalSearchEntity{UUID: "newco"}},
		}, Freshness: TemporalSearchFreshness{Mode: "current", EvaluatedAt: now}})
	}))
	defer server.Close()
	svc := &aiboxQueryService{temporalRepo: repo, adapterURL: server.URL}
	got, err := svc.Query(aiboxTenantContext(), types.AIBOXQueryInput{Query: "现在 Alice 在哪里？", KBIDs: []string{"kb-1"}, Mode: types.AIBOXQueryModeCurrent})
	require.NoError(t, err)
	require.False(t, got.TemporalDegraded)
	require.Len(t, got.TemporalFacts, 1)
	require.Equal(t, "fact-new", got.TemporalFacts[0].FactUUID)
}

func TestAIBOXTemporalIncompleteBindingFailsClosedBeforeAdapter(t *testing.T) {
	repo, db := testAIBOXTemporalDB(t)
	seedAIBOXGeneration(t, db)
	now := time.Now().UTC()
	require.NoError(t, db.Create(&types.SourceRevision{RevisionID: "rev-1", KnowledgeID: "kn-1", RevisionSeq: 1, IsCurrent: true, ObservedAt: now, CreatedAt: now}).Error)
	require.NoError(t, db.Create(&types.TemporalEpisodeBinding{EpisodeUUID: "ep-1", GenerationID: "gen-1", GroupID: "group-1", KnowledgeID: "kn-1", SourceRevisionID: "rev-1", WindowOrdinal: intPtr(0), ReferenceTime: now, SourceKind: "text", AuthorityKey: "official", EvidenceSnapshot: "x", SnapshotHash: "h", ExtractionSchemaVersion: "v1", ModelConfigFingerprint: "m1", TemporalStatus: types.TemporalStatusFailed}).Error)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	svc := &aiboxQueryService{temporalRepo: repo, adapterURL: server.URL}
	got, err := svc.Query(aiboxTenantContext(), types.AIBOXQueryInput{Query: "当前状态？", KBIDs: []string{"kb-1"}, Mode: types.AIBOXQueryModeCurrent})
	require.NoError(t, err)
	require.True(t, got.TemporalRequired)
	require.True(t, got.TemporalDegraded)
	require.Zero(t, calls.Load())
}

func TestAIBOXDifferentAuthorityClaimsRemainConflict(t *testing.T) {
	now := time.Now().UTC()
	facts := []types.AIBOXTemporalEvidence{
		{FactUUID: "f1", Fact: "Alice works at Acme", Relation: "works_at", GroupID: "g", AuthorityKeys: []string{"official"}, CreatedAt: now, Source: types.AIBOXTemporalEntity{UUID: "alice"}, Target: types.AIBOXTemporalEntity{UUID: "acme"}},
		{FactUUID: "f2", Fact: "Alice works at Beta", Relation: "works_at", GroupID: "g", AuthorityKeys: []string{"forum"}, CreatedAt: now, Source: types.AIBOXTemporalEntity{UUID: "alice"}, Target: types.AIBOXTemporalEntity{UUID: "beta"}},
	}
	conflicts := detectAIBOXConflicts(facts)
	require.Len(t, conflicts, 1)
	require.Len(t, conflicts[0].Claims, 2)
}

func intPtr(v int) *int { return &v }

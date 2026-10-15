package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type migrationTaskRecorder struct{ tasks []*asynq.Task }

func (r *migrationTaskRecorder) Enqueue(t *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	r.tasks = append(r.tasks, t)
	return &asynq.TaskInfo{ID: "migration-task"}, nil
}

func newMigrationFixture(t *testing.T) (*AIBOXMigrationService, *gorm.DB, *migrationTaskRecorder) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.KnowledgeBase{}, &types.Knowledge{}, &types.Chunk{}, &types.SourceRevision{}, &types.TemporalGraphGeneration{}, &types.TemporalEpisodeBinding{}, &types.TemporalTransitionBinding{}))
	rec := &migrationTaskRecorder{}
	kRepo := apprepo.NewKnowledgeRepository(db)
	cRepo := apprepo.NewChunkRepository(db)
	temporal := apprepo.NewTemporalRepository(db)
	return NewAIBOXMigrationService(db, kRepo, cRepo, temporal, rec), db, rec
}

func migrationConfig() AIBOXMigrationConfig {
	return AIBOXMigrationConfig{GraphitiCommit: "graphiti-pin", ExtractionSchemaVersion: "schema-v1", ModelConfigFingerprint: "model-v1"}
}

func TestG7PrepareBootstrapsRevisionGenerationAndSelectiveBackfill(t *testing.T) {
	svc, db, tasks := newMigrationFixture(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 15, 1, 2, 3, 0, time.UTC)
	require.NoError(t, db.Create(&types.KnowledgeBase{ID: "kb-1", TenantID: 7, Name: "KB"}).Error)
	k := &types.Knowledge{ID: "kn-1", TenantID: 7, KnowledgeBaseID: "kb-1", Type: "file", Source: "https://example.invalid/a.pdf", FileName: "a.pdf", FileType: "pdf", FileHash: "abc123", FileSize: 42, ParseStatus: types.ParseStatusCompleted, EnableStatus: "enabled", CreatedAt: now, UpdatedAt: now}
	require.NoError(t, db.Create(k).Error)
	require.NoError(t, db.Create(&types.Chunk{ID: "c1", TenantID: 7, KnowledgeID: "kn-1", KnowledgeBaseID: "kb-1", Content: "Alice works at Acme.", ChunkIndex: 0, IsEnabled: true, ChunkType: types.ChunkTypeText}).Error)
	report, err := svc.Prepare(ctx, migrationConfig())
	require.NoError(t, err)
	require.Empty(t, report.Blocked)
	require.Equal(t, 1, report.SourceRevisionsCreated)
	require.Equal(t, 1, report.GenerationsCreated)
	require.Equal(t, 1, report.BackfillEnqueued)
	require.Len(t, tasks.tasks, 1)
	var rev types.SourceRevision
	require.NoError(t, db.First(&rev, "knowledge_id = ? AND is_current = ?", "kn-1", true).Error)
	require.Equal(t, int64(1), rev.RevisionSeq)
	require.Equal(t, "abc123", rev.SourceHash)
	require.Equal(t, "https://example.invalid/a.pdf", rev.Locator)
	var gen types.TemporalGraphGeneration
	require.NoError(t, db.First(&gen, "knowledge_base_id = ? AND status = ?", "kb-1", types.TemporalGenerationActive).Error)
	require.Equal(t, "aibox_t7_kb_"+sha256Hex([]byte("kb-1"))[:24]+"_g_1", gen.GroupID)
	// Crash/restart replay keeps revision/generation identity stable.
	report2, err := svc.Prepare(ctx, migrationConfig())
	require.NoError(t, err)
	require.Zero(t, report2.SourceRevisionsCreated)
	require.Equal(t, 1, report2.SourceRevisionsReused)
	require.Zero(t, report2.GenerationsCreated)
	require.Equal(t, 1, report2.GenerationsReused)
	var revs, gens int64
	require.NoError(t, db.Model(&types.SourceRevision{}).Where("knowledge_id = ?", "kn-1").Count(&revs).Error)
	require.NoError(t, db.Model(&types.TemporalGraphGeneration{}).Where("knowledge_base_id = ?", "kb-1").Count(&gens).Error)
	require.EqualValues(t, 1, revs)
	require.EqualValues(t, 1, gens)
}

func TestG7PreflightRequiresReadySupportAndThenPasses(t *testing.T) {
	svc, db, _ := newMigrationFixture(t)
	ctx := context.Background()
	require.NoError(t, db.Create(&types.KnowledgeBase{ID: "kb-1", TenantID: 7, Name: "KB"}).Error)
	k := &types.Knowledge{ID: "kn-1", TenantID: 7, KnowledgeBaseID: "kb-1", Type: "file", FileName: "a.pdf", FileType: "pdf", FileHash: "abc", ParseStatus: types.ParseStatusCompleted, EnableStatus: "enabled"}
	require.NoError(t, db.Create(k).Error)
	require.NoError(t, db.Create(&types.Chunk{ID: "c1", TenantID: 7, KnowledgeID: "kn-1", KnowledgeBaseID: "kb-1", Content: "Alice works at Acme.", ChunkIndex: 0, IsEnabled: true, ChunkType: types.ChunkTypeText}).Error)
	_, err := svc.Prepare(ctx, migrationConfig())
	require.NoError(t, err)
	pre, err := svc.Preflight(ctx, migrationConfig())
	require.NoError(t, err)
	require.Contains(t, pre.Blocked, "knowledge:kn-1:temporal-backfill-incomplete")
	var rev types.SourceRevision
	require.NoError(t, db.First(&rev, "knowledge_id = ?", "kn-1").Error)
	var gen types.TemporalGraphGeneration
	require.NoError(t, db.First(&gen, "knowledge_base_id = ?", "kb-1").Error)
	chunks, err := svc.chunkRepo.ListChunksByKnowledgeIDAndTypes(ctx, 7, "kn-1", []types.ChunkType{types.ChunkTypeText})
	require.NoError(t, err)
	windows, err := buildTemporalEvidenceWindows(chunks, defaultTemporalWindowChars)
	require.NoError(t, err)
	require.NotEmpty(t, windows)
	for _, w := range windows {
		ep, err := deterministicTemporalEpisodeUUID("kn-1", rev.RevisionID, gen.GenerationID, w.Ordinal)
		require.NoError(t, err)
		ord := w.Ordinal
		require.NoError(t, db.Create(&types.TemporalEpisodeBinding{EpisodeUUID: ep, GenerationID: gen.GenerationID, GroupID: gen.GroupID, KnowledgeID: "kn-1", SourceRevisionID: rev.RevisionID, WindowOrdinal: &ord, ReferenceTime: time.Now(), SourceKind: "text", AuthorityKey: "knowledge:kn-1", EvidenceSnapshot: w.Content, SnapshotHash: w.SnapshotHash, ExtractionSchemaVersion: gen.ExtractionSchemaVersion, ModelConfigFingerprint: gen.ModelConfigFingerprint, TemporalStatus: types.TemporalStatusReady}).Error)
	}
	pre, err = svc.Preflight(ctx, migrationConfig())
	require.NoError(t, err)
	require.Empty(t, pre.Blocked)
	require.Equal(t, 1, pre.BackfillReady)
}

func TestG7BootstrapFailsClosedOnInsufficientSourceIdentity(t *testing.T) {
	svc, db, _ := newMigrationFixture(t)
	require.NoError(t, db.Create(&types.KnowledgeBase{ID: "kb-1", TenantID: 7, Name: "KB"}).Error)
	require.NoError(t, db.Create(&types.Knowledge{ID: "kn-empty", TenantID: 7, KnowledgeBaseID: "kb-1", Type: "file", ParseStatus: types.ParseStatusCompleted, EnableStatus: "enabled"}).Error)
	report, err := svc.Prepare(context.Background(), migrationConfig())
	require.NoError(t, err)
	require.Contains(t, report.Blocked, "knowledge:kn-empty:insufficient-source-identity")
	var n int64
	require.NoError(t, db.Model(&types.SourceRevision{}).Count(&n).Error)
	require.Zero(t, n)
}

func TestG7ManualBootstrapHashesExistingManualSource(t *testing.T) {
	svc, db, _ := newMigrationFixture(t)
	require.NoError(t, db.Create(&types.KnowledgeBase{ID: "kb-1", TenantID: 7, Name: "KB"}).Error)
	k := &types.Knowledge{ID: "manual-1", TenantID: 7, KnowledgeBaseID: "kb-1", Type: types.KnowledgeTypeManual, ParseStatus: types.ManualKnowledgeStatusDraft, EnableStatus: "disabled"}
	require.NoError(t, k.SetManualMetadata(types.NewManualKnowledgeMetadata("# Existing note", types.ManualKnowledgeStatusDraft, 3)))
	require.NoError(t, db.Create(k).Error)
	report, err := svc.Prepare(context.Background(), migrationConfig())
	require.NoError(t, err)
	require.Empty(t, report.Blocked)
	var rev types.SourceRevision
	require.NoError(t, db.First(&rev, "knowledge_id = ?", "manual-1").Error)
	require.Equal(t, sha256Hex([]byte("# Existing note")), rev.SourceHash)
	require.Equal(t, "manual:manual-1", rev.Locator)
}

func TestG7BackfillPayloadUsesExistingTemporalQueue(t *testing.T) {
	svc, db, tasks := newMigrationFixture(t)
	require.NoError(t, db.Create(&types.KnowledgeBase{ID: "kb", TenantID: 7, Name: "KB"}).Error)
	require.NoError(t, db.Create(&types.Knowledge{ID: "k", TenantID: 7, KnowledgeBaseID: "kb", Type: "file", FileHash: "h", ParseStatus: types.ParseStatusCompleted, EnableStatus: "enabled"}).Error)
	require.NoError(t, db.Create(&types.Chunk{ID: "c", TenantID: 7, KnowledgeID: "k", KnowledgeBaseID: "kb", Content: "x", ChunkIndex: 0, IsEnabled: true, ChunkType: types.ChunkTypeText}).Error)
	_, err := svc.Prepare(context.Background(), migrationConfig())
	require.NoError(t, err)
	require.Len(t, tasks.tasks, 1)
	require.Equal(t, types.TypeTemporalEnrichment, tasks.tasks[0].Type())
	var p types.TemporalEnrichmentPayload
	require.NoError(t, json.Unmarshal(tasks.tasks[0].Payload(), &p))
	require.Equal(t, "k", p.KnowledgeID)
}

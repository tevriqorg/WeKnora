package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"gorm.io/gorm"
)

type AIBOXMigrationConfig struct {
	GraphitiCommit          string `json:"graphiti_commit"`
	ExtractionSchemaVersion string `json:"extraction_schema_version"`
	ModelConfigFingerprint  string `json:"model_config_fingerprint"`
}

type AIBOXMigrationReport struct {
	SourceRevisionsCreated int      `json:"source_revisions_created"`
	SourceRevisionsReused  int      `json:"source_revisions_reused"`
	GenerationsCreated     int      `json:"generations_created"`
	GenerationsReused      int      `json:"generations_reused"`
	BackfillEnqueued       int      `json:"backfill_enqueued"`
	BackfillReady          int      `json:"backfill_ready"`
	Blocked                []string `json:"blocked,omitempty"`
}

type migrationSourceRevisionStore interface {
	UpsertSourceRevision(context.Context, *types.SourceRevision) (*types.SourceRevision, bool, error)
}

type AIBOXMigrationService struct {
	db            *gorm.DB
	knowledgeRepo interfaces.KnowledgeRepository
	chunkRepo     interfaces.ChunkRepository
	temporalRepo  *apprepo.TemporalRepository
	task          interfaces.TaskEnqueuer
}

func NewAIBOXMigrationService(db *gorm.DB, knowledgeRepo interfaces.KnowledgeRepository, chunkRepo interfaces.ChunkRepository, temporalRepo *apprepo.TemporalRepository, task interfaces.TaskEnqueuer) *AIBOXMigrationService {
	return &AIBOXMigrationService{db: db, knowledgeRepo: knowledgeRepo, chunkRepo: chunkRepo, temporalRepo: temporalRepo, task: task}
}

func (s *AIBOXMigrationService) Prepare(ctx context.Context, cfg AIBOXMigrationConfig) (*AIBOXMigrationReport, error) {
	if err := validateMigrationConfig(cfg); err != nil {
		return nil, err
	}
	report := &AIBOXMigrationReport{}
	if err := s.bootstrapSourceRevisions(ctx, report); err != nil {
		return report, err
	}
	if err := s.ensureInitialGenerations(ctx, cfg, report); err != nil {
		return report, err
	}
	if err := s.enqueueTemporalBackfill(ctx, report); err != nil {
		return report, err
	}
	sort.Strings(report.Blocked)
	return report, nil
}

func (s *AIBOXMigrationService) Preflight(ctx context.Context, cfg AIBOXMigrationConfig) (*AIBOXMigrationReport, error) {
	if err := validateMigrationConfig(cfg); err != nil {
		return nil, err
	}
	report := &AIBOXMigrationReport{}
	var kbs []types.KnowledgeBase
	if err := s.db.WithContext(ctx).Where("is_temporary = ?", false).Find(&kbs).Error; err != nil {
		return nil, err
	}
	for i := range kbs {
		kb := &kbs[i]
		generation, err := s.temporalRepo.GetActiveGeneration(ctx, kb.TenantID, kb.ID)
		if err != nil {
			return report, err
		}
		if generation == nil {
			report.Blocked = append(report.Blocked, "kb:"+kb.ID+":missing-active-generation")
			continue
		}
		if !generationMatchesConfig(generation, cfg) {
			report.Blocked = append(report.Blocked, "kb:"+kb.ID+":generation-config-mismatch")
			continue
		}
		rows, err := s.eligibleKnowledge(ctx, kb.TenantID, kb.ID)
		if err != nil {
			return report, err
		}
		for _, knowledge := range rows {
			revision, err := s.temporalRepo.GetCurrentSourceRevision(ctx, knowledge.ID)
			if err != nil {
				return report, err
			}
			if revision == nil {
				report.Blocked = append(report.Blocked, "knowledge:"+knowledge.ID+":missing-current-revision")
				continue
			}
			ready, err := s.temporalReady(ctx, knowledge, revision, generation)
			if err != nil {
				return report, err
			}
			if ready {
				report.BackfillReady++
			} else {
				report.Blocked = append(report.Blocked, "knowledge:"+knowledge.ID+":temporal-backfill-incomplete")
			}
		}
	}
	sort.Strings(report.Blocked)
	return report, nil
}

func validateMigrationConfig(cfg AIBOXMigrationConfig) error {
	if strings.TrimSpace(cfg.GraphitiCommit) == "" || strings.TrimSpace(cfg.ExtractionSchemaVersion) == "" || strings.TrimSpace(cfg.ModelConfigFingerprint) == "" {
		return errors.New("migration requires graphiti commit, extraction schema version and model config fingerprint")
	}
	return nil
}

func (s *AIBOXMigrationService) bootstrapSourceRevisions(ctx context.Context, report *AIBOXMigrationReport) error {
	store, ok := s.knowledgeRepo.(migrationSourceRevisionStore)
	if !ok {
		return errors.New("knowledge repository does not support SourceRevision bootstrap")
	}
	var rows []types.Knowledge
	if err := s.db.WithContext(ctx).Find(&rows).Error; err != nil {
		return err
	}
	for i := range rows {
		knowledge := &rows[i]
		current, err := s.temporalRepo.GetCurrentSourceRevision(ctx, knowledge.ID)
		if err != nil {
			return err
		}
		if current != nil {
			report.SourceRevisionsReused++
			continue
		}
		candidate, err := s.bootstrapRevisionCandidate(ctx, knowledge)
		if err != nil {
			report.Blocked = append(report.Blocked, "knowledge:"+knowledge.ID+":"+err.Error())
			continue
		}
		_, created, err := store.UpsertSourceRevision(ctx, candidate)
		if err != nil {
			return err
		}
		if created {
			report.SourceRevisionsCreated++
		} else {
			report.SourceRevisionsReused++
		}
	}
	return nil
}

func (s *AIBOXMigrationService) bootstrapRevisionCandidate(ctx context.Context, k *types.Knowledge) (*types.SourceRevision, error) {
	if k == nil || strings.TrimSpace(k.ID) == "" {
		return nil, errors.New("invalid-knowledge")
	}
	meta := k.GetMetadata()
	candidate := &types.SourceRevision{KnowledgeID: k.ID, SourceHash: strings.TrimSpace(k.FileHash), NativeVersion: strings.TrimSpace(meta["native_version"]), ETag: strings.TrimSpace(meta["etag"]), Size: k.FileSize, Mime: strings.TrimSpace(meta["content_type"])}
	if candidate.Mime == "" {
		candidate.Mime = strings.TrimSpace(k.FileType)
	}
	if raw := strings.TrimSpace(meta["source_updated_at"]); raw != "" {
		if t, err := time.Parse(time.RFC3339, raw); err == nil {
			u := t.UTC()
			candidate.SourceUpdatedAt = &u
		}
	}
	if ref := strings.TrimSpace(meta["resource_ref"]); durableMigrationResourceRef(ref) {
		candidate.ResourceRef = ref
	} else if durableMigrationResourceRef(k.FilePath) {
		candidate.ResourceRef = strings.TrimSpace(k.FilePath)
	}
	externalID := strings.TrimSpace(meta["external_id"])
	dataSourceID := strings.TrimSpace(meta["datasource_id"])
	switch {
	case strings.TrimSpace(k.Source) != "" && strings.TrimSpace(k.Source) != "manual":
		candidate.Locator = strings.TrimSpace(k.Source)
	case externalID != "" && dataSourceID != "":
		candidate.Locator = "datasource:" + dataSourceID + ":" + externalID
	case externalID != "":
		candidate.Locator = "external:" + externalID
	case strings.TrimSpace(k.FileName) != "":
		candidate.Locator = strings.TrimSpace(k.FileName)
	}
	if candidate.SourceHash == "" && k.IsManual() {
		if manual, err := k.ManualMetadata(); err == nil && manual != nil && strings.TrimSpace(manual.Content) != "" {
			candidate.SourceHash = sha256Hex([]byte(manual.Content))
			candidate.Locator = "manual:" + k.ID
		}
	}
	if candidate.SourceHash == "" && k.Type == "passage" {
		chunks, err := s.chunkRepo.ListChunksByKnowledgeIDAndTypes(ctx, k.TenantID, k.ID, []types.ChunkType{types.ChunkTypeText})
		if err != nil {
			return nil, fmt.Errorf("passage-bootstrap-read-failed")
		}
		if len(chunks) > 0 {
			var b strings.Builder
			for _, c := range chunks {
				b.WriteString(c.Content)
				b.WriteByte('\n')
			}
			candidate.SourceHash = sha256Hex([]byte(b.String()))
			candidate.Locator = "passage:" + k.ID
		}
	}
	if candidate.SourceHash == "" && candidate.NativeVersion == "" && candidate.ETag == "" && candidate.SourceUpdatedAt == nil && candidate.Locator == "" {
		return nil, errors.New("insufficient-source-identity")
	}
	observed := k.UpdatedAt.UTC()
	if observed.IsZero() {
		observed = time.Now().UTC()
	}
	candidate.ObservedAt = observed
	return candidate, nil
}

func durableMigrationResourceRef(ref string) bool {
	ref = strings.TrimSpace(ref)
	return strings.HasPrefix(ref, "resource://") || strings.HasPrefix(ref, "storage://") || strings.HasPrefix(ref, "p16://") || strings.HasPrefix(ref, "asset://")
}
func sha256Hex(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

func initialGenerationIdentity(tenantID uint64, kbID string) (string, string) {
	sum := sha256.Sum256([]byte(kbID))
	hash24 := hex.EncodeToString(sum[:])[:24]
	group := fmt.Sprintf("aibox_t%d_kb_%s_g_1", tenantID, hash24)
	name := fmt.Sprintf("urn:tevriq:aibox:temporal-generation:v1\ntenant=%d\nkb=%s\nseq=1", tenantID, kbID)
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(name)).String(), group
}

func generationMatchesConfig(g *types.TemporalGraphGeneration, cfg AIBOXMigrationConfig) bool {
	return g != nil && g.GraphitiCommit == cfg.GraphitiCommit && g.ExtractionSchemaVersion == cfg.ExtractionSchemaVersion && g.ModelConfigFingerprint == cfg.ModelConfigFingerprint
}

func (s *AIBOXMigrationService) ensureInitialGenerations(ctx context.Context, cfg AIBOXMigrationConfig, report *AIBOXMigrationReport) error {
	var kbs []types.KnowledgeBase
	if err := s.db.WithContext(ctx).Where("is_temporary = ?", false).Find(&kbs).Error; err != nil {
		return err
	}
	for i := range kbs {
		kb := &kbs[i]
		active, err := s.temporalRepo.GetActiveGeneration(ctx, kb.TenantID, kb.ID)
		if err != nil {
			return err
		}
		if active != nil {
			if !generationMatchesConfig(active, cfg) {
				report.Blocked = append(report.Blocked, "kb:"+kb.ID+":active-generation-config-mismatch")
			} else {
				report.GenerationsReused++
			}
			continue
		}
		generationID, groupID := initialGenerationIdentity(kb.TenantID, kb.ID)
		var existing types.TemporalGraphGeneration
		err = s.db.WithContext(ctx).First(&existing, "generation_id = ?", generationID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			g := &types.TemporalGraphGeneration{GenerationID: generationID, TenantID: kb.TenantID, KnowledgeBaseID: kb.ID, GroupID: groupID, GraphitiCommit: cfg.GraphitiCommit, ExtractionSchemaVersion: cfg.ExtractionSchemaVersion, ModelConfigFingerprint: cfg.ModelConfigFingerprint, Status: types.TemporalGenerationBuilding, CreatedAt: time.Now().UTC()}
			if err = s.temporalRepo.CreateGeneration(ctx, g); err != nil {
				return err
			}
			report.GenerationsCreated++
		} else if err != nil {
			return err
		} else if !generationMatchesConfig(&existing, cfg) {
			report.Blocked = append(report.Blocked, "kb:"+kb.ID+":bootstrap-generation-config-mismatch")
			continue
		} else {
			report.GenerationsReused++
		}
		if err := s.temporalRepo.ActivateGeneration(ctx, kb.TenantID, kb.ID, generationID); err != nil {
			return err
		}
	}
	return nil
}

func (s *AIBOXMigrationService) eligibleKnowledge(ctx context.Context, tenantID uint64, kbID string) ([]*types.Knowledge, error) {
	var rows []*types.Knowledge
	err := s.db.WithContext(ctx).Where("tenant_id = ? AND knowledge_base_id = ? AND enable_status = ? AND parse_status IN ?", tenantID, kbID, "enabled", []string{types.ParseStatusCompleted, types.ParseStatusFinalizing}).Order("created_at ASC").Find(&rows).Error
	return rows, err
}

func (s *AIBOXMigrationService) temporalReady(ctx context.Context, k *types.Knowledge, rev *types.SourceRevision, g *types.TemporalGraphGeneration) (bool, error) {
	chunks, err := s.chunkRepo.ListChunksByKnowledgeIDAndTypes(ctx, k.TenantID, k.ID, []types.ChunkType{types.ChunkTypeText, types.ChunkTypeImageOCR, types.ChunkTypeImageCaption})
	if err != nil {
		return false, err
	}
	windows, err := buildTemporalEvidenceWindows(chunks, defaultTemporalWindowChars)
	if err != nil {
		return false, err
	}
	if len(windows) == 0 {
		return true, nil
	}
	for _, w := range windows {
		episode, err := deterministicTemporalEpisodeUUID(k.ID, rev.RevisionID, g.GenerationID, w.Ordinal)
		if err != nil {
			return false, err
		}
		binding, err := s.temporalRepo.GetEpisodeBinding(ctx, g.GenerationID, episode)
		if err != nil {
			return false, err
		}
		if binding == nil || binding.TemporalStatus != types.TemporalStatusReady {
			return false, nil
		}
	}
	return true, nil
}

func (s *AIBOXMigrationService) enqueueTemporalBackfill(ctx context.Context, report *AIBOXMigrationReport) error {
	if s.task == nil {
		return errors.New("migration temporal backfill requires task enqueuer")
	}
	var kbs []types.KnowledgeBase
	if err := s.db.WithContext(ctx).Where("is_temporary = ?", false).Find(&kbs).Error; err != nil {
		return err
	}
	for i := range kbs {
		kb := &kbs[i]
		generation, err := s.temporalRepo.GetActiveGeneration(ctx, kb.TenantID, kb.ID)
		if err != nil {
			return err
		}
		if generation == nil {
			continue
		}
		rows, err := s.eligibleKnowledge(ctx, kb.TenantID, kb.ID)
		if err != nil {
			return err
		}
		for _, k := range rows {
			rev, err := s.temporalRepo.GetCurrentSourceRevision(ctx, k.ID)
			if err != nil {
				return err
			}
			if rev == nil {
				continue
			}
			ready, err := s.temporalReady(ctx, k, rev, generation)
			if err != nil {
				return err
			}
			if ready {
				report.BackfillReady++
				continue
			}
			payload, _ := json.Marshal(types.TemporalEnrichmentPayload{TenantID: k.TenantID, KnowledgeBaseID: k.KnowledgeBaseID, KnowledgeID: k.ID})
			task := asynq.NewTask(types.TypeTemporalEnrichment, payload, asynq.Queue(types.QueueTemporal), asynq.MaxRetry(3), asynq.Timeout(30*time.Minute))
			_, err = s.task.Enqueue(task, asynq.Unique(30*time.Minute))
			if errors.Is(err, asynq.ErrDuplicateTask) {
				continue
			}
			if err != nil {
				return err
			}
			report.BackfillEnqueued++
		}
	}
	return nil
}

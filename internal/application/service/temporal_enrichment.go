package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
)

const temporalAdapterURLEnv = "WEKNORA_TEMPORAL_ADAPTER_URL"

type TemporalEnrichmentService struct {
	knowledgeRepo interfaces.KnowledgeRepository
	chunkRepo     interfaces.ChunkRepository
	temporalRepo  *apprepo.TemporalRepository
	tracker       SpanTracker
}

func NewTemporalEnrichmentService(
	knowledgeRepo interfaces.KnowledgeRepository,
	chunkRepo interfaces.ChunkRepository,
	temporalRepo *apprepo.TemporalRepository,
	tracker SpanTracker,
) interfaces.TaskHandler {
	return &TemporalEnrichmentService{
		knowledgeRepo: knowledgeRepo,
		chunkRepo:     chunkRepo,
		temporalRepo:  temporalRepo,
		tracker:       tracker,
	}
}

func (s *TemporalEnrichmentService) Handle(ctx context.Context, task *asynq.Task) (retErr error) {
	var payload types.TemporalEnrichmentPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("unmarshal temporal enrichment payload: %w", err)
	}
	ctx = types.WithExecutionTenant(ctx, payload.TenantID)
	if attemptSuperseded(ctx, s.tracker, payload.KnowledgeID, payload.Attempt) {
		logger.Infof(ctx, "temporal: attempt %d superseded for %s, skipping stale enrichment", payload.Attempt, payload.KnowledgeID)
		return nil
	}
	defer func() {
		finalizeSubtaskDetached(ctx, s.knowledgeRepo, payload.KnowledgeID, "temporal",
			retErr, false, isFinalAsynqAttempt(ctx))
	}()

	generation, err := s.temporalRepo.GetActiveGeneration(ctx, payload.TenantID, payload.KnowledgeBaseID)
	if err != nil {
		return fmt.Errorf("get active temporal generation: %w", err)
	}
	if generation == nil {
		logger.Warnf(ctx, "temporal: no active generation for kb=%s; leaving temporal state degraded", payload.KnowledgeBaseID)
		return nil
	}
	revision, err := s.temporalRepo.GetCurrentSourceRevision(ctx, payload.KnowledgeID)
	if err != nil {
		return fmt.Errorf("get current source revision: %w", err)
	}
	if revision == nil {
		logger.Warnf(ctx, "temporal: no current SourceRevision for knowledge=%s; leaving temporal state degraded", payload.KnowledgeID)
		return nil
	}
	chunks, err := s.chunkRepo.ListChunksByKnowledgeIDAndTypes(ctx, payload.TenantID, payload.KnowledgeID,
		[]types.ChunkType{types.ChunkTypeText, types.ChunkTypeImageOCR, types.ChunkTypeImageCaption})
	if err != nil {
		return fmt.Errorf("list temporal evidence chunks: %w", err)
	}
	windows, err := buildTemporalEvidenceWindows(chunks, defaultTemporalWindowChars)
	if err != nil {
		return fmt.Errorf("build temporal evidence windows: %w", err)
	}
	if len(windows) == 0 {
		logger.Infof(ctx, "temporal: no evidence windows for knowledge=%s", payload.KnowledgeID)
		return nil
	}
	factService, err := NewTemporalFactService(os.Getenv(temporalAdapterURLEnv), nil)
	if err != nil {
		return err
	}
	retryCount, _ := asynq.GetRetryCount(ctx)
	temporalAttempt := retryCount + 1
	finalAttempt := isFinalAsynqAttempt(ctx)
	previousEpisodeUUIDs := make([]string, 0, len(windows))
	referenceTime := temporalReferenceTime(revision)
	authorityKey := "knowledge:" + payload.KnowledgeID

	for _, window := range windows {
		episodeUUID, err := deterministicTemporalEpisodeUUID(payload.KnowledgeID, revision.RevisionID, generation.GenerationID, window.Ordinal)
		if err != nil {
			return err
		}
		existing, err := s.temporalRepo.GetEpisodeBinding(ctx, generation.GenerationID, episodeUUID)
		if err != nil {
			return fmt.Errorf("get temporal episode binding: %w", err)
		}
		if existing != nil && existing.TemporalStatus == types.TemporalStatusReady {
			previousEpisodeUUIDs = append(previousEpisodeUUIDs, episodeUUID)
			continue
		}
		ordinal := window.Ordinal
		binding := existing
		if binding == nil {
			binding = &types.TemporalEpisodeBinding{
				EpisodeUUID:             episodeUUID,
				GenerationID:            generation.GenerationID,
				GroupID:                 generation.GroupID,
				KnowledgeID:             payload.KnowledgeID,
				SourceRevisionID:        revision.RevisionID,
				WindowOrdinal:           &ordinal,
				ReferenceTime:           referenceTime,
				SourceKind:              "text",
				AuthorityKey:            authorityKey,
				EvidenceSnapshot:        window.Content,
				EvidenceLocatorJSON:     window.LocatorJSON,
				SnapshotHash:            window.SnapshotHash,
				ExtractionSchemaVersion: generation.ExtractionSchemaVersion,
				ModelConfigFingerprint:  generation.ModelConfigFingerprint,
				CreatedAt:               time.Now().UTC(),
			}
		}
		binding.TemporalStatus = types.TemporalStatusRunning
		binding.TemporalAttempt = temporalAttempt
		binding.LastError = ""
		if err := s.temporalRepo.UpsertEpisodeBinding(ctx, binding); err != nil {
			return fmt.Errorf("persist running temporal binding: %w", err)
		}

		sourceDescription := strings.TrimSpace(revision.Locator)
		if sourceDescription == "" {
			sourceDescription = authorityKey
		}
		result, err := factService.UpsertEpisode(ctx, TemporalEpisodeRequest{
			EpisodeUUID:          episodeUUID,
			GroupID:              generation.GroupID,
			Name:                 fmt.Sprintf("%s/window-%d", payload.KnowledgeID, window.Ordinal),
			Content:              window.Content,
			SourceType:           "text",
			SourceDescription:    sourceDescription,
			ReferenceTime:        referenceTime,
			PreviousEpisodeUUIDs: append([]string(nil), previousEpisodeUUIDs...),
			AuthorityKey:         authorityKey,
			GenerationID:         generation.GenerationID,
		})
		if err != nil {
			binding.TemporalAttempt = temporalAttempt
			binding.LastError = err.Error()
			if finalAttempt {
				binding.TemporalStatus = types.TemporalStatusFailed
			} else {
				binding.TemporalStatus = types.TemporalStatusRunning
			}
			_ = s.temporalRepo.UpsertEpisodeBinding(context.WithoutCancel(ctx), binding)
			return fmt.Errorf("upsert temporal episode %s: %w", episodeUUID, err)
		}

		now := time.Now().UTC()
		transitions := make([]types.TemporalTransitionBinding, 0)
		for oldFactUUID, newFactUUIDs := range result.InvalidatedBy {
			for _, newFactUUID := range newFactUUIDs {
				transitions = append(transitions, types.TemporalTransitionBinding{
					GenerationID:       generation.GenerationID,
					OldFactUUID:        oldFactUUID,
					NewFactUUID:        newFactUUID,
					CausingEpisodeUUID: episodeUUID,
					TransitionType:     "invalidated",
					CreatedAt:          now,
				})
			}
		}
		// Transition provenance must become durable before the binding is marked
		// ready. If this write fails, the binding remains running; a retry uses
		// the deterministic Episode UUID and Graphiti's Episode receipt to rebuild
		// these rows without re-running extraction.
		if err := s.temporalRepo.InsertTransitions(ctx, transitions); err != nil {
			return fmt.Errorf("persist temporal transitions: %w", err)
		}
		binding.TemporalStatus = types.TemporalStatusReady
		binding.TemporalAttempt = temporalAttempt
		binding.CompletedAt = &now
		binding.LastError = ""
		if err := s.temporalRepo.UpsertEpisodeBinding(ctx, binding); err != nil {
			return fmt.Errorf("persist ready temporal binding: %w", err)
		}
		previousEpisodeUUIDs = append(previousEpisodeUUIDs, episodeUUID)
	}
	return nil
}

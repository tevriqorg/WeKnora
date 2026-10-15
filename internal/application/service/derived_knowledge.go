package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const derivedKnowledgeChannel = "aibox_derived"

type derivedKnowledgeWriter interface {
	CreateKnowledgeFromManual(context.Context, string, *types.ManualKnowledgePayload, string) (*types.Knowledge, error)
	UpdateManualKnowledge(context.Context, string, *types.ManualKnowledgePayload) (*types.Knowledge, error)
	GetKnowledgeByID(context.Context, string) (*types.Knowledge, error)
}

type derivedSourceRevisionStore interface {
	UpsertSourceRevision(context.Context, *types.SourceRevision) (*types.SourceRevision, bool, error)
}

type DerivedKnowledgeEnvelopeService struct {
	knowledge derivedKnowledgeWriter
	revisions derivedSourceRevisionStore
}

func NewDerivedKnowledgeEnvelopeService(knowledge interfaces.KnowledgeService) *DerivedKnowledgeEnvelopeService {
	var revisions derivedSourceRevisionStore
	if knowledge != nil {
		revisions, _ = knowledge.GetRepository().(derivedSourceRevisionStore)
	}
	return &DerivedKnowledgeEnvelopeService{knowledge: knowledge, revisions: revisions}
}

func newDerivedKnowledgeEnvelopeService(writer derivedKnowledgeWriter, revisions derivedSourceRevisionStore) *DerivedKnowledgeEnvelopeService {
	return &DerivedKnowledgeEnvelopeService{knowledge: writer, revisions: revisions}
}

func (s *DerivedKnowledgeEnvelopeService) Upsert(ctx context.Context, kbID string, envelope *types.DerivedKnowledgeEnvelope) (*types.Knowledge, error) {
	if s == nil || s.knowledge == nil || s.revisions == nil {
		return nil, errors.New("derived knowledge ingestion is not configured")
	}
	kbID = strings.TrimSpace(kbID)
	if kbID == "" {
		return nil, errors.New("knowledge base id is required")
	}
	meta, err := envelope.Metadata()
	if err != nil {
		return nil, err
	}
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		return nil, errors.New("workspace ID not found in context")
	}
	knowledgeID := types.DerivedKnowledgeID(tenantID, kbID, meta.IdempotencyKey)
	payload := &types.ManualKnowledgePayload{
		Title: envelope.Title, Content: envelope.Content(), Status: types.ManualKnowledgeStatusDraft,
		Channel: derivedKnowledgeChannel, RequestedKnowledgeID: knowledgeID, DerivedMetadata: meta,
	}

	existing, getErr := s.knowledge.GetKnowledgeByID(ctx, knowledgeID)
	if getErr != nil && !errors.Is(getErr, apprepo.ErrKnowledgeNotFound) {
		return nil, getErr
	}
	created := false
	var stored *types.DerivedKnowledgeMetadata
	if existing == nil || errors.Is(getErr, apprepo.ErrKnowledgeNotFound) {
		existing, err = s.knowledge.CreateKnowledgeFromManual(ctx, kbID, payload, derivedKnowledgeChannel)
		if err != nil {
			return nil, fmt.Errorf("create derived knowledge draft: %w", err)
		}
		created = true
		stored = meta
	} else {
		stored, err = existing.DerivedMetadata()
		if err != nil {
			return nil, err
		}
		if stored == nil || stored.IdempotencyKey != meta.IdempotencyKey || existing.KnowledgeBaseID != kbID {
			return nil, errors.New("deterministic derived knowledge id is already bound to different provenance")
		}
	}

	// Source reality must be durable before publishing work that may reach
	// Temporal Enrichment. The source-specific revision ID remains the native
	// version while WeKnora owns its internal monotonic SourceRevision identity.
	if _, _, err := s.revisions.UpsertSourceRevision(ctx, &types.SourceRevision{
		KnowledgeID: existing.ID, NativeVersion: envelope.SourceRevisionID,
		ObservedAt: envelope.ProducedAt, ResourceRef: envelope.SourceResourceRef,
		Locator: envelope.SourceKey, Mime: "text/markdown",
	}); err != nil {
		return existing, fmt.Errorf("persist derived source revision: %w", err)
	}

	if meta.AnalysisStatus != "ready" {
		if !created && (stored.SnapshotHash != meta.SnapshotHash || existing.ParseStatus == types.ParseStatusFailed) {
			updated, err := s.knowledge.UpdateManualKnowledge(ctx, existing.ID, payload)
			if err != nil {
				return existing, fmt.Errorf("refresh derived knowledge draft: %w", err)
			}
			return updated, nil
		}
		return existing, nil
	}
	if !created && stored.SnapshotHash == meta.SnapshotHash &&
		existing.ParseStatus != types.ManualKnowledgeStatusDraft && existing.ParseStatus != types.ParseStatusFailed {
		return existing, nil
	}
	payload.Status = types.ManualKnowledgeStatusPublish
	updated, err := s.knowledge.UpdateManualKnowledge(ctx, existing.ID, payload)
	if err != nil {
		return existing, fmt.Errorf("publish derived knowledge: %w", err)
	}
	return updated, nil
}

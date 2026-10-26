package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

type manualSourceRevisionStore interface {
	UpsertSourceRevision(context.Context, *types.SourceRevision) (*types.SourceRevision, bool, error)
	RollbackSourceRevision(context.Context, string, string) error
}

func manualSourceRevisionCandidate(knowledgeID, content string, observedAt time.Time) *types.SourceRevision {
	sum := sha256.Sum256([]byte(content))
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	return &types.SourceRevision{
		KnowledgeID: knowledgeID,
		SourceHash:  hex.EncodeToString(sum[:]),
		ObservedAt:  observedAt.UTC(),
		Locator:     "manual:" + knowledgeID,
		Mime:        "manual",
	}
}

func (s *knowledgeService) stageManualSourceRevision(ctx context.Context, knowledge *types.Knowledge, content string) (*types.SourceRevision, bool, error) {
	store, ok := s.repo.(manualSourceRevisionStore)
	if !ok || knowledge == nil {
		return nil, false, nil
	}
	// Derived text is a projection, not a new raw source. Its native revision
	// was persisted by DerivedKnowledgeEnvelopeService before publication.
	// Hashing this Markdown would replace source provenance with "manual:" and
	// create a false reality revision on every processor refresh.
	meta, err := knowledge.DerivedMetadata()
	if err != nil {
		return nil, false, err
	}
	if meta != nil {
		return nil, false, nil
	}
	return store.UpsertSourceRevision(ctx, manualSourceRevisionCandidate(knowledge.ID, content, knowledge.UpdatedAt))
}

func (s *knowledgeService) rollbackManualSourceRevision(ctx context.Context, knowledgeID string, revision *types.SourceRevision, created bool) {
	if !created || revision == nil {
		return
	}
	if store, ok := s.repo.(manualSourceRevisionStore); ok {
		_ = store.RollbackSourceRevision(context.WithoutCancel(ctx), knowledgeID, revision.RevisionID)
	}
}

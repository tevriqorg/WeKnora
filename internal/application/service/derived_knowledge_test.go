package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type derivedWriterFixture struct {
	rows             map[string]*types.Knowledge
	creates, updates int
	lastPayload      *types.ManualKnowledgePayload
	updateErr        error
}

func newDerivedWriterFixture() *derivedWriterFixture {
	return &derivedWriterFixture{rows: map[string]*types.Knowledge{}}
}
func (f *derivedWriterFixture) GetKnowledgeByID(_ context.Context, id string) (*types.Knowledge, error) {
	if row := f.rows[id]; row != nil {
		return row, nil
	}
	return nil, apprepo.ErrKnowledgeNotFound
}
func (f *derivedWriterFixture) CreateKnowledgeFromManual(_ context.Context, kbID string, p *types.ManualKnowledgePayload, _ string) (*types.Knowledge, error) {
	f.creates++
	f.lastPayload = p
	k := &types.Knowledge{ID: p.RequestedKnowledgeID, TenantID: 7, KnowledgeBaseID: kbID, Type: types.KnowledgeTypeManual}
	_ = k.SetManualMetadata(types.NewManualKnowledgeMetadata(p.Content, p.Status, 1))
	_ = k.SetDerivedMetadata(p.DerivedMetadata)
	if p.Status == types.ManualKnowledgeStatusPublish {
		k.ParseStatus = types.ParseStatusPending
	} else {
		k.ParseStatus = types.ManualKnowledgeStatusDraft
	}
	f.rows[k.ID] = k
	return k, nil
}
func (f *derivedWriterFixture) UpdateManualKnowledge(_ context.Context, id string, p *types.ManualKnowledgePayload) (*types.Knowledge, error) {
	f.updates++
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	f.lastPayload = p
	k := f.rows[id]
	_ = k.SetManualMetadata(types.NewManualKnowledgeMetadata(p.Content, p.Status, 2))
	_ = k.SetDerivedMetadata(p.DerivedMetadata)
	if p.Status == types.ManualKnowledgeStatusPublish {
		k.ParseStatus = types.ParseStatusPending
	} else {
		k.ParseStatus = types.ManualKnowledgeStatusDraft
	}
	return k, nil
}

type derivedRevisionFixture struct {
	calls     int
	last      *types.SourceRevision
	history   []*types.SourceRevision
	rollbacks int
}

func (f *derivedRevisionFixture) UpsertSourceRevision(_ context.Context, r *types.SourceRevision) (*types.SourceRevision, bool, error) {
	f.calls++
	copy := *r
	created := f.last == nil || f.last.NativeVersion != copy.NativeVersion
	if created {
		copy.RevisionID = "internal-rev-" + copy.NativeVersion
		f.history = append(f.history, &copy)
	} else {
		copy.RevisionID = f.last.RevisionID
		f.history[len(f.history)-1] = &copy
	}
	f.last = &copy
	return &copy, created, nil
}

func (f *derivedRevisionFixture) RollbackSourceRevision(_ context.Context, knowledgeID, revisionID string) error {
	if f.last == nil || f.last.RevisionID != revisionID || f.last.KnowledgeID != knowledgeID {
		return errors.New("cannot rollback superseded revision")
	}
	f.rollbacks++
	f.history = f.history[:len(f.history)-1]
	f.last = nil
	if len(f.history) != 0 {
		f.last = f.history[len(f.history)-1]
	}
	return nil
}

func derivedEnvelope(status string) *types.DerivedKnowledgeEnvelope {
	return &types.DerivedKnowledgeEnvelope{
		SourceKey: "p16:rec-1", SourceRevisionID: "rev-1", DerivedObjectID: "asset-1", Title: "clip",
		Markdown: "person enters", SourceResourceRef: "p16://asset/asset-1",
		SourceLocators: types.SourceLocators{{Type: types.SourceLocatorTime, StartMs: 1000, EndMs: 2000, SourceID: "event-1"}},
		Processor:      "p16-home-vision", ProcessorVersion: "consumer-api-v2", Capabilities: []string{"vlm", "timeline"},
		AnalysisStatus: status, ProducedAt: time.Unix(10, 0).UTC(),
	}
}

func TestDerivedEnvelopeReplayReusesSameKnowledge(t *testing.T) {
	writer := newDerivedWriterFixture()
	revisions := &derivedRevisionFixture{}
	svc := newDerivedKnowledgeEnvelopeService(writer, revisions)
	ctx := types.WithExecutionTenant(context.Background(), 7)
	first, err := svc.Upsert(ctx, "kb-1", derivedEnvelope("ready"))
	require.NoError(t, err)
	second, err := svc.Upsert(ctx, "kb-1", derivedEnvelope("ready"))
	require.NoError(t, err)
	require.Equal(t, first.ID, second.ID)
	require.Equal(t, 1, writer.creates)
	require.Equal(t, 1, writer.updates)
	require.Equal(t, types.ManualKnowledgeStatusPublish, writer.lastPayload.Status)
	require.Equal(t, []string{"timeline", "vlm"}, writer.lastPayload.DerivedMetadata.Capabilities)
	require.Equal(t, 2, revisions.calls)
	require.Equal(t, "rev-1", revisions.last.NativeVersion)
	require.Equal(t, "p16://asset/asset-1", revisions.last.ResourceRef)
}

func TestDerivedPendingThenReadyUpdatesSameKnowledge(t *testing.T) {
	writer := newDerivedWriterFixture()
	revisions := &derivedRevisionFixture{}
	svc := newDerivedKnowledgeEnvelopeService(writer, revisions)
	ctx := types.WithExecutionTenant(context.Background(), 7)
	pending := derivedEnvelope("pending")
	first, err := svc.Upsert(ctx, "kb-1", pending)
	require.NoError(t, err)
	require.Equal(t, types.ManualKnowledgeStatusDraft, writer.lastPayload.Status)
	ready := derivedEnvelope("ready")
	ready.Markdown = "person enters; analysis complete"
	second, err := svc.Upsert(ctx, "kb-1", ready)
	require.NoError(t, err)
	require.Equal(t, first.ID, second.ID)
	require.Equal(t, 1, writer.creates)
	require.Equal(t, 1, writer.updates)
	require.Equal(t, types.ManualKnowledgeStatusPublish, writer.lastPayload.Status)
}

func TestApplyDerivedSourceLocatorsCopiesTimeEvidence(t *testing.T) {
	parsed := []types.ParsedChunk{{Content: "a"}, {Content: "b"}}
	parents := []types.ParsedParentChunk{{Content: "p"}}
	locators := types.SourceLocators{{Type: types.SourceLocatorTime, StartMs: 100, EndMs: 200, SourceID: "e1"}}
	applyDerivedSourceLocators(parsed, parents, locators)
	require.Equal(t, int64(100), parsed[0].SourceLocators[0].StartMs)
	require.Equal(t, "e1", parsed[1].SourceLocators[0].SourceID)
	require.Equal(t, int64(200), parents[0].SourceLocators[0].EndMs)
}

// #126: content revisions and processor upgrades must not change source identity.
func TestDerivedSourceRevisionAndProcessorKeepStableKnowledge(t *testing.T) {
	writer := newDerivedWriterFixture()
	revisions := &derivedRevisionFixture{}
	svc := newDerivedKnowledgeEnvelopeService(writer, revisions)
	ctx := types.WithExecutionTenant(context.Background(), 7)
	first, err := svc.Upsert(ctx, "kb-1", derivedEnvelope("ready"))
	require.NoError(t, err)
	firstID := first.ID
	changed := derivedEnvelope("ready")
	changed.SourceRevisionID = "rev-2"
	changed.Markdown = "person leaves"
	second, err := svc.Upsert(ctx, "kb-1", changed)
	require.NoError(t, err)
	require.Equal(t, firstID, second.ID, "source content update created a different Knowledge")
	upgraded := derivedEnvelope("ready")
	upgraded.SourceRevisionID = "rev-2"
	upgraded.ProcessorVersion = "consumer-api-v3"
	third, err := svc.Upsert(ctx, "kb-1", upgraded)
	require.NoError(t, err)
	require.Equal(t, firstID, third.ID, "processor upgrade created a different Knowledge")
	require.Equal(t, 1, writer.creates)
}

func TestDerivedDistinctSourcesDoNotAliasSameRevision(t *testing.T) {
	writer := newDerivedWriterFixture()
	svc := newDerivedKnowledgeEnvelopeService(writer, &derivedRevisionFixture{})
	ctx := types.WithExecutionTenant(context.Background(), 7)
	first, err := svc.Upsert(ctx, "kb-1", derivedEnvelope("ready"))
	require.NoError(t, err)
	other := derivedEnvelope("ready")
	other.SourceKey = "public:another-source"
	second, err := svc.Upsert(ctx, "kb-1", other)
	require.NoError(t, err)
	require.NotEqual(t, first.ID, second.ID, "distinct sources with identical revision labels were merged")
}

// #126 Codex finding 4365682879: a rejected revision must not replace the
// currently published source reality (or enqueue an update for stale chunks).
func TestDerivedRejectedOversizeRevisionKeepsCurrentSource(t *testing.T) {
	writer := newDerivedWriterFixture()
	revisions := &derivedRevisionFixture{}
	svc := newDerivedKnowledgeEnvelopeService(writer, revisions)
	ctx := types.WithExecutionTenant(context.Background(), 7)
	original, err := svc.Upsert(ctx, "kb-1", derivedEnvelope("ready"))
	require.NoError(t, err)
	originalMeta, err := original.DerivedMetadata()
	require.NoError(t, err)
	require.Equal(t, "rev-1", revisions.last.NativeVersion)

	rejected := derivedEnvelope("ready")
	rejected.SourceRevisionID = "rev-2"
	rejected.Markdown = strings.Repeat("x", manualContentMaxLength+1)
	_, err = svc.Upsert(ctx, "kb-1", rejected)
	require.Error(t, err, "oversize update must be rejected")
	require.Equal(t, 1, revisions.calls, "rejected revision must not become current")
	require.Equal(t, "rev-1", revisions.last.NativeVersion)
	require.Equal(t, 1, writer.updates, "rejected revision must not publish")
	stored, err := writer.rows[original.ID].DerivedMetadata()
	require.NoError(t, err)
	require.Equal(t, originalMeta.SnapshotHash, stored.SnapshotHash)
}

// #126 Codex finding 4365682879: a downstream publication failure after
// successful preflight must restore the previous current SourceRevision.
func TestDerivedFailedPublicationRollsBackSourceRevision(t *testing.T) {
	writer := newDerivedWriterFixture()
	revisions := &derivedRevisionFixture{}
	svc := newDerivedKnowledgeEnvelopeService(writer, revisions)
	ctx := types.WithExecutionTenant(context.Background(), 7)
	_, err := svc.Upsert(ctx, "kb-1", derivedEnvelope("ready"))
	require.NoError(t, err)
	require.Equal(t, "rev-1", revisions.last.NativeVersion)

	writer.updateErr = errors.New("injected downstream publish failure")
	failed := derivedEnvelope("ready")
	failed.SourceRevisionID = "rev-2"
	failed.Markdown = "new content"
	_, err = svc.Upsert(ctx, "kb-1", failed)
	require.ErrorContains(t, err, "injected downstream publish failure")
	require.Equal(t, "rev-1", revisions.last.NativeVersion,
		"failed publish must not leave v2 as current source reality")
	require.Equal(t, 1, revisions.rollbacks, "publication failure must invoke durable rollback")
}

package service

import (
	"context"
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
	calls int
	last  *types.SourceRevision
}

func (f *derivedRevisionFixture) UpsertSourceRevision(_ context.Context, r *types.SourceRevision) (*types.SourceRevision, bool, error) {
	f.calls++
	copy := *r
	if copy.RevisionID == "" {
		copy.RevisionID = "internal-rev"
	}
	f.last = &copy
	return &copy, f.calls == 1, nil
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

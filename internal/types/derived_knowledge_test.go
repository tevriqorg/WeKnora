package types

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDerivedKnowledgeIdempotencyAndIdentity(t *testing.T) {
	key, err := DerivedKnowledgeIdempotencyKey("rev-1", "obj-1", "proc-v1")
	require.NoError(t, err)
	require.Equal(t, "sha256:583c1bfccef02235932ad89f2f4b25d25e3cb11e529293e004708b61f10775a8", key)
	require.Equal(t, DerivedKnowledgeID(7, "kb-1", key), DerivedKnowledgeID(7, "kb-1", key))
	require.NotEqual(t, DerivedKnowledgeID(7, "kb-1", key), DerivedKnowledgeID(7, "kb-2", key))
}

func TestDerivedEnvelopeRejectsTemporaryResourceAndNormalizesCapabilities(t *testing.T) {
	env := &DerivedKnowledgeEnvelope{
		SourceKey: "source", SourceRevisionID: "rev", DerivedObjectID: "obj", Title: "title",
		Markdown: "body", Processor: "processor", ProcessorVersion: "v1",
		Capabilities: []string{"ASR", "asr", "timeline"}, AnalysisStatus: "ready", ProducedAt: time.Unix(1, 0).UTC(),
	}
	require.NoError(t, env.NormalizeAndValidate())
	require.Equal(t, []string{"asr", "timeline"}, env.Capabilities)
	env.SourceResourceRef = "file:///archive/staging/x.part"
	require.Error(t, env.NormalizeAndValidate())
}

func TestDerivedMetadataSurvivesManualMetadataRewrite(t *testing.T) {
	k := &Knowledge{}
	require.NoError(t, k.SetManualMetadata(NewManualKnowledgeMetadata("one", ManualKnowledgeStatusDraft, 1)))
	require.NoError(t, k.SetDerivedMetadata(&DerivedKnowledgeMetadata{IdempotencyKey: "key", SourceRevisionID: "rev"}))
	require.NoError(t, k.SetManualMetadata(NewManualKnowledgeMetadata("two", ManualKnowledgeStatusPublish, 2)))
	meta, err := k.DerivedMetadata()
	require.NoError(t, err)
	require.Equal(t, "key", meta.IdempotencyKey)
	require.Equal(t, "rev", meta.SourceRevisionID)
}

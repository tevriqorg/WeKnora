package router

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestTemporalDeadLetterDoesNotFailSearchReadyKnowledge(t *testing.T) {
	_, affectsDocumentStatus := taskTypesAffectingKnowledgeStatus[types.TypeTemporalEnrichment]
	require.False(t, affectsDocumentStatus)
}

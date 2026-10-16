package mcpserver

import (
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestAskToolKeepsSingleSurfaceAndExposesTemporalControls(t *testing.T) {
	tool := askTool()
	for _, name := range []string{"temporal_mode", "valid_time", "known_time"} {
		_, ok := tool.InputSchema.Properties[name]
		require.True(t, ok, name)
	}
	require.Equal(t, types.MCPEndpointToolAsk, tool.Name)
}

func TestAskTemporalDegradedMessageFailsClosed(t *testing.T) {
	msg := temporalUnavailableAnswer(&types.AIBOXQueryResult{TemporalRequired: true, TemporalDegraded: true, DegradedReasons: []string{"Graphiti unavailable"}})
	require.Contains(t, msg, "cannot safely answer")
	require.Contains(t, msg, "Graphiti unavailable")
}

func TestRenderAIBOXQueryContextPreservesConflict(t *testing.T) {
	now := time.Now().UTC()
	result := &types.AIBOXQueryResult{
		EffectiveMode: types.AIBOXQueryModeCurrent, TemporalRequired: true, EvaluatedAt: now,
		TemporalFacts: []types.AIBOXTemporalEvidence{{FactUUID: "f1", Fact: "Alice works at Acme", Relation: "works_at", AuthorityKeys: []string{"official"}, Episodes: []string{"ep1"}, CreatedAt: now, Source: types.AIBOXTemporalEntity{Name: "Alice"}, Target: types.AIBOXTemporalEntity{Name: "Acme"}}},
		Conflicts:     []types.AIBOXConflict{{Key: "g|alice|works_at", Claims: []types.AIBOXTemporalEvidence{{FactUUID: "f1"}, {FactUUID: "f2"}}}},
	}
	text := renderAIBOXQueryContext("Where does Alice work now?", result)
	require.Contains(t, text, "Original question")
	require.Contains(t, text, "fact_uuid=f1")
	require.Contains(t, text, "Conflicts (do not collapse)")
	require.Contains(t, strings.ToLower(text), "never choose one conflicting authority")
}

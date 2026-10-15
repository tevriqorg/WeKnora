package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTemporalEnrichmentUsesExistingEnrichmentPool(t *testing.T) {
	queue, ok := QueueForTaskType(TypeTemporalEnrichment)
	require.True(t, ok)
	require.Equal(t, QueueTemporal, queue)
	found := false
	for _, definition := range QueueDefinitions() {
		if definition.Name == QueueTemporal {
			found = true
			require.Equal(t, WorkerPoolEnrichment, definition.Pool)
			require.Equal(t, 1, definition.SharedWeight)
		}
	}
	require.True(t, found)
}

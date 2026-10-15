package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTemporalEnrichmentFeatureFlagDefaultsOff(t *testing.T) {
	t.Setenv("WEKNORA_TEMPORAL_ENABLED", "")
	require.False(t, temporalEnrichmentEnabled())
	t.Setenv("WEKNORA_TEMPORAL_ENABLED", "true")
	require.True(t, temporalEnrichmentEnabled())
}

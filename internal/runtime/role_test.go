package runtime

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestRuntimeRoleDefaultPreservesAllPools(t *testing.T) {
	cfg, err := ParseRuntimeConfig("", "")
	require.NoError(t, err)
	require.Equal(t, RoleAll, cfg.Role)
	require.True(t, cfg.RunsAPISurface())
	require.True(t, cfg.RunsCoordinator())
	require.True(t, cfg.RunsWorkers())
	require.False(t, cfg.RequiresRedis())
	for _, pool := range allWorkerPools {
		require.True(t, cfg.WorkerPoolEnabled(pool))
	}
}

func TestRuntimeRoleAPIHasNoWorkers(t *testing.T) {
	cfg, err := ParseRuntimeConfig("api", "")
	require.NoError(t, err)
	require.True(t, cfg.RunsAPISurface())
	require.True(t, cfg.RunsCoordinator())
	require.False(t, cfg.RunsWorkers())
	require.True(t, cfg.RequiresRedis())
	require.Empty(t, cfg.WorkerPools)
}

func TestRuntimeRoleWorkerSelectsOnlyRequestedPools(t *testing.T) {
	cfg, err := ParseRuntimeConfig("worker", "core, enrichment")
	require.NoError(t, err)
	require.False(t, cfg.RunsAPISurface())
	require.False(t, cfg.RunsCoordinator())
	require.True(t, cfg.RunsWorkers())
	require.True(t, cfg.RequiresRedis())
	require.True(t, cfg.WorkerPoolEnabled(types.WorkerPoolCore))
	require.True(t, cfg.WorkerPoolEnabled(types.WorkerPoolEnrichment))
	require.False(t, cfg.WorkerPoolEnabled(types.WorkerPoolMaintenance))
}

func TestRuntimeRoleRejectsInvalidConfiguration(t *testing.T) {
	cases := [][2]string{
		{"other", ""},
		{"worker", ""},
		{"worker", "core,unknown"},
		{"worker", "core,core"},
		{"api", "core"},
		{"all", "core"},
	}
	for _, tc := range cases {
		_, err := ParseRuntimeConfig(tc[0], tc[1])
		require.Error(t, err, "%q / %q must fail", tc[0], tc[1])
	}
}

package runtime

import (
	"fmt"
	"os"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

type Role string

const (
	RoleAll    Role = "all"
	RoleAPI    Role = "api"
	RoleWorker Role = "worker"

	RuntimeRoleEnv = "WEKNORA_RUNTIME_ROLE"
	WorkerPoolsEnv = "WEKNORA_WORKER_POOLS"
)

type RuntimeConfig struct {
	Role        Role
	WorkerPools []string
}

var allWorkerPools = []string{
	types.WorkerPoolCore,
	types.WorkerPoolPostProcess,
	types.WorkerPoolEnrichment,
	types.WorkerPoolMaintenance,
	types.WorkerPoolShared,
	types.WorkerPoolWiki,
}

func ParseRuntimeConfig(roleRaw, poolsRaw string) (RuntimeConfig, error) {
	role := Role(strings.ToLower(strings.TrimSpace(roleRaw)))
	if role == "" {
		role = RoleAll
	}
	if role != RoleAll && role != RoleAPI && role != RoleWorker {
		return RuntimeConfig{}, fmt.Errorf("invalid runtime role %q", roleRaw)
	}

	poolsRaw = strings.TrimSpace(poolsRaw)
	if role != RoleWorker {
		if poolsRaw != "" {
			return RuntimeConfig{}, fmt.Errorf("%s is only valid with role=worker", WorkerPoolsEnv)
		}
		if role == RoleAll {
			return RuntimeConfig{Role: role, WorkerPools: append([]string(nil), allWorkerPools...)}, nil
		}
		return RuntimeConfig{Role: role}, nil
	}
	if poolsRaw == "" {
		return RuntimeConfig{}, fmt.Errorf("role=worker requires %s", WorkerPoolsEnv)
	}

	allowed := make(map[string]struct{}, len(allWorkerPools))
	for _, name := range allWorkerPools {
		allowed[name] = struct{}{}
	}
	seen := make(map[string]struct{})
	pools := make([]string, 0, len(allWorkerPools))
	for _, raw := range strings.Split(poolsRaw, ",") {
		name := strings.ToLower(strings.TrimSpace(raw))
		if name == "" {
			return RuntimeConfig{}, fmt.Errorf("empty worker pool in %s", WorkerPoolsEnv)
		}
		if _, ok := allowed[name]; !ok {
			return RuntimeConfig{}, fmt.Errorf("unknown worker pool %q", name)
		}
		if _, ok := seen[name]; ok {
			return RuntimeConfig{}, fmt.Errorf("duplicate worker pool %q", name)
		}
		seen[name] = struct{}{}
		pools = append(pools, name)
	}
	return RuntimeConfig{Role: role, WorkerPools: pools}, nil
}

func RuntimeConfigFromEnv() (RuntimeConfig, error) {
	return ParseRuntimeConfig(os.Getenv(RuntimeRoleEnv), os.Getenv(WorkerPoolsEnv))
}

func MustRuntimeConfigFromEnv() RuntimeConfig {
	cfg, err := RuntimeConfigFromEnv()
	if err != nil {
		panic(err)
	}
	return cfg
}

func (c RuntimeConfig) RunsAPISurface() bool  { return c.Role != RoleWorker }
func (c RuntimeConfig) RunsCoordinator() bool { return c.Role != RoleWorker }
func (c RuntimeConfig) RunsWorkers() bool     { return c.Role != RoleAPI }
func (c RuntimeConfig) RequiresRedis() bool   { return c.Role != RoleAll }

func (c RuntimeConfig) WorkerPoolEnabled(name string) bool {
	for _, pool := range c.WorkerPools {
		if pool == name {
			return true
		}
	}
	return false
}

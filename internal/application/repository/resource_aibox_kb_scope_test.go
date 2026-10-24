package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// Regression: tenant-scoped external source registration in KB A must not
// authorize a derived handoff in KB B. A resource handle is not a KB grant.
func TestAIBOXSourceRegistrationRequiresExplicitKBBinding(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:aibox-kb-scope?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.ResourceBinding{}))
	repo := &resourceRepository{db: db}
	ctx := context.Background()
	const tenantID uint64 = 42
	const resourceID = "resource-in-kb-a"
	const kbA = "kb-aaa"
	const kbB = "kb-bbb"

	require.NoError(t, repo.CreateBinding(ctx, &types.ResourceBinding{
		ResourceID: resourceID, TenantID: tenantID, OwnerType: "knowledge_base",
		OwnerID: kbA, Relation: "aibox_source",
	}))
	allowedA, err := repo.IsAIBOXSourceRegisteredForKB(ctx, tenantID, kbA, resourceID)
	require.NoError(t, err)
	require.True(t, allowedA)

	allowedB, err := repo.IsAIBOXSourceRegisteredForKB(ctx, tenantID, kbB, resourceID)
	require.NoError(t, err)
	require.False(t, allowedB, "same-tenant KB B must not inherit KB A's source")

	wrongTenant, err := repo.IsAIBOXSourceRegisteredForKB(ctx, 99, kbA, resourceID)
	require.NoError(t, err)
	require.False(t, wrongTenant)
}

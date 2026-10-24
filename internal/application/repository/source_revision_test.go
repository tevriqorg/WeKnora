package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newSourceRevisionRepoForTest(t *testing.T) (*knowledgeRepository, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.SourceRevision{}))
	return &knowledgeRepository{db: db}, db
}

func TestSourceRevisionMonotonicCurrentAndSameHashRefresh(t *testing.T) {
	repo, db := newSourceRevisionRepoForTest(t)
	ctx := context.Background()
	updated := time.Date(2026, 10, 14, 1, 2, 3, 0, time.UTC)

	first, created, err := repo.UpsertSourceRevision(ctx, &types.SourceRevision{
		KnowledgeID: "kn-1", SourceHash: "hash-a", Locator: "source/a", ResourceRef: "resource://aaaaaaaaaaaaaaaaaaaaaa",
		SourceUpdatedAt: &updated, Size: 10, Mime: "text/plain",
	})
	require.NoError(t, err)
	require.True(t, created)
	require.EqualValues(t, 1, first.RevisionSeq)
	require.True(t, first.IsCurrent)

	refreshed, created, err := repo.UpsertSourceRevision(ctx, &types.SourceRevision{
		KnowledgeID: "kn-1", SourceHash: "hash-a", Locator: "source/renamed", ResourceRef: "resource://bbbbbbbbbbbbbbbbbbbbbb",
		SourceUpdatedAt: &updated, Size: 10, Mime: "text/plain",
	})
	require.NoError(t, err)
	require.False(t, created, "same source hash is not a new reality revision")
	require.Equal(t, first.RevisionID, refreshed.RevisionID)
	require.EqualValues(t, 1, refreshed.RevisionSeq)
	require.Equal(t, "source/renamed", refreshed.Locator)

	second, created, err := repo.UpsertSourceRevision(ctx, &types.SourceRevision{
		KnowledgeID: "kn-1", SourceHash: "hash-b", Locator: "source/renamed", ResourceRef: "resource://cccccccccccccccccccccc",
		Size: 11, Mime: "text/plain",
	})
	require.NoError(t, err)
	require.True(t, created)
	require.EqualValues(t, 2, second.RevisionSeq)

	var currentCount int64
	require.NoError(t, db.Model(&types.SourceRevision{}).Where("knowledge_id = ? AND is_current = ?", "kn-1", true).Count(&currentCount).Error)
	require.EqualValues(t, 1, currentCount)
	var firstStored types.SourceRevision
	require.NoError(t, db.First(&firstStored, "revision_id = ?", first.RevisionID).Error)
	require.False(t, firstStored.IsCurrent)
}

func TestSourceRevisionRollbackRestoresPreviousCurrent(t *testing.T) {
	repo, db := newSourceRevisionRepoForTest(t)
	ctx := context.Background()
	first, _, err := repo.UpsertSourceRevision(ctx, &types.SourceRevision{KnowledgeID: "kn-1", SourceHash: "a"})
	require.NoError(t, err)
	second, _, err := repo.UpsertSourceRevision(ctx, &types.SourceRevision{KnowledgeID: "kn-1", SourceHash: "b"})
	require.NoError(t, err)

	require.NoError(t, repo.RollbackSourceRevision(ctx, "kn-1", second.RevisionID))
	var rows []types.SourceRevision
	require.NoError(t, db.Order("revision_seq").Find(&rows).Error)
	require.Len(t, rows, 1)
	require.Equal(t, first.RevisionID, rows[0].RevisionID)
	require.True(t, rows[0].IsCurrent)
}


// #126: rollback of an obsolete staged revision must never delete history
// or disturb a newer concurrent publication's current identity.
func TestSourceRevisionRollbackRejectsSupersededRevision(t *testing.T) {
	repo, db := newSourceRevisionRepoForTest(t)
	ctx := context.Background()
	_, _, err := repo.UpsertSourceRevision(ctx, &types.SourceRevision{KnowledgeID: "kn-1", SourceHash: "v1"})
	require.NoError(t, err)
	v2, _, err := repo.UpsertSourceRevision(ctx, &types.SourceRevision{KnowledgeID: "kn-1", SourceHash: "v2"})
	require.NoError(t, err)
	v3, _, err := repo.UpsertSourceRevision(ctx, &types.SourceRevision{KnowledgeID: "kn-1", SourceHash: "v3"})
	require.NoError(t, err)

	require.Error(t, repo.RollbackSourceRevision(ctx, "kn-1", v2.RevisionID),
		"obsolete rollback must fail closed rather than erase historical revision")
	var rows []types.SourceRevision
	require.NoError(t, db.Order("revision_seq").Find(&rows).Error)
	require.Len(t, rows, 3)
	require.True(t, rows[2].IsCurrent)
	require.Equal(t, v3.RevisionID, rows[2].RevisionID)
}

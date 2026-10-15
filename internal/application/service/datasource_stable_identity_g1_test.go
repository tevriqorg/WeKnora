package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func stableMetadata(values map[string]string) types.JSON {
	raw, err := json.Marshal(values)
	if err != nil {
		panic(err)
	}
	return types.JSON(raw)
}

func TestG1DataSourceChangedContentReplacesStableKnowledgeID(t *testing.T) {
	oldBody := []byte("old body")
	existing := &types.Knowledge{ID: "kn-stable", FileName: "doc.md", FileHash: dataSourceContentHash(oldBody),
		Metadata: stableMetadata(map[string]string{"datasource_id": "ds-1", "external_id": "doc-1"})}
	repo := &deletionLookupKnowledgeRepo{knowledge: existing}
	ks := &sweepFakeKS{repo: repo}
	svc := &DataSourceService{knowledgeService: ks}
	ds := &types.DataSource{ID: "ds-1", TenantID: 7, KnowledgeBaseID: "kb-1"}

	isUpdate, err := svc.ingestItem(context.Background(), ds, &types.FetchedItem{
		ExternalID: "doc-1", FileName: "doc.md", Content: []byte("new body"),
		ContentType: "text/markdown",
	}, nil)
	require.NoError(t, err)
	require.True(t, isUpdate)
	require.Equal(t, []string{"kn-stable"}, ks.replacedFileIDs)
	require.Empty(t, ks.deleted)
	require.Empty(t, repo.hardDeleted)
}

func TestG1DataSourceSameHashRefreshesMetadataWithoutReparse(t *testing.T) {
	body := []byte("same body")
	existing := &types.Knowledge{ID: "kn-stable", FileName: "before.md", FileHash: dataSourceContentHash(body),
		Title: "before.md", Metadata: stableMetadata(map[string]string{"datasource_id": "ds-1", "external_id": "doc-1"})}
	repo := &deletionLookupKnowledgeRepo{knowledge: existing}
	ks := &sweepFakeKS{repo: repo}
	svc := &DataSourceService{knowledgeService: ks}
	ds := &types.DataSource{ID: "ds-1", TenantID: 7, KnowledgeBaseID: "kb-1"}
	isUpdate, err := svc.ingestItem(context.Background(), ds, &types.FetchedItem{
		ExternalID: "doc-1", FileName: "renamed.md", Content: body,
		ContentType: "text/markdown", Metadata: map[string]string{"label": "new"},
	}, nil)
	require.NoError(t, err)
	require.True(t, isUpdate)
	require.Empty(t, ks.replacedFileIDs)
	require.Empty(t, ks.deleted)
	require.Empty(t, repo.hardDeleted)
	require.Len(t, repo.columnUpdates, 1)
	require.Equal(t, "renamed.md", repo.columnUpdates[0]["file_name"])
}

func TestG1DataSourceChangedURLReplacesStableKnowledgeID(t *testing.T) {
	existing := &types.Knowledge{ID: "kn-url", Type: "url", Source: "https://example.com/old",
		Metadata: stableMetadata(map[string]string{"datasource_id": "ds-1", "external_id": "url-1"})}
	repo := &deletionLookupKnowledgeRepo{knowledge: existing}
	ks := &sweepFakeKS{repo: repo}
	svc := &DataSourceService{knowledgeService: ks}
	ds := &types.DataSource{ID: "ds-1", TenantID: 7, KnowledgeBaseID: "kb-1"}
	isUpdate, err := svc.ingestItem(context.Background(), ds, &types.FetchedItem{
		ExternalID: "url-1", URL: "https://example.com/new", Title: "New",
	}, nil)
	require.NoError(t, err)
	require.True(t, isUpdate)
	require.Equal(t, []string{"kn-url"}, ks.replacedURLIDs)
	require.Empty(t, ks.deleted)
	require.Empty(t, repo.hardDeleted)
}

func TestG1DataSourceURLNativeVersionControlsRevisionWithoutDeleteCreate(t *testing.T) {
	existing := &types.Knowledge{ID: "kn-url", Type: "url", Source: "https://example.com/doc",
		Metadata: stableMetadata(map[string]string{
			"datasource_id": "ds-1", "external_id": "url-1", "native_version": "v1",
		})}
	repo := &deletionLookupKnowledgeRepo{knowledge: existing}
	ks := &sweepFakeKS{repo: repo}
	svc := &DataSourceService{knowledgeService: ks}
	ds := &types.DataSource{ID: "ds-1", TenantID: 7, KnowledgeBaseID: "kb-1"}

	_, err := svc.ingestItem(context.Background(), ds, &types.FetchedItem{
		ExternalID: "url-1", URL: "https://example.com/doc", Metadata: map[string]string{"native_version": "v2"},
		UpdatedAt: time.Date(2026, 10, 14, 2, 0, 0, 0, time.UTC),
	}, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"kn-url"}, ks.replacedURLIDs)
	require.Empty(t, ks.deleted)
	require.Empty(t, repo.hardDeleted)
}

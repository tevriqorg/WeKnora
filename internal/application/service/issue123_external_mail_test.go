// Copied into the exact-pinned WeKnora internal/application/service package by CI.
// These tests call real WeKnora resource and datasource code, not a second Python
// reproduction. All files live in t.TempDir(); there is no NAS/mail connection.
package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"testing"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	appfile "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

func TestIssue123ThreeExistingExternalFilesNeverCopiedOrDeleted(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	catalog, _ := newResourceCatalogForTest(t)
	fileService := appfile.NewResourceCatalogFileService(appfile.NewLocalFileService(root, ""), catalog)
	writer := newDerivedWriterFixture()
	revisions := &derivedRevisionFixture{}
	derived := newDerivedKnowledgeEnvelopeService(writer, revisions)
	for n := 1; n <= 3; n++ {
		relative := fmt.Sprintf("7/archived/%d.md", n)
		absolute := filepath.Join(root, relative)
		require.NoError(t, os.MkdirAll(filepath.Dir(absolute), 0755))
		body := []byte(fmt.Sprintf("archived-%d-original", n))
		require.NoError(t, os.WriteFile(absolute, body, 0644))
		physical := "local://" + relative
		ref, err := catalog.Register(ctx, 7, physical, interfaces.ResourceRegistration{
			Kind: "file", Ownership: types.ResourceOwnershipExternal,
			OriginalName: filepath.Base(relative), Size: int64(len(body)),
		})
		require.NoError(t, err)
		same, err := catalog.Register(ctx, 7, physical, interfaces.ResourceRegistration{
			Ownership: types.ResourceOwnershipExternal,
		})
		require.NoError(t, err)
		require.Equal(t, ref, same)
		require.NoError(t, catalog.Bind(ctx, ref, types.ResourceOwnerKnowledge,
			fmt.Sprintf("external-%d", n), types.ResourceRelationSourceFile))
		data, err := fileService.GetFile(ctx, ref)
		require.NoError(t, err)
		got, err := io.ReadAll(data)
		require.NoError(t, err)
		require.NoError(t, data.Close())
		require.Equal(t, body, got)
		env := &types.DerivedKnowledgeEnvelope{
			SourceKey: fmt.Sprintf("mockqnap:%d", n), SourceRevisionID: "v1",
			DerivedObjectID: fmt.Sprintf("asset-%d", n), Title: fmt.Sprintf("existing-%d", n),
			Markdown: string(body), SourceResourceRef: ref,
			SourceLocators: types.SourceLocators{{Type: types.SourceLocatorText, SourceID: fmt.Sprintf("asset-%d", n)}},
			Processor:      "mock-fixture", ProcessorVersion: "v1",
			Capabilities: []string{"metadata"}, AnalysisStatus: "ready", ProducedAt: time.Now().UTC(),
		}
		knowledge, err := derived.Upsert(types.WithExecutionTenant(ctx, 7), "kb-1", env)
		require.NoError(t, err)
		require.NotEmpty(t, knowledge.ID)
		replay, err := derived.Upsert(types.WithExecutionTenant(ctx, 7), "kb-1", env)
		require.NoError(t, err)
		require.Equal(t, knowledge.ID, replay.ID)
		remaining, err := catalog.Release(ctx, ref, types.ResourceOwnerKnowledge, fmt.Sprintf("external-%d", n))
		require.NoError(t, err)
		require.Positive(t, remaining, "external raw must never become eligible for managed delete")
		require.NoError(t, fileService.DeleteFile(ctx, ref))
		after, err := os.ReadFile(absolute)
		require.NoError(t, err)
		require.Equal(t, body, after)
	}
	files := 0
	require.NoError(t, filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			files++
		}
		return nil
	}))
	require.Equal(t, 3, files, "WeKnora derived ingestion must not persist duplicate RAW files")
}

// Unlike the fast fixture above, this executes real WeKnora repository writes
// (Knowledge + SourceRevision) through the actual derived ingestion service.
// No FileService.SaveFile is invoked: the source remains externally owned.
func TestIssue123ExternalRawActualKnowledgeDatabaseHandoff(t *testing.T) {
	mini := miniredis.RunT(t)
	client := asynq.NewClient(asynq.RedisClientOpt{Addr: mini.Addr()})
	defer client.Close()
	fixture := newDocumentWriteFixture(t)
	fixture.svc.kbService = &g2OrdinaryDocKBService{kb: fixture.kbs.values["kb"]}
	fixture.svc.task = client
	require.NoError(t, fixture.db.AutoMigrate(
		&types.SourceRevision{}, &types.StoredResource{}, &types.ResourceBinding{}, &types.ResourceAccessGrant{},
	))
	catalog := NewResourceCatalog(apprepo.NewResourceRepository(fixture.db))
	concreteRepo := apprepo.NewKnowledgeRepository(fixture.db)
	revisionStore, ok := any(concreteRepo).(derivedSourceRevisionStore)
	require.True(t, ok)
	derived := newDerivedKnowledgeEnvelopeService(fixture.svc, revisionStore)
	root := t.TempDir()
	for n := 1; n <= 3; n++ {
		raw := []byte(fmt.Sprintf("QNAP archived original %d", n))
		physical := fmt.Sprintf("local://7/qnap/%d.md", n)
		path := filepath.Join(root, "7", "qnap", fmt.Sprintf("%d.md", n))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
		require.NoError(t, os.WriteFile(path, raw, 0644))
		ref, err := catalog.Register(fixture.ctx, 7, physical, interfaces.ResourceRegistration{
			Ownership: types.ResourceOwnershipExternal, OriginalName: fmt.Sprintf("%d.md", n),
		})
		require.NoError(t, err)
		env := &types.DerivedKnowledgeEnvelope{
			SourceKey: fmt.Sprintf("qnap:fixture:%d", n), SourceRevisionID: "sha256-fixture-v1",
			DerivedObjectID: fmt.Sprintf("file-%d", n), Title: fmt.Sprintf("%d.md", n),
			Markdown: string(raw), SourceResourceRef: ref,
			SourceLocators: types.SourceLocators{{Type: types.SourceLocatorText, SourceID: fmt.Sprintf("file-%d", n)}},
			Processor:      "mock-atrest", ProcessorVersion: "1",
			Capabilities: []string{"metadata"}, AnalysisStatus: "ready", ProducedAt: time.Now().UTC(),
		}
		knowledge, err := derived.Upsert(fixture.ctx, "kb", env)
		require.NoError(t, err)
		require.NotEmpty(t, knowledge.ID)
		stored, err := fixture.repo.GetKnowledgeByID(fixture.ctx, 7, knowledge.ID)
		require.NoError(t, err)
		require.Empty(t, stored.FilePath, "derived Knowledge must never store original Raw through SaveFile")
		require.Equal(t, types.KnowledgeTypeManual, stored.Type)
		var revision types.SourceRevision
		require.NoError(t, fixture.db.First(&revision, "knowledge_id = ?", knowledge.ID).Error)
		require.Equal(t, ref, revision.ResourceRef)
		after, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, raw, after)
	}
	count := 0
	require.NoError(t, filepath.WalkDir(root, func(_ string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			count++
		}
		return nil
	}))
	require.Equal(t, 3, count)
}

// When WeKnora's Local Storage is mapped to the archive location, the
// provider writes the one durable managed copy there, not to a second location.
func TestIssue123NewUploadGoesDirectlyToConfiguredArchiveRoot(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir() // mock QNAP mount, no real NAS
	svc := appfile.NewLocalFileService(root, "")
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", "4.md")
	require.NoError(t, err)
	_, err = part.Write([]byte("new-4-original"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	form, err := multipart.NewReader(&buf, writer.Boundary()).ReadForm(1024 * 1024)
	require.NoError(t, err)
	defer form.RemoveAll()
	f := form.File["file"][0]
	stored, err := svc.SaveFile(ctx, f, 7, "new-4")
	require.NoError(t, err)
	require.Contains(t, stored, "local://7/new-4/")
	reader, err := svc.GetFile(ctx, stored)
	require.NoError(t, err)
	content, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	require.Equal(t, "new-4-original", string(content))
	count := 0
	require.NoError(t, filepath.WalkDir(root, func(_ string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			count++
		}
		return nil
	}))
	require.Equal(t, 1, count, "only one managed final file under configured archive root")
}

// The mailbox is a deterministic 1..6 test-only feed; it is deliberately NOT a
// new live Mail Hub connector. The test exercises actual DataSourceService
// stable-identity decisions and existing cursor helper, not a copied algorithm.
type issue123MailRepository struct {
	interfaces.KnowledgeRepository
	rows map[string]*types.Knowledge
}

func (r *issue123MailRepository) FindByDataSourceExternalID(
	_ context.Context, _ uint64, _, datasourceID, externalID string,
) (*types.Knowledge, error) {
	for _, k := range r.rows {
		meta := k.GetMetadata()
		if meta["datasource_id"] == datasourceID && meta["external_id"] == externalID {
			return k, nil
		}
	}
	return nil, nil
}

func (r *issue123MailRepository) UpdateKnowledgeColumn(
	_ context.Context, id string, column string, value interface{},
) error {
	k := r.rows[id]
	if k == nil {
		return fmt.Errorf("unknown knowledge %s", id)
	}
	if column == "metadata" {
		if data, ok := value.(types.JSON); ok {
			k.Metadata = data
		}
	}
	return nil
}

func (r *issue123MailRepository) UpdateKnowledgeColumns(
	_ context.Context, id string, values map[string]interface{},
) error {
	k := r.rows[id]
	if k == nil {
		return fmt.Errorf("unknown knowledge %s", id)
	}
	if data, ok := values["metadata"].(types.JSON); ok {
		k.Metadata = data
	}
	if name, ok := values["file_name"].(string); ok {
		k.FileName = name
	}
	if title, ok := values["title"].(string); ok {
		k.Title = title
	}
	return nil
}

type issue123MailKnowledge struct {
	interfaces.KnowledgeService
	repo              *issue123MailRepository
	created, replaced int
}

func (k *issue123MailKnowledge) GetRepository() interfaces.KnowledgeRepository { return k.repo }

func (k *issue123MailKnowledge) CreateKnowledgeFromURL(
	_ context.Context, _ string, url, fileName, _ string,
	_ *bool, title string, _ []string, _ string,
	_ *types.KnowledgeProcessOverrides,
) (*types.Knowledge, error) {
	k.created++
	kn := &types.Knowledge{ID: fmt.Sprintf("mail-knowledge-%d", k.created),
		FileName: fileName, Title: title, Source: url, Type: "url"}
	k.repo.rows[kn.ID] = kn
	return kn, nil
}

func (k *issue123MailKnowledge) ReplaceKnowledgeURL(
	_ context.Context, id, url, fileName, title string, metadata map[string]string,
) (*types.Knowledge, error) {
	k.replaced++
	kn := k.repo.rows[id]
	kn.Source, kn.FileName, kn.Title = url, fileName, title
	raw, err := json.Marshal(metadata)
	if err != nil {
		return nil, err
	}
	kn.Metadata = types.JSON(raw)
	return kn, nil
}

func TestIssue123VirtualMailboxBackfillThenIncremental(t *testing.T) {
	repo := &issue123MailRepository{rows: make(map[string]*types.Knowledge)}
	knowledge := &issue123MailKnowledge{repo: repo}
	ds := &types.DataSource{ID: "virtual-mail", Type: "mockmail",
		TenantID: 7, KnowledgeBaseID: "kb-1"}
	service := &DataSourceService{knowledgeService: knowledge}
	ctx := context.Background()
	push := func(seq int, version string) {
		item := &types.FetchedItem{
			ExternalID: fmt.Sprintf("message-%d", seq),
			Title:      fmt.Sprintf("Virtual email %d", seq),
			FileName:   fmt.Sprintf("mail-%d.md", seq),
			URL:        fmt.Sprintf("https://mail.invalid/message/%d", seq),
			Metadata:   map[string]string{"native_version": version},
		}
		_, err := service.ingestItem(ctx, ds, item, nil)
		require.NoError(t, err)
	}
	for n := 1; n <= 3; n++ {
		push(n, "v1")
	}
	require.Equal(t, 3, knowledge.created)
	before := map[string]string{}
	for id, kn := range repo.rows {
		before[kn.GetMetadata()["external_id"]] = id
	}
	// Simulates a connector checkpoint after email 3 and incremental iteration.
	cursor := &types.SyncCursor{ConnectorCursor: map[string]interface{}{"last_message": float64(3)}}
	raw, err := json.Marshal(cursor)
	require.NoError(t, err)
	ds.LastSyncCursor = types.JSON(raw)
	next, err := streamStartCursor(ds, false, 0)
	require.NoError(t, err)
	require.Equal(t, float64(3), next.ConnectorCursor["last_message"])
	for n := 4; n <= 6; n++ {
		push(n, "v1")
	}
	require.Equal(t, 6, knowledge.created)
	require.Len(t, repo.rows, 6)
	for n := 4; n <= 6; n++ {
		push(n, "v1")
	}
	require.Equal(t, 6, knowledge.created, "replay must not make a seventh knowledge")
	require.Equal(t, 0, knowledge.replaced)
	push(2, "v2")
	require.Equal(t, 6, knowledge.created)
	require.Equal(t, 1, knowledge.replaced)
	for externalID, originalID := range before {
		kn, err := repo.FindByDataSourceExternalID(ctx, 7, "kb-1", "virtual-mail", externalID)
		require.NoError(t, err)
		require.NotNil(t, kn)
		require.Equal(t, originalID, kn.ID, "mail revision must preserve Knowledge identity")
	}
}

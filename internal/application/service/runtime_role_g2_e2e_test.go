package service

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

type g2OrdinaryDocKBService struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

func (s *g2OrdinaryDocKBService) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

func (s *g2OrdinaryDocKBService) GetKnowledgeBaseByIDOnly(context.Context, string) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

func TestG2OrdinaryDocumentApplicationAPIReachesCompletedThroughAsynq(t *testing.T) {
	mini := miniredis.RunT(t)
	client := asynq.NewClient(asynq.RedisClientOpt{Addr: mini.Addr()})
	defer client.Close()

	fixture := newDocumentWriteFixture(t)
	kbSvc := &g2OrdinaryDocKBService{kb: fixture.kbs.values["kb"]}
	fixture.svc.kbService = kbSvc
	fixture.svc.task = client

	knowledge, err := fixture.svc.CreateKnowledgeFromPassage(
		fixture.ctx,
		fixture.kbs.values["kb"].ID,
		[]string{"G2 ordinary document runtime reaches a durable terminal state."},
		"",
	)
	require.NoError(t, err)

	disabled := false
	require.NoError(t, knowledge.SetProcessOverrides(&types.KnowledgeProcessOverrides{
		SummaryEnabled: &disabled,
	}))
	require.NoError(t, fixture.repo.UpdateKnowledge(fixture.ctx, knowledge))

	post := &KnowledgePostProcessService{
		knowledgeRepo: fixture.repo,
		kbService:     kbSvc,
		chunkRepo:     fixture.chunkRepo,
		taskEnqueuer:  client,
	}

	core := asynq.NewServer(asynq.RedisClientOpt{Addr: mini.Addr()}, asynq.Config{
		Concurrency: 1,
		Queues:      map[string]int{types.QueueDefault: 1},
	})
	coreMux := asynq.NewServeMux()
	coreMux.HandleFunc(types.TypeDocumentProcess, fixture.svc.ProcessDocument)
	require.NoError(t, core.Start(coreMux))
	defer core.Shutdown()

	postprocess := asynq.NewServer(asynq.RedisClientOpt{Addr: mini.Addr()}, asynq.Config{
		Concurrency: 1,
		Queues:      map[string]int{types.QueuePostProcess: 1},
	})
	postMux := asynq.NewServeMux()
	postMux.HandleFunc(types.TypeKnowledgePostProcess, post.Handle)
	require.NoError(t, postprocess.Start(postMux))
	defer postprocess.Shutdown()

	deadline := time.Now().Add(8 * time.Second)
	for {
		current, getErr := fixture.repo.GetKnowledgeByID(fixture.ctx, knowledge.TenantID, knowledge.ID)
		require.NoError(t, getErr)
		if current.ParseStatus == types.ParseStatusCompleted {
			require.Equal(t, "enabled", current.EnableStatus)
			break
		}
		if current.ParseStatus == types.ParseStatusFailed {
			t.Fatalf("ordinary document failed: %s", current.ErrorMessage)
		}
		if time.Now().After(deadline) {
			t.Fatalf("ordinary document did not reach completed; status=%s", current.ParseStatus)
		}
		time.Sleep(25 * time.Millisecond)
	}

	chunks, err := fixture.chunkRepo.ListChunksByKnowledgeID(
		fixture.ctx, knowledge.TenantID, knowledge.ID,
	)
	require.NoError(t, err)
	require.NotEmpty(t, chunks)
	require.Contains(t, chunks[0].Content, "G2 ordinary document runtime")
}

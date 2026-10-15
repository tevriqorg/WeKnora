package router

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestG2OrdinaryDocumentTaskSurvivesWorkerTakeover(t *testing.T) {
	mini := miniredis.RunT(t)
	t.Setenv("REDIS_ADDR", mini.Addr())
	t.Setenv("REDIS_PASSWORD", "")
	t.Setenv("REDIS_USERNAME", "")
	t.Setenv("REDIS_DB", "0")

	rdb := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	client := asynq.NewClientFromRedisClient(rdb)
	inspector := asynq.NewInspectorFromRedisClient(rdb)
	t.Cleanup(func() {
		_ = inspector.Close()
		_ = client.Close()
		_ = rdb.Close()
	})

	startWorker := func(name string, seen chan<- string) *asynq.Server {
		worker := newAsynqServer(1, types.QueueWeightsForPool(types.WorkerPoolCore))
		mux := asynq.NewServeMux()
		mux.HandleFunc(types.TypeDocumentProcess, func(_ context.Context, task *asynq.Task) error {
			seen <- name + ":" + string(task.Payload())
			return nil
		})
		require.NoError(t, worker.Start(mux))
		return worker
	}

	seen := make(chan string, 2)
	workerOne := startWorker("worker-1", seen)
	_, err := client.Enqueue(
		asynq.NewTask(types.TypeDocumentProcess, []byte("doc-1")),
		asynq.Queue(types.QueueDefault), asynq.TaskID("g2-doc-1"),
	)
	require.NoError(t, err)
	select {
	case got := <-seen:
		require.Equal(t, "worker-1:doc-1", got)
	case <-time.After(5 * time.Second):
		t.Fatal("first ordinary-document task was not consumed")
	}
	workerOne.Shutdown()

	_, err = client.Enqueue(
		asynq.NewTask(types.TypeDocumentProcess, []byte("doc-2")),
		asynq.Queue(types.QueueDefault), asynq.TaskID("g2-doc-2"),
	)
	require.NoError(t, err)
	info, err := inspector.GetTaskInfo(types.QueueDefault, "g2-doc-2")
	require.NoError(t, err)
	require.Equal(t, asynq.TaskStatePending, info.State)

	workerTwo := startWorker("worker-2", seen)
	defer workerTwo.Shutdown()
	select {
	case got := <-seen:
		require.Equal(t, "worker-2:doc-2", got)
	case <-time.After(5 * time.Second):
		t.Fatal("pending ordinary-document task was not taken over by replacement worker")
	}
}

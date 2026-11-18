package plan_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx/bigwish"
)

// benchDB is the environment variable naming a database file a benchmark reads its large wish from: bigwish.Dew,
// made there the first time (about a minute, 300 MB), and kept for the next runs. Unset, the benchmark reads
// bigwish.Real from memory.
const benchDB = "DJINN_BENCH_DB"

// bigStore opens the store of a large wish, and returns its tasks in the order TaskService/List gives them.
func bigStore(b *testing.B) (*store.Store, []*planv1.Task) {
	b.Helper()
	ctx := context.Background()
	path := os.Getenv(benchDB)
	size := bigwish.Dew
	if path == "" {
		size = bigwish.Real
	}
	_, statErr := os.Stat(path)
	fresh := path == "" || errors.Is(statErr, os.ErrNotExist)
	s, err := store.Open(ctx, path, plan.Entities()...)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { s.Close() })
	if fresh {
		data, err := bigwish.Data(size)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := (&plan.Wishes{Store: s}).ImportData(ctx, connect.NewRequest(
			&planv1.WishServiceImportDataRequest{Data: data})); err != nil {
			b.Fatal(err)
		}
	}
	tasks, err := store.List[*planv1.Task](ctx, s, store.Where{"wish_id": bigwish.WishID})
	if err != nil {
		b.Fatal(err)
	}
	return s, tasks
}

// promptsPerTask is FillPrompts before W123: a statement per task, over every event of the task.
func promptsPerTask(ctx context.Context, r store.Reader, tasks []*planv1.Task) error {
	kind, _ := proto.Marshal(&planv1.TaskEvent{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_PROMPT})
	for _, t := range tasks {
		events, err := store.ListAfter[*planv1.TaskEvent](ctx, r, store.Where{"task_id": t.GetId()},
			store.After{Field: "seq", Contains: [][]byte{kind}})
		if err != nil {
			return err
		}
		if len(events) > 0 {
			t.Prompt = events[len(events)-1].GetText()
		}
	}
	return nil
}

// BenchmarkFillPrompts reads the prompts of a page of a large wish's tasks (50 that ran, as a page of TaskService/List
// shows), and of all its tasks: a statement per task before W123 (per-task), one through the index of the prompts
// after (indexed).
//
//	DJINN_BENCH_DB=$DJINN_SCRATCH/dew.db go test ./internal/plan -run '^$' -bench FillPrompts -benchtime 5x
func BenchmarkFillPrompts(b *testing.B) {
	s, tasks := bigStore(b)
	var ran []*planv1.Task
	for _, t := range tasks {
		if t.GetStartTime() != nil && len(ran) < 50 {
			ran = append(ran, t)
		}
	}
	pages := []struct {
		name  string
		tasks []*planv1.Task
	}{{"page", ran}, {"all", tasks}}
	ways := []struct {
		name string
		fill func(context.Context, store.Reader, []*planv1.Task) error
	}{{"per-task", promptsPerTask}, {"indexed", plan.FillPrompts}}
	for _, p := range pages {
		for _, w := range ways {
			b.Run(p.name+"/"+w.name, func(b *testing.B) {
				for b.Loop() {
					if err := w.fill(b.Context(), s, p.tasks); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

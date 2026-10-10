package plan_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/harness"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// size is how much a wish holds. Its tasks are split among its azimas: each has the tasks after it as parts.
type size struct{ tasks, azimas, questions, blocks, tilasms int }

// large is a wish as large as a long-lived one: ten times this project's own.
var large = size{tasks: 2000, azimas: 50, questions: 500, blocks: 2000, tilasms: 10}

// fillWish fills db with one wish of this size, and returns it and its tasks.
func fillWish(tb testing.TB, db *store.Store, n size) (string, []*planv1.Task) {
	tb.Helper()
	wish := &planv1.Wish{Id: store.NewID(), Title: "A wish of real size"}
	now := timestamppb.Now()
	text := func(n int) string { return strings.Repeat("Djinn keeps the wish up to date. ", n/33+1)[:n] }
	var tasks []*planv1.Task
	var azima string
	for i := range n.tasks {
		t := &planv1.Task{
			Id: store.NewID(), WishId: wish.GetId(), Code: fmt.Sprintf("W%d", i+1), Title: text(120),
			Status: planv1.TaskStatus_TASK_STATUS_DONE, Provider: planv1.Provider_PROVIDER_CLAUDE,
			Branch: text(60), Worktree: text(100), CreateTime: now, StartTime: now, EndTime: now, LastLine: text(100),
		}
		if i%(n.tasks/n.azimas) == 0 {
			t.Kind, t.Status = planv1.TaskKind_TASK_KIND_AZIMA, planv1.TaskStatus_TASK_STATUS_PENDING
			azima = t.GetId()
		} else {
			t.PartOf = azima
		}
		if i > 0 {
			t.DependsOn = []string{tasks[i-1].GetId()}
		}
		tasks = append(tasks, t)
	}
	err := db.Tx(tb.Context(), func(tx *store.Tx) error {
		if err := tx.Journal("bench", "bench/fill", wish); err != nil {
			return err
		}
		all := []proto.Message{wish}
		for _, t := range tasks {
			all = append(all, t)
		}
		for i := range n.questions {
			all = append(all, &planv1.Question{
				Id: store.NewID(), WishId: wish.GetId(), Code: fmt.Sprintf("Q%d", i+1), Text: text(300),
				Context: text(700), Recommendation: text(150), CreateTime: now,
			})
		}
		for i := range n.blocks {
			all = append(all, &planv1.Block{
				Id: store.NewID(), WishId: wish.GetId(), Kind: "journal", Title: text(60), Content: text(800),
				Position: int64(i), CreateTime: now, UpdateTime: now,
			})
		}
		for i := range n.tilasms {
			all = append(all, &planv1.Tilasm{
				Id: store.NewID(), WishId: wish.GetId(), Code: fmt.Sprintf("L%02d", i+1), Title: text(80),
				Cites: []string{tasks[i*7].GetId(), tasks[i*7+1].GetId()}, CreateTime: now,
			})
		}
		for _, m := range all {
			if err := tx.Put(m); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		tb.Fatal(err)
	}
	return wish.GetId(), tasks
}

// watched is djinn up in a test, on one wish: its store, its tasks, the task service, and a Watch stream of the wish
// past its first message.
type watched struct {
	db     *store.Store
	wishID string
	tasks  []*planv1.Task
	list   planv1connect.TaskServiceClient
	wishes planv1connect.WishServiceClient
	stream *connect.ServerStreamForClient[planv1.WishServiceWatchResponse]
	steps  int
}

func watchWish(tb testing.TB, n size) *watched {
	tb.Helper()
	tb.Cleanup(plan.SetWatchInterval(0))
	db, err := store.Open(tb.Context(), "", plan.Entities()...)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { db.Close() })
	wishID, tasks := fillWish(tb, db, n)
	h := harness.New(db, tb.TempDir(), harness.Providers())
	tb.Cleanup(h.Close)
	mux := http.NewServeMux()
	for prefix, handler := range plan.Handlers(db) {
		mux.Handle(prefix, handler)
	}
	mux.Handle(harness.Handler(h))
	srv := httptest.NewServer(mux)
	tb.Cleanup(srv.Close)
	ctx, cancel := context.WithCancel(tb.Context())
	tb.Cleanup(cancel)
	wishes := planv1connect.NewWishServiceClient(srv.Client(), srv.URL)
	stream, err := wishes.Watch(ctx, connect.NewRequest(&planv1.WishServiceWatchRequest{WishId: wishID}))
	if err != nil {
		tb.Fatal(err)
	}
	if !stream.Receive() {
		tb.Fatal(stream.Err())
	}
	return &watched{
		db: db, wishID: wishID, tasks: tasks, stream: stream, wishes: wishes,
		list: planv1connect.NewTaskServiceClient(srv.Client(), srv.URL),
	}
}

// change commits a change of one task, as a worker's progress does, or with done a change of its state, between
// running and done, and receives its message.
func (w *watched) change(tb testing.TB, done bool) *planv1.WishServiceWatchResponse {
	tb.Helper()
	w.steps++
	t := w.tasks[1+w.steps%(len(w.tasks)-1)]
	t.LastLine = fmt.Sprintf("step %d", w.steps)
	switch {
	case done && t.GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE:
		t.Status = planv1.TaskStatus_TASK_STATUS_RUNNING
	case done:
		t.Status = planv1.TaskStatus_TASK_STATUS_DONE
	case plan.IsAzima(t):
		t = w.tasks[1] // An azima never has progress: a part does.
	}
	err := w.db.Tx(tb.Context(), func(tx *store.Tx) error {
		if err := tx.Journal("bench", "bench/progress", t); err != nil {
			return err
		}
		return tx.Put(t)
	})
	if err != nil {
		tb.Fatal(err)
	}
	for w.stream.Receive() {
		if msg := w.stream.Msg(); slices.Contains(msg.GetChanges(), planv1.Change_CHANGE_TASK) {
			return msg
		}
	}
	tb.Fatal(w.stream.Err())
	return nil
}

// listed is the wish's tasks as TaskService.List gives them.
func (w *watched) listed(tb testing.TB) []*planv1.Task {
	tb.Helper()
	res, err := w.list.List(tb.Context(), connect.NewRequest(&planv1.TaskServiceListRequest{WishId: w.wishID}))
	if err != nil {
		tb.Fatal(err)
	}
	return res.Msg.GetTasks()
}

// listedWishes is the wishes as WishService.List gives them: the window reads them again when a message names the wish.
func (w *watched) listedWishes(tb testing.TB) *planv1.WishServiceListResponse {
	tb.Helper()
	res, err := w.wishes.List(tb.Context(), connect.NewRequest(&planv1.WishServiceListRequest{}))
	if err != nil {
		tb.Fatal(err)
	}
	return res.Msg
}

// TestWatchSendsTasksAsListed: the tasks a Watch message carries are those TaskService.List gives, azimas and tilasms
// filled, and every one that changed comes: a window that puts them in place of its own has what List gives.
func TestWatchSendsTasksAsListed(t *testing.T) {
	w := watchWish(t, size{tasks: 60, azimas: 3, tilasms: 4})
	window := map[string]*planv1.Task{}
	for _, task := range w.listed(t) {
		window[task.GetId()] = task
	}
	for i, done := range []bool{true, false, true, true, false, true} {
		msg := w.change(t, done)
		if msg.GetChanged() == nil {
			t.Fatalf("change %d: %v, want the tasks", i, msg)
		}
		for _, task := range msg.GetChanged().GetTasks() {
			window[task.GetId()] = task
		}
		for _, want := range w.listed(t) {
			if got := window[want.GetId()]; !proto.Equal(got, want) {
				t.Fatalf("change %d: the window has\n%v\nTaskService.List gives\n%v", i, got, want)
			}
		}
	}
}

// BenchmarkWatchATaskChange is what one change of a task costs djinn up and the window, on a large wish: the change
// committed, its Watch message received, and what the window then reads: the wishes when the message names the wish,
// and the tasks when the message does not carry them. A worker's progress changes the task alone;
// a change of its state, its azima and the wish too.
func BenchmarkWatchATaskChange(b *testing.B) {
	w := watchWish(b, large)
	for _, done := range []bool{false, true} {
		name := "progress"
		if done {
			name = "done"
		}
		// reread: the window reads the wishes and the wish's tasks again, as it did before Watch sent the tasks that
		// changed: every change of a task named the wish then.
		b.Run(name+"/reread", func(b *testing.B) {
			var bytes int
			for b.Loop() {
				msg := w.change(b, done)
				bytes += proto.Size(msg) + proto.Size(&planv1.TaskServiceListResponse{Tasks: w.listed(b)}) +
					proto.Size(w.listedWishes(b))
			}
			b.ReportMetric(float64(bytes)/float64(b.N), "wire-B/op")
		})
		// incremental: the message carries what changed.
		b.Run(name+"/incremental", func(b *testing.B) {
			var bytes int
			for b.Loop() {
				msg := w.change(b, done)
				if msg.GetChanged() == nil {
					b.Fatalf("%v: no tasks", msg)
				}
				bytes += proto.Size(msg)
				if slices.Contains(msg.GetChanges(), planv1.Change_CHANGE_WISH) {
					bytes += proto.Size(w.listedWishes(b))
				}
			}
			b.ReportMetric(float64(bytes)/float64(b.N), "wire-B/op")
		})
	}
}

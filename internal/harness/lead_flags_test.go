package harness

import (
	"context"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// TestTaskList tests TaskService.List with --short, --open, and --status flags.
func TestTaskList(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	t.Run("short", func(t *testing.T) {
		t.Parallel()
		e := up(t, t.TempDir())
		wishID, _ := e.wish(t, fakeProject(t, ""))
		done := e.ended(t, e.spawn(t, wishID, "text the final note").GetId())

		res, err := e.tasks.List(t.Context(), connect.NewRequest(&planv1.TaskServiceListRequest{WishId: wishID, Short: true}))
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Msg.GetTasks()) != 1 || res.Msg.GetTotal() != 1 {
			t.Fatalf("list --short = %v", res.Msg)
		}
		got := res.Msg.GetTasks()[0]
		if got.GetId() != done.GetId() || got.GetCode() != done.GetCode() || got.GetTitle() != done.GetTitle() ||
			got.GetStatus() != done.GetStatus() || got.GetEndTime() == nil {
			t.Errorf("list --short task = %v, want the fields of %v", got, done)
		}
		if got.GetPrompt() != "" || len(got.GetRuns()) != 0 || got.GetResources() != nil || got.GetWorktree() != "" ||
			got.GetUsage() != nil || got.GetRunning() != nil {
			t.Errorf("list --short holds more than where the task stands: %v", got)
		}
		whole, err := e.tasks.List(t.Context(), connect.NewRequest(&planv1.TaskServiceListRequest{WishId: wishID}))
		if err != nil || len(whole.Msg.GetTasks()) != 1 || len(whole.Msg.GetTasks()[0].GetRuns()) == 0 {
			t.Errorf("list without --short lost the task's runs: %v, %v", whole, err)
		}
	})

	t.Run("open", func(t *testing.T) {
		t.Parallel()
		e := up(t, t.TempDir())
		wishID, projectID := e.wish(t, fakeProject(t, ""))
		for code, status := range map[string]planv1.TaskStatus{
			"W1": planv1.TaskStatus_TASK_STATUS_RUNNING, "W2": planv1.TaskStatus_TASK_STATUS_PAUSED,
			"W3": planv1.TaskStatus_TASK_STATUS_RESUMING, "W4": planv1.TaskStatus_TASK_STATUS_PENDING,
			"W5": planv1.TaskStatus_TASK_STATUS_WAITING, "W6": planv1.TaskStatus_TASK_STATUS_INTERRUPTED,
			"W7": planv1.TaskStatus_TASK_STATUS_DONE, "W8": planv1.TaskStatus_TASK_STATUS_FAILED,
			"W9": planv1.TaskStatus_TASK_STATUS_STOPPED, "W10": planv1.TaskStatus_TASK_STATUS_CANCELLED,
		} {
			err := e.db.Tx(t.Context(), func(tx *store.Tx) error {
				if err := tx.Journal("local", "test", &planv1.Task{}); err != nil {
					return err
				}
				return tx.Put(&planv1.Task{Id: store.NewID(), WishId: wishID, ProjectId: projectID, Code: code,
					Title: "Task " + code, Status: status, CreateTime: timestamppb.Now()})
			})
			if err != nil {
				t.Fatal(err)
			}
		}
		list := func(req *planv1.TaskServiceListRequest) []string {
			t.Helper()
			req.WishId, req.Short = wishID, true
			res, err := e.tasks.List(t.Context(), connect.NewRequest(req))
			if err != nil {
				t.Fatal(err)
			}
			var codes []string
			for _, task := range res.Msg.GetTasks() {
				codes = append(codes, task.GetCode())
			}
			slices.Sort(codes)
			return codes
		}
		if got, want := list(&planv1.TaskServiceListRequest{Open: true}), []string{"W1", "W2", "W3", "W4", "W5", "W6"}; !slices.Equal(got, want) {
			t.Errorf("list --open = %v, want %v", got, want)
		}
		running, pending := planv1.TaskStatus_TASK_STATUS_RUNNING, planv1.TaskStatus_TASK_STATUS_PENDING
		failed := planv1.TaskStatus_TASK_STATUS_FAILED
		if got, want := list(&planv1.TaskServiceListRequest{Status: []planv1.TaskStatus{running, pending, failed}}), []string{"W1", "W4", "W8"}; !slices.Equal(got, want) {
			t.Errorf("list --status running,pending,failed = %v, want %v", got, want)
		}
		if got, want := list(&planv1.TaskServiceListRequest{Open: true, Status: []planv1.TaskStatus{running, failed}}), []string{"W1"}; !slices.Equal(got, want) {
			t.Errorf("list --open --status running,failed = %v, want %v: both filters keep a task", got, want)
		}
		if got := list(&planv1.TaskServiceListRequest{}); len(got) != 10 {
			t.Errorf("list = %v, want all 10 tasks", got)
		}
		_, err := e.tasks.List(t.Context(), connect.NewRequest(&planv1.TaskServiceListRequest{
			Status: []planv1.TaskStatus{planv1.TaskStatus_TASK_STATUS_UNSPECIFIED},
		}))
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("list --status unspecified: %v, want invalid argument", err)
		}
	})
}

// TestTaskWatchLast: djinn task watch --last n gives the last n events stored, returning at once on a running task
// unless --follow is set.
func TestTaskWatchLast(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	t.Run("done", func(t *testing.T) {
		t.Parallel()
		e := up(t, t.TempDir())
		wishID, _ := e.wish(t, fakeProject(t, ""))
		task := e.ended(t, e.spawn(t, wishID, "text one\ntext two\ntext the final note").GetId())
		all := e.watch(t.Context(), t, task.GetId(), 0)
		if len(all) < 5 {
			t.Fatalf("too few events to cut: %v", eventKinds(all))
		}
		watch := func(req *planv1.TaskServiceWatchRequest) []*planv1.TaskEvent {
			t.Helper()
			req.TaskId = task.GetId()
			s, err := e.tasks.Watch(t.Context(), connect.NewRequest(req))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			var out []*planv1.TaskEvent
			for s.Receive() {
				out = append(out, s.Msg().GetEvent())
			}
			if err := s.Err(); err != nil {
				t.Fatal(err)
			}
			return out
		}
		last := watch(&planv1.TaskServiceWatchRequest{Last: 3})
		if len(last) != 3 {
			t.Fatalf("--last 3 = %v", eventKinds(last))
		}
		checkSeqs(t, last, all[len(all)-3].GetSeq())
		var note bool
		for _, ev := range last {
			note = note || ev.GetText() == "the final note"
		}
		if !note {
			t.Errorf("--last 3 lacks the final note: %v", last)
		}
		if got := watch(&planv1.TaskServiceWatchRequest{Last: 1000}); len(got) != len(all) {
			t.Errorf("--last beyond the events = %d events, want all %d", len(got), len(all))
		}
		// --after-seq still wins when it is further.
		if got := watch(&planv1.TaskServiceWatchRequest{Last: 1000, AfterSeq: all[len(all)-2].GetSeq()}); len(got) != 1 {
			t.Errorf("--last with --after-seq = %v", eventKinds(got))
		}
	})

	t.Run("running", func(t *testing.T) {
		t.Parallel()
		e := up(t, t.TempDir())
		wishID, _ := e.wish(t, fakeProject(t, ""))
		task := e.spawn(t, wishID, "text working\nwait")
		e.untilText(t, task.GetId(), "working")
		watch := func(ctx context.Context, req *planv1.TaskServiceWatchRequest) []*planv1.TaskEvent {
			t.Helper()
			req.TaskId = task.GetId()
			s, err := e.tasks.Watch(ctx, connect.NewRequest(req))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			var out []*planv1.TaskEvent
			for s.Receive() {
				out = append(out, s.Msg().GetEvent())
			}
			return out
		}
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		start := time.Now()
		got := watch(ctx, &planv1.TaskServiceWatchRequest{Last: 2})
		if ctx.Err() != nil {
			t.Fatalf("--last 2 on a running task followed it until the timeout")
		}
		if len(got) != 2 || got[len(got)-1].GetText() != "working" {
			t.Errorf("--last 2 = %v, want the last 2 events, its text last", eventKinds(got))
		}
		if d := time.Since(start); d > 5*time.Second {
			t.Errorf("--last 2 took %s", d)
		}
		if e.get(t, task.GetId()).GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
			t.Fatal("the task no longer runs: the test proves nothing")
		}
		// A cancel, not a deadline: connect hands a deadline to the server, which then ends the stream cleanly a moment
		// before the client's own timer marks short as done.
		short, stop := context.WithCancel(t.Context())
		defer stop()
		time.AfterFunc(100*time.Millisecond, stop)
		watch(short, &planv1.TaskServiceWatchRequest{Last: 2, Follow: true})
		if short.Err() == nil {
			t.Error("--last 2 --follow returned before the task ended")
		}
	})
}

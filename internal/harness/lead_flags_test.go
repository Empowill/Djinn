package harness

import (
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/testx"
)

// TestTaskListShort: djinn task list --short gives each task in the few fields that say where it stands, never its
// prompt nor its runs, so that a lead reads a long list without piping it through head or grep (15/10/2026).
func TestTaskListShort(t *testing.T) {
	testx.Portable(t)
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
}

// TestTaskWatchLast: djinn task watch --last n starts at the last n events stored, the final note among them, in
// place of a pipe through tail.
func TestTaskWatchLast(t *testing.T) {
	testx.Portable(t)
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
}

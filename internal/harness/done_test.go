package harness

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/cli"
	"github.com/empowill/djinn/internal/store"
)

// TestDone: a task no worker runs is marked done by hand, whatever it was, and records who closed it, when and why.
// A running task, and a task done already, are refused.
func TestDone(t *testing.T) {
	e := up(t, t.TempDir())
	wishID, _ := e.wish(t, t.TempDir())
	ended := timestamppb.New(time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC))
	cases := []struct {
		status planv1.TaskStatus
		error  string
		end    *timestamppb.Timestamp
		by     planv1.Closer
		note   string
		event  string
	}{
		{status: planv1.TaskStatus_TASK_STATUS_PENDING, event: "marked done by the lead, was pending"},
		{status: planv1.TaskStatus_TASK_STATUS_WAITING, note: "not needed", event: "marked done by the lead, was waiting: not needed"},
		{status: planv1.TaskStatus_TASK_STATUS_INTERRUPTED, error: "djinn up stopped", end: ended, by: planv1.Closer_CLOSER_DEVELOPER,
			note: "merged in Git", event: "marked done by the developer, was interrupted (djinn up stopped): merged in Git"},
		{status: planv1.TaskStatus_TASK_STATUS_FAILED, error: "exit code 1", end: ended, event: "marked done by the lead, was failed (exit code 1)"},
		{status: planv1.TaskStatus_TASK_STATUS_STOPPED, error: "stopped on request", end: ended, event: "marked done by the lead, was stopped (stopped on request)"},
	}
	for i, c := range cases {
		task := &planv1.Task{Id: store.NewID(), WishId: wishID, Code: "T0" + string(rune('1'+i)), Title: "a task",
			Status: c.status, Error: c.error, EndTime: c.end, CreateTime: timestamppb.Now()}
		if err := e.db.Tx(t.Context(), func(tx *store.Tx) error {
			if err := tx.Journal("test", "put", task); err != nil {
				return err
			}
			return tx.Put(task)
		}); err != nil {
			t.Fatal(err)
		}
		res, err := e.tasks.Done(t.Context(), connect.NewRequest(&planv1.TaskServiceDoneRequest{TaskId: task.GetId(), Note: c.note, By: c.by}))
		if err != nil {
			t.Fatalf("%s: %v", short(c.status), err)
		}
		got := e.get(t, task.GetId())
		want := cmpOr(c.by, planv1.Closer_CLOSER_LEAD)
		if got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE || got.GetError() != "" || got.GetClosed().GetActor() != want ||
			got.GetClosed().GetNote() != c.note || got.GetClosed().GetCreateTime() == nil || got.GetEndTime() == nil {
			t.Errorf("%s, marked done: %v", short(c.status), got)
		}
		if c.end != nil && !got.GetEndTime().AsTime().Equal(c.end.AsTime()) {
			t.Errorf("%s: end time %v, want the one it had", short(c.status), got.GetEndTime().AsTime())
		}
		if res.Msg.GetTask().GetClosed() == nil {
			t.Errorf("%s: the response holds no closure", short(c.status))
		}
		events, _ := store.List[*planv1.TaskEvent](t.Context(), e.db, store.Where{"task_id": task.GetId()})
		if len(events) != 1 || events[0].GetText() != c.event || events[0].GetSeq() != 1 {
			t.Errorf("%s: events %v, want %q", short(c.status), events, c.event)
		}
		_, err = e.tasks.Done(t.Context(), connect.NewRequest(&planv1.TaskServiceDoneRequest{TaskId: task.GetId()}))
		if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "done already") {
			t.Errorf("%s, done twice: %v", short(c.status), err)
		}
	}

	running := e.spawn(t, wishID, "text working\nsleep 1h")
	_, err := e.tasks.Done(t.Context(), connect.NewRequest(&planv1.TaskServiceDoneRequest{TaskId: running.GetId()}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "stop it first") {
		t.Errorf("done on a running task: %v", err)
	}
	if _, err := e.tasks.Stop(t.Context(), connect.NewRequest(&planv1.TaskServiceStopRequest{TaskId: running.GetId()})); err != nil {
		t.Fatal(err)
	}
	if _, err := e.tasks.Done(t.Context(), connect.NewRequest(&planv1.TaskServiceDoneRequest{TaskId: running.GetId()})); err != nil {
		t.Errorf("done once stopped: %v", err)
	}
	done, err := store.Commands(t.Context(), e.db, func(c store.Command) bool {
		return c.Method == planv1connect.TaskServiceDoneProcedure
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(done) != len(cases)+1 {
		t.Errorf("%d Done commands journaled, want %d", len(done), len(cases)+1)
	}
}

func cmpOr(c, def planv1.Closer) planv1.Closer {
	if c == planv1.Closer_CLOSER_UNSPECIFIED {
		return def
	}
	return c
}

// TestDoneStartsDependent: a planned task marked done by hand lets the task waiting on it start.
func TestDoneStartsDependent(t *testing.T) {
	l := &limit{slots: 0}
	e := up(t, t.TempDir(), WithCapacity(l.capacity), WithTick(20*time.Millisecond))
	wishID, _ := e.wish(t, gitRepo(t))
	first := e.mustSpawn(t, wishID, "First", "text one", nil)
	second := e.mustSpawn(t, wishID, "Second", "text two", &planv1.TaskServiceSpawnRequest{DependsOn: []string{"W1"}})
	if _, err := e.tasks.Done(t.Context(), connect.NewRequest(&planv1.TaskServiceDoneRequest{TaskId: first.GetId(), Note: "done elsewhere"})); err != nil {
		t.Fatal(err)
	}
	if got := e.get(t, first.GetId()); got.GetStartTime() != nil || got.GetWorktree() != "" {
		t.Errorf("the closed task started: %v", got)
	}
	l.set(1, "")
	if got := e.ended(t, second.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE || got.GetClosed() != nil {
		t.Errorf("dependent = %v, want done by its worker", got)
	}
}

// TestDoneWaitingThenYes: a task waiting for its edit question, marked done by hand, stays done when the question
// is answered yes afterwards: its worker does not start again.
func TestDoneWaitingThenYes(t *testing.T) {
	e := up(t, t.TempDir())
	dir := folder(t)
	wishID, _ := e.wish(t, dir)
	task := e.spawn(t, wishID, "text reading\nwrite notes.md hello")
	e.watch(t.Context(), t, task.GetId(), 0)
	if got := e.get(t, task.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_WAITING {
		t.Fatalf("task = %v, want waiting", got)
	}
	if _, err := e.tasks.Done(t.Context(), connect.NewRequest(&planv1.TaskServiceDoneRequest{TaskId: task.GetId()})); err != nil {
		t.Fatal(err)
	}
	e.answer(t, e.editQuestionOf(t, task), planv1.Choice_CHOICE_A)
	events := e.watch(t.Context(), t, task.GetId(), 0)
	if hasText(events, "started fake again") || !hasText(events, "edit granted (Q01): the task was marked done by hand") {
		t.Errorf("events = %q", eventTexts(events))
	}
	if got := e.get(t, task.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE || got.GetClosed() == nil {
		t.Errorf("after yes: %v", got)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("written after the task was closed: %v", entries)
	}
}

// TestDoneCommand: djinn task done closes a task from the command line, with a note; a second time says it is done
// already, and exits 1.
func TestDoneCommand(t *testing.T) {
	e := up(t, t.TempDir())
	wishID, _ := e.wish(t, t.TempDir())
	task := &planv1.Task{Id: store.NewID(), WishId: wishID, Code: "W1", Title: "imported work",
		Status: planv1.TaskStatus_TASK_STATUS_INTERRUPTED, CreateTime: timestamppb.Now()}
	if err := e.db.Tx(t.Context(), func(tx *store.Tx) error {
		if err := tx.Journal("test", "put", task); err != nil {
			return err
		}
		return tx.Put(task)
	}); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (int, string, string) {
		var out, errs bytes.Buffer
		code := cli.Run(t.Context(), args, cli.Config{Version: "test", Addr: e.srv.URL, HTTP: e.srv.Client(), Stdout: &out, Stderr: &errs})
		return code, out.String(), errs.String()
	}
	code, out, errs := run("task", "done", task.GetId(), "--note", "merged in Git")
	if code != 0 || !strings.Contains(out, "status: done") || !strings.Contains(out, "actor: lead") || !strings.Contains(out, "note: merged in Git") {
		t.Errorf("djinn task done: code %d\nstdout:\n%s\nstderr:\n%s", code, out, errs)
	}
	code, _, errs = run("task", "done", task.GetId())
	if code != 1 || !strings.Contains(errs, "task W1 is done already") {
		t.Errorf("djinn task done twice: code %d, stderr %q", code, errs)
	}
}

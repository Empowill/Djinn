package harness

import (
	"bytes"
	"context"
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
	"github.com/empowill/djinn/internal/testx"
)

func (e *env) continueTask(t *testing.T, id, prompt string) (*planv1.Task, error) {
	t.Helper()
	res, err := e.tasks.Continue(t.Context(), connect.NewRequest(&planv1.TaskServiceContinueRequest{TaskId: id, Prompt: prompt}))
	if err != nil {
		return nil, err
	}
	return res.Msg.GetTask(), nil
}

// TestContinue: a done task is continued in place: the same task, running again on its own session, in its own
// worktree and branch, then done, its usage summed. Its events follow on: who continued it and the prompt's first
// line, the prompt, its new start, its end. No other task appears.
func TestContinue(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	e := up(t, t.TempDir())
	wishID, _ := e.wish(t, gitRepo(t))
	first := e.ended(t, e.mustSpawn(t, wishID, "Add the feature", "text first\nusage 1000 100 0.02", nil).GetId())
	if first.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE || first.GetWorktree() == "" {
		t.Fatalf("first run: %v", first)
	}
	before := len(e.watch(t.Context(), t, first.GetId(), 0))

	got, err := e.continueTask(t, first.GetId(), "text merged\nusage 200 20 0.01\n")
	if err != nil {
		t.Fatal(err)
	}
	if got.GetId() != first.GetId() || got.GetCode() != "W1" || got.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING ||
		got.GetContinuing() || got.GetResumes() != 0 {
		t.Fatalf("continued: %v, want W1 running", got)
	}
	done := e.ended(t, first.GetId())
	if done.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE || done.GetWorktree() != first.GetWorktree() ||
		done.GetBranch() != first.GetBranch() || done.GetSessionId() != first.GetSessionId() {
		t.Errorf("after its new turn: %v, want done in %s on %s", done, first.GetWorktree(), first.GetBranch())
	}
	if u := done.GetUsage(); u.GetInputTokens() != 1200 || u.GetOutputTokens() != 120 || u.GetCostUsd() < 0.0299 || u.GetCostUsd() > 0.0301 {
		t.Errorf("usage %v, want both runs summed", u)
	}
	if n := len(e.list(t, wishID)); n != 1 {
		t.Errorf("%d tasks, want the same one", n)
	}

	events := e.watch(t.Context(), t, first.GetId(), 0)
	checkSeqs(t, events, 1)
	after := events[before:]
	want := []struct {
		kind planv1.TaskEventKind
		text string
	}{
		{planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, "continued by the lead, was done: text merged"},
		{planv1.TaskEventKind_TASK_EVENT_KIND_PROMPT, "text merged\nusage 200 20 0.01\n"},
		{planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, "continued: started fake in "},
		{planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, "fake session " + first.GetId() + ", resumed"},
		{planv1.TaskEventKind_TASK_EVENT_KIND_TEXT, "merged"},
		{planv1.TaskEventKind_TASK_EVENT_KIND_USAGE, ""},
		{planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, "done"},
	}
	if len(after) != len(want) {
		t.Fatalf("events after the first run: %v", eventTexts(after))
	}
	for i, w := range want {
		if after[i].GetKind() != w.kind || !strings.HasPrefix(after[i].GetText(), w.text) {
			t.Errorf("event %d: %s %q, want %s %q", i, after[i].GetKind(), after[i].GetText(), w.kind, w.text)
		}
	}
	if start := after[2].GetText(); !strings.Contains(start, "on branch "+first.GetBranch()) || !strings.Contains(start, "resuming its session") {
		t.Errorf("start event %q", start)
	}

	cmds, err := store.Commands(t.Context(), e.db, func(c store.Command) bool {
		return c.Method == planv1connect.TaskServiceContinueProcedure
	})
	if err != nil || len(cmds) != 1 {
		t.Errorf("%d Continue commands journaled (%v), want 1", len(cmds), err)
	}

	// Failed, then continued: the same task ends done, its error gone.
	failed := e.ended(t, e.mustSpawn(t, wishID, "Fix it", "fail tests broke", nil).GetId())
	if _, err := e.continueTask(t, failed.GetId(), "text fixed"); err != nil {
		t.Fatal(err)
	}
	if again := e.ended(t, failed.GetId()); again.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE || again.GetError() != "" {
		t.Errorf("a failed task continued: %v", again)
	}
}

// TestContinueWaitsForASlot: a continued task goes back through the scheduler: on a full machine it waits, says why,
// and starts once a slot frees.
func TestContinueWaitsForASlot(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	l := &limit{slots: 1}
	e := up(t, t.TempDir(), WithCapacity(l.capacity))
	wishID, _ := e.wish(t, gitRepo(t))
	first := e.ended(t, e.mustSpawn(t, wishID, "First", "text one", nil).GetId())
	busy := e.mustSpawn(t, wishID, "Busy", "sleep 1h", nil)
	got, err := e.continueTask(t, first.GetId(), "text two")
	if err != nil {
		t.Fatal(err)
	}
	if got.GetStatus() != planv1.TaskStatus_TASK_STATUS_RESUMING || !strings.Contains(got.GetWaitReason(), "the most this machine holds") || !got.GetContinuing() {
		t.Fatalf("continued on a full machine: %v", got)
	}
	if _, err := e.tasks.Stop(t.Context(), connect.NewRequest(&planv1.TaskServiceStopRequest{TaskId: busy.GetId()})); err != nil {
		t.Fatal(err)
	}
	done := e.ended(t, first.GetId())
	texts := eventTexts(e.watch(t.Context(), t, first.GetId(), 0))
	if done.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE || done.GetContinuing() || texts[len(texts)-2] != "two" {
		t.Errorf("after the slot freed: %v\n%s", done, strings.Join(texts, "\n"))
	}
}

// TestContinueRefused: a task that runs, has lost its worktree, runs an agent that cannot resume a session, has no
// session, came from another Djinn, or continues in a fork is not continued, and says why.
func TestContinueRefused(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	e := up(t, t.TempDir())
	wishID, _ := e.wish(t, gitRepo(t))

	running := e.mustSpawn(t, wishID, "Long", "sleep 1h", nil)
	if _, err := e.continueTask(t, running.GetId(), "text more"); connectCode(err) != connect.CodeFailedPrecondition ||
		!strings.Contains(err.Error(), "task W1 runs") {
		t.Errorf("a running task: %v", err)
	}
	if _, err := e.tasks.Stop(t.Context(), connect.NewRequest(&planv1.TaskServiceStopRequest{TaskId: running.GetId()})); err != nil {
		t.Fatal(err)
	}

	cleaned := e.ended(t, e.mustSpawn(t, wishID, "Cleaned", "text one", nil).GetId())
	if _, err := e.tasks.Clean(t.Context(), connect.NewRequest(&planv1.TaskServiceCleanRequest{TaskId: cleaned.GetId()})); err != nil {
		t.Fatal(err)
	}
	if _, err := e.continueTask(t, cleaned.GetId(), "text more"); connectCode(err) != connect.CodeFailedPrecondition ||
		!strings.Contains(err.Error(), "has no worktree any more") {
		t.Errorf("a task without worktree: %v", err)
	}
	gone := e.ended(t, e.mustSpawn(t, wishID, "Gone", "text one", nil).GetId())
	if err := os.RemoveAll(gone.GetWorktree()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.continueTask(t, gone.GetId(), "text more"); connectCode(err) != connect.CodeFailedPrecondition ||
		!strings.Contains(err.Error(), "has lost its worktree") {
		t.Errorf("a task whose worktree was removed: %v", err)
	}

	put := func(code string, edit func(*planv1.Task)) *planv1.Task {
		task := &planv1.Task{Id: store.NewID(), WishId: wishID, Code: code, Title: "put", Provider: planv1.Provider_PROVIDER_FAKE,
			Status: planv1.TaskStatus_TASK_STATUS_INTERRUPTED, SessionId: "s", Scheduled: true, CreateTime: timestamppb.Now(),
			StartTime: timestamppb.Now(), EndTime: timestamppb.Now()}
		edit(task)
		putTask(t, e.db, task, "text first")
		return task
	}
	for _, c := range []struct {
		name string
		edit func(*planv1.Task)
		want string
	}{
		{"antigravity", func(t *planv1.Task) { t.Provider = planv1.Provider_PROVIDER_ANTIGRAVITY }, "cannot resume a session"},
		{"watcher", func(t *planv1.Task) { t.Provider = planv1.Provider_PROVIDER_WATCH }, "has no session"},
		{"no session", func(t *planv1.Task) { t.SessionId = "" }, "has no session to resume"},
		{"imported", func(t *planv1.Task) { t.Scheduled = false }, "was not run by this Djinn"},
		{"continued in a fork", func(t *planv1.Task) {
			t.Status, t.Closed = planv1.TaskStatus_TASK_STATUS_DONE, &planv1.Closure{ContinuedIn: "W9", Note: "continued in W9"}
		}, "continues in W9"},
		{"planned", func(t *planv1.Task) {
			t.Status, t.StartTime, t.EndTime, t.WaitReason = planv1.TaskStatus_TASK_STATUS_PENDING, nil, nil, "planned"
		}, "has not started"},
	} {
		task := put("X"+strings.ReplaceAll(c.name, " ", ""), c.edit)
		if _, err := e.continueTask(t, task.GetId(), "text more"); connectCode(err) != connect.CodeFailedPrecondition ||
			!strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		}
		if got := e.get(t, task.GetId()); got.GetStatus() != task.GetStatus() {
			t.Errorf("%s: refused, yet %v", c.name, got)
		}
	}
	// The prompt is required.
	if _, err := e.continueTask(t, running.GetId(), ""); connectCode(err) != connect.CodeInvalidArgument {
		t.Errorf("no prompt: %v", err)
	}
}

// TestForkClosesParent: a fork of a task cut short, failed or stopped continues it: the parent is done, closed by the
// lead with "continued in W…", and Djinn no longer resumes it. A fork of a done task leaves it as it is.
func TestForkClosesParent(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	e := up(t, t.TempDir())
	wishID, _ := e.wish(t, gitRepo(t))

	failed := e.ended(t, e.mustSpawn(t, wishID, "Broken", "fail tests broke", nil).GetId())
	fork := e.mustSpawn(t, wishID, "Broken (resumed)", "text fixed", &planv1.TaskServiceSpawnRequest{Fork: "W1"})
	parent := e.get(t, failed.GetId())
	c := parent.GetClosed()
	if parent.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE || parent.GetError() != "" || c.GetActor() != planv1.Closer_CLOSER_LEAD ||
		c.GetNote() != "continued in "+fork.GetCode() || c.GetContinuedIn() != fork.GetCode() || c.GetCreateTime() == nil {
		t.Fatalf("the failed parent: %v", parent)
	}
	texts := eventTexts(e.watch(t.Context(), t, parent.GetId(), 0))
	if last := texts[len(texts)-1]; last != "closed by the lead, was failed (tests broke): continued in W2" {
		t.Errorf("the parent's last event: %q", last)
	}
	// Closed, it continues in its fork only.
	if _, err := e.continueTask(t, parent.GetId(), "text more"); !strings.Contains(err.Error(), "continues in W2: continue W2 instead") {
		t.Errorf("continue the closed parent: %v", err)
	}

	// A task cut short, forked for later: closed with the planned fork.
	cut := &planv1.Task{Id: store.NewID(), WishId: wishID, Code: "W3", Title: "Cut", Provider: planv1.Provider_PROVIDER_FAKE,
		Status: planv1.TaskStatus_TASK_STATUS_INTERRUPTED, Error: "djinn up stopped while the worker ran", SessionId: "s3",
		Scheduled: true, CreateTime: timestamppb.Now(), StartTime: timestamppb.Now(), EndTime: timestamppb.Now()}
	putTask(t, e.db, cut, "text cut")
	later := e.mustSpawn(t, wishID, "Cut (resumed)", "text on", &planv1.TaskServiceSpawnRequest{Fork: "W3", Later: true})
	if got := e.get(t, cut.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE || got.GetClosed().GetContinuedIn() != later.GetCode() {
		t.Errorf("the interrupted parent of a planned fork: %v", got)
	}
	e.ended(t, later.GetId())

	// A fork of a done task starts another task from its context: the parent stays as it is.
	done := e.ended(t, fork.GetId())
	e.ended(t, e.mustSpawn(t, wishID, "Another", "text other", &planv1.TaskServiceSpawnRequest{Fork: done.GetCode()}).GetId())
	if got := e.get(t, done.GetId()); got.GetClosed() != nil || got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Errorf("the done parent of a fork: %v", got)
	}
}

// TestContinueCommand: djinn task continue plans a task again from the command line, with its prompt as a flag.
func TestContinueCommand(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	e := up(t, t.TempDir())
	wishID, _ := e.wish(t, gitRepo(t))
	first := e.ended(t, e.mustSpawn(t, wishID, "First", "text one", nil).GetId())
	var out, errs bytes.Buffer
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	code := cli.Run(ctx, []string{"task", "continue", first.GetId(), "--prompt", "text two"},
		cli.Config{Version: "test", Addr: e.srv.URL, HTTP: e.srv.Client(), Stdout: &out, Stderr: &errs})
	if code != 0 || !strings.Contains(out.String(), "code: W1") {
		t.Errorf("djinn task continue: code %d\nstdout:\n%s\nstderr:\n%s", code, out.String(), errs.String())
	}
	if done := e.ended(t, first.GetId()); done.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Errorf("continued from the command line: %v", done)
	}
}

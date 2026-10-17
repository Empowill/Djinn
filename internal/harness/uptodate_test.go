package harness

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/testx"
)

// saidStatus is the status event whose text starts with prefix.
func saidStatus(prefix string) func(*planv1.TaskEvent) bool {
	return func(ev *planv1.TaskEvent) bool {
		return ev.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_STATUS && strings.HasPrefix(ev.GetText(), prefix)
	}
}

// statuses are the task's status events that start with prefix.
func (in *integration) statuses(t *testing.T, task *planv1.Task, prefix string) []string {
	t.Helper()
	var texts []string
	for _, e := range in.events(t, task) {
		if saidStatus(prefix)(e) {
			texts = append(texts, e.GetText())
		}
	}
	return texts
}

// stopped stops the task's worker, and waits for it to be stopped.
func (in *integration) stopped(t *testing.T, task *planv1.Task) {
	t.Helper()
	if _, err := in.tasks.Stop(t.Context(), connect.NewRequest(&planv1.TaskServiceStopRequest{TaskId: task.GetId()})); err != nil {
		t.Fatal(err)
	}
	in.until(t, task.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_STOPPED))
}

// TestContinuedWorkerRebasesFirst (W250): a stopped task continued while its branch holds all of its target says
// nothing of a rebase; continued once work went into the target, the first line of what its worker is told asks it to
// rebase onto the target, listing what came in, and its events say it was asked. Once its branch holds the target,
// after a tool's result, its events say it rebased, from which commit to which. Djinn touches no worktree.
func TestContinuedWorkerRebasesFirst(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)
	late := in.mustSpawn(t, in.wishID, "Late", "wait", nil)
	in.follow(t, late.GetId())("its session", saidSession(late.GetId()))
	wt := in.get(t, late.GetId()).GetWorktree()
	writeFile(t, wt, "app/src/late.txt", "late\n")
	commitAll(t, wt, "Late's own file")
	in.stopped(t, late)

	// Up to date: nothing said of a rebase.
	if _, err := in.continueTask(t, late.GetId(), "text current\nwait"); err != nil {
		t.Fatal(err)
	}
	in.follow(t, late.GetId())("its text", said("current"))
	if got := in.statuses(t, late, "asked to rebase"); len(got) != 0 {
		t.Errorf("up to date, continued: %q; want no rebase asked", got)
	}
	in.stopped(t, late)

	// W5's work goes into the target: continued, Late is asked to rebase first.
	w5 := in.finished(t, "W5", map[string]string{"app/src/b.txt": "b\n"})
	in.pass(t, 0)
	if got := in.states(t, w5); got != "W5 COMMITTED" {
		t.Fatalf("W5: %s", got)
	}
	from, head := in.git(t, wt, "rev-parse", "HEAD"), in.tip(t)
	if _, err := in.continueTask(t, late.GetId(), "wait\ntool Bash git rebase\nresult done\ntext after"); err != nil {
		t.Fatal(err)
	}
	follow := in.follow(t, late.GetId()) // Once it runs: a stopped task's stream ends with its stored events.
	first := "Djinn: before you go on, rebase your branch onto " + in.branch + ": commit what you have in progress " +
		"(or let git rebase --autostash " + in.branch + " set it aside and restore it), then git rebase " + in.branch +
		"; resolve any conflict with your own changes in mind, then run the tests that cover what you touched. " +
		in.branch + " holds work your branch lacks:"
	follow("the rebase line", said(first))
	follow("what came in", said("- Work of W5"))
	branch := late.GetBranch()
	asked := fmt.Sprintf("asked to rebase %s (at %s) onto %s (at %s): 1 commit came in", branch, shortSHA(from),
		in.branch, shortSHA(head))
	if got := in.statuses(t, late, "asked to rebase"); !slices.Equal(got, []string{asked}) {
		t.Errorf("asked: %q; want %q", got, asked)
	}
	if got := in.git(t, wt, "rev-parse", "HEAD"); got != from {
		t.Errorf("Late's HEAD %s; want %s: Djinn rebases nothing itself", got, from)
	}

	// The worker rebases; after its tool's result, its events say so.
	in.git(t, wt, "rebase", "--quiet", in.branch)
	now := in.git(t, wt, "rev-parse", "HEAD")
	in.send(t, late.GetId(), "go on")
	follow("its rebase said", saidStatus("rebased "))
	want := fmt.Sprintf("rebased %s onto %s (at %s): %s → %s", branch, in.branch, shortSHA(head), shortSHA(from),
		shortSHA(now))
	if got := in.statuses(t, late, "rebased "); !slices.Equal(got, []string{want}) {
		t.Errorf("rebased: %q; want %q", got, want)
	}
}

// TestPausedWorkerRebasesPastThreshold (W250): a worker paused while work went into its target is asked to rebase on
// resuming only when its pause lasted PauseRebaseAfter or more: a message between two turns, the events saying it was
// asked. A shorter pause says nothing; neither pause fails it for its silence (the Lamplighter skips a paused worker).
func TestPausedWorkerRebasesPastThreshold(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)
	held := in.mustSpawn(t, in.wishID, "Held", "wait\ntext told", nil)
	in.follow(t, held.GetId())("its session", saidSession(held.GetId()))
	from := in.git(t, in.get(t, held.GetId()).GetWorktree(), "rev-parse", "HEAD")
	w5 := in.finished(t, "W5", map[string]string{"app/src/b.txt": "b\n"})
	in.pass(t, 0)
	if got := in.states(t, w5); got != "W5 COMMITTED" {
		t.Fatalf("W5: %s", got)
	}

	in.pause(t, held.GetId())
	in.pass(t, PauseRebaseAfter-time.Minute)
	in.resume(t, held.GetId())
	if got := in.told(t, held); len(got) != 0 {
		t.Errorf("resumed after a short pause, told %q; want nothing", got)
	}

	in.pause(t, held.GetId())
	in.pass(t, PauseRebaseAfter)
	follow := in.follow(t, held.GetId())
	if got := in.resume(t, held.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
		t.Fatalf("resumed: %v", got)
	}
	follow("the message taken", said("told"))
	b := &behind{branch: held.GetBranch(), from: from, target: in.branch, head: in.tip(t), subjects: []string{"Work of W5"}}
	if got := in.told(t, held); !slices.Equal(got, []string{rebaseText(b)}) {
		t.Errorf("resumed after a long pause, told %q; want %q", got, rebaseText(b))
	}
	if got, want := in.statuses(t, held, "asked to rebase"), askedText(b); !slices.Equal(got, []string{want}) {
		t.Errorf("asked: %q; want %q", got, want)
	}
}

// TestRebaseText: the rebase line lists what came in, the newest first, as W184's message does: twenty at most.
func TestRebaseText(t *testing.T) {
	subjects := make([]string, repairedSubjects+3)
	for i := range subjects {
		subjects[i] = fmt.Sprintf("commit %d", i)
	}
	text := rebaseText(&behind{target: "feat/x", subjects: subjects})
	lines := strings.Split(text, "\n")
	if !strings.HasPrefix(lines[0], "Djinn: before you go on, rebase your branch onto feat/x: ") ||
		lines[1] != "- commit 0" || lines[len(lines)-1] != "- and 3 more" || len(lines) != repairedSubjects+2 {
		t.Errorf("rebase text:\n%s", text)
	}
}

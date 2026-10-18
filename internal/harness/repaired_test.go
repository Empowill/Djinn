package harness

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/testx"
)

// told are the messages the task's worker was given, in their order.
func (in *integration) told(t *testing.T, task *planv1.Task) []string {
	t.Helper()
	var texts []string
	for _, e := range in.events(t, task) {
		if e.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_MESSAGE {
			texts = append(texts, e.GetText())
		}
	}
	return texts
}

// TestTellRepaired (W184, Q41 B): a merge with no red before it tells no running worker; once a repair's work is in,
// each running work worker whose branch lacks the integration branch's commits gets one message asking it to merge, and
// a worker in the middle of a tool call gets it only once the call has its result.
func TestTellRepaired(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	t.Run("green merge tells no worker", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		running := in.mustSpawn(t, in.wishID, "Running", "wait\ntext running", nil)
		in.until(t, running.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
		w5 := in.finished(t, "W5", map[string]string{"app/src/b.txt": "b\n"})
		in.pass(t, 0)
		if got := in.states(t, w5); got != "W5 COMMITTED" {
			t.Fatalf("a green merge: %s", got)
		}
		if got := in.told(t, running); len(got) != 0 {
			t.Errorf("told %q after a merge with no red before; want nothing", got)
		}
	})

	t.Run("repaired branch to workers", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		in.correctWith(func(_, prompt string) string {
			if strings.Contains(prompt, "the work of W6") {
				in.mu.Lock()
				in.testCode = 0
				in.mu.Unlock()
				return "text fixed"
			}
			return ""
		})
		clean := in.mustSpawn(t, in.wishID, "Clean", "wait\ntext clean", nil)
		clash := in.mustSpawn(t, in.wishID, "Clash", "wait\ntext clash", nil)
		busy := in.mustSpawn(t, in.wishID, "Busy", "tool Bash go test ./...\nwait\nresult ok\ntext busy", nil)
		for _, w := range []*planv1.Task{clean, clash} {
			in.until(t, w.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
		}
		in.untilText(t, busy.GetId(), "go test ./...")
		// Clash commits its own app/src/a.txt, which W6 writes too: merging the branch would conflict.
		wt := in.get(t, clash.GetId()).GetWorktree()
		writeFile(t, wt, "app/src/a.txt", "mine\n")
		commitAll(t, wt, "Clash's own a.txt")
		head := in.git(t, wt, "rev-parse", "HEAD")

		// Red: W6's checks fail, W7 repairs them; its work in, the branch is green again.
		in.testCode, in.testOut = 1, "--- FAIL: TestA"
		w6 := in.finished(t, "W6", map[string]string{"app/src/a.txt": "a\n"})
		in.pass(t, 0)
		w7 := in.correction(t, w6)
		in.pass(t, time.Second)
		if got := in.states(t, w6, w7); got != "W6 COMMITTED, W7 COMMITTED" {
			t.Fatalf("after the repair: %s", got)
		}
		want := "Djinn: the integration branch " + in.branch + " is green again: " + w7.GetCode() + " repaired it. " +
			"It holds work your branch lacks:\n- Work of W6\nMerge it into your branch now: commit what you " +
			"have in progress, then git merge " + in.branch + " (never a rebase). Resolve any conflict with your own " +
			"changes in mind, run the tests that cover what you touched, then go on with your task."
		for _, w := range []*planv1.Task{clean, clash} {
			if got := in.told(t, w); !slices.Equal(got, []string{want}) {
				t.Errorf("%s told %q; want %q", w.GetTitle(), got, want)
			}
		}
		in.untilText(t, clean.GetId(), "received: Djinn: the integration branch")
		// Djinn merged nothing in Clash's worktree: its commit, its file, no merge under way.
		if got := in.git(t, wt, "rev-parse", "HEAD"); got != head {
			t.Errorf("Clash's HEAD %s; want %s, untouched", got, head)
		}
		if b, err := os.ReadFile(filepath.Join(wt, "app", "src", "a.txt")); err != nil || string(b) != "mine\n" {
			t.Errorf("Clash's a.txt %q, %v; want its own", b, err)
		}
		if _, err := git(t.Context(), wt, "rev-parse", "--quiet", "--verify", "MERGE_HEAD"); err == nil {
			t.Error("a merge is under way in Clash's worktree; want none")
		}

		// Busy is in the middle of a tool call: nothing until its result.
		if got := in.told(t, busy); len(got) != 0 {
			t.Fatalf("Busy told %q mid-turn; want nothing yet", got)
		}
		in.send(t, busy.GetId(), "go on")
		in.untilText(t, busy.GetId(), "received: Djinn: the integration branch")
		var result, message int64
		for _, e := range in.events(t, busy) {
			switch {
			case e.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_RESULT && e.GetText() == "ok":
				result = e.GetSeq()
			case e.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_MESSAGE && e.GetText() == want:
				message = e.GetSeq()
			}
		}
		if result == 0 || message < result {
			t.Errorf("Busy's tool result at %d, the message at %d; want the message after the result", result, message)
		}
	})
}

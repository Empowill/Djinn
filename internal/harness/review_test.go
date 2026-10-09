package harness

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// reviewer is the review worker that reviews task's work left not committed, once it has ended.
func (in *integration) reviewer(t *testing.T, task *planv1.Task) *planv1.Task {
	t.Helper()
	got, texts := in.integration(t, task)
	if got.GetReviewedBy() == "" {
		t.Fatalf("%s's work has no review worker: %v; events %q", task.GetCode(), got, texts)
	}
	return in.ended(t, got.GetReviewedBy())
}

// wishTasks are the wish's tasks.
func (in *integration) wishTasks(t *testing.T) []*planv1.Task {
	t.Helper()
	tasks, err := store.List[*planv1.Task](t.Context(), in.db, store.Where{"wish_id": in.wishID})
	if err != nil {
		t.Fatal(err)
	}
	return tasks
}

// TestIntegrateACleanWorktree: a worktree whose work is committed, a file the project's .gitignore ignores left in it,
// integrates as it is: no review worker, the ignored file left out.
func TestIntegrateACleanWorktree(t *testing.T) {
	in := integrating(t)
	w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n", ".gitignore": "*.log\n"})
	in.leave(t, w1, map[string]string{"app/build.log": "built\n"})

	in.pass(t, 0)
	if got := in.states(t, w1); got != "W1 COMMITTED" {
		t.Fatalf("a clean worktree: %s", got)
	}
	if n := len(in.wishTasks(t)); n != 1 {
		t.Errorf("%d tasks: a review worker started for a clean worktree", n)
	}
	if out := in.git(t, in.repo, "ls-tree", "-r", "--name-only", in.tip(t), "app"); strings.Contains(out, "build.log") ||
		!strings.Contains(out, "app/src/a.txt") {
		t.Errorf("the branch holds:\n%s", out)
	}
}

// TestReviewUncommittedWork: a worktree with changes not committed is not merged and nothing commits them blindly; a
// review worker starts by itself in that worktree, the files and their diff in its first prompt; it commits what
// belongs to the task and drops the rest, and its work integrates like any task's, the reviewed work with it.
func TestReviewUncommittedWork(t *testing.T) {
	in := integrating(t)
	old := in.tip(t)
	azima := in.azima(t, in.wishID, "The sources")
	w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = azima.GetId() })
	in.leave(t, w1, map[string]string{"app/src/b.txt": "b\n", "app/README.md": "# App, with b\n", "app/scratch.txt": "debug\n"})
	var dir, before, status string
	in.correctWith(func(d, _ string) string {
		dir = d
		before, _ = git(t.Context(), d, "log", "-1", "--format=%s")
		status, _ = git(t.Context(), d, "status", "--porcelain")
		if err := os.Remove(filepath.Join(d, "scratch.txt")); err != nil {
			t.Error(err)
		}
		commitAll(t, d, "Add b, named in the readme")
		return "text Kept src/b.txt and README.md, the task's; dropped scratch.txt, debug output."
	})

	in.pass(t, 0)
	w2 := in.reviewer(t, w1)
	got, texts := in.integration(t, w1)
	if got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_UNCOMMITTED || got.GetReason() != "uncommitted: reviewed by W2" ||
		got.GetAttempts() != 1 || got.GetBranch() != in.branch {
		t.Fatalf("W1's integration %v", got)
	}
	want := "uncommitted: W1's worktree holds changes not committed (app/README.md, app/scratch.txt, app/src/b.txt); not merged, " +
		"nothing committed; W2 reviews it, attempt 1 of 2"
	if texts[len(texts)-1] != want {
		t.Errorf("W1's last event %q; want %q", texts[len(texts)-1], want)
	}
	// Not merged, not tested, and nothing committed blindly: the reviewer found W1's work as its worker left it.
	if in.tip(t) != old || len(in.runs) != 0 || len(in.commits(t)) != 0 {
		t.Errorf("merged: the branch at %s from %s; runs %v; commits %v", in.tip(t), old, in.runs, in.commits(t))
	}
	if before != "Work of W1" || status != "M app/README.md\n?? app/scratch.txt\n?? app/src/b.txt" {
		t.Errorf("the reviewer found the last commit %q and the changes\n%s", before, status)
	}
	// The review worker: work part of W1's azima, of its provider, Djinn's own, in W1's worktree and on its branch.
	if w2.GetCode() != "W2" || w2.GetPartOf() != azima.GetId() || w2.GetProvider() != planv1.Provider_PROVIDER_FAKE ||
		!slices.Equal(w2.GetReview().GetTaskIds(), []string{w1.GetId()}) || w2.GetReview().GetAttempt() != 1 ||
		w2.GetBranch() != w1.GetBranch() || w2.GetWorktree() != w1.GetWorktree() || w2.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Fatalf("the review worker %v", w2)
	}
	if dir != filepath.Join(w1.GetWorktree(), "app") {
		t.Errorf("W2 ran in %s", dir)
	}
	prompt, err := firstPrompt(in.db, w2.GetId())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"the work of W1 into " + in.branch, "The task, W1: Work of W1. Its prompt:\n\n    work\n",
		"     M app/README.md\n    ?? app/scratch.txt\n    ?? app/src/b.txt\n", "    -# App\n    +# App, with b\n",
		"    +++ b/app/scratch.txt\n    +debug\n", "    +++ b/app/src/b.txt\n    +b", "Commit what belongs to the task",
		"debug output, scratch files, build artifacts", "its commit checks (`djinn gate run test -- test`) run through their gates", "what you kept, what you dropped, and why"} {
		if !strings.Contains(prompt, s) {
			t.Errorf("W2's prompt lacks %q:\n%s", s, prompt)
		}
	}
	if got, _ := in.integration(t, w2); got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_PENDING {
		t.Fatalf("W2's work: %v", got)
	}

	// The review's commit integrates at once, alone, like any task's: W1's work goes in with it.
	in.pass(t, time.Second)
	if got := in.states(t, w1, w2); got != "W1 COMMITTED, W2 COMMITTED" {
		t.Fatalf("after the review: %s", got)
	}
	tip := in.tip(t)
	got, texts = in.integration(t, w1)
	if got.GetSha() != tip || got.GetReason() != "uncommitted: reviewed by W2" || got.GetReviewedBy() != w2.GetId() ||
		texts[len(texts)-1] != "committed into "+in.branch+" as "+tip[:8]+", its work not committed reviewed by W2" {
		t.Errorf("W1's integration %v; events %q", got, texts)
	}
	if out := in.git(t, in.repo, "ls-tree", "-r", "--name-only", tip, "app"); out != "app/README.md\napp/gen/index.txt\napp/src/.keep\napp/src/a.txt\napp/src/b.txt" {
		t.Errorf("the branch holds:\n%s", out)
	}
	if out := in.git(t, in.repo, "log", "--format=%s", "-2", w1.GetBranch()); out != "Add b, named in the readme\nWork of W1" {
		t.Errorf("W1's branch ends with:\n%s", out)
	}
	commits := in.commits(t)
	if len(commits) != 1 || commits[0].GetOldSha() != old || !slices.Equal(commits[0].GetTaskIds(), []string{w2.GetId(), w1.GetId()}) {
		t.Errorf("journal: %v", commits)
	}
}

// TestReviewAttemptsThenAQuestion: a review worker that fails, then one that leaves the work not committed in turn,
// spend the attempts; Djinn then asks the person, and trying again starts a new review, its attempts counted from one.
func TestReviewAttemptsThenAQuestion(t *testing.T) {
	in := integrating(t)
	w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"})
	in.leave(t, w1, map[string]string{"app/src/b.txt": "b\n"})
	reviews := 0
	in.correctWith(func(d, _ string) string {
		in.mu.Lock()
		defer in.mu.Unlock()
		switch reviews++; reviews {
		case 1:
			return "fail I cannot tell what belongs to it"
		case 2:
			return "text I leave it as it is."
		}
		commitAll(t, d, "Add b")
		return ""
	})

	in.pass(t, 0)
	w2 := in.reviewer(t, w1)
	if w2.GetStatus() != planv1.TaskStatus_TASK_STATUS_FAILED {
		t.Fatalf("W2 %v", w2)
	}
	in.pass(t, time.Second)
	w3 := in.reviewer(t, w1)
	if w3.GetReview().GetAttempt() != 2 || !slices.Equal(w3.GetReview().GetTaskIds(), []string{w1.GetId()}) {
		t.Fatalf("the second attempt %v", w3)
	}
	if _, texts := in.integration(t, w1); texts[len(texts)-1] != "W2, its review worker, failed: I cannot tell what belongs to it; "+
		"W3 reviews it, attempt 2 of 2" {
		t.Errorf("W1's events %q", texts)
	}
	in.pass(t, time.Second)
	if got := in.states(t, w1, w3); got != "W1 UNCOMMITTED, W3 UNCOMMITTED" {
		t.Fatalf("two attempts spent: %s", got)
	}
	q := in.question(t, w1)
	got, texts := in.integration(t, w3)
	if got.GetQuestionId() != q.GetId() || got.GetAttempts() != 2 || got.GetReviewedBy() != "" ||
		!strings.HasSuffix(texts[len(texts)-1], "; Djinn asks you "+q.GetCode()) {
		t.Errorf("W3's integration %v; events %q", got, texts)
	}
	if q.GetText() != "The work of W1 does not go into "+in.branch+": its worktree holds changes not committed. Djinn started 2 "+
		"review workers, one after the other: none left it committed. What should Djinn do?" ||
		!slices.Equal(q.GetOptions(), []string{reviewRetryOption, "Leave it: the work stays out of " + in.branch, takeOption}) ||
		!strings.Contains(q.GetContext(), "**The files not committed.** app/src/b.txt") || !strings.Contains(q.GetContext(), "**The attempts.** W3") {
		t.Errorf("the question %q, %q:\n%s", q.GetText(), q.GetOptions(), q.GetContext())
	}
	// Nothing more starts by itself, and nothing is merged.
	in.pass(t, time.Hour)
	if n := len(in.wishTasks(t)); n != 3 || len(in.commits(t)) != 0 {
		t.Fatalf("%d tasks, commits %v; want 3 tasks, no commit", n, in.commits(t))
	}

	// Try again: a new review, its attempts counted from one, which commits; the work goes in.
	in.answer(t, q, planv1.Choice_CHOICE_A)
	got, texts = in.integration(t, w1)
	if got.GetQuestionId() != "" || got.GetAttempts() != 1 || got.GetReason() != "uncommitted: reviewed by W4" ||
		texts[len(texts)-1] != "you said to try again ("+q.GetCode()+"); W4 reviews it, attempt 1 of 2" {
		t.Fatalf("tried again: %v; events %q", got, texts)
	}
	w4 := in.reviewer(t, w1)
	in.pass(t, time.Second)
	if got := in.states(t, w1, w3, w4); got != "W1 COMMITTED, W3 COMMITTED, W4 COMMITTED" {
		t.Errorf("after the third review: %s", got)
	}
	if out := in.git(t, in.repo, "show", in.tip(t)+":app/src/b.txt"); out != "b" {
		t.Errorf("b.txt in the branch: %q", out)
	}
}

// TestReviewACorrectionsWork: a correction worker that leaves its work not committed is not merged either: a review
// worker commits it, in the correction's worktree, which concludes the merge; its success brings in the correction's
// work and the work it corrected.
func TestReviewACorrectionsWork(t *testing.T) {
	in := integrating(t)
	azima := in.azima(t, in.wishID, "The readme")
	partOf := func(task *planv1.Task) { task.PartOf = azima.GetId() }
	w1 := in.finished(t, "W1", map[string]string{"app/README.md": "# One\n"}, partOf)
	w2 := in.finished(t, "W2", map[string]string{"app/README.md": "# Two\n"}, partOf)
	in.correctWith(func(d, prompt string) string {
		if strings.HasPrefix(prompt, "Djinn could not integrate") {
			writeFile(t, d, "README.md", "# One and Two\n") // Settled, not committed.
			return ""
		}
		commitAll(t, d, "Settle the readme")
		return ""
	})

	in.pass(t, 0) // W1 goes in; W2 conflicts with it, which starts W3.
	w3 := in.correction(t, w2)
	in.pass(t, time.Second)
	w4 := in.reviewer(t, w3)
	if !slices.Equal(w4.GetReview().GetTaskIds(), []string{w3.GetId()}) || w4.GetWorktree() != w3.GetWorktree() {
		t.Fatalf("the review of W3's work %v", w4)
	}
	if got := in.states(t, w1, w2, w3); got != "W1 COMMITTED, W2 CONFLICT, W3 UNCOMMITTED" {
		t.Fatalf("W3's work not committed: %s", got)
	}
	if n := len(in.commits(t)); n != 1 {
		t.Fatalf("%d commits; want W1's alone: %v", n, in.commits(t))
	}
	in.pass(t, time.Second)
	if got := in.states(t, w1, w2, w3, w4); got != "W1 COMMITTED, W2 COMMITTED, W3 COMMITTED, W4 COMMITTED" {
		t.Fatalf("after the review: %s", got)
	}
	tip := in.tip(t)
	if got, _ := in.integration(t, w2); got.GetReason() != "corrected by W3" || got.GetSha() != tip {
		t.Errorf("W2's integration %v", got)
	}
	if got, _ := in.integration(t, w3); got.GetReason() != "uncommitted: reviewed by W4" {
		t.Errorf("W3's integration %v", got)
	}
	if out := in.git(t, in.repo, "show", tip+":app/README.md"); out != "# One and Two" {
		t.Errorf("README.md in the branch: %q", out)
	}
	if _, err := os.Stat(w3.GetWorktree()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("W3's worktree, all committed, is still there: %v", err)
	}
	for _, task := range []*planv1.Task{w1, w2} {
		if _, err := git(t.Context(), in.repo, "merge-base", "--is-ancestor", task.GetBranch(), tip); err != nil {
			t.Errorf("%s's branch is not in %s: %v", task.GetCode(), in.branch, err)
		}
	}
}

// TestAReviewThatFailsAfterItsCommit: a review worker that fails once the work is committed leaves nothing to review:
// the work waits to be merged, and integrates.
func TestAReviewThatFailsAfterItsCommit(t *testing.T) {
	in := integrating(t)
	w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"})
	in.leave(t, w1, map[string]string{"app/src/b.txt": "b\n"})
	in.correctWith(func(d, _ string) string {
		commitAll(t, d, "Add b")
		return "fail out of budget"
	})

	in.pass(t, 0)
	in.reviewer(t, w1)
	in.pass(t, time.Second) // W2 failed: W1 waits again.
	in.pass(t, time.Second)
	got, texts := in.integration(t, w1)
	if got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED || !slices.Contains(texts,
		"W2, its review worker, failed: out of budget; W1's worktree holds nothing not committed any more: it waits to be merged") {
		t.Fatalf("W1's integration %v; events %q", got, texts)
	}
	if out := in.git(t, in.repo, "show", in.tip(t)+":app/src/b.txt"); out != "b" {
		t.Errorf("b.txt in the branch: %q", out)
	}
}

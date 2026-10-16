package harness

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/empowill/djinn/internal/testx"
)

// TestStartUnlocksDjinnsWorktrees: a start takes the dead gits' index.lock away in the worktrees Djinn made, and
// aborts the merge left half done where Djinn owns it, its integration worktree; a worker's worktree keeps its merge
// (a correction starts on its conflict), and the person's checkout keeps its lock, which the restart line names (W52).
func TestStartUnlocksDjinnsWorktrees(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)
	head := commitOf(t.Context(), in.repo, "HEAD")
	mustGit(t, in.repo, "branch", "other")
	other := filepath.Join(t.TempDir(), "other")
	mustGit(t, in.repo, "worktree", "add", "--quiet", other, "other")
	writeFile(t, other, "app/other.txt", "other\n")
	commitAll(t, other, "Other")

	wt := integrationDir(in.home, in.projectID, in.wishID)
	if _, _, err := integrationWorktree(t.Context(), in.repo, wt, head); err != nil {
		t.Fatal(err)
	}
	worker := in.finished(t, "W1", nil).GetWorktree()
	for _, dir := range []string{wt, worker} {
		mustGit(t, dir, "merge", "--quiet", "--no-ff", "--no-commit", "other")
		writeFile(t, gitDir(dir), "index.lock", "")
	}
	writeFile(t, in.repo, ".git/index.lock", "")

	in.restart(t)

	for _, dir := range []string{wt, worker} {
		if _, err := os.Stat(filepath.Join(gitDir(dir), "index.lock")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("index.lock left in %s: %v", dir, err)
		}
	}
	if _, err := os.Stat(filepath.Join(gitDir(wt), "MERGE_HEAD")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the integration worktree's merge left half done: %v", err)
	}
	if _, err := os.Stat(filepath.Join(gitDir(worker), "MERGE_HEAD")); err != nil {
		t.Errorf("the worker's merge was aborted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(in.repo, ".git", "index.lock")); err != nil {
		t.Errorf("the person's index.lock was removed: %v", err)
	}
	restarts := restartEvents(t, in.env)
	if want := "an index.lock is left in " + in.repo + ", not Djinn's"; len(restarts) == 0 ||
		!strings.Contains(restarts[len(restarts)-1], want) {
		t.Errorf("restart lines %q; want the last to say %q", restarts, want)
	}
}

// TestRestartNamesWhatTheStopCut: the restart line names the integration a stop cut, its task and its step (W52).
func TestRestartNamesWhatTheStopCut(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)
	in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"})
	in.afterTest = func() { in.h.cancel() } // djinn up stops during W1's check.
	in.h.integratePass(in.h.ctx)
	in.restart(t)

	restarts := restartEvents(t, in.env)
	want := "the stop cut the integration of W1 into " + in.branch + ", checking test"
	if len(restarts) == 0 || !strings.Contains(restarts[len(restarts)-1], want) {
		t.Errorf("restart lines %q; want the last to say %q", restarts, want)
	}
	if _, err := os.Stat(filepath.Join(in.home, cutFile)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s kept after it was told: %v", cutFile, err)
	}
	in.restart(t)
	if restarts := restartEvents(t, in.env); strings.Contains(restarts[len(restarts)-1], "the stop cut") {
		t.Errorf("a stop that cut nothing told %q", restarts[len(restarts)-1])
	}
}

func TestCutPart(t *testing.T) {
	for _, c := range []struct {
		cut, integrating []string
		want             string
	}{
		{nil, nil, ""},
		{[]string{"the push of main to origin"}, nil, "the stop cut the push of main to origin"},
		{nil, []string{"W2", "W3"}, "the stop cut the integration of W2, W3"},
		{[]string{"the integration of W2 into main, checking lint", "git merge-base in the install worktree"}, []string{"W2", "W23"},
			"the stop cut the integration of W2 into main, checking lint, git merge-base in the install worktree, the integration of W23"},
	} {
		if got := cutPart(c.cut, c.integrating); got != c.want {
			t.Errorf("cutPart(%q, %q) = %q; want %q", c.cut, c.integrating, got, c.want)
		}
	}
	h := &Harness{home: filepath.FromSlash("/d")}
	for dir, want := range map[string]string{
		"/d/projects/p/integration/w": "the integration worktree of wish w",
		"/d/projects/p/install":       "the install worktree",
		"/d/projects/p/worktrees/t":   "the worktree of task t",
		"/src/app":                    "/src/app",
	} {
		if got := h.worktreeName(filepath.FromSlash(dir)); got != filepath.FromSlash(want) {
			t.Errorf("worktreeName(%q) = %q; want %q", dir, got, want)
		}
	}
}

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := git(t.Context(), dir, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

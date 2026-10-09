package plan

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// TestDeleteWishWorktrees: deleting a wish, in a real Git repository, removes the worktree of a task that holds no
// work, even behind the project's current branch, and keeps one with a commit beyond it, one with a change not
// committed, and one with a file Git does not track, saying why. No branch goes.
func TestDeleteWishWorktrees(t *testing.T) {
	ctx := t.Context()
	repo := gitRepo(t, "app", "https://example.com/acme/app.git")
	git := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=Test", "-c", "user.email=test@example.com",
			"-c", "commit.gpgsign=false"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(path, text string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(repo, "README.md"), "# App\n")
	git(repo, "add", ".")
	git(repo, "commit", "-q", "-m", "First")
	base := git(repo, "rev-parse", "--abbrev-ref", "HEAD")

	c := serve(t)
	wish, err := c.make(t, "Clean up", false)
	if err != nil {
		t.Fatal(err)
	}
	project := &planv1.Project{Id: store.NewID(), Name: "app", Directory: repo}
	worktrees := resolvedTempDir(t)
	tasks := map[string]*planv1.Task{}
	for _, code := range []string{"W1", "W2", "W3", "W4"} {
		task := &planv1.Task{Id: store.NewID(), WishId: wish.GetId(), ProjectId: project.GetId(), Code: code,
			Title: "work", Status: planv1.TaskStatus_TASK_STATUS_DONE, Branch: strings.ToLower(code) + "-work",
			Worktree: filepath.Join(worktrees, code)}
		git(repo, "worktree", "add", "-q", "-b", task.GetBranch(), task.GetWorktree(), "HEAD")
		tasks[code] = task
		c.put(t, task)
	}
	c.put(t, project)
	write(filepath.Join(tasks["W2"].GetWorktree(), "done.txt"), "done\n")
	git(tasks["W2"].GetWorktree(), "add", ".")
	git(tasks["W2"].GetWorktree(), "commit", "-q", "-m", "Work")
	write(filepath.Join(tasks["W3"].GetWorktree(), "README.md"), "# App, changed\n")
	write(filepath.Join(tasks["W4"].GetWorktree(), "notes.txt"), "not tracked\n")
	// The project moves on: W1 is behind its current branch, with nothing of its own.
	write(filepath.Join(repo, "CHANGELOG.md"), "Later\n")
	git(repo, "add", ".")
	git(repo, "commit", "-q", "-m", "Later")

	res, err := c.wishes.Delete(ctx, connect.NewRequest(&planv1.WishServiceDeleteRequest{WishId: wish.GetId()}))
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Msg.GetWorktreesRemoved(); got != 1 {
		t.Errorf("removed %d worktrees, want 1", got)
	}
	if _, err := os.Stat(tasks["W1"].GetWorktree()); !os.IsNotExist(err) {
		t.Errorf("W1's clean worktree is still there: %v", err)
	}
	if list := git(repo, "worktree", "list", "--porcelain"); strings.Contains(list, tasks["W1"].GetWorktree()) {
		t.Errorf("git still lists W1's worktree:\n%s", list)
	}
	kept := map[string]*planv1.KeptWorktree{}
	for _, k := range res.Msg.GetKept() {
		kept[k.GetTaskCode()] = k
		if k.GetError() != "" || k.GetBase() != base || k.GetBranch() != tasks[k.GetTaskCode()].GetBranch() ||
			k.GetWorktree() != tasks[k.GetTaskCode()].GetWorktree() {
			t.Errorf("kept %v", k)
		}
		if _, err := os.Stat(k.GetWorktree()); err != nil {
			t.Errorf("%s's worktree is gone: %v", k.GetTaskCode(), err)
		}
	}
	for code, want := range map[string]struct {
		commits int32
		changed bool
	}{"W2": {1, false}, "W3": {0, true}, "W4": {0, true}} {
		if k := kept[code]; k == nil || k.GetCommits() != want.commits || k.GetChanged() != want.changed {
			t.Errorf("%s kept %v, want %d commits, changed %v", code, k, want.commits, want.changed)
		}
	}
	if len(kept) != 3 {
		t.Errorf("kept %v", res.Msg.GetKept())
	}
	for _, task := range tasks {
		git(repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+task.GetBranch())
	}
}

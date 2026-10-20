package harness

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestBranchName(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ template, code, title, want string }{
		{"", "W1", "Fix the login page", "w1-fix-the-login-page-89abcdef"},
		{"", "W12", "Café's naïve façade — über piñata!", "w12-cafe-s-naive-facade-uber-pinata-89abcdef"},
		{"", "W2", "A very long title that goes well beyond forty characters", "w2-a-very-long-title-that-goes-well-beyond-89abcdef"},
		{"", "W3", "!!!", "w3-89abcdef"},
		{"{code}-{slug}-{uuid8}", "W3", "!!!", "w3-89abcdef"},
		{"djinn/{code}_{uuid8}", "W4", "Fix it", "djinn/w4_89abcdef"},
		{"feature/{slug}/{uuid8}", "W5", "!!!", "feature/89abcdef"},
		{"{slug}.{code}.{uuid8}", "W6", "", "w6.89abcdef"},
		{"Team-{uuid8}", "W7", "Fix it", "Team-89abcdef"},
	} {
		if got := branchName(tt.template, tt.code, tt.title, "01234567-0123-7123-8123-0123456789abcdef"[0:28]+"89abcdef"); got != tt.want {
			t.Errorf("branchName(%q, %q, %q) = %q, want %q", tt.template, tt.code, tt.title, got, tt.want)
		}
	}
}

// gitRepo creates a Git repository with one commit, and returns its folder, symbolic links resolved. The repository
// sets its own identity, which its worktrees share: the test process has none (testx.HideGitIdentity).
func gitRepo(t *testing.T) string {
	t.Helper()
	return gitRepoWith(t, nil)
}

// gitRepoWith is gitRepo, its one commit holding files too, by slash-separated path.
func gitRepoWith(t *testing.T, files map[string]string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "app"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app", "README.md"), []byte("# App\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		writeFile(t, dir, name, content)
	}
	if _, err := git(t.Context(), dir, "init", "--quiet"); err != nil {
		t.Fatal(err)
	}
	// Written, not set with git config: each git costs 0.1 s on Windows. No maintenance after each of Djinn's commits.
	appendFile(t, filepath.Join(dir, ".git", "config"),
		"[user]\n\tname = Test\n\temail = test@example.com\n[maintenance]\n\tauto = false\n[gc]\n\tauto = 0\n")
	for _, args := range [][]string{{"add", "."}, {"commit", "--quiet", "-m", "First"}} {
		if _, err := git(t.Context(), dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestWorktree(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	path := worktreeDir(t.TempDir(), "p1", "t1")
	// The project is a folder inside the repository: the worker runs in the same folder of the worktree.
	dir, err := addWorktreeFrom(t.Context(), filepath.Join(repo, "app"), path, "w1-try-t1", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join(path, "app") {
		t.Errorf("dir = %s, want %s", dir, filepath.Join(path, "app"))
	}
	if _, err := os.Stat(filepath.Join(dir, "README.md")); err != nil {
		t.Errorf("the worktree lacks the project's files: %v", err)
	}
	if out, _ := git(t.Context(), repo, "branch", "--list", "w1-try-t1"); out == "" {
		t.Error("the branch was not created")
	}

	// A change not committed: removing needs force.
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removeWorktree(t.Context(), repo, path, false); err == nil {
		t.Error("removed a worktree with changes not committed")
	}
	if err := removeWorktree(t.Context(), repo, path, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the worktree is still there: %v", err)
	}
	if out, _ := git(t.Context(), repo, "branch", "--list", "w1-try-t1"); out == "" {
		t.Error("the branch went with the worktree")
	}
	if out, _ := git(t.Context(), repo, "status", "--porcelain"); out != "" {
		t.Errorf("the project's folder changed: %q", out)
	}

	// Deleted by hand before removeWorktree: git worktree list is still pruned cleanly.
	byHand := worktreeDir(t.TempDir(), "p1", "t2")
	if _, err := addWorktreeFrom(t.Context(), repo, byHand, "w2-t2", "HEAD"); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(byHand); err != nil {
		t.Fatal(err)
	}
	if err := removeWorktree(t.Context(), repo, byHand, false); err != nil {
		t.Fatal(err)
	}
	if out, _ := git(t.Context(), repo, "worktree", "list", "--porcelain"); strings.Contains(out, byHand) {
		t.Errorf("git still lists the worktree: %s", out)
	}
}

// TestWorktreeAddBesidePrune adds worktrees while git worktree prune runs over and over in the same repository, from
// another folder of it: no add fails, for a prune never deletes the folder of an add not finished (W53).
func TestWorktreeAddBesidePrune(t *testing.T) {
	t.Parallel()
	const pruners, adders, adds = 8, 4, 30
	repo := gitRepo(t)
	home := t.TempDir()
	stop := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, pruners+adders*adds)
	for range pruners {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				if err := removeWorktree(t.Context(), filepath.Join(repo, "app"), worktreeDir(home, "p1", "gone"), true); err != nil {
					errs <- fmt.Errorf("prune: %w", err)
					return
				}
			}
		})
	}
	var added sync.WaitGroup
	for w := range adders {
		added.Go(func() {
			for i := range adds {
				id := fmt.Sprintf("t%d-%d", w, i)
				if _, err := addWorktreeFrom(t.Context(), repo, worktreeDir(home, "p1", id), "w-"+id, "HEAD"); err != nil {
					errs <- fmt.Errorf("add %s: %w", id, err)
				}
			}
		})
	}
	added.Wait()
	close(stop)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

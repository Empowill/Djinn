package harness

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// gitRepo creates a Git repository with one commit, and returns its folder, symbolic links resolved.
func gitRepo(t *testing.T) string {
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
	for _, args := range [][]string{
		{"init", "--quiet"},
		{"add", "."},
		{"-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--quiet", "-m", "First"},
	} {
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
	dir, err := addWorktree(t.Context(), filepath.Join(repo, "app"), path, "w1-try-t1")
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
}

func TestWorktreeDeletedByHand(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	path := worktreeDir(t.TempDir(), "p1", "t1")
	if _, err := addWorktree(t.Context(), repo, path, "w1-t1"); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	if err := removeWorktree(t.Context(), repo, path, false); err != nil {
		t.Fatal(err)
	}
	if out, _ := git(t.Context(), repo, "worktree", "list", "--porcelain"); strings.Contains(out, path) {
		t.Errorf("git still lists the worktree: %s", out)
	}
}

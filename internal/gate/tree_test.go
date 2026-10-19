package gate

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWorktreeTree covers worktreeTree inside a git worktree (with tracked, untracked, and ignored files) and outside
// any git worktree.
func TestWorktreeTree(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	gitT(t, repo, "init", "--quiet")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitT(t, repo, "add", "a.txt")
	if err := os.WriteFile(filepath.Join(repo, "b.txt"), []byte("b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tree := worktreeTree(t.Context(), repo)
	if tree == "" {
		t.Fatal("worktreeTree returned empty tree for git repo")
	}
	if files := gitT(t, repo, "ls-tree", "--name-only", tree); files != "a.txt\nb.txt" {
		t.Errorf("ls-tree = %q, want a.txt and b.txt", files)
	}
	if got := worktreeTree(t.Context(), t.TempDir()); got != "" {
		t.Errorf("worktreeTree outside git repo = %q", got)
	}
}

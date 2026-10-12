package gate

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// worktreeTree is the git tree of what the worktree holding dir holds: its changes and its files not tracked
// included, the ignored ones left out, what a commit of all of it would hold. It is written through an index of its
// own, a copy of the worktree's, which stays as it is. "" when dir is not in a git worktree, or git fails.
func worktreeTree(ctx context.Context, dir string) string {
	index, err := gitIn(ctx, dir, nil, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil {
		return ""
	}
	tmp, err := os.MkdirTemp("", "djinn-gate-index-*")
	if err != nil {
		return ""
	}
	defer os.RemoveAll(tmp)
	// The worktree's index, copied, keeps what git knows of the files: only the changed ones are read again. A
	// worktree without one yet starts from no index.
	copied := filepath.Join(tmp, "index")
	if err := copyFile(index, copied); err != nil && !errors.Is(err, os.ErrNotExist) {
		return ""
	}
	env := append(os.Environ(), "GIT_INDEX_FILE="+copied)
	if _, err := gitIn(ctx, dir, env, "add", "--all"); err != nil {
		return ""
	}
	tree, err := gitIn(ctx, dir, env, "write-tree")
	if err != nil {
		return ""
	}
	return tree
}

// copyFile copies the file from to the new file to.
func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// gitIn runs git with args in dir, with env (nil: Djinn's), and returns what it wrote, trimmed.
func gitIn(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir, cmd.Env = dir, env
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

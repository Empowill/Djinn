package harness

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// worktreeDir is where a task's worktree goes: in Djinn's data folder, on the project's side, named after the
// task. Djinn writes nothing in the project's folder but what git worktree add records under .git/worktrees.
func worktreeDir(home, projectID, taskID string) string {
	return filepath.Join(home, "projects", projectID, "worktrees", taskID)
}

// branchName is the default branch of a task: <task-code>-<slug>-<uuid8>, in lower case, the last 8 characters
// of its UUIDv7 being the random part.
// scratchDir is the folder of a task outside any project, under Djinn's data folder.
func scratchDir(home, taskID string) string {
	return filepath.Join(home, "tasks", taskID)
}

func branchName(code, title, taskID string) string {
	parts := []string{}
	for _, p := range []string{slug(code, 20), slug(title, 40), taskID[max(0, len(taskID)-8):]} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, "-")
}

// accents are folded to their letter in a slug.
var accents = strings.NewReplacer(
	"à", "a", "â", "a", "ä", "a", "á", "a", "ç", "c", "é", "e", "è", "e", "ê", "e", "ë", "e",
	"î", "i", "ï", "i", "í", "i", "ô", "o", "ö", "o", "ó", "o", "ù", "u", "û", "u", "ü", "u", "ú", "u", "ñ", "n",
)

// slug keeps lower-case letters and digits, joined by single dashes, at most n characters cut on a dash.
func slug(s string, n int) string {
	s = accents.Replace(strings.ToLower(s))
	var b strings.Builder
	dash := false
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			dash = false
			continue
		}
		dash = true
	}
	out := b.String()
	if len(out) > n {
		out = out[:n]
		if i := strings.LastIndexByte(out, '-'); i > 0 {
			out = out[:i]
		}
	}
	return strings.Trim(out, "-")
}

// git runs git in dir and returns its output, trimmed.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errOut.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", args[0], msg)
	}
	return strings.TrimSpace(out.String()), nil
}

// addWorktree creates the worktree path of the repository holding dir, on a new branch from its HEAD. It returns
// the folder the worker runs in: the project's own folder within the worktree.
func addWorktree(ctx context.Context, dir, path, branch string) (string, error) {
	prefix, err := git(ctx, dir, "rev-parse", "--show-prefix")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if _, err := git(ctx, dir, "worktree", "add", "--quiet", "-b", branch, path, "HEAD"); err != nil {
		return "", err
	}
	return filepath.Join(path, filepath.FromSlash(prefix)), nil
}

// removeWorktree removes the worktree path of the repository holding dir; its branch stays. Without force, git
// refuses a worktree with changes not committed. A worktree already deleted by hand is forgotten.
func removeWorktree(ctx context.Context, dir, path string, force bool) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		_, err := git(ctx, dir, "worktree", "prune")
		return err
	}
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	_, err := git(ctx, dir, append(args, path)...)
	return err
}

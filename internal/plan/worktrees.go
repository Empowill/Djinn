package plan

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// cleanWorktree removes the worktree of a task of a deleted wish, from the repository of dir, its project's folder,
// unless it holds work: a commit of its HEAD or its branch that the project's current branch does not have, or a
// change not committed. It uses git worktree remove, never --force, and never deletes a branch. It returns whether
// it removed one, or what keeps it on disk; a task without a worktree, or one already gone, gives neither.
func cleanWorktree(ctx context.Context, dir string, task *planv1.Task) (bool, *planv1.KeptWorktree) {
	path := task.GetWorktree()
	if path == "" {
		return false, nil
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	kept := &planv1.KeptWorktree{TaskCode: task.GetCode(), Worktree: path, Branch: task.GetBranch()}
	fail := func(err error) (bool, *planv1.KeptWorktree) {
		kept.Error = err.Error()
		return false, kept
	}
	if !isFolder(dir) {
		return fail(fmt.Errorf("the project's folder %q is not on this machine", dir))
	}
	base, err := gitOut(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return fail(err)
	}
	kept.Base = base
	head, err := gitOut(ctx, path, "rev-parse", "HEAD")
	if err != nil {
		return fail(err)
	}
	tips := []string{head}
	if b := task.GetBranch(); b != "" {
		if _, err := gitOut(ctx, dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+b); err == nil {
			tips = append(tips, "refs/heads/"+b)
		}
	}
	count, err := gitOut(ctx, dir, append(append([]string{"rev-list", "--count"}, tips...), "--not", "HEAD")...)
	if err != nil {
		return fail(err)
	}
	commits, err := strconv.ParseInt(count, 10, 32)
	if err != nil {
		return fail(fmt.Errorf("git rev-list: %q is not a count", count))
	}
	kept.Commits = int32(commits)
	status, err := gitOut(ctx, path, "status", "--porcelain")
	if err != nil {
		return fail(err)
	}
	kept.Changed = status != ""
	if kept.GetCommits() > 0 || kept.GetChanged() {
		return false, kept
	}
	if _, err := gitOut(ctx, dir, "worktree", "remove", path); err != nil {
		return fail(err)
	}
	return true, nil
}

// gitOut runs git in dir and returns its output, trimmed; an error carries git's own message.
func gitOut(ctx context.Context, dir string, args ...string) (string, error) {
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

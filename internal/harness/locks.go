package harness

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/store"
)

// A git that dies before it ends, killed or with Djinn, leaves its index.lock, and every git after it in that worktree
// fails: "Unable to create index.lock: File exists" (W52). Djinn takes such locks away in the worktrees it made, where
// only its own gits run, when none can still hold them: at start, before any worker or pass (unlockWorktrees), and in
// an integration worktree before its reset, its caller holding it alone (integrationWorktree). Never in the person's
// checkout: a lock there may be their git's, a commit waiting for its message; the restart line names it instead.

// gitDir is the folder git keeps the worktree wt's own state in: .git for a main checkout, the folder its .git file
// names for a linked worktree. "" when wt has none.
func gitDir(wt string) string {
	dotGit := filepath.Join(wt, ".git")
	info, err := os.Stat(dotGit)
	switch {
	case err != nil:
		return ""
	case info.IsDir():
		return dotGit
	}
	b, err := os.ReadFile(dotGit)
	if err != nil {
		return ""
	}
	dir, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir: ")
	if !ok {
		return ""
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(wt, dir)
	}
	return filepath.Clean(dir)
}

// unlockWorktree takes away the index.lock of the worktree wt, which no git of Djinn's holds now, unless a process
// works in wt (busyIn, Linux only): it may be a git started by hand. It tells whether it took one away.
func unlockWorktree(wt string) bool {
	dir := gitDir(wt)
	if dir == "" {
		return false
	}
	lock := filepath.Join(dir, "index.lock")
	if _, err := os.Stat(lock); err != nil {
		return false
	}
	if pid := busyIn(wt); pid != 0 {
		logx.L().Warn("an index.lock stays: a process works in the worktree", "worktree", wt, "pid", pid)
		return false
	}
	if err := os.Remove(lock); err != nil && !errors.Is(err, os.ErrNotExist) {
		logx.L().Warn("take a dead git's index.lock away", "worktree", wt, "err", err)
		return false
	}
	logx.L().Warn("took a dead git's index.lock away", "worktree", wt)
	return true
}

// unlockWorktrees readies the worktrees Djinn made in its data folder after a djinn up that may have stopped a git half
// way, before any worker or pass starts: it takes their index.lock away; in its integration and install worktrees,
// where Djinn owns the merge, it aborts one left half done. A worker's worktree keeps its merge: a correction's starts
// on its conflict, left for its worker. It returns the person's checkouts left with an index.lock, untouched.
func (h *Harness) unlockWorktrees(ctx context.Context) []string {
	if h.home == "" {
		return nil
	}
	projects, err := store.List[*planv1.Project](ctx, h.store, nil)
	if err != nil {
		return nil
	}
	root := filepath.Join(h.home, "projects")
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	var theirs []string
	for _, p := range projects {
		if !p.GetGit() || p.GetDirectory() == "" {
			continue
		}
		out, err := git(ctx, p.GetDirectory(), "worktree", "list", "--porcelain")
		if err != nil {
			continue
		}
		for line := range strings.Lines(out) {
			wt, ok := strings.CutPrefix(strings.TrimSpace(line), "worktree ")
			if !ok {
				continue
			}
			wt = filepath.Clean(wt)
			if resolved, err := filepath.EvalSymlinks(wt); err == nil {
				wt = resolved
			}
			if !strings.HasPrefix(wt+string(filepath.Separator), root+string(filepath.Separator)) {
				if dir := gitDir(wt); dir != "" {
					if _, err := os.Stat(filepath.Join(dir, "index.lock")); err == nil && !slices.Contains(theirs, wt) {
						logx.L().Warn("an index.lock is left in a checkout Djinn did not make: it stays", "checkout", wt)
						theirs = append(theirs, wt)
					}
				}
				continue
			}
			unlockWorktree(wt)
			rel, err := filepath.Rel(filepath.Join(root, p.GetId()), wt)
			if first, _, _ := strings.Cut(filepath.ToSlash(rel), "/"); err != nil || first != "integration" && first != "install" {
				continue
			}
			if _, err := os.Stat(filepath.Join(gitDir(wt), "MERGE_HEAD")); err != nil {
				continue
			}
			if _, err := git(ctx, wt, "merge", "--abort"); err != nil {
				logx.L().Warn("abort a merge left half done", "worktree", wt, "err", err)
			} else {
				logx.L().Warn("aborted a merge left half done", "worktree", wt)
			}
		}
	}
	return theirs
}

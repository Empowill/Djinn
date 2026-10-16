// Package gitx starts the git commands Djinn runs, each with a deadline: a git that talks to a remote (over ssh, a
// stalled connection or a prompt nobody sees) or waits on a lock never holds Djinn for good (W504).
package gitx

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/empowill/djinn/internal/logx"
)

var (
	// Local is the longest a git command that stays in the repository may run: a merge or a checkout of a large one.
	Local = 10 * time.Minute
	// Network is the longest a git command that reaches a remote may run: fetch, push, ls-remote.
	Network = 5 * time.Minute
	// WaitDelay is how long a git stopped at its deadline or at its context's end has to end on SIGTERM, and then, once
	// killed, may keep its output open: an ssh it started may hold it.
	WaitDelay = 5 * time.Second
	// Finish is how long a git that changes a repository may run on once Djinn stops (Uncut), so that the stop leaves no
	// half a move; with WaitDelay, it stays well within the 30 s djinn up gives itself to stop.
	Finish = 15 * time.Second
)

// network names the git commands that reach a remote.
var network = map[string]bool{"fetch": true, "push": true, "ls-remote": true, "pull": true, "clone": true}

// Command is git with args, run in dir, never prompting for credentials, stopped at the earlier of ctx's end and its
// own deadline: Network for a command that reaches a remote, Local otherwise. Stopped, git gets SIGTERM, which lets it
// take its locks away (index.lock), and SIGKILL only WaitDelay later: killed first, it left them, and every later git
// in that worktree failed (W52). Call cancel once it has run: it logs it.
func Command(ctx context.Context, dir string, args ...string) (cmd *exec.Cmd, cancel context.CancelFunc) {
	limit := Local
	if len(args) > 0 && network[args[0]] {
		limit = Network
	}
	ctx, stop := context.WithTimeout(ctx, limit)
	cmd = exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	cmd.Cancel = func() error {
		if cut, ok := ctx.Value(cutKey{}).(func(string, string)); ok && errors.Is(ctx.Err(), context.Canceled) && len(args) > 0 {
			cut("git "+args[0], dir)
		}
		return terminate(cmd.Process)
	}
	cmd.WaitDelay = WaitDelay
	start := time.Now()
	return cmd, func() {
		// The log tells how long each git ran, how it exited, and which one met its deadline (W511).
		logx.Ran(ctx, cmd, start, nil)
		stop()
	}
}

// terminate asks p to end: SIGTERM, which Windows has not, where it is killed.
func terminate(p *os.Process) error {
	if runtime.GOOS == "windows" {
		return p.Kill()
	}
	return p.Signal(syscall.SIGTERM)
}

type cutKey struct{}

// OnCut returns ctx telling cut of each git a cancel of ctx stops before it ends: what it ran, "git reset", and the
// folder it ran in. A git that meets its deadline is not told.
func OnCut(ctx context.Context, cut func(what, dir string)) context.Context {
	return context.WithValue(ctx, cutKey{}, cut)
}

// Uncut is ctx without its cancel, for a git that changes a repository, or steps that must not stop half way (a merge
// and its abort, a branch moved and the checkout that follows it, or put back): a stop lets them end, for Finish at
// most past ctx's end, after which git is stopped as at a cancel, for ctx's cause. A deadline of ctx still holds, and
// so do its values. Call done once they ran.
func Uncut(ctx context.Context) (kept context.Context, done context.CancelFunc) {
	finish := Finish
	base, stopDeadline := context.WithoutCancel(ctx), context.CancelFunc(func() {})
	if deadline, ok := ctx.Deadline(); ok {
		base, stopDeadline = context.WithDeadline(base, deadline)
	}
	kept, cancel := context.WithCancelCause(base)
	var mu sync.Mutex
	var timer *time.Timer
	stop := context.AfterFunc(ctx, func() {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return // The deadline ends kept by itself.
		}
		mu.Lock()
		defer mu.Unlock()
		timer = time.AfterFunc(finish, func() { cancel(context.Cause(ctx)) })
	})
	return kept, func() {
		stop()
		mu.Lock()
		if timer != nil {
			timer.Stop()
		}
		mu.Unlock()
		cancel(nil)
		stopDeadline()
	}
}

// FetchBranch fetches branch from remote into its remote-tracking ref, refs/remotes/<remote>/<branch>, with the
// person's own credentials: what the remote holds of it now. It returns that commit; "" when the remote has no such
// branch, its remote-tracking ref then left as it was; an error when the remote did not answer.
func FetchBranch(ctx context.Context, dir, remote, branch string) (string, error) {
	ref := "refs/remotes/" + remote + "/" + branch
	cmd, cancel := Command(ctx, dir, "fetch", "--no-tags", remote, "+refs/heads/"+branch+":"+ref)
	cmd.Env = append(cmd.Env, "LC_ALL=C")
	out, err := cmd.CombinedOutput()
	cancel()
	if err != nil {
		if strings.Contains(string(out), "couldn't find remote ref") {
			return "", nil
		}
		return "", fmt.Errorf("fetch %s from %s: %s", branch, remote, cmp.Or(strings.TrimSpace(string(out)), err.Error()))
	}
	read, cancel := Command(ctx, dir, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	sha, err := read.Output()
	cancel()
	if err != nil {
		return "", fmt.Errorf("read %s: %w", ref, err)
	}
	return strings.TrimSpace(string(sha)), nil
}

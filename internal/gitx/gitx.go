// Package gitx starts the git commands Djinn runs, each with a deadline: a git that talks to a remote (over ssh, a
// stalled connection or a prompt nobody sees) or waits on a lock never holds Djinn for good (W504).
package gitx

import (
	"context"
	"os"
	"os/exec"
	"time"

	"github.com/empowill/djinn/internal/logx"
)

var (
	// Local is the longest a git command that stays in the repository may run: a merge or a checkout of a large one.
	Local = 10 * time.Minute
	// Network is the longest a git command that reaches a remote may run: fetch, push, ls-remote.
	Network = 5 * time.Minute
	// WaitDelay is how long a git killed at its deadline may keep its output open: an ssh it started may hold it.
	WaitDelay = 5 * time.Second
)

// network names the git commands that reach a remote.
var network = map[string]bool{"fetch": true, "push": true, "ls-remote": true, "pull": true, "clone": true}

// Command is git with args, run in dir, never prompting for credentials, killed at the earlier of ctx's deadline and
// its own: Network for a command that reaches a remote, Local otherwise. Call cancel once it has run: it logs it.
func Command(ctx context.Context, dir string, args ...string) (cmd *exec.Cmd, cancel context.CancelFunc) {
	limit := Local
	if len(args) > 0 && network[args[0]] {
		limit = Network
	}
	ctx, stop := context.WithTimeout(ctx, limit)
	cmd = exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	cmd.WaitDelay = WaitDelay
	start := time.Now()
	return cmd, func() {
		// The log tells how long each git ran, how it exited, and which one met its deadline (W511).
		logx.Ran(ctx, cmd, start, nil)
		stop()
	}
}

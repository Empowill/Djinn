//go:build !windows

package harness

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/empowill/djinn/internal/testx"
)

// TestKilledResetLeavesALockTheNextPassTakes: a git killed mid-reset in an integration worktree leaves its
// index.lock, and every reset there failed after it (W52); the next pass takes the dead git's lock away, and resets.
func TestKilledResetLeavesALockTheNextPassTakes(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	first := commitOf(t.Context(), repo, "HEAD")
	writeFile(t, repo, "app/README.md", "# App, second\n")
	commitAll(t, repo, "Second")
	second := commitOf(t.Context(), repo, "HEAD")
	wt := integrationDir(t.TempDir(), "p", "w")
	if _, _, err := integrationWorktree(t.Context(), repo, wt, second); err != nil {
		t.Fatal(err)
	}

	hold := testx.HoldCheckout(t, filepath.Join(repo, ".git"))
	reset := exec.Command("git", "-C", wt, "reset", "--quiet", "--hard", first)
	reset.Env = append(os.Environ(), hold.Env...)
	reset.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := reset.Start(); err != nil {
		t.Fatal(err)
	}
	hold.Held(t)
	_ = syscall.Kill(-reset.Process.Pid, syscall.SIGKILL) // git and its filter, as a stop killed them before W84.
	_ = reset.Wait()
	lock := filepath.Join(gitDir(wt), "index.lock")
	if _, err := os.Stat(lock); err != nil {
		t.Fatalf("the killed reset left no index.lock: %v", err)
	}
	if _, err := git(t.Context(), wt, "reset", "--quiet", "--hard", first); err == nil || !strings.Contains(err.Error(), "index.lock") {
		t.Fatalf("a reset after the killed one: %v; want it refused for the lock", err)
	}

	if _, _, err := integrationWorktree(t.Context(), repo, wt, first); err != nil {
		t.Fatalf("the next pass: %v", err)
	}
	if got := commitOf(t.Context(), wt, "HEAD"); got != first {
		t.Errorf("the worktree is at %s; want %s", short8(got), short8(first))
	}
	if b, err := os.ReadFile(filepath.Join(wt, "app", "README.md")); err != nil || string(b) != "# App\n" {
		t.Errorf("README.md = %q, %v; want the first commit's", b, err)
	}
	if _, err := os.Stat(lock); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("index.lock still there: %v", err)
	}
}

// heldRefs holds the first ref change git commits in repo, until let is called: a stop then comes between two steps
// of a move. It returns a channel closed once one is held.
func heldRefs(t *testing.T, repo string) (held <-chan struct{}, let func()) {
	t.Helper()
	dir := t.TempDir()
	hook := "#!/bin/sh\n[ \"$1\" = committed ] && [ ! -e '" + dir + "/held' ] || exit 0\n: > '" + dir + "/held'\n" +
		"while [ ! -e '" + dir + "/go' ]; do sleep 0.01; done\n"
	if err := os.WriteFile(filepath.Join(repo, ".git", "hooks", "reference-transaction"), []byte(hook), 0o700); err != nil {
		t.Fatal(err)
	}
	c := make(chan struct{})
	stop := make(chan struct{})
	go func() {
		defer close(c)
		for {
			if _, err := os.Stat(filepath.Join(dir, "held")); err == nil {
				return
			}
			select {
			case <-stop:
				return
			case <-time.After(2 * time.Millisecond):
			}
		}
	}()
	let = func() { _ = os.WriteFile(filepath.Join(dir, "go"), nil, 0o600) }
	t.Cleanup(func() { close(stop); let() })
	return c, let
}

// TestMoveBranchEndsWholeAfterACancel: a stop between moving a branch and its checkout following it lets the move end
// whole: the checkout follows; or, when it cannot, the branch goes back, where the put back once failed on the cancel
// and the person's checkout showed the work undone (W52).
func TestMoveBranchEndsWholeAfterACancel(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name     string
		inTheWay bool
	}{{"the checkout follows", false}, {"the branch goes back", true}} {
		t.Run(c.name, func(t *testing.T) {
			repo := gitRepo(t)
			branch := strings.TrimSpace(mustGit(t, repo, "branch", "--show-current"))
			old := commitOf(t.Context(), repo, "HEAD")
			writeFile(t, repo, "app/new.txt", "new\n")
			commitAll(t, repo, "New")
			sha := commitOf(t.Context(), repo, "HEAD")
			mustGit(t, repo, "reset", "--quiet", "--hard", old)
			if c.inTheWay {
				writeFile(t, repo, "app/new.txt", "mine, not tracked\n")
			}
			held, let := heldRefs(t, repo)
			ctx, stop := context.WithCancel(t.Context())
			defer stop()
			type moved struct{ holder, why string }
			got := make(chan moved, 1)
			go func() {
				holder, why, _ := moveBranch(ctx, repo, branch, old, sha, "djinn: test")
				got <- moved{holder, why}
			}()
			<-held // The branch moved; its checkout has not followed yet.
			stop()
			let()
			m := <-got
			at := commitOf(t.Context(), repo, "refs/heads/"+branch)
			if c.inTheWay {
				if m.why == "" || at != old {
					t.Errorf("why %q, branch at %s; want it put back at %s", m.why, short8(at), short8(old))
				}
				return
			}
			if m.why != "" || at != sha || m.holder == "" {
				t.Errorf("why %q, branch at %s, holder %q; want it at %s, its checkout following", m.why, short8(at), m.holder, short8(sha))
			}
			if _, err := os.Stat(filepath.Join(repo, "app", "new.txt")); err != nil {
				t.Errorf("the checkout did not follow: %v", err)
			}
		})
	}
}

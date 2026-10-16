//go:build !windows

package gitx

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/empowill/djinn/internal/testx"
)

// twoCommits is a repository whose HEAD changes f.txt from its parent; a reset to HEAD~1 writes it.
func twoCommits(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range []func() error{
		func() error { return run(t, repo, "init", "--quiet") },
		func() error { return run(t, repo, "config", "user.name", "Test") },
		func() error { return run(t, repo, "config", "user.email", "test@example.com") },
		func() error { return os.WriteFile(filepath.Join(repo, "f.txt"), []byte("a\n"), 0o600) },
		func() error { return run(t, repo, "add", ".") },
		func() error { return run(t, repo, "commit", "--quiet", "-m", "a") },
		func() error { return os.WriteFile(filepath.Join(repo, "f.txt"), []byte("b\n"), 0o600) },
		func() error { return run(t, repo, "commit", "--quiet", "-am", "b") },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

func run(t *testing.T, dir string, args ...string) error {
	cmd, cancel := Command(t.Context(), dir, args...)
	defer cancel()
	return cmd.Run()
}

// cuts records what OnCut tells.
type cuts struct {
	mu   sync.Mutex
	what []string
}

func (c *cuts) add(what, dir string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.what = append(c.what, what+" in "+dir)
}

func (c *cuts) told() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.what)
}

// TestStopTermsGit: a git a stop cuts mid-reset gets SIGTERM, takes its index.lock away, and is told as cut; killed,
// it left the lock, and every later reset there failed (W52).
func TestStopTermsGit(t *testing.T) {
	repo := twoCommits(t)
	hold := testx.HoldCheckout(t, filepath.Join(repo, ".git"))
	var c cuts
	ctx, stop := context.WithCancel(OnCut(t.Context(), c.add))
	defer stop()
	cmd, cancel := Command(ctx, repo, "reset", "--quiet", "--hard", "HEAD~1")
	defer cancel()
	cmd.Env = append(cmd.Env, hold.Env...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	hold.Held(t)
	if _, err := os.Stat(filepath.Join(repo, ".git", "index.lock")); err != nil {
		t.Fatalf("the held reset holds no index.lock: %v", err)
	}
	stop()
	err := cmd.Wait()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGTERM {
		t.Errorf("the reset ended with %v; want SIGTERM", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".git", "index.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("index.lock left after SIGTERM: %v", err)
	}
	if got, want := c.told(), []string{"git reset in " + repo}; !slices.Equal(got, want) {
		t.Errorf("cut %q; want %q", got, want)
	}
}

// TestUncutLetsGitEnd: a git under Uncut ends what it began though its context ends, and nothing is told as cut; one
// still running Finish later is stopped, and told.
func TestUncutLetsGitEnd(t *testing.T) {
	repo := twoCommits(t)
	hold := testx.HoldCheckout(t, filepath.Join(repo, ".git"))
	var c cuts
	ctx, stop := context.WithCancel(OnCut(t.Context(), c.add))
	kept, done := Uncut(ctx)
	defer done()
	cmd, cancel := Command(kept, repo, "reset", "--quiet", "--hard", "HEAD~1")
	defer cancel()
	cmd.Env = append(cmd.Env, hold.Env...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	hold.Held(t)
	stop()
	time.Sleep(20 * time.Millisecond) // Long enough for a cancel to reach git, were it not uncut.
	hold.Release()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("the uncut reset failed: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(repo, "f.txt")); err != nil || string(b) != "a\n" {
		t.Errorf("f.txt = %q, %v; want the reset done", b, err)
	}
	if got := c.told(); len(got) != 0 {
		t.Errorf("an uncut git told as cut: %q", got)
	}

	finish := Finish
	Finish = 50 * time.Millisecond
	t.Cleanup(func() { Finish = finish })
	repo = twoCommits(t)
	hold = testx.HoldCheckout(t, filepath.Join(repo, ".git"))
	ctx, stop = context.WithCancel(OnCut(t.Context(), c.add))
	defer stop()
	kept, done = Uncut(ctx)
	defer done()
	cmd, cancel = Command(kept, repo, "reset", "--quiet", "--hard", "HEAD~1")
	defer cancel()
	cmd.Env = append(cmd.Env, hold.Env...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	hold.Held(t)
	stop()
	if err := cmd.Wait(); err == nil {
		t.Error("a git held past Finish ended well")
	}
	if got, want := c.told(), []string{"git reset in " + repo}; !slices.Equal(got, want) {
		t.Errorf("cut %q; want %q", got, want)
	}
}

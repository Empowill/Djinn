package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
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

// output is the trimmed output of git with args in dir.
func output(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd, cancel := Command(t.Context(), dir, args...)
	defer cancel()
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out))
}

// heldReset starts, under ctx, a reset of a fresh twoCommits repository that holds mid-way through writing f.txt,
// its index.lock taken. The filter is a shell script: Unix only.
func heldReset(ctx context.Context, t *testing.T) (repo string, cmd *exec.Cmd, hold *testx.HeldCheckout) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the held git is a shell script")
	}
	repo = twoCommits(t)
	hold = testx.HoldCheckout(t, filepath.Join(repo, ".git"))
	cmd, cancel := Command(ctx, repo, "reset", "--quiet", "--hard", "HEAD~1")
	t.Cleanup(cancel)
	cmd.Env = append(cmd.Env, hold.Env...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	hold.Held(t)
	return repo, cmd, hold
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

// set gives *v the value d until the test ends.
func set[T any](t *testing.T, v *T, d T) {
	old := *v
	*v = d
	t.Cleanup(func() { *v = old })
}

// TestCommand: a git that reaches a remote is killed at Network, its output let go after WaitDelay though a child it
// started holds it, as an ssh does; one that stays in the repository runs as before; a stop cuts git with SIGTERM,
// which takes its index.lock away: killed, it left the lock, and every later reset there failed (W52).
func TestCommand(t *testing.T) {
	for _, tt := range []struct {
		name string
		test func(t *testing.T)
	}{
		{"a fetch ends at the Network deadline", func(t *testing.T) {
			if runtime.GOOS == "windows" {
				t.Skip("the hanging git is a shell script")
			}
			realGit, err := exec.LookPath("git")
			if err != nil {
				t.Fatal(err)
			}
			bin := t.TempDir()
			script := fmt.Sprintf("#!/bin/sh\nif [ \"$3\" = fetch ]; then sleep 30 & wait; fi\nexec %q \"$@\"\n", realGit)
			if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			set(t, &Network, 100*time.Millisecond)
			set(t, &WaitDelay, 50*time.Millisecond)
			cmd, cancel := Command(t.Context(), t.TempDir(), "fetch", "origin")
			defer cancel()
			if !slices.Contains(cmd.Env, "GIT_TERMINAL_PROMPT=0") {
				t.Error("git may prompt for credentials nobody sees")
			}
			var out bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &out
			start := time.Now()
			if err := cmd.Run(); err == nil {
				t.Error("a fetch past its deadline did not fail")
			}
			if d := time.Since(start); d > 5*time.Second {
				t.Errorf("a fetch killed at %v ended after %v", Network, d)
			}
		}},
		{"a local command runs", func(t *testing.T) {
			cmd, cancel := Command(t.Context(), t.TempDir(), "version")
			defer cancel()
			if out, err := cmd.Output(); err != nil || !bytes.HasPrefix(out, []byte("git version")) {
				t.Errorf("git version = %q, %v", out, err)
			}
		}},
		{"a stop terms git, which takes its lock away", func(t *testing.T) {
			ctx, stop := context.WithCancel(t.Context())
			defer stop()
			repo, cmd, _ := heldReset(ctx, t)
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
		}},
	} {
		t.Run(tt.name, tt.test)
	}
}

// expiring is a context that meets its deadline when expire is called.
type expiring struct {
	context.Context
	done chan struct{}
}

func (e expiring) Done() <-chan struct{} { return e.done }

func (e expiring) Err() error {
	select {
	case <-e.done:
		return context.DeadlineExceeded
	default:
		return nil
	}
}

func (e expiring) expire() { close(e.done) }

// TestOnCut: a git a cancel stops is told as cut, with the folder it ran in; one that meets its deadline is not.
func TestOnCut(t *testing.T) {
	for _, tt := range []struct {
		name     string
		deadline bool // the git meets its deadline instead of a cancel
	}{
		{name: "a cancel tells it"},
		{name: "a deadline tells nothing", deadline: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var c cuts
			ctx, stop := context.WithCancel(OnCut(t.Context(), c.add))
			defer stop()
			end := stop
			if tt.deadline {
				e := expiring{ctx, make(chan struct{})}
				ctx, end = e, e.expire
			}
			repo, cmd, _ := heldReset(ctx, t)
			end()
			if err := cmd.Wait(); err == nil {
				t.Error("a stopped git ended well")
			}
			var want []string
			if !tt.deadline {
				want = []string{"git reset in " + repo}
			}
			if got := c.told(); !slices.Equal(got, want) {
				t.Errorf("cut %q; want %q", got, want)
			}
		})
	}
}

// TestUncut: a git under Uncut ends what it began though its context ends, and nothing is told as cut; one still
// running Finish later is stopped, and told. A deadline of the context still holds, and so do its values.
func TestUncut(t *testing.T) {
	type key struct{}
	for _, tt := range []struct {
		name string
		test func(t *testing.T)
	}{
		{"a git ends what it began", func(t *testing.T) {
			var c cuts
			ctx, stop := context.WithCancel(OnCut(t.Context(), c.add))
			defer stop()
			kept, done := Uncut(ctx)
			defer done()
			repo, cmd, hold := heldReset(kept, t)
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
		}},
		{"a git past Finish is stopped and told", func(t *testing.T) {
			set(t, &Finish, 50*time.Millisecond)
			var c cuts
			ctx, stop := context.WithCancelCause(OnCut(t.Context(), c.add))
			defer stop(nil)
			kept, done := Uncut(ctx)
			defer done()
			repo, cmd, _ := heldReset(kept, t)
			cause := errors.New("djinn stops")
			stop(cause)
			if err := cmd.Wait(); err == nil {
				t.Error("a git held past Finish ended well")
			}
			if got, want := c.told(), []string{"git reset in " + repo}; !slices.Equal(got, want) {
				t.Errorf("cut %q; want %q", got, want)
			}
			if got := context.Cause(kept); !errors.Is(got, cause) {
				t.Errorf("kept ended for %v; want %v", got, cause)
			}
		}},
		{"a deadline still holds", func(t *testing.T) {
			ctx, stop := context.WithTimeout(t.Context(), 20*time.Millisecond)
			defer stop()
			kept, done := Uncut(ctx)
			defer done()
			want, _ := ctx.Deadline()
			if d, ok := kept.Deadline(); !ok || !d.Equal(want) {
				t.Errorf("kept's deadline = %v, %v; want ctx's", d, ok)
			}
			select {
			case <-kept.Done():
			case <-time.After(5 * time.Second):
				t.Fatal("kept outlived its deadline")
			}
			if !errors.Is(kept.Err(), context.DeadlineExceeded) {
				t.Errorf("kept ended with %v; want its deadline", kept.Err())
			}
		}},
		{"values are kept, and done ends it", func(t *testing.T) {
			kept, done := Uncut(context.WithValue(t.Context(), key{}, "v"))
			if got := kept.Value(key{}); got != "v" {
				t.Errorf("value = %v; want v", got)
			}
			done()
			if kept.Err() == nil {
				t.Error("kept still runs after done")
			}
		}},
	} {
		t.Run(tt.name, tt.test) // Not side by side: a case sets Finish.
	}
}

// TestFetchBranch: the branch the remote holds is fetched into its remote-tracking ref; a branch it lacks is "",
// that ref deleted when it was cached, the remote having deleted it (W81); a remote that does not answer is an error
// that says which.
func TestFetchBranch(t *testing.T) {
	remote := twoCommits(t)
	if err := run(t, remote, "branch", "feat"); err != nil {
		t.Fatal(err)
	}
	tip := output(t, remote, "rev-parse", "HEAD")
	clone := t.TempDir()
	for _, args := range [][]string{
		{"init", "--quiet"}, {"remote", "add", "origin", remote}, {"remote", "add", "gone", filepath.Join(clone, "none")},
		{"fetch", "--quiet", "origin", "+refs/heads/feat:refs/remotes/origin/merged"}, // Cached, deleted since.
	} {
		if err := run(t, clone, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, tt := range []struct {
		name, remote, branch string
		want, wantErr        string
	}{
		{name: "a branch the remote holds", remote: "origin", branch: "feat", want: tip},
		{name: "a branch the remote lacks", remote: "origin", branch: "ghost"},
		{name: "a branch the remote deleted", remote: "origin", branch: "merged"},
		{name: "a remote that does not answer", remote: "gone", branch: "feat", wantErr: "fetch feat from gone: "},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FetchBranch(t.Context(), clone, tt.remote, tt.branch)
			if got != tt.want || (err == nil) != (tt.wantErr == "") || (err != nil && !strings.HasPrefix(err.Error(), tt.wantErr)) {
				t.Errorf("FetchBranch(%s, %s) = %q, %v; want %q, %q", tt.remote, tt.branch, got, err, tt.want, tt.wantErr)
			}
			ref := "refs/remotes/" + tt.remote + "/" + tt.branch
			if tt.want != "" {
				if got := output(t, clone, "rev-parse", ref); got != tt.want {
					t.Errorf("%s is %s; want %s", ref, got, tt.want)
				}
			} else if run(t, clone, "rev-parse", "--verify", "--quiet", ref) == nil {
				t.Errorf("%s was made", ref)
			}
		})
	}
}

package testx

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestHoldCheckout checks that a git started with the held environment stops in the middle of writing a file, its
// filter's process named, and writes it whole once released.
func TestHoldCheckout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the filter is a shell script: Unix only")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	t.Parallel()
	repo := t.TempDir()
	git := func(env []string, args ...string) *exec.Cmd {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(), env...)
		return cmd
	}
	file := filepath.Join(repo, "f")
	if err := os.WriteFile(file, []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "--quiet"}, {"config", "user.name", "Test"},
		{"config", "user.email", "test@example.com"}, {"add", "f"}, {"commit", "--quiet", "-m", "x"}} {
		if out, err := git(nil, args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	h := HoldCheckout(t, filepath.Join(repo, ".git"))
	checkout := git(h.Env, "checkout", "--", "f")
	if err := checkout.Start(); err != nil {
		t.Fatal(err)
	}
	if pid := h.Held(t); pid <= 0 {
		t.Errorf("held filter pid %d", pid)
	}
	if _, err := os.Stat(filepath.Join(repo, ".git", "index.lock")); err != nil {
		t.Errorf("index.lock while held: %v", err)
	}
	h.Release()
	h.Release() // A second release, as the test's end makes, is a no-op.
	if err := checkout.Wait(); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(file); err != nil || string(b) != "one\n" {
		t.Errorf("f after release = %q, %v; want one", b, err)
	}
}

//go:build !windows

package harness

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestRunCommandEndsWithWhatItStarted: a project's check stopped as Djinn stops ends at once with the test runner it
// started, which held its output: Djinn's shutdown waited on it for as long as the tests ran (W504).
func TestRunCommandEndsWithWhatItStarted(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child")
	ctx, cancel := context.WithCancel(t.Context())
	ended := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		_, _, _ = runCommand(ctx, dir, []string{"sh", "-c", `sleep 60 & echo $! > child; wait`})
		ended <- time.Since(start)
	}()
	var child int
	for deadline := time.Now().Add(10 * time.Second); child == 0; time.Sleep(10 * time.Millisecond) {
		if b, err := os.ReadFile(pidFile); err == nil && strings.HasSuffix(string(b), "\n") {
			child, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		}
		if time.Now().After(deadline) {
			t.Fatal("the command never started its child")
		}
	}
	cancel()
	select {
	case d := <-ended:
		if d > 30*time.Second {
			t.Errorf("the command ended %v after it was stopped", d)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("a stopped command still waits for its child")
	}
	for deadline := time.Now().Add(5 * time.Second); syscall.Kill(child, 0) == nil; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			_ = syscall.Kill(child, syscall.SIGKILL)
			t.Fatal("the command's child outlived it")
		}
	}
}

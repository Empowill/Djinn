package gitx

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"
)

// TestCommandEndsAtItsDeadline: a git that reaches a remote is killed at Network, its output let go after WaitDelay
// though a child it started holds it, as an ssh does; one that stays in the repository runs as before.
func TestCommandEndsAtItsDeadline(t *testing.T) {
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
	network, waitDelay := Network, WaitDelay
	Network, WaitDelay = 300*time.Millisecond, 100*time.Millisecond
	t.Cleanup(func() { Network, WaitDelay = network, waitDelay })

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

	cmd, cancel = Command(t.Context(), t.TempDir(), "version")
	defer cancel()
	if out, err := cmd.Output(); err != nil || !bytes.HasPrefix(out, []byte("git version")) {
		t.Errorf("git version = %q, %v", out, err)
	}
}

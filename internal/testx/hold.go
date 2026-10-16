package testx

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// HeldCheckout holds a git mid-way through writing a file to a worktree, as a reset, a checkout or a merge does, its
// index.lock taken: what a stop or a kill finds when it cuts one (W52). Unix only: the filter is a shell script.
type HeldCheckout struct {
	// Env holds the variables that make a git command hold: add them to its environment.
	Env           []string
	mark, release string
	released      bool
}

// HoldCheckout makes git, in the repository whose common folder is common (".git" of the main checkout), hold in a
// smudge filter for every file it writes, when started with h.Env; other gits are left alone. The filter lets go of
// the file once Release is called, and the test's end calls it.
func HoldCheckout(t testing.TB, common string) *HeldCheckout {
	t.Helper()
	dir := t.TempDir()
	h := &HeldCheckout{mark: filepath.Join(dir, "held"), release: filepath.Join(dir, "release")}
	script := filepath.Join(dir, "hold.sh")
	body := "#!/bin/sh\necho $$ > '" + h.mark + ".new'\nmv '" + h.mark + ".new' '" + h.mark + "'\n" +
		"while [ ! -e '" + h.release + "' ]; do sleep 0.01; done\nexec cat\n"
	if err := os.WriteFile(script, []byte(body), 0o600); err != nil { // Run by sh, not executed.
		t.Fatal(err)
	}
	info := filepath.Join(common, "info")
	if err := os.MkdirAll(info, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(info, "attributes"), []byte("* filter=djinnhold\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.Env = []string{"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=filter.djinnhold.smudge", "GIT_CONFIG_VALUE_0=sh '" + script + "'"}
	t.Cleanup(h.Release)
	return h
}

// Held waits for a git to hold, and returns its filter's process ID.
func (h *HeldCheckout) Held(t testing.TB) int {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if b, err := os.ReadFile(h.mark); err == nil {
			pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
			if err != nil {
				t.Fatal(err)
			}
			return pid
		}
	}
	t.Fatal("no git held within 5 s")
	return 0
}

// Release lets the held git write its file and go on.
func (h *HeldCheckout) Release() {
	if !h.released {
		h.released = true
		_ = os.WriteFile(h.release, nil, 0o600)
	}
}

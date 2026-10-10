package harness

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/empowill/djinn/internal/machine"
)

// TestScopeTakesTheTree runs a command in a real systemd user scope, where this machine has one (CI has none: it
// skips). The command starts a child in a session of its own, outside its process group: measuring, pausing and
// stopping the worker take that child too. Where systemd gives the user the memory controller, the scope holds its
// ceiling.
func TestScopeTakesTheTree(t *testing.T) {
	t.Parallel()
	scopes, _, err := machine.ProbeScopes(t.Context(), 0, 512<<20)
	if err != nil {
		t.Skip(err)
	}
	ticks := filepath.Join(t.TempDir(), "ticks")
	// The child ticks into a file, in a session of its own; the command waits for it.
	script := `setsid sh -c 'while :; do echo x >> "$1"; sleep 0.05; done' child "$0" & echo "$!"; wait`
	h := &Harness{scopes: scopes}
	p, err := startProcess(t.TempDir(), "sh", []string{"-c", script, ticks}, nil, h.scope("W1"), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	if !strings.HasPrefix(filepath.Base(p.cgroup), "djinn-W1-") {
		t.Fatalf("cgroup %q", p.cgroup)
	}
	l := <-p.lines
	child, err := strconv.Atoi(l.text)
	if err != nil {
		t.Fatalf("the child's pid: %q", l.text)
	}
	size := func() int64 {
		st, _ := os.Stat(ticks)
		if st == nil {
			return 0
		}
		return st.Size()
	}
	waitFor(t, "the child's ticks", func() bool { return size() > 0 })

	if scopes.Memory > 0 {
		if b, err := os.ReadFile(filepath.Join(p.cgroup, "memory.max")); err != nil || strings.TrimSpace(string(b)) != "536870912" {
			t.Errorf("memory.max = %q, %v", b, err)
		}
	}

	g, err := machine.ReadWorker(p.cmd.Process.Pid, p.cgroup)
	if err != nil || g.Processes < 3 || g.Memory == 0 || g.Peak < g.Memory {
		t.Errorf("ReadWorker = %+v, %v: want the command, the child and its sleep, read from the cgroup", g, err)
	}

	if err := p.Pause(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	before := size()
	time.Sleep(300 * time.Millisecond)
	if after := size(); after != before {
		t.Errorf("the child ticked while paused: %d bytes", after-before)
	}
	if err := p.Resume(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "ticks after resuming", func() bool { return size() > before })

	p.Stop()
	select {
	case <-p.done:
	case <-time.After(10 * time.Second):
		t.Fatal("the command did not stop")
	}
	waitFor(t, "the child to end", func() bool { return errors.Is(syscall.Kill(child, 0), syscall.ESRCH) })
}

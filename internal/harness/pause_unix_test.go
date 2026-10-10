//go:build !windows

// Not on Windows: pausing is SIGSTOP to the worker's process group, which Windows lacks; pause_windows_test.go
// checks that a pause is refused there.

package harness

import (
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestPauseProcess: a worker's process group stops on SIGSTOP and its output with it, goes on with SIGCONT, and a
// stop while paused ends it at once, not after the grace delay.
func TestPauseProcess(t *testing.T) {
	t.Parallel()
	env, _, _ := fake{provider: "claude", fixture: "success", end: "tick"}.env(t)
	w, err := Claude{Command: os.Args[0], Grace: time.Minute}.Start(t.Context(), Spec{TaskID: "t1", Dir: t.TempDir(), Prompt: "x", Env: env})
	if err != nil {
		t.Fatal(err)
	}
	var ticks atomic.Int64
	go func() {
		for ev := range w.Events() {
			if strings.HasPrefix(ev.Text, "tick ") {
				ticks.Add(1)
			}
		}
	}()
	p, ok := w.(Pauser)
	if !ok {
		t.Fatal("a Claude worker cannot be paused")
	}
	waitFor(t, "the first ticks", func() bool { return ticks.Load() >= 3 })

	if err := p.Pause(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond) // What was written before the stop is still read.
	before := ticks.Load()
	time.Sleep(200 * time.Millisecond) // Ten ticks, were it not paused.
	if after := ticks.Load(); after != before {
		t.Errorf("%d ticks while paused", after-before)
	}

	if err := p.Resume(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "ticks after resuming", func() bool { return ticks.Load() > before+2 })

	if err := p.Pause(); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	w.Stop()
	res := w.Wait()
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("a paused worker took %v to stop: the grace delay, not its SIGTERM", d)
	}
	if res.ExitCode != -1 {
		t.Errorf("exit code = %d, want -1 (ended by a signal)", res.ExitCode)
	}
	if err := p.Pause(); err == nil {
		t.Errorf("an ended worker paused")
	}
}

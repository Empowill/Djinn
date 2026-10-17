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

// pausedStream starts claude's fake on a turn that waits for a message mid-turn (stream-idle), with an idle timeout,
// and pauses it once it said its first words: the timers of a stream Djinn holds still. The idle timeout must outlast
// the fake's start, slow under -race on a loaded machine: from 2 s, doubled while the start alone outlasts it. It
// gives the idle timeout it took.
func pausedStream(t *testing.T) (w Worker, events <-chan Event, input string, idle time.Duration) {
	t.Helper()
	for idle = 2 * time.Second; ; idle *= 2 {
		w, events, input, ok := startPaused(t, idle)
		if ok {
			return w, events, input, idle
		}
		if idle >= 8*time.Second {
			t.Fatalf("the worker never said its first words before an idle timeout of %v", idle)
		}
	}
}

// startPaused is one try of pausedStream: false when the worker ended before its first words.
func startPaused(t *testing.T, idle time.Duration) (w Worker, events <-chan Event, input string, ok bool) {
	t.Helper()
	env, _, input := fake{provider: "claude", fixture: "stream-idle", end: "eof"}.env(t)
	spec := Spec{TaskID: "t-paused", Dir: t.TempDir(), Prompt: "x", Env: env}
	a := streamAgent{name: "claude", parser: &claudeParser{}, encode: claudeMessageLine, idle: idle}
	w, err := startStream(t.Context(), spec, os.Args[0], Claude{}.args(spec), Grace, a)
	if err != nil {
		t.Fatal(err)
	}
	all := make(chan Event, 64)
	first, ended := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(all)
		defer close(ended)
		said := false
		for ev := range w.Events() {
			all <- ev
			if !said && ev.Kind.String() == "TASK_EVENT_KIND_TEXT" {
				said = true
				close(first)
			}
		}
	}()
	select {
	case <-first:
	case <-ended:
		if res := w.Wait(); !isStreamIdle(res.Err) {
			t.Fatalf("the worker ended before its first words: %+v", res)
		}
		return nil, nil, "", false
	case <-time.After(30 * time.Second):
		w.Stop()
		t.Fatal("the worker said nothing in 30s")
	}
	if err := w.(Pauser).Pause(); err != nil {
		t.Fatal(err)
	}
	return w, all, input, true
}

// TestStreamPausedIsNotIdle (W226): a worker Djinn holds paused cannot stream; held longer than its idle timeout, it
// is not cut for an idle stream, and the message sent to it while paused is what it reads once resumed.
func TestStreamPausedIsNotIdle(t *testing.T) {
	t.Parallel()
	w, events, input, idle := pausedStream(t)
	if err := w.Send("Also check the docs."); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * idle / 2)
	if err := w.(Pauser).Resume(); err != nil {
		t.Fatal(err)
	}
	hung := time.AfterFunc(10*time.Second, func() {
		t.Errorf("the worker still runs after 10s")
		w.Stop()
	})
	var got []Event
	for ev := range events {
		got = append(got, ev)
	}
	hung.Stop()
	if res := w.Wait(); res.Err != nil || res.ExitCode != 0 {
		t.Errorf("result = %+v, want a turn that ends well: paused is not idle", res)
	}
	if errs := texts(got, "ERROR"); len(errs) > 0 {
		t.Errorf("errors = %q", errs)
	}
	b, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "Also check the docs.") {
		t.Errorf("the worker never read the message sent while it was paused; its input:\n%s", b)
	}
}

// TestStreamIdleAfterResume (W226): the idle timer starts again from zero when a paused worker resumes, and a stream
// silent that long once it runs is still cut.
func TestStreamIdleAfterResume(t *testing.T) {
	t.Parallel()
	w, events, _, idle := pausedStream(t)
	time.Sleep(3 * idle / 2)
	resumed := time.Now()
	if err := w.(Pauser).Resume(); err != nil {
		t.Fatal(err)
	}
	hung := time.AfterFunc(10*time.Second, func() {
		t.Errorf("the worker still runs after 10s")
		w.Stop()
	})
	for range events {
	}
	hung.Stop()
	silent := time.Since(resumed)
	if res := w.Wait(); !isStreamIdle(res.Err) {
		t.Errorf("result = %+v, want a stream idle error: silent once it runs", res)
	}
	if silent < idle {
		t.Errorf("cut %v after it resumed, before its idle timeout of %v: the time paused counted", silent, idle)
	}
}

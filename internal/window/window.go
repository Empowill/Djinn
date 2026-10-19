// Package window watches the page of Djinn's native window from outside it, on Linux. When the web process that draws
// the page ends (after an Intel GPU hang, out of memory, or killed; W353, W366), it is logged with WebKit's state (the
// page's load events, the web processes' CPU and memory) and the kernel's GPU hangs, told outside the window, and
// rescued step by step, once each: the page loads again, then the window opens again with a new view. The new view
// draws in software rendering only after a GPU hang, until Djinn restarts: rendering stays on the GPU at every start
// (decision Q89).
package window

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/empowill/djinn/internal/logx"
)

// Lookback is how long before djinn up started the kernel's log is read: the hang that kills a web process may come
// just before a start (17/10: 30 s before an install restarted Djinn).
const Lookback = 10 * time.Minute

// Sample is how long the web processes' CPU is measured, when a page's web process ends.
const Sample = 500 * time.Millisecond

// Rescue is what a Watch did about a dead page.
type Rescue int

const (
	// Nothing: the page loaded again and the window opened again already, or neither can be done.
	Nothing Rescue = iota
	// Reloaded: the page loads again, in the same view.
	Reloaded
	// Recreated: the window opens again, with a new view and a new web process.
	Recreated
)

// Event is what a Watch tells outside the window.
type Event struct {
	// Ended is the reason the page's web process ended ("crashed", "memory", "terminated").
	Ended string
	// Hangs are the GPU hangs and resets of the kernel's log since Lookback before djinn up started.
	Hangs []Hang
	// Did is what the Watch did.
	Did Rescue
	// Software tells that the new view draws in software rendering, after a GPU hang, until Djinn restarts.
	Software bool
	// Err is why the page could not load again, or the window open again.
	Err error
}

// Watch follows the windows of one djinn up: Opened as each opens, Loaded at each of its page's load events, Ended as
// its web process ends. It reloads a page once and recreates a window once, per djinn up, never in a loop.
type Watch struct {
	// Started is when djinn up started: the kernel's log is read from Lookback before it, the load events are timed
	// from it.
	Started time.Time
	// Hangs reads the GPU hangs and resets of the kernel's log since a time: KernelHangs when nil. Tests replace it.
	Hangs func(ctx context.Context, since time.Time) ([]Hang, error)
	// Processes reads the web processes of the window: djinn up's children whose name starts with WebKit, when nil.
	// Tests replace it.
	Processes func() []Process
	// Sample is how long the web processes' CPU is measured: Sample when zero, none when negative.
	Sample time.Duration
	// Reload loads the page again in its view. Nil: no reload.
	Reload func() error
	// Recreate opens the window again with a new view, in software rendering when software, in place of the dead one.
	// Nil: no new view.
	Recreate func(software bool) error
	// Tell says what happened outside the window: a line where djinn up runs, a desktop notification. Nil says nothing.
	Tell func(Event)

	mu        sync.Mutex
	page      int      // pages loaded: the current one's number
	loads     []string // the load events since the last rescue, timed from Started
	reloaded  bool     // a reload was tried
	recreated bool     // a new view was tried
	gpuHang   bool     // a GPU hang was found in the kernel's log
	rescuing  bool     // a rescue runs: another trigger of the same page waits for none
}

// Opened tells that a window opened, its page the current one from now on.
func (w *Watch) Opened() {
	w.mu.Lock()
	w.page++
	w.mu.Unlock()
}

// Loaded logs a load event of the current window's page ("started", "committed", "finished", "failed: …"), and keeps
// it for the log of a dead page.
func (w *Watch) Loaded(event string) {
	w.mu.Lock()
	at := time.Since(w.Started).Round(time.Millisecond)
	w.loads = append(w.loads, fmt.Sprintf("%s +%s", event, at))
	if len(w.loads) > 32 {
		w.loads = w.loads[len(w.loads)-32:]
	}
	page := w.page
	w.mu.Unlock()
	logx.L().Info("window: load", "event", event, "page", page, "since_start", at)
}

// Ended tells that the web process of the current page ended, for reason, at its start or any time after: the page is
// dead.
func (w *Watch) Ended(reason string) {
	w.mu.Lock()
	n := w.page
	w.mu.Unlock()
	w.rescue(n, Event{Ended: reason})
}

// rescue logs WebKit's state and the kernel's GPU hangs, then takes the next step for page n: a reload, then a new
// view, in software rendering after a GPU hang; then nothing more. It tells what it did.
func (w *Watch) rescue(n int, e Event) {
	w.mu.Lock()
	if n != w.page || w.rescuing {
		w.mu.Unlock()
		return
	}
	w.rescuing = true
	w.mu.Unlock()

	hangs := w.Hangs
	if hangs == nil {
		hangs = KernelHangs
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var err error
	e.Hangs, err = hangs(ctx, w.Started.Add(-Lookback))
	processes := w.webProcesses()
	w.mu.Lock()
	loads := w.loads
	w.loads = nil
	w.mu.Unlock()
	logx.L().Warn("window: the page's web process ended", "page", n, "ended", e.Ended, "loads", loads,
		"web_processes", processes, "gpu_hangs", len(e.Hangs), "kernel", hangLines(e.Hangs), "kernel_err", err)

	w.mu.Lock()
	w.gpuHang = w.gpuHang || len(e.Hangs) > 0
	reload := !w.reloaded && w.Reload != nil
	recreate := !reload && !w.recreated && w.Recreate != nil
	w.reloaded = w.reloaded || reload
	w.recreated = w.recreated || recreate
	software := w.gpuHang
	next := 0
	if reload || recreate {
		w.page++
		next = w.page
	}
	w.mu.Unlock()
	switch {
	case reload:
		e.Did, e.Err = Reloaded, w.Reload()
	case recreate:
		e.Did, e.Software, e.Err = Recreated, software, w.Recreate(software)
	}
	switch {
	case e.Err != nil:
		logx.L().Error("window: the rescue failed", "did", e.Did, "err", e.Err)
		e.Did = Nothing
	case reload:
		logx.L().Warn("window: the page loads again", "page", next)
	case recreate:
		logx.L().Warn("window: opened again with a new view", "page", next, "software_rendering", software)
	default:
		logx.L().Warn("window: no rescue left: quit Djinn and start it again", "page", n)
	}
	w.mu.Lock()
	w.rescuing = false
	w.mu.Unlock()
	if w.Tell != nil {
		w.Tell(e)
	}
}

// webProcesses is the web processes' state, for the log: their CPU measured over Sample.
func (w *Watch) webProcesses() []string {
	read := w.Processes
	if read == nil {
		pid := os.Getpid()
		read = func() []Process { return WebKitProcesses(pid) }
	}
	sample := w.Sample
	if sample == 0 {
		sample = Sample
	}
	before := read()
	if sample > 0 {
		time.Sleep(sample)
	}
	after := read()
	out := make([]string, 0, len(after))
	for _, p := range after {
		out = append(out, p.Describe(before, sample))
	}
	return out
}

func hangLines(hangs []Hang) []string {
	lines := make([]string, len(hangs))
	for i, h := range hangs {
		lines[i] = h.Line
	}
	return lines
}

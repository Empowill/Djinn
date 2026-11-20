// Package window watches the page of Djinn's native window from outside it, on Linux. When the web process that draws
// the page ends (after an Intel GPU hang, out of memory, or killed; W353, W366), it is logged with WebKit's state (the
// page's load events, the web processes' CPU and memory) and the kernel's GPU hangs, told outside the window, and
// rescued step by step, once each: the page loads again, then the window opens again with a new view. The new view
// draws in software rendering only after a GPU hang, until Djinn restarts: rendering stays on the GPU at every start
// (decision Q89).
//
// A page can also load and then wait, its web process alive and idle, never calling the server (W373, W172): a page
// that makes no call within Silence of its load finished is silent. It is logged with a probe of the page and the
// web processes' threads, then loaded again once per djinn up, and told nowhere else: no notification, no line on
// djinn up's output (decisions Q108, Q111), a false alarm costing only a reload.
package window

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/empowill/djinn/internal/logx"
)

// Silence is how long a page has, from its load finished, to call the server (decision Q111): a good start calls about
// 0.2 s after (W172), a stuck one not at all in 85 s.
const Silence = 3 * time.Second

// ProbeWait is how long the probe of a silent page has to answer: no answer tells a JS thread that does not run.
const ProbeWait = 2 * time.Second

// ProbeScript is what the probe of a silent page evaluates in it: how far the document went, whether the loader still
// shows and the interface mounted, and whether boot.js attached the entry module (the page's load finishes before it
// does, vite.config.ts), with the document's scripts. The resource timing would name the loads still pending, but it
// lists nothing loaded from wails:// (W181's native run): no field for it.
const ProbeScript = `(() => {
  const root = document.getElementById("root");
  const scripts = [...document.scripts].map((s) => s.getAttribute("src")).filter(Boolean);
  const entry = document.querySelector('meta[name="djinn-entry-js"]')?.getAttribute("content");
  return {
    ready: document.readyState,
    splash: !!document.getElementById("djinn-splash"),
    root: root ? root.childElementCount : -1,
    visibility: document.visibilityState,
    entry: !entry ? "none" : scripts.includes(entry) ? "attached" : "not attached",
    scripts,
    now: Math.round(performance.now()),
  };
})()`

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

// Watch follows the windows of one djinn up: Opened as each opens, Loaded at each of its page's load events, Called
// as its page calls the server, Ended as its web process ends. It reloads a dead page once and recreates a window
// once, and reloads a silent page once, per djinn up, never in a loop.
type Watch struct {
	// After is how long a page has, from its load finished, to call the server: Silence when zero.
	After time.Duration
	// Probe evaluates script in the current page and gives its answer as JSON. Nil: a silent page is logged without.
	Probe func(ctx context.Context, script string) (string, error)
	// ProbeWait is how long the probe has to answer: ProbeWait when zero.
	ProbeWait time.Duration
	// AfterFunc runs f after d unless the returned function stops it first: time.AfterFunc when nil. Tests replace it.
	AfterFunc func(d time.Duration, f func()) (stop func() bool)
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

	heard atomic.Bool // the current document called the server

	mu        sync.Mutex
	page      int         // pages loaded: the current one's number
	document  int         // documents committed: the current one's number, which a silence names
	finished  time.Time   // when the current document's load finished
	stop      func() bool // the current document's silence timer
	loads     []string    // the load events since the last rescue or silence, timed from Started
	reloaded  bool        // a reload was tried
	recreated bool        // a new view was tried
	quieted   bool        // a silent page was loaded again
	gpuHang   bool        // a GPU hang was found in the kernel's log
	rescuing  bool        // a rescue runs: another trigger of the same page waits for none
}

// Opened tells that a window opened, its page the current one from now on.
func (w *Watch) Opened() {
	w.mu.Lock()
	w.page++
	w.mu.Unlock()
}

// Loaded logs a load event of the current window's page ("started", "committed", "finished", "failed: …"), and keeps
// it for the log of a dead or silent page. A document committed is a new one, not heard yet; once its load finished,
// it has After to call the server.
func (w *Watch) Loaded(event string) {
	w.mu.Lock()
	at := time.Since(w.Started).Round(time.Millisecond)
	w.loads = append(w.loads, fmt.Sprintf("%s +%s", event, at))
	if len(w.loads) > 32 {
		w.loads = w.loads[len(w.loads)-32:]
	}
	page := w.page
	switch event {
	case "committed":
		w.stopLocked()
		w.document++
		w.finished = time.Time{}
		w.heard.Store(false)
	case "finished":
		w.finished = time.Now()
		if !w.heard.Load() && w.stop == nil {
			w.armLocked(w.document)
		}
	}
	w.mu.Unlock()
	logx.L().Info("window: load", "event", event, "page", page, "since_start", at)
}

// stopLocked stops the silence timer of the current document.
func (w *Watch) stopLocked() {
	if w.stop != nil {
		w.stop()
		w.stop = nil
	}
}

// armLocked arms the silence timer of document n, the current one.
func (w *Watch) armLocked(n int) {
	after := w.After
	if after == 0 {
		after = Silence
	}
	afterFunc := w.AfterFunc
	if afterFunc == nil {
		afterFunc = func(d time.Duration, f func()) func() bool { return time.AfterFunc(d, f).Stop }
	}
	w.stop = afterFunc(after, func() {
		defer logx.Crash("window: a silent page")
		w.silent(n, after)
	})
}

// Called tells that the current page called the server: it runs. The first call of each document is logged.
func (w *Watch) Called() {
	if w.heard.Load() {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.heard.Swap(true) {
		return
	}
	w.stopLocked()
	if !w.finished.IsZero() {
		logx.L().Info("window: the page calls the server", "page", w.page,
			"after_load", time.Since(w.finished).Round(time.Millisecond))
	} else {
		logx.L().Info("window: the page calls the server", "page", w.page, "before_load_finished", true)
	}
}

// Handler marks each call of the window's page to h: a Connect method, not a file of the page, which the web process
// loads before any script runs.
func (w *Watch) Handler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if isCall(r.URL.Path) {
			w.Called()
		}
		h.ServeHTTP(rw, r)
	})
}

// isCall tells that path is a Connect method, /package.v1.SomeService/Method.
func isCall(path string) bool {
	service, method, ok := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	return ok && method != "" && !strings.Contains(method, "/") && strings.HasSuffix(service, "Service") &&
		strings.Contains(service, ".")
}

// silent logs document n, silent for after since its load finished, with a probe of the page and the web processes'
// threads, then loads it again, once per djinn up, unless it called meanwhile or another rescue took it. It tells
// nothing outside the log.
func (w *Watch) silent(n int, after time.Duration) {
	w.mu.Lock()
	w.stop = nil
	page := w.page
	current := n == w.document && !w.heard.Load() && !w.rescuing
	w.mu.Unlock()
	if !current {
		return
	}
	probe := w.probe()
	processes := w.webProcesses()
	w.mu.Lock()
	loads := w.loads
	w.loads = nil
	// The page called during the probe, or its web process ended and its rescue took it.
	called := w.heard.Load() || n != w.document || page != w.page || w.rescuing
	reload := !called && !w.quieted && w.Reload != nil
	w.quieted = w.quieted || reload
	if reload {
		w.page++
	}
	next := w.page
	w.mu.Unlock()
	logx.L().Warn("window: the page is silent", "page", page, "after", after, "probe", probe, "loads", loads,
		"web_processes", processes)
	switch {
	case called:
		logx.L().Info("window: the silent page called or was rescued meanwhile, not loaded again", "page", page)
	case reload:
		if err := w.Reload(); err != nil {
			logx.L().Error("window: the silent page could not load again", "err", err)
		} else {
			logx.L().Warn("window: the page loads again", "page", next, "silent", true)
		}
	default:
		logx.L().Warn("window: the silent page was loaded again already: quit Djinn and start it again if it stays on its loader", "page", page)
	}
}

// probe asks the page what it waits on, ProbeScript, within ProbeWait: its answer, or why there is none.
func (w *Watch) probe() string {
	if w.Probe == nil {
		return "none"
	}
	wait := w.ProbeWait
	if wait == 0 {
		wait = ProbeWait
	}
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	type answer struct {
		json string
		err  error
	}
	done := make(chan answer, 1)
	start := time.Now()
	go func() {
		defer logx.Crash("window: probe the page")
		json, err := w.Probe(ctx, ProbeScript)
		done <- answer{json, err}
	}()
	select {
	case a := <-done:
		took := time.Since(start).Round(time.Millisecond)
		if a.err != nil {
			return fmt.Sprintf("failed in %s: %v", took, a.err)
		}
		return fmt.Sprintf("answered in %s: %s", took, a.json)
	case <-ctx.Done():
		return fmt.Sprintf("no answer within %s", wait)
	}
}

// Ended tells that the web process of the current page ended, for reason, at its start or any time after: the page is
// dead.
func (w *Watch) Ended(reason string) {
	w.mu.Lock()
	n := w.page
	w.stopLocked()
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

// Package window watches the page of Djinn's native window from outside it, on Linux. A page can stay black with
// nothing to say so: after an Intel GPU hang (W353), when its web process dies (W366), and most often with its web
// process alive and idle, about 155 MB and 0 % CPU, that never calls the server: half the update restarts of 17-18/10,
// and some starts from the desktop, with no GPU hang (W373). A page that makes no call to the server within Silence of
// its window opening is silent; a page whose web process ended is dead. Either is logged with WebKit's state (the
// page's load events, the web processes' CPU and memory) and the kernel's GPU hangs, told outside the window, and
// rescued step by step, once each: the page loads again, then the window opens again with a new view. The new view
// draws in software rendering only after a GPU hang, until Djinn restarts: rendering stays on the GPU at every start
// (decision Q89).
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

// Silence is how long the page of a window that opened has to call the server. W354 measured the first call of a
// fresh WebKitGTK view on Djinn's page at 0.5 to 1.1 s at rest, 1.7 to 2.4 s with 32 busy loops on 16 cores, and
// 2.5 to 3.6 s with 80 (load 58); W373 saw healthy starts call in 1 to 4 s, while their web process grew past 300 MB
// at 40-60 % CPU. The black starts made no call at all, 17 to 28 s on.
const Silence = 10 * time.Second

// Lookback is how long before djinn up started the kernel's log is read: the hang that blackens a window may come
// just before a start (17/10: 30 s before an install restarted Djinn).
const Lookback = 10 * time.Minute

// Sample is how long the web processes' CPU is measured, when a page is found silent or dead.
const Sample = 500 * time.Millisecond

// Rescue is what a Watch did about a silent or dead page.
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
	// Ended is the reason the page's web process ended ("crashed", "memory", "terminated"); empty for a silent page.
	Ended string
	// After is how long the page stayed silent.
	After time.Duration
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
// as its page calls the server, Ended as its web process ends. It reloads a page once and recreates a window once, per
// djinn up, never in a loop.
type Watch struct {
	// After is how long a page has to call the server: Silence when zero.
	After time.Duration
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
	// Reload loads the page again in its view. The Watch takes it as a new page before the call, so that its first
	// call counts. Nil: no reload.
	Reload func() error
	// Recreate opens the window again with a new view, in software rendering when software, in place of the silent
	// one. The Watch takes it as a new page before the call. Nil: no new view.
	Recreate func(software bool) error
	// Tell says what happened outside the window: a line where djinn up runs, a desktop notification. Nil says nothing.
	Tell func(Event)
	// AfterFunc runs f after d unless the returned function stops it first: time.AfterFunc when nil. Tests replace it.
	AfterFunc func(d time.Duration, f func()) (stop func() bool)

	heard atomic.Bool // the current page called

	mu        sync.Mutex
	page      int         // pages loaded: the current one's number
	stop      func() bool // the current page's timer
	opened    time.Time
	loads     []string // the load events since the last rescue, timed from Started
	reloaded  bool     // a reload was tried
	recreated bool     // a new view was tried
	gpuHang   bool     // a GPU hang was found in the kernel's log
	rescuing  bool     // a rescue runs: another trigger of the same page waits for none
}

// Opened tells that a window opened, its page the current one from now on: it has After to call the server.
func (w *Watch) Opened() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.nextLocked()
}

// nextLocked takes a new page as the current one, and arms its timer.
func (w *Watch) nextLocked() int {
	n := w.takeLocked()
	w.armLocked(n)
	return n
}

// takeLocked takes a new page as the current one, its timer not armed yet.
func (w *Watch) takeLocked() int {
	if w.stop != nil {
		w.stop()
		w.stop = nil
	}
	w.page++
	w.heard.Store(false)
	w.opened = time.Now()
	return w.page
}

// armLocked arms the timer of page n, the current one.
func (w *Watch) armLocked(n int) {
	after := w.After
	if after == 0 {
		after = Silence
	}
	afterFunc := w.AfterFunc
	if afterFunc == nil {
		afterFunc = func(d time.Duration, f func()) func() bool { return time.AfterFunc(d, f).Stop }
	}
	w.stop = afterFunc(after, func() { w.silent(n, after) })
}

// Loaded logs a load event of the current window's page ("started", "committed", "finished", "failed: …"), and keeps
// it for the log of a silent page.
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

// Called tells that the current page called the server: it runs.
func (w *Watch) Called() {
	if w.heard.Load() {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.heard.Swap(true) {
		return
	}
	if w.stop != nil {
		w.stop()
		w.stop = nil
	}
	if w.page > 0 {
		logx.L().Info("window: the page calls the server", "page", w.page, "after", time.Since(w.opened).Round(time.Millisecond))
	}
}

// Ended tells that the web process of the current page ended, for reason, at its start or any time after: the page is
// dead.
func (w *Watch) Ended(reason string) {
	w.mu.Lock()
	n := w.page
	if w.stop != nil {
		w.stop()
		w.stop = nil
	}
	w.mu.Unlock()
	w.rescue(n, Event{Ended: reason})
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

func (w *Watch) silent(n int, after time.Duration) {
	w.mu.Lock()
	current := n == w.page && !w.heard.Load()
	w.mu.Unlock()
	if current {
		w.rescue(n, Event{After: after})
	}
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
	what := "window: the page is silent"
	if e.Ended != "" {
		what = "window: the page's web process ended"
	}
	w.mu.Lock()
	loads := w.loads
	w.loads = nil
	w.mu.Unlock()
	logx.L().Warn(what, "page", n, "after", e.After, "ended", e.Ended, "loads", loads, "web_processes", processes,
		"gpu_hangs", len(e.Hangs), "kernel", hangLines(e.Hangs), "kernel_err", err)

	// The next page is taken before the step, so that its first call counts; its timer is armed once the step is
	// done, so that a silence never falls during a rescue.
	w.mu.Lock()
	w.gpuHang = w.gpuHang || len(e.Hangs) > 0
	reload := !w.reloaded && w.Reload != nil
	recreate := !reload && !w.recreated && w.Recreate != nil
	w.reloaded = w.reloaded || reload
	w.recreated = w.recreated || recreate
	software := w.gpuHang
	next := 0
	if reload || recreate {
		next = w.takeLocked()
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
	if e.Did != Nothing && w.page == next && !w.heard.Load() {
		w.armLocked(next)
	}
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

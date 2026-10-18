package window

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/empowill/djinn/internal/logx"
)

// fakeClock holds the timers of a Watch until fire runs them.
type fakeClock struct {
	mu     sync.Mutex
	timers []*fakeTimer
}

type fakeTimer struct {
	d          time.Duration
	f          func()
	done       bool // fired or stopped
	stopCalled bool
}

func (c *fakeClock) AfterFunc(d time.Duration, f func()) func() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{d: d, f: f}
	c.timers = append(c.timers, t)
	return func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		t.stopCalled = true
		was := !t.done
		t.done = true
		return was
	}
}

// fire runs the timers pending now, as if their time came; it returns how many ran.
func (c *fakeClock) fire() int {
	c.mu.Lock()
	var due []*fakeTimer
	for _, t := range c.timers {
		if !t.done {
			t.done = true
			due = append(due, t)
		}
	}
	c.mu.Unlock()
	for _, t := range due {
		t.f()
	}
	return len(due)
}

// rig is a Watch on a fake clock, a fake kernel log and fake web processes, that counts its reloads and new views
// and keeps what it told.
type rig struct {
	*Watch
	clock     *fakeClock
	since     []time.Time
	hangs     []Hang
	reloads   int
	recreates []bool // software, by new view
	told      []Event
	log       *bytes.Buffer
}

func newRig(t *testing.T, fail error) *rig {
	r := &rig{clock: &fakeClock{}, log: &bytes.Buffer{}}
	t.Cleanup(logx.Use(logx.New(r.log, slog.LevelInfo)))
	started := time.Date(2026, 10, 17, 21, 27, 25, 0, time.UTC)
	cpu := time.Second
	r.Watch = &Watch{
		Started:   started,
		AfterFunc: r.clock.AfterFunc,
		Sample:    -1,
		Hangs: func(_ context.Context, since time.Time) ([]Hang, error) {
			r.since = append(r.since, since)
			return r.hangs, nil
		},
		Processes: func() []Process {
			return []Process{{PID: 4242, Name: "WebKitWebProces", State: "S", RSS: 155 << 20, CPU: cpu}}
		},
		Reload:   func() error { r.reloads++; return fail },
		Recreate: func(software bool) error { r.recreates = append(r.recreates, software); return fail },
		Tell:     func(e Event) { r.told = append(r.told, e) },
	}
	return r
}

// gpuHang is the kernel's line of 17/10.
var gpuHang = []Hang{{Time: time.Date(2026, 10, 17, 21, 26, 55, 0, time.UTC), Line: "i915 0000:00:02.0: [drm] Xorg[8937] context reset due to GPU hang"}}

// call is a request of the window's page through the Watch's handler.
func (r *rig) call(t *testing.T, path string) {
	t.Helper()
	h := r.Handler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, path, nil))
}

// TestWatch covers Watch: a page silent past Silence, or whose web process ended, is logged with WebKit's state and
// the kernel's GPU hangs, told, then loaded again, then given a new view, in software rendering only after a GPU hang,
// once each per djinn up, never in a loop; a page that calls the server is left alone.
func TestWatch(t *testing.T) {
	// W373: the web process lives, idle, and the page never calls; no GPU hang.
	t.Run("a silent page loads again, then gets a new view on the GPU, then is only told", func(t *testing.T) {
		r := newRig(t, nil)
		r.Loaded("started")
		r.Loaded("committed")
		r.Opened()
		if len(r.clock.timers) != 1 || r.clock.timers[0].d != Silence {
			t.Fatalf("timers = %+v; want one of %s", r.clock.timers, Silence)
		}
		r.clock.fire()
		if r.reloads != 1 || len(r.recreates) != 0 || len(r.told) != 1 || r.told[0].Did != Reloaded || r.told[0].After != Silence {
			t.Fatalf("reloads %d, new views %v, told %+v", r.reloads, r.recreates, r.told)
		}
		if want := r.Started.Add(-Lookback); len(r.since) != 1 || !r.since[0].Equal(want) {
			t.Errorf("kernel read since %v; want %v", r.since, want)
		}
		for _, want := range []string{`msg="window: the page is silent" page=1`, "loads=\"[started +", "committed +",
			"pid 4242 WebKitWebProces S rss 155 MB cpu 1.0 s", "gpu_hangs=0", `msg="window: the page loads again" page=2`} {
			if !strings.Contains(r.log.String(), want) {
				t.Errorf("log lacks %q:\n%s", want, r.log)
			}
		}

		// The page loaded again is silent too: a new view, on the GPU.
		if n := r.clock.fire(); n != 1 {
			t.Fatalf("%d timers for the page loaded again; want 1", n)
		}
		if len(r.recreates) != 1 || r.recreates[0] || len(r.told) != 2 || r.told[1].Did != Recreated || r.told[1].Software {
			t.Fatalf("new views %v, told %+v", r.recreates, r.told)
		}
		// Silent again: told, nothing more.
		r.clock.fire()
		r.Ended("crashed")
		if r.reloads != 1 || len(r.recreates) != 1 || len(r.told) != 4 || r.told[2].Did != Nothing || r.told[3].Did != Nothing {
			t.Errorf("then: reloads %d, new views %v, told %+v", r.reloads, r.recreates, r.told)
		}
		if r.clock.fire() != 0 {
			t.Error("a timer left once every rescue was tried")
		}
	})

	t.Run("after a GPU hang the new view draws in software rendering", func(t *testing.T) {
		r := newRig(t, nil)
		r.hangs = gpuHang
		r.Opened()
		r.clock.fire()
		r.hangs = nil // The kernel's log rolled: the hang found once counts.
		r.clock.fire()
		if r.reloads != 1 || len(r.recreates) != 1 || !r.recreates[0] || !r.told[1].Software || len(r.told[0].Hangs) != 1 {
			t.Errorf("reloads %d, new views %v, told %+v", r.reloads, r.recreates, r.told)
		}
	})

	t.Run("a page that calls the server is not silent", func(t *testing.T) {
		r := newRig(t, nil)
		r.Opened()
		r.call(t, "/ui.v1.UiService/GetEnvironment")
		if !r.clock.timers[0].stopCalled || r.clock.fire() != 0 || len(r.told) != 0 {
			t.Errorf("told %+v after a call", r.told)
		}
		if !strings.Contains(r.log.String(), `msg="window: the page calls the server" page=1`) {
			t.Errorf("log:\n%s", r.log)
		}
		// A stale timer that fires anyway finds the page heard.
		r.clock.timers[0].f()
		if len(r.told) != 0 || r.reloads != 0 {
			t.Errorf("a stale timer told %+v", r.told)
		}
	})

	t.Run("files of the page are no call", func(t *testing.T) {
		r := newRig(t, nil)
		r.Opened()
		for _, path := range []string{"/", "/index.html", "/assets/index-abc.js", "/docs/", "/tilasms/x/index.html", "/ui.v1.UiService/"} {
			r.call(t, path)
		}
		r.clock.fire()
		if len(r.told) != 1 || r.told[0].Did != Reloaded {
			t.Errorf("told %+v; files counted as calls", r.told)
		}
	})

	// W366: the web process died by itself mid-session, the kernel logged no GPU hang.
	t.Run("a web process that ends mid-session loads its page again, with no GPU hang logged", func(t *testing.T) {
		r := newRig(t, nil)
		r.Hangs = func(context.Context, time.Time) ([]Hang, error) {
			return nil, errors.New("journalctl: permission denied")
		}
		r.Opened()
		r.call(t, "/ui.v1.UiService/GetEnvironment")
		for range 3 {
			r.call(t, "/plan.v1.WishService/Watch")
		}
		r.Ended("terminated")
		if r.reloads != 1 || len(r.told) != 1 || r.told[0].Did != Reloaded || len(r.told[0].Hangs) != 0 || r.told[0].Ended != "terminated" {
			t.Fatalf("reloads %d, told %+v", r.reloads, r.told)
		}
		if !strings.Contains(r.log.String(), "window: the page's web process ended") ||
			!strings.Contains(r.log.String(), "journalctl: permission denied") {
			t.Errorf("log:\n%s", r.log)
		}
		// The page loaded again calls: nothing more.
		r.call(t, "/ui.v1.UiService/GetEnvironment")
		if r.clock.fire() != 0 || len(r.told) != 1 {
			t.Errorf("told %+v", r.told)
		}
	})

	t.Run("a step that fails is not tried again", func(t *testing.T) {
		r := newRig(t, errors.New("no display"))
		r.Opened()
		r.clock.fire()
		if r.reloads != 1 || len(r.told) != 1 || r.told[0].Err == nil || r.told[0].Did != Nothing {
			t.Fatalf("reloads %d, told %+v", r.reloads, r.told)
		}
		if r.clock.fire() != 0 {
			t.Error("the page that did not load is watched")
		}
		r.Ended("crashed")
		r.Ended("crashed")
		if r.reloads != 1 || len(r.recreates) != 1 || len(r.told) != 3 || r.told[2].Err != nil || r.told[2].Did != Nothing {
			t.Errorf("then ended: reloads %d, new views %v, told %+v", r.reloads, r.recreates, r.told)
		}
	})

	t.Run("without a rescue, only told", func(t *testing.T) {
		r := newRig(t, nil)
		r.Reload, r.Recreate = nil, nil
		r.Opened()
		r.clock.fire()
		if len(r.told) != 1 || r.told[0].Did != Nothing {
			t.Errorf("told %+v", r.told)
		}
	})
}

// TestWatchLoaded covers Loaded: each load event is logged as it comes, timed from djinn up's start.
func TestWatchLoaded(t *testing.T) {
	r := newRig(t, nil)
	r.Started = time.Now().Add(-2 * time.Second)
	r.Opened()
	r.Loaded("failed: wails://localhost/: Load request cancelled")
	if !strings.Contains(r.log.String(), `msg="window: load" event="failed: wails://localhost/: Load request cancelled" page=1 since_start=2`) {
		t.Errorf("log:\n%s", r.log)
	}
}

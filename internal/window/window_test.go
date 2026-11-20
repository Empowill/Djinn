package window

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/empowill/djinn/internal/logx"
)

// rig is a Watch on a fake kernel log and fake web processes, that counts its reloads and new views and keeps what it
// told.
type rig struct {
	*Watch
	since     []time.Time
	hangs     []Hang
	reloads   int
	recreates []bool // software, by new view
	told      []Event
	log       *bytes.Buffer
	timers    []*timer
}

// timer is a silence timer the rig armed, fired by hand.
type timer struct {
	after   time.Duration
	fire    func()
	stopped bool
}

func newRig(t *testing.T, fail error) *rig {
	r := &rig{log: &bytes.Buffer{}}
	t.Cleanup(logx.Use(logx.New(r.log, slog.LevelInfo)))
	started := time.Date(2026, 10, 17, 21, 27, 25, 0, time.UTC)
	cpu := time.Second
	r.Watch = &Watch{
		Started: started,
		Sample:  -1,
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
		AfterFunc: func(d time.Duration, f func()) func() bool {
			tm := &timer{after: d, fire: f}
			r.timers = append(r.timers, tm)
			return func() bool { tm.stopped = true; return true }
		},
		Probe: func(context.Context, string) (string, error) {
			return `{"ready":"complete","splash":true,"root":0}`, nil
		},
	}
	return r
}

// gpuHang is the kernel's line of 17/10.
var gpuHang = []Hang{{Time: time.Date(2026, 10, 17, 21, 26, 55, 0, time.UTC), Line: "i915 0000:00:02.0: [drm] Xorg[8937] context reset due to GPU hang"}}

// TestWatch covers Watch: a page whose web process ended is logged with WebKit's state and the kernel's GPU hangs,
// told, then loaded again, then given a new view, in software rendering only after a GPU hang, once each per djinn up,
// never in a loop.
func TestWatch(t *testing.T) {
	t.Run("a dead page loads again, then gets a new view on the GPU, then is only told", func(t *testing.T) {
		r := newRig(t, nil)
		r.Loaded("started")
		r.Loaded("committed")
		r.Opened()
		r.Ended("crashed")
		if r.reloads != 1 || len(r.recreates) != 0 || len(r.told) != 1 || r.told[0].Did != Reloaded || r.told[0].Ended != "crashed" {
			t.Fatalf("reloads %d, new views %v, told %+v", r.reloads, r.recreates, r.told)
		}
		if want := r.Started.Add(-Lookback); len(r.since) != 1 || !r.since[0].Equal(want) {
			t.Errorf("kernel read since %v; want %v", r.since, want)
		}
		for _, want := range []string{`msg="window: the page's web process ended" page=1 ended=crashed`, "loads=\"[started +", "committed +",
			"pid 4242 WebKitWebProces S rss 155 MB cpu 1.0 s", "gpu_hangs=0", `msg="window: the page loads again" page=2`} {
			if !strings.Contains(r.log.String(), want) {
				t.Errorf("log lacks %q:\n%s", want, r.log)
			}
		}

		// The page loaded again loses its web process too: a new view, on the GPU.
		r.Ended("crashed")
		if len(r.recreates) != 1 || r.recreates[0] || len(r.told) != 2 || r.told[1].Did != Recreated || r.told[1].Software {
			t.Fatalf("new views %v, told %+v", r.recreates, r.told)
		}
		// Dead again: told, nothing more.
		r.Ended("crashed")
		if r.reloads != 1 || len(r.recreates) != 1 || len(r.told) != 3 || r.told[2].Did != Nothing {
			t.Errorf("then: reloads %d, new views %v, told %+v", r.reloads, r.recreates, r.told)
		}
	})

	t.Run("after a GPU hang the new view draws in software rendering", func(t *testing.T) {
		r := newRig(t, nil)
		r.hangs = gpuHang
		r.Opened()
		r.Ended("crashed")
		r.hangs = nil // The kernel's log rolled: the hang found once counts.
		r.Ended("crashed")
		if r.reloads != 1 || len(r.recreates) != 1 || !r.recreates[0] || !r.told[1].Software || len(r.told[0].Hangs) != 1 {
			t.Errorf("reloads %d, new views %v, told %+v", r.reloads, r.recreates, r.told)
		}
	})

	// W366: the web process died by itself mid-session, the kernel logged no GPU hang.
	t.Run("a web process that ends mid-session loads its page again, with no GPU hang logged", func(t *testing.T) {
		r := newRig(t, nil)
		r.Hangs = func(context.Context, time.Time) ([]Hang, error) {
			return nil, errors.New("journalctl: permission denied")
		}
		r.Opened()
		r.Ended("terminated")
		if r.reloads != 1 || len(r.told) != 1 || r.told[0].Did != Reloaded || len(r.told[0].Hangs) != 0 || r.told[0].Ended != "terminated" {
			t.Fatalf("reloads %d, told %+v", r.reloads, r.told)
		}
		if !strings.Contains(r.log.String(), "window: the page's web process ended") ||
			!strings.Contains(r.log.String(), "journalctl: permission denied") {
			t.Errorf("log:\n%s", r.log)
		}
	})

	t.Run("a step that fails is not tried again", func(t *testing.T) {
		r := newRig(t, errors.New("no display"))
		r.Opened()
		r.Ended("crashed")
		if r.reloads != 1 || len(r.told) != 1 || r.told[0].Err == nil || r.told[0].Did != Nothing {
			t.Fatalf("reloads %d, told %+v", r.reloads, r.told)
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
		r.Ended("crashed")
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

// TestWatchSilent covers a page that loads and never calls the server (W373, W172; decision Q111): Silence after its
// load finished it is logged with a probe of the page and the web processes, then loaded again once per djinn up,
// and told nowhere else.
func TestWatchSilent(t *testing.T) {
	load := func(r *rig) {
		r.Loaded("started")
		r.Loaded("committed")
		r.Loaded("finished")
	}

	t.Run("silent from its load finished, probed, loaded again once, then only logged", func(t *testing.T) {
		r := newRig(t, nil)
		r.Opened()
		r.Loaded("started")
		r.Loaded("committed")
		if len(r.timers) != 0 {
			t.Fatalf("timers %+v before the load finished", r.timers)
		}
		r.Loaded("finished")
		if len(r.timers) != 1 || r.timers[0].after != Silence || Silence != 3*time.Second {
			t.Fatalf("timers %+v; want one of %s", r.timers, Silence)
		}
		r.timers[0].fire()
		if r.reloads != 1 || len(r.recreates) != 0 || len(r.told) != 0 {
			t.Fatalf("reloads %d, new views %v, told %+v", r.reloads, r.recreates, r.told)
		}
		for _, want := range []string{`msg="window: the page is silent" page=1 after=3s probe="answered in `,
			`{\"ready\":\"complete\",\"splash\":true,\"root\":0}`, "loads=\"[started +", "finished +",
			"pid 4242 WebKitWebProces S rss 155 MB", `msg="window: the page loads again" page=2 silent=true`} {
			if !strings.Contains(r.log.String(), want) {
				t.Errorf("log lacks %q:\n%s", want, r.log)
			}
		}

		// The page loaded again is silent too: logged, not loaded again.
		r.log.Reset()
		r.Loaded("committed")
		r.Loaded("finished")
		if len(r.timers) != 2 || r.timers[1].stopped {
			t.Fatalf("timers %+v", r.timers)
		}
		r.timers[1].fire()
		if r.reloads != 1 || len(r.told) != 0 || !strings.Contains(r.log.String(), `msg="window: the page is silent" page=2`) ||
			!strings.Contains(r.log.String(), "the silent page was loaded again already") {
			t.Errorf("reloads %d, told %+v, log:\n%s", r.reloads, r.told, r.log)
		}

		// A web process that ends still gets its own rescue: the silent reload is not counted against it.
		r.Ended("crashed")
		if r.reloads != 2 || len(r.told) != 1 || r.told[0].Did != Reloaded {
			t.Errorf("after an end: reloads %d, told %+v", r.reloads, r.told)
		}
	})

	t.Run("a page that calls arms nothing, or stops its timer", func(t *testing.T) {
		r := newRig(t, nil)
		calls := r.Handler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		get := func(path string) {
			calls.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, path, nil))
		}
		r.Opened()
		r.Loaded("committed")
		get("/assets/index-abc.js") // a file of the page is no call
		get("/ui.v1.UiService/Get")
		r.Loaded("finished")
		if len(r.timers) != 0 {
			t.Fatalf("timers %+v for a page that called before its load finished", r.timers)
		}
		if !strings.Contains(r.log.String(), `msg="window: the page calls the server" page=1 before_load_finished=true`) {
			t.Errorf("log:\n%s", r.log)
		}

		r.Loaded("committed") // the developer reloads: a new document, not heard yet
		r.Loaded("finished")
		get("/plan.v1.WishService/List")
		get("/plan.v1.WishService/List")
		if len(r.timers) != 1 || !r.timers[0].stopped {
			t.Fatalf("timers %+v; want one stopped", r.timers)
		}
		r.timers[0].fire() // fired as it was stopped
		if r.reloads != 0 || strings.Contains(r.log.String(), "the page is silent") ||
			strings.Count(r.log.String(), "the page calls the server") != 2 || !strings.Contains(r.log.String(), "after_load=") {
			t.Errorf("reloads %d, log:\n%s", r.reloads, r.log)
		}
	})

	t.Run("a page that calls during the probe is not loaded again", func(t *testing.T) {
		r := newRig(t, nil)
		r.Probe = func(context.Context, string) (string, error) {
			r.Called()
			return "{}", nil
		}
		r.Opened()
		load(r)
		r.timers[0].fire()
		if r.reloads != 0 || !strings.Contains(r.log.String(), "the silent page called or was rescued meanwhile") {
			t.Errorf("reloads %d, log:\n%s", r.reloads, r.log)
		}
	})

	t.Run("a probe with no answer says so", func(t *testing.T) {
		r := newRig(t, nil)
		r.ProbeWait = 20 * time.Millisecond
		r.Probe = func(ctx context.Context, _ string) (string, error) {
			<-ctx.Done() // the JS thread does not run
			return "", ctx.Err()
		}
		r.Opened()
		load(r)
		r.timers[0].fire()
		if r.reloads != 1 || !strings.Contains(r.log.String(), `probe="no answer within 20ms"`) {
			t.Errorf("reloads %d, log:\n%s", r.reloads, r.log)
		}
	})

	t.Run("a web process that ends stops the timer", func(t *testing.T) {
		r := newRig(t, nil)
		r.Opened()
		load(r)
		r.Ended("crashed")
		if len(r.timers) != 1 || !r.timers[0].stopped || r.reloads != 1 {
			t.Errorf("timers %+v, reloads %d", r.timers, r.reloads)
		}
	})
}

// TestIsCall covers isCall: a Connect method of the server, not a file of the page.
func TestIsCall(t *testing.T) {
	for path, want := range map[string]bool{
		"/ui.v1.UiService/Get": true, "/plan.v1.WishService/List": true, "/": false, "/index.html": false,
		"/assets/index-abc.js": false, "/ui.v1.UiService/": false, "/UiService/Get": false, "/a.b.UiService/Get/x": false,
	} {
		if got := isCall(path); got != want {
			t.Errorf("isCall(%q) = %v; want %v", path, got, want)
		}
	}
}

package window

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
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

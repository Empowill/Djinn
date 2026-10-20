package machine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	djinnv1connect "github.com/empowill/djinn/gen/go/djinn/v1/djinnv1connect"
)

// settingsHome is a home folder whose settings.json holds settings; none when settings is "".
func settingsHome(t *testing.T, settings string) string {
	t.Helper()
	home := t.TempDir()
	if settings != "" {
		if err := os.WriteFile(filepath.Join(home, settingsFile), []byte(settings), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

// TestReadNotch: the notch saved in settings.json; Medium when none is, when the file is missing or broken, and for
// an old saved Light.
func TestReadNotch(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		noHome   bool
		settings string
		want     djinnv1.LoadNotch
	}{
		{name: "no home", noHome: true, want: djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM},
		{name: "missing file", want: djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM},
		{name: "broken file", settings: `{"load":`, want: djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM},
		{name: "not saved", settings: `{"shortcut":"Ctrl+J"}`, want: djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM},
		{name: "saved light", settings: `{"shortcut":"Ctrl+J","load":"LOAD_NOTCH_LIGHT"}`,
			want: djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM},
		{name: "saved numeric light", settings: `{"load":2}`, want: djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM},
		{name: "saved overclock", settings: `{"load":"LOAD_NOTCH_OVERCLOCK"}`,
			want: djinnv1.LoadNotch_LOAD_NOTCH_OVERCLOCK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			home := ""
			if !c.noHome {
				home = settingsHome(t, c.settings)
			}
			if got := ReadNotch(home); got != c.want {
				t.Errorf("ReadNotch = %v, want %v", got, c.want)
			}
		})
	}
}

// TestWriteNotch: the notch is saved in settings.json beside the other settings, an old Light as Medium; without a
// home, nothing is written.
func TestWriteNotch(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		noHome  bool
		notches []djinnv1.LoadNotch // written in turn
		want    djinnv1.LoadNotch
	}{
		{name: "overclock", notches: []djinnv1.LoadNotch{djinnv1.LoadNotch_LOAD_NOTCH_OVERCLOCK},
			want: djinnv1.LoadNotch_LOAD_NOTCH_OVERCLOCK},
		{name: "written again", notches: []djinnv1.LoadNotch{djinnv1.LoadNotch_LOAD_NOTCH_OVERCLOCK,
			djinnv1.LoadNotch_LOAD_NOTCH_HIGH}, want: djinnv1.LoadNotch_LOAD_NOTCH_HIGH},
		{name: "light", notches: []djinnv1.LoadNotch{djinnv1.LoadNotch_LOAD_NOTCH_LIGHT},
			want: djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM},
		{name: "no home", noHome: true, notches: []djinnv1.LoadNotch{djinnv1.LoadNotch_LOAD_NOTCH_HIGH}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if c.noHome {
				if err := WriteNotch("", c.notches[0]); err != nil {
					t.Errorf("WriteNotch without a home: %v", err)
				}
				return
			}
			home := settingsHome(t, `{"shortcut":"Ctrl+J"}`)
			for _, n := range c.notches {
				if err := WriteNotch(home, n); err != nil {
					t.Fatalf("WriteNotch(%v): %v", n, err)
				}
			}
			if got := ReadNotch(home); got != c.want {
				t.Errorf("ReadNotch after WriteNotch = %v, want %v", got, c.want)
			}
			data, err := os.ReadFile(filepath.Join(home, settingsFile))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), "Ctrl+J") {
				t.Errorf("WriteNotch did not preserve the shortcut in %s", data)
			}
		})
	}
}

// TestReadIfritIdle: the minutes before an ifrit is put out, from 5 to 240; 0 when settings.json does not say.
func TestReadIfritIdle(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		noHome   bool
		settings string
		want     uint32
	}{
		{name: "no home", noHome: true},
		{name: "missing file"},
		{name: "broken file", settings: `{"ifritIdleMinutes":`},
		{name: "saved", settings: `{"ifritIdleMinutes":30}`, want: 30},
		{name: "too short", settings: `{"ifritIdleMinutes":4}`},
		{name: "too long", settings: `{"ifritIdleMinutes":241}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			home := ""
			if !c.noHome {
				home = settingsHome(t, c.settings)
			}
			if got := ReadIfritIdle(home); got != c.want {
				t.Errorf("ReadIfritIdle = %d, want %d", got, c.want)
			}
		})
	}
}

// TestReadLeadDriven: whether Djinn drives the lead, true unless settings.json says false.
func TestReadLeadDriven(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		noHome   bool
		settings string
		want     bool
	}{
		{name: "no home", noHome: true, want: true},
		{name: "missing file", want: true},
		{name: "broken file", settings: `{"leadDriven":`, want: true},
		{name: "unset", settings: `{}`, want: true},
		{name: "saved true", settings: `{"leadDriven":true}`, want: true},
		{name: "saved false", settings: `{"leadDriven":false}`, want: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			home := ""
			if !c.noHome {
				home = settingsHome(t, c.settings)
			}
			if got := ReadLeadDriven(home); got != c.want {
				t.Errorf("ReadLeadDriven = %v, want %v", got, c.want)
			}
		})
	}
}

// serveLoad serves the load service of LoadHandler on a test server, and returns its client and the service.
func serveLoad(t *testing.T, home string, m *Monitor, setPolicy func(Policy), broadcast func(djinnv1.LoadNotch),
	workerMemory WorkerMemoryFunc, opts ...LoadOption,
) (djinnv1connect.LoadServiceClient, *loadService) {
	t.Helper()
	var s *loadService
	opts = append(opts, func(ls *loadService) { s = ls })
	mux := http.NewServeMux()
	mux.Handle(LoadHandler(home, m, setPolicy, broadcast, workerMemory, opts...))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return djinnv1connect.NewLoadServiceClient(srv.Client(), srv.URL), s
}

// TestLoadHandler: a notch saved as auto is applied at start: the bar shows the notch the scheduler runs.
func TestLoadHandler(t *testing.T) {
	t.Parallel()
	home := settingsHome(t, `{"load":"LOAD_NOTCH_AUTO"}`)
	monitor := NewMonitor(NotchPolicy(ReadNotch(home)), func() (Snapshot, error) {
		return Snapshot{Cores: 16}, nil
	})
	var calls []djinnv1.LoadNotch
	var lastPolicy Policy
	client, _ := serveLoad(t, home, monitor, func(p Policy) {
		calls = append(calls, p.Notch)
		lastPolicy = p
	}, nil, nil, WithLoadTickInterval(0))

	res, err := client.Get(t.Context(), connect.NewRequest(&djinnv1.LoadServiceGetRequest{}))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	resp := res.Msg
	var barSlots int32
	for _, ns := range resp.GetNotchSlots() {
		if ns.GetNotch() == resp.GetNotch() {
			barSlots = ns.GetSlots()
			break
		}
	}
	if resp.GetNotch() != monitor.Policy().Notch || barSlots != resp.GetSlots() || len(calls) == 0 {
		t.Fatalf("the bar shows %v (%d slots), the scheduler runs %v (%d slots); setPolicy calls: %v",
			resp.GetNotch(), barSlots, monitor.Policy().Notch, resp.GetSlots(), calls)
	}
	if !monitor.Policy().Auto || !lastPolicy.Auto {
		t.Errorf("policy Auto: monitor=%v, setPolicy=%v, want true", monitor.Policy().Auto, lastPolicy.Auto)
	}
}

// TestLoadService_Get: the notch in effect, the percentile it engages a worker at, and, at each read, how many workers
// the scheduler lets run now and at each notch, how many run, how many the developer started by hand, and whether
// lowering the notch pauses workers.
func TestLoadService_Get(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"medium", func(t *testing.T) {
			client, _ := serveLoad(t, t.TempDir(), &Monitor{policy: NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM)},
				nil, nil, nil)
			res, err := client.Get(t.Context(), connect.NewRequest(&djinnv1.LoadServiceGetRequest{}))
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if res.Msg.GetNotch() != djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM {
				t.Errorf("Get notch: got %v, want MEDIUM", res.Msg.GetNotch())
			}
			if res.Msg.GetWorkerPercentile() != 80 || res.Msg.GetWorkerEngagedAt() != "engaged at p80 (medium)" {
				t.Errorf("Get percentile: p%d, %q", res.Msg.GetWorkerPercentile(), res.Msg.GetWorkerEngagedAt())
			}
			if res.Msg.GetAuto() || res.Msg.GetAutoReason() != nil {
				t.Errorf("Get: %v, want no auto", res.Msg)
			}
		}},
		{"slots", func(t *testing.T) {
			snap := slotMachines[3]
			m := NewMonitor(NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_HIGH), func() (Snapshot, error) { return snap, nil })
			m.every = 0
			client, _ := serveLoad(t, t.TempDir(), m, nil, nil, nil, WithLoadTickInterval(0),
				WithLoadRunningWorkers(func() int { return 7 }), WithLoadPausedWorkers(func() int { return 1 }),
				WithLoadForcedWorkers(func() int { return 1 }), WithLoadLowerPauses(func() bool { return true }))
			res, err := client.Set(t.Context(), connect.NewRequest(&djinnv1.LoadServiceSetRequest{Notch: djinnv1.LoadNotch_LOAD_NOTCH_HIGH}))
			if err != nil {
				t.Fatal(err)
			}
			if res.Msg.GetNotch() != djinnv1.LoadNotch_LOAD_NOTCH_HIGH {
				t.Fatalf("Set: %v", res.Msg)
			}
			got, err := client.Get(t.Context(), connect.NewRequest(&djinnv1.LoadServiceGetRequest{}))
			if err != nil {
				t.Fatal(err)
			}
			g := got.Msg
			if g.GetSlots() != 6 || g.GetRunningWorkers() != 7 || g.GetPausedWorkers() != 1 || g.GetForcedWorkers() != 1 ||
				!g.GetLowerPauses() {
				t.Errorf("Get: slots %d, running %d, paused %d, forced %d, lower pauses %v", g.GetSlots(), g.GetRunningWorkers(),
					g.GetPausedWorkers(), g.GetForcedWorkers(), g.GetLowerPauses())
			}
			want := map[djinnv1.LoadNotch]int32{}
			for _, ns := range m.NotchSlots() {
				want[ns.Notch] = int32(ns.Slots)
			}
			if len(g.GetNotchSlots()) != len(ManualNotches) {
				t.Fatalf("notch slots: %v", g.GetNotchSlots())
			}
			for _, ns := range g.GetNotchSlots() {
				if ns.GetSlots() != want[ns.GetNotch()] {
					t.Errorf("%v: %d slots, want %d", ns.GetNotch(), ns.GetSlots(), want[ns.GetNotch()])
				}
			}
			if want[djinnv1.LoadNotch_LOAD_NOTCH_HIGH] != g.GetSlots() {
				t.Errorf("the notch in effect shows %d, runs %d", want[djinnv1.LoadNotch_LOAD_NOTCH_HIGH], g.GetSlots())
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.run(t)
		})
	}
}

// TestLoadService_Set: a notch set is applied to the policy, broadcast, and saved; one unspecified is refused; the
// overrides of djinn up's flags stay.
func TestLoadService_Set(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"unspecified", func(t *testing.T) {
			client, _ := serveLoad(t, t.TempDir(), &Monitor{policy: DefaultPolicy()}, nil, nil, nil)
			if _, err := client.Set(t.Context(), connect.NewRequest(&djinnv1.LoadServiceSetRequest{
				Notch: djinnv1.LoadNotch_LOAD_NOTCH_UNSPECIFIED,
			})); err == nil {
				t.Fatal("Set with UNSPECIFIED should fail")
			}
		}},
		{"minimal", func(t *testing.T) {
			ctx := t.Context()
			home := t.TempDir()
			monitor := &Monitor{policy: NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM)}
			var updatedPolicy Policy
			setPolicyCalled := 0
			var broadcastedNotch djinnv1.LoadNotch
			broadcastCalled := 0
			client, _ := serveLoad(t, home, monitor, func(p Policy) {
				setPolicyCalled++
				updatedPolicy = p
			}, func(n djinnv1.LoadNotch) {
				broadcastCalled++
				broadcastedNotch = n
			}, nil)
			setRes, err := client.Set(ctx, connect.NewRequest(&djinnv1.LoadServiceSetRequest{
				Notch: djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL,
			}))
			if err != nil {
				t.Fatalf("Set MINIMAL: %v", err)
			}
			if setRes.Msg.GetNotch() != djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL {
				t.Errorf("Set response notch: got %v, want MINIMAL", setRes.Msg.GetNotch())
			}
			// The callbacks, the monitor's state, and the notch saved.
			if setPolicyCalled != 1 {
				t.Errorf("setPolicy called %d times, want 1", setPolicyCalled)
			}
			if updatedPolicy.Notch != djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL {
				t.Errorf("updatedPolicy notch: got %v, want MINIMAL", updatedPolicy.Notch)
			}
			if broadcastCalled != 1 {
				t.Errorf("broadcast called %d times, want 1", broadcastCalled)
			}
			if broadcastedNotch != djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL {
				t.Errorf("broadcasted notch: got %v, want MINIMAL", broadcastedNotch)
			}
			if monitor.Policy().Notch != djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL {
				t.Errorf("monitor policy notch: got %v, want MINIMAL", monitor.Policy().Notch)
			}
			if diskNotch := ReadNotch(home); diskNotch != djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL {
				t.Errorf("disk notch: got %v, want MINIMAL", diskNotch)
			}
			// Get after Set.
			getRes, err := client.Get(ctx, connect.NewRequest(&djinnv1.LoadServiceGetRequest{}))
			if err != nil {
				t.Fatalf("Get after Set: %v", err)
			}
			if getRes.Msg.GetNotch() != djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL {
				t.Errorf("Get after Set: got %v, want MINIMAL", getRes.Msg.GetNotch())
			}
			if getRes.Msg.GetWorkerPercentile() != 95 || getRes.Msg.GetWorkerEngagedAt() != "engaged at p95 (minimal)" {
				t.Errorf("Get after Set percentile: p%d, %q", getRes.Msg.GetWorkerPercentile(), getRes.Msg.GetWorkerEngagedAt())
			}
		}},
		{"keeps worker memory guard", func(t *testing.T) {
			initPolicy := NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM)
			initPolicy.Workers = 5
			initPolicy.WorkerMemory = 4 * GiB
			initPolicy.WorkerMemoryGuard = 1.75
			monitor := NewMonitor(initPolicy, func() (Snapshot, error) {
				return Snapshot{Cores: 16}, nil
			})
			var recorded Policy
			client, _ := serveLoad(t, t.TempDir(), monitor, func(p Policy) { recorded = p }, nil, nil, WithLoadTickInterval(0))
			if _, err := client.Set(t.Context(), connect.NewRequest(&djinnv1.LoadServiceSetRequest{
				Notch: djinnv1.LoadNotch_LOAD_NOTCH_HIGH,
			})); err != nil {
				t.Fatalf("Set HIGH: %v", err)
			}
			got := monitor.Policy()
			if got.Workers != 5 || got.WorkerMemory != 4*GiB || got.WorkerMemoryGuard != 1.75 {
				t.Errorf("monitor policy overrides after Set: Workers=%d WorkerMemory=%d WorkerMemoryGuard=%v, want 5, %d, 1.75",
					got.Workers, got.WorkerMemory, got.WorkerMemoryGuard, 4*GiB)
			}
			if recorded.Workers != 5 || recorded.WorkerMemory != 4*GiB || recorded.WorkerMemoryGuard != 1.75 {
				t.Errorf("setPolicy overrides after Set: Workers=%d WorkerMemory=%d WorkerMemoryGuard=%v, want 5, %d, 1.75",
					recorded.Workers, recorded.WorkerMemory, recorded.WorkerMemoryGuard, 4*GiB)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.run(t)
		})
	}
}

// TestLoadService_Tick: in auto mode, set from Max, a build outside Djinn lowers the notch one step once it stayed 3
// minutes above the high mark, applied to the policy and broadcast, and the reason read by Get and Watch; a manual
// notch leaves auto mode: no reason any more.
func TestLoadService_Tick(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	home := t.TempDir()
	fakeNow := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return fakeNow }
	outside := Outside{CPU: 60, CPUKnown: true}
	source := func(time.Time) (Outside, bool) { return outside, true }
	monitor := &Monitor{policy: NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM)}
	var latestPolicy Policy
	var latestBroadcast djinnv1.LoadNotch
	client, s := serveLoad(t, home, monitor,
		func(p Policy) { latestPolicy = p },
		func(n djinnv1.LoadNotch) { latestBroadcast = n },
		nil,
		WithLoadClock(clock),
		WithLoadOutside(source),
		WithLoadTickInterval(0), // manual beats in the test
	)

	getRes, err := client.Get(ctx, connect.NewRequest(&djinnv1.LoadServiceGetRequest{}))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if getRes.Msg.GetNotch() != djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM || getRes.Msg.GetAuto() || getRes.Msg.GetAutoReason() != nil {
		t.Fatalf("Initial Get: %v", getRes.Msg)
	}

	// Max chosen, then Auto: it starts from Max, the next beats deciding; nothing measured yet.
	if _, err := client.Set(ctx, connect.NewRequest(&djinnv1.LoadServiceSetRequest{Notch: djinnv1.LoadNotch_LOAD_NOTCH_MAX})); err != nil {
		t.Fatal(err)
	}
	setRes, err := client.Set(ctx, connect.NewRequest(&djinnv1.LoadServiceSetRequest{Notch: djinnv1.LoadNotch_LOAD_NOTCH_AUTO}))
	if err != nil {
		t.Fatalf("Set AUTO: %v", err)
	}
	if !setRes.Msg.GetAuto() || setRes.Msg.GetNotch() != djinnv1.LoadNotch_LOAD_NOTCH_MAX || setRes.Msg.GetChosenNotch() != djinnv1.LoadNotch_LOAD_NOTCH_MAX {
		t.Fatalf("Set AUTO: %v, want auto at Max", setRes.Msg)
	}
	if ReadNotch(home) != djinnv1.LoadNotch_LOAD_NOTCH_AUTO {
		t.Errorf("saved notch %v, want auto", ReadNotch(home))
	}

	// A build outside Djinn: one notch down once it stayed 3 minutes above the high mark, from the first beat on,
	// applied to the policy and broadcast.
	for range 10 {
		fakeNow = fakeNow.Add(AutoBeat)
		s.Tick()
	}
	if latestPolicy.Notch != djinnv1.LoadNotch_LOAD_NOTCH_HIGH || !latestPolicy.Auto || latestBroadcast != djinnv1.LoadNotch_LOAD_NOTCH_HIGH {
		t.Errorf("after 3 min outside: policy %v auto %v, broadcast %v, want High", latestPolicy.Notch, latestPolicy.Auto, latestBroadcast)
	}
	getRes, err = client.Get(ctx, connect.NewRequest(&djinnv1.LoadServiceGetRequest{}))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	reason := getRes.Msg.GetAutoReason()
	if getRes.Msg.GetNotch() != djinnv1.LoadNotch_LOAD_NOTCH_HIGH || reason.GetOutsidePercent() != 60 ||
		reason.GetStep() != djinnv1.LoadAutoStep_LOAD_AUTO_STEP_LOWER || reason.GetNotch() != djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM ||
		reason.GetInSeconds() != 180 {
		t.Errorf("Get in auto: %v, want High, outside 60 %%, down to Medium in 3 min", getRes.Msg)
	}
	if w := s.snapshot(ctx); w.GetAutoReason().GetOutsidePercent() != 60 {
		t.Errorf("Watch: reason %v", w.GetAutoReason())
	}

	// A manual notch leaves auto mode: no reason any more.
	setRes, err = client.Set(ctx, connect.NewRequest(&djinnv1.LoadServiceSetRequest{Notch: djinnv1.LoadNotch_LOAD_NOTCH_MAX}))
	if err != nil {
		t.Fatalf("Set MAX: %v", err)
	}
	if setRes.Msg.GetAuto() || setRes.Msg.GetNotch() != djinnv1.LoadNotch_LOAD_NOTCH_MAX || latestPolicy.Notch != djinnv1.LoadNotch_LOAD_NOTCH_MAX {
		t.Errorf("Set MAX: %v, policy %v", setRes.Msg, latestPolicy.Notch)
	}
	getRes, err = client.Get(ctx, connect.NewRequest(&djinnv1.LoadServiceGetRequest{}))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if getRes.Msg.GetAuto() || getRes.Msg.GetAutoReason() != nil {
		t.Errorf("Get after manual set: %v", getRes.Msg)
	}
}

// TestLoadService_OneSetting: a Set in the middle of a message does not mix two settings in it: its notch and its
// slots come from one reading, the one before the Set; the next message says the new setting (W27).
func TestLoadService_OneSetting(t *testing.T) {
	t.Parallel()
	snap := slotMachines[3]
	medium, high := NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM), NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_HIGH)
	ms, _ := medium.Slots(snap)
	hs, _ := high.Slots(snap)
	if ms == hs {
		t.Fatalf("Medium and High run %d workers each on this machine: no mix to see", ms)
	}
	// reading is what one message says: its notch, its slots and its CPU slots.
	type reading struct {
		notch       djinnv1.LoadNotch
		slots, cpus int32
	}
	cases := []struct {
		name string
		read func(s *loadService) reading
	}{
		{"watch", func(s *loadService) reading {
			r := s.snapshot(t.Context())
			return reading{r.GetNotch(), r.GetSlots(), r.GetCpuSlots()}
		}},
		{"get", func(s *loadService) reading {
			r, err := s.Get(t.Context(), connect.NewRequest(&djinnv1.LoadServiceGetRequest{}))
			if err != nil {
				t.Fatal(err)
			}
			return reading{r.Msg.GetNotch(), r.Msg.GetSlots(), r.Msg.GetCpuSlots()}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			m := NewMonitor(medium, func() (Snapshot, error) { return snap, nil })
			var s *loadService
			var once sync.Once
			// The workers are counted while the message is made: the setting moves to High then.
			_, s = serveLoad(t, t.TempDir(), m, nil, nil, nil, WithLoadTickInterval(0), WithLoadRunningWorkers(func() int {
				once.Do(func() {
					if _, err := s.Set(t.Context(), connect.NewRequest(&djinnv1.LoadServiceSetRequest{
						Notch: djinnv1.LoadNotch_LOAD_NOTCH_HIGH,
					})); err != nil {
						t.Errorf("Set High: %v", err)
					}
				})
				return 1
			}))
			for _, want := range []djinnv1.LoadNotch{djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM, djinnv1.LoadNotch_LOAD_NOTCH_HIGH} {
				got := c.read(s)
				p := NotchPolicy(want)
				slots, _ := p.Slots(snap)
				if w := (reading{want, int32(slots), int32(p.CPUSlots(snap))}); got != w {
					t.Errorf("message: got %+v, want %+v (the setting %v alone)", got, w, want)
				}
			}
		})
	}
}

// TestLoadService_ChosenInEffect: a Set between the reading of the auto mode and that of the policy waits for the
// message to be made: a manual notch's message says the same notch chosen and in effect, the old one, then the new one
// (W30).
func TestLoadService_ChosenInEffect(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		read func(s *loadService) (notch, chosen djinnv1.LoadNotch)
	}{
		{"watch", func(s *loadService) (djinnv1.LoadNotch, djinnv1.LoadNotch) {
			r := s.snapshot(t.Context())
			return r.GetNotch(), r.GetChosenNotch()
		}},
		{"get", func(s *loadService) (djinnv1.LoadNotch, djinnv1.LoadNotch) {
			r, err := s.Get(t.Context(), connect.NewRequest(&djinnv1.LoadServiceGetRequest{}))
			if err != nil {
				t.Fatal(err)
			}
			return r.Msg.GetNotch(), r.Msg.GetChosenNotch()
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			m := NewMonitor(NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM), func() (Snapshot, error) {
				return slotMachines[3], nil
			})
			_, s := serveLoad(t, t.TempDir(), m, nil, nil, nil, WithLoadTickInterval(0))
			set := make(chan struct{})
			var once sync.Once
			s.betweenReads = func() {
				once.Do(func() {
					go func() {
						defer close(set)
						if _, err := s.Set(t.Context(), connect.NewRequest(&djinnv1.LoadServiceSetRequest{
							Notch: djinnv1.LoadNotch_LOAD_NOTCH_HIGH,
						})); err != nil {
							t.Errorf("Set High: %v", err)
						}
					}()
					// The Set ends here when nothing holds it back; once held, the message goes on without it.
					select {
					case <-set:
					case <-time.After(100 * time.Millisecond):
					}
				})
			}
			for i, want := range []djinnv1.LoadNotch{djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM, djinnv1.LoadNotch_LOAD_NOTCH_HIGH} {
				notch, chosen := c.read(s)
				if notch != want || chosen != want {
					t.Errorf("message %d: notch %v, chosen %v; want %v for both", i, notch, chosen, want)
				}
				<-set
			}
		})
	}
}

// TestLoadService_LateBeat: a beat's notch applied after a Set does not undo it: the policy stays the Set's (W30).
func TestLoadService_LateBeat(t *testing.T) {
	t.Parallel()
	m := NewMonitor(NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM), func() (Snapshot, error) {
		return slotMachines[3], nil
	})
	_, s := serveLoad(t, t.TempDir(), m, nil, nil, nil, WithLoadTickInterval(0))
	if _, err := s.Set(t.Context(), connect.NewRequest(&djinnv1.LoadServiceSetRequest{
		Notch: djinnv1.LoadNotch_LOAD_NOTCH_HIGH,
	})); err != nil {
		t.Fatal(err)
	}
	// The beat stepped to Minimal before the Set, and applies it after.
	s.applyNotch(djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL)
	if got := m.Policy().Notch; got != djinnv1.LoadNotch_LOAD_NOTCH_HIGH {
		t.Errorf("policy %v after the late beat, want the Set's LOAD_NOTCH_HIGH", got)
	}
}

// TestLoadService_Watch: the stream sends the state at once, then only what changed, at most once an interval; it
// says the slots Get says. Not parallel: SetWatchInterval sets the interval of every stream.
func TestLoadService_Watch(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"stream", func(t *testing.T) {
			restore := SetWatchInterval(30 * time.Millisecond)
			t.Cleanup(restore)
			monitor := NewMonitor(NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM), func() (Snapshot, error) {
				return Snapshot{
					MemoryTotal:     16 * GiB,
					MemoryAvailable: 10 * GiB,
				}, nil
			})
			var engagedMem atomic.Uint64
			var workerMem atomic.Uint64
			engagedMem.Store(3 * GiB)
			workerMem.Store(1 * GiB)
			workerMemory := func(ctx context.Context, p Policy) (uint64, uint64, error) {
				return engagedMem.Load(), workerMem.Load(), nil
			}
			client, _ := serveLoad(t, t.TempDir(), monitor, nil, nil, workerMemory)

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			stream, err := client.Watch(ctx, connect.NewRequest(&djinnv1.LoadServiceWatchRequest{}))
			if err != nil {
				t.Fatalf("Watch: %v", err)
			}
			recv := make(chan *djinnv1.LoadServiceWatchResponse, 10)
			recvErr := make(chan error, 1)
			go func() {
				for stream.Receive() {
					recv <- stream.Msg()
				}
				recvErr <- stream.Err()
			}()

			// 1. Initial message sent immediately
			var first *djinnv1.LoadServiceWatchResponse
			select {
			case first = <-recv:
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for initial watch response")
			}
			if first.GetNotch() != djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM {
				t.Errorf("initial notch: got %v, want MEDIUM", first.GetNotch())
			}
			if first.GetMemoryTotalBytes() != 16*GiB {
				t.Errorf("initial total: got %v, want 16 GiB", first.GetMemoryTotalBytes())
			}
			if first.GetMemoryAvailableBytes() != 10*GiB {
				t.Errorf("initial available: got %v, want 10 GiB", first.GetMemoryAvailableBytes())
			}
			if first.GetEngagedMemoryBytes() != 3*GiB {
				t.Errorf("initial engaged: got %v, want 3 GiB", first.GetEngagedMemoryBytes())
			}
			if first.GetWorkerMemoryBytes() != 1*GiB {
				t.Errorf("initial worker: got %v, want 1 GiB", first.GetWorkerMemoryBytes())
			}

			// 2. Deduplication: no changes -> no messages sent across ticks (nothing sent when nothing changes)
			select {
			case msg := <-recv:
				t.Fatalf("unexpected message without changes: %v", msg)
			case <-time.After(80 * time.Millisecond):
				// Expected: nothing sent
			}

			// 3. Set notch triggers rate-limited update
			if _, err := client.Set(ctx, connect.NewRequest(&djinnv1.LoadServiceSetRequest{
				Notch: djinnv1.LoadNotch_LOAD_NOTCH_HIGH,
			})); err != nil {
				t.Fatalf("Set HIGH: %v", err)
			}
			// A tick's snapshot may have read the setting before the Set: its message still says Medium, the next High.
			deadline := time.After(time.Second)
			for high := false; !high; {
				select {
				case second := <-recv:
					high = second.GetNotch() == djinnv1.LoadNotch_LOAD_NOTCH_HIGH
				case <-deadline:
					t.Fatal("timed out waiting for the notch High after Set")
				}
			}

			// 4. Memory change detected on periodic tick
			engagedMem.Store(5 * GiB)
			workerMem.Store(2 * GiB)
			var third *djinnv1.LoadServiceWatchResponse
			select {
			case third = <-recv:
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for update after memory change")
			}
			if third.GetEngagedMemoryBytes() != 5*GiB || third.GetWorkerMemoryBytes() != 2*GiB {
				t.Errorf("third memory: got engaged=%v worker=%v, want 5 GiB and 2 GiB",
					third.GetEngagedMemoryBytes(), third.GetWorkerMemoryBytes())
			}

			// 5. Rate limiting: rapid changes within interval are throttled
			t0 := time.Now()
			_, _ = client.Set(ctx, connect.NewRequest(&djinnv1.LoadServiceSetRequest{Notch: djinnv1.LoadNotch_LOAD_NOTCH_LIGHT}))
			_, _ = client.Set(ctx, connect.NewRequest(&djinnv1.LoadServiceSetRequest{Notch: djinnv1.LoadNotch_LOAD_NOTCH_MAX}))
			var fourth *djinnv1.LoadServiceWatchResponse
			select {
			case fourth = <-recv:
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for fourth message")
			}
			if fourth.GetNotch() == djinnv1.LoadNotch_LOAD_NOTCH_UNSPECIFIED {
				t.Error("fourth notch unspecified")
			}
			if elapsed := time.Since(t0); elapsed < 20*time.Millisecond {
				t.Errorf("update arrived too fast (%v), expected rate limit around 30ms", elapsed)
			}

			// 6. Context cancellation ends stream
			cancel()
			select {
			case <-recvErr:
			case <-time.After(time.Second):
				t.Fatal("stream did not terminate after context cancel")
			}
		}},
		// The watch says the slots Get says.
		{"slots", func(t *testing.T) {
			snap := slotMachines[3]
			m := NewMonitor(NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_HIGH), func() (Snapshot, error) { return snap, nil })
			m.every = 0
			client, _ := serveLoad(t, t.TempDir(), m, nil, nil, nil, WithLoadTickInterval(0),
				WithLoadForcedWorkers(func() int { return 1 }), WithLoadLowerPauses(func() bool { return true }))
			got, err := client.Get(t.Context(), connect.NewRequest(&djinnv1.LoadServiceGetRequest{}))
			if err != nil {
				t.Fatal(err)
			}
			stream, err := client.Watch(t.Context(), connect.NewRequest(&djinnv1.LoadServiceWatchRequest{}))
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			if !stream.Receive() {
				t.Fatal(stream.Err())
			}
			w := stream.Msg()
			if w.GetSlots() != got.Msg.GetSlots() || w.GetSlots() != 6 || w.GetForcedWorkers() != 1 || !w.GetLowerPauses() ||
				len(w.GetNotchSlots()) != len(ManualNotches) {
				t.Errorf("Watch: %v", w)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, c.run)
	}
}

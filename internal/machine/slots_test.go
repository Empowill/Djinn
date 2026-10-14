package machine

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	djinnv1connect "github.com/empowill/djinn/gen/go/djinn/v1/djinnv1connect"
)

// slotMachines are machines from a small laptop to a big workstation, memory unknown, short or plenty, the
// developer's of W577 among them (16 cores, 31 GiB, 14.6 GiB available, 5 GiB held by the workers running).
var slotMachines = []Snapshot{
	{Cores: 1},
	{Cores: 4, MemoryTotal: 8 * GiB, MemoryAvailable: 3 * GiB},
	{Cores: 8, MemoryTotal: 16 * GiB, MemoryAvailable: 12 * GiB},
	{Cores: 16, MemoryTotal: 31 * GiB, MemoryAvailable: 14600 * MiB, WorkersResident: 5 * GiB},
	{Cores: 16, MemoryTotal: 64 * GiB, MemoryAvailable: 60 * GiB},
	{Cores: 32, MemoryTotal: 128 * GiB, MemoryAvailable: 120 * GiB},
	{Cores: 32, MemoryTotal: 16 * GiB, MemoryAvailable: 4 * GiB},
}

// TestNotchSlotsAreTheSchedulersSlots: on every machine, with or without djinn up --workers, what the load bar says each
// notch runs (NotchSlots) is what the scheduler lets run once that notch is in effect (Capacity, the harness's
// WithCapacity); a higher notch never runs fewer; what the notch itself lets run (BarSlots) is never below it.
func TestNotchSlotsAreTheSchedulersSlots(t *testing.T) {
	for _, s := range slotMachines {
		for _, workers := range []int{0, 3} {
			t.Run(fmt.Sprintf("cores=%d avail=%d workers=%d", s.Cores, s.MemoryAvailable>>20, workers), func(t *testing.T) {
				snap := s
				m := NewMonitor(DefaultPolicy(), func() (Snapshot, error) { return snap, nil })
				m.every = 0
				previous := 0
				for _, n := range ManualNotches {
					p := NotchPolicy(n)
					p.Workers = workers
					m.SetPolicy(p)
					shown := -1
					for _, ns := range m.NotchSlots() {
						if ns.Notch == n {
							shown = ns.Slots
						}
					}
					slots, rule, _ := m.Capacity()
					if shown != slots {
						t.Errorf("%s: the bar shows %d, the scheduler lets %d run (%s)", NotchName(n), shown, slots, rule)
					}
					if workers > 0 && slots != workers {
						t.Errorf("%s: %d slots, want --workers %d", NotchName(n), slots, workers)
					}
					if slots < 1 || slots < previous {
						t.Errorf("%s: %d slots after %d at the notch below", NotchName(n), slots, previous)
					}
					if bar := m.BarSlots(n); bar < slots {
						t.Errorf("%s: the notch lets %d run, below the scheduler's %d", NotchName(n), bar, slots)
					}
					previous = slots
				}
			})
		}
	}
}

// TestDevelopersMachineAtHigh: W577's machine at High runs 6 workers, the CPU's share (80 % of 8): what the bar said,
// and what the scheduler let run; the 8 the developer counted were 6 workers and 2 merges waiting, no process.
func TestDevelopersMachineAtHigh(t *testing.T) {
	s := slotMachines[3]
	p := NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_HIGH)
	if slots, rule := p.Slots(s); slots != 6 {
		t.Errorf("High: %d slots (%s), want 6", slots, rule)
	}
	if bar := p.BarSlots(s); bar != 6 {
		t.Errorf("High: the notch lets %d run, want 6", bar)
	}
}

// TestBarSlotsIgnoreMemory: the memory available moves every second; what the notch itself lets run does not move
// with it, so that a dip pauses no worker (W39 was paused and resumed every 2 s at Medium), while the scheduler's
// slots follow it, new tasks waiting.
func TestBarSlotsIgnoreMemory(t *testing.T) {
	for _, n := range ManualNotches {
		p := NotchPolicy(n)
		want := p.BarSlots(Snapshot{Cores: 16})
		for _, avail := range []uint64{0, 2 * GiB, 4 * GiB, 12 * GiB, 13500 * MiB, 30 * GiB} {
			s := Snapshot{Cores: 16, MemoryTotal: 31 * GiB, MemoryAvailable: avail}
			if got := p.BarSlots(s); got != want {
				t.Errorf("%s, %d MiB available: the notch lets %d run, want %d whatever the memory", NotchName(n), avail>>20, got, want)
			}
			if slots, _ := p.Slots(s); slots > want {
				t.Errorf("%s, %d MiB available: %d slots, above the notch's %d", NotchName(n), avail>>20, slots, want)
			}
		}
	}
}

// TestLoadServiceSlots: the load service says, at each read, how many workers the scheduler lets run now and at each
// notch, how many run, how many the developer started by hand, and whether lowering the notch pauses workers.
func TestLoadServiceSlots(t *testing.T) {
	snap := slotMachines[3]
	m := NewMonitor(NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_HIGH), func() (Snapshot, error) { return snap, nil })
	m.every = 0
	mux := http.NewServeMux()
	mux.Handle(LoadHandler(t.TempDir(), m, nil, nil, nil, WithLoadTickInterval(0),
		WithLoadRunningWorkers(func() int { return 7 }), WithLoadPausedWorkers(func() int { return 1 }),
		WithLoadForcedWorkers(func() int { return 1 }), WithLoadLowerPauses(func() bool { return true })))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	client := djinnv1connect.NewLoadServiceClient(srv.Client(), srv.URL)

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

	// The watch says the same.
	stream, err := client.Watch(t.Context(), connect.NewRequest(&djinnv1.LoadServiceWatchRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if !stream.Receive() {
		t.Fatal(stream.Err())
	}
	w := stream.Msg()
	if w.GetSlots() != g.GetSlots() || w.GetForcedWorkers() != 1 || !w.GetLowerPauses() || len(w.GetNotchSlots()) != len(ManualNotches) {
		t.Errorf("Watch: %v", w)
	}
}

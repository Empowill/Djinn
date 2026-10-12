package machine

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	"github.com/empowill/djinn/gen/go/machine/v1/machinev1connect"
)

// TestNotchPercentile: the higher the notch, the lower the percentile a worker is engaged at, and the smaller the
// margin kept beside it.
func TestNotchPercentile(t *testing.T) {
	for _, tt := range []struct {
		notch      djinnv1.LoadNotch
		percentile int
		margin     uint64
		engagedAt  string
	}{
		{djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL, 95, 512 * MiB, "engaged at p95 (minimal)"},
		{djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM, 80, 512 * MiB, "engaged at p80 (medium)"},
		{djinnv1.LoadNotch_LOAD_NOTCH_HIGH, 70, 384 * MiB, "engaged at p70 (high)"},
		{djinnv1.LoadNotch_LOAD_NOTCH_MAX, 60, 256 * MiB, "engaged at p60 (max)"},
		{djinnv1.LoadNotch_LOAD_NOTCH_OVERCLOCK, 50, 256 * MiB, "engaged at p50 (overclock)"},
	} {
		p := NotchPolicy(tt.notch)
		if p.MemoryPercentile() != tt.percentile || p.WorkerMargin != tt.margin || p.EngagedAt() != tt.engagedAt {
			t.Errorf("%v: p%d, margin %d MiB, %q; want p%d, %d MiB, %q", tt.notch, p.MemoryPercentile(),
				p.WorkerMargin>>20, p.EngagedAt(), tt.percentile, tt.margin>>20, tt.engagedAt)
		}
	}
	if got := (Policy{}).MemoryPercentile(); got != 95 {
		t.Errorf("a policy without a percentile engages at p%d, want p95", got)
	}
}

// TestWorkerRoomCountsRunningOnce replays the incident (16 cores, 32 GiB, Overclock, 12.9 GiB available, the 5
// workers running holding 3.63 GiB, engaged at 11.3 GiB, a new one forecast at 2.6 GiB): without their resident memory
// counted back in, the 6th is held; with it, it fits.
func TestWorkerRoomCountsRunningOnce(t *testing.T) {
	p := NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_OVERCLOCK)
	p.WorkerMargin = 512 * MiB // the margin of the incident
	gib := func(f float64) uint64 { return uint64(f * GiB) }
	total, available := uint64(32*GiB), gib(12.9)
	engaged, resident, typical := gib(11.3), gib(3.63), gib(2.1)
	want := "load overclock commits up to 12.9 GiB of memory, 11.3 GiB already engaged, needs 2.6 GiB at p50"
	if why := p.WorkerRoom(total, available, 0, 0, engaged, "claude", typical, 10); why != want {
		t.Errorf("counted twice: %q, want %q", why, want)
	}
	if why := p.WorkerRoom(total, available, resident, 0, engaged, "claude", typical, 10); why != "" {
		t.Errorf("counted once: %q, want room", why)
	}
	// What the workers running may still take stays checked against the memory free.
	if why := p.WorkerRoom(total, available, resident, 11*GiB, engaged, "claude", typical, 10); !strings.Contains(why,
		"11.0 GiB of it for the workers running") {
		t.Errorf("held: %q", why)
	}
	// The workers' resident memory never lifts what Djinn could use above the machine.
	if got := p.commitBase(total, 30*GiB, 8*GiB); got != total {
		t.Errorf("commitBase beyond the total: %d GiB, want 32", got>>30)
	}
	// Minimal stays cautious: 20% of what Djinn could use beyond the reserve, 2.9 GiB, holds no 6th worker.
	pMin := NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL)
	if why := pMin.WorkerRoom(total, available, resident, 0, engaged, "claude", typical, 10); !strings.HasPrefix(why,
		"load minimal commits up to 2.9 GiB of memory (12.9 GiB available, 3.6 GiB held by the workers running)") {
		t.Errorf("minimal: %q", why)
	}
}

// TestSlotsCountRunningOnce: the workers' own memory does not take slots from the others: the slots stay what the
// machine allows with no worker running.
func TestSlotsCountRunningOnce(t *testing.T) {
	p := NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_MAX)
	idle := Snapshot{Cores: 16, MemoryTotal: 16 * GiB, MemoryAvailable: 14 * GiB}
	busy := Snapshot{Cores: 16, MemoryTotal: 16 * GiB, MemoryAvailable: 8 * GiB, WorkersResident: 6 * GiB}
	n, _ := p.Slots(idle)
	if m, rule := p.Slots(busy); m != n {
		t.Errorf("6 GiB held by the workers: %d slots (%s), want %d as idle", m, rule, n)
	}
	if a, b := p.MemorySlots(idle), p.MemorySlots(busy); a != b {
		t.Errorf("memory slots: %d busy, %d idle", b, a)
	}
	busy.WorkersResident = 0
	if m, _ := p.Slots(busy); m >= n {
		t.Errorf("counted twice: %d slots, want fewer than %d", m, n)
	}
}

// TestMonitorWorkerMemory: each snapshot carries the workers' resident memory read now, even from the cache.
func TestMonitorWorkerMemory(t *testing.T) {
	m := NewMonitor(DefaultPolicy(), func() (Snapshot, error) {
		return Snapshot{Cores: 4, MemoryTotal: 8 * GiB, MemoryAvailable: 4 * GiB}, nil
	})
	if got := m.Snapshot().WorkersResident; got != 0 {
		t.Errorf("without workers: %d", got)
	}
	resident := uint64(GiB)
	m.SetWorkerMemory(func() uint64 { return resident })
	if got := m.Snapshot().WorkersResident; got != GiB {
		t.Errorf("WorkersResident = %d, want 1 GiB", got)
	}
	resident = 2 * GiB
	if got := m.Snapshot().WorkersResident; got != 2*GiB {
		t.Errorf("cached snapshot: WorkersResident = %d, want 2 GiB", got)
	}
}

// TestShowEngagedAt: djinn machine show says the percentile the notch engages a worker at.
func TestShowEngagedAt(t *testing.T) {
	m := NewMonitor(NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_OVERCLOCK), func() (Snapshot, error) {
		return Snapshot{Cores: 16, MemoryTotal: 32 * GiB, MemoryAvailable: 12 * GiB}, nil
	})
	mux := http.NewServeMux()
	mux.Handle(Handler(m, nil, nil))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	res, err := machinev1connect.NewMachineServiceClient(srv.Client(), srv.URL).Show(t.Context(),
		connect.NewRequest(&machinev1.MachineServiceShowRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Msg.GetMachine(); got.GetWorkerPercentile() != 50 || got.GetWorkerEngagedAt() != "engaged at p50 (overclock)" {
		t.Errorf("show: p%d, %q", got.GetWorkerPercentile(), got.GetWorkerEngagedAt())
	}
}

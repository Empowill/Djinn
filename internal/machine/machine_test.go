package machine

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
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

// TestNotchPolicy: each notch's shares, margins and pressure marks; Light and an unspecified notch are Medium.
func TestNotchPolicy(t *testing.T) {
	t.Parallel()
	cases := []struct {
		notch        djinnv1.LoadNotch
		want         djinnv1.LoadNotch
		name         string
		share        float64
		gateMargin   uint64
		workerMargin uint64
		cpu          float64
		mem          float64
		load         float64
		free         float64
	}{
		{djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL, djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL, "minimal", 0.20, 1024 * MiB, 512 * MiB, 40, 5, 1.0, 0.20},
		{djinnv1.LoadNotch_LOAD_NOTCH_LIGHT, djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM, "medium", 0.50, 512 * MiB, 512 * MiB, 65, 10, 2.0, 0.10},
		{djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM, djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM, "medium", 0.50, 512 * MiB, 512 * MiB, 65, 10, 2.0, 0.10},
		{djinnv1.LoadNotch_LOAD_NOTCH_UNSPECIFIED, djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM, "medium", 0.50, 512 * MiB, 512 * MiB, 65, 10, 2.0, 0.10},
		{djinnv1.LoadNotch_LOAD_NOTCH_HIGH, djinnv1.LoadNotch_LOAD_NOTCH_HIGH, "high", 0.80, 384 * MiB, 384 * MiB, 80, 15, 3.0, 0.05},
		{djinnv1.LoadNotch_LOAD_NOTCH_MAX, djinnv1.LoadNotch_LOAD_NOTCH_MAX, "max", 1.00, 256 * MiB, 256 * MiB, 90, 25, 5.0, 0.02},
		{djinnv1.LoadNotch_LOAD_NOTCH_OVERCLOCK, djinnv1.LoadNotch_LOAD_NOTCH_OVERCLOCK, "overclock", 1.50, 128 * MiB, 256 * MiB, 95, 40, 8.0, 0.01},
	}
	for _, c := range cases {
		t.Run(c.notch.String(), func(t *testing.T) {
			t.Parallel()
			p := NotchPolicy(c.notch)
			if p.Notch != c.want {
				t.Errorf("notch: got %v, want %v", p.Notch, c.want)
			}
			if p.NotchName() != c.name {
				t.Errorf("name: got %s, want %s", p.NotchName(), c.name)
			}
			if p.Share != c.share {
				t.Errorf("Share: got %f, want %f", p.Share, c.share)
			}
			if p.MemoryShare != c.share {
				t.Errorf("MemoryShare: got %f, want %f", p.MemoryShare, c.share)
			}
			if p.CommandMargin != c.gateMargin {
				t.Errorf("CommandMargin: got %d, want %d", p.CommandMargin, c.gateMargin)
			}
			if p.WorkerMargin != c.workerMargin {
				t.Errorf("WorkerMargin: got %d MiB, want %d MiB", p.WorkerMargin>>20, c.workerMargin>>20)
			}
			if p.CPUPressure != c.cpu {
				t.Errorf("CPUPressure: got %f, want %f", p.CPUPressure, c.cpu)
			}
			if p.MemoryPressure != c.mem {
				t.Errorf("MemoryPressure: got %f, want %f", p.MemoryPressure, c.mem)
			}
			if p.LoadPerCore != c.load {
				t.Errorf("LoadPerCore: got %f, want %f", p.LoadPerCore, c.load)
			}
			if p.MemoryFree != c.free {
				t.Errorf("MemoryFree: got %f, want %f", p.MemoryFree, c.free)
			}
			if p.WorkerMemory != 0 || p.WorkerMemoryGuard != 0 {
				t.Errorf("WorkerMemory %d and WorkerMemoryGuard %f should remain 0", p.WorkerMemory, p.WorkerMemoryGuard)
			}
		})
	}
}

// TestDefaultPolicy: Djinn runs at Medium by default.
func TestDefaultPolicy(t *testing.T) {
	t.Parallel()
	def := DefaultPolicy()
	med := NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM)
	if def.Notch != med.Notch || def.Share != med.Share || def.MemoryShare != med.MemoryShare ||
		def.CommandMargin != med.CommandMargin {
		t.Errorf("DefaultPolicy does not match medium: %+v vs %+v", def, med)
	}
}

// TestPolicy_MemoryPercentile: the higher the notch, the lower the percentile a worker is engaged at; p95 for a
// policy without one.
func TestPolicy_MemoryPercentile(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		policy Policy
		want   int
	}{
		{"minimal", NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL), 95},
		{"medium", NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM), 80},
		{"high", NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_HIGH), 70},
		{"max", NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_MAX), 60},
		{"overclock", NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_OVERCLOCK), 50},
		{"none", Policy{}, 95},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := c.policy.MemoryPercentile(); got != c.want {
				t.Errorf("MemoryPercentile = p%d, want p%d", got, c.want)
			}
		})
	}
}

// TestPolicy_EngagedAt: djinn machine show says the percentile the notch engages a worker at.
func TestPolicy_EngagedAt(t *testing.T) {
	t.Parallel()
	cases := []struct {
		notch djinnv1.LoadNotch
		want  string
	}{
		{djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL, "engaged at p95 (minimal)"},
		{djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM, "engaged at p80 (medium)"},
		{djinnv1.LoadNotch_LOAD_NOTCH_HIGH, "engaged at p70 (high)"},
		{djinnv1.LoadNotch_LOAD_NOTCH_MAX, "engaged at p60 (max)"},
		{djinnv1.LoadNotch_LOAD_NOTCH_OVERCLOCK, "engaged at p50 (overclock)"},
	}
	for _, c := range cases {
		t.Run(c.notch.String(), func(t *testing.T) {
			t.Parallel()
			if got := NotchPolicy(c.notch).EngagedAt(); got != c.want {
				t.Errorf("EngagedAt = %q, want %q", got, c.want)
			}
		})
	}
}

// TestParseNotch: a notch's name gives the notch, an old light Medium; an unknown name fails.
func TestParseNotch(t *testing.T) {
	t.Parallel()
	cases := []struct {
		s       string
		want    djinnv1.LoadNotch
		wantErr bool
	}{
		{s: "minimal", want: djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL},
		{s: "light", want: djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM}, // migrated
		{s: "medium", want: djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM},
		{s: "high", want: djinnv1.LoadNotch_LOAD_NOTCH_HIGH},
		{s: "max", want: djinnv1.LoadNotch_LOAD_NOTCH_MAX},
		{s: "overclock", want: djinnv1.LoadNotch_LOAD_NOTCH_OVERCLOCK},
		{s: "auto", want: djinnv1.LoadNotch_LOAD_NOTCH_AUTO},
		{s: "unknown", wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.s, func(t *testing.T) {
			t.Parallel()
			if got, err := ParseNotch(c.s); (err != nil) != c.wantErr || got != c.want {
				t.Errorf("ParseNotch(%q) = %v, %v; want %v, error %v", c.s, got, err, c.want, c.wantErr)
			}
		})
	}
}

// TestNotchName: the name ParseNotch reads back; an unknown notch is Medium's.
func TestNotchName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		notch djinnv1.LoadNotch
		want  string
	}{
		{djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL, "minimal"},
		{djinnv1.LoadNotch_LOAD_NOTCH_LIGHT, "light"},
		{djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM, "medium"},
		{djinnv1.LoadNotch_LOAD_NOTCH_HIGH, "high"},
		{djinnv1.LoadNotch_LOAD_NOTCH_MAX, "max"},
		{djinnv1.LoadNotch_LOAD_NOTCH_OVERCLOCK, "overclock"},
		{djinnv1.LoadNotch_LOAD_NOTCH_AUTO, "auto"},
		{djinnv1.LoadNotch_LOAD_NOTCH_UNSPECIFIED, "medium"},
	}
	for _, c := range cases {
		t.Run(c.notch.String(), func(t *testing.T) {
			t.Parallel()
			if got := NotchName(c.notch); got != c.want {
				t.Errorf("NotchName(%v) = %q, want %q", c.notch, got, c.want)
			}
		})
	}
}

// TestNotchTitle: the title-case name of a notch, of a policy's notch too; an unknown notch is Medium.
func TestNotchTitle(t *testing.T) {
	t.Parallel()
	cases := []struct {
		notch djinnv1.LoadNotch
		want  string
	}{
		{djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL, "Minimal"},
		{djinnv1.LoadNotch_LOAD_NOTCH_LIGHT, "Light"},
		{djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM, "Medium"},
		{djinnv1.LoadNotch_LOAD_NOTCH_HIGH, "High"},
		{djinnv1.LoadNotch_LOAD_NOTCH_MAX, "Max"},
		{djinnv1.LoadNotch_LOAD_NOTCH_OVERCLOCK, "Overclock"},
		{djinnv1.LoadNotch_LOAD_NOTCH_AUTO, "Auto"},
		{djinnv1.LoadNotch_LOAD_NOTCH_UNSPECIFIED, "Medium"},
		{djinnv1.LoadNotch(99), "Medium"},
	}
	for _, c := range cases {
		t.Run(c.notch.String(), func(t *testing.T) {
			t.Parallel()
			if got := NotchTitle(c.notch); got != c.want {
				t.Errorf("NotchTitle(%v) = %q, want %q", c.notch, got, c.want)
			}
			if got := (Policy{Notch: c.notch}).NotchTitle(); got != c.want {
				t.Errorf("Policy.NotchTitle() for %v = %q, want %q", c.notch, got, c.want)
			}
		})
	}
}

// TestLowPriority: Minimal and Medium run workers at a low CPU priority, the higher notches at the standard one.
func TestLowPriority(t *testing.T) {
	t.Parallel()
	cases := []struct {
		notch djinnv1.LoadNotch
		want  bool
	}{
		{djinnv1.LoadNotch_LOAD_NOTCH_UNSPECIFIED, false},
		{djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL, true},
		{djinnv1.LoadNotch_LOAD_NOTCH_LIGHT, true},
		{djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM, true},
		{djinnv1.LoadNotch_LOAD_NOTCH_HIGH, false},
		{djinnv1.LoadNotch_LOAD_NOTCH_MAX, false},
		{djinnv1.LoadNotch_LOAD_NOTCH_OVERCLOCK, false},
	}
	for _, c := range cases {
		t.Run(c.notch.String(), func(t *testing.T) {
			t.Parallel()
			if got := LowPriority(c.notch); got != c.want {
				t.Errorf("LowPriority(%v) = %v, want %v", c.notch, got, c.want)
			}
			if got := (Policy{Notch: c.notch}).LowPriority(); got != c.want {
				t.Errorf("Policy.LowPriority() for notch %v = %v, want %v", c.notch, got, c.want)
			}
		})
	}
}

// TestPolicy_Slots: the most workers a notch runs at once, its share of what the CPU and the memory allow, and the rule
// that gave it; djinn up --workers sets it by hand. The workers' own memory does not take slots from the others: the
// slots stay what the machine allows with no worker running.
func TestPolicy_Slots(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		notch   djinnv1.LoadNotch
		workers int
		s       Snapshot
		peaks   []uint64
		want    int
		rule    string // a part of the rule
	}{
		{name: "this laptop", notch: djinnv1.LoadNotch_LOAD_NOTCH_MAX, s: Snapshot{Cores: 16, MemoryTotal: 32 * GiB},
			want: 8, rule: "one per 2 cores of 16"},
		{name: "little memory", notch: djinnv1.LoadNotch_LOAD_NOTCH_MAX, s: Snapshot{Cores: 16, MemoryTotal: 8 * GiB},
			want: 4, rule: "one per 1.2 GiB of memory beyond 2.0 GiB, of 8.0 GiB"},
		{name: "one core", notch: djinnv1.LoadNotch_LOAD_NOTCH_MAX, s: Snapshot{Cores: 1}, want: 1, rule: "one per 2 cores of 1"},
		{name: "tiny", notch: djinnv1.LoadNotch_LOAD_NOTCH_MAX, s: Snapshot{Cores: 2, MemoryTotal: GiB}, want: 1,
			rule: "1: one per 2 cores of 2, and one per"},
		{name: "a big server", notch: djinnv1.LoadNotch_LOAD_NOTCH_MAX, s: Snapshot{Cores: 128, MemoryTotal: 512 * GiB},
			want: 16, rule: "16, the most Djinn runs"},
		{name: "set by hand", notch: djinnv1.LoadNotch_LOAD_NOTCH_MAX, workers: 3, s: Snapshot{Cores: 64}, want: 3,
			rule: "--workers"},

		// Machine sizes: a notch's share of BaseSlots, at least 1; Overclock runs more than the rule.
		{name: "2 cores 8 GiB minimal", notch: djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL, s: Snapshot{Cores: 2, MemoryTotal: 8 * GiB}, want: 1},
		{name: "2 cores 8 GiB medium", notch: djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM, s: Snapshot{Cores: 2, MemoryTotal: 8 * GiB}, want: 1},
		{name: "2 cores 8 GiB high", notch: djinnv1.LoadNotch_LOAD_NOTCH_HIGH, s: Snapshot{Cores: 2, MemoryTotal: 8 * GiB}, want: 1},
		{name: "2 cores 8 GiB max", notch: djinnv1.LoadNotch_LOAD_NOTCH_MAX, s: Snapshot{Cores: 2, MemoryTotal: 8 * GiB}, want: 1},
		// max(1, round(1.5*1)) = 2 > BaseSlots.
		{name: "2 cores 8 GiB overclock", notch: djinnv1.LoadNotch_LOAD_NOTCH_OVERCLOCK, s: Snapshot{Cores: 2, MemoryTotal: 8 * GiB}, want: 2},
		{name: "8 cores 8 GiB minimal", notch: djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL, s: Snapshot{Cores: 8, MemoryTotal: 8 * GiB}, want: 1},
		{name: "8 cores 8 GiB medium", notch: djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM, s: Snapshot{Cores: 8, MemoryTotal: 8 * GiB}, want: 2},
		{name: "8 cores 8 GiB high", notch: djinnv1.LoadNotch_LOAD_NOTCH_HIGH, s: Snapshot{Cores: 8, MemoryTotal: 8 * GiB}, want: 3},
		{name: "8 cores 8 GiB max", notch: djinnv1.LoadNotch_LOAD_NOTCH_MAX, s: Snapshot{Cores: 8, MemoryTotal: 8 * GiB}, want: 4},
		// max(1, min(6, 8/1.25=6)) = 6 > BaseSlots (256 MiB margin).
		{name: "8 cores 8 GiB overclock", notch: djinnv1.LoadNotch_LOAD_NOTCH_OVERCLOCK, s: Snapshot{Cores: 8, MemoryTotal: 8 * GiB}, want: 6},
		{name: "8 cores 32 GiB minimal", notch: djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL, s: Snapshot{Cores: 8, MemoryTotal: 32 * GiB}, want: 1},
		{name: "8 cores 32 GiB medium", notch: djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM, s: Snapshot{Cores: 8, MemoryTotal: 32 * GiB}, want: 2},
		{name: "8 cores 32 GiB high", notch: djinnv1.LoadNotch_LOAD_NOTCH_HIGH, s: Snapshot{Cores: 8, MemoryTotal: 32 * GiB}, want: 3},
		{name: "8 cores 32 GiB max", notch: djinnv1.LoadNotch_LOAD_NOTCH_MAX, s: Snapshot{Cores: 8, MemoryTotal: 32 * GiB}, want: 4},
		// max(1, round(1.5*4=6.0)) = 6 > BaseSlots.
		{name: "8 cores 32 GiB overclock", notch: djinnv1.LoadNotch_LOAD_NOTCH_OVERCLOCK, s: Snapshot{Cores: 8, MemoryTotal: 32 * GiB}, want: 6},
		{name: "32 cores 32 GiB minimal", notch: djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL, s: Snapshot{Cores: 32, MemoryTotal: 32 * GiB}, want: 3},
		{name: "32 cores 32 GiB medium", notch: djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM, s: Snapshot{Cores: 32, MemoryTotal: 32 * GiB}, want: 8},
		{name: "32 cores 32 GiB high", notch: djinnv1.LoadNotch_LOAD_NOTCH_HIGH, s: Snapshot{Cores: 32, MemoryTotal: 32 * GiB}, want: 13},
		{name: "32 cores 32 GiB max", notch: djinnv1.LoadNotch_LOAD_NOTCH_MAX, s: Snapshot{Cores: 32, MemoryTotal: 32 * GiB}, want: 16},
		// min(round(1.5*16=24), 32/1.25=25) = 24 > BaseSlots (256 MiB margin).
		{name: "32 cores 32 GiB overclock", notch: djinnv1.LoadNotch_LOAD_NOTCH_OVERCLOCK, s: Snapshot{Cores: 32, MemoryTotal: 32 * GiB}, want: 24},

		// Memory binds: 16 cores (CPU allows 8), 8 GiB (usable 6 GiB, forecast 1.5 GiB allows 4): 50 % of 4.
		{name: "memory binds", notch: djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM,
			s: Snapshot{Cores: 16, MemoryTotal: 8 * GiB, MemoryAvailable: 8 * GiB}, want: 2, rule: "2: 50% of 4"},
		// CPU binds: 4 cores (CPU allows 2), 32 GiB: 50 % of 2.
		{name: "cpu binds", notch: djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM,
			s: Snapshot{Cores: 4, MemoryTotal: 32 * GiB, MemoryAvailable: 32 * GiB}, want: 1, rule: "1: 50% of 2"},
		// A measured peak of 2.5 GiB (forecast 3 GiB) on 8 cores, 8 GiB: memory allows 2, 50 % of it.
		{name: "measured peak binds", notch: djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM,
			s: Snapshot{Cores: 8, MemoryTotal: 8 * GiB, MemoryAvailable: 8 * GiB}, peaks: []uint64{2560 * MiB}, want: 1,
			rule: "one per 3.0 GiB of memory"},

		// The workers' memory counted once: 6 GiB held by them takes no slot.
		{name: "idle", notch: djinnv1.LoadNotch_LOAD_NOTCH_MAX,
			s: Snapshot{Cores: 16, MemoryTotal: 16 * GiB, MemoryAvailable: 14 * GiB}, want: 8},
		{name: "busy", notch: djinnv1.LoadNotch_LOAD_NOTCH_MAX,
			s: Snapshot{Cores: 16, MemoryTotal: 16 * GiB, MemoryAvailable: 8 * GiB, WorkersResident: 6 * GiB}, want: 8},
		// Counted twice, it would leave fewer.
		{name: "busy counted twice", notch: djinnv1.LoadNotch_LOAD_NOTCH_MAX,
			s: Snapshot{Cores: 16, MemoryTotal: 16 * GiB, MemoryAvailable: 8 * GiB}, want: 4},

		// W577's machine at High runs 6 workers, the CPU's share (80 % of 8): the 8 the developer counted were 6
		// workers and 2 merges waiting, no process.
		{name: "developer's machine at high", notch: djinnv1.LoadNotch_LOAD_NOTCH_HIGH, s: slotMachines[3], want: 6,
			rule: "6: 80% of 8"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			p := NotchPolicy(c.notch)
			p.Workers = c.workers
			if n, rule := p.Slots(c.s, c.peaks...); n != c.want || !strings.Contains(rule, c.rule) {
				t.Errorf("Slots = %d (%s), want %d (%s)", n, rule, c.want, c.rule)
			}
		})
	}
}

// TestBaseSlots: what the CPU rule and the memory allow at Medium, before a notch's share.
func TestBaseSlots(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		s    Snapshot
		want int
	}{
		{"2 cores, 8 GiB", Snapshot{Cores: 2, MemoryTotal: 8 * GiB}, 1},      // min(2/2=1, (8-2)/2=3) = 1
		{"8 cores, 8 GiB", Snapshot{Cores: 8, MemoryTotal: 8 * GiB}, 4},      // min(8/2=4, (8-2)/1.5=4) = 4
		{"8 cores, 32 GiB", Snapshot{Cores: 8, MemoryTotal: 32 * GiB}, 4},    // min(8/2=4, (32-2)/1.5=20 -> maxW 16) = 4
		{"32 cores, 32 GiB", Snapshot{Cores: 32, MemoryTotal: 32 * GiB}, 16}, // min(32/2=16, (32-2)/1.5=20 -> maxW 16) = 16
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := BaseSlots(c.s); got != c.want {
				t.Errorf("BaseSlots = %d, want %d", got, c.want)
			}
		})
	}
}

// TestPolicy_CPUSlots: one worker per 2 cores.
func TestPolicy_CPUSlots(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		s    Snapshot
		want int
	}{
		{"16 cores", Snapshot{Cores: 16, MemoryTotal: 8 * GiB, MemoryAvailable: 8 * GiB}, 8},
		{"4 cores", Snapshot{Cores: 4, MemoryTotal: 32 * GiB, MemoryAvailable: 32 * GiB}, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := DefaultPolicy().CPUSlots(c.s); got != c.want {
				t.Errorf("CPUSlots = %d, want %d", got, c.want)
			}
		})
	}
}

// TestPolicy_MemorySlots: the memory Djinn may use beyond the reserve over a worker's forecast, its running workers'
// memory counted back in.
func TestPolicy_MemorySlots(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		notch djinnv1.LoadNotch
		s     Snapshot
		peaks []uint64
		want  int
	}{
		// 8 GiB: usable 6 GiB, forecast 1.5 GiB.
		{"8 GiB", djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM, Snapshot{Cores: 16, MemoryTotal: 8 * GiB, MemoryAvailable: 8 * GiB}, nil, 4},
		// 32 GiB: usable 30 GiB, at most 16.
		{"32 GiB", djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM, Snapshot{Cores: 4, MemoryTotal: 32 * GiB, MemoryAvailable: 32 * GiB}, nil, 16},
		// A measured peak of 2.5 GiB and 512 MiB of margin: 3 GiB a worker, 6 / 3.
		{"measured peak", djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM, Snapshot{Cores: 8, MemoryTotal: 8 * GiB, MemoryAvailable: 8 * GiB},
			[]uint64{2560 * MiB}, 2},
		{"idle", djinnv1.LoadNotch_LOAD_NOTCH_MAX, Snapshot{Cores: 16, MemoryTotal: 16 * GiB, MemoryAvailable: 14 * GiB}, nil, 9},
		// 6 GiB held by the workers running: as many as idle.
		{"busy", djinnv1.LoadNotch_LOAD_NOTCH_MAX,
			Snapshot{Cores: 16, MemoryTotal: 16 * GiB, MemoryAvailable: 8 * GiB, WorkersResident: 6 * GiB}, nil, 9},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := NotchPolicy(c.notch).MemorySlots(c.s, c.peaks...); got != c.want {
				t.Errorf("MemorySlots = %d, want %d", got, c.want)
			}
		})
	}
}

// TestPolicy_BindingResource: the resource that allows fewer workers.
func TestPolicy_BindingResource(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		s     Snapshot
		peaks []uint64
		want  string
	}{
		{"memory", Snapshot{Cores: 16, MemoryTotal: 8 * GiB, MemoryAvailable: 8 * GiB}, nil, "memory"},
		{"cpu", Snapshot{Cores: 4, MemoryTotal: 32 * GiB, MemoryAvailable: 32 * GiB}, nil, "cpu"},
		{"measured peak", Snapshot{Cores: 8, MemoryTotal: 8 * GiB, MemoryAvailable: 8 * GiB}, []uint64{2560 * MiB}, "memory"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := DefaultPolicy().BindingResource(c.s, c.peaks...); got != c.want {
				t.Errorf("BindingResource = %q, want %q", got, c.want)
			}
		})
	}
}

// TestPolicy_BarSlots: what the notch itself lets run does not move with the memory available, which moves every
// second, so that a dip pauses no worker (W39 was paused and resumed every 2 s at Medium), while the scheduler's slots
// follow it, new tasks waiting.
func TestPolicy_BarSlots(t *testing.T) {
	t.Parallel()
	cases := []struct {
		notch djinnv1.LoadNotch
		want  int // on 16 cores
	}{
		{djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL, 2},
		{djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM, 4},
		// W577's machine at High: 6, what the bar said.
		{djinnv1.LoadNotch_LOAD_NOTCH_HIGH, 6},
		{djinnv1.LoadNotch_LOAD_NOTCH_MAX, 8},
		{djinnv1.LoadNotch_LOAD_NOTCH_OVERCLOCK, 12},
	}
	for _, c := range cases {
		t.Run(c.notch.String(), func(t *testing.T) {
			t.Parallel()
			p := NotchPolicy(c.notch)
			if got := p.BarSlots(slotMachines[3]); got != c.want {
				t.Errorf("on W577's machine: the notch lets %d run, want %d", got, c.want)
			}
			for _, avail := range []uint64{0, 2 * GiB, 4 * GiB, 12 * GiB, 13500 * MiB, 30 * GiB} {
				s := Snapshot{Cores: 16, MemoryTotal: 31 * GiB, MemoryAvailable: avail}
				if got := p.BarSlots(s); got != c.want {
					t.Errorf("%d MiB available: the notch lets %d run, want %d whatever the memory", avail>>20, got, c.want)
				}
				if slots, _ := p.Slots(s); slots > c.want {
					t.Errorf("%d MiB available: %d slots, above the notch's %d", avail>>20, slots, c.want)
				}
			}
		})
	}
}

// TestPolicy_Pressure: why the machine holds new workers, from the kernel's pressure, the load, the memory free or
// macOS's level: its warning holds workers at Minimal only, its critical level at every notch; a policy that names no
// level holds at critical.
func TestPolicy_Pressure(t *testing.T) {
	t.Parallel()
	type pressureCase struct {
		name   string
		policy Policy
		s      Snapshot
		want   string
	}
	def := DefaultPolicy()
	cases := []pressureCase{
		{"calm", def, Snapshot{Cores: 8, CPU: &Pressure{Some: 10}, Memory: &Pressure{Some: 1}, LoadKnown: true, Load1: 40}, ""},
		// A go test's bursts of goroutines on 16 cores, the machine half idle: 30 to 57 % (W347) holds nothing.
		{"test bursts", def, Snapshot{Cores: 16, CPU: &Pressure{Some: 57}, Memory: &Pressure{}}, ""},
		{"cpu", def, Snapshot{Cores: 8, CPU: &Pressure{Some: 70}, Memory: &Pressure{}},
			"tasks waited for the CPU 70% of the last 10 s (65% at most)"},
		{"memory", def, Snapshot{Cores: 8, CPU: &Pressure{}, Memory: &Pressure{Some: 12}},
			"tasks waited for memory 12% of the last 10 s (10% at most)"},
		{"macOS critical", def, Snapshot{Cores: 8, MemoryLevel: 4, MemoryTotal: 16 * GiB}, "the system reports critical memory pressure"},
		{"macOS warning", def, Snapshot{Cores: 8, MemoryLevel: 2, MemoryTotal: 16 * GiB}, ""},
		// A Mac at warning, little memory free as macOS counts it, a load of 1.9 a core: below Medium's 2.
		{"macOS busy", def, Snapshot{Cores: 10, MemoryLevel: 2, MemoryTotal: 16 * GiB, MemoryAvailable: GiB / 4, LoadKnown: true,
			Load1: 19}, ""},
		// The load rule holds on macOS too (LoadPerCore per notch): a test run at 3.4 a core holds at Medium.
		{"macOS loaded", def, Snapshot{Cores: 10, MemoryLevel: 2, MemoryTotal: 16 * GiB, LoadKnown: true, Load1: 34},
			"the load is 34.0 on 10 cores (2.0 a core at most)"},
		{"macOS calm", def, Snapshot{Cores: 8, MemoryLevel: 1, MemoryTotal: 16 * GiB, MemoryAvailable: GiB / 2}, ""},
		{"load without PSI", def, Snapshot{Cores: 4, LoadKnown: true, Load1: 9}, "the load is 9.0 on 4 cores (2.0 a core at most)"},
		{"memory without PSI", def, Snapshot{Cores: 4, MemoryTotal: 16 * GiB, MemoryAvailable: GiB},
			"1.0 GiB of memory available of 16.0 GiB (10% at least)"},
		{"no level named, at warning", Policy{}, Snapshot{Cores: 8, MemoryLevel: MemoryWarning, MemoryTotal: 16 * GiB}, ""},
		{"no level named, at critical", Policy{}, Snapshot{Cores: 8, MemoryLevel: MemoryCritical, MemoryTotal: 16 * GiB},
			"the system reports critical memory pressure"},
	}
	for _, notch := range append(ManualNotches, djinnv1.LoadNotch_LOAD_NOTCH_AUTO) {
		p := NotchPolicy(notch)
		warning := ""
		if notch == djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL || notch == djinnv1.LoadNotch_LOAD_NOTCH_AUTO {
			warning = "the system reports memory pressure"
		}
		cases = append(cases,
			pressureCase{NotchName(notch) + " at macOS warning", p,
				Snapshot{Cores: 8, MemoryLevel: MemoryWarning, MemoryTotal: 16 * GiB}, warning},
			pressureCase{NotchName(notch) + " at macOS critical", p,
				Snapshot{Cores: 8, MemoryLevel: MemoryCritical, MemoryTotal: 16 * GiB}, "the system reports critical memory pressure"},
			pressureCase{NotchName(notch) + " at macOS normal", p, Snapshot{Cores: 8, MemoryLevel: 1, MemoryTotal: 16 * GiB}, ""},
		)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := c.policy.Pressure(c.s); got != c.want {
				t.Errorf("Pressure = %q, want %q", got, c.want)
			}
		})
	}
}

// TestPolicy_Room: the machine holds a measured command when the memory free, less what the gates held take, holds
// its peak and the margin; a command never measured, or a machine whose memory is unknown, always has room.
func TestPolicy_Room(t *testing.T) {
	t.Parallel()
	s := Snapshot{MemoryTotal: 16 * GiB, MemoryAvailable: 4 * GiB}
	cases := []struct {
		name       string
		s          Snapshot
		peak, held uint64
		want       string
	}{
		{"room", s, 3 * GiB, 0, ""},
		{"no margin left", s, 36 * GiB / 10, 0, "e2e peaks at 3.6 GiB, 4.0 GiB free"},
		{"held", s, 2 * GiB, 2 * GiB, "e2e peaks at 2.0 GiB, 4.0 GiB free, 2.0 GiB of it for the commands holding a gate"},
		{"held beyond the free", s, 300 << 20, 6 * GiB, "e2e peaks at 300 MiB, 4.0 GiB free, 4.0 GiB of it for the commands holding a gate"},
		{"never measured", s, 0, 6 * GiB, ""},
		{"memory unknown", Snapshot{}, 3 * GiB, 0, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := DefaultPolicy().Room(c.s, "e2e", c.peak, c.held); got != c.want {
				t.Errorf("Room = %q, want %q", got, c.want)
			}
		})
	}
}

// TestPolicy_Typical: the typical peak of a worker is the median of the latest measured, WorkerPeak while none is.
func TestPolicy_Typical(t *testing.T) {
	t.Parallel()
	p := DefaultPolicy()
	p.WorkerPeaks = 3
	cases := []struct {
		name  string
		peaks []uint64
		want  uint64
		n     int
	}{
		{"none measured", nil, GiB, 0},
		{"odd", []uint64{3 * GiB, GiB, 2 * GiB}, 2 * GiB, 3},
		{"even", []uint64{GiB, 3 * GiB}, 2 * GiB, 2},
		{"the latest only", []uint64{GiB, GiB, 2 * GiB, 9 * GiB, 9 * GiB}, GiB, 3},
		{"zero is not measured", []uint64{0, 2 * GiB, 0}, 2 * GiB, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got, n := p.Typical(c.peaks); got != c.want || n != c.n {
				t.Errorf("Typical = %d over %d, want %d over %d", got, n, c.want, c.n)
			}
		})
	}
}

// TestPolicy_WorkerRoom: another worker starts only when the memory available, less what the running ones may still
// take, holds its typical peak and the margin, and when the notch's share of the memory Djinn could use holds what
// the workers engage and its forecast; the reason names those numbers. The workers' resident memory is counted once.
func TestPolicy_WorkerRoom(t *testing.T) {
	t.Parallel()
	gib := func(f float64) uint64 { return uint64(f * GiB) }
	// The incident: 16 cores, 32 GiB, Overclock with a 512 MiB margin, 12.9 GiB available, the 5 workers running
	// holding 3.63 GiB, engaged at 11.3 GiB, a new one forecast at 2.6 GiB.
	incident := NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_OVERCLOCK)
	incident.WorkerMargin = 512 * MiB
	cases := []struct {
		name                                      string
		policy                                    Policy
		total, available, resident, held, engaged uint64
		peak                                      uint64
		measured                                  int
		want                                      string
	}{
		{name: "room", policy: DefaultPolicy(), available: 4 * GiB, held: GiB, peak: 2 * GiB, measured: 3},
		{name: "tight", policy: DefaultPolicy(), available: 2 * GiB, peak: 2 * GiB, measured: 3,
			want: "a claude worker peaks at 2.0 GiB (the median of the last 3 measured), 2.0 GiB free, 512 MiB kept"},
		{name: "held", policy: DefaultPolicy(), available: 3 * GiB, held: 2 * GiB, peak: GiB,
			want: "a claude worker peaks at 1.0 GiB (none measured yet), 3.0 GiB free, 2.0 GiB of it for the workers running, 512 MiB kept"},
		{name: "one measured", policy: DefaultPolicy(), available: GiB, held: 4 * GiB, peak: GiB, measured: 1,
			want: "a claude worker peaks at 1.0 GiB (the one measured), 1.0 GiB free, 1.0 GiB of it for the workers running, 512 MiB kept"},
		{name: "memory unknown", policy: DefaultPolicy(), held: GiB, peak: GiB},

		// Each notch commits its share of the 10 GiB usable beyond the 2 GiB reserve; a worker of 500 MiB fits.
		// Forecast 1.0 GiB + 512 MiB = 1.5 GiB; 1.0 + 1.5 > 2.0 (20 % of 10 GiB).
		{name: "minimal engaged", policy: NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL), total: 16 * GiB, available: 12 * GiB,
			engaged: GiB, peak: GiB,
			want: "load minimal commits up to 2.0 GiB of memory, 1.0 GiB already engaged, needs 1.5 GiB at p95"},
		{name: "minimal fits", policy: NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL), total: 16 * GiB, available: 12 * GiB,
			peak: 500 * MiB},
		{name: "minimal none engaged", policy: NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL), total: 16 * GiB,
			available: 12 * GiB, peak: 2 * GiB, want: "load minimal commits up to 2.0 GiB of memory, needs 2.5 GiB at p95"},
		// 4.0 + 1.5 > 5.0 (50 % of 10 GiB).
		{name: "medium engaged", policy: NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM), total: 16 * GiB, available: 12 * GiB,
			engaged: 4 * GiB, peak: GiB,
			want: "load medium commits up to 5.0 GiB of memory, 4.0 GiB already engaged, needs 1.5 GiB at p80"},
		{name: "medium fits", policy: NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM), total: 16 * GiB, available: 12 * GiB,
			peak: 500 * MiB},
		// Forecast 1.0 GiB + 384 MiB = 1.4 GiB; 7.0 + 1.4 > 8.0 (80 % of 10 GiB).
		{name: "high engaged", policy: NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_HIGH), total: 16 * GiB, available: 12 * GiB,
			engaged: 7 * GiB, peak: GiB,
			want: "load high commits up to 8.0 GiB of memory, 7.0 GiB already engaged, needs 1.4 GiB at p70"},
		{name: "high fits", policy: NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_HIGH), total: 16 * GiB, available: 12 * GiB,
			peak: 500 * MiB},
		// Forecast 1.0 GiB + 256 MiB = 1.25 GiB; 9.0 + 1.25 > 10.0.
		{name: "max engaged", policy: NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_MAX), total: 16 * GiB, available: 12 * GiB,
			engaged: 9 * GiB, peak: GiB,
			want: "load max commits up to 10.0 GiB of memory, 9.0 GiB already engaged, needs 1.2 GiB at p60"},
		{name: "max fits", policy: NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_MAX), total: 16 * GiB, available: 12 * GiB,
			peak: 500 * MiB},

		// Without their resident memory counted back in, the 6th worker of the incident is held; with it, it fits.
		{name: "incident counted twice", policy: incident, total: 32 * GiB, available: gib(12.9), engaged: gib(11.3),
			peak: gib(2.1), measured: 10,
			want: "load overclock commits up to 12.9 GiB of memory, 11.3 GiB already engaged, needs 2.6 GiB at p50"},
		{name: "incident counted once", policy: incident, total: 32 * GiB, available: gib(12.9), resident: gib(3.63),
			engaged: gib(11.3), peak: gib(2.1), measured: 10},
		// What the workers running may still take stays checked against the memory free.
		{name: "incident held", policy: incident, total: 32 * GiB, available: gib(12.9), resident: gib(3.63), held: 11 * GiB,
			engaged: gib(11.3), peak: gib(2.1), measured: 10,
			want: "a claude worker peaks at 2.1 GiB (the median of the last 10 measured), 12.9 GiB free, 11.0 GiB of it for the workers running, 512 MiB kept"},
		// Minimal stays cautious: 20 % of what Djinn could use beyond the reserve, 2.9 GiB, holds no 6th worker.
		{name: "incident at minimal", policy: NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL), total: 32 * GiB,
			available: gib(12.9), resident: gib(3.63), engaged: gib(11.3), peak: gib(2.1), measured: 10,
			want: "load minimal commits up to 2.9 GiB of memory (12.9 GiB available, 3.6 GiB held by the workers running), " +
				"11.3 GiB already engaged, needs 2.6 GiB at p95"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := c.policy.WorkerRoom(c.total, c.available, c.resident, c.held, c.engaged, "claude", c.peak, c.measured)
			if got != c.want {
				t.Errorf("WorkerRoom = %q, want %q", got, c.want)
			}
		})
	}
}

// TestPolicy_commitBase: the memory available plus the workers' own; their resident memory never lifts what Djinn
// could use above the machine.
func TestPolicy_commitBase(t *testing.T) {
	t.Parallel()
	p := NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_OVERCLOCK)
	cases := []struct {
		name                       string
		total, available, resident uint64
		want                       uint64
	}{
		{"within the total", 32 * GiB, 12 * GiB, 4 * GiB, 16 * GiB},
		{"beyond the total", 32 * GiB, 30 * GiB, 8 * GiB, 32 * GiB},
		{"nothing available", 32 * GiB, 0, 8 * GiB, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := p.commitBase(c.total, c.available, c.resident); got != c.want {
				t.Errorf("commitBase = %d GiB, want %d", got>>30, c.want>>30)
			}
		})
	}
}

// TestPolicy_WorkerMemoryMax: the guardrail is a factor of a worker's measured peak, at least its forecast; none
// without a factor or a measure.
func TestPolicy_WorkerMemoryMax(t *testing.T) {
	t.Parallel()
	p := DefaultPolicy() // WorkerMargin = 512 MiB
	cases := []struct {
		name     string
		factor   float64
		peak     uint64
		measured int
		want     uint64
	}{
		{"guardrail disabled", 0, 2 * GiB, 5, 0},
		{"negative factor", -1.5, 2 * GiB, 5, 0},
		{"no measurements with factor 2", 2.0, GiB, 0, 0},
		{"no measurements with factor 3", 3.0, GiB, 0, 0},
		{"factor 2.0 above forecast", 2.0, GiB, 3, 2 * GiB},
		{"factor 2.5 above forecast", 2.5, GiB, 3, 2560 * MiB},
		{"factor 3.0 above forecast", 3.0, GiB, 3, 3 * GiB},
		// Small peak: 200 MiB peak, margin 512 MiB -> forecast 712 MiB.
		// 2.0 * 200 MiB = 400 MiB < 712 MiB -> at least forecast (712 MiB).
		{"small peak clamped to forecast", 2.0, 200 * MiB, 2, 712 * MiB},
		// Peak 500 MiB, margin 512 MiB -> forecast 1012 MiB.
		// 2.0 * 500 MiB = 1000 MiB < 1012 MiB -> at least forecast (1012 MiB).
		// 2.5 * 500 MiB = 1250 MiB > 1012 MiB -> 1250 MiB.
		{"factor 2 clamped to forecast", 2.0, 500 * MiB, 1, 1012 * MiB},
		{"factor 2.5 above a small forecast", 2.5, 500 * MiB, 1, 1250 * MiB},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := p.WorkerMemoryMax(c.factor, c.peak, c.measured); got != c.want {
				t.Errorf("WorkerMemoryMax(%g, %d, %d) = %d, want %d", c.factor, c.peak, c.measured, got, c.want)
			}
		})
	}
}

// TestPolicy_WorkerMemoryCeiling: the lower of the hard ceiling (WorkerMemory) and the guardrail (WorkerMemoryGuard);
// none when neither is set, the hard ceiling alone before a measure.
func TestPolicy_WorkerMemoryCeiling(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		memory   uint64
		guard    float64
		peak     uint64
		measured int
		want     uint64
	}{
		{"default policy", 0, 0, GiB, 5, 0},
		{"guardrail without measurements", 0, 2.0, GiB, 0, 0},
		{"guardrail measured", 0, 2.0, GiB, 1, 2 * GiB},
		{"hard ceiling without measurements", 4 * GiB, 0, GiB, 0, 4 * GiB},
		{"hard ceiling with measurements", 4 * GiB, 0, GiB, 3, 4 * GiB},
		// Without measurements: guard is 0, hard ceiling applies.
		{"both without measurements", 3 * GiB, 2.0, GiB, 0, 3 * GiB},
		// With peak 1 GiB: guard is 2 GiB < 3 GiB hard ceiling -> 2 GiB.
		{"both with guard below hard", 3 * GiB, 2.0, GiB, 1, 2 * GiB},
		// With peak 2 GiB: guard is 4 GiB > 3 GiB hard ceiling -> 3 GiB.
		{"both with guard above hard", 3 * GiB, 2.0, 2 * GiB, 1, 3 * GiB},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			p := DefaultPolicy()
			p.WorkerMemory, p.WorkerMemoryGuard = c.memory, c.guard
			if got := p.WorkerMemoryCeiling(c.peak, c.measured); got != c.want {
				t.Errorf("WorkerMemoryCeiling = %d, want %d", got, c.want)
			}
		})
	}
}

// TestPolicy_CommittableLimit: each notch commits its share of the memory beyond the 2 GiB reserve, Overclock past
// it up to the memory available.
func TestPolicy_CommittableLimit(t *testing.T) {
	t.Parallel()
	avail12 := uint64(12 * GiB) // usable 10 GiB beyond 2 GiB reserve
	cases := []struct {
		name      string
		notch     djinnv1.LoadNotch
		available uint64
		want      uint64
	}{
		{"minimal 12G", djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL, avail12, 2 * GiB},      // 20% of 10G
		{"medium 12G", djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM, avail12, 5 * GiB},        // 50% of 10G
		{"high 12G", djinnv1.LoadNotch_LOAD_NOTCH_HIGH, avail12, 8 * GiB},            // 80% of 10G
		{"max 12G", djinnv1.LoadNotch_LOAD_NOTCH_MAX, avail12, 10 * GiB},             // 100% of 10G
		{"overclock 12G", djinnv1.LoadNotch_LOAD_NOTCH_OVERCLOCK, avail12, 12 * GiB}, // 150% of 10G = 15G, capped at available 12G
		{"zero available", djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM, 0, 0},
		{"available below reserve", djinnv1.LoadNotch_LOAD_NOTCH_MAX, 2 * GiB, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := NotchPolicy(c.notch).CommittableLimit(c.available); got != c.want {
				t.Errorf("CommittableLimit = %d, want %d", got, c.want)
			}
		})
	}
}

// TestPolicy_LocalModel: whether a local model runs on the machine, on which GPU, and why not.
func TestPolicy_LocalModel(t *testing.T) {
	t.Parallel()
	rtx := GPU{Vendor: "nvidia", Name: "NVIDIA GeForce RTX 3080", Driver: "nvidia", DriverVersion: "550.54.14", Memory: 10 * GiB}
	intel := GPU{Vendor: "intel", Name: "8086:4680", Driver: "i915"}
	roomy := &Disk{Path: "/data", Total: 500 * GiB, Available: 100 * GiB}
	cases := []struct {
		name string
		s    Snapshot
		want bool
		why  string
	}{
		{"nvidia", Snapshot{MemoryTotal: 8 * GiB, GPUs: []GPU{intel, rtx}, Disk: roomy}, true,
			"on the NVIDIA GeForce RTX 3080, with 10.0 GiB of its own memory, driver nvidia 550.54.14"},
		{"full disk", Snapshot{MemoryTotal: 64 * GiB, GPUs: []GPU{rtx}, Disk: &Disk{Path: "/data", Available: 3 * GiB}}, false,
			"3.0 GiB free on the disk of /data, 10.0 GiB at least"},
		{"nouveau", Snapshot{MemoryTotal: 8 * GiB, GPUs: []GPU{{Vendor: "nvidia", Name: "10de:2206", Driver: "nouveau"}}}, false,
			"the 10de:2206 has no driver a model runs on (nouveau), and 8.0 GiB of memory (16.0 GiB at least"},
		{"no smi", Snapshot{MemoryTotal: 8 * GiB, GPUs: []GPU{{Vendor: "nvidia", Name: "10de:2206", Driver: "nvidia"}}}, false,
			"the memory of the 10de:2206 is unknown"},
		{"small amd", Snapshot{MemoryTotal: 8 * GiB, GPUs: []GPU{{Vendor: "amd", Name: "1002:164e", Driver: "amdgpu", Memory: GiB / 2}}}, false,
			"has 512 MiB of its own memory, 6.0 GiB at least"},
		{"big amd", Snapshot{MemoryTotal: 8 * GiB, GPUs: []GPU{{Vendor: "amd", Name: "1002:73bf", Driver: "amdgpu", Memory: 16 * GiB}}}, true,
			"driver amdgpu"},
		{"apple", Snapshot{MemoryTotal: 32 * GiB, GPUs: []GPU{AppleGPU("Apple M2 Pro", 32*GiB)}}, true,
			"on the Apple M2 Pro, through metal, with 32.0 GiB of unified memory"},
		{"small apple", Snapshot{MemoryTotal: 8 * GiB, GPUs: []GPU{AppleGPU("Apple M1", 8*GiB)}}, false,
			"the Apple M1 has 8.0 GiB of unified memory, 16.0 GiB at least"},
		{"cpu", Snapshot{MemoryTotal: 32 * GiB, GPUs: []GPU{intel}, Disk: roomy}, true,
			"on the CPU, slowly, with 32.0 GiB of memory: the intel GPU 8086:4680 is not counted"},
		{"little", Snapshot{MemoryTotal: 8 * GiB}, false, "no GPU found, and 8.0 GiB of memory"},
		{"unknown", Snapshot{}, false, "no GPU found, and the memory is unknown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ok, why := DefaultPolicy().LocalModel(c.s)
			if ok != c.want || !strings.Contains(why, c.why) {
				t.Errorf("LocalModel = %v (%s), want %v (%s)", ok, why, c.want, c.why)
			}
		})
	}
}

// TestMonitor_Snapshot: the machine is read once a second at most, the cores kept when reading fails; each snapshot
// carries the workers' resident memory read now, even from the cache.
func TestMonitor_Snapshot(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"read once a second", func(t *testing.T) {
			reads := 0
			m := NewMonitor(DefaultPolicy(), func() (Snapshot, error) {
				reads++
				return Snapshot{Cores: 4, MemoryTotal: 64 * GiB}, nil
			})
			m.Capacity()
			m.Snapshot()
			if reads != 1 {
				t.Errorf("%d reads, want 1", reads)
			}
		}},
		{"failed read", func(t *testing.T) {
			failing := NewMonitor(DefaultPolicy(), func() (Snapshot, error) { return Snapshot{}, errors.New("no /proc") })
			if s := failing.Snapshot(); s.Cores == 0 || s.OS == "" {
				t.Errorf("after a failed read: %+v", s)
			}
		}},
		{"worker memory", func(t *testing.T) {
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
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.run(t)
		})
	}
}

// TestMonitor_Capacity: the slots the scheduler lets run and why the machine is under pressure.
func TestMonitor_Capacity(t *testing.T) {
	t.Parallel()
	m := NewMonitor(DefaultPolicy(), func() (Snapshot, error) {
		return Snapshot{Cores: 4, MemoryTotal: 64 * GiB, CPU: &Pressure{Some: 90}}, nil
	})
	if slots, _, pressure := m.Capacity(); slots != 1 || pressure == "" || pressure != m.Pressure() {
		t.Errorf("slots %d, pressure %q (%q now)", slots, pressure, m.Pressure())
	}

	// A 16 GB Mac at Max, 3.47 GB available, its workers measured at 302 MiB: 1.32 GB beyond the reserve holds one
	// worker at the notch's 1 GiB, two at the measure (302 + 256 MiB of margin).
	mac := NewMonitor(NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_MAX), func() (Snapshot, error) {
		return Snapshot{Cores: 8, MemoryTotal: 16 * GiB, MemoryAvailable: 3466969088}, nil
	})
	mac.every = 0
	if slots, rule, _ := mac.Capacity(); slots != 1 {
		t.Errorf("unmeasured: %d slots, want 1 (%s)", slots, rule)
	}
	mac.SetWorkerPeak(func() uint64 { return 302 * MiB })
	if slots, rule, _ := mac.Capacity(); slots != 2 {
		t.Errorf("measured: %d slots, want 2 (%s)", slots, rule)
	}
	mac.SetWorkerPeak(func() uint64 { return 0 })
	if slots, rule, _ := mac.Capacity(); slots != 1 {
		t.Errorf("none measured yet: %d slots, want 1 (%s)", slots, rule)
	}
}

// TestMonitor_NotchSlots: on every machine, with or without djinn up --workers, what the load bar says each notch runs
// (NotchSlots) is what the scheduler lets run once that notch is in effect (Capacity, the harness's WithCapacity); a
// higher notch never runs fewer; what the notch itself lets run (BarSlots) is never below it.
func TestMonitor_NotchSlots(t *testing.T) {
	t.Parallel()
	for _, s := range slotMachines {
		for _, workers := range []int{0, 3} {
			t.Run(fmt.Sprintf("cores=%d avail=%d workers=%d", s.Cores, s.MemoryAvailable>>20, workers), func(t *testing.T) {
				t.Parallel()
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

// TestReader reads the real machine: whatever it is, it has cores, and memory and a disk where Djinn reads them; and
// a verdict on a local model, with its reason. Its GPUs depend on the machine: they are logged.
func TestReader(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := NewMonitor(DefaultPolicy(), Reader(dir)).Snapshot()
	if s.Cores < 1 {
		t.Errorf("no cores: %+v", s)
	}
	if s.OS == "linux" || s.OS == "darwin" || s.OS == "windows" {
		if s.MemoryTotal == 0 {
			t.Errorf("no memory: %+v", s)
		}
		if s.Disk == nil || s.Disk.Path != dir || s.Disk.Total == 0 || s.Disk.Available > s.Disk.Total {
			t.Errorf("disk: %+v", s.Disk)
		}
	}
	ok, why := DefaultPolicy().LocalModel(s)
	if why == "" {
		t.Error("a verdict without a reason")
	}
	t.Logf("GPUs %+v; a local model: %v, %s", s.GPUs, ok, why)
}

// TestCalm: a Djinn under test reads this machine's cores and memory, all of it available.
func TestCalm(t *testing.T) {
	t.Parallel()
	s, _ := Calm()
	if s.Cores < 1 || s.MemoryAvailable != s.MemoryTotal || s.CPU != nil || s.LoadKnown {
		t.Errorf("Calm = %+v", s)
	}
}

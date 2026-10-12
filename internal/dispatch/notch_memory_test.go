package dispatch

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/machine"
)

// gib is a fraction of a GiB in bytes.
func gib(f float64) uint64 { return uint64(f * machine.GiB) }

// spread is a worker's memory series: its percentiles from p50 to p95, evenly between p50 and p95.
func spread(p50, p95 float64) *planv1.Resources {
	step := (p95 - p50) / 4
	return &planv1.Resources{
		P50MemoryBytes: gib(p50), P60MemoryBytes: gib(p50 + step), P70MemoryBytes: gib(p50 + 2*step),
		P80MemoryBytes: gib(p50 + 3*step), P95MemoryBytes: gib(p95), PeakMemoryBytes: gib(p95 + 0.9),
	}
}

// incident is the developer's machine on 12/10/2026 (decision block "Higher notches exploit the machine..."): 16 cores,
// 32 GiB, 12.9 GiB available, 5 claude workers running, which hold 3.63 GiB (0.5 + 0.75 + 2.2 + 0.18 GiB, the fifth
// not read yet), and 10 finished claude workers whose p95 is 2.1 GiB, their p50 0.6 GiB. Today's rule engaged them at
// 11.3 GiB: each at the p95 of its run plus 512 MiB, the fifth at the providers' 2.1 GiB, and held the next one with
// "load overclock commits up to 12.9 GiB of memory, 11.3 GiB already engaged, needs 2.6 GiB".
func incident(at time.Time, planned int) []*planv1.Task {
	var tasks []*planv1.Task
	for i := range 10 {
		res := spread(0.6, 2.1)
		tasks = append(tasks, &planv1.Task{
			Id: fmt.Sprintf("done%d", i), WishId: "w", Status: planv1.TaskStatus_TASK_STATUS_DONE,
			Provider: planv1.Provider_PROVIDER_CLAUDE, EndTime: timestamppb.New(at.Add(-time.Duration(i+1) * time.Hour)),
			Resources: res,
		})
	}
	for i, w := range []struct{ uses, p50, p95 float64 }{
		{0.5, 0.45, 1.6}, {0.75, 0.7, 2.0}, {2.2, 1.2, 2.4}, {0.18, 0.17, 0.7},
	} {
		res := spread(w.p50, w.p95)
		res.MemoryBytes = gib(w.uses)
		tasks = append(tasks, &planv1.Task{
			Id: fmt.Sprintf("run%d", i), Code: fmt.Sprintf("W%d", 460+i), WishId: "w",
			Status: planv1.TaskStatus_TASK_STATUS_RUNNING, Provider: planv1.Provider_PROVIDER_CLAUDE,
			StartTime: timestamppb.New(at.Add(-20 * time.Minute)), Resources: res,
		})
	}
	tasks = append(tasks, &planv1.Task{
		Id: "run4", Code: "W464", WishId: "w", Status: planv1.TaskStatus_TASK_STATUS_RUNNING,
		Provider: planv1.Provider_PROVIDER_CLAUDE, StartTime: timestamppb.New(at.Add(-time.Minute)),
	})
	for i := range planned {
		tasks = append(tasks, &planv1.Task{
			Id: fmt.Sprintf("new%d", i), Code: fmt.Sprintf("W%d", 470+i), WishId: "w",
			Status: planv1.TaskStatus_TASK_STATUS_PENDING, Provider: planv1.Provider_PROVIDER_CLAUDE, Scheduled: true,
			CreateTime: timestamppb.New(at.Add(time.Duration(i) * time.Minute)),
		})
	}
	return tasks
}

// TestOverclockIncident replays the incident: at Overclock, the 6th worker was held while the workers running held
// 3.6 GiB of 32 GiB. Their memory is now counted once (back into what Djinn could use, not only out of what is
// available), and each is engaged at its p50 plus 256 MiB: the scheduler fills the 8 slots the CPU allows.
func TestOverclockIncident(t *testing.T) {
	at := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	wish := &planv1.Wish{Id: "w", State: planv1.WishState_WISH_STATE_ACTIVE}
	tasks := incident(at, 5)
	m := &Machine{
		Slots: 8, Rule: "cpu_slots 8, as djinn load get said", Running: 5,
		Total: 32 * machine.GiB, Available: gib(12.9),
		Policy: machine.NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_OVERCLOCK),
	}
	started := 0
	for _, d := range New(tasks, []*planv1.Wish{wish}, nil, m).At(at).Pass() {
		switch {
		case d.Why == "" && d.Failed == "":
			started++
		case !strings.Contains(d.Why, "the most this machine holds"):
			t.Errorf("%s waits for memory, not for a slot: %q", d.Task.GetCode(), d.Why)
		}
	}
	if running := 5 + started; running < 8 {
		t.Errorf("%d workers run at Overclock, want at least 8 (the CPU allows 8)", running)
	}

	// Even engaged at p95, the 6th worker fits once the running workers' memory is counted once.
	m.Policy.Percentile = 95
	m.Policy.WorkerMargin = 512 * machine.MiB
	if d := New(incident(at, 1), []*planv1.Wish{wish}, nil, m).At(at).Pass(); d[0].Why != "" {
		t.Errorf("at p95, the 6th worker waits: %q", d[0].Why)
	}

	// Minimal stays cautious on the same machine: no 6th worker.
	m.Policy = machine.NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL)
	m.Slots = 8
	d := New(incident(at, 1), []*planv1.Wish{wish}, nil, m).At(at).Pass()
	if !strings.HasPrefix(d[0].Why, "load minimal commits up to") {
		t.Errorf("at Minimal, the 6th worker: %q, want it held by what minimal commits", d[0].Why)
	}
}

// TestEngagedAtNotchPercentile: a running worker is engaged at its own memory at the notch's percentile plus the
// notch's margin, a new one at the median of the finished workers' value at that percentile.
func TestEngagedAtNotchPercentile(t *testing.T) {
	at := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	res := &planv1.Resources{
		MemoryBytes:    100 * machine.MiB,
		P50MemoryBytes: 500 * machine.MiB, P60MemoryBytes: 600 * machine.MiB, P70MemoryBytes: 700 * machine.MiB,
		P80MemoryBytes: 800 * machine.MiB, P95MemoryBytes: 950 * machine.MiB, PeakMemoryBytes: 3000 * machine.MiB,
	}
	running := &planv1.Task{Id: "r", Status: planv1.TaskStatus_TASK_STATUS_RUNNING,
		StartTime: timestamppb.New(at.Add(-time.Hour)), Resources: res}
	done := &planv1.Task{Id: "d", Status: planv1.TaskStatus_TASK_STATUS_DONE, EndTime: timestamppb.New(at), Resources: res}
	for _, tt := range []struct {
		notch      djinnv1.LoadNotch
		percentile int
		at, margin uint64
	}{
		{djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL, 95, 950, 512},
		{djinnv1.LoadNotch_LOAD_NOTCH_LIGHT, 80, 800, 512}, // Light is Medium (NotchPolicy)
		{djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM, 80, 800, 512},
		{djinnv1.LoadNotch_LOAD_NOTCH_HIGH, 70, 700, 384},
		{djinnv1.LoadNotch_LOAD_NOTCH_MAX, 60, 600, 256},
		{djinnv1.LoadNotch_LOAD_NOTCH_OVERCLOCK, 50, 500, 256},
	} {
		p := machine.NotchPolicy(tt.notch)
		if got := p.MemoryPercentile(); got != tt.percentile {
			t.Errorf("%v: percentile %d, want %d", tt.notch, got, tt.percentile)
		}
		want := (tt.at + tt.margin) * machine.MiB
		if engaged, actual := WorkerMemory([]*planv1.Task{running}, p, at); engaged != want || actual != 100*machine.MiB {
			t.Errorf("%v: engaged %d MiB (actual %d MiB), want %d MiB", tt.notch, engaged>>20, actual>>20, want>>20)
		}
		if got := TypicalForecast([]*planv1.Task{done}, p); got != want {
			t.Errorf("%v: a new worker's forecast %d MiB, want %d MiB", tt.notch, got>>20, want>>20)
		}
	}

	// A worker measured before the percentiles were kept falls back to its p95, then its peak.
	old := &planv1.Resources{P95MemoryBytes: 950 * machine.MiB, PeakMemoryBytes: 3000 * machine.MiB}
	if got := MemoryAt(old, 50); got != 950*machine.MiB {
		t.Errorf("MemoryAt(no p50, 50) = %d MiB, want its p95", got>>20)
	}
	if got := MemoryAt(&planv1.Resources{PeakMemoryBytes: 3 * machine.GiB}, 50); got != 3*machine.GiB {
		t.Errorf("MemoryAt(peak only, 50) = %d MiB, want its peak", got>>20)
	}
	// The guardrail keeps the p95, whatever the notch.
	if got := ProviderPeaks([]*planv1.Task{done})["claude"]; len(got) != 1 || got[0] != 950*machine.MiB {
		t.Errorf("ProviderPeaks = %v, want the p95", got)
	}
}

// TestRunningWorkerCountedOnce: a running worker's resident memory is left out of the memory available; WorkerRoom
// counts it back into what Djinn could use, and what the worker may still take is what it is engaged at beyond what it
// holds.
func TestRunningWorkerCountedOnce(t *testing.T) {
	at := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	wish := &planv1.Wish{Id: "w", State: planv1.WishState_WISH_STATE_ACTIVE}
	// One worker holds 3 GiB, its whole series at 3 GiB: at Max, engaged at 3 GiB + 256 MiB.
	res := spread(3, 3)
	res.MemoryBytes = 3 * machine.GiB
	busy := &planv1.Task{Id: "busy", WishId: "w", Status: planv1.TaskStatus_TASK_STATUS_RUNNING,
		StartTime: timestamppb.New(at.Add(-time.Hour)), Resources: res}
	next := &planv1.Task{Id: "next", Code: "W2", WishId: "w", Status: planv1.TaskStatus_TASK_STATUS_PENDING,
		Scheduled: true, CreateTime: timestamppb.New(at)}
	// 8 GiB total, 4 GiB available: 3 GiB for busy, 1 GiB for the rest. Djinn could use 7 GiB, 5 GiB beyond the
	// reserve; at Max, 3.25 GiB engaged plus 1.25 GiB for the next (1 GiB default + 256 MiB) = 4.5 GiB fits.
	m := &Machine{Slots: 4, Running: 1, Total: 8 * machine.GiB, Available: 4 * machine.GiB,
		Policy: machine.NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_MAX)}
	sit := New([]*planv1.Task{busy, next}, []*planv1.Wish{wish}, nil, m).At(at)
	if got := sit.resident(); got != 3*machine.GiB {
		t.Errorf("resident = %d MiB, want 3072", got>>20)
	}
	if got := sit.growing(); got != 0 {
		t.Errorf("growing = %d MiB, want 0: busy holds what it is engaged at", got>>20)
	}
	// Counted twice, the limit was the share of 4 GiB available beyond the reserve: 2 GiB, against 4.5 GiB engaged.
	if why := m.Policy.WorkerRoom(m.Total, m.Available, 0, 0, sit.Engaged(), "claude", machine.GiB, 0); !strings.Contains(why,
		"commits up to 2.0 GiB") {
		t.Errorf("counted twice: %q, want the old 2.0 GiB limit", why)
	}
	if d := sit.Pass(); d[0].Why != "" {
		t.Errorf("next waits: %q", d[0].Why)
	}
}

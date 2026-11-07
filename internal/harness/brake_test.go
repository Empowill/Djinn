package harness

import (
	"context"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/machine"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// TestPickBrake: the brake freezes what grows outside the gates, the most first; never a worker the gates admitted
// while anything else grows or waits for its interruption; when admitted workers alone grow, the last admitted.
func TestPickBrake(t *testing.T) {
	t.Parallel()
	const mib = 1 << 20
	at := time.Date(2026, 11, 7, 18, 0, 0, 0, time.UTC)
	escaped := func(code string, growth int64) brakeCandidate {
		return brakeCandidate{id: code, code: code, growth: growth * mib}
	}
	admitted := func(code string, growth int64, granted time.Time) brakeCandidate {
		return brakeCandidate{id: code, code: code, growth: growth * mib, admitted: true, granted: granted}
	}
	withGit := func(c brakeCandidate) brakeCandidate { c.git = true; return c }
	for _, tc := range []struct {
		name    string
		cands   []brakeCandidate
		pending bool
		want    string // "" for none
	}{
		{"nothing grows", []brakeCandidate{escaped("W1", 100), admitted("W2", 200, at)}, false, ""},
		{"the most grown outside the gates", []brakeCandidate{escaped("W1", 300), escaped("W2", 900), escaped("W3", 500)}, false, "W2"},
		{"outside the gates before an admitted one that grew more",
			[]brakeCandidate{admitted("W1", 5000, at), escaped("W2", 300)}, false, "W2"},
		{"no admitted one while what grows outside runs git",
			[]brakeCandidate{admitted("W1", 5000, at), withGit(escaped("W2", 300))}, false, ""},
		{"the next grown outside when the first runs git",
			[]brakeCandidate{withGit(escaped("W1", 900)), escaped("W2", 300), admitted("W3", 5000, at)}, false, "W2"},
		{"no admitted one while a frozen one waits for its interruption",
			[]brakeCandidate{admitted("W1", 5000, at)}, true, ""},
		{"only admitted ones grow: the last admitted, not the most grown",
			[]brakeCandidate{admitted("W1", 5000, at), admitted("W2", 300, at.Add(time.Minute)), admitted("W3", 900, at.Add(-time.Minute))},
			false, "W2"},
		{"the last admitted that does not run git",
			[]brakeCandidate{admitted("W1", 5000, at), withGit(admitted("W2", 300, at.Add(time.Minute)))}, false, "W1"},
		{"admitted ones, grants unknown: the most grown",
			[]brakeCandidate{admitted("W1", 300, time.Time{}), admitted("W2", 900, time.Time{})}, false, "W2"},
		{"an admitted one that does not grow is left", []brakeCandidate{admitted("W1", 10, at.Add(time.Hour)), admitted("W2", 300, at)}, false, "W2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ""
			if c := pickBrake(slices.Clone(tc.cands), 256*mib, tc.pending); c != nil {
				got = c.code
			}
			if got != tc.want {
				t.Errorf("pickBrake = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestBrakeThresholds: the memory is short under 10 % available or at a PSI full of 20; calm above 15 % and a PSI full
// under 5; no brake at 0.
func TestBrakeThresholds(t *testing.T) {
	t.Parallel()
	b := DefaultBrake(10)
	const total = 32 << 30
	snap := func(availablePercent int, full float64) machine.Snapshot {
		s := machine.Snapshot{MemoryTotal: total, MemoryAvailable: total / 100 * uint64(availablePercent)}
		if full >= 0 {
			s.Memory = &machine.Pressure{Some: full, Full: full}
		}
		return s
	}
	for _, tc := range []struct {
		name        string
		s           machine.Snapshot
		short, calm bool
	}{
		{"8 % available", snap(8, 0), true, false},
		{"12 % available", snap(12, 0), false, false},
		{"16 % available", snap(16, 0), false, true},
		{"a PSI full of 25", snap(50, 25), true, false},
		{"a PSI full of 6", snap(50, 6), false, false},
		{"no PSI", snap(50, -1), false, true},
	} {
		if short := b.short(tc.s) != ""; short != tc.short {
			t.Errorf("%s: short = %v, want %v (%q)", tc.name, short, tc.short, b.short(tc.s))
		}
		if calm := b.calm(tc.s); calm != tc.calm {
			t.Errorf("%s: calm = %v, want %v", tc.name, calm, tc.calm)
		}
	}
	if got := b.short(snap(8, 0)); got != "8% of memory available (2.6 GiB)" {
		t.Errorf("short says %q", got)
	}
	if DefaultBrake(0).on() {
		t.Error("a brake at 0 percent is on")
	}
}

// brakeTest is a harness whose machine, workers' memory and gates the test sets, its clock moved by the test.
type brakeTest struct {
	*env
	clock    *fakeClock
	snap     atomic.Pointer[machine.Snapshot]
	mu       sync.Mutex
	memory   map[string]uint64 // by task
	admitted map[string]bool
	paused   []string // what docker paused or unpaused: "+W1", "-W1"
	told     []string
}

// upBrake starts a harness with a 32 GiB machine, 50 % available.
func upBrake(t *testing.T, opts ...Option) *brakeTest {
	t.Helper()
	bt := &brakeTest{clock: &fakeClock{at: time.Date(2026, 11, 7, 18, 0, 0, 0, time.UTC)},
		memory: map[string]uint64{}, admitted: map[string]bool{}}
	bt.press(50, 0)
	b := DefaultBrake(10)
	b.Memory = func(taskID, _ string) (uint64, bool) {
		bt.mu.Lock()
		defer bt.mu.Unlock()
		m, ok := bt.memory[taskID]
		return m, ok
	}
	b.Git = func(string) bool { return false }
	containers := Containers{
		Read: func(string) (machine.Group, error) { return machine.Group{Processes: 1}, nil },
		Pause: func(_ context.Context, pause bool, labels ...string) int {
			bt.mu.Lock()
			defer bt.mu.Unlock()
			sign := "-"
			if pause {
				sign = "+"
			}
			bt.paused = append(bt.paused, sign+strings.TrimPrefix(labels[0], "djinn.task="))
			return 1
		},
	}
	bt.env = up(t, t.TempDir(), append([]Option{
		WithClock(bt.clock.now), WithTick(10 * time.Millisecond), WithSilenceLimit(10 * time.Hour),
		WithTellDelay(10 * time.Millisecond), WithContainers(containers),
		WithBrake(func() machine.Snapshot { return *bt.snap.Load() }, b),
	}, opts...)...)
	bt.h.TellLeads(func(_ context.Context, _, line string) error {
		bt.mu.Lock()
		defer bt.mu.Unlock()
		bt.told = append(bt.told, line)
		return nil
	})
	bt.h.HeldGates(func(id string) []string {
		bt.mu.Lock()
		defer bt.mu.Unlock()
		if bt.admitted[id] {
			return []string{AdmissionGate}
		}
		return nil
	}, func(string) []string { return nil })
	return bt
}

// press sets the machine's memory available, in percent of its 32 GiB, and its PSI full.
func (bt *brakeTest) press(availablePercent int, full float64) {
	const total = 32 << 30
	bt.snap.Store(&machine.Snapshot{MemoryTotal: total, MemoryAvailable: total / 100 * uint64(availablePercent),
		Memory: &machine.Pressure{Some: full, Full: full}})
}

// hold sets what the task's worker holds, in MiB.
func (bt *brakeTest) hold(id string, mib uint64) {
	bt.mu.Lock()
	defer bt.mu.Unlock()
	bt.memory[id] = mib << 20
}

// step moves the clock by d, and waits until a pass of the scheduler read the machine at that time.
func (bt *brakeTest) step(t *testing.T, d time.Duration) {
	t.Helper()
	at := bt.clock.now().Add(d)
	bt.clock.set(at)
	waitFor(t, "a brake reading at "+at.Format(time.TimeOnly), func() bool {
		bt.h.Wake()
		bt.h.sched.Lock()
		defer bt.h.sched.Unlock()
		return bt.h.brakeAt.read.Equal(at)
	})
}

// toldLines are the lines the lead heard of the brake, once flushed.
func (bt *brakeTest) toldLines(t *testing.T, want string) []string {
	t.Helper()
	var lines []string
	waitFor(t, "the lead told "+want, func() bool {
		bt.h.batcher.Flush(t.Context())
		bt.mu.Lock()
		defer bt.mu.Unlock()
		lines = nil
		for _, l := range bt.told {
			if strings.Contains(l, "by the memory brake") || strings.Contains(l, " thawed: ") {
				lines = append(lines, l)
			}
		}
		return slices.ContainsFunc(lines, func(l string) bool { return strings.Contains(l, want) })
	})
	return lines
}

// hasEvent says whether the task has a status event that holds text.
func (bt *brakeTest) hasEvent(t *testing.T, id, text string) bool {
	t.Helper()
	return slices.ContainsFunc(bt.storedEvents(t, id), func(ev *planv1.TaskEvent) bool {
		return ev.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_STATUS && strings.Contains(ev.GetText(), text)
	})
}

// TestBrakeFreezesWhatEscapesTheGates: under strong pressure, the worker that grows outside the gates freezes first,
// its containers too, while the admitted one that grows more runs on; frozen 30 s, the memory still short, it is
// interrupted. Then the admitted one, alone to grow, freezes, is never interrupted however long the pressure lasts,
// and thaws after a minute of calm, its frozen time out of its budget, judged neither silent nor stalled. The lead hears
// each step.
func TestBrakeFreezesWhatEscapesTheGates(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	bt := upBrake(t)
	wishID, _ := bt.wish(t, nativeFolder(t))
	w1 := bt.mustSpawn(t, wishID, "Admitted", "wait", &planv1.TaskServiceSpawnRequest{WriteScopes: []string{"a"}})
	w2 := bt.mustSpawn(t, wishID, "Escaped", "wait", &planv1.TaskServiceSpawnRequest{WriteScopes: []string{"b"}})
	bt.until(t, w1.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
	bt.until(t, w2.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
	bt.mu.Lock()
	bt.admitted[w1.GetId()] = true
	bt.mu.Unlock()
	granted := bt.clock.now()
	bt.h.GateGrants(func() map[string]time.Time { return map[string]time.Time{w1.GetId(): granted} })

	// Both start at 1 GiB; then the admitted one grows 3 GiB, the other 1 GiB, and the memory runs short.
	bt.hold(w1.GetId(), 1024)
	bt.hold(w2.GetId(), 1024)
	bt.step(t, time.Second)
	bt.hold(w1.GetId(), 4096)
	bt.hold(w2.GetId(), 2048)
	bt.press(6, 0)
	bt.step(t, time.Second)
	if st := bt.get(t, w2.GetId()).GetStatus(); st != planv1.TaskStatus_TASK_STATUS_RUNNING {
		t.Fatalf("one reading of short memory froze W2: %v", st)
	}
	bt.step(t, time.Second)
	bt.until(t, w2.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_PAUSED))
	if st := bt.get(t, w1.GetId()).GetStatus(); st != planv1.TaskStatus_TASK_STATUS_RUNNING {
		t.Fatalf("the admitted worker froze while another grew: %v", st)
	}
	if !bt.hasEvent(t, w2.GetId(), "paused: memory brake: 6% of memory available (1.9 GiB); it grew 1 GiB in 1 min outside the gates") {
		t.Errorf("W2's freeze event: %v", eventTexts(bt.storedEvents(t, w2.GetId())))
	}
	bt.toldLines(t, w2.GetCode()+" frozen by the memory brake")

	// Frozen past 30 s, the memory still short: W2 is interrupted, the admitted one untouched meanwhile.
	for range 3 {
		bt.step(t, 10*time.Second)
		if st := bt.get(t, w1.GetId()).GetStatus(); st != planv1.TaskStatus_TASK_STATUS_RUNNING {
			t.Fatalf("the admitted worker froze before W2's interruption: %v", st)
		}
	}
	bt.toldLines(t, w2.GetCode()+" interrupted by the memory brake")
	if !bt.hasEvent(t, w2.GetId(), "resuming: memory brake: frozen 30 s, the memory stayed short") {
		t.Errorf("W2's interruption event: %v", eventTexts(bt.storedEvents(t, w2.GetId())))
	}
	bt.hold(w2.GetId(), 200) // What grew went with its worker.
	waitFor(t, "W2 resumed in its session", func() bool { return bt.hasEvent(t, w2.GetId(), "resumed after the memory brake") })

	// Only the admitted one grows now: it freezes, the last admitted, once the readings had 10 s to show what W2's
	// interruption gave back; it stays frozen, never interrupted.
	bt.hold(w1.GetId(), 6144)
	bt.step(t, time.Second)
	if st := bt.get(t, w1.GetId()).GetStatus(); st != planv1.TaskStatus_TASK_STATUS_RUNNING {
		t.Fatalf("the admitted worker froze a second after W2's interruption: %v", st)
	}
	bt.step(t, 9*time.Second)
	bt.until(t, w1.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_PAUSED))
	frozenAt := bt.clock.now()
	bt.toldLines(t, w1.GetCode()+" frozen by the memory brake")
	if !bt.hasEvent(t, w1.GetId(), "only admitted commands grow, this one admitted last: never interrupted") {
		t.Errorf("W1's freeze event: %v", eventTexts(bt.storedEvents(t, w1.GetId())))
	}
	for range 4 {
		bt.step(t, 10*time.Minute)
	}
	if st := bt.get(t, w1.GetId()).GetStatus(); st != planv1.TaskStatus_TASK_STATUS_PAUSED {
		t.Fatalf("the admitted worker, frozen 40 min under pressure: %v, want still PAUSED", st)
	}
	if bt.hasEvent(t, w1.GetId(), "resuming") {
		t.Error("the admitted worker was interrupted")
	}

	// Calm: it thaws after a minute of it, not before.
	bt.press(40, 0)
	bt.step(t, time.Second)
	bt.step(t, 30*time.Second)
	if st := bt.get(t, w1.GetId()).GetStatus(); st != planv1.TaskStatus_TASK_STATUS_PAUSED {
		t.Fatalf("thawed after 30 s of calm: %v", st)
	}
	bt.step(t, 30*time.Second)
	thawed := bt.until(t, w1.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
	lines := bt.toldLines(t, w1.GetCode()+" thawed")
	if !slices.ContainsFunc(lines, func(l string) bool {
		return strings.Contains(l, w1.GetCode()+" thawed: the memory is calm again (40% available). Frozen 41 min at 6 GiB, its command's peak kept by the gates")
	}) {
		t.Errorf("the lead heard: %q", lines)
	}
	for _, l := range lines {
		if len(l) > 160 {
			t.Errorf("a line past 160 characters: %q", l)
		}
	}
	// Its frozen time is out of its budget, and it is judged neither silent nor stalled for it.
	now := bt.clock.now()
	if wall, held := taskWallTime(thawed, now), now.Sub(frozenAt); wall >= held {
		t.Errorf("wall time %v counts the %v frozen", wall, held)
	}
	if hl := bt.h.runningHealth(thawed, now.Add(time.Second)); hl != nil {
		t.Errorf("thawed, judged %v", hl)
	}
	bt.mu.Lock()
	paused := slices.Clone(bt.paused)
	bt.mu.Unlock()
	if want := []string{"+" + w2.GetId(), "+" + w1.GetId(), "-" + w1.GetId()}; !slices.Equal(paused, want) {
		t.Errorf("containers paused %v, want %v", paused, want)
	}
	cmds, err := store.Commands(t.Context(), bt.db, func(c store.Command) bool {
		return c.Actor == actorHarness && c.Method == methodBrake
	})
	if err != nil || len(cmds) != 4 {
		t.Errorf("journaled %d brake commands, want 4 (2 freezes, 1 interruption, 1 thaw): %v", len(cmds), err)
	}
	bt.release(t, w1.GetId())
	bt.ended(t, w1.GetId())
}

// TestBrakeThawedByHand: a worker the brake froze, resumed by hand, has its containers thawed too, and the brake
// forgets it: calm comes, nothing else thaws.
func TestBrakeThawedByHand(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	bt := upBrake(t)
	wishID, _ := bt.wish(t, nativeFolder(t))
	w := bt.mustSpawn(t, wishID, "Escaped", "wait", nil)
	bt.until(t, w.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
	bt.hold(w.GetId(), 500)
	bt.step(t, time.Second)
	bt.hold(w.GetId(), 1500)
	bt.press(5, 0)
	bt.step(t, time.Second)
	bt.step(t, time.Second)
	bt.until(t, w.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_PAUSED))
	if _, err := bt.tasks.Resume(t.Context(), connect.NewRequest(&planv1.TaskServiceResumeRequest{TaskId: w.GetId()})); err != nil {
		t.Fatal(err)
	}
	bt.until(t, w.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
	waitFor(t, "its containers thawed", func() bool {
		bt.mu.Lock()
		defer bt.mu.Unlock()
		return slices.Equal(bt.paused, []string{"+" + w.GetId(), "-" + w.GetId()})
	})
	bt.press(40, 0)
	bt.step(t, time.Second)
	bt.h.sched.Lock()
	frozen := len(bt.h.brakeAt.frozen)
	bt.h.sched.Unlock()
	if frozen != 0 {
		t.Errorf("the brake still counts %d frozen", frozen)
	}
	bt.release(t, w.GetId())
	bt.ended(t, w.GetId())
}

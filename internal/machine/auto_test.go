package machine

import (
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
)

// autoRig is an auto controller on a fake clock, reading the load outside Djinn from a recorder fed fake Hourglass
// points every 5 seconds, as djinn up's sampler does, and beating every AutoBeat.
type autoRig struct {
	t       *testing.T
	now     time.Time
	rec     *SeriesRecorder
	auto    *AutoController
	changes []djinnv1.LoadNotch
	steps   []time.Duration // when each change came, from the start
	start   time.Time
}

const testMemory = 32 << 30

func newAutoRig(t *testing.T, initial, chosen djinnv1.LoadNotch) *autoRig {
	t.Helper()
	r := &autoRig{t: t, now: time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)}
	r.start = r.now
	r.rec = NewSeriesRecorder(nil, nil)
	r.rec.cpuRead, r.rec.memoryTotal = true, testMemory
	r.auto = NewAutoController(chosen,
		WithAutoClock(func() time.Time { return r.now }),
		WithAutoOutside(r.rec.Outside),
		WithAutoMarks(AutoMarks{CPUHigh: 30, CPULow: 15, MemoryHigh: 70, MemoryLow: 60}),
		WithAutoOnChange(func(n djinnv1.LoadNotch) {
			r.changes = append(r.changes, n)
			r.steps = append(r.steps, r.now.Sub(r.start))
		}),
	)
	r.auto.SetNotch(djinnv1.LoadNotch_LOAD_NOTCH_AUTO)
	r.auto.mu.Lock()
	r.auto.effective = initial
	r.auto.mu.Unlock()
	return r
}

// run feeds d of points where the machine's CPU is machine percent and Djinn's line djinn percent, its memory 10 %
// outside Djinn, beating every AutoBeat.
func (r *autoRig) run(d time.Duration, machine, djinn float64) {
	r.t.Helper()
	for end := r.now.Add(d); r.now.Before(end); {
		r.now = r.now.Add(5 * time.Second)
		r.rec.AddPoint(&machinev1.MachinePoint{
			Time:             timestamppb.New(r.now),
			CpuPercent:       machine,
			DjinnCpuPercent:  djinn,
			MemoryBytes:      testMemory / 2,
			DjinnMemoryBytes: testMemory/2 - testMemory/10,
		})
		if r.now.Sub(r.start)%AutoBeat == 0 {
			r.auto.Tick()
		}
	}
}

func (r *autoRig) want(n djinnv1.LoadNotch) {
	r.t.Helper()
	if got := r.auto.Effective(); got != n {
		r.t.Fatalf("at %v: notch %v, want %v (changes %v at %v)", r.now.Sub(r.start), got, n, r.changes, r.steps)
	}
}

// The developer using only Djinn, its workers at full tilt and the window watched: no load outside Djinn, the notch
// climbs one step every 10 minutes up to the chosen one, never down (W253: it dropped to Minimal).
func TestAutoDjinnOnlyRaises(t *testing.T) {
	r := newAutoRig(t, djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM, djinnv1.LoadNotch_LOAD_NOTCH_MAX)
	r.run(10*time.Minute, 99, 96) // below the low mark from the first beat, at 20 s
	r.want(djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM)
	r.run(20*time.Second, 99, 96)
	r.want(djinnv1.LoadNotch_LOAD_NOTCH_HIGH)
	r.run(10*time.Minute, 100, 97)
	r.want(djinnv1.LoadNotch_LOAD_NOTCH_MAX)
	r.run(30*time.Minute, 100, 97)
	r.want(djinnv1.LoadNotch_LOAD_NOTCH_MAX)
	if len(r.changes) != 2 {
		t.Fatalf("changes %v, want High then Max", r.changes)
	}
	reason := r.auto.Reason()
	if reason.GetStep() != djinnv1.LoadAutoStep_LOAD_AUTO_STEP_CEILING || reason.GetNotch() != djinnv1.LoadNotch_LOAD_NOTCH_MAX ||
		reason.GetOutsidePercent() != 3 || reason.GetMemory() || reason.GetWindowSeconds() != 1800 {
		t.Errorf("reason %v, want outside load 3 %% for 30 min, up to Max", reason)
	}
}

// Djinn-only activity at the chosen notch keeps it there.
func TestAutoDjinnOnlyKeeps(t *testing.T) {
	r := newAutoRig(t, djinnv1.LoadNotch_LOAD_NOTCH_HIGH, djinnv1.LoadNotch_LOAD_NOTCH_HIGH)
	r.run(time.Hour, 100, 95)
	r.want(djinnv1.LoadNotch_LOAD_NOTCH_HIGH)
	if len(r.changes) != 0 {
		t.Fatalf("changes %v, want none", r.changes)
	}
}

// A build outside Djinn that lasts lowers the notch one step once its minute average stayed 2 minutes above the high
// mark, then one step every 2 minutes down to Minimal, and no lower.
func TestAutoOutsideBuildLowers(t *testing.T) {
	r := newAutoRig(t, djinnv1.LoadNotch_LOAD_NOTCH_MAX, djinnv1.LoadNotch_LOAD_NOTCH_MAX)
	r.run(10*time.Minute, 40, 35)
	r.want(djinnv1.LoadNotch_LOAD_NOTCH_MAX)
	r.run(time.Minute, 95, 30) // 65 % outside: the minute average passes 30 % at the second beat, 40 s in
	r.want(djinnv1.LoadNotch_LOAD_NOTCH_MAX)
	reason := r.auto.Reason()
	if reason.GetStep() != djinnv1.LoadAutoStep_LOAD_AUTO_STEP_LOWER || reason.GetNotch() != djinnv1.LoadNotch_LOAD_NOTCH_HIGH ||
		reason.GetInSeconds() != 100 || reason.GetWindowSeconds() != 20 || reason.GetOutsidePercent() != 65 {
		t.Errorf("reason %v, want outside 65 %% for 20 s, down to High in 100 s", reason)
	}
	r.run(time.Minute+20*time.Second, 95, 30)
	r.want(djinnv1.LoadNotch_LOAD_NOTCH_MAX)
	r.run(20*time.Second, 95, 30)
	r.want(djinnv1.LoadNotch_LOAD_NOTCH_HIGH)
	r.run(2*time.Minute, 95, 30)
	r.want(djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM)
	r.run(2*time.Minute, 95, 30)
	r.want(djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL)
	r.run(10*time.Minute, 95, 30)
	r.want(djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL)
	want := []time.Duration{12*time.Minute + 40*time.Second, 14*time.Minute + 40*time.Second, 16*time.Minute + 40*time.Second}
	if len(r.steps) != len(want) {
		t.Fatalf("steps at %v, want %v", r.steps, want)
	}
	for i, at := range want {
		if r.steps[i] != at {
			t.Errorf("step %d at %v, want %v", i, r.steps[i], at)
		}
	}
	if s := r.auto.Reason().GetStep(); s != djinnv1.LoadAutoStep_LOAD_AUTO_STEP_FLOOR {
		t.Errorf("step %v at Minimal, want FLOOR", s)
	}
}

// A short spike outside Djinn, every core for 30 seconds, moves nothing.
func TestAutoShortSpike(t *testing.T) {
	r := newAutoRig(t, djinnv1.LoadNotch_LOAD_NOTCH_HIGH, djinnv1.LoadNotch_LOAD_NOTCH_HIGH)
	r.run(10*time.Minute, 50, 45)
	r.run(30*time.Second, 100, 0)
	r.run(10*time.Minute, 50, 45)
	r.want(djinnv1.LoadNotch_LOAD_NOTCH_HIGH)
	if len(r.changes) != 0 {
		t.Fatalf("changes %v, want none", r.changes)
	}
}

// Once the outside load is gone, the notch comes back slowly: one step after 10 minutes below the low mark, the next
// 10 minutes later; a load between the marks holds it, and a spike starts the wait again.
func TestAutoSlowRecovery(t *testing.T) {
	r := newAutoRig(t, djinnv1.LoadNotch_LOAD_NOTCH_MAX, djinnv1.LoadNotch_LOAD_NOTCH_MAX)
	r.run(7*time.Minute, 90, 10)
	r.want(djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL)
	r.run(10*time.Minute, 30, 10) // 20 % outside: between the marks
	r.want(djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL)
	if s := r.auto.Reason().GetStep(); s != djinnv1.LoadAutoStep_LOAD_AUTO_STEP_HOLD {
		t.Errorf("step %v between the marks, want HOLD", s)
	}
	r.run(5*time.Minute, 50, 45) // 5 % outside: below the low mark from the beat at 40 s
	r.run(30*time.Second, 80, 45)
	r.run(10*time.Minute, 50, 45) // the spike held the minute average above 15 % for a minute after it
	r.want(djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL)
	reason := r.auto.Reason()
	wait := time.Duration(reason.GetInSeconds()) * time.Second
	if reason.GetStep() != djinnv1.LoadAutoStep_LOAD_AUTO_STEP_RAISE || reason.GetNotch() != djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM ||
		wait <= 0 || wait > 2*time.Minute {
		t.Errorf("reason %v, want up to Medium within 2 min", reason)
	}
	r.run(wait-AutoBeat, 50, 45)
	r.want(djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL)
	r.run(AutoBeat, 50, 45)
	r.want(djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM)
	r.run(9*time.Minute+40*time.Second, 50, 45)
	r.want(djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM)
	r.run(20*time.Second, 50, 45)
	r.want(djinnv1.LoadNotch_LOAD_NOTCH_HIGH)
}

// Memory used outside Djinn past its high mark lowers the notch as the CPU does, and the reason names the memory.
func TestAutoOutsideMemory(t *testing.T) {
	r := newAutoRig(t, djinnv1.LoadNotch_LOAD_NOTCH_HIGH, djinnv1.LoadNotch_LOAD_NOTCH_MAX)
	for range 12 * 3 { // 3 minutes, the memory outside Djinn at 75 %
		r.now = r.now.Add(5 * time.Second)
		r.rec.AddPoint(&machinev1.MachinePoint{
			Time: timestamppb.New(r.now), CpuPercent: 20, DjinnCpuPercent: 18,
			MemoryBytes: testMemory * 9 / 10, DjinnMemoryBytes: testMemory * 15 / 100,
		})
		if r.now.Sub(r.start)%AutoBeat == 0 {
			r.auto.Tick()
		}
	}
	r.want(djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM)
	reason := r.auto.Reason()
	if !reason.GetMemory() || reason.GetOutsidePercent() != 75 {
		t.Errorf("reason %v, want outside memory 75 %%", reason)
	}
}

// Nothing measured (no Hourglass, or a system where it reads neither): auto holds its notch and says so.
func TestAutoNotMeasuredHolds(t *testing.T) {
	now := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	c := NewAutoController(djinnv1.LoadNotch_LOAD_NOTCH_AUTO, WithAutoClock(func() time.Time { return now }))
	for range 90 {
		now = now.Add(AutoBeat)
		c.Tick()
	}
	if c.Effective() != djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM {
		t.Errorf("notch %v, want Medium held", c.Effective())
	}
	if r := c.Reason(); r.GetStep() != djinnv1.LoadAutoStep_LOAD_AUTO_STEP_HOLD || r.GetWindowSeconds() != 0 {
		t.Errorf("reason %v, want held, nothing measured", r)
	}
}

// A manual notch leaves auto mode and becomes its ceiling; back in auto, the notch in effect never passes it.
func TestAutoManualExitAndReentry(t *testing.T) {
	c := NewAutoController(djinnv1.LoadNotch_LOAD_NOTCH_AUTO)
	if !c.IsAuto() || c.Chosen() != djinnv1.LoadNotch_LOAD_NOTCH_MAX || c.Effective() != djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM {
		t.Fatalf("auto %v chosen %v effective %v, want auto from Medium up to Max", c.IsAuto(), c.Chosen(), c.Effective())
	}
	if c.Reason() == nil {
		t.Error("no reason in auto mode")
	}
	eff, changed := c.SetNotch(djinnv1.LoadNotch_LOAD_NOTCH_HIGH)
	if c.IsAuto() || eff != djinnv1.LoadNotch_LOAD_NOTCH_HIGH || !changed || c.Chosen() != djinnv1.LoadNotch_LOAD_NOTCH_HIGH {
		t.Fatalf("manual High: auto %v effective %v changed %v chosen %v", c.IsAuto(), eff, changed, c.Chosen())
	}
	if c.Reason() != nil {
		t.Error("a reason outside auto mode")
	}
	c.SetNotch(djinnv1.LoadNotch_LOAD_NOTCH_MAX)
	c.SetNotch(djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL)
	if eff, _ := c.SetNotch(djinnv1.LoadNotch_LOAD_NOTCH_AUTO); eff != djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL {
		t.Errorf("auto from Minimal chosen: %v, want Minimal", eff)
	}
	if eff, _ := c.SetNotch(djinnv1.LoadNotch_LOAD_NOTCH_LIGHT); eff != djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM {
		t.Errorf("light: %v, want Medium", eff)
	}
}

// BenchmarkAutoBeat is the auto mode's cost: one beat over a full Hourglass. W253 measured 0.6 µs a beat, one every
// 20 s: three hundred-millionths of a core.
func BenchmarkAutoBeat(b *testing.B) {
	now := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	rec := NewSeriesRecorder(nil, nil)
	rec.cpuRead, rec.memoryTotal = true, testMemory
	for i := range maxMachineSamples {
		rec.AddPoint(&machinev1.MachinePoint{
			Time:       timestamppb.New(now.Add(time.Duration(i) * 5 * time.Second)),
			CpuPercent: 90, DjinnCpuPercent: 80, MemoryBytes: testMemory / 2, DjinnMemoryBytes: testMemory / 4,
		})
	}
	now = now.Add(maxMachineSamples * 5 * time.Second)
	c := NewAutoController(djinnv1.LoadNotch_LOAD_NOTCH_AUTO, WithAutoClock(func() time.Time { return now }),
		WithAutoOutside(rec.Outside))
	b.ReportAllocs()
	for b.Loop() {
		c.Tick()
	}
}

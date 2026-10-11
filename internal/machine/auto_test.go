package machine

import (
	"testing"
	"time"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
)

func TestAutoControllerHysteresisAndProgression(t *testing.T) {
	fakeNow := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return fakeNow }
	var working bool
	signal := func() bool { return working }

	var changedNotches []djinnv1.LoadNotch
	onChange := func(n djinnv1.LoadNotch) {
		changedNotches = append(changedNotches, n)
	}

	ctrl := NewAutoController(
		djinnv1.LoadNotch_LOAD_NOTCH_AUTO,
		WithAutoClock(clock),
		WithAutoSignal(signal),
		WithAutoStepDuration(1*time.Minute),
		WithAutoOnChange(onChange),
	)

	if !ctrl.IsAuto() {
		t.Fatal("expected AutoController to be in auto mode")
	}

	// Initial effective notch when starting at rest is Minimal or Medium (defaulted to Minimal on rest if fresh or initialized).
	// Let's set it to Minimal to test the full progression to Max.
	working = false
	// Start with Minimal
	ctrl.effective = djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL
	ctrl.lastChange = fakeNow

	// 1. Tick before 1 minute elapsed: no change (hysteresis)
	fakeNow = fakeNow.Add(30 * time.Second)
	changed, notch := ctrl.Tick()
	if changed {
		t.Fatalf("Tick before 1m: expected no change, got changed=true, notch=%v", notch)
	}
	if notch != djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL {
		t.Fatalf("Tick before 1m: expected notch MINIMAL, got %v", notch)
	}

	// 2. Slow climb at rest: 1 notch per minute
	steps := []djinnv1.LoadNotch{
		djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM,
		djinnv1.LoadNotch_LOAD_NOTCH_HIGH,
		djinnv1.LoadNotch_LOAD_NOTCH_MAX,
	}

	for _, expectedNotch := range steps {
		fakeNow = fakeNow.Add(1 * time.Minute)
		changed, notch = ctrl.Tick()
		if !changed {
			t.Fatalf("Tick at rest: expected change to %v, got changed=false", expectedNotch)
		}
		if notch != expectedNotch {
			t.Fatalf("Tick at rest: got %v, want %v", notch, expectedNotch)
		}
	}

	// 3. Stays at Max when machine remains at rest
	fakeNow = fakeNow.Add(2 * time.Minute)
	changed, notch = ctrl.Tick()
	if changed {
		t.Fatalf("Tick at Max at rest: expected changed=false, got true (notch=%v)", notch)
	}
	if notch != djinnv1.LoadNotch_LOAD_NOTCH_MAX {
		t.Fatalf("Tick at Max at rest: got %v, want MAX", notch)
	}

	// 4. Activity: developer starts working
	working = true

	// If 1 minute hasn't elapsed since last change: hysteresis prevents immediate drop
	// Advance clock by only 20 seconds from lastChange
	ctrl.lastChange = fakeNow
	fakeNow = fakeNow.Add(20 * time.Second)
	changed, notch = ctrl.Tick()
	if changed {
		t.Fatalf("Tick under activity before 1m hysteresis: expected no change, got notch=%v", notch)
	}

	// Once 1 minute elapses, fast drop directly to Minimal
	fakeNow = fakeNow.Add(45 * time.Second) // total 65s > 1m
	changed, notch = ctrl.Tick()
	if !changed {
		t.Fatal("Tick under activity after 1m: expected changed=true, got false")
	}
	if notch != djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL {
		t.Fatalf("Tick under activity: got %v, want MINIMAL (fast drop)", notch)
	}

	// 5. Subsequent ticks while working stay at Minimal
	fakeNow = fakeNow.Add(2 * time.Minute)
	changed, notch = ctrl.Tick()
	if changed {
		t.Fatalf("Tick while working at Minimal: expected changed=false, got true (notch=%v)", notch)
	}
	if notch != djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL {
		t.Fatalf("Tick while working: got %v, want MINIMAL", notch)
	}

	// 6. Verify onChange callbacks were called for all transitions
	expectedChanged := []djinnv1.LoadNotch{
		djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM,
		djinnv1.LoadNotch_LOAD_NOTCH_HIGH,
		djinnv1.LoadNotch_LOAD_NOTCH_MAX,
		djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL,
	}
	if len(changedNotches) != len(expectedChanged) {
		t.Fatalf("onChange calls count: got %d, want %d (%v)", len(changedNotches), len(expectedChanged), changedNotches)
	}
	for i, want := range expectedChanged {
		if changedNotches[i] != want {
			t.Errorf("onChange[%d]: got %v, want %v", i, changedNotches[i], want)
		}
	}
}

// TestAutoNeverAboveChosenNotch verifies that Auto mode never climbs above the notch
// the developer chose.
func TestAutoNeverAboveChosenNotch(t *testing.T) {
	for _, chosen := range ManualNotches {
		t.Run(NotchName(chosen), func(t *testing.T) {
			fakeNow := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
			clock := func() time.Time { return fakeNow }
			signal := func() bool { return false } // machine always at rest

			// Developer chose a notch, then activated AUTO
			ctrl := NewAutoController(
				chosen,
				WithAutoClock(clock),
				WithAutoSignal(signal),
				WithAutoStepDuration(1*time.Minute),
			)
			ctrl.SetNotch(djinnv1.LoadNotch_LOAD_NOTCH_AUTO)

			// Start at Minimal
			ctrl.effective = djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL
			ctrl.lastChange = fakeNow

			// Tick repeatedly (up to 10 times)
			for i := 0; i < 10; i++ {
				fakeNow = fakeNow.Add(1 * time.Minute)
				ctrl.Tick()
				if NotchIndex(ctrl.Effective()) > NotchIndex(chosen) {
					t.Fatalf("Auto climbed to %v, which is above chosen notch %v", ctrl.Effective(), chosen)
				}
			}

			// When at rest, it must eventually have reached the chosen notch
			if ctrl.Effective() != chosen {
				t.Fatalf("Auto did not reach chosen notch: got %v, want %v", ctrl.Effective(), chosen)
			}
		})
	}
}

func TestAutoControllerManualExitAndReentry(t *testing.T) {
	fakeNow := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return fakeNow }
	var working bool
	signal := func() bool { return working }

	ctrl := NewAutoController(
		djinnv1.LoadNotch_LOAD_NOTCH_AUTO,
		WithAutoClock(clock),
		WithAutoSignal(signal),
		WithAutoStepDuration(1*time.Minute),
	)

	if !ctrl.IsAuto() {
		t.Fatal("expected auto mode")
	}

	// Manual notch selection exits Auto mode
	effective, changed := ctrl.SetNotch(djinnv1.LoadNotch_LOAD_NOTCH_HIGH)
	if !changed || effective != djinnv1.LoadNotch_LOAD_NOTCH_HIGH {
		t.Fatalf("SetNotch HIGH: effective=%v changed=%v", effective, changed)
	}
	if ctrl.IsAuto() {
		t.Fatal("expected auto mode to be disabled after manual notch selection")
	}
	if ctrl.Effective() != djinnv1.LoadNotch_LOAD_NOTCH_HIGH {
		t.Fatalf("effective notch: got %v, want HIGH", ctrl.Effective())
	}

	// Ticks while in manual mode do not change notch
	fakeNow = fakeNow.Add(5 * time.Minute)
	changed, notch := ctrl.Tick()
	if changed || notch != djinnv1.LoadNotch_LOAD_NOTCH_HIGH {
		t.Fatalf("Tick in manual mode: changed=%v, notch=%v, want HIGH", changed, notch)
	}

	// Setting AUTO enters auto mode
	working = true // developer working -> should be Minimal
	effective, _ = ctrl.SetNotch(djinnv1.LoadNotch_LOAD_NOTCH_AUTO)
	if !ctrl.IsAuto() {
		t.Fatal("expected auto mode to be enabled after SetNotch AUTO")
	}
	if effective != djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL {
		t.Fatalf("effective notch under work on AUTO entry: got %v, want MINIMAL", effective)
	}
}

func TestAutoIdleSourceActiveLowers(t *testing.T) {
	fakeNow := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return fakeNow }
	signal := func() bool { return false } // no host load

	var idleDuration time.Duration
	idleOk := true
	idleSource := func() (time.Duration, bool) {
		return idleDuration, idleOk
	}

	ctrl := NewAutoController(
		djinnv1.LoadNotch_LOAD_NOTCH_AUTO,
		WithAutoClock(clock),
		WithAutoSignal(signal),
		WithAutoIdleSource(idleSource),
		WithAutoStepDuration(1*time.Minute),
	)

	// Start at High notch
	ctrl.effective = djinnv1.LoadNotch_LOAD_NOTCH_HIGH
	ctrl.lastChange = fakeNow

	// Developer is active at keyboard or mouse (idle = 5s < 30s)
	idleDuration = 5 * time.Second

	// Tick before 1m: hysteresis prevents change
	fakeNow = fakeNow.Add(30 * time.Second)
	changed, notch := ctrl.Tick()
	if changed {
		t.Fatalf("Tick before hysteresis: expected no change, got %v", notch)
	}

	// Once hysteresis (1m) elapses, active input lowers directly to Minimal
	fakeNow = fakeNow.Add(35 * time.Second) // total 65s > 1m
	changed, notch = ctrl.Tick()
	if !changed {
		t.Fatal("Tick when active: expected changed=true")
	}
	if notch != djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL {
		t.Fatalf("Tick when active: got %v, want MINIMAL", notch)
	}

	// Subsequent ticks while still active stay at Minimal
	fakeNow = fakeNow.Add(2 * time.Minute)
	changed, notch = ctrl.Tick()
	if changed || notch != djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL {
		t.Fatalf("subsequent tick while active: changed=%v notch=%v", changed, notch)
	}
}

func TestAutoIdleSourceIdleRaises(t *testing.T) {
	fakeNow := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return fakeNow }
	signal := func() bool { return false }

	var idleDuration time.Duration
	idleOk := true
	idleSource := func() (time.Duration, bool) {
		return idleDuration, idleOk
	}

	ctrl := NewAutoController(
		djinnv1.LoadNotch_LOAD_NOTCH_AUTO,
		WithAutoClock(clock),
		WithAutoSignal(signal),
		WithAutoIdleSource(idleSource),
		WithAutoStepDuration(1*time.Minute),
		WithAutoChosen(djinnv1.LoadNotch_LOAD_NOTCH_MAX),
	)

	// Start at Minimal
	ctrl.effective = djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL
	ctrl.lastChange = fakeNow

	// Developer is idle for 5 minutes (>= 3m idleThreshold) with little host load
	idleDuration = 5 * time.Minute

	// Climbs step by step (+1 notch per step duration): Medium -> High -> Max
	expected := []djinnv1.LoadNotch{
		djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM,
		djinnv1.LoadNotch_LOAD_NOTCH_HIGH,
		djinnv1.LoadNotch_LOAD_NOTCH_MAX,
	}

	for _, want := range expected {
		fakeNow = fakeNow.Add(1 * time.Minute)
		changed, notch := ctrl.Tick()
		if !changed {
			t.Fatalf("Tick while idle: expected change to %v", want)
		}
		if notch != want {
			t.Fatalf("Tick while idle: got %v, want %v", notch, want)
		}
	}

	// At Max, does not climb higher
	fakeNow = fakeNow.Add(2 * time.Minute)
	changed, notch := ctrl.Tick()
	if changed || notch != djinnv1.LoadNotch_LOAD_NOTCH_MAX {
		t.Fatalf("Tick at ceiling: changed=%v notch=%v", changed, notch)
	}
}

func TestAutoIdleSourceHoldBetweenThresholds(t *testing.T) {
	fakeNow := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return fakeNow }
	signal := func() bool { return false }

	var idleDuration time.Duration
	idleOk := true
	idleSource := func() (time.Duration, bool) {
		return idleDuration, idleOk
	}

	ctrl := NewAutoController(
		djinnv1.LoadNotch_LOAD_NOTCH_AUTO,
		WithAutoClock(clock),
		WithAutoSignal(signal),
		WithAutoIdleSource(idleSource),
		WithAutoStepDuration(1*time.Minute),
	)

	// Start at Medium
	ctrl.effective = djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM
	ctrl.lastChange = fakeNow

	// Idle time is 90 seconds (between 30s activeThreshold and 3m idleThreshold)
	idleDuration = 90 * time.Second

	// Advance clock past hysteresis
	fakeNow = fakeNow.Add(2 * time.Minute)
	changed, notch := ctrl.Tick()
	if changed {
		t.Fatalf("expected hold between thresholds, got changed=true, notch=%v", notch)
	}
	if notch != djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM {
		t.Fatalf("notch should remain MEDIUM during hold, got %v", notch)
	}
}

func TestAutoIdleSourceIdleWithHostLoadActive(t *testing.T) {
	fakeNow := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return fakeNow }
	var hostBusy bool
	signal := func() bool { return hostBusy }

	var idleDuration time.Duration
	idleOk := true
	idleSource := func() (time.Duration, bool) {
		return idleDuration, idleOk
	}

	ctrl := NewAutoController(
		djinnv1.LoadNotch_LOAD_NOTCH_AUTO,
		WithAutoClock(clock),
		WithAutoSignal(signal),
		WithAutoIdleSource(idleSource),
		WithAutoStepDuration(1*time.Minute),
	)

	// Start at High
	ctrl.effective = djinnv1.LoadNotch_LOAD_NOTCH_HIGH
	ctrl.lastChange = fakeNow

	// Desktop is idle (10 minutes), but another host program is under heavy load
	idleDuration = 10 * time.Minute
	hostBusy = true

	fakeNow = fakeNow.Add(1 * time.Minute)
	changed, notch := ctrl.Tick()
	if !changed {
		t.Fatal("expected drop when host is busy despite desktop idle")
	}
	if notch != djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL {
		t.Fatalf("got %v, want MINIMAL", notch)
	}
}

func TestAutoIdleSourceUnavailableFallsBackToLoad(t *testing.T) {
	fakeNow := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return fakeNow }
	var hostBusy bool
	signal := func() bool { return hostBusy }

	// Idle source unavailable (e.g. headless, SSH, unsupported session)
	idleSource := func() (time.Duration, bool) {
		return 0, false
	}

	ctrl := NewAutoController(
		djinnv1.LoadNotch_LOAD_NOTCH_AUTO,
		WithAutoClock(clock),
		WithAutoSignal(signal),
		WithAutoIdleSource(idleSource),
		WithAutoStepDuration(1*time.Minute),
	)

	// Start at High
	ctrl.effective = djinnv1.LoadNotch_LOAD_NOTCH_HIGH
	ctrl.lastChange = fakeNow

	// 1. Host load triggers drop to Minimal
	hostBusy = true
	fakeNow = fakeNow.Add(1 * time.Minute)
	changed, notch := ctrl.Tick()
	if !changed || notch != djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL {
		t.Fatalf("fallback drop on load: changed=%v notch=%v", changed, notch)
	}

	// 2. Machine at rest: climbs step by step
	hostBusy = false
	fakeNow = fakeNow.Add(1 * time.Minute)
	changed, notch = ctrl.Tick()
	if !changed || notch != djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM {
		t.Fatalf("fallback climb on rest: changed=%v notch=%v", changed, notch)
	}
}

func TestAutoSetNotchWithIdleSource(t *testing.T) {
	fakeNow := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return fakeNow }
	signal := func() bool { return false }

	var idleDuration time.Duration
	idleSource := func() (time.Duration, bool) {
		return idleDuration, true
	}

	// 1. Active at keyboard/mouse when entering AUTO: drops to Minimal immediately
	idleDuration = 10 * time.Second
	ctrl := NewAutoController(
		djinnv1.LoadNotch_LOAD_NOTCH_HIGH,
		WithAutoClock(clock),
		WithAutoSignal(signal),
		WithAutoIdleSource(idleSource),
	)
	effective, changed := ctrl.SetNotch(djinnv1.LoadNotch_LOAD_NOTCH_AUTO)
	if !changed || effective != djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL {
		t.Fatalf("SetNotch AUTO while active: effective=%v changed=%v, want MINIMAL", effective, changed)
	}

	// 2. Idle when entering AUTO: preserves chosen ceiling if valid
	idleDuration = 5 * time.Minute
	ctrl2 := NewAutoController(
		djinnv1.LoadNotch_LOAD_NOTCH_HIGH,
		WithAutoClock(clock),
		WithAutoSignal(signal),
		WithAutoIdleSource(idleSource),
	)
	effective2, _ := ctrl2.SetNotch(djinnv1.LoadNotch_LOAD_NOTCH_AUTO)
	if effective2 != djinnv1.LoadNotch_LOAD_NOTCH_HIGH {
		t.Fatalf("SetNotch AUTO while idle: effective=%v, want HIGH", effective2)
	}
}

func TestAutoCustomThresholds(t *testing.T) {
	fakeNow := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return fakeNow }
	signal := func() bool { return false }

	var idleDuration time.Duration
	idleSource := func() (time.Duration, bool) {
		return idleDuration, true
	}

	// Custom thresholds: active <= 10s, idle >= 20s
	ctrl := NewAutoController(
		djinnv1.LoadNotch_LOAD_NOTCH_AUTO,
		WithAutoClock(clock),
		WithAutoSignal(signal),
		WithAutoIdleSource(idleSource),
		WithAutoActiveThreshold(10*time.Second),
		WithAutoIdleThreshold(20*time.Second),
		WithAutoStepDuration(30*time.Second),
	)
	ctrl.effective = djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM
	ctrl.lastChange = fakeNow

	// 15 seconds is between 10s and 20s -> hold
	idleDuration = 15 * time.Second
	fakeNow = fakeNow.Add(35 * time.Second)
	changed, notch := ctrl.Tick()
	if changed || notch != djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM {
		t.Fatalf("hold: changed=%v notch=%v", changed, notch)
	}

	// 25 seconds is >= 20s -> rest (climb)
	idleDuration = 25 * time.Second
	fakeNow = fakeNow.Add(35 * time.Second)
	changed, notch = ctrl.Tick()
	if !changed || notch != djinnv1.LoadNotch_LOAD_NOTCH_HIGH {
		t.Fatalf("climb: changed=%v notch=%v", changed, notch)
	}

	// 5 seconds is < 10s -> active (drop to Minimal)
	idleDuration = 5 * time.Second
	fakeNow = fakeNow.Add(35 * time.Second)
	changed, notch = ctrl.Tick()
	if !changed || notch != djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL {
		t.Fatalf("drop: changed=%v notch=%v", changed, notch)
	}
}

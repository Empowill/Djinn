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

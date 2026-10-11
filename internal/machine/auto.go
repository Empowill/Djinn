package machine

import (
	"sync"
	"time"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
)

// ActivitySignal returns true if the developer is actively working on the machine,
// or false if the machine is at rest.
type ActivitySignal func() bool

// SystemActivitySignal returns an ActivitySignal based on host CPU load and memory pressure.
// Outside Djinn processes, CPU and memory load is the only reliable, unprivileged, cross-platform
// signal across Linux (Wayland, X11, headless, SSH, containers), macOS, and Windows.
func SystemActivitySignal(m *Monitor) ActivitySignal {
	return func() bool {
		if m == nil {
			return false
		}
		s := m.Snapshot()
		// PSI CPU pressure (Linux): tasks waited for CPU > 15% in last 10s.
		if s.CPU != nil && s.CPU.Some > 15.0 {
			return true
		}
		// Load average per core: load > 1.0 per core indicates active host processes.
		if s.LoadKnown && s.Cores > 0 && s.Load1/float64(s.Cores) > 1.0 {
			return true
		}
		// macOS memory pressure: warning (2) or critical (4).
		if s.MemoryLevel >= 2 {
			return true
		}
		return false
	}
}

// AutoOption configures an AutoController.
type AutoOption func(*AutoController)

// WithAutoClock sets the clock function (used for testing with a fake clock).
func WithAutoClock(clock func() time.Time) AutoOption {
	return func(c *AutoController) { c.clock = clock }
}

// WithAutoSignal sets the activity signal function (used for testing with a fake signal).
func WithAutoSignal(signal ActivitySignal) AutoOption {
	return func(c *AutoController) { c.signal = signal }
}

// WithAutoIdleSource sets the desktop session idle time source.
func WithAutoIdleSource(source IdleSource) AutoOption {
	return func(c *AutoController) { c.idleSource = source }
}

// WithAutoActiveThreshold sets the maximum idle duration considered active keyboard or mouse input.
func WithAutoActiveThreshold(d time.Duration) AutoOption {
	return func(c *AutoController) { c.activeThreshold = d }
}

// WithAutoIdleThreshold sets the minimum idle duration before the user is considered at rest.
func WithAutoIdleThreshold(d time.Duration) AutoOption {
	return func(c *AutoController) { c.idleThreshold = d }
}

// WithAutoStepDuration sets the hysteresis minimum interval between notch changes.
func WithAutoStepDuration(d time.Duration) AutoOption {
	return func(c *AutoController) { c.stepDuration = d }
}

// WithAutoOnChange sets a callback invoked whenever the effective notch changes.
func WithAutoOnChange(onChange func(djinnv1.LoadNotch)) AutoOption {
	return func(c *AutoController) { c.onChange = onChange }
}

// WithAutoChosen sets the ceiling notch for auto mode.
func WithAutoChosen(notch djinnv1.LoadNotch) AutoOption {
	return func(c *AutoController) {
		if notch == djinnv1.LoadNotch_LOAD_NOTCH_LIGHT {
			notch = djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM
		}
		c.chosen = notch
	}
}

type activityState int

const (
	activityWorking activityState = iota // active keyboard/mouse or heavy host load -> drop to Minimal
	activityRest                         // idle for minutes and little host load -> climb (+1 notch)
	activityHold                         // between active and idle thresholds with little load -> hold current notch
)

// AutoController manages the operating load notch in auto mode.
// In auto mode, it dynamically moves between Minimal and the notch the developer chose,
// based on developer activity: Minimal when working, slowly climbing towards the chosen notch at rest,
// with hysteresis (no change more than once per step duration) to prevent oscillation.
//
// Activity combines the desktop session's idle time (keyboard and mouse inactivity) with the
// load of programs other than Djinn (CPU wait/load and memory pressure):
// - Active at keyboard or mouse (< ActiveThreshold): drops notch directly to Minimal.
// - Heavy host load from other programs: drops notch directly to Minimal.
// - Idle for a few minutes (>= IdleThreshold) with little load: climbs (+1 notch per step duration).
// - Where no desktop idle source is available: falls back to host load alone.
//
// In Auto mode, lowering the notch lowers CPU priority and stops starting new workers,
// never pausing or terminating running workers (Decision Q124 (C)).
type AutoController struct {
	mu              sync.Mutex
	isAuto          bool
	chosen          djinnv1.LoadNotch
	effective       djinnv1.LoadNotch
	lastChange      time.Time
	stepDuration    time.Duration
	activeThreshold time.Duration
	idleThreshold   time.Duration
	clock           func() time.Time
	signal          ActivitySignal
	idleSource      IdleSource
	onChange        func(djinnv1.LoadNotch)
}

// NewAutoController creates an AutoController with the given initial notch and options.
func NewAutoController(initial djinnv1.LoadNotch, opts ...AutoOption) *AutoController {
	if initial == djinnv1.LoadNotch_LOAD_NOTCH_LIGHT {
		initial = djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM
	}
	c := &AutoController{
		stepDuration:    DefaultAutoHysteresis,
		activeThreshold: DefaultActiveThreshold,
		idleThreshold:   DefaultIdleThreshold,
		clock:           time.Now,
		signal:          func() bool { return false },
		chosen:          djinnv1.LoadNotch_LOAD_NOTCH_MAX,
		effective:       djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM,
	}
	for _, opt := range opts {
		opt(c)
	}
	c.SetNotch(initial)
	return c
}

// IsAuto reports whether auto mode is active.
func (c *AutoController) IsAuto() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.isAuto
}

// Chosen returns the ceiling notch for auto mode.
func (c *AutoController) Chosen() djinnv1.LoadNotch {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.chosen
}

// Effective returns the operating load notch currently in effect.
func (c *AutoController) Effective() djinnv1.LoadNotch {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.effective
}

// LastChange returns the time of the last notch change.
func (c *AutoController) LastChange() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastChange
}

func (c *AutoController) evalActivity() activityState {
	if c.idleSource != nil {
		if idle, ok := c.idleSource(); ok {
			// Person typing or moving the mouse lowers the notch.
			if idle < c.activeThreshold {
				return activityWorking
			}
			// Load of programs other than Djinn.
			if c.signal != nil && c.signal() {
				return activityWorking
			}
			// Idle for a few minutes with little load raises it again.
			if idle >= c.idleThreshold {
				return activityRest
			}
			// Between active and idle thresholds with little load: neither drop nor raise.
			return activityHold
		}
	}

	// No desktop idle source available: host load alone, as today.
	if c.signal != nil && c.signal() {
		return activityWorking
	}
	return activityRest
}

// SetNotch updates the notch setting. If notch is LOAD_NOTCH_AUTO, auto mode is enabled;
// if notch is a manual notch (Minimal to Overclock), auto mode is disabled and chosen is updated.
func (c *AutoController) SetNotch(notch djinnv1.LoadNotch) (effective djinnv1.LoadNotch, changed bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if notch == djinnv1.LoadNotch_LOAD_NOTCH_LIGHT {
		notch = djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM
	}

	now := c.clock()
	if notch == djinnv1.LoadNotch_LOAD_NOTCH_AUTO {
		c.isAuto = true
		if c.chosen == djinnv1.LoadNotch_LOAD_NOTCH_UNSPECIFIED {
			c.chosen = djinnv1.LoadNotch_LOAD_NOTCH_MAX
		}
		activity := c.evalActivity()
		oldEffective := c.effective
		if activity == activityWorking {
			c.effective = djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL
		} else {
			if NotchIndex(c.effective) < 0 {
				c.effective = djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL
			} else if NotchIndex(c.effective) > NotchIndex(c.chosen) {
				c.effective = c.chosen
			}
		}
		c.lastChange = now
		if c.effective != oldEffective {
			changed = true
		}
		return c.effective, changed
	}

	if NotchIndex(notch) >= 0 {
		c.isAuto = false
		c.chosen = notch
		oldEffective := c.effective
		c.effective = notch
		c.lastChange = now
		if c.effective != oldEffective {
			changed = true
		}
		return c.effective, changed
	}

	return c.effective, false
}

// Tick evaluates developer activity and updates the effective notch if hysteresis allows.
// In auto mode:
// - If developer is working (active typing/mouse or heavy load): drops fast directly to Minimal.
// - If machine is at rest (idle for minutes with little load): climbs slowly (+1 notch per stepDuration) towards chosen notch.
// - Never climbs above the notch the developer chose.
// - No change is made if less than stepDuration has elapsed since the last change.
func (c *AutoController) Tick() (bool, djinnv1.LoadNotch) {
	c.mu.Lock()

	if !c.isAuto {
		eff := c.effective
		c.mu.Unlock()
		return false, eff
	}

	now := c.clock()
	if !c.lastChange.IsZero() && now.Sub(c.lastChange) < c.stepDuration {
		eff := c.effective
		c.mu.Unlock()
		return false, eff
	}

	var notify func(djinnv1.LoadNotch)
	var changed bool

	activity := c.evalActivity()
	switch activity {
	case activityWorking:
		if c.effective != djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL {
			c.effective = djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL
			c.lastChange = now
			notify = c.onChange
			changed = true
		}
	case activityRest:
		curIdx := NotchIndex(c.effective)
		chosenIdx := NotchIndex(c.chosen)
		if chosenIdx < 0 {
			chosenIdx = NotchIndex(djinnv1.LoadNotch_LOAD_NOTCH_MAX)
		}
		if curIdx >= 0 && curIdx < chosenIdx {
			c.effective = ManualNotches[curIdx+1]
			c.lastChange = now
			notify = c.onChange
			changed = true
		}
	case activityHold:
		// In hold state (between active and idle thresholds with little load), no notch change.
	}

	eff := c.effective
	c.mu.Unlock()

	if notify != nil {
		notify(eff)
	}

	return changed, eff
}

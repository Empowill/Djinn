package machine

import (
	"sync"
	"time"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
)

// DefaultAutoHysteresis is the minimum time that must elapse between notch changes in auto mode.
const DefaultAutoHysteresis = 1 * time.Minute

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

// AutoController manages the operating load notch in auto mode.
// In auto mode, it dynamically moves between Minimal and the notch the developer chose,
// based on developer activity: Minimal when working, slowly climbing towards the chosen notch at rest,
// with hysteresis (no change more than once per step duration) to prevent oscillation.
type AutoController struct {
	mu           sync.Mutex
	isAuto       bool
	chosen       djinnv1.LoadNotch
	effective    djinnv1.LoadNotch
	lastChange   time.Time
	stepDuration time.Duration
	clock        func() time.Time
	signal       ActivitySignal
	onChange     func(djinnv1.LoadNotch)
}

// NewAutoController creates an AutoController with the given initial notch and options.
func NewAutoController(initial djinnv1.LoadNotch, opts ...AutoOption) *AutoController {
	if initial == djinnv1.LoadNotch_LOAD_NOTCH_LIGHT {
		initial = djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM
	}
	c := &AutoController{
		stepDuration: DefaultAutoHysteresis,
		clock:        time.Now,
		signal:       func() bool { return false },
		chosen:       djinnv1.LoadNotch_LOAD_NOTCH_MAX,
		effective:    djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM,
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
		working := c.signal != nil && c.signal()
		oldEffective := c.effective
		if working {
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
// - If developer is working (signal = true): drops fast directly to Minimal.
// - If machine is at rest (signal = false): climbs slowly (+1 notch per stepDuration) towards chosen notch.
// - Never climbs above the notch the developer chose.
// - No change is made if less than stepDuration has elapsed since the last change.
func (c *AutoController) Tick() (bool, djinnv1.LoadNotch) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.isAuto {
		return false, c.effective
	}

	now := c.clock()
	if !c.lastChange.IsZero() && now.Sub(c.lastChange) < c.stepDuration {
		return false, c.effective
	}

	working := c.signal != nil && c.signal()
	if working {
		if c.effective != djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL {
			c.effective = djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL
			c.lastChange = now
			if c.onChange != nil {
				c.onChange(c.effective)
			}
			return true, c.effective
		}
	} else {
		curIdx := NotchIndex(c.effective)
		chosenIdx := NotchIndex(c.chosen)
		if chosenIdx < 0 {
			chosenIdx = NotchIndex(djinnv1.LoadNotch_LOAD_NOTCH_MAX)
		}
		if curIdx >= 0 && curIdx < chosenIdx {
			c.effective = ManualNotches[curIdx+1]
			c.lastChange = now
			if c.onChange != nil {
				c.onChange(c.effective)
			}
			return true, c.effective
		}
	}

	return false, c.effective
}

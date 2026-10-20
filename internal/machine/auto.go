package machine

import (
	"math"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
)

// The auto mode's pace (W253, calmed by W347). Once a beat it reads the load outside Djinn, averaged over the last
// AutoSmooth of what the Hourglass already measured, and steps one notch at a time: down once that average stayed above
// its high mark for AutoDownAfter, up once it stayed below its low marks for AutoUpAfter. A side once reached is left
// only AutoHysteresis points past its mark, each step starts the wait again, and a notch is held at least AutoHold.
const (
	// AutoBeat is how often auto mode reads the outside load and decides; never faster.
	AutoBeat = 20 * time.Second
	// AutoSmooth is the span of the moving average a beat reads: a test run's bursts of a minute do not break it.
	AutoSmooth = 3 * time.Minute
	// AutoDownAfter is how long the outside load stays above the high mark before auto lowers a notch.
	AutoDownAfter = 3 * time.Minute
	// AutoUpAfter is how long the outside load stays below the low marks before auto raises a notch.
	AutoUpAfter = 5 * time.Minute
	// AutoHold is the least time auto mode keeps a notch it stepped to, whatever the load does.
	AutoHold = 3 * time.Minute
	// AutoHysteresis is how far past a mark, in points of the machine, the load goes before it leaves the side it
	// reached: below 15 % it climbs, and only above 20 % does the wait start again; above 30 % it lowers, and only
	// below 25 % does that wait start again. A beat at 15.1 % no longer throws away minutes of calm (W347).
	AutoHysteresis = 5.0
)

// AutoMarks are the outside loads, in percent of the machine, that auto mode steps at: above a high mark it lowers,
// below every low mark it raises, in between it holds.
type AutoMarks struct {
	CPUHigh, CPULow       float64
	MemoryHigh, MemoryLow float64
}

// DefaultAutoMarks come from the developer's machine (W253, 16 cores, 31 GB). Over 3 days of its history (below, every
// 5 s), the CPU outside Djinn, as a minute's average, stayed under 14 % nine times out of ten and under 17 % 19 times
// out of 20: the desktop, the browser, the window drawn. Played on those days, 30 and 15 took 4 steps down in 3
// episodes (an IDE indexing on half the machine, a browser with containers, a video call) and kept auto at Max 98 % of
// the time; 20 and 10 kept it at Minimal 30 % of the time. The memory outside Djinn (used memory, the kernel's and
// shared memory included, beyond the Djinn line) stayed under 70 % over 5 days of the Hourglass, 36 % at the median: 70
// lowers only past what it saw, 60 holds the climb at its top.
var DefaultAutoMarks = AutoMarks{CPUHigh: 30, CPULow: 15, MemoryHigh: 70, MemoryLow: 60}

// Outside is the machine's load outside Djinn: its CPU and memory beyond Djinn's own tree (the Hourglass's Djinn
// line: djinn up, its window, its workers and all they started), in percent of the machine.
type Outside struct {
	CPU, Memory float64
	// Whether each is measured: the machine's CPU is read on Linux, Djinn's memory on Linux and macOS.
	CPUKnown, MemoryKnown bool
}

// OutsideSource reads the load outside Djinn measured after since, averaged (SeriesRecorder.Outside); false when
// nothing was measured since.
type OutsideSource func(since time.Time) (Outside, bool)

// AutoOption configures an AutoController.
type AutoOption func(*AutoController)

// WithAutoClock sets the clock function (used for testing with a fake clock).
func WithAutoClock(clock func() time.Time) AutoOption {
	return func(c *AutoController) { c.clock = clock }
}

// WithAutoOutside sets what reads the load outside Djinn; without it, auto mode holds its notch.
func WithAutoOutside(source OutsideSource) AutoOption {
	return func(c *AutoController) { c.source = source }
}

// WithAutoMarks sets the marks auto mode steps at.
func WithAutoMarks(marks AutoMarks) AutoOption {
	return func(c *AutoController) { c.marks = marks }
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

// autoSide is where the outside load stands against the marks.
type autoSide int

const (
	sideUnknown autoSide = iota // nothing measured
	sideBetween
	sideAbove // above a high mark
	sideBelow // below every low mark
)

// AutoController manages the operating load notch in auto mode: between Minimal and the notch the developer chose,
// from the load of the programs outside Djinn alone. Djinn's own work, and the developer watching it, never lower it:
// the developer at the keyboard is no signal, they move the mouse precisely to watch Djinn (W253).
//
// Once a beat (Tick, every AutoBeat) it reads the outside load's moving average over AutoSmooth. It lowers one notch
// when that average stayed above a high mark for AutoDownAfter, raises one when it stayed below every low mark for
// AutoUpAfter, and each step starts the wait again: never more than one step a beat nor a notch held less than
// AutoHold, a short spike moves nothing, and it comes back steadily. The wait goes on while the load stays within
// AutoHysteresis of the mark it passed, and the step comes once it is past the mark again: a load that hovers about a
// mark starts no wait again. Lowering lowers the CPU priority and starts no new worker, never pausing a running one
// (Decision Q124 (C)). The lead hears nothing of it: the bar shows why (Reason).
type AutoController struct {
	mu        sync.Mutex
	isAuto    bool
	chosen    djinnv1.LoadNotch
	effective djinnv1.LoadNotch
	load      Outside   // the outside load the last beat read
	side      autoSide  // where it stands against the marks
	since     time.Time // the beat it came to that side, or the last step
	past      bool      // the load is past the mark of its side, not only within AutoHysteresis of it
	stepped   time.Time // the last step; zero: none since auto mode started
	reason    *djinnv1.LoadAutoReason
	marks     AutoMarks
	clock     func() time.Time
	source    OutsideSource
	onChange  func(djinnv1.LoadNotch)
}

// NewAutoController creates an AutoController with the given initial notch and options.
func NewAutoController(initial djinnv1.LoadNotch, opts ...AutoOption) *AutoController {
	if initial == djinnv1.LoadNotch_LOAD_NOTCH_LIGHT {
		initial = djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM
	}
	c := &AutoController{
		marks:     DefaultAutoMarks,
		clock:     time.Now,
		chosen:    djinnv1.LoadNotch_LOAD_NOTCH_MAX,
		effective: djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM,
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

// Reason says why auto mode runs at its notch, for the bar; nil outside auto mode.
func (c *AutoController) Reason() *djinnv1.LoadAutoReason {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.isAuto || c.reason == nil {
		return nil
	}
	return proto.CloneOf(c.reason)
}

// state is the auto mode's whole state, read at once: whether it is on, the chosen notch, the one in effect and why
// (Reason).
func (c *AutoController) state() (isAuto bool, chosen, effective djinnv1.LoadNotch, reason *djinnv1.LoadAutoReason) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.isAuto && c.reason != nil {
		reason = proto.CloneOf(c.reason)
	}
	return c.isAuto, c.chosen, c.effective, reason
}

// SetNotch updates the notch setting. If notch is LOAD_NOTCH_AUTO, auto mode is enabled, from the notch in effect
// held between Minimal and the chosen one, the next beat deciding; a manual notch (Minimal to Overclock) disables it
// and becomes the chosen one.
func (c *AutoController) SetNotch(notch djinnv1.LoadNotch) (effective djinnv1.LoadNotch, changed bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if notch == djinnv1.LoadNotch_LOAD_NOTCH_LIGHT {
		notch = djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM
	}

	old := c.effective
	if notch == djinnv1.LoadNotch_LOAD_NOTCH_AUTO {
		if !c.isAuto {
			c.stepped = time.Time{}
		}
		c.isAuto = true
		if NotchIndex(c.chosen) < 0 {
			c.chosen = djinnv1.LoadNotch_LOAD_NOTCH_MAX
		}
		if NotchIndex(c.effective) < 0 {
			c.effective = djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM
		}
		if NotchIndex(c.effective) > NotchIndex(c.chosen) {
			c.effective = c.chosen
		}
		c.reason = c.explain(c.clock())
		return c.effective, c.effective != old
	}

	if NotchIndex(notch) >= 0 {
		c.isAuto = false
		c.chosen = notch
		c.effective = notch
		return c.effective, c.effective != old
	}

	return c.effective, false
}

// Tick is one beat: it reads the outside load, and in auto mode steps the notch one way if that load stayed past a
// mark long enough. The beat reads even outside auto mode, so that switching to it starts from what was measured.
func (c *AutoController) Tick() (bool, djinnv1.LoadNotch) {
	c.mu.Lock()
	now := c.clock()
	c.sample(now)
	if !c.isAuto {
		eff := c.effective
		c.mu.Unlock()
		return false, eff
	}
	changed := c.step(now)
	c.reason = c.explain(now)
	eff, notify := c.effective, c.onChange
	c.mu.Unlock()

	if changed && notify != nil {
		notify(eff)
	}
	return changed, eff
}

// sample reads the outside load's moving average, and notes when it came to its side of the marks. A side once
// reached is kept until the load goes AutoHysteresis points back past its mark; past says whether it is past the mark
// itself, where a step may come.
func (c *AutoController) sample(now time.Time) {
	side, past := sideUnknown, false
	c.load = Outside{}
	if c.source != nil {
		if o, ok := c.source(now.Add(-AutoSmooth)); ok && (o.CPUKnown || o.MemoryKnown) {
			c.load = o
			switch {
			case c.high(o, c.slack(sideAbove)):
				side, past = sideAbove, c.high(o, 0)
			case c.low(o, c.slack(sideBelow)):
				side, past = sideBelow, c.low(o, 0)
			default:
				side = sideBetween
			}
		}
	}
	c.past = past
	if side != c.side || c.since.IsZero() {
		c.side, c.since = side, now
	}
}

// slack is how far back past its mark the load may come and keep to the side s: AutoHysteresis where it stands, none
// on the other sides.
func (c *AutoController) slack(s autoSide) float64 {
	if c.side == s {
		return AutoHysteresis
	}
	return 0
}

// cpuHigh says whether the CPU outside Djinn is measured and above its high mark less slack.
func (c *AutoController) cpuHigh(o Outside, slack float64) bool {
	return o.CPUKnown && o.CPU > c.marks.CPUHigh-slack
}

// high says whether a load outside Djinn is above its high mark less slack.
func (c *AutoController) high(o Outside, slack float64) bool {
	return c.cpuHigh(o, slack) || o.MemoryKnown && o.Memory > c.marks.MemoryHigh-slack
}

// cpuLow says whether the CPU outside Djinn is unmeasured or below its low mark plus slack.
func (c *AutoController) cpuLow(o Outside, slack float64) bool {
	return !o.CPUKnown || o.CPU < c.marks.CPULow+slack
}

// low says whether every load measured is below its low mark plus slack.
func (c *AutoController) low(o Outside, slack float64) bool {
	return c.cpuLow(o, slack) && (!o.MemoryKnown || o.Memory < c.marks.MemoryLow+slack)
}

// ceiling is the index of the chosen notch, Max when none is.
func (c *AutoController) ceiling() int {
	if i := NotchIndex(c.chosen); i >= 0 {
		return i
	}
	return NotchIndex(djinnv1.LoadNotch_LOAD_NOTCH_MAX)
}

// next is the notch the load's side leads to, and how long it must stay there for it; false when it leads nowhere.
func (c *AutoController) next() (djinnv1.LoadNotch, time.Duration, bool) {
	i := NotchIndex(c.effective)
	switch {
	case i < 0:
	case c.side == sideAbove && i > 0:
		return ManualNotches[i-1], AutoDownAfter, true
	case c.side == sideBelow && i < c.ceiling():
		return ManualNotches[i+1], AutoUpAfter, true
	}
	return c.effective, 0, false
}

// wait is how long the load must still stay on its side before the step it leads to: what remains of after, and of
// AutoHold since the last step.
func (c *AutoController) wait(now time.Time, after time.Duration) time.Duration {
	left := after - now.Sub(c.since)
	if !c.stepped.IsZero() {
		left = max(left, AutoHold-now.Sub(c.stepped))
	}
	return max(left, 0)
}

// step moves the notch one way once the load stayed long enough on a side of the marks and is past the mark now, and
// the notch was held AutoHold; the wait starts again.
func (c *AutoController) step(now time.Time) bool {
	n, after, ok := c.next()
	if !ok || !c.past || c.wait(now, after) > 0 {
		return false
	}
	c.effective, c.since, c.stepped = n, now, now
	return true
}

// explain says why the notch is where it is, and what comes next if the load stays where it is.
func (c *AutoController) explain(now time.Time) *djinnv1.LoadAutoReason {
	r := &djinnv1.LoadAutoReason{Step: djinnv1.LoadAutoStep_LOAD_AUTO_STEP_HOLD, Notch: c.effective}
	if c.side == sideUnknown {
		return r
	}
	n, after, ok := c.next()
	switch {
	case ok && !c.past:
		// Back within AutoHysteresis of the mark: the wait goes on, no step until the load passes the mark again.
		ok = false
	case ok && c.side == sideAbove:
		r.Step, r.Notch = djinnv1.LoadAutoStep_LOAD_AUTO_STEP_LOWER, n
	case ok:
		r.Step, r.Notch = djinnv1.LoadAutoStep_LOAD_AUTO_STEP_RAISE, n
	case c.side == sideAbove:
		r.Step = djinnv1.LoadAutoStep_LOAD_AUTO_STEP_FLOOR
	case c.side == sideBelow:
		r.Step, r.Notch = djinnv1.LoadAutoStep_LOAD_AUTO_STEP_CEILING, c.chosen
	}
	stayed := now.Sub(c.since)
	if ok {
		r.InSeconds = seconds(c.wait(now, after))
	}
	// Memory is shown where it decides: above its high mark while the CPU is not, below its low mark with no CPU
	// read, between the marks while the CPU is below its own.
	o := c.load
	switch c.side {
	case sideAbove:
		r.Memory = !c.cpuHigh(o, AutoHysteresis)
	case sideBelow:
		r.Memory = !o.CPUKnown
	default:
		r.Memory = c.cpuLow(o, 0)
	}
	if r.Memory {
		r.OutsidePercent = math.Round(o.Memory)
	} else {
		r.OutsidePercent = math.Round(o.CPU)
	}
	r.WindowSeconds = seconds(stayed)
	return r
}

func seconds(d time.Duration) int32 { return int32(math.Round(d.Seconds())) }

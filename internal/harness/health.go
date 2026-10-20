package harness

// The workers' health (W188). On each pass, after the Lamplighter, the scheduler judges every work task's worker from
// what Djinn already knows of it: its status, its last event (Task.last_activity_time), what its process used of the
// CPU since (the readings of measure.go), the gates it holds or waits for, its usage limit (Task.resume_after) and the
// account its agent is signed in with, its error. A worker is well, stalled, waiting for its limit, past a limit that
// lifted, or to relaunch after a failure a new turn may get past (Task.health, HealthState). The others are never
// flagged: a task stopped on request, failed for good, done, or not started.
//
// The health is written only when its state changes. A change into a flagged state (stalled, retriable) tells the
// task's lead once, in one line, with the command that relaunches it, djinn task continue; the window shows a mark
// beside the worker. When a limit lifts, its reset time passed or its agent's account changed, Djinn resumes the tasks
// it stopped by itself, at once for an account changed, and tells each wish's lead once which ones.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// The thresholds of a stall, from the history of this machine's tasks (W188, 7 to 17/10/2026: 330 817 gaps between
// two events of a running worker). 118 gaps passed 5 minutes, 64 passed 10, 47 passed 15. Every gap past 5 minutes
// whose worker used 5% of a core or more on average was a command at work (tests, builds, benches: 13% to 944%); the
// idle ones (1% to 3% on average, at most 12% in a reading) were the API outage of 16/10 (W44, W55, W67: 23 minutes,
// ended by the agent's own "cut off mid-stream"), workers the Lamplighter woke (W3), and workers that waited for their
// background work after their turn's result (W31, W33, W47: 10 to 25 minutes).
const (
	// StallSilence is how long a running worker may say nothing, with its CPU idle, before it is stalled.
	StallSilence = 10 * time.Minute
	// StallAfterTurn is StallSilence after the worker's turn ended (its last event its result): it may wait for the
	// background work it started, a monitor or a subagent, which said nothing for up to 25 minutes on this machine.
	StallAfterTurn = 30 * time.Minute
	// stallCPU is the most a stalled worker's processes use of a core on average over its silence, in percent: an idle
	// agent uses 1% to 3%.
	stallCPU = 5.0
	// stallPeak is the most one reading of a stalled worker shows, in percent of a core: a command at work goes past it.
	stallPeak = 25.0
	// stallReadings is how many readings of its CPU a silence needs to be judged: none, and the worker is well.
	stallReadings = 2
)

// accountEvery is how often the scheduler asks an agent which account it is signed in with, while a task waits for
// a usage limit of that agent: once a minute at most.
const accountEvery = time.Minute

// methodHealth is journaled when a task's health changes; the request is the task's health.
const methodHealth = "harness/health"

// AccountFunc tells which account provider's agent is signed in with on this machine, as a fingerprint that never
// holds the account's name; empty when it cannot tell. djinn up gives ui.Account.
type AccountFunc func(ctx context.Context, provider planv1.Provider) string

// WithAccount lets the scheduler see an agent's account change while a task waits for that agent's usage limit: the
// limit no longer holds, and the task resumes at once.
func WithAccount(f AccountFunc) Option { return func(h *Harness) { h.accountOf = f } }

// accountSeen is an agent's account as the scheduler last read it.
type accountSeen struct {
	fingerprint string
	at          time.Time
}

// account is the fingerprint of provider's account, read again when the last reading is older than accountEvery or
// than since. Empty without an AccountFunc, or when the agent tells none. The caller holds h.sched.
func (h *Harness) account(ctx context.Context, provider planv1.Provider, since, now time.Time) string {
	if h.accountOf == nil {
		return ""
	}
	if seen, ok := h.accounts[provider]; ok && now.Sub(seen.at) < accountEvery && !seen.at.Before(since) {
		return seen.fingerprint
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	fingerprint := h.accountOf(ctx, provider)
	if h.accounts == nil {
		h.accounts = map[planv1.Provider]accountSeen{}
	}
	h.accounts[provider] = accountSeen{fingerprint: fingerprint, at: now}
	return fingerprint
}

// judged tells whether the scheduler judges the health of t: a work task of this machine, not a watcher, an ifrit, an
// azima or a question worker.
func judged(t *planv1.Task) bool {
	switch t.GetKind() {
	case planv1.TaskKind_TASK_KIND_AZIMA, planv1.TaskKind_TASK_KIND_IFRIT:
		return false
	}
	return t.GetScheduled() && t.GetWishId() != "" && !watching(t) && !light(t)
}

// judge gives each work task its health on a pass, and tells the leads what changed. The caller holds h.sched; tasks
// are the pass's, changed in place so that what the pass writes next keeps the health; now is the pass's time.
func (h *Harness) judge(ctx context.Context, tasks []*planv1.Task, now time.Time) {
	var lifted []*planv1.Task
	for i, t := range tasks {
		if !judged(t) {
			continue
		}
		was := t.GetHealth()
		next, resumeNow := h.healthOf(ctx, t, now)
		if next.GetState() == was.GetState() && (next.GetState() != planv1.HealthState_HEALTH_STATE_LIMITED ||
			next.GetAccount() == was.GetAccount()) {
			continue
		}
		if next != nil {
			next.Since = timestamppb.New(now)
		}
		t = h.putHealth(ctx, t, next, resumeNow)
		tasks[i] = t
		switch next.GetState() {
		case planv1.HealthState_HEALTH_STATE_STALLED, planv1.HealthState_HEALTH_STATE_RETRIABLE:
			h.tellHealth(ctx, t)
		case planv1.HealthState_HEALTH_STATE_LIMIT_LIFTED:
			lifted = append(lifted, t)
		}
	}
	h.tellLifted(ctx, lifted)
}

// healthOf judges t now. resumeNow is set for a task waiting for a limit its agent's account no longer holds: it
// resumes at once.
func (h *Harness) healthOf(ctx context.Context, t *planv1.Task, now time.Time) (health *planv1.TaskHealth, resumeNow bool) {
	switch t.GetStatus() {
	case planv1.TaskStatus_TASK_STATUS_RUNNING:
		return h.runningHealth(t, now), false
	case planv1.TaskStatus_TASK_STATUS_RESUMING:
		if t.GetResumeAfter() == nil {
			return nil, false // Djinn resumes it by itself, for a restart, a continue, a pause of its wish.
		}
		return h.limitHealth(ctx, t, now, t.GetResumeAfter().AsTime(), t.GetWaitReason())
	case planv1.TaskStatus_TASK_STATUS_FAILED, planv1.TaskStatus_TASK_STATUS_INTERRUPTED:
		why, limited := retriable(t)
		if why == "" || h.continuable(ctx, t, h.Works(t.GetId())) != nil {
			return nil, false
		}
		if limited {
			l, _ := limitIn(t.GetError(), endOf(t, now))
			reset := l.Until
			if reset.IsZero() {
				if l.PerMinute {
					reset = endOf(t, now).Add(rateLimitBackoff(0))
				} else {
					reset = endOf(t, now).Add(limitBackoff(0))
				}
			}
			health, _ := h.limitHealth(ctx, t, now, reset, why)
			if health.GetState() == planv1.HealthState_HEALTH_STATE_LIMITED {
				return health, false // Not yet: relaunched now, it would hit its limit again.
			}
			why = "its usage limit lifted (" + health.GetReason() + "): " + why
		}
		return &planv1.TaskHealth{State: planv1.HealthState_HEALTH_STATE_RETRIABLE, Reason: why}, false
	}
	return nil, false
}

// endOf is when t's worker ended: its end time, else now.
func endOf(t *planv1.Task, now time.Time) time.Time {
	if t.GetEndTime() != nil {
		return t.GetEndTime().AsTime()
	}
	return now
}

// limitHealth judges a task its usage limit stopped, until reset: lifted once reset passed or its agent's account
// changed since the limit stopped it, else limited, keeping the account it was stopped on.
func (h *Harness) limitHealth(ctx context.Context, t *planv1.Task, now, reset time.Time, why string) (*planv1.TaskHealth, bool) {
	if !reset.After(now) {
		return &planv1.TaskHealth{State: planv1.HealthState_HEALTH_STATE_LIMIT_LIFTED, Reason: "reset at " + ClockText(reset, now)}, false
	}
	was := t.GetHealth()
	stopped := endOf(t, now)
	account := h.account(ctx, t.GetProvider(), stopped, now)
	if was.GetState() == planv1.HealthState_HEALTH_STATE_LIMITED && was.GetAccount() != "" {
		if account != "" && account != was.GetAccount() {
			return &planv1.TaskHealth{State: planv1.HealthState_HEALTH_STATE_LIMIT_LIFTED, Reason: "its account changed"}, true
		}
		account = was.GetAccount()
	}
	return &planv1.TaskHealth{State: planv1.HealthState_HEALTH_STATE_LIMITED, Reason: why, Account: account}, false
}

// runningHealth judges a running worker: stalled when it said nothing for StallSilence (StallAfterTurn after its turn's
// result) while its processes used almost no CPU, holding and waiting for no gate; well otherwise, and while Djinn
// has too few readings of its CPU to tell (Windows, the fake agent), or no run for it (the Lamplighter's).
func (h *Harness) runningHealth(t *planv1.Task, now time.Time) *planv1.TaskHealth {
	id := t.GetId()
	h.mu.Lock()
	r := h.runs[id]
	if r == nil || r.stopping || r.final || r.paused || r.lamplighterEnd || r.silentWhy != "" || r.watcher || r.ifrit {
		h.mu.Unlock()
		return nil
	}
	last, kind := r.lastActivity, r.lastKind
	h.mu.Unlock()
	silence := StallSilence
	if kind == planv1.TaskEventKind_TASK_EVENT_KIND_USAGE {
		silence = StallAfterTurn
	}
	quiet := now.Sub(last)
	if last.IsZero() || quiet < silence {
		return nil
	}
	if (h.held != nil && len(h.held(id)) > 0) || (h.waiting != nil && len(h.waiting(id)) > 0) {
		return nil // A gated command runs, or waits for its turn.
	}
	mean, idle := h.idleSince(r, last)
	if !idle {
		return nil
	}
	why := fmt.Sprintf("silent %d min, CPU %.0f%%", int(math.Round(quiet.Minutes())), mean)
	if kind == planv1.TaskEventKind_TASK_EVENT_KIND_USAGE {
		why += ", since its turn ended"
	}
	return &planv1.TaskHealth{State: planv1.HealthState_HEALTH_STATE_STALLED, Reason: why}
}

// idleSince tells whether the processes of r's worker used almost no CPU since: stallReadings readings at least, their
// mean under stallCPU and none past stallPeak; and that mean. Too few readings (Windows, the fake agent) is no idle.
func (h *Harness) idleSince(r *run, since time.Time) (mean float64, idle bool) {
	h.useMu.Lock()
	var points []float64
	if r.samples != nil {
		for _, p := range r.samples.Points(since) {
			points = append(points, p.GetCpuPercent())
		}
	}
	h.useMu.Unlock()
	if len(points) < stallReadings {
		return 0, false
	}
	var sum float64
	for _, p := range points {
		sum += p
	}
	mean = sum / float64(len(points))
	return mean, mean < stallCPU && slices.Max(points) < stallPeak // Past either, a command at work: a test, a build.
}

// The failures a new turn may get past, read in the error of a task failed or cut short, from the history of this
// machine's tasks (W188): its agent or its stream broke, Djinn stopped, a usage limit.
var (
	retriableCrash = regexp.MustCompile(`(?i)ended before the end of its turn|stream was interrupted|stream idle|` +
		`cut off mid-stream|network issue|connection (reset|refused)|API error|request failed|overloaded|` +
		`output token limit|improperly formatted function call|lost process|signal: (killed|terminated|segmentation)|` +
		`killed by the machine`)
	retriableRestart = regexp.MustCompile(`(?i)djinn up (stopped|ended) while the worker ran|djinn restarted`)
)

// retriable says why t, failed or cut short, may be relaunched, and whether a usage limit stopped it; "" when it
// failed for good (its budget, its permissions, its settings, a dependency, an error Djinn does not know).
func retriable(t *planv1.Task) (why string, limited bool) {
	err := firstLine(t.GetError())
	if t.GetStatus() == planv1.TaskStatus_TASK_STATUS_INTERRUPTED {
		return "cut short by a stop of Djinn", false
	}
	if t.GetStatus() != planv1.TaskStatus_TASK_STATUS_FAILED || strings.HasPrefix(t.GetError(), "budget exceeded") ||
		strings.Contains(t.GetError(), "and gives up") { // The Lamplighter woke it maxWakes times already.
		return "", false
	}
	if l, ok := limitIn(t.GetError(), time.Time{}); ok {
		return "stopped by " + l.What, true
	}
	switch {
	case retriableRestart.MatchString(err):
		return "cut short by a stop of Djinn", false
	case retriableCrash.MatchString(err):
		return "a crash: " + clipRunes(err, 80), false
	}
	return "", false
}

// putHealth writes t's health, and for a task resumed now its wait, on the task as stored: a run's pump keeps it
// (carry). It returns t with them.
func (h *Harness) putHealth(ctx context.Context, t *planv1.Task, health *planv1.TaskHealth, resumeNow bool) *planv1.Task {
	t = proto.CloneOf(t)
	t.Health = health
	var ev *Event
	if resumeNow {
		t.ResumeAfter = timestamppb.New(h.now())
		t.WaitReason = "its account changed: the usage limit no longer holds"
		ev = &Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: "resuming: " + t.GetWaitReason() + "; Djinn resumes it now"}
	}
	err := h.store.Tx(context.WithoutCancel(ctx), func(tx *store.Tx) error {
		stored, err := store.Get[*planv1.Task](ctx, tx, t.GetId())
		if err != nil {
			return err
		}
		journaled := health
		if journaled == nil {
			journaled = &planv1.TaskHealth{}
		}
		if err := tx.Journal(actorHarness, methodHealth, journaled); err != nil {
			return err
		}
		stored.Health = health
		if resumeNow {
			if stored.GetStatus() != planv1.TaskStatus_TASK_STATUS_RESUMING {
				return nil
			}
			stored.ResumeAfter, stored.WaitReason = t.GetResumeAfter(), t.GetWaitReason()
		}
		if err := tx.Put(stored); err != nil {
			return err
		}
		if ev != nil {
			return putEvents(ctx, tx, h.newEvent(t.GetId(), *ev))
		}
		return nil
	})
	if err != nil && ctx.Err() == nil {
		logx.L().Error("record a worker's health", "task", t.GetCode(), "err", err)
	}
	h.notify()
	return t
}

// HealthLine is the line that tells a task's lead its worker needs a relaunch, and the command that does it.
func HealthLine(t *planv1.Task) string {
	what := "can be relaunched"
	if t.GetHealth().GetState() == planv1.HealthState_HEALTH_STATE_STALLED {
		what = "stalled"
	}
	return fmt.Sprintf("Djinn: %s %s (%s); relaunch: djinn task continue %s", t.GetCode(), what,
		t.GetHealth().GetReason(), t.GetId())
}

// tellHealth tells t's lead, once, that its worker needs a relaunch.
func (h *Harness) tellHealth(ctx context.Context, t *planv1.Task) {
	if err := h.toLead(ctx, planv1.LeadEventKind_LEAD_EVENT_KIND_HEALTH, t.GetWishId(), t.GetCode(), HealthLine(t)); err != nil &&
		!errors.Is(err, plan.ErrNoLead) {
		logx.L().Warn("tell the lead", "wish", t.GetWishId(), "task", t.GetCode(), "err", err)
	}
}

// tellLifted tells each wish's lead, in one line, the tasks whose usage limit lifted, which Djinn resumes by itself.
func (h *Harness) tellLifted(ctx context.Context, tasks []*planv1.Task) {
	type group struct{ wish, how string }
	var groups []group
	for _, t := range tasks {
		if g := (group{t.GetWishId(), t.GetHealth().GetReason()}); !slices.Contains(groups, g) {
			groups = append(groups, g)
		}
	}
	for _, g := range groups {
		wish, how := g.wish, g.how
		var codes []string
		for _, t := range tasks {
			if t.GetWishId() == wish && t.GetHealth().GetReason() == how {
				codes = append(codes, t.GetCode())
			}
		}
		code := ""
		if len(codes) == 1 {
			code = codes[0]
		}
		line := fmt.Sprintf("Djinn: %s: the usage limit that stopped it lifted (%s); Djinn resumes it by itself.",
			strings.Join(codes, ", "), how)
		if len(codes) > 1 {
			line = fmt.Sprintf("Djinn: %s: the usage limit that stopped them lifted (%s); Djinn resumes them by itself.",
				strings.Join(codes, ", "), how)
		}
		if err := h.toLead(ctx, planv1.LeadEventKind_LEAD_EVENT_KIND_HEALTH, wish, code, line); err != nil &&
			!errors.Is(err, plan.ErrNoLead) {
			logx.L().Warn("tell the lead", "wish", wish, "err", err)
		}
	}
}

// flaggedAtEnd tells whether t, its worker failed now, is one the scheduler flags to relaunch at its next pass, for a
// reason a new turn may get past: its lead hears it then, in one line with the command, rather than twice.
func (h *Harness) flaggedAtEnd(t *planv1.Task) bool {
	if !judged(t) || t.GetStatus() != planv1.TaskStatus_TASK_STATUS_FAILED {
		return false
	}
	why, limited := retriable(t)
	return why != "" && !limited && h.continuable(context.Background(), t, false) == nil
}

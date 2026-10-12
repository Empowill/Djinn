package harness

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// A task's seal is the set of constraints Djinn gives the worker of a task (docs/providers.md, "A task's seal"). Its
// budgets are here, enforced by the Lamplighter: SilenceLimit, TaskMaxWallTime and TaskMaxTurns. The rest is decided
// where it applies: the access and how each agent's sandbox applies it (decideAccess, accessSpec), the wakes
// (maxWakes) and usage-limit resumes (maxResumes), the spending cap (Task.max_budget_usd), the push guard and the
// worker's identity (gitGuard, plan.WorkerGuard), the write scopes (dispatch), the load notch's CPU priority
// (lowPriority) and the worker's memory ceiling (scopeFor), the slots and the gates.

// SilenceLimit is how long a worker may produce no event before the Lamplighter checks its process tree and CPU:
// long builds and test runs print periodically; background tasks in agy might execute commands, and any gated
// command is excluded by checking the gate it holds or waits for. An idle or dead worker past this limit is ended.
const SilenceLimit = 15 * time.Minute

// TaskMaxWallTime is how long a task may run in total before being stopped: the sum of its runs (taskWallTime), time
// waiting for a gate included, never reset by a continue. Generous for heavy builds and e2e-heavy tasks (past an hour),
// while stopping 4h runaway zombies.
const TaskMaxWallTime = 2 * time.Hour

// TaskMaxTurns is how many model turns a worker may take before being stopped: a guard against an endless loop, not a
// cap on real work. Real Opus tasks take a hundred turns and more (W442: 102, W453: 112, both stopped at the former
// limit of 50). The count is per run, not per task: start sets it to 1 each time a worker starts (first start,
// restart, resume, continue, answer); each prompt the worker says adds one; claude's count of its assistant messages
// (Event.Turns) and the provider's own count of a result (num_turns) replace it when larger. The task shows it
// (Task.turns).
const TaskMaxTurns = 500

// WithSilenceLimit sets the silence limit before the Lamplighter checks a worker: tests set a shorter one.
func WithSilenceLimit(d time.Duration) Option { return func(h *Harness) { h.silenceLimit = d } }

// WithTaskMaxWallTime sets the maximum wall time budget per task before being stopped: tests set a shorter one.
func WithTaskMaxWallTime(d time.Duration) Option { return func(h *Harness) { h.maxWallTime = d } }

// turnLimit is how many model turns a run may take before the Lamplighter stops it.
func (h *Harness) turnLimit() int { return cmp.Or(h.maxTurns, TaskMaxTurns) }

// setTurns sets the run's turns to f of them, and keeps them on its task: the turns of its current worker, out of
// the limit, and over all its runs. They are written with the task's next write: the sampler's (measure), or an
// event's that changes the task. Runs where the task is owned: start, then the pump. A watcher runs no model: its
// task shows none.
func (h *Harness) setTurns(r *run, f func(turns int) int) {
	h.mu.Lock()
	r.turns = f(r.turns)
	turns := r.turns
	h.mu.Unlock()
	if r.watcher {
		return
	}
	n := &planv1.Turns{Run: int32(turns), Limit: int32(h.turnLimit()), Total: int32(r.turnsBefore + turns)}
	if !proto.Equal(r.task.GetTurns(), n) {
		r.task.Turns = n // Replaced, never changed: a copy of the task taken before shares it, and measure compares it.
	}
}

// WithTaskMaxTurns sets the maximum turn budget per task before being stopped: tests set a lower one.
func WithTaskMaxTurns(n int) Option { return func(h *Harness) { h.maxTurns = n } }

// lamplighter is Djinn's watchdog, the Lamplighter: it wakes before it fails. It checks running workers: if a worker
// produces no events for longer than the silence limit, its process tree and CPU usage are inspected. Dead or idle
// workers are ended, then woken (continued on their session, W296/W320) up to maxWakes times in a row, else failed. A
// task whose status is RUNNING but has no active worker run (a lost process) is also found and ended the same way. A
// budget of the task's seal (wall time, turns) stops a worker for good: no wake. A watcher is supervised apart, before
// any budget (watcherLife).
func (h *Harness) lamplighter(ctx context.Context, tasks []*planv1.Task) (anyLost bool) {
	now := h.now()
	limit := cmp.Or(h.silenceLimit, SilenceLimit)

	for _, t := range tasks {
		if t.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
			continue
		}
		id := t.GetId()
		h.mu.Lock()
		r := h.runs[id]
		if r == nil {
			h.mu.Unlock()
			if watching(t) {
				logx.L().Info("lamplighter restarts a lost watcher", "task", t.GetCode())
				h.restartLostWatcher(ctx, t)
			} else {
				logx.L().Info("lamplighter ends a lost task", "task", t.GetCode())
				h.endLostProcess(ctx, t)
			}
			anyLost = true
			continue
		}

		if r.stopping || r.final || r.paused || r.lamplighterEnd {
			h.mu.Unlock()
			continue
		}

		// A watcher is no worker: no silence rule, no wall-time budget, no turn budget. Its run lasts as long as the
		// loop that starts its command again (watcherLife); a lost one is started again above.
		if r.watcher {
			h.mu.Unlock()
			continue
		}

		// Budget per task: check wall time and turns against limits.
		maxWall := cmp.Or(h.maxWallTime, TaskMaxWallTime)
		maxTurns := h.turnLimit()

		wallTime := taskWallTime(t, now)
		if wallTime >= maxWall {
			r.stopping = true
			r.stoppingReason = fmt.Sprintf("budget exceeded: wall time %s (limit %s)", wallTime.Round(time.Second), maxWall.Round(time.Second))
			worker := r.worker
			h.mu.Unlock()
			logx.L().Info("lamplighter stops a task", "task", t.GetCode(), "why", "wall time", "took", wallTime.Round(time.Second))
			if worker != nil {
				worker.Stop()
			}
			continue
		}

		if r.turns > maxTurns {
			r.stopping = true
			r.stoppingReason = fmt.Sprintf("budget exceeded: %d turns (limit %d)", r.turns, maxTurns)
			worker := r.worker
			turns := r.turns
			h.mu.Unlock()
			logx.L().Info("lamplighter stops a task", "task", t.GetCode(), "why", "turns", "turns", turns)
			if worker != nil {
				worker.Stop()
			}
			continue
		}

		// agy's background tasks may be silent while a gated command runs: check the gate it holds or waits for.
		if (h.held != nil && len(h.held(id)) > 0) || (h.waiting != nil && len(h.waiting(id)) > 0) {
			h.mu.Unlock()
			continue
		}

		lastAct := r.lastActivity
		if lastAct.IsZero() {
			if t.GetStartTime() != nil {
				lastAct = t.GetStartTime().AsTime()
			} else {
				lastAct = now
			}
			r.lastActivity = lastAct
		}

		silentDuration := now.Sub(lastAct)
		if silentDuration < limit {
			// Update CPU baseline while worker is active or within limit.
			if p, ok := r.worker.(Process); ok && p != nil && p.PID() != 0 && h.measureRead != nil {
				if g, err := h.measureRead(p.PID(), p.Cgroup()); err == nil {
					r.lastLamplighterCPU = g.CPU
				}
			}
			h.mu.Unlock()
			continue
		}

		// Worker is silent past the limit. Check process tree and CPU used since last check.
		alive := false
		busy := false
		if p, ok := r.worker.(Process); ok && p != nil && p.PID() != 0 && h.measureRead != nil {
			g, err := h.measureRead(p.PID(), p.Cgroup())
			if err == nil && g.Processes > 0 {
				alive = true
				if g.CPU > r.lastLamplighterCPU {
					busy = true
					r.lastLamplighterCPU = g.CPU
				}
			}
		}

		if alive && busy {
			h.mu.Unlock()
			continue
		}

		// Dead or idle: end it.
		mins := int(math.Round(silentDuration.Minutes()))
		if mins < 1 {
			mins = 1
		}
		r.lamplighterEnd = true
		r.lamplighterReason = fmt.Sprintf("silent for %d min", mins)
		worker := r.worker
		h.mu.Unlock()
		logx.L().Info("lamplighter ends a silent task", "task", t.GetCode(), "silent", silentDuration.Round(time.Second),
			"alive", alive)

		if worker != nil {
			worker.Stop()
		}
	}
	return anyLost
}

// maxWakes is how many times in a row the Lamplighter wakes a worker it found silent, idle or lost, or whose stream
// went idle: found so once more, its task fails, saying so, and its lead is told once. A run that ends otherwise
// (done, a usage limit, a restart of Djinn) ends the row. Usage-limit resumes are counted apart (maxResumes): a worker that
// waited for its limits is woken all the same.
const maxWakes = 3

// runWoken is why a run ended that the Lamplighter ended to wake its worker (TaskRun.reason).
const runWoken = "wake"

// wakes counts the last runs of t in a row that the Lamplighter ended to wake its worker, before the one that runs.
func wakes(t *planv1.Task) int {
	runs := t.GetRuns()
	if len(runs) > 0 && runs[len(runs)-1].GetEndTime() == nil {
		runs = runs[:len(runs)-1]
	}
	n := 0
	for i := len(runs) - 1; i >= 0 && runs[i].GetReason() == runWoken; i-- {
		n++
	}
	return n
}

// canWake tells whether the Lamplighter wakes the worker of t rather than failing it: its agent can resume its
// session, and it was not woken maxWakes times in a row.
func canWake(t *planv1.Task) bool { return plan.CanResume(t.GetProvider()) && wakes(t) < maxWakes }

// notWoken is the error of a task the Lamplighter does not wake, ended for why.
func notWoken(t *planv1.Task, why string) string {
	if !plan.CanResume(t.GetProvider()) {
		return why
	}
	return fmt.Sprintf("%s; the Lamplighter woke it %d times in a row and gives up", why, wakes(t))
}

// markWoken makes t, whose worker the Lamplighter ended for why, wait to be continued on its session: its last run
// ends woken.
func markWoken(t *planv1.Task, why string, end *timestamppb.Timestamp) {
	t.Status, t.Error, t.Continuing, t.WaitReason = planv1.TaskStatus_TASK_STATUS_RESUMING, "", true, why
	if len(t.Runs) > 0 && t.Runs[len(t.Runs)-1].GetEndTime() == nil {
		t.Runs[len(t.Runs)-1].EndTime = end
	}
	if len(t.Runs) > 0 {
		t.Runs[len(t.Runs)-1].Reason = runWoken
	}
}

// endLostProcess ends a task whose status is RUNNING but that the harness has no run for (a lost process).
func (h *Harness) endLostProcess(ctx context.Context, t *planv1.Task) {
	now := h.now()
	nowPb := timestamppb.New(now)
	t = proto.CloneOf(t)
	t.EndTime = nowPb
	if canWake(t) {
		markWoken(t, whyLost, nowPb)
		h.writeAlone(ctx, actorHarness, methodEvent, nil, t.GetId(), nil, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_PROMPT, Text: agyContinuePrompt})
		h.writeAlone(ctx, actorHarness, methodEnd, nil, t.GetId(), t, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: "resuming: lost process; continued"})
	} else {
		t.Status = planv1.TaskStatus_TASK_STATUS_FAILED
		t.Error = notWoken(t, "lost process: worker is not running")
		h.writeAlone(ctx, actorHarness, methodEnd, nil, t.GetId(), t, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: "failed: " + t.GetError()})
	}
	h.tellWorkerFailed(t)
	h.wake()
}

// taskWallTime computes the total wall time consumed by task t up to now across all its runs.
func taskWallTime(t *planv1.Task, now time.Time) time.Duration {
	if t == nil {
		return 0
	}
	var total time.Duration
	for _, run := range t.GetRuns() {
		if run.GetStartTime() == nil {
			continue
		}
		start := run.GetStartTime().AsTime()
		end := now
		if run.GetEndTime() != nil {
			end = run.GetEndTime().AsTime()
		}
		if end.After(start) {
			total += end.Sub(start)
		}
	}
	if total == 0 && t.GetStartTime() != nil {
		total = now.Sub(t.GetStartTime().AsTime())
	}
	return total
}

// A watcher runs a command, not an agent: the Lamplighter supervises it as a process of its own. Its silence is its
// normal state, and it watches as long as its wish needs it: no silence rule, no wall-time budget, no turn budget.
// What counts is that its command lives while its task runs. A command that dies (an error, an exit code other than
// 0) starts again after a backoff, in the same task: its folder and its state folder (DJINN_HOME/watchers/<task>)
// are as it left them, so it does not say again what it said. A run that lived watchLived resets the backoff and the
// count; after watchTries deaths in a row, the task fails, and its lead is told why with the command's last error
// lines. A command that exits 0 ends its task done, or starts again when the task restarts it (--restart). A watcher
// whose process is lost (its task running, no run) starts again (restartLostWatcher).
const (
	// watchBackoff is the first wait before a command that died starts again; it doubles up to watchMaxBackoff.
	watchBackoff    = time.Second
	watchMaxBackoff = 5 * time.Minute
	// watchLived is how long a run lives for its death not to count: the backoff and the count start over.
	watchLived = time.Minute
	// watchTries is how many deaths in a row fail a watcher's task.
	watchTries = 5
	// watchErrLines is how many of the last lines of the command's error output its failure keeps.
	watchErrLines = 3
)

// watcherLife is what the Lamplighter keeps of a watcher's command from one run to the next: the next backoff, and its
// deaths in a row. The watcher's loop asks it what follows each run.
type watcherLife struct {
	first, backoff time.Duration
	deaths         int
	endless        bool // an inbox source never gives up
}

func newWatcherLife(backoff time.Duration, endless bool) *watcherLife {
	return &watcherLife{first: backoff, backoff: backoff, endless: endless}
}

// died tells whether a run of the command died rather than ended: an error, or an exit code other than 0.
func died(res Result) bool { return res.Err != nil || res.ExitCode != 0 }

// ended says what follows a run of the command that lasted ran and ended with res, in a task that restarts its
// command after exit 0 or not: how long to wait before the next start, and whether there is one. A command that
// exits 0 ends a task that does not restart it, done; one that died watchTries times in a row ends it, failed.
func (l *watcherLife) ended(res Result, restart bool, ran time.Duration) (wait time.Duration, again bool) {
	if !died(res) {
		l.deaths, l.backoff = 0, l.first
		return 0, restart
	}
	if ran >= watchLived {
		l.deaths, l.backoff = 0, l.first
	}
	l.deaths++
	if !l.endless && l.deaths >= watchTries {
		return 0, false
	}
	wait = l.backoff
	l.backoff = min(2*l.backoff, max(watchMaxBackoff, l.first))
	return wait, true
}

// gaveUp is the error of a watcher whose command died watchTries times in a row: the last death, and the last lines
// of its error output.
func gaveUp(res Result, errLines []string) error {
	why := fmt.Sprintf("exit code %d", res.ExitCode)
	if res.Err != nil {
		why = res.Err.Error()
	}
	text := fmt.Sprintf("its command died %d times in a row; the last time: %s", watchTries, why)
	if len(errLines) > 0 {
		text += "; its error output: " + strings.Join(errLines, " / ")
	}
	return errors.New(text)
}

// restartLostWatcher starts again the command of a watcher whose process is lost: its task waits to resume, and the
// scheduler starts it at once, in the same task, as it does after a restart of Djinn. A watcher never fails for it.
// Read again first: a watcher that ended or was stopped since the pass listed it stays as it is.
func (h *Harness) restartLostWatcher(ctx context.Context, t *planv1.Task) {
	code := t.GetCode()
	t, err := store.Get[*planv1.Task](ctx, h.store, t.GetId())
	if err != nil {
		if ctx.Err() == nil {
			logx.L().Error("restart a lost watcher", "loop", "lamplighter", "task", code, "err", err)
		}
		return
	}
	if t.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
		return
	}
	h.mu.Lock()
	_, runs := h.runs[t.GetId()]
	h.mu.Unlock()
	if runs {
		return
	}
	t.EndTime = timestamppb.New(h.now())
	t.Status, t.Error, t.WaitReason, t.ResumeAfter = planv1.TaskStatus_TASK_STATUS_RESUMING, "", whyLost, nil
	h.writeAlone(ctx, actorHarness, methodEnd, nil, t.GetId(), t, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS,
		Text: "resuming: " + whyLost + "; Djinn starts its command again"})
	h.wake()
}

// tellWatcherFailed tells the lead of the watcher t's wish, once, why its watcher failed.
func (h *Harness) tellWatcherFailed(t *planv1.Task) {
	if t.GetWishId() == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	line := fmt.Sprintf("Djinn: %s's watcher failed: %s", t.GetCode(), clipRunes(t.GetError(), watchLineMax))
	if err := h.toLead(ctx, planv1.LeadEventKind_LEAD_EVENT_KIND_FAILED, t.GetWishId(), t.GetCode(), line); err != nil &&
		!errors.Is(err, plan.ErrNoLead) {
		logx.L().Warn("tell the lead", "loop", "lamplighter", "wish", t.GetWishId(), "task", t.GetCode(), "err", err)
	}
}

package harness

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
)

// SilenceLimit is how long a worker may produce no event before the watchdog checks its process tree and CPU:
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

// WithSilenceLimit sets the silence limit before the watchdog checks a worker: tests set a shorter one.
func WithSilenceLimit(d time.Duration) Option { return func(h *Harness) { h.silenceLimit = d } }

// WithTaskMaxWallTime sets the maximum wall time budget per task before being stopped: tests set a shorter one.
func WithTaskMaxWallTime(d time.Duration) Option { return func(h *Harness) { h.maxWallTime = d } }

// turnLimit is how many model turns a run may take before the watchdog stops it.
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

// watchdog checks running workers: if a worker produces no events for longer than the silence limit,
// its process tree and CPU usage are inspected; a watcher's silence is never checked. Dead or idle workers are ended (continued per W296/W320
// if resumable, else failed). A task whose status is RUNNING but has no active worker run (a lost process)
// is also found and ended the same way.
func (h *Harness) watchdog(ctx context.Context, tasks []*planv1.Task) (anyLost bool) {
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
			h.endLostProcess(ctx, t)
			anyLost = true
			continue
		}

		if r.stopping || r.final || r.paused || r.watchdogEnd {
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
			if worker != nil {
				worker.Stop()
			}
			continue
		}

		if r.turns > maxTurns {
			r.stopping = true
			r.stoppingReason = fmt.Sprintf("budget exceeded: %d turns (limit %d)", r.turns, maxTurns)
			worker := r.worker
			h.mu.Unlock()
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

		// A watcher's command prints only when what it watches changes: its silence is its normal state. A command
		// that ends is the watcher's own to handle: it fails the task, or starts again (--restart).
		if r.watcher {
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
					r.lastWatchdogCPU = g.CPU
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
				if g.CPU > r.lastWatchdogCPU {
					busy = true
					r.lastWatchdogCPU = g.CPU
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
		r.watchdogEnd = true
		r.watchdogReason = fmt.Sprintf("silent for %d min", mins)
		worker := r.worker
		h.mu.Unlock()

		if worker != nil {
			worker.Stop()
		}
	}
	return anyLost
}

// endLostProcess ends a task whose status is RUNNING but that the harness has no run for (a lost process).
func (h *Harness) endLostProcess(ctx context.Context, t *planv1.Task) {
	now := h.now()
	nowPb := timestamppb.New(now)
	canResume := plan.CanResume(t.GetProvider()) && t.GetResumes() < maxResumes
	t = proto.CloneOf(t)
	t.EndTime = nowPb
	if canResume {
		t.Status = planv1.TaskStatus_TASK_STATUS_RESUMING
		t.WaitReason = "lost process"
		t.Continuing = true
		t.Resumes++
		t.Error = ""
		h.writeAlone(ctx, actorHarness, methodEvent, nil, t.GetId(), nil, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_PROMPT, Text: agyContinuePrompt})
		h.writeAlone(ctx, actorHarness, methodEnd, nil, t.GetId(), t, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: "resuming: lost process; continued"})
	} else {
		t.Status = planv1.TaskStatus_TASK_STATUS_FAILED
		t.Error = "lost process: worker is not running"
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

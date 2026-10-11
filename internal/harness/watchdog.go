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

// WithSilenceLimit sets the silence limit before the watchdog checks a worker: tests set a shorter one.
func WithSilenceLimit(d time.Duration) Option { return func(h *Harness) { h.silenceLimit = d } }

// watchdog checks running workers: if a worker produces no events for longer than the silence limit,
// its process tree and CPU usage are inspected. Dead or idle workers are ended (continued per W296/W320
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

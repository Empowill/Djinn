package harness

import (
	"cmp"
	"context"
	"math"
	"runtime"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// methodPreempt is what the journal records when Djinn preempts a work task to make room for an investigator.
const methodPreempt = "harness/preempt"

// isMemoryShort reports whether why says the task cannot start because memory is short.
// It is false for pressure reasons, and true for room forecasts and committed limits.
func isMemoryShort(why string) bool {
	if strings.HasPrefix(why, "the machine is under pressure") {
		return false
	}
	return strings.Contains(why, "peaks at") || strings.Contains(why, "commits up to") || strings.Contains(why, "memory")
}

// slotsFull reports whether all worker slots are taken by running workers or outside gates.
func (h *Harness) slotsFull() bool {
	if h.capacity == nil {
		return false
	}
	slots, _, _ := h.capacity()
	if slots <= 0 {
		return false
	}
	h.mu.Lock()
	running := 0
	for _, r := range h.runs {
		if !r.paused && !r.watcher && !r.light {
			running++
		}
	}
	if h.outside != nil {
		running += h.outside()
	}
	h.mu.Unlock()
	return running >= slots
}

type candidate struct {
	r *run
	t *planv1.Task
}

// lowestWorkTask returns the running work task of lowest priority to preempt:
// the last wish by rank, then the most recently started.
// It never returns:
// - an investigator or question worker;
// - a correction or review worker;
// - a task holding or waiting for a gate;
// - on Windows when forSlot is true;
// - a worker that cannot pause (when forSlot is true);
// - a worker whose provider cannot resume (when forSlot is false).
func (h *Harness) lowestWorkTask(ctx context.Context, forSlot bool) (*planv1.Task, *run) {
	goos := runtime.GOOS
	if h.goos != nil {
		goos = h.goos()
	}
	if forSlot && goos == "windows" {
		return nil, nil
	}
	h.mu.Lock()
	var candidates []candidate
	var ids []string
	for _, r := range h.runs {
		if r.paused || r.watcher || r.light || r.stopping || r.final || r.shelved || r.worker == nil {
			continue
		}
		t := r.task
		if t == nil || t.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
			continue
		}
		if questionWorker(t) {
			continue
		}
		if t.GetCorrection() != nil || t.GetReview() != nil {
			continue
		}
		if h.held != nil && len(h.held(t.GetId())) > 0 {
			continue
		}
		if h.waiting != nil && len(h.waiting(t.GetId())) > 0 {
			continue
		}
		if forSlot {
			if _, ok := r.worker.(Pauser); !ok {
				continue
			}
		} else {
			if !plan.CanResume(t.GetProvider()) {
				continue
			}
		}
		candidates = append(candidates, candidate{r: r, t: proto.CloneOf(t)})
		ids = append(ids, t.GetId())
	}
	h.mu.Unlock()

	if len(candidates) == 0 {
		return nil, nil
	}

	ranks := h.Ranks(ctx, ids)

	// isLowerPriority reports whether candidate a has lower priority than candidate b:
	// 1. Last wish by rank (highest index in ranks; unranked/unknown is math.MaxInt)
	// 2. Most recently started (later StartTime)
	// 3. Task code descending
	isLowerPriority := func(a, b candidate) bool {
		rankA, okA := ranks[a.t.GetId()]
		if !okA {
			rankA = math.MaxInt
		}
		rankB, okB := ranks[b.t.GetId()]
		if !okB {
			rankB = math.MaxInt
		}
		if rankA != rankB {
			return rankA > rankB
		}
		var startA, startB time.Time
		if a.t.GetStartTime() != nil {
			startA = a.t.GetStartTime().AsTime()
		}
		if b.t.GetStartTime() != nil {
			startB = b.t.GetStartTime().AsTime()
		}
		if !startA.Equal(startB) {
			return startA.After(startB)
		}
		return a.t.GetCode() > b.t.GetCode()
	}

	lowest := candidates[0]
	for _, cand := range candidates[1:] {
		if isLowerPriority(cand, lowest) {
			lowest = cand
		}
	}
	return lowest.t, lowest.r
}

// preemptForSlot pauses the lowest work task to free its slot for an investigator.
// It returns the preempted task's code, or "" if no task was preempted.
func (h *Harness) preemptForSlot(ctx context.Context, inv *planv1.Task) (string, error) {
	cand, _ := h.lowestWorkTask(ctx, true)
	if cand == nil {
		return "", nil
	}
	why := "made room for " + cmp.Or(inv.GetTitle(), inv.GetCode())
	req := &planv1.TaskServicePauseRequest{TaskId: cand.GetId()}
	if _, err := h.hold(ctx, methodPreempt, req, cand.GetId(), true, why); err != nil {
		return "", err
	}
	h.mu.Lock()
	if h.preempted == nil {
		h.preempted = make(map[string]string)
	}
	h.preempted[inv.GetId()] = cand.GetId()
	h.mu.Unlock()
	return cand.GetCode(), nil
}

// preemptForMemory interrupts the lowest work task so it resumes later in its session/conversation.
// It returns the preempted task's code, or "" if no task was preempted.
func (h *Harness) preemptForMemory(ctx context.Context, inv *planv1.Task) (string, error) {
	cand, r := h.lowestWorkTask(ctx, false)
	if cand == nil || r == nil {
		return "", nil
	}
	why := "made room for " + cmp.Or(inv.GetTitle(), inv.GetCode())
	h.mu.Lock()
	r.preemptedWhy = why
	r.stopping = true
	if p, ok := r.worker.(Pauser); ok && r.paused {
		_ = p.Resume()
	}
	if r.worker != nil {
		r.worker.Stop()
	}
	done := r.done
	h.mu.Unlock()
	if err := h.store.Tx(ctx, func(tx *store.Tx) error {
		return tx.Journal(actorHarness, methodPreempt, &planv1.TaskServiceStopRequest{TaskId: cand.GetId()})
	}); err != nil {
		return "", err
	}
	select {
	case <-done:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	return cand.GetCode(), nil
}

// unpausePreempted resumes a task that was paused to make room for investigator t.
func (h *Harness) unpausePreempted(t *planv1.Task) {
	h.mu.Lock()
	if h.preempted == nil {
		h.mu.Unlock()
		return
	}
	targetID, ok := h.preempted[t.GetId()]
	if ok {
		delete(h.preempted, t.GetId())
	}
	h.mu.Unlock()
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _ = h.Resume(ctx, methodPreempt, &planv1.TaskServiceResumeRequest{TaskId: targetID})
}

// preemptedWaitReason is the wait reason of a task interrupted to make room for an investigator, as the scheduler
// says why it waits: it keeps saying it made room, so that its worker is told so when it starts.
func preemptedWaitReason(t *planv1.Task, why string) string {
	const prefix = "made room for "
	if !strings.HasPrefix(t.GetWaitReason(), prefix) || why == "" || strings.HasPrefix(why, prefix) {
		return why
	}
	orig := t.GetWaitReason()
	if idx := strings.Index(orig, "; "); idx >= 0 {
		orig = orig[:idx]
	}
	return orig + "; " + why
}

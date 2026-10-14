package harness

import (
	"cmp"
	"context"
	"math"
	"runtime"
	"slices"
	"time"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/machine"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// methodNotch is what the journal records when Djinn pauses, resumes or interrupts a worker for an operating load notch change.
const methodNotch = "harness/notch"

// Paused is the number of workers paused now. A paused worker holds still: it takes no slot, but keeps its memory.
func (h *Harness) Paused() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, r := range h.runs {
		if r.paused && !r.watcher && !r.light {
			n++
		}
	}
	return n
}

// updatePriorities dynamically updates CPU priority on every running worker's process tree (Q120 A).
func (h *Harness) updatePriorities(low bool) {
	h.mu.Lock()
	var workers []Prioritizer
	for _, r := range h.runs {
		if r.worker != nil && !r.stopping && !r.final {
			if pr, ok := r.worker.(Prioritizer); ok {
				workers = append(workers, pr)
			}
		}
	}
	h.mu.Unlock()
	for _, w := range workers {
		_ = w.SetPriority(low)
	}
}

// enforceNotchCapacity pauses extra workers when the operating load notch allows fewer workers than run: the newest
// first (Q121: Q117's order reversed), never an investigator, a correction, a review, a worker holding or waiting for
// a gate (it pauses once it lets the gate go), one started by hand, nor any on Windows, where a worker cannot pause:
// those finish their run, new tasks waiting meanwhile. It weighs the workers running against what the notch itself
// allows (barSlots), not against the memory's share of the slots: pausing a worker frees no memory, and the memory
// available moves every second (W577: W39 was paused and resumed every 2 s at Medium, its notch unchanged).
// In Auto mode, it never pauses (Q124 C).
func (h *Harness) enforceNotchCapacity(ctx context.Context, p machine.Policy) {
	if p.Auto || h.capacity == nil {
		return
	}
	slots := h.barSlotsAt(p.Notch)
	if slots <= 0 {
		return
	}
	why := "operating load lowered to " + machine.NotchTitle(p.Notch)
	// A worker started by hand stays beyond the limit (Forced): it pushes no other worker out.
	for h.Running()-h.Forced() > slots {
		cand, r := h.lowestWorkTask(ctx, true)
		if cand == nil || r == nil {
			break
		}
		req := &planv1.TaskServicePauseRequest{TaskId: cand.GetId()}
		if _, err := h.hold(ctx, methodNotch, req, cand.GetId(), true, why); err != nil {
			break
		}
	}
}

// barSlotsAt is the most workers the notch itself lets run (WithBarSlots), or the machine's slots without it.
func (h *Harness) barSlotsAt(n djinnv1.LoadNotch) int {
	if h.barSlots != nil {
		return h.barSlots(n)
	}
	slots, _, _ := h.capacity()
	return slots
}

// LowerPauses tells whether lowering the operating load pauses the workers above what it lets run, as the window
// says: not in Auto mode (Q124 C), nor on Windows, where a worker cannot pause; they finish their run then.
func (h *Harness) LowerPauses() bool {
	goos := runtime.GOOS
	if h.goos != nil {
		goos = h.goos()
	}
	return !h.Policy().Auto && goos != "windows"
}

// resumeNotchPaused resumes the workers paused by the notch, or to give their slot to an investigator, when a slot is
// free again: before any new task (Q123), in Q117's order. One paused for an investigator resumes even under pressure:
// it ran before the investigator took its slot.
func (h *Harness) resumeNotchPaused(ctx context.Context) {
	if h.capacity != nil {
		slots, _, _ := h.capacity()
		if slots > 0 && h.Running() >= slots {
			return
		}
	}

	h.mu.Lock()
	var candidates []candidate
	var ids []string
	for _, r := range h.runs {
		if !r.paused || !r.notchPaused && !r.slotPaused || r.stopping || r.final || r.worker == nil {
			continue
		}
		t := r.task
		if t == nil || t.GetStatus() != planv1.TaskStatus_TASK_STATUS_PAUSED {
			continue
		}
		candidates = append(candidates, candidate{r: r, t: h.copyRunning(t)})
		ids = append(ids, t.GetId())
	}
	h.mu.Unlock()

	if len(candidates) == 0 {
		return
	}

	ranks := h.Ranks(ctx, ids)

	// Q117 order:
	// 1. Wish rank ascending (lowest index in ranks first)
	// 2. Start time ascending (started earlier first)
	// 3. Task code ascending
	isHigherPriority := func(a, b candidate) bool {
		rankA, okA := ranks[a.t.GetId()]
		if !okA {
			rankA = math.MaxInt
		}
		rankB, okB := ranks[b.t.GetId()]
		if !okB {
			rankB = math.MaxInt
		}
		if rankA != rankB {
			return rankA < rankB
		}
		var startA, startB time.Time
		if a.t.GetStartTime() != nil {
			startA = a.t.GetStartTime().AsTime()
		}
		if b.t.GetStartTime() != nil {
			startB = b.t.GetStartTime().AsTime()
		}
		if !startA.Equal(startB) {
			return startA.Before(startB)
		}
		return a.t.GetCode() < b.t.GetCode()
	}

	slices.SortFunc(candidates, func(a, b candidate) int {
		if isHigherPriority(a, b) {
			return -1
		}
		if isHigherPriority(b, a) {
			return 1
		}
		return 0
	})

	for _, cand := range candidates {
		h.mu.Lock()
		forSlot := cand.r.slotPaused
		h.mu.Unlock()
		if h.capacity != nil {
			slots, _, pressure := h.capacity()
			if slots > 0 && h.Running() >= slots {
				break
			}
			if pressure != "" && !forSlot {
				continue
			}
		}
		// Not "raised to" a notch: the monitor's slots change before the harness's notch (applyNotch), and a pass
		// between the two would name the notch left.
		why := "a slot is free again"
		req := &planv1.TaskServiceResumeRequest{TaskId: cand.t.GetId()}
		if _, err := h.hold(ctx, methodNotch, req, cand.t.GetId(), false, why); err != nil {
			break
		}
	}
}

// lowestPausedWorkTask returns the paused work task of lowest priority to interrupt for memory:
// Q122 (A): only when memory is short for a higher-priority worker that must start,
// never a provider that cannot resume.
func (h *Harness) lowestPausedWorkTask(ctx context.Context, forTask *planv1.Task) (*planv1.Task, *run) {
	h.mu.Lock()
	var candidates []candidate
	var ids []string
	for _, r := range h.runs {
		if !r.paused || r.watcher || r.light || r.stopping || r.final || r.shelved || r.worker == nil {
			continue
		}
		t := r.task
		if t == nil || t.GetStatus() != planv1.TaskStatus_TASK_STATUS_PAUSED {
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
		if !plan.CanResume(t.GetProvider()) {
			continue
		}
		candidates = append(candidates, candidate{r: r, t: h.copyRunning(t)})
		ids = append(ids, t.GetId())
	}
	h.mu.Unlock()

	if len(candidates) == 0 {
		return nil, nil
	}

	ranks := h.Ranks(ctx, ids)
	active, _ := plan.ActiveWishes(ctx, h.store)
	rankForTask := slices.IndexFunc(active, func(w *planv1.Wish) bool { return w.GetId() == forTask.GetWishId() })
	if rankForTask < 0 {
		rankForTask = math.MaxInt
	}

	isSpecial := forTask.GetRole() != planv1.TaskRole_TASK_ROLE_UNSPECIFIED ||
		forTask.GetCorrection() != nil || forTask.GetReview() != nil

	var eligible []candidate
	for _, c := range candidates {
		rankC, okC := ranks[c.t.GetId()]
		if !okC {
			rankC = math.MaxInt
		}
		if isSpecial {
			if rankC >= rankForTask {
				eligible = append(eligible, c)
			}
			continue
		}
		if rankC > rankForTask {
			eligible = append(eligible, c)
		} else if rankC == rankForTask {
			var startC time.Time
			if c.t.GetStartTime() != nil {
				startC = c.t.GetStartTime().AsTime()
			}
			var startFor time.Time
			if forTask.GetStartTime() != nil {
				startFor = forTask.GetStartTime().AsTime()
			} else if forTask.GetCreateTime() != nil {
				startFor = forTask.GetCreateTime().AsTime()
			}
			if !startC.Equal(startFor) && !startFor.IsZero() {
				if startC.After(startFor) {
					eligible = append(eligible, c)
				}
			} else if c.t.GetCode() > forTask.GetCode() {
				eligible = append(eligible, c)
			}
		}
	}

	if len(eligible) == 0 {
		return nil, nil
	}

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

	lowest := eligible[0]
	for _, cand := range eligible[1:] {
		if isLowerPriority(cand, lowest) {
			lowest = cand
		}
	}
	return lowest.t, lowest.r
}

// interruptPausedForMemory interrupts the lowest paused work task so it resumes later in its session/conversation.
// Q122 (A): only when memory is short for a higher-priority worker that must start.
func (h *Harness) interruptPausedForMemory(ctx context.Context, forTask *planv1.Task) (string, error) {
	cand, r := h.lowestPausedWorkTask(ctx, forTask)
	if cand == nil || r == nil {
		return "", nil
	}
	why := "made room for " + cmp.Or(forTask.GetTitle(), forTask.GetCode())
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
		return tx.Journal(actorHarness, methodNotch, &planv1.TaskServiceStopRequest{TaskId: cand.GetId()})
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

package harness

// A paused wish stops its workers. Each one is cut short as djinn up stopping would cut it, and waits RESUMING: the
// scheduler holds the tasks of a wish that is not active, and starts them again, on their sessions, in their
// worktrees, once the developer makes the wish active again. A pause is no failure: it does not count among the
// resumes a task is allowed.

import (
	"context"
	"strings"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

const (
	// whyWishPaused begins the wait reason of a task its wish's pause stopped, until its worker starts again.
	whyWishPaused = "stopped by its wish's pause"
	byWish        = "after its wish was active again"
	wishLine      = "Your wish was paused, which stopped you; it is active again. Your worktree is as you left it. " +
		"Continue your task."
)

// Shelve stops the workers of the wish wishID, watchers too: each task waits to resume with its wish. It does not
// wait for them to end. The plan calls it once a wish is paused (plan.WishWorkers).
func (h *Harness) Shelve(_ context.Context, wishID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.runs {
		if r.wish != wishID || r.stopping || r.final || r.shelved {
			continue
		}
		r.shelved = true
		if r.worker == nil {
			continue // It starts: launch stops the worker it gets.
		}
		if p, ok := r.worker.(Pauser); ok && r.paused {
			_ = p.Resume() // A process held still would only hear the stop once let go.
		}
		r.worker.Stop()
	}
}

// Wake asks the scheduler for a pass: a wish active again has tasks to start, or a gate held outside the workers
// was given back (gate.Gates.Freed).
func (h *Harness) Wake() { h.wake() }

// shelve gives the task of a run its wish's pause stopped its waiting status: RESUMING, held with its wish.
func shelve(t *planv1.Task) {
	t.Status, t.Error, t.WaitReason, t.ResumeAfter = planv1.TaskStatus_TASK_STATUS_RESUMING, "", whyWishPaused, nil
}

// StopWish stops the workers of the wish wishID for good, watchers too, and waits until each one has ended: the
// wish is about to be deleted. Each task ends stopped.
func (h *Harness) StopWish(ctx context.Context, wishID string) error {
	h.mu.Lock()
	var done []chan struct{}
	for _, r := range h.runs {
		if r.wish != wishID {
			continue
		}
		done = append(done, r.done)
		if r.stopping || r.final {
			continue
		}
		r.stopping = true
		if r.worker == nil {
			continue // It starts: launch stops the worker it gets.
		}
		if p, ok := r.worker.(Pauser); ok && r.paused {
			_ = p.Resume()
		}
		r.worker.Stop()
	}
	h.mu.Unlock()
	for _, d := range done {
		select {
		case <-d:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// shelvedWhy is the wait reason of a task its wish's pause stopped, as the scheduler says why it waits: it keeps
// saying the pause stopped it, so that its worker is told so when it starts again.
func shelvedWhy(t *planv1.Task, why string) string {
	if !strings.HasPrefix(t.GetWaitReason(), whyWishPaused) || why == whyWishPaused {
		return why
	}
	if strings.HasPrefix(why, "its wish is ") {
		return whyWishPaused // Still paused: said once already.
	}
	return whyWishPaused + "; " + why
}

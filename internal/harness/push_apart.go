package harness

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/logx"
)

// Push checks apart (W180, Q40 A): a push Djinn starts by itself no longer runs in the integration's pass, which
// merged nothing while the push checks ran (up to 25 minutes). The pass decides that a push is due and that no work is
// queued for its branch (pushWaitsForMerges); then the push runs apart, one per branch at a time (pushApart). Its
// push checks run on the branch's tip as it was then, a frozen commit, in a worktree of their own (pushDir), so the
// merges go on meanwhile. A merge that lands during them makes them moot: nothing is pushed nor held, and Djinn checks
// again once no merge is queued. So does work queued for the branch during them (W281, Q39/Q40): a task's work waiting
// or being merged, a review or a correction at work. Each push Djinn starts has a context of its own, cancelled as soon
// as such work is queued (stopWhenQueued, told by queueMoved): its check command stops, which frees its gate, its
// machine slot and its memory, nothing is recorded red, and a pass checks again once the queue is empty. The push's
// last step, which looks whether the branch moved or work got queued, then holds the push or pushes the commit
// checked, holds integrateMu: no merge lands between that look and git push, and a correction of red push checks starts
// only when none repairs the branch (Q39). A push the person asked for, or approved, is never stopped for the queue: it
// checks the new tip at once when a merge moved its branch, Push trying pushTries times, an approval staying until its
// push goes.

// errMoot is why a push did not go: a merge moved its branch during its push checks, or work got queued for it.
var errMoot = errors.New("a merge moved the branch during its push checks")

// queuedError is why a push Djinn started stopped: codes, work queued to merge into branch, got queued during it. It is
// moot (errMoot).
type queuedError struct {
	branch string
	codes  []string
}

func (e *queuedError) Error() string {
	return fmt.Sprintf("%s queued to merge into %s during its push checks, nothing pushed", strings.Join(e.codes, ", "), e.branch)
}

func (e *queuedError) Unwrap() error { return errMoot }

// pushJob is a push run apart from the pass.
type pushJob struct {
	done chan struct{} // closed once the push ended
	look chan struct{} // work may be queued for its branch: it looks (stopWhenQueued); nil for a push the person asked for
}

// pushTries is how many times a push the person asked for (Push) checks the branch, each merge landing during its
// checks having them run again on the new tip.
const pushTries = 3

// mootError says that branch moved from sha, the commit its push checks ran on, to tip during them: nothing pushed.
func mootError(branch, sha, tip string) error {
	return fmt.Errorf("%w: %s went from %s to %s, nothing pushed", errMoot, branch, short8(sha), short8(tip))
}

// pushDir is the worktree the push checks of a wish's branches run in, for one project: in Djinn's data folder, beside
// its integration worktree, which merges meanwhile. pushMu holds it.
func pushDir(home, projectID, wishID string) string {
	return filepath.Join(home, "projects", projectID, "push", wishID)
}

// pushApart runs push, the push of branch of the wish wishID in the project projectID, apart from the integration's
// pass, unless that push runs already: it tells whether it started. Unless approved, a push the person asked for or
// approved, its context is cancelled as soon as work is queued for its branch (stopWhenQueued). A push made moot asks
// for a pass, which checks again once the queue is empty.
func (h *Harness) pushApart(wishID, projectID, branch string, approved bool, push func(ctx context.Context) error) bool {
	key := pushKey(wishID, projectID, branch)
	h.pushJobsMu.Lock()
	defer h.pushJobsMu.Unlock()
	if _, ok := h.pushJobs[key]; ok || h.ctx.Err() != nil {
		return false
	}
	job := &pushJob{done: make(chan struct{})}
	if !approved {
		job.look = make(chan struct{}, 1)
		job.look <- struct{}{} // Work queued between the pass's look and now.
	}
	h.pushJobs[key] = job
	go func() {
		defer logx.Crash("push")
		defer func() {
			h.pushJobsMu.Lock()
			delete(h.pushJobs, key)
			h.pushJobsMu.Unlock()
			close(job.done)
		}()
		ctx, stop := context.WithCancelCause(h.ctx)
		watched := make(chan struct{})
		if job.look != nil {
			go func() {
				defer logx.Crash("push")
				defer close(watched)
				h.stopWhenQueued(ctx, stop, job.look, wishID, projectID, branch)
			}()
		} else {
			close(watched)
		}
		err := push(ctx)
		stop(nil)
		<-watched
		logPush(err, "wish", wishID, "project", projectID, "branch", branch)
		if errors.Is(err, errMoot) {
			h.kickIntegrate()
		}
	}()
	return true
}

// stopWhenQueued cancels ctx, a push's of branch of the wish wishID in the project projectID, with a *queuedError as
// soon as work is queued for that branch: it looks each time look says work may be; it returns once ctx is done.
func (h *Harness) stopWhenQueued(
	ctx context.Context, stop context.CancelCauseFunc, look <-chan struct{}, wishID, projectID, branch string,
) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-look:
		}
		if queued := h.pushQueued(ctx, wishID, projectID, branch); len(queued) > 0 && ctx.Err() == nil {
			stop(&queuedError{branch: branch, codes: queued})
			return
		}
	}
}

// queueMoved tells the pushes run apart that work may be queued for their branch: a task's work settled, waiting, or
// a review or a correction spawned. Each looks (stopWhenQueued).
func (h *Harness) queueMoved() {
	h.pushJobsMu.Lock()
	defer h.pushJobsMu.Unlock()
	for _, job := range h.pushJobs {
		if job.look == nil {
			continue
		}
		select {
		case job.look <- struct{}{}:
		default:
		}
	}
}

// pushQueuedStop tells whether pushing, a push's context, was cancelled for work queued for branch (a *queuedError):
// then it says it in the events of committed, the tasks the push brings, its push checks at sha ("" before they began),
// and returns that error; else nil.
func (h *Harness) pushQueuedStop(ctx, pushing context.Context, committed []*planv1.Task, branch, sha string) error {
	var queued *queuedError
	if !errors.As(context.Cause(pushing), &queued) {
		return nil
	}
	h.pushSteps(ctx, committed, queuedStep(queued, sha))
	return queued
}

// queuedStep is the event that says the push of err.branch, its push checks at sha ("" before they began), stopped for
// the work queued.
func queuedStep(err *queuedError, sha string) string {
	what := "the push of " + err.branch + " stops before its push checks"
	if sha != "" {
		what = "the push checks of " + err.branch + " at " + short8(sha) + " stop"
	}
	return fmt.Sprintf("integration: %s: %s queued to merge into it; nothing pushed nor held, Djinn checks it again once the queue is empty",
		what, strings.Join(err.codes, ", "))
}

// pastTheQueue is pushing, a push's context, past its last look at the queue: work queued cancels it no more, only
// djinn up stopping does. done releases it.
func (h *Harness) pastTheQueue(pushing context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(context.WithoutCancel(pushing))
	stop := context.AfterFunc(h.ctx, cancel)
	return ctx, func() { stop(); cancel() }
}

// pushBranchApart runs the push of branch, of wish in project, apart (pushBranch).
func (h *Harness) pushBranchApart(
	wish *planv1.Wish, project *planv1.Project, branch string, committed []*planv1.Task, why string, approved bool,
) {
	h.pushApart(wish.GetId(), project.GetId(), branch, approved, func(ctx context.Context) error {
		_, err := h.pushBranch(ctx, wish, project, branch, committed, why, approved)
		return err
	})
}

// pushApartRuns tells whether the push of branch, of the wish wishID in the project projectID, runs apart now.
func (h *Harness) pushApartRuns(wishID, projectID, branch string) bool {
	h.pushJobsMu.Lock()
	defer h.pushJobsMu.Unlock()
	_, ok := h.pushJobs[pushKey(wishID, projectID, branch)]
	return ok
}

// waitPushes waits until no push runs apart.
func (h *Harness) waitPushes() {
	for {
		h.pushJobsMu.Lock()
		var done chan struct{}
		for _, job := range h.pushJobs {
			done = job.done
			break
		}
		h.pushJobsMu.Unlock()
		if done == nil {
			return
		}
		<-done
	}
}

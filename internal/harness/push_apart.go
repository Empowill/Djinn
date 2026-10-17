package harness

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/logx"
)

// Push checks apart (W180, Q40 A): a push Djinn starts by itself no longer runs in the integration's pass, which
// merged nothing while the push checks ran (up to 25 minutes). The pass decides that a push is due and that no merge
// is queued for its branch (pushWaitsForMerges); then the push runs apart, one per branch at a time (pushApart). Its
// push checks run on the branch's tip as it was then, a frozen commit, in a worktree of their own (pushDir), so the
// merges go on meanwhile. A merge that lands during them makes them moot: nothing is pushed nor held, and Djinn checks
// again once no merge is queued. The push's last step, which looks whether the branch moved, then holds the push or
// pushes the commit checked, holds integrateMu: no merge lands between that look and git push, and a correction of
// red push checks starts only when none repairs the branch (Q39). A push the person asked for, or approved, checks
// the new tip at once: Push tries pushTries times, an approval stays until its push goes.

// errMoot is why a push did not go: a merge moved its branch during its push checks.
var errMoot = errors.New("a merge moved the branch during its push checks")

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
// pass, unless that push runs already: it tells whether it started. A push made moot asks for a pass, which checks
// again once no merge is queued.
func (h *Harness) pushApart(wishID, projectID, branch string, push func(ctx context.Context) error) bool {
	key := pushKey(wishID, projectID, branch)
	h.pushJobsMu.Lock()
	defer h.pushJobsMu.Unlock()
	if _, ok := h.pushJobs[key]; ok || h.ctx.Err() != nil {
		return false
	}
	done := make(chan struct{})
	h.pushJobs[key] = done
	go func() {
		defer logx.Crash("push")
		defer func() {
			h.pushJobsMu.Lock()
			delete(h.pushJobs, key)
			h.pushJobsMu.Unlock()
			close(done)
		}()
		err := push(h.ctx)
		logPush(err, "wish", wishID, "project", projectID, "branch", branch)
		if errors.Is(err, errMoot) {
			h.kickIntegrate()
		}
	}()
	return true
}

// pushBranchApart runs the push of branch, of wish in project, apart (pushBranch).
func (h *Harness) pushBranchApart(
	wish *planv1.Wish, project *planv1.Project, branch string, committed []*planv1.Task, why string, approved bool,
) {
	h.pushApart(wish.GetId(), project.GetId(), branch, func(ctx context.Context) error {
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
		for _, c := range h.pushJobs {
			done = c
			break
		}
		h.pushJobsMu.Unlock()
		if done == nil {
			return
		}
		<-done
	}
}

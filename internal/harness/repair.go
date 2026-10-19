package harness

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// Repairs and batches (W126, decisions Q30 A and Q39 A): one correction worker repairs a branch at a time, whatever
// failed (a merge's conflict, its commit checks, the push checks, main's merge), and nothing else merges into the
// branch while it does: the work finished meanwhile waits in a queue, its reason naming the repair, and the repair's own
// work goes in first. Once no correction repairs the branch, the queue merges as one batch, its branches one on top of
// the other, the commit checks run once for all of it. A branch that conflicts leaves the batch and merges alone after
// it, a correction of its own starting if it still conflicts, one at a time. A red batch gets one correction worker that
// sees all of it; when that one fails, Djinn merges the batch's work one branch at a time (TaskIntegration.alone), to
// find what breaks the checks, which gets its own correction. A push due waits until the queue is empty, however long
// (W281): the work waiting or being merged, the repair, and the work left not committed while its review worker is at
// it; then it pushes if green. Push checks that began on an empty queue stop as soon as work is queued (push_apart.go).

// repairing tells whether c, a correction worker, still repairs its branch: it is at work (atWork), or its work, done,
// is left not committed and reviewed. One whose work is in, or whose merge failed, is over: the correction after it, if
// any, repairs.
func (h *Harness) repairing(c *planv1.Task) bool {
	if c.GetCorrection() == nil {
		return false
	}
	if h.atWork(c) {
		return true
	}
	in := c.GetIntegration()
	return c.GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE &&
		in.GetState() == planv1.IntegrationState_INTEGRATION_STATE_UNCOMMITTED && in.GetReviewedBy() != ""
}

// atWork tells whether t, a worker on a branch's work (a correction, a review), is not over: it runs or waits to, or it
// ended done and its work waits to be merged, or is being merged. One that failed, was cancelled, or stopped past
// stopGrace is over; so is one done whose work is anywhere else.
func (h *Harness) atWork(t *planv1.Task) bool {
	if h.running(t.GetId()) {
		return true
	}
	switch t.GetStatus() {
	case planv1.TaskStatus_TASK_STATUS_FAILED, planv1.TaskStatus_TASK_STATUS_CANCELLED:
		return false
	case planv1.TaskStatus_TASK_STATUS_STOPPED:
		return h.stoppedLately(t)
	case planv1.TaskStatus_TASK_STATUS_DONE:
		switch t.GetIntegration().GetState() {
		case planv1.IntegrationState_INTEGRATION_STATE_PENDING, planv1.IntegrationState_INTEGRATION_STATE_INTEGRATING:
			return true
		}
		return false
	}
	return true
}

// laneOf is the branch the work of t, a task of wish in project, merges into, as integrateBatch finds it: "" when it
// cannot tell (settings unreadable, no branch), its work then merging alone, which says why.
func (h *Harness) laneOf(ctx context.Context, wish *planv1.Wish, project *planv1.Project, t *planv1.Task) string {
	settings, err := plan.LoadSettings(h.home, project)
	if err != nil {
		return ""
	}
	if !settings.Integrates() {
		if branch, err := h.uncheckedBranch(ctx, wish, project, settings, t); err == nil {
			return branch
		}
		return ""
	}
	branch, why, ok := h.targetBranch(ctx, wish, project, settings, []*planv1.Task{t})
	if !ok || why != "" {
		return ""
	}
	return branch
}

// laneKnown is laneOf as the branches and the wish stand, read only: laneOf may cut an azima's branch, record the wish's
// integration branch, or tell the lead a branch is apart, which the pass does, never a look apart from it (pushQueued).
// "" when only laneOf can tell.
func (h *Harness) laneKnown(ctx context.Context, wish *planv1.Wish, project *planv1.Project, t *planv1.Task) string {
	settings, err := plan.LoadSettings(h.home, project)
	if err != nil {
		return ""
	}
	if strat, _ := plan.ResolveWishPushStrategy(wish, settings); strat == planv1.PushStrategy_PUSH_STRATEGY_AZIMA {
		azima, err := h.taskAzima(ctx, t)
		switch {
		case err != nil:
			return ""
		case azima != nil:
			if azima.GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE || !settings.Integrates() && !azima.GetAzima().GetNamed() {
				return "" // Held out, or no branch for unchecked work (uncheckedBranch).
			}
			return azimaBranchName(azima)
		case !settings.Integrates():
			return ""
		}
	}
	branch := plan.IntegrationBranchOf(wish, project.GetId())
	if settings.Integrates() {
		return cmp.Or(branch, plan.CheckedOutBranch(ctx, project.GetDirectory()))
	}
	if main, _, err := resolveMainSha(ctx, project.GetDirectory(), settings); branch == "" || err != nil || strings.EqualFold(main, branch) {
		return ""
	}
	return branch
}

// repairOf is the correction worker among tasks, all of wish's, that repairs branch in project; nil when none does.
func (h *Harness) repairOf(ctx context.Context, wish *planv1.Wish, project *planv1.Project, branch string, tasks []*planv1.Task) *planv1.Task {
	return h.repairIn(project, branch, tasks, func(t *planv1.Task) string { return h.laneOf(ctx, wish, project, t) })
}

// repairIn is repairOf, lane telling the branch a task's work merges into.
func (h *Harness) repairIn(project *planv1.Project, branch string, tasks []*planv1.Task, lane func(*planv1.Task) string) *planv1.Task {
	if branch == "" {
		return nil
	}
	for _, c := range tasks {
		if c.GetProjectId() == project.GetId() && h.repairing(c) && lane(c) == branch {
			return c
		}
	}
	return nil
}

// repairOn is repairOf, the wish's tasks read.
func (h *Harness) repairOn(ctx context.Context, wish *planv1.Wish, project *planv1.Project, branch string) *planv1.Task {
	tasks, err := store.List[*planv1.Task](ctx, h.store, store.Where{"wish_id": wish.GetId()})
	if err != nil {
		logx.L().Error("read the tasks", "loop", "integrate", "wish", wish.GetId(), "err", err)
		return nil
	}
	return h.repairOf(ctx, wish, project, branch, tasks)
}

// repairWork tells whether t's work is a repair's own: a correction worker's, or a review worker's of one.
func repairWork(t *planv1.Task, tasks []*planv1.Task) bool {
	if t.GetCorrection() != nil {
		return true
	}
	return slices.ContainsFunc(t.GetReview().GetTaskIds(), func(id string) bool {
		i := slices.IndexFunc(tasks, func(o *planv1.Task) bool { return o.GetId() == id })
		return i >= 0 && tasks[i].GetCorrection() != nil
	})
}

// lane is the work waiting to merge into one branch of a wish in a project, in the order it ended.
type lane struct {
	wish    *planv1.Wish
	project *planv1.Project
	branch  string // "": unknown, each task merges alone and says why
	tasks   []*planv1.Task
}

// integrateLane merges the work of l: while a correction worker repairs the branch, only its own work, the rest waiting
// for it; otherwise the work waiting as one batch, then the work that merges alone, one at a time, until a correction
// starts. Each committed batch checks whether the push is due. It returns the batches that moved the branch.
func (h *Harness) integrateLane(ctx context.Context, l *lane) []*mergedBatch {
	var merged []*mergedBatch
	commit := func(batch []*planv1.Task) {
		committed, mb := h.integrateBatch(ctx, l.wish, l.project, batch)
		if committed {
			h.pushIfDue(ctx, l.wish.GetId(), l.project)
			if mb != nil {
				merged = append(merged, mb)
			}
		}
	}
	if l.branch == "" {
		for _, t := range l.tasks {
			if ctx.Err() != nil {
				break
			}
			commit([]*planv1.Task{t})
		}
		return merged
	}
	// How each task was tried in this pass: 1 in a batch, 2 alone. A task that left the batch merges alone after it.
	tried := map[string]int{}
	for ctx.Err() == nil {
		tasks, err := store.List[*planv1.Task](ctx, h.store, store.Where{"wish_id": l.wish.GetId()})
		if err != nil {
			logx.L().Error("read the tasks", "loop", "integrate", "wish", l.wish.GetId(), "err", err)
			break
		}
		var queue []*planv1.Task
		for _, w := range l.tasks {
			i := slices.IndexFunc(tasks, func(t *planv1.Task) bool { return t.GetId() == w.GetId() })
			if i >= 0 && tasks[i].GetIntegration().GetState() == planv1.IntegrationState_INTEGRATION_STATE_PENDING &&
				tasks[i].GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE && !h.running(w.GetId()) {
				queue = append(queue, tasks[i])
			}
		}
		if len(queue) == 0 {
			break
		}
		if repair := h.repairOf(ctx, l.wish, l.project, l.branch, tasks); repair != nil {
			own := slices.IndexFunc(queue, func(t *planv1.Task) bool { return repairWork(t, tasks) && tried[t.GetId()] < 2 })
			if own >= 0 {
				tried[queue[own].GetId()] = 2
				commit(queue[own : own+1])
				continue
			}
			h.waitForRepair(ctx, queue, l.branch, repair)
			break
		}
		var batch []*planv1.Task
		for _, t := range queue {
			if !t.GetIntegration().GetAlone() && tried[t.GetId()] == 0 {
				batch = append(batch, t)
			}
		}
		if len(batch) > 0 {
			for _, t := range batch {
				tried[t.GetId()] = 1
			}
			commit(batch)
			continue
		}
		i := slices.IndexFunc(queue, func(t *planv1.Task) bool { return t.GetIntegration().GetAlone() && tried[t.GetId()] < 2 })
		if i < 0 {
			break
		}
		tried[queue[i].GetId()] = 2
		commit(queue[i : i+1])
	}
	return merged
}

// waitForRepair records the work of queue as waiting for repair, the correction worker that repairs branch: it merges
// once the repair's work is in.
func (h *Harness) waitForRepair(ctx context.Context, queue []*planv1.Task, branch string, repair *planv1.Task) {
	why := fmt.Sprintf("waits for %s, which repairs %s: it merges once that work is in", repair.GetCode(), branch)
	for _, t := range queue {
		in := proto.CloneOf(t.GetIntegration())
		in.Branch, in.Reason = branch, why
		h.settleIntegration(ctx, []*planv1.Task{t}, in, "integration: waiting: "+why, nil)
	}
}

// leftOut is a branch that left its batch, conflicting, and why.
type leftOut struct {
	task *planv1.Task
	why  string
}

// leaveBatch records the work of left, whose branches conflicted in their batch into branch, as waiting to merge
// alone, after the batch, a correction of its own starting if it conflicts again.
func (h *Harness) leaveBatch(ctx context.Context, left []leftOut, branch string) {
	for _, l := range left {
		h.settleIntegration(ctx, []*planv1.Task{l.task}, &planv1.TaskIntegration{
			State: planv1.IntegrationState_INTEGRATION_STATE_PENDING, Branch: branch, Reason: "left its batch: " + l.why, Alone: true,
		}, "integration: left its batch: "+l.why+"; it merges alone after the batch, with a correction of its own if it still conflicts", nil)
	}
}

// works counts the tasks of group whose work is a worker's own: no correction worker's, no review worker's.
func works(group []*planv1.Task) int {
	n := 0
	for _, t := range group {
		if t.GetCorrection() == nil && t.GetReview() == nil {
			n++
		}
	}
	return n
}

// stepByStep gives up on correcting the work of group, a red batch, together, in saying how it failed and text what
// Djinn saw: its tasks wait to merge one at a time (TaskIntegration.alone), so that the work that breaks the checks is
// found, and gets a correction of its own; the work of the batch's correction workers is left out.
func (h *Harness) stepByStep(ctx context.Context, group []*planv1.Task, in *planv1.TaskIntegration, text string) {
	var work []*planv1.Task
	for _, t := range group {
		if t.GetCorrection() == nil {
			work = append(work, t)
			continue
		}
		if t.GetIntegration() != nil {
			out := proto.CloneOf(in)
			out.CorrectedBy, out.QuestionId = "", ""
			out.Reason = "left out (step by step): " + headline(in.GetReason())
			h.settleIntegration(ctx, []*planv1.Task{t}, out, text+"; its work is left out: Djinn merges the batch one branch at a time", nil)
		}
	}
	note := fmt.Sprintf("%s; step by step: Djinn merges %s one at a time, to find the work that breaks the checks", text, codes(work))
	for _, t := range work {
		h.settleIntegration(ctx, []*planv1.Task{t}, &planv1.TaskIntegration{
			State: planv1.IntegrationState_INTEGRATION_STATE_PENDING, Branch: in.GetBranch(),
			Reason: "step by step: the correction of its batch failed", Alone: true,
		}, note, nil)
	}
	h.kickIntegrate()
}

// mergesQueued lists the work queued to merge into branch, of wish in project, among tasks, the wish's: finished work
// waiting or being merged, the correction worker that repairs the branch, and the review worker at work on what a task
// left not committed (W281).
func (h *Harness) mergesQueued(ctx context.Context, wish *planv1.Wish, project *planv1.Project, branch string, tasks []*planv1.Task) []string {
	return h.queuedIn(project, branch, tasks, func(t *planv1.Task) string { return h.laneOf(ctx, wish, project, t) })
}

// queuedIn is mergesQueued, lane telling the branch a task's work merges into.
func (h *Harness) queuedIn(project *planv1.Project, branch string, tasks []*planv1.Task, lane func(*planv1.Task) string) []string {
	var out []string
	for _, t := range tasks {
		in := t.GetIntegration()
		if t.GetProjectId() != project.GetId() || t.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
			continue
		}
		code := t.GetCode()
		switch in.GetState() {
		case planv1.IntegrationState_INTEGRATION_STATE_PENDING, planv1.IntegrationState_INTEGRATION_STATE_INTEGRATING:
		case planv1.IntegrationState_INTEGRATION_STATE_UNCOMMITTED:
			// Its review worker commits it, then merges it: queued while that one is at work.
			i := slices.IndexFunc(tasks, func(r *planv1.Task) bool { return r.GetId() == in.GetReviewedBy() })
			if in.GetReviewedBy() == "" || i < 0 || !h.atWork(tasks[i]) {
				continue
			}
			code = tasks[i].GetCode()
		default:
			continue
		}
		if b := in.GetBranch(); (b == branch || b == "" && lane(t) == branch) && !slices.Contains(out, code) {
			out = append(out, code)
		}
	}
	if c := h.repairIn(project, branch, tasks, lane); c != nil && !slices.Contains(out, c.GetCode()) {
		out = append(out, c.GetCode())
	}
	return out
}

// pushQueued is mergesQueued read apart from the integration's pass, for the push of branch of the wish wishID in the
// project projectID: the lanes as they stand (laneKnown).
func (h *Harness) pushQueued(ctx context.Context, wishID, projectID, branch string) []string {
	wish, err1 := store.Get[*planv1.Wish](ctx, h.store, wishID)
	project, err2 := store.Get[*planv1.Project](ctx, h.store, projectID)
	tasks, err3 := store.List[*planv1.Task](ctx, h.store, store.Where{"wish_id": wishID})
	if err := errors.Join(err1, err2, err3); err != nil {
		if ctx.Err() == nil {
			logx.L().Error("read the wish, its project and its tasks", "loop", "push", "wish", wishID, "project", projectID, "err", err)
		}
		return nil
	}
	return h.queuedIn(project, branch, tasks, func(t *planv1.Task) string { return h.laneKnown(ctx, wish, project, t) })
}

// pushWaitsForMerges tells whether the push of branch, of wish in project, due for why, waits for the work queued to
// merge into it (Q39: "merged safely"): until all of it is merged and checked, however long (W281); then it goes, its
// push checks saying whether it is green. tasks are the wish's; committed, the tasks the push brings, are told when it
// begins to wait.
func (h *Harness) pushWaitsForMerges(
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, branch, why string, tasks, committed []*planv1.Task,
) bool {
	key := wish.GetId() + "/" + project.GetId() + "/" + branch
	queued := h.mergesQueued(ctx, wish, project, branch, tasks)
	if len(queued) == 0 {
		delete(h.pushWaits, key)
		return false
	}
	if !h.pushWaits[key] {
		h.pushWaits[key] = true
		h.pushSteps(ctx, committed, fmt.Sprintf("integration: a push of %s is due (%s); it waits for %s to merge",
			branch, why, strings.Join(queued, ", ")))
	}
	return true
}

// forgetPushWait forgets that a push of branch, of the wish wishID in the project projectID, waited: it is not due.
func (h *Harness) forgetPushWait(wishID, projectID, branch string) {
	delete(h.pushWaits, wishID+"/"+projectID+"/"+branch)
}

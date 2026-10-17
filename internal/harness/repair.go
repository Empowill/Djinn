package harness

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

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
// find what breaks the checks, which gets its own correction. A push due waits until the queue is merged and checked,
// pushQueueCap at most, then pushes if green.

// pushQueueCap is the longest a push due waits for the work queued to merge into its branch: a stream of merges never
// holds it back for good.
const pushQueueCap = 15 * time.Minute

// repairing tells whether c, a correction worker, still repairs its branch: it runs or waits to, or it ended done and
// its work waits to be merged, or is reviewed. One that failed, was cancelled, or stopped past stopGrace is over; so is
// one whose work is in, or whose merge failed: the correction after it, if any, repairs.
func (h *Harness) repairing(c *planv1.Task) bool {
	if c.GetCorrection() == nil {
		return false
	}
	if h.running(c.GetId()) {
		return true
	}
	switch c.GetStatus() {
	case planv1.TaskStatus_TASK_STATUS_FAILED, planv1.TaskStatus_TASK_STATUS_CANCELLED:
		return false
	case planv1.TaskStatus_TASK_STATUS_STOPPED:
		return h.stoppedLately(c)
	case planv1.TaskStatus_TASK_STATUS_DONE:
		in := c.GetIntegration()
		switch in.GetState() {
		case planv1.IntegrationState_INTEGRATION_STATE_PENDING, planv1.IntegrationState_INTEGRATION_STATE_INTEGRATING:
			return true
		case planv1.IntegrationState_INTEGRATION_STATE_UNCOMMITTED:
			return in.GetReviewedBy() != ""
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
		if branch, why := h.uncheckedBranch(ctx, wish, project, settings, t); why == "" {
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

// repairOf is the correction worker among tasks, all of wish's, that repairs branch in project; nil when none does.
func (h *Harness) repairOf(ctx context.Context, wish *planv1.Wish, project *planv1.Project, branch string, tasks []*planv1.Task) *planv1.Task {
	if branch == "" {
		return nil
	}
	for _, c := range tasks {
		if c.GetProjectId() == project.GetId() && h.repairing(c) && h.laneOf(ctx, wish, project, c) == branch {
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
// starts. Each committed batch checks whether the push is due. A repair's work in, and none after it, the running
// workers are told to merge the branch (tellRepaired). It returns the batches that moved the branch.
func (h *Harness) integrateLane(ctx context.Context, l *lane) []*mergedBatch {
	var merged []*mergedBatch
	var repairs []string // the repairs whose work moved the branch in this pass (repaired.go)
	commit := func(batch []*planv1.Task) bool {
		committed, mb := h.integrateBatch(ctx, l.wish, l.project, batch)
		if committed {
			h.pushIfDue(ctx, l.wish.GetId(), l.project)
			if mb != nil {
				merged = append(merged, mb)
			}
		}
		return mb != nil
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
				if commit(queue[own : own+1]) {
					repairs = append(repairs, queue[own].GetCode())
				}
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
	// Green again: the repair's work is in, with the work that waited for it, and no other repair began.
	if len(repairs) > 0 && ctx.Err() == nil && h.repairOn(ctx, l.wish, l.project, l.branch) == nil {
		h.tellRepaired(ctx, l.wish, l.project, l.branch, repairs)
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
// waiting or being merged, and the correction worker that repairs the branch.
func (h *Harness) mergesQueued(ctx context.Context, wish *planv1.Wish, project *planv1.Project, branch string, tasks []*planv1.Task) []string {
	var out []string
	for _, t := range tasks {
		in := t.GetIntegration()
		if t.GetProjectId() != project.GetId() || t.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
			continue
		}
		switch in.GetState() {
		case planv1.IntegrationState_INTEGRATION_STATE_PENDING, planv1.IntegrationState_INTEGRATION_STATE_INTEGRATING:
		default:
			continue
		}
		if b := in.GetBranch(); b == branch || b == "" && h.laneOf(ctx, wish, project, t) == branch {
			out = append(out, t.GetCode())
		}
	}
	if c := h.repairOf(ctx, wish, project, branch, tasks); c != nil && !slices.Contains(out, c.GetCode()) {
		out = append(out, c.GetCode())
	}
	return out
}

// pushWaitsForMerges tells whether the push of branch, of wish in project, due for why, waits for the work queued to
// merge into it (Q39: "merged safely"): until all of it is merged and checked, pushQueueCap at most since the push
// became due; then it goes, its push checks saying whether it is green. tasks are the wish's; committed, the tasks
// the push brings, are told when it begins to wait.
func (h *Harness) pushWaitsForMerges(
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, branch, why string, tasks, committed []*planv1.Task,
) bool {
	key := wish.GetId() + "/" + project.GetId() + "/" + branch
	queued := h.mergesQueued(ctx, wish, project, branch, tasks)
	if len(queued) == 0 {
		delete(h.pushWaits, key)
		return false
	}
	since, ok := h.pushWaits[key]
	if !ok {
		since = h.now()
		h.pushWaits[key] = since
		h.pushSteps(ctx, committed, fmt.Sprintf("integration: a push of %s is due (%s); it waits for %s to merge, %s at most",
			branch, why, strings.Join(queued, ", "), duration(pushQueueCap)))
	}
	if h.now().Sub(since) < pushQueueCap {
		return true
	}
	delete(h.pushWaits, key)
	h.pushSteps(ctx, committed, fmt.Sprintf("integration: the push of %s waited %s for %s to merge: it goes, if its push checks pass",
		branch, duration(pushQueueCap), strings.Join(queued, ", ")))
	return false
}

// forgetPushWait forgets that a push of branch, of the wish wishID in the project projectID, waited: it is not due.
func (h *Harness) forgetPushWait(wishID, projectID, branch string) {
	delete(h.pushWaits, wishID+"/"+projectID+"/"+branch)
}

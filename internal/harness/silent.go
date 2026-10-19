package harness

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
)

// A worker silent after its result (W272, decision Q55 A). On 17/10/2026 the correction W267 fixed the red e2e of
// feat/qol, committed, ran lint and the e2e green, wrote its final note, made one more call its permissions refused,
// and its turn ended; then it said nothing more while Djinn counted it running: a wait it had sent to the background
// (a loop on a log it had deleted) kept its agent alive, idle, until the lead stopped it 11 minutes later, and the
// wish's work waited for it. Neither watch caught it: the Lamplighter ends a silent worker only when its CPU does not
// move at all (an idle claude uses 1% to 3%), and the health flags one silent since its result only past
// StallAfterTurn, without stopping it.
//
// Now a worker whose turn ended with its result, silent since (no output, no tool running, no gate held or waited for)
// with its CPU idle (the health's readings, idleSince) for SilentAfterResult, whatever the cause (a refused call, a
// stuck tool, a lost stream), is stopped by the scheduler. A worker paused is never silent (W226): its pause and its
// resume are events, its silence starts again after them. If it had written its final note, it ends done, and its
// committed work goes to integration; otherwise it is continued once, on its session, told finishPrompt, and if it
// falls silent so again with no final note, it fails. The lead is told in one line either way.

// SilentAfterResult is how long a worker may stay silent after its turn's result, its CPU idle, before Djinn stops it:
// StallSilence, the health's own threshold (W188). Picked from the runs of the wish Djinn QoL (14 to 17/10/2026, 315
// results followed by an event): 7 silences after a result passed 5 minutes. Three waited for tests the worker had
// sent to the background, busy at 160% to 210% of a core (W7, 22.7 min; W53, 8.7; W135): the CPU test spares them.
// Two waited for a usage limit (W63, 18.6 min; W93, 12): a turn that ended on a limit or an error is told otherwise.
// W86 waited 9.5 minutes, idle (1.8%), for a monitor on the machine's load, then went on. W267 stayed idle (1.2%) 11
// minutes until the lead stopped it. Ten minutes spares the longest idle wait that went on, and stops W267.
const SilentAfterResult = StallSilence

// finishPrompt is what a worker stopped silent after its result, without a final note, is told on its session.
const finishPrompt = "Finish your task and write your final note."

// runSilent is why a run ended that Djinn stopped silent after its result to continue it (TaskRun.reason): once only.
const runSilent = "silent"

// endSilent stops the workers silent after their turn's result, their CPU idle, for SilentAfterResult. The caller
// holds h.sched; tasks are the pass's.
func (h *Harness) endSilent(tasks []*planv1.Task) {
	now := h.now()
	for _, t := range tasks {
		if t.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING || !judged(t) {
			continue
		}
		id := t.GetId()
		h.mu.Lock()
		r := h.runs[id]
		if r == nil || r.watcher || r.ifrit || r.light || !r.silenceCounts() || r.resultAt.IsZero() {
			h.mu.Unlock()
			continue
		}
		last, note := r.lastActivity, r.resultNote
		h.mu.Unlock()
		quiet := now.Sub(last)
		if quiet < SilentAfterResult {
			continue
		}
		if (h.held != nil && len(h.held(id)) > 0) || (h.waiting != nil && len(h.waiting(id)) > 0) {
			continue // A gated command runs, or waits for its turn.
		}
		mean, idle := h.idleSince(r, last)
		if !idle {
			continue // Its background work computes: tests, a build.
		}
		why := fmt.Sprintf("silent %d min after its result, CPU %.0f%%", int(math.Round(quiet.Minutes())), mean)
		h.mu.Lock()
		if !r.silenceCounts() || !r.lastActivity.Equal(last) {
			h.mu.Unlock()
			continue // Stopped, paused or heard from meanwhile.
		}
		r.silentWhy, r.silentNote = why, note
		worker := r.worker
		h.mu.Unlock()
		logx.L().Info("a worker silent after its result is stopped", "task", t.GetCode(), "why", why, "final note", note)
		if worker != nil {
			worker.Stop()
		}
	}
}

// silenceCounts tells whether the scheduler may stop r's worker for its silence: nobody stops it already, and it is
// not paused. The caller holds h.mu.
func (r *run) silenceCounts() bool {
	return !r.stopping && !r.final && !r.paused && !r.lamplighterEnd && r.silentWhy == ""
}

// resulted records that r's worker ended its turn with its result: its silence after it starts, a final note written
// in that turn or not, until a word of a new turn (said, in writeAs). A turn that ended on an error or a usage limit
// starts none: its end tells it. Runs where the pump records the worker's events.
func (h *Harness) resulted(r *run) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r.failure == "" && r.limit == nil {
		r.resultAt, r.resultNote = h.now(), r.answered
	}
}

// said ends the silence after its result of r's worker on an event of kind: a text, a tool call or a prompt starts a
// new turn. The caller holds h.mu.
func (r *run) said(kind planv1.TaskEventKind) {
	switch kind {
	case planv1.TaskEventKind_TASK_EVENT_KIND_TEXT, planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_CALL,
		planv1.TaskEventKind_TASK_EVENT_KIND_PROMPT:
		r.resultAt = time.Time{}
	}
}

// finishAsked tells whether t's worker was continued already, silent after its result with no final note: the run
// before the one that ends ended so.
func finishAsked(t *planv1.Task) bool {
	runs := t.GetRuns()
	if len(runs) > 0 && runs[len(runs)-1].GetEndTime() == nil {
		runs = runs[:len(runs)-1]
	}
	return len(runs) > 0 && runs[len(runs)-1].GetReason() == runSilent
}

// endedSilent gives t, whose worker Djinn stopped silent after its result for why, its status: done when it had
// written its final note; else continued once on its session, told finishPrompt; else failed.
func endedSilent(t *planv1.Task, why string, note bool) {
	switch {
	case note:
		t.Status, t.Error, t.ExitCode = planv1.TaskStatus_TASK_STATUS_DONE, "", 0
	case plan.CanResume(t.GetProvider()) && !finishAsked(t):
		t.Status, t.Error, t.Continuing, t.WaitReason, t.ResumeAfter = planv1.TaskStatus_TASK_STATUS_RESUMING, "", true, why+", with no final note", nil
	case plan.CanResume(t.GetProvider()):
		t.Status, t.Error = planv1.TaskStatus_TASK_STATUS_FAILED, why+", with no final note, once more after it was told to write it"
	default:
		t.Status, t.Error = planv1.TaskStatus_TASK_STATUS_FAILED, why+", with no final note; its agent cannot resume its session"
	}
}

// silentText is the status event of t's end, its worker stopped silent after its result for why.
func silentText(t *planv1.Task, why string) string {
	switch t.GetStatus() {
	case planv1.TaskStatus_TASK_STATUS_DONE:
		return "done: " + why + "; Djinn stopped it, its final note written"
	case planv1.TaskStatus_TASK_STATUS_RESUMING:
		return "resuming: " + t.GetWaitReason() + "; Djinn continues it once: " + finishPrompt
	}
	return "failed: " + t.GetError()
}

// SilentLine is the line that tells t's lead Djinn stopped its worker, silent after its result for why, and what
// follows; integrate says whether its work goes to integration. "" for a failure: the failure's own line tells it.
func SilentLine(t *planv1.Task, why string, integrate bool) string {
	switch t.GetStatus() {
	case planv1.TaskStatus_TASK_STATUS_DONE:
		what := "its committed work goes to integration"
		if !integrate {
			what = "nothing of it goes to integration"
		}
		return fmt.Sprintf("Djinn: %s %s: stopped and ended done, its final note written; %s.", t.GetCode(), why, what)
	case planv1.TaskStatus_TASK_STATUS_RESUMING:
		return fmt.Sprintf("Djinn: %s %s, with no final note: stopped and continued once, told to finish its task and "+
			"write its final note; silent so again, it fails.", t.GetCode(), why)
	}
	return ""
}

// tellSilent tells t's lead, once, that Djinn stopped its worker silent after its result.
func (h *Harness) tellSilent(t *planv1.Task, why string, integrate bool) {
	line := SilentLine(t, why, integrate)
	if line == "" || t.GetWishId() == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.toLead(ctx, planv1.LeadEventKind_LEAD_EVENT_KIND_HEALTH, t.GetWishId(), t.GetCode(), line); err != nil &&
		!errors.Is(err, plan.ErrNoLead) {
		logx.L().Warn("tell the lead", "wish", t.GetWishId(), "task", t.GetCode(), "err", err)
	}
}

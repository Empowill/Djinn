package harness

import (
	"context"
	"fmt"
	"log"
	"slices"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// Correction (T30): a batch whose merge conflicts in code, or whose tests fail, waits for no lead. Djinn starts a
// correction worker by itself: a work task part of the same azima as the failed task, of its provider, whose worktree
// starts on the failed merge, with what failed in its first prompt. Its branch integrates like any task's, and its
// success commits the failed work with it. Past the project's correction_attempts, Djinn asks the person a question
// on the wish, which blocks nothing else.

// The options of the question Djinn asks once the attempts are spent, answered by their letter.
const (
	retryOption = "Try again: Djinn starts a new correction worker on the failed merge"
	leaveOption = "Leave it: the work stays out of %s"
	takeOption  = "I take it: Djinn leaves the failed merge to you"
)

// failed records a batch whose merge conflicted in code or whose tests failed, in.Failure saying what: the tasks whose
// work the failure holds wait for a correction worker, which Djinn starts by itself, or for the person once the
// attempts are spent. The batch's tasks whose merge was not tried go back to wait for the next batch.
func (h *Harness) failed(
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, settings plan.Settings, batch []*planv1.Task,
	in *planv1.TaskIntegration, text string,
) {
	f := in.GetFailure()
	held := slices.DeleteFunc(slices.Clone(batch), func(t *planv1.Task) bool { return !slices.Contains(f.GetTaskIds(), t.GetId()) })
	if len(held) < len(batch) {
		untried := batch[len(held):]
		h.settleIntegration(ctx, untried, pending(in.GetBranch(), ""), fmt.Sprintf(
			"integration: waiting: not merged, %s having conflicted before; it goes with the next batch", held[len(held)-1].GetCode()), nil)
	}
	// A correction worker whose work failed in turn: the failure holds what it corrected, and its attempts count.
	var attempts int32
	var before []string
	for _, t := range held {
		if c := t.GetCorrection(); c != nil {
			attempts = max(attempts, c.GetAttempt())
			before = append(before, c.GetFailure().GetTaskIds()...)
		}
	}
	f.TaskIds = unique(append(before, f.GetTaskIds()...))
	group, err := tasksByID(ctx, h.store, f.GetTaskIds())
	if err != nil {
		log.Printf("djinn: integrate %s: %v", codes(held), err)
		group = held
	}
	h.correct(ctx, wish, project, settings, group, in, text, attempts, true)
}

// correct settles the integration of group, tasks whose work failed as in says, after attempts correction workers:
// another one starts when more are allowed and the project's correction_attempts are not spent; otherwise Djinn asks
// the person. text is the event that says what failed.
func (h *Harness) correct(
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, settings plan.Settings, group []*planv1.Task,
	in *planv1.TaskIntegration, text string, attempts int32, more bool,
) {
	in = proto.CloneOf(in)
	in.CorrectedBy, in.QuestionId, in.Attempts = "", "", attempts
	if more && int(attempts) < settings.CorrectionAttempts {
		c, err := h.spawnCorrection(ctx, wish, project, group, in, attempts+1)
		if err == nil {
			in.CorrectedBy, in.Attempts = c.GetId(), attempts+1
			h.settleIntegration(ctx, group, in, fmt.Sprintf("%s; %s corrects it, attempt %d of %d", text, c.GetCode(),
				attempts+1, settings.CorrectionAttempts), nil)
			return
		}
		text += "; the correction worker could not start: " + err.Error()
	}
	q, err := h.askIntegration(ctx, wish, group, in)
	if err != nil {
		log.Printf("djinn: integrate %s: ask: %v", codes(group), err)
	} else {
		in.QuestionId = q.GetId()
		text += "; Djinn asks you " + q.GetCode()
	}
	h.settleIntegration(ctx, group, in, text, nil)
}

// spawnCorrection starts the attempt-th correction worker of group, tasks whose work failed as in says: a work task of
// the failed task's provider, part of its azima, in a worktree on the failed merge, what failed in its first prompt.
func (h *Harness) spawnCorrection(
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, group []*planv1.Task, in *planv1.TaskIntegration, attempt int32,
) (*planv1.Task, error) {
	f := in.GetFailure()
	from := failedTask(group, f)
	settings, err := plan.LoadSettings(h.home, project)
	if err != nil {
		return nil, err
	}
	return h.spawn(context.WithoutCancel(ctx), planv1connect.TaskServiceSpawnProcedure, &planv1.TaskServiceSpawnRequest{
		WishId: wish.GetId(), ProjectId: project.GetId(), Title: correctionTitle(group, f, in.GetBranch()),
		Prompt:   correctionPrompt(group, f, in.GetBranch(), settings, attempt),
		Provider: from.GetProvider(), Model: from.GetModel(), PartOf: from.GetPartOf(),
	}, &planv1.TaskCorrection{Failure: proto.CloneOf(f), Attempt: attempt})
}

// failedTask is the task of group whose work failed: the one whose merge conflicted, or the first one not a
// correction when the tests failed.
func failedTask(group []*planv1.Task, f *planv1.IntegrationFailure) *planv1.Task {
	if b := f.GetMergeBranch(); b != "" {
		if i := slices.IndexFunc(group, func(t *planv1.Task) bool { return t.GetBranch() == b }); i >= 0 {
			return group[i]
		}
	}
	if i := slices.IndexFunc(group, func(t *planv1.Task) bool { return t.GetCorrection() == nil }); i >= 0 {
		return group[i]
	}
	return group[0]
}

// originals are the tasks of group that are no correction workers: the work the corrections are for.
func originals(group []*planv1.Task) []*planv1.Task {
	out := slices.DeleteFunc(slices.Clone(group), func(t *planv1.Task) bool { return t.GetCorrection() != nil })
	if len(out) == 0 {
		return group
	}
	return out
}

// correctionTitle is the title of a correction worker of group's failure f, into branch.
func correctionTitle(group []*planv1.Task, f *planv1.IntegrationFailure, branch string) string {
	if f.GetState() == planv1.IntegrationState_INTEGRATION_STATE_RED {
		return "Make the tests pass with the work of " + codes(originals(group))
	}
	return fmt.Sprintf("Settle the conflict of %s with %s", failedTask(group, f).GetCode(), branch)
}

// correctionPrompt is the first prompt of the attempt-th correction worker of group's failure f, into branch: what
// failed, the files, the command.
func correctionPrompt(group []*planv1.Task, f *planv1.IntegrationFailure, branch string, settings plan.Settings, attempt int32) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Djinn could not integrate the work of %s into %s: %s.\n\n", codes(group), branch, headline(f.GetReason()))
	if f.GetState() == planv1.IntegrationState_INTEGRATION_STATE_RED {
		fmt.Fprintf(&b, "You correct it. Your worktree is on that merge: their work merged onto %s, where the tests failed. "+
			"The command, run in the project's folder: `%s`. Its output ends with:\n\n", branch, f.GetCommand())
		for line := range strings.Lines(strings.TrimPrefix(f.GetOutput(), ":\n")) {
			b.WriteString("    " + line)
		}
		b.WriteString("\n\nMake the tests pass without undoing what the tasks did.")
	} else {
		fmt.Fprintf(&b, "You correct it. Your worktree is on that merge: the merge of %s is under way, its conflicts left "+
			"in place, in:\n\n", f.GetMergeBranch())
		for _, file := range f.GetFiles() {
			b.WriteString("- " + file + "\n")
		}
		b.WriteString("\nSettle each conflict keeping what both sides meant, and leave no conflict marker.")
		if settings.Generate != "" && len(settings.Generated) > 0 {
			fmt.Fprintf(&b, " Files code generation makes (%s) are not settled by hand: run `%s` once the code is settled.",
				strings.Join(settings.Generated, ", "), settings.Generate)
		}
	}
	fmt.Fprintf(&b, " Do not commit: when you end, Djinn commits what you leave, which concludes the merge, then integrates "+
		"your branch like any task's, the tests (`%s`) run through a gate. Its success brings the work of %s in with yours.",
		settings.Test, codes(group))
	if attempt > 1 {
		fmt.Fprintf(&b, "\n\nThis is attempt %d: the correction before yours failed.", attempt)
	}
	return b.String()
}

// headline is the first line of a reason, without the colon that introduces what follows.
func headline(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return strings.TrimSuffix(line, ":")
}

// askIntegration asks the person, on wish, what to do with the work of group, which failed as in says once the
// correction attempts were spent: try again, leave it, or take it.
func (h *Harness) askIntegration(ctx context.Context, wish *planv1.Wish, group []*planv1.Task, in *planv1.TaskIntegration) (*planv1.Question, error) {
	f := in.GetFailure()
	tried := fmt.Sprintf("Djinn started %d correction workers, one after the other: none got it in.", in.GetAttempts())
	switch in.GetAttempts() {
	case 0:
		tried = "The project's settings start no correction worker (correction_attempts: 0)."
	case 1:
		tried = "Djinn started a correction worker: it did not get it in."
	}
	var ctxt strings.Builder
	fmt.Fprintf(&ctxt, "**What failed.** %s\n\n", f.GetReason())
	if len(f.GetFiles()) > 0 {
		fmt.Fprintf(&ctxt, "**The files in conflict.** %s\n\n", strings.Join(f.GetFiles(), ", "))
	}
	if f.GetCommand() != "" {
		fmt.Fprintf(&ctxt, "**The command.** `%s`\n\n", f.GetCommand())
	}
	var corrections []string
	for _, t := range group {
		if t.GetCorrection() != nil {
			corrections = append(corrections, t.GetCode())
		}
	}
	if len(corrections) > 0 {
		fmt.Fprintf(&ctxt, "**The attempts.** %s, whose work failed in turn.\n\n", strings.Join(corrections, ", "))
	}
	if f.GetMergeBranch() != "" {
		fmt.Fprintf(&ctxt, "**To take it.** `git switch --detach %s`, then `git merge %s`: the conflicts are yours to settle.\n\n",
			short8(f.GetBase()), f.GetMergeBranch())
	} else {
		fmt.Fprintf(&ctxt, "**To take it.** `git switch --detach %s`: the work merged, where `%s` fails.\n\n", short8(f.GetBase()), f.GetCommand())
	}
	fmt.Fprintf(&ctxt, "The rest of the wish goes on meanwhile: only this work waits, out of %s.", in.GetBranch())
	q := &planv1.Question{
		Id: store.NewID(), WishId: wish.GetId(), CreateTime: timestamppb.New(h.now()), Icon: "🔀",
		Text: fmt.Sprintf("The work of %s does not go into %s: %s. %s What should Djinn do?", codes(originals(group)),
			in.GetBranch(), headline(f.GetReason()), tried),
		Context: ctxt.String(),
		Options: []string{retryOption, fmt.Sprintf(leaveOption, in.GetBranch()), takeOption},
	}
	err := h.store.Tx(context.WithoutCancel(ctx), func(tx *store.Tx) error {
		if err := tx.Journal(actorHarness, methodIntegrate, q); err != nil {
			return err
		}
		return plan.Ask(ctx, tx, q)
	})
	if err != nil {
		return nil, err
	}
	h.notify()
	return q, nil
}

// settleCorrected records the work the correction workers of batch corrected as committed with theirs, in in.Sha:
// "corrected by W5". It returns the tasks whose work that is.
func (h *Harness) settleCorrected(ctx context.Context, batch []*planv1.Task, in *planv1.TaskIntegration) []*planv1.Task {
	var out []*planv1.Task
	for _, c := range batch {
		if c.GetCorrection() == nil {
			continue
		}
		group, err := tasksByID(ctx, h.store, c.GetCorrection().GetFailure().GetTaskIds())
		if err != nil {
			log.Printf("djinn: integrate %s: %v", c.GetCode(), err)
			continue
		}
		h.settleIntegration(ctx, group, &planv1.TaskIntegration{
			State: planv1.IntegrationState_INTEGRATION_STATE_COMMITTED, Branch: in.GetBranch(), Sha: in.GetSha(),
			Reason: "corrected by " + c.GetCode(), CorrectedBy: c.GetId(), Attempts: c.GetCorrection().GetAttempt(),
		}, fmt.Sprintf("integration: committed into %s as %s, corrected by %s", in.GetBranch(), short8(in.GetSha()), c.GetCode()), nil)
		out = append(out, group...)
	}
	return out
}

// correctedIDs are the tasks the correction workers of batch corrected.
func correctedIDs(batch []*planv1.Task) []string {
	var out []string
	for _, t := range batch {
		out = append(out, t.GetCorrection().GetFailure().GetTaskIds()...)
	}
	return out
}

// correctionsEnded settles the work of the correction workers among tasks that ended without being done: one that
// failed counts as an attempt, the next one starting while the attempts are not spent; one stopped leaves it to the
// person, asked.
func (h *Harness) correctionsEnded(ctx context.Context, tasks []*planv1.Task) {
	for _, c := range tasks {
		failed := c.GetStatus() == planv1.TaskStatus_TASK_STATUS_FAILED
		if c.GetCorrection() == nil || !failed && c.GetStatus() != planv1.TaskStatus_TASK_STATUS_STOPPED || h.running(c.GetId()) {
			continue
		}
		var group []*planv1.Task
		for _, t := range tasks {
			in := t.GetIntegration()
			if in.GetCorrectedBy() == c.GetId() && in.GetQuestionId() == "" && (in.GetState() == planv1.IntegrationState_INTEGRATION_STATE_CONFLICT ||
				in.GetState() == planv1.IntegrationState_INTEGRATION_STATE_RED) {
				group = append(group, t)
			}
		}
		if len(group) == 0 {
			continue
		}
		wish, project, settings, err := h.integrationOf(ctx, c)
		if err != nil {
			log.Printf("djinn: integrate %s: %v", c.GetCode(), err)
			continue
		}
		text := "integration: " + c.GetCode() + ", its correction worker, was stopped"
		if failed {
			text = "integration: " + c.GetCode() + ", its correction worker, failed: " + c.GetError()
		}
		h.correct(ctx, wish, project, settings, group, group[0].GetIntegration(), text, c.GetCorrection().GetAttempt(), failed)
	}
}

// integrationOf reads the wish and the project of t, and the project's settings.
func (h *Harness) integrationOf(ctx context.Context, t *planv1.Task) (*planv1.Wish, *planv1.Project, plan.Settings, error) {
	wish, err := store.Get[*planv1.Wish](ctx, h.store, t.GetWishId())
	if err != nil {
		return nil, nil, plan.Settings{}, err
	}
	project, err := store.Get[*planv1.Project](ctx, h.store, t.GetProjectId())
	if err != nil {
		return nil, nil, plan.Settings{}, err
	}
	settings, err := plan.LoadSettings(h.home, project)
	return wish, project, settings, err
}

// answerIntegration takes the person's answer to a question Djinn asked once a failed integration's attempts were
// spent: A starts a new correction worker, its attempts counted again from one; B leaves the work out; C leaves it to
// the person.
func (h *Harness) answerIntegration(ctx context.Context, q *planv1.Question) {
	tasks, err := store.List[*planv1.Task](ctx, h.store, store.Where{"wish_id": q.GetWishId()})
	if err != nil {
		log.Printf("djinn: question %s: %v", q.GetCode(), err)
		return
	}
	group := slices.DeleteFunc(tasks, func(t *planv1.Task) bool { return t.GetIntegration().GetQuestionId() != q.GetId() })
	if len(group) == 0 {
		return
	}
	slices.SortFunc(group, func(a, b *planv1.Task) int { return a.GetCreateTime().AsTime().Compare(b.GetCreateTime().AsTime()) })
	in := proto.CloneOf(group[0].GetIntegration())
	switch q.GetAnswer().GetChoice() {
	case planv1.Choice_CHOICE_A:
		wish, project, settings, err := h.integrationOf(ctx, group[0])
		if err != nil {
			log.Printf("djinn: question %s: %v", q.GetCode(), err)
			return
		}
		settings.CorrectionAttempts = max(settings.CorrectionAttempts, 1) // Asked for, even where none starts by itself.
		h.correct(ctx, wish, project, settings, group, in, "integration: you said to try again ("+q.GetCode()+")", 0, true)
	case planv1.Choice_CHOICE_B:
		in.Reason = "left out (" + q.GetCode() + "): " + in.GetReason()
		h.settleIntegration(ctx, group, in, "integration: left out of "+in.GetBranch()+" ("+q.GetCode()+")", nil)
	case planv1.Choice_CHOICE_C:
		in.Reason = "you take it (" + q.GetCode() + "): " + in.GetReason()
		h.settleIntegration(ctx, group, in, "integration: you take it ("+q.GetCode()+"); it stays out of "+in.GetBranch()+
			" until you bring it in", nil)
	}
}

// unique is ids without repeats, in the order they first come.
func unique(ids []string) []string {
	var out []string
	for _, id := range ids {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// tasksByID reads the tasks ids, in their order.
func tasksByID(ctx context.Context, r store.Reader, ids []string) ([]*planv1.Task, error) {
	out := make([]*planv1.Task, 0, len(ids))
	for _, id := range ids {
		t, err := store.Get[*planv1.Task](ctx, r, id)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

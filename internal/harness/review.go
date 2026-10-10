package harness

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// Review (T07): a task whose worktree holds changes not committed is not merged, and nothing commits them blindly:
// debug output, scratch files or artifacts would go in with the work. Djinn starts a review worker by itself: a work
// task part of the same azima, of the task's provider, in that task's worktree and on its branch, the files and their
// diff in its first prompt. It commits what belongs to the task and drops the rest; its branch then integrates like any
// task's, and the reviewed work with it. Past the project's correction_attempts, Djinn asks the person.

// reviewRetryOption is the first option of the question Djinn asks once the review attempts are spent.
const reviewRetryOption = "Try again: Djinn starts a new review worker on the work not committed"

// How much of a task's prompt, and of the diff of its work not committed, a review worker's first prompt holds.
const (
	maxReviewPrompt = 4 << 10
	maxReviewDiff   = 12 << 10
)

// uncommitted lists what the worktree of t holds not committed, as git status --porcelain says it, untracked files
// one by one, the project's .gitignore applying: none when it is clean. A worktree removed already holds nothing.
func uncommitted(ctx context.Context, t *planv1.Task) ([]string, error) {
	wt := t.GetWorktree()
	if wt == "" {
		return nil, nil
	}
	if _, err := os.Stat(wt); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	out, err := gitRaw(ctx, wt, "status", "--porcelain", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	var lines []string
	fields := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 4 {
			continue
		}
		if f[0] == 'R' || f[0] == 'C' { // A rename or a copy: the path it came from follows.
			if i+1 < len(fields) {
				f += " <- " + fields[i+1]
			}
			i++
		}
		lines = append(lines, f)
	}
	return lines, nil
}

// statusPaths are the files of status lines, as uncommitted gives them.
func statusPaths(status []string) []string {
	out := make([]string, 0, len(status))
	for _, s := range status {
		p, _, _ := strings.Cut(s[3:], " <- ")
		out = append(out, p)
	}
	return out
}

// reviewUncommitted takes out of batch, tasks of wish in project whose work would be merged into branch, the ones whose
// worktree holds changes not committed: none of them is merged, and a review worker starts for each. It returns the
// others.
func (h *Harness) reviewUncommitted(
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, settings plan.Settings, branch string, batch []*planv1.Task,
) []*planv1.Task {
	var clean []*planv1.Task
	for _, t := range batch {
		status, err := uncommitted(ctx, t)
		if err != nil {
			why := "read its worktree: " + err.Error()
			h.settleIntegration(ctx, []*planv1.Task{t}, pending(branch, why), "integration: waiting: "+why, nil)
			continue
		}
		if len(status) == 0 {
			clean = append(clean, t)
			continue
		}
		// A review whose own work is left not committed: the next one reviews the same work, its attempts counted.
		group := []*planv1.Task{t}
		var attempts int32
		if r := t.GetReview(); r != nil {
			attempts = r.GetAttempt()
			before, err := tasksByID(ctx, h.store, r.GetTaskIds())
			if err != nil {
				log.Printf("djinn: integrate %s: %v", t.GetCode(), err)
			}
			group = append(before, t)
		}
		files := strings.Join(statusPaths(status), ", ")
		in := &planv1.TaskIntegration{
			State: planv1.IntegrationState_INTEGRATION_STATE_UNCOMMITTED, Branch: branch, Reason: "uncommitted: " + files,
		}
		h.review(ctx, wish, project, settings, group, in, fmt.Sprintf(
			"integration: uncommitted: %s's worktree holds changes not committed (%s); not merged, nothing committed", t.GetCode(), files),
			attempts, true)
	}
	return clean
}

// review settles the integration of group, tasks whose work is left not committed in the worktree of the first one,
// after attempts review workers: another one starts when more are allowed and the project's correction_attempts are
// not spent; otherwise Djinn asks the person. text is the event that says why. It says what Djinn did, for the lead.
func (h *Harness) review(
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, settings plan.Settings, group []*planv1.Task,
	in *planv1.TaskIntegration, text string, attempts int32, more bool,
) string {
	if status, err := uncommitted(ctx, group[0]); err == nil && len(status) == 0 {
		// Committed meanwhile (a review that failed after its commit, the person): the work waits to be merged.
		left := group[0].GetCode() + "'s worktree holds nothing not committed any more: it waits to be merged"
		h.settleIntegration(ctx, group, pending(in.GetBranch(), ""), text+"; "+left, nil)
		h.kickIntegrate()
		return "Djinn starts no review: " + left
	}
	in = proto.CloneOf(in)
	in.State, in.ReviewedBy, in.QuestionId, in.Attempts = planv1.IntegrationState_INTEGRATION_STATE_UNCOMMITTED, "", "", attempts
	var unstarted error
	if more && int(attempts) < settings.CorrectionAttempts {
		r, err := h.spawnReview(ctx, wish, project, group, in.GetBranch(), attempts+1)
		if err == nil {
			in.ReviewedBy, in.Attempts, in.Reason = r.GetId(), attempts+1, "uncommitted: reviewed by "+r.GetCode()
			h.settleIntegration(ctx, group, in, fmt.Sprintf("%s; %s reviews it, attempt %d of %d", text, r.GetCode(),
				attempts+1, settings.CorrectionAttempts), nil)
			return didStart(r)
		}
		unstarted = err
		text += "; the review worker could not start: " + err.Error()
	}
	q, err := h.askReview(ctx, wish, group, in, unstarted)
	if err != nil {
		log.Printf("djinn: integrate %s: ask: %v", codes(group), err)
	} else {
		in.QuestionId = q.GetId()
		text += "; Djinn asks you " + q.GetCode()
	}
	h.settleIntegration(ctx, group, in, text, nil)
	return didAsk("review", unstarted, q)
}

// spawnReview starts the attempt-th review worker of group's work, left not committed in the worktree of its first task:
// a work task of that task's provider, part of its azima, in that worktree and on its branch, the files and their diff
// in its first prompt.
func (h *Harness) spawnReview(
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, group []*planv1.Task, branch string, attempt int32,
) (*planv1.Task, error) {
	from := group[0]
	status, err := uncommitted(ctx, from)
	if err != nil {
		return nil, err
	}
	if len(status) == 0 {
		return nil, errors.New(from.GetCode() + "'s worktree holds nothing not committed any more")
	}
	settings, err := plan.LoadSettings(h.home, project)
	if err != nil {
		return nil, err
	}
	prompt, err := firstPrompt(h.store, from.GetId())
	if err != nil {
		prompt = "(" + err.Error() + ")"
	}
	review := &planv1.TaskReview{TaskIds: ids(group), Attempt: attempt}
	azima, note := h.azimaOf(ctx, from)
	r, err := h.spawn(context.WithoutCancel(ctx), planv1connect.TaskServiceSpawnProcedure, &planv1.TaskServiceSpawnRequest{
		WishId: wish.GetId(), ProjectId: project.GetId(), Title: "Review the work " + from.GetCode() + " left not committed",
		Prompt:   reviewPrompt(from, prompt, status, uncommittedDiff(ctx, from.GetWorktree(), status), branch, settings, attempt),
		Provider: from.GetProvider(), Model: from.GetModel(), PartOf: azima,
	}, &planv1.Task{Review: review, Branch: from.GetBranch(), Worktree: from.GetWorktree()})
	if err == nil && note != "" {
		h.Note(r.GetId(), Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: note})
	}
	return r, err
}

// reviewPrompt is the first prompt of the attempt-th review worker of the work from left not committed, as status and
// diff say it, on its way into branch: the task, its files, what to keep and what to drop.
func reviewPrompt(from *planv1.Task, prompt string, status []string, diff, branch string, settings plan.Settings, attempt int32) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Djinn does not integrate the work of %s into %s as it is: its worktree, where you are, on its branch %s, "+
		"holds changes no commit has. Djinn commits nothing blindly: you judge them.\n\n", from.GetCode(), branch, from.GetBranch())
	fmt.Fprintf(&b, "The task, %s: %s. Its prompt:\n\n%s\n\n", from.GetCode(), from.GetTitle(), indent(clipTo(prompt, maxReviewPrompt)))
	fmt.Fprintf(&b, "The files not committed (`git status --porcelain`):\n\n%s\n\n", indent(strings.Join(status, "\n")))
	fmt.Fprintf(&b, "Their diff, against the branch's last commit:\n\n%s\n\n", indent(diff))
	fmt.Fprintf(&b, "Commit what belongs to the task, with a message in the repository's style (`git log` shows it). Revert "+
		"(`git restore`) or delete what does not: debug output, scratch files, build artifacts. Leave nothing not committed: "+
		"`git status --porcelain` prints nothing when you end. Do not push: Djinn integrates the branch like any task's, its "+
		"commit checks (%s) run through their gates. End with one line saying what you kept, what you dropped, and why.",
		settings.CommitGates())
	if attempt > 1 {
		fmt.Fprintf(&b, "\n\nThis is attempt %d: the review before yours left changes not committed.", attempt)
	}
	return b.String()
}

// uncommittedDiff is what the worktree wt holds not committed, status saying which files, as a diff clipped to
// maxReviewDiff bytes: the changes to tracked files against HEAD, then the text of each untracked one.
func uncommittedDiff(ctx context.Context, wt string, status []string) string {
	var b strings.Builder
	if d, err := gitRaw(ctx, wt, "diff", "--no-color", "--no-ext-diff", "HEAD"); err != nil {
		fmt.Fprintf(&b, "(%v)\n", err)
	} else {
		b.WriteString(d)
	}
	for _, s := range status {
		if b.Len() > maxReviewDiff {
			break
		}
		if !strings.HasPrefix(s, "??") {
			continue
		}
		name := s[3:]
		f, err := os.Open(filepath.Join(wt, filepath.FromSlash(name)))
		if err != nil {
			fmt.Fprintf(&b, "new file %s: %v\n", name, err)
			continue
		}
		data, err := io.ReadAll(io.LimitReader(f, int64(maxReviewDiff-b.Len()+1)))
		_ = f.Close()
		switch {
		case err != nil:
			fmt.Fprintf(&b, "new file %s: %v\n", name, err)
		case bytes.IndexByte(data, 0) >= 0:
			fmt.Fprintf(&b, "new file %s: binary\n", name)
		default:
			fmt.Fprintf(&b, "--- /dev/null\n+++ b/%s\n", name)
			for line := range strings.Lines(string(data)) {
				b.WriteString("+" + line)
			}
			if len(data) > 0 && data[len(data)-1] != '\n' {
				b.WriteString("\n")
			}
		}
	}
	return strings.TrimRight(clipTo(b.String(), maxReviewDiff), "\n")
}

// clipTo is s cut to n bytes, saying how much it left out.
func clipTo(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + fmt.Sprintf("\n… (%d bytes cut)", len(s)-n)
}

// indent puts four spaces before each line of s.
func indent(s string) string {
	var b strings.Builder
	for line := range strings.Lines(s) {
		b.WriteString("    " + line)
	}
	return b.String()
}

// askReview asks the person, on wish, what to do with the work of group, left not committed once the review attempts
// were spent, or once unstarted kept the next review worker from starting: try again, leave it, or take it.
func (h *Harness) askReview(
	ctx context.Context, wish *planv1.Wish, group []*planv1.Task, in *planv1.TaskIntegration, unstarted error,
) (*planv1.Question, error) {
	from := group[0]
	tried := fmt.Sprintf("Djinn started %d review workers, one after the other: none left it committed.", in.GetAttempts())
	switch in.GetAttempts() {
	case 0:
		tried = "The project's settings start no review worker (correction_attempts: 0)."
	case 1:
		tried = "Djinn started a review worker: it did not leave it committed."
	}
	tried = unstartedText(tried, "review", in.GetAttempts(), unstarted)
	var ctxt strings.Builder
	if status, err := uncommitted(ctx, from); err == nil && len(status) > 0 {
		fmt.Fprintf(&ctxt, "**The files not committed.** %s\n\n", strings.Join(statusPaths(status), ", "))
	}
	if len(group) > 1 {
		fmt.Fprintf(&ctxt, "**The attempts.** %s, whose work was left not committed in turn.\n\n", codes(group[1:]))
	}
	fmt.Fprintf(&ctxt, "**To take it.** In `%s`, on the branch `%s`: commit what belongs to %s, drop the rest.\n\n",
		from.GetWorktree(), from.GetBranch(), from.GetCode())
	fmt.Fprintf(&ctxt, "The rest of the wish goes on meanwhile: only this work waits, out of %s.", in.GetBranch())
	q := &planv1.Question{
		Id: store.NewID(), WishId: wish.GetId(), CreateTime: timestamppb.New(h.now()), Icon: "🔀",
		Text: fmt.Sprintf("The work of %s does not go into %s: its worktree holds changes not committed. %s What should Djinn do?",
			from.GetCode(), in.GetBranch(), tried),
		Context: ctxt.String(),
		Options: []string{reviewRetryOption, fmt.Sprintf(leaveOption, in.GetBranch()), takeOption},
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

// settleReviewed records the work the review workers of batch reviewed as committed with theirs, in in.Sha:
// "uncommitted: reviewed by W5". It returns the tasks it settled.
func (h *Harness) settleReviewed(ctx context.Context, batch []*planv1.Task, in *planv1.TaskIntegration) []*planv1.Task {
	var out []*planv1.Task
	for _, r := range batch {
		if r.GetReview() == nil {
			continue
		}
		group, err := tasksByID(ctx, h.store, r.GetReview().GetTaskIds())
		if err != nil {
			log.Printf("djinn: integrate %s: %v", r.GetCode(), err)
			continue
		}
		h.settleIntegration(ctx, group, &planv1.TaskIntegration{
			State: planv1.IntegrationState_INTEGRATION_STATE_COMMITTED, Branch: in.GetBranch(), Sha: in.GetSha(),
			Reason: "uncommitted: reviewed by " + r.GetCode(), ReviewedBy: r.GetId(), Attempts: r.GetReview().GetAttempt(),
		}, fmt.Sprintf("integration: committed into %s as %s, its work not committed reviewed by %s", in.GetBranch(),
			short8(in.GetSha()), r.GetCode()), nil)
		out = append(out, group...)
	}
	return out
}

// reviewedIDs are the tasks the review workers of batch reviewed.
func reviewedIDs(batch []*planv1.Task) []string {
	var out []string
	for _, t := range batch {
		out = append(out, t.GetReview().GetTaskIds()...)
	}
	return out
}

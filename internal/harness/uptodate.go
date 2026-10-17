package harness

import (
	"context"
	"fmt"
	"strings"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/store"
)

// A restarted worker first brings its branch up to date (W250): a worker stopped and started again (a continue, a
// resume after its usage limit or a Djinn restart, a wake of the Lamplighter or after a stream went idle, a wish
// paused then resumed) may resume on a branch cut long ago, while other work went into the branch its own merges
// into (laneOf: the wish's integration branch, its azima's branch, or the reference branch). The first line of what it
// is told on resuming asks it to rebase its branch onto that branch, listing what came in, as W184's message does:
// commit or set aside its work in progress, rebase, settle any conflict with its own changes in mind, run the tests
// that cover what it touched. A worker paused where it was (by the load notch, for an investigator's slot, or by
// hand) is told so between two turns once resumed, but only after a pause of PauseRebaseAfter or more. Djinn never
// rebases in a worker's worktree, as W184 decided for a repaired branch: only the worker knows its changes. Nothing
// is said when the branch lacks no commit of its target. The task's events say that the worker was asked, then that
// its branch took the target, from which commit to which (rebased).

// PauseRebaseAfter is how long a worker must have been paused for its resume to ask it to rebase: a shorter pause
// seldom lets work in, and a worker held for a minute by the load notch is not to be interrupted for it. As long as
// the Lamplighter lets a worker be silent before it looks at it (SilenceLimit).
const PauseRebaseAfter = 15 * time.Minute

// methodRebase is how the journal records the message Djinn gives a worker resumed after a long pause, asking it to
// rebase; the request is the message, as djinn task send's.
const methodRebase = "harness/rebase"

// behind is what a worker's branch lacks of the branch its work merges into, when it was asked to take it.
type behind struct {
	repo     string   // the project's folder, where the branches are
	branch   string   // the worker's branch
	from     string   // its head when the worker was asked
	target   string   // the branch its work merges into
	head     string   // the target's head when the worker was asked
	subjects []string // the subjects of the target's commits the branch lacks, the newest first
}

// behindTarget is what t's branch lacks of the branch its work merges into in project; nil when it lacks nothing,
// when the worker cannot or need not rebase (one that may not run Git's commands, a watcher, a question worker, an
// ifrit, a correction or a review worker, as W184), or when the target is not known.
func (h *Harness) behindTarget(ctx context.Context, r *run, t *planv1.Task, project *planv1.Project) *behind {
	if !mayRebase(t.GetAccess()) || project == nil || !project.GetGit() || t.GetWorktree() == "" || t.GetBranch() == "" ||
		r.watcher || r.light || r.ifrit || t.GetCorrection() != nil || t.GetReview() != nil {
		return nil
	}
	wish, err := store.Get[*planv1.Wish](ctx, h.store, t.GetWishId())
	if err != nil {
		return nil
	}
	target := h.laneOf(ctx, wish, project, t)
	if target == "" || target == t.GetBranch() {
		return nil
	}
	repo := project.GetDirectory()
	head, err := git(ctx, repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+target)
	if err != nil || head == "" {
		return nil
	}
	from, err := git(ctx, repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+t.GetBranch())
	if err != nil || from == "" || isAncestor(ctx, repo, head, from) {
		return nil
	}
	// Merges alone bring nothing to take: the merge of the task's own work, done at its end, before a continue.
	out, err := git(ctx, repo, "log", "--no-merges", "--topo-order", "--format=%s", from+".."+head)
	if err != nil {
		logx.L().Warn("read what a worker's branch lacks", "task", t.GetCode(), "err", err)
		return nil
	}
	if out == "" {
		return nil
	}
	return &behind{repo: repo, branch: t.GetBranch(), from: from, target: target, head: head, subjects: strings.Split(out, "\n")}
}

// mayRebase tells whether a worker of access may rebase its branch: it edits, and runs Git's commands; a read-only
// worker does not, nor one granted the edits alone, nor one held to Djinn's commands (accessSpec).
func mayRebase(access planv1.TaskAccess) bool {
	readOnly, _ := accessSpec(access, nil)
	return !readOnly && access != planv1.TaskAccess_TASK_ACCESS_EDIT_GRANTED && access != planv1.TaskAccess_TASK_ACCESS_DJINN
}

// rebaseText asks a worker to rebase its branch onto b's target, before anything else: its first line says what to
// do, the next ones what came in.
func rebaseText(b *behind) string {
	var s strings.Builder
	fmt.Fprintf(&s, "Djinn: before you go on, rebase your branch onto %s: commit what you have in progress (or let "+
		"git rebase --autostash %s set it aside and restore it), then git rebase %s; resolve any conflict with your "+
		"own changes in mind, then run the tests that cover what you touched. %s holds work your branch lacks:\n",
		b.target, b.target, b.target, b.target)
	cameIn(&s, b.subjects)
	return strings.TrimSuffix(s.String(), "\n")
}

// cameIn lists subjects, the newest first, repairedSubjects of them at most.
func cameIn(s *strings.Builder, subjects []string) {
	for i, subject := range subjects {
		if i == repairedSubjects {
			fmt.Fprintf(s, "- and %d more\n", len(subjects)-i)
			break
		}
		fmt.Fprintf(s, "- %s\n", subject)
	}
}

// askedText is the event that says the worker was asked to rebase onto b's target.
func askedText(b *behind) string {
	commits := "commits"
	if len(b.subjects) == 1 {
		commits = "commit"
	}
	return fmt.Sprintf("asked to rebase %s (at %s) onto %s (at %s): %d %s came in", b.branch, shortSHA(b.from),
		b.target, shortSHA(b.head), len(b.subjects), commits)
}

// shortSHA is a commit's name as the events say it.
func shortSHA(sha string) string { return sha[:min(8, len(sha))] }

// rebasePending makes r's worker owe the rebase b, which the events say it was asked; the pump checks it is done
// (rebased). The caller owns the run's task.
func (h *Harness) rebasePending(r *run, b *behind) {
	r.rebase = b
	h.write(r, actorHarness, methodEvent, nil, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: askedText(b)})
}

// rebased says, once r's worker brought its branch up to date with the target's head it was asked to take, from which
// commit to which its branch went; the pump calls it after a tool's result and as the worker ends. A worker that
// merged the target instead is said to have merged it.
func (h *Harness) rebased(r *run) {
	b := r.rebase
	if b == nil {
		return
	}
	ctx := context.WithoutCancel(h.ctx)
	now, err := git(ctx, b.repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+b.branch)
	if err != nil || now == "" || now == b.from || !isAncestor(ctx, b.repo, b.head, now) {
		return
	}
	r.rebase = nil
	how := "rebased"
	if isAncestor(ctx, b.repo, b.from, now) && !isAncestor(ctx, b.repo, b.from, b.head) {
		how = "merged" // Its own commits kept as they were, under a merge.
	}
	text := fmt.Sprintf("%s %s onto %s (at %s): %s → %s", how, b.branch, b.target, shortSHA(b.head), shortSHA(b.from),
		shortSHA(now))
	h.write(r, actorHarness, methodEvent, nil, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: text})
}

// rebaseAfterPause asks r's worker, resumed after a pause of paused, to rebase when the pause lasted PauseRebaseAfter
// or more and its branch lacks commits of its target: between two turns, as W184's message. The pump calls it.
func (h *Harness) rebaseAfterPause(r *run, paused time.Duration) {
	if paused < PauseRebaseAfter || r.rebase != nil {
		return
	}
	t := r.task
	ctx := context.WithoutCancel(h.ctx)
	project, err := store.Get[*planv1.Project](ctx, h.store, t.GetProjectId())
	if err != nil {
		return
	}
	b := h.behindTarget(ctx, r, t, project)
	if b == nil {
		return
	}
	h.rebasePending(r, b)
	m := &message{procedure: methodRebase, actor: actorHarness, betweenTurns: true, reply: make(chan sent, 1),
		req: &planv1.TaskServiceSendRequest{TaskId: t.GetId(), Text: rebaseText(b)}}
	if res := h.take(r, m); res.err != nil {
		logx.L().Info("a worker resumed after a pause could not be asked to rebase", "task", t.GetCode(), "err", res.err)
	}
}

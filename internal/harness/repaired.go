package harness

import (
	"context"
	"fmt"
	"strings"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/store"
)

// Workers take the repaired branch (W184, decision Q41 B): a work worker started from its wish's integration branch
// before a repair builds on a base that was broken, or about to be fixed. Once the repair's work is in, checked, and no
// correction repairs the branch any more (repair.go), each running work worker of the wish whose branch lacks commits
// of the integration branch gets one message: merge it now, resolve any conflict with your own changes in mind, run
// the tests you touched, go on. Djinn never merges in a worker's worktree: the worker may be about to write a file, and
// only it knows its changes. The message goes between two turns of the worker, never while one of its tool calls has
// no result yet (message.betweenTurns). A merge that brings no repair tells nobody: the workers take it at their end.

// methodRepaired is how the journal records the message Djinn gives a running worker once its integration branch is
// repaired; the request is the message, as djinn task send's.
const methodRepaired = "harness/repaired"

// repairedSubjects is how many commit subjects the message lists at most, the newest first.
const repairedSubjects = 20

// tellRepaired gives each running work worker of wish in project whose work merges into branch, and whose branch lacks
// commits of it, the message to merge it: repairs, the codes of the correction workers whose work just went in, are
// named.
func (h *Harness) tellRepaired(ctx context.Context, wish *planv1.Wish, project *planv1.Project, branch string, repairs []string) {
	h.mu.Lock()
	var runs []*run
	for _, r := range h.runs {
		if r.wish == wish.GetId() && r.worker != nil && !r.watcher && !r.light && !r.ifrit && !r.final && !r.stopping {
			runs = append(runs, r)
		}
	}
	h.mu.Unlock()
	for _, r := range runs {
		task, err := store.Get[*planv1.Task](ctx, h.store, r.id)
		if err != nil || task.GetProjectId() != project.GetId() || task.GetBranch() == "" ||
			task.GetCorrection() != nil || task.GetReview() != nil || h.laneOf(ctx, wish, project, task) != branch {
			continue
		}
		out, err := git(ctx, project.GetDirectory(), "log", "--no-merges", "--topo-order", "--format=%s",
			"refs/heads/"+task.GetBranch()+"..refs/heads/"+branch)
		if err != nil {
			logx.L().Warn("read what a worker's branch lacks", "loop", "integrate", "task", task.GetCode(), "err", err)
			continue
		}
		if out == "" {
			continue // It started from the repaired branch, or merged it already.
		}
		text := repairedText(branch, repairs, strings.Split(out, "\n"))
		m := &message{procedure: methodRepaired, actor: actorHarness, betweenTurns: true, reply: make(chan sent, 1),
			req: &planv1.TaskServiceSendRequest{TaskId: task.GetId(), Text: text}}
		if _, err := h.sendTo(ctx, r, task.GetCode(), m); err != nil {
			logx.L().Info("a worker ended before the repaired branch reached it", "task", task.GetCode(), "err", err)
		}
	}
}

// repairedText is the message that asks a worker to merge branch, which repairs repaired, holding the commits whose
// subjects are given, the newest first.
func repairedText(branch string, repairs, subjects []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Djinn: the integration branch %s is green again: %s repaired it. It holds work your branch lacks:\n",
		branch, strings.Join(repairs, ", "))
	cameIn(&b, subjects)
	fmt.Fprintf(&b, "Merge it into your branch now: commit what you have in progress, then git merge %s (never a rebase). "+
		"Resolve any conflict with your own changes in mind, run the tests that cover what you touched, then go on with "+
		"your task.", branch)
	return b.String()
}

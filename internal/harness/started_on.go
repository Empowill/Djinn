package harness

// Work of no azima may start from another azima's branch: its worker checks out the head of the pull request an azima
// carries, or the azima's own branch, and commits on top of it (W355, wish Dew, 05/11/2026: started from the head of
// merge request !2965, the branch of azima T7, it merged into main, which then held the whole request; tilasm L44,
// decision Q273 A). Its work belongs to that azima: when Djinn merges it, the task is made part of the azima its branch
// started from and lands on that azima's branch (joinStart), never in main, nor in an azima made for it (own_azima.go),
// whose branch, cut from main, would carry the whole request a second time. The azima made for it at its spawn, left
// with no part, is closed.

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/dispatch"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// methodStartedOn is how the journal records work of no azima made part of the azima its branch started from, and the
// azima made for it closed; the request is the task changed.
const methodStartedOn = "harness/started-on"

// joinStart makes task, work Djinn is about to merge in project, part of the azima of wish its branch started from
// (startedOn), when it is work of no azima, or part of the azima made for it (own, madeFor), in a wish that pushes per
// azima. It returns that azima, task changed in place; nil when task stays where it is. The azima made for it, left
// with no other part, is closed.
func (h *Harness) joinStart(
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, settings plan.Settings, task, own *planv1.Task,
) (*planv1.Task, error) {
	if !mergesWork(task, project) || !h.pushesPerAzima(ctx, h.store, wish) {
		return nil, nil
	}
	switch {
	case own == nil && task.GetPartOf() != "":
		return nil, nil // Part of a task that is no azima, under none: not work of no azima.
	case own != nil && (task.GetPartOf() != own.GetId() || !h.madeFor(ctx, own, task)):
		return nil, nil // Its azima is one the lead gave it.
	}
	started, ref := h.startedOn(ctx, wish, project, settings, task, own)
	if started == nil {
		return nil, nil
	}
	ctx = context.WithoutCancel(ctx)
	closed := false
	err := h.store.Tx(ctx, func(tx *store.Tx) error {
		t, err := store.Get[*planv1.Task](ctx, tx, task.GetId())
		if err != nil {
			return err
		}
		if t.GetPartOf() != own.GetId() { // Grouped meanwhile: where the lead put it.
			task.PartOf = t.GetPartOf()
			started = nil
			if t.GetPartOf() != "" {
				started, err = store.Get[*planv1.Task](ctx, tx, t.GetPartOf())
			}
			return err
		}
		t.PartOf = started.GetId()
		if err := tx.Journal(actorHarness, methodStartedOn, t); err != nil {
			return err
		}
		if err := tx.Put(t); err != nil {
			return err
		}
		task.PartOf = t.GetPartOf()
		events := []*planv1.TaskEvent{h.newEvent(t.GetId(), Event{
			Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS,
			Text: "part of " + started.GetCode() + ": its branch started from " + ref + ", the branch of " + started.GetCode() +
				", and its work lands there, never in main",
		})}
		if own != nil {
			if closed, err = h.closeEmptied(ctx, tx, own, t); err != nil {
				return err
			}
			if closed {
				events = append(events, h.newEvent(own.GetId(), Event{
					Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS,
					Text: "closed: " + t.GetCode() + ", the work it was made for, started from the branch of " +
						started.GetCode() + " and is part of it now",
				}))
			}
		}
		return putEvents(ctx, tx, events...)
	})
	if err != nil {
		return nil, err
	}
	if started == nil || task.GetPartOf() != started.GetId() {
		return started, nil
	}
	logx.L().Info("work part of the azima its branch started from", "task", task.GetCode(), "azima", started.GetCode(),
		"from", ref, "made for it", own.GetCode(), "closed", closed)
	h.notify()
	return started, nil
}

// closeEmptied closes own, the azima made for task, in tx, now that task is part of another: done, when no other task
// is part of it. It tells whether it closed it.
func (h *Harness) closeEmptied(ctx context.Context, tx *store.Tx, own, task *planv1.Task) (bool, error) {
	a, err := store.Get[*planv1.Task](ctx, tx, own.GetId())
	if err != nil || a.GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE {
		return false, err
	}
	tasks, err := store.List[*planv1.Task](ctx, tx, store.Where{"wish_id": a.GetWishId()})
	if err != nil {
		return false, err
	}
	if slices.ContainsFunc(tasks, func(p *planv1.Task) bool { return p.GetPartOf() == a.GetId() && p.GetId() != task.GetId() }) {
		return false, nil
	}
	a.Status, a.EndTime = planv1.TaskStatus_TASK_STATUS_DONE, timestamppb.New(h.now())
	if err := tx.Journal(actorHarness, methodStartedOn, a); err != nil {
		return false, err
	}
	return true, tx.Put(a)
}

// mergesWork tells whether task, of project, is work whose commits Djinn merges into a branch of its own choosing: in
// Git, neither an azima, a watcher, an investigation or a mockup, a question worker, a merge, nor a correction or a
// review, which merge into the branch of the work they correct or review.
func mergesWork(task *planv1.Task, project *planv1.Project) bool {
	return project.GetGit() && !plan.IsAzima(task) && !dispatch.Watcher(task) && !dispatch.ChangesNoCode(task) &&
		!dispatch.QuestionWorker(task) && !plan.IsMerge(task) && task.GetCorrection() == nil && task.GetReview() == nil
}

// madeFor tells whether azima was made by Djinn for task, work spawned with no azima (ownAzimaFor, adoptOrphan).
func (h *Harness) madeFor(ctx context.Context, azima, task *planv1.Task) bool {
	if azima.GetTitle() != task.GetTitle() {
		return false
	}
	events, err := store.List[*planv1.TaskEvent](ctx, h.store, store.Where{"task_id": azima.GetId()})
	if err != nil {
		return false
	}
	why := ownAzimaWhy(task)
	return slices.ContainsFunc(events, func(e *planv1.TaskEvent) bool { return e.GetText() == why })
}

// startedOn is the azima of wish in project, other than own, whose branch the branch of task started from, and that
// branch as git names it: the azima's branch, or the remote's branch of it, the head of its pull request, holds commits
// of task's branch that neither main nor own's branch hold. Of several (an azima's branch stacked on another's), the one
// that holds most of them. nil when none, or when git cannot tell.
func (h *Harness) startedOn(
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, settings plan.Settings, task, own *planv1.Task,
) (*planv1.Task, string) {
	repo := project.GetDirectory()
	tip := ""
	if task.GetBranch() != "" {
		tip = commitOf(ctx, repo, "refs/heads/"+task.GetBranch())
	}
	_, main, err := resolveMainSha(ctx, repo, settings)
	if tip == "" || err != nil {
		return nil, ""
	}
	// What the task's branch brings: its commits that main, and the branch of the azima made for it, lack.
	brings := []string{tip, "^" + main}
	if own != nil {
		if c := commitOf(ctx, repo, "refs/heads/"+azimaBranchName(own)); c != "" {
			brings = append(brings, "^"+c)
		}
	}
	roots := broughtRoots(ctx, repo, brings)
	if len(roots) == 0 {
		return nil, ""
	}
	// A branch holds one of those commits only if it holds one of their roots: one git call for every azima's branch.
	azimaOf := map[string]*planv1.Task{}
	var refs []string
	for _, a := range h.wishAzimas(ctx, wish) {
		if a.GetId() == own.GetId() || a.GetProjectId() != "" && a.GetProjectId() != project.GetId() {
			continue
		}
		branch := azimaBranchName(a)
		names := []string{"refs/heads/" + branch}
		if remote, target := pushTarget(ctx, repo, branch); remote != "" {
			names = append(names, "refs/remotes/"+remote+"/"+target)
		}
		for _, n := range names {
			azimaOf[n] = a
			refs = append(refs, n)
		}
	}
	if len(refs) == 0 {
		return nil, ""
	}
	args := []string{"for-each-ref", "--format=%(refname)"}
	for _, r := range roots {
		args = append(args, "--contains="+r)
	}
	out, err := git(ctx, repo, append(args, refs...)...)
	if err != nil {
		logx.L().Warn("find the azima a task's branch started from", "task", task.GetCode(), "err", err)
		return nil, ""
	}
	var found *planv1.Task
	var at string
	lacks := -1
	for _, ref := range strings.Fields(out) {
		a := azimaOf[ref]
		if a == nil {
			continue
		}
		// Of the commits the task brings, how many this branch lacks: the fewest, the branch it started from.
		n, err := git(ctx, repo, append(append([]string{"rev-list", "--count"}, brings...), "^"+ref)...)
		c, convErr := strconv.Atoi(n)
		if err != nil || convErr != nil {
			continue
		}
		if lacks < 0 || c < lacks {
			found, at, lacks = a, ref, c
		}
	}
	if found == nil {
		return nil, ""
	}
	return found, strings.TrimPrefix(strings.TrimPrefix(at, "refs/heads/"), "refs/remotes/")
}

// broughtRoots are the oldest of the commits git rev-list lists for revs, in the repository holding repo: those whose
// parents it does not list. Any branch holding one of those commits holds one of them.
func broughtRoots(ctx context.Context, repo string, revs []string) []string {
	out, err := git(ctx, repo, append([]string{"rev-list", "--parents"}, revs...)...)
	if err != nil || out == "" {
		return nil
	}
	lines := strings.Split(out, "\n")
	listed := make(map[string]bool, len(lines))
	for _, l := range lines {
		c, _, _ := strings.Cut(l, " ")
		listed[c] = true
	}
	var roots []string
	for _, l := range lines {
		f := strings.Fields(l)
		if len(f) > 0 && !slices.ContainsFunc(f[1:], func(p string) bool { return listed[p] }) {
			roots = append(roots, f[0])
		}
	}
	return roots
}

package harness

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// Warm workers. A warm worker is an agent started ahead for the next task of an active wish in one of its projects:
// loaded, and waiting for its first message on its input. The next task spawned there takes it instead of starting
// an agent, and the scheduler starts another one. It costs no token while it waits, only memory. Only Claude warms
// (Warmer), and only for what Djinn knows ahead: the task's identifier (its session, DJINN_TASK_ID, its worktree),
// its folder, its rights, and the model and budget the project's settings give (plan.LoadSettings), as Spawn fills
// them. A task that asks for anything else (another model or budget, a fork, other rights) starts cold, as does a
// planned task: its identifier is older than the warm worker.

// Warmer is a provider that starts its agent before it knows the first message. The first Send of the worker is
// its first message.
type Warmer interface {
	Warm(ctx context.Context, spec Spec) (Worker, error)
}

// warmBranchPrefix starts the branch of a warm worker's worktree, until a task takes it and renames it. A branch
// that still carries it after a crash is a leftover: Recover removes it with its worktree.
const warmBranchPrefix = "djinn-warm-"

// WithWarm keeps a warm worker ready for each project of each active wish, within the machine's limit: djinn up
// --warm-workers. Off by default.
func WithWarm() Option { return func(h *Harness) { h.warmOn = true } }

// warm is a worker started ahead.
type warm struct {
	key      string // wish/project
	id       string // the identifier of the task that takes it
	wishID   string
	project  *planv1.Project
	spec     Spec // as started; the task's spec must match it, prompt and folder aside
	worker   Worker
	worktree string // in Git: its worktree, its placeholder branch and the commit it started from
	branch   string
	base     string
	from     string // the wish's integration branch base was the tip of; empty: the project's HEAD
	since    time.Time
}

// wantWarm is a warm worker the scheduler wants: for a wish, in one of its projects, with the rights, the model and
// the budget its next task would have.
type wantWarm struct {
	key      string
	wishID   string
	project  *planv1.Project
	readOnly bool
	perms    *djinnv1.Permissions
	model    string
	budget   float64
	from     string // the wish's integration branch in the project, which a task's worktree starts from
}

func warmKey(wishID, projectID string) string { return wishID + "/" + projectID }

// warmer is the provider that warms, when the harness has one: Claude's.
func (h *Harness) warmer() Warmer {
	w, _ := h.providers[planv1.Provider_PROVIDER_CLAUDE].(Warmer)
	return w
}

// WarmCount is the number of warm workers waiting.
func (h *Harness) WarmCount() int {
	h.sched.Lock()
	defer h.sched.Unlock()
	return len(h.warm)
}

// refreshWarm brings the warm workers in line with the active wishes and the machine: one per project of each
// active wish, the first wish first, while the workers running and warm stay within the slots, none under pressure.
// It runs in the scheduler's pass, under h.sched.
func (h *Harness) refreshWarm(ctx context.Context) {
	if !h.warmOn || h.ctx.Err() != nil || h.warmer() == nil {
		return
	}
	for key, w := range h.warm {
		if ended(w.worker) {
			h.dropWarm(w, "its process ended")
			delete(h.warm, key)
		}
	}
	want := h.wantedWarm(ctx)
	limit := len(want)
	if h.capacity != nil {
		slots, _, pressure := h.capacity()
		limit = min(limit, max(slots-h.Running(), 0))
		if pressure != "" {
			limit = 0
		}
	}
	keep := map[string]wantWarm{}
	for _, ww := range want[:limit] {
		keep[ww.key] = ww
	}
	for key, w := range h.warm {
		ww, ok := keep[key]
		switch {
		case !ok:
			h.dropWarm(w, "no longer wanted, or no slot left for it")
		case !w.fits(ww.readOnly, ww.perms):
			h.dropWarm(w, "the rights of its project changed")
		case !w.settled(ww.model, ww.budget):
			h.dropWarm(w, "the settings of its project changed")
		case w.stale(ctx, ww.from):
			h.dropWarm(w, "its project moved on")
		default:
			continue
		}
		delete(h.warm, key)
	}
	for _, ww := range want[:limit] {
		if h.warm[ww.key] == nil {
			h.startWarm(ctx, ww)
		}
	}
}

// wantedWarm are the warm workers the active wishes call for, the first wish first: one per project with a folder
// here whose next task would be Claude's by default and start now, without a question to ask first.
func (h *Harness) wantedWarm(ctx context.Context) []wantWarm {
	wishes, err := plan.ActiveWishes(ctx, h.store)
	if err != nil {
		return nil
	}
	var out []wantWarm
	for _, wish := range wishes {
		for _, id := range wish.GetProjectIds() {
			project, err := store.Get[*planv1.Project](ctx, h.store, id)
			if err != nil || project.GetDirectory() == "" {
				continue
			}
			// Its model and budget are the ones Spawn fills from the project's settings. A project whose settings
			// cannot be read spawns no task; one whose tasks go to another agent has no use for a claude.
			settings, err := plan.LoadSettings(h.home, project)
			if err != nil || settings.Provider != planv1.Provider_PROVIDER_CLAUDE {
				continue
			}
			access, declared, err := decideAccess(project, planv1.Provider_PROVIDER_CLAUDE, plan.AllowanceOf(wish, id))
			if err != nil || access == planv1.TaskAccess_TASK_ACCESS_ASKING || access == planv1.TaskAccess_TASK_ACCESS_READ_ONLY {
				continue
			}
			readOnly, perms := accessSpec(access, declared)
			out = append(out, wantWarm{
				key: warmKey(wish.GetId(), id), wishID: wish.GetId(), project: project, readOnly: readOnly, perms: perms,
				model: settings.Model, budget: settings.MaxBudgetUSD, from: plan.IntegrationBranchOf(wish, id),
			})
		}
	}
	return out
}

// startWarm starts a warm worker for ww: in Git, in the worktree its task will have, on a placeholder branch.
func (h *Harness) startWarm(ctx context.Context, ww wantWarm) {
	id := store.NewID()
	w := &warm{key: ww.key, id: id, wishID: ww.wishID, project: ww.project, since: time.Now()}
	dir := ww.project.GetDirectory()
	if ww.project.GetGit() {
		var err error
		if w.base, w.from, err = startPoint(ctx, dir, ww.from); err != nil {
			log.Printf("djinn: warm worker for %s: %v", ww.project.GetName(), err)
			return
		}
		w.worktree, w.branch = worktreeDir(h.home, ww.project.GetId(), id), warmBranchPrefix+id[len(id)-8:]
		if dir, err = addWorktreeFrom(ctx, ww.project.GetDirectory(), w.worktree, w.branch, w.base); err != nil {
			log.Printf("djinn: warm worker for %s: %v", ww.project.GetName(), err)
			return
		}
	}
	guard, err := gitGuard(h.home, dir)
	if err != nil {
		log.Printf("djinn: warm worker for %s: %v", ww.project.GetName(), err)
		h.removeWarmTree(w)
		return
	}
	w.spec = Spec{
		TaskID: id, Dir: dir, ReadOnly: ww.readOnly, Permissions: ww.perms, Model: ww.model, MaxBudgetUSD: ww.budget,
		Env: append([]string{"DJINN_TASK_ID=" + id, "DJINN_WISH_ID=" + ww.wishID}, guard...), Scope: h.scope("warm"),
	}
	worker, err := h.warmer().Warm(h.ctx, w.spec)
	if err != nil {
		log.Printf("djinn: warm worker for %s: %v", ww.project.GetName(), err)
		h.removeWarmTree(w)
		return
	}
	w.worker = worker
	h.warm[ww.key] = w
}

// claimWarm hands the warm worker of the wish in the project to the task, which starts now, and takes it out of the
// pool; nil when there is none, or it does not fit. A task that asks for a fork, or another model or budget than
// the warm worker's, leaves it to the next one. Under h.sched.
func (h *Harness) claimWarm(ctx context.Context, wish *planv1.Wish, project *planv1.Project, task *planv1.Task) *warm {
	kind := task.GetProvider()
	if !h.warmOn || project == nil || kind != planv1.Provider_PROVIDER_CLAUDE || task.GetForkSession() != "" || task.GetCorrection() != nil ||
		task.GetReview() != nil {
		return nil
	}
	key := warmKey(wish.GetId(), project.GetId())
	w := h.warm[key]
	if w == nil || !w.settled(task.GetModel(), task.GetMaxBudgetUsd()) {
		return nil
	}
	delete(h.warm, key)
	access, declared, err := decideAccess(project, kind, plan.AllowanceOf(wish, project.GetId()))
	readOnly, perms := accessSpec(access, declared)
	switch {
	case ended(w.worker):
		h.dropWarm(w, "its process ended")
	case err != nil || !w.fits(readOnly, perms):
		h.dropWarm(w, "the task's rights differ")
	case w.stale(ctx, plan.IntegrationBranchOf(wish, project.GetId())):
		h.dropWarm(w, "its project moved on")
	default:
		h.wake() // Another warm worker for the next task.
		return w
	}
	return nil
}

// fits tells whether a task with these rights may take the warm worker.
func (w *warm) fits(readOnly bool, perms *djinnv1.Permissions) bool {
	return w.spec.ReadOnly == readOnly && proto.Equal(w.spec.Permissions, perms)
}

// settled tells whether a task with this model and budget may take the warm worker.
func (w *warm) settled(model string, budget float64) bool {
	return w.spec.Model == model && w.spec.MaxBudgetUSD == budget
}

// stale tells whether the project has moved on since the warm worker started: the commit a task's worktree starts
// from now, the tip of the wish's integration branch integration or else HEAD, is no longer the one it started from.
func (w *warm) stale(ctx context.Context, integration string) bool {
	if w.base == "" {
		return false
	}
	sha, _, err := startPoint(ctx, w.project.GetDirectory(), integration)
	return err != nil || sha != w.base
}

// dropWarm stops a warm worker and removes its worktree, in the background.
func (h *Harness) dropWarm(w *warm, why string) {
	log.Printf("djinn: warm worker for %s dropped: %s", w.project.GetName(), why)
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		h.stopWarm(w)
	}()
}

// stopWarm stops a warm worker, waits for it, and removes its worktree and branch.
func (h *Harness) stopWarm(w *warm) {
	if w.worker != nil {
		w.worker.Stop()
		go func() {
			for range w.worker.Events() { // Drained, so that it ends.
			}
		}()
		w.worker.Wait()
	}
	h.removeWarmTree(w)
}

func (h *Harness) removeWarmTree(w *warm) {
	if w.worktree == "" {
		return
	}
	ctx := context.Background()
	if err := removeWorktree(ctx, w.project.GetDirectory(), w.worktree, true); err != nil {
		log.Printf("djinn: remove the worktree of a warm worker: %v", err)
	}
	if _, err := git(ctx, w.project.GetDirectory(), "branch", "-D", w.branch); err != nil {
		log.Printf("djinn: remove the branch of a warm worker: %v", err)
	}
}

// closeWarm stops every warm worker and removes their worktrees: djinn up stops.
func (h *Harness) closeWarm() {
	h.sched.Lock()
	all := h.warm
	h.warm = map[string]*warm{}
	h.sched.Unlock()
	for _, w := range all {
		h.stopWarm(w)
	}
}

// ended tells whether a worker has ended, when it can tell.
func ended(w Worker) bool {
	d, ok := w.(interface{ Done() <-chan struct{} })
	if !ok {
		return false
	}
	select {
	case <-d.Done():
		return true
	default:
		return false
	}
}

// adoptWarm starts the run's task on its warm worker: in Git, its branch takes the task's name; then the prompt is
// its first message. A warm worker that takes no message is stopped, and the task starts cold in the same folder.
func (h *Harness) adoptWarm(ctx context.Context, r *run, provider Provider, w *warm, spec Spec, text string) error {
	if w.branch != "" {
		if _, err := git(ctx, w.worktree, "branch", "-m", w.branch, r.task.GetBranch()); err != nil {
			h.stopWarm(w)
			return fmt.Errorf("name the warm worker's branch: %w", err)
		}
	}
	text += fmt.Sprintf(", on a warm worker loaded %s before", time.Since(w.since).Round(time.Second))
	t := r.task
	t.Status, t.StartTime, t.EndTime, t.ExitCode, t.Error = planv1.TaskStatus_TASK_STATUS_RUNNING, timestamppb.Now(), nil, 0, ""
	r.failure, r.limit = "", nil
	r.answered = false
	if r.startHead == "" {
		wt := t.GetWorktree()
		if wt == "" {
			wt = spec.Dir
		}
		if wt != "" {
			if head, err := git(ctx, wt, "rev-parse", "HEAD"); err == nil {
				r.startHead = strings.TrimSpace(head)
			}
		}
	}
	if (t.GetModel() == "" || foreignModel(t.GetProvider(), t.GetModel())) && !r.watcher {
		t.Model = DefaultModel(t.GetProvider())
	}
	h.write(r, actorHarness, methodStart, t, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: text})
	if err := w.worker.Send(spec.Prompt); err != nil {
		h.stopWarm(w)
		h.write(r, actorHarness, methodEvent, nil, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS,
			Text: "the warm worker took no message (" + err.Error() + "): starting cold"})
		return h.start(r, provider, spec, "started "+short(t.GetProvider())+" cold in "+spec.Dir)
	}
	h.mu.Lock()
	r.worker = w.worker
	if r.stopping {
		w.worker.Stop()
	}
	h.mu.Unlock()
	return nil
}

// cleanWarmLeftovers removes the worktrees and branches of warm workers a previous djinn up left when it died: no
// task took them. Only worktrees in Djinn's data folder, on a branch that still has the warm prefix.
func (h *Harness) cleanWarmLeftovers(ctx context.Context) {
	projects, err := store.List[*planv1.Project](ctx, h.store, nil)
	if err != nil {
		return
	}
	root := filepath.Join(h.home, "projects")
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	root += string(filepath.Separator)
	for _, p := range projects {
		if !p.GetGit() || p.GetDirectory() == "" {
			continue
		}
		if _, err := os.Stat(p.GetDirectory()); err != nil {
			continue
		}
		out, err := git(ctx, p.GetDirectory(), "worktree", "list", "--porcelain")
		if err != nil {
			continue
		}
		var path string
		for _, line := range strings.Split(out, "\n") {
			switch {
			case strings.HasPrefix(line, "worktree "):
				path = filepath.Clean(strings.TrimPrefix(line, "worktree "))
			case strings.HasPrefix(line, "branch refs/heads/"+warmBranchPrefix) && strings.HasPrefix(path+string(filepath.Separator), root):
				branch := strings.TrimPrefix(line, "branch refs/heads/")
				h.removeWarmTree(&warm{project: p, worktree: path, branch: branch})
			}
		}
	}
}

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
// its folder and its rights. A task that asks for anything else (a model, a budget, a fork, other rights) starts
// cold, as does a planned task: its identifier is older than the warm worker.

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
	since    time.Time
}

// wantWarm is a warm worker the scheduler wants: for a wish, in one of its projects, with the rights its next task
// would have.
type wantWarm struct {
	key      string
	wishID   string
	project  *planv1.Project
	readOnly bool
	perms    *djinnv1.Permissions
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
		case w.stale(ctx):
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
// here whose next Claude task would start now, without a question to ask first.
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
			access, declared, err := decideAccess(project, planv1.Provider_PROVIDER_CLAUDE, plan.AllowanceOf(wish, id))
			if err != nil || access == planv1.TaskAccess_TASK_ACCESS_ASKING || access == planv1.TaskAccess_TASK_ACCESS_READ_ONLY {
				continue
			}
			readOnly, perms := accessSpec(access, declared)
			out = append(out, wantWarm{key: warmKey(wish.GetId(), id), wishID: wish.GetId(), project: project, readOnly: readOnly, perms: perms})
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
		if w.base, err = git(ctx, dir, "rev-parse", "HEAD"); err != nil {
			log.Printf("djinn: warm worker for %s: %v", ww.project.GetName(), err)
			return
		}
		w.worktree, w.branch = worktreeDir(h.home, ww.project.GetId(), id), warmBranchPrefix+id[len(id)-8:]
		if dir, err = addWorktree(ctx, ww.project.GetDirectory(), w.worktree, w.branch); err != nil {
			log.Printf("djinn: warm worker for %s: %v", ww.project.GetName(), err)
			return
		}
	}
	w.spec = Spec{
		TaskID: id, Dir: dir, ReadOnly: ww.readOnly, Permissions: ww.perms,
		Env: []string{"DJINN_TASK_ID=" + id, "DJINN_WISH_ID=" + ww.wishID},
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

// claimWarm hands the warm worker of the wish in the project to a task that starts now with these rights, and
// takes it out of the pool; nil when there is none, or it does not fit. Under h.sched.
func (h *Harness) claimWarm(ctx context.Context, wish *planv1.Wish, project *planv1.Project, kind planv1.Provider) *warm {
	if !h.warmOn || project == nil || kind != planv1.Provider_PROVIDER_CLAUDE {
		return nil
	}
	key := warmKey(wish.GetId(), project.GetId())
	w := h.warm[key]
	if w == nil {
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
	case w.stale(ctx):
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

// stale tells whether the project has moved on since the warm worker started: its HEAD is no longer the commit
// the worktree started from.
func (w *warm) stale(ctx context.Context) bool {
	if w.base == "" {
		return false
	}
	head, err := git(ctx, w.project.GetDirectory(), "rev-parse", "HEAD")
	return err != nil || head != w.base
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
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
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

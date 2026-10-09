package harness

// Djinn resumes the workers it cut short, by itself, in the same task. A task interrupted by djinn up stopping or
// crashing, or failed on its provider's usage limit, becomes RESUMING: the scheduler starts its worker again first,
// in the same worktree and session, once its slot is free and the limit has reset. A task stopped by a person never
// resumes; one resumed maxResumes times without finishing fails, saying so.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/render"
	"github.com/empowill/djinn/internal/store"
)

// methodResume is journaled when a task cut short waits for Djinn to resume it; the request is the task.
const methodResume = "harness/resume"

// maxResumes is how many times Djinn resumes a task by itself: cut short once more, it fails.
const maxResumes = 3

// What a resumed worker is told, after its own session; a worker that cannot resume its session gets its first
// prompt before it.
const (
	restartedLine = "Djinn restarted while you worked; your worktree is as you left it. Continue your task."
	limitLine     = "Your provider's usage limit stopped you; it has reset. Your worktree is as you left it. Continue your task."
)

// Why a task waits to be resumed, as its wait reason and its events say.
const (
	whyRestarted = "djinn restarted while the worker ran"
	byRestart    = "after djinn restarted"
	byLimit      = "after its usage limit reset"
)

// WithClock gives the harness its time: when a usage limit resets is compared with it. Tests set a fake one.
func WithClock(now func() time.Time) Option { return func(h *Harness) { h.clock = now } }

func (h *Harness) now() time.Time {
	if h.clock == nil {
		return time.Now()
	}
	return h.clock()
}

// queueInterrupted gives back to the scheduler the tasks djinn up cut short: each one resumes, unless it cannot (see
// resumable), or was resumed maxResumes times already, which fails it. Recover calls it.
func (h *Harness) queueInterrupted(ctx context.Context, tasks []*planv1.Task) error {
	wishes, err := store.List[*planv1.Wish](ctx, h.store, nil)
	if err != nil {
		return err
	}
	projects, err := store.List[*planv1.Project](ctx, h.store, nil)
	if err != nil {
		return err
	}
	wish := map[string]*planv1.Wish{}
	for _, w := range wishes {
		wish[w.GetId()] = w
	}
	project := map[string]*planv1.Project{}
	for _, p := range projects {
		project[p.GetId()] = p
	}
	for _, t := range tasks {
		if t.GetStatus() != planv1.TaskStatus_TASK_STATUS_INTERRUPTED || !resumable(t, tasks, wish[t.GetWishId()], project[t.GetProjectId()]) {
			continue
		}
		t = proto.CloneOf(t)
		ev := Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS}
		if t.GetResumes() >= maxResumes {
			t.Status, t.Error = planv1.TaskStatus_TASK_STATUS_FAILED, exhausted(t.GetError())
			ev.Text = "failed: " + t.GetError()
		} else {
			t.Status, t.Error, t.WaitReason, t.ResumeAfter = planv1.TaskStatus_TASK_STATUS_RESUMING, "", whyRestarted, nil
			ev.Text = "resuming: " + whyRestarted + "; Djinn starts it again by itself"
		}
		h.writeAlone(ctx, actorHarness, methodResume, t, t.GetId(), t, ev)
	}
	return nil
}

// resumable tells whether Djinn may resume an interrupted task by itself: planned on this machine (not imported), not
// resumed already as another task (a fork of it), its wish not granted, and in Git its worktree still there.
func resumable(t *planv1.Task, tasks []*planv1.Task, wish *planv1.Wish, project *planv1.Project) bool {
	if !t.GetScheduled() || wish == nil || wish.GetState() == planv1.WishState_WISH_STATE_GRANTED || render.ForkedAs(t, tasks) != "" {
		return false
	}
	if project.GetGit() {
		if t.GetWorktree() == "" {
			return false
		}
		if _, err := os.Stat(t.GetWorktree()); err != nil {
			return false
		}
	}
	return t.GetProjectId() == "" || project != nil
}

// exhausted is the error of a task cut short once more after maxResumes resumes.
func exhausted(last string) string {
	why := fmt.Sprintf("resumed %d times without finishing", maxResumes)
	if last != "" {
		why += "; the last time: " + last
	}
	return why
}

// limited turns the failure of a worker its provider's usage limit stopped into a wait: the task resumes once the
// limit resets, when its message says when, else after a backoff. A task resumed maxResumes times stays failed,
// saying so. Another failure is left as it is. The caller is end, which owns the run's task.
func (h *Harness) limited(r *run, t *planv1.Task) {
	now := h.now()
	l, ok := limitIn(t.GetError(), now)
	if r.limit != nil {
		// The provider said so on a line of its own: what limit, and when it resets, to the second.
		l.What = r.limit.What
		if !r.limit.Until.IsZero() {
			l.Until = r.limit.Until
		}
	} else if !ok {
		return
	}
	if t.GetResumes() >= maxResumes {
		t.Error = exhausted(t.GetError())
		return
	}
	after := l.Until
	if !after.After(now) {
		after, l.Until = now.Add(limitBackoff(t.GetResumes())), time.Time{}
	} else {
		after = after.Add(time.Minute) // Once it has reset, not at the very second.
	}
	t.Status, t.Error = planv1.TaskStatus_TASK_STATUS_RESUMING, ""
	t.WaitReason, t.ResumeAfter = limitText(l, after, now), timestamppb.New(after)
}

// relaunch starts again the worker of a task Djinn resumes: in its worktree (or its project's folder, or its own
// folder outside any project), on its session when its agent can resume one, told why it stopped. Its access is
// decided again in a project, as for a planned task, but for an answer the developer gave about editing, which
// stays. A worker that cannot start fails the task.
func (h *Harness) relaunch(ctx context.Context, t *planv1.Task) error {
	t = proto.CloneOf(t)
	limit := t.GetResumeAfter() != nil
	// Running from now on, as its worker starts: never waiting again without a reason.
	t.Status, t.WaitReason, t.ResumeAfter = planv1.TaskStatus_TASK_STATUS_RUNNING, "", nil
	t.Resumes++
	provider, ok := h.providers[t.GetProvider()]
	if !ok {
		h.failPlanned(ctx, t, fmt.Sprintf("provider %s is not available", t.GetProvider()))
		return nil
	}
	prompt, err := firstPrompt(h.store, t.GetId())
	if err != nil {
		h.failPlanned(ctx, t, err.Error())
		return nil
	}
	seq, err := lastSeq(ctx, h.store, t.GetId())
	if err != nil {
		return err
	}
	r, err := h.newRun(t, seq)
	if err != nil {
		return err
	}
	var project *planv1.Project
	var prep prepared
	err = h.store.Tx(ctx, func(tx *store.Tx) error {
		wish, err := store.Get[*planv1.Wish](ctx, tx, t.GetWishId())
		if err != nil {
			return err
		}
		if t.GetProjectId() != "" {
			if project, err = store.Get[*planv1.Project](ctx, tx, t.GetProjectId()); err != nil {
				return err
			}
		}
		switch t.GetAccess() {
		case planv1.TaskAccess_TASK_ACCESS_ASKING, planv1.TaskAccess_TASK_ACCESS_EDIT_GRANTED,
			planv1.TaskAccess_TASK_ACCESS_EDIT_REFUSED, planv1.TaskAccess_TASK_ACCESS_READ_ONLY:
			// Decided by the developer's answer, or outside any project: it stays.
		default:
			access, declared, err := decideAccess(project, t.GetProvider(), plan.AllowanceOf(wish, project.GetId()))
			if err != nil {
				return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("project %s: %w", project.GetName(), err))
			}
			if access != planv1.TaskAccess_TASK_ACCESS_ASKING {
				t.Access, prep.declared = access, declared
			}
		}
		if err := tx.Journal(actorHarness, methodSchedule, t); err != nil {
			return err
		}
		return tx.Put(t)
	})
	if err == nil {
		err = h.resumeWorker(r, provider, project, prep, prompt, limit)
	}
	if err != nil {
		h.finish(r, Result{ExitCode: -1, Err: err})
		return err
	}
	go h.pump(r)
	return nil
}

// resumeWorker starts the worker of a resumed task where its last one worked. The caller owns the run's task.
func (h *Harness) resumeWorker(r *run, provider Provider, project *planv1.Project, prep prepared, prompt string, limit bool) error {
	t := r.task
	dir := project.GetDirectory()
	switch {
	case project == nil:
		dir = scratchDir(h.home, t.GetId())
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create the task's folder: %w", err)
		}
	case t.GetWorktree() != "":
		prefix, err := git(h.ctx, project.GetDirectory(), "rev-parse", "--show-prefix")
		if err != nil {
			return err
		}
		dir = filepath.Join(t.GetWorktree(), filepath.FromSlash(prefix))
		if _, err := os.Stat(dir); err != nil {
			return errors.New("its worktree is gone: it cannot be resumed")
		}
	case project.GetGit():
		return errors.New("its worktree is gone: it cannot be resumed")
	}
	budget := t.GetMaxBudgetUsd()
	if budget > 0 {
		if budget -= t.GetUsage().GetCostUsd(); budget <= 0 {
			return fmt.Errorf("its budget of $%.2f is spent", t.GetMaxBudgetUsd())
		}
	}
	line, by := restartedLine, byRestart
	if limit {
		line, by = limitLine, byLimit
	}
	readOnly, perms := accessSpec(t.GetAccess(), prep.declared)
	spec := Spec{
		TaskID: t.GetId(), Dir: dir, ReadOnly: readOnly, Permissions: perms, Model: t.GetModel(), MaxBudgetUSD: budget,
		Resume: t.GetSessionId(), Prompt: line,
	}
	how := ", resuming its session"
	if t.GetProvider() == planv1.Provider_PROVIDER_ANTIGRAVITY || spec.Resume == "" {
		// agy's resume is not verified (docs/providers.md): it starts again on its first prompt, as does a worker
		// whose session was never known.
		spec.Resume, spec.Prompt, how = "", prompt+"\n\n"+line, ", from its first prompt"
	}
	r.base = t.GetUsage()
	spec.Skills, spec.SkillsDir = h.summon(context.Background(), r, project)
	where := "in " + dir
	if t.GetBranch() != "" {
		where += ", on branch " + t.GetBranch()
	}
	text := fmt.Sprintf("resumed %s (%d of %d): started %s %s%s, %s%s", by, t.GetResumes(), maxResumes,
		short(t.GetProvider()), where, how, accessText(t, nil), skillsText(spec.Skills))
	return h.start(r, provider, spec, text)
}

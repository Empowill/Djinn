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
	"strings"
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
	envReplayLine = "Djinn replays this task after an environment failure. Continue your task."
)

// Why a task waits to be resumed, as its wait reason and its events say.
const (
	whyRestarted = "djinn restarted while the worker ran"
	byRestart    = "after djinn restarted"
	byLimit      = "after its usage limit reset"
	byContinue   = "continued" // djinn task continue: its worker takes the new prompt on its session
	byEnvReplay  = "replaying environment failure"
)

// whyStoppedFirst is why a planned task waits that djinn up stopped before its worker started (its worktree being
// made): it starts at the next start, as planned.
const whyStoppedFirst = "djinn up stopped before its worker started"

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
		if t.GetStatus() == planv1.TaskStatus_TASK_STATUS_FAILED &&
			t.GetEnvCause() != "" &&
			t.GetEnvReplays() < 2 &&
			h.version != "" &&
			h.version != t.GetEnvBuild() {
			w := wish[t.GetWishId()]
			if !t.GetScheduled() || w == nil || w.GetState() == planv1.WishState_WISH_STATE_GRANTED || render.ForkedAs(t, tasks) != "" {
				continue
			}
			t = proto.CloneOf(t)
			t.EnvReplays++
			t.EnvBuild = h.version
			t.Status, t.Error, t.WaitReason, t.ResumeAfter = planv1.TaskStatus_TASK_STATUS_RESUMING, "", byEnvReplay, nil
			ev := Event{
				Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS,
				Text: fmt.Sprintf("resuming: %s (%d of 2); Djinn starts it again by itself", byEnvReplay, t.GetEnvReplays()),
			}
			h.writeAlone(ctx, actorHarness, methodResume, t, t.GetId(), t, ev)
			_ = h.unblockDependencyFailed(ctx, t.GetCode())
			continue
		}
		if t.GetStatus() != planv1.TaskStatus_TASK_STATUS_INTERRUPTED {
			continue
		}
		w := wish[t.GetWishId()]
		p := project[t.GetProjectId()]
		if !t.GetScheduled() || w == nil || w.GetState() == planv1.WishState_WISH_STATE_GRANTED || render.ForkedAs(t, tasks) != "" {
			continue
		}
		if p != nil && p.GetGit() && !light(t) {
			if t.GetWorktree() == "" || isGone(t.GetWorktree()) {
				h.tellWorkerFailed(t)
				continue
			}
		}
		if !resumable(t, tasks, w, p) {
			continue
		}
		t = proto.CloneOf(t)
		ev := Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS}
		if t.GetResumes() >= maxResumes && !watching(t) { // A watcher watches across restarts, however many.
			t.Status, t.Error = planv1.TaskStatus_TASK_STATUS_FAILED, exhausted(t.GetError())
			ev.Text = "failed: " + t.GetError()
			h.writeAlone(ctx, actorHarness, methodResume, t, t.GetId(), t, ev)
			h.tellWorkerFailed(t)
		} else {
			t.Status, t.Error, t.WaitReason, t.ResumeAfter = planv1.TaskStatus_TASK_STATUS_RESUMING, "", whyRestarted, nil
			ev.Text = "resuming: " + whyRestarted + "; Djinn starts it again by itself"
			h.writeAlone(ctx, actorHarness, methodResume, t, t.GetId(), t, ev)
		}
	}
	return nil
}

// isGone tells whether the folder no longer exists.
func isGone(dir string) bool {
	_, err := os.Stat(dir)
	return err != nil
}

// resumable tells whether Djinn may resume an interrupted task by itself: planned on this machine (not imported), not
// resumed already as another task (a fork of it), its wish not granted, and in Git its worktree still there (but for
// a watcher, which has none).
func resumable(t *planv1.Task, tasks []*planv1.Task, wish *planv1.Wish, project *planv1.Project) bool {
	if !t.GetScheduled() || wish == nil || wish.GetState() == planv1.WishState_WISH_STATE_GRANTED || render.ForkedAs(t, tasks) != "" {
		return false
	}
	if project.GetGit() && !light(t) { // A watcher or a question worker runs in the project's folder.
		if t.GetWorktree() == "" || isGone(t.GetWorktree()) {
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
// folder outside any project), on its session when its agent can resume one, told why it stopped. A continued task's
// worker takes its last prompt on its session instead (Continue); one a yes to its edit question gave back is told
// it may edit now, with its first prompt (grant). Its access is decided again in a project, as for a planned task,
// but for an answer the developer gave about editing, which stays. A worker that cannot start fails the task.
func (h *Harness) relaunch(ctx context.Context, t *planv1.Task) error {
	t = proto.CloneOf(t)
	by := byRestart
	switch {
	case t.GetContinuing():
		by = byContinue
	case t.GetResumeAfter() != nil:
		by = byLimit
	case strings.HasPrefix(t.GetWaitReason(), whyWishPaused):
		by = byWish
	case strings.HasPrefix(t.GetWaitReason(), whyEditGranted):
		by = byAnswer
	case strings.HasPrefix(t.GetWaitReason(), byEnvReplay):
		by = byEnvReplay
	}
	// Running from now on, as its worker starts: never waiting again without a reason.
	t.Status, t.WaitReason, t.ResumeAfter = planv1.TaskStatus_TASK_STATUS_RUNNING, "", nil
	switch by {
	case byContinue:
		t.Continuing = false // Cut short again, it resumes as any task does.
	case byWish, byAnswer, byEnvReplay: // The developer paused it, or let it edit: no resume spent.
	default:
		t.Resumes++
	}
	provider, ok := h.providers[t.GetProvider()]
	if !ok {
		h.failPlanned(ctx, t, fmt.Sprintf("provider %s is not available", t.GetProvider()))
		return nil
	}
	priorProvider := t.GetPriorProvider()
	providerChanged := priorProvider != planv1.Provider_PROVIDER_UNSPECIFIED && priorProvider != t.GetProvider()
	prompt, err := firstPrompt(h.store, t.GetId())
	if by == byContinue && !providerChanged {
		prompt, err = lastPrompt(ctx, h.store, t.GetId())
	}
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
			planv1.TaskAccess_TASK_ACCESS_EDIT_REFUSED, planv1.TaskAccess_TASK_ACCESS_READ_ONLY,
			planv1.TaskAccess_TASK_ACCESS_DJINN:
			// Decided by the developer's answer, outside any project, or by the task's role: it stays.
		default:
			access, declared, err := decideAccess(project, t.GetProvider(), plan.AllowanceOf(wish, project.GetId()))
			if err != nil {
				return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("project %s: %w", project.GetName(), err))
			}
			if access != planv1.TaskAccess_TASK_ACCESS_ASKING {
				t.Access, prep.declared = access, declared
			}
		}
		if t.GetPriorProvider() != planv1.Provider_PROVIDER_UNSPECIFIED {
			t.PriorProvider = planv1.Provider_PROVIDER_UNSPECIFIED
		}
		if foreignModel(t.GetProvider(), t.GetModel()) && !r.watcher {
			t.Model = DefaultModel(t.GetProvider())
		}
		if err := tx.Journal(actorHarness, methodSchedule, t); err != nil {
			return err
		}
		return tx.Put(t)
	})
	if err == nil {
		err = h.resumeWorker(r, provider, project, prep, prompt, by, priorProvider)
	}
	if err != nil {
		h.finish(r, Result{ExitCode: -1, Err: err})
		return err
	}
	go h.pump(r)
	return nil
}

// resumeWorker starts the worker of a resumed task where its last one worked, by: byRestart, byLimit, or byContinue
// with the prompt it was continued with. The caller owns the run's task.
func (h *Harness) resumeWorker(r *run, provider Provider, project *planv1.Project, prep prepared, prompt, by string, priorProvider planv1.Provider) error {
	t := r.task
	dir := project.GetDirectory()
	switch {
	case project == nil:
		dir = scratchDir(h.home, t.GetId())
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create the task's folder: %w", err)
		}
	case r.watcher:
		// In its project's folder, on its command: it has no session to resume, and nothing to be told.
		readOnly, perms := accessSpec(t.GetAccess(), prep.declared)
		spec := Spec{TaskID: t.GetId(), Dir: dir, ReadOnly: readOnly, Permissions: perms, Prompt: prompt, Restart: t.GetRestart()}
		r.base = t.GetUsage()
		text := fmt.Sprintf("resumed %s: started watch in %s%s", byRestart, dir, watchText(t))
		return h.start(r, provider, spec, text)
	case t.GetWorktree() != "":
		prefix, err := git(h.ctx, project.GetDirectory(), "rev-parse", "--show-prefix")
		if err != nil {
			return err
		}
		dir = filepath.Join(t.GetWorktree(), filepath.FromSlash(prefix))
		if _, err := os.Stat(dir); err != nil {
			if by == byEnvReplay {
				sha, _, _ := startPoint(h.ctx, project.GetDirectory(), "")
				if sha == "" {
					sha = "HEAD"
				}
				if dir, err = addWorktreeFrom(h.ctx, project.GetDirectory(), t.GetWorktree(), t.GetBranch(), sha); err != nil {
					return fmt.Errorf("create the worktree: %w", err)
				}
			} else {
				return errors.New("its worktree is gone: it cannot be resumed")
			}
		}
	case project.GetGit() && !r.light: // A question worker reads in the project's folder.
		if by == byEnvReplay {
			t.Worktree = worktreeDir(h.home, project.GetId(), t.GetId())
			sha, _, _ := startPoint(h.ctx, project.GetDirectory(), "")
			if sha == "" {
				sha = "HEAD"
			}
			var err error
			if dir, err = addWorktreeFrom(h.ctx, project.GetDirectory(), t.GetWorktree(), t.GetBranch(), sha); err != nil {
				return fmt.Errorf("create the worktree: %w", err)
			}
		} else {
			return errors.New("its worktree is gone: it cannot be resumed")
		}
	}
	budget := t.GetMaxBudgetUsd()
	if budget > 0 {
		if budget -= t.GetUsage().GetCostUsd(); budget <= 0 {
			return fmt.Errorf("its budget of $%.2f is spent", t.GetMaxBudgetUsd())
		}
	}
	if project.GetGit() {
		// The checks its first prompt said at launch, as the project's settings say them now.
		settings, err := plan.LoadSettings(h.home, project)
		if err != nil {
			return fmt.Errorf("project %s: %w", project.GetName(), err)
		}
		r.checks = settings.ChecksBrief()
	}
	readOnly, perms := accessSpec(t.GetAccess(), prep.declared)
	perms = withCommit(t, perms)
	line := restartedLine
	switch by {
	case byLimit:
		line = limitLine
	case byWish:
		line = wishLine
	case byContinue:
		line = prompt
	case byAnswer:
		line = editLine + briefed(r, prompt, readOnly)
	case byEnvReplay:
		line = envReplayLine
	}
	model := t.GetModel()
	if foreignModel(t.GetProvider(), model) && !r.watcher {
		model = DefaultModel(t.GetProvider())
		t.Model = model
	}
	spec := Spec{
		TaskID: t.GetId(), Dir: dir, ReadOnly: readOnly, Permissions: perms, Model: model, MaxBudgetUSD: budget,
		Resume: t.GetSessionId(), Prompt: line,
	}
	how := ", resuming its session"
	providerChanged := priorProvider != planv1.Provider_PROVIDER_UNSPECIFIED && priorProvider != t.GetProvider()
	switch {
	case providerChanged:
		spec.Resume = ""
		spec.Prompt = briefed(r, prompt, readOnly)
		how = fmt.Sprintf(", provider changed: %s → %s, starts from its first prompt", short(priorProvider), short(t.GetProvider()))
	case by == byContinue && spec.Resume == "":
		return errors.New("its session cannot be resumed")
	case by == byEnvReplay:
		if spec.Resume != "" && plan.CanResume(t.GetProvider()) {
			spec.Prompt = line
			how = ", resuming its session"
		} else {
			spec.Resume = ""
			spec.Prompt = briefed(r, prompt, readOnly)
			how = ", from its first prompt"
		}
	case spec.Resume == "":
		// A worker whose session was never known starts again on its first prompt, its checks with it as at launch.
		spec.Resume, spec.Prompt, how = "", briefed(r, prompt, readOnly)+"\n\n"+line, ", from its first prompt"
		if by == byAnswer {
			spec.Prompt = line // It holds the first prompt already.
		}
	}
	r.base = t.GetUsage()
	spec.Skills, spec.SkillsDir = h.summon(context.Background(), r, project)
	where := "in " + dir
	if t.GetBranch() != "" {
		where += ", on branch " + t.GetBranch()
	}
	text := fmt.Sprintf("resumed %s (%d of %d): started %s %s%s, %s%s", by, t.GetResumes(), maxResumes,
		short(t.GetProvider()), where, how, accessText(t, nil), skillsText(spec.Skills))
	if by == byWish {
		text = fmt.Sprintf("resumed %s: started %s %s%s, %s%s", by, short(t.GetProvider()), where, how,
			accessText(t, nil), skillsText(spec.Skills))
	}
	if by == byAnswer {
		text = fmt.Sprintf("started %s again %s%s, %s%s", short(t.GetProvider()), where, how, accessText(t, nil),
			skillsText(spec.Skills))
	}
	if by == byContinue {
		text = fmt.Sprintf("continued: started %s %s%s, %s%s", short(t.GetProvider()), where, how, accessText(t, nil),
			skillsText(spec.Skills))
	}
	if by == byEnvReplay {
		text = fmt.Sprintf("resumed %s (%d of 2): started %s %s%s, %s%s", by, t.GetEnvReplays(),
			short(t.GetProvider()), where, how, accessText(t, nil), skillsText(spec.Skills))
	}
	return h.start(r, provider, spec, text)
}

// replayEnvFailures restarts tasks of the given provider that failed for an environment cause.
func (h *Harness) replayEnvFailures(ctx context.Context, provider planv1.Provider) error {
	tasks, err := store.List[*planv1.Task](ctx, h.store, nil)
	if err != nil {
		return err
	}
	wishes, err := store.List[*planv1.Wish](ctx, h.store, nil)
	if err != nil {
		return err
	}
	wish := map[string]*planv1.Wish{}
	for _, w := range wishes {
		wish[w.GetId()] = w
	}
	for _, t := range tasks {
		if t.GetStatus() == planv1.TaskStatus_TASK_STATUS_FAILED &&
			t.GetEnvCause() != "" &&
			t.GetEnvReplays() < 2 &&
			(provider == planv1.Provider_PROVIDER_UNSPECIFIED || t.GetProvider() == provider) {
			w := wish[t.GetWishId()]
			if !t.GetScheduled() || w == nil || w.GetState() == planv1.WishState_WISH_STATE_GRANTED || render.ForkedAs(t, tasks) != "" {
				continue
			}
			t = proto.CloneOf(t)
			t.EnvReplays++
			t.Status, t.Error, t.WaitReason, t.ResumeAfter = planv1.TaskStatus_TASK_STATUS_RESUMING, "", byEnvReplay, nil
			ev := Event{
				Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS,
				Text: fmt.Sprintf("resuming: %s (%d of 2); Djinn starts it again by itself", byEnvReplay, t.GetEnvReplays()),
			}
			h.writeAlone(ctx, actorHarness, methodResume, t, t.GetId(), t, ev)
			_ = h.unblockDependencyFailed(ctx, t.GetCode())
		}
	}
	h.wake()
	return nil
}

// unblockDependencyFailed turns tasks that failed only because a dependency failed back to pending.
func (h *Harness) unblockDependencyFailed(ctx context.Context, dep string) error {
	tasks, err := store.List[*planv1.Task](ctx, h.store, nil)
	if err != nil {
		return err
	}
	for _, t := range tasks {
		if t.GetStatus() == planv1.TaskStatus_TASK_STATUS_FAILED {
			errStr := t.GetError()
			if strings.Contains(errStr, "ended failed") || strings.Contains(errStr, "is failed") ||
				(strings.Contains(errStr, "dependency") && strings.Contains(errStr, "fail")) {
				matches := dep == "" || strings.Contains(errStr, dep)
				if !matches && dep != "" {
					for _, d := range t.GetDependsOn() {
						if d == dep {
							matches = true
							break
						}
					}
				}
				if matches {
					t = proto.CloneOf(t)
					t.Status, t.StartTime, t.EndTime, t.Scheduled = planv1.TaskStatus_TASK_STATUS_PENDING, nil, nil, true
					t.Error = ""
					t.WaitReason = "waiting: dependency unblocked"
					ev := Event{
						Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS,
						Text: "waiting: dependency unblocked",
					}
					h.writeAlone(ctx, actorHarness, methodSchedule, t, t.GetId(), t, ev)
				}
			}
		}
	}
	h.wake()
	return nil
}

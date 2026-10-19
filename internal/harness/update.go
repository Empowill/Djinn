package harness

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/dispatch"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// Update edits a task in place: only the fields given change, in one journaled transaction.
// A task not started (planned, waiting) may change everything; a running or paused task may change its title and its
// urgent mark only (one urgent repair and one urgent feedback task per wish, refuseSecondUrgent);
// a finished task (done, failed, stopped, cut short) may change title, provider/model, prompt (for its next continue),
// azima, and integration (none clears it, pending puts a done task's work back in line); never its dependencies once
// it ran. An azima changes its title (renameAzima), its branch (nameAzimaBranch) or its rank (rankAzima) only.
func (h *Harness) Update(ctx context.Context, procedure string, req *planv1.TaskServiceUpdateRequest) (*planv1.Task, error) {
	id := req.GetTaskId()
	h.answering.Lock()
	defer h.answering.Unlock()
	logx.Lock(&h.sched, "scheduler")
	defer h.sched.Unlock()

	task, running, err := h.taskAndRun(ctx, id)
	if err != nil {
		return nil, err
	}
	if req.Rank != nil {
		if !plan.IsAzima(task) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
				"task %s is work: only an azima has a rank; its azima's rank orders it", task.GetCode()))
		}
		if req.Title != nil || req.Prompt != nil || req.Provider != nil || req.Model != nil || req.PartOf != nil || req.After != nil ||
			req.Decision != nil || req.MaxBudgetUsd != nil || req.WriteScopes != nil || req.Integration != nil || req.Urgent != nil || req.Feedback != nil ||
			req.Branch != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("--rank goes alone: rank an azima in a call of its own"))
		}
		return h.rankAzima(ctx, procedure, req, task)
	}
	if req.Branch != nil {
		if !plan.IsAzima(task) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
				"task %s is work: only an azima has a branch of its own; a work task's branch is Djinn's", task.GetCode()))
		}
		if req.Title != nil || req.Prompt != nil || req.Provider != nil || req.Model != nil || req.PartOf != nil || req.After != nil ||
			req.Decision != nil || req.MaxBudgetUsd != nil || req.WriteScopes != nil || req.Integration != nil || req.Urgent != nil || req.Feedback != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("--branch goes alone: name an azima's branch in a call of its own"))
		}
		return h.nameAzimaBranch(ctx, procedure, req, task)
	}
	if task.GetKind() == planv1.TaskKind_TASK_KIND_AZIMA {
		if req.Title == nil || req.Prompt != nil || req.Provider != nil || req.Model != nil || req.PartOf != nil || req.After != nil ||
			req.Decision != nil || req.MaxBudgetUsd != nil || req.WriteScopes != nil || req.Integration != nil || req.Urgent != nil || req.Feedback != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
				"task %s is an azima of the plan: no worker runs it; only its title, or its branch, changes", task.GetCode()))
		}
		return h.renameAzima(ctx, procedure, req, task)
	}

	titleGiven := req.Title != nil
	promptGiven := req.Prompt != nil
	providerGiven := req.Provider != nil
	modelGiven := req.Model != nil
	partOfGiven := req.PartOf != nil
	afterGiven := req.After != nil
	decisionGiven := req.Decision != nil
	budgetGiven := req.MaxBudgetUsd != nil
	scopesGiven := req.WriteScopes != nil
	integrationGiven := req.Integration != nil
	urgentGiven := req.Urgent != nil || req.Feedback != nil

	if !titleGiven && !promptGiven && !providerGiven && !modelGiven && !partOfGiven && !afterGiven && !decisionGiven && !budgetGiven && !scopesGiven && !integrationGiven && !urgentGiven {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("nothing to update: give at least one field"))
	}

	if task.GetProvider() == planv1.Provider_PROVIDER_WATCH {
		if providerGiven || modelGiven || req.GetUrgent() || req.GetFeedback() {
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task %s is a watcher: no agent runs it", task.GetCode()))
		}
	}

	if providerGiven {
		p := *req.Provider
		if p == planv1.Provider_PROVIDER_UNSPECIFIED || p == planv1.Provider_PROVIDER_WATCH {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("provider %s is not an agent", short(p)))
		}
	}

	s := task.GetStatus()
	if running && s != planv1.TaskStatus_TASK_STATUS_PAUSED {
		s = planv1.TaskStatus_TASK_STATUS_RUNNING
	}
	runningOrPaused := running || s == planv1.TaskStatus_TASK_STATUS_RUNNING || s == planv1.TaskStatus_TASK_STATUS_PAUSED
	hasRun := task.GetStartTime() != nil || task.GetSessionId() != "" || task.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING

	if runningOrPaused {
		if promptGiven {
			return nil, connect.NewError(connect.CodeFailedPrecondition,
				fmt.Errorf("task %s is %s: use djinn task send", task.GetCode(), short(s)))
		}
		if providerGiven || modelGiven || partOfGiven || afterGiven || decisionGiven || budgetGiven || scopesGiven || integrationGiven {
			return nil, connect.NewError(connect.CodeFailedPrecondition,
				fmt.Errorf("task %s is %s: the rest after it ends", task.GetCode(), short(s)))
		}
	} else if hasRun {
		if afterGiven {
			return nil, connect.NewError(connect.CodeFailedPrecondition,
				fmt.Errorf("task %s has run: never its dependencies once it ran", task.GetCode()))
		}
		if decisionGiven || budgetGiven || scopesGiven {
			return nil, connect.NewError(connect.CodeFailedPrecondition,
				fmt.Errorf("task %s has run: only title, prompt, provider, model and azima may change", task.GetCode()))
		}
	}

	t := proto.CloneOf(task)

	if titleGiven {
		t.Title = *req.Title
	}

	currentProvider := task.GetProvider()
	if currentProvider == planv1.Provider_PROVIDER_UNSPECIFIED {
		currentProvider = planv1.Provider_PROVIDER_CLAUDE
	}
	providerChanged := req.Provider != nil && (*req.Provider != currentProvider || task.GetProvider() != *req.Provider)
	if req.Provider != nil {
		t.Provider = *req.Provider
	}
	if providerChanged {
		if hasRun {
			t.PriorProvider = currentProvider
			t.SessionId = ""
		}
		if t.GetStatus() == planv1.TaskStatus_TASK_STATUS_RESUMING && t.GetResumeAfter() != nil {
			t.ResumeAfter = nil
			t.WaitReason = ""
		}
	}
	if req.Model != nil {
		if req.Provider == nil && *req.Model != "" {
			if inferred := plan.ProviderForCircleModel(*req.Model); inferred != planv1.Provider_PROVIDER_UNSPECIFIED && inferred != t.GetProvider() {
				return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("model %q belongs to %s, not %s", *req.Model, short(inferred), short(t.GetProvider())))
			}
		}
		norm, err := plan.ValidateTaskModel(t.GetProvider(), *req.Model)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		t.Model = norm
	} else if providerChanged || foreignModel(t.GetProvider(), t.GetModel()) {
		t.Model = DefaultModel(t.GetProvider())
	}

	if decisionGiven {
		if *req.Decision == "" {
			t.Decision = ""
		} else {
			d, err := plan.Decision(ctx, h.store, task.GetWishId(), *req.Decision)
			if err != nil {
				return nil, plan.Status(err)
			}
			t.Decision = d
		}
	}

	if budgetGiven {
		t.MaxBudgetUsd = *req.MaxBudgetUsd
	}

	if scopesGiven {
		scopes, err := cleanScopes(req.GetWriteScopes())
		if err != nil {
			return nil, err
		}
		t.WriteScopes = scopes
	}

	if integrationGiven {
		if task.GetIntegration().GetState() == planv1.IntegrationState_INTEGRATION_STATE_INTEGRATING {
			return nil, connect.NewError(connect.CodeFailedPrecondition,
				fmt.Errorf("task %s is integrating: refused while it merges", task.GetCode()))
		}
		switch req.GetIntegration() {
		case "pending":
			if err := h.pendAgain(task, t); err != nil {
				return nil, err
			}
		case "none":
			if task.GetBranch() != "" && !req.GetForce() {
				wish, _ := store.Get[*planv1.Wish](ctx, h.store, task.GetWishId())
				project, _ := store.Get[*planv1.Project](ctx, h.store, task.GetProjectId())
				if project != nil && project.GetGit() {
					intBranch, _ := h.integrationBranch(ctx, wish, project)
					if branchHoldsUnmergedCommits(ctx, project.GetDirectory(), task.GetBranch(), intBranch) {
						return nil, connect.NewError(connect.CodeFailedPrecondition,
							fmt.Errorf("task %s's branch holds commits not in the integration branch: refused unless --force is given", task.GetCode()))
					}
				}
			}
			t.Integration = nil
		default:
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("integration may only be set to none or pending"))
		}
	}

	tasks, err := store.List[*planv1.Task](ctx, h.store, store.Where{"wish_id": task.GetWishId()})
	if err != nil {
		return nil, plan.Status(err)
	}

	if urgentGiven {
		// --feedback marks it urgent for feedback, or takes the mark off; --urgent alone marks it a repair. --feedback=false
		// alone leaves a repair's mark: it takes off only the mark it names.
		switch {
		case req.Urgent == nil && !req.GetFeedback():
			t.Urgent, t.Feedback = t.GetUrgent() && !t.GetFeedback(), false
		default:
			t.Urgent, t.Feedback = req.GetUrgent() || req.GetFeedback(), req.GetFeedback()
		}
		if t.GetUrgent() && !dispatch.Urgent(t) {
			return nil, connect.NewError(connect.CodeFailedPrecondition,
				fmt.Errorf("task %s is %s: only a task to run or running may be urgent", task.GetCode(), short(task.GetStatus())))
		}
		if t.GetUrgent() {
			if err := refuseSecondUrgent(tasks, t); err != nil {
				return nil, err
			}
		}
	}

	newDeps := t.GetDependsOn()
	if afterGiven {
		deps, err := resolveIn(tasks, req.GetAfter(), "dependency")
		if err != nil {
			return nil, err
		}
		if slices.Contains(deps, task.GetId()) {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("task %s cannot wait for itself", task.GetCode()))
		}
		newDeps = deps
		t.DependsOn = newDeps
	}

	newAzima := t.GetPartOf()
	if partOfGiven {
		azima, err := resolveAzima(ctx, h.store, task.GetWishId(), *req.PartOf)
		if err != nil {
			return nil, err
		}
		if azima == task.GetId() {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("azima %s cannot be part of itself", task.GetCode()))
		}
		newAzima = azima
		t.PartOf = newAzima
	}

	if afterGiven || partOfGiven {
		if cycle := plan.ClosesCycle(task, newDeps, newAzima, tasks); cycle != "" {
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("the tasks of a wish form no cycle: %s", cycle))
		}
		wish, err := store.Get[*planv1.Wish](ctx, h.store, task.GetWishId())
		if err != nil {
			return nil, plan.Status(err)
		}
		if err := h.refuseAzimaDependencies(ctx, h.store, wish, t); err != nil {
			return nil, plan.Status(err)
		}
	}

	err = h.store.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal(plan.CallerActor(ctx), procedure, req); err != nil {
			return err
		}
		if promptGiven {
			if err := putEvents(ctx, tx, newEvent(id, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_PROMPT, Text: *req.Prompt})); err != nil {
				return err
			}
		}
		if partOfGiven && newAzima != "" && task.GetPartOf() != newAzima {
			if err := reopenAzima(ctx, tx, newAzima, task, "grouped"); err != nil {
				return err
			}
		}
		if integrationGiven && t.GetIntegration() == nil {
			if err := h.clearCorrectionLinks(ctx, tx, task.GetWishId(), task.GetId()); err != nil {
				return err
			}
		}
		if integrationGiven && t.GetIntegration().GetState() == planv1.IntegrationState_INTEGRATION_STATE_PENDING {
			if err := putEvents(ctx, tx, newEvent(id, Event{
				Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: "integration: pending, to be merged into the integration branch",
			})); err != nil {
				return err
			}
		}
		return tx.Put(t)
	})
	if err != nil {
		return nil, plan.Status(err)
	}

	if urgentGiven && t.GetUrgent() {
		// A running worker marked urgent stays beyond the slots: the load bar never pauses it (enforceNotchCapacity).
		h.mu.Lock()
		if r := h.runs[id]; r != nil {
			r.forced = true
		}
		h.mu.Unlock()
	}
	h.notify()
	h.wake()
	if integrationGiven {
		h.kickIntegrate()
	}

	filled, err := h.withAzimas(ctx, []*planv1.Task{t})
	if err != nil {
		return nil, plan.Status(err)
	}
	if err := plan.FillTilasms(ctx, h.store, filled); err != nil {
		return nil, plan.Status(err)
	}
	if err := plan.FillPrompts(ctx, h.store, filled); err != nil {
		return nil, plan.Status(err)
	}
	return filled[0], nil
}

// refuseSecondUrgent refuses to mark t urgent while another task of its wish is of its class (dispatch.UrgentOf),
// naming it: one urgent repair and one urgent feedback task at a time, or none passes the queue.
func refuseSecondUrgent(tasks []*planv1.Task, t *planv1.Task) error {
	if dispatch.Investigation(t) {
		return connect.NewError(connect.CodeInvalidArgument,
			errors.New("an investigation is served first already: --urgent and --feedback are for work"))
	}
	if o := dispatch.UrgentOf(tasks, t.GetWishId(), t.GetId(), t.GetFeedback()); o != nil {
		if t.GetFeedback() {
			return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
				"%s (%s) is urgent for feedback already: one urgent feedback task per wish at a time; take its mark off first: djinn task update %s --feedback=false",
				o.GetCode(), o.GetTitle(), o.GetCode()))
		}
		return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"%s (%s) is urgent already: one urgent repair per wish at a time (a feedback task may be urgent beside it); take its mark off first: djinn task update %s --urgent=false",
			o.GetCode(), o.GetTitle(), o.GetId()))
	}
	return nil
}

// pendAgain puts the work of task, done without an integration record, back in line to be merged in t, its copy: as
// when its worker ended (pendIntegration). It returns why not when Djinn does not integrate it.
func (h *Harness) pendAgain(task, t *planv1.Task) error {
	if task.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE || task.GetBranch() == "" {
		return connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("task %s is not done on a branch of its own: no work to integrate", task.GetCode()))
	}
	if task.GetIntegration() != nil {
		return connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("task %s's work is %s already", task.GetCode(),
				strings.ToLower(strings.TrimPrefix(task.GetIntegration().GetState().String(), "INTEGRATION_STATE_"))))
	}
	integrate, left := h.pendIntegration(t)
	switch {
	case integrate:
		return nil
	case left != "":
		return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task %s's work: %s", task.GetCode(), left))
	}
	return connect.NewError(connect.CodeFailedPrecondition,
		fmt.Errorf("task %s's work: Djinn does not integrate it: its branch brings nothing, or its project is not in Git", task.GetCode()))
}

// renameAzima gives an azima a new title, whatever its state, and writes it back to its plan file's heading when it has
// one: djinn plan sync then reads the same title again.
func (h *Harness) renameAzima(ctx context.Context, procedure string, req *planv1.TaskServiceUpdateRequest, azima *planv1.Task) (*planv1.Task, error) {
	title := strings.TrimSpace(req.GetTitle())
	if title == "" || strings.ContainsAny(title, "\r\n") {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("azima %s: a title is one line, not empty", azima.GetCode()))
	}
	if title == azima.GetTitle() {
		return h.withAzima(ctx, azima)
	}
	wish, err := store.Get[*planv1.Wish](ctx, h.store, azima.GetWishId())
	if err != nil {
		return nil, plan.Status(err)
	}
	if file := resolvePlanFilePath(ctx, h.store, azima, wish); file != "" {
		if _, err := plan.WriteTitle(file, azima.GetCode(), title); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("write the title of %s: %w", azima.GetCode(), err))
		}
	}
	t := proto.CloneOf(azima)
	t.Title = title
	err = h.store.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal(plan.CallerActor(ctx), procedure, req); err != nil {
			return err
		}
		return tx.Put(t)
	})
	if err != nil {
		return nil, plan.Status(err)
	}
	h.notify()
	return h.withAzima(ctx, t)
}

// UpdateModels changes the provider and model of every open task that calls a model (work, investigations,
// corrections, reviews, enlighten workers) and the defaults for future spawns, skipping watchers and finished
// tasks. Dry run by default; applied when req.GetYes() is true. Running tasks change at their next start, or
// now when req.GetNow() is true.
func (h *Harness) UpdateModels(ctx context.Context, procedure string, req *planv1.TaskServiceUpdateModelsRequest) (*planv1.TaskServiceUpdateModelsResponse, error) {
	if req.GetWishId() != "" && req.GetProjectId() != "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("give at most one of --wish-id or --project-id"))
	}
	if req.GetWishId() != "" {
		if _, err := store.Get[*planv1.Wish](ctx, h.store, req.GetWishId()); err != nil {
			return nil, plan.Status(err)
		}
	}
	if req.GetProjectId() != "" {
		if _, err := store.Get[*planv1.Project](ctx, h.store, req.GetProjectId()); err != nil {
			return nil, plan.Status(err)
		}
	}

	p := req.GetProvider()
	rawModel := strings.TrimSpace(req.GetModel())
	if p == planv1.Provider_PROVIDER_UNSPECIFIED && rawModel != "" {
		p = plan.ProviderForCircleModel(rawModel)
	}
	if p == planv1.Provider_PROVIDER_UNSPECIFIED || p == planv1.Provider_PROVIDER_WATCH {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("give --provider (claude, codex or antigravity) or a known --model"))
	}
	if _, ok := h.providers[p]; !ok && !plan.ValidWorkerProvider(p) {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("provider %s is not available", short(p)))
	}

	var model string
	if rawModel != "" {
		if inferred := plan.ProviderForCircleModel(rawModel); inferred != planv1.Provider_PROVIDER_UNSPECIFIED && inferred != p {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("model %q belongs to %s, not %s", rawModel, short(inferred), short(p)))
		}
		norm, err := plan.ValidateTaskModel(p, rawModel)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		model = norm
	}
	if model == "" {
		model = DefaultModel(p)
	}

	where := store.Where{}
	if id := req.GetWishId(); id != "" {
		where["wish_id"] = id
	}
	tasks, err := store.List[*planv1.Task](ctx, h.store, where)
	if err != nil {
		return nil, plan.Status(err)
	}
	if projectID := req.GetProjectId(); projectID != "" {
		wishes, err := store.List[*planv1.Wish](ctx, h.store, nil)
		if err != nil {
			return nil, plan.Status(err)
		}
		wishInProject := make(map[string]bool)
		for _, w := range wishes {
			if slices.Contains(w.GetProjectIds(), projectID) {
				wishInProject[w.GetId()] = true
			}
		}
		tasks = slices.DeleteFunc(tasks, func(t *planv1.Task) bool {
			return t.GetProjectId() != projectID && !wishInProject[t.GetWishId()]
		})
	}

	var (
		updated      []*planv1.Task
		skipped      []*planv1.Task
		changedIDs   []string
		runningCount int
	)

	for _, task := range tasks {
		if plan.IsAzima(task) || !openTask(task) {
			continue
		}
		if watching(task) || dispatch.Ifrit(task) {
			skipped = append(skipped, shortModelTask(task))
			continue
		}

		h.mu.Lock()
		r := h.runs[task.GetId()]
		h.mu.Unlock()
		isRunning := r != nil || task.GetStatus() == planv1.TaskStatus_TASK_STATUS_RUNNING || task.GetStatus() == planv1.TaskStatus_TASK_STATUS_PAUSED
		if isRunning {
			runningCount++
		}

		cp := proto.CloneOf(task)
		currentProvider := task.GetProvider()
		if currentProvider == planv1.Provider_PROVIDER_UNSPECIFIED {
			currentProvider = planv1.Provider_PROVIDER_CLAUDE
		}
		hasRun := isRunning || task.GetStartTime() != nil || task.GetSessionId() != "" || task.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING
		providerChanged := p != currentProvider || task.GetProvider() != p
		cp.Provider = p
		cp.Model = model
		if providerChanged {
			changedIDs = append(changedIDs, cp.GetId())
			if hasRun {
				cp.PriorProvider = currentProvider
				cp.SessionId = ""
			}
			if !isRunning && cp.GetStatus() == planv1.TaskStatus_TASK_STATUS_RESUMING && cp.GetResumeAfter() != nil {
				cp.ResumeAfter = nil
				cp.WaitReason = ""
			}
		}
		updated = append(updated, cp)
	}

	shortUpdated := make([]*planv1.Task, len(updated))
	for i, cp := range updated {
		shortUpdated[i] = shortModelTask(cp)
	}

	var defaults []*planv1.WorkerSeat
	if !req.GetYes() {
		defaults = plan.PreviewWorkerDefaults(p, model)
		runDetail := fmt.Sprintf("%d running", runningCount)
		if runningCount > 0 {
			if req.GetNow() {
				runDetail = fmt.Sprintf("%d running: would restart now", runningCount)
			} else {
				runDetail = fmt.Sprintf("%d running: at next start", runningCount)
			}
		}
		note := fmt.Sprintf("dry run: %d open task(s) would change to %s (%s) (%s, %d skipped) and defaults for future spawns; pass --yes to apply",
			len(updated), short(p), model, runDetail, len(skipped))
		return &planv1.TaskServiceUpdateModelsResponse{
			Text:     note,
			DryRun:   true,
			Provider: p,
			Model:    model,
			Tasks:    shortUpdated,
			Skipped:  skipped,
			Circle:   defaults,
		}, nil
	}

	defaults, err = plan.UpdateWorkerDefaults(h.home, p, model)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	h.mu.Lock()
	for _, cp := range updated {
		if r := h.runs[cp.GetId()]; r != nil {
			r.nextProvider = p
			r.nextModel = model
		}
	}
	h.mu.Unlock()

	err = h.store.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal(plan.CallerActor(ctx), procedure, req); err != nil {
			return err
		}
		for _, cp := range updated {
			if err := tx.Put(cp); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, plan.Status(err)
	}

	for _, id := range changedIDs {
		h.clearRateHits(id)
	}

	if req.GetNow() {
		why := fmt.Sprintf("switched to %s (%s)", short(p), model)
		var dones []chan struct{}
		h.mu.Lock()
		for _, cp := range updated {
			if r := h.runs[cp.GetId()]; r != nil {
				r.preemptedWhy = why
				r.stopping = true
				if pauser, ok := r.worker.(Pauser); ok && r.paused {
					_ = pauser.Resume()
				}
				if r.worker != nil {
					r.worker.Stop()
				}
				dones = append(dones, r.done)
			}
		}
		h.mu.Unlock()
		for _, done := range dones {
			select {
			case <-done:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}

	h.notify()
	h.wake()

	runDetail := fmt.Sprintf("%d running", runningCount)
	if runningCount > 0 {
		if req.GetNow() {
			runDetail = fmt.Sprintf("%d running: restarted now", runningCount)
		} else {
			runDetail = fmt.Sprintf("%d running: at next start", runningCount)
		}
	}
	note := fmt.Sprintf("updated %d open task(s) to %s (%s) (%s, %d skipped) and defaults for future spawns",
		len(updated), short(p), model, runDetail, len(skipped))
	return &planv1.TaskServiceUpdateModelsResponse{
		Text:     note,
		DryRun:   false,
		Provider: p,
		Model:    model,
		Tasks:    shortUpdated,
		Skipped:  skipped,
		Circle:   defaults,
	}, nil
}

func shortModelTask(t *planv1.Task) *planv1.Task {
	return &planv1.Task{
		Id:            t.GetId(),
		Code:          t.GetCode(),
		WishId:        t.GetWishId(),
		ProjectId:     t.GetProjectId(),
		Title:         t.GetTitle(),
		Status:        t.GetStatus(),
		Kind:          t.GetKind(),
		Role:          t.GetRole(),
		Provider:      t.GetProvider(),
		Model:         t.GetModel(),
		PriorProvider: t.GetPriorProvider(),
	}
}

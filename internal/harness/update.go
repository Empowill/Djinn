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
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// Update edits a task in place: only the fields given change, in one journaled transaction.
// A task not started (planned, waiting) may change everything; a running or paused task may change its title only;
// a finished task (done, failed, stopped, cut short) may change title, provider/model, prompt (for its next continue),
// azima, and integration (none clears it, pending puts a done task's work back in line); never its dependencies once
// it ran. An azima changes its branch only (nameAzimaBranch).
func (h *Harness) Update(ctx context.Context, procedure string, req *planv1.TaskServiceUpdateRequest) (*planv1.Task, error) {
	id := req.GetTaskId()
	h.answering.Lock()
	defer h.answering.Unlock()
	h.sched.Lock()
	defer h.sched.Unlock()

	task, err := store.Get[*planv1.Task](ctx, h.store, id)
	if err != nil {
		return nil, plan.Status(err)
	}
	if req.Branch != nil {
		if !plan.IsAzima(task) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
				"task %s is work: only an azima has a branch of its own; a work task's branch is Djinn's", task.GetCode()))
		}
		if req.Title != nil || req.Prompt != nil || req.Provider != nil || req.Model != nil || req.PartOf != nil || req.After != nil ||
			req.Decision != nil || req.MaxBudgetUsd != nil || req.WriteScopes != nil || req.Integration != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("--branch goes alone: name an azima's branch in a call of its own"))
		}
		return h.nameAzimaBranch(ctx, procedure, req, task)
	}
	if task.GetKind() == planv1.TaskKind_TASK_KIND_AZIMA {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task %s is an azima of the plan: no worker runs it", task.GetCode()))
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

	if !titleGiven && !promptGiven && !providerGiven && !modelGiven && !partOfGiven && !afterGiven && !decisionGiven && !budgetGiven && !scopesGiven && !integrationGiven {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("nothing to update: give at least one field"))
	}

	if task.GetProvider() == planv1.Provider_PROVIDER_WATCH {
		if providerGiven || modelGiven {
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task %s is a watcher: no agent runs it", task.GetCode()))
		}
	}

	if providerGiven {
		p := *req.Provider
		if p == planv1.Provider_PROVIDER_UNSPECIFIED || p == planv1.Provider_PROVIDER_WATCH {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("provider %s is not an agent", short(p)))
		}
	}

	h.mu.Lock()
	running := h.runs[id] != nil
	h.mu.Unlock()
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
		t.Model = *req.Model
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
			seq, err := lastSeq(ctx, tx, id)
			if err != nil {
				return err
			}
			if err := tx.Put(newEvent(id, seq+1, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_PROMPT, Text: *req.Prompt})); err != nil {
				return err
			}
		}
		if partOfGiven && newAzima != "" && task.GetPartOf() != newAzima {
			if err := reopenAzima(ctx, tx, newAzima, task.GetCode()+" grouped"); err != nil {
				return err
			}
		}
		if integrationGiven && t.GetIntegration() == nil {
			if err := h.clearCorrectionLinks(ctx, tx, task.GetWishId(), task.GetId()); err != nil {
				return err
			}
		}
		if integrationGiven && t.GetIntegration().GetState() == planv1.IntegrationState_INTEGRATION_STATE_PENDING {
			seq, err := lastSeq(ctx, tx, id)
			if err != nil {
				return err
			}
			if err := tx.Put(newEvent(id, seq+1, Event{
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
	if p, err := lastPrompt(ctx, h.store, t.GetId()); err == nil {
		filled[0].Prompt = p
	}
	return filled[0], nil
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

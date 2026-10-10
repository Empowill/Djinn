package harness

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// SetAgent changes the provider and/or the model of a task no worker runs now: planned, waiting, failed, stopped, cut
// short (for a finished one, its next djinn task continue uses them). A running or paused task is refused, saying so
// (stop it first). Only what is given changes; an unknown model for the provider is accepted as given, an empty
// --model means the provider's default. Its prompt, azima, decision, dependencies, dependents and code stay: nothing
// is spawned again.
func (h *Harness) SetAgent(ctx context.Context, procedure string, req *planv1.TaskServiceSetAgentRequest) (*planv1.Task, error) {
	id := req.GetTaskId()
	h.answering.Lock()
	defer h.answering.Unlock()
	h.sched.Lock()
	defer h.sched.Unlock()

	task, err := store.Get[*planv1.Task](ctx, h.store, id)
	if err != nil {
		return nil, plan.Status(err)
	}
	if task.GetKind() == planv1.TaskKind_TASK_KIND_AZIMA {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task %s is an azima of the plan: no worker runs it", task.GetCode()))
	}
	if task.GetProvider() == planv1.Provider_PROVIDER_WATCH {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task %s is a watcher: no agent runs it", task.GetCode()))
	}

	h.mu.Lock()
	running := h.runs[id] != nil
	h.mu.Unlock()
	s := task.GetStatus()
	if running && s != planv1.TaskStatus_TASK_STATUS_PAUSED {
		s = planv1.TaskStatus_TASK_STATUS_RUNNING
	}
	if running || s == planv1.TaskStatus_TASK_STATUS_RUNNING || s == planv1.TaskStatus_TASK_STATUS_PAUSED {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("task %s is %s: stop it first (djinn task stop)", task.GetCode(), short(s)))
	}

	if req.Provider == nil && req.Model == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("give --provider, --model, or both"))
	}
	if req.Provider != nil {
		p := *req.Provider
		if p == planv1.Provider_PROVIDER_UNSPECIFIED || p == planv1.Provider_PROVIDER_WATCH {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("provider %s is not an agent", short(p)))
		}
	}

	currentProvider := task.GetProvider()
	if currentProvider == planv1.Provider_PROVIDER_UNSPECIFIED {
		currentProvider = planv1.Provider_PROVIDER_CLAUDE
	}

	t := proto.CloneOf(task)
	providerChanged := req.Provider != nil && (*req.Provider != currentProvider || task.GetProvider() != *req.Provider)
	if req.Provider != nil {
		t.Provider = *req.Provider
	}
	if providerChanged {
		if task.GetStartTime() != nil || task.GetSessionId() != "" || task.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING {
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

	err = h.store.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal(actorLocal, procedure, req); err != nil {
			return err
		}
		return tx.Put(t)
	})
	if err != nil {
		return nil, plan.Status(err)
	}

	h.notify()
	h.wake()

	filled, err := h.withAzimas(ctx, []*planv1.Task{t})
	if err != nil {
		return nil, plan.Status(err)
	}
	if err := plan.FillTilasms(ctx, h.store, filled); err != nil {
		return nil, plan.Status(err)
	}
	if p, err := firstPrompt(h.store, t.GetId()); err == nil {
		filled[0].Prompt = p
	}
	return filled[0], nil
}

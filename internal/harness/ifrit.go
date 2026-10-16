package harness

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/machine"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// DefaultIfritIdle is how long the developer may leave Djinn alone before it puts an ifrit out, when neither the ifrit,
// djinn up --ifrit-idle nor the window's settings say (T51).
const DefaultIfritIdle = 30 * time.Minute

// WithIfritIdle sets how long the developer may leave Djinn alone before it puts an ifrit out, for an ifrit spawned
// without its own (djinn up --ifrit-idle); zero leaves it to the window's settings.
func WithIfritIdle(d time.Duration) Option { return func(h *Harness) { h.ifritIdle = d } }

// IfritIdle is how long the developer may leave Djinn alone before it puts the ifrit t out: its own (djinn task spawn
// --idle-minutes), else djinn up --ifrit-idle, else the window's settings, else DefaultIfritIdle.
func (h *Harness) IfritIdle(t *planv1.Task) time.Duration {
	if m := t.GetIfrit().GetIdleMinutes(); m > 0 {
		return time.Duration(m) * time.Minute
	}
	if h.ifritIdle > 0 {
		return h.ifritIdle
	}
	if m := machine.ReadIfritIdle(h.home); m > 0 {
		return time.Duration(m) * time.Minute
	}
	return DefaultIfritIdle
}

// ifritSpawn makes task, about to be spawned by req for an agent of provider kind in project, an ifrit when req asks
// for one: what it stands at is stored, nothing more yet.
func ifritSpawn(req *planv1.TaskServiceSpawnRequest, kind planv1.Provider, project *planv1.Project, task *planv1.Task) error {
	if req.GetKind() != planv1.TaskKind_TASK_KIND_IFRIT {
		if req.GetIdleMinutes() != 0 {
			return connect.NewError(connect.CodeInvalidArgument, errors.New("--idle-minutes is for an ifrit (--kind ifrit)"))
		}
		return nil
	}
	switch {
	case kind == planv1.Provider_PROVIDER_WATCH:
		return connect.NewError(connect.CodeInvalidArgument, errors.New("an ifrit is an agent: a watcher runs a command"))
	case kind == planv1.Provider_PROVIDER_CODEX:
		return connect.NewError(connect.CodeInvalidArgument, errors.New("an ifrit runs claude or agy: Djinn does not keep codex's input open yet"))
	case project == nil:
		return connect.NewError(connect.CodeInvalidArgument, errors.New("an ifrit works in a project: the wish has none"))
	}
	task.Kind = planv1.TaskKind_TASK_KIND_IFRIT
	task.Ifrit = &planv1.TaskIfrit{IdleMinutes: req.GetIdleMinutes()}
	return nil
}

// An ifrit's agent keeps its input open between turns (Spec.Listen): it works on a turn (IFRIT_STATE_WORKING) and
// takes a slot then, or listens (IFRIT_STATE_LISTENING), its process blocked on its input, spending nothing. While it
// listens it takes no slot (slotted), the Lamplighter leaves its silence alone, and its duration budget does not count
// the time (listenedBy). It starts at once whatever the slots, like a task started by hand (dispatch, newRun: forced),
// so no load notch pauses it and no investigator preempts it. djinn task done closes its input (doneIfrit): its agent
// ends, and its work integrates like any task's.

// listeningEvent is what a worker started with Spec.Listen says once a turn of its agent ended and the agent waits on
// its open input.
func listeningEvent(agent string) Event {
	return Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Ifrit: planv1.IfritState_IFRIT_STATE_LISTENING,
		Text: agent + " listens: its turn ended, its input stays open for the next message"}
}

// workingEvent is what it says once a message starts the next turn.
func workingEvent(agent string) Event {
	return Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Ifrit: planv1.IfritState_IFRIT_STATE_WORKING,
		Text: "a message came: " + agent + " works on it"}
}

// ifritTurn records that the ifrit of r starts a turn or listens, as its worker said: listening, it frees its slot.
// The pump calls it, as it owns the task.
func (h *Harness) ifritTurn(r *run, state planv1.IfritState) {
	now := h.now()
	h.mu.Lock()
	switch {
	case state == planv1.IfritState_IFRIT_STATE_LISTENING && r.listenSince.IsZero():
		r.listenSince = now
	case state == planv1.IfritState_IFRIT_STATE_WORKING && !r.listenSince.IsZero():
		r.listened += now.Sub(r.listenSince)
		r.listenSince = time.Time{}
	}
	listened := r.listened
	h.mu.Unlock()
	if state == planv1.IfritState_IFRIT_STATE_WORKING {
		r.failure, r.limit = "", nil // What a turn before said is not what this one says.
	}
	setIfrit(r.task, state, listened)
	if state == planv1.IfritState_IFRIT_STATE_LISTENING {
		h.wake() // Its slot is free.
	}
}

// listenedBy is how long the ifrit of r listened by now, over all its runs. The caller holds h.mu.
func (r *run) listenedBy(now time.Time) time.Duration {
	if r.listenSince.IsZero() {
		return r.listened
	}
	return r.listened + now.Sub(r.listenSince)
}

// setIfrit sets where the ifrit t stands, and how long it listened. Replaced, never changed: a copy of the task taken
// before shares it.
func setIfrit(t *planv1.Task, state planv1.IfritState, listened time.Duration) {
	ifrit := &planv1.TaskIfrit{}
	if t.GetIfrit() != nil {
		ifrit = proto.CloneOf(t.GetIfrit())
	}
	ifrit.State, ifrit.Listened = state, nil
	if listened > 0 {
		ifrit.Listened = durationpb.New(listened)
	}
	t.Ifrit = ifrit
}

// doneIfrit ends the lit ifrit task as req asks: its input closes once the turn under way, if any, has its result,
// and its agent ends; the task then ends as any worker's does, done, its work integrated. It returns the task as it
// is now, still running.
func (h *Harness) doneIfrit(ctx context.Context, procedure string, req *planv1.TaskServiceDoneRequest, task *planv1.Task) (*planv1.Task, error) {
	h.mu.Lock()
	r := h.runs[task.GetId()]
	var input Listener
	if r != nil && !r.stopping && !r.final {
		input, _ = r.worker.(Listener)
	}
	h.mu.Unlock()
	if input == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("ifrit %s is %s: stop it first (djinn task stop)", task.GetCode(), short(task.GetStatus())))
	}
	by := req.GetBy()
	if by == planv1.Closer_CLOSER_PLAN_FILE {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("only djinn plan sync closes a task for its plan file"))
	}
	if by == planv1.Closer_CLOSER_UNSPECIFIED {
		by = planv1.Closer_CLOSER_LEAD
	}
	text := fmt.Sprintf("ended by the %s: its input closes once its turn ends, and its work integrates", plan.CloserWord(by))
	if n := req.GetNote(); n != "" {
		text += ": " + n
	}
	if err := h.store.Tx(ctx, func(tx *store.Tx) error { return tx.Journal(actorLocal, procedure, req) }); err != nil {
		return nil, plan.Status(err)
	}
	select {
	case r.notes <- Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: text}:
	case <-r.done:
	}
	input.CloseInput()
	return task, nil
}

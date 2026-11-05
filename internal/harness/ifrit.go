package harness

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/dispatch"
	"github.com/empowill/djinn/internal/logx"
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
	if input != nil {
		r.ending, r.out = true, "" // Done, even when it was going out.
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

// An ifrit stays lit while the developer is there (Presence): once they left Djinn alone for its idle delay
// (IfritIdle), counted from their last action or from when it was lit, whichever is later, it goes out. Djinn warns
// ifritWarnAhead before (WarnIfrits: a system notification, with Keep lit), and an action of theirs clears the
// warning. A turn under way ends first: the ifrit goes out once it listens. Out, its input closes, its agent ends, and
// it gives its slot and gates back; its task is stopped, Task.ifrit OUT and why, its worktree, branch and session
// kept; the lead hears of it (LEAD_EVENT_KIND_OUT). djinn task send lights it again (relight): its next worker
// resumes its session on the message. A Djinn restart brings a lit ifrit back out (putOut), as the restart line says.

// ifritWarnAhead is how long before an ifrit goes out Djinn warns the developer.
const ifritWarnAhead = 5 * time.Minute

// methodIfrit is journaled when an ifrit's timers move; the request is where it stands.
const methodIfrit = "harness/ifrit"

// WarnIfrits sets what shows the developer that an ifrit goes out soon (Task.ifrit.out_time): djinn up's system
// notification, whose Keep lit button touches the Presence. It must not wait.
func (h *Harness) WarnIfrits(warned func(*planv1.Task)) {
	h.mu.Lock()
	h.ifritWarned = warned
	h.mu.Unlock()
}

// ifritOut tells whether t is an ifrit put out: a message lights it again.
func ifritOut(t *planv1.Task) bool {
	return dispatch.Ifrit(t) && t.GetIfrit().GetState() == planv1.IfritState_IFRIT_STATE_OUT
}

// pokeIfrits has the pump of every lit ifrit look at its timers (ifritTimers): the developer did something, or time
// went by (each scheduler pass).
func (h *Harness) pokeIfrits() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.runs {
		if r.ifrit && !r.final && !r.stopping {
			select {
			case r.timers <- struct{}{}:
			default: // Its pump has one to look at already.
			}
		}
	}
}

// ifritTimers puts the ifrit of r out once the developer left Djinn alone for its idle delay and it listens, warns
// them before, and keeps Task.ifrit's out and warning times as the developer's last action sets them. The pump calls
// it, as it owns the task.
func (h *Harness) ifritTimers(r *run) {
	now := h.now()
	h.mu.Lock()
	lit, listening, input, warn := r.startTime, !r.listenSince.IsZero(), r.worker, h.ifritWarned
	going := r.out != "" || r.ending || r.stopping || r.final || input == nil
	h.mu.Unlock()
	if going {
		return
	}
	since := h.presence.Last()
	if since.Before(lit) {
		since = lit // Lit at the developer's word: they were there.
	}
	idle := h.IfritIdle(r.task)
	outAt := since.Add(idle)
	h.mu.Lock()
	r.outAt = outAt // Its gates' expire time (Ifrit).
	h.mu.Unlock()
	ifrit := r.task.GetIfrit()
	warned := ifrit.GetWarnTime() != nil
	switch {
	case !now.Before(outAt) && listening:
		closer, ok := input.(Listener)
		if !ok {
			return
		}
		why := "out: you left Djinn alone at " + ClockText(since, now)
		h.mu.Lock()
		r.out = why
		h.mu.Unlock()
		h.write(r, actorHarness, methodEvent, nil, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS,
			Text: fmt.Sprintf("going out: nothing from you in Djinn for %s: its input closes", minutesText(now.Sub(since)))})
		closer.CloseInput()
	case !now.Before(outAt.Add(-ifritWarnAhead)):
		if warned && ifrit.GetOutTime().AsTime().Equal(outAt) {
			return // Warned of this out time already.
		}
		h.setIfritTimes(r, outAt, outAt.Add(-ifritWarnAhead))
		h.write(r, actorHarness, methodEvent, r.task, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS,
			Text: "goes out at " + ClockText(outAt, now) + " unless you come back to Djinn: Keep lit keeps it"})
		if warn != nil {
			warn(proto.CloneOf(r.task))
		}
	case warned:
		h.setIfritTimes(r, outAt, time.Time{})
		h.write(r, actorHarness, methodEvent, r.task, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS,
			Text: "kept lit: you came back; it goes out at " + ClockText(outAt, now) + " if you leave"})
	case ifrit.GetOutTime() == nil || outAt.Sub(ifrit.GetOutTime().AsTime()) >= time.Minute:
		// Pushed back by the developer's actions: written a minute at a time, with no event.
		h.setIfritTimes(r, outAt, time.Time{})
		err := h.store.Tx(context.Background(), func(tx *store.Tx) error {
			if err := tx.Journal(actorHarness, methodIfrit, r.task.GetIfrit()); err != nil {
				return err
			}
			carry(tx, r.task)
			return tx.Put(r.task)
		})
		if err != nil {
			logx.L().Error("record when an ifrit goes out", "task", r.id, "err", err)
		}
	}
}

// Ifrit tells the gates whether the task taskID is an ifrit lit now, and when it goes out unless the developer comes
// back: its gates have no timeout, their expire time is its out time, and they go back once it goes out or ends
// (Q11). It is lit until its run ends, a djinn task done waiting for the turn under way included, unless it goes out;
// then why says why, from its task once its run is over. A task that is no ifrit is not lit, with no why.
func (h *Harness) Ifrit(ctx context.Context, taskID string) (lit bool, out time.Time, why string) {
	h.mu.Lock()
	r := h.runs[taskID]
	if r != nil {
		defer h.mu.Unlock()
		switch {
		case !r.ifrit:
			return false, time.Time{}, ""
		case r.out != "":
			return false, time.Time{}, "went " + r.out
		}
		return true, r.outAt, ""
	}
	h.mu.Unlock()
	t, err := store.Get[*planv1.Task](ctx, h.store, taskID)
	switch {
	case err != nil || !dispatch.Ifrit(t):
		return false, time.Time{}, ""
	case ifritOut(t):
		return false, time.Time{}, "went " + t.GetError()
	case t.GetStatus() == planv1.TaskStatus_TASK_STATUS_RUNNING:
		return true, time.Time{}, "" // Between its run's end and its final status: asked again at the next tick.
	case t.GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE:
		return false, time.Time{}, "ended"
	}
	return false, time.Time{}, "is " + short(t.GetStatus())
}

// setIfritTimes sets when the ifrit of r goes out, and when it was warned (zero: not yet). Replaced, never changed: a
// copy of the task taken before shares it.
func (h *Harness) setIfritTimes(r *run, outAt, warnAt time.Time) {
	ifrit := proto.CloneOf(r.task.GetIfrit())
	ifrit.OutTime, ifrit.WarnTime = timestamppb.New(outAt), nil
	if !warnAt.IsZero() {
		ifrit.WarnTime = timestamppb.New(warnAt)
	}
	r.task.Ifrit = ifrit
}

// minutesText writes d in whole minutes: "30 min".
func minutesText(d time.Duration) string {
	return fmt.Sprintf("%d min", int(d.Round(time.Minute)/time.Minute))
}

// tellOut tells the lead of t's wish that the ifrit t went out, and why.
func (h *Harness) tellOut(t *planv1.Task) {
	line := fmt.Sprintf("%s (ifrit) is %s; a message lights it again (djinn task send %s)", t.GetCode(), t.GetError(), t.GetCode())
	if err := h.toLead(context.Background(), planv1.LeadEventKind_LEAD_EVENT_KIND_OUT, t.GetWishId(), t.GetCode(), line); err != nil {
		logx.L().Warn("tell the lead an ifrit is out", "task", t.GetCode(), "err", err)
	}
}

// whyRestartOut is why an ifrit a restart brought back is out.
const whyRestartOut = "out: Djinn restarted"

// putOut puts out the ifrit t that a djinn up left lit (running, or interrupted as it stopped): stopped, Task.ifrit
// OUT, its worktree, branch and session kept. Nothing is resumed by itself: no turn is paid for nothing. Recover
// calls it, and changes t in place, so that the rest of Recover sees it out.
func (h *Harness) putOut(ctx context.Context, t *planv1.Task) error {
	now := timestamppb.New(h.now())
	if t.GetStatus() != planv1.TaskStatus_TASK_STATUS_INTERRUPTED {
		t.EndTime = now
	}
	if len(t.Runs) > 0 {
		if last := t.Runs[len(t.Runs)-1]; last.EndTime == nil {
			last.EndTime, last.Reason = timestamppb.New(h.lastSeen(ctx, t, last)), "interrupted"
		}
	}
	t.Status, t.Error, t.WaitReason, t.ResumeAfter = planv1.TaskStatus_TASK_STATUS_STOPPED, whyRestartOut, "", nil
	setIfrit(t, planv1.IfritState_IFRIT_STATE_OUT, t.GetIfrit().GetListened().AsDuration())
	t.Ifrit.OutTime, t.Ifrit.WarnTime = nil, nil
	return h.store.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal(actorHarness, methodRecover, t); err != nil {
			return err
		}
		if err := tx.Put(t); err != nil {
			return err
		}
		return putEvents(ctx, tx, h.newEvent(t.GetId(), Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS,
			Text: whyRestartOut + "; its worktree, branch and session are kept, a message lights it again"}))
	})
}

// errGoingOut is why a message did not reach an ifrit: it goes out. Send lights it again once it is out.
var errGoingOut = errors.New("the ifrit goes out")

// relight lights the out ifrit task again for the message of req, as djinn task continue would: its next worker
// resumes its session in its worktree at once, its turn the message, told how long it was out. It returns the
// message's prompt event.
func (h *Harness) relight(ctx context.Context, req *planv1.TaskServiceSendRequest, task *planv1.Task) (*planv1.TaskEvent, error) {
	away := h.now().Sub(task.GetEndTime().AsTime())
	prompt := fmt.Sprintf("You were out for %s; Djinn lit you again for this message.\n\n%s", minutesText(away), req.GetText())
	if _, err := h.Continue(ctx, planv1connect.TaskServiceContinueProcedure, &planv1.TaskServiceContinueRequest{
		TaskId: task.GetId(), Prompt: &prompt, By: planv1.Closer_CLOSER_LEAD,
	}); err != nil {
		return nil, err
	}
	var ev *planv1.TaskEvent
	err := store.Latest(ctx, h.store, store.Where{"task_id": task.GetId()}, func(e *planv1.TaskEvent) bool {
		if e.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_PROMPT {
			ev = e
		}
		return ev == nil
	})
	return ev, plan.Status(err)
}

package harness

import (
	"cmp"
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// message is an instruction for a running worker, on its way to the pump.
type message struct {
	procedure string
	req       *planv1.TaskServiceSendRequest
	reply     chan sent
	actor     string // who the journal says sent it; "" for the person (actorLocal)
	// betweenTurns holds the message while a tool call of the worker has no result yet: the pump writes it once the
	// worker's turn is over (take). It is answered at once all the same, with no event while held.
	betweenTurns bool
}

// sent is what delivering a message gave: its event, or why it was not delivered.
type sent struct {
	event *planv1.TaskEvent
	err   error
}

// Send gives a running worker a message, an instruction added while it works. The pump writes it on the worker's
// input and records it as a MESSAGE event; the first thing the worker says after it is preceded by a RECEIVED
// event. A worker that ends first never acknowledges it. An ifrit out, or going out, is lit again for the message
// (relight).
func (h *Harness) Send(ctx context.Context, procedure string, req *planv1.TaskServiceSendRequest) (*planv1.TaskEvent, error) {
	task, err := store.Get[*planv1.Task](ctx, h.store, req.GetTaskId())
	if err != nil {
		return nil, plan.Status(err)
	}
	h.mu.Lock()
	r := h.runs[task.GetId()]
	ready := r != nil && r.worker != nil && !r.final && !r.stopping && r.out == ""
	going := r != nil && r.out != "" && !r.stopping
	h.mu.Unlock()
	if ready {
		res, err := h.sendTo(ctx, r, task.GetCode(), &message{procedure: procedure, req: req, reply: make(chan sent, 1)})
		if !errors.Is(err, errGoingOut) {
			return res, err
		}
		going = true
	}
	if going { // It goes out as its turn ended: lit again once it is.
		select {
		case <-r.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if task, err = store.Get[*planv1.Task](ctx, h.store, req.GetTaskId()); err != nil {
			return nil, plan.Status(err)
		}
	}
	if ifritOut(task) {
		return h.relight(ctx, req, task)
	}
	return nil, connect.NewError(connect.CodeFailedPrecondition,
		fmt.Errorf("task %s is not running: %s", task.GetCode(), short(task.GetStatus())))
}

// sendTo hands m to the pump of r, the run of the task code, and returns what delivering it gave.
func (h *Harness) sendTo(ctx context.Context, r *run, code string, m *message) (*planv1.TaskEvent, error) {
	select {
	case r.sends <- m:
	case <-r.done:
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task %s is not running: it ended", code))
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	// The pump answers every message it takes, at once.
	res := <-m.reply
	return res.event, res.err
}

// take is what the pump does with m: it delivers it, or holds it when it waits for the worker's turn to be over
// (betweenTurns) and a tool call of the worker has no result yet. A newer message held replaces the one before.
func (h *Harness) take(r *run, m *message) sent {
	if m.betweenTurns && r.tools > 0 {
		r.held = m
		return sent{}
	}
	return h.deliver(r, m)
}

// turnOver delivers the message held for the end of the worker's turn, now that no tool call of the worker waits for
// its result; the pump calls it.
func (h *Harness) turnOver(r *run) {
	m := r.held
	if m == nil || r.tools > 0 {
		return
	}
	r.held = nil
	if res := h.deliver(r, m); res.err != nil {
		logx.L().Info("a message held for the end of a turn is lost", "task", r.task.GetCode(), "err", res.err)
	}
}

// deliver writes a message on the worker's input, then records it; the pump calls it. An ifrit going out takes none.
func (h *Harness) deliver(r *run, m *message) sent {
	h.mu.Lock()
	going := r.out != ""
	h.mu.Unlock()
	if going {
		return sent{err: errGoingOut}
	}
	if err := r.worker.Send(m.req.GetText()); err != nil {
		code := connect.CodeInternal
		if errors.Is(err, ErrClosed) {
			code = connect.CodeFailedPrecondition
		}
		return sent{err: connect.NewError(code, fmt.Errorf("task %s: %w", r.task.GetCode(), err))}
	}
	ev, err := h.writeAs(r, cmp.Or(m.actor, actorLocal), m.procedure, m.req, nil,
		Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_MESSAGE, Text: m.req.GetText()})
	if err != nil {
		return sent{err: plan.Status(fmt.Errorf("the worker has the message, but it is not recorded: %w", err))}
	}
	r.unread = append(r.unread, m.req.GetText())
	return sent{event: ev}
}

// acknowledge records that the worker took its unread messages in, when ev is something it says: text, or a tool
// it calls. What comes on its error output, its usage or its other lines do not show that it read anything.
func (h *Harness) acknowledge(r *run, ev Event) {
	if len(r.unread) == 0 {
		return
	}
	switch ev.Kind {
	case planv1.TaskEventKind_TASK_EVENT_KIND_TEXT, planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_CALL:
	default:
		return
	}
	for _, text := range r.unread {
		h.write(r, actorHarness, methodReceived, nil, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_RECEIVED, Text: text})
	}
	r.unread = nil
}

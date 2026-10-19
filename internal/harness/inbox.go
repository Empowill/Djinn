package harness

import (
	"cmp"
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

// message is an instruction for a running worker, on its way to the pump.
type message struct {
	procedure string
	req       *planv1.TaskServiceSendRequest
	reply     chan sent
	actor     string // who the journal says sent it; "" for the person (actorLocal)
	// betweenTurns holds the message while a tool call of the worker has no result yet: the pump writes it once the
	// worker's turn is over (take). It is answered at once all the same, with no event while held.
	betweenTurns bool
	// publish is set on Djinn's message that asks an investigation to publish (askToPublish): what it reached.
	publish string
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
	task, err := h.resolveTaskID(ctx, req.GetTaskId(), req.GetWishId())
	if err != nil {
		return nil, err
	}
	if req.GetTaskId() != task.GetId() || req.GetWishId() != "" {
		req = proto.CloneOf(req)
		req.TaskId, req.WishId = task.GetId(), ""
	}
	h.mu.Lock()
	r := h.runs[task.GetId()]
	ready := r != nil && r.worker != nil && !r.final && !r.stopping && r.out == ""
	ending := r != nil && (r.out != "" || r.final || r.stopping)
	h.mu.Unlock()
	if ready {
		res, err := h.sendTo(ctx, r, task.GetCode(), &message{procedure: procedure, req: req, reply: make(chan sent, 1)})
		if !errors.Is(err, errGoingOut) {
			return res, err
		}
		ending = true
	}
	if ending { // A run ending (or an ifrit going out) writes its new status before r.done closes.
		select {
		case <-r.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if ending || task.GetStatus() == planv1.TaskStatus_TASK_STATUS_RUNNING {
		if task, err = store.Get[*planv1.Task](ctx, h.store, req.GetTaskId()); err != nil {
			return nil, plan.Status(err)
		}
	}
	if ifritOut(task) {
		return h.relight(ctx, req, task)
	}
	state := short(task.GetStatus())
	if state == "running" {
		state = "starting"
	}
	return nil, connect.NewError(connect.CodeFailedPrecondition,
		fmt.Errorf("task %s is not running: %s", task.GetCode(), state))
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
	if m.publish != "" && dispatch.ChangesNoCode(r.task) && changesNoCodeDone(h.ctx, h.store, r.task) {
		return
	}
	if res := h.deliver(r, m); res.err != nil {
		logx.L().Info("a message held for the end of a turn is lost", "task", r.task.GetCode(), "err", res.err)
	}
}

// deliver writes a message on the worker's input, then records it; the pump calls it. An ifrit going out takes none.
func (h *Harness) deliver(r *run, m *message) sent {
	h.mu.Lock()
	going := r.out != ""
	midAgyTurn := r.task.GetProvider() == planv1.Provider_PROVIDER_ANTIGRAVITY && r.resultAt.IsZero() && r.listenSince.IsZero()
	h.mu.Unlock()
	if going {
		return sent{err: errGoingOut}
	}
	if m.publish != "" && dispatch.ChangesNoCode(r.task) && changesNoCodeDone(h.ctx, h.store, r.task) {
		return sent{}
	}
	if err := r.worker.Send(m.req.GetText()); err != nil {
		code := connect.CodeInternal
		if errors.Is(err, ErrClosed) {
			code = connect.CodeFailedPrecondition
		}
		return sent{err: connect.NewError(code, fmt.Errorf("task %s: %w", r.task.GetCode(), err))}
	}
	actor, task := cmp.Or(m.actor, actorLocal), proto.Message(nil)
	if m.publish != "" {
		actor, task = actorHarness, published(r, m.publish, h.now())
	}
	ev, err := h.writeAs(r, actor, m.procedure, m.req, task,
		Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_MESSAGE, Text: m.req.GetText()})
	if err != nil {
		return sent{err: plan.Status(fmt.Errorf("the worker has the message, but it is not recorded: %w", err))}
	}
	if midAgyTurn {
		r.unreadWaitTurn = true
	}
	r.unread = append(r.unread, m.req.GetText())
	return sent{event: ev}
}

// acknowledge records that the worker took its unread messages in, when ev is something it says: text, or a tool
// it calls. What comes on its error output, its usage or its other lines do not show that it read anything.
// On Antigravity, messages delivered while a turn is under way wait for the next turn to speak before being
// acknowledged, as agy queues stdin lines between turns instead of injecting them mid-turn.
func (h *Harness) acknowledge(r *run, ev Event) {
	if len(r.unread) == 0 || r.unreadWaitTurn {
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

// unreceived are the messages the task's last worker took on its input and said nothing after, the oldest first: it
// ended before it read them (stopped while Djinn held it paused, cut for an idle stream, woken by the Lamplighter, a
// restart), and its session never had them. A worker started again is told them with its prompt (W226: W219 lost the
// developer's message, sent while Djinn held it paused, to the continuation that followed its idle stream).
// Messages a worker ended done after, or took as answered by the turn they came in (Quiet), are no longer waiting.
func unreceived(ctx context.Context, r store.Reader, taskID string) ([]string, error) {
	var texts []string
	err := store.Latest(ctx, r, store.Where{"task_id": taskID}, func(ev *planv1.TaskEvent) bool {
		switch ev.GetKind() {
		case planv1.TaskEventKind_TASK_EVENT_KIND_MESSAGE:
			texts = append(texts, ev.GetText())
		case planv1.TaskEventKind_TASK_EVENT_KIND_TEXT, planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_CALL,
			planv1.TaskEventKind_TASK_EVENT_KIND_RECEIVED:
			return false
		case planv1.TaskEventKind_TASK_EVENT_KIND_STATUS:
			text := ev.GetText()
			if text == short(planv1.TaskStatus_TASK_STATUS_DONE) || strings.HasSuffix(text, takenAsAnswered) {
				return false
			}
		}
		return true
	})
	slices.Reverse(texts)
	return texts, err
}

// unreceivedText tells a worker started again the messages its last worker never read.
func unreceivedText(texts []string) string {
	var b strings.Builder
	b.WriteString("Messages were sent to you while you worked, and your worker ended before it read them. Take them in now:")
	for i, text := range texts {
		fmt.Fprintf(&b, "\n\nMessage %d of %d:\n%s", i+1, len(texts), text)
	}
	return b.String()
}

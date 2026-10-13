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

// message is an instruction for a running worker, on its way to the pump.
type message struct {
	procedure     string
	req           *planv1.TaskServiceSendRequest
	journal       proto.Message
	attachments   []plan.AttachmentData
	eventImages   []*planv1.Attachment
	skipWhenEmpty bool
	reply         chan sent
}

// sent is what delivering a message gave: its event, or why it was not delivered.
type sent struct {
	event *planv1.TaskEvent
	err   error
}

// Send gives a running worker a message, an instruction added while it works. The pump writes it on the worker's
// input and records it as a MESSAGE event; the first thing the worker says after it is preceded by a RECEIVED
// event. A worker that ends first never acknowledges it.
func (h *Harness) Send(ctx context.Context, procedure string, req *planv1.TaskServiceSendRequest) (*planv1.TaskEvent, error) {
	// A text-only task indication carries no image snapshot. Durable instruction images are delivered at worker
	// start and by the post-Assign hook; resolving every historical assignment here would make an ordinary text
	// indication fail after the bounded native-image batch is full.
	return h.sendWithAttachments(ctx, procedure, req, nil, req)
}

// sendWithAttachments is the common pump bridge for the TaskService inbox and the durable instruction assignment
// hook. The latter supplies bytes read in the Assign transaction path; ordinary TaskService.Send supplies text only.
func (h *Harness) sendWithAttachments(ctx context.Context, procedure string, req *planv1.TaskServiceSendRequest, attachments []plan.AttachmentData, journal proto.Message) (*planv1.TaskEvent, error) {
	return h.sendWithAttachmentsEvent(ctx, procedure, req, attachments, nil, journal, false)
}

func (h *Harness) sendWithAttachmentsMode(ctx context.Context, procedure string, req *planv1.TaskServiceSendRequest, attachments []plan.AttachmentData, journal proto.Message, skipWhenEmpty bool) (*planv1.TaskEvent, error) {
	return h.sendWithAttachmentsEvent(ctx, procedure, req, attachments, nil, journal, skipWhenEmpty)
}

func (h *Harness) sendWithAttachmentsEvent(ctx context.Context, procedure string, req *planv1.TaskServiceSendRequest, attachments []plan.AttachmentData, eventImages []*planv1.Attachment, journal proto.Message, skipWhenEmpty bool) (*planv1.TaskEvent, error) {
	task, err := store.Get[*planv1.Task](ctx, h.store, req.GetTaskId())
	if err != nil {
		return nil, plan.Status(err)
	}
	notRunning := connect.NewError(connect.CodeFailedPrecondition,
		fmt.Errorf("task %s is not running: %s", task.GetCode(), short(task.GetStatus())))
	h.mu.Lock()
	r := h.runs[task.GetId()]
	ready := r != nil && r.worker != nil && !r.final && !r.stopping
	h.mu.Unlock()
	if !ready {
		return nil, notRunning
	}
	m := &message{procedure: procedure, req: req, journal: journal, attachments: attachments, eventImages: eventImages, skipWhenEmpty: skipWhenEmpty, reply: make(chan sent, 1)}
	select {
	case r.sends <- m:
	case <-r.done:
		return nil, notRunning
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	// The pump answers every message it takes, at once.
	res := <-m.reply
	return res.event, res.err
}

// deliver writes a message on the worker's input, then records it; the pump calls it.
func (h *Harness) deliver(r *run, m *message) sent {
	attachments := r.pendingImages(m.attachments)
	if m.skipWhenEmpty && len(m.attachments) > 0 && len(attachments) == 0 {
		// The worker received this durable assignment in its launch message. The Assign hook raced with
		// startup; do not send the instruction text a second time merely because no new image block remains.
		return sent{}
	}
	if err := sendWorkerInput(r.worker, m.req.GetText(), attachments); err != nil {
		code := connect.CodeInternal
		if errors.Is(err, ErrClosed) {
			code = connect.CodeFailedPrecondition
		}
		return sent{err: connect.NewError(code, fmt.Errorf("task %s: %w", r.task.GetCode(), err))}
	}
	r.markImages(attachments)
	actor, journal := actorLocal, m.journal
	if journal == nil {
		actor = actorHarness
	}
	ev, err := h.writeAs(r, actor, m.procedure, journal, nil,
		Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_MESSAGE, Text: m.req.GetText(), Attachments: m.eventImages})
	if err != nil {
		return sent{err: plan.Status(fmt.Errorf("the worker has the message, but it is not recorded: %w", err))}
	}
	r.unread = append(r.unread, m.req.GetText())
	return sent{event: ev}
}

// pendingImages removes durable instruction image blocks already sent to this run. Assignment snapshots can be
// present both in the launch spec and in one or more later task messages; only the stored attachment ID is an
// idempotence key. Transient TaskService.SendData inputs have no durable ID and every indication remains distinct.
func (r *run) pendingImages(all []plan.AttachmentData) []plan.AttachmentData {
	if len(all) == 0 {
		return nil
	}
	out := make([]plan.AttachmentData, 0, len(all))
	for _, attachment := range all {
		// Only durable instruction references are idempotent. Transient TaskService.SendData inputs have
		// metadata without an ID; identical bytes in two separate indications are two intentional image
		// blocks and must not be collapsed by a SHA/size fallback.
		if id := attachmentID(attachment); id != "" {
			if _, sent := r.deliveredImages[id]; sent {
				continue
			}
		}
		out = append(out, attachment)
	}
	return out
}

func (r *run) markImages(images []plan.AttachmentData) {
	if len(images) == 0 {
		return
	}
	if r.deliveredImages == nil {
		r.deliveredImages = map[string]struct{}{}
	}
	for _, attachment := range images {
		if id := attachmentID(attachment); id != "" {
			r.deliveredImages[id] = struct{}{}
		}
	}
}

// beginWorkerImages starts a fresh delivery ledger for a provider worker. A run may reuse its pump when a
// permission answer restarts the provider, but the new process has not seen any image that the old process saw. Keep
// only the launch snapshot for the current worker so a deferred assignment is not suppressed by the prior worker's
// ledger.
func (r *run) beginWorkerImages(images []plan.AttachmentData) {
	r.deliveredImages = map[string]struct{}{}
	r.markImages(images)
}

func attachmentID(attachment plan.AttachmentData) string {
	if attachment.Metadata == nil || attachment.Metadata.GetId() == "" {
		return ""
	}
	return "id:" + attachment.Metadata.GetId()
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

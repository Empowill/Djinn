package plan

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/locales"
)

// Instructions implements the durable developer requests of InstructionService. Tell remains a transient message.
type Instructions struct {
	planv1connect.UnimplementedInstructionServiceHandler
	Store   *store.Store
	Leads   Leads
	Workers WishWorkers
	// Home is Djinn's data folder, where validated image bytes are stored outside the SQLite journal.
	Home string
}

// ImageLeads is the optional native lead transport for an indication with image blocks. A PTY implementation that
// only has Leads.Tell must not turn refs into filename text: Send returns an explicit unsupported delivery state.
// Implementations preserve the same choice and typing queues as Leads.Tell.
type ImageLeads interface {
	TellWithAttachments(name, text string, attachments []*planv1.Attachment) (planv1.TellWait, error)
}

// InstructionImageDeliverer is the optional post-Assign bridge to an already running provider worker. It closes the
// race where a task starts before its indication is assigned: the worker receives image blocks through its native
// input API instead of waiting for a second process start. Implementations return a delivery state and never turn an
// attachment into prompt text.
type InstructionImageDeliverer interface {
	DeliverInstructionImages(context.Context, *planv1.Instruction, []AttachmentData) (planv1.InstructionImageDelivery, string)
}

var instructionCode = regexp.MustCompile(`^I[0-9]{2,}$`)

func instructionError(code connect.Code, key string, args ...string) error {
	params := map[string]string{}
	for n := 0; n+1 < len(args); n += 2 {
		params[args[n]] = args[n+1]
	}
	return connect.NewError(code, fmt.Errorf("%s", locales.T(locales.Source, key, params)))
}

func (i *Instructions) Send(ctx context.Context, req *connect.Request[planv1.InstructionServiceSendRequest]) (*connect.Response[planv1.InstructionServiceSendResponse], error) {
	prepared, err := prepareAttachments(i.Home, nil, req.Msg.GetAttachmentPaths())
	if err != nil {
		return nil, err
	}
	return i.send(ctx, req.Spec(), req.Msg.GetWishId(), req.Msg.GetText(), req.Msg.GetRequestId(), prepared,
		proto.Clone(req.Msg).(*planv1.InstructionServiceSendRequest))
}

// SendData is the typed window path for image bytes. It has the same durable behavior as Send but is internal so
// generic command-line and MCP generation never attempts to represent binary nested messages as scalar arguments.
func (i *Instructions) SendData(ctx context.Context, req *connect.Request[planv1.InstructionServiceSendDataRequest]) (*connect.Response[planv1.InstructionServiceSendDataResponse], error) {
	if len(req.Msg.GetAttachmentMetadata()) > 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("attachment_metadata is server-generated and cannot be supplied"))
	}
	prepared, err := prepareAttachments(i.Home, req.Msg.GetAttachments(), nil)
	if err != nil {
		return nil, err
	}
	sent, err := i.send(ctx, req.Spec(), req.Msg.GetWishId(), req.Msg.GetText(), req.Msg.GetRequestId(), prepared,
		proto.Clone(req.Msg).(*planv1.InstructionServiceSendDataRequest))
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.InstructionServiceSendDataResponse{
		Instruction:       sent.Msg.GetInstruction(),
		ImageDelivery:     sent.Msg.GetImageDelivery(),
		ImageDeliveryNote: sent.Msg.GetImageDeliveryNote(),
	}), nil
}

// GetAttachment returns one validated image owned by an instruction of the supplied wish. The request accepts only
// stored identifiers; callers cannot supply a filesystem path or use the UI RPC as an arbitrary file reader.
func (i *Instructions) GetAttachment(ctx context.Context, req *connect.Request[planv1.InstructionServiceGetAttachmentRequest]) (*connect.Response[planv1.InstructionServiceGetAttachmentResponse], error) {
	if req == nil || req.Msg == nil {
		return nil, instructionError(connect.CodeInvalidArgument, "instructionBackend.blank")
	}
	if err := protovalidate.Validate(req.Msg); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	instruction, err := store.Get[*planv1.Instruction](ctx, i.Store, req.Msg.GetInstructionId())
	if err != nil {
		return nil, Status(err)
	}
	if !strings.EqualFold(instruction.GetWishId(), req.Msg.GetWishId()) {
		return nil, instructionError(connect.CodeInvalidArgument, "instructionBackend.wrong_wish")
	}
	var ref *planv1.Attachment
	for _, candidate := range instruction.GetAttachments() {
		if candidate != nil && strings.EqualFold(candidate.GetId(), req.Msg.GetAttachmentId()) {
			ref = candidate
			break
		}
	}
	if ref == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("attachment %q is not part of instruction %q", req.Msg.GetAttachmentId(), instruction.GetId()))
	}
	data, err := ReadAttachment(i.Home, ref)
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewResponse(&planv1.InstructionServiceGetAttachmentResponse{
		Attachment: protoCloneAttachment(ref),
		Data:       append([]byte(nil), data...),
	}), nil
}

type instructionSendRequest interface {
	proto.Message
	GetWishId() string
	GetText() string
	GetRequestId() string
}

func (i *Instructions) send(ctx context.Context, spec connect.Spec, wishID, text, requestID string, prepared []preparedAttachment, original instructionSendRequest) (*connect.Response[planv1.InstructionServiceSendResponse], error) {
	if strings.TrimSpace(text) == "" && len(prepared) == 0 {
		return nil, instructionError(connect.CodeInvalidArgument, "instructionBackend.blank")
	}
	var instruction *planv1.Instruction
	created := false
	imageDelivery := planv1.InstructionImageDelivery_INSTRUCTION_IMAGE_DELIVERY_UNSPECIFIED
	imageDeliveryNote := ""
	var placed []string
	committed := false
	defer func() {
		if !committed {
			removeAttachmentFiles(i.Home, placedIDs(placed))
		}
	}()
	err := Status(i.Store.Tx(ctx, func(tx *store.Tx) error {
		wish, err := store.Get[*planv1.Wish](ctx, tx, wishID)
		if err != nil {
			return err
		}
		if requestID != "" {
			id := requestID
			existing, err := store.List[*planv1.Instruction](ctx, tx, store.Where{"wish_id": wish.GetId(), "request_id": id})
			if err != nil {
				return err
			}
			if len(existing) > 0 {
				if existing[0].GetText() != text || !attachmentInputsEqual(existing[0], prepared) {
					return instructionError(connect.CodeAlreadyExists, "instructionBackend.retry_conflict")
				}
				instruction = existing[0]
				if err := tx.Journal(actor, spec.Procedure, journalInstructionRequest(original, nil, existing[0].GetAttachments())); err != nil {
					return err
				}
				return nil
			}
		}
		all, err := store.List[*planv1.Instruction](ctx, tx, store.Where{"wish_id": wish.GetId()})
		if err != nil {
			return err
		}
		next := 1
		for _, old := range all {
			n, err := strconv.Atoi(strings.TrimPrefix(strings.ToUpper(old.GetCode()), "I"))
			if err == nil {
				next = max(next, n+1)
			}
		}
		now := timestamppb.Now()
		attachments, files, err := placeAttachments(i.Home, prepared)
		if err != nil {
			return err
		}
		placed = append(placed, files...)
		if err := tx.Journal(actor, spec.Procedure, journalInstructionRequest(original, prepared, attachments)); err != nil {
			return err
		}
		instruction = &planv1.Instruction{Id: tx.JournalID(), WishId: wish.GetId(), Code: fmt.Sprintf("I%02d", next), Text: text,
			CreateTime: now, UpdateTime: now, Status: planv1.InstructionStatus_INSTRUCTION_STATUS_PENDING, RequestId: requestID, Attachments: attachments}
		if err := tx.Put(instruction); err != nil {
			return err
		}
		created = true
		return nil
	}))
	if err != nil {
		return nil, err
	}
	committed = true
	// Acceptance is durable already. A missing, stopped or busy lead cannot turn it into a failed submission. Image
	// delivery is reported in the response because it may be unsupported or deferred after the journal commits.
	// Lead terminals also protect against typing into a shell or an approval prompt.
	if created && i.Leads != nil {
		attachmentNote := ""
		if count := len(instruction.GetAttachments()); count > 0 {
			attachmentNote = fmt.Sprintf(" It includes %d validated image attachment(s).", count)
		}
		text := fmt.Sprintf(
			"Developer instruction %s (%s) persisted.%s Read its full text: djinn instruction list %s. "+
				"Run djinn instruction reflect %s; then djinn instruction assign %s <worker-task> after delegating to an existing or new worker of this wish. "+
				"Never implement source or test changes yourself. Verify the worker result, then explicitly djinn instruction complete %s. Continue.",
			instruction.GetCode(), instruction.GetId(), attachmentNote, instruction.GetWishId(), instruction.GetId(), instruction.GetId(), instruction.GetId())
		name := LeadTerminal(instruction.GetWishId())
		if len(instruction.GetAttachments()) > 0 {
			if rich, ok := i.Leads.(ImageLeads); ok {
				if _, err := rich.TellWithAttachments(name, text, instruction.GetAttachments()); err != nil {
					if errors.Is(err, ErrNoLead) {
						imageDelivery = planv1.InstructionImageDelivery_INSTRUCTION_IMAGE_DELIVERY_DEFERRED
						imageDeliveryNote = "the instruction is durable, but no live lead accepted the image"
					} else {
						imageDelivery = planv1.InstructionImageDelivery_INSTRUCTION_IMAGE_DELIVERY_FAILED
						imageDeliveryNote = fmt.Sprintf("image indication persisted but lead delivery failed: %v", err)
					}
				} else {
					imageDelivery = planv1.InstructionImageDelivery_INSTRUCTION_IMAGE_DELIVERY_QUEUED
				}
			} else {
				imageDelivery = planv1.InstructionImageDelivery_INSTRUCTION_IMAGE_DELIVERY_UNSUPPORTED
				imageDeliveryNote = errors.New("the live lead transport cannot carry image indications; no image was delivered").Error()
				// Keep the existing durable instruction wakeup for text-only lead transports. The text carries
				// lifecycle guidance, but never substitutes a filename or image bytes for the unsupported image
				// block. A lead may be offline or may be showing a shell/choice; either way persistence already
				// succeeded and the response reports the image limitation explicitly.
				notice := text + " This lead transport cannot carry image blocks; the image remains attached to the instruction."
				_, _ = i.Leads.Tell(name, notice)
			}
		} else {
			_, _ = i.Leads.Tell(name, text)
		}
	} else if created && len(instruction.GetAttachments()) > 0 {
		imageDelivery = planv1.InstructionImageDelivery_INSTRUCTION_IMAGE_DELIVERY_DEFERRED
		imageDeliveryNote = "the instruction is durable, but no live lead accepted the image"
	}
	return connect.NewResponse(&planv1.InstructionServiceSendResponse{
		Instruction:       instruction,
		ImageDelivery:     imageDelivery,
		ImageDeliveryNote: imageDeliveryNote,
	}), nil
}

func journalInstructionRequest(req proto.Message, prepared []preparedAttachment, stored []*planv1.Attachment) proto.Message {
	clone := proto.Clone(req)
	switch r := clone.(type) {
	case *planv1.InstructionServiceSendRequest:
		// Paths are scalar request data, never image bytes. Keep them so an image-only request remains a valid
		// local journal message; portable() removes the machine-local paths before an export leaves this host.
	case *planv1.InstructionServiceSendDataRequest:
		// The binary inputs are staged in the private attachment store before the instruction is committed. Journal
		// only their validated identity so a retry and an export retain image identity without embedding payloads.
		r.Attachments = nil
		if len(stored) > 0 {
			r.AttachmentMetadata = make([]*planv1.Attachment, 0, len(stored))
			for _, ref := range stored {
				r.AttachmentMetadata = append(r.AttachmentMetadata, protoCloneAttachment(ref))
			}
		} else {
			r.AttachmentMetadata = make([]*planv1.Attachment, 0, len(prepared))
			for _, p := range prepared {
				r.AttachmentMetadata = append(r.AttachmentMetadata, protoCloneAttachment(p.meta))
			}
		}
	}
	return clone
}

// placedIDs returns the identifiers encoded in paths returned by placeAttachments. Paths are only ever generated as
// <home>/attachments/<uuid>; malformed paths are ignored so cleanup cannot become a path traversal primitive.
func placedIDs(paths []string) []string {
	ids := make([]string, 0, len(paths))
	for _, path := range paths {
		ids = append(ids, filepath.Base(path))
	}
	return ids
}

func (i *Instructions) List(ctx context.Context, req *connect.Request[planv1.InstructionServiceListRequest]) (*connect.Response[planv1.InstructionServiceListResponse], error) {
	if _, err := store.Get[*planv1.Wish](ctx, i.Store, req.Msg.GetWishId()); err != nil {
		return nil, Status(err)
	}
	all, err := store.List[*planv1.Instruction](ctx, i.Store, store.Where{"wish_id": req.Msg.GetWishId()})
	if err != nil {
		return nil, Status(err)
	}
	sortInstructions(all)
	return connect.NewResponse(&planv1.InstructionServiceListResponse{Instructions: all}), nil
}

// sortInstructions follows creation time, not UUID ordering: imported instructions may have UUIDs that do not
// encode their creation time. Equal timestamps use numeric codes (I02 before I10), then IDs for a stable order.
func sortInstructions(instructions []*planv1.Instruction) {
	slices.SortFunc(instructions, func(a, b *planv1.Instruction) int {
		code := func(instruction *planv1.Instruction) string {
			return strings.TrimLeft(strings.TrimPrefix(strings.ToUpper(instruction.GetCode()), "I"), "0")
		}
		ac, bc := code(a), code(b)
		return cmp.Or(a.GetCreateTime().AsTime().Compare(b.GetCreateTime().AsTime()), cmp.Compare(len(ac), len(bc)), strings.Compare(ac, bc), strings.Compare(a.GetId(), b.GetId()))
	})
}

func findInstruction(ctx context.Context, r store.Reader, ref, wishID string) (*planv1.Instruction, error) {
	if _, err := uuid.Parse(ref); err == nil {
		instruction, err := store.Get[*planv1.Instruction](ctx, r, ref)
		if err != nil {
			return nil, err
		}
		if wishID != "" && !strings.EqualFold(wishID, instruction.GetWishId()) {
			return nil, instructionError(connect.CodeInvalidArgument, "instructionBackend.wrong_wish")
		}
		return instruction, nil
	}
	where := store.Where{"code": ref}
	if wishID != "" {
		where["wish_id"] = wishID
	}
	all, err := store.List[*planv1.Instruction](ctx, r, where)
	if err != nil {
		return nil, err
	}
	switch len(all) {
	case 0:
		return nil, instructionError(connect.CodeNotFound, "instructionBackend.not_found", "instruction", ref)
	case 1:
		return all[0], nil
	default:
		return nil, instructionError(connect.CodeFailedPrecondition, "instructionBackend.ambiguous", "instruction", ref)
	}
}

// instructionTask resolves a worker reference only within the instruction's wish, including UUID references.
func instructionTask(ctx context.Context, r store.Reader, ref, wishID string) (*planv1.Task, error) {
	if _, err := uuid.Parse(ref); err == nil {
		task, err := store.Get[*planv1.Task](ctx, r, ref)
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(task.GetWishId(), wishID) {
			return nil, instructionError(connect.CodeInvalidArgument, "instructionBackend.wrong_wish")
		}
		return task, nil
	}
	tasks, err := store.List[*planv1.Task](ctx, r, store.Where{"wish_id": wishID})
	if err != nil {
		return nil, err
	}
	for _, task := range tasks {
		if strings.EqualFold(task.GetCode(), ref) {
			return task, nil
		}
	}
	return nil, instructionError(connect.CodeNotFound, "instructionBackend.invalid_task", "task", ref)
}

type instructionRequest interface {
	proto.Message
	GetInstruction() string
	GetWishId() string
}

// changeInstruction resolves, validates and journals in the same transaction. The journal records canonical IDs
// so that a command addressed by a wish-local code stays unambiguous when exported to another machine.
func changeInstruction[R instructionRequest](ctx context.Context, i *Instructions, spec connect.Spec, req R, fn func(*store.Tx, *planv1.Instruction) error) (*planv1.Instruction, error) {
	var instruction *planv1.Instruction
	err := i.Store.Tx(ctx, func(tx *store.Tx) error {
		var err error
		instruction, err = findInstruction(ctx, tx, req.GetInstruction(), req.GetWishId())
		if err != nil {
			return err
		}
		journal := proto.Clone(req).ProtoReflect()
		journal.Set(journal.Descriptor().Fields().ByName("instruction"), protoreflect.ValueOfString(instruction.GetId()))
		journal.Set(journal.Descriptor().Fields().ByName("wish_id"), protoreflect.ValueOfString(instruction.GetWishId()))
		if assign, ok := any(req).(*planv1.InstructionServiceAssignRequest); ok {
			task, err := instructionTask(ctx, tx, assign.GetTask(), instruction.GetWishId())
			if err != nil {
				return err
			}
			journal.Set(journal.Descriptor().Fields().ByName("task"), protoreflect.ValueOfString(task.GetId()))
		}
		if err := tx.Journal(actor, spec.Procedure, journal.Interface()); err != nil {
			return err
		}
		priorUpdate := instruction.GetUpdateTime()
		before := proto.Clone(instruction)
		if err := fn(tx, instruction); err != nil {
			return err
		}
		if proto.Equal(before, instruction) {
			return nil
		}
		updated := timestamppb.Now()
		// An imported instruction may carry a future reply or update timestamp. Keep the lifecycle timestamp
		// monotonic when a new local reply is appended after that import.
		for _, old := range []*timestamppb.Timestamp{priorUpdate, instruction.GetUpdateTime()} {
			if old != nil && old.CheckValid() == nil && old.AsTime().After(updated.AsTime()) {
				updated = old
			}
		}
		instruction.UpdateTime = updated
		return tx.Put(instruction)
	})
	return instruction, Status(err)
}

func instructionTransition(instruction *planv1.Instruction, next planv1.InstructionStatus) error {
	return instructionError(connect.CodeFailedPrecondition, "instructionBackend.transition", "instruction", instruction.GetCode(),
		"status", instructionStatusWord(instruction.GetStatus()), "next", instructionStatusWord(next))
}

func instructionStatusWord(status planv1.InstructionStatus) string {
	return strings.ToLower(strings.TrimPrefix(status.String(), "INSTRUCTION_STATUS_"))
}

func (i *Instructions) Reflect(ctx context.Context, req *connect.Request[planv1.InstructionServiceReflectRequest]) (*connect.Response[planv1.InstructionServiceReflectResponse], error) {
	instruction, err := changeInstruction(ctx, i, req.Spec(), req.Msg, func(_ *store.Tx, instruction *planv1.Instruction) error {
		switch instruction.GetStatus() {
		case planv1.InstructionStatus_INSTRUCTION_STATUS_REFLECTING:
			return nil
		case planv1.InstructionStatus_INSTRUCTION_STATUS_PENDING:
			instruction.Status = planv1.InstructionStatus_INSTRUCTION_STATUS_REFLECTING
			return nil
		default:
			return instructionTransition(instruction, planv1.InstructionStatus_INSTRUCTION_STATUS_REFLECTING)
		}
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.InstructionServiceReflectResponse{Instruction: instruction}), nil
}

func (i *Instructions) Assign(ctx context.Context, req *connect.Request[planv1.InstructionServiceAssignRequest]) (*connect.Response[planv1.InstructionServiceAssignResponse], error) {
	previousTaskID := ""
	instruction, err := changeInstruction(ctx, i, req.Spec(), req.Msg, func(tx *store.Tx, instruction *planv1.Instruction) error {
		previousTaskID = instruction.GetTaskId()
		switch instruction.GetStatus() {
		case planv1.InstructionStatus_INSTRUCTION_STATUS_REFLECTING, planv1.InstructionStatus_INSTRUCTION_STATUS_PROCESSING:
		default:
			return instructionTransition(instruction, planv1.InstructionStatus_INSTRUCTION_STATUS_PROCESSING)
		}
		task, err := instructionTask(ctx, tx, req.Msg.GetTask(), instruction.GetWishId())
		if err != nil {
			return err
		}
		instruction.TaskId, instruction.Status = task.GetId(), planv1.InstructionStatus_INSTRUCTION_STATUS_PROCESSING
		return nil
	})
	if err != nil {
		return nil, err
	}
	delivery := planv1.InstructionImageDelivery_INSTRUCTION_IMAGE_DELIVERY_UNSPECIFIED
	deliveryNote := ""
	if len(instruction.GetAttachments()) > 0 && previousTaskID != instruction.GetTaskId() {
		delivery = planv1.InstructionImageDelivery_INSTRUCTION_IMAGE_DELIVERY_DEFERRED
		deliveryNote = "image delivery is deferred until the assigned worker accepts a native image input"
		if deliverer, ok := i.Workers.(InstructionImageDeliverer); ok {
			attachments, readErr := ReadInstructionAttachments(i.Home, instruction)
			if readErr != nil {
				delivery = planv1.InstructionImageDelivery_INSTRUCTION_IMAGE_DELIVERY_FAILED
				deliveryNote = fmt.Sprintf("assigned image could not be read: %v", readErr)
			} else {
				delivery, deliveryNote = deliverer.DeliverInstructionImages(ctx, instruction, attachments)
			}
		}
	}
	return connect.NewResponse(&planv1.InstructionServiceAssignResponse{
		Instruction:       instruction,
		ImageDelivery:     delivery,
		ImageDeliveryNote: deliveryNote,
	}), nil
}

func (i *Instructions) Complete(ctx context.Context, req *connect.Request[planv1.InstructionServiceCompleteRequest]) (*connect.Response[planv1.InstructionServiceCompleteResponse], error) {
	instruction, err := changeInstruction(ctx, i, req.Spec(), req.Msg, func(tx *store.Tx, instruction *planv1.Instruction) error {
		if instruction.GetStatus() == planv1.InstructionStatus_INSTRUCTION_STATUS_DONE {
			return nil
		}
		if instruction.GetStatus() != planv1.InstructionStatus_INSTRUCTION_STATUS_PROCESSING {
			return instructionTransition(instruction, planv1.InstructionStatus_INSTRUCTION_STATUS_DONE)
		}
		if instruction.GetTaskId() == "" {
			return instructionError(connect.CodeFailedPrecondition, "instructionBackend.task_required")
		}
		task, err := instructionTask(ctx, tx, instruction.GetTaskId(), instruction.GetWishId())
		if err != nil {
			return err
		}
		if task.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
			return instructionError(connect.CodeFailedPrecondition, "instructionBackend.task_not_done", "task", task.GetCode())
		}
		instruction.Status = planv1.InstructionStatus_INSTRUCTION_STATUS_DONE
		return nil
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.InstructionServiceCompleteResponse{Instruction: instruction}), nil
}

// Reply appends one informational reply while leaving the instruction's lifecycle and worker assignment alone.
// Reply timestamps are nondecreasing, even when an imported instruction came from the future or the system clock
// has not advanced between two submissions. The maximum valid protobuf timestamp is kept equal because it cannot
// be incremented without becoming invalid.
func (i *Instructions) Reply(ctx context.Context, req *connect.Request[planv1.InstructionServiceReplyRequest]) (*connect.Response[planv1.InstructionServiceReplyResponse], error) {
	if req == nil || req.Msg == nil {
		return nil, instructionError(connect.CodeInvalidArgument, "instructionBackend.blank")
	}
	if err := protovalidate.Validate(req.Msg); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if strings.TrimSpace(req.Msg.GetReply()) == "" {
		return nil, instructionError(connect.CodeInvalidArgument, "instructionBackend.blank")
	}
	instruction, err := changeInstruction(ctx, i, req.Spec(), req.Msg, func(_ *store.Tx, instruction *planv1.Instruction) error {
		at := timestamppb.Now()
		if replies := instruction.GetReplies(); len(replies) > 0 {
			last := replies[len(replies)-1].GetCreateTime()
			if last != nil && last.CheckValid() == nil && !last.AsTime().Before(at.AsTime()) {
				candidate := timestamppb.New(last.AsTime().Add(time.Nanosecond))
				if candidate.CheckValid() == nil {
					at = candidate
				} else {
					// Timestamp has reached its representable upper bound. Equal timestamps remain chronological;
					// the import validator still rejects malformed timestamps.
					at = last
				}
			}
		}
		instruction.Replies = append(instruction.GetReplies(), &planv1.InstructionReply{Text: req.Msg.GetReply(), CreateTime: at})
		// changeInstruction keeps this timestamp when it is ahead of the local clock.
		instruction.UpdateTime = at
		return nil
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.InstructionServiceReplyResponse{Instruction: instruction}), nil
}

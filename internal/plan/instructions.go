package plan

import (
	"cmp"
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

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
	Store *store.Store
	Leads Leads
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
	if strings.TrimSpace(req.Msg.GetText()) == "" {
		return nil, instructionError(connect.CodeInvalidArgument, "instructionBackend.blank")
	}
	var instruction *planv1.Instruction
	created := false
	err := write(ctx, i.Store, req.Spec(), req.Msg, func(tx *store.Tx) error {
		wish, err := store.Get[*planv1.Wish](ctx, tx, req.Msg.GetWishId())
		if err != nil {
			return err
		}
		if id := req.Msg.GetRequestId(); id != "" {
			existing, err := store.List[*planv1.Instruction](ctx, tx, store.Where{"wish_id": wish.GetId(), "request_id": id})
			if err != nil {
				return err
			}
			if len(existing) > 0 {
				if existing[0].GetText() != req.Msg.GetText() {
					return instructionError(connect.CodeAlreadyExists, "instructionBackend.retry_conflict")
				}
				instruction = existing[0]
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
		instruction = &planv1.Instruction{Id: tx.JournalID(), WishId: wish.GetId(), Code: fmt.Sprintf("I%02d", next), Text: req.Msg.GetText(),
			CreateTime: now, UpdateTime: now, Status: planv1.InstructionStatus_INSTRUCTION_STATUS_PENDING, RequestId: req.Msg.GetRequestId()}
		if err := tx.Put(instruction); err != nil {
			return err
		}
		created = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Acceptance is durable already. A missing, stopped or busy lead cannot turn it into a failed submission.
	// Lead terminals also protect against typing into a shell or an approval prompt.
	if created && i.Leads != nil {
		_, _ = i.Leads.Tell(LeadTerminal(instruction.GetWishId()), fmt.Sprintf(
			"Developer instruction %s (%s) persisted. Read its full text: djinn instruction list %s. "+
				"Run djinn instruction reflect %s; then djinn instruction assign %s <worker-task> after delegating to an existing or new worker of this wish. "+
				"Never implement source or test changes yourself. Verify the worker result, then explicitly djinn instruction complete %s. Continue.",
			instruction.GetCode(), instruction.GetId(), instruction.GetWishId(), instruction.GetId(), instruction.GetId(), instruction.GetId()))
	}
	return connect.NewResponse(&planv1.InstructionServiceSendResponse{Instruction: instruction}), nil
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
		before := proto.Clone(instruction)
		if err := fn(tx, instruction); err != nil {
			return err
		}
		if proto.Equal(before, instruction) {
			return nil
		}
		instruction.UpdateTime = timestamppb.Now()
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
	instruction, err := changeInstruction(ctx, i, req.Spec(), req.Msg, func(tx *store.Tx, instruction *planv1.Instruction) error {
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
	return connect.NewResponse(&planv1.InstructionServiceAssignResponse{Instruction: instruction}), nil
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

package harness

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log"
	"slices"

	"connectrpc.com/connect"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"google.golang.org/protobuf/proto"
)

// DeliverInstructionImages is the post-Assign bridge used by plan.Instructions. It sends an assigned indication
// through the same worker pump as task send, preserving ordering and typing guards. The event is journaled as a
// harness event because the Assign RPC already owns the durable command journal.
func (h *Harness) DeliverInstructionImages(ctx context.Context, instruction *planv1.Instruction, attachments []plan.AttachmentData) (planv1.InstructionImageDelivery, string) {
	if instruction == nil || instruction.GetTaskId() == "" {
		return planv1.InstructionImageDelivery_INSTRUCTION_IMAGE_DELIVERY_FAILED, "the image instruction has no assigned worker task"
	}
	if len(attachments) == 0 {
		return planv1.InstructionImageDelivery_INSTRUCTION_IMAGE_DELIVERY_QUEUED, ""
	}
	_, err := h.sendWithAttachmentsMode(ctx, methodEvent,
		&planv1.TaskServiceSendRequest{TaskId: instruction.GetTaskId(), Text: instruction.GetText()}, attachments, nil, true)
	if err == nil {
		return planv1.InstructionImageDelivery_INSTRUCTION_IMAGE_DELIVERY_QUEUED, ""
	}
	if errors.Is(err, ErrImageInputUnsupported) {
		return planv1.InstructionImageDelivery_INSTRUCTION_IMAGE_DELIVERY_UNSUPPORTED,
			"the assigned worker cannot carry image input blocks; no text-only fallback was sent"
	}
	if connect.CodeOf(err) == connect.CodeFailedPrecondition {
		return planv1.InstructionImageDelivery_INSTRUCTION_IMAGE_DELIVERY_DEFERRED,
			"the assigned worker is not accepting input; its next start will read the durable image"
	}
	return planv1.InstructionImageDelivery_INSTRUCTION_IMAGE_DELIVERY_FAILED, fmt.Sprintf("assigned image delivery failed: %v", err)
}

// deliverAssignedImagesAfterStart closes the small startup window in which Assign can commit an image after the
// launch snapshot was read but before the provider worker became ready. Assign reports that case as deferred; once
// the worker and its pump exist, reread each durable instruction and use the same typed bridge. The per-instruction
// reads keep this recovery path bounded even when a task has accumulated many historical assignments.
func (h *Harness) deliverAssignedImagesAfterStart(ctx context.Context, taskID string) {
	instructions, err := store.List[*planv1.Instruction](ctx, h.store, store.Where{"task_id": taskID})
	if err != nil {
		log.Printf("djinn: task %s: find assigned image instructions after start: %v", taskID, err)
		return
	}
	slices.SortStableFunc(instructions, func(a, b *planv1.Instruction) int {
		return cmp.Or(a.GetCreateTime().AsTime().Compare(b.GetCreateTime().AsTime()), cmp.Compare(a.GetCode(), b.GetCode()), cmp.Compare(a.GetId(), b.GetId()))
	})
	for _, instruction := range instructions {
		if len(instruction.GetAttachments()) == 0 {
			continue
		}
		attachments, err := plan.ReadInstructionAttachments(h.home, instruction)
		if err != nil {
			log.Printf("djinn: task %s: read assigned image instruction %s after start: %v", taskID, instruction.GetCode(), err)
			continue
		}
		delivery, note := h.DeliverInstructionImages(ctx, instruction, attachments)
		if delivery != planv1.InstructionImageDelivery_INSTRUCTION_IMAGE_DELIVERY_QUEUED {
			log.Printf("djinn: task %s: assigned image instruction %s after start: %s (%s)", taskID, instruction.GetCode(), delivery, note)
		}
	}
}

// taskAttachments resolves the durable images assigned to a worker task. The instruction metadata contains only
// validated identifiers; plan.ReadInstructionAttachments verifies each stored blob and returns fresh bytes before a
// provider is started. This keeps filenames and store paths out of every provider prompt.
func (h *Harness) taskAttachments(ctx context.Context, taskID string) ([]plan.AttachmentData, error) {
	if taskID == "" {
		return nil, nil
	}
	instructions, err := store.List[*planv1.Instruction](ctx, h.store, store.Where{"task_id": taskID})
	if err != nil {
		return nil, fmt.Errorf("find image instructions for task %s: %w", taskID, err)
	}
	if err := checkTaskImageBudget(instructions); err != nil {
		return nil, fmt.Errorf("task %s: %w", taskID, err)
	}
	slices.SortStableFunc(instructions, func(a, b *planv1.Instruction) int {
		return cmp.Or(a.GetCreateTime().AsTime().Compare(b.GetCreateTime().AsTime()), cmp.Compare(a.GetCode(), b.GetCode()), cmp.Compare(a.GetId(), b.GetId()))
	})
	// A provider input has a bounded image batch. Historical assignments are delivered one instruction at a time by
	// deliverAssignedImagesAfterStart; putting only the first batch in the launch message keeps a task restartable after
	// it has received more than one batch over its lifetime.
	remaining := maxProviderImages
	var out []plan.AttachmentData
	for _, instruction := range instructions {
		refs := instruction.GetAttachments()
		if len(refs) == 0 || remaining == 0 {
			continue
		}
		if len(refs) > remaining {
			refs = refs[:remaining]
		}
		limited := proto.Clone(instruction).(*planv1.Instruction)
		limited.Attachments = refs
		attachments, err := plan.ReadInstructionAttachments(h.home, limited)
		if err != nil {
			return nil, fmt.Errorf("read image instruction %s for task %s: %w", instruction.GetCode(), taskID, err)
		}
		out = append(out, attachments...)
		remaining -= len(attachments)
	}
	return out, nil
}

// checkTaskImageBudget validates every stored instruction reference before a launch reads any blob. Each instruction
// is one bounded native input batch; a task may have many historical assignments, which taskAttachments feeds to the
// first launch batch and the post-start delivery hook separately.
func checkTaskImageBudget(instructions []*planv1.Instruction) error {
	for _, instruction := range instructions {
		if len(instruction.GetAttachments()) > maxProviderImages {
			return fmt.Errorf("instruction %s has more than %d image attachments", instruction.GetCode(), maxProviderImages)
		}
		for n, attachment := range instruction.GetAttachments() {
			if attachment == nil {
				return fmt.Errorf("image attachment %d in instruction %s is empty", n+1, instruction.GetCode())
			}
			size := attachment.GetSize()
			if size < 1 {
				return fmt.Errorf("image attachment %s has invalid size %d", attachment.GetId(), size)
			}
			if size > int64(maxProviderImageBytes) {
				return fmt.Errorf("image attachment %s exceeds %d bytes", attachment.GetId(), maxProviderImageBytes)
			}
		}
	}
	return nil
}

package harness

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// Handler returns the Connect handler of TaskService on h, and its path prefix.
func Handler(h *Harness) (string, http.Handler) {
	return planv1connect.NewTaskServiceHandler(&Tasks{h: h}, connect.WithInterceptors(plan.Validate))
}

// Tasks implements TaskService.
type Tasks struct {
	planv1connect.UnimplementedTaskServiceHandler
	h *Harness
}

func (s *Tasks) Spawn(
	ctx context.Context, req *connect.Request[planv1.TaskServiceSpawnRequest],
) (*connect.Response[planv1.TaskServiceSpawnResponse], error) {
	task, err := s.h.Spawn(ctx, req.Spec().Procedure, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.TaskServiceSpawnResponse{Task: task}), nil
}

func (s *Tasks) List(
	ctx context.Context, req *connect.Request[planv1.TaskServiceListRequest],
) (*connect.Response[planv1.TaskServiceListResponse], error) {
	where := store.Where{}
	if id := req.Msg.GetWishId(); id != "" {
		where["wish_id"] = id
	}
	if id := req.Msg.GetProjectId(); id != "" {
		where["project_id"] = id
	}
	tasks, err := store.List[*planv1.Task](ctx, s.h.store, where)
	if err != nil {
		return nil, plan.Status(err)
	}
	if tasks, err = s.h.withAzimas(ctx, tasks); err != nil {
		return nil, plan.Status(err)
	}
	if err := plan.FillTilasms(ctx, s.h.store, tasks); err != nil {
		return nil, plan.Status(err)
	}
	return connect.NewResponse(&planv1.TaskServiceListResponse{Tasks: tasks}), nil
}

func (s *Tasks) Get(
	ctx context.Context, req *connect.Request[planv1.TaskServiceGetRequest],
) (*connect.Response[planv1.TaskServiceGetResponse], error) {
	task, err := store.Get[*planv1.Task](ctx, s.h.store, req.Msg.GetTaskId())
	if err != nil {
		return nil, plan.Status(err)
	}
	filled, err := s.h.withAzimas(ctx, []*planv1.Task{task})
	if err != nil {
		return nil, plan.Status(err)
	}
	if err := plan.FillTilasms(ctx, s.h.store, filled); err != nil {
		return nil, plan.Status(err)
	}
	return connect.NewResponse(&planv1.TaskServiceGetResponse{Task: filled[0]}), nil
}

// GetEvent returns one persisted task event and validates every event-owned image before returning its bytes. The
// task ID is checked separately so this identifier-scoped read cannot become an arbitrary attachment-store reader.
func (s *Tasks) GetEvent(
	ctx context.Context, req *connect.Request[planv1.TaskServiceGetEventRequest],
) (*connect.Response[planv1.TaskServiceGetEventResponse], error) {
	if _, err := store.Get[*planv1.Task](ctx, s.h.store, req.Msg.GetTaskId()); err != nil {
		return nil, plan.Status(err)
	}
	event, err := store.Get[*planv1.TaskEvent](ctx, s.h.store, req.Msg.GetEventId())
	if err != nil {
		return nil, plan.Status(err)
	}
	if event.GetTaskId() != req.Msg.GetTaskId() {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("task event %q is not part of task %q", req.Msg.GetEventId(), req.Msg.GetTaskId()))
	}
	response := &planv1.TaskServiceGetEventResponse{Event: proto.CloneOf(event)}
	for _, ref := range event.GetAttachments() {
		data, err := plan.ReadAttachment(s.h.home, ref)
		if err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		response.Attachments = append(response.Attachments, &planv1.AttachmentExport{
			Attachment: proto.Clone(ref).(*planv1.Attachment),
			Data:       append([]byte(nil), data...),
		})
	}
	return connect.NewResponse(response), nil
}

func (s *Tasks) Stop(
	ctx context.Context, req *connect.Request[planv1.TaskServiceStopRequest],
) (*connect.Response[planv1.TaskServiceStopResponse], error) {
	task, err := s.h.Stop(ctx, req.Spec().Procedure, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.TaskServiceStopResponse{Task: task}), nil
}

func (s *Tasks) Pause(
	ctx context.Context, req *connect.Request[planv1.TaskServicePauseRequest],
) (*connect.Response[planv1.TaskServicePauseResponse], error) {
	task, err := s.h.Pause(ctx, req.Spec().Procedure, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.TaskServicePauseResponse{Task: task}), nil
}

func (s *Tasks) Resume(
	ctx context.Context, req *connect.Request[planv1.TaskServiceResumeRequest],
) (*connect.Response[planv1.TaskServiceResumeResponse], error) {
	task, err := s.h.Resume(ctx, req.Spec().Procedure, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.TaskServiceResumeResponse{Task: task}), nil
}

func (s *Tasks) Start(
	ctx context.Context, req *connect.Request[planv1.TaskServiceStartRequest],
) (*connect.Response[planv1.TaskServiceStartResponse], error) {
	task, err := s.h.Start(ctx, req.Spec().Procedure, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.TaskServiceStartResponse{Task: task}), nil
}

func (s *Tasks) Watch(
	ctx context.Context, req *connect.Request[planv1.TaskServiceWatchRequest],
	stream *connect.ServerStream[planv1.TaskServiceWatchResponse],
) error {
	// The validating interceptor only sees unary calls.
	if err := protovalidate.Validate(req.Msg); err != nil {
		return connect.NewError(connect.CodeInvalidArgument, err)
	}
	return s.h.Watch(ctx, req.Msg.GetTaskId(), req.Msg.GetAfterSeq(), func(ev *planv1.TaskEvent) error {
		if !req.Msg.GetRaw() && ev.GetRaw() != "" {
			ev = proto.CloneOf(ev)
			ev.Raw = ""
		}
		return stream.Send(&planv1.TaskServiceWatchResponse{Event: ev})
	})
}

func (s *Tasks) Clean(
	ctx context.Context, req *connect.Request[planv1.TaskServiceCleanRequest],
) (*connect.Response[planv1.TaskServiceCleanResponse], error) {
	task, err := s.h.Clean(ctx, req.Spec().Procedure, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.TaskServiceCleanResponse{Task: task}), nil
}

func (s *Tasks) Depend(
	ctx context.Context, req *connect.Request[planv1.TaskServiceDependRequest],
) (*connect.Response[planv1.TaskServiceDependResponse], error) {
	tasks, err := s.h.Depend(ctx, req.Spec().Procedure, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.TaskServiceDependResponse{Task: tasks[0], Also: tasks[1:]}), nil
}

func (s *Tasks) Group(
	ctx context.Context, req *connect.Request[planv1.TaskServiceGroupRequest],
) (*connect.Response[planv1.TaskServiceGroupResponse], error) {
	task, err := s.h.Group(ctx, req.Spec().Procedure, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.TaskServiceGroupResponse{Task: task}), nil
}

func (s *Tasks) Delete(
	ctx context.Context, req *connect.Request[planv1.TaskServiceDeleteRequest],
) (*connect.Response[planv1.TaskServiceDeleteResponse], error) {
	task, err := s.h.Delete(ctx, req.Spec().Procedure, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.TaskServiceDeleteResponse{Task: task}), nil
}

func (s *Tasks) Done(
	ctx context.Context, req *connect.Request[planv1.TaskServiceDoneRequest],
) (*connect.Response[planv1.TaskServiceDoneResponse], error) {
	task, err := s.h.Done(ctx, req.Spec().Procedure, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.TaskServiceDoneResponse{Task: task}), nil
}

func (s *Tasks) Continue(
	ctx context.Context, req *connect.Request[planv1.TaskServiceContinueRequest],
) (*connect.Response[planv1.TaskServiceContinueResponse], error) {
	task, err := s.h.Continue(ctx, req.Spec().Procedure, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.TaskServiceContinueResponse{Task: task}), nil
}

func (s *Tasks) Send(
	ctx context.Context, req *connect.Request[planv1.TaskServiceSendRequest],
) (*connect.Response[planv1.TaskServiceSendResponse], error) {
	if len(req.Msg.GetAttachmentPaths()) > 0 {
		attachments, stagedIDs, err := plan.StoreAttachmentPaths(s.h.home, req.Msg.GetAttachmentPaths())
		if err != nil {
			return nil, err
		}
		committed := false
		defer func() {
			if !committed {
				plan.RemoveStoredAttachments(s.h.home, stagedIDs)
			}
		}()
		eventImages := make([]*planv1.Attachment, 0, len(attachments))
		for _, attachment := range attachments {
			eventImages = append(eventImages, proto.Clone(attachment.Metadata).(*planv1.Attachment))
		}
		ev, err := s.h.sendWithAttachmentsEvent(ctx, req.Spec().Procedure, req.Msg, attachments, eventImages, req.Msg, false)
		if err != nil {
			return nil, err
		}
		committed = true
		return connect.NewResponse(&planv1.TaskServiceSendResponse{Event: ev}), nil
	}
	ev, err := s.h.Send(ctx, req.Spec().Procedure, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.TaskServiceSendResponse{Event: ev}), nil
}

// SendData stages validated image bytes, delivers them through the worker's typed input path, and records their
// metadata on the resulting TaskEvent. The command journal contains the same metadata but never binary payloads.
func (s *Tasks) SendData(
	ctx context.Context, req *connect.Request[planv1.TaskServiceSendDataRequest],
) (*connect.Response[planv1.TaskServiceSendDataResponse], error) {
	if len(req.Msg.GetAttachmentMetadata()) > 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("attachment_metadata is server-generated and cannot be supplied"))
	}
	attachments, stagedIDs, err := plan.StoreAttachmentInputs(s.h.home, req.Msg.GetAttachments())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	committed := false
	defer func() {
		if !committed {
			plan.RemoveStoredAttachments(s.h.home, stagedIDs)
		}
	}()
	// Keep the durable command journal free of image bytes. The task event owns the staged files after the worker
	// accepts the native input and the event transaction commits.
	journal := proto.Clone(req.Msg).(*planv1.TaskServiceSendDataRequest)
	journal.Attachments = nil
	for _, attachment := range attachments {
		journal.AttachmentMetadata = append(journal.AttachmentMetadata, proto.Clone(attachment.Metadata).(*planv1.Attachment))
	}
	eventImages := make([]*planv1.Attachment, 0, len(attachments))
	for _, attachment := range attachments {
		eventImages = append(eventImages, proto.Clone(attachment.Metadata).(*planv1.Attachment))
	}
	ev, err := s.h.sendWithAttachmentsEvent(ctx, req.Spec().Procedure,
		&planv1.TaskServiceSendRequest{TaskId: req.Msg.GetTaskId(), Text: req.Msg.GetText()}, attachments, eventImages, journal, false)
	if err != nil {
		if errors.Is(err, ErrImageInputUnsupported) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		return nil, err
	}
	delivery := planv1.InstructionImageDelivery_INSTRUCTION_IMAGE_DELIVERY_UNSPECIFIED
	if len(attachments) > 0 {
		delivery = planv1.InstructionImageDelivery_INSTRUCTION_IMAGE_DELIVERY_QUEUED
	}
	committed = true
	return connect.NewResponse(&planv1.TaskServiceSendDataResponse{Event: ev, ImageDelivery: delivery}), nil
}

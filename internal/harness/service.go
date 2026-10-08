package harness

import (
	"context"
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
	return connect.NewResponse(&planv1.TaskServiceListResponse{Tasks: tasks}), nil
}

func (s *Tasks) Get(
	ctx context.Context, req *connect.Request[planv1.TaskServiceGetRequest],
) (*connect.Response[planv1.TaskServiceGetResponse], error) {
	task, err := store.Get[*planv1.Task](ctx, s.h.store, req.Msg.GetTaskId())
	if err != nil {
		return nil, plan.Status(err)
	}
	return connect.NewResponse(&planv1.TaskServiceGetResponse{Task: task}), nil
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

func (s *Tasks) Delete(
	ctx context.Context, req *connect.Request[planv1.TaskServiceDeleteRequest],
) (*connect.Response[planv1.TaskServiceDeleteResponse], error) {
	task, err := s.h.Delete(ctx, req.Spec().Procedure, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.TaskServiceDeleteResponse{Task: task}), nil
}

func (s *Tasks) Send(
	ctx context.Context, req *connect.Request[planv1.TaskServiceSendRequest],
) (*connect.Response[planv1.TaskServiceSendResponse], error) {
	ev, err := s.h.Send(ctx, req.Spec().Procedure, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.TaskServiceSendResponse{Event: ev}), nil
}

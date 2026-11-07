package gate

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	"github.com/empowill/djinn/gen/go/machine/v1/machinev1connect"
	"github.com/empowill/djinn/internal/machine"
	"github.com/empowill/djinn/internal/plan"
)

// Handler returns the Connect handler of GateService on g, and its path prefix.
func Handler(g *Gates) (string, http.Handler) {
	return machinev1connect.NewGateServiceHandler(&service{g: g}, connect.WithInterceptors(plan.WorkerGuard, plan.Validate))
}

type service struct {
	machinev1connect.UnimplementedGateServiceHandler
	g *Gates
}

// Hold takes the gate, says why it waits, then that it holds it, and gives it back when the call ends: the
// client is done, interrupted or gone, or djinn up stops. When Djinn takes it back first (its holder's process
// ended, or its timeout passed), the stream says so and ends.
func (s *service) Hold(
	ctx context.Context, req *connect.Request[machinev1.GateServiceHoldRequest],
	stream *connect.ServerStream[machinev1.GateServiceHoldResponse],
) error {
	// The validating interceptor only sees unary calls.
	if err := protovalidate.Validate(req.Msg); err != nil {
		return connect.NewError(connect.CodeInvalidArgument, err)
	}
	if caller := plan.CallerFromContext(ctx); caller != nil {
		if req.Msg.TaskId == "" {
			req.Msg.TaskId = caller.TaskID
		} else if req.Msg.TaskId != caller.TaskID {
			return connect.NewError(connect.CodePermissionDenied, fmt.Errorf("a worker can only hold a gate for its own task (%s)", caller.TaskID))
		}
	}
	name := Name(req.Msg.GetName())
	hold, err := s.g.Take(ctx, Request{
		Name: name, TaskID: req.Msg.GetTaskId(), What: req.Msg.GetWhat(), Dir: req.Msg.GetDirectory(),
		PID: int(req.Msg.GetPid()), Timeout: time.Duration(req.Msg.GetTimeoutSeconds()) * time.Second,
	}, func(why string) {
		_ = stream.Send(&machinev1.GateServiceHoldResponse{State: machinev1.GateState_GATE_STATE_WAITING, Reason: why})
	})
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return plan.Status(err)
	}
	defer hold.Give()
	if plan.CallerFromContext(ctx) != nil {
		// A worker's command: it runs as long as the gate is held, and must not outlive djinn up.
		defer s.g.tracked(int(req.Msg.GetPid()), req.Msg.GetTaskId())()
	}
	if err := stream.Send(&machinev1.GateServiceHoldResponse{State: machinev1.GateState_GATE_STATE_HELD, Reason: "gate " + name + " held"}); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return nil
	case <-hold.Done():
		return stream.Send(&machinev1.GateServiceHoldResponse{
			State: machinev1.GateState_GATE_STATE_TAKEN_BACK, Reason: "gate " + name + " taken back: " + hold.TakenBack(),
		})
	}
}

// Admit admits a command of a worker's Bash before it runs: it says why it waits, then that it is admitted, and ends;
// the admission is held until the shell that runs the command, found among the agent's processes, ends.
func (s *service) Admit(
	ctx context.Context, req *connect.Request[machinev1.GateServiceAdmitRequest],
	stream *connect.ServerStream[machinev1.GateServiceAdmitResponse],
) error {
	if err := protovalidate.Validate(req.Msg); err != nil {
		return connect.NewError(connect.CodeInvalidArgument, err)
	}
	taskID, err := ownTask(ctx, req.Msg.GetTaskId())
	if err != nil {
		return err
	}
	agent, match := int(req.Msg.GetAgentPid()), req.Msg.GetMatch()
	_, err = s.g.Take(ctx, Request{
		Shared: true, TaskID: taskID, What: req.Msg.GetWhat(), Dir: req.Msg.GetDirectory(),
		Find: func(skip func(int) bool) int { return machine.FindCommand(agent, match, skip) },
	}, func(why string) {
		_ = stream.Send(&machinev1.GateServiceAdmitResponse{State: machinev1.GateState_GATE_STATE_WAITING, Reason: why})
	})
	if err != nil {
		if ctx.Err() != nil {
			return nil //nolint:nilerr // The hook left while it waited: nothing to answer.
		}
		return plan.Status(err)
	}
	// Held until the command's shell ends (keep), not until this call does: the hook ends now, and the command runs.
	return stream.Send(&machinev1.GateServiceAdmitResponse{State: machinev1.GateState_GATE_STATE_HELD, Reason: "admitted"})
}

// ownTask is the task a call names, a worker's own when it names none; a worker naming another's is refused.
func ownTask(ctx context.Context, taskID string) (string, error) {
	caller := plan.CallerFromContext(ctx)
	switch {
	case caller == nil:
		return taskID, nil
	case taskID == "":
		return caller.TaskID, nil
	case taskID != caller.TaskID:
		return "", connect.NewError(connect.CodePermissionDenied, fmt.Errorf("a worker's command is admitted for its own task only (%s)", caller.TaskID))
	}
	return taskID, nil
}

func (s *service) List(
	context.Context, *connect.Request[machinev1.GateServiceListRequest],
) (*connect.Response[machinev1.GateServiceListResponse], error) {
	res := &machinev1.GateServiceListResponse{}
	for _, st := range s.g.List() {
		gate := &machinev1.Gate{
			Name: st.Name, Holder: st.Holder, HolderTaskId: st.HolderTaskID, Waiting: st.Waiting,
			WaitingTaskIds: st.WaitingTaskIDs, TakesSlot: st.TakesSlot, Resources: st.Use, Ifrit: st.Ifrit,
		}
		if !st.Since.IsZero() {
			gate.Since = timestamppb.New(st.Since)
		}
		if !st.Since.IsZero() && !st.Until.IsZero() {
			gate.ExpireTime = timestamppb.New(st.Until)
		}
		res.Gates = append(res.Gates, gate)
	}
	return connect.NewResponse(res), nil
}

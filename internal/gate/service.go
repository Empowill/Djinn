package gate

import (
	"context"
	"net/http"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	"github.com/empowill/djinn/gen/go/machine/v1/machinev1connect"
	"github.com/empowill/djinn/internal/plan"
)

// Handler returns the Connect handler of GateService on g, and its path prefix.
func Handler(g *Gates) (string, http.Handler) {
	return machinev1connect.NewGateServiceHandler(&service{g: g}, connect.WithInterceptors(plan.Validate))
}

type service struct {
	machinev1connect.UnimplementedGateServiceHandler
	g *Gates
}

// Hold takes the gate, says why it waits, then that it holds it, and gives it back when the call ends: the
// client is done, interrupted or gone, or djinn up stops.
func (s *service) Hold(
	ctx context.Context, req *connect.Request[machinev1.GateServiceHoldRequest],
	stream *connect.ServerStream[machinev1.GateServiceHoldResponse],
) error {
	// The validating interceptor only sees unary calls.
	if err := protovalidate.Validate(req.Msg); err != nil {
		return connect.NewError(connect.CodeInvalidArgument, err)
	}
	name := Name(req.Msg.GetName())
	give, err := s.g.Take(ctx, name, req.Msg.GetTaskId(), req.Msg.GetWhat(), func(why string) {
		_ = stream.Send(&machinev1.GateServiceHoldResponse{State: machinev1.GateState_GATE_STATE_WAITING, Reason: why})
	})
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return plan.Status(err)
	}
	defer give()
	if err := stream.Send(&machinev1.GateServiceHoldResponse{State: machinev1.GateState_GATE_STATE_HELD, Reason: "gate " + name + " held"}); err != nil {
		return err
	}
	<-ctx.Done()
	return nil
}

func (s *service) List(
	context.Context, *connect.Request[machinev1.GateServiceListRequest],
) (*connect.Response[machinev1.GateServiceListResponse], error) {
	res := &machinev1.GateServiceListResponse{}
	for _, st := range s.g.List() {
		gate := &machinev1.Gate{Name: st.Name, Holder: st.Holder, HolderTaskId: st.HolderTaskID, Waiting: st.Waiting}
		if !st.Since.IsZero() {
			gate.Since = timestamppb.New(st.Since)
		}
		res.Gates = append(res.Gates, gate)
	}
	return connect.NewResponse(res), nil
}

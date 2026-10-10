// Package demo implements demo.v1.DemoService, which only proves that a server stream reaches the interface.
package demo

import (
	"context"
	"time"

	"connectrpc.com/connect"

	demov1 "github.com/empowill/djinn/gen/go/demo/v1"
	"github.com/empowill/djinn/gen/go/demo/v1/demov1connect"
)

// Interval is the time between two values of a count.
const Interval = 100 * time.Millisecond

// Service implements demov1connect.DemoServiceHandler.
type Service struct{}

var _ demov1connect.DemoServiceHandler = Service{}

// Count sends 1, 2, … up to req.UpTo, one value every Interval.
func (Service) Count(
	ctx context.Context, req *connect.Request[demov1.CountRequest], stream *connect.ServerStream[demov1.CountResponse],
) error {
	ticker := time.NewTicker(Interval)
	defer ticker.Stop()
	for v := int32(1); v <= req.Msg.GetUpTo(); v++ {
		if v > 1 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
			}
		}
		if err := stream.Send(&demov1.CountResponse{Value: v}); err != nil {
			return err
		}
	}
	return nil
}

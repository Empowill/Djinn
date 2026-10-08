package machine

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	"github.com/empowill/djinn/gen/go/machine/v1/machinev1connect"
)

// Handler returns the Connect handler of MachineService on m, and its path prefix. running counts the workers that
// run now; nil counts none.
func Handler(m *Monitor, running func() int) (string, http.Handler) {
	return machinev1connect.NewMachineServiceHandler(&service{m: m, running: running})
}

type service struct {
	machinev1connect.UnimplementedMachineServiceHandler
	m       *Monitor
	running func() int
}

func (s *service) Show(
	context.Context, *connect.Request[machinev1.MachineServiceShowRequest],
) (*connect.Response[machinev1.MachineServiceShowResponse], error) {
	snap := s.m.Snapshot()
	slots, rule := s.m.policy.Slots(snap)
	out := &machinev1.Machine{
		Os: snap.OS, Arch: snap.Arch, Cores: int32(snap.Cores), MemoryTotalBytes: snap.MemoryTotal,
		MemoryAvailableBytes: snap.MemoryAvailable, Workers: int32(slots), WorkersRule: rule,
		Pressure: s.m.policy.Pressure(snap), ReadTime: timestamppb.New(snap.Time),
	}
	if snap.LoadKnown {
		out.Load1 = &snap.Load1
	}
	if p := snap.CPU; p != nil {
		out.CpuPressure = &machinev1.Pressure{SomeAvg10: p.Some, FullAvg10: p.Full}
	}
	if p := snap.Memory; p != nil {
		out.MemoryPressure = &machinev1.Pressure{SomeAvg10: p.Some, FullAvg10: p.Full}
	}
	if s.running != nil {
		out.Running = int32(s.running())
	}
	return connect.NewResponse(&machinev1.MachineServiceShowResponse{Machine: out}), nil
}

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
// run now, uses lists what each one uses; nil counts and lists none.
func Handler(m *Monitor, running func() int, uses func() []*machinev1.WorkerUse) (string, http.Handler) {
	return machinev1connect.NewMachineServiceHandler(&service{m: m, running: running, uses: uses})
}

type service struct {
	machinev1connect.UnimplementedMachineServiceHandler
	m       *Monitor
	running func() int
	uses    func() []*machinev1.WorkerUse
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
	if d := snap.Disk; d != nil {
		out.Disk = &machinev1.Disk{Path: d.Path, TotalBytes: d.Total, AvailableBytes: d.Available}
	}
	for _, g := range snap.GPUs {
		out.Gpus = append(out.Gpus, &machinev1.Gpu{
			Vendor: g.Vendor, Name: g.Name, Driver: g.Driver, DriverVersion: g.DriverVersion, MemoryBytes: g.Memory,
			UnifiedMemory: g.Unified,
		})
	}
	out.CanRunLocalModel, out.LocalModelReason = s.m.policy.LocalModel(snap)
	if s.running != nil {
		out.Running = int32(s.running())
	}
	if s.uses != nil {
		out.WorkerUses = s.uses()
	}
	out.WorkerMeasure = NotMeasured
	return connect.NewResponse(&machinev1.MachineServiceShowResponse{Machine: out}), nil
}

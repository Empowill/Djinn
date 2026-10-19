package machine

import (
	"context"
	"net/http"
	"runtime"
	"slices"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	"github.com/empowill/djinn/gen/go/machine/v1/machinev1connect"
)

// HandlerOption configures the machine service handler.
type HandlerOption func(*service)

// WithSeriesRecorder sets the machine series recorder.
func WithSeriesRecorder(rec *SeriesRecorder) HandlerOption {
	return func(s *service) { s.recorder = rec }
}

// WithHistory reads the series from the store's history, which outlives a restart, rather than from memory.
func WithHistory(h *History) HandlerOption {
	return func(s *service) { s.history = h }
}

// WithSeriesWorkers sets the function returning series of running workers.
func WithSeriesWorkers(fn func(cutoff time.Time, gateIntervals []*machinev1.GateInterval) []*machinev1.WorkerSeries) HandlerOption {
	return func(s *service) { s.workers = fn }
}

// WithSeriesIntervals sets the function returning gate intervals.
func WithSeriesIntervals(fn func(cutoff time.Time) []*machinev1.GateInterval) HandlerOption {
	return func(s *service) { s.intervals = fn }
}

// WithNetwork sets the function telling since when the network is down (the harness's NetworkDownSince); nil while
// it is up.
func WithNetwork(fn func() *timestamppb.Timestamp) HandlerOption {
	return func(s *service) { s.network = fn }
}

// WithDiskFolders sets the function returning the main folders on disk, biggest first, with their size.
func WithDiskFolders(fn func() []*machinev1.DiskFolder) HandlerOption {
	return func(s *service) { s.diskFolders = fn }
}

// WithDiskItems sets the function returning the known caches and stores on the machine, biggest first.
func WithDiskItems(fn func() []*machinev1.DiskItem) HandlerOption {
	return func(s *service) { s.diskItems = fn }
}

// Handler returns the Connect handler of MachineService on m, and its path prefix. running counts the workers that
// run now, uses lists what each one uses; nil counts and lists none.
func Handler(m *Monitor, running func() int, uses func() []*machinev1.WorkerUse, opts ...HandlerOption) (string, http.Handler) {
	s := &service{m: m, running: running, uses: uses}
	for _, opt := range opts {
		opt(s)
	}
	return machinev1connect.NewMachineServiceHandler(s)
}

type service struct {
	machinev1connect.UnimplementedMachineServiceHandler
	m           *Monitor
	running     func() int
	uses        func() []*machinev1.WorkerUse
	recorder    *SeriesRecorder
	history     *History
	workers     func(cutoff time.Time, gateIntervals []*machinev1.GateInterval) []*machinev1.WorkerSeries
	intervals   func(cutoff time.Time) []*machinev1.GateInterval
	network     func() *timestamppb.Timestamp
	diskFolders func() []*machinev1.DiskFolder
	diskItems   func() []*machinev1.DiskItem
}

func (s *service) disk(snap Snapshot) *machinev1.Disk {
	var folders []*machinev1.DiskFolder
	var items []*machinev1.DiskItem
	switch {
	case s.diskFolders != nil && s.diskItems != nil:
		done := make(chan struct{})
		go func() {
			defer close(done)
			items = s.diskItems()
		}()
		folders = s.diskFolders()
		<-done
	case s.diskFolders != nil:
		folders = s.diskFolders()
	case s.diskItems != nil:
		items = s.diskItems()
	}
	if snap.Disk == nil && len(folders) == 0 && len(items) == 0 {
		return nil
	}
	out := &machinev1.Disk{Folders: folders, Items: items}
	if d := snap.Disk; d != nil {
		out.Path = d.Path
		out.TotalBytes = d.Total
		out.AvailableBytes = d.Available
	}
	return out
}

func (s *service) Show(
	context.Context, *connect.Request[machinev1.MachineServiceShowRequest],
) (*connect.Response[machinev1.MachineServiceShowResponse], error) {
	snap := s.m.Snapshot()
	p := s.m.Policy()
	slots, rule := p.Slots(snap)
	out := &machinev1.Machine{
		Os: snap.OS, Arch: snap.Arch, Cores: int32(snap.Cores), MemoryTotalBytes: snap.MemoryTotal,
		MemoryAvailableBytes: snap.MemoryAvailable, Workers: int32(slots), WorkersRule: rule,
		Pressure: p.Pressure(snap), ReadTime: timestamppb.New(snap.Time),
		Load: p.Notch, WorkerPercentile: int32(p.MemoryPercentile()), WorkerEngagedAt: p.EngagedAt(),
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
	out.Disk = s.disk(snap)
	for _, g := range snap.GPUs {
		out.Gpus = append(out.Gpus, &machinev1.Gpu{
			Vendor: g.Vendor, Name: g.Name, Driver: g.Driver, DriverVersion: g.DriverVersion, MemoryBytes: g.Memory,
			UnifiedMemory: g.Unified,
		})
	}
	out.CanRunLocalModel, out.LocalModelReason = p.LocalModel(snap)
	if s.running != nil {
		out.Running = int32(s.running())
	}
	if s.uses != nil {
		out.WorkerUses = s.uses()
	}
	out.WorkerMeasure = NotMeasured
	if s.network != nil {
		out.NetworkDownSince = s.network()
	}
	return connect.NewResponse(&machinev1.MachineServiceShowResponse{Machine: out}), nil
}

// SeriesMinutes are the ranges a series may be asked for: 10 and 30 minutes, 2 hours, a day, a week.
var SeriesMinutes = []int32{10, 30, 120, 24 * 60, 7 * 24 * 60}

func (s *service) Series(
	ctx context.Context, req *connect.Request[machinev1.MachineServiceSeriesRequest],
) (*connect.Response[machinev1.MachineServiceSeriesResponse], error) {
	minutes := req.Msg.GetMinutes()
	if !slices.Contains(SeriesMinutes, minutes) {
		minutes = 30
	}
	now := time.Now()
	cutoff := now.Add(-time.Duration(minutes) * time.Minute)
	// The points: those of the window, or those measured after since; the gate intervals are always the window's.
	points := cutoff
	if since := req.Msg.GetSince(); since != nil && since.AsTime().After(cutoff) {
		points = since.AsTime().Add(time.Nanosecond)
	}

	var gateIntervals []*machinev1.GateInterval
	if s.intervals != nil {
		gateIntervals = s.intervals(cutoff)
	}

	var workerSeries []*machinev1.WorkerSeries
	if s.workers != nil {
		workerSeries = s.workers(points, gateIntervals)
	}
	if s.history != nil {
		for _, ws := range workerSeries {
			ws.Points = s.history.WorkerPoints(ctx, ws.GetTaskId(), points, now)
		}
	}

	var snap Snapshot
	var policy Policy
	if s.m != nil {
		snap = s.m.Snapshot()
		policy = s.m.Policy()
	}

	var machinePoints []*machinev1.MachinePoint
	switch {
	case s.history != nil:
		machinePoints = s.history.MachinePoints(ctx, points, now)
	case s.recorder != nil:
		machinePoints = s.recorder.Points(points)
	}

	limitShare := policy.Share
	limitMemory := policy.CommittableLimit(policy.CommitMemory(snap))

	cores := snap.Cores
	if cores <= 0 {
		cores = runtime.NumCPU()
	}

	machineSeries := &machinev1.MachineSeries{
		Points:               machinePoints,
		MemoryTotalBytes:     snap.MemoryTotal,
		Cores:                int32(cores),
		LoadLimitShare:       limitShare,
		LoadLimitMemoryBytes: limitMemory,
	}

	return connect.NewResponse(&machinev1.MachineServiceSeriesResponse{
		Machine:       machineSeries,
		Workers:       workerSeries,
		Gates:         gateIntervals,
		WorkerMeasure: NotMeasured,
		Disk:          s.disk(snap),
	}), nil
}

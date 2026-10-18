package machine

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	"github.com/empowill/djinn/gen/go/machine/v1/machinev1connect"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// serveMachine serves the machine service of Handler on a test server, and returns its client.
func serveMachine(t *testing.T, m *Monitor, running func() int, uses func() []*machinev1.WorkerUse,
	opts ...HandlerOption,
) machinev1connect.MachineServiceClient {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle(Handler(m, running, uses, opts...))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return machinev1connect.NewMachineServiceClient(srv.Client(), srv.URL)
}

// TestService_Show: djinn machine show says the machine as read, the workers it runs and the percentile the notch
// engages a worker at.
func TestService_Show(t *testing.T) {
	t.Parallel()
	load := 3.5
	down := timestamppb.New(time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC))
	cases := []struct {
		name    string
		notch   djinnv1.LoadNotch
		s       Snapshot
		running func() int
		uses    func() []*machinev1.WorkerUse
		opts    []HandlerOption
		check   func(t *testing.T, m *machinev1.Machine)
	}{
		{name: "engaged at", notch: djinnv1.LoadNotch_LOAD_NOTCH_OVERCLOCK,
			s: Snapshot{Cores: 16, MemoryTotal: 32 * GiB, MemoryAvailable: 12 * GiB},
			check: func(t *testing.T, got *machinev1.Machine) {
				if got.GetWorkerPercentile() != 50 || got.GetWorkerEngagedAt() != "engaged at p50 (overclock)" {
					t.Errorf("show: p%d, %q", got.GetWorkerPercentile(), got.GetWorkerEngagedAt())
				}
			}},
		{name: "everything read", notch: djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM,
			s: Snapshot{Cores: 8, MemoryTotal: 16 * GiB, MemoryAvailable: 8 * GiB, LoadKnown: true, Load1: load,
				CPU: &Pressure{Some: 1}, Memory: &Pressure{Some: 2, Full: 1}, Disk: &Disk{Path: "/data", Total: 100, Available: 40},
				GPUs: []GPU{AppleGPU("Apple M2 Pro", 32*GiB)}},
			running: func() int { return 2 },
			uses:    func() []*machinev1.WorkerUse { return []*machinev1.WorkerUse{{TaskId: "t1"}} },
			opts:    []HandlerOption{WithNetwork(func() *timestamppb.Timestamp { return down })},
			check: func(t *testing.T, got *machinev1.Machine) {
				if got.GetLoad1() != load || got.GetCpuPressure().GetSomeAvg10() != 1 || got.GetMemoryPressure().GetFullAvg10() != 1 ||
					got.GetDisk().GetAvailableBytes() != 40 || len(got.GetGpus()) != 1 || !got.GetGpus()[0].GetUnifiedMemory() ||
					got.GetRunning() != 2 || len(got.GetWorkerUses()) != 1 || !got.GetNetworkDownSince().AsTime().Equal(down.AsTime()) {
					t.Errorf("show: %v", got)
				}
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			m := NewMonitor(NotchPolicy(c.notch), func() (Snapshot, error) { return c.s, nil })
			client := serveMachine(t, m, c.running, c.uses, c.opts...)
			res, err := client.Show(t.Context(), connect.NewRequest(&machinev1.MachineServiceShowRequest{}))
			if err != nil {
				t.Fatal(err)
			}
			c.check(t, res.Msg.GetMachine())
		})
	}
}

// TestService_Series: the machine's points of the window, or those measured after a point; the workers' series and
// the gate intervals of the whole window; from the recorder, or from the store's history when kept.
func TestService_Series(t *testing.T) {
	t.Parallel()
	now := time.Now()
	gateIvs := []*machinev1.GateInterval{
		{
			Gate:        "lint",
			TaskId:      "task-1",
			Command:     "go tool task lint",
			AskedTime:   timestamppb.New(now.Add(-4 * time.Minute)),
			GrantTime:   timestamppb.New(now.Add(-3 * time.Minute)),
			ReleaseTime: timestamppb.New(now.Add(-2 * time.Minute)),
		},
	}
	workerList := func() []*machinev1.WorkerSeries {
		return []*machinev1.WorkerSeries{
			{
				TaskId:   "task-1",
				Code:     "W1",
				Title:    "Worker 1",
				Provider: planv1.Provider_PROVIDER_CLAUDE,
				Model:    "claude-3-7-sonnet",
				Points: []*machinev1.WorkerPoint{
					{
						Time:        timestamppb.New(now.Add(-5 * time.Minute)),
						CpuPercent:  12.0,
						MemoryBytes: 512 << 20,
					},
				},
				Gates: gateIvs,
			},
		}
	}
	first := &machinev1.MachinePoint{Time: timestamppb.New(now.Add(-5 * time.Minute)), CpuPercent: 25.5, MemoryBytes: 2048 << 20}
	second := &machinev1.MachinePoint{Time: timestamppb.New(now.Add(-time.Minute)), CpuPercent: 40}
	cases := []struct {
		name    string
		points  []*machinev1.MachinePoint
		history bool
		since   time.Time
		want    []float64 // the CPU of each machine point
		after   bool      // the workers asked from after the first point
	}{
		{name: "window", points: []*machinev1.MachinePoint{first}, want: []float64{25.5}},
		// Since a point: only the points measured after it, the gate intervals of the whole window.
		{name: "since a point", points: []*machinev1.MachinePoint{first, second}, since: now.Add(-5 * time.Minute),
			want: []float64{40}, after: true},
		// A since older than the window: the window.
		{name: "since before the window", points: []*machinev1.MachinePoint{first, second}, since: now.Add(-2 * time.Hour),
			want: []float64{25.5, 40}},
		// Kept in the store: the history's points, the workers' too.
		{name: "history", points: []*machinev1.MachinePoint{first, second}, history: true, want: []float64{25.5, 40}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			rec := NewSeriesRecorder(nil, nil)
			opts := []HandlerOption{WithSeriesRecorder(rec)}
			if c.history {
				db, err := store.Open(t.Context(), "", plan.Entities()...)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { db.Close() })
				h := NewHistory(db)
				for _, p := range c.points {
					h.Machine(p)
				}
				h.Worker("task-1", &machinev1.WorkerPoint{Time: timestamppb.New(now.Add(-2 * time.Minute)), CpuPercent: 30})
				opts = append(opts, WithHistory(h))
			} else {
				for _, p := range c.points {
					rec.AddPoint(p)
				}
			}
			var asked time.Time
			opts = append(opts,
				WithSeriesWorkers(func(cutoff time.Time, ivs []*machinev1.GateInterval) []*machinev1.WorkerSeries {
					asked = cutoff
					return workerList()
				}),
				WithSeriesIntervals(func(cutoff time.Time) []*machinev1.GateInterval { return gateIvs }),
			)
			client := serveMachine(t, NewMonitor(Policy{Share: 0.8}, func() (Snapshot, error) {
				return Snapshot{Cores: 8, MemoryTotal: 16 << 30, MemoryAvailable: 8 << 30}, nil
			}), func() int { return 1 }, nil, opts...)
			req := &machinev1.MachineServiceSeriesRequest{Minutes: 30}
			if !c.since.IsZero() {
				req.Since = timestamppb.New(c.since)
			}
			res, err := client.Series(t.Context(), connect.NewRequest(req))
			if err != nil {
				t.Fatalf("Series RPC failed: %v", err)
			}
			msg := res.Msg
			if msg.GetMachine() == nil {
				t.Fatal("expected machine series")
			}
			pts := msg.GetMachine().GetPoints()
			got := make([]float64, len(pts))
			for i, p := range pts {
				got[i] = p.GetCpuPercent()
			}
			if len(got) != len(c.want) || len(got) > 0 && (got[0] != c.want[0] || got[len(got)-1] != c.want[len(c.want)-1]) {
				t.Errorf("machine points %v, want %v", got, c.want)
			}
			if len(msg.GetWorkers()) != 1 {
				t.Fatalf("expected 1 worker series, got %d", len(msg.GetWorkers()))
			}
			ws := msg.GetWorkers()[0]
			if ws.GetCode() != "W1" || ws.GetTitle() != "Worker 1" {
				t.Errorf("unexpected worker: %v", ws)
			}
			if c.history && (len(ws.GetPoints()) != 1 || ws.GetPoints()[0].GetCpuPercent() != 30) {
				t.Errorf("worker points %v, want the history's", ws.GetPoints())
			}
			if len(msg.GetGates()) != 1 {
				t.Errorf("expected 1 gate interval, got %d", len(msg.GetGates()))
			}
			if c.after && !asked.After(now.Add(-5*time.Minute)) {
				t.Errorf("workers asked from %v, want after the first point", asked)
			}
		})
	}
}

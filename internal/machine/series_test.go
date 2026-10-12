package machine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	"github.com/empowill/djinn/gen/go/machine/v1/machinev1connect"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

func TestSeriesRingBuffer(t *testing.T) {
	rec := NewSeriesRecorder(nil, nil)
	now := time.Now()

	// Empty recorder
	if pts := rec.Points(time.Time{}); len(pts) != 0 {
		t.Fatalf("expected 0 points, got %d", len(pts))
	}

	// Add 5 points
	for i := 0; i < 5; i++ {
		rec.AddPoint(&machinev1.MachinePoint{
			Time:            timestamppb.New(now.Add(time.Duration(i) * time.Minute)),
			CpuPercent:      float64(i * 10),
			MemoryBytes:     uint64(i * 100),
			DjinnCpuPercent: float64(i),
		})
	}

	pts := rec.Points(time.Time{})
	if len(pts) != 5 {
		t.Fatalf("expected 5 points, got %d", len(pts))
	}
	if pts[0].GetCpuPercent() != 0 || pts[4].GetCpuPercent() != 40 {
		t.Errorf("unexpected points: first=%v, last=%v", pts[0], pts[4])
	}

	// Cutoff filter
	cutoff := now.Add(2 * time.Minute)
	filtered := rec.Points(cutoff)
	if len(filtered) != 3 {
		t.Fatalf("expected 3 points after cutoff, got %d", len(filtered))
	}
	if filtered[0].GetCpuPercent() != 20 {
		t.Errorf("expected first point after cutoff to be 20, got %v", filtered[0].GetCpuPercent())
	}
}

func TestSeriesRecorderProcStat(t *testing.T) {
	fsys := fstest.MapFS{
		"proc/stat": &fstest.MapFile{
			Data: []byte("cpu  100 20 50 830 0 0 0 0 0 0\n"),
		},
	}
	rec := NewSeriesRecorder(nil, func(pid int, cgroup string) (Group, error) {
		return Group{CPU: 100 * time.Millisecond, Memory: 10 << 20}, nil
	})
	rec.SetFS(fsys)

	// First sample sets baseline
	rec.Sample()

	// Update proc stat to simulate 100 ticks elapsed, 50 busy (50% CPU)
	fsys["proc/stat"] = &fstest.MapFile{
		Data: []byte("cpu  140 20 60 880 0 0 0 0 0 0\n"), // total 1100 (+100), idle 880 (+50), busy 50
	}

	rec.Sample()

	pts := rec.Points(time.Time{})
	if len(pts) != 2 {
		t.Fatalf("expected 2 points, got %d", len(pts))
	}
	last := pts[1]
	if last.GetCpuPercent() != 50 {
		t.Errorf("expected 50%% CPU, got %v", last.GetCpuPercent())
	}
	if last.GetDjinnMemoryBytes() != 10<<20 {
		t.Errorf("expected 10 MiB djinn memory, got %v", last.GetDjinnMemoryBytes())
	}
}

func TestMachineServiceSeries(t *testing.T) {
	now := time.Now()
	rec := NewSeriesRecorder(nil, nil)
	rec.AddPoint(&machinev1.MachinePoint{
		Time:        timestamppb.New(now.Add(-5 * time.Minute)),
		CpuPercent:  25.5,
		MemoryBytes: 2048 << 20,
	})

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

	workerList := []*machinev1.WorkerSeries{
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

	workers := func(time.Time) {}
	mux := http.NewServeMux()
	path, h := Handler(
		NewMonitor(Policy{Share: 0.8}, func() (Snapshot, error) {
			return Snapshot{Cores: 8, MemoryTotal: 16 << 30, MemoryAvailable: 8 << 30}, nil
		}),
		func() int { return 1 },
		nil,
		WithSeriesRecorder(rec),
		WithSeriesWorkers(func(cutoff time.Time, ivs []*machinev1.GateInterval) []*machinev1.WorkerSeries {
			workers(cutoff)
			return workerList
		}),
		WithSeriesIntervals(func(cutoff time.Time) []*machinev1.GateInterval {
			return gateIvs
		}),
	)
	mux.Handle(path, h)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := machinev1connect.NewMachineServiceClient(srv.Client(), srv.URL)
	res, err := client.Series(context.Background(), connect.NewRequest(&machinev1.MachineServiceSeriesRequest{
		Minutes: 30,
	}))
	if err != nil {
		t.Fatalf("Series RPC failed: %v", err)
	}

	msg := res.Msg
	if msg.GetMachine() == nil {
		t.Fatal("expected machine series")
	}
	if len(msg.GetMachine().GetPoints()) != 1 {
		t.Errorf("expected 1 machine point, got %d", len(msg.GetMachine().GetPoints()))
	}
	if len(msg.GetWorkers()) != 1 {
		t.Fatalf("expected 1 worker series, got %d", len(msg.GetWorkers()))
	}
	ws := msg.GetWorkers()[0]
	if ws.GetCode() != "W1" || ws.GetTitle() != "Worker 1" {
		t.Errorf("unexpected worker: %v", ws)
	}
	if len(msg.GetGates()) != 1 {
		t.Errorf("expected 1 gate interval, got %d", len(msg.GetGates()))
	}

	// Since a point: only the points measured after it, the gate intervals of the whole window.
	rec.AddPoint(&machinev1.MachinePoint{Time: timestamppb.New(now.Add(-time.Minute)), CpuPercent: 40})
	var asked time.Time
	workers = func(cutoff time.Time) { asked = cutoff }
	res, err = client.Series(context.Background(), connect.NewRequest(&machinev1.MachineServiceSeriesRequest{
		Minutes: 30, Since: timestamppb.New(now.Add(-5 * time.Minute)),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if pts := res.Msg.GetMachine().GetPoints(); len(pts) != 1 || pts[0].GetCpuPercent() != 40 {
		t.Errorf("points since the first = %v, want the second alone", pts)
	}
	if !asked.After(now.Add(-5 * time.Minute)) {
		t.Errorf("workers asked from %v, want after the first point", asked)
	}
	if len(res.Msg.GetGates()) != 1 {
		t.Errorf("gate intervals since = %d, want the window's 1", len(res.Msg.GetGates()))
	}
	// A since older than the window: the window.
	res, err = client.Series(context.Background(), connect.NewRequest(&machinev1.MachineServiceSeriesRequest{
		Minutes: 30, Since: timestamppb.New(now.Add(-2 * time.Hour)),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if pts := res.Msg.GetMachine().GetPoints(); len(pts) != 2 {
		t.Errorf("points since before the window = %d, want 2", len(pts))
	}
}

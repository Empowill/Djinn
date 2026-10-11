package machine

import (
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	"github.com/empowill/djinn/gen/go/machine/v1/machinev1connect"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// TestCosts: each run of a command counts in its project's profile, found by the task or by the folder; a command
// outside any project is not recorded.
func TestCosts(t *testing.T) {
	ctx := t.Context()
	db, err := store.Open(ctx, "", plan.Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app := &planv1.Project{Id: store.NewID(), Name: "app", Directory: dir}
	inner := &planv1.Project{Id: store.NewID(), Name: "inner", Directory: filepath.Join(dir, "inner")}
	task := &planv1.Task{Id: store.NewID(), WishId: store.NewID(), ProjectId: inner.GetId(), Code: "W1"}
	if err := db.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal("test", "setup", app); err != nil {
			return err
		}
		if err := tx.Put(app); err != nil {
			return err
		}
		if err := tx.Put(inner); err != nil {
			return err
		}
		return tx.Put(task)
	}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle(CostsHandler(db))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	client := machinev1connect.NewCommandServiceClient(srv.Client(), srv.URL)
	record := func(req *machinev1.CommandServiceRecordRequest) *machinev1.CommandCost {
		t.Helper()
		res, err := client.Record(ctx, connect.NewRequest(req))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg.GetCost()
	}

	// By the folder: the deepest project holding it.
	if err := os.MkdirAll(filepath.Join(dir, "inner", "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	record(&machinev1.CommandServiceRecordRequest{Directory: filepath.Join(dir, "src"), Command: "go test ./...",
		CpuSeconds: 10, Seconds: 4, PeakMemoryBytes: 300 << 20})
	got := record(&machinev1.CommandServiceRecordRequest{Directory: dir, Command: "GO TEST ./...",
		CpuSeconds: 20, Seconds: 6, PeakMemoryBytes: 200 << 20, ExitCode: 1})
	if got.GetProjectId() != app.GetId() || got.GetRuns() != 2 || got.GetCommand() != "go test ./..." ||
		got.GetCpuSeconds() != 15 || got.GetSeconds() != 5 || got.GetPeakMemoryBytes() != 300<<20 ||
		got.GetLastCpuSeconds() != 20 || got.GetLastPeakMemoryBytes() != 200<<20 || got.GetLastExitCode() != 1 ||
		got.GetLastTime() == nil {
		t.Errorf("after two runs: %v", got)
	}
	if got := record(&machinev1.CommandServiceRecordRequest{Directory: filepath.Join(dir, "inner", "src"),
		Command: "npm test", Seconds: 1}); got.GetProjectId() != inner.GetId() {
		t.Errorf("in the inner project: %v", got)
	}
	// By the task, wherever it runs.
	if got := record(&machinev1.CommandServiceRecordRequest{TaskId: task.GetId(), Directory: os.TempDir(),
		Command: "npm test", Seconds: 3}); got.GetProjectId() != inner.GetId() || got.GetRuns() != 2 ||
		math.Abs(got.GetSeconds()-2) > 1e-9 {
		t.Errorf("by the task: %v", got)
	}
	// Outside any project: nothing.
	if got := record(&machinev1.CommandServiceRecordRequest{Directory: os.TempDir(), Command: "ls"}); got != nil {
		t.Errorf("outside any project: %v", got)
	}
	if _, err := client.Record(ctx, connect.NewRequest(&machinev1.CommandServiceRecordRequest{Command: "x", CpuSeconds: -1})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("a negative CPU time: %v", err)
	}

	// The gates read the highest peak of a command in its project; 0 for one never measured there.
	peaks := NewPeaks(db)
	for _, tt := range []struct {
		taskID, dir, command string
		want                 uint64
	}{
		{"", filepath.Join(dir, "src"), "Go Test ./...", 300 << 20},
		{task.GetId(), os.TempDir(), "go test ./...", 0},
		{"", os.TempDir(), "go test ./...", 0},
		{"", dir, "go vet ./...", 0},
	} {
		if got := peaks.Peak(ctx, tt.taskID, tt.dir, tt.command); got != tt.want {
			t.Errorf("peak of %q in %q, task %q = %d, want %d", tt.command, tt.dir, tt.taskID, got, tt.want)
		}
	}

	list := func(project string) []*machinev1.CommandCost {
		t.Helper()
		res, err := client.List(ctx, connect.NewRequest(&machinev1.CommandServiceListRequest{Project: project}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg.GetCosts()
	}
	if all := list(""); len(all) != 2 {
		t.Errorf("all = %v", all)
	}
	if got := list("APP"); len(got) != 1 || got[0].GetCommand() != "go test ./..." {
		t.Errorf("app = %v", got)
	}
	if _, err := client.List(ctx, connect.NewRequest(&machinev1.CommandServiceListRequest{Project: "nope"})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("an unknown project: %v", err)
	}
}

func TestGateCostWaitAndHeld(t *testing.T) {
	ctx := t.Context()
	db, err := store.Open(ctx, "", plan.Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app := &planv1.Project{Id: store.NewID(), Name: "app", Directory: dir}
	if err := db.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal("test", "setup", app); err != nil {
			return err
		}
		return tx.Put(app)
	}); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.Handle(CostsHandler(db))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	client := machinev1connect.NewCommandServiceClient(srv.Client(), srv.URL)

	// Record 10 runs with varying wait and held times
	// held: 10s, 20s, 30s, 40s, 50s, 60s, 70s, 80s, 90s, 100s
	// wait: 1s, 2s, 3s, 4s, 5s, 6s, 7s, 8s, 9s, 10s
	for i := 1; i <= 10; i++ {
		_, err := client.Record(ctx, connect.NewRequest(&machinev1.CommandServiceRecordRequest{
			Directory:   dir,
			Command:     "go tool task lint",
			Gate:        "lint",
			WaitSeconds: float64(i),
			HeldSeconds: float64(i * 10),
			CpuSeconds:  float64(i),
			Seconds:     float64(i * 10),
		}))
		if err != nil {
			t.Fatalf("run %d failed: %v", i, err)
		}
	}

	res, err := client.List(ctx, connect.NewRequest(&machinev1.CommandServiceListRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	costs := res.Msg.GetCosts()
	if len(costs) != 1 {
		t.Fatalf("expected 1 cost entry, got %d", len(costs))
	}
	c := costs[0]
	if c.GetRuns() != 10 {
		t.Errorf("runs = %d, want 10", c.GetRuns())
	}
	if c.GetGate() != "lint" {
		t.Errorf("gate = %q, want lint", c.GetGate())
	}
	if c.GetProjectName() != "app" {
		t.Errorf("projectName = %q, want app", c.GetProjectName())
	}
	if c.GetLastHeldSeconds() != 100 {
		t.Errorf("lastHeld = %v, want 100", c.GetLastHeldSeconds())
	}
	if c.GetLastWaitSeconds() != 10 {
		t.Errorf("lastWait = %v, want 10", c.GetLastWaitSeconds())
	}
	if c.GetP50HeldSeconds() < 50 || c.GetP50HeldSeconds() > 60 {
		t.Errorf("p50Held = %v, want ~55", c.GetP50HeldSeconds())
	}
	if c.GetP95HeldSeconds() < 90 {
		t.Errorf("p95Held = %v, want >= 90", c.GetP95HeldSeconds())
	}
	if len(c.GetRecentHeldSeconds()) != 10 {
		t.Errorf("recentHeld length = %d, want 10", len(c.GetRecentHeldSeconds()))
	}
}

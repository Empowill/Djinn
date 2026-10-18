package machine

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"connectrpc.com/connect"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	"github.com/empowill/djinn/gen/go/machine/v1/machinev1connect"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// costsRig is a store holding the project app in dir, the project inner in dir/inner and a task of inner, with the
// command service on it.
type costsRig struct {
	db         *store.Store
	client     machinev1connect.CommandServiceClient
	dir        string
	app, inner *planv1.Project
	task       *planv1.Task
}

// openCosts makes a costsRig in dir, and returns what closes it.
func openCosts(ctx context.Context, dir string) (*costsRig, func(), error) {
	db, err := store.Open(ctx, "", plan.Entities()...)
	if err != nil {
		return nil, nil, err
	}
	if dir, err = filepath.EvalSymlinks(dir); err != nil {
		db.Close()
		return nil, nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, "inner", "src"), 0o755); err != nil {
		db.Close()
		return nil, nil, err
	}
	r := &costsRig{db: db, dir: dir}
	r.app = &planv1.Project{Id: store.NewID(), Name: "app", Directory: dir}
	r.inner = &planv1.Project{Id: store.NewID(), Name: "inner", Directory: filepath.Join(dir, "inner")}
	r.task = &planv1.Task{Id: store.NewID(), WishId: store.NewID(), ProjectId: r.inner.GetId(), Code: "W1"}
	if err := db.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal("test", "setup", r.app); err != nil {
			return err
		}
		if err := tx.Put(r.app); err != nil {
			return err
		}
		if err := tx.Put(r.inner); err != nil {
			return err
		}
		return tx.Put(r.task)
	}); err != nil {
		db.Close()
		return nil, nil, err
	}
	mux := http.NewServeMux()
	mux.Handle(CostsHandler(db))
	srv := httptest.NewServer(mux)
	r.client = machinev1connect.NewCommandServiceClient(srv.Client(), srv.URL)
	return r, func() { srv.Close(); db.Close() }, nil
}

// newCostsRig is a costsRig of the test t alone.
func newCostsRig(t *testing.T) *costsRig {
	t.Helper()
	r, closeRig, err := openCosts(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeRig)
	return r
}

// recordedCosts is a costsRig shared by the tests that only read it, a store being slow to open under -race (0.8 s):
// two runs of go test in app (recordGoTest), one of npm test in inner. It lives until the tests end, in DJINN_HOME,
// which testx.Main removes.
var recordedCosts = sync.OnceValues(func() (*costsRig, error) {
	ctx := context.Background()
	dir, err := os.MkdirTemp(os.Getenv("DJINN_HOME"), "costs-")
	if err != nil {
		return nil, err
	}
	r, _, err := openCosts(ctx, dir)
	if err != nil {
		return nil, err
	}
	for _, req := range []*machinev1.CommandServiceRecordRequest{
		{Directory: filepath.Join(r.dir, "src"), Command: "go test ./...", CpuSeconds: 10, Seconds: 4, PeakMemoryBytes: 300 << 20},
		{Directory: r.dir, Command: "GO TEST ./...", CpuSeconds: 20, Seconds: 6, PeakMemoryBytes: 200 << 20, ExitCode: 1},
		{Directory: filepath.Join(r.dir, "inner", "src"), Command: "npm test", Seconds: 1},
	} {
		if _, err := r.client.Record(ctx, connect.NewRequest(req)); err != nil {
			return nil, err
		}
	}
	return r, nil
})

// sharedCosts is recordedCosts, the test failing when it could not be made.
func sharedCosts(t *testing.T) *costsRig {
	t.Helper()
	r, err := recordedCosts()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// record records a run and returns the command's cost after it.
func (r *costsRig) record(t *testing.T, req *machinev1.CommandServiceRecordRequest) *machinev1.CommandCost {
	t.Helper()
	res, err := r.client.Record(t.Context(), connect.NewRequest(req))
	if err != nil {
		t.Fatal(err)
	}
	return res.Msg.GetCost()
}

// list lists the costs of a project, of all when project is "".
func (r *costsRig) list(t *testing.T, project string) []*machinev1.CommandCost {
	t.Helper()
	res, err := r.client.List(t.Context(), connect.NewRequest(&machinev1.CommandServiceListRequest{Project: project}))
	if err != nil {
		t.Fatal(err)
	}
	return res.Msg.GetCosts()
}

// recordGoTest records two runs of go test in app, the first in a folder of it.
func (r *costsRig) recordGoTest(t *testing.T) *machinev1.CommandCost {
	t.Helper()
	r.record(t, &machinev1.CommandServiceRecordRequest{Directory: filepath.Join(r.dir, "src"), Command: "go test ./...",
		CpuSeconds: 10, Seconds: 4, PeakMemoryBytes: 300 << 20})
	return r.record(t, &machinev1.CommandServiceRecordRequest{Directory: r.dir, Command: "GO TEST ./...",
		CpuSeconds: 20, Seconds: 6, PeakMemoryBytes: 200 << 20, ExitCode: 1})
}

// TestCosts_Record: each run of a command counts in its project's profile, found by the task or by the folder; a
// command outside any project is not recorded. The cases share a store, each with commands of its own.
func TestCosts_Record(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		run  func(t *testing.T, r *costsRig)
	}{
		// By the folder: the deepest project holding it.
		{"by the folder", func(t *testing.T, r *costsRig) {
			got := r.recordGoTest(t)
			if got.GetProjectId() != r.app.GetId() || got.GetRuns() != 2 || got.GetCommand() != "go test ./..." ||
				got.GetCpuSeconds() != 15 || got.GetSeconds() != 5 || got.GetPeakMemoryBytes() != 300<<20 ||
				got.GetLastCpuSeconds() != 20 || got.GetLastPeakMemoryBytes() != 200<<20 || got.GetLastExitCode() != 1 ||
				got.GetLastTime() == nil {
				t.Errorf("after two runs: %v", got)
			}
		}},
		{"in the inner project", func(t *testing.T, r *costsRig) {
			if got := r.record(t, &machinev1.CommandServiceRecordRequest{Directory: filepath.Join(r.dir, "inner", "src"),
				Command: "npm ci", Seconds: 1}); got.GetProjectId() != r.inner.GetId() {
				t.Errorf("in the inner project: %v", got)
			}
		}},
		// By the task, wherever it runs.
		{"by the task", func(t *testing.T, r *costsRig) {
			r.record(t, &machinev1.CommandServiceRecordRequest{Directory: filepath.Join(r.dir, "inner", "src"),
				Command: "npm test", Seconds: 1})
			if got := r.record(t, &machinev1.CommandServiceRecordRequest{TaskId: r.task.GetId(), Directory: os.TempDir(),
				Command: "npm test", Seconds: 3}); got.GetProjectId() != r.inner.GetId() || got.GetRuns() != 2 ||
				math.Abs(got.GetSeconds()-2) > 1e-9 {
				t.Errorf("by the task: %v", got)
			}
		}},
		{"outside any project", func(t *testing.T, r *costsRig) {
			if got := r.record(t, &machinev1.CommandServiceRecordRequest{Directory: os.TempDir(), Command: "ls"}); got != nil {
				t.Errorf("outside any project: %v", got)
			}
		}},
		{"negative CPU time", func(t *testing.T, r *costsRig) {
			_, err := r.client.Record(t.Context(), connect.NewRequest(&machinev1.CommandServiceRecordRequest{Command: "x",
				CpuSeconds: -1}))
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Errorf("a negative CPU time: %v", err)
			}
		}},
		// A gate's runs keep their wait and held times, and the tree each ran on.
		{"wait and held", func(t *testing.T, r *costsRig) {
			// held: 10s, 20s, 30s, 40s, 50s, 60s, 70s, 80s, 90s, 100s
			// wait: 1s, 2s, 3s, 4s, 5s, 6s, 7s, 8s, 9s, 10s
			for i := 1; i <= 10; i++ {
				r.record(t, &machinev1.CommandServiceRecordRequest{
					Directory:   r.dir,
					Command:     "go tool task lint",
					Gate:        "lint",
					WaitSeconds: float64(i),
					HeldSeconds: float64(i * 10),
					CpuSeconds:  float64(i),
					Seconds:     float64(i * 10),
					Tree:        "4fc36b4b8037059160c6d398c70b3cf49a87e2d8",
				})
			}
			var c *machinev1.CommandCost
			for _, cost := range r.list(t, "") {
				if cost.GetCommand() == "go tool task lint" {
					c = cost
				}
			}
			if c == nil {
				t.Fatal("no cost for go tool task lint")
			}
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
			// Each run keeps the tree it ran on: Djinn skips a commit check a worker passed on the tree it commits.
			if runs := c.GetRecentRuns(); len(runs) != 10 || runs[9].GetTree() != "4fc36b4b8037059160c6d398c70b3cf49a87e2d8" {
				t.Errorf("recent runs %v; want 10, each with its tree", runs)
			}
			// Runs recorded before asks were counted: as many asked as ran.
			if c.GetAsked() != 10 {
				t.Errorf("asked = %d, want the 10 runs", c.GetAsked())
			}
		}},
	}
	r := newCostsRig(t)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.run(t, r)
		})
	}
}

// TestCosts_List: the costs of every project, or of one named in any case; an unknown project is not found.
func TestCosts_List(t *testing.T) {
	t.Parallel()
	r := sharedCosts(t)
	cases := []struct {
		name     string
		project  string
		want     []string // the commands listed
		wantCode connect.Code
	}{
		{name: "all", want: []string{"go test ./...", "npm test"}},
		{name: "by name", project: "APP", want: []string{"go test ./..."}},
		{name: "unknown project", project: "nope", wantCode: connect.CodeNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			res, err := r.client.List(t.Context(), connect.NewRequest(&machinev1.CommandServiceListRequest{Project: c.project}))
			if c.wantCode != 0 {
				if connect.CodeOf(err) != c.wantCode {
					t.Errorf("List(%q): %v, want %v", c.project, err, c.wantCode)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]bool{}
			for _, cost := range res.Msg.GetCosts() {
				got[cost.GetCommand()] = true
			}
			if len(got) != len(c.want) {
				t.Errorf("List(%q) = %v, want %v", c.project, res.Msg.GetCosts(), c.want)
			}
			for _, command := range c.want {
				if !got[command] {
					t.Errorf("List(%q) lacks %q: %v", c.project, command, res.Msg.GetCosts())
				}
			}
		})
	}
}

// TestPeaks_Peak: the gates read the highest peak of a command in its project; 0 for one never measured there.
func TestPeaks_Peak(t *testing.T) {
	t.Parallel()
	r := sharedCosts(t)
	peaks := NewPeaks(r.db)
	cases := []struct {
		name                 string
		taskID, dir, command string
		want                 uint64
	}{
		{"by the folder", "", filepath.Join(r.dir, "src"), "Go Test ./...", 300 << 20},
		{"by the task, another project", r.task.GetId(), os.TempDir(), "go test ./...", 0},
		{"outside any project", "", os.TempDir(), "go test ./...", 0},
		{"never run", "", r.dir, "go vet ./...", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := peaks.Peak(t.Context(), c.taskID, c.dir, c.command); got != c.want {
				t.Errorf("peak of %q in %q, task %q = %d, want %d", c.command, c.dir, c.taskID, got, c.want)
			}
		})
	}
}

// TestPeaks_Ask: each gate asked for a command counts a run asked, ended or not; a command never run shows asked
// alone; outside any project nothing is counted.
func TestPeaks_Ask(t *testing.T) {
	t.Parallel()
	r := newCostsRig(t)
	ctx := t.Context()
	peaks := NewPeaks(r.db)

	// Three asks, one interrupted: two runs end by themselves.
	for range 3 {
		peaks.Ask(ctx, "e2e", "", r.dir, "go tool task e2e")
	}
	for range 2 {
		r.record(t, &machinev1.CommandServiceRecordRequest{
			Directory: r.dir, Command: "go tool task e2e", Gate: "e2e", Seconds: 60,
		})
	}
	peaks.Ask(ctx, "lint", "", r.dir, "go tool task lint")
	peaks.Ask(ctx, "lint", "", os.TempDir(), "ls")

	got := map[string][2]int32{}
	for _, c := range r.list(t, "") {
		got[c.GetCommand()] = [2]int32{c.GetAsked(), c.GetRuns()}
	}
	want := map[string][2]int32{"go tool task e2e": {3, 2}, "go tool task lint": {1, 0}}
	if len(got) != len(want) || got["go tool task e2e"] != want["go tool task e2e"] ||
		got["go tool task lint"] != want["go tool task lint"] {
		t.Errorf("asked and runs = %v, want %v", got, want)
	}
}

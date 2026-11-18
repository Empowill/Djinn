package machine

import (
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	"github.com/empowill/djinn/internal/store"
)

// TestCosts_ListPages: the Gates tab lists the commands a page at a time, the most total time first, without their
// recent runs, with the summary of the gates over them all; it opens a row by its id, its runs included; a command
// recorded after a list shows in the next one.
func TestCosts_ListPages(t *testing.T) {
	t.Parallel()
	r := newCostsRig(t)
	for _, req := range []*machinev1.CommandServiceRecordRequest{
		{Directory: r.dir, Command: "go tool task lint", Seconds: 10, Gate: "lint", WaitSeconds: 0},
		{Directory: r.dir, Command: "go tool task lint", Seconds: 10, Gate: "lint", WaitSeconds: 1},
		{Directory: r.dir, Command: "go tool task lint", Seconds: 10, Gate: "lint", WaitSeconds: 12},
		{Directory: r.dir, Command: "go tool task test", Seconds: 100, Gate: "test", WaitSeconds: 7, PeakMemoryBytes: 3 << 30},
		{Directory: filepath.Join(r.dir, "inner"), Command: "npm test", Seconds: 1},
		{Directory: filepath.Join(r.dir, "inner"), Command: "npm test", Seconds: 1},
	} {
		r.record(t, req)
	}
	list := func(req *machinev1.CommandServiceListRequest) *machinev1.CommandServiceListResponse {
		t.Helper()
		res, err := r.client.List(t.Context(), connect.NewRequest(req))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg
	}
	commands := func(costs []*machinev1.CommandCost) []string {
		var out []string
		for _, c := range costs {
			out = append(out, fmt.Sprintf("%s %d runs", c.GetCommand(), len(c.GetRecentRuns())))
		}
		return out
	}

	first := list(&machinev1.CommandServiceListRequest{ByTotal: true, PageSize: 2, Brief: true, Summary: true})
	if got := commands(first.GetCosts()); !slices.Equal(got, []string{"go tool task test 0 runs", "go tool task lint 0 runs"}) ||
		first.GetTotal() != 3 || first.GetNextPageToken() == "" {
		t.Errorf("first page = %v, total %d, next %q", got, first.GetTotal(), first.GetNextPageToken())
	}
	sum := first.GetSummary()
	if sum.GetAsked() != 6 || sum.GetRan() != 6 || sum.GetHolds() != 4 || sum.GetP50WaitSeconds() != 1 ||
		sum.GetP90WaitSeconds() != 12 || sum.GetMaxWaitSeconds() != 12 || sum.GetLongWaits() != 2 {
		t.Errorf("summary = %v", sum)
	}
	if p := sum.GetPeak(); p.GetCommand() != "go tool task test" || p.GetProjectName() != "app" || len(p.GetRecentRuns()) != 0 {
		t.Errorf("peak = %v, want go tool task test of app, brief", p)
	}
	var gates []string
	for _, g := range sum.GetGates() {
		gates = append(gates, fmt.Sprintf("%s %d %g %g %g %d %s", g.GetGate(), g.GetHolds(), g.GetP50Seconds(),
			g.GetP90Seconds(), g.GetMaxSeconds(), g.GetLong(), g.GetLastCommand()))
	}
	if !slices.Equal(gates, []string{"lint 3 1 12 12 1 go tool task lint", "test 1 7 7 7 1 go tool task test"}) {
		t.Errorf("gates = %v", gates)
	}

	next := list(&machinev1.CommandServiceListRequest{ByTotal: true, PageSize: 2, Brief: true, PageToken: first.GetNextPageToken()})
	if got := commands(next.GetCosts()); !slices.Equal(got, []string{"npm test 0 runs"}) || next.GetNextPageToken() != "" ||
		next.GetSummary() != nil {
		t.Errorf("last page = %v, next %q, summary %v", got, next.GetNextPageToken(), next.GetSummary())
	}

	// A row opened: its runs all there.
	lint := first.GetCosts()[1]
	if got := commands(list(&machinev1.CommandServiceListRequest{Id: lint.GetId()}).GetCosts()); !slices.Equal(got,
		[]string{"go tool task lint 3 runs"}) {
		t.Errorf("by id = %v", got)
	}
	// A project's: its own total and summary.
	if inner := list(&machinev1.CommandServiceListRequest{Project: "inner", Summary: true}); inner.GetTotal() != 1 ||
		inner.GetSummary().GetRan() != 2 || len(inner.GetSummary().GetGates()) != 0 {
		t.Errorf("inner = %v", inner)
	}
	// A command by its text, case ignored, in a project.
	if got := commands(list(&machinev1.CommandServiceListRequest{Project: "app", Command: "GO TOOL TASK LINT"}).GetCosts()); !slices.Equal(got,
		[]string{"go tool task lint 3 runs"}) {
		t.Errorf("by command = %v", got)
	}
	if got := list(&machinev1.CommandServiceListRequest{Project: "inner", Command: "go tool task lint"}); got.GetTotal() != 0 {
		t.Errorf("lint in inner = %v", got.GetCosts())
	}
	// Recorded after: in the next list, the costs read again.
	r.record(t, &machinev1.CommandServiceRecordRequest{Directory: r.dir, Command: "go tool task e2e", Seconds: 1000})
	if got := commands(list(&machinev1.CommandServiceListRequest{ByTotal: true, PageSize: 1, Brief: true}).GetCosts()); !slices.Equal(got,
		[]string{"go tool task e2e 0 runs"}) {
		t.Errorf("after a record = %v", got)
	}
	// The cache keeps its runs: the brief rows are copies.
	if got := commands(list(&machinev1.CommandServiceListRequest{ByTotal: true}).GetCosts()); got[2] != "go tool task lint 3 runs" {
		t.Errorf("whole list = %v", got)
	}

	for _, bad := range []*machinev1.CommandServiceListRequest{{PageToken: "x"}, {PageToken: "-1"}} {
		if _, err := r.client.List(t.Context(), connect.NewRequest(bad)); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("token %q: %v, want invalid_argument", bad.GetPageToken(), err)
		}
	}
	if _, err := r.client.List(t.Context(), connect.NewRequest(&machinev1.CommandServiceListRequest{Id: store.NewID()})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("unknown id: %v, want not_found", err)
	}
}

// heavyCosts fills the store of r with n commands as a busy Djinn keeps them: one in 270 with 200 recent runs, one in
// three with one, the rest none (the real store on 18/11/2026: 5,348 commands, 3,865 runs, 2 MB).
func heavyCosts(tb testing.TB, r *costsRig, n int) {
	tb.Helper()
	now := time.Now()
	if err := r.db.Tx(tb.Context(), func(tx *store.Tx) error {
		if err := tx.Journal("test", "fill", &machinev1.CommandCost{}); err != nil {
			return err
		}
		for i := range n {
			c := &machinev1.CommandCost{
				Id: store.NewID(), ProjectId: r.app.GetId(), Command: fmt.Sprintf("go tool task e2e -- e2e/spec-%d.spec.ts", i),
				Runs: int32(1 + i%50), Seconds: float64(10 + i%600), PeakMemoryBytes: uint64(1+i%7) << 30,
				Gate: []string{"lint", "test", "e2e", "codegen", "stack", "paid", "fmt"}[i%7],
			}
			runs := 0
			if i%270 == 0 {
				runs = 200
			} else if i%3 == 0 {
				runs = 1
			}
			for j := range runs {
				c.RecentRuns = append(c.RecentRuns, &machinev1.CommandRun{
					Time: timestamppb.New(now.Add(-time.Duration(j) * time.Minute)), Seconds: 40, WaitSeconds: float64(j % 11),
					HeldSeconds: 41, Gate: c.GetGate(), TaskId: store.NewID(), PeakMemoryBytes: 1 << 30,
				})
				c.RecentHeldSeconds = append(c.RecentHeldSeconds, 41)
			}
			if err := tx.Put(c); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		tb.Fatal(err)
	}
}

// TestCosts_ListHeavy: among 5,400 commands, the Gates tab's read (a page of 25, brief, with the summary) weighs a
// hundredth of the whole list and, once read, does not read the store again until a command changes.
func TestCosts_ListHeavy(t *testing.T) {
	t.Parallel()
	r := newCostsRig(t)
	heavyCosts(t, r, 5400)
	page := &machinev1.CommandServiceListRequest{ByTotal: true, PageSize: 25, Brief: true, Summary: true}
	read := func(req *machinev1.CommandServiceListRequest) (*machinev1.CommandServiceListResponse, time.Duration) {
		t.Helper()
		start := time.Now()
		res, err := r.client.List(t.Context(), connect.NewRequest(req))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg, time.Since(start)
	}
	whole, _ := read(&machinev1.CommandServiceListRequest{})
	tab, _ := read(page)
	if len(tab.GetCosts()) != 25 || tab.GetTotal() != 5400 || tab.GetSummary().GetHolds() != 20*200+1780 {
		t.Fatalf("page: %d costs, total %d, %d holds", len(tab.GetCosts()), tab.GetTotal(), tab.GetSummary().GetHolds())
	}
	w, p := proto.Size(whole), proto.Size(tab)
	t.Logf("whole list %d bytes, the tab's page %d bytes", w, p)
	if p*100 > w {
		t.Errorf("the tab's page weighs %d bytes, over a hundredth of the whole list (%d)", p, w)
	}
	// Warm: the commands are not read again; well under the 20 ms a read of the store takes.
	var best time.Duration
	for i := range 5 {
		_, d := read(page)
		if i == 0 || d < best {
			best = d
		}
	}
	t.Logf("a warm page in %v", best)
	if best > 50*time.Millisecond {
		t.Errorf("a warm page took %v, want under 50 ms", best)
	}
}

// BenchmarkCostsList reads the commands among 5,400: whole as before W124, then the Gates tab's page, warm and after a
// command changed.
func BenchmarkCostsList(b *testing.B) {
	r, closeRig, err := openCosts(b.Context(), b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(closeRig)
	heavyCosts(b, r, 5400)
	page := &machinev1.CommandServiceListRequest{ByTotal: true, PageSize: 25, Brief: true, Summary: true}
	for _, c := range []struct {
		name  string
		req   *machinev1.CommandServiceListRequest
		stale bool
	}{{"whole", &machinev1.CommandServiceListRequest{}, true}, {"page", page, false}, {"page-changed", page, true}} {
		b.Run(c.name, func(b *testing.B) {
			for b.Loop() {
				if c.stale {
					r.record(b, &machinev1.CommandServiceRecordRequest{Directory: r.dir, Command: "true", Seconds: 1})
				}
				if _, err := r.client.List(b.Context(), connect.NewRequest(c.req)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

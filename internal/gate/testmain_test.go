package gate

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	"github.com/empowill/djinn/gen/go/machine/v1/machinev1connect"
	"github.com/empowill/djinn/internal/harness"
	"github.com/empowill/djinn/internal/machine"
	"github.com/empowill/djinn/internal/testx"
)

const helperEnv = "DJINN_GATE_HELPER"

// TestMain lets the test binary play the command run under a gate.
func TestMain(m *testing.M) {
	switch os.Getenv(helperEnv) {
	case "":
		cleanup := testx.Guard()
		code := m.Run()
		cleanup()
		os.Exit(code)
	case "exit3":
		os.Stdout.WriteString("working\n")
		os.Exit(3)
	case "sleep":
		time.Sleep(time.Minute)
	case "nap":
		time.Sleep(400 * time.Millisecond)
	case "write":
		// A command that changes its worktree: a formatter, or a worker editing while it runs.
		if err := os.WriteFile("written.txt", []byte("written\n"), 0o600); err != nil {
			os.Exit(1)
		}
	case "burn":
		// The cost of a child counts once the command waited for it.
		child := exec.Command(os.Args[0])
		child.Env = append(os.Environ(), helperEnv+"=burn-child")
		if err := child.Run(); err != nil {
			os.Exit(1)
		}
	case "burn-child":
		memory := make([]byte, 64<<20)
		for i := range memory {
			memory[i] = byte(i)
		}
		for start := time.Now(); time.Since(start) < 300*time.Millisecond; {
		}
		runtime.KeepAlive(memory)
	}
}

// tasks is a fake harness: tasks by id with their wish's rank and their azima's place, those whose worker runs, the
// ifrits, and the events noted.
type tasks struct {
	mu     sync.Mutex
	rank   map[string]int
	place  map[string]int
	works  map[string]bool
	ifrits map[string]ifritAt
	notes  []string
}

// ifritAt is where an ifrit stands: lit, going out at out, or no longer lit, and why.
type ifritAt struct {
	out time.Time
	why string
}

func (f *tasks) Ifrit(_ context.Context, id string) (bool, time.Time, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	at, ok := f.ifrits[id]
	return ok && at.why == "", at.out, at.why
}

// light lights the ifrit id, or pushes its out time back to out.
func (f *tasks) light(id string, out time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ifrits == nil {
		f.ifrits = map[string]ifritAt{}
	}
	f.ifrits[id] = ifritAt{out: out}
}

// putOut has the ifrit id no longer lit, for why.
func (f *tasks) putOut(id, why string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ifrits[id] = ifritAt{why: why}
}

func (f *tasks) Works(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.works[id]
}

func (f *tasks) Describe(_ context.Context, id string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.rank[id]; !ok {
		return "", errors.New("no such task")
	}
	return id, nil
}

func (f *tasks) Ranks(_ context.Context, ids []string) map[string]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]int{}
	for _, id := range ids {
		if r, ok := f.rank[id]; ok {
			out[id] = r
		}
	}
	return out
}

func (f *tasks) Places(_ context.Context, ids []string) map[string]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]int{}
	for _, id := range ids {
		if p, ok := f.place[id]; ok {
			out[id] = p
		}
	}
	return out
}

func (f *tasks) Note(id string, ev harness.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notes = append(f.notes, id+" "+ev.Text)
}

func (f *tasks) noted() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.notes)
}

// times replaces the durations of a text by Ns: a loaded machine takes longer.
func times(s string) string {
	return regexp.MustCompile(`[0-9][0-9.hms]*s\b`).ReplaceAllString(s, "Ns")
}

// box is a fake machine of 8 cores and 16 GiB, under the default policy; a test sets its CPU pressure and the
// memory free as it goes.
type box struct {
	mu   sync.Mutex
	cpu  float64 // percent of the last 10 s tasks waited for the CPU
	free uint64
}

// squeezed says the CPU pressure of a box under pressure.
const squeezed = "the machine is under pressure: tasks waited for the CPU 80% of the last 10 s (65% at most)"

func calm() *box { return &box{free: 8 * machine.GiB} }

func (b *box) Snapshot() machine.Snapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	return machine.Snapshot{
		Cores: 8, MemoryTotal: 16 * machine.GiB, MemoryAvailable: b.free,
		CPU: &machine.Pressure{Some: b.cpu}, Memory: &machine.Pressure{},
	}
}

func (b *box) Policy() machine.Policy { return machine.DefaultPolicy() }

func (b *box) squeeze(on bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cpu = 0
	if on {
		b.cpu = 80
	}
}

func (b *box) setFree(free uint64) {
	b.mu.Lock()
	b.free = free
	b.mu.Unlock()
}

// peaks is fake costs: the highest peak memory of each command measured, wherever it runs.
type peaks map[string]uint64

func (p peaks) Peak(_ context.Context, _, _, command string) uint64 { return p[command] }

func (p peaks) Ask(context.Context, string, string, string, string) {}

// costs records what djinn gate run sends.
type costs struct {
	machinev1connect.CommandServiceClient
	got []*machinev1.CommandServiceRecordRequest
}

func (c *costs) Record(
	_ context.Context, req *connect.Request[machinev1.CommandServiceRecordRequest],
) (*connect.Response[machinev1.CommandServiceRecordResponse], error) {
	c.got = append(c.got, req.Msg)
	return connect.NewResponse(&machinev1.CommandServiceRecordResponse{}), nil
}

// take takes the gate in the background; the channel gets the give function once granted.
func take(t *testing.T, g *Gates, name, taskID string) (<-chan func(), <-chan string) {
	t.Helper()
	return run(t, g, name, taskID, "")
}

// run takes the gate in the background to run what; the channel gets the give function once granted.
func run(t *testing.T, g *Gates, name, taskID, what string) (<-chan func(), <-chan string) {
	t.Helper()
	return ask(t, g, Request{Name: name, TaskID: taskID, What: what})
}

// merge takes the gate in the background for Djinn's merge or push of the task taskID, to run what.
func merge(t *testing.T, g *Gates, name, taskID, what string) (<-chan func(), <-chan string) {
	t.Helper()
	return ask(t, g, Request{Name: name, TaskID: taskID, What: what, First: true})
}

// ask takes the gate r asks in the background; the channel gets the give function once granted.
func ask(t *testing.T, g *Gates, r Request) (<-chan func(), <-chan string) {
	t.Helper()
	granted, waits := make(chan func(), 1), make(chan string, 16)
	go func() {
		hold, err := g.Take(t.Context(), r, func(why string) { waits <- why })
		if err == nil {
			granted <- hold.Give
		}
	}()
	return granted, waits
}

func await[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatalf("no %s", what)
	}
	var zero T
	return zero
}

func notGranted(t *testing.T, ch <-chan func(), what string) {
	t.Helper()
	select {
	case <-ch:
		t.Fatalf("%s was granted", what)
	case <-time.After(100 * time.Millisecond):
	}
}

func server(t *testing.T, g *Gates) machinev1connect.GateServiceClient {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle(Handler(g))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return machinev1connect.NewGateServiceClient(srv.Client(), srv.URL)
}

// waitFree waits until nobody holds or waits for any gate.
func waitFree(t *testing.T, g *Gates) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for len(g.List()) > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("gates still taken: %+v", g.List())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// gitT runs git with args in dir, and returns what it wrote.
func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitIn(t.Context(), dir, nil, args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return out
}

func connectRequest(name string) *connect.Request[machinev1.GateServiceHoldRequest] {
	return connect.NewRequest(&machinev1.GateServiceHoldRequest{Name: name, What: "by hand"})
}

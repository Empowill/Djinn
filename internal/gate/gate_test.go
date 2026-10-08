package gate

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	"github.com/empowill/djinn/gen/go/machine/v1/machinev1connect"
	"github.com/empowill/djinn/internal/harness"
)

// tasks is a fake harness: tasks by id with their wish's rank, and the events noted.
type tasks struct {
	mu    sync.Mutex
	rank  map[string]int
	notes []string
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

// pressure is a pressure a test changes as it goes.
type pressure struct {
	mu  sync.Mutex
	why string
}

func (p *pressure) get() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.why
}

func (p *pressure) set(why string) {
	p.mu.Lock()
	p.why = why
	p.mu.Unlock()
}

// take takes the gate in the background; the channel gets the give function once granted.
func take(t *testing.T, g *Gates, name, taskID string) (<-chan func(), <-chan string) {
	t.Helper()
	granted, waits := make(chan func(), 1), make(chan string, 16)
	go func() {
		give, err := g.Take(t.Context(), name, taskID, "", func(why string) { waits <- why })
		if err == nil {
			granted <- give
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

// TestOneAtATime: a gate has one holder at a time; the next waiter gets it when it is given back, and the tasks'
// events say so. Another gate is free meanwhile.
func TestOneAtATime(t *testing.T) {
	f := &tasks{rank: map[string]int{"W1": 0, "W2": 0}}
	g := New(nil, f)
	first := await(t, func() <-chan func() { c, _ := take(t, g, "Codegen", "W1"); return c }(), "first grant")
	second, waits := take(t, g, "codegen", "W2")
	if why := times(await(t, waits, "reason")); why != "held by W1, for Ns" {
		t.Errorf("second waits because %q", why)
	}
	notGranted(t, second, "a held gate")
	other, _ := take(t, g, "e2e", "")
	await(t, other, "another gate")
	if l := g.List(); len(l) != 2 || l[0].Name != "codegen" || l[0].HolderTaskID != "W1" || !slices.Equal(l[0].Waiting, []string{"W2"}) {
		t.Errorf("list = %+v", l)
	}
	first()
	first() // Giving back twice does nothing.
	await(t, second, "second grant")()
	want := []string{
		"W1 gate codegen: taken", "W2 gate codegen: waiting: held by W1, for Ns", "W1 gate codegen: given back after Ns",
		"W2 gate codegen: taken", "W2 gate codegen: given back after Ns",
	}
	var got []string
	for _, n := range f.noted() {
		got = append(got, times(n))
	}
	if !slices.Equal(got, want) {
		t.Errorf("notes = %q\nwant %q", got, want)
	}
	if _, err := g.Take(t.Context(), "codegen", "W9", "", nil); err == nil {
		t.Error("took a gate for an unknown task")
	}
}

// TestUnderPressure: no gate is granted while the machine is under pressure; it is once the pressure falls.
func TestUnderPressure(t *testing.T) {
	p := &pressure{why: "simulated"}
	g := New(p.get, nil)
	g.tick = 10 * time.Millisecond
	granted, waits := take(t, g, "stack", "")
	if why := await(t, waits, "reason"); why != "the machine is under pressure: simulated" {
		t.Errorf("waits because %q", why)
	}
	notGranted(t, granted, "a gate under pressure")
	p.set("")
	await(t, granted, "grant after the pressure")()
}

// TestRankFirst: when a gate frees, the waiter of the first wish of the rank gets it, whoever came first.
func TestRankFirst(t *testing.T) {
	f := &tasks{rank: map[string]int{"holder": 0, "low": 2, "high": 1}}
	g := New(nil, f)
	give := await(t, func() <-chan func() { c, _ := take(t, g, "paid", "holder"); return c }(), "first grant")
	low, lowWaits := take(t, g, "paid", "low")
	await(t, lowWaits, "low waits")
	high, highWaits := take(t, g, "paid", "high")
	await(t, highWaits, "high waits")
	outside, outsideWaits := take(t, g, "paid", "")
	await(t, outsideWaits, "outside waits")
	give()
	await(t, high, "the high wish's grant")()
	await(t, low, "the low wish's grant")()
	await(t, outside, "the grant outside any task")()
}

// TestTakeGivesUp: a waiter that leaves is forgotten; the gate goes to the next one.
func TestTakeGivesUp(t *testing.T) {
	g := New(nil, nil)
	give, err := g.Take(t.Context(), "e2e", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	left := make(chan error, 1)
	go func() { _, err := g.Take(ctx, "e2e", "", "", nil); left <- err }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	if err := await(t, left, "the waiter leaving"); !errors.Is(err, context.Canceled) {
		t.Errorf("left with %v", err)
	}
	give()
	if l := g.List(); len(l) != 0 {
		t.Errorf("after all left: %+v", l)
	}
}

const helperEnv = "DJINN_GATE_HELPER"

// TestMain lets the test binary play the command run under a gate.
func TestMain(m *testing.M) {
	switch os.Getenv(helperEnv) {
	case "":
		os.Exit(m.Run())
	case "exit3":
		os.Stdout.WriteString("working\n")
		os.Exit(3)
	case "sleep":
		time.Sleep(time.Minute)
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

// TestRun: djinn gate run holds the gate while the command runs, says why it waits, and gives the gate back when
// the command fails, cannot start, or its caller goes away.
func TestRun(t *testing.T) {
	p := &pressure{why: "simulated"}
	g := New(p.get, nil)
	g.tick = 10 * time.Millisecond
	client := server(t, g)
	t.Setenv(helperEnv, "exit3")
	var out, notice bytes.Buffer
	go func() { time.Sleep(100 * time.Millisecond); p.set("") }()
	code, err := Run(t.Context(), client, "codegen", "", Command{Args: []string{os.Args[0]}, Stdout: &out, Stderr: &out, Notice: &notice})
	if err != nil || code != 3 || out.String() != "working\n" {
		t.Errorf("run = %d, %v, output %q", code, err, out.String())
	}
	if !strings.Contains(notice.String(), "djinn: gate codegen: waiting: the machine is under pressure: simulated") {
		t.Errorf("notice = %q", notice.String())
	}
	waitFree(t, g)

	if _, err := Run(t.Context(), client, "codegen", "", Command{Args: []string{"djinn-no-such-command"}, Notice: &notice}); err == nil {
		t.Error("ran a command that does not exist")
	}
	waitFree(t, g)

	// The caller goes away while the command runs: the server gives the gate back.
	t.Setenv(helperEnv, "sleep")
	ctx, cancel := context.WithCancel(t.Context())
	stream, err := client.Hold(ctx, connectRequest("codegen"))
	if err != nil {
		t.Fatal(err)
	}
	if !stream.Receive() {
		t.Fatal(stream.Err())
	}
	if l := g.List(); len(l) != 1 || l[0].Holder == "" {
		t.Fatalf("held = %+v", l)
	}
	cancel()
	waitFree(t, g)
}

func connectRequest(name string) *connect.Request[machinev1.GateServiceHoldRequest] {
	return connect.NewRequest(&machinev1.GateServiceHoldRequest{Name: name, What: "by hand"})
}

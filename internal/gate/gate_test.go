package gate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	"github.com/empowill/djinn/gen/go/machine/v1/machinev1connect"
	"github.com/empowill/djinn/internal/harness"
	"github.com/empowill/djinn/internal/machine"
)

// tasks is a fake harness: tasks by id with their wish's rank, those whose worker runs, and the events noted.
type tasks struct {
	mu    sync.Mutex
	rank  map[string]int
	works map[string]bool
	notes []string
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
const squeezed = "the machine is under pressure: tasks waited for the CPU 80% of the last 10 s (50% at most)"

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

// take takes the gate in the background; the channel gets the give function once granted.
func take(t *testing.T, g *Gates, name, taskID string) (<-chan func(), <-chan string) {
	t.Helper()
	return run(t, g, name, taskID, "")
}

// run takes the gate in the background to run what; the channel gets the give function once granted.
func run(t *testing.T, g *Gates, name, taskID, what string) (<-chan func(), <-chan string) {
	t.Helper()
	granted, waits := make(chan func(), 1), make(chan string, 16)
	go func() {
		hold, err := g.Take(t.Context(), Request{Name: name, TaskID: taskID, What: what}, func(why string) { waits <- why })
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

// TestOneAtATime: a gate has one holder at a time; the next waiter gets it when it is given back, and the tasks'
// events say so. Another gate is free meanwhile. Held says what a task holds.
func TestOneAtATime(t *testing.T) {
	f := &tasks{rank: map[string]int{"W1": 0, "W2": 0}}
	g := New(nil, nil, f)
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
	// What a task holds, a worker that waits holding nothing; the holder outside any task is no task's.
	if !slices.Equal(g.Held("W1"), []string{"codegen"}) || g.Held("W2") != nil || g.Held("") != nil {
		t.Errorf("held: W1 %v, W2 %v, none %v", g.Held("W1"), g.Held("W2"), g.Held(""))
	}
	first()
	first() // Giving back twice does nothing.
	if g.Held("W1") != nil {
		t.Errorf("W1 still holds %v", g.Held("W1"))
	}
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
	if _, err := g.Take(t.Context(), Request{Name: "codegen", TaskID: "W9"}, nil); err == nil {
		t.Error("took a gate for an unknown task")
	}
}

// TestWaiting: Waiting says which gates a task waits for, held by another or kept by the pressure, until it gets
// them or stops waiting; a holder waits for nothing, and a waiter outside any task is no task's.
func TestWaiting(t *testing.T) {
	f := &tasks{rank: map[string]int{"W1": 0, "W2": 0}}
	p := calm()
	g := New(p, nil, f)
	g.tick = 10 * time.Millisecond
	give := await(t, func() <-chan func() { c, _ := take(t, g, "test", "W1"); return c }(), "first grant")
	test, waits := take(t, g, "Test", "W2")
	await(t, waits, "reason")
	p.squeeze(true)
	ctx, cancel := context.WithCancel(t.Context())
	left := make(chan error, 1)
	go func() {
		_, err := g.Take(ctx, Request{Name: "e2e", TaskID: "W2"}, nil)
		left <- err
	}()
	_, outside := take(t, g, "e2e", "")
	await(t, outside, "reason outside any task")
	waiting := func() bool { return slices.Equal(g.Waiting("W2"), []string{"e2e", "test"}) }
	for deadline := time.Now().Add(5 * time.Second); !waiting() && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
	if !waiting() || g.Waiting("W1") != nil || g.Waiting("") != nil {
		t.Errorf("waiting: W1 %v, W2 %v, none %v", g.Waiting("W1"), g.Waiting("W2"), g.Waiting(""))
	}

	// Gone, it waits no more; granted, it holds instead.
	cancel()
	if err := await(t, left, "end of the wait"); !errors.Is(err, context.Canceled) {
		t.Errorf("the wait ended with %v", err)
	}
	if got := g.Waiting("W2"); !slices.Equal(got, []string{"test"}) {
		t.Errorf("W2 waits for %v once it left e2e", got)
	}
	p.squeeze(false)
	give()
	await(t, test, "second grant")()
	if got := g.Waiting("W2"); got != nil {
		t.Errorf("W2 still waits for %v once granted", got)
	}
}

// TestUnderPressure: no gate is granted while the machine is under pressure; it is once the pressure falls.
func TestUnderPressure(t *testing.T) {
	p := calm()
	p.squeeze(true)
	g := New(p, nil, nil)
	g.tick = 10 * time.Millisecond
	granted, waits := take(t, g, "stack", "")
	if why := await(t, waits, "reason"); why != squeezed {
		t.Errorf("waits because %q", why)
	}
	notGranted(t, granted, "a gate under pressure")
	p.squeeze(false)
	await(t, granted, "grant after the pressure")()
}

// TestRankFirst: when a gate frees, the waiter of the first wish of the rank gets it, whoever came first.
func TestRankFirst(t *testing.T) {
	f := &tasks{rank: map[string]int{"holder": 0, "low": 2, "high": 1}}
	g := New(nil, nil, f)
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

// TestMemory: a gate goes to a measured command only when the machine holds its peak, the peaks of the commands
// holding a gate counted as taken; a light command goes at once, and one never measured goes as it comes.
func TestMemory(t *testing.T) {
	f := &tasks{rank: map[string]int{"W1": 0}}
	m := calm()
	m.setFree(18 * machine.GiB / 10)
	g := New(m, peaks{"go tool task e2e": 31 * machine.GiB / 10, "go tool task test": 5 * machine.GiB, "go tool task lint": 200 << 20}, f)
	g.tick = 10 * time.Millisecond

	heavy, waits := run(t, g, "e2e", "W1", "go tool task e2e")
	if why := await(t, waits, "reason"); why != "go tool task e2e peaks at 3.1 GiB, 1.8 GiB free" {
		t.Errorf("the heavy command waits because %q", why)
	}
	notGranted(t, heavy, "a command the machine cannot hold")
	if !slices.Contains(f.noted(), "W1 gate e2e: waiting: go tool task e2e peaks at 3.1 GiB, 1.8 GiB free") {
		t.Errorf("notes = %q", f.noted())
	}
	light, _ := run(t, g, "lint", "", "go tool task lint")
	await(t, light, "the light command's grant")()
	unmeasured, _ := run(t, g, "stack", "", "npm run everything")
	await(t, unmeasured, "the unmeasured command's grant")()

	m.setFree(8 * machine.GiB)
	giveE2E := await(t, heavy, "the heavy command's grant once memory frees")

	// The e2e run holds its gate: its peak counts as taken until it gives it back.
	test, waits := run(t, g, "test", "", "go tool task test")
	if why := await(t, waits, "reason"); why != "go tool task test peaks at 5.0 GiB, 8.0 GiB free, 3.1 GiB of it for the commands holding a gate" {
		t.Errorf("the second heavy command waits because %q", why)
	}
	notGranted(t, test, "a command beside a heavy holder")
	giveE2E()
	await(t, test, "the second heavy command's grant")()
}

// TestTakeGivesUp: a waiter that leaves is forgotten; the gate goes to the next one.
func TestTakeGivesUp(t *testing.T) {
	g := New(nil, nil, nil)
	hold, err := g.Take(t.Context(), Request{Name: "e2e"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	left := make(chan error, 1)
	go func() { _, err := g.Take(ctx, Request{Name: "e2e"}, nil); left <- err }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	if err := await(t, left, "the waiter leaving"); !errors.Is(err, context.Canceled) {
		t.Errorf("left with %v", err)
	}
	hold.Give()
	if l := g.List(); len(l) != 0 {
		t.Errorf("after all left: %+v", l)
	}
}

// TestOutside: a gate held outside a running worker (no task, or a task whose worker does not run: a lead, a person)
// takes a slot; a running worker's gate is in the worker's own slot. Giving one back frees its slot (Freed), and the
// list shows which ones take a slot and until when they may be held.
func TestOutside(t *testing.T) {
	f := &tasks{rank: map[string]int{"W1": 0, "W2": 0}, works: map[string]bool{"W1": true}}
	g := New(nil, nil, f)
	var freed atomic.Int32
	g.Freed(func() { freed.Add(1) })
	grant := func(name, taskID string) func() {
		c, _ := take(t, g, name, taskID)
		return await(t, c, "the grant of "+name)
	}
	worker := grant("test", "W1")
	if n := g.Outside(); n != 0 {
		t.Errorf("a running worker's gate: %d outside", n)
	}
	direct, lead := grant("e2e", ""), grant("stack", "W2")
	if n := g.Outside(); n != 2 {
		t.Errorf("two gates held outside the workers: %d", n)
	}
	l := g.List()
	if len(l) != 3 || !l[0].TakesSlot || !l[1].TakesSlot || l[2].TakesSlot || l[0].Until.Sub(l[0].Since) != DefaultTimeout {
		t.Errorf("list = %+v", l)
	}
	direct()
	if n := g.Outside(); n != 1 || freed.Load() != 1 {
		t.Errorf("once given back: %d outside, freed %d times", n, freed.Load())
	}
	lead()
	worker()
}

// TestHolderEnds: a gate held directly, by a process Djinn knows, is measured while it is held, and taken back once
// that process ends: the stream says so and ends, and the slot frees.
func TestHolderEnds(t *testing.T) {
	g := New(nil, nil, nil)
	g.tick = 10 * time.Millisecond
	read := Read(machine.ReadWorker)
	if machine.NotMeasured != "" {
		read = func(int) (machine.Group, error) { return machine.Group{Processes: 1, Memory: 50 << 20}, nil }
	}
	g.Measure(10*time.Millisecond, read)
	freed := make(chan struct{}, 4)
	g.Freed(func() { freed <- struct{}{} })
	client := server(t, g)

	holder := exec.Command(os.Args[0])
	holder.Env = append(os.Environ(), helperEnv+"=sleep")
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holder.Process.Kill() })
	pid := holder.Process.Pid
	stream, err := client.Hold(t.Context(), connect.NewRequest(&machinev1.GateServiceHoldRequest{
		Name: "e2e", What: "by hand", Pid: int32(pid),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !stream.Receive() || stream.Msg().GetState() != machinev1.GateState_GATE_STATE_HELD {
		t.Fatalf("hold: %v, %v", stream.Msg(), stream.Err())
	}
	var gate *machinev1.Gate
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		res, err := client.List(t.Context(), connect.NewRequest(&machinev1.GateServiceListRequest{}))
		if err != nil {
			t.Fatal(err)
		}
		if gate = res.Msg.GetGates()[0]; gate.GetResources().GetMemoryBytes() > 0 {
			break
		}
	}
	if !gate.GetTakesSlot() || gate.GetResources().GetProcesses() < 1 || gate.GetResources().GetMemoryBytes() == 0 ||
		gate.GetExpireTime().AsTime().Sub(gate.GetSince().AsTime()) != DefaultTimeout || g.Outside() != 1 {
		t.Fatalf("held by process %d: %v", pid, gate)
	}

	_ = holder.Process.Kill()
	_ = holder.Wait()
	if !stream.Receive() || stream.Msg().GetState() != machinev1.GateState_GATE_STATE_TAKEN_BACK ||
		stream.Msg().GetReason() != fmt.Sprintf("gate e2e taken back: its process %d ended", pid) {
		t.Fatalf("once its holder ended: %v, %v", stream.Msg(), stream.Err())
	}
	if stream.Receive() {
		t.Errorf("the stream goes on: %v", stream.Msg())
	}
	waitFree(t, g)
	await(t, freed, "the slot freed")
	if n := g.Outside(); n != 0 {
		t.Errorf("%d gates still held outside", n)
	}
}

// TestTimeout: a gate held past its timeout is taken back, its task told, and goes to the next waiter: a forgotten
// hold never blocks the others.
func TestTimeout(t *testing.T) {
	f := &tasks{rank: map[string]int{"W1": 0, "W2": 0}}
	g := New(nil, nil, f)
	g.tick = 10 * time.Millisecond
	hold, err := g.Take(t.Context(), Request{Name: "e2e", TaskID: "W1", Timeout: 50 * time.Millisecond}, nil)
	if err != nil {
		t.Fatal(err)
	}
	next, _ := take(t, g, "e2e", "W2")
	await(t, hold.Done(), "the gate taken back")
	if why := hold.TakenBack(); why != "held past its timeout of 50ms" {
		t.Errorf("taken back because %q", why)
	}
	await(t, next, "the next waiter's grant")()
	hold.Give() // Taken back already: nothing to give.
	notes := f.noted()
	if !slices.Contains(notes, "W1 gate e2e: taken back: held past its timeout of 50ms") ||
		slices.ContainsFunc(notes, func(n string) bool { return strings.HasPrefix(n, "W1 gate e2e: given back") }) {
		t.Errorf("notes = %q", notes)
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
	case "nap":
		time.Sleep(400 * time.Millisecond)
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
	p := calm()
	p.squeeze(true)
	g := New(p, nil, nil)
	g.tick = 10 * time.Millisecond
	client := server(t, g)
	t.Setenv(helperEnv, "exit3")
	var out, notice bytes.Buffer
	go func() { time.Sleep(100 * time.Millisecond); p.squeeze(false) }()
	code, err := Run(t.Context(), client, "codegen", "", Command{Args: []string{os.Args[0]}, Stdout: &out, Stderr: &out, Notice: &notice})
	if err != nil || code != 3 || out.String() != "working\n" {
		t.Errorf("run = %d, %v, output %q", code, err, out.String())
	}
	if !strings.Contains(notice.String(), "djinn: gate codegen: waiting: "+squeezed) {
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

// TestRunCost: djinn gate run measures what the command cost, its children included, and records it for its task;
// not when it was interrupted.
func TestRunCost(t *testing.T) {
	client := server(t, New(nil, nil, nil))
	rec := &costs{}
	t.Setenv(helperEnv, "burn")
	var notice bytes.Buffer
	code, err := Run(t.Context(), client, "codegen", "", Command{Args: []string{os.Args[0], "-test.run=^$"}, Notice: &notice, Costs: rec})
	if err != nil || code != 0 || len(rec.got) != 1 {
		t.Fatalf("run = %d, %v, recorded %v, notice %q", code, err, rec.got, notice.String())
	}
	got := rec.got[0]
	dir, _ := os.Getwd()
	if got.GetCommand() != os.Args[0]+" -test.run=^$" || got.GetDirectory() != dir || got.GetExitCode() != 0 {
		t.Errorf("recorded %v", got)
	}
	if got.GetCpuSeconds() < 0.1 || got.GetSeconds() < 0.3 || got.GetSeconds() < got.GetCpuSeconds()/float64(runtime.NumCPU()) {
		t.Errorf("CPU %.3f s, duration %.3f s", got.GetCpuSeconds(), got.GetSeconds())
	}
	switch peak := got.GetPeakMemoryBytes(); runtime.GOOS {
	case "linux", "darwin", "windows": // Windows through a Job Object (watch_windows.go)
		if peak < 64<<20 {
			t.Errorf("peak memory %d", peak)
		}
	default:
		if peak != 0 {
			t.Errorf("peak memory %d where the system does not give it", peak)
		}
	}

	// Interrupted (Ctrl-C reaches the command and djinn alike): nothing recorded, even if the command ends well.
	t.Setenv(helperEnv, "nap")
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(100*time.Millisecond, cancel)
	if code, err := Run(ctx, client, "codegen", "", Command{Args: []string{os.Args[0]}, Notice: &notice, Costs: rec}); code != 0 || err != nil {
		t.Errorf("interrupted run = %d, %v", code, err)
	}
	if len(rec.got) != 1 {
		t.Errorf("an interrupted command recorded: %v", rec.got[1:])
	}
}

func connectRequest(name string) *connect.Request[machinev1.GateServiceHoldRequest] {
	return connect.NewRequest(&machinev1.GateServiceHoldRequest{Name: name, What: "by hand"})
}

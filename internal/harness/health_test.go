package harness

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/testx"
)

// readCPU gives the running worker of the task readings of its CPU, one a minute from at, as measure does.
func (e *env) readCPU(t *testing.T, id string, at time.Time, percents ...float64) {
	t.Helper()
	e.h.mu.Lock()
	r := e.h.runs[id]
	e.h.mu.Unlock()
	if r == nil {
		t.Fatalf("task %s has no run", id)
	}
	e.h.useMu.Lock()
	defer e.h.useMu.Unlock()
	if r.samples == nil {
		r.samples = newWorkerRing(maxWorkerSamples)
	}
	for i, p := range percents {
		r.samples.Add(&machinev1.WorkerPoint{Time: timestamppb.New(at.Add(time.Duration(i) * time.Minute)), CpuPercent: p})
	}
}

// healthEvents are the lead's health events.
func healthEvents(q *leadQueue) []*planv1.LeadEvent {
	var out []*planv1.LeadEvent
	for _, ev := range q.all() {
		if ev.GetKind() == planv1.LeadEventKind_LEAD_EVENT_KIND_HEALTH {
			out = append(out, ev)
		}
	}
	return out
}

func hasHealth(s planv1.HealthState) func(*planv1.Task) bool {
	return func(t *planv1.Task) bool { return t.GetHealth().GetState() == s }
}

// passes wakes the scheduler a few times, and lets each pass run.
func (e *env) passes(n int) {
	for range n {
		e.h.Wake()
		time.Sleep(30 * time.Millisecond)
	}
}

// TestHealthStalled: a running worker silent for StallSilence whose CPU stays idle is flagged stalled once, its lead
// told in one line with the command; djinn task continue ends it and continues it on its session, and its health
// clears. A worker as silent whose CPU works is well.
func TestHealthStalled(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	clock := &fakeClock{at: time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)}
	e := up(t, t.TempDir(), WithClock(clock.now))
	queue := &leadQueue{}
	e.h.NotifyLead(queue.notify)
	wishID, _ := e.wish(t, gitRepo(t))
	idle := e.spawn(t, wishID, "text start\nwait")
	busy := e.spawn(t, wishID, "text building\nwait")
	e.follow(t, idle.GetId())("its worker said start", said("start"))
	e.follow(t, busy.GetId())("its worker said building", said("building"))

	start := clock.now()
	e.readCPU(t, idle.GetId(), start, 2, 1, 3, 1)
	e.readCPU(t, busy.GetId(), start, 2, 1, 90, 400) // A test run at work.
	clock.set(start.Add(9 * time.Minute))
	e.passes(3)
	if got := e.get(t, idle.GetId()); got.GetHealth() != nil {
		t.Fatalf("silent 9 min: %v, want well", got.GetHealth())
	}
	clock.set(start.Add(11 * time.Minute))
	e.passes(3)
	got := e.until(t, idle.GetId(), hasHealth(planv1.HealthState_HEALTH_STATE_STALLED))
	if got.GetHealth().GetReason() != "silent 11 min, CPU 2%" {
		t.Errorf("reason %q", got.GetHealth().GetReason())
	}
	if h := e.get(t, busy.GetId()).GetHealth(); h != nil {
		t.Errorf("a worker whose command works: %v, want well", h)
	}
	clock.set(start.Add(12 * time.Minute))
	e.passes(3)
	events := healthEvents(queue)
	want := "Djinn: " + idle.GetCode() + " stalled (silent 11 min, CPU 2%); relaunch: djinn task continue " + idle.GetId()
	if len(events) != 1 || events[0].GetText() != want || events[0].GetCode() != idle.GetCode() || events[0].GetWishId() != wishID {
		t.Fatalf("health events %v, want one %q", events, want)
	}

	if _, err := e.tasks.Continue(t.Context(), connect.NewRequest(&planv1.TaskServiceContinueRequest{TaskId: idle.GetId()})); err != nil {
		t.Fatal(err)
	}
	done := e.until(t, idle.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_DONE))
	if runs := done.GetRuns(); len(runs) != 2 || runs[0].GetReason() != runWoken {
		t.Errorf("runs %v, want the first woken", runs)
	}
	e.passes(3)
	if h := e.get(t, idle.GetId()).GetHealth(); h != nil {
		t.Errorf("relaunched and done: %v, want no health", h)
	}
	for _, ev := range queue.all() {
		if ev.GetKind() == planv1.LeadEventKind_LEAD_EVENT_KIND_FAILED && ev.GetCode() == idle.GetCode() {
			t.Errorf("the relaunch the lead asked for told as a failure: %q", ev.GetText())
		}
	}
	if n := len(healthEvents(queue)); n != 1 {
		t.Errorf("%d health events, want 1", n)
	}
	if _, err := e.tasks.Stop(t.Context(), connect.NewRequest(&planv1.TaskServiceStopRequest{TaskId: busy.GetId()})); err != nil {
		t.Fatal(err)
	}
}

// TestHealthAfterTurn: a worker silent since its turn's result, a turn that ended on an error, may wait for its
// background work: well past StallSilence, stalled past StallAfterTurn (a turn that ended well, Djinn stops it past
// SilentAfterResult, silent.go). A worker that holds a gate is never stalled.
func TestHealthAfterTurn(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	clock := &fakeClock{at: time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)}
	var gated sync.Map
	e := up(t, t.TempDir(), WithClock(clock.now), WithSilenceLimit(2*time.Hour))
	e.h.HeldGates(func(id string) []string {
		if _, ok := gated.Load(id); ok {
			return []string{"test"}
		}
		return nil
	}, nil)
	wishID, _ := e.wish(t, gitRepo(t))
	turn := e.spawn(t, wishID, "text start\nerror API Error: 529 overloaded\nusage 100 20 0.01\nwait")
	gate := e.spawn(t, wishID, "text gated\nwait")
	gated.Store(gate.GetId(), true)
	e.follow(t, turn.GetId())("its worker said its usage", func(ev *planv1.TaskEvent) bool {
		return ev.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_USAGE
	})
	e.follow(t, gate.GetId())("its worker said gated", said("gated"))
	start := clock.now()
	e.readCPU(t, turn.GetId(), start, 1, 1, 1)
	e.readCPU(t, gate.GetId(), start, 1, 1, 1)
	clock.set(start.Add(20 * time.Minute))
	e.passes(3)
	if h := e.get(t, turn.GetId()).GetHealth(); h != nil {
		t.Errorf("20 min after its turn's result: %v, want well", h)
	}
	clock.set(start.Add(31 * time.Minute))
	e.passes(3)
	got := e.until(t, turn.GetId(), hasHealth(planv1.HealthState_HEALTH_STATE_STALLED))
	if r := got.GetHealth().GetReason(); r != "silent 31 min, CPU 1%, since its turn ended" {
		t.Errorf("reason %q", r)
	}
	if h := e.get(t, gate.GetId()).GetHealth(); h != nil {
		t.Errorf("a worker that holds a gate: %v, want well", h)
	}
	for _, task := range []*planv1.Task{turn, gate} {
		if _, err := e.tasks.Stop(t.Context(), connect.NewRequest(&planv1.TaskServiceStopRequest{TaskId: task.GetId()})); err != nil {
			t.Fatal(err)
		}
	}
	e.passes(2)
	if h := e.get(t, turn.GetId()).GetHealth(); h != nil {
		t.Errorf("stopped on request: %v, want no health", h)
	}
}

// sessionLimit is what a fake worker the account's session limit stops says, as claude does.
const sessionLimit = "fail You've hit your session limit · resets 7:20am (Europe/Paris)"

// TestHealthLimitReset: a task its usage limit stopped waits, limited; at the reset Djinn resumes it, and tells the
// lead once, with the other task the same limit stopped, in one line.
func TestHealthLimitReset(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	clock := &fakeClock{at: time.Date(2026, 10, 17, 4, 28, 30, 0, time.UTC)}
	e := up(t, t.TempDir(), WithClock(clock.now))
	queue := &leadQueue{}
	e.h.NotifyLead(queue.notify)
	wishID, _ := e.wish(t, gitRepo(t))
	// Both run when the limit stops the first: no worker of their agent starts while it holds.
	first := e.spawn(t, wishID, "sleep 300ms\n"+sessionLimit)
	second := e.spawn(t, wishID, sessionLimit)
	for _, task := range []*planv1.Task{first, second} {
		got := e.until(t, task.GetId(), hasHealth(planv1.HealthState_HEALTH_STATE_LIMITED))
		if got.GetStatus() != planv1.TaskStatus_TASK_STATUS_RESUMING || got.GetResumeAfter() == nil {
			t.Fatalf("after the limit: %v", got)
		}
	}
	reset := e.get(t, first.GetId()).GetResumeAfter().AsTime()
	clock.set(reset.Add(time.Second))
	for _, task := range []*planv1.Task{first, second} {
		e.until(t, task.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_DONE))
	}
	e.passes(3)
	events := healthEvents(queue)
	want := "Djinn: " + first.GetCode() + ", " + second.GetCode() + ": the usage limit that stopped them lifted (reset at " +
		ClockText(reset, reset) + "); Djinn resumes them by itself."
	if len(events) != 1 || events[0].GetText() != want || events[0].GetWishId() != wishID {
		t.Errorf("health events %v, want one %q", events, want)
	}
	if h := e.get(t, first.GetId()).GetHealth(); h != nil {
		t.Errorf("resumed and done: %v, want no health", h)
	}
}

// accounts plays the account an agent is signed in with.
type accounts struct {
	mu   sync.Mutex
	name string
	read int
}

func (a *accounts) of(_ context.Context, _ planv1.Provider) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.read++
	return a.name
}

func (a *accounts) set(name string) {
	a.mu.Lock()
	a.name = name
	a.mu.Unlock()
}

// TestHealthAccountChanged replays the night of 17/10/2026 (W90, W124): the session limit stopped a task until
// 04:00, the developer signed the agent in with another account. Djinn sees the account change within accountEvery,
// resumes the task at once, and tells the lead once; an account that stays the same holds the task.
func TestHealthAccountChanged(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	clock := &fakeClock{at: time.Date(2026, 10, 17, 4, 28, 30, 0, time.UTC)}
	account := &accounts{name: "a1"}
	e := up(t, t.TempDir(), WithClock(clock.now), WithAccount(account.of))
	queue := &leadQueue{}
	e.h.NotifyLead(queue.notify)
	wishID, _ := e.wish(t, gitRepo(t))
	task := e.spawn(t, wishID, sessionLimit)
	got := e.until(t, task.GetId(), hasHealth(planv1.HealthState_HEALTH_STATE_LIMITED))
	if got.GetHealth().GetAccount() != "a1" {
		t.Fatalf("the account the limit stopped it on: %q", got.GetHealth().GetAccount())
	}
	clock.set(clock.now().Add(2 * time.Minute))
	e.passes(3)
	if got := e.get(t, task.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_RESUMING {
		t.Fatalf("the same account: %v, want it waiting", got)
	}
	account.mu.Lock()
	reads := account.read
	account.mu.Unlock()
	if reads > 3 {
		t.Errorf("the account read %d times in 2 minutes, want at most one a minute", reads)
	}

	account.set("a2")
	clock.set(clock.now().Add(2 * time.Minute))
	e.h.Wake()
	done := e.until(t, task.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_DONE))
	if done.GetResumes() != 1 {
		t.Errorf("resumes %d, want 1", done.GetResumes())
	}
	e.passes(2)
	events := healthEvents(queue)
	want := "Djinn: " + task.GetCode() + ": the usage limit that stopped it lifted (its account changed); Djinn resumes it by itself."
	if len(events) != 1 || events[0].GetText() != want || events[0].GetCode() != task.GetCode() {
		t.Errorf("health events %v, want one %q", events, want)
	}
	all := strings.Join(eventTexts(e.watch(t.Context(), t, task.GetId(), 0)), "\n")
	if !strings.Contains(all, "resuming: its account changed: the usage limit no longer holds; Djinn resumes it now") {
		t.Errorf("no resume event in:\n%s", all)
	}
}

// TestHealthRetriable: a worker whose agent broke fails, and its task is flagged to relaunch: its lead hears it once,
// in one health line with the command, not a second time as a failure. Continued, it runs and its health clears. A
// worker failed for good is never flagged, and its lead hears the failure.
func TestHealthRetriable(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	e := up(t, t.TempDir())
	queue := &leadQueue{}
	e.h.NotifyLead(queue.notify)
	wishID, _ := e.wish(t, gitRepo(t))
	crash := e.spawn(t, wishID, "text working\nfail claude ended before the end of its turn")
	final := e.spawn(t, wishID, "fail syntax error on line 42")
	got := e.until(t, crash.GetId(), hasHealth(planv1.HealthState_HEALTH_STATE_RETRIABLE))
	if r := got.GetHealth().GetReason(); r != "a crash: claude ended before the end of its turn" {
		t.Errorf("reason %q", r)
	}
	e.until(t, final.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_FAILED))
	e.passes(3)
	if h := e.get(t, final.GetId()).GetHealth(); h != nil {
		t.Errorf("failed for good: %v, want no health", h)
	}
	want := "Djinn: " + crash.GetCode() + " can be relaunched (a crash: claude ended before the end of its turn); " +
		"relaunch: djinn task continue " + crash.GetId()
	if events := healthEvents(queue); len(events) != 1 || events[0].GetText() != want {
		t.Errorf("health events %v, want one %q", events, want)
	}
	var failed []string
	for _, ev := range queue.all() {
		if ev.GetKind() == planv1.LeadEventKind_LEAD_EVENT_KIND_FAILED {
			failed = append(failed, ev.GetCode())
		}
	}
	if len(failed) != 1 || failed[0] != final.GetCode() {
		t.Errorf("failures told %v, want only %s's", failed, final.GetCode())
	}

	if _, err := e.tasks.Continue(t.Context(), connect.NewRequest(&planv1.TaskServiceContinueRequest{
		TaskId: crash.GetId(), Prompt: new("text resumed"),
	})); err != nil {
		t.Fatal(err)
	}
	e.until(t, crash.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_DONE))
	e.passes(3)
	if h := e.get(t, crash.GetId()).GetHealth(); h != nil {
		t.Errorf("continued and done: %v, want no health", h)
	}
}

// TestRetriable classifies the errors of tasks failed or cut short on this machine (W188, 7 to 17/10/2026).
func TestRetriable(t *testing.T) {
	t.Parallel()
	failed := planv1.TaskStatus_TASK_STATUS_FAILED
	for _, c := range []struct {
		status  planv1.TaskStatus
		err     string
		why     string
		limited bool
	}{
		{failed, "claude ended before the end of its turn", "a crash: claude ended before the end of its turn", false},
		{failed, "The stream was interrupted. Please continue the task you were working on.",
			"a crash: The stream was interrupted. Please continue the task you were working on.", false},
		{failed, "There was a network issue connecting to the server, please try again.",
			"a crash: There was a network issue connecting to the server, please try again.", false},
		{failed, "lost process: worker is not running", "a crash: lost process: worker is not running", false},
		{failed, "lost process: worker is not running; the Lamplighter woke it 3 times in a row and gives up", "", false},
		{failed, "resumed 3 times without finishing; the last time: djinn up stopped while the worker ran",
			"cut short by a stop of Djinn", false},
		{failed, "You've hit your session limit · resets 4am (Europe/Paris)", "stopped by the account's session limit", true},
		{failed, "resumed 3 times without finishing; the last time: You've hit your session limit · resets 4am",
			"stopped by the account's session limit", true},
		{failed, "silent for 16 min; the Lamplighter woke it 3 times in a row and gives up", "", false},
		{planv1.TaskStatus_TASK_STATUS_INTERRUPTED, "djinn up stopped while the worker ran", "cut short by a stop of Djinn", false},
		{failed, "syntax error on line 42", "", false},
		{failed, "its dependency W3 ended stopped", "", false},
		{failed, "budget exceeded: wall time 2h0m1s (limit 2h0m0s)", "", false},
		{failed, `agy stopped: headless, it cannot ask for the "unsandboxed" permission (git status)`, "", false},
		{planv1.TaskStatus_TASK_STATUS_STOPPED, "stopped on request", "", false},
		{planv1.TaskStatus_TASK_STATUS_DONE, "", "", false},
	} {
		why, limited := retriable(&planv1.Task{Status: c.status, Error: c.err})
		if why != c.why || limited != c.limited {
			t.Errorf("%v %q: %q, %v; want %q, %v", c.status, c.err, why, limited, c.why, c.limited)
		}
	}
}

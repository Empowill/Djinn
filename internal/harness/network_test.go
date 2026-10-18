package harness

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/testx"
)

// fakeProbe is the network's probe in a test: it fails while down is set, and counts its calls.
type fakeProbe struct {
	mu    sync.Mutex
	down  error
	calls int
}

func (p *fakeProbe) probe(context.Context, planv1.Provider) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	return p.down
}

func (p *fakeProbe) set(err error) {
	p.mu.Lock()
	p.down = err
	p.mu.Unlock()
}

func (p *fakeProbe) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// networkLines are the texts of the lead's network events.
func networkLines(q *leadQueue) []string {
	var out []string
	for _, ev := range q.all() {
		if ev.GetKind() == planv1.LeadEventKind_LEAD_EVENT_KIND_NETWORK {
			out = append(out, ev.GetText())
		}
	}
	return out
}

// resumedPlays is the fake agent, whose workers Djinn resumes after an outage play then instead of their first prompt:
// the network is back, their turn goes through.
type resumedPlays struct {
	Fake
	then    string
	started func(taskID string) // told of each worker that starts, before it plays
}

func (p resumedPlays) Start(ctx context.Context, spec Spec) (Worker, error) {
	if strings.Contains(spec.Prompt, networkLine) {
		spec.Prompt = p.then
	}
	if p.started != nil {
		p.started(spec.TaskID)
	}
	return p.Fake.Start(ctx, spec)
}

// TestNetSign: what a worker's agent and a command say when the network is down is a sign of an outage; an
// API's refusal, a local connection refused, a code failure are not. A weak sign (Chromium's ERR_NETWORK_CHANGED, which
// Docker's interfaces make too) counts only within two minutes of a strong one.
func TestNetSign(t *testing.T) {
	t.Parallel()
	clock := &fakeClock{at: time.Date(2026, 10, 17, 15, 12, 0, 0, time.UTC)}
	h := &Harness{clock: clock.now, kick: make(chan struct{}, 1)}
	if h.netSign(planv1.Provider_PROVIDER_UNSPECIFIED, "Error: page.goto: net::ERR_NETWORK_CHANGED at http://127.0.0.1:4173/", false) {
		t.Error("a weak sign alone: counted")
	}
	for _, tt := range []struct {
		text   string
		worker bool
		want   bool
	}{
		{"API Error: Connection error.", true, true},
		{"API Error: 529 {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\"}}", true, true},
		{"API Error: Request timed out.", true, true},
		{"The model's response was cut off mid-stream", true, true},
		{"API Error: 400 invalid_request_error", true, false},
		{"exit code 1", true, false},
		{"fatal: unable to access 'https://github.com/empowill/djinn.git/': Could not resolve host: github.com", false, true},
		{"error connecting to api.github.com\ncheck your internet connection or https://githubstatus.com", false, true},
		{"npm error code EAI_AGAIN\nnpm error getaddrinfo EAI_AGAIN registry.npmjs.org", false, true},
		{"dial tcp 127.0.0.1:5432: connect: connection refused", false, false},
		{"--- FAIL: TestLogin\n    login_test.go:12: no session", false, false},
	} {
		if got := h.netSign(planv1.Provider_PROVIDER_CLAUDE, tt.text, tt.worker); got != tt.want {
			t.Errorf("netSign(%q, worker %v) = %v, want %v", tt.text, tt.worker, got, tt.want)
		}
	}
	weak := "Error: page.goto: net::ERR_NETWORK_CHANGED\nTypeError: Failed to fetch"
	if !h.netSign(planv1.Provider_PROVIDER_UNSPECIFIED, weak, false) {
		t.Error("a weak sign with a strong one at the same time: not counted")
	}
	clock.set(clock.now().Add(90 * time.Second)) // A strong sign came 90 s ago.
	if !h.netSign(planv1.Provider_PROVIDER_UNSPECIFIED, weak, false) {
		t.Error("a weak sign 90 s after a strong one: not counted")
	}
	clock.set(clock.now().Add(3 * time.Minute))
	if h.netSign(planv1.Provider_PROVIDER_UNSPECIFIED, weak, false) {
		t.Error("a weak sign alone, minutes after the last strong one: counted")
	}
	if h.netHolds() {
		t.Error("without a probe, an outage was suspected: Djinn holds its work")
	}
}

// TestNetProbePass (W285): a worker its provider's connection error cuts is not failed: it waits as resuming,
// "network down since 10:00", and its lead hears no failure. The probe confirms the outage: the Djinn hears one line,
// the running worker is paused, a new task waits, the window sees since when; the probe asks again every 30 s, no
// sooner. The network back, the Djinn hears one line, the paused worker resumes first, then the task cut, told the
// network came back, at no resume's cost, then the new task; and no probe is made any more.
func TestNetProbePass(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	start := time.Date(2026, 10, 17, 10, 0, 0, 0, time.Local)
	clock := &fakeClock{at: start}
	probe := &fakeProbe{down: errors.New("dial tcp: lookup api.anthropic.com: no such host")}
	providers := testProviders()
	var mu sync.Mutex
	var order []string // the workers started once the network is back, and whether the paused one was paused still
	var e *env
	var running *planv1.Task
	providers[planv1.Provider_PROVIDER_FAKE] = resumedPlays{then: "text resumed", started: func(id string) {
		if e == nil || e.h.NetworkDownSince() != nil || probe.count() < 3 {
			return
		}
		e.h.mu.Lock()
		paused := e.h.runs[running.GetId()].paused
		e.h.mu.Unlock()
		mu.Lock()
		defer mu.Unlock()
		order = append(order, id)
		if paused {
			order = append(order, "while the paused worker still was")
		}
	}}
	e = upWith(t, t.TempDir(), providers, WithClock(clock.now), WithNetworkProbe(probe.probe))
	queue := &leadQueue{}
	e.h.NotifyLead(queue.notify)
	wishID, _ := e.wish(t, gitRepo(t))

	running = e.spawn(t, wishID, "text start\nwait")
	e.follow(t, running.GetId())("its worker said start", said("start"))
	cut := e.spawn(t, wishID, "text working\nfail API Error: Connection error.")
	got := e.until(t, cut.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RESUMING))
	if !strings.HasPrefix(got.GetWaitReason(), "network down since 10:00") || got.GetError() != "" ||
		got.GetResumeAfter() != nil || got.GetRuns()[0].GetReason() != "network" {
		t.Fatalf("the task the network cut: %v; want resuming, network down since 10:00", got)
	}

	e.until(t, running.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_PAUSED))
	if since := e.h.NetworkDownSince(); since == nil || !since.AsTime().Equal(start) {
		t.Errorf("the window's network down since %v; want %v", since, start)
	}
	if lines := networkLines(queue); len(lines) != 1 ||
		lines[0] != "Djinn: the network is down since 10:00 (API Error: Connection error.); Djinn holds its outside work and resumes it once back." {
		t.Errorf("network lines %q; want one, down", lines)
	}
	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Later", Prompt: "text later", Provider: planv1.Provider_PROVIDER_FAKE,
	}))
	if err != nil {
		t.Fatal(err)
	}
	later := res.Msg.GetTask()
	if later.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING ||
		later.GetWaitReason() != "network down since 10:00: Djinn starts no worker until it is back" {
		t.Errorf("a task spawned while the network is down: %v", later)
	}

	calls := probe.count()
	clock.set(start.Add(20 * time.Second))
	e.passes(3)
	if n := probe.count(); n != calls {
		t.Errorf("%d probes 20 s after the last; want none more", n-calls)
	}
	clock.set(start.Add(time.Minute))
	e.passes(3)
	if n := probe.count(); n != calls+1 {
		t.Errorf("%d probes 60 s after the last; want one", n-calls)
	}
	if got := e.get(t, cut.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_RESUMING {
		t.Errorf("the task the network cut, while it is down: %v", got)
	}

	probe.set(nil)
	clock.set(start.Add(2 * time.Minute))
	e.passes(1)
	e.until(t, cut.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_DONE))
	e.until(t, later.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_DONE))
	if got := e.get(t, running.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
		t.Errorf("the paused worker, the network back: %v; want running again", got)
	}
	if e.h.NetworkDownSince() != nil {
		t.Error("the network back, the window still sees it down")
	}
	lines := networkLines(queue)
	if len(lines) != 2 || !strings.HasPrefix(lines[1], "Djinn: the network is back (down from 10:00, 2 min); Djinn resumes, one per pass: paused "+
		running.GetCode()+"; cut "+cut.GetCode()+".") {
		t.Errorf("network lines %q; want down, then back", lines)
	}

	// One per pass, in order: the paused worker, then the task cut, then the new one.
	mu.Lock()
	if want := []string{cut.GetId(), later.GetId()}; !slices.Equal(order, want) {
		t.Errorf("started once back: %q; want %q, after the paused worker resumed", order, want)
	}
	mu.Unlock()
	got = e.get(t, cut.GetId())
	if got.GetResumes() != 0 {
		t.Errorf("the task cut in a confirmed outage spent %d resumes; want none", got.GetResumes())
	}
	all := strings.Join(eventTexts(e.watch(t.Context(), t, cut.GetId(), 0)), "\n")
	for _, want := range []string{
		"resuming: network down since 10:00 (API Error: Connection error.); Djinn resumes it once the network is back",
		"resumed after the network came back",
		"resumed",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("no event %q in:\n%s", want, all)
		}
	}
	for _, ev := range queue.all() {
		if ev.GetKind() == planv1.LeadEventKind_LEAD_EVENT_KIND_FAILED || ev.GetKind() == planv1.LeadEventKind_LEAD_EVENT_KIND_HEALTH {
			t.Errorf("the lead heard %v: %q", ev.GetKind(), ev.GetText())
		}
	}

	calls = probe.count()
	clock.set(start.Add(5 * time.Minute))
	e.passes(3)
	if n := probe.count(); n != calls {
		t.Errorf("%d probes once the network is back; want none", n-calls)
	}
}

// pushDueOnW3 makes a push due as W3 is committed: W3 finishes its azima, so its push is due at once. It returns
// the remote and W3.
func (in *integration) pushDueOnW3(t *testing.T) (bare string, w3 *planv1.Task) {
	t.Helper()
	bare = in.remote(t)
	az := in.azima(t, in.wishID, "NetPush")
	in.ran()
	return bare, in.finished(t, "W3", map[string]string{"app/src/c.txt": "c\n"}, func(task *planv1.Task) {
		task.PartOf = az.GetId()
	})
}

// networkOut is what the end of an e2e run printed on 17/10/2026, its setup's npm unable to reach its registry.
const networkOut = "npm error code EAI_AGAIN\nnpm error syscall getaddrinfo\nnpm error getaddrinfo EAI_AGAIN registry.npmjs.org\n" +
	"Error: page.goto: net::ERR_NETWORK_CHANGED"

// TestIntegrate_NetworkPushChecks (W285): push checks that fail on the network hold nothing and start no correction
// on their first failure; once the network is back (or if it stayed up and they fail again), Djinn runs them again.
func TestIntegrate_NetworkPushChecks(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	// The outage confirmed, no push nor fetch runs while it lasts; once the network is back, the push checks run once
	// again, and green, the branch is pushed.
	t.Run("run again when back", func(t *testing.T) {
		t.Parallel()
		probe := &fakeProbe{down: errors.New("dial tcp: lookup api.anthropic.com: no such host")}
		in := integrating(t, WithNetworkProbe(probe.probe))
		in.checking(t, testAtPush, nil)
		in.correctWith(func(dir, p string) string { return "wait" })
		in.testCode, in.testOut = 1, networkOut
		bare, w3 := in.pushDueOnW3(t)
		old := in.remoteTip(t, bare)
		in.pass(t, 0)
		if runs, _ := in.ran(); !slices.Contains(runs, "test") {
			t.Fatalf("runs %q; want the push checks", runs)
		}
		if state := in.pushState(t); state.GetHeld() != "" || state.GetCorrectedBy() != "" || len(in.corrections(t)) != 0 {
			t.Fatalf("push checks failed on the network: %v, %d corrections; want nothing held, no correction", state,
				len(in.corrections(t)))
		}
		_, texts := in.integration(t, w3)
		if want := "the push checks of " + in.branch; !slices.ContainsFunc(texts, func(s string) bool {
			return strings.HasPrefix(s, want) && strings.Contains(s, "failed on the network (test: test exited 1") &&
				strings.HasSuffix(s, "Djinn runs them again once it is back, with no correction")
		}) {
			t.Errorf("W3's events %q", texts)
		}
		eventually(t, func() bool { return in.h.NetworkDownSince() != nil }, func() string { return "the network still up" })

		// Down: no push, no fetch.
		in.testCode, in.testOut = 0, "ok"
		in.pass(t, time.Minute)
		if runs, _ := in.ran(); len(runs) != 0 {
			t.Errorf("runs %q while the network is down; want none", runs)
		}

		// Back: the push checks run once again, green, and the branch is pushed.
		probe.set(nil)
		in.later(time.Minute)
		eventually(t, func() bool { in.h.Wake(); return !in.h.netHolds() }, func() string { return "the network still down" })
		in.pass(t, 0)
		if runs, _ := in.ran(); !slices.Equal(runs, []string{"test"}) {
			t.Errorf("runs %q once the network is back; want the push checks once", runs)
		}
		if got := in.remoteTip(t, bare); got == old || got != in.tip(t) {
			t.Errorf("origin at %s, the branch at %s; want it pushed", got, in.tip(t))
		}
		if n := len(in.corrections(t)); n != 0 {
			t.Errorf("%d corrections; want none", n)
		}
	})

	// Push checks that fail on the network again once it is up (here no probe ever confirms an outage) are red as any:
	// the push is held, and a correction starts.
	t.Run("run again once when up", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		in.checking(t, testAtPush, nil)
		in.correctWith(func(dir, p string) string { return "wait" })
		in.testCode, in.testOut = 1, networkOut
		in.pushDueOnW3(t)
		in.pass(t, 0)
		if state := in.pushState(t); state.GetHeld() != "" || len(in.corrections(t)) != 0 {
			t.Fatalf("the first failure on the network: %v; want nothing held, no correction", state)
		}
		in.pass(t, time.Minute)
		if state := in.pushState(t); state.GetHeld() == "" || state.GetCorrectedBy() == "" {
			t.Errorf("failed on the network again, the network up: %v; want held, corrected", state)
		}
		if runs, _ := in.ran(); !slices.Equal(slices.DeleteFunc(runs, func(r string) bool { return r != "test" }), []string{"test", "test"}) {
			t.Errorf("runs %q; want the push checks twice", runs)
		}
	})
}

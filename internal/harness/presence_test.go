package harness

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/gen/go/terminal/v1/terminalv1connect"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// lit is an ifrit spawned on the fake clock, its first turn played and listening, with its lead's events and its
// warnings recorded.
type lit struct {
	e      *env
	clock  *fakeClock
	start  time.Time
	wishID string
	id     string
	next   func(what string, ok func(*planv1.TaskEvent) bool) *planv1.Task
	lead   *leadQueue
	warned chan *planv1.Task
}

// lightIfrit spawns an ifrit that plays prompt, on a Djinn of its own in home, at 10:00 on the fake clock. The
// Lamplighter leaves a worker silent for less than 4 hours alone: the clock moves by tens of minutes at once.
func lightIfrit(t *testing.T, home, prompt string) *lit {
	t.Helper()
	clock := &fakeClock{at: time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)}
	e := up(t, home, WithClock(clock.now), WithTick(time.Hour), WithSilenceLimit(4*time.Hour))
	l := &lit{e: e, clock: clock, start: clock.now(), lead: &leadQueue{}, warned: make(chan *planv1.Task, 8)}
	e.h.NotifyLead(l.lead.notify)
	e.h.WarnIfrits(func(t *planv1.Task) { l.warned <- t })
	l.wishID, _ = e.wish(t, gitRepo(t))
	l.id = e.mustSpawn(t, l.wishID, "Front of the lamp", prompt, &planv1.TaskServiceSpawnRequest{
		Kind: planv1.TaskKind_TASK_KIND_IFRIT,
	}).GetId()
	l.next = e.follow(t, l.id)
	return l
}

// at moves the clock to d after the ifrit was lit, and has the scheduler make a pass, as each tick does.
func (l *lit) at(t *testing.T, d time.Duration) {
	l.clock.set(l.start.Add(d))
	l.e.h.schedule(t.Context())
}

// outAt waits until the ifrit's task says it goes out d after it was lit.
func (l *lit) outAt(t *testing.T, d time.Duration) *planv1.Task {
	t.Helper()
	return l.e.until(t, l.id, func(task *planv1.Task) bool {
		return task.GetIfrit().GetOutTime().AsTime().Equal(l.start.Add(d))
	})
}

// goesOutAt is the event that warns the ifrit goes out soon.
func goesOutAt(ev *planv1.TaskEvent) bool { return strings.HasPrefix(ev.GetText(), "goes out at ") }

// goingOut is the event where it goes out, its input closing.
func goingOut(ev *planv1.TaskEvent) bool { return strings.HasPrefix(ev.GetText(), "going out: ") }

// TestIfritWarnsThenGoesOut: left alone, an ifrit is warned of 25 minutes after it was lit, once, then goes out at
// 30: stopped, out and why, its slot given back, its worktree, branch and session kept; the lead hears of it.
func TestIfritWarnsThenGoesOut(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	l := lightIfrit(t, t.TempDir(), "text lit")
	l.next("it listens", listens)
	l.outAt(t, 30*time.Minute)

	l.at(t, 24*time.Minute)
	l.at(t, 25*time.Minute)
	l.next("the warning", goesOutAt)
	warned := <-l.warned
	if !warned.GetIfrit().GetWarnTime().AsTime().Equal(l.start.Add(25*time.Minute)) ||
		!warned.GetIfrit().GetOutTime().AsTime().Equal(l.start.Add(30*time.Minute)) {
		t.Errorf("warned with %v, want warned at 10:25, out at 10:30", warned.GetIfrit())
	}
	if got := l.e.get(t, l.id); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING || ifritState(got) != planv1.IfritState_IFRIT_STATE_LISTENING {
		t.Errorf("warned: %v %v, want it still lit", got.GetStatus(), ifritState(got))
	}
	l.at(t, 27*time.Minute) // Warned already: no second warning.

	l.at(t, 30*time.Minute)
	l.next("it goes out", goingOut)
	out := l.e.ended(t, l.id)
	why := "out: you left Djinn alone at " + ClockText(l.start, l.clock.now())
	if out.GetStatus() != planv1.TaskStatus_TASK_STATUS_STOPPED || ifritState(out) != planv1.IfritState_IFRIT_STATE_OUT || out.GetError() != why {
		t.Errorf("out: %v %v (%s), want stopped, out (%s)", out.GetStatus(), ifritState(out), out.GetError(), why)
	}
	if out.GetIfrit().GetOutTime() != nil || out.GetIfrit().GetWarnTime() != nil {
		t.Errorf("out, its timers stay: %v", out.GetIfrit())
	}
	if _, err := os.Stat(out.GetWorktree()); err != nil || out.GetBranch() == "" || out.GetSessionId() == "" {
		t.Errorf("out: worktree %q (%v), branch %q, session %q: want all kept", out.GetWorktree(), err, out.GetBranch(), out.GetSessionId())
	}
	if l.e.h.Works(l.id) || l.e.h.Running() != 0 {
		t.Errorf("out, it still takes a slot: %d running", l.e.h.Running())
	}
	if n := len(l.warned); n != 0 {
		t.Errorf("%d warnings more, want one in all", n)
	}
	var told []string
	for _, ev := range l.lead.all() {
		if ev.GetKind() == planv1.LeadEventKind_LEAD_EVENT_KIND_OUT && ev.GetCode() == out.GetCode() && ev.GetWishId() == l.wishID {
			told = append(told, ev.GetText())
		}
	}
	if want := out.GetCode() + " (ifrit) is " + why + "; a message lights it again (djinn task send " + out.GetCode() + ")"; len(told) != 1 || told[0] != want {
		t.Errorf("the lead was told %q, want %q", told, want)
	}
}

// TestYourActivityKeepsItLit: each action of the developer pushes the ifrit's out time back, and one after the
// warning clears it, as Keep lit does.
func TestYourActivityKeepsItLit(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	l := lightIfrit(t, t.TempDir(), "text lit")
	l.next("it listens", listens)
	touching := httptest.NewServer(Touching(l.e.srv.Config.Handler, l.e.h.Presence()))
	t.Cleanup(touching.Close)

	// A djinn command typed in a shell, at 10:20: it goes out at 10:50 now.
	l.at(t, 20*time.Minute)
	if _, err := tasksAs(touching, CallerYou).List(t.Context(), connect.NewRequest(&planv1.TaskServiceListRequest{WishId: l.wishID})); err != nil {
		t.Fatal(err)
	}
	if !l.e.h.Presence().Last().Equal(l.start.Add(20 * time.Minute)) {
		t.Fatalf("your command at 10:20: presence %v", l.e.h.Presence().Last())
	}
	l.outAt(t, 50*time.Minute)
	l.at(t, 44*time.Minute)
	l.at(t, 45*time.Minute)
	warning := l.next("the warning", goesOutAt)
	if ev := <-l.warned; !ev.GetIfrit().GetOutTime().AsTime().Equal(l.start.Add(50 * time.Minute)) {
		t.Errorf("warned it goes out at %v, want 10:50", ev.GetIfrit().GetOutTime().AsTime())
	}
	if !warning.GetIfrit().GetWarnTime().AsTime().Equal(l.start.Add(45 * time.Minute)) {
		t.Errorf("warned at %v, want 10:45", warning.GetIfrit().GetWarnTime())
	}

	// Keep lit, at 10:46: the warning goes, it goes out at 11:16.
	l.clock.set(l.start.Add(46 * time.Minute))
	l.e.h.Presence().Touch()
	kept := l.next("kept lit", func(ev *planv1.TaskEvent) bool { return strings.HasPrefix(ev.GetText(), "kept lit: you came back") })
	if kept.GetIfrit().GetWarnTime() != nil || !kept.GetIfrit().GetOutTime().AsTime().Equal(l.start.Add(76*time.Minute)) {
		t.Errorf("kept lit: %v, want no warning, out at 11:16", kept.GetIfrit())
	}

	// A key in a terminal of the window, at 11:10: it goes out at 11:40.
	l.clock.set(l.start.Add(70 * time.Minute))
	key, err := http.NewRequestWithContext(t.Context(), http.MethodPost, touching.URL+terminalv1connect.TerminalServiceWriteProcedure, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := touching.Client().Do(key)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	l.outAt(t, 100*time.Minute)
	l.at(t, 90*time.Minute)
	if got := l.e.get(t, l.id); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING || ifritState(got) != planv1.IfritState_IFRIT_STATE_LISTENING {
		t.Errorf("at 11:30, 20 min after your last key: %v %v, want it lit", got.GetStatus(), ifritState(got))
	}
	l.e.done(t, l.id, "")
	if got := l.e.ended(t, l.id); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Errorf("ended %v (%s), want done", got.GetStatus(), got.GetError())
	}
	if n := len(l.warned); n != 0 {
		t.Errorf("%d warnings more, want one in all", n)
	}
}

// TestLeadAndWorkersAreNoActivity: what the lead, the workers, Djinn's own commands and the window that only reads
// do is no presence: the ifrit the lead talks to, a worker working beside it, goes out on time all the same.
func TestLeadAndWorkersAreNoActivity(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	l := lightIfrit(t, t.TempDir(), "text lit")
	l.next("it listens", listens)
	presence := l.e.h.Presence()
	none := []*http.Request{
		httptest.NewRequest(http.MethodPost, planv1connect.TaskServiceSendProcedure, nil),          // the lead, below
		httptest.NewRequest(http.MethodPost, planv1connect.QuestionServiceAnswerProcedure, nil),    // Djinn's own command
		httptest.NewRequest(http.MethodPost, planv1connect.MarkServicePutProcedure, nil),           // a worker
		httptest.NewRequest(http.MethodPost, planv1connect.TaskServiceListProcedure, nil),          // the window reads
		httptest.NewRequest(http.MethodPost, planv1connect.LeadServiceWatchProcedure, nil),         // the window follows
		httptest.NewRequest(http.MethodPost, terminalv1connect.TerminalServiceWriteProcedure, nil), // a worker's token
	}
	none[0].Header.Set(CallerHeader, CallerLead)
	none[1].Header.Set(CallerHeader, CallerDjinn)
	none[2] = none[2].WithContext(plan.WithCaller(none[2].Context(), &plan.WorkerCaller{TaskID: store.NewID()}))
	none[5] = none[5].WithContext(plan.WithCaller(none[5].Context(), &plan.WorkerCaller{TaskID: store.NewID()}))
	handler := Touching(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), presence)
	for _, r := range none {
		handler.ServeHTTP(httptest.NewRecorder(), r)
		if !presence.Last().IsZero() {
			t.Fatalf("%s (%s) is taken for you", r.URL.Path, r.Header.Get(CallerHeader))
		}
	}

	// The lead relays a message at 10:10, a worker works beside the ifrit: neither keeps it lit.
	touching := httptest.NewServer(Touching(l.e.srv.Config.Handler, presence))
	t.Cleanup(touching.Close)
	l.clock.set(l.start.Add(10 * time.Minute))
	if _, err := tasksAs(touching, CallerLead).Send(t.Context(), connect.NewRequest(&planv1.TaskServiceSendRequest{
		TaskId: l.id, Text: "and the margin",
	})); err != nil {
		t.Fatal(err)
	}
	l.next("it answers the lead", said("received: and the margin"))
	l.next("it listens again", listens)
	worker := l.e.mustSpawn(t, l.wishID, "Beside it", "text busy\nwait", nil)
	l.e.follow(t, worker.GetId())("the worker works", said("busy"))
	if !presence.Last().IsZero() {
		t.Fatalf("the lead's message or a worker is taken for you: %v", presence.Last())
	}
	l.outAt(t, 30*time.Minute)
	l.at(t, 25*time.Minute)
	l.next("the warning", goesOutAt)
	l.at(t, 30*time.Minute)
	if out := l.e.ended(t, l.id); ifritState(out) != planv1.IfritState_IFRIT_STATE_OUT {
		t.Errorf("the lead and a worker at work beside it: %v %v (%s), want out at 10:30", out.GetStatus(), ifritState(out), out.GetError())
	}
	l.e.release(t, worker.GetId())
	l.e.ended(t, worker.GetId())

	// A window's answer, or a djinn command of yours, is you.
	for _, who := range []string{"", CallerYou} {
		r := httptest.NewRequest(http.MethodPost, planv1connect.QuestionServiceAnswerProcedure, nil)
		if who != "" {
			r = httptest.NewRequest(http.MethodPost, planv1connect.TaskServiceListProcedure, nil)
			r.Header.Set(CallerHeader, who)
		}
		l.clock.set(l.clock.now().Add(time.Minute))
		handler.ServeHTTP(httptest.NewRecorder(), r)
		if !presence.Last().Equal(l.clock.now()) {
			t.Errorf("%s (%q) is not taken for you", r.URL.Path, who)
		}
	}
}

// TestIfritOutLetsTheTurnEnd: past its out time in the middle of a turn, an ifrit is warned, and works on; it goes out
// once its turn ended.
func TestIfritOutLetsTheTurnEnd(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	l := lightIfrit(t, t.TempDir(), "text working\nwait")
	l.next("it works", said("working"))

	l.at(t, 31*time.Minute)
	l.next("the warning", goesOutAt)
	<-l.warned
	if got := l.e.get(t, l.id); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING || ifritState(got) != planv1.IfritState_IFRIT_STATE_WORKING {
		t.Fatalf("past its out time at work: %v %v, want it working on", got.GetStatus(), ifritState(got))
	}
	l.at(t, 32*time.Minute)

	l.e.send(t, l.id, "the end of it")
	l.next("its turn ends", said("received: the end of it"))
	l.next("it listens", listens)
	l.next("it goes out", goingOut)
	if out := l.e.ended(t, l.id); out.GetStatus() != planv1.TaskStatus_TASK_STATUS_STOPPED || ifritState(out) != planv1.IfritState_IFRIT_STATE_OUT {
		t.Errorf("after its turn: %v %v (%s), want stopped, out", out.GetStatus(), ifritState(out), out.GetError())
	}
}

// TestSendRelightsAnIfrit: djinn task send to an out ifrit lights it again: its worker resumes its session in its
// worktree at once, on the message, told how long it was out, and listens again.
func TestSendRelightsAnIfrit(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	l := lightIfrit(t, t.TempDir(), "text lit")
	l.next("it listens", listens)
	l.at(t, 25*time.Minute)
	l.next("the warning", goesOutAt)
	l.at(t, 30*time.Minute)
	out := l.e.ended(t, l.id)
	if ifritState(out) != planv1.IfritState_IFRIT_STATE_OUT {
		t.Fatalf("not out: %v %v (%s)", out.GetStatus(), ifritState(out), out.GetError())
	}

	l.clock.set(l.start.Add(70 * time.Minute))
	res, err := l.e.tasks.Send(t.Context(), connect.NewRequest(&planv1.TaskServiceSendRequest{TaskId: l.id, Text: "text relit"}))
	if err != nil {
		t.Fatal(err)
	}
	if ev := res.Msg.GetEvent(); ev.GetKind() != planv1.TaskEventKind_TASK_EVENT_KIND_PROMPT ||
		ev.GetText() != "You were out for 40 min; Djinn lit you again for this message.\n\ntext relit" {
		t.Errorf("sent: %v, want the message as its prompt, told it was out 40 min", ev)
	}
	next := l.e.follow(t, l.id)
	why := "lit again by a message, was " + out.GetError() + ": You were out for 40 min; Djinn lit you again for this message."
	next("it is lit again", func(ev *planv1.TaskEvent) bool { return ev.GetText() == why })
	next("it resumes its session", func(ev *planv1.TaskEvent) bool { return ev.GetText() == "fake session "+l.id+", resumed" })
	next("it says the message", said("relit"))
	again := next("it listens again", listens)
	if again.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING || ifritState(again) != planv1.IfritState_IFRIT_STATE_LISTENING ||
		again.GetWorktree() != out.GetWorktree() || again.GetSessionId() != out.GetSessionId() || len(again.GetRuns()) != 2 {
		t.Errorf("lit again: %v %v, worktree %q, session %q, %d runs; want it listening, in its worktree and session, 2 runs",
			again.GetStatus(), ifritState(again), again.GetWorktree(), again.GetSessionId(), len(again.GetRuns()))
	}
	l.e.done(t, l.id, "")
	if got := l.e.ended(t, l.id); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Errorf("ended %v (%s), want done", got.GetStatus(), got.GetError())
	}
}

// TestIfritOutAfterARestart: Djinn stops while an ifrit is lit; it comes back out, not resumed, and the restart
// line says so. A message lights it again.
func TestIfritOutAfterARestart(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	home := t.TempDir()
	l := lightIfrit(t, home, "text lit")
	l.next("it listens", listens)
	l.e.down()

	e := up(t, home, WithClock(l.clock.now), WithTick(time.Hour), WithSilenceLimit(4*time.Hour), WithVersion("v9.9.9"))
	out := e.get(t, l.id)
	if out.GetStatus() != planv1.TaskStatus_TASK_STATUS_STOPPED || ifritState(out) != planv1.IfritState_IFRIT_STATE_OUT ||
		out.GetError() != whyRestartOut || e.h.Works(l.id) {
		t.Fatalf("after the restart: %v %v (%s), works %v; want stopped, out, not resumed",
			out.GetStatus(), ifritState(out), out.GetError(), e.h.Works(l.id))
	}
	events, err := store.List[*planv1.LeadEvent](t.Context(), e.db, nil)
	if err != nil {
		t.Fatal(err)
	}
	var line string
	for _, ev := range events {
		if ev.GetKind() == planv1.LeadEventKind_LEAD_EVENT_KIND_RESTART && strings.Contains(ev.GetText(), "v9.9.9") {
			line = ev.GetText()
		}
	}
	if want := "Djinn: restarted on version v9.9.9: " + out.GetCode() + " (ifrit) is out: a message lights it again."; line != want {
		t.Errorf("restart line %q, want %q", line, want)
	}
	e.h.schedule(t.Context())
	if got := e.get(t, l.id); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_STOPPED {
		t.Errorf("after a pass: %v, want it out still: no turn paid for nothing", got.GetStatus())
	}

	e.send(t, l.id, "text back")
	next := e.follow(t, l.id)
	next("it says the message", said("back"))
	next("it listens", listens)
	e.done(t, l.id, "")
	e.ended(t, l.id)
}

// tasksAs is a task client of srv whose requests say who makes them, as djinn's command line does.
func tasksAs(srv *httptest.Server, who string) planv1connect.TaskServiceClient {
	client := &http.Client{Transport: callerTransport{who, srv.Client().Transport}}
	return planv1connect.NewTaskServiceClient(client, srv.URL)
}

type callerTransport struct {
	who  string
	base http.RoundTripper
}

func (c callerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set(CallerHeader, c.who)
	return c.base.RoundTrip(r)
}

// ifritFor polls what the gates learn of the ifrit (Ifrit) until ok holds, and says it.
func (l *lit) ifritFor(t *testing.T, what string, ok func(lit bool, out time.Time, why string) bool) (bool, time.Time, string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		lit, out, why := l.e.h.Ifrit(t.Context(), l.id)
		if ok(lit, out, why) {
			return lit, out, why
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: lit %v, out at %v, why %q", what, lit, out, why)
		}
	}
}

// TestIfritKeepsItsGate: the gates hold a lit ifrit's gate until its out time, its turn under way or not, the
// developer's actions pushing it back (Q11). A task that is no ifrit holds its gates as any.
func TestIfritKeepsItsGate(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	l := lightIfrit(t, t.TempDir(), "text working\nwait")
	l.next("it works", said("working"))
	at := func(d time.Duration) func(bool, time.Time, string) bool {
		return func(lit bool, out time.Time, why string) bool { return lit && out.Equal(l.start.Add(d)) && why == "" }
	}
	l.ifritFor(t, "at work, lit at 10:00", at(30*time.Minute))

	l.e.send(t, l.id, "the end of it")
	l.next("it listens", listens)
	l.at(t, 20*time.Minute)
	l.e.h.Presence().Touch()
	l.ifritFor(t, "listening, you came back at 10:20", at(50*time.Minute))
	l.at(t, 40*time.Minute)
	l.ifritFor(t, "at 10:40, left alone since 10:20", at(50*time.Minute))

	if lit, out, why := l.e.h.Ifrit(t.Context(), "01a144ba-0000-7000-8000-000000000000"); lit || !out.IsZero() || why != "" {
		t.Errorf("a task that is no ifrit: lit %v, out at %v, why %q", lit, out, why)
	}
	l.e.done(t, l.id, "")
	l.e.ended(t, l.id)
}

// TestIfritGateGoesBackWhenItGoesOut: the gates take a lit ifrit's gate back once it goes out, and say why, whether its
// run is over or not; lit again, its gates are held again until it ends.
func TestIfritGateGoesBackWhenItGoesOut(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	l := lightIfrit(t, t.TempDir(), "text lit")
	l.next("it listens", listens)
	l.at(t, 25*time.Minute)
	l.next("the warning", goesOutAt)
	l.ifritFor(t, "warned", func(lit bool, out time.Time, _ string) bool { return lit && out.Equal(l.start.Add(30*time.Minute)) })

	l.at(t, 30*time.Minute)
	why := "went out: you left Djinn alone at " + ClockText(l.start, l.clock.now())
	l.ifritFor(t, "going out", func(lit bool, _ time.Time, got string) bool { return !lit && got == why })
	l.e.ended(t, l.id)
	if lit, _, got := l.e.h.Ifrit(t.Context(), l.id); lit || got != why {
		t.Errorf("out, its run over: lit %v, why %q, want %q", lit, got, why)
	}

	l.clock.set(l.start.Add(40 * time.Minute))
	l.e.send(t, l.id, "text relit")
	l.e.follow(t, l.id)("it listens again", listens)
	l.ifritFor(t, "lit again at 10:40", func(lit bool, out time.Time, _ string) bool { return lit && out.Equal(l.start.Add(70*time.Minute)) })
	l.e.done(t, l.id, "")
	l.e.ended(t, l.id)
	if lit, _, got := l.e.h.Ifrit(t.Context(), l.id); lit || got != "ended" {
		t.Errorf("done: lit %v, why %q, want ended", lit, got)
	}
}

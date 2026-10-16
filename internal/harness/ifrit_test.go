package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/testx"
)

// listens is the event where the worker of an ifrit says its turn ended and it listens.
func listens(ev *planv1.TaskEvent) bool {
	return ev.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_STATUS && strings.Contains(ev.GetText(), " listens: its turn ended")
}

// ifritState is where the ifrit stands, as the task says.
func ifritState(t *planv1.Task) planv1.IfritState { return t.GetIfrit().GetState() }

// send gives the running task a message.
func (e *env) send(t *testing.T, id, text string) {
	t.Helper()
	if _, err := e.tasks.Send(t.Context(), connect.NewRequest(&planv1.TaskServiceSendRequest{TaskId: id, Text: text})); err != nil {
		t.Fatal(err)
	}
}

// done ends the task by hand, as djinn task done does, and returns it as Done gave it.
func (e *env) done(t *testing.T, id, note string) *planv1.Task {
	t.Helper()
	res, err := e.tasks.Done(t.Context(), connect.NewRequest(&planv1.TaskServiceDoneRequest{TaskId: id, Note: note}))
	if err != nil {
		t.Fatal(err)
	}
	return res.Msg.GetTask()
}

// TestIfritListensBetweenMessages: claude's input stays open once an ifrit's turn has its result (the two-turn
// fixture): the ifrit listens, its process alive, until a message starts its next turn; the time it listened is
// kept. djinn task done closes its input: claude ends, and the task is done like any.
func TestIfritListensBetweenMessages(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	clock := &fakeClock{at: time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)}
	env, _, input := fake{provider: "claude", fixture: "two-turns"}.env(t)
	providers := testProviders()
	providers[planv1.Provider_PROVIDER_CLAUDE] = envProvider{Claude{Command: os.Args[0]}, env}
	e := upWith(t, t.TempDir(), providers, WithClock(clock.now), WithTick(time.Hour))
	wishID, _ := e.wish(t, gitRepo(t))

	ifrit := e.mustSpawn(t, wishID, "Front of the lamp", "Make the button primary.", &planv1.TaskServiceSpawnRequest{
		Kind: planv1.TaskKind_TASK_KIND_IFRIT, Provider: planv1.Provider_PROVIDER_CLAUDE,
	})
	id := ifrit.GetId()
	if ifrit.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING || ifritState(ifrit) != planv1.IfritState_IFRIT_STATE_WORKING {
		t.Fatalf("spawned: %v %v, want running, working", ifrit.GetStatus(), ifritState(ifrit))
	}
	next := e.follow(t, id)
	lit := next("its first turn ended", listens)
	if lit.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING || ifritState(lit) != planv1.IfritState_IFRIT_STATE_LISTENING {
		t.Fatalf("after its first answer: %v %v, want running, listening", lit.GetStatus(), ifritState(lit))
	}
	if e.h.Works(id) {
		t.Errorf("the listening ifrit works")
	}

	clock.set(clock.now().Add(20 * time.Minute))
	e.send(t, id, "And then?")
	working := next("a message came", func(ev *planv1.TaskEvent) bool { return strings.HasPrefix(ev.GetText(), "a message came: claude") })
	// The fake answers at once: read after the event, the task may have ended its second turn and listen again. The
	// time it listened before the message, counted when the message started a turn, says it worked either way.
	if s := ifritState(working); s != planv1.IfritState_IFRIT_STATE_WORKING && s != planv1.IfritState_IFRIT_STATE_LISTENING ||
		working.GetIfrit().GetListened().AsDuration() != 20*time.Minute {
		t.Errorf("on the message: %v, listened %v, want working (or listening again), 20m", s, working.GetIfrit().GetListened().AsDuration())
	}
	next("its second answer", func(ev *planv1.TaskEvent) bool { return ev.GetText() == "Second answer." })
	again := next("its second turn ended", listens)
	if ifritState(again) != planv1.IfritState_IFRIT_STATE_LISTENING || again.GetIfrit().GetListened().AsDuration() != 20*time.Minute {
		t.Errorf("after its second answer: %v, listened %v, want listening, 20m", ifritState(again), again.GetIfrit().GetListened().AsDuration())
	}

	if got := e.done(t, id, "looks right"); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
		t.Errorf("djinn task done gave %v, want it running till claude ends", got.GetStatus())
	}
	ended := e.ended(t, id)
	if ended.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE || ended.GetError() != "" {
		t.Errorf("ended %v (%s), want done", ended.GetStatus(), ended.GetError())
	}
	if ifritState(ended) != planv1.IfritState_IFRIT_STATE_UNSPECIFIED || ended.GetIfrit().GetListened().AsDuration() != 20*time.Minute {
		t.Errorf("ended as %v, listened %v, want no state, 20m", ifritState(ended), ended.GetIfrit().GetListened().AsDuration())
	}
	if runs := ended.GetRuns(); len(runs) != 1 {
		t.Errorf("runs = %v, want one: it listened, it never ended between its turns", runs)
	}
	b, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(b), `"type":"user"`); n != 2 || !strings.Contains(string(b), "And then?") {
		t.Errorf("claude read %q, want both messages", b)
	}
	var closing bool
	for _, ev := range e.watch(t.Context(), t, id, 0) {
		closing = closing || strings.HasPrefix(ev.GetText(), "ended by the lead: its input closes once its turn ends, and its work integrates: looks right")
	}
	if !closing {
		t.Errorf("no event says the lead ended it")
	}
}

// TestIfritTakesNoSlotWhileListening: an ifrit starts at once on a full machine, counted among the workers started by
// hand, and no preemption picks it; once it listens it takes no slot: a work task takes the free slot, and an ifrit
// that works takes one again.
func TestIfritTakesNoSlotWhileListening(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	l := &limit{slots: 1}
	e := up(t, t.TempDir(), WithCapacity(l.capacity), WithTick(time.Hour))
	wishID, _ := e.wish(t, gitRepo(t))
	busy := e.mustSpawn(t, wishID, "Busy", "wait", nil)

	ifrit := e.mustSpawn(t, wishID, "Front of the lamp", "text lit\nwait", &planv1.TaskServiceSpawnRequest{
		Kind: planv1.TaskKind_TASK_KIND_IFRIT,
	})
	id := ifrit.GetId()
	if ifrit.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
		t.Fatalf("an ifrit on a full machine: %v (%s), want it running at once", ifrit.GetStatus(), ifrit.GetWaitReason())
	}
	next := e.follow(t, id)
	next("it works", said("lit"))
	if running, forced := e.h.Running(), e.h.Forced(); running != 2 || forced != 1 || !e.h.Works(id) {
		t.Errorf("the ifrit at work: %d running, %d forced, works %v; want 2, 1, true", running, forced, e.h.Works(id))
	}
	if picked, _ := e.h.lowestWorkTask(t.Context(), true); picked.GetId() == id {
		t.Errorf("the ifrit at work would be paused for an investigator")
	}
	waits := e.mustSpawn(t, wishID, "Waits", "text one", nil)
	if waits.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING ||
		waits.GetWaitReason() != "2 workers run, the most this machine holds (set by the test)" {
		t.Errorf("beside the ifrit at work: %v (%s)", waits.GetStatus(), waits.GetWaitReason())
	}

	e.send(t, id, "go on")
	lit := next("it listens", listens)
	if ifritState(lit) != planv1.IfritState_IFRIT_STATE_LISTENING {
		t.Fatalf("after its turn: %v, want listening", ifritState(lit))
	}
	if running, forced := e.h.Running(), e.h.Forced(); running != 1 || forced != 0 || e.h.Works(id) {
		t.Errorf("the ifrit listening: %d running, %d forced, works %v; want 1, 0, false", running, forced, e.h.Works(id))
	}
	if picked, _ := e.h.lowestWorkTask(t.Context(), true); picked.GetId() != busy.GetId() {
		t.Errorf("preemption picks %v, want %s", picked.GetCode(), busy.GetCode())
	}

	// The busy task ends: the one waiting takes its slot, then a new one, the ifrit listening beside them.
	e.release(t, busy.GetId())
	e.ended(t, busy.GetId())
	if got := e.ended(t, waits.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Errorf("the task waiting for a slot: %v (%s), want done", got.GetStatus(), got.GetError())
	}
	free := e.mustSpawn(t, wishID, "Free", "wait", nil)
	if free.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
		t.Errorf("beside the listening ifrit, one slot: %v (%s), want running", free.GetStatus(), free.GetWaitReason())
	}

	e.send(t, id, "once more")
	next("it works again", func(ev *planv1.TaskEvent) bool { return strings.HasPrefix(ev.GetText(), "a message came: fake") })
	next("it listens again", listens)
	e.done(t, id, "")
	if got := e.ended(t, id); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Errorf("ended %v (%s), want done", got.GetStatus(), got.GetError())
	}
	e.release(t, free.GetId())
	e.ended(t, free.GetId())
}

// TestLamplighterLeavesAListeningIfrit: a listening ifrit is never silent too long, and its duration budget counts its
// turns only: hours of listening stop nothing (the developer away less than its 4 hours of idle delay). While it
// works, its silence counts as any worker's.
func TestLamplighterLeavesAListeningIfrit(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	clock := &fakeClock{at: time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)}
	e := up(t, t.TempDir(), WithClock(clock.now), WithTick(time.Hour), WithSilenceLimit(15*time.Minute),
		WithTaskMaxWallTime(time.Hour))
	wishID, _ := e.wish(t, gitRepo(t))
	spawn := func(prompt string) string {
		return e.mustSpawn(t, wishID, "Front of the lamp", prompt, &planv1.TaskServiceSpawnRequest{
			Kind: planv1.TaskKind_TASK_KIND_IFRIT, IdleMinutes: 240,
		}).GetId()
	}

	quiet := spawn("text lit")
	next := e.follow(t, quiet)
	next("it listens", listens)
	clock.set(clock.now().Add(3 * time.Hour))
	e.h.schedule(t.Context())
	if got := e.get(t, quiet); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING ||
		ifritState(got) != planv1.IfritState_IFRIT_STATE_LISTENING {
		t.Fatalf("after 3 hours listening: %v %v (%s), want running, listening", got.GetStatus(), ifritState(got), got.GetError())
	}
	e.send(t, quiet, "back")
	next("it answers", said("received: back"))
	back := next("it listens again", listens)
	if d := back.GetIfrit().GetListened().AsDuration(); d != 3*time.Hour {
		t.Errorf("listened %v, want 3h", d)
	}
	e.h.schedule(t.Context())
	if got := e.get(t, quiet); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
		t.Fatalf("its turns took no time, it listened 3 hours: %v (%s), want running", got.GetStatus(), got.GetError())
	}

	// At work, silent past the limit: the Lamplighter wakes it, as any worker.
	busy := spawn("text busy\nwait")
	follow := e.follow(t, busy)
	follow("it works", said("busy"))
	clock.set(clock.now().Add(20 * time.Minute))
	e.h.schedule(t.Context())
	follow("the Lamplighter ends its turn", func(ev *planv1.TaskEvent) bool { return strings.Contains(ev.GetText(), "silent for 20 min") })
	if got := e.get(t, quiet); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
		t.Errorf("the listening ifrit beside it: %v (%s), want running", got.GetStatus(), got.GetError())
	}
	for _, id := range []string{quiet, busy} {
		e.until(t, id, func(task *planv1.Task) bool { return ifritState(task) == planv1.IfritState_IFRIT_STATE_LISTENING })
		e.done(t, id, "")
		e.ended(t, id)
	}
}

// TestSpawnIfrit: djinn task spawn --kind ifrit stores an ifrit, with its own idle minutes when given, and its run
// keeps them; --idle-minutes is for an ifrit only, and an ifrit is an agent in a project (T51).
func TestSpawnIfrit(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	e := up(t, t.TempDir(), WithTick(time.Hour))
	wishID, _ := e.wish(t, gitRepo(t))

	task := e.mustSpawn(t, wishID, "Front of the lamp", "text listening", &planv1.TaskServiceSpawnRequest{
		Kind: planv1.TaskKind_TASK_KIND_IFRIT, IdleMinutes: 60,
	})
	if task.GetKind() != planv1.TaskKind_TASK_KIND_IFRIT || task.GetIfrit().GetIdleMinutes() != 60 {
		t.Errorf("spawned %v %v, want an ifrit of 60 minutes", task.GetKind(), task.GetIfrit())
	}
	e.follow(t, task.GetId())("it listens", listens)
	e.done(t, task.GetId(), "")
	if got := e.ended(t, task.GetId()); got.GetKind() != planv1.TaskKind_TASK_KIND_IFRIT || got.GetIfrit().GetIdleMinutes() != 60 {
		t.Errorf("after its run: %v %v, want the ifrit of 60 minutes still", got.GetKind(), got.GetIfrit())
	}
	plain := e.mustSpawn(t, wishID, "Default pace", "text hi", &planv1.TaskServiceSpawnRequest{Kind: planv1.TaskKind_TASK_KIND_IFRIT})
	if plain.GetIfrit() == nil || plain.GetIfrit().GetIdleMinutes() != 0 {
		t.Errorf("an ifrit without --idle-minutes: %v, want one at this machine's default (0)", plain.GetIfrit())
	}
	e.follow(t, plain.GetId())("it listens", listens)
	e.done(t, plain.GetId(), "")
	e.ended(t, plain.GetId())
	work := e.mustSpawn(t, wishID, "Work", "text hi", nil)
	if work.GetIfrit() != nil || work.GetKind() == planv1.TaskKind_TASK_KIND_IFRIT {
		t.Errorf("a work task is an ifrit: %v %v", work.GetKind(), work.GetIfrit())
	}
	e.ended(t, work.GetId())

	refused := map[string]*planv1.TaskServiceSpawnRequest{
		"--idle-minutes is for an ifrit": {IdleMinutes: 30},
		"an ifrit is an agent":           {Kind: planv1.TaskKind_TASK_KIND_IFRIT, Provider: planv1.Provider_PROVIDER_WATCH},
	}
	for want, req := range refused {
		if _, err := e.spawnReq(t, wishID, "Refused", "text hi", req); connect.CodeOf(err) != connect.CodeInvalidArgument ||
			!strings.Contains(err.Error(), want) {
			t.Errorf("spawn %v: %v, want invalid_argument: %s", req, err, want)
		}
	}
	bare, err := e.wishes.Make(t.Context(), connect.NewRequest(&planv1.WishServiceMakeRequest{Title: "Nowhere"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.spawnReq(t, bare.Msg.GetWish().GetId(), "Ifrit", "text hi", &planv1.TaskServiceSpawnRequest{
		Kind: planv1.TaskKind_TASK_KIND_IFRIT,
	}); connect.CodeOf(err) != connect.CodeInvalidArgument || !strings.Contains(err.Error(), "an ifrit works in a project") {
		t.Errorf("an ifrit outside any project: %v", err)
	}
}

// TestIfritIdle: how long an ifrit stays lit without the developer is its own, else djinn up --ifrit-idle, else the
// window's settings, else 30 minutes.
func TestIfritIdle(t *testing.T) {
	home := t.TempDir()
	own := &planv1.Task{Kind: planv1.TaskKind_TASK_KIND_IFRIT, Ifrit: &planv1.TaskIfrit{IdleMinutes: 60}}
	plain := &planv1.Task{Kind: planv1.TaskKind_TASK_KIND_IFRIT, Ifrit: &planv1.TaskIfrit{}}
	h := &Harness{home: home}
	if got := h.IfritIdle(plain); got != DefaultIfritIdle {
		t.Errorf("no setting: %v, want %v", got, DefaultIfritIdle)
	}
	if err := os.WriteFile(filepath.Join(home, "settings.json"), []byte(`{"ifritIdleMinutes": 20}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := h.IfritIdle(plain); got != 20*time.Minute {
		t.Errorf("the window's settings: %v, want 20m", got)
	}
	WithIfritIdle(45 * time.Minute)(h)
	if got := h.IfritIdle(plain); got != 45*time.Minute {
		t.Errorf("djinn up --ifrit-idle: %v, want 45m", got)
	}
	if got := h.IfritIdle(own); got != time.Hour {
		t.Errorf("its own: %v, want 1h", got)
	}
}

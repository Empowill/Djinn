//go:build !windows

package harness

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// gateBook is a TakeGate that grants every gate at once and keeps what is held.
type gateBook struct {
	mu    sync.Mutex
	held  map[string]string // by gate: what holds it
	given []string
}

func (g *gateBook) take(_ context.Context, name, _, what, _ string, _ bool, _ func(string)) (func(), error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.held == nil {
		g.held = map[string]string{}
	}
	g.held[name] = what
	return func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		delete(g.held, name)
		g.given = append(g.given, name)
	}, nil
}

func (g *gateBook) holds(name string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.held[name]
	return ok
}

// ifritLit spawns an ifrit in the wish and waits until it listens.
func (e *env) ifritLit(t *testing.T, wishID string) *planv1.Task {
	t.Helper()
	ifrit := e.mustSpawn(t, wishID, "Front of the lamp", "text lit", &planv1.TaskServiceSpawnRequest{Kind: planv1.TaskKind_TASK_KIND_IFRIT})
	e.follow(t, ifrit.GetId())("it listens", listens)
	return ifrit
}

// asWorker is ctx as the worker of the task id of the wish wishID calls Djinn.
func asWorker(ctx context.Context, id, wishID string) context.Context {
	return plan.WithCaller(ctx, &plan.WorkerCaller{TaskID: id, WishID: wishID})
}

// serve runs a serve for the ifrit, as its worker would.
func (e *env) serve(t *testing.T, ifrit *planv1.Task, req *planv1.ServeServiceRunRequest) (*planv1.Serve, error) {
	t.Helper()
	req.TaskId = ifrit.GetId()
	res, err := (&Serves{h: e.h}).Run(asWorker(t.Context(), ifrit.GetId(), ifrit.GetWishId()), connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return res.Msg.GetServe(), nil
}

func (e *env) mustServe(t *testing.T, ifrit *planv1.Task, req *planv1.ServeServiceRunRequest) *planv1.Serve {
	t.Helper()
	sv, err := e.serve(t, ifrit, req)
	if err != nil {
		t.Fatal(err)
	}
	return sv
}

// storedServe is the serve name of the task id, as stored, once ok says so of it.
func (e *env) storedServe(t *testing.T, id, name string, ok func(*planv1.Serve) bool) *planv1.Serve {
	t.Helper()
	var sv *planv1.Serve
	waitFor(t, "serve "+name+" stored", func() bool {
		serves := e.get(t, id).GetIfrit().GetServes()
		if at := serveAt(serves, name); at >= 0 {
			sv = serves[at]
			return ok(sv)
		}
		return false
	})
	return sv
}

// seen are the events of the running task id after after, as stored by now: its watch follows it while it runs.
func (e *env) seen(t *testing.T, id string, after int64) []*planv1.TaskEvent {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	return e.watch(ctx, t, id, after)
}

func serveState(s planv1.ServeState) func(*planv1.Serve) bool {
	return func(sv *planv1.Serve) bool { return sv.GetState() == s }
}

// alive tells whether pid runs, a zombie aside.
func alive(pid int) bool {
	if pid <= 0 || syscall.Kill(pid, 0) != nil {
		return false
	}
	_, err := procStart(pid)
	return err == nil
}

// listenLocal listens on a free port of 127.0.0.1 until the test ends, as a container Docker publishes would: no
// process of a serve holds it.
func listenLocal(t *testing.T) uint32 {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	return uint32(ln.Addr().(*net.TCPAddr).Port)
}

// TestServeRunsAndStops (L06 section 5): djinn serve run starts a serve in the ifrit's worktree, holding its gate, and
// returns once its process lives and the port it declares answers; djinn serve list and logs show it. djinn serve
// stop runs its stop command, then ends its processes and gives its gate back, for good: it would not start again
// with its ifrit. The port it declared, a container's that outlives it, still answers: its event says so, and the
// lead hears of it.
func TestServeRunsAndStops(t *testing.T) {
	t.Parallel()
	gates, queue := &gateBook{}, &leadQueue{}
	e := up(t, t.TempDir(), WithTick(time.Hour), WithGates(gates.take))
	e.h.NotifyLead(queue.notify)
	wishID, _ := e.wish(t, gitRepo(t))
	ifrit := e.ifritLit(t, wishID)
	port := listenLocal(t)
	stopped := filepath.Join(t.TempDir(), "stopped")

	sv := e.mustServe(t, ifrit, &planv1.ServeServiceRunRequest{
		Name: "back", Command: "pwd; echo up; exec sleep 60", Gate: "stack", Port: []string{strconv.Itoa(int(port)) + "=api"},
		Stop: "echo down > " + stopped,
	})
	if sv.GetState() != planv1.ServeState_SERVE_STATE_RUNNING || !alive(int(sv.GetPid())) || !sv.GetRelight() {
		t.Fatalf("started: %v, pid %d alive %v, relight %v; want running, alive, to start again", sv.GetState(), sv.GetPid(),
			alive(int(sv.GetPid())), sv.GetRelight())
	}
	want := &planv1.Port{Number: port, Label: "api", Declared: true, Open: true}
	if len(sv.GetPorts()) != 1 || !proto.Equal(sv.GetPorts()[0], want) {
		t.Errorf("ports %v, want %v", sv.GetPorts(), want)
	}
	if !gates.holds("stack") {
		t.Errorf("gate stack not held while the serve runs")
	}
	serves := &Serves{h: e.h}
	list, err := serves.List(t.Context(), connect.NewRequest(&planv1.ServeServiceListRequest{TaskId: ifrit.GetId()}))
	if err != nil {
		t.Fatal(err)
	}
	if got := list.Msg.GetServes(); len(got) != 1 || got[0].GetName() != "back" || got[0].GetPid() != sv.GetPid() {
		t.Errorf("listed %v, want back running", got)
	}
	e.storedServe(t, ifrit.GetId(), "back", serveState(planv1.ServeState_SERVE_STATE_RUNNING))
	worktree := e.get(t, ifrit.GetId()).GetWorktree()
	waitFor(t, "its output in its log", func() bool {
		logs, err := serves.Logs(t.Context(), connect.NewRequest(&planv1.ServeServiceLogsRequest{TaskId: ifrit.GetId(), Name: "back"}))
		return err == nil && slices.Contains(logs.Msg.GetLines(), "up") && slices.Contains(logs.Msg.GetLines(), worktree)
	})

	res, err := serves.Stop(t.Context(), connect.NewRequest(&planv1.ServeServiceStopRequest{TaskId: ifrit.GetId(), Name: "back"}))
	if err != nil {
		t.Fatal(err)
	}
	final := res.Msg.GetServe()
	if final.GetState() != planv1.ServeState_SERVE_STATE_STOPPED || final.GetPid() != 0 || final.GetRelight() {
		t.Errorf("stopped: %v, pid %d, relight %v; want stopped, no pid, not to start again", final.GetState(), final.GetPid(), final.GetRelight())
	}
	if b, err := os.ReadFile(stopped); err != nil || strings.TrimSpace(string(b)) != "down" {
		t.Errorf("its stop command wrote %q (%v), want down", b, err)
	}
	if alive(int(sv.GetPid())) {
		t.Errorf("its process %d still runs", sv.GetPid())
	}
	if gates.holds("stack") {
		t.Errorf("gate stack still held")
	}
	if p := final.GetPorts(); len(p) != 1 || !p[0].GetOpen() {
		t.Errorf("ports %v, want %d still answering", p, port)
	}
	stillLine := strconv.Itoa(int(port)) + " still answers after " + ifrit.GetCode() + "'s serve back stopped"
	waitFor(t, "the lead told the port still answers", func() bool {
		return slices.ContainsFunc(queue.all(), func(ev *planv1.LeadEvent) bool {
			return ev.GetKind() == planv1.LeadEventKind_LEAD_EVENT_KIND_MOVE && strings.Contains(ev.GetText(), stillLine)
		})
	})
	e.storedServe(t, ifrit.GetId(), "back", serveState(planv1.ServeState_SERVE_STATE_STOPPED))
	var said bool
	for _, ev := range e.seen(t, ifrit.GetId(), 0) {
		said = said || strings.HasPrefix(ev.GetText(), "serve back stopped: djinn serve stop; its stop command ran; port "+stillLine)
	}
	if !said {
		t.Errorf("no event says it stopped, its stop command ran, and its port still answers")
	}

	e.done(t, ifrit.GetId(), "")
	if got := e.ended(t, ifrit.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Errorf("ifrit ended %v (%s), want done", got.GetStatus(), got.GetError())
	}
}

// TestServeStopCommandRunsWhenItGoesOut (L06 section 5): an ifrit's run ends (going out ends it as djinn task stop does):
// each serve's stop command runs, then its process group ends, a child it started in the background with it, and its
// gate is given back. Its serve stays recorded, stopped, to start again when it is lit again.
func TestServeStopCommandRunsWhenItGoesOut(t *testing.T) {
	t.Parallel()
	gates := &gateBook{}
	e := up(t, t.TempDir(), WithTick(time.Hour), WithGates(gates.take))
	wishID, _ := e.wish(t, gitRepo(t))
	ifrit := e.ifritLit(t, wishID)
	dir := t.TempDir()
	child, stopped := filepath.Join(dir, "child"), filepath.Join(dir, "stopped")

	sv := e.mustServe(t, ifrit, &planv1.ServeServiceRunRequest{
		Name: "front", Command: "sleep 60 & echo $! > " + child + "; exec sleep 60", Gate: "stack", Stop: "echo down > " + stopped,
	})
	var bg int
	waitFor(t, "its child's pid", func() bool {
		b, err := os.ReadFile(child)
		bg, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		return err == nil && bg > 0
	})
	if !alive(bg) || !alive(int(sv.GetPid())) {
		t.Fatalf("serve %d alive %v, its child %d alive %v", sv.GetPid(), alive(int(sv.GetPid())), bg, alive(bg))
	}

	if _, err := e.tasks.Stop(t.Context(), connect.NewRequest(&planv1.TaskServiceStopRequest{TaskId: ifrit.GetId()})); err != nil {
		t.Fatal(err)
	}
	ended := e.ended(t, ifrit.GetId())
	if b, err := os.ReadFile(stopped); err != nil || strings.TrimSpace(string(b)) != "down" {
		t.Errorf("its stop command wrote %q (%v), want down", b, err)
	}
	if alive(int(sv.GetPid())) || alive(bg) {
		t.Errorf("serve %d alive %v, its child %d alive %v: want both ended", sv.GetPid(), alive(int(sv.GetPid())), bg, alive(bg))
	}
	if gates.holds("stack") {
		t.Errorf("gate stack still held")
	}
	serves := ended.GetIfrit().GetServes()
	if len(serves) != 1 || serves[0].GetState() != planv1.ServeState_SERVE_STATE_STOPPED || serves[0].GetPid() != 0 || !serves[0].GetRelight() {
		t.Errorf("serves %v, want front stopped, to start again", serves)
	}
}

// TestServesComeBackOnRelight (L06 sections 3 and 5): an ifrit lit again (djinn task continue, the path a message takes) starts
// its serves again before its agent, each a new process: those its run's end stopped, not one djinn serve stop
// stopped for good.
func TestServesComeBackOnRelight(t *testing.T) {
	t.Parallel()
	e := up(t, t.TempDir(), WithTick(time.Hour))
	wishID, _ := e.wish(t, gitRepo(t))
	ifrit := e.ifritLit(t, wishID)
	front := e.mustServe(t, ifrit, &planv1.ServeServiceRunRequest{Name: "front", Command: "exec sleep 60"})
	e.mustServe(t, ifrit, &planv1.ServeServiceRunRequest{Name: "back", Command: "exec sleep 60"})
	if _, err := (&Serves{h: e.h}).Stop(t.Context(), connect.NewRequest(&planv1.ServeServiceStopRequest{
		TaskId: ifrit.GetId(), Name: "back",
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := e.tasks.Stop(t.Context(), connect.NewRequest(&planv1.TaskServiceStopRequest{TaskId: ifrit.GetId()})); err != nil {
		t.Fatal(err)
	}
	out := e.ended(t, ifrit.GetId())
	if alive(int(front.GetPid())) {
		t.Fatalf("front %d runs on after its ifrit's end", front.GetPid())
	}
	before := len(e.watch(t.Context(), t, ifrit.GetId(), 0))

	if _, err := e.continueTask(t, ifrit.GetId(), "text lit again"); err != nil {
		t.Fatal(err)
	}
	next := e.follow(t, ifrit.GetId())
	next("it listens again", func(ev *planv1.TaskEvent) bool { return int(ev.GetSeq()) > before && listens(ev) })
	again := e.storedServe(t, ifrit.GetId(), "front", serveState(planv1.ServeState_SERVE_STATE_RUNNING))
	if again.GetPid() == front.GetPid() || !alive(int(again.GetPid())) {
		t.Errorf("front again: pid %d (was %d), alive %v; want a new process", again.GetPid(), front.GetPid(), alive(int(again.GetPid())))
	}
	if !again.GetStartTime().AsTime().After(out.GetEndTime().AsTime()) && !again.GetStartTime().AsTime().Equal(out.GetEndTime().AsTime()) {
		t.Errorf("front started at %v, before its ifrit was lit again", again.GetStartTime().AsTime())
	}
	if sv := e.get(t, ifrit.GetId()).GetIfrit().GetServes()[serveAt(out.GetIfrit().GetServes(), "back")]; sv.GetState() != planv1.ServeState_SERVE_STATE_STOPPED {
		t.Errorf("back %v, want it left stopped", sv)
	}
	// Before its agent: its serve's start is an event before what the agent says.
	var order []string
	for _, ev := range e.seen(t, ifrit.GetId(), int64(before)) {
		switch {
		case strings.HasPrefix(ev.GetText(), "serve front started"):
			order = append(order, "serve")
		case ev.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_TEXT && ev.GetText() == "lit again":
			order = append(order, "agent")
		}
	}
	if !slices.Equal(order, []string{"serve", "agent"}) {
		t.Errorf("events in the order %v, want the serve, then the agent", order)
	}
	e.done(t, ifrit.GetId(), "")
	e.ended(t, ifrit.GetId())
	if alive(int(again.GetPid())) {
		t.Errorf("front %d runs on after its ifrit is done", again.GetPid())
	}
}

// TestServeFollowsTheWatchersRule (L06 section 5, choice 10): where the project's permissions list commands, a serve runs one
// of them, or one the developer allows for themselves (djinn project allow --serve), its stop command too; the
// project's denied commands win.
func TestServeFollowsTheWatchersRule(t *testing.T) {
	t.Parallel()
	e := up(t, t.TempDir(), WithTick(time.Hour))
	repo := gitRepoWith(t, map[string]string{".agents/permissions.txtpb": "commands: \"sleep\"\ndenied_commands: \"rm\"\n"})
	wishID, projectID := e.wish(t, repo)
	ifrit := e.ifritLit(t, wishID)

	if _, err := e.serve(t, ifrit, &planv1.ServeServiceRunRequest{Name: "front", Command: "sleep 60", Stop: "echo down"}); err == nil ||
		!strings.Contains(err.Error(), `do not list "echo down"`) || !strings.Contains(err.Error(), "--serve 'echo down'") {
		t.Errorf("an unlisted stop command: %v, want it refused, and how to allow it", err)
	}
	if _, err := plan.AllowServe(e.home, projectID, "echo", false); err != nil {
		t.Fatal(err)
	}
	sv, err := e.serve(t, ifrit, &planv1.ServeServiceRunRequest{Name: "front", Command: "sleep 60", Stop: "echo down"})
	if err != nil || sv.GetState() != planv1.ServeState_SERVE_STATE_RUNNING {
		t.Errorf("allowed by the developer: %v, %v; want it running", sv.GetState(), err)
	}
	if _, err := plan.AllowServe(e.home, projectID, "rm", false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.serve(t, ifrit, &planv1.ServeServiceRunRequest{Name: "back", Command: "rm -rf build"}); err == nil ||
		!strings.Contains(err.Error(), `denies "rm -rf build"`) {
		t.Errorf("a denied command allowed by the developer: %v, want the project's refusal", err)
	}
	if _, err := e.serve(t, ifrit, &planv1.ServeServiceRunRequest{Name: "back", Command: "exit 3"}); err == nil {
		t.Errorf("an unlisted command ran")
	}
	// Only an ifrit's own worker runs one.
	if _, err := (&Serves{h: e.h}).Run(t.Context(), connect.NewRequest(&planv1.ServeServiceRunRequest{
		TaskId: ifrit.GetId(), Name: "x", Command: "sleep 1",
	})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("run from outside its worker: %v, want permission denied", err)
	}
	e.done(t, ifrit.GetId(), "")
	e.ended(t, ifrit.GetId())
}

// TestServeThatFailsAtOnce: a serve whose command ends as it starts fails djinn serve run with its last lines; one that
// ends later is recorded ended, its gate given back.
func TestServeThatFailsAtOnce(t *testing.T) {
	t.Parallel()
	gates := &gateBook{}
	e := up(t, t.TempDir(), WithTick(time.Hour), WithGates(gates.take))
	wishID, _ := e.wish(t, gitRepo(t))
	ifrit := e.ifritLit(t, wishID)
	_, err := e.serve(t, ifrit, &planv1.ServeServiceRunRequest{Name: "front", Command: "echo no such script >&2; exit 2", Gate: "stack"})
	if err == nil || !strings.Contains(err.Error(), "exit code 2") || !strings.Contains(err.Error(), "no such script") {
		t.Errorf("a serve that fails at once: %v, want its exit code and its last line", err)
	}
	if gates.holds("stack") {
		t.Errorf("gate stack held by a serve that failed")
	}
	e.storedServe(t, ifrit.GetId(), "front", serveState(planv1.ServeState_SERVE_STATE_EXITED))
	e.done(t, ifrit.GetId(), "")
	e.ended(t, ifrit.GetId())
}

// TestServesSweptAtNextStart (L06 section 5): the serves a dead Djinn left are stopped at the next start through run/, the
// stop command of each run, whether its process still ran or not; their ifrit's record says they stopped.
func TestServesSweptAtNextStart(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	run := filepath.Join(home, runDir)
	dead := endedPID(t)
	dir := t.TempDir()
	left, id := startOther(t)
	ended := endedPID(t)
	for _, c := range []child{
		{PID: id.pid, Start: id.start, Group: id.pid, Djinn: dead.pid, DjinnStart: dead.start, Task: "W1", Kind: "serve",
			Stop: "echo running > " + filepath.Join(dir, "running"), Dir: dir},
		{PID: ended.pid, Start: "gone", Djinn: dead.pid, DjinnStart: dead.start, Task: "W1", Kind: "serve",
			Stop: "echo ended > " + filepath.Join(dir, "ended"), Dir: dir},
	} {
		b, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, run, strconv.Itoa(c.PID)+".json", string(b))
	}

	labels := newChildren(run, Grace).sweep()
	if !slices.Equal(labels, []string{"W1's serve"}) {
		t.Errorf("swept %v, want W1's serve", labels)
	}
	if same(id) {
		t.Errorf("the serve left running (pid %d) still runs", left.Process.Pid)
	}
	for _, name := range []string{"running", "ended"} {
		if b, err := os.ReadFile(filepath.Join(dir, name)); err != nil || strings.TrimSpace(string(b)) != name {
			t.Errorf("the stop command of the serve %s wrote %q (%v)", name, b, err)
		}
	}

	// Its ifrit, as the dead Djinn left it: its serve running.
	e := up(t, t.TempDir())
	wishID, _ := e.wish(t, gitRepo(t))
	task := &planv1.Task{Id: store.NewID(), WishId: wishID, Code: "W9", Title: "Front", Kind: planv1.TaskKind_TASK_KIND_IFRIT,
		Status: planv1.TaskStatus_TASK_STATUS_STOPPED, Ifrit: &planv1.TaskIfrit{Serves: []*planv1.Serve{{
			Name: "front", Command: "npm run dev", Pid: 4242, State: planv1.ServeState_SERVE_STATE_RUNNING, Relight: true,
			Ports: []*planv1.Port{{Number: 5173, Open: true}},
		}}}}
	if err := e.db.Tx(t.Context(), func(tx *store.Tx) error {
		if err := tx.Journal("test", "test/crashed", task); err != nil {
			return err
		}
		return tx.Put(task)
	}); err != nil {
		t.Fatal(err)
	}
	h := New(e.db, t.TempDir(), testProviders())
	if err := h.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	h.Close()
	sv := e.get(t, task.GetId()).GetIfrit().GetServes()[0]
	if sv.GetState() != planv1.ServeState_SERVE_STATE_STOPPED || sv.GetPid() != 0 || sv.GetPorts()[0].GetOpen() || !sv.GetRelight() {
		t.Errorf("recovered %v, want stopped, no pid, its port closed, to start again", sv)
	}
}

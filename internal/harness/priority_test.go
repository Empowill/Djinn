package harness

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/machine"
)

// TestNiceByNotch verifies the nice level of what Djinn starts, at each load notch (Q371 B): a worker at 10 but at
// Overclock, where it runs at Djinn's own (0); a check at 5, between the workers and the window, and never below the
// workers (0 at Overclock too); a serve at 10 at every notch. Auto weighs the notch it chose.
func TestNiceByNotch(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		notch                djinnv1.LoadNotch
		worker, check, serve int
	}{
		{djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL, 10, 5, 10},
		{djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM, 10, 5, 10},
		{djinnv1.LoadNotch_LOAD_NOTCH_HIGH, 10, 5, 10},
		{djinnv1.LoadNotch_LOAD_NOTCH_MAX, 10, 5, 10},
		{djinnv1.LoadNotch_LOAD_NOTCH_OVERCLOCK, 0, 0, 10},
	} {
		h, p := &Harness{}, machine.NotchPolicy(c.notch)
		h.policy.Store(&p)
		if got := h.workerNice(); got != c.worker {
			t.Errorf("%v: a worker at nice %d, want %d", c.notch, got, c.worker)
		}
		if got := h.checkNice(); got != c.check {
			t.Errorf("%v: a check at nice %d, want %d", c.notch, got, c.check)
		}
		if serveNice != c.serve {
			t.Errorf("%v: a serve at nice %d, want %d", c.notch, serveNice, c.serve)
		}
		p.Auto = true
		h.policy.Store(&p)
		if got := h.workerNice(); got != c.worker {
			t.Errorf("Auto at %v: a worker at nice %d, want %d", c.notch, got, c.worker)
		}
	}
}

// TestWorkerPriority verifies that a worker started with Spec.Nice runs at that nice level, and at Djinn's own
// priority level at 0. It is a system test that must run under DJINN_TEST_SYSTEM_ONLY=1 (omits testx.Portable).
func TestWorkerPriority(t *testing.T) {
	t.Parallel()
	env, _, _ := fake{provider: "claude", fixture: "success", end: "wait"}.env(t)

	for _, tc := range []struct {
		name string
		nice int
	}{
		{name: "normal priority", nice: 0},
		{name: "low priority", nice: lowNice},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := Spec{
				TaskID: "task-priority-" + tc.name,
				Dir:    t.TempDir(),
				Prompt: "hello",
				Env:    env,
				Nice:   tc.nice,
			}
			w, err := Claude{Command: os.Args[0], Grace: time.Second}.Start(t.Context(), spec)
			if err != nil {
				t.Fatalf("start worker: %v", err)
			}
			defer w.Stop()

			proc, ok := w.(Process)
			if !ok {
				t.Fatalf("worker does not implement Process")
			}
			pid := proc.PID()
			if pid <= 0 {
				t.Fatalf("invalid worker PID %d", pid)
			}

			checkWorkerPriority(t, "worker", pid, tc.nice)
		})
	}
}

// priorityHookProvider records the PID of every started worker.
type priorityHookProvider struct {
	Provider
	mu      sync.Mutex
	started map[string]int
}

func newPriorityHookProvider(base Provider) *priorityHookProvider {
	return &priorityHookProvider{Provider: base, started: make(map[string]int)}
}

func (p *priorityHookProvider) Start(ctx context.Context, spec Spec) (Worker, error) {
	w, err := p.Provider.Start(ctx, spec)
	if err != nil {
		return nil, err
	}
	if proc, ok := w.(Process); ok {
		p.mu.Lock()
		p.started[spec.TaskID] = proc.PID()
		p.mu.Unlock()
	}
	return w, nil
}

func (p *priorityHookProvider) pidOf(taskID string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.started[taskID]
}

// TestWorkerPriorityByNotch verifies that Djinn starts a worker at low CPU priority, below the window, at High (as at
// every notch but Overclock), and at Djinn's own at Overclock (Q298, L50, Q371): a change of notch reaches the next
// worker. Two workers, not one per notch (TestNiceByNotch has each): each costs a start of the test binary, slow on
// Windows.
func TestWorkerPriorityByNotch(t *testing.T) {
	t.Parallel()
	env, _, _ := fake{provider: "claude", fixture: "success", end: "wait"}.env(t)

	baseProvider := envProvider{Claude{Command: os.Args[0], Grace: time.Second}, env}
	hook := newPriorityHookProvider(baseProvider)
	providers := testProviders()
	providers[planv1.Provider_PROVIDER_CLAUDE] = hook

	e := upWith(t, t.TempDir(), providers, WithPolicy(machine.NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_HIGH)))
	wishID, _ := e.wish(t, nativeFolder(t))

	spawnAndCheck := func(title string, nice int) {
		res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
			WishId: wishID, Title: title, Prompt: "do work", Provider: planv1.Provider_PROVIDER_CLAUDE,
		}))
		if err != nil {
			t.Fatalf("spawn %q: %v", title, err)
		}
		id := res.Msg.GetTask().GetId()
		waitFor(t, title+" PID", func() bool { return hook.pidOf(id) > 0 })
		checkWorkerPriority(t, title, hook.pidOf(id), nice)

		// Stop the task to free worker slots.
		if _, err := e.tasks.Stop(t.Context(), connect.NewRequest(&planv1.TaskServiceStopRequest{TaskId: id})); err != nil {
			t.Fatalf("stop %q: %v", title, err)
		}
		e.ended(t, id)
	}

	spawnAndCheck("Worker 1 High", lowNice)
	e.h.SetPolicy(machine.NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_OVERCLOCK))
	spawnAndCheck("Worker 2 Overclock", 0)
}

// fakePriorityArg, as the test binary's first argument, makes it print its own CPU priority level (ownLevel) and exit.
const fakePriorityArg = "djinn-fake-priority"

// TestCheckPriority verifies that a command Djinn runs on a project's work, a commit or push check, the setup, a
// generation, or an install Djinn starts by itself, runs at a check's nice level, below the window and above the
// workers (Q371): nice 5, and at Overclock the workers' (Djinn's own); an install the developer asked for runs at
// Djinn's own, as it is waited for.
func TestCheckPriority(t *testing.T) {
	t.Parallel()
	installing := context.WithValue(t.Context(), installingKey{}, true)
	for _, c := range []struct {
		name  string
		notch djinnv1.LoadNotch
		ctx   context.Context
		nice  int
	}{
		{"check", djinnv1.LoadNotch_LOAD_NOTCH_MAX, t.Context(), checkNice},
		{"check at Overclock", djinnv1.LoadNotch_LOAD_NOTCH_OVERCLOCK, t.Context(), 0},
		{"install by Djinn", djinnv1.LoadNotch_LOAD_NOTCH_MAX, InstallWith(installing, nil, true), checkNice},
		{"install by the developer", djinnv1.LoadNotch_LOAD_NOTCH_MAX, InstallWith(installing, nil, false), 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			h, p := &Harness{}, machine.NotchPolicy(c.notch)
			h.policy.Store(&p)
			out, code, err := h.command(c.ctx, "test", "", os.Args[0]+" "+fakePriorityArg, t.TempDir())
			if err != nil || code != 0 {
				t.Fatalf("command: exit %d, %v: %s", code, err, out)
			}
			if got, want := strings.TrimSpace(out), wantLevel(t, c.nice); got != want {
				t.Errorf("the command ran at priority %s, want %s", got, want)
			}
		})
	}
}

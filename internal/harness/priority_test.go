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

// TestWorkerPriority verifies that a worker started with Spec.LowPriority runs at low OS priority, and at Djinn's own
// priority level without it. It is a system test that must run under DJINN_TEST_SYSTEM_ONLY=1 (omits testx.Portable).
func TestWorkerPriority(t *testing.T) {
	t.Parallel()
	env, _, _ := fake{provider: "claude", fixture: "success", end: "wait"}.env(t)

	for _, tc := range []struct {
		name        string
		lowPriority bool
	}{
		{name: "normal priority", lowPriority: false},
		{name: "low priority", lowPriority: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := Spec{
				TaskID:      "task-priority-" + tc.name,
				Dir:         t.TempDir(),
				Prompt:      "hello",
				Env:         env,
				LowPriority: tc.lowPriority,
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

			checkWorkerPriority(t, "worker", pid, tc.lowPriority)
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

// TestWorkerPriorityAnyNotch verifies that Djinn starts every worker at low CPU priority, below the window, at any
// operating load notch, the highest too: the notch sets only how many run (Q298, L50). A change of notch reaches the
// next worker. Two workers, not one per notch: each costs a start of the test binary, slow on Windows.
func TestWorkerPriorityAnyNotch(t *testing.T) {
	t.Parallel()
	env, _, _ := fake{provider: "claude", fixture: "success", end: "wait"}.env(t)

	baseProvider := envProvider{Claude{Command: os.Args[0], Grace: time.Second}, env}
	hook := newPriorityHookProvider(baseProvider)
	providers := testProviders()
	providers[planv1.Provider_PROVIDER_CLAUDE] = hook

	e := upWith(t, t.TempDir(), providers, WithPolicy(machine.NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_HIGH)))
	wishID, _ := e.wish(t, nativeFolder(t))

	spawnAndCheck := func(title string) {
		res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
			WishId: wishID, Title: title, Prompt: "do work", Provider: planv1.Provider_PROVIDER_CLAUDE,
		}))
		if err != nil {
			t.Fatalf("spawn %q: %v", title, err)
		}
		id := res.Msg.GetTask().GetId()
		waitFor(t, title+" PID", func() bool { return hook.pidOf(id) > 0 })
		checkWorkerPriority(t, title, hook.pidOf(id), true)

		// Stop the task to free worker slots.
		if _, err := e.tasks.Stop(t.Context(), connect.NewRequest(&planv1.TaskServiceStopRequest{TaskId: id})); err != nil {
			t.Fatalf("stop %q: %v", title, err)
		}
		e.ended(t, id)
	}

	spawnAndCheck("Worker 1 High")
	e.h.SetPolicy(machine.NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_OVERCLOCK))
	spawnAndCheck("Worker 2 Overclock")
}

// fakePriorityArg, as the test binary's first argument, makes it print its own CPU priority level (ownLevel) and exit.
const fakePriorityArg = "djinn-fake-priority"

// TestCheckPriority verifies that a command Djinn runs on a project's work, a commit or push check, the setup, a
// generation, or an install Djinn starts by itself, runs at a low-priority worker's CPU priority whatever the notch,
// below the window (Q298, L50); an install the developer asked for runs at Djinn's own, as it is waited for.
func TestCheckPriority(t *testing.T) {
	t.Parallel()
	h, p := &Harness{}, machine.NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_MAX)
	h.policy.Store(&p)
	installing := context.WithValue(t.Context(), installingKey{}, true)
	for _, c := range []struct {
		name string
		ctx  context.Context
		low  bool
	}{
		{"check", t.Context(), true},
		{"install by Djinn", InstallWith(installing, nil, true), true},
		{"install by the developer", InstallWith(installing, nil, false), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			out, code, err := h.command(c.ctx, "test", "", os.Args[0]+" "+fakePriorityArg, t.TempDir())
			if err != nil || code != 0 {
				t.Fatalf("command: exit %d, %v: %s", code, err, out)
			}
			if got, want := strings.TrimSpace(out), wantLevel(t, c.low); got != want {
				t.Errorf("the command ran at priority %s, want %s", got, want)
			}
		})
	}
}

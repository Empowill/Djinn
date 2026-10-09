package harness

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// envClaude is Claude played by the test binary: the fake's environment is added to every worker's, warm or not,
// and each one records its input and its arguments in files named after its task, in dir.
type envClaude struct {
	Claude
	env []string
	dir string
}

func (c envClaude) with(spec Spec) Spec {
	spec.Env = append(append(spec.Env, c.env...), "DJINN_FAKE_INPUT="+filepath.Join(c.dir, "input-"+spec.TaskID),
		"DJINN_FAKE_ARGS="+filepath.Join(c.dir, "args-"+spec.TaskID))
	return spec
}

func (c envClaude) Start(ctx context.Context, spec Spec) (Worker, error) {
	return c.Claude.Start(ctx, c.with(spec))
}

func (c envClaude) Warm(ctx context.Context, spec Spec) (Worker, error) {
	return c.Claude.Warm(ctx, c.with(spec))
}

// warmProviders are the providers with a Claude played by the test binary, which replays a success.
func warmProviders(t *testing.T) (map[planv1.Provider]Provider, func(taskID string) string) {
	t.Helper()
	env, _, _ := fake{provider: "claude", fixture: "success", end: "eof"}.env(t)
	dir := t.TempDir()
	providers := Providers()
	providers[planv1.Provider_PROVIDER_CLAUDE] = envClaude{Claude: Claude{Command: os.Args[0], Grace: time.Second}, env: env, dir: dir}
	return providers, func(taskID string) string { return filepath.Join(dir, "input-"+taskID) }
}

// waitFor polls cond until it holds, or fails after a few seconds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("waited in vain for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// warmOf is the warm worker of the wish in the project, if any.
func (e *env) warmOf(wishID, projectID string) *warm {
	e.h.sched.Lock()
	defer e.h.sched.Unlock()
	return e.h.warm[warmKey(wishID, projectID)]
}

func warmBranches(t *testing.T, repo string) string {
	t.Helper()
	out, err := git(t.Context(), repo, "branch", "--list", warmBranchPrefix+"*")
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestWarmWorker: a process loaded ahead is taken by the next task of its project, and replaced; the task gets
// the warm worker's worktree, renamed after it, and its first message is the task's prompt.
func TestWarmWorker(t *testing.T) {
	repo := gitRepo(t)
	providers, input := warmProviders(t)
	e := upWith(t, t.TempDir(), providers, WithWarm(), WithTick(20*time.Millisecond))
	wishID, projectID := e.wish(t, repo)

	waitFor(t, "a warm worker", func() bool { return e.warmOf(wishID, projectID) != nil })
	w := e.warmOf(wishID, projectID)
	if _, err := os.Stat(w.worktree); err != nil || !strings.HasPrefix(w.branch, warmBranchPrefix) {
		t.Fatalf("warm worktree %s on %s: %v", w.worktree, w.branch, err)
	}
	// It waits: no message yet.
	if b, _ := os.ReadFile(input(w.id)); len(b) != 0 {
		t.Errorf("the warm worker got a message before any task: %q", b)
	}

	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Take the warm one", Prompt: "Say hello", Provider: planv1.Provider_PROVIDER_CLAUDE,
	}))
	if err != nil {
		t.Fatal(err)
	}
	task := res.Msg.GetTask()
	if task.GetId() != w.id || task.GetWorktree() != w.worktree || !strings.HasPrefix(task.GetBranch(), "w1-take-the-warm-one-") {
		t.Errorf("task %v did not take the warm worker %s in %s", task, w.id, w.worktree)
	}
	done := e.ended(t, task.GetId())
	if done.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Errorf("task = %v", done)
	}
	var started string
	for _, ev := range e.watch(t.Context(), t, task.GetId(), 0) {
		if strings.HasPrefix(ev.GetText(), "started claude") {
			started = ev.GetText()
		}
	}
	if !strings.Contains(started, "on a warm worker loaded") {
		t.Errorf("start event %q", started)
	}
	if b, _ := os.ReadFile(input(task.GetId())); !strings.Contains(string(b), "Say hello") {
		t.Errorf("first message = %q", b)
	}
	if b := warmBranches(t, repo); strings.Contains(b, w.branch) {
		t.Errorf("the placeholder branch stayed: %q", b)
	}

	// Another one is loaded for the next task.
	waitFor(t, "a second warm worker", func() bool {
		next := e.warmOf(wishID, projectID)
		return next != nil && next.id != w.id
	})
	// A task that asks for what the warm worker was not started with starts cold.
	cold, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Cold", Prompt: "x", Provider: planv1.Provider_PROVIDER_CLAUDE, Model: "haiku",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if next := e.warmOf(wishID, projectID); next == nil || cold.Msg.GetTask().GetId() == next.id {
		t.Errorf("a task with a model took the warm worker")
	}
	e.ended(t, cold.Msg.GetTask().GetId())

	// djinn up stops: the warm worker and its worktree go.
	last := e.warmOf(wishID, projectID)
	e.down()
	if _, err := os.Stat(last.worktree); !os.IsNotExist(err) {
		t.Errorf("the warm worktree stayed after close: %v", err)
	}
	if b := warmBranches(t, repo); b != "" {
		t.Errorf("warm branches stayed: %q", b)
	}
}

// TestWarmTakesTheProjectSettings: a warm worker starts with the model and budget the project's settings give, as
// Spawn fills them, so that a task of the project takes it; when the settings change, it is replaced. A task that
// asks for another model starts cold and leaves it.
func TestWarmTakesTheProjectSettings(t *testing.T) {
	repo := gitRepo(t)
	writeFile(t, repo, ".agents/settings.txtpb", "model: \"team-model\"\nmax_budget_usd: 2\n")
	home := t.TempDir()
	providers, input := warmProviders(t)
	args := func(taskID string) string {
		b, _ := os.ReadFile(filepath.Join(filepath.Dir(input(taskID)), "args-"+taskID))
		return string(b)
	}
	e := upWith(t, home, providers, WithWarm(), WithTick(20*time.Millisecond))
	wishID, projectID := e.wish(t, repo)

	waitFor(t, "a warm worker", func() bool { return e.warmOf(wishID, projectID) != nil })
	w := e.warmOf(wishID, projectID)
	waitFor(t, "the project's model and budget in the warm worker's arguments", func() bool {
		a := args(w.id)
		return strings.Contains(a, "--model\nteam-model\n") && strings.Contains(a, "--max-budget-usd\n2")
	})
	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Take the warm one", Prompt: "Say hello",
	}))
	if err != nil {
		t.Fatal(err)
	}
	task := res.Msg.GetTask()
	if task.GetId() != w.id || task.GetModel() != "team-model" || task.GetMaxBudgetUsd() != 2 {
		t.Errorf("task %v did not take the warm worker %s", task, w.id)
	}
	e.ended(t, task.GetId())

	// The developer's settings change: the warm worker waiting is replaced by one with the new model.
	waitFor(t, "a second warm worker", func() bool {
		next := e.warmOf(wishID, projectID)
		return next != nil && next.id != w.id
	})
	writeFile(t, home, "projects/"+projectID+"/settings.txtpb", "model: \"my-model\"\n")
	waitFor(t, "a warm worker with the developer's model", func() bool {
		next := e.warmOf(wishID, projectID)
		return next != nil && next.spec.Model == "my-model" && next.spec.MaxBudgetUSD == 2
	})

	// A task that asks for another model starts cold, and the warm worker waits for the next one.
	next := e.warmOf(wishID, projectID)
	cold, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Cold", Prompt: "x", Model: "haiku",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cold.Msg.GetTask().GetId() == next.id || e.warmOf(wishID, projectID) != next {
		t.Errorf("a task with another model took the warm worker")
	}
	e.ended(t, cold.Msg.GetTask().GetId())
	e.down()
}

// TestWarmWithinTheMachine: the warm workers fit in the slots the running workers leave, and none waits while the
// machine is under pressure.
func TestWarmWithinTheMachine(t *testing.T) {
	providers, _ := warmProviders(t)
	var mu sync.Mutex
	slots, pressure := 1, ""
	capacity := func() (int, string, string) {
		mu.Lock()
		defer mu.Unlock()
		return slots, "test", pressure
	}
	set := func(s int, p string) {
		mu.Lock()
		defer mu.Unlock()
		slots, pressure = s, p
	}
	e := upWith(t, t.TempDir(), providers, WithWarm(), WithTick(20*time.Millisecond), WithCapacity(capacity))
	wishID, projectID := e.wish(t, nativeFolder(t))
	waitFor(t, "a warm worker in the free slot", func() bool { return e.warmOf(wishID, projectID) != nil })

	// A task runs in the only slot: the warm worker gives its place.
	set(2, "")
	running := e.mustSpawn(t, wishID, "Sleep", "sleep 2s", nil)
	set(1, "")
	waitFor(t, "the warm worker to leave the slot", func() bool { return e.h.WarmCount() == 0 })
	e.ended(t, running.GetId())
	waitFor(t, "the warm worker back", func() bool { return e.h.WarmCount() == 1 })

	// Under pressure, none waits.
	set(4, "memory")
	waitFor(t, "no warm worker under pressure", func() bool { return e.h.WarmCount() == 0 })
	// Without the option, none ever starts.
	off := upWith(t, t.TempDir(), providers, WithTick(20*time.Millisecond))
	offWish, _ := off.wish(t, nativeFolder(t))
	time.Sleep(200 * time.Millisecond)
	if n := off.h.WarmCount(); n != 0 || offWish == "" {
		t.Errorf("%d warm workers without the option", n)
	}
}

// TestWarmLeftovers: a djinn up that died leaves the worktree of a warm worker no task took; the next one removes
// it, and only it.
func TestWarmLeftovers(t *testing.T) {
	repo := gitRepo(t)
	home := t.TempDir()
	e := up(t, home)
	_, projectID := e.wish(t, repo)
	left := worktreeDir(home, projectID, store.NewID())
	taken := worktreeDir(home, projectID, store.NewID())
	if _, err := addWorktree(t.Context(), repo, left, warmBranchPrefix+"deadbeef"); err != nil {
		t.Fatal(err)
	}
	if _, err := addWorktree(t.Context(), repo, taken, "w1-a-task-12345678"); err != nil {
		t.Fatal(err)
	}
	e.down()

	again := up(t, home)
	defer again.down()
	if _, err := os.Stat(left); !os.IsNotExist(err) {
		t.Errorf("the leftover stayed: %v", err)
	}
	if b := warmBranches(t, repo); b != "" {
		t.Errorf("its branch stayed: %q", b)
	}
	if _, err := os.Stat(filepath.Join(taken, "app")); err != nil {
		t.Errorf("a task's worktree was removed: %v", err)
	}
}

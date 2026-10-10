package harness

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/testx"
)

// TestEnvFailureReplayOnVersionChange verifies that an environment failure is replayed at most twice
// upon a binary version change, not three times, and that work failures or un-versioned restarts are not replayed.
func TestEnvFailureReplayOnVersionChange(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	denied, _, _ := fake{provider: "antigravity", fixture: "permission-denied-first"}.env(t)
	home := t.TempDir()

	providers, _ := antigravityPlaying(denied)
	e := upWith(t, home, providers, WithVersion("v1.0.0"), WithTellDelay(10*time.Millisecond))
	lead := &leadListener{}
	e.h.TellLeads(lead.tell)

	repo := gitRepo(t)
	wishID, _ := e.wish(t, repo)

	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Check headless perms", Prompt: "git grep -n RestartFile",
		Provider: planv1.Provider_PROVIDER_ANTIGRAVITY,
	}))
	if err != nil {
		t.Fatal(err)
	}
	id := res.Msg.GetTask().GetId()

	// Wait until it fails on the environment error.
	got := e.until(t, id, isStatus(planv1.TaskStatus_TASK_STATUS_FAILED))
	if got.GetEnvCause() != "permission refused headless" {
		t.Fatalf("env cause = %q, want 'permission refused headless'", got.GetEnvCause())
	}
	if got.GetEnvBuild() != "v1.0.0" {
		t.Fatalf("env build = %q, want 'v1.0.0'", got.GetEnvBuild())
	}
	if got.GetEnvReplays() != 0 {
		t.Fatalf("env replays = %d, want 0", got.GetEnvReplays())
	}

	// Because replays < 2, the lead is not told yet, and task list --failed does not list it.
	e.h.FlushTell(t.Context())
	if len(lead.all()) != 0 {
		t.Fatalf("lead was told %v, want nothing told yet for env failure with replays < 2", lead.all())
	}
	failedList, err := e.tasks.List(t.Context(), connect.NewRequest(&planv1.TaskServiceListRequest{WishId: wishID, Failed: true}))
	if err != nil {
		t.Fatal(err)
	}
	if len(failedList.Msg.GetTasks()) != 0 {
		t.Fatalf("failed tasks list has %d, want 0 waiting for lead", len(failedList.Msg.GetTasks()))
	}

	// 1. Restart on the SAME version: it must NOT replay.
	e.down()
	e2 := upWith(t, home, providers, WithVersion("v1.0.0"), WithTellDelay(10*time.Millisecond))
	t.Cleanup(e2.down)
	got = e2.get(t, id)
	if got.GetStatus() != planv1.TaskStatus_TASK_STATUS_FAILED || got.GetEnvReplays() != 0 {
		t.Fatalf("same version restart: status = %v, replays = %d; want FAILED and 0", got.GetStatus(), got.GetEnvReplays())
	}

	// 2. Restart on a NEW version (v1.0.1): replays 1st time.
	e2.down()
	e3 := upWith(t, home, providers, WithVersion("v1.0.1"), WithTellDelay(10*time.Millisecond))
	t.Cleanup(e3.down)
	// It replays, runs, and fails again since the denied fixture is still set.
	got = e3.until(t, id, func(tk *planv1.Task) bool {
		return tk.GetStatus() == planv1.TaskStatus_TASK_STATUS_FAILED && tk.GetEnvReplays() == 1
	})
	if got.GetEnvBuild() != "v1.0.1" {
		t.Fatalf("after 1st replay: env build = %q, want 'v1.0.1'", got.GetEnvBuild())
	}

	// 3. Restart on another NEW version (v1.0.2): replays 2nd time.
	e3.down()
	e4 := upWith(t, home, providers, WithVersion("v1.0.2"), WithTellDelay(10*time.Millisecond))
	t.Cleanup(e4.down)
	got = e4.until(t, id, func(tk *planv1.Task) bool {
		return tk.GetStatus() == planv1.TaskStatus_TASK_STATUS_FAILED && tk.GetEnvReplays() == 2
	})
	if got.GetEnvBuild() != "v1.0.2" {
		t.Fatalf("after 2nd replay: env build = %q, want 'v1.0.2'", got.GetEnvBuild())
	}

	// 4. Restart on a third NEW version (v1.0.3): MUST NOT replay a 3rd time!
	e4.down()
	lead4 := &leadListener{}
	e5 := upWith(t, home, providers, WithVersion("v1.0.3"), WithTellDelay(10*time.Millisecond))
	t.Cleanup(e5.down)
	e5.h.TellLeads(lead4.tell)

	// Sleep briefly to ensure scheduler pass would have picked it up if it were replaying.
	time.Sleep(50 * time.Millisecond)
	got = e5.get(t, id)
	if got.GetStatus() != planv1.TaskStatus_TASK_STATUS_FAILED || got.GetEnvReplays() != 2 {
		t.Fatalf("after 3rd version change: status = %v, replays = %d; want FAILED and 2", got.GetStatus(), got.GetEnvReplays())
	}

	// Now that env replays >= 2, task list --failed lists it as waiting for the lead.
	failedList, err = e5.tasks.List(t.Context(), connect.NewRequest(&planv1.TaskServiceListRequest{WishId: wishID, Failed: true}))
	if err != nil {
		t.Fatal(err)
	}
	if len(failedList.Msg.GetTasks()) != 1 {
		t.Fatalf("failed tasks list has %d, want 1 waiting for lead", len(failedList.Msg.GetTasks()))
	}
}

// TestDependentWaitsThenRunsOnceDependencyReplayed verifies that a task waiting on a dependency
// waits while the dependency is failed instead of failing, and runs once the dependency is replayed or done.
func TestDependentWaitsThenRunsOnceDependencyReplayed(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	home := t.TempDir()
	providers := testProviders()
	e := upWith(t, home, providers)

	repo := gitRepo(t)
	wishID, _ := e.wish(t, repo)

	// Spawn T1, which fails.
	res1, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Step 1", Prompt: "fail syntax error",
		Provider: planv1.Provider_PROVIDER_FAKE,
	}))
	if err != nil {
		t.Fatal(err)
	}
	t1 := res1.Msg.GetTask()
	e.until(t, t1.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_FAILED))

	// Spawn T2 depending on T1.
	res2, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Step 2", Prompt: "run after step 1",
		Provider:  planv1.Provider_PROVIDER_FAKE,
		DependsOn: []string{t1.GetId()},
	}))
	if err != nil {
		t.Fatal(err)
	}
	t2 := res2.Msg.GetTask()

	// T2 should stay PENDING and say it waits while its dependency is failed.
	got2 := e.until(t, t2.GetId(), func(tk *planv1.Task) bool {
		return tk.GetStatus() == planv1.TaskStatus_TASK_STATUS_PENDING &&
			strings.Contains(tk.GetWaitReason(), "waits while its dependency")
	})
	if !strings.Contains(got2.GetWaitReason(), t1.GetCode()) {
		t.Errorf("wait reason = %q, want mentioning %s", got2.GetWaitReason(), t1.GetCode())
	}

	// Mark T1 done: T2 should unblock, run, and finish DONE.
	if _, err := e.tasks.Done(t.Context(), connect.NewRequest(&planv1.TaskServiceDoneRequest{TaskId: t1.GetId()})); err != nil {
		t.Fatal(err)
	}

	e.until(t, t2.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_DONE))
}

// TestWorkFailureLeftToLead verifies that work failures are immediately told to the lead
// and not replayed automatically across restarts.
func TestWorkFailureLeftToLead(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	home := t.TempDir()
	providers := testProviders()
	e := upWith(t, home, providers, WithVersion("v1.0.0"), WithTellDelay(10*time.Millisecond))
	lead := &leadListener{}
	e.h.TellLeads(lead.tell)

	repo := gitRepo(t)
	wishID, _ := e.wish(t, repo)

	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Code work", Prompt: "fail agent error",
		Provider: planv1.Provider_PROVIDER_FAKE,
	}))
	if err != nil {
		t.Fatal(err)
	}
	id := res.Msg.GetTask().GetId()

	got := e.until(t, id, isStatus(planv1.TaskStatus_TASK_STATUS_FAILED))
	if got.GetEnvCause() != "" {
		t.Fatalf("env cause = %q, want empty for work failure", got.GetEnvCause())
	}

	e.h.FlushTell(t.Context())
	if len(lead.all()) == 0 {
		t.Fatal("lead was not told of work failure")
	}

	// Restart with a new version: work failure is not replayed.
	e.down()
	e2 := upWith(t, home, providers, WithVersion("v1.0.1"), WithTellDelay(10*time.Millisecond))
	t.Cleanup(e2.down)
	time.Sleep(50 * time.Millisecond)
	got = e2.get(t, id)
	if got.GetStatus() != planv1.TaskStatus_TASK_STATUS_FAILED {
		t.Fatalf("status = %v, want FAILED", got.GetStatus())
	}
}

// failingStartProvider is a provider whose Start fails on a specific error.
type failingStartProvider struct {
	err error
}

func (p failingStartProvider) Start(_ context.Context, _ Spec) (Worker, error) {
	return nil, p.err
}

// TestQuestionWorkerStartFailureRetriedOnce verifies that a question worker failing at start
// (e.g. read-only refusal) is retried once on claude.
func TestQuestionWorkerStartFailureRetriedOnce(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	providers := testProviders()
	// Set Antigravity to fail at start with ErrReadOnly.
	providers[planv1.Provider_PROVIDER_ANTIGRAVITY] = failingStartProvider{err: ErrReadOnly}
	// Set Claude to Fake so the fallback succeeds.
	providers[planv1.Provider_PROVIDER_CLAUDE] = Fake{}

	home := t.TempDir()
	e := upWith(t, home, providers, WithQuestionWorkers())

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".agents"), 0o700); err != nil {
		t.Fatal(err)
	}
	// Configure question_provider as ANTIGRAVITY (which fails at start with ErrReadOnly).
	if err := os.WriteFile(filepath.Join(dir, plan.SettingsFile), []byte("provider: PROVIDER_ANTIGRAVITY\nquestion_provider: PROVIDER_ANTIGRAVITY\nanswer_workers: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	wishID, _ := e.wish(t, dir)
	q := e.asker(wishID)
	asked := q.ask(t, "")
	q.answer(t, asked.GetCode(), planv1.Choice_CHOICE_A, "")

	converters := e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_CONVERTER)
	if len(converters) != 1 {
		t.Fatalf("%d converters, want 1", len(converters))
	}
	c := converters[0]

	// The converter was retried on Claude.
	e.ended(t, c.GetId())
	events := e.watch(t.Context(), t, c.GetId(), 0)
	if !slices.ContainsFunc(events, func(ev *planv1.TaskEvent) bool {
		return strings.Contains(ev.GetText(), "started claude (antigravity cannot run read-only: claude)")
	}) {
		t.Errorf("no start event with fallback text in %v", events)
	}
}

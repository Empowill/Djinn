package harness

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/cli"
	"github.com/empowill/djinn/internal/testx"
)

type spyProvider struct {
	mu    sync.Mutex
	specs []Spec
}

func (s *spyProvider) Start(ctx context.Context, spec Spec) (Worker, error) {
	s.mu.Lock()
	s.specs = append(s.specs, spec)
	s.mu.Unlock()
	return Fake{}.Start(ctx, spec)
}

func (s *spyProvider) lastSpec() Spec {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.specs) == 0 {
		return Spec{}
	}
	return s.specs[len(s.specs)-1]
}

// TestSetAgentPlanned: set-agent on a planned task changes provider and model and keeps its dependents waiting on it.
func TestSetAgentPlanned(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	claudeSpy := &spyProvider{}
	agySpy := &spyProvider{}
	providers := map[planv1.Provider]Provider{
		planv1.Provider_PROVIDER_CLAUDE:      claudeSpy,
		planv1.Provider_PROVIDER_ANTIGRAVITY: agySpy,
		planv1.Provider_PROVIDER_FAKE:        Fake{},
		planv1.Provider_PROVIDER_WATCH:       fastWatch,
	}
	e := upWith(t, t.TempDir(), providers, WithCapacity((&limit{slots: 0}).capacity), WithTick(time.Hour))
	wishID, _ := e.wish(t, gitRepo(t))

	first := e.mustSpawn(t, wishID, "Planned first", "text first step", &planv1.TaskServiceSpawnRequest{
		Provider: planv1.Provider_PROVIDER_CLAUDE,
		Later:    true,
	})
	dep := e.mustSpawn(t, wishID, "Dependent second", "text second step", &planv1.TaskServiceSpawnRequest{
		After: []string{first.GetCode()},
	})

	// dep is waiting on first
	depGot := e.get(t, dep.GetId())
	if !strings.Contains(depGot.GetWaitReason(), first.GetCode()) {
		t.Fatalf("dependent task wait reason: %q, want waiting on %s", depGot.GetWaitReason(), first.GetCode())
	}
	if !slices.Contains(depGot.GetDependsOn(), first.GetId()) {
		t.Fatalf("dependent task depends on: %v, want to contain %s", depGot.GetDependsOn(), first.GetId())
	}

	// Change agent of planned task
	agy := planv1.Provider_PROVIDER_ANTIGRAVITY
	model := "gemini-3.8-flash-high"
	res, err := e.tasks.SetAgent(t.Context(), connect.NewRequest(&planv1.TaskServiceSetAgentRequest{
		TaskId:   first.GetId(),
		Provider: &agy,
		Model:    &model,
	}))
	if err != nil {
		t.Fatalf("set-agent: %v", err)
	}
	task := res.Msg.GetTask()
	if task.GetProvider() != planv1.Provider_PROVIDER_ANTIGRAVITY || task.GetModel() != model {
		t.Errorf("set-agent task: %v", task)
	}
	if task.GetPrompt() != "text first step" {
		t.Errorf("task prompt: %q, want 'text first step'", task.GetPrompt())
	}

	// dep is STILL waiting on first
	depGot = e.get(t, dep.GetId())
	if !strings.Contains(depGot.GetWaitReason(), first.GetCode()) {
		t.Errorf("after set-agent, dependent task wait reason: %q, want waiting on %s", depGot.GetWaitReason(), first.GetCode())
	}
	if !slices.Contains(depGot.GetDependsOn(), first.GetId()) {
		t.Errorf("after set-agent, dependent task depends on: %v, want to contain %s", depGot.GetDependsOn(), first.GetId())
	}
	if depGot.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING {
		t.Errorf("dependent task status: %v, want pending", depGot.GetStatus())
	}
}

// TestSetAgentRefusedWhileRunningOrPaused: set-agent is refused while the task's worker runs or pauses.
func TestSetAgentRefusedWhileRunningOrPaused(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	e := up(t, t.TempDir())
	wishID, _ := e.wish(t, gitRepo(t))

	// Spawn a worker that waits for a message
	task := e.spawn(t, wishID, "wait")
	if task.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
		t.Fatalf("task status: %v, want running", task.GetStatus())
	}

	agy := planv1.Provider_PROVIDER_ANTIGRAVITY
	_, err := e.tasks.SetAgent(t.Context(), connect.NewRequest(&planv1.TaskServiceSetAgentRequest{
		TaskId:   task.GetId(),
		Provider: &agy,
	}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("set-agent while running err: %v, want FailedPrecondition", err)
	}
	wantRunningErr := "task " + task.GetCode() + " is running: stop it first (djinn task stop)"
	if !strings.Contains(err.Error(), wantRunningErr) {
		t.Errorf("err: %q, want %q", err.Error(), wantRunningErr)
	}

	// Pause the worker
	_, err = e.tasks.Pause(t.Context(), connect.NewRequest(&planv1.TaskServicePauseRequest{TaskId: task.GetId()}))
	if err != nil {
		t.Fatalf("pause: %v", err)
	}

	_, err = e.tasks.SetAgent(t.Context(), connect.NewRequest(&planv1.TaskServiceSetAgentRequest{
		TaskId:   task.GetId(),
		Provider: &agy,
	}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("set-agent while paused err: %v, want FailedPrecondition", err)
	}
	wantPausedErr := "task " + task.GetCode() + " is paused: stop it first (djinn task stop)"
	if !strings.Contains(err.Error(), wantPausedErr) {
		t.Errorf("err: %q, want %q", err.Error(), wantPausedErr)
	}

	// Stop the worker
	_, err = e.tasks.Stop(t.Context(), connect.NewRequest(&planv1.TaskServiceStopRequest{TaskId: task.GetId()}))
	if err != nil {
		t.Fatalf("stop: %v", err)
	}

	// Now set-agent succeeds on stopped task
	model := "gemini-3.8-flash-high"
	res, err := e.tasks.SetAgent(t.Context(), connect.NewRequest(&planv1.TaskServiceSetAgentRequest{
		TaskId:   task.GetId(),
		Provider: &agy,
		Model:    &model,
	}))
	if err != nil {
		t.Fatalf("set-agent on stopped task: %v", err)
	}
	if res.Msg.GetTask().GetProvider() != agy || res.Msg.GetTask().GetModel() != model {
		t.Errorf("set-agent stopped task: %v", res.Msg.GetTask())
	}
}

// TestSetAgentFailedContinuedStartsNewSession: a failed task continued after set-agent starts on the new
// provider from its first prompt.
func TestSetAgentFailedContinuedStartsNewSession(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	claudeSpy := &spyProvider{}
	agySpy := &spyProvider{}
	providers := map[planv1.Provider]Provider{
		planv1.Provider_PROVIDER_CLAUDE:      claudeSpy,
		planv1.Provider_PROVIDER_ANTIGRAVITY: agySpy,
		planv1.Provider_PROVIDER_FAKE:        Fake{},
		planv1.Provider_PROVIDER_WATCH:       fastWatch,
	}
	e := upWith(t, t.TempDir(), providers)
	wishID, _ := e.wish(t, gitRepo(t))

	// Spawn a claude task that fails
	firstPrompt := "text hello from claude\nfail tests broke"
	first := e.ended(t, e.mustSpawn(t, wishID, "Failed task", firstPrompt, &planv1.TaskServiceSpawnRequest{
		Provider: planv1.Provider_PROVIDER_CLAUDE,
	}).GetId())
	if first.GetStatus() != planv1.TaskStatus_TASK_STATUS_FAILED {
		t.Fatalf("task status: %v, want failed", first.GetStatus())
	}

	// Change agent to antigravity
	agy := planv1.Provider_PROVIDER_ANTIGRAVITY
	model := "gemini-3.8-flash-high"
	_, err := e.tasks.SetAgent(t.Context(), connect.NewRequest(&planv1.TaskServiceSetAgentRequest{
		TaskId:   first.GetId(),
		Provider: &agy,
		Model:    &model,
	}))
	if err != nil {
		t.Fatalf("set-agent: %v", err)
	}

	// Continue the task
	cont, err := e.tasks.Continue(t.Context(), connect.NewRequest(&planv1.TaskServiceContinueRequest{
		TaskId: first.GetId(),
		Prompt: "text continue prompt",
	}))
	if err != nil {
		t.Fatalf("continue: %v", err)
	}
	if cont.Msg.GetTask().GetProvider() != planv1.Provider_PROVIDER_ANTIGRAVITY {
		t.Errorf("continued provider: %v, want antigravity", cont.Msg.GetTask().GetProvider())
	}

	// Wait for continued run to end
	done := e.ended(t, first.GetId())

	// Check antigravity spy was called with new session and first prompt
	spec := agySpy.lastSpec()
	if spec.Resume != "" {
		t.Errorf("antigravity Resume: %q, want empty (new session)", spec.Resume)
	}
	if !strings.Contains(spec.Prompt, "text hello from claude") {
		t.Errorf("antigravity Prompt: %q, want to contain first prompt", spec.Prompt)
	}
	if strings.Contains(spec.Prompt, "text continue prompt") {
		t.Errorf("antigravity Prompt: %q, should NOT use continue prompt", spec.Prompt)
	}
	if spec.Model != model {
		t.Errorf("antigravity Model: %q, want %q", spec.Model, model)
	}

	// Verify events include the start event saying provider changed
	events := e.watch(t.Context(), t, done.GetId(), 0)
	var foundChangeText bool
	wantEvent := "provider changed: claude → antigravity, starts from its first prompt"
	for _, ev := range events {
		if strings.Contains(ev.GetText(), wantEvent) {
			foundChangeText = true
			break
		}
	}
	if !foundChangeText {
		t.Errorf("events lack %q", wantEvent)
		for i, ev := range events {
			t.Logf("event %d (%s): %s", i, ev.GetKind(), ev.GetText())
		}
	}
}

// TestSetAgentCLI: djinn task set-agent and djinn task get from the command line.
func TestSetAgentCLI(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	e := up(t, t.TempDir(), WithCapacity((&limit{slots: 0}).capacity), WithTick(time.Hour))
	wishID, _ := e.wish(t, gitRepo(t))

	firstPrompt := "text hello CLI"
	task := e.mustSpawn(t, wishID, "CLI test task", firstPrompt, &planv1.TaskServiceSpawnRequest{
		Provider: planv1.Provider_PROVIDER_FAKE,
		Later:    true,
	})

	var out, errs bytes.Buffer
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	// djinn task get shows the prompt
	code := cli.Run(ctx, []string{"task", "get", task.GetId()},
		cli.Config{Version: "test", Addr: e.srv.URL, HTTP: e.srv.Client(), Stdout: &out, Stderr: &errs})
	if code != 0 || !strings.Contains(out.String(), "prompt: "+firstPrompt) {
		t.Errorf("djinn task get: code %d\nstdout:\n%s\nstderr:\n%s", code, out.String(), errs.String())
	}

	// djinn task set-agent changes provider and model
	out.Reset()
	errs.Reset()
	code = cli.Run(ctx, []string{"task", "set-agent", task.GetId(), "--provider", "antigravity", "--model", "gemini-3.8-flash-high"},
		cli.Config{Version: "test", Addr: e.srv.URL, HTTP: e.srv.Client(), Stdout: &out, Stderr: &errs})
	if code != 0 || !strings.Contains(out.String(), "provider: antigravity") || !strings.Contains(out.String(), "model: gemini-3.8-flash-high") {
		t.Errorf("djinn task set-agent: code %d\nstdout:\n%s\nstderr:\n%s", code, out.String(), errs.String())
	}

	// djinn task set-agent --model "" clears model
	out.Reset()
	errs.Reset()
	code = cli.Run(ctx, []string{"task", "set-agent", task.GetId(), "--model", ""},
		cli.Config{Version: "test", Addr: e.srv.URL, HTTP: e.srv.Client(), Stdout: &out, Stderr: &errs})
	if code != 0 {
		t.Errorf("djinn task set-agent --model \"\": code %d\nstdout:\n%s\nstderr:\n%s", code, out.String(), errs.String())
	}
	got := e.get(t, task.GetId())
	if got.GetModel() != "" {
		t.Errorf("model after clearing: %q, want empty", got.GetModel())
	}
}

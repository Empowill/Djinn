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
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/cli"
	"github.com/empowill/djinn/internal/store"
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

// TestUpdatePlannedAllFields: a planned task can update every field in one journaled transaction,
// keeping its id, code, and dependents.
func TestUpdatePlannedAllFields(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	e := up(t, t.TempDir(), WithCapacity((&limit{slots: 0}).capacity), WithTick(time.Hour))
	wishID, _ := e.wish(t, gitRepo(t))

	// Create a dependency task
	second := e.mustSpawn(t, wishID, "Second task", "text second", &planv1.TaskServiceSpawnRequest{Later: true})

	// Create an open azima
	azRes, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID,
		Title:  "Phase 1 Azima",
		Kind:   planv1.TaskKind_TASK_KIND_AZIMA,
	}))
	if err != nil {
		t.Fatal(err)
	}
	azima := azRes.Msg.GetTask()

	// Create a decision block
	blocks := planv1connect.NewBlockServiceClient(e.srv.Client(), e.srv.URL)
	bRes, err := blocks.Put(t.Context(), connect.NewRequest(&planv1.BlockServicePutRequest{
		WishId: wishID,
		Kind:   "decision",
		Title:  "Choose DB",
	}))
	if err != nil {
		t.Fatal(err)
	}
	decisionID := bRes.Msg.GetBlock().GetId()

	// Spawn the planned task
	initialPrompt := "text initial prompt"
	first := e.mustSpawn(t, wishID, "Initial title", initialPrompt, &planv1.TaskServiceSpawnRequest{
		Provider: planv1.Provider_PROVIDER_FAKE,
		Later:    true,
	})

	// Dependent waiting on first
	dep := e.mustSpawn(t, wishID, "Dependent", "text dependent", &planv1.TaskServiceSpawnRequest{
		After: []string{first.GetCode()},
	})

	// Update all fields
	newTitle := "Updated Title"
	newPrompt := "text updated prompt"
	newProvider := planv1.Provider_PROVIDER_ANTIGRAVITY
	newModel := "gemini-3.8-flash-high"
	partOf := azima.GetCode()
	after := []string{second.GetCode()}
	budget := 3.50
	scopes := []string{"doc", "src"}

	res, err := e.tasks.Update(t.Context(), connect.NewRequest(&planv1.TaskServiceUpdateRequest{
		TaskId:       first.GetId(),
		Title:        &newTitle,
		Prompt:       &newPrompt,
		Provider:     &newProvider,
		Model:        &newModel,
		PartOf:       &partOf,
		After:        after,
		Decision:     &decisionID,
		MaxBudgetUsd: &budget,
		WriteScopes:  scopes,
	}))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	task := res.Msg.GetTask()
	if task.GetId() != first.GetId() || task.GetCode() != first.GetCode() {
		t.Errorf("task ID/Code changed: id %q vs %q, code %q vs %q", task.GetId(), first.GetId(), task.GetCode(), first.GetCode())
	}
	if task.GetTitle() != newTitle {
		t.Errorf("title: %q, want %q", task.GetTitle(), newTitle)
	}
	if task.GetPrompt() != newPrompt {
		t.Errorf("prompt: %q, want %q", task.GetPrompt(), newPrompt)
	}
	if task.GetProvider() != newProvider || task.GetModel() != newModel {
		t.Errorf("provider/model: %v/%s, want %v/%s", task.GetProvider(), task.GetModel(), newProvider, newModel)
	}
	if task.GetPartOf() != azima.GetId() {
		t.Errorf("part of: %q, want %q", task.GetPartOf(), azima.GetId())
	}
	if !slices.Contains(task.GetDependsOn(), second.GetId()) {
		t.Errorf("depends on: %v, want to contain %s", task.GetDependsOn(), second.GetId())
	}
	if task.GetDecision() != decisionID {
		t.Errorf("decision: %q, want %q", task.GetDecision(), decisionID)
	}
	if task.GetMaxBudgetUsd() != budget {
		t.Errorf("budget: %v, want %v", task.GetMaxBudgetUsd(), budget)
	}
	if !slices.Equal(task.GetWriteScopes(), scopes) {
		t.Errorf("write scopes: %v, want %v", task.GetWriteScopes(), scopes)
	}

	// Verify prompt in Get
	got := e.get(t, first.GetId())
	if got.GetPrompt() != newPrompt {
		t.Errorf("get prompt: %q, want %q", got.GetPrompt(), newPrompt)
	}

	// Verify history contains both prompt events
	events, err := store.List[*planv1.TaskEvent](t.Context(), e.db, store.Where{"task_id": first.GetId()})
	if err != nil {
		t.Fatal(err)
	}
	var promptTexts []string
	for _, ev := range events {
		if ev.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_PROMPT {
			promptTexts = append(promptTexts, ev.GetText())
		}
	}
	if len(promptTexts) != 2 || promptTexts[0] != initialPrompt || promptTexts[1] != newPrompt {
		t.Errorf("prompt history: %v, want [%q, %q]", promptTexts, initialPrompt, newPrompt)
	}

	// dep is STILL waiting on first
	depGot := e.get(t, dep.GetId())
	if !slices.Contains(depGot.GetDependsOn(), first.GetId()) {
		t.Errorf("dep depends on: %v, want to contain %s", depGot.GetDependsOn(), first.GetId())
	}
}

// TestUpdatePlannedOnlyGivenFieldsChange: only the fields given change, other fields stay as they were.
func TestUpdatePlannedOnlyGivenFieldsChange(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	e := up(t, t.TempDir(), WithCapacity((&limit{slots: 0}).capacity), WithTick(time.Hour))
	wishID, _ := e.wish(t, gitRepo(t))

	firstPrompt := "text first"
	initialTitle := "Initial Title"
	task := e.mustSpawn(t, wishID, initialTitle, firstPrompt, &planv1.TaskServiceSpawnRequest{
		Provider: planv1.Provider_PROVIDER_ANTIGRAVITY,
		Model:    "gemini-3.8-flash-high",
		Later:    true,
	})

	// Update only title
	newTitle := "Changed Title"
	res, err := e.tasks.Update(t.Context(), connect.NewRequest(&planv1.TaskServiceUpdateRequest{
		TaskId: task.GetId(),
		Title:  &newTitle,
	}))
	if err != nil {
		t.Fatalf("Update title: %v", err)
	}
	if res.Msg.GetTask().GetTitle() != newTitle {
		t.Errorf("title: %q, want %q", res.Msg.GetTask().GetTitle(), newTitle)
	}
	if res.Msg.GetTask().GetPrompt() != firstPrompt {
		t.Errorf("prompt: %q, want %q", res.Msg.GetTask().GetPrompt(), firstPrompt)
	}
	if res.Msg.GetTask().GetProvider() != planv1.Provider_PROVIDER_ANTIGRAVITY || res.Msg.GetTask().GetModel() != "gemini-3.8-flash-high" {
		t.Errorf("provider/model: %v/%s", res.Msg.GetTask().GetProvider(), res.Msg.GetTask().GetModel())
	}

	// Update only prompt
	secondPrompt := "text second prompt"
	res, err = e.tasks.Update(t.Context(), connect.NewRequest(&planv1.TaskServiceUpdateRequest{
		TaskId: task.GetId(),
		Prompt: &secondPrompt,
	}))
	if err != nil {
		t.Fatalf("Update prompt: %v", err)
	}
	if res.Msg.GetTask().GetPrompt() != secondPrompt {
		t.Errorf("prompt: %q, want %q", res.Msg.GetTask().GetPrompt(), secondPrompt)
	}
	if res.Msg.GetTask().GetTitle() != newTitle {
		t.Errorf("title: %q, want %q", res.Msg.GetTask().GetTitle(), newTitle)
	}

	// Update only model
	customModel := "custom-model-v1"
	res, err = e.tasks.Update(t.Context(), connect.NewRequest(&planv1.TaskServiceUpdateRequest{
		TaskId: task.GetId(),
		Model:  &customModel,
	}))
	if err != nil {
		t.Fatalf("Update model: %v", err)
	}
	if res.Msg.GetTask().GetModel() != customModel {
		t.Errorf("model: %q, want %q", res.Msg.GetTask().GetModel(), customModel)
	}

	// Clear model with ""
	emptyModel := ""
	res, err = e.tasks.Update(t.Context(), connect.NewRequest(&planv1.TaskServiceUpdateRequest{
		TaskId: task.GetId(),
		Model:  &emptyModel,
	}))
	if err != nil {
		t.Fatalf("Update empty model: %v", err)
	}
	if res.Msg.GetTask().GetModel() != "" {
		t.Errorf("model after clear: %q, want empty", res.Msg.GetTask().GetModel())
	}

	// Change provider without model: resets to provider default model
	claude := planv1.Provider_PROVIDER_CLAUDE
	res, err = e.tasks.Update(t.Context(), connect.NewRequest(&planv1.TaskServiceUpdateRequest{
		TaskId:   task.GetId(),
		Provider: &claude,
	}))
	if err != nil {
		t.Fatalf("Update provider: %v", err)
	}
	if res.Msg.GetTask().GetProvider() != planv1.Provider_PROVIDER_CLAUDE {
		t.Errorf("provider: %v, want claude", res.Msg.GetTask().GetProvider())
	}
	if res.Msg.GetTask().GetModel() != "claude-sonnet-5-5" {
		t.Errorf("model: %q, want default claude-sonnet-5-5", res.Msg.GetTask().GetModel())
	}
}

// TestUpdateNothingGiven: error when no fields are provided.
func TestUpdateNothingGiven(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	e := up(t, t.TempDir())
	wishID, _ := e.wish(t, gitRepo(t))

	task := e.mustSpawn(t, wishID, "Planned", "text ok", &planv1.TaskServiceSpawnRequest{Later: true})

	_, err := e.tasks.Update(t.Context(), connect.NewRequest(&planv1.TaskServiceUpdateRequest{
		TaskId: task.GetId(),
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("err: %v, want InvalidArgument", err)
	}
	if !strings.Contains(err.Error(), "nothing to update: give at least one field") {
		t.Errorf("err: %q, want containing 'nothing to update: give at least one field'", err.Error())
	}
}

// TestUpdateRefusedCycleAndDraftAzima: self-wait, cycle in dependencies, and draft azima are refused.
func TestUpdateRefusedCycleAndDraftAzima(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	ctx := t.Context()
	e := up(t, t.TempDir(), WithCapacity((&limit{slots: 0}).capacity), WithTick(time.Hour))
	wishID, _ := e.wish(t, gitRepo(t))

	taskA := e.mustSpawn(t, wishID, "Task A", "text A", &planv1.TaskServiceSpawnRequest{Later: true})
	taskB := e.mustSpawn(t, wishID, "Task B", "text B", &planv1.TaskServiceSpawnRequest{
		Later: true,
		After: []string{taskA.GetCode()},
	})

	// Self-wait
	_, err := e.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{
		TaskId: taskA.GetId(),
		After:  []string{taskA.GetCode()},
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("self-wait err: %v, want InvalidArgument", err)
	}
	wantSelfErr := "task " + taskA.GetCode() + " cannot wait for itself"
	if !strings.Contains(err.Error(), wantSelfErr) {
		t.Errorf("err: %q, want %q", err.Error(), wantSelfErr)
	}

	// Dependency cycle: B waits on A, update A to wait on B
	_, err = e.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{
		TaskId: taskA.GetId(),
		After:  []string{taskB.GetCode()},
	}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("cycle err: %v, want FailedPrecondition", err)
	}
	if !strings.Contains(err.Error(), "the tasks of a wish form no cycle") {
		t.Errorf("err: %q, want containing 'the tasks of a wish form no cycle'", err.Error())
	}

	// Draft azima refusal
	azRes, err := e.tasks.Spawn(ctx, connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID,
		Title:  "Draft Azima",
		Kind:   planv1.TaskKind_TASK_KIND_AZIMA,
		Draft:  true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	draftAzima := azRes.Msg.GetTask()

	draftCode := draftAzima.GetCode()
	_, err = e.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{
		TaskId: taskA.GetId(),
		PartOf: &draftCode,
	}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("draft azima err: %v, want FailedPrecondition", err)
	}
	wantDraftErr := "azima " + draftCode + " is a draft: open it first with djinn task open " + draftCode
	if !strings.Contains(err.Error(), wantDraftErr) {
		t.Errorf("err: %q, want %q", err.Error(), wantDraftErr)
	}
}

// TestUpdateRunningAndPausedRules: running or paused task may change title only; prompt uses djinn task send; the rest after it ends.
func TestUpdateRunningAndPausedRules(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	ctx := t.Context()
	e := up(t, t.TempDir())
	wishID, _ := e.wish(t, gitRepo(t))

	// Spawn a worker that waits for a message (RUNNING)
	task := e.spawn(t, wishID, "wait")
	if task.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
		t.Fatalf("task status: %v, want running", task.GetStatus())
	}

	// Running task: update title succeeds
	newTitle := "Running Title Updated"
	res, err := e.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{
		TaskId: task.GetId(),
		Title:  &newTitle,
	}))
	if err != nil {
		t.Fatalf("update title while running: %v", err)
	}
	if res.Msg.GetTask().GetTitle() != newTitle {
		t.Errorf("title: %q, want %q", res.Msg.GetTask().GetTitle(), newTitle)
	}

	// Running task: update prompt fails with "use djinn task send"
	p := "new prompt"
	_, err = e.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{
		TaskId: task.GetId(),
		Prompt: &p,
	}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("update prompt while running err: %v, want FailedPrecondition", err)
	}
	wantPromptErr := "task " + task.GetCode() + " is running: use djinn task send"
	if !strings.Contains(err.Error(), wantPromptErr) {
		t.Errorf("err: %q, want %q", err.Error(), wantPromptErr)
	}

	// Running task: update provider/model/after fails with "the rest after it ends"
	claude := planv1.Provider_PROVIDER_CLAUDE
	_, err = e.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{
		TaskId:   task.GetId(),
		Provider: &claude,
	}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("update provider while running err: %v, want FailedPrecondition", err)
	}
	wantRestErr := "task " + task.GetCode() + " is running: the rest after it ends"
	if !strings.Contains(err.Error(), wantRestErr) {
		t.Errorf("err: %q, want %q", err.Error(), wantRestErr)
	}

	// Pause the worker (PAUSED)
	_, err = e.tasks.Pause(ctx, connect.NewRequest(&planv1.TaskServicePauseRequest{TaskId: task.GetId()}))
	if err != nil {
		t.Fatalf("pause: %v", err)
	}

	// Paused task: update title succeeds
	pausedTitle := "Paused Title Updated"
	res, err = e.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{
		TaskId: task.GetId(),
		Title:  &pausedTitle,
	}))
	if err != nil {
		t.Fatalf("update title while paused: %v", err)
	}
	if res.Msg.GetTask().GetTitle() != pausedTitle {
		t.Errorf("title: %q, want %q", res.Msg.GetTask().GetTitle(), pausedTitle)
	}

	// Paused task: update prompt fails with "use djinn task send"
	_, err = e.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{
		TaskId: task.GetId(),
		Prompt: &p,
	}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("update prompt while paused err: %v, want FailedPrecondition", err)
	}
	wantPausedPromptErr := "task " + task.GetCode() + " is paused: use djinn task send"
	if !strings.Contains(err.Error(), wantPausedPromptErr) {
		t.Errorf("err: %q, want %q", err.Error(), wantPausedPromptErr)
	}

	// Paused task: update provider fails with "the rest after it ends"
	_, err = e.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{
		TaskId:   task.GetId(),
		Provider: &claude,
	}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("update provider while paused err: %v, want FailedPrecondition", err)
	}
	wantPausedRestErr := "task " + task.GetCode() + " is paused: the rest after it ends"
	if !strings.Contains(err.Error(), wantPausedRestErr) {
		t.Errorf("err: %q, want %q", err.Error(), wantPausedRestErr)
	}
}

// TestUpdateFinishedTaskRules: finished task (done, failed, stopped, cut short) may change title,
// provider/model, prompt (for next continue), and azima; never its dependencies once it ran;
// decision/budget/scopes refused.
func TestUpdateFinishedTaskRules(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	ctx := t.Context()
	e := up(t, t.TempDir())
	wishID, _ := e.wish(t, gitRepo(t))

	// Create an open azima to move into
	azRes, err := e.tasks.Spawn(ctx, connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID,
		Title:  "Phase 2 Azima",
		Kind:   planv1.TaskKind_TASK_KIND_AZIMA,
	}))
	if err != nil {
		t.Fatal(err)
	}
	azima := azRes.Msg.GetTask()

	anotherTask := e.mustSpawn(t, wishID, "Another", "text", &planv1.TaskServiceSpawnRequest{Later: true})

	// Spawn a task that fails
	first := e.ended(t, e.mustSpawn(t, wishID, "Failed task", "fail tests broke", nil).GetId())
	if first.GetStatus() != planv1.TaskStatus_TASK_STATUS_FAILED {
		t.Fatalf("task status: %v, want failed", first.GetStatus())
	}

	// Update dependencies once it ran: refused
	_, err = e.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{
		TaskId: first.GetId(),
		After:  []string{anotherTask.GetCode()},
	}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("update dependencies on finished task: err %v, want FailedPrecondition", err)
	}
	wantDepErr := "task " + first.GetCode() + " has run: never its dependencies once it ran"
	if !strings.Contains(err.Error(), wantDepErr) {
		t.Errorf("err: %q, want %q", err.Error(), wantDepErr)
	}

	// Update decision / budget / scopes on finished task: refused
	budget := 10.0
	_, err = e.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{
		TaskId:       first.GetId(),
		MaxBudgetUsd: &budget,
	}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("update budget on finished task: err %v, want FailedPrecondition", err)
	}
	wantBudgetErr := "task " + first.GetCode() + " has run: only title, prompt, provider, model and azima may change"
	if !strings.Contains(err.Error(), wantBudgetErr) {
		t.Errorf("err: %q, want %q", err.Error(), wantBudgetErr)
	}

	// Update title, provider, model, prompt, azima: ALL succeed on finished task
	newTitle := "Failed Task Renamed"
	newPrompt := "text new prompt for continue"
	agy := planv1.Provider_PROVIDER_ANTIGRAVITY
	model := "gemini-3.8-flash-high"
	partOf := azima.GetCode()

	res, err := e.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{
		TaskId:   first.GetId(),
		Title:    &newTitle,
		Prompt:   &newPrompt,
		Provider: &agy,
		Model:    &model,
		PartOf:   &partOf,
	}))
	if err != nil {
		t.Fatalf("update finished task allowed fields: %v", err)
	}
	task := res.Msg.GetTask()
	if task.GetTitle() != newTitle || task.GetPrompt() != newPrompt ||
		task.GetProvider() != agy || task.GetModel() != model || task.GetPartOf() != azima.GetId() {
		t.Errorf("updated finished task: %v", task)
	}
}

// TestUpdateFinishedPromptContinueUsesNewPrompt: updating prompt on a finished task makes subsequent
// continue without prompt use that prompt for the continue turn, without duplicate prompt events.
func TestUpdateFinishedPromptContinueUsesNewPrompt(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	ctx := t.Context()
	spy := &spyProvider{}
	providers := map[planv1.Provider]Provider{
		planv1.Provider_PROVIDER_FAKE: spy,
	}
	e := upWith(t, t.TempDir(), providers)
	wishID, _ := e.wish(t, gitRepo(t))

	// Spawn a task that fails
	firstPrompt := "text first run\nfail broke"
	first := e.ended(t, e.mustSpawn(t, wishID, "Failing task", firstPrompt, nil).GetId())
	if first.GetStatus() != planv1.TaskStatus_TASK_STATUS_FAILED {
		t.Fatalf("status: %v, want failed", first.GetStatus())
	}

	// Update prompt
	updatedPrompt := "text continued run\nusage 100 10 0.01"
	_, err := e.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{
		TaskId: first.GetId(),
		Prompt: &updatedPrompt,
	}))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Verify get shows the new prompt
	got := e.get(t, first.GetId())
	if got.GetPrompt() != updatedPrompt {
		t.Errorf("get prompt: %q, want %q", got.GetPrompt(), updatedPrompt)
	}

	// Continue WITHOUT prompt
	cont, err := e.tasks.Continue(ctx, connect.NewRequest(&planv1.TaskServiceContinueRequest{
		TaskId: first.GetId(),
	}))
	if err != nil {
		t.Fatalf("Continue: %v", err)
	}
	if cont.Msg.GetTask().GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
		t.Errorf("status after continue: %v, want running", cont.Msg.GetTask().GetStatus())
	}

	// Let it finish
	done := e.ended(t, first.GetId())
	if done.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Errorf("status: %v, want done", done.GetStatus())
	}

	// Check worker spec received the updated prompt
	spec := spy.lastSpec()
	if !strings.Contains(spec.Prompt, "text continued run") {
		t.Errorf("worker Prompt: %q, want to contain 'text continued run'", spec.Prompt)
	}

	// Events check: exactly 2 PROMPT events (the initial spawn prompt, and the update prompt)
	events := e.watch(ctx, t, first.GetId(), 0)
	var promptCount int
	for _, ev := range events {
		if ev.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_PROMPT {
			promptCount++
		}
	}
	if promptCount != 2 {
		t.Errorf("PROMPT events count: %d, want 2", promptCount)
	}
}

// TestUpdateFailedContinuedStartsNewSession: a failed task continued after provider change starts on the new
// provider from its first prompt.
func TestUpdateFailedContinuedStartsNewSession(t *testing.T) {
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
	_, err := e.tasks.Update(t.Context(), connect.NewRequest(&planv1.TaskServiceUpdateRequest{
		TaskId:   first.GetId(),
		Provider: &agy,
		Model:    &model,
	}))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Continue the task with a prompt
	contPrompt := "text continue prompt"
	cont, err := e.tasks.Continue(t.Context(), connect.NewRequest(&planv1.TaskServiceContinueRequest{
		TaskId: first.GetId(),
		Prompt: &contPrompt,
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
	}
}

// TestUpdateProviderChangeResetsModel: when a task ran on a provider that recorded a model from the stream,
// changing provider without --model resets the model to the new provider's default and clears the recorded model.
func TestUpdateProviderChangeResetsModel(t *testing.T) {
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

	// Spawn a task on Antigravity that records gemini-3.8-flash-high from its stream and fails.
	firstPrompt := "model gemini-3.8-flash-high\ntext working\nfail Antigravity failed"
	first := e.ended(t, e.mustSpawn(t, wishID, "Antigravity task", firstPrompt, &planv1.TaskServiceSpawnRequest{
		Provider: planv1.Provider_PROVIDER_ANTIGRAVITY,
	}).GetId())
	if first.GetStatus() != planv1.TaskStatus_TASK_STATUS_FAILED {
		t.Fatalf("task status: %v, want failed", first.GetStatus())
	}
	if got, want := first.GetModel(), "gemini-3.8-flash-high"; got != want {
		t.Fatalf("task recorded model: %q, want %q", got, want)
	}

	// Change agent to claude without --model: model resets to claude default and output shows it.
	claude := planv1.Provider_PROVIDER_CLAUDE
	res, err := e.tasks.Update(t.Context(), connect.NewRequest(&planv1.TaskServiceUpdateRequest{
		TaskId:   first.GetId(),
		Provider: &claude,
	}))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got, want := res.Msg.GetTask().GetProvider(), planv1.Provider_PROVIDER_CLAUDE; got != want {
		t.Errorf("response provider: %v, want %v", got, want)
	}
	if got, want := res.Msg.GetTask().GetModel(), "claude-sonnet-5-5"; got != want {
		t.Errorf("response model: %q, want %q", got, want)
	}

	// Stored task has new provider and default model.
	stored := e.get(t, first.GetId())
	if got, want := stored.GetProvider(), planv1.Provider_PROVIDER_CLAUDE; got != want {
		t.Errorf("stored provider: %v, want %v", got, want)
	}
	if got, want := stored.GetModel(), "claude-sonnet-5-5"; got != want {
		t.Errorf("stored model: %q, want %q", got, want)
	}
}

// TestUpdateProviderChangeExplicitModelKept: when changing provider with an explicit model,
// that model is kept.
func TestUpdateProviderChangeExplicitModelKept(t *testing.T) {
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

	first := e.ended(t, e.mustSpawn(t, wishID, "Antigravity task", "model gemini-3.8-flash-high\nfail broke", &planv1.TaskServiceSpawnRequest{
		Provider: planv1.Provider_PROVIDER_ANTIGRAVITY,
	}).GetId())

	// Update with explicit model
	claude := planv1.Provider_PROVIDER_CLAUDE
	explicitModel := "claude-3-5-haiku-20241022"
	res, err := e.tasks.Update(t.Context(), connect.NewRequest(&planv1.TaskServiceUpdateRequest{
		TaskId:   first.GetId(),
		Provider: &claude,
		Model:    &explicitModel,
	}))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got, want := res.Msg.GetTask().GetModel(), explicitModel; got != want {
		t.Errorf("model: %q, want %q", got, want)
	}
	stored := e.get(t, first.GetId())
	if got, want := stored.GetModel(), explicitModel; got != want {
		t.Errorf("stored model: %q, want %q", got, want)
	}
}

// TestUpdateWatcher: updating provider or model on a watcher is refused.
func TestUpdateWatcher(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	e := up(t, t.TempDir())
	wishID, _ := e.wish(t, gitRepo(t))

	watcher := e.mustSpawn(t, wishID, "Watcher task", "echo hi", &planv1.TaskServiceSpawnRequest{
		Provider: planv1.Provider_PROVIDER_WATCH,
		Later:    true,
	})

	claude := planv1.Provider_PROVIDER_CLAUDE
	_, err := e.tasks.Update(t.Context(), connect.NewRequest(&planv1.TaskServiceUpdateRequest{
		TaskId:   watcher.GetId(),
		Provider: &claude,
	}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("update watcher provider err: %v, want FailedPrecondition", err)
	}
	wantWatcherErr := "task " + watcher.GetCode() + " is a watcher: no agent runs it"
	if !strings.Contains(err.Error(), wantWatcherErr) {
		t.Errorf("err: %q, want %q", err.Error(), wantWatcherErr)
	}
}

// TestUpdateAzimaRefused: calling update on an azima task is refused.
func TestUpdateAzimaRefused(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	e := up(t, t.TempDir())
	wishID, _ := e.wish(t, gitRepo(t))

	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID,
		Title:  "Phase 1 Azima",
		Kind:   planv1.TaskKind_TASK_KIND_AZIMA,
	}))
	if err != nil {
		t.Fatal(err)
	}
	azima := res.Msg.GetTask()

	newTitle := "New Azima Title"
	_, err = e.tasks.Update(t.Context(), connect.NewRequest(&planv1.TaskServiceUpdateRequest{
		TaskId: azima.GetId(),
		Title:  &newTitle,
	}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("update azima err: %v, want FailedPrecondition", err)
	}
	wantAzimaErr := "task " + azima.GetCode() + " is an azima of the plan: no worker runs it"
	if !strings.Contains(err.Error(), wantAzimaErr) {
		t.Errorf("err: %q, want %q", err.Error(), wantAzimaErr)
	}
}

// TestUpdateJournal: TaskService.Update records a single journal entry.
func TestUpdateJournal(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	e := up(t, t.TempDir(), WithCapacity((&limit{slots: 0}).capacity), WithTick(time.Hour))
	wishID, _ := e.wish(t, gitRepo(t))

	task := e.mustSpawn(t, wishID, "Planned", "text first", &planv1.TaskServiceSpawnRequest{Later: true})

	newTitle := "Journaled Title"
	newPrompt := "text journaled prompt"
	_, err := e.tasks.Update(t.Context(), connect.NewRequest(&planv1.TaskServiceUpdateRequest{
		TaskId: task.GetId(),
		Title:  &newTitle,
		Prompt: &newPrompt,
	}))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	cmds, err := store.Commands(t.Context(), e.db, func(c store.Command) bool {
		return c.Method == planv1connect.TaskServiceUpdateProcedure
	})
	if err != nil || len(cmds) != 1 {
		t.Errorf("journaled %d Update commands (%v), want 1", len(cmds), err)
	}
}

// TestUpdateCLIAndAlias: djinn task update and the alias djinn task set-agent from the command line.
func TestUpdateCLIAndAlias(t *testing.T) {
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

	// djinn task update updates title and prompt
	out.Reset()
	errs.Reset()
	code = cli.Run(ctx, []string{"task", "update", task.GetId(), "--title", "CLI Updated Title", "--prompt", "text updated prompt CLI"},
		cli.Config{Version: "test", Addr: e.srv.URL, HTTP: e.srv.Client(), Stdout: &out, Stderr: &errs})
	if code != 0 || !strings.Contains(out.String(), "title: CLI Updated Title") || !strings.Contains(out.String(), "prompt: text updated prompt CLI") {
		t.Errorf("djinn task update: code %d\nstdout:\n%s\nstderr:\n%s", code, out.String(), errs.String())
	}

	// djinn task set-agent alias changes provider and model
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

	// djinn task set-agent --provider claude (no --model) resets model to claude's default and command output says it
	out.Reset()
	errs.Reset()
	code = cli.Run(ctx, []string{"task", "set-agent", task.GetId(), "--provider", "claude"},
		cli.Config{Version: "test", Addr: e.srv.URL, HTTP: e.srv.Client(), Stdout: &out, Stderr: &errs})
	if code != 0 || !strings.Contains(out.String(), "provider: claude") || !strings.Contains(out.String(), "model: claude-sonnet-5-5") {
		t.Errorf("djinn task set-agent --provider claude: code %d\nstdout:\n%s\nstderr:\n%s", code, out.String(), errs.String())
	}
	got = e.get(t, task.GetId())
	if got.GetModel() != "claude-sonnet-5-5" {
		t.Errorf("model after provider change: %q, want %q", got.GetModel(), "claude-sonnet-5-5")
	}
}

// TestContinueWithForeignModelInStoreSanitizes: if a task in the store has a foreign model
// recorded from another provider, Continue sanitizes it to the current provider's default model.
func TestContinueWithForeignModelInStoreSanitizes(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	claudeSpy := &spyProvider{}
	providers := map[planv1.Provider]Provider{
		planv1.Provider_PROVIDER_CLAUDE: claudeSpy,
		planv1.Provider_PROVIDER_FAKE:   Fake{},
		planv1.Provider_PROVIDER_WATCH:  fastWatch,
	}
	e := upWith(t, t.TempDir(), providers)
	wishID, _ := e.wish(t, gitRepo(t))

	first := e.ended(t, e.mustSpawn(t, wishID, "Failed claude task", "fail broke", &planv1.TaskServiceSpawnRequest{
		Provider: planv1.Provider_PROVIDER_CLAUDE,
	}).GetId())

	// Manually inject a foreign model into the store (simulating an old task or foreign stream record).
	task := e.get(t, first.GetId())
	task.Model = "gemini-3.8-flash-high"
	if err := e.db.Tx(t.Context(), func(tx *store.Tx) error {
		if err := tx.Journal("test", "test/put", task); err != nil {
			return err
		}
		return tx.Put(task)
	}); err != nil {
		t.Fatal(err)
	}

	// Continue the task: Continue sanitizes the model.
	p := "text continue"
	cont, err := e.tasks.Continue(t.Context(), connect.NewRequest(&planv1.TaskServiceContinueRequest{
		TaskId: first.GetId(),
		Prompt: &p,
	}))
	if err != nil {
		t.Fatalf("continue: %v", err)
	}
	if got, want := cont.Msg.GetTask().GetModel(), "claude-sonnet-5-5"; got != want {
		t.Errorf("continued response model: %q, want %q", got, want)
	}
	e.ended(t, first.GetId())

	spec := claudeSpy.lastSpec()
	if got, want := spec.Model, "claude-sonnet-5-5"; got != want {
		t.Errorf("worker spec.Model: %q, want %q", got, want)
	}
}

package harness

import (
	"slices"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// TestTaskModelRecorded: the model reported by a worker's stream (in its init event) is recorded on the task.
func TestTaskModelRecorded(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	t.Run("claude", func(t *testing.T) {
		t.Parallel()
		claudeEnv, _, _ := fake{provider: "claude", fixture: "success", end: "eof"}.env(t)
		providers, _ := claudePlaying(claudeEnv)
		e := upWith(t, t.TempDir(), providers)
		wishID, _ := e.wish(t, gitRepo(t))

		res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
			WishId: wishID, Title: "Claude task", Prompt: "x", Provider: planv1.Provider_PROVIDER_CLAUDE,
		}))
		if err != nil {
			t.Fatal(err)
		}
		id := res.Msg.GetTask().GetId()
		task := e.ended(t, id)
		if got, want := task.GetModel(), "claude-sonnet-4-5"; got != want {
			t.Errorf("model = %q, want %q", got, want)
		}
	})

	t.Run("antigravity", func(t *testing.T) {
		t.Parallel()
		agyEnv, _, _ := fake{provider: "antigravity", fixture: "success", end: "eof"}.env(t)
		providers, _ := antigravityPlaying(agyEnv)
		e := upWith(t, t.TempDir(), providers)
		wishID, _ := e.wish(t, gitRepo(t))

		res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
			WishId: wishID, Title: "Antigravity task", Prompt: "x", Provider: planv1.Provider_PROVIDER_ANTIGRAVITY,
		}))
		if err != nil {
			t.Fatal(err)
		}
		id := res.Msg.GetTask().GetId()
		task := e.ended(t, id)
		if got, want := task.GetModel(), "gemini-3.8-flash-medium"; got != want {
			t.Errorf("model = %q, want %q", got, want)
		}
	})
}

// TestTaskModelDefault: when the stream does not report a model, the requested model is kept,
// else the provider's default as Djinn knows it; watchers have no model.
func TestTaskModelDefault(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	if got, want := DefaultModel(planv1.Provider_PROVIDER_CLAUDE), "claude-sonnet-5-5"; got != want {
		t.Errorf("claude default = %q, want %q", got, want)
	}
	if got, want := DefaultModel(planv1.Provider_PROVIDER_ANTIGRAVITY), "gemini-3.8-flash-high"; got != want {
		t.Errorf("antigravity default = %q, want %q", got, want)
	}
	if got, want := DefaultModel(planv1.Provider_PROVIDER_CODEX), "gpt-5.5"; got != want {
		t.Errorf("codex default = %q, want %q", got, want)
	}
	if got, want := DefaultModel(planv1.Provider_PROVIDER_WATCH), ""; got != want {
		t.Errorf("watch default = %q, want %q", got, want)
	}

	e := up(t, t.TempDir())
	wishID, _ := e.wish(t, t.TempDir())

	// A worker with requested model keeps it when the stream never says otherwise.
	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Requested model", Prompt: "x", Provider: planv1.Provider_PROVIDER_FAKE, Model: "my-custom-model",
	}))
	if err != nil {
		t.Fatal(err)
	}
	task := e.ended(t, res.Msg.GetTask().GetId())
	if got, want := task.GetModel(), "my-custom-model"; got != want {
		t.Errorf("requested model = %q, want %q", got, want)
	}

	// A watcher has no model.
	repo := gitRepo(t)
	watchWishID, _ := e.wish(t, repo)
	watchRes, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: watchWishID, Title: "Watcher", Prompt: watchCommand("exit:0"), Provider: planv1.Provider_PROVIDER_WATCH,
	}))
	if err != nil {
		t.Fatal(err)
	}
	watchTask := e.ended(t, watchRes.Msg.GetTask().GetId())
	if got, want := watchTask.GetModel(), ""; got != want {
		t.Errorf("watch model = %q, want %q", got, want)
	}
}

// TestForeignModel: foreignModel recognizes model identifiers belonging to other providers,
// but leaves custom/unknown models and empty strings alone.
func TestForeignModel(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	cases := []struct {
		provider planv1.Provider
		model    string
		want     bool
	}{
		// Claude provider
		{planv1.Provider_PROVIDER_CLAUDE, "claude-sonnet-5-5", false},
		{planv1.Provider_PROVIDER_CLAUDE, "claude-sonnet-4-5", false},
		{planv1.Provider_PROVIDER_CLAUDE, "claude-3-7-sonnet", false},
		{planv1.Provider_PROVIDER_CLAUDE, "claude-opus-4-0", false},
		{planv1.Provider_PROVIDER_CLAUDE, "sonnet", false},
		{planv1.Provider_PROVIDER_CLAUDE, "opus", false},
		{planv1.Provider_PROVIDER_CLAUDE, "haiku", false},
		{planv1.Provider_PROVIDER_CLAUDE, "anthropic/claude-3.5-sonnet", false},
		{planv1.Provider_PROVIDER_CLAUDE, "gemini-3.8-flash-high", true},
		{planv1.Provider_PROVIDER_CLAUDE, "gemini-3.8-flash-medium", true},
		{planv1.Provider_PROVIDER_CLAUDE, "google/gemini-2.5-flash", true},
		{planv1.Provider_PROVIDER_CLAUDE, "gpt-5.1-codex", true},
		{planv1.Provider_PROVIDER_CLAUDE, "gpt-5.5", true},
		{planv1.Provider_PROVIDER_CLAUDE, "openai/gpt-4o", true},
		{planv1.Provider_PROVIDER_CLAUDE, "o1-mini", true},
		{planv1.Provider_PROVIDER_CLAUDE, "o3", true},
		{planv1.Provider_PROVIDER_CLAUDE, "my-custom-model", false},
		{planv1.Provider_PROVIDER_CLAUDE, "", false},

		// Antigravity provider
		{planv1.Provider_PROVIDER_ANTIGRAVITY, "gemini-3.8-flash-high", false},
		{planv1.Provider_PROVIDER_ANTIGRAVITY, "gemini-3.8-flash-medium", false},
		{planv1.Provider_PROVIDER_ANTIGRAVITY, "google/gemini-pro", false},
		{planv1.Provider_PROVIDER_ANTIGRAVITY, "claude-sonnet-5-5", true},
		{planv1.Provider_PROVIDER_ANTIGRAVITY, "sonnet", true},
		{planv1.Provider_PROVIDER_ANTIGRAVITY, "opus", true},
		{planv1.Provider_PROVIDER_ANTIGRAVITY, "gpt-5.5", true},
		{planv1.Provider_PROVIDER_ANTIGRAVITY, "o3", true},
		{planv1.Provider_PROVIDER_ANTIGRAVITY, "my-custom-model", false},
		{planv1.Provider_PROVIDER_ANTIGRAVITY, "", false},

		// Codex provider
		{planv1.Provider_PROVIDER_CODEX, "gpt-5.5", false},
		{planv1.Provider_PROVIDER_CODEX, "gpt-5.1-codex", false},
		{planv1.Provider_PROVIDER_CODEX, "o1-mini", false},
		{planv1.Provider_PROVIDER_CODEX, "claude-sonnet-5-5", true},
		{planv1.Provider_PROVIDER_CODEX, "gemini-3.8-flash-high", true},
		{planv1.Provider_PROVIDER_CODEX, "my-custom-model", false},
		{planv1.Provider_PROVIDER_CODEX, "", false},

		// Watch provider has no model
		{planv1.Provider_PROVIDER_WATCH, "claude-sonnet-5-5", true},
		{planv1.Provider_PROVIDER_WATCH, "gemini-3.8-flash-high", true},
		{planv1.Provider_PROVIDER_WATCH, "", false},

		// Fake provider allows any model
		{planv1.Provider_PROVIDER_FAKE, "claude-sonnet-5-5", false},
		{planv1.Provider_PROVIDER_FAKE, "gemini-3.8-flash-high", false},
		{planv1.Provider_PROVIDER_FAKE, "gpt-5.5", false},
		{planv1.Provider_PROVIDER_FAKE, "", false},

		// Unspecified provider defaults to Claude
		{planv1.Provider_PROVIDER_UNSPECIFIED, "claude-sonnet-5-5", false},
		{planv1.Provider_PROVIDER_UNSPECIFIED, "gemini-3.8-flash-high", true},
		{planv1.Provider_PROVIDER_UNSPECIFIED, "gpt-5.5", true},
	}

	for _, c := range cases {
		if got := foreignModel(c.provider, c.model); got != c.want {
			t.Errorf("foreignModel(%v, %q) = %v; want %v", c.provider, c.model, got, c.want)
		}
	}
}

// TestTaskModelKeptAfterEnd checks that finished tasks (done, failed, stopped)
// keep the model they actually ran in task.Model and task.Models.
func TestTaskModelKeptAfterEnd(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	t.Run("done task keeps recorded model", func(t *testing.T) {
		t.Parallel()
		e := up(t, t.TempDir())
		wishID, _ := e.wish(t, gitRepo(t))
		res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
			WishId: wishID, Title: "Done task", Prompt: "model claude-sonnet-4-5\ntext done", Provider: planv1.Provider_PROVIDER_FAKE,
		}))
		if err != nil {
			t.Fatal(err)
		}
		task := e.ended(t, res.Msg.GetTask().GetId())
		if task.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
			t.Fatalf("status = %v, want DONE", task.GetStatus())
		}
		if got, want := task.GetModel(), "claude-sonnet-4-5"; got != want {
			t.Errorf("model = %q, want %q", got, want)
		}
		if got, want := task.GetModels(), []string{"claude-sonnet-4-5"}; !slices.Equal(got, want) {
			t.Errorf("models = %v, want %v", got, want)
		}
	})

	t.Run("failed task keeps recorded model", func(t *testing.T) {
		t.Parallel()
		e := up(t, t.TempDir())
		wishID, _ := e.wish(t, gitRepo(t))
		res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
			WishId: wishID, Title: "Failed task", Prompt: "model gemini-3.8-flash-medium\nfail boom", Provider: planv1.Provider_PROVIDER_FAKE,
		}))
		if err != nil {
			t.Fatal(err)
		}
		task := e.ended(t, res.Msg.GetTask().GetId())
		if task.GetStatus() != planv1.TaskStatus_TASK_STATUS_FAILED {
			t.Fatalf("status = %v, want FAILED", task.GetStatus())
		}
		if got, want := task.GetModel(), "gemini-3.8-flash-medium"; got != want {
			t.Errorf("model = %q, want %q", got, want)
		}
		if got, want := task.GetModels(), []string{"gemini-3.8-flash-medium"}; !slices.Equal(got, want) {
			t.Errorf("models = %v, want %v", got, want)
		}
	})

	t.Run("stopped task keeps recorded model", func(t *testing.T) {
		t.Parallel()
		e := up(t, t.TempDir())
		wishID, _ := e.wish(t, gitRepo(t))
		res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
			WishId: wishID, Title: "Stopped task", Prompt: "model gpt-5.5\nsleep 10s", Provider: planv1.Provider_PROVIDER_FAKE,
		}))
		if err != nil {
			t.Fatal(err)
		}
		id := res.Msg.GetTask().GetId()
		waitFor(t, "task running with model", func() bool {
			task := e.get(t, id)
			return task.GetStatus() == planv1.TaskStatus_TASK_STATUS_RUNNING && task.GetModel() == "gpt-5.5"
		})
		if _, err := e.tasks.Stop(t.Context(), connect.NewRequest(&planv1.TaskServiceStopRequest{TaskId: id})); err != nil {
			t.Fatal(err)
		}
		task := e.ended(t, id)
		if task.GetStatus() != planv1.TaskStatus_TASK_STATUS_STOPPED {
			t.Fatalf("status = %v, want STOPPED", task.GetStatus())
		}
		if got, want := task.GetModel(), "gpt-5.5"; got != want {
			t.Errorf("model = %q, want %q", got, want)
		}
		if got, want := task.GetModels(), []string{"gpt-5.5"}; !slices.Equal(got, want) {
			t.Errorf("models = %v, want %v", got, want)
		}
	})
}

// TestTaskMultiModelAcrossResumes checks that when a task runs across multiple models,
// its task.Models contains all distinct models in order, and task.Model is the last one.
func TestTaskMultiModelAcrossResumes(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	e := up(t, t.TempDir())
	wishID, _ := e.wish(t, gitRepo(t))

	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Multi-model task", Prompt: "model claude-sonnet-4-5\ntext first turn", Provider: planv1.Provider_PROVIDER_FAKE,
	}))
	if err != nil {
		t.Fatal(err)
	}
	id := res.Msg.GetTask().GetId()
	task1 := e.ended(t, id)
	if got, want := task1.GetModel(), "claude-sonnet-4-5"; got != want {
		t.Errorf("first turn model = %q, want %q", got, want)
	}
	if got, want := task1.GetModels(), []string{"claude-sonnet-4-5"}; !slices.Equal(got, want) {
		t.Errorf("first turn models = %v, want %v", got, want)
	}

	// Continue with second model
	if _, err := e.continueTask(t, id, "model claude-sonnet-5-5\ntext second turn"); err != nil {
		t.Fatal(err)
	}
	task2 := e.ended(t, id)
	if got, want := task2.GetModel(), "claude-sonnet-5-5"; got != want {
		t.Errorf("second turn model = %q, want %q", got, want)
	}
	if got, want := task2.GetModels(), []string{"claude-sonnet-4-5", "claude-sonnet-5-5"}; !slices.Equal(got, want) {
		t.Errorf("second turn models = %v, want %v", got, want)
	}
}

// TestTaskModelPlannedAndWatchers verifies fillModels in List and Get:
// planned tasks forecast their provider's default model, while watchers and azimas have none.
func TestTaskModelPlannedAndWatchers(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	e := up(t, t.TempDir())
	wishID, _ := e.wish(t, t.TempDir())

	planned := &planv1.Task{
		Id:       "01a118d0-0000-7000-8000-000000000001",
		WishId:   wishID,
		Title:    "Planned task",
		Code:     "W1",
		Status:   planv1.TaskStatus_TASK_STATUS_PENDING,
		Provider: planv1.Provider_PROVIDER_CLAUDE,
	}
	watcher := &planv1.Task{
		Id:       "01a118d0-0000-7000-8000-000000000002",
		WishId:   wishID,
		Title:    "Watcher task",
		Code:     "W2",
		Status:   planv1.TaskStatus_TASK_STATUS_PENDING,
		Provider: planv1.Provider_PROVIDER_WATCH,
	}
	azima := &planv1.Task{
		Id:     "01a118d0-0000-7000-8000-000000000003",
		WishId: wishID,
		Title:  "Azima task",
		Code:   "A1",
		Status: planv1.TaskStatus_TASK_STATUS_PENDING,
		Kind:   planv1.TaskKind_TASK_KIND_AZIMA,
	}

	err := e.db.Tx(t.Context(), func(tx *store.Tx) error {
		if err := tx.Journal("test", "test/put", planned); err != nil {
			return err
		}
		if err := tx.Put(planned); err != nil {
			return err
		}
		if err := tx.Journal("test", "test/put", watcher); err != nil {
			return err
		}
		if err := tx.Put(watcher); err != nil {
			return err
		}
		if err := tx.Journal("test", "test/put", azima); err != nil {
			return err
		}
		return tx.Put(azima)
	})
	if err != nil {
		t.Fatal(err)
	}

	// Test Get
	getPlanned := e.get(t, planned.GetId())
	if got, want := getPlanned.GetModel(), DefaultModel(planv1.Provider_PROVIDER_CLAUDE); got != want {
		t.Errorf("planned get model = %q, want %q", got, want)
	}

	getWatcher := e.get(t, watcher.GetId())
	if got := getWatcher.GetModel(); got != "" {
		t.Errorf("watcher get model = %q, want empty", got)
	}

	getAzima := e.get(t, azima.GetId())
	if got := getAzima.GetModel(); got != "" {
		t.Errorf("azima get model = %q, want empty", got)
	}

	// Test List
	listRes, err := e.tasks.List(t.Context(), connect.NewRequest(&planv1.TaskServiceListRequest{WishId: wishID}))
	if err != nil {
		t.Fatal(err)
	}
	for _, tsk := range listRes.Msg.GetTasks() {
		switch tsk.GetId() {
		case planned.GetId():
			if got, want := tsk.GetModel(), DefaultModel(planv1.Provider_PROVIDER_CLAUDE); got != want {
				t.Errorf("planned list model = %q, want %q", got, want)
			}
		case watcher.GetId():
			if got := tsk.GetModel(); got != "" {
				t.Errorf("watcher list model = %q, want empty", got)
			}
		case azima.GetId():
			if got := tsk.GetModel(); got != "" {
				t.Errorf("azima list model = %q, want empty", got)
			}
		}
	}
}

// TestTaskModelAliasResolution checks that when a worker stream emits a versioned model id,
// Djinn dynamically learns the alias -> versioned model mapping.
func TestTaskModelAliasResolution(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	e := up(t, t.TempDir())
	wishID, _ := e.wish(t, t.TempDir())

	if got, ok := e.h.resolveAlias("sonnet"); ok || got != "" {
		t.Errorf("resolveAlias(sonnet) before run = %q, want empty", got)
	}

	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Claude task", Prompt: "model claude-sonnet-5-5\ntext done", Provider: planv1.Provider_PROVIDER_FAKE,
	}))
	if err != nil {
		t.Fatal(err)
	}
	e.ended(t, res.Msg.GetTask().GetId())

	if got, ok := e.h.resolveAlias("sonnet"); !ok || got != "claude-sonnet-5-5" {
		t.Errorf("resolveAlias(sonnet) = (%q, %v), want %q", got, ok, "claude-sonnet-5-5")
	}
}

// TestSummoningCircleWorkerKinds (W400): each kind of worker in the Summoning Circle takes its row's canonical model ID;
// rows set to "Same as lead" follow the lead when it changes; per-task --provider/--model flags still win.
func TestSummoningCircleWorkerKinds(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	home := t.TempDir()
	providers := testProviders()
	providers[planv1.Provider_PROVIDER_CODEX] = Fake{}
	providers[planv1.Provider_PROVIDER_ANTIGRAVITY] = Fake{}
	e := upWith(t, home, providers)
	repo := gitRepo(t)
	wishID, _ := e.wish(t, repo)

	lead := &plan.LeadService{Wishes: &plan.Wishes{Store: e.db, Home: home}}
	setRow := func(kind planv1.WorkerKind, model string) {
		t.Helper()
		if _, err := lead.SetCircle(t.Context(), connect.NewRequest(&planv1.LeadServiceSetCircleRequest{
			Kind:  kind,
			Model: model,
		})); err != nil {
			t.Fatalf("SetCircle(%v, %q): %v", kind, model, err)
		}
	}

	setRow(planv1.WorkerKind_WORKER_KIND_LEAD, "claude-opus-5-8")
	setRow(planv1.WorkerKind_WORKER_KIND_INVESTIGATION, "claude-haiku-5-5")
	setRow(planv1.WorkerKind_WORKER_KIND_MOCKUP, "gemini-4-argon-high")
	setRow(planv1.WorkerKind_WORKER_KIND_IFRIT, "gemini-3.8-flash-high")
	setRow(planv1.WorkerKind_WORKER_KIND_WORK, "claude-sonnet-5-5")
	setRow(planv1.WorkerKind_WORKER_KIND_CORRECTION, "gpt-5.5")
	setRow(planv1.WorkerKind_WORKER_KIND_REVIEW, "gemini-3.8-pro-high")
	setRow(planv1.WorkerKind_WORKER_KIND_QUESTION, "gpt-5.5-mini")

	if _, err := lead.SetCircle(t.Context(), connect.NewRequest(&planv1.LeadServiceSetCircleRequest{
		Kind:  planv1.WorkerKind_WORKER_KIND_MOCKUP,
		Model: "Gemini 4 Argon (High)",
	})); err == nil {
		t.Fatal("SetCircle with label containing spaces succeeded; want error")
	}

	if seat := plan.ReadLeadSeat(home); seat.GetProvider() != planv1.Provider_PROVIDER_CLAUDE || seat.GetModel() != "claude-opus-5-8" {
		t.Fatalf("ReadLeadSeat = (%v, %q), want (CLAUDE, claude-opus-5-8)", seat.GetProvider(), seat.GetModel())
	}

	// 1. Investigation
	invRes, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Investigate", Prompt: "text ok", Kind: planv1.TaskKind_TASK_KIND_INVESTIGATION,
	}))
	if err != nil {
		t.Fatalf("spawn investigation: %v", err)
	}
	invTask := e.ended(t, invRes.Msg.GetTask().GetId())
	if invTask.GetProvider() != planv1.Provider_PROVIDER_CLAUDE || invTask.GetModel() != "claude-haiku-5-5" {
		t.Errorf("investigation = (%v, %q), want (CLAUDE, claude-haiku-5-5)", invTask.GetProvider(), invTask.GetModel())
	}

	// 2. Question investigator
	q := e.asker(wishID).ask(t, "We need a store.")
	qTask, err := e.h.startQuestionWorker(t.Context(), q, planv1.TaskRole_TASK_ROLE_INVESTIGATOR, "text ok", nil)
	if err != nil {
		t.Fatalf("start question worker: %v", err)
	}
	qTask = e.ended(t, qTask.GetId())
	if qTask.GetProvider() != planv1.Provider_PROVIDER_CODEX || qTask.GetModel() != "gpt-5.5-mini" {
		t.Errorf("question worker = (%v, %q), want (CODEX, gpt-5.5-mini)", qTask.GetProvider(), qTask.GetModel())
	}

	// 3. Mockup
	mockRes, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Mockup options", Prompt: "text ok", Kind: planv1.TaskKind_TASK_KIND_MOCKUP, Question: q.GetCode(),
	}))
	if err != nil {
		t.Fatalf("spawn mockup: %v", err)
	}
	mockTask := e.ended(t, mockRes.Msg.GetTask().GetId())
	if mockTask.GetProvider() != planv1.Provider_PROVIDER_ANTIGRAVITY || mockTask.GetModel() != "gemini-4-argon-high" {
		t.Errorf("mockup = (%v, %q), want (ANTIGRAVITY, gemini-4-argon-high)", mockTask.GetProvider(), mockTask.GetModel())
	}

	// 4. Ifrit
	ifritRes, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Try layout", Prompt: "text ok", Kind: planv1.TaskKind_TASK_KIND_IFRIT,
	}))
	if err != nil {
		t.Fatalf("spawn ifrit: %v", err)
	}
	ifritTask := ifritRes.Msg.GetTask()
	if ifritTask.GetProvider() != planv1.Provider_PROVIDER_ANTIGRAVITY || ifritTask.GetModel() != "gemini-3.8-flash-high" {
		t.Errorf("ifrit = (%v, %q), want (ANTIGRAVITY, gemini-3.8-flash-high)", ifritTask.GetProvider(), ifritTask.GetModel())
	}
	_, _ = e.tasks.Stop(t.Context(), connect.NewRequest(&planv1.TaskServiceStopRequest{TaskId: ifritTask.GetId()}))
	e.ended(t, ifritTask.GetId())

	// 5. Work
	workRes, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Implement feature", Prompt: "text ok",
	}))
	if err != nil {
		t.Fatalf("spawn work: %v", err)
	}
	workTask := e.ended(t, workRes.Msg.GetTask().GetId())
	if workTask.GetProvider() != planv1.Provider_PROVIDER_CLAUDE || workTask.GetModel() != "claude-sonnet-5-5" {
		t.Errorf("work = (%v, %q), want (CLAUDE, claude-sonnet-5-5)", workTask.GetProvider(), workTask.GetModel())
	}

	// 6. Correction
	corrTask, err := e.h.spawn(t.Context(), planv1connect.TaskServiceSpawnProcedure, &planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Correct merge", Prompt: "text ok",
	}, &planv1.Task{Correction: &planv1.TaskCorrection{Attempt: 1}})
	if err != nil {
		t.Fatalf("spawn correction: %v", err)
	}
	corrTask = e.ended(t, corrTask.GetId())
	if corrTask.GetProvider() != planv1.Provider_PROVIDER_CODEX || corrTask.GetModel() != "gpt-5.5" {
		t.Errorf("correction = (%v, %q), want (CODEX, gpt-5.5)", corrTask.GetProvider(), corrTask.GetModel())
	}

	// 7. Review
	revTask, err := e.h.spawn(t.Context(), planv1connect.TaskServiceSpawnProcedure, &planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Review merge", Prompt: "text ok",
	}, &planv1.Task{Review: &planv1.TaskReview{TaskIds: []string{workTask.GetId()}, Attempt: 1}, Branch: workTask.GetBranch(), Worktree: workTask.GetWorktree()})
	if err != nil {
		t.Fatalf("spawn review: %v", err)
	}
	revTask = e.ended(t, revTask.GetId())
	if revTask.GetProvider() != planv1.Provider_PROVIDER_ANTIGRAVITY || revTask.GetModel() != "gemini-3.8-pro-high" {
		t.Errorf("review = (%v, %q), want (ANTIGRAVITY, gemini-3.8-pro-high)", revTask.GetProvider(), revTask.GetModel())
	}

	// "Same as lead" follows a lead change.
	setRow(planv1.WorkerKind_WORKER_KIND_WORK, "")
	setRow(planv1.WorkerKind_WORKER_KIND_INVESTIGATION, "")

	same1Res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Work following lead 1", Prompt: "text ok",
	}))
	if err != nil {
		t.Fatal(err)
	}
	same1 := e.ended(t, same1Res.Msg.GetTask().GetId())
	if same1.GetProvider() != planv1.Provider_PROVIDER_CLAUDE || same1.GetModel() != "claude-opus-5-8" {
		t.Errorf("same as lead (before move) = (%v, %q), want (CLAUDE, claude-opus-5-8)", same1.GetProvider(), same1.GetModel())
	}

	// Change the lead row to gpt-5.5-codex: "Same as lead" rows follow at once.
	setRow(planv1.WorkerKind_WORKER_KIND_LEAD, "gpt-5.5-codex")
	same2Res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Work following lead 2", Prompt: "text ok",
	}))
	if err != nil {
		t.Fatal(err)
	}
	same2 := e.ended(t, same2Res.Msg.GetTask().GetId())
	if same2.GetProvider() != planv1.Provider_PROVIDER_CODEX || same2.GetModel() != "gpt-5.5-codex" {
		t.Errorf("same as lead (after move) = (%v, %q), want (CODEX, gpt-5.5-codex)", same2.GetProvider(), same2.GetModel())
	}

	// Per-task --provider/--model flags remain the lead's escape hatch.
	escapeRes, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Escape hatch", Prompt: "text ok",
		Provider: planv1.Provider_PROVIDER_CLAUDE, Model: "claude-haiku-5-5",
	}))
	if err != nil {
		t.Fatal(err)
	}
	escape := e.ended(t, escapeRes.Msg.GetTask().GetId())
	if escape.GetProvider() != planv1.Provider_PROVIDER_CLAUDE || escape.GetModel() != "claude-haiku-5-5" {
		t.Errorf("escape hatch = (%v, %q), want (CLAUDE, claude-haiku-5-5)", escape.GetProvider(), escape.GetModel())
	}
}

// TestArgonModelRoundTrip (W383, W400): an old per-project setting with "Gemini 4 Argon (High)" migrates once to
// the Summoning Circle as canonical model ID "gemini-4-argon-high", is removed from the project settings file, and
// Antigravity.args passes --model gemini-4-argon-high to agy.
func TestArgonModelRoundTrip(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	home := t.TempDir()
	providers := testProviders()
	providers[planv1.Provider_PROVIDER_ANTIGRAVITY] = Fake{}
	e := upWith(t, home, providers)
	wishID, projectID := e.wish(t, gitRepo(t))

	writeFile(t, home, "projects/"+projectID+"/settings.txtpb", "provider: PROVIDER_ANTIGRAVITY\nmodel: \"Gemini 4 Argon (High)\"\n")

	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Migrated Argon task", Prompt: "text ok",
	}))
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	task := e.ended(t, res.Msg.GetTask().GetId())
	if task.GetProvider() != planv1.Provider_PROVIDER_ANTIGRAVITY || task.GetModel() != "gemini-4-argon-high" {
		t.Fatalf("migrated task = (%v, %q), want (ANTIGRAVITY, gemini-4-argon-high)", task.GetProvider(), task.GetModel())
	}

	for _, input := range []string{"Gemini 4 Argon (High)", "gemini-4-argon-high", "argon"} {
		args, err := (Antigravity{}).args(Spec{Dir: t.TempDir(), Model: input})
		if err != nil {
			t.Fatalf("Antigravity.args(%q): %v", input, err)
		}
		idx := slices.Index(args, "--model")
		if idx < 0 || idx+1 >= len(args) || args[idx+1] != "gemini-4-argon-high" {
			t.Errorf("Antigravity.args(%q) = %v, want --model gemini-4-argon-high", input, args)
		}
	}
}

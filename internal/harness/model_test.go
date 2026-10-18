package harness

import (
	"slices"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
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

// TestArgonModelRoundTrip (W383): "Gemini 4 Argon (High)" and "gemini-4-argon-high" are written to the project's
// developer settings file, read back without error, and passed to agy as "Gemini 4 Argon (High)".
func TestArgonModelRoundTrip(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	home := t.TempDir()
	project := &planv1.Project{Id: "01946059-e68b-7cb8-8c1d-6b586e3f4ea2", Name: "djinn", Directory: t.TempDir(), Git: true}
	agy := planv1.Provider_PROVIDER_ANTIGRAVITY

	for _, input := range []string{"Gemini 4 Argon (High)", "gemini-4-argon-high", "argon"} {
		err := plan.SaveDeveloperDefaults(home, project, &planv1.ProjectServiceSetDefaultsRequest{
			Project:  project.GetName(),
			Provider: &agy,
			Model:    proto.String(input),
		})
		if err != nil {
			t.Fatalf("SaveDeveloperDefaults(%q): %v", input, err)
		}
		s, err := plan.LoadSettings(home, project)
		if err != nil {
			t.Fatalf("LoadSettings(%q): %v", input, err)
		}
		if s.Provider != agy || s.Model != input {
			t.Fatalf("LoadSettings(%q) = (%v, %q), want (%v, %q)", input, s.Provider, s.Model, agy, input)
		}
		if foreignModel(agy, s.Model) {
			t.Errorf("foreignModel(antigravity, %q) = true, want false", s.Model)
		}
		if !foreignModel(planv1.Provider_PROVIDER_CLAUDE, s.Model) {
			t.Errorf("foreignModel(claude, %q) = false, want true", s.Model)
		}
		args, err := (Antigravity{}).args(Spec{Dir: project.GetDirectory(), Model: s.Model})
		if err != nil {
			t.Fatalf("Antigravity.args(%q): %v", input, err)
		}
		idx := slices.Index(args, "--model")
		if idx < 0 || idx+1 >= len(args) || args[idx+1] != "Gemini 4 Argon (High)" {
			t.Errorf("Antigravity.args(%q) = %v, want --model \"Gemini 4 Argon (High)\"", input, args)
		}
	}
}

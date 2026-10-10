package harness

import (
	"testing"

	"connectrpc.com/connect"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
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

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

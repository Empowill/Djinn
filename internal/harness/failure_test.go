package harness

import (
	"context"
	"os"
	"strings"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// envProvider starts its provider with more environment: what makes the test binary play a fixture.
type envProvider struct {
	Provider
	env []string
}

func (p envProvider) Start(ctx context.Context, spec Spec) (Worker, error) {
	spec.Env = append(spec.Env, p.env...)
	return p.Provider.Start(ctx, spec)
}

// TestAgyDenialFailsTask replays a real agy run that stopped at a denied command (agy 1.3.0, 2026-10-08): its
// turn ended with SUCCESS and its process exited 0, and Djinn marked the task done. The task fails, saying why.
func TestAgyDenialFailsTask(t *testing.T) {
	t.Parallel()
	env, _, _ := fake{provider: "antigravity", fixture: "permission-denied"}.env(t)
	providers := testProviders()
	providers[planv1.Provider_PROVIDER_ANTIGRAVITY] = envProvider{Antigravity{Command: os.Args[0]}, env}
	e := upWith(t, t.TempDir(), providers)
	wishID, _ := e.wish(t, gitRepo(t))
	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Pause a worker", Prompt: "x", Provider: planv1.Provider_PROVIDER_ANTIGRAVITY,
	}))
	if err != nil {
		t.Fatal(err)
	}
	id := res.Msg.GetTask().GetId()
	events := e.watch(t.Context(), t, id, 0)

	const why = "agy stopped: it cannot run commands headless (go tool task --list). Run this task with claude or codex."
	got := e.get(t, id)
	if got.GetStatus() != planv1.TaskStatus_TASK_STATUS_FAILED || got.GetError() != why || got.GetExitCode() != 0 {
		t.Errorf("task = %v, want failed: %s", got, why)
	}
	if last := events[len(events)-1].GetText(); last != "failed: "+why {
		t.Errorf("last event %q", last)
	}
}

// TestErrorNeverDone: a worker that said an error and then ended with exit code 0 and no error of its own fails
// with that error, whatever its provider. Here the fake, whose script goes on after a line it cannot read.
func TestErrorNeverDone(t *testing.T) {
	t.Parallel()
	e := up(t, t.TempDir())
	wishID, _ := e.wish(t, t.TempDir())
	task := e.spawn(t, wishID, "text working\nusage many\ntext all good")
	e.watch(t.Context(), t, task.GetId(), 0)
	got := e.get(t, task.GetId())
	if got.GetStatus() != planv1.TaskStatus_TASK_STATUS_FAILED || !strings.HasPrefix(got.GetError(), "usage: ") || got.GetExitCode() != 0 {
		t.Errorf("task = %v, want failed on the usage line", got)
	}
}

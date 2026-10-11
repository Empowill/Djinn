package harness

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/testx"
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

// hookProvider starts its provider with extra environment and an optional hook in spec.Dir before starting.
type hookProvider struct {
	Provider
	env    []string
	before func(dir string)
}

func (p hookProvider) Start(ctx context.Context, spec Spec) (Worker, error) {
	if p.before != nil {
		p.before(spec.Dir)
	}
	spec.Env = append(spec.Env, p.env...)
	return p.Provider.Start(ctx, spec)
}

// TestAgyDenialFailsTask replays a real agy run that stopped at a denied command (agy 1.3.0, 2026-10-08): its
// turn ended with SUCCESS and its process exited 0, and Djinn marked the task done. The task fails, saying why.
func TestAgyDenialFailsTask(t *testing.T) {
	testx.Portable(t)
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
	testx.Portable(t)
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

// TestAgyContinuedFromErrorEvent verifies that when agy sends a retry prompt as an error event,
// Djinn continues the conversation up to 3 times, allowing the worker to finish successfully.
func TestAgyContinuedFromErrorEvent(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	env, _, _ := fake{provider: "antigravity", fixture: "error-event"}.env(t)
	providers := testProviders()
	providers[planv1.Provider_PROVIDER_ANTIGRAVITY] = envProvider{Antigravity{Command: os.Args[0]}, env}
	e := upWith(t, t.TempDir(), providers)
	wishID, _ := e.wish(t, gitRepo(t))
	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Agy error event continuation", Prompt: "x", Provider: planv1.Provider_PROVIDER_ANTIGRAVITY,
	}))
	if err != nil {
		t.Fatal(err)
	}
	id := res.Msg.GetTask().GetId()
	events := e.watch(t.Context(), t, id, 0)
	got := e.get(t, id)
	if got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE || got.GetError() != "" || got.GetExitCode() != 0 {
		t.Fatalf("task = %v, want done with no error", got)
	}
	hasContinue := false
	for _, ev := range events {
		if ev.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_STATUS && strings.Contains(ev.GetText(), "continued: "+agyContinuePrompt) {
			hasContinue = true
			break
		}
	}
	if !hasContinue {
		t.Errorf("events lack continuation event: %v", events)
	}
}

// TestAgyDoneAfterLateModelErrorWithCommittedWork reproduces W316: an agent that answered and committed
// its work before agy ended on a model retry prompt ends DONE with a note rather than FAILED.
func TestAgyDoneAfterLateModelErrorWithCommittedWork(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	env, _, _ := fake{provider: "antigravity", fixture: "w316-end"}.env(t)
	providers := testProviders()
	providers[planv1.Provider_PROVIDER_ANTIGRAVITY] = hookProvider{
		Provider: Antigravity{Command: os.Args[0]},
		env:      env,
		before: func(dir string) {
			if err := os.WriteFile(filepath.Join(dir, "committed.txt"), []byte("done\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := git(t.Context(), dir, "add", "committed.txt"); err != nil {
				t.Fatal(err)
			}
			if _, err := git(t.Context(), dir, "commit", "-m", "worker committed work"); err != nil {
				t.Fatal(err)
			}
		},
	}
	e := upWith(t, t.TempDir(), providers)
	wishID, _ := e.wish(t, gitRepo(t))
	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Agy committed work then retry prompt", Prompt: "x", Provider: planv1.Provider_PROVIDER_ANTIGRAVITY,
	}))
	if err != nil {
		t.Fatal(err)
	}
	id := res.Msg.GetTask().GetId()
	events := e.watch(t.Context(), t, id, 0)
	got := e.get(t, id)
	if got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE || got.GetError() != "" || got.GetExitCode() != 0 {
		t.Fatalf("task = %v, want done with no error", got)
	}
	const wantNote = "agy ended on a model error after the work was done"
	hasNote := false
	for _, ev := range events {
		if ev.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_STATUS && ev.GetText() == wantNote {
			hasNote = true
			break
		}
	}
	if !hasNote {
		t.Errorf("events lack note %q: %v", wantNote, events)
	}
}

// TestAgyFailedAfterThreeContinuations verifies that when agy keeps returning retry prompts without
// work being committed, Djinn retries up to 3 times and then ends the task FAILED.
func TestAgyFailedAfterThreeContinuations(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	env, _, _ := fake{provider: "antigravity", fixture: "w316-end"}.env(t)
	providers := testProviders()
	providers[planv1.Provider_PROVIDER_ANTIGRAVITY] = envProvider{Antigravity{Command: os.Args[0]}, env}
	e := upWith(t, t.TempDir(), providers)
	wishID, _ := e.wish(t, gitRepo(t))
	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Agy retries exhausted", Prompt: "x", Provider: planv1.Provider_PROVIDER_ANTIGRAVITY,
	}))
	if err != nil {
		t.Fatal(err)
	}
	id := res.Msg.GetTask().GetId()
	events := e.watch(t.Context(), t, id, 0)
	got := e.get(t, id)
	if got.GetStatus() != planv1.TaskStatus_TASK_STATUS_FAILED {
		t.Fatalf("task = %v, want failed", got)
	}
	if !strings.Contains(got.GetError(), "improperly formatted function call") {
		t.Errorf("task error = %q, want improperly formatted function call", got.GetError())
	}
	continuations := 0
	for _, ev := range events {
		if ev.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_STATUS && strings.Contains(ev.GetText(), "continued: "+agyContinuePrompt) {
			continuations++
		}
	}
	if continuations != 3 {
		t.Errorf("continuations = %d, want 3", continuations)
	}
}

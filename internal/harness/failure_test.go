package harness

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/testx"
)

func init() {
	agyTransientBackoff = time.Millisecond
}

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
	env []string
	// before runs on the harness's goroutine, not the test's: it returns its error, which fails the start, rather than
	// call t.Fatal, which there would end that goroutine and leave Harness.Close waiting for it.
	before func(dir string) error
}

func (p hookProvider) Start(ctx context.Context, spec Spec) (Worker, error) {
	if p.before != nil {
		if err := p.before(spec.Dir); err != nil {
			return nil, err
		}
	}
	spec.Env = append(spec.Env, p.env...)
	return p.Provider.Start(ctx, spec)
}

// commitWork commits a file in a worker's folder, as a worker that did its work.
func commitWork(dir string) error {
	if err := os.WriteFile(filepath.Join(dir, "committed.txt"), []byte("done\n"), 0o644); err != nil {
		return err
	}
	if _, err := git(context.Background(), dir, "add", "committed.txt"); err != nil {
		return err
	}
	_, err := git(context.Background(), dir, "commit", "-m", "worker committed work")
	return err
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
		before:   commitWork,
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
	hasNote := false
	for _, ev := range events {
		if ev.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_STATUS && strings.HasPrefix(ev.GetText(), "closing call failed: ") {
			hasNote = true
			break
		}
	}
	if !hasNote {
		t.Errorf("events lack closing call failed note: %v", events)
	}
}

// TestAgyStreamErrorDoneWithCommittedWork verifies that when a stream error occurs after the worker's
// final message with committed work and a clean tree, the task ends DONE with a closing call failed event.
func TestAgyStreamErrorDoneWithCommittedWork(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	env, _, _ := fake{provider: "antigravity", fixture: "stream-error"}.env(t)
	providers := testProviders()
	providers[planv1.Provider_PROVIDER_ANTIGRAVITY] = hookProvider{
		Provider: Antigravity{Command: os.Args[0]},
		env:      env,
		before:   commitWork,
	}
	e := upWith(t, t.TempDir(), providers)
	wishID, _ := e.wish(t, gitRepo(t))
	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Agy stream error with committed work", Prompt: "x", Provider: planv1.Provider_PROVIDER_ANTIGRAVITY,
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
	const wantNote = "closing call failed: The stream was interrupted"
	hasNote := false
	continuations := 0
	for _, ev := range events {
		if ev.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_STATUS {
			if ev.GetText() == wantNote {
				hasNote = true
			}
			if strings.HasPrefix(ev.GetText(), "continued: ") {
				continuations++
			}
		}
	}
	if !hasNote {
		t.Errorf("events lack note %q: %v", wantNote, events)
	}
	if continuations != 0 {
		t.Errorf("continuations = %d, want 0", continuations)
	}
}

// TestAgyStreamErrorRetriedThenFailedWithUncommittedChanges verifies that when a stream error occurs
// with uncommitted changes, Djinn retries up to 4 times with backoff, then fails.
func TestAgyStreamErrorRetriedThenFailedWithUncommittedChanges(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	env, _, _ := fake{provider: "antigravity", fixture: "stream-error"}.env(t)
	providers := testProviders()
	providers[planv1.Provider_PROVIDER_ANTIGRAVITY] = hookProvider{
		Provider: Antigravity{Command: os.Args[0]},
		env:      env,
		before: func(dir string) error {
			return os.WriteFile(filepath.Join(dir, "uncommitted.txt"), []byte("dirty\n"), 0o644)
		},
	}
	e := upWith(t, t.TempDir(), providers)
	wishID, _ := e.wish(t, gitRepo(t))
	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Agy stream error with dirty worktree", Prompt: "x", Provider: planv1.Provider_PROVIDER_ANTIGRAVITY,
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
	if !strings.Contains(got.GetError(), "The stream was interrupted") {
		t.Errorf("task error = %q, want The stream was interrupted", got.GetError())
	}
	continuations := 0
	for _, ev := range events {
		if ev.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_STATUS && strings.Contains(ev.GetText(), "continued: "+agyContinuePrompt) {
			continuations++
		}
	}
	if continuations != 4 {
		t.Errorf("continuations = %d, want 4", continuations)
	}
}

// TestAgyMidWorkErrorRetried verifies that a transient stream error occurring mid-work (before final text)
// is retried and the worker completes successfully.
func TestAgyMidWorkErrorRetried(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	env, _, _ := fake{provider: "antigravity", fixture: "mid-work-error"}.env(t)
	providers := testProviders()
	providers[planv1.Provider_PROVIDER_ANTIGRAVITY] = envProvider{Antigravity{Command: os.Args[0]}, env}
	e := upWith(t, t.TempDir(), providers)
	wishID, _ := e.wish(t, gitRepo(t))
	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Agy mid-work error retried", Prompt: "x", Provider: planv1.Provider_PROVIDER_ANTIGRAVITY,
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
	continuations := 0
	for _, ev := range events {
		if ev.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_STATUS && strings.Contains(ev.GetText(), "continued: "+agyContinuePrompt) {
			continuations++
		}
	}
	if continuations != 1 {
		t.Errorf("continuations = %d, want 1", continuations)
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

// TestAgyClosing429DoneAndMerges reproduces W409: when a turn already holds a final answer and its work is committed
// (including after rebasing onto an advanced integration branch), a trailing 429 quota error ends the task DONE,
// keeps the error as a status log line without holding or resuming, and merges the work; a 429 before final text or
// without committed work still waits on the per-minute limit backoff.
func TestAgyClosing429DoneAndMerges(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	t.Run("antigravity", func(t *testing.T) {
		t.Parallel()
		env, _, _ := fake{provider: "antigravity", fixture: "w409-closing-429", end: "exit:3"}.env(t)
		repo := gitRepo(t)
		providers := testProviders()
		providers[planv1.Provider_PROVIDER_ANTIGRAVITY] = hookProvider{
			Provider: Antigravity{Command: os.Args[0]},
			env:      env,
			before: func(dir string) error {
				if err := commitWork(dir); err != nil {
					return err
				}
				// Advance feat/qol in the main repo and rebase the worker's branch onto it, as W409 did before its final turn.
				if err := os.WriteFile(filepath.Join(repo, "upstream.txt"), []byte("upstream\n"), 0o644); err != nil {
					return err
				}
				if _, err := git(context.Background(), repo, "add", "upstream.txt"); err != nil {
					return err
				}
				if _, err := git(context.Background(), repo, "commit", "-m", "advance feat/qol"); err != nil {
					return err
				}
				_, err := git(context.Background(), dir, "rebase", "feat/qol")
				return err
			},
		}
		e := upWith(t, t.TempDir(), providers)
		wishID, projectID := e.wish(t, repo)
		if _, err := e.wishes.SetIntegration(t.Context(), connect.NewRequest(&planv1.WishServiceSetIntegrationRequest{
			WishId: wishID, ProjectId: projectID, Branch: "feat/qol", CreateBranch: true,
		})); err != nil {
			t.Fatal(err)
		}
		if _, err := git(t.Context(), repo, "checkout", "feat/qol"); err != nil {
			t.Fatal(err)
		}
		res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
			WishId: wishID, ProjectId: projectID, Title: "W409 closing 429", Prompt: "x", Provider: planv1.Provider_PROVIDER_ANTIGRAVITY,
		}))
		if err != nil {
			t.Fatal(err)
		}
		id := res.Msg.GetTask().GetId()
		events := e.watch(t.Context(), t, id, 0)
		got := e.get(t, id)
		if got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE || got.GetError() != "" || got.GetExitCode() != 0 {
			t.Fatalf("task = %v, want DONE with no error and exit code 0", got)
		}
		if got.GetHold() != planv1.TaskHold_TASK_HOLD_UNSPECIFIED || got.GetResumes() != 0 {
			t.Fatalf("hold = %v, resumes = %d; want no hold and 0 resumes", got.GetHold(), got.GetResumes())
		}
		hasNote := false
		for _, ev := range events {
			if ev.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_ERROR {
				t.Errorf("unexpected error event: %v", ev)
			}
			if ev.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_STATUS &&
				strings.HasPrefix(ev.GetText(), "closing call failed: ") &&
				strings.Contains(ev.GetText(), "RESOURCE_EXHAUSTED (code 429): TPM quota exhausted") {
				hasNote = true
			}
		}
		if !hasNote {
			t.Errorf("events lack closing call failed 429 note: %v", events)
		}
		e.h.integratePass(t.Context())
		if in := e.get(t, id).GetIntegration(); in.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED || in.GetBranch() != "feat/qol" {
			t.Fatalf("integration = %v, want COMMITTED into feat/qol", in)
		}
	})

	t.Run("fake", func(t *testing.T) {
		t.Parallel()
		repo := gitRepo(t)
		providers := testProviders()
		providers[planv1.Provider_PROVIDER_FAKE] = hookProvider{
			Provider: Fake{},
			before:   commitWork,
		}
		e := upWith(t, t.TempDir(), providers)
		wishID, projectID := e.wish(t, repo)
		if _, err := e.wishes.SetIntegration(t.Context(), connect.NewRequest(&planv1.WishServiceSetIntegrationRequest{
			WishId: wishID, ProjectId: projectID, Branch: "feat/qol", CreateBranch: true,
		})); err != nil {
			t.Fatal(err)
		}
		res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
			WishId: wishID, ProjectId: projectID, Title: "Fake closing 429",
			Prompt:   "text All checks green and ready to merge.\nerror RESOURCE_EXHAUSTED (code 429): TPM quota exhausted\nexit 3",
			Provider: planv1.Provider_PROVIDER_FAKE,
		}))
		if err != nil {
			t.Fatal(err)
		}
		id := res.Msg.GetTask().GetId()
		events := e.watch(t.Context(), t, id, 0)
		got := e.get(t, id)
		if got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE || got.GetError() != "" || got.GetExitCode() != 0 {
			t.Fatalf("task = %v, want DONE with no error and exit code 0", got)
		}
		if got.GetHold() != planv1.TaskHold_TASK_HOLD_UNSPECIFIED || got.GetResumes() != 0 {
			t.Fatalf("hold = %v, resumes = %d; want no hold and 0 resumes", got.GetHold(), got.GetResumes())
		}
		const wantNote = "closing call failed: RESOURCE_EXHAUSTED (code 429): TPM quota exhausted"
		hasNote := false
		for _, ev := range events {
			if ev.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_STATUS && ev.GetText() == wantNote {
				hasNote = true
			}
		}
		if !hasNote {
			t.Errorf("events lack %q: %v", wantNote, events)
		}
		e.h.integratePass(t.Context())
		if in := e.get(t, id).GetIntegration(); in.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED {
			t.Fatalf("integration = %v, want COMMITTED", in)
		}
	})

	t.Run("mid-turn", func(t *testing.T) {
		t.Parallel()
		now := time.Date(2026, 10, 18, 18, 19, 0, 0, time.UTC)
		clock := &fakeClock{at: now}
		e := upWith(t, t.TempDir(), testProviders(), WithClock(clock.now))
		wishID, _ := e.wish(t, gitRepo(t))
		res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
			WishId: wishID, Title: "Mid-turn 429 waits",
			Prompt:   "tool Bash go test ./...\nresult ok\nfail RESOURCE_EXHAUSTED (code 429): TPM quota exhausted",
			Provider: planv1.Provider_PROVIDER_FAKE,
		}))
		if err != nil {
			t.Fatal(err)
		}
		id := res.Msg.GetTask().GetId()
		got := e.until(t, id, isStatus(planv1.TaskStatus_TASK_STATUS_RESUMING))
		if got.GetHold() != planv1.TaskHold_TASK_HOLD_LIMIT {
			t.Fatalf("hold = %v, want TASK_HOLD_LIMIT", got.GetHold())
		}
		if got.GetResumes() != 0 {
			t.Fatalf("resumes = %d, want 0 for per-minute quota backoff", got.GetResumes())
		}
		if wait := got.GetResumeAfter().AsTime().Sub(now); wait != 60*time.Second {
			t.Fatalf("backoff = %v, want 60s", wait)
		}
	})
}

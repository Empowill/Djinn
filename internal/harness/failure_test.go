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

// TestAntigravityFailure covers Antigravity permission refusal, transient error-event continuations,
// closing errors after committed work, and retries when work is uncommitted or mid-turn.
func TestAntigravityFailure(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	const deniedWhy = "agy stopped: it cannot run commands headless (go tool task --list). Run this task with claude or codex."
	dirtyWork := func(dir string) error {
		return os.WriteFile(filepath.Join(dir, "uncommitted.txt"), []byte("dirty\n"), 0o644)
	}

	cases := []struct {
		name       string
		fixture    string
		needGit    bool
		before     func(dir string) error
		wantStatus planv1.TaskStatus
		wantErr    string
		wantNote   string
		wantCont   int
	}{
		{
			name:       "permission_denied_fails",
			fixture:    "permission-denied",
			wantStatus: planv1.TaskStatus_TASK_STATUS_FAILED,
			wantErr:    deniedWhy,
			wantNote:   "failed: " + deniedWhy,
			wantCont:   0,
		},
		{
			name:       "continued_from_error_event",
			fixture:    "error-event",
			wantStatus: planv1.TaskStatus_TASK_STATUS_DONE,
			wantCont:   1,
		},
		{
			name:       "done_after_late_model_error_with_committed_work",
			fixture:    "w316-end",
			needGit:    true,
			before:     commitWork,
			wantStatus: planv1.TaskStatus_TASK_STATUS_DONE,
			wantNote:   "closing call failed: ",
			wantCont:   3,
		},
		{
			name:       "stream_error_done_with_committed_work",
			fixture:    "stream-error",
			needGit:    true,
			before:     commitWork,
			wantStatus: planv1.TaskStatus_TASK_STATUS_DONE,
			wantNote:   "closing call failed: The stream was interrupted",
			wantCont:   0,
		},
		{
			name:       "stream_error_retried_then_failed_with_uncommitted_changes",
			fixture:    "stream-error",
			needGit:    true,
			before:     dirtyWork,
			wantStatus: planv1.TaskStatus_TASK_STATUS_FAILED,
			wantErr:    "The stream was interrupted",
			wantCont:   4,
		},
		{
			name:       "mid_work_error_retried",
			fixture:    "mid-work-error",
			wantStatus: planv1.TaskStatus_TASK_STATUS_DONE,
			wantCont:   1,
		},
		{
			name:       "failed_after_three_continuations",
			fixture:    "w316-end",
			wantStatus: planv1.TaskStatus_TASK_STATUS_FAILED,
			wantErr:    "improperly formatted function call",
			wantCont:   3,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env, _, _ := fake{provider: "antigravity", fixture: tc.fixture}.env(t)
			providers := testProviders()
			providers[planv1.Provider_PROVIDER_ANTIGRAVITY] = hookProvider{
				Provider: Antigravity{Command: os.Args[0]},
				env:      env,
				before:   tc.before,
			}
			e := upWith(t, t.TempDir(), providers)
			dir := nativeFolder(t)
			if tc.needGit {
				dir = gitRepo(t)
			}
			wishID, _ := e.wish(t, dir)
			res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
				WishId: wishID, Title: tc.name, Prompt: "x", Provider: planv1.Provider_PROVIDER_ANTIGRAVITY,
			}))
			if err != nil {
				t.Fatal(err)
			}
			id := res.Msg.GetTask().GetId()
			events := e.watch(t.Context(), t, id, 0)
			got := e.get(t, id)
			if got.GetStatus() != tc.wantStatus {
				t.Fatalf("status = %v, want %v (error %q)", got.GetStatus(), tc.wantStatus, got.GetError())
			}
			if tc.wantErr == "" && got.GetError() != "" {
				t.Errorf("error = %q, want empty", got.GetError())
			}
			if tc.wantErr != "" && !strings.Contains(got.GetError(), tc.wantErr) {
				t.Errorf("error = %q, want containing %q", got.GetError(), tc.wantErr)
			}
			hasNote := false
			continuations := 0
			for _, ev := range events {
				if ev.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_STATUS {
					if tc.wantNote != "" && strings.Contains(ev.GetText(), tc.wantNote) {
						hasNote = true
					}
					if strings.Contains(ev.GetText(), "continued: "+agyContinuePrompt) {
						continuations++
					}
				}
			}
			if tc.wantNote != "" && !hasNote {
				t.Errorf("events lack status note %q: %v", tc.wantNote, events)
			}
			if continuations != tc.wantCont {
				t.Errorf("continuations = %d, want %d", continuations, tc.wantCont)
			}
		})
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

	const w416Err = "API error (attempt 1): RESOURCE_EXHAUSTED (code 429): TPM quota exhausted. No spare capacity is currently available to serve over-limit requests. Please try again later, reduce your request rate or contact the sales team to discuss limit increase options. Details: https://docs.cloud.google.com/gemini-enterprise-agent-platform/models/standard-paygo."

	t.Run("mid-turn", func(t *testing.T) {
		t.Parallel()
		now := time.Date(2026, 10, 18, 20, 12, 41, 0, time.UTC)
		clock := &fakeClock{at: now}
		e := upWith(t, t.TempDir(), testProviders(), WithClock(clock.now))
		wishID, _ := e.wish(t, nativeFolder(t))
		res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
			WishId: wishID, Title: "Mid-turn 429 waits",
			Prompt:   "tool Bash go test ./...\nresult ok\nerror " + w416Err + "\nexit 3",
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
			t.Fatalf("backoff = %v (resume_after %s), want 60s (20:13:41Z, not 2h at 22:12:41Z)", wait, got.GetResumeAfter().AsTime().UTC().Format(time.RFC3339))
		}
	})

	t.Run("w416-continued-after-idle", func(t *testing.T) {
		t.Parallel()
		now := time.Date(2026, 10, 18, 20, 12, 41, 0, time.UTC)
		clock := &fakeClock{at: now}
		repo := gitRepo(t)
		runNum := 0
		providers := testProviders()
		providers[planv1.Provider_PROVIDER_FAKE] = hookProvider{
			Provider: Fake{},
			before: func(dir string) error {
				runNum++
				if runNum == 1 {
					return commitWork(dir)
				}
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
		e := upWith(t, t.TempDir(), providers, WithClock(clock.now))
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
			WishId: wishID, ProjectId: projectID, Title: "W416 continued session",
			Prompt:   "tool Bash go test ./...\nresult ok\nfail stream idle: no output from the agent for 30m0s",
			Provider: planv1.Provider_PROVIDER_FAKE,
		}))
		if err != nil {
			t.Fatal(err)
		}
		id := res.Msg.GetTask().GetId()
		_ = e.until(t, id, isStatus(planv1.TaskStatus_TASK_STATUS_FAILED))
		contPrompt := "text All checks green and rebased onto feat/qol.\nerror " + w416Err + "\nexit 3"
		if _, err := e.tasks.Continue(t.Context(), connect.NewRequest(&planv1.TaskServiceContinueRequest{
			TaskId: id,
			Prompt: &contPrompt,
		})); err != nil {
			t.Fatal(err)
		}
		got := e.until(t, id, isStatus(planv1.TaskStatus_TASK_STATUS_DONE))
		if got.GetError() != "" || got.GetExitCode() != 0 || got.GetHold() != planv1.TaskHold_TASK_HOLD_UNSPECIFIED {
			t.Fatalf("task = %v, want DONE with no error, exit code 0, and no hold", got)
		}
		e.h.integratePass(t.Context())
		if in := e.get(t, id).GetIntegration(); in.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED || in.GetBranch() != "feat/qol" {
			t.Fatalf("integration = %v, want COMMITTED into feat/qol", in)
		}
	})
}

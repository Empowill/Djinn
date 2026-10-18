package harness

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// fakeClock is the harness's time in a test: it moves only when the test says.
type fakeClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *fakeClock) set(at time.Time) {
	c.mu.Lock()
	c.at = at
	c.mu.Unlock()
}

// switchProvider starts its provider with the environment it holds now: a test changes what the next worker plays.
type switchProvider struct {
	Provider
	mu  *sync.Mutex
	env *[]string
}

func (p switchProvider) Start(ctx context.Context, spec Spec) (Worker, error) {
	p.mu.Lock()
	spec.Env = append(spec.Env, *p.env...)
	p.mu.Unlock()
	return p.Provider.Start(ctx, spec)
}

func (p switchProvider) play(env []string) {
	p.mu.Lock()
	*p.env = env
	p.mu.Unlock()
}

// claudePlaying makes claude workers play env, then what play gives.
func claudePlaying(env []string) (map[planv1.Provider]Provider, switchProvider) {
	sw := switchProvider{Provider: Claude{Command: os.Args[0]}, mu: &sync.Mutex{}, env: &env}
	providers := testProviders()
	providers[planv1.Provider_PROVIDER_CLAUDE] = sw
	return providers, sw
}

// antigravityPlaying makes antigravity workers play env, then what play gives.
func antigravityPlaying(env []string) (map[planv1.Provider]Provider, switchProvider) {
	sw := switchProvider{Provider: Antigravity{Command: os.Args[0]}, mu: &sync.Mutex{}, env: &env}
	providers := testProviders()
	providers[planv1.Provider_PROVIDER_ANTIGRAVITY] = sw
	return providers, sw
}

func (e *env) list(t *testing.T, wishID string) []*planv1.Task {
	t.Helper()
	res, err := e.tasks.List(t.Context(), connect.NewRequest(&planv1.TaskServiceListRequest{WishId: wishID}))
	if err != nil {
		t.Fatal(err)
	}
	return res.Msg.GetTasks()
}

// until waits until the task is as ok says, and returns it.
func (e *env) until(t *testing.T, id string, ok func(*planv1.Task) bool) *planv1.Task {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		got := e.get(t, id)
		if ok(got) {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("task %s stays %v", got.GetCode(), got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func isStatus(s planv1.TaskStatus) func(*planv1.Task) bool {
	return func(t *planv1.Task) bool { return t.GetStatus() == s }
}

// waking is ok, waking the scheduler at each look: a task its next pass starts or resumes does so at once, not at its
// next tick, 2 s away.
func waking(h *Harness, ok func(*planv1.Task) bool) func(*planv1.Task) bool {
	return func(t *planv1.Task) bool {
		h.Wake()
		return ok(t)
	}
}

// resetAt is when the account's session limit of the recorded stream resets: its rate_limit_event's resetsAt.
var resetAt = time.Unix(1791436800, 0)

// TestSessionLimitWaitsThenResumes replays the real end of a claude worker the account's session limit stopped: the
// task does not fail, it waits for the reset the provider gave, and no other claude worker starts meanwhile. Once
// the reset is past, the scheduler resumes it on its session, and it finishes.
func TestSessionLimitWaitsThenResumes(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	limit, _, _ := fake{provider: "claude", fixture: "session-limit"}.env(t)
	success, args, _ := fake{provider: "claude", fixture: "success"}.env(t)
	providers, sw := claudePlaying(limit)
	clock := &fakeClock{at: time.Date(2026, 10, 8, 4, 28, 30, 0, time.UTC)}
	e := upWith(t, t.TempDir(), providers, WithClock(clock.now))
	wishID, _ := e.wish(t, gitRepo(t))
	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Reopen the leads", Prompt: "x", Provider: planv1.Provider_PROVIDER_CLAUDE,
	}))
	if err != nil {
		t.Fatal(err)
	}
	id := res.Msg.GetTask().GetId()

	got := e.until(t, id, isStatus(planv1.TaskStatus_TASK_STATUS_RESUMING))
	why := "the account's session limit, resets at " + ClockText(resetAt, clock.now())
	if got.GetWaitReason() != why || !got.GetResumeAfter().AsTime().Equal(resetAt.Add(time.Minute)) || got.GetError() != "" ||
		got.GetResumes() != 0 || got.GetSessionId() != "01a119c3-80f9-76b3-ba8a-13d6778efbdf" {
		t.Fatalf("after the limit: %v, want waiting for %q", got, why)
	}

	// No other claude worker starts while the limit holds; a fake one does.
	other, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Another", Prompt: "x", Provider: planv1.Provider_PROVIDER_CLAUDE,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if o := other.Msg.GetTask(); o.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING || o.GetWaitReason() != "claude waits for "+why {
		t.Errorf("another claude task: %v", o)
	}
	if _, err := e.tasks.Stop(t.Context(), connect.NewRequest(&planv1.TaskServiceStopRequest{TaskId: other.Msg.GetTask().GetId()})); err != nil {
		t.Fatal(err)
	}
	if f := e.spawn(t, wishID, "text free"); f.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
		t.Errorf("a fake task while claude's limit holds: %v", f)
	}

	// Before the reset nothing starts; after it, the task resumes on its session and finishes.
	sw.play(success)
	clock.set(resetAt.Add(30 * time.Second))
	e.h.wake()
	time.Sleep(50 * time.Millisecond)
	if got := e.get(t, id); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_RESUMING {
		t.Fatalf("resumed before the limit reset: %v", got)
	}
	clock.set(resetAt.Add(2 * time.Minute))
	e.h.wake()
	got = e.until(t, id, isStatus(planv1.TaskStatus_TASK_STATUS_DONE))
	if got.GetResumes() != 1 || got.GetWaitReason() != "" || got.GetResumeAfter() != nil {
		t.Errorf("after the resume: %v", got)
	}
	if b, err := os.ReadFile(args); err != nil || !strings.Contains(string(b), "--resume\n01a119c3-80f9-76b3-ba8a-13d6778efbdf") {
		t.Errorf("the resumed worker's arguments: %q, %v", b, err)
	}
	all := strings.Join(eventTexts(e.watch(t.Context(), t, id, 0)), "\n")
	for _, want := range []string{
		"waiting for the limit: " + why + " (You've hit your session limit · resets 7:20am (Europe/Paris)); Djinn resumes it then",
		"resumed after its usage limit reset (1 of 3): started claude in ",
		"done",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("no event %q in:\n%s", want, all)
		}
	}
	if n := len(e.list(t, wishID)); n != 3 {
		t.Errorf("%d tasks, want the 3 spawned", n)
	}
}

// TestSessionLimitBounded: a task the limit stops each time it resumes waits after a backoff once the reset it was
// given is past (15 minutes, doubled each time), and after 3 resumes it fails, saying so.
func TestSessionLimitBounded(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	limit, _, _ := fake{provider: "claude", fixture: "session-limit"}.env(t)
	providers, _ := claudePlaying(limit)
	clock := &fakeClock{at: time.Date(2026, 10, 8, 4, 28, 30, 0, time.UTC)}
	e := upWith(t, t.TempDir(), providers, WithClock(clock.now))
	wishID, _ := e.wish(t, gitRepo(t))
	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Reopen the leads", Prompt: "x", Provider: planv1.Provider_PROVIDER_CLAUDE,
	}))
	if err != nil {
		t.Fatal(err)
	}
	id := res.Msg.GetTask().GetId()
	got := e.until(t, id, isStatus(planv1.TaskStatus_TASK_STATUS_RESUMING))
	for i := range int32(maxResumes) {
		now := got.GetResumeAfter().AsTime().Add(time.Second)
		clock.set(now)
		e.h.wake()
		if i == maxResumes-1 {
			break
		}
		got = e.until(t, id, func(t *planv1.Task) bool {
			return t.GetStatus() == planv1.TaskStatus_TASK_STATUS_RESUMING && t.GetResumes() == i+1 && t.GetResumeAfter() != nil
		})
		// The reset the stream gives is past: a backoff, 30 minutes after the first resume, then an hour.
		if wait := got.GetResumeAfter().AsTime().Sub(now); wait != limitBackoff(i+1) || !strings.Contains(got.GetWaitReason(), "tries again at") {
			t.Errorf("resume %d: waits %v (%s), want %v", i+1, wait, got.GetWaitReason(), limitBackoff(i+1))
		}
	}
	got = e.until(t, id, isStatus(planv1.TaskStatus_TASK_STATUS_FAILED))
	if got.GetResumes() != maxResumes || got.GetError() != "resumed 3 times without finishing; the last time: You've hit your session limit · resets 7:20am (Europe/Paris)" {
		t.Errorf("after %d resumes: %v", maxResumes, got)
	}
}

// TestResumeRules: at the start, a task cut short resumes; one stopped by a person, one already resumed as another
// task (a fork of it), one imported, never do; one cut short with resumes already accumulated resumes too.
func TestResumeRules(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	home := t.TempDir()
	e := up(t, home)
	wishID, projectID := e.wish(t, gitRepo(t))
	worktree := t.TempDir()
	task := func(code string, s planv1.TaskStatus, edit func(*planv1.Task)) *planv1.Task {
		tk := &planv1.Task{
			Id: store.NewID(), WishId: wishID, ProjectId: projectID, Code: code, Title: code, Status: s,
			Provider: planv1.Provider_PROVIDER_FAKE, Worktree: worktree, SessionId: "s-" + code, Scheduled: true,
			Error: "djinn up ended while the worker ran", StartTime: timestamppb.Now(), EndTime: timestamppb.Now(),
		}
		if edit != nil {
			edit(tk)
		}
		putTask(t, e.db, tk, "text first prompt")
		return tk
	}
	cut := task("W1", planv1.TaskStatus_TASK_STATUS_INTERRUPTED, nil)
	stopped := task("W2", planv1.TaskStatus_TASK_STATUS_STOPPED, func(tk *planv1.Task) { tk.Error = "stopped on request" })
	forked := task("W3", planv1.TaskStatus_TASK_STATUS_INTERRUPTED, nil)
	task("W4", planv1.TaskStatus_TASK_STATUS_DONE, func(tk *planv1.Task) { tk.ForkOf, tk.Error = "W3", "" })
	imported := task("W5", planv1.TaskStatus_TASK_STATUS_INTERRUPTED, func(tk *planv1.Task) { tk.Scheduled = false })
	worn := task("W6", planv1.TaskStatus_TASK_STATUS_INTERRUPTED, func(tk *planv1.Task) { tk.Resumes = maxResumes })
	gone := task("W7", planv1.TaskStatus_TASK_STATUS_INTERRUPTED, func(tk *planv1.Task) { tk.Worktree = "" })
	e.down()

	e = up(t, home)
	got := e.until(t, cut.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_DONE))
	if got.GetResumes() != 0 {
		t.Errorf("W1: %v", got)
	}
	if all := strings.Join(eventTexts(e.watch(t.Context(), t, cut.GetId(), 0)), "\n"); !strings.Contains(all, "resumed after djinn restarted: started fake in "+worktree+
		", resuming its session") || !strings.Contains(all, restartedLine) {
		t.Errorf("W1's events:\n%s", all)
	}
	for _, tk := range []*planv1.Task{stopped, forked, imported, gone} {
		if got := e.get(t, tk.GetId()); got.GetStatus() != tk.GetStatus() || got.GetResumes() != 0 {
			t.Errorf("%s: %v, want it left %s", tk.GetCode(), got, tk.GetStatus())
		}
	}
	gotWorn := e.until(t, worn.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_DONE))
	if gotWorn.GetResumes() != maxResumes {
		t.Errorf("W6: %v, want Resumes == %d", gotWorn, maxResumes)
	}
}

// promptsProvider is a provider that keeps what each worker it starts is given: its prompt, and the session it resumes.
type promptsProvider struct {
	Provider
	mu    *sync.Mutex
	specs map[string]Spec
}

func (p promptsProvider) Start(ctx context.Context, spec Spec) (Worker, error) {
	p.mu.Lock()
	p.specs[spec.TaskID] = spec
	p.mu.Unlock()
	return p.Provider.Start(ctx, spec)
}

func (p promptsProvider) spec(id string) Spec {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.specs[id]
}

// TestResumeFromTheFirstPromptKeepsTheChecks: a worker resumed from its first prompt, one whose session was
// never known, is told the project's checks as at launch (briefed); one resumed on its session has them there already.
func TestResumeFromTheFirstPromptKeepsTheChecks(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	home := t.TempDir()
	rec := promptsProvider{Provider: Fake{}, mu: &sync.Mutex{}, specs: map[string]Spec{}}
	providers := testProviders()
	providers[planv1.Provider_PROVIDER_FAKE], providers[planv1.Provider_PROVIDER_ANTIGRAVITY] = rec, rec
	e := upWith(t, home, providers)
	wishID, projectID := e.wish(t, gitRepo(t))
	writeFile(t, home, filepath.Join("projects", projectID, "settings.txtpb"), lintAtCommit)
	task := func(code string, provider planv1.Provider, session string) *planv1.Task {
		tk := &planv1.Task{
			Id: store.NewID(), WishId: wishID, ProjectId: projectID, Code: code, Title: code,
			Status: planv1.TaskStatus_TASK_STATUS_INTERRUPTED, Provider: provider, Worktree: t.TempDir(),
			Branch: strings.ToLower(code), SessionId: session, Scheduled: true, Error: "djinn up ended while the worker ran",
			StartTime: timestamppb.Now(), EndTime: timestamppb.Now(),
		}
		putTask(t, e.db, tk, "text first prompt")
		return tk
	}
	unknown := task("W1", planv1.Provider_PROVIDER_FAKE, "")
	agyUnknown := task("W2", planv1.Provider_PROVIDER_ANTIGRAVITY, "")
	agySession := task("W3", planv1.Provider_PROVIDER_ANTIGRAVITY, "s-W3")
	session := task("W4", planv1.Provider_PROVIDER_FAKE, "s-W4")
	e.down()

	e = upWith(t, home, providers)
	checks := "Djinn checks this project's work before it merges a task's work, with `djinn gate run lint -- lint`."
	for _, tk := range []*planv1.Task{unknown, agyUnknown, agySession, session} {
		e.until(t, tk.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_DONE))
	}
	for _, tk := range []*planv1.Task{unknown, agyUnknown} {
		spec := rec.spec(tk.GetId())
		if !strings.HasPrefix(spec.Prompt, "text first prompt\n\n"+checks) || !strings.HasSuffix(spec.Prompt, "\n\n"+restartedLine) ||
			spec.Resume != "" {
			t.Errorf("%s resumed on %q with:\n%s", tk.GetCode(), spec.Resume, spec.Prompt)
		}
	}
	for _, tk := range []*planv1.Task{agySession, session} {
		spec := rec.spec(tk.GetId())
		if spec.Prompt != restartedLine || spec.Resume != tk.GetSessionId() {
			t.Errorf("%s resumed on %q with:\n%s", tk.GetCode(), spec.Resume, spec.Prompt)
		}
	}
}

// scriptStepProvider gives a scripted Fake worker a new prompt on each run.
type scriptStepProvider struct {
	mu    sync.Mutex
	steps []string
	step  int
}

func (p *scriptStepProvider) Start(ctx context.Context, spec Spec) (Worker, error) {
	p.mu.Lock()
	script := "fail exhausted"
	if p.step < len(p.steps) {
		script = p.steps[p.step]
		p.step++
	}
	p.mu.Unlock()
	spec.Prompt = script
	return Fake{}.Start(ctx, spec)
}

// untilText waits until the task has recorded an event containing text. Its prompt is what it was told, not what it
// said: a fake's script "text working" holds the text it will say before it says it.
func (e *env) untilText(t *testing.T, id, text string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		for _, ev := range e.storedEvents(t, id) {
			if ev.GetKind() != planv1.TaskEventKind_TASK_EVENT_KIND_PROMPT && strings.Contains(ev.GetText(), text) {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("task %s never said %q", id, text)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestThreeRestartsThenFinishes: a task interrupted three times by djinn up stopping or restarting resumes each
// time without counting against maxResumes, and finishes.
func TestThreeRestartsThenFinishes(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	home := t.TempDir()
	p := &scriptStepProvider{steps: []string{
		"text r0\nsleep 1h",
		"text r1\nsleep 1h",
		"text r2\nsleep 1h",
		"text done",
	}}
	providers := map[planv1.Provider]Provider{planv1.Provider_PROVIDER_FAKE: p}
	e := upWith(t, home, providers)
	wishID, _ := e.wish(t, gitRepo(t))
	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Three restarts", Prompt: "start", Provider: planv1.Provider_PROVIDER_FAKE,
	}))
	if err != nil {
		t.Fatal(err)
	}
	id := res.Msg.GetTask().GetId()

	for i := range 3 {
		e.untilText(t, id, fmt.Sprintf("r%d", i))
		e.down()
		e = upWith(t, home, providers)
	}

	got := e.until(t, id, isStatus(planv1.TaskStatus_TASK_STATUS_DONE))
	if got.GetResumes() != 0 {
		t.Errorf("task finished with %d resumes, want 0", got.GetResumes())
	}
	all := strings.Join(eventTexts(e.watch(t.Context(), t, id, 0)), "\n")
	if n := strings.Count(all, whyRestarted); n != 3 {
		t.Errorf("got %d %q in events, want 3:\n%s", n, whyRestarted, all)
	}
	if n := strings.Count(all, "resumed after djinn restarted"); n != 3 {
		t.Errorf("got %d 'resumed after djinn restarted' in events, want 3:\n%s", n, all)
	}
	if strings.Contains(all, "(1 of 3)") {
		t.Errorf("events contain '(1 of 3)' for restart resume:\n%s", all)
	}
}

// TestResumeMixCountsOnlyLimits: a mix of restarts and usage limits counts only the usage limits against maxResumes.
func TestResumeMixCountsOnlyLimits(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	home := t.TempDir()
	p := &scriptStepProvider{steps: []string{
		"text mix0\nsleep 1h",       // Run 0: interrupted by Restart 1
		"fail usage limit exceeded", // Run 1: hits limit 1 (resumes with Resumes == 1)
		"text mix1\nsleep 1h",       // Run 2: interrupted by Restart 2
		"fail usage limit exceeded", // Run 3: hits limit 2 (resumes with Resumes == 2)
		"text mix2\nsleep 1h",       // Run 4: interrupted by Restart 3
		"fail usage limit exceeded", // Run 5: hits limit 3 (resumes with Resumes == 3)
		"fail usage limit exceeded", // Run 6: hits limit 4 -> exhausted!
	}}
	providers := map[planv1.Provider]Provider{planv1.Provider_PROVIDER_FAKE: p}
	clock := &fakeClock{at: time.Date(2026, 10, 8, 4, 28, 30, 0, time.UTC)}
	e := upWith(t, home, providers, WithClock(clock.now))
	wishID, _ := e.wish(t, gitRepo(t))
	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Mix restarts and limits", Prompt: "start", Provider: planv1.Provider_PROVIDER_FAKE,
	}))
	if err != nil {
		t.Fatal(err)
	}
	id := res.Msg.GetTask().GetId()

	for i := range int32(maxResumes) {
		// Worker runs and sleeps.
		e.untilText(t, id, fmt.Sprintf("mix%d", i))
		if got := e.get(t, id); got.GetResumes() != i {
			t.Fatalf("cycle %d running: resumes = %d, want %d", i+1, got.GetResumes(), i)
		}

		// Interrupted by Djinn restart: should NOT increment resumes.
		e.down()
		e = upWith(t, home, providers, WithClock(clock.now))

		// Worker fails on usage limit: limited sets RESUMING with ResumeAfter.
		got := e.until(t, id, func(tk *planv1.Task) bool {
			return tk.GetStatus() == planv1.TaskStatus_TASK_STATUS_RESUMING && tk.GetResumeAfter() != nil
		})
		if got.GetResumes() != i {
			t.Fatalf("cycle %d limit wait: resumes = %d, want %d", i+1, got.GetResumes(), i)
		}

		// Advance clock past the limit reset and wake the harness.
		clock.set(got.GetResumeAfter().AsTime().Add(time.Second))
		e.h.wake()
	}

	// 4th usage limit fails the task because maxResumes (3) has been reached.
	got := e.until(t, id, isStatus(planv1.TaskStatus_TASK_STATUS_FAILED))
	if got.GetResumes() != maxResumes {
		t.Errorf("failed task resumes = %d, want %d", got.GetResumes(), maxResumes)
	}
	if !strings.HasPrefix(got.GetError(), "resumed 3 times without finishing") {
		t.Errorf("failed task error = %q, want 'resumed 3 times without finishing...'", got.GetError())
	}

	all := strings.Join(eventTexts(e.watch(t.Context(), t, id, 0)), "\n")
	if n := strings.Count(all, "resumed after djinn restarted"); n != 3 {
		t.Errorf("got %d restarts in events, want 3:\n%s", n, all)
	}
	if n := strings.Count(all, "resumed after its usage limit reset"); n != 3 {
		t.Errorf("got %d usage limit resumes in events, want 3:\n%s", n, all)
	}
	for i := 1; i <= int(maxResumes); i++ {
		wantLimit := fmt.Sprintf("resumed after its usage limit reset (%d of 3)", i)
		if !strings.Contains(all, wantLimit) {
			t.Errorf("events missing %q:\n%s", wantLimit, all)
		}
	}
	if strings.Contains(all, "resumed after djinn restarted (") {
		t.Errorf("restart events should not contain counter:\n%s", all)
	}
}

// TestPerMinuteRateLimitBackoff checks that per-minute TPM/RPM quotas use a 60s -> 90s -> 120s backoff without
// incrementing Resumes or exhausting maxResumes = 3, whereas multi-hour session/daily quotas use 15m -> 30m -> 60m.
func TestPerMinuteRateLimitBackoff(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

	for _, tc := range []struct {
		text      string
		perMinute bool
	}{
		{"RESOURCE_EXHAUSTED (code 429): TPM quota exhausted. No spare capacity available for model.", true},
		{"rate limit exceeded", true},
		{"HTTP 429 Too Many Requests", true},
		{"You've hit your session limit · resets 5pm (UTC)", false},
		{"daily quota exhausted", false},
		{"usage limit exceeded", false},
	} {
		l, ok := limitIn(tc.text, now)
		if !ok {
			t.Fatalf("limitIn(%q) = _, false, want Limit", tc.text)
		}
		if l.PerMinute != tc.perMinute {
			t.Errorf("limitIn(%q).PerMinute = %v, want %v", tc.text, l.PerMinute, tc.perMinute)
		}
	}

	home := t.TempDir()
	p := &scriptStepProvider{steps: []string{
		"fail RESOURCE_EXHAUSTED (code 429): TPM quota exhausted",
		"fail RESOURCE_EXHAUSTED (code 429): TPM quota exhausted",
		"fail RESOURCE_EXHAUSTED (code 429): TPM quota exhausted",
		"fail RESOURCE_EXHAUSTED (code 429): TPM quota exhausted",
		"text finished after 4 TPM waits",
	}}
	providers := map[planv1.Provider]Provider{planv1.Provider_PROVIDER_FAKE: p}
	clock := &fakeClock{at: now}
	e := upWith(t, home, providers, WithClock(clock.now))
	wishID, _ := e.wish(t, gitRepo(t))
	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "TPM quota task", Prompt: "start", Provider: planv1.Provider_PROVIDER_FAKE,
	}))
	if err != nil {
		t.Fatal(err)
	}
	id := res.Msg.GetTask().GetId()

	wantBackoffs := []time.Duration{60 * time.Second, 90 * time.Second, 120 * time.Second, 120 * time.Second}
	for i, wantWait := range wantBackoffs {
		stepAt := clock.now()
		got := e.until(t, id, func(tk *planv1.Task) bool {
			return tk.GetStatus() == planv1.TaskStatus_TASK_STATUS_RESUMING && len(tk.GetRuns()) == i+1 && tk.GetResumeAfter() != nil
		})
		if got.GetResumes() != 0 {
			t.Fatalf("TPM hit %d: Resumes = %d, want 0", i+1, got.GetResumes())
		}
		gotWait := got.GetResumeAfter().AsTime().Sub(stepAt)
		if gotWait != wantWait {
			t.Fatalf("TPM hit %d: backoff = %v, want %v", i+1, gotWait, wantWait)
		}
		clock.set(got.GetResumeAfter().AsTime().Add(time.Second))
		e.h.wake()
	}

	got := e.until(t, id, isStatus(planv1.TaskStatus_TASK_STATUS_DONE))
	if got.GetResumes() != 0 {
		t.Errorf("completed task Resumes = %d, want 0", got.GetResumes())
	}
}

// TestReplayEnvFailuresResetsResumesAndHonorsCooldown checks that replayEnvFailures waits for the limit cooldown
// before replaying a task that exhausted maxResumes, and resets Resumes to 0 when replaying so the next 429 does not
// immediately fail the task.
func TestReplayEnvFailuresResetsResumesAndHonorsCooldown(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	home := t.TempDir()
	p := &scriptStepProvider{steps: []string{
		"fail usage limit exceeded", // Task A run 0 -> Resumes 0 -> 1
		"fail usage limit exceeded", // Task A run 1 -> Resumes 1 -> 2
		"fail usage limit exceeded", // Task A run 2 -> Resumes 2 -> 3
		"fail usage limit exceeded", // Task A run 3 -> FAILED (resumed 3 times without finishing)
		"text sibling done",         // Task B run 0 -> DONE (within cooldown: does not replay Task A yet)
		"text sibling2 done",        // Task C run 0 -> DONE (past cooldown: replays Task A with Resumes = 0)
		"fail usage limit exceeded", // Task A run 4 (replayed) -> hits limit again, must RESUME (not FAIL!)
		"text task A done",          // Task A run 5 -> DONE
	}}
	providers := map[planv1.Provider]Provider{planv1.Provider_PROVIDER_FAKE: p}
	clock := &fakeClock{at: now}
	e := upWith(t, home, providers, WithClock(clock.now))
	wishID, _ := e.wish(t, gitRepo(t))
	resA, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Task A", Prompt: "start A", Provider: planv1.Provider_PROVIDER_FAKE,
	}))
	if err != nil {
		t.Fatal(err)
	}
	idA := resA.Msg.GetTask().GetId()

	for i := range maxResumes {
		got := e.until(t, idA, func(tk *planv1.Task) bool {
			return tk.GetStatus() == planv1.TaskStatus_TASK_STATUS_RESUMING && len(tk.GetRuns()) == i+1 && tk.GetResumeAfter() != nil
		})
		clock.set(got.GetResumeAfter().AsTime().Add(time.Second))
		e.h.wake()
	}
	failedA := e.until(t, idA, isStatus(planv1.TaskStatus_TASK_STATUS_FAILED))
	if failedA.GetResumes() != maxResumes {
		t.Fatalf("failedA.Resumes = %d, want %d", failedA.GetResumes(), maxResumes)
	}

	// Only 10 seconds after Task A failed (well within the 15m usage limit cooldown), Task B finishes.
	clock.set(clock.now().Add(10 * time.Second))
	resB, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Task B", Prompt: "start B", Provider: planv1.Provider_PROVIDER_FAKE,
	}))
	if err != nil {
		t.Fatal(err)
	}
	e.until(t, resB.Msg.GetTask().GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_DONE))
	if gotA := e.get(t, idA); gotA.GetStatus() != planv1.TaskStatus_TASK_STATUS_FAILED {
		t.Fatalf("Task A status after sibling finished during cooldown = %v, want FAILED", gotA.GetStatus())
	}

	// Advance past the 15m cooldown and run Task C -> Task A is replayed with Resumes reset to 0.
	clock.set(clock.now().Add(16 * time.Minute))
	resC, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Task C", Prompt: "start C", Provider: planv1.Provider_PROVIDER_FAKE,
	}))
	if err != nil {
		t.Fatal(err)
	}
	e.until(t, resC.Msg.GetTask().GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_DONE))

	// Replayed Task A hits another usage limit and must enter RESUMING with Resumes == 0 (not fail immediately).
	resumingA := e.until(t, idA, func(tk *planv1.Task) bool {
		return tk.GetStatus() == planv1.TaskStatus_TASK_STATUS_RESUMING && len(tk.GetRuns()) == 5 && tk.GetResumeAfter() != nil
	})
	if resumingA.GetResumes() != 0 {
		t.Fatalf("replayed Task A Resumes = %d, want 0", resumingA.GetResumes())
	}
	clock.set(resumingA.GetResumeAfter().AsTime().Add(time.Second))
	e.h.wake()
	e.until(t, idA, isStatus(planv1.TaskStatus_TASK_STATUS_DONE))
}

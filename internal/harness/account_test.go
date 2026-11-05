package harness

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/testx"
)

// TestFirstFree: the policy by default keeps a task on the sign-in it ran on while that one is free, else takes the
// first free one in the developer's order, and none when each is paused or limited.
func TestFirstFree(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	own := AccountOption{}
	work := AccountOption{SignIn: plan.SignIn{Name: "work"}}
	perso := AccountOption{SignIn: plan.SignIn{Name: "perso"}}
	paused := func(o AccountOption) AccountOption { o.Paused = true; return o }
	limited := func(o AccountOption, until time.Time) AccountOption { o.LimitedUntil = until; return o }
	for _, c := range []struct {
		name    string
		options []AccountOption
		current string
		ran     bool
		want    string
		ok      bool
	}{
		{"a new task: the first", []AccountOption{own, work, perso}, "", false, "", true},
		{"the one it ran on", []AccountOption{own, work, perso}, "perso", true, "perso", true},
		{"the one it ran on, limited: the first free", []AccountOption{own, work, limited(perso, now.Add(time.Hour))}, "perso", true, "", true},
		{"a limit past holds no more", []AccountOption{own, limited(work, now.Add(-time.Minute))}, "work", true, "work", true},
		{"its own paused: the next", []AccountOption{paused(own), work}, "", false, "work", true},
		{"a forgotten account: the first free", []AccountOption{paused(own), work}, "gone", true, "work", true},
		{"each limited: the one it ran on (W409)", []AccountOption{limited(own, now.Add(time.Hour)), limited(work, now.Add(time.Hour))}, "work", true, "work", true},
		{"each paused or limited: the one not paused", []AccountOption{paused(own), limited(work, now.Add(time.Hour))}, "", false, "work", true},
		{"each paused", []AccountOption{paused(own), paused(work)}, "work", true, "", false},
	} {
		got, ok := FirstFree(AccountChoice{Provider: planv1.Provider_PROVIDER_CLAUDE, Options: c.options, Current: c.current, Ran: c.ran, Now: now})
		if got != c.want || ok != c.ok {
			t.Errorf("%s: %q, %v; want %q, %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

// signIns writes the accounts and the paused own sign-ins of settings.json in home, as djinn lead add-account and
// pause-account do.
func signIns(t *testing.T, home string, settings *uiv1.Settings) {
	t.Helper()
	data, err := protojson.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "settings.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// signInProbe records the spec of each worker it starts, and plays its scripts in turn as the fake does: "text ok"
// once they are spent.
type signInProbe struct {
	mu      sync.Mutex
	specs   []Spec
	scripts []string
}

func (p *signInProbe) Start(ctx context.Context, spec Spec) (Worker, error) {
	p.mu.Lock()
	n := len(p.specs)
	p.specs = append(p.specs, spec)
	spec.Prompt = "text ok"
	if n < len(p.scripts) {
		spec.Prompt = p.scripts[n]
	}
	p.mu.Unlock()
	return Fake{}.Start(ctx, spec)
}

// started is the spec of the nth worker the probe started, once it has.
func (p *signInProbe) started(t *testing.T, n int) Spec {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		p.mu.Lock()
		if len(p.specs) > n {
			spec := p.specs[n]
			p.mu.Unlock()
			return spec
		}
		p.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatalf("no worker %d started", n+1)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// configDir is the CLAUDE_CONFIG_DIR a worker's environment sets; "" when it sets none.
func configDir(spec Spec) string {
	for _, v := range spec.Env {
		if dir, ok := strings.CutPrefix(v, "CLAUDE_CONFIG_DIR="); ok {
			return dir
		}
	}
	return ""
}

// spawnClaude spawns a task on Claude in wishID.
func (e *env) spawnClaude(t *testing.T, wishID string) *planv1.Task {
	t.Helper()
	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Try the accounts", Prompt: "x", Provider: planv1.Provider_PROVIDER_CLAUDE,
	}))
	if err != nil {
		t.Fatal(err)
	}
	return res.Msg.GetTask()
}

// TestAccountChosenAtLaunch: with the agent's own sign-in paused, a worker starts on the first account: its folder in
// its environment, the variables that would win over its login cleared, the memory of the own folder kept, the
// account on its task and on its run. With every sign-in paused, a task waits, saying so, until one is resumed.
func TestAccountChosenAtLaunch(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	home := t.TempDir()
	work := filepath.Join(t.TempDir(), ".claude-work")
	accounts := []*planv1.Account{{Name: "work", Provider: planv1.Provider_PROVIDER_CLAUDE, ConfigDirectory: work}}
	signIns(t, home, &uiv1.Settings{Accounts: accounts, PausedSignIns: []planv1.Provider{planv1.Provider_PROVIDER_CLAUDE}})
	probe := &signInProbe{}
	providers := testProviders()
	providers[planv1.Provider_PROVIDER_CLAUDE] = probe
	e := upWith(t, home, providers)
	wishID, _ := e.wish(t, nativeFolder(t))

	task := e.spawnClaude(t, wishID)
	spec := probe.started(t, 0)
	if got := configDir(spec); got != work {
		t.Errorf("CLAUDE_CONFIG_DIR %q, want %q", got, work)
	}
	for _, key := range []string{"ANTHROPIC_API_KEY=", "ANTHROPIC_AUTH_TOKEN=", "CLAUDE_CODE_OAUTH_TOKEN="} {
		if !slices.Contains(spec.Env, key) {
			t.Errorf("%s not cleared: %v", key, spec.Env)
		}
	}
	if !strings.HasSuffix(spec.MemoryDir, string(filepath.Separator)+"memory") || strings.HasPrefix(spec.MemoryDir, work) {
		t.Errorf("memory folder %q: want the own folder's", spec.MemoryDir)
	}
	done := e.until(t, task.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_DONE))
	if done.GetAccount() != "work" || len(done.GetRuns()) != 1 || done.GetRuns()[0].GetAccount() != "work" {
		t.Errorf("account %q, runs %v: want work", done.GetAccount(), done.GetRuns())
	}

	accounts[0].Paused = true
	signIns(t, home, &uiv1.Settings{Accounts: accounts, PausedSignIns: []planv1.Provider{planv1.Provider_PROVIDER_CLAUDE}})
	second := e.spawnClaude(t, wishID)
	want := "waits for a claude sign-in: its own is paused; work is paused"
	e.until(t, second.GetId(), func(t *planv1.Task) bool {
		return t.GetWaitReason() == want && t.GetHold() == planv1.TaskHold_TASK_HOLD_LIMIT
	})
	accounts[0].Paused = false
	signIns(t, home, &uiv1.Settings{Accounts: accounts, PausedSignIns: []planv1.Provider{planv1.Provider_PROVIDER_CLAUDE}})
	e.h.Wake()
	if got := e.until(t, second.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_DONE)); got.GetAccount() != "work" {
		t.Errorf("resumed on %q, want work", got.GetAccount())
	}
}

// TestAccountLimitMoves: a worker that hits its account's session limit moves at once to the next sign-in free,
// spending no resume, and is told so; the account limited holds the next task too, which starts on the free one.
func TestAccountLimitMoves(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	home := t.TempDir()
	work, perso := filepath.Join(t.TempDir(), ".claude-work"), filepath.Join(t.TempDir(), ".claude-perso")
	signIns(t, home, &uiv1.Settings{
		Accounts: []*planv1.Account{
			{Name: "work", Provider: planv1.Provider_PROVIDER_CLAUDE, ConfigDirectory: work},
			{Name: "perso", Provider: planv1.Provider_PROVIDER_CLAUDE, ConfigDirectory: perso},
		},
		PausedSignIns: []planv1.Provider{planv1.Provider_PROVIDER_CLAUDE},
	})
	probe := &signInProbe{scripts: []string{sessionLimit}}
	providers := testProviders()
	providers[planv1.Provider_PROVIDER_CLAUDE] = probe
	clock := &fakeClock{at: time.Date(2026, 11, 5, 4, 28, 30, 0, time.UTC)}
	e := upWith(t, home, providers, WithClock(clock.now))
	wishID, _ := e.wish(t, nativeFolder(t))

	task := e.spawnClaude(t, wishID)
	if got := configDir(probe.started(t, 0)); got != work {
		t.Fatalf("first worker on %q, want %q", got, work)
	}
	moved := probe.started(t, 1)
	if got := configDir(moved); got != perso {
		t.Errorf("moved to %q, want %q", got, perso)
	}
	if !strings.Contains(moved.Prompt, accountLine) || moved.Resume != "" {
		t.Errorf("moved worker told %q", moved.Prompt)
	}
	done := e.until(t, task.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_DONE))
	if done.GetAccount() != "perso" || done.GetResumes() != 0 {
		t.Errorf("account %q, resumes %d: want perso, none spent", done.GetAccount(), done.GetResumes())
	}
	runs := done.GetRuns()
	if len(runs) != 2 || runs[0].GetAccount() != "work" || runs[1].GetAccount() != "perso" {
		t.Errorf("runs %v: want work then perso", runs)
	}
	texts := strings.Join(eventTexts(e.storedEvents(t, task.GetId())), "\n")
	for _, want := range []string{
		"the account work hit the account's session limit: resumes on the account perso",
		"resumed " + byAccount + ": started claude",
		"moved from the account work to the account perso, on a new session",
	} {
		if !strings.Contains(texts, want) {
			t.Errorf("no event says %q:\n%s", want, texts)
		}
	}

	next := e.spawnClaude(t, wishID)
	if got := configDir(probe.started(t, 2)); got != perso {
		t.Errorf("next task on %q, want %q: work is limited until its reset", got, perso)
	}
	e.until(t, next.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_DONE))
}

// TestAccountWaitsThenMoves: a worker whose limit finds no other sign-in free waits for its reset; once the
// developer resumes another account, the task moves there before the reset, spending no resume.
func TestAccountWaitsThenMoves(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	home := t.TempDir()
	work, perso := filepath.Join(t.TempDir(), ".claude-work"), filepath.Join(t.TempDir(), ".claude-perso")
	accounts := []*planv1.Account{
		{Name: "work", Provider: planv1.Provider_PROVIDER_CLAUDE, ConfigDirectory: work},
		{Name: "perso", Provider: planv1.Provider_PROVIDER_CLAUDE, ConfigDirectory: perso, Paused: true},
	}
	own := []planv1.Provider{planv1.Provider_PROVIDER_CLAUDE}
	signIns(t, home, &uiv1.Settings{Accounts: accounts, PausedSignIns: own})
	probe := &signInProbe{scripts: []string{sessionLimit}}
	providers := testProviders()
	providers[planv1.Provider_PROVIDER_CLAUDE] = probe
	clock := &fakeClock{at: time.Date(2026, 11, 5, 4, 28, 30, 0, time.UTC)}
	e := upWith(t, home, providers, WithClock(clock.now))
	wishID, _ := e.wish(t, nativeFolder(t))

	task := e.spawnClaude(t, wishID)
	waiting := e.until(t, task.GetId(), func(t *planv1.Task) bool {
		return t.GetStatus() == planv1.TaskStatus_TASK_STATUS_RESUMING && t.GetResumeAfter() != nil
	})
	if waiting.GetAccount() != "work" || !waiting.GetResumeAfter().AsTime().After(clock.now()) {
		t.Fatalf("waits on %q until %v", waiting.GetAccount(), waiting.GetResumeAfter().AsTime())
	}
	e.passes(2)
	if got := e.get(t, task.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_RESUMING {
		t.Fatalf("started before a sign-in is free: %v", got.GetStatus())
	}

	accounts[1].Paused = false
	signIns(t, home, &uiv1.Settings{Accounts: accounts, PausedSignIns: own})
	e.h.Wake()
	if got := configDir(probe.started(t, 1)); got != perso {
		t.Errorf("moved to %q, want %q", got, perso)
	}
	done := e.until(t, task.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_DONE))
	if done.GetAccount() != "perso" || done.GetResumes() != 0 {
		t.Errorf("account %q, resumes %d: want perso, none spent", done.GetAccount(), done.GetResumes())
	}
}

// TestMoveSession: a session moving to another account is copied into that account's folder, under the project's
// folder name, so that --resume finds it there; one already there is kept; none found, it cannot move.
func TestMoveSession(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	work, perso := filepath.Join(t.TempDir(), ".claude-work"), filepath.Join(t.TempDir(), ".claude-perso")
	accounts := []*planv1.Account{
		{Name: "work", Provider: planv1.Provider_PROVIDER_CLAUDE, ConfigDirectory: work},
		{Name: "perso", Provider: planv1.Provider_PROVIDER_CLAUDE, ConfigDirectory: perso},
	}
	signIns(t, home, &uiv1.Settings{Accounts: accounts})
	h := &Harness{home: home}
	dir := "/w/djinn"
	slug := plan.ClaudeProjectSlug(dir)
	src := filepath.Join(work, "projects", slug, "s1.jsonl")
	if err := os.MkdirAll(filepath.Dir(src), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte(`{"type":"user"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !h.moveSession(planv1.Provider_PROVIDER_CLAUDE, "s1", dir, "work", accounts[1]) {
		t.Fatal("the session did not move")
	}
	if b, err := os.ReadFile(filepath.Join(perso, "projects", slug, "s1.jsonl")); err != nil || string(b) != `{"type":"user"}`+"\n" {
		t.Errorf("copy: %q, %v", b, err)
	}
	if h.moveSession(planv1.Provider_PROVIDER_CLAUDE, "s2", dir, "work", accounts[1]) {
		t.Error("a session never written moved")
	}
	if h.moveSession(planv1.Provider_PROVIDER_CODEX, "s1", dir, "work", accounts[1]) {
		t.Error("a Codex session moved: Djinn copies Claude Code's only")
	}
}

// TestNoSignInWhy: the reason of a task no sign-in takes names each one, paused or limited until when.
func TestNoSignInWhy(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 5, 9, 0, 0, 0, time.Local)
	got := noSignInWhy(planv1.Provider_PROVIDER_CLAUDE, []AccountOption{
		{SignIn: plan.SignIn{Paused: true}},
		{SignIn: plan.SignIn{Name: "work"}, LimitedUntil: now.Add(time.Hour), Limit: "the account's weekly limit"},
	}, now)
	if want := "waits for a claude sign-in: its own is paused; work hit the account's weekly limit, resets at 10:00"; got != want {
		t.Errorf("%q, want %q", got, want)
	}
}

// TestAccountWideLimit: a limit of the whole account holds its sign-in; a per-minute bucket, or a limit of one model
// or family of models (Claude's seven_day_opus), does not: a worker on another model may still run there.
func TestAccountWideLimit(t *testing.T) {
	t.Parallel()
	for kind, want := range map[string]bool{"five_hour": true, "seven_day": true, "seven_day_opus": false, "seven_day_sonnet": false} {
		if got := accountWide(Limit{What: limitWhat(kind), Family: limitFamily(kind)}); got != want {
			t.Errorf("%s: account-wide %v, want %v", kind, got, want)
		}
	}
	if accountWide(Limit{What: "the provider's rate limit", PerMinute: true}) {
		t.Error("a per-minute bucket held the account")
	}
	if l, _ := limitIn("You've hit your session limit · resets 7:20am (Europe/Paris)", time.Now()); !accountWide(l) {
		t.Errorf("the session limit: %+v", l)
	}
}

package harness

import (
	"cmp"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/testx"
)

// sessionProbe plays Claude Code's sessions over its sign-in folders, as signInProbe plays its scripts: a worker
// writes its transcript, and its tool results beside it, in the folder of the sign-in it starts on
// (CLAUDE_CONFIG_DIR), under its folder's name; one that resumes a session that folder does not hold fails as claude
// does. With lost, claude never finds a session it resumes, though its transcript is there.
type sessionProbe struct {
	signInProbe
	lost bool
}

func (p *sessionProbe) Start(ctx context.Context, spec Spec) (Worker, error) {
	session := cmp.Or(spec.Resume, spec.TaskID)
	file := filepath.Join(configDir(spec), "projects", plan.ClaudeProjectSlug(spec.Dir), session+".jsonl")
	p.mu.Lock()
	n := len(p.specs)
	p.specs = append(p.specs, spec)
	script := "text ok"
	if n < len(p.scripts) {
		script = p.scripts[n]
	}
	p.mu.Unlock()
	if _, err := os.Stat(file); spec.Resume != "" && (err != nil || p.lost) {
		script = "fail " + sessionNotFound + ": " + spec.Resume
	} else {
		results := filepath.Join(strings.TrimSuffix(file, ".jsonl"), "tool-results")
		if err := os.MkdirAll(results, 0o700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(results, "r1.txt"), []byte("a long output"), 0o600); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(file, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, err
		}
		_, err = f.WriteString(`{"type":"user","run":` + strconv.Itoa(n) + "}\n")
		if err = cmp.Or(err, f.Close()); err != nil {
			return nil, err
		}
	}
	spec.Prompt = script
	return Fake{}.Start(ctx, spec)
}

// twoSignIns names the accounts work and perso in home, each its own folder, the agent's own sign-in paused, and a
// login in work's. It returns their folders.
func twoSignIns(t *testing.T, home string) (work, perso string) {
	t.Helper()
	work, perso = filepath.Join(t.TempDir(), ".claude-work"), filepath.Join(t.TempDir(), ".claude-perso")
	for _, dir := range []string{work, perso} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// A login of the account: it never leaves its folder.
	if err := os.WriteFile(filepath.Join(work, ".credentials.json"), []byte(`{"token":"t"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	signIns(t, home, &uiv1.Settings{
		Accounts: []*planv1.Account{
			{Name: "work", Provider: planv1.Provider_PROVIDER_CLAUDE, ConfigDirectory: work},
			{Name: "perso", Provider: planv1.Provider_PROVIDER_CLAUDE, ConfigDirectory: perso},
		},
		PausedSignIns: []planv1.Provider{planv1.Provider_PROVIDER_CLAUDE},
	})
	return work, perso
}

// TestResumeSameAccount: a task continued on the sign-in it ran on resumes its session there, nothing copied, no
// relay.
func TestResumeSameAccount(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	home := t.TempDir()
	work, perso := twoSignIns(t, home)
	probe := &sessionProbe{signInProbe: signInProbe{scripts: []string{"text first part done"}}}
	providers := testProviders()
	providers[planv1.Provider_PROVIDER_CLAUDE] = probe
	e := upWith(t, home, providers)
	wishID, _ := e.wish(t, nativeFolder(t))

	task := e.spawnClaude(t, wishID)
	first := e.until(t, task.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_DONE))
	if _, err := e.tasks.Continue(t.Context(), connect.NewRequest(&planv1.TaskServiceContinueRequest{
		TaskId: task.GetId(), Prompt: new("text go on"),
	})); err != nil {
		t.Fatal(err)
	}
	again := probe.started(t, 1)
	if configDir(again) != work || again.Resume != first.GetSessionId() || again.Resume == "" {
		t.Errorf("continued on %q resuming %q: want %q resuming %q", configDir(again), again.Resume, work,
			first.GetSessionId())
	}
	if strings.Contains(again.Prompt, relayHead) {
		t.Errorf("told a relay on its own session: %q", again.Prompt)
	}
	done := e.until(t, task.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_DONE))
	if done.GetSessionId() != first.GetSessionId() || done.GetAccount() != "work" {
		t.Errorf("session %q on %q: want %q on work", done.GetSessionId(), done.GetAccount(), first.GetSessionId())
	}
	if entries, _ := os.ReadDir(perso); len(entries) != 0 {
		t.Errorf("perso's folder holds %v: nothing moved", entries)
	}
	texts := strings.Join(eventTexts(e.storedEvents(t, task.GetId())), "\n")
	if !strings.Contains(texts, "continued: started claude") || strings.Contains(texts, "copied") {
		t.Errorf("events:\n%s", texts)
	}
}

// TestResumeOtherAccount: a worker that hits its account's limit moves to the other sign-in, its session copied into
// that account's folder (its transcript and its tool results, never the login), and resumes it there.
func TestResumeOtherAccount(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	home := t.TempDir()
	work, perso := twoSignIns(t, home)
	probe := &sessionProbe{signInProbe: signInProbe{scripts: []string{sessionLimit}}}
	providers := testProviders()
	providers[planv1.Provider_PROVIDER_CLAUDE] = probe
	clock := &fakeClock{at: time.Date(2026, 11, 5, 4, 28, 30, 0, time.UTC)}
	e := upWith(t, home, providers, WithClock(clock.now))
	folder := nativeFolder(t)
	wishID, _ := e.wish(t, folder)

	task := e.spawnClaude(t, wishID)
	if got := configDir(probe.started(t, 0)); got != work {
		t.Fatalf("first worker on %q, want %q", got, work)
	}
	moved := probe.started(t, 1)
	if configDir(moved) != perso || moved.Resume != task.GetId() {
		t.Errorf("moved to %q resuming %q: want %q resuming %q", configDir(moved), moved.Resume, perso, task.GetId())
	}
	if !strings.Contains(moved.Prompt, accountLine) || strings.Contains(moved.Prompt, relayHead) {
		t.Errorf("moved worker told %q", moved.Prompt)
	}
	done := e.until(t, task.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_DONE))
	if done.GetAccount() != "perso" || done.GetResumes() != 0 || done.GetSessionId() != task.GetId() {
		t.Errorf("account %q, resumes %d, session %q: want perso, none spent, its own", done.GetAccount(),
			done.GetResumes(), done.GetSessionId())
	}
	session := filepath.Join(perso, "projects", plan.ClaudeProjectSlug(folder), task.GetId())
	if b, err := os.ReadFile(session + ".jsonl"); err != nil || !strings.HasPrefix(string(b), `{"type":"user","run":0}`) {
		t.Errorf("transcript in perso: %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(session, "tool-results", "r1.txt")); err != nil {
		t.Errorf("tool results not copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(perso, ".credentials.json")); err == nil {
		t.Error("the login of work was copied")
	}
	texts := strings.Join(eventTexts(e.storedEvents(t, task.GetId())), "\n")
	if want := "moved from the account work to the account perso, its session copied there"; !strings.Contains(texts, want) {
		t.Errorf("no event says %q:\n%s", want, texts)
	}
}

// TestResumeRelayFallback: a session copied to the other sign-in that claude does not find there fails no task: the
// worker starts again at once on a new session, its first prompt and a relay of what came before, spending no resume.
func TestResumeRelayFallback(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	home := t.TempDir()
	_, perso := twoSignIns(t, home)
	probe := &sessionProbe{lost: true, signInProbe: signInProbe{scripts: []string{"text I wrote the parser\n" + sessionLimit}}}
	providers := testProviders()
	providers[planv1.Provider_PROVIDER_CLAUDE] = probe
	clock := &fakeClock{at: time.Date(2026, 11, 5, 4, 28, 30, 0, time.UTC)}
	e := upWith(t, home, providers, WithClock(clock.now))
	wishID, _ := e.wish(t, nativeFolder(t))

	task := e.spawnClaude(t, wishID)
	if moved := probe.started(t, 1); configDir(moved) != perso || moved.Resume == "" {
		t.Fatalf("moved to %q resuming %q: want perso, resuming", configDir(moved), moved.Resume)
	}
	fresh := probe.started(t, 2)
	if configDir(fresh) != perso || fresh.Resume != "" || fresh.TaskID == task.GetId() || fresh.TaskID == "" {
		t.Errorf("new session on %q resuming %q as %q: want perso, a new session of its own", configDir(fresh),
			fresh.Resume, fresh.TaskID)
	}
	for _, want := range []string{"x\n", relayHead, "You wrote:\nI wrote the parser", newSessionLine} {
		if !strings.Contains(fresh.Prompt, want) {
			t.Errorf("new session not told %q:\n%s", want, fresh.Prompt)
		}
	}
	done := e.until(t, task.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_DONE))
	if done.GetResumes() != 0 || done.GetSessionId() != fresh.TaskID || done.GetAccount() != "perso" {
		t.Errorf("resumes %d, session %q on %q: want none spent, %q on perso", done.GetResumes(), done.GetSessionId(),
			done.GetAccount(), fresh.TaskID)
	}
	if runs := done.GetRuns(); len(runs) != 3 {
		t.Errorf("runs %v: want work, perso, perso again", runs)
	}
	texts := strings.Join(eventTexts(e.storedEvents(t, task.GetId())), "\n")
	for _, want := range []string{
		whySessionLost + " on the account perso: starts a new session, told what came before",
		"resumed " + byNewSession + ": started claude",
	} {
		if !strings.Contains(texts, want) {
			t.Errorf("no event says %q:\n%s", want, texts)
		}
	}
	probe.mu.Lock()
	defer probe.mu.Unlock()
	if len(probe.specs) > 3 {
		t.Errorf("%d workers started: the new session must not loop", len(probe.specs))
	}
}

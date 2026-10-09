//go:build !windows

package ui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"

	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
)

// fake writes an executable shell script named name in dir: a fake agent command line, never the real one.
func fake(t *testing.T, dir, name, script string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func environment(t *testing.T, dir string, agents bool) []*uiv1.Provider {
	t.Helper()
	s, _ := newService(t)
	s.AgentPath = func() string { return dir }
	env, err := s.GetEnvironment(context.Background(),
		connect.NewRequest(&uiv1.UiServiceGetEnvironmentRequest{Agents: agents}))
	if err != nil {
		t.Fatal(err)
	}
	return env.Msg.GetProviders()
}

func byID(providers []*uiv1.Provider) map[string]*uiv1.Provider {
	m := map[string]*uiv1.Provider{}
	for _, p := range providers {
		m[p.GetId()] = p
	}
	return m
}

// Each agent says whether it is installed, its version, and whether it is signed in, from its own status command.
func TestGetEnvironmentAgents(t *testing.T) {
	dir := t.TempDir()
	// The status command runs with the search path as its PATH: an npm command finds node there.
	fake(t, dir, "claude", `case "$*" in
"--version") echo "2.1.294 (Claude Code)" ;;
"auth status") [ "$PATH" = "`+dir+`" ] && exit 0; exit 7 ;;
*) exit 9 ;;
esac
`)
	fake(t, dir, "codex", `case "$*" in
"--version") echo "codex-cli 0.162.0-alpha.2" ;;
"login status") echo "Not logged in"; exit 1 ;;
*) exit 9 ;;
esac
`)
	got := byID(environment(t, dir, true))
	if len(got) != 3 {
		t.Fatalf("providers = %v; want claude, codex and agy", got)
	}
	claude, codex, agy := got["claude"], got["codex"], got["agy"]
	if claude.GetState() != uiv1.ProviderState_PROVIDER_STATE_READY || !claude.GetAvailable() ||
		claude.GetVersion() != "2.1.294" || claude.GetCommand() != filepath.Join(dir, "claude") ||
		claude.GetLoginCommand() != "claude auth login" || claude.GetInstallCommand() == "" {
		t.Errorf("claude = %v; want ready, 2.1.294", claude)
	}
	if codex.GetState() != uiv1.ProviderState_PROVIDER_STATE_SIGNED_OUT || codex.GetVersion() != "0.162.0-alpha.2" ||
		codex.GetLoginCommand() != "codex login" {
		t.Errorf("codex = %v; want signed out, 0.162.0-alpha.2", codex)
	}
	if agy.GetState() != uiv1.ProviderState_PROVIDER_STATE_MISSING || agy.GetAvailable() || agy.GetCommand() != "agy" ||
		agy.GetInstallCommand() != "curl -fsSL https://antigravity.google/cli/install.sh | bash" {
		t.Errorf("agy = %v; want missing, with its install command", agy)
	}
}

// Antigravity documents no status command nor version flag: installed, its sign-in is unknown, and Djinn runs it
// not at all.
func TestGetEnvironmentAgyUnknown(t *testing.T) {
	dir := t.TempDir()
	ran := filepath.Join(t.TempDir(), "ran")
	fake(t, dir, "agy", "/usr/bin/touch "+ran+"\n")
	agy := byID(environment(t, dir, true))["agy"]
	if agy.GetState() != uiv1.ProviderState_PROVIDER_STATE_UNKNOWN || !agy.GetAvailable() || agy.GetVersion() != "" ||
		agy.GetLoginCommand() != "agy" {
		t.Errorf("agy = %v; want installed, sign-in unknown, no version", agy)
	}
	if _, err := os.Stat(ran); err == nil {
		t.Error("agy ran; want it left alone")
	}
}

// A status command that fails otherwise than "not signed in", or hangs, leaves the sign-in unknown, in time.
func TestGetEnvironmentStatusFailsOrHangs(t *testing.T) {
	old := checkTimeout
	checkTimeout = 200 * time.Millisecond
	t.Cleanup(func() { checkTimeout = old })
	dir := t.TempDir()
	fake(t, dir, "claude", `[ "$1" = "--version" ] && exec /bin/sleep 10
exec /bin/sleep 10
`)
	fake(t, dir, "codex", "exit 2\n")
	start := time.Now()
	got := byID(environment(t, dir, true))
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("checks took %v; want them cut at their timeout", took)
	}
	for _, id := range []string{"claude", "codex"} {
		if p := got[id]; p.GetState() != uiv1.ProviderState_PROVIDER_STATE_UNKNOWN || p.GetVersion() != "" {
			t.Errorf("%s = %v; want installed, sign-in unknown", id, p)
		}
	}
}

// Without the agents asked, GetEnvironment runs nothing and answers at once.
func TestGetEnvironmentWithoutAgents(t *testing.T) {
	dir := t.TempDir()
	ran := filepath.Join(t.TempDir(), "ran")
	fake(t, dir, "claude", "/usr/bin/touch "+ran+"\n")
	if got := environment(t, dir, false); len(got) != 0 {
		t.Errorf("providers = %v; want none", got)
	}
	if _, err := os.Stat(ran); err == nil {
		t.Error("claude ran; want it left alone")
	}
}

// The panel looks where the workers and the leads run from: Djinn's PATH, brought up to date by machine.ExtendPath.
// An agent installed after Djinn started, in a folder that did not exist then, is ready in the panel and found by a
// worker (exec.LookPath) at the same path.
func TestPanelAndLaunchAgree(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "")
	t.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	fake(t, bin, "claude", `[ "$1" = "--version" ] && echo "2.1.294 (Claude Code)"; exit 0
`)
	// Claude alone: the installers' folders of this machine may hold real agents, which no test runs.
	claude := checkAgent(context.Background(), agents[0], launchPath())
	if claude.GetState() != uiv1.ProviderState_PROVIDER_STATE_READY || claude.GetCommand() != filepath.Join(bin, "claude") {
		t.Fatalf("claude = %v; want ready in %s", claude, bin)
	}
	if got, err := exec.LookPath("claude"); err != nil || got != claude.GetCommand() {
		t.Errorf("a worker finds claude at %q, %v; the panel says %s", got, err, claude.GetCommand())
	}
}

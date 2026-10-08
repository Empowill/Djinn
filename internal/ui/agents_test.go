//go:build !windows

package ui

import (
	"context"
	"os"
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

// A command an application ships is found when the search path has none.
func TestAgentFromAnApplication(t *testing.T) {
	app := t.TempDir()
	fake(t, app, "codex", `[ "$1" = "--version" ] && echo "codex-cli 0.1.0"; exit 0
`)
	a := agents[1]
	a.apps = []string{filepath.Join(t.TempDir(), "codex"), filepath.Join(app, "codex")}
	p := checkAgent(context.Background(), a, t.TempDir())
	if p.GetCommand() != filepath.Join(app, "codex") || p.GetState() != uiv1.ProviderState_PROVIDER_STATE_READY ||
		p.GetVersion() != "0.1.0" {
		t.Errorf("codex = %v; want the application's, ready", p)
	}
}

// Only an executable file in an absolute folder counts.
func TestLookPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte("not executable"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	fake(t, dir, "agy", "")
	t.Chdir(dir)
	for _, name := range []string{"codex", "claude"} {
		if got, err := lookPath(name, dir); err == nil {
			t.Errorf("lookPath(%s) = %s; want not found", name, got)
		}
	}
	if got, err := lookPath("agy", "."+string(os.PathListSeparator)+dir); err != nil || got != filepath.Join(dir, "agy") {
		t.Errorf("lookPath(agy) = %s, %v; want the one of the absolute folder", got, err)
	}
	if _, err := lookPath("agy", "."); err == nil {
		t.Error("lookPath(agy) in . found it; want relative folders skipped")
	}
}

// The login shell's PATH is read from what env prints, whatever the shell prints before it.
func TestLoginShellPath(t *testing.T) {
	dir := t.TempDir()
	fake(t, dir, "shell", `[ "$1 $2 $3 $4" = "-i -l -c /usr/bin/env" ] || exit 1
echo "Welcome"
echo "HOME=/home/someone"
echo "PATH=/opt/homebrew/bin:/usr/bin"
`)
	if got := loginShellPath(filepath.Join(dir, "shell")); got != "/opt/homebrew/bin:/usr/bin" {
		t.Errorf("loginShellPath = %q; want the shell's PATH", got)
	}
	fake(t, dir, "broken", "exit 1\n")
	if got := loginShellPath(filepath.Join(dir, "broken")); got != "" {
		t.Errorf("loginShellPath of a failing shell = %q; want empty", got)
	}
	if got := loginShellPath(""); got != "" {
		t.Errorf("loginShellPath without a shell = %q; want empty", got)
	}
}

// The search path keeps Djinn's PATH first and adds the usual folders, each once.
func TestSearchPath(t *testing.T) {
	if got := joinPaths("/a:/b", "/b:/c", ":/a:/d"); got != "/a:/b:/c:/d" {
		t.Errorf("joinPaths = %q; want /a:/b:/c:/d", got)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/bin:/bin")
	got := filepath.SplitList(searchPath())
	if len(got) < 2 || got[0] != "/usr/bin" || got[1] != "/bin" {
		t.Errorf("searchPath = %v; want Djinn's PATH first", got)
	}
	for _, want := range []string{filepath.Join(home, ".local", "bin"), "/opt/homebrew/bin", "/usr/local/bin"} {
		found := false
		for _, dir := range got {
			found = found || dir == want
		}
		if !found {
			t.Errorf("searchPath = %v; want %s", got, want)
		}
	}
}

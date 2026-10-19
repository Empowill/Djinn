package ui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
)

// unix skips a test whose fake agents are shell scripts.
func unix(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake agents are shell scripts")
	}
}

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

// launchHome gives the test a home of its own and a bare PATH, as Djinn has when a launcher starts it, and returns the
// home's ~/.local/bin, a folder machine.ExtendPath adds: an agent installed there after Djinn started is found.
func launchHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "")
	t.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// TestGetEnvironment: each agent says whether it is installed, its version, and whether it is signed in, from its own
// status command; without the agents asked, the PATH alone says which are there, and nothing runs.
func TestGetEnvironment(t *testing.T) {
	// The status command runs with the search path as its PATH: an npm command finds node there.
	t.Run("each agent says where it stands", func(t *testing.T) {
		unix(t)
		t.Parallel()
		dir := t.TempDir()
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
			t.Fatalf("providers = %v; want claude, codex and antigravity", got)
		}
		claude, codex, agy := got["claude"], got["codex"], got["antigravity"]
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
	})
	// Each account of an agent is checked in its own folder, the agent's status command run with its folder variable:
	// one signed in, one not; its sign-in command names its folder. The default sign-in is checked apart.
	t.Run("each account is checked in its own folder", func(t *testing.T) {
		unix(t)
		dir := t.TempDir()
		work, home := filepath.Join(t.TempDir(), "claude-work"), filepath.Join(t.TempDir(), "claude-home")
		fake(t, dir, "claude", `case "$*" in
"auth status") [ "$CLAUDE_CONFIG_DIR" = "`+work+`" ] && [ -z "$ANTHROPIC_API_KEY" ] && exit 0; exit 1 ;;
esac
`)
		t.Setenv("ANTHROPIC_API_KEY", "not-a-key") // It would win over the folder's login: the check clears it.
		s, _ := newService(t)
		s.AgentPath = func() string { return dir }
		settings := `{"accounts": [
  {"name": "work", "provider": "PROVIDER_CLAUDE", "label": "Work", "configDirectory": "` + work + `"},
  {"name": "home", "provider": "PROVIDER_CLAUDE", "configDirectory": "` + home + `"},
  {"name": "gpt", "provider": "PROVIDER_CODEX", "configDirectory": "/nowhere"}
]}`
		if err := os.WriteFile(filepath.Join(s.Home, "settings.json"), []byte(settings), 0o600); err != nil {
			t.Fatal(err)
		}
		env, err := s.GetEnvironment(context.Background(),
			connect.NewRequest(&uiv1.UiServiceGetEnvironmentRequest{Agents: true}))
		if err != nil {
			t.Fatal(err)
		}
		got := byID(env.Msg.GetProviders())
		if got["claude"].GetState() != uiv1.ProviderState_PROVIDER_STATE_SIGNED_OUT {
			t.Errorf("claude's own sign-in = %v; want signed out", got["claude"].GetState())
		}
		accounts := got["claude"].GetAccounts()
		if len(accounts) != 2 || accounts[0].GetAccount().GetName() != "work" ||
			accounts[0].GetState() != uiv1.ProviderState_PROVIDER_STATE_READY ||
			accounts[1].GetState() != uiv1.ProviderState_PROVIDER_STATE_SIGNED_OUT ||
			accounts[0].GetLoginCommand() != "CLAUDE_CONFIG_DIR="+work+" claude auth login" {
			t.Errorf("claude's accounts = %v; want work ready, home signed out", accounts)
		}
		if codex := got["codex"].GetAccounts(); len(codex) != 1 || codex[0].GetState() != uiv1.ProviderState_PROVIDER_STATE_MISSING {
			t.Errorf("codex's accounts = %v; want gpt, codex missing", codex)
		}
	})
	// Antigravity documents no status command nor version flag: installed, its sign-in is unknown, and Djinn runs it
	// not at all.
	t.Run("antigravity is never run", func(t *testing.T) {
		unix(t)
		t.Parallel()
		dir := t.TempDir()
		ran := filepath.Join(t.TempDir(), "ran")
		fake(t, dir, "agy", "/usr/bin/touch "+ran+"\n")
		agy := byID(environment(t, dir, true))["antigravity"]
		if agy.GetState() != uiv1.ProviderState_PROVIDER_STATE_UNKNOWN || !agy.GetAvailable() || agy.GetVersion() != "" ||
			agy.GetLoginCommand() != "agy" {
			t.Errorf("agy = %v; want installed, sign-in unknown, no version", agy)
		}
		if _, err := os.Stat(ran); err == nil {
			t.Error("agy ran; want it left alone")
		}
	})
	// A status command that fails otherwise than "not signed in", or hangs, leaves the sign-in unknown, in time. It
	// shortens checkTimeout, a package variable: not parallel.
	t.Run("a status that fails or hangs leaves the sign-in unknown", func(t *testing.T) {
		unix(t)
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
	})
	// Without the agents asked, GetEnvironment runs nothing and answers at once.
	t.Run("without the agents asked nothing runs", func(t *testing.T) {
		unix(t)
		dir := t.TempDir()
		ran := filepath.Join(t.TempDir(), "ran")
		fake(t, dir, "claude", "/usr/bin/touch "+ran+"\n")
		// The quick provider list uses LookPath, so isolate the process PATH from the user's installed agents. It must
		// not run any command: setup/status checks happen only when Agents is requested.
		t.Setenv("PATH", dir)
		got := byID(environment(t, dir, false))
		if len(got) != 3 || !got["claude"].GetAvailable() || got["claude"].GetCommand() != filepath.Join(dir, "claude") ||
			got["codex"].GetAvailable() || got["antigravity"].GetAvailable() {
			t.Errorf("providers = %v; want only the fake Claude command available", got)
		}
		if _, err := os.Stat(ran); err == nil {
			t.Error("claude ran; want it left alone")
		}
	})
	// The agents a lead may run, found on the PATH: Antigravity is the program agy.
	t.Run("without the agents asked the PATH names them", func(t *testing.T) {
		bin := t.TempDir()
		agy := filepath.Join(bin, "agy")
		if runtime.GOOS == "windows" {
			agy += ".exe"
		}
		if err := os.WriteFile(agy, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", bin)
		got := byID(environment(t, t.TempDir(), false))
		if p := got["antigravity"]; p.GetName() != "Antigravity" || !p.GetAvailable() || p.GetCommand() != agy {
			t.Errorf("antigravity = %v, want available at %s", p, agy)
		}
		if p := got["claude"]; p.GetAvailable() || p.GetCommand() != "claude" {
			t.Errorf("claude = %v, want missing from this PATH", p)
		}
	})
	// The handler the server mounts answers over HTTP, and Watch is left to the server.
	t.Run("answers over HTTP", func(t *testing.T) {
		t.Parallel()
		s, _ := newService(t)
		env, err := serve(t, s).GetEnvironment(context.Background(),
			connect.NewRequest(&uiv1.UiServiceGetEnvironmentRequest{Agents: true}))
		if err != nil {
			t.Fatal(err)
		}
		if env.Msg.GetVersion() != "test" || env.Msg.GetPlatform() == "" || len(env.Msg.GetProviders()) != 3 {
			t.Fatalf("GetEnvironment = %v", env.Msg)
		}
	})
	// The panel and the lead's menu name each agent alike: the menu reads antigravity, whose program is agy.
	t.Run("the panel and the menu share their ids", func(t *testing.T) {
		unix(t)
		dir := t.TempDir()
		t.Setenv("PATH", dir)
		quick, checked := byID(environment(t, dir, false)), byID(environment(t, dir, true))
		if len(quick) != len(checked) {
			t.Fatalf("menu = %v, panel = %v; want the same agents", quick, checked)
		}
		for id, p := range quick {
			if c, ok := checked[id]; !ok || c.GetCommand() != p.GetCommand() {
				t.Errorf("%s: menu %v, panel %v; want the same agent and command", id, p, c)
			}
		}
	})
}

// TestLaunchPath: the panel looks where the workers and the leads run from: Djinn's PATH, brought up to date by
// machine.ExtendPath. An agent installed after Djinn started, in a folder that did not exist then, is ready in the
// panel and found by a worker (exec.LookPath) at the same path.
func TestLaunchPath(t *testing.T) {
	unix(t)
	bin := launchHome(t)
	fake(t, bin, "claude", `[ "$1" = "--version" ] && echo "2.1.294 (Claude Code)"; exit 0
`)
	// Claude alone: the installers' folders of this machine may hold real agents, which no test runs.
	claude := checkAgent(context.Background(), agents[0], launchPath(), nil)
	if claude.GetState() != uiv1.ProviderState_PROVIDER_STATE_READY || claude.GetCommand() != filepath.Join(bin, "claude") {
		t.Fatalf("claude = %v; want ready in %s", claude, bin)
	}
	if got, err := exec.LookPath("claude"); err != nil || got != claude.GetCommand() {
		t.Errorf("a worker finds claude at %q, %v; the panel says %s", got, err, claude.GetCommand())
	}
}

// TestSignedIn: the driven lead asks where an agent stands, its status command run with an account's variables added;
// an agent that documents no status command, or a provider Djinn has no agent for, is unknown.
func TestSignedIn(t *testing.T) {
	unix(t)
	bin := launchHome(t)
	fake(t, bin, "claude", `[ "$SIGNED" = yes ] && exit 0; exit 1
`)
	fake(t, bin, "agy", "exit 0\n")
	for _, c := range []struct {
		name     string
		provider planv1.Provider
		env      []string
		want     uiv1.ProviderState
	}{
		{"signed in with the account's variables", planv1.Provider_PROVIDER_CLAUDE, []string{"SIGNED=yes"},
			uiv1.ProviderState_PROVIDER_STATE_READY},
		{"signed out without them", planv1.Provider_PROVIDER_CLAUDE, nil, uiv1.ProviderState_PROVIDER_STATE_SIGNED_OUT},
		{"no status command documented", planv1.Provider_PROVIDER_ANTIGRAVITY, nil, uiv1.ProviderState_PROVIDER_STATE_UNKNOWN},
		{"no agent for the provider", planv1.Provider_PROVIDER_UNSPECIFIED, nil, uiv1.ProviderState_PROVIDER_STATE_UNKNOWN},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := SignedIn(context.Background(), c.provider, c.env); got != c.want {
				t.Errorf("SignedIn(%v, %v) = %v, want %v", c.provider, c.env, got, c.want)
			}
		})
	}
}

// TestAccount: the fingerprint of the account an agent is signed in with is what its status command prints, hashed;
// empty when signed out, or when the agent documents no status command.
func TestAccount(t *testing.T) {
	unix(t)
	const status = `{"loggedIn": true, "email": "one@example.com", "orgId": "d602a714", "authMethod": "claude.ai"}`
	for _, c := range []struct {
		name     string
		provider planv1.Provider
		claude   string // the fake claude's script
		want     string
	}{
		{"signed in", planv1.Provider_PROVIDER_CLAUDE, "echo '" + status + "'\n", accountFingerprint([]byte(status))},
		{"signed out", planv1.Provider_PROVIDER_CLAUDE, "echo '" + status + "'; exit 1\n", ""},
		{"no status command documented", planv1.Provider_PROVIDER_ANTIGRAVITY, "exit 0\n", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			bin := launchHome(t)
			fake(t, bin, "claude", c.claude)
			fake(t, bin, "agy", "exit 0\n")
			if got := Account(context.Background(), c.provider); got != c.want {
				t.Errorf("Account(%v) = %q, want %q", c.provider, got, c.want)
			}
		})
	}
}

// TestAccountFingerprint: the fingerprint of claude auth status (claude 2.1.302's output) changes with the account,
// not with what else the status prints, never holds the account's name, and is empty when signed out.
func TestAccountFingerprint(t *testing.T) {
	t.Parallel()
	status := func(email, analytics string) []byte {
		return []byte(`{"loggedIn": true, "authMethod": "claude.ai", "apiProvider": "firstParty", "analyticsDisabled": ` +
			analytics + `, "configDirectory": "/home/me/.claude", "email": "` + email + `", "orgId": "d602a714", ` +
			`"orgName": "Acme", "subscriptionType": "team"}`)
	}
	one := accountFingerprint(status("one@example.com", "false"))
	if one == "" || len(one) != 16 {
		t.Fatalf("fingerprint %q, want 16 hex digits", one)
	}
	for _, c := range []struct {
		name  string
		out   []byte
		empty bool // no fingerprint
		same  bool // one's fingerprint
	}{
		{"the same account, another setting", status("one@example.com", "true"), false, true},
		{"another account", status("two@example.com", "false"), false, false},
		{"signed out", []byte(`{"loggedIn": false}`), true, false},
		{"codex's line", []byte("Logged in using ChatGPT\n"), false, false},
		{"nothing printed", nil, true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := accountFingerprint(c.out)
			if (got == "") != c.empty || (got == one) != c.same {
				t.Errorf("fingerprint %q; want empty %v, the same as the first account's (%q) %v", got, c.empty, one, c.same)
			}
		})
	}
}

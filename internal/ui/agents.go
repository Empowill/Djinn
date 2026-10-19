package ui

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/machine"
	"github.com/empowill/djinn/internal/plan"
)

// agent is an agent command line Djinn knows, and how to ask it where it stands. Every command comes from the
// agent's official documentation (read 2026-10-08); where it documents none, the field is empty and the answer
// unknown, never guessed.
type agent struct {
	// id is the agent's identifier, the one the lead's menu reads; program is the command that runs it.
	id, name, program string
	// version are the arguments that print the version; nil: none documented.
	version []string
	// status are the arguments that exit 0 when signed in and 1 when not; nil: none documented.
	status []string
	// install and installWindows install the agent; login signs it in. A terminal runs them through the user's
	// shell, cmd.exe on Windows: a PowerShell installer names PowerShell.
	install, installWindows, login string
}

// agents are the agent command lines Djinn knows, in the order the window shows them.
var agents = []agent{
	{
		// code.claude.com/docs/en/setup and /cli-reference: "claude auth status … Exits with code 0 if logged in,
		// 1 if not."
		id: "claude", name: "Claude Code", program: "claude", version: []string{"--version"}, status: []string{"auth", "status"},
		install:        "curl -fsSL https://claude.ai/install.sh | bash",
		installWindows: `powershell -NoProfile -Command "irm https://claude.ai/install.ps1 | iex"`,
		login:          "claude auth login",
	},
	{
		// github.com/openai/codex README; `codex login status` exits 0 when logged in, 1 when not.
		id: "codex", name: "Codex", program: "codex", version: []string{"--version"}, status: []string{"login", "status"},
		install: "npm install -g @openai/codex",
		login:   "codex login",
	},
	{
		// antigravity.google/docs/cli/install: the first run of agy signs in. No version flag nor status command is
		// documented.
		id: "antigravity", name: "Antigravity", program: "agy",
		install:        "curl -fsSL https://antigravity.google/cli/install.sh | bash",
		installWindows: `powershell -NoProfile -Command "irm https://antigravity.google/cli/install.ps1 | iex"`,
		login:          "agy",
	},
}

// checkTimeout bounds each command Djinn runs to check an agent.
var checkTimeout = 3 * time.Second

// checkAgents says where each agent stands, looking for the commands on path only: the PATH machine.ExtendPath
// gives the workers and the leads, so that what the panel finds is what they run; and where each of its accounts
// stands, its status command run in the account's folder. The agents and accounts are checked together.
func checkAgents(ctx context.Context, path string, accounts []*planv1.Account) []*uiv1.Provider {
	res := make([]*uiv1.Provider, len(agents))
	var wg sync.WaitGroup
	for i, a := range agents {
		wg.Go(func() {
			res[i] = checkAgent(ctx, a, path, nil)
			var mu sync.Mutex
			var each sync.WaitGroup
			for _, account := range accounts {
				if agentProviders[a.id] != account.GetProvider() {
					continue
				}
				setup := &uiv1.AccountSetup{Account: account, LoginCommand: plan.AccountLogin(account, runtime.GOOS),
					State: uiv1.ProviderState_PROVIDER_STATE_MISSING}
				res[i].Accounts = append(res[i].Accounts, setup)
				if !res[i].GetAvailable() {
					continue
				}
				each.Go(func() {
					state := checkAgent(ctx, a, path, plan.AccountEnv(account)).GetState()
					mu.Lock()
					setup.State = state
					mu.Unlock()
				})
			}
			each.Wait()
		})
	}
	wg.Wait()
	return res
}

// agentProviders are the providers of the agents, by their identifier.
var agentProviders = map[string]planv1.Provider{
	"claude":      planv1.Provider_PROVIDER_CLAUDE,
	"codex":       planv1.Provider_PROVIDER_CODEX,
	"antigravity": planv1.Provider_PROVIDER_ANTIGRAVITY,
}

// SignedIn says where provider's agent stands, run with env added to Djinn's environment (an account's folder):
// missing, signed out, ready, or unknown when it documents no status command. The driven lead asks it before it
// offers to move there.
func SignedIn(ctx context.Context, provider planv1.Provider, env []string) uiv1.ProviderState {
	for _, a := range agents {
		if agentProviders[a.id] == provider {
			return checkAgent(ctx, a, launchPath(), env).GetState()
		}
	}
	return uiv1.ProviderState_PROVIDER_STATE_UNKNOWN
}

// launchPath is the PATH the workers and the leads start with, brought up to date: an agent installed from the panel
// meanwhile, in a folder that did not exist yet, is found here, and by them.
func launchPath() string {
	machine.ExtendPath()
	return os.Getenv("PATH")
}

// checkAgent says where agent a stands, its commands run with env added to Djinn's environment.
func checkAgent(ctx context.Context, a agent, path string, env []string) *uiv1.Provider {
	p := &uiv1.Provider{
		Id: a.id, Name: a.name, Command: a.program, InstallCommand: a.install, LoginCommand: a.login,
		State: uiv1.ProviderState_PROVIDER_STATE_MISSING,
	}
	if runtime.GOOS == "windows" && a.installWindows != "" {
		p.InstallCommand = a.installWindows
	}
	command, err := machine.LookPath(a.program, path)
	if err != nil {
		return p
	}
	p.Available, p.Command, p.State = true, command, uiv1.ProviderState_PROVIDER_STATE_UNKNOWN
	var wg sync.WaitGroup
	if a.version != nil {
		wg.Go(func() {
			var out bytes.Buffer
			if run(ctx, command, a.version, path, env, &out) == nil {
				p.Version = versionOf(out.String())
			}
		})
	}
	if a.status != nil {
		var exit *exec.ExitError
		switch err := run(ctx, command, a.status, path, env, nil); {
		case err == nil:
			p.State = uiv1.ProviderState_PROVIDER_STATE_READY
		case errors.As(err, &exit) && exit.ExitCode() == 1:
			p.State = uiv1.ProviderState_PROVIDER_STATE_SIGNED_OUT
		}
	}
	wg.Wait()
	return p
}

// run runs command with args under checkTimeout, with path as its PATH (an npm command finds node there) and env
// added, and writes its output to out when not nil. Djinn never reads what a status command prints.
func run(ctx context.Context, command string, args []string, path string, env []string, out *bytes.Buffer) error {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = append(append(withPath(os.Environ(), path), env...), "NO_COLOR=1")
	if out != nil {
		cmd.Stdout = out
	}
	// A child that keeps the output open does not hold the check past its time.
	cmd.WaitDelay = 500 * time.Millisecond
	start := time.Now()
	err := cmd.Run()
	logx.Ran(ctx, cmd, start, err)
	return err
}

// Account is a fingerprint of the account provider's agent is signed in with on this machine, in its own folder:
// what its status command prints of it (claude auth status: its email, organization and way of signing in, as JSON;
// codex login status: its line), hashed at once. Empty when the agent documents no status command, is missing or
// signed out. Djinn keeps the fingerprint alone, never the account's name: the scheduler compares two of them to see
// that the account changed while a task waits for the usage limit of the one before (harness.WithAccount).
func Account(ctx context.Context, provider planv1.Provider) string {
	for _, a := range agents {
		if agentProviders[a.id] != provider || a.status == nil {
			continue
		}
		path := launchPath()
		command, err := machine.LookPath(a.program, path)
		if err != nil {
			return ""
		}
		var out bytes.Buffer
		if run(ctx, command, a.status, path, nil, &out) != nil {
			return "" // Signed out, or no answer.
		}
		return accountFingerprint(out.Bytes())
	}
	return ""
}

// accountFingerprint hashes what names the account in a status command's output: the fields of Claude's JSON that
// name it, else the whole output.
func accountFingerprint(out []byte) string {
	var status struct {
		LoggedIn    *bool  `json:"loggedIn"`
		Email       string `json:"email"`
		OrgID       string `json:"orgId"`
		AuthMethod  string `json:"authMethod"`
		APIProvider string `json:"apiProvider"`
	}
	text := strings.TrimSpace(string(out))
	if json.Unmarshal(out, &status) == nil && status.LoggedIn != nil {
		if !*status.LoggedIn {
			return ""
		}
		text = strings.Join([]string{status.Email, status.OrgID, status.AuthMethod, status.APIProvider}, "\x00")
	}
	if text == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:8])
}

var versionPattern = regexp.MustCompile(`\d+(\.\d+)+[0-9A-Za-z.+-]*`)

// versionOf finds the version in what a version flag prints: "2.1.294 (Claude Code)", "codex-cli 0.162.0".
func versionOf(out string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	if v := versionPattern.FindString(line); v != "" {
		return v
	}
	if len(line) > 40 {
		line = line[:40]
	}
	return strings.TrimSpace(line)
}

// withPath is env with path as its PATH.
func withPath(env []string, path string) []string {
	env = slices.DeleteFunc(slices.Clone(env), func(kv string) bool {
		return strings.HasPrefix(strings.ToUpper(kv), "PATH=")
	})
	return append(env, "PATH="+path)
}

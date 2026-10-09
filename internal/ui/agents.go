package ui

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
	"github.com/empowill/djinn/internal/machine"
)

// agent is an agent command line Djinn knows, and how to ask it where it stands. Every command comes from the
// agent's official documentation (read 2026-10-08); where it documents none, the field is empty and the answer
// unknown, never guessed.
type agent struct {
	id, name string
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
		id: "claude", name: "Claude Code", version: []string{"--version"}, status: []string{"auth", "status"},
		install:        "curl -fsSL https://claude.ai/install.sh | bash",
		installWindows: `powershell -NoProfile -Command "irm https://claude.ai/install.ps1 | iex"`,
		login:          "claude auth login",
	},
	{
		// github.com/openai/codex README; `codex login status` exits 0 when logged in, 1 when not.
		id: "codex", name: "Codex", version: []string{"--version"}, status: []string{"login", "status"},
		install: "npm install -g @openai/codex",
		login:   "codex login",
	},
	{
		// antigravity.google/docs/cli/install: the first run of agy signs in. No version flag nor status command is
		// documented.
		id: "agy", name: "Antigravity",
		install:        "curl -fsSL https://antigravity.google/cli/install.sh | bash",
		installWindows: `powershell -NoProfile -Command "irm https://antigravity.google/cli/install.ps1 | iex"`,
		login:          "agy",
	},
}

// checkTimeout bounds each command Djinn runs to check an agent.
var checkTimeout = machine.CheckTimeout

// checkAgents says where each agent stands, looking for the commands on path only: the PATH machine.ExtendPath
// gives the workers and the leads, so that what the panel finds is what they run. The agents are checked together.
func checkAgents(ctx context.Context, path string) []*uiv1.Provider {
	res := make([]*uiv1.Provider, len(agents))
	var wg sync.WaitGroup
	for i, a := range agents {
		wg.Go(func() { res[i] = checkAgent(ctx, a, path) })
	}
	wg.Wait()
	return res
}

// launchPath is the PATH the workers and the leads start with, brought up to date: an agent installed from the panel
// meanwhile, in a folder that did not exist yet, is found here, and by them.
func launchPath() string {
	machine.ExtendPath()
	return os.Getenv("PATH")
}

func checkAgent(ctx context.Context, a agent, path string) *uiv1.Provider {
	p := &uiv1.Provider{
		Id: a.id, Name: a.name, Command: a.id, InstallCommand: a.install, LoginCommand: a.login,
		State: uiv1.ProviderState_PROVIDER_STATE_MISSING,
	}
	if runtime.GOOS == "windows" && a.installWindows != "" {
		p.InstallCommand = a.installWindows
	}
	command, err := machine.LookPath(a.id, path)
	if err != nil {
		return p
	}
	p.Available, p.Command, p.State = true, command, uiv1.ProviderState_PROVIDER_STATE_UNKNOWN
	var wg sync.WaitGroup
	if a.version != nil {
		wg.Go(func() {
			var out bytes.Buffer
			if run(ctx, command, a.version, path, &out) == nil {
				p.Version = versionOf(out.String())
			}
		})
	}
	if a.status != nil {
		var exit *exec.ExitError
		switch err := run(ctx, command, a.status, path, nil); {
		case err == nil:
			p.State = uiv1.ProviderState_PROVIDER_STATE_READY
		case errors.As(err, &exit) && exit.ExitCode() == 1:
			p.State = uiv1.ProviderState_PROVIDER_STATE_SIGNED_OUT
		}
	}
	wg.Wait()
	return p
}

// run runs command with args under checkTimeout, with path as its PATH (an npm command finds node there), and
// writes its output to out when not nil. Djinn never reads what a status command prints.
func run(ctx context.Context, command string, args []string, path string, out *bytes.Buffer) error {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = append(withPath(os.Environ(), path), "NO_COLOR=1")
	if out != nil {
		cmd.Stdout = out
	}
	// A child that keeps the output open does not hold the check past its time.
	cmd.WaitDelay = 500 * time.Millisecond
	return cmd.Run()
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

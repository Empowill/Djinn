package ui

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
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
	// install and installWindows install the agent; login signs it in.
	install, installWindows, login string
	// apps are where an application ships the command, tried after the search path.
	apps []string
}

// agents are the agent command lines Djinn knows, in the order the window shows them.
var agents = []agent{
	{
		// code.claude.com/docs/en/setup and /cli-reference: "claude auth status … Exits with code 0 if logged in,
		// 1 if not."
		id: "claude", name: "Claude Code", version: []string{"--version"}, status: []string{"auth", "status"},
		install:        "curl -fsSL https://claude.ai/install.sh | bash",
		installWindows: "irm https://claude.ai/install.ps1 | iex",
		login:          "claude auth login",
	},
	{
		// github.com/openai/codex README; `codex login status` exits 0 when logged in, 1 when not.
		id: "codex", name: "Codex", version: []string{"--version"}, status: []string{"login", "status"},
		install: "npm install -g @openai/codex",
		login:   "codex login",
		// The ChatGPT and Codex applications for macOS ship the command.
		apps: []string{
			"/Applications/ChatGPT.app/Contents/Resources/codex-cli/CodexCLI.app/Contents/MacOS/codex",
			"/Applications/Codex.app/Contents/Resources/codex",
		},
	},
	{
		// antigravity.google/docs/cli/install: the first run of agy signs in. No version flag nor status command is
		// documented.
		id: "agy", name: "Antigravity",
		install:        "curl -fsSL https://antigravity.google/cli/install.sh | bash",
		installWindows: "irm https://antigravity.google/cli/install.ps1 | iex",
		login:          "agy",
	},
}

// checkTimeout bounds each command Djinn runs to check an agent, and the login shell it asks for its PATH.
var checkTimeout = 3 * time.Second

// checkAgents says where each agent stands, looking for the commands on path, then in the applications that ship
// them when apps. The agents are checked together.
func checkAgents(ctx context.Context, path string, apps bool) []*uiv1.Provider {
	res := make([]*uiv1.Provider, len(agents))
	var wg sync.WaitGroup
	for i, a := range agents {
		if !apps {
			a.apps = nil
		}
		wg.Go(func() { res[i] = checkAgent(ctx, a, path) })
	}
	wg.Wait()
	return res
}

func checkAgent(ctx context.Context, a agent, path string) *uiv1.Provider {
	p := &uiv1.Provider{
		Id: a.id, Name: a.name, Command: a.id, InstallCommand: a.install, LoginCommand: a.login,
		State: uiv1.ProviderState_PROVIDER_STATE_MISSING,
	}
	if runtime.GOOS == "windows" && a.installWindows != "" {
		p.InstallCommand = a.installWindows
	}
	command, err := lookPath(a.id, path)
	for _, app := range a.apps {
		if err != nil && executable(app) {
			command, err = app, nil
		}
	}
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

// lookPath finds name in the folders of path, as the shell would. Only absolute folders count.
func lookPath(name, path string) (string, error) {
	exts := []string{""}
	if runtime.GOOS == "windows" {
		exts = strings.Split(strings.ToLower(os.Getenv("PATHEXT")), ";")
		if os.Getenv("PATHEXT") == "" {
			exts = []string{".com", ".exe", ".bat", ".cmd"}
		}
	}
	for _, dir := range filepath.SplitList(path) {
		if !filepath.IsAbs(dir) {
			continue
		}
		for _, ext := range exts {
			if file := filepath.Join(dir, name+ext); executable(file) {
				return file, nil
			}
		}
	}
	return "", exec.ErrNotFound
}

// executable tells that file is a file Djinn can run.
func executable(file string) bool {
	info, err := os.Stat(file)
	return err == nil && !info.IsDir() && (runtime.GOOS == "windows" || info.Mode()&0o111 != 0)
}

var (
	shellOnce sync.Once
	shellPATH string
)

// searchPath is where Djinn looks for the agents: its own PATH, then the PATH of the user's login shell, then the
// folders the agents' installers use (those the Electron window searched, and agy's). A window started from the Finder or a desktop menu gets a short PATH, without
// what the user's shell profile adds (Homebrew, npm, ~/.local/bin). The login shell is asked once.
func searchPath() string {
	shellOnce.Do(func() { shellPATH = loginShellPath(os.Getenv("SHELL")) })
	home, _ := os.UserHomeDir()
	var usual []string
	if runtime.GOOS == "windows" {
		usual = []string{
			filepath.Join(os.Getenv("LOCALAPPDATA"), "agy", "bin"), filepath.Join(os.Getenv("APPDATA"), "npm"),
			filepath.Join(home, ".local", "bin"),
		}
	} else {
		usual = []string{
			filepath.Join(home, ".local", "bin"), "/opt/homebrew/bin", "/usr/local/bin",
			filepath.Join(home, ".npm-global", "bin"), filepath.Join(home, ".volta", "bin"),
			filepath.Join(home, ".bun", "bin"),
		}
	}
	return joinPaths(os.Getenv("PATH"), shellPATH, strings.Join(usual, string(os.PathListSeparator)))
}

// loginShellPath is the PATH that shell sets up as an interactive login shell, as a terminal opens it; empty on
// Windows, without a shell, or when it fails or takes longer than checkTimeout.
func loginShellPath(shell string) string {
	if runtime.GOOS == "windows" || shell == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
	defer cancel()
	// env prints PATH joined by colons whatever the shell (fish keeps it as a list).
	cmd := exec.CommandContext(ctx, shell, "-i", "-l", "-c", "/usr/bin/env")
	detach(cmd)
	cmd.WaitDelay = 500 * time.Millisecond
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	path := ""
	for line := range strings.SplitSeq(string(out), "\n") {
		if v, ok := strings.CutPrefix(line, "PATH="); ok {
			path = strings.TrimSpace(v)
		}
	}
	return path
}

// joinPaths joins PATH lists, each folder once, in order.
func joinPaths(lists ...string) string {
	var dirs []string
	for _, list := range lists {
		for _, dir := range filepath.SplitList(list) {
			if dir != "" && !slices.Contains(dirs, dir) {
				dirs = append(dirs, dir)
			}
		}
	}
	return strings.Join(dirs, string(os.PathListSeparator))
}

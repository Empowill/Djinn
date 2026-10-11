package machine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
)

// The one place where Djinn looks for the agents: the window's agents panel says what it finds there, and the
// workers, the leads and the gates run what it finds, so the two always agree.

// agentDirs are the folders where the agents' installers put their programs, from the home folder unless absolute:
// Claude Code's and agy's, Homebrew, npm, Volta and Bun. A shell adds them in its own startup files, which an app
// started from the Finder or a desktop menu never reads: there claude is not found.
func agentDirs(goos string) []string {
	if goos == "windows" {
		// Installers set the PATH of the user, which a Djinn started before them does not see.
		return []string{
			filepath.Join(os.Getenv("LOCALAPPDATA"), "agy", "bin"), filepath.Join(os.Getenv("APPDATA"), "npm"),
			filepath.Join(".local", "bin"),
		}
	}
	return []string{".local/bin", "/opt/homebrew/bin", "/usr/local/bin", ".npm-global/bin", ".volta/bin", ".bun/bin"}
}

// agentApps are where an application ships an agent's command, tried when the search path has none: the ChatGPT
// and Codex applications for macOS ship codex.
var agentApps = map[string][]string{
	"codex": {
		"/Applications/ChatGPT.app/Contents/Resources/codex-cli/CodexCLI.app/Contents/MacOS/codex",
		"/Applications/Codex.app/Contents/Resources/codex",
	},
}

// AgentPath is path, then the folders of shell (the login shell's PATH), then those of agentDirs that exist, each
// once: what is on path keeps its place and wins.
func AgentPath(goos, path, home, shell string) string {
	var dirs []string
	for _, d := range agentDirs(goos) {
		if !filepath.IsAbs(d) {
			if home == "" {
				continue
			}
			d = filepath.Join(home, d)
		}
		if info, err := os.Stat(d); err == nil && info.IsDir() {
			dirs = append(dirs, d)
		}
	}
	return joinPaths(path, shell, strings.Join(dirs, string(os.PathListSeparator)))
}

// withApps is path with the folder of each agent's application command added, for the agents path lacks: a
// worker that runs "codex" then runs the one the panel found.
func withApps(path string, apps map[string][]string) string {
	for name, files := range apps {
		if _, err := LookPath(name, path); err == nil {
			continue
		}
		for _, file := range files {
			if executable(file) {
				path = joinPaths(path, filepath.Dir(file))
				break
			}
		}
	}
	return path
}

var (
	extending  sync.Mutex
	loginShell = sync.OnceValue(func() string { return loginShellPath(os.Getenv("SHELL")) })
)

// ExtendPath gives Djinn's own PATH the folders where the agents are, as they are now: AgentPath with the login
// shell's PATH, and the folders of the applications that ship an agent. Djinn calls it before it starts a worker or
// a lead and when the panel checks the agents, so that an agent installed while Djinn runs is found at once, by
// both. The login shell is asked once; the first call may wait for it, at most CheckTimeout.
func ExtendPath() {
	home, _ := os.UserHomeDir()
	shell := loginShell()
	extending.Lock()
	defer extending.Unlock()
	path := withApps(AgentPath(runtime.GOOS, os.Getenv("PATH"), home, shell), agentApps)
	if path != os.Getenv("PATH") {
		_ = os.Setenv("PATH", path)
	}
}

// CheckTimeout bounds the login shell Djinn asks for its PATH, and each command the panel runs to check an agent.
var CheckTimeout = 3 * time.Second

// loginShellPath is the PATH that shell sets up as an interactive login shell, as a terminal opens it; empty on
// Windows, without a shell, or when it fails or takes longer than CheckTimeout.
func loginShellPath(shell string) string {
	if runtime.GOOS == "windows" || shell == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), CheckTimeout)
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

// LookPath finds name in the folders of path, as exec.LookPath does for a worker: only absolute folders count.
func LookPath(name, path string) (string, error) {
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

package machine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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
	extending sync.Mutex // one ExtendPath at a time
	loginPATH string     // the login shell's PATH, once AskLoginShell had it; guarded by extending
)

// ExtendPath gives Djinn's own PATH the folders where the agents are, as they are now: AgentPath with the login
// shell's PATH (AskLoginShell), and the folders of the applications that ship an agent. Djinn calls it as djinn up
// starts and before it starts a worker or a lead, so that an agent installed while Djinn runs is found at once. It
// asks no shell: it only looks at folders.
func ExtendPath() {
	home, _ := os.UserHomeDir()
	extending.Lock()
	defer extending.Unlock()
	path := withApps(AgentPath(runtime.GOOS, os.Getenv("PATH"), home, loginPATH), agentApps)
	if path != os.Getenv("PATH") {
		_ = os.Setenv("PATH", path)
	}
}

// loginTimeout is how long a login shell has to give its PATH: a profile that waits for input must not keep the
// app from opening.
const loginTimeout = 10 * time.Second

// loginWaitDelay is how long a child of the profile may keep the output open once the shell is done. Tests shorten it.
var loginWaitDelay = time.Second

// pathMark marks the PATH in what a login shell prints: a profile may print more.
const pathMark = "djinn-login-path="

// AskLoginShell asks the user's login shell for its PATH, which ExtendPath adds from then on. djinn up asks it once,
// when a launcher started it (a menu entry, the Dock, Finder): a graphical session gives its apps its own PATH,
// without what the user's shell profiles add (~/.zshrc's go/bin, nvm's node, an agent's own folder), so a watcher
// would not find gh, nor a lead go. A terminal reads those profiles, and a djinn up started from one has its PATH
// already. Nothing to ask on Windows, which passes the user's PATH to its apps.
func AskLoginShell() error {
	if runtime.GOOS == "windows" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), loginTimeout)
	defer cancel()
	path, err := loginPath(ctx, loginShell(os.Getenv("SHELL")))
	if err != nil {
		return err
	}
	extending.Lock()
	loginPATH = path
	extending.Unlock()
	return nil
}

// loginShell is the shell whose PATH djinn takes: the user's when it is a POSIX one (bash, zsh, ksh or sh) and
// runs, zsh otherwise, the default of macOS.
func loginShell(shell string) string {
	switch filepath.Base(shell) {
	case "bash", "zsh", "ksh", "sh":
		if info, err := os.Stat(shell); err == nil && !info.IsDir() && info.Mode().Perm()&0o111 != 0 {
			return shell
		}
	}
	return "/bin/zsh"
}

// loginPath gives the PATH that shell sets as an interactive login shell, reading the user's profiles. Interactive
// too, as VS Code asks: a login shell alone skips ~/.zshrc and ~/.bashrc, where nvm and many agent installers add
// to PATH. What an interactive profile prints is left out by the mark, and one that waits for input is cut by ctx.
func loginPath(ctx context.Context, shell string) (string, error) {
	cmd := exec.CommandContext(ctx, shell, "-i", "-l", "-c", `printf '\n%s%s\n' "$1" "$PATH"`, "djinn", pathMark)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.WaitDelay = loginWaitDelay
	detach(cmd)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %w", shell, err)
	}
	lines := strings.Split(out.String(), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if path, ok := strings.CutPrefix(lines[i], pathMark); ok && path != "" {
			return path, nil
		}
	}
	return "", errors.New(shell + " printed no PATH")
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

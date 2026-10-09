package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/term"
)

// loginTimeout is how long a login shell has to give its PATH: a profile that waits for input must not keep the
// app from opening.
const loginTimeout = 10 * time.Second

// loginWaitDelay is how long a child of the profile may keep the output open once the shell is done. Tests shorten it.
var loginWaitDelay = time.Second

// pathMark marks the PATH in what a login shell prints: a profile may print more.
const pathMark = "djinn-login-path="

// fromBundle gives the arguments djinn runs with. Finder, the Dock and Launchpad start Djinn.app with no arguments
// (older macOS with a -psn_… one), where djinn alone would print its help: djinn started from its bundle runs up.
func fromBundle(args []string) []string {
	if runtime.GOOS != "darwin" || !fromFinder(args) {
		return args
	}
	exe, err := os.Executable()
	if err != nil || !bundled(exe) {
		return args
	}
	return []string{"up"}
}

// fromLauncher completes the PATH of a djinn up a launcher started, the same on Linux and macOS: a menu entry, the
// Dock, Finder. A graphical session gives its apps its own PATH, without what the user's shell profiles add
// (~/.zshrc's go/bin, nvm's node, an agent's own folder), so a watcher would not find gh, nor a lead go; a terminal
// reads those profiles, and a djinn up started from one has its PATH already. It adds the folders of the login
// shell's PATH that are missing, after the others: what the PATH already holds comes first, as a test's fake agents
// do. Windows passes the user's PATH to its apps: nothing to do there.
func fromLauncher(args []string, say io.Writer) {
	if !launcherPath || runtime.GOOS == "windows" || len(args) == 0 || args[0] != "up" || term.IsTerminal(int(os.Stdin.Fd())) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), loginTimeout)
	defer cancel()
	path, err := loginPath(ctx, loginShell(os.Getenv("SHELL")))
	if err == nil {
		err = os.Setenv("PATH", joinPaths(os.Getenv("PATH"), path))
	}
	if err != nil {
		fmt.Fprintf(say, "djinn: the PATH of the login shell: %v; the agents may not be found\n", err)
	}
}

// launcherPath completes a launcher's PATH; the tests' djinn up, started without a terminal, keep the PATH they give.
var launcherPath = true

// joinPaths is the PATH have, then each folder of add it does not hold, in add's order.
func joinPaths(have, add string) string {
	seen := map[string]bool{}
	var out []string
	for _, list := range []string{have, add} {
		for _, dir := range filepath.SplitList(list) {
			if dir != "" && !seen[dir] {
				seen[dir] = true
				out = append(out, dir)
			}
		}
	}
	return strings.Join(out, string(os.PathListSeparator))
}

// bundled reports whether exe is the executable of a macOS app bundle: …/Djinn.app/Contents/MacOS/djinn.
func bundled(exe string) bool {
	macos := filepath.Dir(exe)
	contents := filepath.Dir(macos)
	return filepath.Base(exe) == "djinn" && filepath.Base(macos) == "MacOS" && filepath.Base(contents) == "Contents" &&
		strings.EqualFold(filepath.Ext(filepath.Dir(contents)), ".app")
}

// fromFinder reports whether args are what macOS gives an app it opens: none, or a process serial number.
func fromFinder(args []string) bool {
	for _, a := range args {
		if !strings.HasPrefix(a, "-psn_") {
			return false
		}
	}
	return true
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

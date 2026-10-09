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
)

// loginTimeout is how long a login shell has to give its PATH: a profile that waits for input must not keep the
// app from opening.
const loginTimeout = 10 * time.Second

// pathMark marks the PATH in what a login shell prints: a profile may print more.
const pathMark = "djinn-login-path="

// fromBundle gives the arguments djinn runs with. Finder, the Dock and Launchpad start Djinn.app with no arguments
// (older macOS with a -psn_… one), where djinn alone would print its help: djinn started from its bundle runs up.
// macOS gives such an app a bare PATH, without the agents' commands: it takes the PATH of the user's login shell
// first, as a terminal would.
func fromBundle(args []string, say io.Writer) []string {
	if runtime.GOOS != "darwin" || !fromFinder(args) {
		return args
	}
	exe, err := os.Executable()
	if err != nil || !bundled(exe) {
		return args
	}
	ctx, cancel := context.WithTimeout(context.Background(), loginTimeout)
	defer cancel()
	path, err := loginPath(ctx, loginShell(os.Getenv("SHELL")))
	if err == nil {
		err = os.Setenv("PATH", path)
	}
	if err != nil {
		fmt.Fprintf(say, "djinn: the PATH of the login shell: %v; the agents may not be found\n", err)
	}
	return []string{"up"}
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
	cmd.WaitDelay = time.Second // A child of the profile may keep the output open.
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

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/empowill/djinn/internal/machine"
	"golang.org/x/term"
)

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

// fromLauncher completes the PATH of a djinn up, with the folders where the agents are (machine.ExtendPath). Started
// by a launcher, the same on Linux and macOS (a menu entry, the Dock, Finder), it first asks the login shell for its
// PATH, once (machine.AskLoginShell): what the PATH already holds comes first, as a test's fake agents do.
func fromLauncher(args []string, say io.Writer) {
	if !extendPath || len(args) == 0 || args[0] != "up" {
		return
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		if err := machine.AskLoginShell(); err != nil {
			fmt.Fprintf(say, "djinn: the PATH of the login shell: %v; the agents may not be found\n", err)
		}
	}
	machine.ExtendPath()
}

// extendPath completes djinn up's PATH, as it starts and before each worker and lead starts; the tests' djinn up,
// started without a terminal, keep the PATH they give.
var extendPath = true

// beforeStart is what djinn up runs before a worker or a lead starts: machine.ExtendPath, so that an agent
// installed while Djinn runs is found at once; nil in the tests (extendPath).
func beforeStart() func() {
	if !extendPath {
		return nil
	}
	return machine.ExtendPath
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

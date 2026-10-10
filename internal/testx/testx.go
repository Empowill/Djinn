// Package testx holds what the tests of several packages share: for now, the mark of a test that checks portable
// logic, which the Windows job skips (CONTRIBUTING.md).
package testx

import (
	"os"
	"testing"
)

// SystemOnly is the variable that keeps only the tests that target the system: the Windows job of CI sets it to 1.
const SystemOnly = "DJINN_TEST_SYSTEM_ONLY"

// Portable marks a test that pays for git, processes or a whole djinn up to check Djinn's own logic, the same on every
// system: it skips where SystemOnly is 1, as Linux and macOS prove it. Never mark a test that reaches a _windows.go
// file, a runtime.GOOS branch, paths, processes or the terminal: that is what Windows must run.
func Portable(t testing.TB) {
	t.Helper()
	if os.Getenv(SystemOnly) == "1" {
		t.Skip("portable logic, proven on Linux and macOS: skipped where " + SystemOnly + "=1, the Windows job " +
			"(CONTRIBUTING.md)")
	}
}

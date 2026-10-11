// Package testx holds what the tests of several packages share: for now, the mark of a test that checks portable
// logic, which the Windows job skips (CONTRIBUTING.md).
package testx

import (
	"os"
	"path/filepath"
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

// Guard sets DJINN_HOME and DJINN_ADDR to a temporary directory before tests run, so an inherited value
// is never used. It also sets DJINN_TEST=1. It returns a cleanup function to restore the environment and
// delete the directory.
func Guard() func() {
	dir, err := os.MkdirTemp("", "djinn-test-home-*")
	if err != nil {
		panic(err)
	}
	oldHome, hasHome := os.LookupEnv("DJINN_HOME")
	oldAddr, hasAddr := os.LookupEnv("DJINN_ADDR")
	oldTest, hasTest := os.LookupEnv("DJINN_TEST")

	os.Setenv("DJINN_HOME", dir)
	os.Setenv("DJINN_ADDR", "unix://"+filepath.Join(dir, "djinn.sock"))
	os.Setenv("DJINN_TEST", "1")

	return func() {
		os.RemoveAll(dir)
		if hasHome {
			os.Setenv("DJINN_HOME", oldHome)
		} else {
			os.Unsetenv("DJINN_HOME")
		}
		if hasAddr {
			os.Setenv("DJINN_ADDR", oldAddr)
		} else {
			os.Unsetenv("DJINN_ADDR")
		}
		if hasTest {
			os.Setenv("DJINN_TEST", oldTest)
		} else {
			os.Unsetenv("DJINN_TEST")
		}
	}
}

// Main runs m after setting DJINN_HOME and DJINN_ADDR to a temporary directory with Guard, and exits with m's code.
func Main(m *testing.M) {
	cleanup := Guard()
	code := m.Run()
	cleanup()
	os.Exit(code)
}

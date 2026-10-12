// Package testx holds what the tests of several packages share: the mark of a test that checks portable logic, which
// the Windows job skips (CONTRIBUTING.md), and the environment every test process starts from (Guard).
package testx

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
// is never used. It also sets DJINN_TEST=1, and hides the machine's git identity (HideGitIdentity). It returns a
// cleanup function to restore the environment and delete the directory.
func Guard() func() {
	dir, err := os.MkdirTemp("", "djinn-test-home-*")
	if err != nil {
		panic(err)
	}
	restore := saveEnv(append([]string{"DJINN_HOME", "DJINN_ADDR", "DJINN_TEST"}, gitIdentityEnv...))
	os.Setenv("DJINN_HOME", dir)
	os.Setenv("DJINN_ADDR", "unix://"+filepath.Join(dir, "djinn.sock"))
	os.Setenv("DJINN_TEST", "1")
	if err := HideGitIdentity(dir); err != nil {
		panic(err)
	}
	return func() {
		os.RemoveAll(dir)
		restore()
	}
}

// gitIdentityEnv are the variables git reads an identity from, and the one that names its global config.
var gitIdentityEnv = []string{"GIT_CONFIG_GLOBAL", "GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME",
	"GIT_COMMITTER_EMAIL", "EMAIL"}

// HideGitIdentity leaves git, in this process and the ones it starts, as a CI runner has it: no identity, so that a
// commit fails with "empty ident name" where a developer's machine would sign it with theirs. A test repository that
// commits sets its own identity (git config user.name); Djinn's merges run in such repositories. The global config
// is still read, through an include in a file of dir that GIT_CONFIG_GLOBAL names, for its other settings (the
// Windows job's core.longpaths), and its user.name is emptied after it; a repository's own config comes after both.
func HideGitIdentity(dir string) error {
	var includes []string
	if g, ok := os.LookupEnv("GIT_CONFIG_GLOBAL"); ok {
		includes = append(includes, g)
	} else {
		if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
			includes = append(includes, filepath.Join(x, "git", "config"))
		}
		if home, err := os.UserHomeDir(); err == nil {
			includes = append(includes, filepath.Join(home, ".config", "git", "config"), filepath.Join(home, ".gitconfig"))
		}
	}
	var b strings.Builder
	for _, path := range includes {
		fmt.Fprintf(&b, "[include]\n\tpath = %s\n", strconv.Quote(filepath.ToSlash(path)))
	}
	b.WriteString("[user]\n\tname = \"\"\n\temail = \"\"\n")
	global := filepath.Join(dir, "gitconfig")
	if err := os.WriteFile(global, []byte(b.String()), 0o600); err != nil {
		return err
	}
	for _, key := range gitIdentityEnv {
		os.Unsetenv(key)
	}
	return os.Setenv("GIT_CONFIG_GLOBAL", global)
}

// saveEnv returns a function that gives the keys back the values they have now, unset ones unset.
func saveEnv(keys []string) func() {
	old := map[string]string{}
	for _, key := range keys {
		if v, ok := os.LookupEnv(key); ok {
			old[key] = v
		}
	}
	return func() {
		for _, key := range keys {
			if v, ok := old[key]; ok {
				os.Setenv(key, v)
			} else {
				os.Unsetenv(key)
			}
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

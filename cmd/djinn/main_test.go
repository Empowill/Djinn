package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/empowill/djinn/internal/cli"
	"github.com/empowill/djinn/internal/testx"
	"github.com/empowill/djinn/internal/ui"
)

// TestHandwritten fails when a command written here (handwritten) runs without its entry in cli.Builtins, which is
// its --help and its place in the Command line tab of the documentation, or when an entry runs nowhere.
func TestHandwritten(t *testing.T) {
	t.Parallel()
	documented := map[string]bool{}
	for _, b := range cli.Builtins {
		documented[b.Name] = true
		if handwritten[b.Name] != nil {
			continue
		}
		// The others are cli.Run's own.
		var out, errs bytes.Buffer
		if code := cli.Run(t.Context(), append(strings.Fields(b.Name), "--help"), cli.Config{Stdout: &out, Stderr: &errs}); code != 0 ||
			!strings.HasPrefix(out.String(), "Usage: djinn "+b.Name) {
			t.Errorf("djinn %s is documented and runs nowhere: %d %s%s", b.Name, code, out.String(), errs.String())
		}
	}
	for name := range handwritten {
		if !documented[name] {
			t.Errorf("djinn %s runs and is not documented: add it to cli.Builtins", name)
		}
		if b, _, _ := cli.Builtin(strings.Fields(name)); b.Name != name {
			t.Errorf("djinn %s does not reach its command: %q", name, b.Name)
		}
	}
}

// TestModuleVersion: a stamped version wins; without one, a test binary (built from the checkout) stays "dev".
func TestModuleVersion(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ name, in, want string }{
		{"stamped", "local-abc", "local-abc"},
		{"from a checkout", "dev", "dev"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := moduleVersion(c.in); got != c.want {
				t.Errorf("moduleVersion(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestMainRefusesRealData: main, run by a test with the real data folder in its environment, or under the name of the
// e2e binary without the address and the data folder the test harness gives, refuses it loudly.
func TestMainRefusesRealData(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	realHome := ui.RealHome()
	if realHome == "" {
		t.Skip("no real home directory found on this system")
	}
	// without drops the variables that name a data folder, an address or a task from the environment.
	without := func(names ...string) []string {
		var env []string
		for _, kv := range os.Environ() {
			if !slices.ContainsFunc(names, func(n string) bool { return strings.HasPrefix(kv, n+"=") }) {
				env = append(env, kv)
			}
		}
		return append(env, "DJINN_TEST_MAIN=1")
	}
	e2e := filepath.Join(t.TempDir(), "djinn-e2e")
	data, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e2e, data, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, exe string
		env       []string
		want      string
	}{
		{"the real home given", os.Args[0], append(os.Environ(), "DJINN_TEST_MAIN=1", "DJINN_HOME="+realHome),
			"refusing to use real data directory"},
		{"the real home by default", os.Args[0], without("DJINN_HOME"), "refusing to use real data directory"},
		{"the e2e binary without the harness", e2e, without("DJINN_HOME", "DJINN_ADDR", "DJINN_TEST", "DJINN_TASK_ID", "DJINN_WISH_ID"),
			"without an address and a data folder given by the test harness, start a Djinn of your own (a DJINN_HOME of its own, djinn up on its own socket)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			cmd := exec.Command(c.exe, "wish", "make", "Culprit")
			cmd.Env = c.env
			out, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(out), c.want) {
				t.Fatalf("djinn wish make: %v, want a refusal saying %q:\n%s", err, c.want, out)
			}
		})
	}
}

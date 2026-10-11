package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/empowill/djinn/internal/cli"
	"github.com/empowill/djinn/internal/testx"
	"github.com/empowill/djinn/internal/ui"
)

// TestEveryCommandIsDocumented fails when a command written here runs without its entry in cli.Builtins, which is
// its --help and its place in the Command line tab of the documentation, or when an entry runs nowhere.
func TestEveryCommandIsDocumented(t *testing.T) {
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

// TestCliRefusesRealHome: running a CLI command from a test with the real home in the environment does not reach it,
// failing loudly.
func TestCliRefusesRealHome(t *testing.T) {
	testx.Portable(t)
	realHome := ui.RealHome()
	if realHome == "" {
		t.Skip("no real home directory found on this system")
	}

	t.Run("explicit real home", func(t *testing.T) {
		cmd := exec.Command(os.Args[0], "wish", "make", "Culprit")
		cmd.Env = append(os.Environ(), "DJINN_TEST_MAIN=1", "DJINN_HOME="+realHome)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("command unexpectedly succeeded with output: %s", out)
		}
		if !strings.Contains(string(out), "refusing to use real data directory") {
			t.Fatalf("expected refusal message, got: %s", out)
		}
	})

	t.Run("default real home", func(t *testing.T) {
		cmd := exec.Command(os.Args[0], "wish", "make", "Culprit")
		var env []string
		for _, kv := range os.Environ() {
			if !strings.HasPrefix(kv, "DJINN_HOME=") {
				env = append(env, kv)
			}
		}
		cmd.Env = append(env, "DJINN_TEST_MAIN=1")
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("command unexpectedly succeeded with output: %s", out)
		}
		if !strings.Contains(string(out), "refusing to use real data directory") {
			t.Fatalf("expected refusal message, got: %s", out)
		}
	})
}

// TestE2EBinaryRefusesWithoutHarness: a binary named djinn-e2e executed without test harness environment
// refuses to dial or use the real data directory.
func TestE2EBinaryRefusesWithoutHarness(t *testing.T) {
	testx.Portable(t)
	tmp := t.TempDir()
	e2eBin := filepath.Join(tmp, "djinn-e2e")
	data, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e2eBin, data, 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(e2eBin, "wish", "make", "StrayWish")
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "DJINN_HOME=") || strings.HasPrefix(kv, "DJINN_ADDR=") ||
			strings.HasPrefix(kv, "DJINN_TEST=") || strings.HasPrefix(kv, "DJINN_TASK_ID=") ||
			strings.HasPrefix(kv, "DJINN_WISH_ID=") {
			continue
		}
		env = append(env, kv)
	}
	cmd.Env = append(env, "DJINN_TEST_MAIN=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("djinn-e2e unexpectedly succeeded: %s", out)
	}
	want := "without an address and a data folder given by the test harness, start a Djinn of your own (a DJINN_HOME of its own, djinn up on its own socket)"
	if !strings.Contains(string(out), want) {
		t.Fatalf("expected refusal message containing %q, got: %s", want, out)
	}
}

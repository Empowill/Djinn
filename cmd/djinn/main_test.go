package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/empowill/djinn/internal/cli"
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

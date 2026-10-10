package main

import (
	"bytes"
	"runtime/debug"
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

func TestModuleVersionFromBuildInfo(t *testing.T) {
	for _, tt := range []struct {
		name, stamped, main, want string
		settings                  []debug.BuildSetting
	}{
		{
			name: "local pseudo version with dirty checkout metadata", stamped: "dev", main: "v0.0.0-20261010140310-4fd1c055e61c+dirty",
			settings: []debug.BuildSetting{{Key: "vcs", Value: "git"}, {Key: "vcs.revision", Value: "4fd1c055e61c"}, {Key: "vcs.modified", Value: "true"}}, want: "dev",
		},
		{name: "old devel build", stamped: "dev", main: "(devel)", want: "dev"},
		{name: "module release without checkout metadata", stamped: "dev", main: "v0.1.0", want: "v0.1.0"},
		{name: "stamped local build", stamped: "local-4fd1c055e61c", main: "v0.1.0", want: "local-4fd1c055e61c"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			info := &debug.BuildInfo{Main: debug.Module{Version: tt.main}, Settings: tt.settings}
			if got := moduleVersionFromBuildInfo(tt.stamped, info); got != tt.want {
				t.Errorf("moduleVersionFromBuildInfo(%q, %q) = %q, want %q", tt.stamped, tt.main, got, tt.want)
			}
		})
	}
}

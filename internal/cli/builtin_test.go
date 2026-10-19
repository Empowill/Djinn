package cli

import (
	"slices"
	"testing"
)

func TestBuiltin(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want string
		rest []string
	}{
		{"a command", []string{"up", "--browser"}, "up", []string{"--browser"}},
		{"a command with flags", []string{"backup", "--file", "a.zip"}, "backup", []string{"--file", "a.zip"}},
		{"a subcommand", []string{"backup", "restore", "a.zip"}, "backup restore", []string{"a.zip"}},
		{"a subcommand of a service", []string{"gate", "run", "e2e", "--", "true"}, "gate run", []string{"e2e", "--", "true"}},
		{"a method of the same service", []string{"gate", "hold", "e2e"}, "", []string{"gate", "hold", "e2e"}},
		{"a method", []string{"wish", "make"}, "", []string{"wish", "make"}},
		{"nothing", nil, "", nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b, rest, ok := Builtin(tt.args)
			if b.Name != tt.want || ok != (tt.want != "") || !slices.Equal(rest, tt.rest) {
				t.Errorf("Builtin(%q) = %q, %q, %v", tt.args, b.Name, rest, ok)
			}
		})
	}
}

func TestFlagSet(t *testing.T) {
	c := Command{Name: "x", Flags: []Param{
		{Name: "--on", Type: "bool", Default: "true", Help: "On."},
		{Name: "--n", Type: "int", Help: "N."},
		{Name: "--f", Type: "float", Default: "1.5", Help: "F."},
		{Name: "--s", Type: "string", Help: "S."},
	}}
	t.Run("defaults and values", func(t *testing.T) {
		var on bool
		var n int
		var f float64
		var s string
		fs := c.FlagSet(map[string]any{"on": &on, "n": &n, "f": &f, "s": &s})
		if !on {
			t.Error("--on does not default to true")
		}
		if f != 1.5 {
			t.Errorf("--f does not default to 1.5, got %v", f)
		}
		if err := fs.Parse([]string{"--on=false", "--n", "3", "--f=2.5", "--s=a"}); err != nil || on || n != 3 || f != 2.5 || s != "a" {
			t.Errorf("parse: %v, %v %d %f %q", err, on, n, f, s)
		}
	})
	var on bool
	var n int
	var f float64
	var s string
	for _, tt := range []struct {
		name string
		vars map[string]any
	}{
		{"a flag without its variable panics", map[string]any{"on": &on, "n": &n, "f": &f}},
		{"a variable without its flag panics", map[string]any{"on": &on, "n": &n, "f": &f, "s": &s, "t": &s}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("no panic")
				}
			}()
			c.FlagSet(tt.vars)
		})
	}
}

package cli

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

// TestReference checks that the documentation of the command line has every command once: each public method in the
// group of its service, with an argument or a flag per field, and each command written by hand; each with its usage,
// its help, and a type and a help for each argument and flag.
func TestReference(t *testing.T) {
	groups := Reference()
	if groups[0].Name != Own {
		t.Errorf("the first group is %q, want djinn's own commands", groups[0].Name)
	}
	documented := map[string]Command{}
	for _, g := range groups {
		if g.Help == "" {
			t.Errorf("group %s has no help", g.Name)
		}
		for _, c := range g.Commands {
			if _, twice := documented[c.Name]; twice {
				t.Errorf("djinn %s is documented twice", c.Name)
			}
			documented[c.Name] = c
			if g.Name != Own && !strings.HasPrefix(c.Name, g.Name+" ") {
				t.Errorf("djinn %s is in the group %s", c.Name, g.Name)
			}
			if !strings.HasPrefix(c.Usage, "djinn "+c.Name) || c.Summary == "" || c.Help == "" {
				t.Errorf("djinn %s: usage %q, summary %q, help %q", c.Name, c.Usage, c.Summary, c.Help)
			}
			for _, p := range slices.Concat(c.Args, c.Flags) {
				if p.Type == "" || p.Help == "" {
					t.Errorf("djinn %s %s: type %q, help %q", c.Name, p.Name, p.Type, p.Help)
				}
			}
		}
	}
	for _, sd := range commands() {
		for _, md := range public(sd) {
			name := command(sd) + " " + kebab(string(md.Name()))
			c, ok := documented[name]
			if !ok {
				t.Errorf("djinn %s is not documented", name)
				continue
			}
			if got := len(c.Args) + len(c.Flags); got != md.Input().Fields().Len() {
				t.Errorf("djinn %s documents %d arguments and flags, its request has %d fields", name, got, md.Input().Fields().Len())
			}
		}
	}
	for _, b := range Builtins {
		if _, ok := documented[b.Name]; !ok {
			t.Errorf("djinn %s is written by hand and not documented", b.Name)
		}
	}
	answer := documented["question answer"]
	if len(answer.Args) != 2 || answer.Args[1].Type != "enum" || !slices.Contains(answer.Args[1].Values, "yes") {
		t.Errorf("djinn question answer: %+v", answer.Args)
	}
	if !slices.Contains(documented["wish brief"].Tags, "reads only") {
		t.Errorf("djinn wish brief tags %v", documented["wish brief"].Tags)
	}
}

func TestBuiltin(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
		rest []string
	}{
		{[]string{"up", "--browser"}, "up", []string{"--browser"}},
		{[]string{"backup", "--file", "a.zip"}, "backup", []string{"--file", "a.zip"}},
		{[]string{"backup", "restore", "a.zip"}, "backup restore", []string{"a.zip"}},
		{[]string{"gate", "run", "e2e", "--", "true"}, "gate run", []string{"e2e", "--", "true"}},
		{[]string{"gate", "hold", "e2e"}, "", []string{"gate", "hold", "e2e"}},
		{[]string{"wish", "make"}, "", []string{"wish", "make"}},
		{nil, "", nil},
	} {
		b, rest, ok := Builtin(tc.args)
		if b.Name != tc.want || ok != (tc.want != "") || !slices.Equal(rest, tc.rest) {
			t.Errorf("Builtin(%q) = %q, %q, %v", tc.args, b.Name, rest, ok)
		}
	}
}

// TestBuiltinHelp checks that djinn help answers for the commands written by hand too, from the same text.
func TestBuiltinHelp(t *testing.T) {
	for _, args := range [][]string{{"help", "gate", "run"}, {"gate", "run", "--help"}, {"help", "up"}, {"backup", "restore", "-h"}} {
		var out, errs bytes.Buffer
		if code := Run(t.Context(), args, Config{Stdout: &out, Stderr: &errs}); code != 0 {
			t.Errorf("djinn %q exited %d: %s", args, code, errs.String())
		}
		b, _, _ := Builtin(slices.DeleteFunc(slices.Clone(args), func(a string) bool { return a == "help" || a[0] == '-' }))
		var want bytes.Buffer
		b.WriteHelp(&want)
		if out.String() != want.String() {
			t.Errorf("djinn %q printed %q", args, out.String())
		}
	}
	var out bytes.Buffer
	Run(t.Context(), []string{"gate"}, Config{Stdout: &out, Stderr: &out})
	if !strings.Contains(out.String(), "\n  run ") {
		t.Errorf("djinn gate does not list run:\n%s", out.String())
	}
	out.Reset()
	Run(t.Context(), nil, Config{Stdout: &out, Stderr: &out})
	for _, name := range []string{"up", "open", "update", "backup", "version", "mcp", "--addr value"} {
		if !strings.Contains(out.String(), "\n  "+name+" ") {
			t.Errorf("djinn does not list %s:\n%s", name, out.String())
		}
	}
}

func TestFlagSet(t *testing.T) {
	c := Command{Name: "x", Flags: []Param{
		{Name: "--on", Type: "bool", Default: "true", Help: "On."},
		{Name: "--n", Type: "int", Help: "N."},
		{Name: "--f", Type: "float", Default: "1.5", Help: "F."},
		{Name: "--s", Type: "string", Help: "S."},
	}}
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
	for name, vars := range map[string]map[string]any{
		"a flag without its variable": {"on": &on, "n": &n, "f": &f},
		"a variable without its flag": {"on": &on, "n": &n, "f": &f, "s": &s, "t": &s},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s does not panic", name)
				}
			}()
			c.FlagSet(vars)
		}()
	}
}

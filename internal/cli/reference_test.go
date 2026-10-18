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

// TestWriteHelp checks that djinn help answers for the commands written by hand too, from the same text.
func TestWriteHelp(t *testing.T) {
	for _, args := range [][]string{{"help", "gate", "run"}, {"gate", "run", "--help"}, {"help", "up"}, {"backup", "restore", "-h"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()
			var out, errs bytes.Buffer
			if code := Run(t.Context(), args, Config{Stdout: &out, Stderr: &errs}); code != 0 {
				t.Errorf("exited %d: %s", code, errs.String())
			}
			b, _, _ := Builtin(slices.DeleteFunc(slices.Clone(args), func(a string) bool { return a == "help" || a[0] == '-' }))
			var want bytes.Buffer
			b.WriteHelp(&want)
			if out.String() != want.String() {
				t.Errorf("printed %q, want %q", out.String(), want.String())
			}
		})
	}
}

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/empowill/djinn/internal/cli"
)

// The user guide describes the app as it is: every djinn command it names exists, with the flags it gives, and every
// label it names in bold is a text of the window. A command or a button renamed without the guide fails here.

var (
	// fence is a fenced code block of the guide.
	fence = regexp.MustCompile("(?s)```[a-z]*\n(.*?)```")
	// span is an inline code span; one may wrap over two lines.
	span = regexp.MustCompile("`([^`]+)`")
	// bold is a phrase in bold. One that ends a sentence is emphasis; any other is a label of the window.
	bold = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	// placeholder is a value a label takes, {wish}; the guide writes an example or <wish> in its place.
	placeholder = regexp.MustCompile(`\\\{\w+\\\}`)
)

// builtIn are the commands cmd/djinn runs itself, outside the protos: their flags are not checked here.
var builtIn = map[string]bool{"gate": true, "backup": true, "open": true, "update": true, "mcp": true}

// guideCommands returns the djinn commands of the guide, each as its words.
func guideCommands(guide string) [][]string {
	var commands [][]string
	add := func(line string) {
		line = strings.TrimSpace(line)
		if i := strings.Index(line, " #"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if strings.HasPrefix(line, "djinn ") {
			commands = append(commands, words(line)[1:])
		}
	}
	for _, m := range fence.FindAllStringSubmatch(guide, -1) {
		for line := range strings.SplitSeq(m[1], "\n") {
			add(line)
		}
	}
	for _, m := range span.FindAllStringSubmatch(fence.ReplaceAllString(guide, ""), -1) {
		add(strings.ReplaceAll(m[1], "\n", " "))
	}
	return commands
}

// words splits a command line on spaces, a quoted string being one word.
func words(line string) []string {
	var out []string
	var word strings.Builder
	quoted := false
	for _, r := range line {
		switch {
		case r == '"':
			quoted = !quoted
		case r == ' ' && !quoted:
			if word.Len() > 0 {
				out = append(out, word.String())
				word.Reset()
			}
		default:
			word.WriteRune(r)
		}
	}
	if word.Len() > 0 {
		out = append(out, word.String())
	}
	return out
}

func TestUserGuide(t *testing.T) {
	data, err := os.ReadFile("../../docs/user-guide.md")
	if err != nil {
		t.Fatal(err)
	}
	guide := strings.ReplaceAll(string(data), "\r\n", "\n")

	t.Run("commands", func(t *testing.T) {
		commands := guideCommands(guide)
		if len(commands) < 20 {
			t.Fatalf("found %d djinn commands in the guide, expected many more: is the parsing broken?", len(commands))
		}
		for _, args := range commands {
			line := "djinn " + strings.Join(args, " ")
			switch {
			case builtIn[args[0]]:
			case args[0] == "up":
				// Parsing stops at --help, after every flag the guide gives: an unknown one fails before it.
				if _, err := runUp(append(args[1:], "--help")); !errors.Is(err, flag.ErrHelp) {
					t.Errorf("%s: %v", line, err)
				}
			default:
				if len(args) < 2 {
					t.Errorf("%s: a command names its service and its method", line)
					continue
				}
				var out, errs bytes.Buffer
				code := cli.Run(t.Context(), []string{args[0], args[1], "--help"},
					cli.Config{Stdout: &out, Stderr: &errs})
				if code != 0 {
					t.Errorf("%s: %s", line, strings.TrimSpace(errs.String()))
					continue
				}
				help := out.String()
				if usage := "Usage: djinn " + args[0] + " " + args[1]; !strings.HasPrefix(help, usage) {
					t.Errorf("%s: not its full name, the help says %q", line, strings.SplitN(help, "\n", 2)[0])
				}
				for _, a := range args[2:] {
					name, _, _ := strings.Cut(a, "=")
					if strings.HasPrefix(name, "--") && !regexp.MustCompile(`(?m)^\s+`+name+`\b`).MatchString(help) {
						t.Errorf("%s: %s is no flag of djinn %s %s", line, name, args[0], args[1])
					}
				}
			}
		}
	})

	t.Run("labels", func(t *testing.T) {
		data, err := os.ReadFile("../../locales/en.json")
		if err != nil {
			t.Fatal(err)
		}
		var texts map[string]string
		if err := json.Unmarshal(data, &texts); err != nil {
			t.Fatal(err)
		}
		var labels []*regexp.Regexp
		for _, text := range texts {
			labels = append(labels, regexp.MustCompile("^"+placeholder.ReplaceAllString(regexp.QuoteMeta(text), ".+")+"$"))
		}
		for _, m := range bold.FindAllStringSubmatch(guide, -1) {
			phrase := strings.ReplaceAll(m[1], "\n", " ")
			if strings.HasSuffix(phrase, ".") {
				continue
			}
			found := false
			for _, label := range labels {
				if label.MatchString(phrase) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("**%s** is no text of the window (locales/en.json): a label, or a sentence ending in a period", phrase)
			}
		}
	})
}

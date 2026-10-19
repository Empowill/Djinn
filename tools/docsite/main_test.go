package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestBuild builds the site of the repository and checks that every file index.html names is there: the styles,
// the scripts, the fonts and the mark; that each page has its script; and that the Command line's holds the commands.
func TestBuild(t *testing.T) {
	t.Parallel()
	if err := build(t.TempDir(), filepath.Join(t.TempDir(), "docs")); err == nil {
		t.Error("a repository without a site builds")
	}
	out := filepath.Join(t.TempDir(), "docs")
	if err := os.MkdirAll(filepath.Join(out, "stale"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := build(filepath.Join("..", ".."), out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "stale")); !os.IsNotExist(err) {
		t.Error("what the folder held stays")
	}
	index, err := os.ReadFile(filepath.Join(out, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	names := regexp.MustCompile(`(?:src|href)="([^"#:]+)"`).FindAllStringSubmatch(string(index), -1)
	css, err := os.ReadFile(filepath.Join(out, "site.css"))
	if err != nil {
		t.Fatal(err)
	}
	names = append(names, regexp.MustCompile(`url\("([^"]+)"\)`).FindAllStringSubmatch(string(css), -1)...)
	if len(names) < 10 {
		t.Fatalf("only %d files named", len(names))
	}
	for _, m := range names {
		if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(m[1]))); err != nil {
			t.Errorf("%s is named and missing: %v", m[1], err)
		}
	}
	// Each page is a script of its own, the Command line's with the commands; index.html loads them.
	commands, err := os.ReadFile(filepath.Join(out, "pages", "commands.js"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`id=\"cmd-wish-make\"`, `id=\"cmd-gate-run\"`, `id=\"cmd-up\"`, `id=\"group-tilasm\"`} {
		if !strings.Contains(string(commands), want) {
			t.Errorf("pages/commands.js has no %s", want)
		}
	}
	for _, p := range []string{"introduction", "wish", "gates"} {
		if _, err := os.Stat(filepath.Join(out, "pages", p+".js")); err != nil {
			t.Errorf("page %s has no script: %v", p, err)
		}
	}
}

// TestRun builds into the one folder named and prints where to open it; a wrong count of folders is a usage error,
// a repository without a site an error.
func TestRun(t *testing.T) {
	t.Parallel()
	repo := filepath.Join("..", "..")
	cases := []struct {
		name   string
		repo   string
		args   []string
		code   int
		stdout string
		stderr string
	}{
		{"builds the site", repo, []string{"docs"}, 0, "docsite: open ", ""},
		{"no folder", repo, nil, 2, "", "usage: docsite <folder>"},
		{"two folders", repo, []string{"a", "b"}, 2, "", "usage: docsite <folder>"},
		{"no site", "nowhere", []string{"docs"}, 1, "", "docsite: "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			for i, a := range c.args {
				c.args[i] = filepath.Join(t.TempDir(), a)
			}
			var stdout, stderr bytes.Buffer
			if code := run(c.repo, c.args, &stdout, &stderr); code != c.code {
				t.Fatalf("exit %d, want %d: %s", code, c.code, stderr.String())
			}
			if !strings.Contains(stdout.String(), c.stdout) || !strings.Contains(stderr.String(), c.stderr) {
				t.Errorf("stdout %q, stderr %q; want %q and %q", stdout.String(), stderr.String(), c.stdout, c.stderr)
			}
			if c.code == 0 && !strings.HasSuffix(strings.TrimSpace(stdout.String()), filepath.Join(c.args[0], "index.html")) {
				t.Errorf("stdout %q names no index.html in %s", stdout.String(), c.args[0])
			}
		})
	}
}

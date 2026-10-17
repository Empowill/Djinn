package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestBuild builds the site of the repository and checks that every file index.html names is there: the styles,
// the scripts, the fonts and the mark; that each page has its script; and that the Command line's holds the commands.
func TestBuild(t *testing.T) {
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

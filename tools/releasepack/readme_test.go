package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The install lines at the top of the README follow scripts/install.*: each one takes a script that exists, from the
// releases its script defaults to, says what the script's own usage says, and the release workflow ships that
// script. A change to the install that forgets the README fails here.

// installLine is an install command of the README: what runs, the script's URL, and the shell it is piped into.
var installLine = regexp.MustCompile(`^(curl -fsSL|irm) (https://\S+) \| (sh|iex)\b`)

// defaultReleases finds where a script takes its releases from when DJINN_RELEASES is not set.
var defaultReleases = map[string]*regexp.Regexp{
	"install.sh":  regexp.MustCompile(`releases=\$\{DJINN_RELEASES:-(https://[^}]+)\}`),
	"install.ps1": regexp.MustCompile(`\$env:DJINN_RELEASES\.TrimEnd\('/'\) \} else \{ '(https://[^']+)' \}`),
}

func read(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}

func TestReadmeInstallLines(t *testing.T) {
	readme := read(t, "README.md")
	workflow := read(t, ".github/workflows/release.yml")
	found := map[string]bool{}
	for line := range strings.SplitSeq(readme, "\n") {
		m := installLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		command, url := m[0], m[2]
		name := url[strings.LastIndex(url, "/")+1:]
		if found[name] {
			t.Errorf("README: %s is installed by two lines", name)
		}
		found[name] = true
		want, ok := defaultReleases[name]
		if !ok {
			t.Errorf("README: %q takes %s, which is not a script of scripts/", line, name)
			continue
		}
		script := read(t, "scripts/"+name)
		releases := want.FindStringSubmatch(script)
		if releases == nil {
			t.Errorf("scripts/%s: no default for DJINN_RELEASES", name)
			continue
		}
		if url != releases[1]+"/latest/download/"+name {
			t.Errorf("README: %s comes from %s, want %s/latest/download/%s", name, url, releases[1], name)
		}
		if shell := map[string]string{"install.sh": "sh", "install.ps1": "iex"}[name]; m[3] != shell {
			t.Errorf("README: %s is piped into %s, want %s", name, m[3], shell)
		}
		if !strings.Contains(script, "#   "+command+"\n") {
			t.Errorf("scripts/%s: its usage does not say %q, as the README does", name, command)
		}
		if !strings.Contains(workflow, "scripts/"+name) {
			t.Errorf(".github/workflows/release.yml does not ship scripts/%s, which the README downloads", name)
		}
	}
	for name := range defaultReleases {
		if !found[name] {
			t.Errorf("README: no install line takes scripts/%s", name)
		}
	}
}

func TestReleaseNotes(t *testing.T) {
	notes := read(t, "docs/releases/v0.1.0.md")
	if !strings.HasPrefix(notes, "# Djinn v0.1.0\n") {
		t.Errorf("v0.1.0.md does not start with # Djinn v0.1.0")
	}
	workflow := read(t, ".github/workflows/release.yml")
	if !strings.Contains(workflow, `docs/releases/$GITHUB_REF_NAME.md`) || !strings.Contains(workflow, `--notes-file`) {
		t.Errorf(".github/workflows/release.yml does not use --notes-file with docs/releases/$GITHUB_REF_NAME.md")
	}
	for _, azima := range []string{
		"Djinn is a native window, the same interface as in the browser",
		"What waits for you comes first, every question reads clearly",
		"Every request finds its wish",
		"Djinn decides, starts, resumes, integrates and pushes the workers' work by itself",
		"Workers start fast, with the right context and the right model",
		"Djinn can hand a task to Google's Antigravity CLI",
		"One wish spans several projects, and a project's skills serve in another",
		"Everything starts from the protos",
		"Djinn runs on Windows as well as on macOS and Linux",
		"Your wishes follow you",
		"Djinn installs in a few minutes, and updates from inside the app",
		"Every release ships ready-made binaries for each target",
	} {
		if !strings.Contains(notes, azima) {
			t.Errorf("v0.1.0.md missing azima goal: %s", azima)
		}
	}
	for _, section := range []string{
		"## Install",
		"## Under the hood",
		"## License",
	} {
		if !strings.Contains(notes, section) {
			t.Errorf("v0.1.0.md missing section: %s", section)
		}
	}
}

func TestReleaseWorkflow(t *testing.T) {
	workflow := read(t, ".github/workflows/release.yml")
	for _, want := range []string{
		"types: [published]",
		"tags: [\"v*\"]",
		"workflow_dispatch:",
		"hashFiles('dist/index.html') == ''",
		"npm ci && go tool task ui",
		"gh release upload \"$GITHUB_REF_NAME\" --clobber",
		"gh release edit \"$GITHUB_REF_NAME\" --notes-file",
	} {
		if !strings.Contains(workflow, want) {
			t.Errorf(".github/workflows/release.yml does not contain %q", want)
		}
	}
}

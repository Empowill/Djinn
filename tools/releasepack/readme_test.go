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

// The notes of a release follow the release-notes skill (.agents/skills/release-notes/SKILL.md): the install first,
// sections that each say what is not checked yet, what was fixed, how to upgrade, the credits, and the changelog.
// A release page resolves no relative link, and a task or question code means nothing outside the developer's Djinn.
func TestReleaseNotes(t *testing.T) {
	notes := read(t, "docs/releases/v0.1.2.md")
	if !strings.HasPrefix(notes, "# Djinn v0.1.2\n") {
		t.Errorf("v0.1.2.md does not start with # Djinn v0.1.2")
	}
	workflow := read(t, ".github/workflows/release.yml")
	if !strings.Contains(workflow, `docs/releases/$GITHUB_REF_NAME.md`) || !strings.Contains(workflow, `--notes-file`) {
		t.Errorf(".github/workflows/release.yml does not use --notes-file with docs/releases/$GITHUB_REF_NAME.md")
	}
	for _, section := range []string{
		"## Install",
		"**Still needs:**",
		"## Fixed",
		"## Upgrading from v0.1.1",
		"## Credits",
		"## License",
		"**Full changelog**: https://github.com/Empowill/Djinn/compare/v0.1.1...v0.1.2",
	} {
		if !strings.Contains(notes, section) {
			t.Errorf("v0.1.2.md missing: %s", section)
		}
	}
	if strings.Index(notes, "## Install") > strings.Index(notes, "\n## Djinn") {
		t.Errorf("v0.1.2.md: the install does not come first")
	}
	for _, link := range regexp.MustCompile(`\]\(([^)]*)\)`).FindAllStringSubmatch(notes, -1) {
		if !strings.HasPrefix(link[1], "https://") {
			t.Errorf("v0.1.2.md: link %q is not absolute", link[1])
		}
	}
	if code := regexp.MustCompile(`\b[WQ][0-9]+\b`).FindString(notes); code != "" {
		t.Errorf("v0.1.2.md names the task or question %s", code)
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

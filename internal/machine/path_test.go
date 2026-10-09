package machine

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeAgent writes an executable shell script named name in dir: a fake agent, which says it ran and calls no model.
func fakeAgent(t *testing.T, dir, name string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, name)
	if err := os.WriteFile(file, []byte("#!/bin/sh\necho fake "+name+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return file
}

// bare gives the test the PATH launchd gives an app, a home folder of its own and no login shell to ask.
func bare(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "")
	t.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")
	return home
}

// An app started from the Finder gets a bare PATH, without the folder where Claude Code's installer puts claude:
// a worker (found with LookPath) and a lead (a shell line) did not find it. ExtendPath gives it back.
func TestExtendPathFindsClaude(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake agent is a shell script")
	}
	home := bare(t)
	bin := filepath.Join(home, ".local", "bin")
	fakeAgent(t, bin, "claude")
	if _, err := exec.LookPath("claude"); err == nil {
		t.Fatal("claude found on the bare PATH: the test reproduces nothing")
	}
	if out, err := exec.Command("/bin/sh", "-c", "claude").CombinedOutput(); err == nil {
		t.Fatalf("the shell found claude on the bare PATH: %s", out)
	}

	ExtendPath()
	if got, err := exec.LookPath("claude"); err != nil || got != filepath.Join(bin, "claude") {
		t.Fatalf("LookPath(claude) = %q, %v; want the fake in %s", got, err, bin)
	}
	out, err := exec.Command("/bin/sh", "-c", "claude").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "fake claude" {
		t.Fatalf("the shell ran claude: %q, %v", out, err)
	}
	if path := os.Getenv("PATH"); !strings.HasPrefix(path, "/usr/bin:/bin:/usr/sbin:/sbin:") {
		t.Errorf("PATH = %q: the inherited folders must keep their place, first", path)
	}
}

// On a new Mac, ~/.local/bin does not exist when Djinn starts: the panel's installer creates it. The next
// ExtendPath, before a worker or a lead starts, finds claude there, without a restart.
func TestExtendPathAfterAnInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake agent is a shell script")
	}
	home := bare(t)
	ExtendPath() // djinn up starts
	if _, err := exec.LookPath("claude"); err == nil {
		t.Fatal("claude found before its install: the test reproduces nothing")
	}
	bin := filepath.Join(home, ".local", "bin")
	fakeAgent(t, bin, "claude") // the installer runs in the panel's terminal
	ExtendPath()
	if got, err := exec.LookPath("claude"); err != nil || got != filepath.Join(bin, "claude") {
		t.Fatalf("LookPath(claude) = %q, %v; want the one just installed", got, err)
	}
}

func TestAgentPath(t *testing.T) {
	home := t.TempDir()
	local := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	sep := string(os.PathListSeparator)
	// Present already: not added twice, and its place is kept.
	if got := AgentPath("darwin", local+sep+"/x", home, ""); strings.Count(got, local) != 1 || !strings.HasPrefix(got, local+sep+"/x") {
		t.Errorf("%q: %s added twice or moved", got, local)
	}
	// A folder that does not exist is not added.
	if got := AgentPath("linux", "/x", home, ""); strings.Contains(got, ".bun") || !strings.Contains(got, local) {
		t.Errorf("%q: want %s and no missing folder", got, local)
	}
	// No home folder: only the absolute folders may be added.
	if got := AgentPath("linux", "/x", "", ""); strings.Contains(got, ".local") {
		t.Errorf("%q: a folder of no home", got)
	}
	// The login shell's PATH comes after Djinn's own, before the installers' folders.
	if got := AgentPath("darwin", "/x", home, "/shell"+sep+"/x"); got != strings.Join([]string{"/x", "/shell", local}, sep) {
		t.Errorf("%q: want Djinn's PATH, the shell's, then %s", got, local)
	}
	// Volta's folder is one of them: the window's panel looked there.
	volta := filepath.Join(home, ".volta", "bin")
	if err := os.MkdirAll(volta, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := AgentPath("darwin", "/x", home, ""); !strings.Contains(got, volta) {
		t.Errorf("%q: want %s", got, volta)
	}
}

// A codex only an application ships is run from the application's folder, added to the PATH: the panel says it is
// there, and a worker that runs "codex" runs that one. One already on the PATH wins, and nothing is added.
func TestWithApps(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake agent is a shell script")
	}
	app := filepath.Join(t.TempDir(), "Codex.app", "Contents", "Resources")
	fakeAgent(t, app, "codex")
	apps := map[string][]string{"codex": {filepath.Join(t.TempDir(), "codex"), filepath.Join(app, "codex")}}
	bin := t.TempDir()
	path := withApps(bin, apps)
	if got, err := LookPath("codex", path); err != nil || got != filepath.Join(app, "codex") {
		t.Errorf("LookPath(codex) on %q = %q, %v; want the application's", path, got, err)
	}
	fakeAgent(t, bin, "codex")
	if got := withApps(bin, apps); got != bin {
		t.Errorf("withApps = %q; want %q, its codex first", got, bin)
	}
}

// Only an executable file in an absolute folder counts.
func TestLookPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake agent is a shell script")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte("not executable"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	fakeAgent(t, dir, "agy")
	t.Chdir(dir)
	for _, name := range []string{"codex", "claude"} {
		if got, err := LookPath(name, dir); err == nil {
			t.Errorf("LookPath(%s) = %s; want not found", name, got)
		}
	}
	if got, err := LookPath("agy", "."+string(os.PathListSeparator)+dir); err != nil || got != filepath.Join(dir, "agy") {
		t.Errorf("LookPath(agy) = %s, %v; want the one of the absolute folder", got, err)
	}
	if _, err := LookPath("agy", "."); err == nil {
		t.Error("LookPath(agy) in . found it; want relative folders skipped")
	}
}

// The login shell's PATH is read from what env prints, whatever the shell prints before it.
func TestLoginShellPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Djinn asks no login shell on Windows")
	}
	dir := t.TempDir()
	write := func(name, script string) string {
		file := filepath.Join(dir, name)
		if err := os.WriteFile(file, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
			t.Fatal(err)
		}
		return file
	}
	shell := write("shell", `[ "$1 $2 $3 $4" = "-i -l -c /usr/bin/env" ] || exit 1
echo "Welcome"
echo "HOME=/home/someone"
echo "PATH=/opt/homebrew/bin:/usr/bin"
`)
	if got := loginShellPath(shell); got != "/opt/homebrew/bin:/usr/bin" {
		t.Errorf("loginShellPath = %q; want the shell's PATH", got)
	}
	if got := loginShellPath(write("broken", "exit 1\n")); got != "" {
		t.Errorf("loginShellPath of a failing shell = %q; want empty", got)
	}
	if got := loginShellPath(""); got != "" {
		t.Errorf("loginShellPath without a shell = %q; want empty", got)
	}
}

func TestJoinPaths(t *testing.T) {
	sep := string(os.PathListSeparator)
	in := []string{"/a" + sep + "/b", "/b" + sep + "/c", sep + "/a" + sep + "/d"}
	if got, want := joinPaths(in...), strings.Join([]string{"/a", "/b", "/c", "/d"}, sep); got != want {
		t.Errorf("joinPaths = %q; want %q", got, want)
	}
}

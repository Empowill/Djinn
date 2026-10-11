package machine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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

// Started by a launcher, djinn up asks the login shell for its PATH once (AskLoginShell): each ExtendPath after it
// adds that PATH's folders, after Djinn's own, without asking the shell again.
func TestExtendPathAddsTheLoginShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Djinn asks no login shell on Windows")
	}
	bare(t)
	shell := filepath.Join(t.TempDir(), "zsh")
	if err := os.WriteFile(shell, []byte(`#!/bin/sh
PATH="/from/the/profile:$PATH"
export PATH
shift 3
cmd=$1
shift
exec /bin/sh -c "$cmd" "$@"
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", shell)
	t.Cleanup(func() {
		extending.Lock()
		loginPATH = ""
		extending.Unlock()
	})
	if err := AskLoginShell(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(shell); err != nil { // Asked once: the shell is not run again.
		t.Fatal(err)
	}
	ExtendPath()
	// The machine's own installers' folders (/usr/local/bin…) may follow.
	if got, want := os.Getenv("PATH"), "/usr/bin:/bin:/usr/sbin:/sbin:/from/the/profile"; got != want && !strings.HasPrefix(got, want+":") {
		t.Errorf("PATH = %q, want %q: Djinn's own folders, then the login shell's", got, want)
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
	// The machine's own absolute folders (/usr/local/bin…) may follow.
	got, want := AgentPath("darwin", "/x", home, "/shell"+sep+"/x"), strings.Join([]string{"/x", "/shell", local}, sep)
	if got != want && !strings.HasPrefix(got, want+sep) {
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

// TestLoginShell: the user's shell when it is a POSIX one that runs, zsh otherwise.
func TestLoginShell(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cases := map[string]string{
		"":                             "/bin/zsh",
		write("fish", 0o755):           "/bin/zsh",
		write("bash", 0o644):           "/bin/zsh",
		filepath.Join(dir, "ksh"):      "/bin/zsh", // Missing.
		filepath.Join(dir, "none/zsh"): "/bin/zsh",
	}
	if runtime.GOOS != "windows" { // No executable bit there.
		zsh := write("zsh", 0o755)
		cases[zsh] = zsh
	}
	for shell, want := range cases {
		if got := loginShell(shell); got != want {
			t.Errorf("loginShell(%q) = %q, want %q", shell, got, want)
		}
	}
}

// TestLoginPath: djinn takes the PATH an interactive login shell sets from its profile and its rc, through what the profile prints, and
// says why when the shell fails, prints no PATH or hangs.
func TestLoginPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a fake shell is a shell script")
	}
	dir := t.TempDir()
	shell := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	// A fake shell: it insists on -i and -l, reads a "profile" and an "rc" that print and set PATH, then runs the
	// command. Without -i, a real one skips the rc, where nvm adds to PATH.
	login := shell("zsh", `[ "$1" = -i ] && [ "$2" = -l ] && [ "$3" = -c ] || exit 3
echo "Last login: a profile that talks"
PATH="/from/the/login/profile:/opt/homebrew/bin:$PATH"
echo "an rc that talks too"
PATH="/from/the/rc/nvm:$PATH"
export PATH
shift 3
cmd=$1
shift
exec /bin/sh -c "$cmd" "$@"
`)
	path, err := loginPath(t.Context(), login)
	if err != nil {
		t.Fatal(err)
	}
	if want := "/from/the/rc/nvm:/from/the/login/profile:/opt/homebrew/bin:"; !strings.HasPrefix(path, want) || path == want {
		t.Errorf("PATH = %q, want the rc's and the profile's first", path)
	}

	if _, err := loginPath(t.Context(), shell("broken", "exit 1\n")); err == nil {
		t.Error("a failing shell gave a PATH")
	}
	if _, err := loginPath(t.Context(), shell("mute", "exit 0\n")); err == nil {
		t.Error("a shell that printed nothing gave a PATH")
	}
	// Its sleep keeps the output open once the shell is killed: the wait delay cuts it too.
	delay := loginWaitDelay
	loginWaitDelay = 50 * time.Millisecond
	t.Cleanup(func() { loginWaitDelay = delay })
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := loginPath(ctx, shell("hangs", "sleep 30\n")); err == nil {
		t.Error("a hanging shell gave a PATH")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("a hanging shell held djinn %v", d)
	}
}

func TestJoinPaths(t *testing.T) {
	sep := string(os.PathListSeparator)
	in := []string{"/a" + sep + "/b", "/b" + sep + "/c", sep + "/a" + sep + "/d"}
	if got, want := joinPaths(in...), strings.Join([]string{"/a", "/b", "/c", "/d"}, sep); got != want {
		t.Errorf("joinPaths = %q; want %q", got, want)
	}
}

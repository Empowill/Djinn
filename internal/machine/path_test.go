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

// TestExtendPath: Djinn's own PATH gets the folders where the agents are, as they are now, and the login shell's PATH
// once asked. Not parallel: it sets the process's PATH.
func TestExtendPath(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		// An app started from the Finder gets a bare PATH, without the folder where Claude Code's installer puts
		// claude: a worker (found with LookPath) and a lead (a shell line) did not find it. ExtendPath gives it back.
		{"finds claude", func(t *testing.T) {
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
		}},
		// On a new Mac, ~/.local/bin does not exist when Djinn starts: the panel's installer creates it. The next
		// ExtendPath, before a worker or a lead starts, finds claude there, without a restart.
		{"after an install", func(t *testing.T) {
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
		}},
		// Started by a launcher, djinn up asks the login shell for its PATH once (AskLoginShell): each ExtendPath after
		// it adds that PATH's folders, after Djinn's own, without asking the shell again.
		{"adds the login shell", func(t *testing.T) {
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
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.run(t)
		})
	}
}

// TestAgentPath: the PATH, the login shell's, then the agents' folders that exist, each once.
func TestAgentPath(t *testing.T) {
	t.Parallel()
	sep := string(os.PathListSeparator)
	cases := []struct {
		name string
		run  func(t *testing.T, home, local string)
	}{
		// Present already: not added twice, and its place is kept.
		{"present already", func(t *testing.T, home, local string) {
			if got := AgentPath("darwin", local+sep+"/x", home, ""); strings.Count(got, local) != 1 || !strings.HasPrefix(got, local+sep+"/x") {
				t.Errorf("%q: %s added twice or moved", got, local)
			}
		}},
		// A folder that does not exist is not added.
		{"missing folder", func(t *testing.T, home, local string) {
			if got := AgentPath("linux", "/x", home, ""); strings.Contains(got, ".bun") || !strings.Contains(got, local) {
				t.Errorf("%q: want %s and no missing folder", got, local)
			}
		}},
		// No home folder: only the absolute folders may be added.
		{"no home", func(t *testing.T, home, local string) {
			if got := AgentPath("linux", "/x", "", ""); strings.Contains(got, ".local") {
				t.Errorf("%q: a folder of no home", got)
			}
		}},
		// The login shell's PATH comes after Djinn's own, before the installers' folders. The machine's own absolute
		// folders (/usr/local/bin…) may follow.
		{"login shell", func(t *testing.T, home, local string) {
			got, want := AgentPath("darwin", "/x", home, "/shell"+sep+"/x"), strings.Join([]string{"/x", "/shell", local}, sep)
			if got != want && !strings.HasPrefix(got, want+sep) {
				t.Errorf("%q: want Djinn's PATH, the shell's, then %s", got, local)
			}
		}},
		// Volta's folder is one of them: the window's panel looked there.
		{"volta", func(t *testing.T, home, local string) {
			volta := filepath.Join(home, ".volta", "bin")
			if err := os.MkdirAll(volta, 0o755); err != nil {
				t.Fatal(err)
			}
			if got := AgentPath("darwin", "/x", home, ""); !strings.Contains(got, volta) {
				t.Errorf("%q: want %s", got, volta)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			local := filepath.Join(home, ".local", "bin")
			if err := os.MkdirAll(local, 0o755); err != nil {
				t.Fatal(err)
			}
			c.run(t, home, local)
		})
	}
}

// TestWithApps: a codex only an application ships is run from the application's folder, added to the PATH: the panel
// says it is there, and a worker that runs "codex" runs that one. One already on the PATH wins, and nothing is added.
func TestWithApps(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the fake agent is a shell script")
	}
	cases := []struct {
		name     string
		onPath   bool // a codex on the PATH already
		wantPath func(bin, app string) string
		want     func(bin, app string) string // what LookPath finds on the PATH
	}{
		{"the application's", false, func(bin, app string) string { return bin + string(os.PathListSeparator) + app },
			func(_, app string) string { return filepath.Join(app, "codex") }},
		{"on the PATH", true, func(bin, _ string) string { return bin },
			func(bin, _ string) string { return filepath.Join(bin, "codex") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			app := filepath.Join(t.TempDir(), "Codex.app", "Contents", "Resources")
			fakeAgent(t, app, "codex")
			apps := map[string][]string{"codex": {filepath.Join(t.TempDir(), "codex"), filepath.Join(app, "codex")}}
			bin := t.TempDir()
			if c.onPath {
				fakeAgent(t, bin, "codex")
			}
			path := withApps(bin, apps)
			if path != c.wantPath(bin, app) {
				t.Errorf("withApps = %q; want %q", path, c.wantPath(bin, app))
			}
			if got, err := LookPath("codex", path); err != nil || got != c.want(bin, app) {
				t.Errorf("LookPath(codex) on %q = %q, %v; want %q", path, got, err, c.want(bin, app))
			}
		})
	}
}

// TestLookPath: only an executable file in an absolute folder counts. Not parallel: it changes the working folder.
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
	cases := []struct {
		name string
		file string
		path string
		want string // "": not found
	}{
		{"not executable", "codex", dir, ""},
		{"a folder", "claude", dir, ""},
		{"absolute folder", "agy", "." + string(os.PathListSeparator) + dir, filepath.Join(dir, "agy")},
		{"relative folder", "agy", ".", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := LookPath(c.file, c.path)
			if (err == nil) != (c.want != "") || got != c.want {
				t.Errorf("LookPath(%s, %q) = %q, %v; want %q", c.file, c.path, got, err, c.want)
			}
		})
	}
}

// TestLoginShell: the user's shell when it is a POSIX one that runs, zsh otherwise.
func TestLoginShell(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(name string, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cases := []struct {
		name  string
		shell string
		want  string
	}{
		{"none", "", "/bin/zsh"},
		{"fish", write("fish", 0o755), "/bin/zsh"},
		{"bash not executable", write("bash", 0o644), "/bin/zsh"},
		{"missing", filepath.Join(dir, "ksh"), "/bin/zsh"},
		{"in a missing folder", filepath.Join(dir, "none/zsh"), "/bin/zsh"},
	}
	if runtime.GOOS != "windows" { // No executable bit there.
		zsh := write("zsh", 0o755)
		cases = append(cases, struct {
			name  string
			shell string
			want  string
		}{"zsh", zsh, zsh})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := loginShell(c.shell); got != c.want {
				t.Errorf("loginShell(%q) = %q, want %q", c.shell, got, c.want)
			}
		})
	}
}

// TestLoginPath: djinn takes the PATH an interactive login shell sets from its profile and its rc, through what the
// profile prints, and says why when the shell fails, prints no PATH or hangs. Not parallel: the hanging case shortens
// loginWaitDelay.
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
	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		// A fake shell: it insists on -i and -l, reads a "profile" and an "rc" that print and set PATH, then runs the
		// command. Without -i, a real one skips the rc, where nvm adds to PATH.
		{"profile and rc", func(t *testing.T) {
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
		}},
		{"fails", func(t *testing.T) {
			if _, err := loginPath(t.Context(), shell("broken", "exit 1\n")); err == nil {
				t.Error("a failing shell gave a PATH")
			}
		}},
		{"prints nothing", func(t *testing.T) {
			if _, err := loginPath(t.Context(), shell("mute", "exit 0\n")); err == nil {
				t.Error("a shell that printed nothing gave a PATH")
			}
		}},
		// Its sleep keeps the output open once the shell is killed: the wait delay cuts it too.
		{"hangs", func(t *testing.T) {
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
		}},
	}
	for _, c := range cases {
		t.Run(c.name, c.run)
	}
}

// TestJoinPaths: PATH lists joined, each folder once, in order.
func TestJoinPaths(t *testing.T) {
	t.Parallel()
	sep := string(os.PathListSeparator)
	in := []string{"/a" + sep + "/b", "/b" + sep + "/c", sep + "/a" + sep + "/d"}
	if got, want := joinPaths(in...), strings.Join([]string{"/a", "/b", "/c", "/d"}, sep); got != want {
		t.Errorf("joinPaths = %q; want %q", got, want)
	}
}

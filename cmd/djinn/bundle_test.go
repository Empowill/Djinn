package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestBundled: djinn knows it runs from an app bundle by its path alone.
func TestBundled(t *testing.T) {
	for exe, want := range map[string]bool{
		"/Applications/Djinn.app/Contents/MacOS/djinn":                               true,
		"/Users/me/Applications/Djinn.app/Contents/MacOS/djinn":                      true,
		"/Users/me/Downloads/Djinn 2.app/Contents/MacOS/djinn":                       true,
		"/private/var/folders/x/AppTranslocation/y/d/Djinn.APP/Contents/MacOS/djinn": true,
		"/usr/local/bin/djinn":                             false,
		"/Users/me/go/bin/djinn":                           false,
		"/Applications/Djinn.app/Contents/MacOS/djinn-app": false,
		"/Applications/Djinn.app/Contents/Resources/djinn": false,
		"/Applications/Djinn/Contents/MacOS/djinn":         false,
		"/Applications/Djinn.app/MacOS/djinn":              false,
		"djinn":                                            false,
	} {
		if got := bundled(filepath.FromSlash(exe)); got != want {
			t.Errorf("bundled(%q) = %v, want %v", exe, got, want)
		}
	}
}

// TestFromFinder: no argument, or the process serial number older macOS gives, is an app opened from Finder;
// anything else is a command.
func TestFromFinder(t *testing.T) {
	for _, args := range [][]string{nil, {}, {"-psn_0_12345"}} {
		if !fromFinder(args) {
			t.Errorf("fromFinder(%q) = false", args)
		}
	}
	for _, args := range [][]string{{"up"}, {"version"}, {"-psn_0_12345", "up"}, {"--help"}} {
		if fromFinder(args) {
			t.Errorf("fromFinder(%q) = true", args)
		}
	}
	// Anywhere but in a bundle on macOS, the arguments stay.
	if got := fromBundle(nil, os.Stderr); runtime.GOOS != "darwin" && got != nil {
		t.Errorf("fromBundle(nil) = %q on %s", got, runtime.GOOS)
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
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := loginPath(ctx, shell("hangs", "sleep 30\n")); err == nil {
		t.Error("a hanging shell gave a PATH")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("a hanging shell held djinn %v", d)
	}
}

package testx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPortable(t *testing.T) {
	for _, tc := range []struct {
		name, systemOnly string
		runs             bool
	}{
		{"everything", "", true},
		{"system only", "1", false},
	} {
		ran := false
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(SystemOnly, tc.systemOnly)
			Portable(t)
			ran = true
		})
		if ran != tc.runs {
			t.Errorf("%s=%q: the test ran %v, want %v", SystemOnly, tc.systemOnly, ran, tc.runs)
		}
	}
}

// TestGuard checks that Guard points Djinn at a folder of its own, and that its cleanup gives the variables back,
// unset ones unset, and removes the folder.
func TestGuard(t *testing.T) {
	for _, tc := range []struct {
		name string
		home string // DJINN_HOME before Guard; "" leaves it unset.
	}{
		{"set before", "/old/home"},
		{"unset before", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DJINN_HOME", tc.home)
			t.Setenv("DJINN_TEST", "")
			os.Unsetenv("DJINN_TEST")
			if tc.home == "" {
				os.Unsetenv("DJINN_HOME")
			}
			t.Setenv("GIT_AUTHOR_NAME", "Dev")
			cleanup := Guard()
			dir := os.Getenv("DJINN_HOME")
			if info, err := os.Stat(dir); err != nil || !info.IsDir() || dir == tc.home {
				t.Errorf("DJINN_HOME = %q (%v), want a new folder", dir, err)
			}
			if got, want := os.Getenv("DJINN_ADDR"), "unix://"+filepath.Join(dir, "djinn.sock"); got != want {
				t.Errorf("DJINN_ADDR = %q, want %q", got, want)
			}
			if got := os.Getenv("DJINN_TEST"); got != "1" {
				t.Errorf("DJINN_TEST = %q, want 1", got)
			}
			if v, ok := os.LookupEnv("GIT_AUTHOR_NAME"); ok {
				t.Errorf("GIT_AUTHOR_NAME = %q, want it hidden", v)
			}
			cleanup()
			if v, ok := os.LookupEnv("DJINN_HOME"); v != tc.home || ok != (tc.home != "") {
				t.Errorf("DJINN_HOME after cleanup = %q, %v; want %q", v, ok, tc.home)
			}
			if v, ok := os.LookupEnv("DJINN_TEST"); ok {
				t.Errorf("DJINN_TEST after cleanup = %q, want it unset", v)
			}
			if got := os.Getenv("GIT_AUTHOR_NAME"); got != "Dev" {
				t.Errorf("GIT_AUTHOR_NAME after cleanup = %q, want Dev", got)
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Errorf("%s after cleanup: %v, want it removed", dir, err)
			}
		})
	}
}

// TestHideGitIdentity reproduces the CI runner of PR #2, which has no git identity: a commit in a repository that sets
// none fails, even on a machine whose global config or environment names one, and passes once the repository does.
// The global config's other settings stay.
func TestHideGitIdentity(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	global := filepath.Join(t.TempDir(), "global")
	if err := os.WriteFile(global, []byte("[user]\n\tname = Dev\n\temail = dev@example.com\n[core]\n\tabbrev = 12\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, key := range gitIdentityEnv {
		t.Setenv(key, "Dev")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	if err := HideGitIdentity(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	for _, key := range gitIdentityEnv {
		if v, ok := os.LookupEnv(key); ok && key != "GIT_CONFIG_GLOBAL" {
			t.Errorf("%s = %q, want it unset", key, v)
		}
	}
	repo := t.TempDir()
	git := func(args ...string) (string, error) {
		out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	if out, err := git("init", "--quiet"); err != nil {
		t.Fatal(out)
	}
	if out, err := git("config", "core.abbrev"); err != nil || out != "12" {
		t.Errorf("core.abbrev = %q, %v; want 12, the global config's", out, err)
	}
	out, err := git("commit", "--quiet", "--allow-empty", "-m", "x")
	if err == nil || !strings.Contains(out, "empty ident name") {
		t.Errorf("commit without an identity: %v, %s; want it to fail with empty ident name, as on CI", err, out)
	}
	for _, args := range [][]string{{"config", "user.name", "Test"}, {"config", "user.email", "test@example.com"},
		{"commit", "--quiet", "--allow-empty", "-m", "x"}} {
		if out, err := git(args...); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
}

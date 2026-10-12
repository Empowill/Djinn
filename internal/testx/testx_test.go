package testx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPortable(t *testing.T) {
	ran := false
	t.Run("everything", func(t *testing.T) {
		t.Setenv(SystemOnly, "")
		Portable(t)
		ran = true
	})
	if !ran {
		t.Error("a portable test skipped without " + SystemOnly)
	}
	ran = false
	t.Run("system only", func(t *testing.T) {
		t.Setenv(SystemOnly, "1")
		Portable(t)
		ran = true
	})
	if ran {
		t.Error("a portable test ran with " + SystemOnly + "=1")
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

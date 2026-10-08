package machine

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// An app started from the Finder gets a bare PATH, without the folder where Claude Code's installer puts claude:
// a worker (found with LookPath) and a lead (a shell line) did not find it. ExtendPath gives it back.
func TestExtendPathFindsClaude(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("installers set the PATH of the user on Windows")
	}
	home := t.TempDir()
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	// A fake claude: it says it ran, and calls no model.
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\necho fake claude\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin") // what launchd gives an app
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

func TestAgentPath(t *testing.T) {
	home := t.TempDir()
	local := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	sep := string(os.PathListSeparator)
	if got := AgentPath("windows", "C:\\bin", home); got != "C:\\bin" {
		t.Errorf("Windows: %q, want the PATH as it is", got)
	}
	// Present already: not added twice, and its place is kept.
	if got := AgentPath("darwin", local+sep+"/x", home); strings.Count(got, local) != 1 || !strings.HasPrefix(got, local+sep+"/x") {
		t.Errorf("%q: %s added twice or moved", got, local)
	}
	// A folder that does not exist is not added.
	if got := AgentPath("linux", "/x", home); strings.Contains(got, ".bun") || !strings.Contains(got, local) {
		t.Errorf("%q: want %s and no missing folder", got, local)
	}
	// No home folder: only the absolute folders may be added.
	if got := AgentPath("linux", "/x", ""); strings.Contains(got, ".local") {
		t.Errorf("%q: a folder of no home", got)
	}
}

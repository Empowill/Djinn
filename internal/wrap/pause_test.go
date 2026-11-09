package wrap

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestPause: the brake freezes a task's running containers and thaws its paused ones, found by their labels; with no
// such container, docker pauses nothing.
func TestPause(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake docker is a shell script")
	}
	t.Parallel()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	// The fake docker lists c1 and c2 for the task's labels, none for another task's, and notes every call.
	docker := filepath.Join(dir, "docker")
	script := "#!/bin/sh\necho \"$*\" >> " + log + "\n" +
		"case \"$*\" in ps*label=djinn.task=t1*) echo c1; echo c2;; esac\n"
	if err := os.WriteFile(docker, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	labels := []string{TaskLabel + "=t1", HomeLabel + "=/home/x/.config/djinn"}
	if n := pauseWith(t.Context(), docker, true, labels...); n != 2 {
		t.Errorf("froze %d containers, want 2", n)
	}
	if n := pauseWith(t.Context(), docker, false, labels...); n != 2 {
		t.Errorf("thawed %d containers, want 2", n)
	}
	if n := pauseWith(t.Context(), docker, true, TaskLabel+"=t2"); n != 0 {
		t.Errorf("froze %d containers of a task without any, want 0", n)
	}
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"ps --quiet --filter status=running --filter label=djinn.task=t1 --filter label=djinn.home=/home/x/.config/djinn",
		"pause c1 c2",
		"ps --quiet --filter status=paused --filter label=djinn.task=t1 --filter label=djinn.home=/home/x/.config/djinn",
		"unpause c1 c2",
		"ps --quiet --filter status=running --filter label=djinn.task=t2",
	}
	if got := strings.Split(strings.TrimSpace(string(b)), "\n"); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("docker ran:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if n := pauseWith(t.Context(), filepath.Join(dir, "none"), true, labels...); n != 0 {
		t.Errorf("without docker, froze %d containers", n)
	}
}

package wrap

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// ran is what Main ran in its place.
type ran struct {
	path string
	args []string
	env  []string
}

// mainEnv makes a folder of real commands (docker, go) after the wrappers' folders on the path, and runs Main for head
// with args, the environment set by kv; it returns what ran (nil when nothing did), the exit code, what it wrote, and
// the commands it had admitted.
func mainEnv(t *testing.T, head string, args []string, kv ...string) (*ran, int, string, []string, string) {
	t.Helper()
	root, found := filepath.Join(t.TempDir(), "wrappers"), t.TempDir()
	if err := Write(root, "/opt/djinn/djinn"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"docker", "go"} {
		if err := os.WriteFile(filepath.Join(found, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", Path(root, found, true))
	t.Setenv("DJINN_TASK_ID", testTask)
	for _, k := range []string{EnvMemory, EnvPIDs, EnvHome, EnvAdmit, EnvAdmitted} {
		t.Setenv(k, "")
	}
	for i := 0; i+1 < len(kv); i += 2 {
		t.Setenv(kv[i], kv[i+1])
	}
	var r *ran
	var admitted []string
	var stderr bytes.Buffer
	code := Main(root, head, args, &stderr, func(command, run string, agent int) (string, error) {
		if !strings.HasPrefix(run, head+" ") {
			t.Errorf("found by %q", run)
		}
		if agent != os.Getppid() {
			t.Errorf("admitted under %d, want the caller %d", agent, os.Getppid())
		}
		admitted = append(admitted, command)
		return "djinn: this command waited 3s for its admission by the gates: the machine is under pressure", nil
	}, func(path string, args, env []string) error {
		r = &ran{path, args, env}
		return nil
	})
	return r, code, stderr.String(), admitted, found
}

// TestMain_Docker: Djinn's docker runs the real one in its place, its run bounded by the ceilings in the worker's
// environment; it asks no admission for a Claude worker, whose hook did; it refuses an update that lifts a ceiling.
func TestMain_Docker(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no wrapper on Windows")
	}
	r, _, stderr, admitted, found := mainEnv(t, "docker", []string{"run", "--memory", "8g", "alpine"},
		EnvMemory, "4294967296", EnvPIDs, "1024", EnvHome, testHome)
	if r == nil || r.path != filepath.Join(found, "docker") || r.args[0] != "docker" {
		t.Fatalf("ran %+v, want the real docker", r)
	}
	if want := slices.Concat([]string{"docker", "run"}, bounded("4294967296", "1024"), []string{"alpine"}); !slices.Equal(r.args, want) {
		t.Errorf("args = %q, want %q", r.args, want)
	}
	if !strings.Contains(stderr, "4 GiB") || !strings.Contains(stderr, Setting) {
		t.Errorf("stderr = %q, want the ceiling and its setting named", stderr)
	}
	if len(admitted) != 0 || slices.Contains(r.env, EnvAdmitted+"=1") {
		t.Errorf("admitted %q without %s", admitted, EnvAdmit)
	}

	r, code, stderr, _, _ := mainEnv(t, "docker", []string{"update", "--memory", "8g", "c"},
		EnvMemory, "4294967296", EnvPIDs, "1024")
	if r != nil || code != 1 || !strings.Contains(stderr, "refused") {
		t.Errorf("update: ran %+v, code %d, stderr %q; want it refused", r, code, stderr)
	}

	// Without ceilings in its environment (a worker of an older Djinn), docker runs as it is.
	r, _, _, _, _ = mainEnv(t, "docker", []string{"run", "alpine"})
	if r == nil || !slices.Equal(r.args, []string{"docker", "run", "alpine"}) {
		t.Errorf("no ceilings: ran %+v", r)
	}
}

// TestMain_Admit: for an agent without a hook, a heavy command waits for its admission, says why it waited, and
// runs marked admitted: what it runs, a go under go test, asks none again.
func TestMain_Admit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no wrapper on Windows")
	}
	r, _, stderr, admitted, found := mainEnv(t, "go", []string{"test", "./..."}, EnvAdmit, "1")
	if !slices.Equal(admitted, []string{"go test ./..."}) || r == nil || r.path != filepath.Join(found, "go") {
		t.Fatalf("admitted %q, ran %+v", admitted, r)
	}
	if !slices.Contains(r.env, EnvAdmitted+"=1") || !strings.Contains(stderr, "waited 3s") {
		t.Errorf("env %q, stderr %q: want it marked admitted and why it waited said", r.env, stderr)
	}
	// docker's cost is kept under the command as the worker wrote it, not under its task's label.
	_, _, _, admitted, _ = mainEnv(t, "docker", []string{"run", "alpine"}, EnvAdmit, "1", EnvMemory, "4294967296", EnvPIDs, "1024")
	if !slices.Equal(admitted, []string{"docker run alpine"}) {
		t.Errorf("docker admitted as %q", admitted)
	}
	_, _, _, admitted, _ = mainEnv(t, "go", []string{"build", "."}, EnvAdmit, "1", EnvAdmitted, "1")
	if len(admitted) != 0 {
		t.Errorf("a command under an admitted one asked again: %q", admitted)
	}
	_, _, _, admitted, _ = mainEnv(t, "go", []string{"build", "."})
	if len(admitted) != 0 {
		t.Errorf("admitted %q for a worker whose hook admits", admitted)
	}
}

// TestMain_NotFound: a wrapped command missing beside the wrappers fails as the shell would; a command Djinn does not
// wrap is refused.
func TestMain_NotFound(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no wrapper on Windows")
	}
	if r, code, stderr, _, _ := mainEnv(t, "npm", []string{"ci"}); r != nil || code != 127 || !strings.Contains(stderr, "not found") {
		t.Errorf("npm missing: ran %+v, code %d, %q", r, code, stderr)
	}
	if r, code, _, _, _ := mainEnv(t, "rm", []string{"-rf", "/"}); r != nil || code != 2 {
		t.Errorf("rm: ran %+v, code %d", r, code)
	}
}

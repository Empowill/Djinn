package machine

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeSystemdRun puts on PATH, alone, a systemd-run that runs script, and returns the file where it writes its
// arguments.
func fakeSystemdRun(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + args + "\n" + script + "\n"
	if err := os.WriteFile(filepath.Join(dir, "systemd-run"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return args
}

// TestProbeScopesFallback: without systemd-run, or with one that finds no user systemd, there are no scopes, and
// the error says why; with one that makes the scope, its cgroup gives the slice and the caps.
func TestProbeScopesFallback(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if s, _, err := ProbeScopes(t.Context(), 150, 0); s != nil || !errors.Is(err, ErrNoScope) ||
		!strings.Contains(err.Error(), "no systemd-run") {
		t.Errorf("without systemd-run: %+v, %v", s, err)
	}

	fakeSystemdRun(t, "echo 'Failed to connect to bus: No medium found' >&2; exit 1")
	if s, _, err := ProbeScopes(t.Context(), 0, 0); s != nil || !errors.Is(err, ErrNoScope) ||
		!strings.Contains(err.Error(), "Failed to connect to bus") {
		t.Errorf("without a user systemd: %+v, %v", s, err)
	}

	args := fakeSystemdRun(t, "echo /user.slice/user-1000.slice/user@1000.service/app.slice/djinn-probe-0a1b2c3d.scope; "+
		"echo none; echo 1073741824")
	s, notes, err := ProbeScopes(t.Context(), 150, 1<<30)
	if err != nil || s.CPU != 0 || s.Memory != 1<<30 || !s.MemoryController || len(notes) != 1 || !strings.Contains(notes[0], "cpu controller") {
		t.Fatalf("a user systemd without the cpu controller: %+v, %q, %v", s, notes, err)
	}
	b, err := os.ReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(b); !strings.HasPrefix(got, "--user\n--scope\n--quiet\n--collect\n--unit=djinn-probe-") ||
		!strings.Contains(got, "\n-p\nCPUQuota=150%\n-p\nMemoryMax=1073741824\n--\nsh\n-c\n") {
		t.Errorf("the probe ran systemd-run with %q", got)
	}
	if c := s.New("W3").Cgroup; !strings.HasPrefix(c, "/sys/fs/cgroup/user.slice/user-1000.slice/user@1000.service/app.slice/djinn-W3-") {
		t.Errorf("a worker's cgroup: %q", c)
	}

	fakeSystemdRun(t, "echo /user.slice/user-1000.slice/user@1000.service/app.slice/djinn-probe-0a1b2c3d.scope; "+
		"echo 150000 100000; echo none")
	s2, notes2, err := ProbeScopes(t.Context(), 150, 0)
	if err != nil || s2.CPU != 150 || s2.MemoryController || len(notes2) != 0 {
		t.Fatalf("a user systemd without memory controller: %+v, %q, %v", s2, notes2, err)
	}
}

package logx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestArgv: a command's message, title and body, and a URL's user, stay out of the log; a long argument and a long
// list are cut.
func TestArgv(t *testing.T) {
	t.Parallel()
	many := make([]string, argsMax+3)
	for i := range many {
		many[i] = "a"
	}
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"hides messages and users", []string{"/usr/bin/git", "-C", "/r", "commit", "-m", "secret words",
			"--body=more words", "https://x-access-token:ghs_abc@github.com/o/r.git", strings.Repeat("y", 100)},
			"git -C /r commit -m … --body=… https://%E2%80%A6@github.com/o/r.git " + strings.Repeat("y", argMax) + "…"},
		{"many arguments", many, strings.TrimSpace(strings.Repeat("a ", argsMax)) + " …+3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Argv(tc.args)
			if got != tc.want {
				t.Errorf("Argv = %q, want %q", got, tc.want)
			}
			if strings.Contains(got, "ghs_abc") || strings.Contains(got, "secret") {
				t.Errorf("Argv leaks: %q", got)
			}
		})
	}
}

// TestRan: a command logs its argv, folder, exit code: at debug when it succeeds at once, at info when it exits
// non-zero, at warn when slow or killed at its deadline, at error when it cannot run (Q243).
func TestRan(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	expired, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-expired.Done()
	for _, tc := range []struct {
		name  string
		ctx   context.Context
		cmd   func(ctx context.Context) *exec.Cmd
		start time.Time
		want  []string
	}{
		{"fast success", context.Background(), func(ctx context.Context) *exec.Cmd {
			cmd := exec.CommandContext(ctx, exe, "-test.run=^$")
			cmd.Dir = t.TempDir()
			return cmd
		}, time.Now(), []string{"level=DEBUG msg=command argv=\"" + filepath.Base(exe) + " -test.run=^$\"", "exit=0"}},
		{"slow success", context.Background(), func(ctx context.Context) *exec.Cmd {
			return exec.CommandContext(ctx, exe, "-test.run=^$")
		}, time.Now().Add(-2 * Slow), []string{"level=WARN msg=command", "took=2"}},
		{"failure", context.Background(), func(ctx context.Context) *exec.Cmd {
			return exec.CommandContext(ctx, exe, "-test.run=^$", "-test.count=x")
		}, time.Now(), []string{"level=INFO msg=command", "exit=2"}},
		{"not run", context.Background(), func(ctx context.Context) *exec.Cmd {
			return exec.CommandContext(ctx, filepath.Join(t.TempDir(), "missing"))
		}, time.Now(), []string{"level=ERROR msg=command", "exit=-1", "err="}},
		{"deadline", expired, func(ctx context.Context) *exec.Cmd {
			return exec.CommandContext(ctx, exe, "-test.run=^$")
		}, time.Now(), []string{"level=WARN msg=command", "timeout=true"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := capture(t)
			cmd := tc.cmd(tc.ctx)
			Ran(tc.ctx, cmd, tc.start, cmd.Run())
			lines := strings.Split(strings.TrimSpace(b.String()), "\n")
			s := lines[len(lines)-1]
			for _, want := range tc.want {
				if !strings.Contains(s, want) {
					t.Errorf("the line lacks %q: %s", want, s)
				}
			}
		})
	}
}

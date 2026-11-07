package machine

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestMatch: a command is found by the letters and digits of its first line, whatever the quoting.
func TestMatch(t *testing.T) {
	t.Parallel()
	for command, want := range map[string]string{
		"go test ./... -run 'TestX'":    "gotestrunTestX",
		"npm ci\nnpm test":              "npmci",
		`eval 'go vet '"'"'./...'"'"''`: "evalgovet",
	} {
		if got := Match(command); got != want {
			t.Errorf("Match(%q) = %q, want %q", command, got, want)
		}
	}
}

// TestFindCommand: among the processes a root started, the shell whose command line holds the command, not the
// processes it started, nor one left out, nor one outside the root.
func TestFindCommand(t *testing.T) {
	t.Parallel()
	procs := []proc{
		{pid: 10, ppid: 1}, // The agent.
		{pid: 11, ppid: 10, start: 5},
		{pid: 12, ppid: 11},
		{pid: 13, ppid: 10, start: 9},
		{pid: 20, ppid: 1},
	}
	lines := map[int]string{
		10: "node claude -p",
		// Claude's Bash tool: the command comes after a long preamble.
		11: `/usr/bin/zsh -c source /home/u/.claude/shell-snapshots/snapshot-zsh-1794064714933-5vq842.sh 2>/dev/null || true && ` +
			`setopt NO_EXTENDED_GLOB NO_BARE_GLOB_QUAL 2>/dev/null || true && { \builtin unalias -- 'which'; \builtin unset -f -- ` +
			`'which'; } >/dev/null 2>&1 || true && eval 'go test ./...' < /dev/null && pwd -P >| /tmp/claude-7ce2-cwd`,
		12: "go test ./...",
		13: `zsh -c eval 'go test ./...'`,
		20: "zsh -c go test ./...",
	}
	match := Match("go test ./...")
	if got := findCommand(procs, lines, 10, match, nil); got != 13 {
		t.Errorf("found %d, want the shell started last, 13", got)
	}
	if got := findCommand(procs, lines, 10, match, func(pid int) bool { return pid == 13 }); got != 11 {
		t.Errorf("found %d, want 11 once 13 is left out", got)
	}
	if got := findCommand(procs, lines, 10, Match("npm ci"), nil); got != 0 {
		t.Errorf("found %d for a command none runs", got)
	}
	if got := findCommand(procs, lines, 30, match, nil); got != 0 {
		t.Errorf("found %d under a root that started none", got)
	}

	// For real: a shell this test starts.
	if runtime.GOOS != "linux" {
		return
	}
	shell := exec.Command("sh", "-c", "true "+strings.Repeat("preamble ", 40)+"&& eval 'sleep 30; echo find-command-test'")
	if err := shell.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = shell.Process.Kill(); _ = shell.Wait() }()
	deadline := time.Now().Add(5 * time.Second)
	got := 0
	for got == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		got = FindCommand(os.Getpid(), Match("sleep 30; echo find-command-test"), nil)
	}
	if got != shell.Process.Pid {
		t.Errorf("FindCommand = %d, want the shell %d", got, shell.Process.Pid)
	}
}

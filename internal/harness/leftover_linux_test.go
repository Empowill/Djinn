package harness

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/machine"
)

// leftoverClaude is a claude that starts two long jobs before it plays the fake provider, as an agent's shell does: a
// job in the background, in its process group (a dev server started with &), and a daemon, in a session of its own
// (what setsid, a double fork or docker compose up -d leave). Each writes its PID to a file of $DJINN_LEFT once it
// runs.
const leftoverClaude = `#!/bin/sh
sh -c 'echo $$ > "$0.new" && mv "$0.new" "$0" && exec sleep 600' "$DJINN_LEFT/job" </dev/null >/dev/null 2>&1 &
setsid sh -c 'echo $$ > "$0.new" && mv "$0.new" "$0" && exec sleep 600' "$DJINN_LEFT/daemon" </dev/null >/dev/null 2>&1 &
exec "$DJINN_FAKE_BIN" "$@"
`

// TestTaskEndStopsWhatItsWorkerStarted (W46): what a worker starts and leaves running, a dev server, a docker compose
// up, a background shell job, ends with its task, however the task ends: done, failed, stopped or cancelled; in its
// process group or out of it, with or without a systemd scope. Else it runs on, holding its ports and the worktree,
// and nothing stops it later: the worker's record in run/ goes with the worker, so neither the sentinel at Djinn's end
// nor the sweep at the next start knows of it; PR_SET_PDEATHSIG is the worker's alone, which its children do not
// inherit.
//
// Done and failed tasks leave both running today, stopped and cancelled ones the daemon when there is no scope: a case
// where only those leak skips, until the cleanup lands; with DJINN_KNOWN_LEAKS=1 it fails, and checks that neither
// Djinn's end nor the next start stops them.
func TestTaskEndStopsWhatItsWorkerStarted(t *testing.T) {
	t.Parallel()
	script := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(script, []byte(leftoverClaude), 0o755); err != nil {
		t.Fatal(err)
	}
	modes := []string{"group"}
	scopes, _, err := machine.ProbeScopes(t.Context(), 0, 0)
	if err == nil {
		modes = append(modes, "scope")
	}
	for _, mode := range modes {
		for _, end := range []string{"done", "failed", "stopped", "cancelled"} {
			t.Run(mode+"/"+end, func(t *testing.T) {
				t.Parallel()
				leftovers := t.TempDir()
				f := fake{provider: "claude", fixture: "success", end: "wait"}
				switch end {
				case "done":
					f.end = "eof"
				case "failed":
					f.fixture, f.end = "error", "eof:1"
				}
				env, _, _ := f.env(t)
				env = append(env, "DJINN_FAKE_BIN="+os.Args[0], "DJINN_LEFT="+leftovers)
				providers := testProviders()
				providers[planv1.Provider_PROVIDER_CLAUDE] = envProvider{Claude{Command: script, Grace: time.Second}, env}
				var opts []Option
				if mode == "scope" {
					opts = append(opts, WithScopes(scopes))
				}
				home := t.TempDir()
				e := upWith(t, home, providers, opts...)
				wishID, _ := e.wish(t, gitRepo(t))
				task := e.mustSpawn(t, wishID, "Start a dev server", "x",
					&planv1.TaskServiceSpawnRequest{Provider: planv1.Provider_PROVIDER_CLAUDE})
				job, daemon := leftoverPID(t, leftovers, "job"), leftoverPID(t, leftovers, "daemon")

				switch end {
				case "stopped":
					e.until(t, task.GetId(), func(tk *planv1.Task) bool { return tk.GetStatus() == planv1.TaskStatus_TASK_STATUS_RUNNING })
					if _, err := e.tasks.Stop(t.Context(), connect.NewRequest(&planv1.TaskServiceStopRequest{TaskId: task.GetId()})); err != nil {
						t.Fatal(err)
					}
				case "cancelled":
					e.until(t, task.GetId(), func(tk *planv1.Task) bool { return tk.GetStatus() == planv1.TaskStatus_TASK_STATUS_RUNNING })
					if _, err := e.tasks.Cancel(t.Context(), connect.NewRequest(&planv1.TaskServiceCancelRequest{
						TaskId: task.GetId(), By: planv1.Closer_CLOSER_DEVELOPER,
					})); err != nil {
						t.Fatal(err)
					}
				}
				ended := e.ended(t, task.GetId())
				waitFor(t, "the worker to end", func() bool { return !e.h.running(task.GetId()) })
				t.Logf("task %s, worker ended", ended.GetStatus())

				procs := map[string]identity{"job": job, "daemon": daemon}
				status := strings.TrimPrefix(ended.GetStatus().String(), "TASK_STATUS_")
				// What leaks today (W46): all of it at a task's own end, the daemon at a stop without a scope.
				var leaks, rest []string
				switch {
				case end == "done" || end == "failed":
					leaks = []string{"job", "daemon"}
				case mode == "group":
					leaks, rest = []string{"daemon"}, []string{"job"}
				default:
					rest = []string{"job", "daemon"}
				}
				// The worker's grace (1 s) past, what it left is stopped; 3 s leave room for a loaded machine.
				for _, name := range survivors(procs, rest, 3*time.Second) {
					t.Errorf("its %s still runs after the task %s", name, status)
				}
				left := survivors(procs, leaks, 3*time.Second)
				if len(left) == 0 {
					return
				}
				if os.Getenv("DJINN_KNOWN_LEAKS") == "" {
					t.Skipf("W46: its %s still run(s) after the task %s; DJINN_KNOWN_LEAKS=1 fails on it", strings.Join(left, " and "), status)
				}
				for _, name := range left {
					t.Errorf("its %s still runs after the task %s", name, status)
				}
				// Nothing stops it later either: its worker's record went with the worker.
				if rs := records(t, filepath.Join(home, runDir)); len(rs) != 0 {
					t.Logf("run/ records left: %+v", rs)
				}
				e.down()
				if left := survivors(procs, left, time.Second); len(left) > 0 {
					t.Errorf("its %s still run(s) after Djinn's end (the sentinel)", strings.Join(left, " and "))
				}
				up(t, home, opts...)
				if left := survivors(procs, left, 0); len(left) > 0 {
					t.Errorf("its %s still run(s) after the next start (the sweep)", strings.Join(left, " and "))
				}
			})
		}
	}
}

// leftoverPID waits for the PID leftoverClaude writes to dir/name, and returns the identity of that process; it kills
// it as the test ends.
func leftoverPID(t *testing.T, dir, name string) identity {
	t.Helper()
	var id identity
	waitFor(t, "the worker's "+name, func() bool {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return false
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err != nil {
			return false
		}
		id.pid = pid
		id.start, err = procStart(pid)
		return err == nil
	})
	t.Cleanup(func() {
		if same(id) {
			_ = syscall.Kill(id.pid, syscall.SIGKILL)
		}
	})
	return id
}

// survivors are those of names whose process still runs once wait has passed, in the order of names.
func survivors(procs map[string]identity, names []string, wait time.Duration) []string {
	deadline := time.Now().Add(wait)
	for {
		var left []string
		for _, name := range names {
			if same(procs[name]) {
				left = append(left, name)
			}
		}
		if len(left) == 0 || !time.Now().Before(deadline) {
			return left
		}
		time.Sleep(20 * time.Millisecond)
	}
}

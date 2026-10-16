package harness

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
// (what setsid or a double fork leave). Each writes its PID to a file of $DJINN_LEFT once it runs.
const leftoverClaude = `#!/bin/sh
sh -c 'echo $$ > "$0.new" && mv "$0.new" "$0" && exec sleep 600' "$DJINN_LEFT/job" </dev/null >/dev/null 2>&1 &
setsid sh -c 'echo $$ > "$0.new" && mv "$0.new" "$0" && exec sleep 600' "$DJINN_LEFT/daemon" </dev/null >/dev/null 2>&1 &
exec "$DJINN_FAKE_BIN" "$@"
`

// TestTaskEndStopsWhatItsWorkerStarted (W46, W68): what a worker starts and leaves running, a dev server, a daemon,
// a background shell job, ends with its task, however the task ends: done, failed, stopped or cancelled;
// in its process group or out of it, with or without a systemd scope. Else it runs on, holding its ports and the
// worktree. W46 found both left running at a task's own end, and the daemon at a stop without a scope; since W68 the
// worker's end stops its group, its cgroup and what carries its mark (stopLeft).
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
				// The worker's grace (1 s) past, what it left is stopped; 3 s leave room for a loaded machine.
				for _, name := range survivors(procs, []string{"job", "daemon"}, 3*time.Second) {
					t.Errorf("its %s still runs after the task %s", name, status)
				}
				// Its record went once what it left was stopped.
				if rs := records(t, filepath.Join(home, runDir)); len(rs) != 0 {
					t.Errorf("run/ records left: %+v", rs)
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

// startMarked starts a daemon, in a session of its own, whose environment holds the mark: what a worker left running.
// It kills it as the test ends.
func startMarked(t *testing.T, mark string) identity {
	t.Helper()
	cmd := exec.Command("sleep", "600")
	cmd.Env = append(os.Environ(), markEntry(mark))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	ended := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(ended)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-ended
	})
	var id identity
	waitFor(t, "the daemon's start time", func() bool {
		var err error
		id = identity{pid: cmd.Process.Pid}
		id.start, err = procStart(id.pid)
		return err == nil
	})
	return id
}

// TestSweepStopsWhatCarriesItsMark (W68): a djinn up that died as its worker's end stopped what the worker left (the
// grace delay) leaves a record of a worker that ended, and a daemon with its mark: the next start stops the daemon,
// and leaves alone a process with another mark.
func TestSweepStopsWhatCarriesItsMark(t *testing.T) {
	t.Parallel()
	daemon, other := startMarked(t, "MINE"), startMarked(t, "ANOTHER")
	crashed := endedPID(t)
	dir := t.TempDir()
	cs := &children{dir: dir, grace: time.Second, self: identity{pid: crashed.pid + 1, start: "another"}}
	gone := endedPID(t)
	b, _ := json.Marshal(child{PID: gone.pid, Start: "an ended worker", Mark: "MINE", Djinn: crashed.pid,
		DjinnStart: crashed.start, Task: "W1", Kind: "worker"})
	if err := os.WriteFile(filepath.Join(dir, strconv.Itoa(gone.pid)+".json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := cs.sweep(); !slices.Equal(got, []string{"W1's worker"}) {
		t.Errorf("sweep() = %v, want W1's worker", got)
	}
	if same(daemon) {
		t.Error("the daemon with the worker's mark still runs")
	}
	if !same(other) {
		t.Error("the process with another mark was stopped")
	}
	if rs := records(t, dir); len(rs) != 0 {
		t.Errorf("records left: %+v", rs)
	}
}

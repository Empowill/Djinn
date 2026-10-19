package harness

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// withCrashedChildren records the children in dir as a djinn up of before W514 did, owner being that djinn up: they
// do not die with it (no guard), and owner, ended, plays a crashed one.
func withCrashedChildren(dir string, owner identity) Option {
	return func(h *Harness) {
		h.children = &children{dir: dir, grace: Grace, self: owner, noGuard: true}
	}
}

// endedPID is the identity of a process that ran and ended: a crashed djinn up.
func endedPID(t *testing.T) identity {
	t.Helper()
	cmd := exec.Command(os.Args[0], fakeWatchArg)
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return identity{pid: cmd.Process.Pid, start: "ended"}
}

// startOther starts a process that waits, as Djinn would not: the test's own, in a group of its own on Unix.
func startOther(t *testing.T) (*exec.Cmd, identity) {
	t.Helper()
	cmd := exec.Command(os.Args[0], fakeWatchArg, "wait")
	ownGroup(cmd, false)
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
	var start string
	waitFor(t, "the process's start time", func() bool {
		var err error
		start, err = procStart(cmd.Process.Pid)
		return err == nil
	})
	return cmd, identity{pid: cmd.Process.Pid, start: start}
}

func records(t *testing.T, dir string) []child {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	var cs []child
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var c child
		if json.Unmarshal(b, &c) == nil {
			cs = append(cs, c)
		}
	}
	return cs
}

// TestOrphansStoppedAtStart (Q230, 12/10/2026): forced quits of djinn up left two watch.sh of W488 running beside the
// live watcher, polling GitHub in double, and earlier resumed workers beside the old claude of their task. A worker,
// a watcher and a worker's command under a gate, left running by a crashed djinn up, are stopped at the next start,
// before any task resumes, and the restart event names them.
func TestOrphansStoppedAtStart(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	run := filepath.Join(home, runDir)
	// A temporary file of WriteAtomicNoSync, not yet renamed to <pid>.json, is no record.
	writeFile(t, run, ".99.json.djinn-1.tmp", `{"pid":99,"start":"1","djinn":1,"djinn_start":"ended","task":"W9","kind":"watcher"}`)
	env, _, _ := fake{provider: "claude", fixture: "success", end: "wait"}.env(t)
	providers := testProviders()
	providers[planv1.Provider_PROVIDER_CLAUDE] = envProvider{Claude{Command: os.Args[0]}, env}
	crashed := upWith(t, t.TempDir(), providers, withCrashedChildren(run, endedPID(t)))
	wishID, _ := crashed.wish(t, nativeFolder(t))
	res, err := crashed.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Work", Prompt: "x", Provider: planv1.Provider_PROVIDER_CLAUDE,
	}))
	if err != nil {
		t.Fatal(err)
	}
	worker := res.Msg.GetTask()
	if _, err := crashed.spawnWatch(t, wishID, watchCommand("wait"), true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the worker and the watcher recorded", func() bool { return len(records(t, run)) == 2 })
	// The worker runs a command under a gate, in a group of its own (its shell's).
	gated, _ := startOther(t)
	crashed.h.TrackGate(gated.Process.Pid, worker.GetId())
	left := records(t, run)
	if len(left) != 3 {
		t.Fatalf("records %+v, want the worker, the watcher and the gate's command", left)
	}

	e := up(t, home)
	want := "3 orphans stopped: W1's gate, W1's worker, W2's watcher."
	if restarts := restartEvents(t, e); len(restarts) != 1 || !strings.HasSuffix(restarts[0], want) {
		t.Errorf("restart events %q, want one ending with %q", restarts, want)
	}
	for _, c := range left {
		if same(identity{c.PID, c.Start}) {
			t.Errorf("%s (pid %d) still runs", c.label(), c.PID)
		}
	}
	crashed.down()
}

// TestSweepLeavesOthersAlone: a recorded PID that another process took since is no child of Djinn's, and the
// children of a djinn up that still runs are its own: the sweep touches neither, and says nothing of them.
func TestSweepLeavesOthersAlone(t *testing.T) {
	t.Parallel()
	_, other := startOther(t)
	crashed := endedPID(t)
	self, err := procStart(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name   string
		record child
		kept   bool
	}{
		// The PID of a worker of a crashed djinn up, now another process's.
		{"reused PID", child{PID: other.pid, Start: "an earlier process", Djinn: crashed.pid, DjinnStart: crashed.start,
			Task: "W1", Kind: "worker"}, false},
		// A worker of the djinn up that runs: the test process.
		{"live djinn up", child{PID: other.pid, Start: other.start, Djinn: os.Getpid(), DjinnStart: self,
			Task: "W1", Kind: "worker"}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			cs := &children{dir: dir, grace: time.Second, self: identity{pid: crashed.pid + 1, start: "another"}}
			b, _ := json.Marshal(c.record)
			if err := os.WriteFile(filepath.Join(dir, strconv.Itoa(other.pid)+".json"), b, 0o600); err != nil {
				t.Fatal(err)
			}
			if got := cs.sweep(); len(got) != 0 {
				t.Errorf("sweep() = %v, want nothing stopped", got)
			}
			if !same(other) {
				t.Fatal("the other process was stopped")
			}
			if kept := len(records(t, dir)) == 1; kept != c.kept {
				t.Errorf("record kept = %v, want %v", kept, c.kept)
			}
		})
	}
}

// fakeParentArg makes the test binary play a djinn up that starts a worker, prints its PID and on Unix the PID of
// what the worker started, on Linux that of the daemon it started too (setsid), then waits to be killed. With "unguarded" after it, it starts no guard (no sentinel nor
// job): only PR_SET_PDEATHSIG remains.
const fakeParentArg = "djinn-fake-parent"

func fakeParent(args []string) int {
	cs := newChildren(os.Getenv("DJINN_FAKE_RUN"), time.Second)
	cs.noGuard = slices.Contains(args, "unguarded")
	name, cargs := os.Args[0], []string{fakeWatchArg, "wait"}
	switch runtime.GOOS {
	case "windows":
	case "linux":
		name, cargs = "sh", []string{"-c", "sleep 600 & echo $!; setsid sleep 600 </dev/null >/dev/null 2>&1 & echo $!; wait"}
	default:
		name, cargs = "sh", []string{"-c", "sleep 600 & echo $!; wait"}
	}
	p, err := startProcess(os.TempDir(), name, cargs, nil, nil, time.Second, false, cs.tracker("W1", "worker"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println(p.cmd.Process.Pid)
	for range startedBy() {
		fmt.Println((<-p.lines).text)
	}
	time.Sleep(time.Hour)
	return 0
}

// startedBy is how many processes fakeParent's worker starts: a job on Unix, a daemon too on Linux.
func startedBy() int {
	switch runtime.GOOS {
	case "windows":
		return 0
	case "linux":
		return 2
	}
	return 1
}

// TestChildrenDieWithDjinn: a djinn up killed, its worker dies, and what the worker started too. On Linux the kernel
// kills the worker (PR_SET_PDEATHSIG), even without the sentinel; the sentinel stops the rest of its group, on macOS
// all of it, on Linux its daemon too, by its mark; on Windows, the job object kills the tree.
func TestChildrenDieWithDjinn(t *testing.T) {
	t.Parallel()
	modes := []string{"guarded"}
	if runtime.GOOS == "linux" {
		modes = append(modes, "unguarded")
	}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			parent := exec.Command(os.Args[0], fakeParentArg, mode)
			parent.Env = append(os.Environ(), "DJINN_FAKE_RUN="+t.TempDir())
			out, err := parent.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := parent.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = parent.Process.Kill() })
			lines := bufio.NewScanner(out)
			var ids []identity
			for range 1 + startedBy() {
				if !lines.Scan() {
					t.Fatalf("the parent said no PID: %v", lines.Err())
				}
				pid, err := strconv.Atoi(strings.TrimSpace(lines.Text()))
				if err != nil {
					t.Fatal(err)
				}
				start, err := procStart(pid)
				if err != nil {
					t.Fatal(err)
				}
				ids = append(ids, identity{pid, start})
			}
			if err := parent.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			_ = parent.Wait()
			waitFor(t, "the worker to die with its parent", func() bool { return !same(ids[0]) })
			if len(ids) < 2 {
				return
			}
			if mode == "unguarded" {
				// Without the sentinel, what the worker started outlives it: this test's to stop.
				for _, id := range ids[1:] {
					if p, err := os.FindProcess(id.pid); err == nil && same(id) {
						_ = p.Kill()
					}
				}
				return
			}
			waitFor(t, "what the worker started to die with it", func() bool {
				return !slices.ContainsFunc(ids[1:], same)
			})
		})
	}
}

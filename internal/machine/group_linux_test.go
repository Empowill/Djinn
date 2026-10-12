package machine

import (
	"os/exec"
	"runtime"
	"syscall"
	"testing"
	"time"
)

// TestReadWorker reads a real process group: a shell that leads its group and a child it waits for.
func TestReadWorker(t *testing.T) {
	cmd := exec.Command("sh", "-c", "sleep 30 & wait")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
	})
	var g Group
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		listing.mu.Lock()
		listing.at = time.Time{} // Listed again.
		listing.mu.Unlock()
		var err error
		if g, err = ReadWorker(cmd.Process.Pid, ""); err != nil {
			t.Fatal(err)
		}
		if g.Processes == 2 {
			break
		}
	}
	if g.Processes != 2 || g.Memory == 0 {
		t.Errorf("sh and sleep: %+v", g)
	}
}

// TestReadWorkerAtExec: a worker listed at its exec, before it touched a page, has no resident memory yet. PR #3's CI
// kept that reading, the worker at 0 bytes (TestMeasureWorker): it is no reading, as a worker not listed yet.
func TestReadWorkerAtExec(t *testing.T) {
	runtime.LockOSThread() // ptrace: the thread that started the process waits for it.
	defer runtime.UnlockOSThread()
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Ptrace: true} // Stopped at its exec.
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	listing.mu.Lock()
	listing.at = time.Time{} // Listed again.
	listing.mu.Unlock()
	procs, err := listProcs()
	if err != nil {
		t.Fatal(err)
	}
	if g := sumGroup(procs, cmd.Process.Pid); g.Processes != 1 || g.Memory != 0 {
		t.Fatalf("listed at its exec: %+v, want one process and no memory yet", g)
	}
	if g, err := ReadWorker(cmd.Process.Pid, ""); err != nil || g != (Group{}) {
		t.Errorf("ReadWorker at its exec = %+v, %v: want no reading", g, err)
	}
}

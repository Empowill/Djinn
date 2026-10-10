package machine

import (
	"os/exec"
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

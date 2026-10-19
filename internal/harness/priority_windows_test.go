//go:build windows

package harness

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

// processPriorityClass returns the priority class of pid on Windows.
func processPriorityClass(pid int) (uint32, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(handle)
	return windows.GetPriorityClass(handle)
}

// wantClass is the priority class a worker must run at: the test process's own at standard priority, as Djinn's own
// is a worker's; BELOW_NORMAL_PRIORITY_CLASS at low priority, or the test process's own when it runs lower already
// (IDLE_PRIORITY_CLASS). A CI runner does not run at NORMAL_PRIORITY_CLASS: Windows's ran the tests at BELOW_NORMAL.
func wantClass(t *testing.T, low bool) uint32 {
	t.Helper()
	own, err := windows.GetPriorityClass(windows.CurrentProcess())
	if err != nil {
		t.Fatalf("read the test process's priority class: %v", err)
	}
	if low && own != windows.IDLE_PRIORITY_CLASS {
		return windows.BELOW_NORMAL_PRIORITY_CLASS
	}
	return own
}

func checkWorkerPriority(t *testing.T, what string, pid int, low bool) {
	t.Helper()
	want := wantClass(t, low)
	cls, err := processPriorityClass(pid)
	if err != nil {
		t.Fatalf("read priority class for %s (PID %d): %v", what, pid, err)
	}
	if cls != want {
		t.Errorf("%s (PID %d) priority class = 0x%08x, want 0x%08x", what, pid, cls, want)
	}
}

// TestWorkerPriorityUnderBelowNormal runs the priority tests again from a test process started at
// BELOW_NORMAL_PRIORITY_CLASS: the Windows CI runner ran the tests there, and a worker at standard priority runs at
// Djinn's own class (PR #2's CI). The Unix twin starts it under nice -n 5.
func TestWorkerPriorityUnderBelowNormal(t *testing.T) {
	t.Parallel()
	tests := []string{"TestWorkerPriority", "TestWorkerPriorityPolicyChange"}
	cmd := exec.CommandContext(t.Context(), os.Args[0],
		"-test.run", "^("+strings.Join(tests, "|")+")$", "-test.count=1", "-test.v")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.BELOW_NORMAL_PRIORITY_CLASS}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the priority tests fail at BELOW_NORMAL_PRIORITY_CLASS: %v\n%s", err, out)
	}
	for _, name := range []string{"TestWorkerPriority/normal_priority", "TestWorkerPriorityPolicyChange"} {
		if !strings.Contains(string(out), "--- PASS: "+name+" ") {
			t.Errorf("%s did not pass at BELOW_NORMAL_PRIORITY_CLASS:\n%s", name, out)
		}
	}
}

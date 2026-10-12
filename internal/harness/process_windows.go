//go:build windows

package harness

import (
	"errors"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// ownGroup starts the process in a group of its own, so that a Ctrl+C in Djinn's console does not reach it, and in
// its worker priority class (workerClass).
func ownGroup(cmd *exec.Cmd, lowPriority bool) {
	flags := uint32(syscall.CREATE_NEW_PROCESS_GROUP) | workerClass(lowPriority)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags}
}

// workerClass is the priority class of a worker. At standard priority, Djinn's own: a worker runs as Djinn was
// started, as on Unix (Windows alone would give a child NORMAL_PRIORITY_CLASS under an ABOVE_NORMAL Djinn, and
// BELOW_NORMAL_PRIORITY_CLASS under a BELOW_NORMAL one). At low priority, BELOW_NORMAL_PRIORITY_CLASS, below the
// developer's applications, or Djinn's own when Djinn already runs lower (IDLE_PRIORITY_CLASS).
func workerClass(low bool) uint32 {
	own, err := windows.GetPriorityClass(windows.CurrentProcess())
	if err != nil {
		own = windows.NORMAL_PRIORITY_CLASS
	}
	if low && own != windows.IDLE_PRIORITY_CLASS {
		return windows.BELOW_NORMAL_PRIORITY_CLASS
	}
	return own
}

// lowCommand runs path as it is: on Windows, a worker starts in its class (ownGroup).
func lowCommand(path string, args []string) (string, []string, bool) { return path, args, true }

// applyPriority is a no-op on Windows: the priority class is set at process creation via CreationFlags.
func applyPriority(cmd *exec.Cmd, lowPriority bool) {}

// setPriority sets CPU priority class on the running process.
func setPriority(p *process, low bool) error {
	if p.cmd.Process == nil {
		return errors.New("the process has not started")
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_INFORMATION, false, uint32(p.cmd.Process.Pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return windows.SetPriorityClass(h, workerClass(low))
}

// terminate stops the process. Windows has no signal to ask a console process to stop: it is killed at once.
// What it started survives; a job object will hold the whole tree when the machine task (T14) needs one.
func terminate(p *process) { kill(p) }

// kill kills the process.
func kill(p *process) {
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
}

// errNoPause says why a worker's process cannot be paused on Windows. Windows has no signal that stops a process:
// it would take suspending each thread of each process of the tree, or a job object holding the tree (T14).
var errNoPause = errors.New("pausing a worker is not possible on Windows yet: Windows has no signal that stops " +
	"a process and what it started; stop the task instead, or let it run")

// pause refuses: see errNoPause.
func pause(*process) error { return errNoPause }

// resume refuses: nothing was paused.
func resume(*process) error { return errNoPause }

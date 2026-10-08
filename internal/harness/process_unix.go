//go:build !windows

package harness

import (
	"os/exec"
	"syscall"
)

// ownGroup makes the process lead a group of its own: a signal to the group reaches what it started.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// terminate asks the process group to stop.
func terminate(cmd *exec.Cmd) { signalGroup(cmd, syscall.SIGTERM) }

// kill kills what is left of the process group.
func kill(cmd *exec.Cmd) { signalGroup(cmd, syscall.SIGKILL) }

func signalGroup(cmd *exec.Cmd, sig syscall.Signal) {
	if cmd.Process == nil {
		return
	}
	// The group may be gone already: nothing to do then.
	if err := syscall.Kill(-cmd.Process.Pid, sig); err != nil {
		_ = cmd.Process.Signal(sig)
	}
}

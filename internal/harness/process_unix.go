//go:build !windows

package harness

import (
	"errors"
	"os/exec"
	"syscall"
)

// ownGroup makes the process lead a group of its own: a signal to the group reaches what it started.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// terminate asks the process group to stop. A paused group gets the request once it goes on: it goes on at once.
func terminate(cmd *exec.Cmd) {
	signalGroup(cmd, syscall.SIGTERM)
	signalGroup(cmd, syscall.SIGCONT)
}

// pause stops the process group where it is: SIGSTOP cannot be caught nor ignored.
func pause(cmd *exec.Cmd) error { return signalGroupErr(cmd, syscall.SIGSTOP) }

// resume lets the process group go on.
func resume(cmd *exec.Cmd) error { return signalGroupErr(cmd, syscall.SIGCONT) }

// kill kills what is left of the process group.
func kill(cmd *exec.Cmd) { signalGroup(cmd, syscall.SIGKILL) }

func signalGroup(cmd *exec.Cmd, sig syscall.Signal) {
	// The group may be gone already: nothing to do then.
	_ = signalGroupErr(cmd, sig)
}

// signalGroupErr sends sig to the process group, or to the process alone when the group is gone.
func signalGroupErr(cmd *exec.Cmd, sig syscall.Signal) error {
	if cmd.Process == nil {
		return errors.New("the process has not started")
	}
	if err := syscall.Kill(-cmd.Process.Pid, sig); err != nil {
		return cmd.Process.Signal(sig)
	}
	return nil
}

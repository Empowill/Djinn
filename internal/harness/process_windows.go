//go:build windows

package harness

import (
	"os/exec"
	"syscall"
)

// ownGroup starts the process in a group of its own, so that a Ctrl+C in Djinn's console does not reach it.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// terminate stops the process. Windows has no signal to ask a console process to stop: it is killed at once.
// What it started survives; a job object will hold the whole tree when the machine task (T17) needs one.
func terminate(cmd *exec.Cmd) { kill(cmd) }

// kill kills the process.
func kill(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

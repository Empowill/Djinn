//go:build windows

package harness

import (
	"errors"
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

// errNoPause says why a worker's process cannot be paused on Windows. Windows has no signal that stops a process:
// it would take suspending each thread of each process of the tree, or a job object holding the tree (T17).
var errNoPause = errors.New("pausing a worker is not possible on Windows yet: Windows has no signal that stops " +
	"a process and what it started; stop the task instead, or let it run")

// pause refuses: see errNoPause.
func pause(*exec.Cmd) error { return errNoPause }

// resume refuses: nothing was paused.
func resume(*exec.Cmd) error { return errNoPause }

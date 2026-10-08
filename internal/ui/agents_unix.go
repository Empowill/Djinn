//go:build !windows

package ui

import (
	"os/exec"
	"syscall"
)

// detach runs cmd in a session of its own, without a controlling terminal: an interactive shell started by a
// djinn up in a terminal then cannot take that terminal, nor be stopped waiting for it.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

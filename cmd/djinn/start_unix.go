//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// detach runs cmd in a session of its own: it outlives this process and its terminal, and a Ctrl+C there does not
// reach it.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

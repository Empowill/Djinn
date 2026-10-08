package main

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// detach runs cmd without a console and in a process group of its own: it outlives this process and its console,
// and a Ctrl+C there does not reach it.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS,
		HideWindow:    true,
	}
}

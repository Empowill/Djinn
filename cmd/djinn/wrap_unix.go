//go:build !windows

package main

import "syscall"

// execCommand runs the command at path in place of djinn's process: the same process, as the worker's shell started.
func execCommand(path string, args, env []string) error { return syscall.Exec(path, args, env) }

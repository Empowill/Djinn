//go:build !windows

package gate

import (
	"errors"
	"syscall"
)

// alive says whether the process pid still runs: signal 0 checks it without touching it. A process of another user
// runs too.
func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

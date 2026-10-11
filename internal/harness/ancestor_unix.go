//go:build !linux && !windows

package harness

import "syscall"

func isDescendant(peerPID, workerPID int, _ string) bool {
	if peerPID <= 0 || workerPID <= 0 {
		return false
	}
	if peerPID == workerPID {
		return true
	}
	if pgid, err := syscall.Getpgid(peerPID); err == nil && pgid == workerPID {
		return true
	}
	return false
}

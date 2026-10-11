//go:build darwin

package server

import (
	"net"
	"syscall"

	"golang.org/x/sys/unix"
)

func peerPID(c net.Conn) int {
	sc, ok := c.(syscall.Conn)
	if !ok {
		return 0
	}
	rc, err := sc.SyscallConn()
	if err != nil {
		return 0
	}
	var pid int
	_ = rc.Control(func(fd uintptr) {
		if p, err := unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID); err == nil && p > 0 {
			pid = p
		}
	})
	return pid
}

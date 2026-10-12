//go:build linux

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
		if ucred, err := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED); err == nil && ucred != nil {
			pid = int(ucred.Pid)
		}
	})
	return pid
}

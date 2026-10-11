//go:build !linux && !darwin

package server

import "net"

func peerPID(c net.Conn) int {
	return 0
}

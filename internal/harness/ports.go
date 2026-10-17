package harness

// The ports a serve's processes listen on (listeners, one way per system), read from what each system prints of its
// TCP sockets: Linux's /proc/net/tcp and tcp6, macOS's lsof, Windows' GetExtendedTcpTable. Their parsers are here, on
// every system, so that Linux's tests read them all.

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"net"
	"strconv"
	"strings"
)

// keptPort adds the port number listening on ip to found, unless its address is not kept (keptAddress) or found has
// the port already: a server listening on 0.0.0.0 and :: is one port.
func keptPort(found []listener, ip net.IP, number uint64) []listener {
	if !keptAddress(ip) || number == 0 || number > 65535 {
		return found
	}
	for _, l := range found {
		if l.port == uint32(number) {
			return found
		}
	}
	return append(found, listener{address: ip.String(), port: uint32(number)})
}

// parseNetTCP reads a Linux /proc/net/tcp or tcp6: the sockets that listen (state 0A) among inodes, those of a serve's
// processes. An address is printed as the hex of its 32-bit words in the host's order, little-endian on every machine
// Djinn runs on; its port as the hex of the number.
func parseNetTCP(b []byte, inodes map[string]bool, found []listener) []listener {
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Scan() // The header.
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 10 || f[3] != "0A" || !inodes[f[9]] {
			continue
		}
		addr, port, ok := strings.Cut(f[1], ":")
		if !ok {
			continue
		}
		raw, err := hex.DecodeString(addr)
		if err != nil || (len(raw) != net.IPv4len && len(raw) != net.IPv6len) {
			continue
		}
		for i := 0; i+4 <= len(raw); i += 4 {
			raw[i], raw[i+1], raw[i+2], raw[i+3] = raw[i+3], raw[i+2], raw[i+1], raw[i]
		}
		n, err := strconv.ParseUint(port, 16, 32)
		if err != nil {
			continue
		}
		found = keptPort(found, net.IP(raw), n)
	}
	return found
}

// parseLsof reads what lsof -nP -a -iTCP -sTCP:LISTEN -Fn prints of the sockets that listen: a line n<address>:<port>
// for each, the address * for every interface.
func parseLsof(b []byte) []listener {
	var found []listener
	for line := range strings.SplitSeq(string(b), "\n") {
		name, ok := strings.CutPrefix(strings.TrimSpace(line), "n")
		if !ok {
			continue
		}
		host, port, err := net.SplitHostPort(name)
		if err != nil {
			continue
		}
		if host == "*" {
			host = "0.0.0.0"
		}
		n, err := strconv.ParseUint(port, 10, 32)
		if err != nil {
			continue
		}
		found = keptPort(found, net.ParseIP(host), n)
	}
	return found
}

// tcpListen is the state of a socket that listens in Windows' TCP tables (MIB_TCP_STATE_LISTEN).
const tcpListen = 2

// parseTCPTable reads a table GetExtendedTcpTable gives (TCP_TABLE_OWNER_PID_LISTENER): MIB_TCPTABLE_OWNER_PID, or
// with v6 MIB_TCP6TABLE_OWNER_PID, its count then its rows. It keeps the sockets that listen and whose process is
// among pids. A port is in the network's order, an address as its bytes.
func parseTCPTable(b []byte, v6 bool, pids map[int]bool, found []listener) []listener {
	if len(b) < 4 {
		return found
	}
	n := int(binary.LittleEndian.Uint32(b))
	size, addr, port, state, pid := 24, 4, 8, 0, 20 // MIB_TCPROW_OWNER_PID
	if v6 {
		size, addr, port, state, pid = 56, 0, 20, 48, 52 // MIB_TCP6ROW_OWNER_PID
	}
	for i := range n {
		row := b[min(4+i*size, len(b)):]
		if len(row) < size {
			break
		}
		if binary.LittleEndian.Uint32(row[state:]) != tcpListen || !pids[int(binary.LittleEndian.Uint32(row[pid:]))] {
			continue
		}
		ip := net.IP(append([]byte(nil), row[addr:addr+4]...))
		if v6 {
			ip = net.IP(append([]byte(nil), row[addr:addr+16]...))
		}
		found = keptPort(found, ip, uint64(binary.BigEndian.Uint16(row[port:])))
	}
	return found
}

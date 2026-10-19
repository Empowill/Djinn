package harness

import (
	"encoding/binary"
	"net"
	"slices"
	"testing"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// TestParseNetTCP: Linux's tables of sockets, as /proc/net/tcp and tcp6 print them: the sockets of the serve that
// listen on the loopback or every interface, a port once.
func TestParseNetTCP(t *testing.T) {
	t.Parallel()
	tcp := []byte(`  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:1435 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 101 1 0000000000000000 100 0 0 10 0
   1: 00000000:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 102 1 0000000000000000 100 0 0 10 0
   2: 0F02000A:0016 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 103 1 0000000000000000 100 0 0 10 0
   3: 0100007F:1435 0100007F:C350 01 00000000:00000000 00:00000000 00000000  1000        0 104 1 0000000000000000 20 4 30 10 -1
   4: 0100007F:0CEA 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 999 1 0000000000000000 100 0 0 10 0
`)
	tcp6 := []byte(`  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000000000000000000001000000:22B8 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 105 1 0000000000000000 100 0 0 10 0
   1: 00000000000000000000000000000000:1F90 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 106 1 0000000000000000 100 0 0 10 0
`)
	inodes := map[string]bool{"101": true, "102": true, "103": true, "104": true, "105": true, "106": true}
	got := parseNetTCP(tcp6, inodes, parseNetTCP(tcp, inodes, nil))
	want := []listener{{"127.0.0.1", 5173}, {"0.0.0.0", 8080}, {"::1", 8888}}
	if !slices.Equal(got, want) {
		t.Errorf("found %v, want %v: not 10.0.2.15:22, not a connection, not another process's socket, 8080 once", got, want)
	}
}

// TestParseLsof: macOS's sockets that listen, as lsof -Fn prints them.
func TestParseLsof(t *testing.T) {
	t.Parallel()
	out := []byte("p501\nf12\nn127.0.0.1:5173\nf13\nn*:8080\np502\nf4\nn[::1]:8888\nf5\nn192.168.1.20:22\nf6\nn*:8080\n")
	want := []listener{{"127.0.0.1", 5173}, {"0.0.0.0", 8080}, {"::1", 8888}}
	if got := parseLsof(out); !slices.Equal(got, want) {
		t.Errorf("found %v, want %v", got, want)
	}
}

// TestParseTCPTable: Windows' tables of sockets, as GetExtendedTcpTable gives them: those that listen, of the serve's
// processes.
func TestParseTCPTable(t *testing.T) {
	t.Parallel()
	row4 := func(state uint32, ip net.IP, port uint16, pid uint32) []byte {
		b := make([]byte, 24)
		binary.LittleEndian.PutUint32(b[0:], state)
		copy(b[4:8], ip.To4())
		binary.BigEndian.PutUint16(b[8:], port)
		binary.LittleEndian.PutUint32(b[20:], pid)
		return b
	}
	table := binary.LittleEndian.AppendUint32(nil, 4)
	table = append(table, row4(tcpListen, net.IPv4(127, 0, 0, 1), 5173, 7)...)
	table = append(table, row4(tcpListen, net.IPv4(0, 0, 0, 0), 8080, 8)...)
	table = append(table, row4(tcpListen, net.IPv4(127, 0, 0, 1), 9000, 9)...) // Another process's.
	table = append(table, row4(5, net.IPv4(127, 0, 0, 1), 5174, 7)...)         // Established.
	pids := map[int]bool{7: true, 8: true}
	got := parseTCPTable(table, false, pids, nil)

	row6 := make([]byte, 56)
	copy(row6[0:16], net.IPv6loopback)
	binary.BigEndian.PutUint16(row6[20:], 8888)
	binary.LittleEndian.PutUint32(row6[48:], tcpListen)
	binary.LittleEndian.PutUint32(row6[52:], 8)
	got = parseTCPTable(append(binary.LittleEndian.AppendUint32(nil, 1), row6...), true, pids, got)
	want := []listener{{"127.0.0.1", 5173}, {"0.0.0.0", 8080}, {"::1", 8888}}
	if !slices.Equal(got, want) {
		t.Errorf("found %v, want %v", got, want)
	}
}

// TestMergePorts: a serve's ports are those it declares, first, with their names, then those its processes listen on,
// by number; a declared port found keeps its name and takes its address.
func TestMergePorts(t *testing.T) {
	t.Parallel()
	was := []*planv1.Port{
		{Number: 8080, Label: "api", Declared: true},
		{Number: 9999, Open: true}, // Found at the last reading, gone since.
	}
	got := mergePorts(was, []listener{{"0.0.0.0", 8080}, {"::", 5174}, {"127.0.0.1", 5173}})
	var numbers []uint32
	for _, p := range got {
		numbers = append(numbers, p.GetNumber())
	}
	if !slices.Equal(numbers, []uint32{8080, 5173, 5174}) || got[0].GetLabel() != "api" || got[0].GetAddress() != "0.0.0.0" ||
		!got[0].GetOpen() || !got[0].GetDeclared() || got[1].GetDeclared() || !got[1].GetOpen() {
		t.Errorf("merged %v", got)
	}
}

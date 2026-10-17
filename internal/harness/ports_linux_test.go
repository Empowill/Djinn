package harness

import (
	"net"
	"os"
	"slices"
	"testing"
)

// TestListenersOfAProcess (L06 section 5): the ports a process listens on are read from its sockets and the LISTEN lines of
// /proc/net/tcp and tcp6: the test binary's, on 127.0.0.1 and ::1, and none of an excluded process's (the agent's
// own).
func TestListenersOfAProcess(t *testing.T) {
	t.Parallel()
	v4, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer v4.Close()
	want := []listener{{address: "127.0.0.1", port: uint32(v4.Addr().(*net.TCPAddr).Port)}}
	if v6, err := net.Listen("tcp", "[::1]:0"); err == nil { // A machine may have no IPv6.
		defer v6.Close()
		want = append(want, listener{address: "::1", port: uint32(v6.Addr().(*net.TCPAddr).Port)})
	}
	found := listeners([]int{os.Getpid()}, nil)
	for _, w := range want {
		if !slices.Contains(found, w) {
			t.Errorf("found %v, want %v among them", found, w)
		}
	}
	if found := listeners([]int{os.Getpid()}, []int{os.Getpid()}); len(found) != 0 {
		t.Errorf("found %v on an excluded process, want none", found)
	}
}

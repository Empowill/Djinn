package harness

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// listeners lists the TCP ports that the processes pids listen on, but exclude's, on the loopback or every interface:
// the sockets each holds (/proc/<pid>/fd, socket:[inode]) that listen in the network of the first one (its
// /proc/<pid>/net/tcp and tcp6).
func listeners(pids, exclude []int) []listener {
	inodes := map[string]bool{}
	first := 0
	for _, pid := range pids {
		if slices.Contains(exclude, pid) {
			continue
		}
		dir := filepath.Join("/proc", strconv.Itoa(pid), "fd")
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // Ended, or not Djinn's user's.
		}
		if first == 0 {
			first = pid
		}
		for _, e := range entries {
			link, err := os.Readlink(filepath.Join(dir, e.Name()))
			if inode, ok := strings.CutPrefix(link, "socket:["); err == nil && ok {
				inodes[strings.TrimSuffix(inode, "]")] = true
			}
		}
	}
	if len(inodes) == 0 {
		return nil
	}
	var found []listener
	for _, table := range []string{"tcp", "tcp6"} {
		if b, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(first), "net", table)); err == nil {
			found = parseNetTCP(b, inodes, found)
		}
	}
	return found
}

//go:build linux

package harness

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/empowill/djinn/internal/machine"
)

func isDescendant(peerPID, workerPID int, cgroup string) bool {
	if peerPID <= 0 || workerPID <= 0 {
		return false
	}
	if peerPID == workerPID {
		return true
	}
	if pgid, err := syscall.Getpgid(peerPID); err == nil && pgid == workerPID {
		return true
	}
	if cgroup != "" {
		if pids, err := machine.CgroupPIDs(cgroup); err == nil && slices.Contains(pids, peerPID) {
			return true
		}
	}
	cur := peerPID
	for range 32 {
		stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", cur))
		if err != nil {
			break
		}
		s := string(stat)
		idx := strings.LastIndexByte(s, ')')
		if idx < 0 || idx+2 >= len(s) {
			break
		}
		fields := strings.Fields(s[idx+2:])
		if len(fields) < 2 {
			break
		}
		ppid, err := strconv.Atoi(fields[1])
		if err != nil || ppid <= 1 {
			break
		}
		if ppid == workerPID {
			return true
		}
		cur = ppid
	}
	return false
}

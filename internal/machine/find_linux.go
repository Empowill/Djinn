package machine

import (
	"os"
	"strconv"
	"strings"
)

// listCommands lists the processes of /proc and their command lines, arguments one space apart.
func listCommands() ([]proc, map[int]string, error) {
	procs, err := listProcs()
	if err != nil {
		return nil, nil, err
	}
	lines := make(map[int]string, len(procs))
	for _, p := range procs {
		if b, err := os.ReadFile("/proc/" + strconv.Itoa(p.pid) + "/cmdline"); err == nil {
			lines[p.pid] = strings.ReplaceAll(string(b), "\x00", " ")
		}
	}
	return procs, lines, nil
}

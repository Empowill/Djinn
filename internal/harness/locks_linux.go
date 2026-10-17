package harness

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// busyIn is a process, Djinn aside, whose working folder is dir or within it; 0 for none.
func busyIn(dir string) int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	dir = filepath.Clean(dir)
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	self := os.Getpid()
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == self {
			continue
		}
		cwd, err := os.Readlink(filepath.Join("/proc", e.Name(), "cwd"))
		if err == nil && (cwd == dir || strings.HasPrefix(cwd, dir+string(filepath.Separator))) {
			return pid
		}
	}
	return 0
}

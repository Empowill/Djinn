package harness

import (
	"os/exec"
	"slices"
	"strconv"
	"strings"
)

// listeners lists the TCP ports that the processes pids listen on, but exclude's, on the loopback or every interface,
// as lsof prints them. Untested: no Mac runs Djinn's tests yet.
func listeners(pids, exclude []int) []listener {
	var list []string
	for _, pid := range pids {
		if !slices.Contains(exclude, pid) {
			list = append(list, strconv.Itoa(pid))
		}
	}
	if len(list) == 0 {
		return nil
	}
	// It exits 1 when none listens: what it printed is read all the same.
	out, _ := exec.Command("lsof", "-nP", "-a", "-iTCP", "-sTCP:LISTEN", "-p", strings.Join(list, ","), "-Fn").Output()
	return parseLsof(out)
}

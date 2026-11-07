package machine

import (
	"bufio"
	"bytes"
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// listCommands lists the processes and their command lines with ps, for at most 5 seconds. Not run on a Mac yet.
func listCommands() ([]proc, map[int]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-A", "-o", "pid=,ppid=,command=").Output()
	if err != nil {
		return nil, nil, err
	}
	var procs []proc
	lines := map[int]string{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 2 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		if err1 != nil || err2 != nil {
			continue
		}
		procs = append(procs, proc{pid: pid, ppid: ppid})
		lines[pid] = strings.Join(f[2:], " ")
	}
	return procs, lines, sc.Err()
}

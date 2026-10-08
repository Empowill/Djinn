package machine

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrNoCPULimit is why the CPU of a worker cannot be capped here: CPULimit wraps it.
var ErrNoCPULimit = errors.New("CPU limits per worker are off")

// scopePrefix is the command that runs a worker in a systemd user scope of its own, its CPU capped at percent of
// one core. The scope keeps the process: same PID, same process group, same streams.
func scopePrefix(percent int) []string {
	return []string{"systemd-run", "--user", "--scope", "--quiet", "--collect", "-p", "CPUQuota=" + strconv.Itoa(percent) + "%", "--"}
}

// checkQuota checks what a scope reads in its cpu.max ("<quota> <period>", in microseconds) against percent of a
// core.
func checkQuota(cpuMax string, percent int) error {
	f := strings.Fields(cpuMax)
	if len(f) != 2 {
		return fmt.Errorf("%w: the scope's cpu.max reads %q", ErrNoCPULimit, strings.TrimSpace(cpuMax))
	}
	quota, err1 := strconv.Atoi(f[0])
	period, err2 := strconv.Atoi(f[1])
	if err1 != nil || err2 != nil || period <= 0 {
		return fmt.Errorf("%w: the scope's CPU is not capped (cpu.max reads %q)", ErrNoCPULimit, strings.TrimSpace(cpuMax))
	}
	// systemd rounds the quota to the millisecond of a period of 100 ms.
	if want := percent * period / 100; quota < want-1000 || quota > want+1000 {
		return fmt.Errorf("%w: the scope's quota is %d µs every %d µs, not %d%%", ErrNoCPULimit, quota, period, percent)
	}
	return nil
}

package machine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// probe reads, from a scope started as a worker would be, its cgroup's cpu.max. Without the cpu controller the
// file does not exist: systemd accepts CPUQuota and silently caps nothing.
const probe = `cat "/sys/fs/cgroup$(sed -n 's/^0:://p' /proc/self/cgroup)/cpu.max"`

// CPULimit returns the command prefix that runs each worker in a systemd user scope of its own, its CPU capped at
// percent of one core (150 is a core and a half), without root. It starts one such scope first and reads its
// cgroup: an error wrapping ErrNoCPULimit says why the cap would not hold, and Djinn runs workers as they are.
func CPULimit(ctx context.Context, percent int) ([]string, error) {
	if percent <= 0 {
		return nil, fmt.Errorf("%w: a limit of %d%%", ErrNoCPULimit, percent)
	}
	prefix := scopePrefix(percent)
	if _, err := exec.LookPath(prefix[0]); err != nil {
		return nil, fmt.Errorf("%w: no systemd-run here, so no systemd scope", ErrNoCPULimit)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var out, errOut bytes.Buffer
	cmd := exec.CommandContext(ctx, prefix[0], append(prefix[1:], "sh", "-c", probe)...)
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		why := strings.TrimSpace(errOut.String())
		var exit *exec.ExitError
		switch {
		case strings.Contains(why, "cpu.max"):
			return nil, fmt.Errorf("%w: systemd does not give the cpu controller to your user (an administrator "+
				"adds Delegate=cpu cpuset io memory pids to user@.service, once)", ErrNoCPULimit)
		case errors.As(err, &exit) && why != "":
			return nil, fmt.Errorf("%w: systemd-run --user failed: %s", ErrNoCPULimit, why)
		}
		return nil, fmt.Errorf("%w: systemd-run --user failed: %v", ErrNoCPULimit, err)
	}
	if err := checkQuota(out.String(), percent); err != nil {
		return nil, err
	}
	return prefix, nil
}

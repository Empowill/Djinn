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

// probe prints, from a scope started as a worker's would be, its cgroup, then its cpu.max and its memory.max, or
// "none" for a file that does not exist: without the controller, systemd accepts the cap and silently holds nothing.
const probe = `d="$(sed -n 's/^0:://p' /proc/self/cgroup)"; echo "$d"; ` +
	`cat "/sys/fs/cgroup$d/cpu.max" 2>/dev/null || echo none; cat "/sys/fs/cgroup$d/memory.max" 2>/dev/null || echo none`

// ProbeScopes returns the scopes that run each worker in a systemd user scope of its own, its CPU capped at cpu
// percent of one core (150 is a core and a half) and its memory at memory bytes, 0 capping nothing. It starts one
// such scope first and reads its cgroup. An error wrapping ErrNoScope says why there is none: Djinn runs workers as
// they are. A cap that would not hold is set to 0, and a note says why.
func ProbeScopes(ctx context.Context, cpu int, memory uint64) (*Scopes, []string, error) {
	s := &Scopes{CPU: max(cpu, 0), Memory: memory}
	sc := s.New("probe")
	if _, err := exec.LookPath(sc.Prefix[0]); err != nil {
		return nil, nil, fmt.Errorf("%w: no systemd-run here", ErrNoScope)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var out, errOut bytes.Buffer
	cmd := exec.CommandContext(ctx, sc.Prefix[0], append(sc.Prefix[1:], "sh", "-c", probe)...)
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		why := strings.TrimSpace(errOut.String())
		var exit *exec.ExitError
		if errors.As(err, &exit) && why != "" {
			return nil, nil, fmt.Errorf("%w: systemd-run --user failed: %s", ErrNoScope, why)
		}
		return nil, nil, fmt.Errorf("%w: systemd-run --user failed: %w", ErrNoScope, err)
	}
	notes, err := readProbe(out.String(), s)
	if err != nil {
		return nil, nil, err
	}
	return s, notes, nil
}

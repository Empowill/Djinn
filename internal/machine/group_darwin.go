package machine

import (
	"context"
	"os/exec"
	"time"
)

// NotMeasured is why the workers' CPU and memory are not measured here; empty: they are.
const NotMeasured = ""

// listProcs lists the processes with ps, for at most 5 seconds. Not run on a Mac yet.
func listProcs() ([]proc, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", PSArgs...).Output()
	if err != nil {
		return nil, err
	}
	return parsePS(out)
}

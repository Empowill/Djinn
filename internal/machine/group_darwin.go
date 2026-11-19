package machine

import (
	"context"
	"os/exec"
	"time"
)

// NotMeasured is why the workers' CPU and memory are not measured here; empty: they are.
const NotMeasured = ""

// listProcs lists the processes with ps, for at most 5 seconds, each one's memory its footprint (Footprint, Q86 A)
// when the kernel gives it: ps rss leaves out the compressed memory of a Mac, and saw 0.06 GB of a process holding
// 3 GB in W140's probe. A process the footprint cannot be read of, another user's, keeps its rss.
func listProcs() ([]proc, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", PSArgs...).Output()
	if err != nil {
		return nil, err
	}
	procs, err := parsePS(out)
	for i := range procs {
		if now, _, ok := Footprint(procs[i].pid); ok {
			procs[i].memory = now
		}
	}
	return procs, err
}

package main

import (
	"context"
	"fmt"
	"strings"

	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
	"github.com/empowill/djinn/internal/store"
)

// held is why a djinn up resumes none of the workers the last one left interrupted (harness.WithResumeHeld), or none.
type held struct {
	why     uiv1.HeldStart // unspecified: it resumes them
	percent float64        // the machine's memory at the Hourglass's last reading, for HELD_START_MEMORY
	lead    string         // what the lead's restart event says of it, after "as"
}

// heldStart decides whether this djinn up resumes the workers the last one left interrupted. It holds them when the
// Hourglass's last reading of the last djinn up (last, the store's) had the machine's memory, of total, at or above
// limit percent (0 never), when the last djinn up did not stop cleanly (unclean: it left its address behind, a
// crash, a kill or the machine going down), and when djinn up --paused asks it: a worker that brought the machine down
// would bring it down again. The memory comes first, then the unclean stop, then --paused: the most a person can act on.
func heldStart(ctx context.Context, paused, unclean bool, limit int, total uint64,
	last func(context.Context) (store.Sample, bool, error)) held {
	if limit > 0 && total > 0 && last != nil {
		x, ok, err := last(ctx)
		if err == nil && ok {
			if percent := float64(x.Memory) / float64(total) * 100; percent >= float64(limit) {
				stop := ""
				if unclean {
					stop = ", and that djinn up did not stop cleanly"
				}
				return held{why: uiv1.HeldStart_HELD_START_MEMORY, percent: percent, lead: fmt.Sprintf(
					"the Hourglass's last reading of the last djinn up, at %s, had the machine's memory at %.0f %% "+
						"(--paused-memory %d)%s: find what filled it (djinn machine series, logs/djinn.log) before you continue a worker",
					x.At.Format("2006-01-02 15:04:05"), percent, limit, stop)}
			}
		}
	}
	if unclean {
		return held{why: uiv1.HeldStart_HELD_START_UNCLEAN, lead: "the last djinn up did not stop cleanly " +
			"(a crash, a kill, the machine going down): find what ended it (logs/djinn.log) before you continue a worker"}
	}
	if paused {
		return held{why: uiv1.HeldStart_HELD_START_PAUSED, lead: "djinn up --paused asked it"}
	}
	return held{}
}

// withoutPaused is the flags of djinn up but --paused: a restart on a newer djinn (main.go) resumes the workers its
// stop interrupted, --paused holding the start it was given to only.
func withoutPaused(args []string) []string {
	var out []string
	for _, a := range args {
		name, _, _ := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if strings.HasPrefix(a, "-") && name == "paused" {
			continue
		}
		out = append(out, a)
	}
	return out
}

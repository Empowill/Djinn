//go:build linux

package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/empowill/djinn/internal/machine"
)

// workerScopes are the systemd scopes the workers run in, one each, their CPU capped at cpu percent of a core and
// their memory at memory bytes (0: uncapped); nil where there are none: djinn up says why, once, and workers run in
// their process group.
func workerScopes(ctx context.Context, w io.Writer, cpu int, memory uint64) *machine.Scopes {
	scopes, notes, err := machine.ProbeScopes(ctx, cpu, memory)
	if err != nil {
		uncapped := ""
		if cpu > 0 || memory > 0 {
			uncapped = ", uncapped"
		}
		fmt.Fprintf(w, "djinn: %v; workers run in their process group%s\n", err, uncapped)
		return nil
	}
	for _, note := range notes {
		fmt.Fprintf(w, "djinn: %s\n", note)
	}
	var caps []string
	if scopes.CPU > 0 {
		caps = append(caps, fmt.Sprintf("its CPU capped at %d%% of a core", scopes.CPU))
	}
	if scopes.Memory > 0 {
		caps = append(caps, fmt.Sprintf("its memory at %d MiB", scopes.Memory>>20))
	}
	if len(caps) > 0 {
		fmt.Fprintf(w, "djinn: each worker runs in a systemd scope of its own, %s\n", strings.Join(caps, ", "))
	}
	return scopes
}

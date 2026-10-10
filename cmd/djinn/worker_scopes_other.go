//go:build !linux

package main

import (
	"context"
	"fmt"
	"io"

	"github.com/empowill/djinn/internal/machine"
)

// workerScopes reports the platform's lack of per-worker systemd scopes. Linux has the capped implementation.
func workerScopes(ctx context.Context, w io.Writer, cpu int, memory uint64) *machine.Scopes {
	_, _, err := machine.ProbeScopes(ctx, cpu, memory)
	uncapped := ""
	if cpu > 0 || memory > 0 {
		uncapped = ", uncapped"
	}
	fmt.Fprintf(w, "djinn: %v; workers run in their process group%s\n", err, uncapped)
	return nil
}

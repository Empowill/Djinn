package main

import (
	"context"
	"os"
	"syscall"
	"testing"
	"time"
)

// TestStopContext covers stopContext and stopWhy: djinn up's context stops at SIGINT, SIGTERM or a call, and the first
// source is the one the log says (decision Q84).
func TestStopContext(t *testing.T) {
	for _, c := range []struct {
		name string
		stop func(chan os.Signal, func(string))
		want string
	}{
		{"SIGINT", func(s chan os.Signal, _ func(string)) { s <- os.Interrupt }, stopSIGINT},
		{"SIGTERM", func(s chan os.Signal, _ func(string)) { s <- syscall.SIGTERM }, stopSIGTERM},
		{"Ctrl+Q", func(_ chan os.Signal, stop func(string)) { stop(stopCtrlQ) }, stopCtrlQ},
		{"the tray", func(_ chan os.Signal, stop func(string)) { stop(stopTray) }, stopTray},
		{"an install from the window, then the window ends", func(_ chan os.Signal, stop func(string)) {
			stop(restartStop + "an install from the window")
			stop(stopWindow)
		}, "restart: an install from the window"},
		{"djinn update", func(_ chan os.Signal, stop func(string)) {
			stop(restartStop + "a restart on a newer Djinn from the command line (djinn update)")
		}, "restart: a restart on a newer Djinn from the command line (djinn update)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			signals := make(chan os.Signal, 1)
			ctx, stop, release := stopContext(t.Context(), signals)
			defer release()
			c.stop(signals, stop)
			select {
			case <-ctx.Done():
			case <-time.After(5 * time.Second):
				t.Fatal("djinn up's context is not done")
			}
			stop(stopSIGTERM) // Too late: the first source stays.
			if got := stopWhy(ctx); got != c.want {
				t.Errorf("stopWhy = %q; want %q", got, c.want)
			}
		})
	}

	t.Run("released without a source", func(t *testing.T) {
		ctx, _, release := stopContext(context.Background(), make(chan os.Signal))
		release()
		if got := stopWhy(ctx); got != "unknown" {
			t.Errorf("stopWhy = %q", got)
		}
	})
}

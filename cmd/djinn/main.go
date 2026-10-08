// Command djinn is the single entry point of Djinn: the native window, the local server and the command line
// that agents use to drive a mission.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/empowill/djinn/internal/cli"
	"github.com/empowill/djinn/internal/ui"
)

// version is set at build time with -ldflags "-X main.version=…". "dev" is a development build, which keeps its data
// in a directory of its own (see ui.Develop).
var version = "dev"

func main() {
	ui.Develop = version == "dev"
	// `djinn up` opens the app; every other command is generated from the protos by the cli package.
	if len(os.Args) > 1 && os.Args[1] == "up" {
		restart, err := runUp(os.Args[2:])
		if err != nil {
			fmt.Fprintln(os.Stderr, "djinn up:", err)
			os.Exit(1)
		}
		if restart {
			// Everything is closed: the newer djinn at this path takes over, with the same flags.
			home, _ := ui.Home()
			if _, err := startDetached(context.Background(), home, os.Stderr, os.Args[1:]...); err != nil {
				fmt.Fprintf(os.Stderr, "djinn up: the update did not start: %v\n"+
					"djinn up starts it again, and reopens the terminals noted in %s\n", err, RestartFile)
				os.Exit(1)
			}
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "update" {
		if err := runUpdate(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "djinn update:", err)
			os.Exit(1)
		}
		return
	}
	// `djinn gate run` runs a command here, under a gate the server grants.
	if len(os.Args) > 2 && os.Args[1] == "gate" && os.Args[2] == "run" {
		os.Exit(runGate(os.Args[3:]))
	}
	home, _ := ui.Home() // Only fails without a home directory; the command line then says it finds no server.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := cli.Run(ctx, os.Args[1:], cli.Config{
		Version: version,
		Addr:    os.Getenv("DJINN_ADDR"),
		Home:    home,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
		Start:   func(ctx context.Context) (string, error) { return startDetached(ctx, home, os.Stderr, "up") },
	})
	stop()
	os.Exit(code)
}

// Command djinn is the single entry point of Djinn: the native window, the local server and the command line
// that agents use to drive a mission.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"

	"github.com/empowill/djinn/internal/cli"
	"github.com/empowill/djinn/internal/ui"
)

// version is set at build time with -ldflags "-X main.version=…". "dev" is a development build, which keeps its data
// in a directory of its own (see ui.Develop).
var version = "dev"

// moduleVersion is the version go install recorded, such as v0.1.0, when the build stamped none: a binary installed
// with go install is a release, not a development build. A build from a checkout reports (devel), and stays "dev".
func moduleVersion(v string) string {
	if v != "dev" {
		return v
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return v
}

func main() {
	version = moduleVersion(version)
	ui.Develop = version == "dev"
	args := fromBundle(os.Args[1:])
	// `djinn open <link>` is what the system runs for a djinn:// link clicked anywhere; without a link, as the menu entry
	// of Linux runs it, it is djinn up.
	if len(args) == 1 && args[0] == "open" {
		args = []string{"up"}
	}
	fromLauncher(args, os.Stderr)
	if len(args) > 0 && args[0] == "open" {
		os.Exit(runOpen(args[1:], os.Stdout, os.Stderr))
	}
	// `djinn up` opens the app; every other command is generated from the protos by the cli package.
	if len(args) > 0 && args[0] == "up" {
		restart, err := runUp(args[1:])
		if err != nil {
			fmt.Fprintln(os.Stderr, "djinn up:", err)
			os.Exit(1)
		}
		if restart {
			// Everything is closed: the newer djinn at this path takes over, with the same flags.
			home, _ := ui.Home()
			if _, err := startDetached(context.Background(), home, os.Stderr, args...); err != nil {
				fmt.Fprintf(os.Stderr, "djinn up: the update did not start: %v\n"+
					"djinn up starts it again, and reopens the terminals noted in %s\n", err, RestartFile)
				os.Exit(1)
			}
		}
		return
	}
	if len(args) > 0 && args[0] == "update" {
		if err := runUpdate(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "djinn update:", err)
			os.Exit(1)
		}
		return
	}
	// `djinn gate run` runs a command here, under a gate the server grants.
	if len(args) > 1 && args[0] == "gate" && args[1] == "run" {
		os.Exit(runGate(args[2:]))
	}
	// `djinn backup` and `djinn backup restore` work with or without a running djinn.
	if len(args) > 0 && args[0] == "backup" {
		os.Exit(runBackup(args[1:], os.Stdout, os.Stderr))
	}
	home, _ := ui.Home() // Only fails without a home directory; the command line then says it finds no server.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := cli.Run(ctx, args, cli.Config{
		Version: version,
		Addr:    os.Getenv("DJINN_ADDR"),
		Home:    home,
		Stdin:   os.Stdin,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
		Start:   func(ctx context.Context) (string, error) { return startDetached(ctx, home, os.Stderr, "up") },
	})
	stop()
	os.Exit(code)
}

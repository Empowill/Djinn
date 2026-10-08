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
		if err := runUp(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "djinn up:", err)
			os.Exit(1)
		}
		return
	}
	home, _ := ui.Home() // Only fails without a home directory; the command line then says it finds no server.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := cli.Run(ctx, os.Args[1:], cli.Config{
		Version: version,
		Addr:    os.Getenv("DJINN_ADDR"),
		Home:    home,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
		Start:   func(ctx context.Context) (string, error) { return startDetached(ctx, home, os.Stderr) },
	})
	stop()
	os.Exit(code)
}

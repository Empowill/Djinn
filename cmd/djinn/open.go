package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect"

	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
	"github.com/empowill/djinn/gen/go/ui/v1/uiv1connect"
	"github.com/empowill/djinn/internal/cli"
	"github.com/empowill/djinn/internal/link"
	"github.com/empowill/djinn/internal/server"
	"github.com/empowill/djinn/internal/ui"
)

// runOpen is djinn open <link>: it hands the link to the djinn that runs on this data folder, one per folder as djinn
// up and djinn wish resume find it, after starting one in the background when none answers. It returns the exit code:
// 2 for a command line that is wrong, or a link that is not a djinn:// one, where nothing is started. Its help is
// cli.Open.
func runOpen(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		cli.Open.WriteHelp(stdout)
		return 0
	}
	if len(args) != 1 {
		fmt.Fprint(stderr, "djinn open: one link expected\n\n")
		cli.Open.WriteHelp(stderr)
		return 2
	}
	raw := args[0]
	// A djinn:// link the window does not know still goes to it, which says so where it was clicked; anything else is
	// not for Djinn.
	if scheme, _, ok := strings.Cut(strings.TrimSpace(raw), ":"); !ok || !strings.EqualFold(scheme, link.Scheme) {
		fmt.Fprintln(stderr, "djinn open:", link.UnknownError{URL: raw})
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), startTimeout+10*time.Second)
	defer cancel()
	home, err := ui.Home()
	if err == nil {
		err = openLink(ctx, home, os.Getenv("DJINN_ADDR"), raw, stdout, stderr)
	}
	if err != nil {
		fmt.Fprintln(stderr, "djinn open:", err)
		return 1
	}
	return 0
}

// openLink asks the djinn at addr, or the one that answers in home, started when none does, to show raw.
func openLink(ctx context.Context, home, addr, raw string, stdout, stderr io.Writer) error {
	if addr == "" {
		if running, err := server.ReadAddr(home); err == nil && cli.Alive(running) {
			addr = running
		} else if addr, err = startDetached(ctx, home, stderr, "up"); err != nil {
			return err
		}
	}
	client, base, err := cli.Dial(addr)
	if err != nil {
		return err
	}
	res, err := uiv1connect.NewUiServiceClient(client, base).OpenLink(ctx,
		connect.NewRequest(&uiv1.UiServiceOpenLinkRequest{Url: raw}))
	if err != nil {
		var ce *connect.Error
		if errors.As(err, &ce) {
			return errors.New(ce.Message())
		}
		return err
	}
	shown := "wish " + res.Msg.GetWishId()
	if id := res.Msg.GetTilasmId(); id != "" {
		shown = "tilasm " + id + " in its wish's Tilasms tab"
	}
	if res.Msg.GetWindow() {
		fmt.Fprintln(stdout, "djinn shows "+shown+" in its window")
	} else {
		fmt.Fprintln(stdout, "djinn shows "+shown+" in its page: open "+addr)
	}
	return nil
}

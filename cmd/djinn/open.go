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

const openUsage = `Usage: djinn open <link>

Open a djinn:// link in Djinn: djinn://tilasm/<id> shows the wish's Tilasms tab on that tilasm, djinn://wish/<id> the
wish. The system runs it for a link clicked anywhere (a browser, a chat, a Markdown file). It hands the link to the
running Djinn, starting it in the background when none runs. A link Djinn does not know is refused, and the window
says so. Without a link, djinn open is djinn up.
`

// runOpen is djinn open <link>: it hands the link to the djinn that runs on this data folder, one per folder as djinn
// up and djinn wish resume find it, after starting one in the background when none answers. It returns the exit code:
// 2 for a command line that is wrong, or a link that is not a djinn:// one, where nothing is started.
func runOpen(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprint(stdout, openUsage)
		return 0
	}
	if len(args) != 1 {
		fmt.Fprint(stderr, "djinn open: one link expected\n\n"+openUsage)
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

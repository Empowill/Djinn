//go:build !windows

package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
	"github.com/empowill/djinn/gen/go/ui/v1/uiv1connect"
	"github.com/empowill/djinn/internal/cli"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// TestOpenHandsTheLinkToTheRunningDjinn: djinn open, what the system runs for a djinn:// link, starts djinn when none
// runs, then hands the link to the one that runs: one djinn per data folder, as djinn up and djinn wish resume.
func TestOpenHandsTheLinkToTheRunningDjinn(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	env := environ(home, t.TempDir())
	// A wish and its tilasm, written while no djinn runs.
	db, err := store.Open(ctx, filepath.Join(home, store.File), plan.Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	wish := &planv1.Wish{Id: store.NewID(), Title: "Explain the lamp", CreateTime: timestamppb.Now()}
	tilasm := &planv1.Tilasm{Id: store.NewID(), WishId: wish.GetId(), Code: "L01", Title: "The lamp", CreateTime: timestamppb.Now()}
	err = db.Tx(ctx, func(tx *store.Tx) error {
		return errors.Join(tx.Journal("test", planv1connect.WishServiceMakeProcedure, &planv1.WishServiceMakeRequest{Title: wish.GetTitle()}),
			tx.Put(wish), tx.Put(tilasm))
	})
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	tilasmLink := "djinn://tilasm/" + strings.ToUpper(tilasm.GetId()) + "/"

	// Not a djinn:// link: refused, nothing started.
	if code, out, errs := runDjinn(t, env, "open", "https://example.com/tilasm/"+tilasm.GetId()); code != 2 ||
		!strings.Contains(errs, "not a link Djinn knows") || strings.Contains(errs, "started djinn up") {
		t.Fatalf("an https link: exit %d\n%s%s", code, out, errs)
	}

	// No djinn runs: open starts one in the background, which shows the tilasm, even to a page that watches after.
	code, out, errs := runDjinn(t, env, "open", tilasmLink)
	if strings.Contains(errs, "(pid ") {
		stopStarted(t, home, errs)
	}
	if code != 0 || !strings.Contains(errs, "started djinn up in the background") ||
		!strings.Contains(out, "djinn shows tilasm "+tilasm.GetId()+" in its wish's Tilasms tab") {
		t.Fatalf("open with no djinn: exit %d\n%s%s", code, out, errs)
	}
	addr := answering(t, home, func() string { return errs })
	httpClient, base, err := cli.Dial(addr)
	if err != nil {
		t.Fatal(err)
	}
	shows, err := uiv1connect.NewUiServiceClient(httpClient, base).WatchShow(ctx, connect.NewRequest(&uiv1.UiServiceWatchShowRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	defer shows.Close()
	next := func() *uiv1.UiServiceWatchShowResponse {
		t.Helper()
		if !shows.Receive() {
			t.Fatalf("the stream ended: %v", shows.Err())
		}
		return shows.Msg()
	}
	if got := next(); got.GetWishId() != wish.GetId() || got.GetTilasmId() != tilasm.GetId() {
		t.Fatalf("shown: %v", got)
	}

	// It runs: open hands the link over, and starts nothing.
	code, out, errs = runDjinn(t, env, "open", "djinn://wish/"+wish.GetId())
	if code != 0 || strings.Contains(errs, "started djinn up") || !strings.Contains(out, "djinn shows wish "+wish.GetId()) {
		t.Fatalf("open with djinn running: exit %d\n%s%s", code, out, errs)
	}
	if got := next(); got.GetWishId() != wish.GetId() || got.GetTilasmId() != "" {
		t.Fatalf("shown: %v", got)
	}

	// A djinn:// link it does not know, or a tilasm not here: it says so, and so does the window.
	for _, c := range []struct{ link, says string }{
		{"djinn://moon/" + wish.GetId(), "not a link Djinn knows"},
		{"djinn://tilasm/" + store.NewID(), "on this machine"},
	} {
		code, out, errs = runDjinn(t, env, "open", c.link)
		if code != 1 || !strings.Contains(errs, c.says) || strings.Contains(errs, "started djinn up") {
			t.Fatalf("%s: exit %d\n%s%s", c.link, code, out, errs)
		}
		if got := next(); got.GetUnknownLink() != c.link {
			t.Fatalf("%s: shown %v", c.link, got)
		}
	}
}

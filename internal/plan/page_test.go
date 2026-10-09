package plan

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/store"
)

// servePages runs the plan services with pages kept in home, rendered at most every 10 ms.
func servePages(t *testing.T, home string) (clients, *Pages) {
	t.Helper()
	s, err := store.Open(t.Context(), "", Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	pages := NewPages(s, home, "v0-test")
	pages.Interval = 10 * time.Millisecond
	mux := http.NewServeMux()
	for prefix, h := range Handlers(s, WithPages(pages)) {
		mux.Handle(prefix, h)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return clients{
		projects:     planv1connect.NewProjectServiceClient(srv.Client(), srv.URL),
		wishes:       planv1connect.NewWishServiceClient(srv.Client(), srv.URL),
		instructions: planv1connect.NewInstructionServiceClient(srv.Client(), srv.URL),
		questions:    planv1connect.NewQuestionServiceClient(srv.Client(), srv.URL),
		blocks:       planv1connect.NewBlockServiceClient(srv.Client(), srv.URL),
		store:        s,
	}, pages
}

// eventually waits for the file to hold want, or to be missing when want is empty.
func eventually(t *testing.T, file, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, err := os.ReadFile(file)
		if want != "" && err == nil && strings.Contains(string(data), want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never held %q (%v)", file, want, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestPageHasNoLocalPath(t *testing.T) {
	ctx := t.Context()
	c, wish, repo := source(t)
	data := t.TempDir()
	home, _ := os.UserHomeDir()
	note := "Logs in " + filepath.Join(data, "worktrees", "w1") + " and " + filepath.Join(home, "notes.md")
	if _, err := c.blocks.Put(ctx, connect.NewRequest(&planv1.BlockServicePutRequest{WishId: wish.GetId(), Title: "Where", Content: note})); err != nil {
		t.Fatal(err)
	}
	page, err := NewPages(c.store, data, "v0-test").Page(ctx, wish.GetId())
	if err != nil {
		t.Fatal(err)
	}
	html := string(page)
	for _, leak := range []string{repo, filepath.Dir(repo), home, data, "s3cret", "session-42", "secretCode", "the code itself"} {
		if strings.Contains(html, leak) {
			t.Errorf("the page holds %q", leak)
		}
	}
	for _, s := range []string{"Ship the API", "Which store?", "Write the store", "Logs in djinn-data", "~"} {
		if !strings.Contains(html, s) {
			t.Errorf("the page lacks %q", s)
		}
	}
}

func TestRender(t *testing.T) {
	ctx := t.Context()
	c, _ := servePages(t, t.TempDir())
	wish := c.wish(t)
	file := filepath.Join(t.TempDir(), "page.html")
	res, err := c.wishes.Render(ctx, connect.NewRequest(&planv1.WishServiceRenderRequest{WishId: wish, File: file}))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(res.Msg.GetFile())
	if err != nil || int64(len(data)) != res.Msg.GetSize() || !strings.Contains(string(data), "<h1>Try Djinn on itself</h1>") {
		t.Fatalf("render wrote %d bytes, said %d (%v)", len(data), res.Msg.GetSize(), err)
	}
	if !strings.Contains(string(data), "Active · rank 1") {
		t.Error("the page lacks the wish's rank")
	}
	if _, err := c.wishes.Render(ctx, connect.NewRequest(&planv1.WishServiceRenderRequest{WishId: store.NewID(), File: file})); code(err) != connect.CodeNotFound {
		t.Errorf("an unknown wish: %v, want not_found", err)
	}
}

func TestSync(t *testing.T) {
	home := t.TempDir()
	c, pages := servePages(t, home)
	ctx, stop := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { pages.Run(ctx); close(done) }()
	wish := c.wish(t)

	res, err := c.wishes.Sync(t.Context(), connect.NewRequest(&planv1.WishServiceSyncRequest{WishId: wish}))
	if err != nil {
		t.Fatal(err)
	}
	file := res.Msg.GetFile()
	if want := filepath.Join(home, PagesDir, wish, PageFile); file != want {
		t.Errorf("the page is at %s, want %s", file, want)
	}
	eventually(t, file, "<h1>Try Djinn on itself</h1>")

	// A change of the wish renders its page again.
	c.ask(t, wish)
	eventually(t, file, "Which store?")

	// A change of another wish leaves the page alone.
	other := c.wish(t)
	c.ask(t, other)
	before, _ := os.ReadFile(file)

	// Another djinn up takes the page back, and keeps it up to date.
	stop()
	<-done
	pages = NewPages(c.store, home, "v0-test")
	pages.Interval = 10 * time.Millisecond
	ctx, stop = context.WithCancel(t.Context())
	defer stop()
	go pages.Run(ctx)
	if _, err := c.blocks.Put(t.Context(), connect.NewRequest(&planv1.BlockServicePutRequest{WishId: wish, Title: "Plan B", Content: "Go."})); err != nil {
		t.Fatal(err)
	}
	eventually(t, file, "Plan B")
	if strings.Count(string(before), `<details class="q `) != 1 {
		t.Errorf("the page shows the other wish's question too")
	}

	// Deleting the file stops it.
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	c.ask(t, wish)
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("a deleted page came back: %v", err)
	}

	// Sync needs djinn up.
	plain := serve(t)
	if _, err := plain.wishes.Sync(t.Context(), connect.NewRequest(&planv1.WishServiceSyncRequest{WishId: wish})); code(err) != connect.CodeUnavailable {
		t.Errorf("sync without pages: %v, want unavailable", err)
	}
}

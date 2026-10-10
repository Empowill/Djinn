package plan

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/render"
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
		projects:  planv1connect.NewProjectServiceClient(srv.Client(), srv.URL),
		wishes:    planv1connect.NewWishServiceClient(srv.Client(), srv.URL),
		questions: planv1connect.NewQuestionServiceClient(srv.Client(), srv.URL),
		blocks:    planv1connect.NewBlockServiceClient(srv.Client(), srv.URL),
		store:     s,
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

// talk writes, in one transaction, the tasks and then n events of each line of says, in order: "W1 text Hello" is a
// text of W1. The tasks are made the first time they are named, in wish.
func talk(t testing.TB, s *store.Store, wish string, tasks map[string]*planv1.Task, n int, says ...string) {
	t.Helper()
	err := s.Tx(t.Context(), func(tx *store.Tx) error {
		if err := tx.Journal("test", "test/talk", &planv1.TaskEvent{}); err != nil {
			return err
		}
		for range n {
			for _, line := range says {
				code, rest, _ := strings.Cut(line, " ")
				kind, text, _ := strings.Cut(rest, " ")
				task := tasks[code]
				if task == nil {
					task = &planv1.Task{
						Id: store.NewID(), WishId: wish, Code: code, Title: "Work of " + code, Status: planv1.TaskStatus_TASK_STATUS_RUNNING,
					}
					tasks[code] = task
					if err := tx.Put(task); err != nil {
						return err
					}
				}
				task.Usage = &planv1.Usage{InputTokens: task.GetUsage().GetInputTokens() + 1} // the event's position
				ev := &planv1.TaskEvent{
					Id: store.NewID(), TaskId: task.GetId(), Seq: task.GetUsage().GetInputTokens(), Text: text,
					Kind: planv1.TaskEventKind(planv1.TaskEventKind_value["TASK_EVENT_KIND_"+strings.ToUpper(kind)]),
					Raw:  strings.Repeat("x", 4<<10), CreateTime: timestamppb.Now(),
				}
				if err := tx.Put(ev); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestPageReadsTheEventsItShows: a page renders each time a worker speaks, and a wish's events are most of the store.
// It reads only those it shows, from the last written back: the latest ones and each task's last word, never a tool's
// output, nor another wish's. The page is the one every event would make.
func TestPageReadsTheEventsItShows(t *testing.T) {
	ctx := t.Context()
	c, pages := servePages(t, t.TempDir())
	wish, other := c.wish(t), c.wish(t)
	tasks, others := map[string]*planv1.Task{}, map[string]*planv1.Task{}
	// W1 spoke long ago, then only ran tools; W2 talks a lot; W3 only runs tools; another wish's W1 talks last.
	talk(t, c.store, wish, tasks, 1, "W1 text Long ago, W1 said this", "W3 tool_call Bash ls")
	talk(t, c.store, wish, tasks, 20, "W1 tool_result total 8", "W3 tool_result x")
	talk(t, c.store, wish, tasks, 150, "W2 text Step", "W2 tool_call Bash ls", "W2 status running", "W2 text  ")
	talk(t, c.store, other, others, 30, "W1 text Elsewhere")

	all := []*planv1.Task{tasks["W1"], tasks["W2"], tasks["W3"]}
	events, err := pageEvents(ctx, c.store, all)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range events {
		got = append(got, fmt.Sprint(e.GetTaskId() == tasks["W1"].GetId(), e.GetSeq()))
	}
	// W1's last word, then W2's render.MaxEvents last texts and statuses: its last 100 rounds of four events, of which
	// a tool call, and an empty text that is not shown.
	want := []string{"true 1"}
	for i := 150 - render.MaxEvents/2 + 1; i <= 150; i++ {
		want = append(want, fmt.Sprint(false, 4*i-3), fmt.Sprint(false, 4*i-1))
	}
	if !slices.Equal(got, want) {
		t.Errorf("events read: %d, %v…; want %d, %v…", len(got), got[:min(len(got), 3)], len(want), want[:3])
	}

	// Another wish talks so much that its events hide this one's: each task's are read apart, to the same events.
	talk(t, c.store, other, others, foreignEvents+1, "W1 text Elsewhere")
	again, err := pageEvents(ctx, c.store, all)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.EqualFunc(again, events, func(a, b *planv1.TaskEvent) bool { return a.GetId() == b.GetId() }) {
		t.Errorf("events read past another wish's: %d, want the %d read before", len(again), len(events))
	}

	now := time.Now()
	lean, err := pages.page(ctx, wish, pageEvents, now)
	if err != nil {
		t.Fatal(err)
	}
	full, err := pages.page(ctx, wish, allEvents, now)
	if err != nil {
		t.Fatal(err)
	}
	if string(lean) != string(full) {
		t.Error("the page from the events it reads differs from the page from every event")
	}
	if html := string(lean); !strings.Contains(html, "Long ago, W1 said this") || strings.Contains(html, "Elsewhere") {
		t.Error("the page lacks W1's last word, or shows another wish's")
	}
}

// BenchmarkPage renders the page of a wish of 40 tasks of 250 events each, 4 KiB apiece, as djinn up does each time a
// worker speaks. Before W169 it read every event: 1.0 s a page on a store of 26,000 events.
func BenchmarkPage(b *testing.B) {
	s, err := store.Open(b.Context(), "", Entities()...)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { s.Close() })
	pages := NewPages(s, b.TempDir(), "v0-test")
	wish := &planv1.Wish{Id: store.NewID(), Title: "Bench"}
	if err := s.Tx(b.Context(), func(tx *store.Tx) error {
		if err := tx.Journal("test", "test/wish", wish); err != nil {
			return err
		}
		return tx.Put(wish)
	}); err != nil {
		b.Fatal(err)
	}
	tasks := map[string]*planv1.Task{}
	for i := range 40 {
		w := fmt.Sprintf("W%d ", i+1)
		talk(b, s, wish.GetId(), tasks, 50, w+"text Step", w+"tool_call Bash ls", w+"tool_result total 8",
			w+"status running", w+"tool_result total 8")
	}
	b.ResetTimer()
	for b.Loop() {
		if _, err := pages.Page(b.Context(), wish.GetId()); err != nil {
			b.Fatal(err)
		}
	}
}

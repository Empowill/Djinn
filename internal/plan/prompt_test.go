package plan

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/locales"
)

func TestMakePrompt(t *testing.T) {
	c := serve(t, WithLanguage("fr"))
	// The made wish's commit, before the lead's event is queued in its own.
	var committed []proto.Message
	c.store.OnCommit(func(ms []proto.Message) {
		if committed == nil {
			committed = slices.Clone(ms)
		}
	})
	prompt := "# Original request\n\n" + strings.Repeat("**Complete** request, with a list.\n- Keep this.\n", 30) + "\nFINAL DETAIL\n"
	for _, paused := range []bool{false, true} {
		committed = nil
		res, err := c.wishes.Make(t.Context(), connect.NewRequest(&planv1.WishServiceMakeRequest{Prompt: prompt, Paused: paused}))
		if err != nil {
			t.Fatal(err)
		}
		wish := res.Msg.GetWish()
		if len(committed) != 2 {
			t.Fatalf("wish and creation block were not committed together: %v", committed)
		}
		history, _, err := collect(t.Context(), c.store, wish.GetId(), allEvents)
		if err != nil || len(history.GetCommands()) != 1 || history.GetCommands()[0].GetId() != wish.GetId() {
			t.Fatalf("creation was not tied exactly to its command: %v, %v", history, err)
		}
		if wish.GetTitle() != locales.T("fr", "wish.provisional_title", nil) || Active(wish) == paused {
			t.Fatalf("prompt-only wish: %v", wish)
		}
		blocks, err := store.List[*planv1.Block](t.Context(), c.store, store.Where{"wish_id": wish.GetId()})
		if err != nil || len(blocks) != 1 {
			t.Fatalf("creation blocks: %v, %v", blocks, err)
		}
		b := blocks[0]
		if b.GetContent() != prompt || b.GetKind() != creationKind || b.GetMediaType() != "text/markdown" ||
			b.GetTitle() != locales.T("fr", "wish.original_request", nil) {
			t.Fatalf("creation block changed the original request: %v", b)
		}
	}
	res, err := c.wishes.Make(t.Context(), connect.NewRequest(&planv1.WishServiceMakeRequest{
		Title: "An explicit title", Prompt: prompt, Paused: true,
	}))
	if err != nil || res.Msg.GetWish().GetTitle() != "An explicit title" {
		t.Fatalf("explicit title with prompt: %v, %v", res, err)
	}
	brief, err := BuildBrief(t.Context(), c.store, "", res.Msg.GetWish().GetId())
	if err != nil || strings.Contains(brief.Moving, "First, write a concise title") {
		t.Fatalf("explicit title was made provisional: %v", err)
	}
	legacy, err := c.make(t, "Title only", true)
	if err != nil || legacy.GetTitle() != "Title only" {
		t.Fatalf("title-only creation: %v, %v", legacy, err)
	}
	blocks, err := store.List[*planv1.Block](t.Context(), c.store, store.Where{"wish_id": legacy.GetId()})
	if err != nil || len(blocks) != 0 {
		t.Fatalf("title-only wish got a creation block: %v, %v", blocks, err)
	}
}

func TestPromptCreationJournalExact(t *testing.T) {
	c := serve(t)
	var wishes []*planv1.Wish
	for _, prompt := range []string{"First request", "Second request", "Third request"} {
		res, err := c.wishes.Make(t.Context(), connect.NewRequest(&planv1.WishServiceMakeRequest{Prompt: prompt, Paused: true}))
		if err != nil {
			t.Fatal(err)
		}
		wishes = append(wishes, res.Msg.GetWish())
	}
	// Give all three the same time, deliberately outside the legacy five-second window. The common provisional
	// title and timestamp must have no say in which request is exported or supplies the lead's title instruction.
	collidingTime := timestamppb.New(wishes[0].GetCreateTime().AsTime().Add(time.Hour))
	for i, w := range wishes {
		w.CreateTime = collidingTime
		c.put(t, w)
		exp, _, err := collect(t.Context(), c.store, w.GetId(), allEvents)
		if err != nil || len(exp.GetCommands()) != 1 || exp.GetCommands()[0].GetId() != w.GetId() {
			t.Fatalf("wish %d used a neighboring creation command: %v, %v", i, exp, err)
		}
		var made planv1.WishServiceMakeRequest
		if err := exp.GetCommands()[0].GetRequest().UnmarshalTo(&made); err != nil || made.GetPrompt() != exp.GetBlocks()[0].GetContent() {
			t.Fatalf("wish %d exported a different original request: %v, %v", i, &made, err)
		}
		if !needsTitle(exp) {
			t.Fatalf("wish %d lost title authorship instruction", i)
		}
	}
}

// A wish made from a request queues an event for the one lead: name it when its title is provisional, then take it
// on. A wish made from a title alone, or routed from another wish, tells the lead nothing new.
func TestMadeFromRequestTellsTheLead(t *testing.T) {
	c := serve(t)
	res, err := c.wishes.Make(t.Context(), connect.NewRequest(&planv1.WishServiceMakeRequest{Prompt: "Ship the export", Paused: true}))
	if err != nil {
		t.Fatal(err)
	}
	provisional := res.Msg.GetWish()
	res, err = c.wishes.Make(t.Context(), connect.NewRequest(&planv1.WishServiceMakeRequest{
		Title: "Ship the export", Prompt: "Ship the export, in CSV", Paused: true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	titled := res.Msg.GetWish()
	if _, err := c.make(t, "Title only", true); err != nil {
		t.Fatal(err)
	}
	want := []string{LeadTerminal + ": " + MadeLine(provisional, true), LeadTerminal + ": " + MadeLine(titled, false)}
	if got := told(t, c.store); !slices.Equal(got, want) {
		t.Fatalf("lead told %q, want %q", got, want)
	}
	events, err := waitingEvents(t.Context(), c.store)
	if err != nil || events[0].GetKind() != planv1.LeadEventKind_LEAD_EVENT_KIND_REQUEST || events[0].GetWishId() != provisional.GetId() {
		t.Fatalf("events %v, %v", events, err)
	}
	// The digest cuts a line at digestLineMax, "Djinn: " left out: the command must fit.
	if line := MadeLine(provisional, true); !strings.Contains(line, "djinn wish rename "+provisional.GetId()) ||
		len(strings.TrimPrefix(line, "Djinn: ")) > digestLineMax {
		t.Errorf("the line %q does not name the wish within a digest's line", line)
	}
	if line := MadeLine(titled, false); strings.Contains(line, "rename") || !strings.Contains(line, "djinn wish brief "+titled.GetId()) {
		t.Errorf("a titled wish: %q", line)
	}
}

// A wish made before its identifier was its creation's keeps its own creation after a rename, never the creation of
// a wish made just after it under its new title.
func TestLegacyRenameKeepsItsOwnCreation(t *testing.T) {
	c := serve(t)
	legacy := &planv1.Wish{Id: store.NewID(), Title: "Original title", State: planv1.WishState_WISH_STATE_PAUSED}
	if err := c.store.Tx(t.Context(), func(tx *store.Tx) error {
		if err := tx.Journal(actor, planv1connect.WishServiceMakeProcedure, &planv1.WishServiceMakeRequest{
			Title: legacy.GetTitle(), Paused: true,
		}); err != nil {
			return err
		}
		legacy.CreateTime = timestamppb.Now()
		return tx.Put(legacy)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.make(t, "New title", true); err != nil {
		t.Fatal(err)
	}
	if _, err := c.wishes.Rename(t.Context(), connect.NewRequest(&planv1.WishServiceRenameRequest{
		WishId: legacy.GetId(), Title: "New title",
	})); err != nil {
		t.Fatal(err)
	}
	exp, _, err := collect(t.Context(), c.store, legacy.GetId(), allEvents)
	if err != nil || len(exp.GetCommands()) != 2 {
		t.Fatalf("commands %v, %v", exp.GetCommands(), err)
	}
	var made planv1.WishServiceMakeRequest
	if err := exp.GetCommands()[0].GetRequest().UnmarshalTo(&made); err != nil || made.GetTitle() != "Original title" ||
		exp.GetCommands()[1].GetMethod() != planv1connect.WishServiceRenameProcedure {
		t.Fatalf("the renamed wish's history: %v, %v", exp.GetCommands(), err)
	}
}

func TestMakePromptValidationAndRollback(t *testing.T) {
	c := serve(t)
	for _, m := range []*planv1.WishServiceMakeRequest{
		{}, {Prompt: " \n\t "}, {Title: "  "}, {Prompt: "\u00a0\u2003\u0085"}, {Title: strings.Repeat("x", 501)},
		{Prompt: strings.Repeat("x", 1000001)},
	} {
		if _, err := c.wishes.Make(t.Context(), connect.NewRequest(m)); code(err) != connect.CodeInvalidArgument {
			t.Errorf("invalid request accepted (title %d, prompt %d): %v", len(m.GetTitle()), len(m.GetPrompt()), err)
		}
	}
	for range MaxActive {
		if _, err := c.make(t, "Existing", false); err != nil {
			t.Fatal(err)
		}
	}
	before, err := store.Commands(t.Context(), c.store, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []*planv1.WishServiceMakeRequest{
		{Prompt: "Unknown project", ProjectIds: []string{store.NewID()}},
		{Prompt: "Unknown project", ProjectIds: []string{store.NewID()}, Paused: true},
	} {
		if _, err := c.wishes.Make(t.Context(), connect.NewRequest(m)); err == nil {
			t.Fatal("refused creation succeeded")
		}
	}
	blocks, err := store.List[*planv1.Block](t.Context(), c.store, nil)
	if err != nil || len(blocks) != 0 || len(c.list(t)) != MaxActive {
		t.Fatalf("failed creation left entities: %v, %v", blocks, err)
	}
	after, err := store.Commands(t.Context(), c.store, nil)
	if err != nil || len(before) != len(after) {
		t.Fatalf("failed creation left a journal entry: %d → %d, %v", len(before), len(after), err)
	}
}

func TestBriefOriginalPromptWholeAndPortable(t *testing.T) {
	c := serve(t)
	repo, home := t.TempDir(), t.TempDir()
	p, err := c.projects.Add(t.Context(), connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: repo, Name: "api"}))
	if err != nil {
		t.Fatal(err)
	}
	userHome, _ := os.UserHomeDir()
	prompt := "# Original\n\n" + strings.Repeat("- Keep **all** requirements.\n", 80) +
		"In " + repo + "/internal, " + home + "/state and " + userHome +
		"/notes.\nRemote: https://user:secret@example.com/api.git\nFINAL REQUIREMENT\n"
	res, err := c.wishes.Make(t.Context(), connect.NewRequest(&planv1.WishServiceMakeRequest{
		Prompt: prompt, ProjectIds: []string{p.Msg.GetProject().GetId()}, Paused: true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	wishID := res.Msg.GetWish().GetId()
	// Both a duplicate creation block and a regular block repeating the prompt must appear only once.
	for _, kind := range []string{creationKind, "section"} {
		if _, err := c.blocks.Put(t.Context(), connect.NewRequest(&planv1.BlockServicePutRequest{
			WishId: wishID, Kind: kind, Content: prompt,
		})); err != nil {
			t.Fatal(err)
		}
	}
	for range briefBlocks + 3 {
		if _, err := c.blocks.Put(t.Context(), connect.NewRequest(&planv1.BlockServicePutRequest{
			WishId: wishID, Kind: "log", Content: "Recent block",
		})); err != nil {
			t.Fatal(err)
		}
	}
	brief, err := BuildBrief(t.Context(), c.store, home, wishID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(brief.Moving, "FINAL REQUIREMENT") != 1 ||
		strings.Count(brief.Moving, "- Keep **all** requirements.") != 80 ||
		!strings.Contains(brief.Moving, "First, write a concise title") ||
		!strings.Contains(brief.Moving, "api/internal") ||
		!strings.Contains(brief.Moving, "https://example.com/api.git") {
		t.Fatalf("original prompt clipped, duplicated, or missing title instruction: %s", brief.Moving)
	}
	for _, never := range []string{repo, home, userHome, "user:secret"} {
		if never != "" && strings.Contains(brief.Text(), never) {
			t.Errorf("brief leaks %q", never)
		}
	}
}

// promptDiskClient uses a real Connect server over a durable store, so closing and reopening it exercises the
// same journal and creation-block transaction that a paused wish keeps across a Djinn restart.
func promptDiskClient(t *testing.T, file string) clients {
	t.Helper()
	s, err := store.Open(t.Context(), file, Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	mux := http.NewServeMux()
	for prefix, h := range Handlers(s) {
		mux.Handle(prefix, h)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return clients{store: s, wishes: planv1connect.NewWishServiceClient(srv.Client(), srv.URL)}
}

func TestPromptRestartExportImport(t *testing.T) {
	file := filepath.Join(t.TempDir(), store.File)
	c := promptDiskClient(t, file)
	prompt := "# Original\n\n" + strings.Repeat("**Requirement**\n\n", 100) + "FINAL DETAIL\n"
	res, err := c.wishes.Make(t.Context(), connect.NewRequest(&planv1.WishServiceMakeRequest{Prompt: prompt, Paused: true}))
	if err != nil {
		t.Fatal(err)
	}
	wishID := res.Msg.GetWish().GetId()
	if err := c.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := promptDiskClient(t, file)
	brief, err := BuildBrief(t.Context(), reopened.store, "", wishID)
	if err != nil || !strings.Contains(brief.Moving, prompt) || !strings.Contains(brief.Moving, "First, write a concise title") {
		t.Fatalf("restart lost the original request: %v", err)
	}
	for _, json := range []bool{false, true} {
		ext := ".djinn"
		if json {
			ext = ".json"
		}
		exportFile := filepath.Join(t.TempDir(), "wish"+ext)
		if _, err := reopened.wishes.Export(t.Context(), connect.NewRequest(&planv1.WishServiceExportRequest{
			WishId: wishID, File: exportFile,
		})); err != nil {
			t.Fatal(err)
		}
		dst := serve(t)
		if _, err := dst.wishes.Import(t.Context(), connect.NewRequest(&planv1.WishServiceImportRequest{File: exportFile})); err != nil {
			t.Fatal(err)
		}
		imported, _, err := collect(t.Context(), dst.store, wishID, allEvents)
		if err != nil || len(imported.GetBlocks()) != 1 || imported.GetBlocks()[0].GetContent() != prompt ||
			imported.GetWish().GetState() != planv1.WishState_WISH_STATE_PAUSED {
			t.Fatalf("import lost paused wish or full prompt: %v, %v", imported, err)
		}
		brief, err := BuildBrief(t.Context(), dst.store, "", wishID)
		if err != nil || !strings.Contains(brief.Moving, prompt) || !strings.Contains(brief.Moving, "First, write a concise title") {
			t.Fatalf("imported brief lost prompt or title instruction: %v", err)
		}
		if _, err := dst.wishes.Rename(t.Context(), connect.NewRequest(&planv1.WishServiceRenameRequest{
			WishId: wishID, Title: "The lead's title",
		})); err != nil {
			t.Fatal(err)
		}
		carried, _, err := collect(t.Context(), dst.store, wishID, allEvents)
		if err != nil || len(carried.GetCommands()) != 2 || carried.GetBlocks()[0].GetContent() != prompt {
			t.Fatalf("renaming imported wish lost history: %v, %v", carried, err)
		}
	}
}

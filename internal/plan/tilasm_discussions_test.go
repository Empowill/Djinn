package plan

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

const discussed = "# Brief\n\nWe cache it. Then we **cache it** again.\n\n- one store\n- two stores\n\nThe end.\n"

// newDocument makes a Markdown tilasm of the wish from its text, and returns its identifier.
func newDocument(t *testing.T, c clients, wish, text string) *planv1.Tilasm {
	t.Helper()
	res, err := c.tilasms.Create(t.Context(), connect.NewRequest(&planv1.TilasmServiceCreateRequest{
		Wish: wish, Title: "Brief", Markdown: text, Author: "developer",
	}))
	if err != nil {
		t.Fatal(err)
	}
	return res.Msg.GetTilasm()
}

func saveDocument(t *testing.T, c clients, tilasm *planv1.Tilasm, text string) *planv1.Tilasm {
	t.Helper()
	current := getTilasm(t, c, tilasm.GetCode(), 0).GetTilasm()
	res, err := c.tilasms.Save(t.Context(), connect.NewRequest(&planv1.TilasmServiceSaveRequest{
		Id: tilasm.GetId(), BaseRevision: current.GetRevision(), Markdown: text,
	}))
	if err != nil {
		t.Fatal(err)
	}
	return res.Msg.GetTilasm()
}

func discussions(t *testing.T, c clients, code string) *planv1.TilasmServiceDiscussionsResponse {
	t.Helper()
	r := ref(code)
	if uuidLike(code) {
		r = &planv1.TilasmRef{Ref: &planv1.TilasmRef_Id{Id: code}}
	}
	res, err := c.tilasms.Discussions(t.Context(), connect.NewRequest(&planv1.TilasmServiceDiscussionsRequest{Tilasm: r}))
	if err != nil {
		t.Fatal(err)
	}
	return res.Msg
}

// TestComment: a discussion names its passage by its quote, its context filled by Djinn; a quote found twice is
// refused until a prefix or a suffix says which; a request sent again opens nothing more; the passage stays anchored
// through edits elsewhere, and says when it changed or went.
func TestComment(t *testing.T) {
	ctx := t.Context()
	c := serve(t, WithHome(t.TempDir()))
	wish := c.wish(t)
	doc := newDocument(t, c, wish, discussed)

	comment := func(req *planv1.TilasmServiceCommentRequest) (*planv1.TilasmThread, error) {
		req.Tilasm = ref("L01")
		res, err := c.tilasms.Comment(ctx, connect.NewRequest(req))
		if err != nil {
			return nil, err
		}
		return res.Msg.GetThread(), nil
	}
	_, err := comment(&planv1.TilasmServiceCommentRequest{Quote: "cache it", Text: "Why?"})
	if code(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "ambiguous") ||
		!strings.Contains(err.Error(), "--prefix") {
		t.Errorf("a quote found twice: %v, want failed_precondition naming --prefix", err)
	}
	if _, err := comment(&planv1.TilasmServiceCommentRequest{Quote: "three stores", Text: "?"}); code(err) != connect.CodeFailedPrecondition ||
		!strings.Contains(err.Error(), "missing") {
		t.Errorf("a quote not found: %v, want failed_precondition, missing", err)
	}
	if _, err := comment(&planv1.TilasmServiceCommentRequest{Text: "?"}); code(err) != connect.CodeInvalidArgument {
		t.Errorf("no passage: %v, want invalid_argument", err)
	}

	second, err := comment(&planv1.TilasmServiceCommentRequest{Quote: "cache it", Prefix: "Then we ", Text: "Why again?", RequestId: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	if a := second.GetAnchor(); a.GetQuote() != "cache it" || !strings.HasSuffix(a.GetPrefix(), "Then we ") ||
		!strings.HasPrefix(a.GetSuffix(), " again.") || a.GetRevision() != doc.GetRevision() {
		t.Errorf("anchor = %v, want its context filled", a)
	}
	if second.GetAnchorState() != anchored || second.GetCurrent() != "cache it" || len(second.GetMessages()) != 1 ||
		second.GetMessages()[0].GetAuthor() != "lead" {
		t.Errorf("thread = %v", second)
	}
	again, err := comment(&planv1.TilasmServiceCommentRequest{Quote: "cache it", Prefix: "Then we ", Text: "Why again?", RequestId: "r1"})
	if err != nil || again.GetId() != second.GetId() {
		t.Errorf("the same request again = %v, %v; want the same discussion", again, err)
	}
	first, err := comment(&planv1.TilasmServiceCommentRequest{Quote: "We cache it", Text: "Fine.", Author: "developer"})
	if err != nil {
		t.Fatal(err)
	}

	reply := func(rid string) *planv1.TilasmThread {
		res, err := c.tilasms.Reply(ctx, connect.NewRequest(&planv1.TilasmServiceReplyRequest{
			Thread: second.GetId(), Text: "Because.", Author: "W3", RequestId: rid,
		}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg.GetThread()
	}
	reply("r2")
	if got := reply("r2"); len(got.GetMessages()) != 2 || got.GetMessages()[1].GetAuthor() != "W3" {
		t.Errorf("a reply sent twice = %v, want one", got.GetMessages())
	}

	got := discussions(t, c, "L01")
	if len(got.GetThreads()) != 2 || got.GetThreads()[0].GetId() != first.GetId() || got.GetThreads()[1].GetId() != second.GetId() {
		t.Errorf("discussions = %v, want them in the document's order", got.GetThreads())
	}

	// An edit just before the passages, and an identical sentence added after them: still there, still apart.
	saveDocument(t, c, doc, strings.Replace(discussed, "We cache", "Intro. We cache", 1)+"\nThen we cache it later.\n")
	if s := discussions(t, c, "L01").GetThreads(); s[0].GetAnchorState() != anchored || s[1].GetAnchorState() != anchored {
		t.Errorf("after edits elsewhere: %v, %v", s[0].GetAnchorState(), s[1].GetAnchorState())
	}
	saveDocument(t, c, doc, strings.Replace(discussed, "We cache", "Intro. We cache", 1)+"\nThen we cache it later.\n")
	saveDocument(t, c, doc, strings.Replace(strings.Replace(discussed, "We cache", "Intro. We cache", 1), "**cache it**", "**store it**", 1)+
		"\nThen we cache it later.\n")
	s := discussions(t, c, "L01").GetThreads()
	if s[1].GetAnchorState() != changed || s[1].GetCurrent() != "store it" {
		t.Errorf("after an edit inside: %v %q, want changed", s[1].GetAnchorState(), s[1].GetCurrent())
	}
	saveDocument(t, c, doc, "# Brief\n\nIntro.\n")
	s = discussions(t, c, "L01").GetThreads()
	if s[0].GetAnchorState() != missing || s[1].GetAnchorState() != missing || len(s[0].GetMessages())+len(s[1].GetMessages()) != 3 {
		t.Errorf("after the passages went: %v; want them missing, kept", s)
	}

	res, err := c.tilasms.Resolve(ctx, connect.NewRequest(&planv1.TilasmServiceResolveRequest{Thread: second.GetId(), Resolved: true}))
	if err != nil || !res.Msg.GetThread().GetResolved() {
		t.Errorf("resolve = %v, %v", res, err)
	}
	if got := getTilasm(t, c, "L01", 0).GetTilasm().GetRevision(); got != 5 {
		t.Errorf("revision %d: a discussion changed the document", got)
	}
}

// TestProposals: a proposal leaves the document as it is until the developer accepts it; Djinn then writes it as a
// new revision by its author, and accepting it again changes nothing. One whose passage changed is refused, and a
// rejection never touches the document.
func TestProposals(t *testing.T) {
	ctx := t.Context()
	clock := &fakeClock{now: time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)}
	c := serve(t, WithHome(t.TempDir()), WithClock(clock.Now))
	wish := c.wish(t)
	doc := newDocument(t, c, wish, discussed)
	propose := func(req *planv1.TilasmServiceProposeRequest) (*planv1.TilasmProposal, error) {
		req.Tilasm = ref("L01")
		res, err := c.tilasms.Propose(ctx, connect.NewRequest(req))
		if err != nil {
			return nil, err
		}
		return res.Msg.GetProposal(), nil
	}
	accept := func(id string) (*planv1.TilasmServiceAcceptResponse, error) {
		res, err := c.tilasms.Accept(ctx, connect.NewRequest(&planv1.TilasmServiceAcceptRequest{Proposal: id}))
		if err != nil {
			return nil, err
		}
		return res.Msg, nil
	}
	text := func() string { return getTilasm(t, c, "L01", 0).GetText() }

	if _, err := propose(&planv1.TilasmServiceProposeRequest{Quote: "two stores"}); code(err) != connect.CodeInvalidArgument {
		t.Errorf("no replacement: %v, want invalid_argument", err)
	}
	thread, err := c.tilasms.Comment(ctx, connect.NewRequest(&planv1.TilasmServiceCommentRequest{
		Tilasm: ref("L01"), Quote: "cache it", Suffix: " again", Text: "Say store.", Author: "developer",
	}))
	if err != nil {
		t.Fatal(err)
	}
	clock.add(time.Hour)
	p, err := propose(&planv1.TilasmServiceProposeRequest{
		Thread: thread.Msg.GetThread().GetId(), Replacement: "store it", Note: "As asked.", RequestId: "p1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.GetStatus() != planv1.TilasmProposalStatus_TILASM_PROPOSAL_STATUS_PENDING || p.GetOriginal() != "cache it" ||
		p.GetAnchorState() != anchored || p.GetAuthor() != "lead" || p.GetBaseRevision() != 1 ||
		p.GetThreadId() != thread.Msg.GetThread().GetId() {
		t.Errorf("proposal = %v", p)
	}
	if same, err := propose(&planv1.TilasmServiceProposeRequest{Quote: "two stores", Replacement: "x", RequestId: "p1"}); err != nil ||
		same.GetId() != p.GetId() {
		t.Errorf("the same request again = %v, %v; want the same proposal", same, err)
	}
	if text() != discussed {
		t.Error("a proposal changed the document")
	}

	res, err := accept(p.GetId())
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(discussed, "**cache it**", "**store it**", 1)
	if text() != want {
		t.Errorf("after accept:\n%s\nwant:\n%s", text(), want)
	}
	tilasm := res.GetTilasm()
	last := tilasm.GetVersions()[len(tilasm.GetVersions())-1]
	if tilasm.GetRevision() != 2 || last.GetAuthor() != "lead" || last.GetOrigin() != planv1.TilasmOrigin_TILASM_ORIGIN_PROPOSAL {
		t.Errorf("tilasm after accept = %v", tilasm)
	}
	if a := res.GetProposal(); a.GetStatus() != planv1.TilasmProposalStatus_TILASM_PROPOSAL_STATUS_ACCEPTED ||
		a.GetDecidedBy() != "developer" || a.GetAcceptedRevision() != 2 || a.GetDecideTime() == nil {
		t.Errorf("accepted = %v", a)
	}
	if again, err := accept(p.GetId()); err != nil || again.GetTilasm().GetRevision() != 2 || text() != want {
		t.Errorf("accept again = %v, %v; want it unchanged", again, err)
	}
	if _, err := c.tilasms.Reject(ctx, connect.NewRequest(&planv1.TilasmServiceRejectRequest{Proposal: p.GetId()})); code(err) != connect.CodeFailedPrecondition {
		t.Errorf("reject an accepted proposal: %v, want failed_precondition", err)
	}

	// The developer edits the passage after a proposal: accepting it is refused, and says why.
	stale, err := propose(&planv1.TilasmServiceProposeRequest{Quote: "two stores", Replacement: "three stores"})
	if err != nil {
		t.Fatal(err)
	}
	saveDocument(t, c, doc, strings.Replace(want, "two stores", "four stores", 1))
	if _, err := accept(stale.GetId()); code(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "changed") {
		t.Errorf("accept a changed passage: %v, want failed_precondition, changed", err)
	}
	if got := discussions(t, c, "L01").GetProposals(); len(got) != 2 || got[1].GetAnchorState() != changed || got[1].GetOriginal() != "four stores" {
		t.Errorf("proposals = %v", got)
	}
	rejected, err := c.tilasms.Reject(ctx, connect.NewRequest(&planv1.TilasmServiceRejectRequest{Proposal: stale.GetId()}))
	if err != nil || rejected.Msg.GetProposal().GetStatus() != planv1.TilasmProposalStatus_TILASM_PROPOSAL_STATUS_REJECTED {
		t.Errorf("reject = %v, %v", rejected, err)
	}
	if _, err := accept(stale.GetId()); code(err) != connect.CodeFailedPrecondition {
		t.Errorf("accept a rejected proposal: %v, want failed_precondition", err)
	}

	// An insertion at a place, and a deletion.
	before := text()
	insert, err := propose(&planv1.TilasmServiceProposeRequest{Prefix: "four stores ", Suffix: "The end.", Replacement: "A new paragraph.\n\n"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := accept(insert.GetId()); err != nil {
		t.Fatal(err)
	}
	if got := text(); got != strings.Replace(before, "The end.", "A new paragraph.\n\nThe end.", 1) {
		t.Errorf("after an insertion:\n%s", got)
	}
	if _, err := propose(&planv1.TilasmServiceProposeRequest{Prefix: "Brief ", Delete: true}); code(err) != connect.CodeInvalidArgument {
		t.Errorf("delete at a place: %v, want invalid_argument", err)
	}
	del, err := propose(&planv1.TilasmServiceProposeRequest{Quote: "A new paragraph.", Delete: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := accept(del.GetId()); err != nil {
		t.Fatal(err)
	}
	if got := text(); !strings.Contains(got, "four stores\n\n\n\nThe end.") {
		t.Errorf("after a deletion:\n%s", got)
	}

	// A replacement read from a file.
	file := filepath.Join(t.TempDir(), "x.md")
	if err := os.WriteFile(file, []byte("**The** end."), 0o600); err != nil {
		t.Fatal(err)
	}
	fromFile, err := propose(&planv1.TilasmServiceProposeRequest{Quote: "The end.", ReplacementFile: file})
	if err != nil || fromFile.GetReplacement() != "**The** end." {
		t.Errorf("a replacement file = %v, %v", fromFile, err)
	}

	html := putTilasm(t, c, &planv1.TilasmServicePutRequest{
		Path: folder(t, map[string]string{"index.html": page("Page", "Text.")}), Wish: wish,
	}).GetTilasm()
	if _, err := c.tilasms.Propose(ctx, connect.NewRequest(&planv1.TilasmServiceProposeRequest{
		Tilasm: ref(html.GetCode()), Quote: "Text.", Replacement: "x",
	})); code(err) != connect.CodeFailedPrecondition {
		t.Errorf("a proposal on an HTML page: %v, want failed_precondition", err)
	}
}

// TestAgentsNeverAccept: accepting, rejecting and resolving are the window's; commenting, replying, proposing and
// reading are commands.
func TestAgentsNeverAccept(t *testing.T) {
	sd := planv1.File_plan_v1_tilasm_proto.Services().ByName("TilasmService")
	for method, want := range map[string]djinnv1.Visibility{
		"Accept": djinnv1.Visibility_VISIBILITY_INTERNAL, "Reject": djinnv1.Visibility_VISIBILITY_INTERNAL,
		"Resolve": djinnv1.Visibility_VISIBILITY_INTERNAL, "Comment": djinnv1.Visibility_VISIBILITY_PUBLIC,
		"Reply": djinnv1.Visibility_VISIBILITY_PUBLIC, "Propose": djinnv1.Visibility_VISIBILITY_PUBLIC,
		"Discussions": djinnv1.Visibility_VISIBILITY_PUBLIC, "Context": djinnv1.Visibility_VISIBILITY_PUBLIC,
	} {
		md := sd.Methods().ByName(protoreflect.Name(method))
		if got := proto.GetExtension(md.Options(), djinnv1.E_Visibility); got != want {
			t.Errorf("%s is %v, want %v", method, got, want)
		}
	}
}

// TestContext: an agent reads the passage, its source, its paragraphs and its discussion with the whole document; a
// document too large comes as a window around the passage, saying so.
func TestContext(t *testing.T) {
	ctx := t.Context()
	c := serve(t, WithHome(t.TempDir()))
	wish := c.wish(t)
	newDocument(t, c, wish, discussed)
	thread, err := c.tilasms.Comment(ctx, connect.NewRequest(&planv1.TilasmServiceCommentRequest{
		Tilasm: ref("L01"), Quote: "two stores", Text: "Which?", Author: "developer",
	}))
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.tilasms.Context(ctx, connect.NewRequest(&planv1.TilasmServiceContextRequest{
		Tilasm: ref("L01"), Thread: thread.Msg.GetThread().GetId(),
	}))
	if err != nil {
		t.Fatal(err)
	}
	got := res.Msg
	if got.GetMarkdown() != discussed || got.GetTruncated() || got.GetOriginal() != "two stores" ||
		got.GetParagraphs() != "two stores" || got.GetAnchorState() != anchored ||
		len(got.GetThread().GetMessages()) != 1 || got.GetRevision() != 1 || got.GetCode() != "L01" {
		t.Errorf("context = %v", got)
	}

	large := strings.Repeat("Filler line of the document.\n\n", 20000) + "The **target** here.\n\n" +
		strings.Repeat("More filler after it.\n\n", 20000)
	big := newDocument(t, c, wish, large)
	res, err = c.tilasms.Context(ctx, connect.NewRequest(&planv1.TilasmServiceContextRequest{
		Tilasm: ref(big.GetCode()), Quote: "target here",
	}))
	if err != nil {
		t.Fatal(err)
	}
	got = res.Msg
	if !got.GetTruncated() || got.GetTotalBytes() != int64(len(large)) || len(got.GetMarkdown()) > contextBytes ||
		!strings.Contains(got.GetMarkdown(), "The **target** here.") || got.GetOriginal() != "target** here" ||
		large[got.GetWindowStart():int(got.GetWindowStart())+len(got.GetMarkdown())] != got.GetMarkdown() {
		t.Errorf("context of a large document: truncated %v, %d bytes from %d, original %q", got.GetTruncated(),
			len(got.GetMarkdown()), got.GetWindowStart(), got.GetOriginal())
	}
}

// TestDiscussionsTravel: a wish's export carries its tilasms' discussions and proposals, a tilasm's too; an import
// puts them back, and deleting the wish deletes them.
func TestDiscussionsTravel(t *testing.T) {
	ctx := t.Context()
	c := serve(t, WithHome(t.TempDir()))
	wish := c.wish(t)
	first := newDocument(t, c, wish, discussed)
	thread, err := c.tilasms.Comment(ctx, connect.NewRequest(&planv1.TilasmServiceCommentRequest{
		Tilasm: ref("L01"), Quote: "two stores", Text: "Which?", Author: "developer",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.tilasms.Propose(ctx, connect.NewRequest(&planv1.TilasmServiceProposeRequest{
		Tilasm: ref("L01"), Thread: thread.Msg.GetThread().GetId(), Replacement: "one store",
	})); err != nil {
		t.Fatal(err)
	}

	file := filepath.Join(t.TempDir(), "wish.djinn")
	export(t, c, wish, file)
	dst := serve(t, WithHome(t.TempDir()))
	if _, err := dst.wishes.Import(ctx, connect.NewRequest(&planv1.WishServiceImportRequest{File: file})); err != nil {
		t.Fatal(err)
	}
	got := discussions(t, dst, getTilasm(t, dst, "L01", 0).GetTilasm().GetId())
	if len(got.GetThreads()) != 1 || len(got.GetProposals()) != 1 || got.GetThreads()[0].GetAnchorState() != anchored ||
		got.GetProposals()[0].GetThreadId() != got.GetThreads()[0].GetId() {
		t.Errorf("after a wish's import: %v", got)
	}
	if _, err := dst.wishes.Import(ctx, connect.NewRequest(&planv1.WishServiceImportRequest{File: file, Replace: true})); err != nil {
		t.Fatalf("import again, replacing: %v", err)
	}

	zip := filepath.Join(t.TempDir(), "l01.zip")
	if _, err := c.tilasms.Export(ctx, connect.NewRequest(&planv1.TilasmServiceExportRequest{Tilasm: ref("L01"), File: zip})); err != nil {
		t.Fatal(err)
	}
	other := c.wish(t)
	imported, err := c.tilasms.Import(ctx, connect.NewRequest(&planv1.TilasmServiceImportRequest{File: zip, Wish: other}))
	if err != nil {
		t.Fatal(err)
	}
	copied := discussions(t, c, imported.Msg.GetTilasm().GetId())
	if len(copied.GetThreads()) != 1 || copied.GetThreads()[0].GetId() == thread.Msg.GetThread().GetId() ||
		copied.GetProposals()[0].GetThreadId() != copied.GetThreads()[0].GetId() {
		t.Errorf("a tilasm imported into another wish: %v; want its discussions under identifiers of their own", copied)
	}
	if _, err := c.tilasms.Import(ctx, connect.NewRequest(&planv1.TilasmServiceImportRequest{File: zip, Wish: wish})); err != nil {
		t.Fatal(err)
	}
	if again := discussions(t, c, first.GetId()); len(again.GetThreads()) != 1 || len(again.GetProposals()) != 1 {
		t.Errorf("a tilasm imported into its own wish: %v; want its discussions once", again)
	}

	if _, err := dst.wishes.Delete(ctx, connect.NewRequest(&planv1.WishServiceDeleteRequest{WishId: wish})); err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{count[*planv1.TilasmThread](t, dst.store), count[*planv1.TilasmProposal](t, dst.store)} {
		if n != 0 {
			t.Errorf("%d discussions or proposals stay after their wish's deletion", n)
		}
	}
}

func count[T proto.Message](t *testing.T, s *store.Store) int {
	t.Helper()
	all, err := store.List[T](t.Context(), s, nil)
	if err != nil {
		t.Fatal(err)
	}
	return len(all)
}

// TestBriefDiscussions: the brief says what of a support's discussions waits, and tells agents to answer in the
// thread and to propose rather than rewrite, never to accept.
func TestBriefDiscussions(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	c := serve(t, WithHome(home))
	wish := c.wish(t)
	newDocument(t, c, wish, discussed)
	if _, err := c.tilasms.Comment(ctx, connect.NewRequest(&planv1.TilasmServiceCommentRequest{
		Tilasm: ref("L01"), Quote: "two stores", Text: "Which?", Author: "developer",
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := c.tilasms.Propose(ctx, connect.NewRequest(&planv1.TilasmServiceProposeRequest{
		Tilasm: ref("L01"), Quote: "one store", Replacement: "a store",
	})); err != nil {
		t.Fatal(err)
	}
	brief, err := BuildBrief(ctx, c.store, home, wish)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(brief.Moving, "; 1 open discussion, 1 proposal waiting for the developer\n") {
		t.Errorf("the brief's tilasms:\n%s", brief.Moving)
	}
	for _, want := range []string{"`djinn doc discussions L01`", "`djinn doc reply <thread> --text", "propose rather than rewrite",
		"you never accept a proposal", "`djinn doc context <code> --thread <thread>`", "--request-id"} {
		if !strings.Contains(brief.Stable, want) {
			t.Errorf("the rules lack %q", want)
		}
	}
}

// TestDiscussedByDeveloper: only a message the developer commits calls the Discussed hooks, once, after the write: a
// request sent again, or a message by the lead, the document agent or a worker, calls none. A comment is a comment
// unless it says it is a request.
func TestDiscussedByDeveloper(t *testing.T) {
	ctx := t.Context()
	var got []string
	c := serve(t, WithHome(t.TempDir()), WithDiscussed(func(_ context.Context, thread *planv1.TilasmThread) {
		msgs := thread.GetMessages()
		got = append(got, thread.GetKind().String()+" "+msgs[len(msgs)-1].GetText())
	}))
	wish := c.wish(t)
	newDocument(t, c, wish, discussed)
	comment := func(author, text, request string, kind planv1.TilasmThreadKind) *planv1.TilasmThread {
		t.Helper()
		res, err := c.tilasms.Comment(ctx, connect.NewRequest(&planv1.TilasmServiceCommentRequest{
			Tilasm: ref("L01"), Quote: "The end.", Text: text, Author: author, RequestId: request, Kind: kind,
		}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg.GetThread()
	}
	reply := func(thread, author, text string) {
		t.Helper()
		if _, err := c.tilasms.Reply(ctx, connect.NewRequest(&planv1.TilasmServiceReplyRequest{
			Thread: thread, Author: author, Text: text,
		})); err != nil {
			t.Fatal(err)
		}
	}
	asked := comment("developer", "Say more", "r1", planv1.TilasmThreadKind_TILASM_THREAD_KIND_REQUEST)
	comment("developer", "Say more", "r1", planv1.TilasmThreadKind_TILASM_THREAD_KIND_REQUEST) // Sent again.
	note := comment("", "The lead's note", "", planv1.TilasmThreadKind_TILASM_THREAD_KIND_UNSPECIFIED)
	comment(DocumentAuthor, "The agent's note", "", planv1.TilasmThreadKind_TILASM_THREAD_KIND_COMMENT)
	reply(asked.GetId(), DocumentAuthor, "Proposed.")
	reply(asked.GetId(), "W03", "Seen.")
	reply(note.GetId(), "Developer", "Why?")
	want := []string{"TILASM_THREAD_KIND_REQUEST Say more", "TILASM_THREAD_KIND_COMMENT Why?"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("hooks called with %q, want %q", got, want)
	}
	if note.GetKind() != planv1.TilasmThreadKind_TILASM_THREAD_KIND_COMMENT {
		t.Errorf("a comment that says nothing: kind %v", note.GetKind())
	}
	// No document agent: nothing pending.
	for _, thread := range discussions(t, c, "L01").GetThreads() {
		if thread.GetAgentPending() {
			t.Errorf("discussion %s pending without an agent", thread.GetId())
		}
	}
}

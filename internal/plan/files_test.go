package plan

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// filesLines reads the "Files:" lines of a message to the lead: path, then the rest of the line.
var filesLines = regexp.MustCompile(`(?m)^Files: (\S+) \((.+)\)$`)

// TestLeadTellFiles: the files of a message reach the lead's terminal as a line each naming its path in the wish's
// folder, the file whole there, with a block of kind file on the wish; a 20 MB file passes the server's request
// limit, a larger one is refused before anything is kept.
func TestLeadTellFiles(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	home := t.TempDir()
	leads := &fakeLeads{}
	c := serve(t, WithLeads(leads), WithHome(home))
	if _, err := c.projects.Add(ctx, connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: t.TempDir()})); err != nil {
		t.Fatal(err)
	}
	if _, err := setLead(t, c, &planv1.LeadServiceSetRequest{SessionId: session}); err != nil {
		t.Fatal(err)
	}
	leads.running = map[string][]string{LeadTerminal: {"claude", "--resume", session}}
	id := c.wish(t)

	shot := bytes.Repeat([]byte{0x89, 'P', 'N', 'G'}, 5<<20) // 20 MB: the largest file a message takes.
	log := []byte("line one\nline two\n")
	files := []*planv1.LeadFile{
		{Name: "Screen shot.png", Type: "image/png", Data: shot},
		{Name: "../../notes", Data: log},
	}
	if _, err := c.lead.Tell(ctx, connect.NewRequest(&planv1.LeadServiceTellRequest{
		Text: "See the overlap", WishId: id, Files: files,
	})); err != nil {
		t.Fatalf("tell with a 20 MB file: %v", err)
	}
	if len(leads.said) != 1 {
		t.Fatalf("said %q, want one text", leads.said)
	}
	said := leads.said[0]
	if !strings.HasPrefix(said, LeadTerminal+": About wish Try Djinn on itself ("+id+"): See the overlap\nFiles: ") {
		t.Errorf("said %q: the text first, then the files", said)
	}
	got := filesLines.FindAllStringSubmatch(said, -1)
	if len(got) != 2 {
		t.Fatalf("files lines of %q: %q", said, got)
	}
	folder := filepath.Join(home, "wishes", id, "files")
	for i, want := range []struct {
		suffix, rest string
		data         []byte
	}{
		{"-Screen-shot.png", "image/png, 20.0 MB", shot},
		{"-notes", "text/plain, 18 B", log},
	} {
		path, rest := got[i][1], got[i][2]
		if filepath.Dir(path) != folder || !strings.HasSuffix(path, want.suffix) || rest != want.rest {
			t.Errorf("file %d: %s (%s), want %s/<time>%s (%s)", i, path, rest, folder, want.suffix, want.rest)
		}
		if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, want.data) {
			t.Errorf("file %d kept: %d bytes, %v", i, len(data), err)
		}
	}
	blocks, err := c.blocks.List(ctx, connect.NewRequest(&planv1.BlockServiceListRequest{WishId: id}))
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, b := range blocks.Msg.GetBlocks() {
		if b.GetKind() == fileKind {
			kept = append(kept, b.GetTitle())
			if !strings.Contains(b.GetContent(), folder) {
				t.Errorf("block %q does not name its path: %q", b.GetTitle(), b.GetContent())
			}
		}
	}
	if !slices.Equal(kept, []string{"Screen-shot.png", "notes"}) {
		t.Errorf("file blocks %q", kept)
	}

	// A file alone, about no wish: kept in the lead's folder, no block.
	if _, err := c.lead.Tell(ctx, connect.NewRequest(&planv1.LeadServiceTellRequest{
		Files: []*planv1.LeadFile{{Name: "a.txt", Data: []byte("a")}},
	})); err != nil {
		t.Fatal(err)
	}
	if last := leads.said[len(leads.said)-1]; !strings.HasPrefix(last, LeadTerminal+": Files: "+filepath.Join(home, "lead", "files")) {
		t.Errorf("a file alone: said %q", last)
	}

	before, _ := os.ReadDir(folder)
	big := connect.NewRequest(&planv1.LeadServiceTellRequest{Text: "too big", WishId: id, Files: []*planv1.LeadFile{
		{Name: "big.bin", Data: make([]byte, MaxTellFileBytes+1)},
	}})
	if _, err := c.lead.Tell(ctx, big); code(err) != connect.CodeInvalidArgument {
		t.Errorf("a file of 20 MB and a byte: %v, want invalid", err)
	}
	many := connect.NewRequest(&planv1.LeadServiceTellRequest{Text: "many", WishId: id})
	for range MaxTellFiles + 1 {
		many.Msg.Files = append(many.Msg.Files, &planv1.LeadFile{Name: "x", Data: []byte("x")})
	}
	if _, err := c.lead.Tell(ctx, many); code(err) != connect.CodeInvalidArgument {
		t.Errorf("11 files: %v, want invalid", err)
	}
	if after, _ := os.ReadDir(folder); len(after) != len(before) {
		t.Errorf("a refused message kept files: %d, then %d", len(before), len(after))
	}
}

// TestLeadTellFilesDriven: a driven lead's message event ends with the files' lines too.
func TestLeadTellFilesDriven(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	home := t.TempDir()
	c := serve(t, WithHome(home))
	DriveLead(c.store, func() {})
	t.Cleanup(func() { leadDrives.Delete(c.store) })
	id := c.wish(t)
	if _, err := c.lead.Tell(ctx, connect.NewRequest(&planv1.LeadServiceTellRequest{
		Text: "Look", WishId: id, Files: []*planv1.LeadFile{{Name: "shot.png", Data: []byte("\x89PNG\r\n\x1a\n")}},
	})); err != nil {
		t.Fatal(err)
	}
	events, err := store.List[*planv1.LeadEvent](ctx, c.store, store.Where{"ack_session_id": ""})
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, e := range events {
		if e.GetKind() == planv1.LeadEventKind_LEAD_EVENT_KIND_MESSAGE {
			texts = append(texts, e.GetText())
		}
	}
	want := regexp.MustCompile(`^Look\nFiles: ` + regexp.QuoteMeta(filepath.Join(home, "wishes", id, "files")+string(filepath.Separator)) +
		`\S+-shot\.png \(image/png, 8 B\)$`)
	if len(texts) != 1 || !want.MatchString(texts[0]) {
		t.Errorf("message events %q, want %s", texts, want)
	}
}

func TestSafeFileName(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"image.png": "image.png",
		"Capture d'" + string(rune(0xe9)) + "cran 12:30.png": "Capture-d-cran-12-30.png",
		`C:\Users\me\shot.png`:                               "shot.png",
		"../../etc/passwd":                                   "passwd",
		"":                                                   "file",
		".png":                                               "file.png",
		strings.Repeat("a", 100) + ".x":                      strings.Repeat("a", 78) + ".x",
	} {
		if got := safeFileName(in); got != want {
			t.Errorf("safeFileName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFileDirs(t *testing.T) {
	t.Parallel()
	home := filepath.Join(t.TempDir(), "djinn")
	wish := filepath.Join(home, "wishes", "01a1f1af-7791-713f-a65e-e6af39871369", "files")
	lead := filepath.Join(home, "lead", "files")
	prompt := "Fix the overlap.\nFiles: " + filepath.Join(wish, "2026-11-19-143205-shot.png") + " (image/png, 240 KB)\n" +
		"See also " + filepath.Join(wish, "x.log") + " and " + filepath.Join(lead, "a.txt") + ".\n" +
		"Not " + filepath.Join(home, "wishes", "nope", "files", "y") + " nor " + filepath.Join(home, "settings.json")
	if got := FileDirs(home, prompt); !slices.Equal(got, []string{wish, lead}) {
		t.Errorf("FileDirs = %q, want %q", got, []string{wish, lead})
	}
	if got := FileDirs("", prompt); got != nil {
		t.Errorf("FileDirs without a home = %q", got)
	}
	if got := ReadDirs(home, "Fix it."); got != nil {
		t.Errorf("ReadDirs of a plain prompt = %q", got)
	}
}

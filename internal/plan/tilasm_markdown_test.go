package plan

import (
	"bytes"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/fsx"
	"github.com/empowill/djinn/internal/store"
)

// fakeClock is a clock a test moves.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func TestMarkdownTitle(t *testing.T) {
	for text, want := range map[string]string{
		"# Brief\n\nText.":                      "Brief",
		"Intro\n\n## Part\n\n# The title #\n":   "The title",
		"```\n# not a title\n```\n# After code": "After code",
		"    # indented code\n#No space":        "",
		"":                                      "",
	} {
		if got := markdownTitle(text); got != want {
			t.Errorf("markdownTitle(%q) = %q, want %q", text, got, want)
		}
	}
}

// TestPutMarkdown: a .md file, or a folder with an index.md, is a Markdown tilasm, titled by its first heading, else
// by its name; its text is its Markdown, searched by List; --code puts a new version, and a format change is refused.
func TestPutMarkdown(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	c := serve(t, WithHome(home))
	wish := c.wish(t)
	tasks(t, c, wish, "T07")

	file := filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(file, []byte("# The brief\n\nWe compare **two** stores.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	put := putTilasm(t, c, &planv1.TilasmServicePutRequest{Path: file, Wish: wish, Cites: []string{"T07"}})
	tilasm := put.GetTilasm()
	if tilasm.GetFormat() != planv1.TilasmFormat_TILASM_FORMAT_MARKDOWN || tilasm.GetTitle() != "The brief" ||
		tilasm.GetRevision() != 1 || tilasm.GetVersions()[0].GetOrigin() != planv1.TilasmOrigin_TILASM_ORIGIN_PUT ||
		tilasm.GetVersions()[0].GetRevision() != 1 {
		t.Errorf("put a .md = %v", tilasm)
	}
	if _, err := os.Stat(filepath.Join(put.GetDirectory(), "index.md")); err != nil {
		t.Errorf("the version lacks its index.md: %v", err)
	}
	got := getTilasm(t, c, "L01", 0)
	if got.GetText() != "# The brief\n\nWe compare **two** stores.\n" {
		t.Errorf("Get text = %q, want the Markdown", got.GetText())
	}

	untitled := putTilasm(t, c, &planv1.TilasmServicePutRequest{
		Path: folder(t, map[string]string{"index.md": "No heading here.", "img/a.svg": "<svg/>"}), Wish: wish,
	}).GetTilasm()
	if untitled.GetFormat() != planv1.TilasmFormat_TILASM_FORMAT_MARKDOWN || untitled.GetTitle() == "" ||
		untitled.GetVersions()[0].GetFiles() != 2 {
		t.Errorf("a folder with an index.md = %v", untitled)
	}

	html := putTilasm(t, c, &planv1.TilasmServicePutRequest{
		Path: folder(t, map[string]string{"index.html": page("A page", "Text."), "notes.md": "# Notes"}), Wish: wish,
	}).GetTilasm()
	if html.GetFormat() != planv1.TilasmFormat_TILASM_FORMAT_HTML {
		t.Errorf("a folder with an index.html = %v, want an HTML page", html.GetFormat())
	}

	list, err := c.tilasms.List(ctx, connect.NewRequest(&planv1.TilasmServiceListRequest{Wish: wish, Search: "two stores"}))
	if err != nil {
		t.Fatal(err)
	}
	if ts := list.Msg.GetTilasms(); len(ts) != 1 || ts[0].GetCode() != "L01" || ts[0].GetRevision() != 1 {
		t.Errorf("search the Markdown = %v", ts)
	}

	again := filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(again, []byte("# The brief\n\nWe chose one.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	v2 := putTilasm(t, c, &planv1.TilasmServicePutRequest{Path: again, Wish: wish, Code: "L01"}).GetTilasm()
	if len(v2.GetVersions()) != 2 || v2.GetRevision() != 2 || v2.GetId() != tilasm.GetId() {
		t.Errorf("a new version = %v", v2)
	}

	if _, err := c.tilasms.Put(ctx, connect.NewRequest(&planv1.TilasmServicePutRequest{
		Path: folder(t, map[string]string{"index.html": page("X", "y")}), Wish: wish, Code: "L01",
	})); code(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "Markdown document") {
		t.Errorf("an HTML page onto a Markdown tilasm: %v, want failed_precondition", err)
	}
	if _, err := c.tilasms.Put(ctx, connect.NewRequest(&planv1.TilasmServicePutRequest{
		Path: again, Wish: wish, Code: html.GetCode(),
	})); code(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "HTML page") {
		t.Errorf("a .md onto an HTML tilasm: %v, want failed_precondition", err)
	}
	if _, err := c.tilasms.Put(ctx, connect.NewRequest(&planv1.TilasmServicePutRequest{
		Path: folder(t, map[string]string{"README.md": "# Neither"}), Wish: wish,
	})); code(err) != connect.CodeInvalidArgument {
		t.Errorf("a folder with neither: %v, want invalid_argument", err)
	}
}

// TestCreateMarkdown: a document made from a title and a text, by the lead by default; its title comes from its first
// heading when none is given, and one of the two is needed. The journal keeps the request, not its text.
func TestCreateMarkdown(t *testing.T) {
	ctx := t.Context()
	c := serve(t, WithHome(t.TempDir()))
	wish := c.wish(t)
	tasks(t, c, wish, "W1")

	res, err := c.tilasms.Create(ctx, connect.NewRequest(&planv1.TilasmServiceCreateRequest{
		Wish: wish, Title: "Spec", Markdown: "Secret plan text.", Cites: []string{"W1"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	tilasm := res.Msg.GetTilasm()
	if tilasm.GetCode() != "L01" || tilasm.GetTitle() != "Spec" || tilasm.GetAuthor() != "lead" ||
		tilasm.GetFormat() != planv1.TilasmFormat_TILASM_FORMAT_MARKDOWN || tilasm.GetRevision() != 1 ||
		len(tilasm.GetCites()) != 1 || tilasm.GetVersions()[0].GetOrigin() != planv1.TilasmOrigin_TILASM_ORIGIN_CREATE {
		t.Errorf("create = %v", tilasm)
	}
	if got := getTilasm(t, c, "L01", 0).GetText(); got != "Secret plan text." {
		t.Errorf("its text = %q", got)
	}

	empty, err := c.tilasms.Create(ctx, connect.NewRequest(&planv1.TilasmServiceCreateRequest{
		Wish: wish, Markdown: "# From the heading\n", Author: "developer",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := empty.Msg.GetTilasm(); got.GetTitle() != "From the heading" || got.GetAuthor() != "developer" {
		t.Errorf("create without a title = %v", got)
	}
	if _, err := c.tilasms.Create(ctx, connect.NewRequest(&planv1.TilasmServiceCreateRequest{Wish: wish})); code(err) != connect.CodeInvalidArgument {
		t.Errorf("neither title nor heading: %v, want invalid_argument", err)
	}

	if _, err := store.Commands(ctx, c.store, func(cmd store.Command) bool {
		if bytes.Contains(cmd.Request, []byte("Secret plan text")) {
			t.Errorf("%s journals the document's text", cmd.Method)
		}
		return false
	}); err != nil {
		t.Fatal(err)
	}
}

// TestSaveMarkdown: a save from the current revision writes the text; by the latest version's author within minutes
// of it, it rewrites that version, else it adds one. A save from an older revision is refused with ABORTED, and a
// save of an HTML page with FAILED_PRECONDITION.
func TestSaveMarkdown(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	clock := &fakeClock{now: time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)}
	c := serve(t, WithHome(home), WithClock(clock.Now))
	wish := c.wish(t)

	created, err := c.tilasms.Create(ctx, connect.NewRequest(&planv1.TilasmServiceCreateRequest{
		Wish: wish, Title: "Notes", Author: "developer",
	}))
	if err != nil {
		t.Fatal(err)
	}
	id := created.Msg.GetTilasm().GetId()
	save := func(base int64, text, author string) (*planv1.Tilasm, error) {
		res, err := c.tilasms.Save(ctx, connect.NewRequest(&planv1.TilasmServiceSaveRequest{
			Id: id, BaseRevision: base, Markdown: text, Author: author,
		}))
		if err != nil {
			return nil, err
		}
		return res.Msg.GetTilasm(), nil
	}

	// Within the sitting, by the same author: version 1 is rewritten.
	clock.add(2 * time.Minute)
	got, err := save(1, "# Notes\n\nFirst words.", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.GetVersions()) != 1 || got.GetRevision() != 2 || got.GetVersions()[0].GetRevision() != 2 ||
		got.GetVersions()[0].GetSize() != int64(len("# Notes\n\nFirst words.")) ||
		!got.GetVersions()[0].GetUpdateTime().AsTime().Equal(clock.Now()) {
		t.Errorf("a save in the sitting = %v", got)
	}
	if text := getTilasm(t, c, "L01", 1).GetText(); text != "# Notes\n\nFirst words." {
		t.Errorf("version 1 = %q", text)
	}
	if got.GetTitle() != "Notes" {
		t.Errorf("a save without title changed it: %q", got.GetTitle())
	}

	// A stale revision is refused, the text unchanged.
	if _, err := save(1, "lost", ""); code(err) != connect.CodeAborted {
		t.Errorf("a stale save: %v, want aborted", err)
	}
	if text := getTilasm(t, c, "L01", 0).GetText(); text != "# Notes\n\nFirst words." {
		t.Errorf("after a stale save the text = %q", text)
	}

	// After the sitting: a new version, the earlier one kept.
	clock.add(11 * time.Minute)
	if got, err = save(2, "# Notes\n\nLater words.", ""); err != nil {
		t.Fatal(err)
	}
	if len(got.GetVersions()) != 2 || got.GetRevision() != 3 ||
		got.GetVersions()[1].GetOrigin() != planv1.TilasmOrigin_TILASM_ORIGIN_SAVE {
		t.Errorf("a save after the sitting = %v", got)
	}
	if text := getTilasm(t, c, "L01", 1).GetText(); text != "# Notes\n\nFirst words." {
		t.Errorf("version 1 after a new one = %q", text)
	}

	// Another author: a new version, even within the sitting.
	clock.add(time.Minute)
	if got, err = save(3, "# Notes\n\nThe lead's words.", "lead"); err != nil {
		t.Fatal(err)
	}
	if len(got.GetVersions()) != 3 || got.GetVersions()[2].GetAuthor() != "lead" {
		t.Errorf("a save by another author = %v", got)
	}

	// After a restore: a new version, even by its author within the sitting.
	if _, err := c.tilasms.Restore(ctx, connect.NewRequest(&planv1.TilasmServiceRestoreRequest{
		Tilasm: &planv1.TilasmRef{Ref: &planv1.TilasmRef_Id{Id: id}}, Version: 1, Author: "developer",
	})); err != nil {
		t.Fatal(err)
	}
	if got, err = save(5, "# Notes\n\nAfter restore.", ""); err != nil {
		t.Fatal(err)
	}
	if len(got.GetVersions()) != 5 || got.GetRevision() != 6 {
		t.Errorf("a save after a restore = %v", got)
	}

	// The window reads the document at its address, as text, and a rewrite changes its ETag.
	res, body := fetch(t, http.MethodGet, c.url+"/tilasm/"+id+"/")
	if body != "# Notes\n\nAfter restore." || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/plain") ||
		res.Header.Get("Etag") != `"v5-r6"` {
		t.Errorf("the document served = %q, %q, ETag %q", body, res.Header.Get("Content-Type"), res.Header.Get("Etag"))
	}

	// A title given renames it.
	res2, err := c.tilasms.Save(ctx, connect.NewRequest(&planv1.TilasmServiceSaveRequest{
		Id: id, BaseRevision: 6, Markdown: "x", Title: "Renamed",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if res2.Msg.GetTilasm().GetTitle() != "Renamed" || len(res2.Msg.GetTilasm().GetVersions()) != 5 {
		t.Errorf("a save with a title = %v", res2.Msg.GetTilasm())
	}

	// An HTML page is read only.
	page := putTilasm(t, c, &planv1.TilasmServicePutRequest{
		Path: folder(t, map[string]string{"index.html": page("Page", "x")}), Wish: wish,
	}).GetTilasm()
	if _, err := c.tilasms.Save(ctx, connect.NewRequest(&planv1.TilasmServiceSaveRequest{
		Id: page.GetId(), BaseRevision: page.GetRevision(), Markdown: "x",
	})); code(err) != connect.CodeFailedPrecondition {
		t.Errorf("save an HTML page: %v, want failed_precondition", err)
	}
}

// TestSaveCeiling: a save beyond the tilasm's ceiling is refused, saying so, in place or as a new version.
func TestSaveCeiling(t *testing.T) {
	ctx := t.Context()
	c := serve(t, WithHome(t.TempDir()))
	wish := c.wish(t)
	created, err := c.tilasms.Create(ctx, connect.NewRequest(&planv1.TilasmServiceCreateRequest{Wish: wish, Title: "Small"}))
	if err != nil {
		t.Fatal(err)
	}
	tilasm := created.Msg.GetTilasm()
	tilasm.MaxBytes = 10
	if err := c.store.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal(actor, planv1connect.TilasmServiceSaveProcedure, &planv1.TilasmServiceSaveRequest{Id: tilasm.GetId()}); err != nil {
			return err
		}
		return tx.Put(tilasm)
	}); err != nil {
		t.Fatal(err)
	}
	for _, author := range []string{"lead", "developer"} {
		_, err := c.tilasms.Save(ctx, connect.NewRequest(&planv1.TilasmServiceSaveRequest{
			Id: tilasm.GetId(), BaseRevision: 1, Markdown: strings.Repeat("x", 11), Author: author,
		}))
		if code(err) != connect.CodeResourceExhausted {
			t.Errorf("a save by %s beyond the ceiling: %v, want resource_exhausted", author, err)
		}
	}
	if text := getTilasm(t, c, "L01", 0).GetText(); text != "" {
		t.Errorf("after refused saves the text = %q", text)
	}
}

// TestPutDataMarkdown: a .md dropped on the window is a Markdown tilasm by the developer.
func TestPutDataMarkdown(t *testing.T) {
	ctx := t.Context()
	c := serve(t, WithHome(t.TempDir()))
	wish := c.wish(t)
	res, err := c.tilasms.PutData(ctx, connect.NewRequest(&planv1.TilasmServicePutDataRequest{
		Wish: wish, Name: "notes.md", Files: []*planv1.TilasmFile{{Path: "notes.md", Content: []byte("Plain notes.")}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	got := res.Msg.GetTilasm()
	if got.GetFormat() != planv1.TilasmFormat_TILASM_FORMAT_MARKDOWN || got.GetTitle() != "notes" ||
		got.GetAuthor() != "developer" || got.GetVersions()[0].GetOrigin() != planv1.TilasmOrigin_TILASM_ORIGIN_IMPORT {
		t.Errorf("a .md dropped = %v", got)
	}
	if text := getTilasm(t, c, "L01", 0).GetText(); text != "Plain notes." {
		t.Errorf("its text = %q", text)
	}
}

// TestMarkdownExportImport: a Markdown tilasm's .zip, and a wish's export, carry its index.md and its format.
func TestMarkdownExportImport(t *testing.T) {
	ctx := t.Context()
	c := serve(t, WithHome(t.TempDir()))
	wish := c.wish(t)
	if _, err := c.tilasms.Create(ctx, connect.NewRequest(&planv1.TilasmServiceCreateRequest{
		Wish: wish, Markdown: "# Carried\n\nAcross.",
	})); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "l01.zip")
	if _, err := c.tilasms.Export(ctx, connect.NewRequest(&planv1.TilasmServiceExportRequest{Tilasm: ref("L01"), File: file})); err != nil {
		t.Fatal(err)
	}
	if files := zipped(t, file); files["index.md"] != "# Carried\n\nAcross." || !strings.Contains(files["tilasm.json"], "TILASM_FORMAT_MARKDOWN") {
		t.Errorf("the .zip holds %v", files)
	}

	dst := serve(t, WithHome(t.TempDir()))
	there := dst.wish(t)
	imported, err := dst.tilasms.Import(ctx, connect.NewRequest(&planv1.TilasmServiceImportRequest{File: file, Wish: there}))
	if err != nil {
		t.Fatal(err)
	}
	if got := imported.Msg.GetTilasm(); got.GetFormat() != planv1.TilasmFormat_TILASM_FORMAT_MARKDOWN || got.GetTitle() != "Carried" {
		t.Errorf("imported = %v", got)
	}
	if text := getTilasm(t, dst, "L01", 0).GetText(); text != "# Carried\n\nAcross." {
		t.Errorf("imported text = %q", text)
	}

	wishFile := filepath.Join(t.TempDir(), "wish.djinn")
	export(t, c, wish, wishFile)
	other := serve(t, WithHome(t.TempDir()))
	if _, err := other.wishes.Import(ctx, connect.NewRequest(&planv1.WishServiceImportRequest{File: wishFile})); err != nil {
		t.Fatal(err)
	}
	got := getTilasm(t, other, "L01", 0)
	if got.GetText() != "# Carried\n\nAcross." || got.GetTilasm().GetFormat() != planv1.TilasmFormat_TILASM_FORMAT_MARKDOWN ||
		got.GetTilasm().GetRevision() != 1 {
		t.Errorf("after a wish's import = %v", got)
	}
}

// TestSaveRollsBack: a save that rewrites the latest version but cannot put its text in place leaves the version as
// it was, its revision unchanged.
func TestSaveRollsBack(t *testing.T) {
	ctx := t.Context()
	c := serve(t, WithHome(t.TempDir()))
	wish := c.wish(t)
	created, err := c.tilasms.Create(ctx, connect.NewRequest(&planv1.TilasmServiceCreateRequest{
		Wish: wish, Title: "Kept", Markdown: "Before.", Author: "developer",
	}))
	if err != nil {
		t.Fatal(err)
	}
	saved := renamer
	renamer = fsx.Renamer{Func: func(oldpath, newpath string) error {
		if filepath.Base(oldpath) == "index.md" && filepath.Base(filepath.Dir(newpath)) == "v1" {
			return errors.New("refused")
		}
		return os.Rename(oldpath, newpath)
	}, Sleep: func(time.Duration) {}}
	t.Cleanup(func() { renamer = saved })
	if _, err := c.tilasms.Save(ctx, connect.NewRequest(&planv1.TilasmServiceSaveRequest{
		Id: created.Msg.GetTilasm().GetId(), BaseRevision: 1, Markdown: "After.",
	})); err == nil {
		t.Fatal("a save whose text cannot be put in place succeeded")
	}
	renamer = saved
	got := getTilasm(t, c, "L01", 0)
	if got.GetText() != "Before." || got.GetTilasm().GetRevision() != 1 {
		t.Errorf("after a failed save: %q at revision %d", got.GetText(), got.GetTilasm().GetRevision())
	}
}

// TestExportMarkdown: a Markdown document exports as its text alone, a .md headed by its title; an HTML one is
// refused, and keeps its .zip.
func TestExportMarkdown(t *testing.T) {
	ctx := t.Context()
	c := serve(t, WithHome(t.TempDir()))
	wish := c.wish(t)
	if _, err := c.tilasms.Create(ctx, connect.NewRequest(&planv1.TilasmServiceCreateRequest{
		Wish: wish, Title: "Spec", Markdown: "Plan *text*.\n",
	})); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "spec.md")
	res, err := c.tilasms.Export(ctx, connect.NewRequest(&planv1.TilasmServiceExportRequest{
		Tilasm: &planv1.TilasmRef{Ref: &planv1.TilasmRef_Code{Code: "L01"}}, Wish: wish, File: file, Markdown: true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "# Spec\n\nPlan *text*.\n" || res.Msg.GetFile() != file || res.Msg.GetSize() != int64(len(data)) {
		t.Errorf("export = %v, file holds %q", res.Msg, data)
	}
	html, err := c.tilasms.Put(ctx, connect.NewRequest(&planv1.TilasmServicePutRequest{
		Path: folder(t, map[string]string{"index.html": "<p>Hi</p>"}), Wish: wish,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.tilasms.Export(ctx, connect.NewRequest(&planv1.TilasmServiceExportRequest{
		Tilasm: &planv1.TilasmRef{Ref: &planv1.TilasmRef_Id{Id: html.Msg.GetTilasm().GetId()}},
		File:   filepath.Join(t.TempDir(), "page.md"), Markdown: true,
	})); code(err) != connect.CodeFailedPrecondition {
		t.Errorf("an HTML tilasm as Markdown: %v, want failed_precondition", err)
	}
}

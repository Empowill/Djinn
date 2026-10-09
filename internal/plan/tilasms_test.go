package plan

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/link"
	"github.com/empowill/djinn/internal/store"
)

// page is an index.html whose visible text is text, with a script and a style that are not.
func page(title, text string) string {
	return "<!doctype html><html><head><title>" + title + "</title><style>body{color:red}</style>" +
		"<script>const secret = 'not text';</script></head><body><h1>" + title + "</h1><p>" + text +
		"</p><svg><title>a label</title><text>Wish</text></svg></body></html>"
}

// folder writes files into a new folder and returns it.
func folder(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// zipOf writes files into a new .zip and returns its path.
func zipOf(t *testing.T, files map[string]string) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(files[name]))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "tilasm.zip")
	if err := os.WriteFile(p, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// zipped reads the files of a .zip.
func zipped(t *testing.T, file string) map[string]string {
	t.Helper()
	zr, err := zip.OpenReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		b.ReadFrom(rc)
		rc.Close()
		out[f.Name] = b.String()
	}
	return out
}

// tasks stores tasks of the wish as the harness does, and returns them by code.
func tasks(t *testing.T, c clients, wishID string, codes ...string) map[string]*planv1.Task {
	t.Helper()
	out := map[string]*planv1.Task{}
	err := c.store.Tx(t.Context(), func(tx *store.Tx) error {
		for _, code := range codes {
			task := &planv1.Task{Id: store.NewID(), WishId: wishID, Code: code, Title: "Task " + code, CreateTime: timestamppb.Now()}
			if strings.HasPrefix(code, "T") {
				task.Kind = planv1.TaskKind_TASK_KIND_AZIMA
			}
			spawn := &planv1.TaskServiceSpawnRequest{WishId: wishID, Title: task.GetTitle()}
			if err := tx.Journal(actor, planv1connect.TaskServiceSpawnProcedure, spawn); err != nil {
				return err
			}
			if err := tx.Put(task); err != nil {
				return err
			}
			out[code] = task
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func putTilasm(t *testing.T, c clients, req *planv1.TilasmServicePutRequest) *planv1.TilasmServicePutResponse {
	t.Helper()
	res, err := c.tilasms.Put(t.Context(), connect.NewRequest(req))
	if err != nil {
		t.Fatal(err)
	}
	return res.Msg
}

func getTilasm(t *testing.T, c clients, code string, version int32) *planv1.TilasmServiceGetResponse {
	t.Helper()
	res, err := c.tilasms.Get(t.Context(), connect.NewRequest(&planv1.TilasmServiceGetRequest{
		Tilasm: &planv1.TilasmRef{Ref: &planv1.TilasmRef_Code{Code: code}}, Version: version,
	}))
	if err != nil {
		t.Fatal(err)
	}
	return res.Msg
}

func ref(code string) *planv1.TilasmRef {
	return &planv1.TilasmRef{Ref: &planv1.TilasmRef_Code{Code: code}}
}

func TestTilasmPutReplaceHistoryRestore(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	c := serve(t, WithHome(home))
	wish := c.wish(t)
	ts := tasks(t, c, wish, "T29", "W1")

	src := folder(t, map[string]string{
		"index.html":  page("The objects in the database", "A wish holds tasks."),
		"img/one.svg": "<svg/>",
		"tilasm.json": `{"not":"a file of the tilasm"}`,
	})
	put := putTilasm(t, c, &planv1.TilasmServicePutRequest{Path: src, Wish: wish, Cites: []string{"T29", "w1", ts["T29"].GetId()}})
	first := put.GetTilasm()
	if first.GetCode() != "L01" || first.GetTitle() != "The objects in the database" || first.GetAuthor() != "lead" ||
		first.GetMaxBytes() != DefaultTilasmBytes || len(first.GetVersions()) != 1 {
		t.Fatalf("first put = %v", first)
	}
	if !slices.Equal(first.GetCites(), []string{ts["T29"].GetId(), ts["W1"].GetId()}) {
		t.Errorf("cites = %v, want T29 then W1, each once", first.GetCites())
	}
	if v := first.GetVersions()[0]; v.GetNumber() != 1 || v.GetFiles() != 2 || v.GetSize() == 0 {
		t.Errorf("version 1 = %v, want 2 files: the manifest's name is Djinn's", v)
	}
	if want := filepath.Join(home, "tilasms", first.GetId(), "v1"); put.GetDirectory() != want {
		t.Errorf("directory = %s, want %s", put.GetDirectory(), want)
	}

	got := getTilasm(t, c, "l01", 0)
	if got.GetVersion() != 1 || !strings.Contains(got.GetText(), "A wish holds tasks.") || strings.Contains(got.GetText(), "not text") ||
		strings.Contains(got.GetText(), "color") || !strings.Contains(got.GetText(), "a label") {
		t.Errorf("text = %q: the page's words, without script nor style", got.GetText())
	}
	if !slices.Equal(got.GetFiles(), []string{"img/one.svg", "index.html"}) {
		t.Errorf("files = %v", got.GetFiles())
	}

	// Put again by its code: a new version, same tilasm, its title and cites kept.
	src2 := folder(t, map[string]string{"index.html": page("Renamed", "A wish holds tilasms too.")})
	second := putTilasm(t, c, &planv1.TilasmServicePutRequest{Path: src2, Wish: wish, Code: "L01", Author: "W1"}).GetTilasm()
	if second.GetId() != first.GetId() || second.GetTitle() != first.GetTitle() || len(second.GetVersions()) != 2 ||
		second.GetVersions()[1].GetAuthor() != "W1" || len(second.GetCites()) != 2 {
		t.Fatalf("second put = %v", second)
	}
	if text := getTilasm(t, c, "L01", 0).GetText(); !strings.Contains(text, "tilasms too") {
		t.Errorf("latest text = %q, want version 2", text)
	}
	if text := getTilasm(t, c, "L01", 1).GetText(); !strings.Contains(text, "A wish holds tasks.") {
		t.Errorf("version 1 text = %q", text)
	}
	history, err := c.tilasms.History(ctx, connect.NewRequest(&planv1.TilasmServiceHistoryRequest{Tilasm: ref("L01")}))
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Msg.GetVersions()) != 2 || history.Msg.GetDirectory() != filepath.Join(home, "tilasms", first.GetId()) {
		t.Errorf("history = %v", history.Msg)
	}

	// Restore version 1: it comes back as version 3, and the others stay.
	restored, err := c.tilasms.Restore(ctx, connect.NewRequest(&planv1.TilasmServiceRestoreRequest{Tilasm: ref("L01"), Version: 1}))
	if err != nil {
		t.Fatal(err)
	}
	if vs := restored.Msg.GetTilasm().GetVersions(); len(vs) != 3 || vs[2].GetRestoredFrom() != 1 || vs[2].GetFiles() != 2 {
		t.Errorf("versions after restore = %v", vs)
	}
	if text := getTilasm(t, c, "L01", 0).GetText(); !strings.Contains(text, "A wish holds tasks.") {
		t.Errorf("text after restore = %q, want version 1's", text)
	}
	if _, err := c.tilasms.Restore(ctx, connect.NewRequest(&planv1.TilasmServiceRestoreRequest{Tilasm: ref("L01"), Version: 9})); code(err) != connect.CodeNotFound {
		t.Errorf("restore of a version it never had: %v, want not_found", err)
	}

	// A .zip of a folder is that folder; without a code, a new tilasm takes the next one.
	z := zipOf(t, map[string]string{"notes/index.html": page("Notes", "Zipped."), "notes/a.txt": "a"})
	other := putTilasm(t, c, &planv1.TilasmServicePutRequest{Path: z, Wish: wish, Title: "Notes, zipped"}).GetTilasm()
	if other.GetCode() != "L02" || other.GetTitle() != "Notes, zipped" || other.GetVersions()[0].GetFiles() != 2 {
		t.Errorf("zipped put = %v", other)
	}

	// What is not a tilasm is refused, saying why.
	for name, req := range map[string]*planv1.TilasmServicePutRequest{
		"no index.html":   {Path: folder(t, map[string]string{"readme.md": "#"}), Wish: wish},
		"an unknown cite": {Path: src, Wish: wish, Cites: []string{"W9"}},
		"a .zip escaping": {Path: zipOf(t, map[string]string{"index.html": "x", "../evil": "x"}), Wish: wish},
	} {
		if _, err := c.tilasms.Put(ctx, connect.NewRequest(req)); code(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: %v, want invalid_argument", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "tilasms", "..", "evil")); err == nil {
		t.Error("a .zip wrote outside the tilasm")
	}

	// Every change is journaled.
	journaled := map[string]int{}
	store.Commands(ctx, c.store, func(cmd store.Command) bool { journaled[cmd.Method]++; return false })
	if journaled[planv1connect.TilasmServicePutProcedure] != 3 || journaled[planv1connect.TilasmServiceRestoreProcedure] != 1 {
		t.Errorf("journal = %v, want 3 puts and 1 restore", journaled)
	}
	// Nothing is left on the way.
	entries, _ := os.ReadDir(filepath.Join(home, "tilasms"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			t.Errorf("a temporary folder is left: %s", e.Name())
		}
	}
}

func TestTilasmSearch(t *testing.T) {
	c := serve(t, WithHome(t.TempDir()))
	wish := c.wish(t)
	putTilasm(t, c, &planv1.TilasmServicePutRequest{Path: folder(t, map[string]string{"index.html": page("Data model", "Wish, task, question.")}), Wish: wish})
	putTilasm(t, c, &planv1.TilasmServicePutRequest{Path: folder(t, map[string]string{"index.html": page("Releases", "How a binary updates itself.")}), Wish: wish})
	for search, want := range map[string][]string{
		"":                 {"L01", "L02"},
		"talisman":         {"L01", "L02"},
		"Talismans":        {"L01", "L02"},
		"tilasm question":  {"L01"},
		"BINARY itself":    {"L02"},
		"l02":              {"L02"},
		"question binary":  nil,
		"talisman nothing": nil,
	} {
		res, err := c.tilasms.List(t.Context(), connect.NewRequest(&planv1.TilasmServiceListRequest{Wish: wish, Search: search}))
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, tl := range res.Msg.GetTilasms() {
			got = append(got, tl.GetCode())
		}
		if !slices.Equal(got, want) {
			t.Errorf("search %q = %v, want %v", search, got, want)
		}
	}
}

func TestTilasmCeiling(t *testing.T) {
	ctx := t.Context()
	c := serve(t, WithHome(t.TempDir()))
	wish := c.wish(t)
	first := putTilasm(t, c, &planv1.TilasmServicePutRequest{Path: folder(t, map[string]string{"index.html": page("Small", "ok")}), Wish: wish}).GetTilasm()

	// The ceiling is a field of the tilasm: lowered, a bigger version is refused, saying so.
	first.MaxBytes = 1 << 10
	err := c.store.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal(actor, planv1connect.TilasmServicePutProcedure, &planv1.TilasmServicePutRequest{Wish: wish}); err != nil {
			return err
		}
		return tx.Put(first)
	})
	if err != nil {
		t.Fatal(err)
	}
	big := folder(t, map[string]string{"index.html": page("Big", "ok"), "video.mp4": strings.Repeat("x", 2<<10)})
	for _, path := range []string{big, zipOf(t, map[string]string{"index.html": page("Big", "ok"), "video.mp4": strings.Repeat("x", 2<<10)})} {
		_, err = c.tilasms.Put(ctx, connect.NewRequest(&planv1.TilasmServicePutRequest{Path: path, Wish: wish, Code: "L01"}))
		if code(err) != connect.CodeResourceExhausted || !strings.Contains(err.Error(), "ceiling, 1 KiB") || !strings.Contains(err.Error(), "video.mp4") {
			t.Errorf("a version beyond the ceiling: %v, want resource_exhausted naming 1 KiB and video.mp4", err)
		}
	}
	if vs := getTilasm(t, c, "L01", 0).GetTilasm().GetVersions(); len(vs) != 1 {
		t.Errorf("versions = %d, want the first only", len(vs))
	}
}

func TestTilasmExportImport(t *testing.T) {
	ctx := t.Context()
	c := serve(t, WithHome(t.TempDir()))
	wish := c.wish(t)
	tasks(t, c, wish, "T29")
	src := folder(t, map[string]string{"index.html": page("Model", "v1")})
	putTilasm(t, c, &planv1.TilasmServicePutRequest{Path: src, Wish: wish, Cites: []string{"T29"}})
	tilasm := putTilasm(t, c, &planv1.TilasmServicePutRequest{
		Path: folder(t, map[string]string{"index.html": page("Model", "v2"), "css/a.css": "p{}"}), Wish: wish, Code: "L01",
	}).GetTilasm()

	file := filepath.Join(t.TempDir(), "model.zip")
	exported, err := c.tilasms.Export(ctx, connect.NewRequest(&planv1.TilasmServiceExportRequest{Tilasm: ref("L01"), File: file}))
	if err != nil {
		t.Fatal(err)
	}
	files := zipped(t, exported.Msg.GetFile())
	if len(files) != 3 || !strings.Contains(files["index.html"], "v2") || files["css/a.css"] != "p{}" ||
		!strings.Contains(files["tilasm.json"], `"T29"`) || !strings.Contains(files["tilasm.json"], tilasm.GetId()) {
		t.Fatalf("the .zip holds %v", files)
	}

	// Into the same wish: a new version of the same tilasm.
	same, err := c.tilasms.Import(ctx, connect.NewRequest(&planv1.TilasmServiceImportRequest{File: file, Wish: wish}))
	if err != nil {
		t.Fatal(err)
	}
	if got := same.Msg.GetTilasm(); got.GetId() != tilasm.GetId() || len(got.GetVersions()) != 3 || got.GetCode() != "L01" {
		t.Errorf("import into its wish = %v, want version 3 of L01", got)
	}

	// Into another wish here: a tilasm of its own, its cites found by code.
	other := c.wish(t)
	azima := tasks(t, c, other, "T29")["T29"]
	imported, err := c.tilasms.Import(ctx, connect.NewRequest(&planv1.TilasmServiceImportRequest{File: file, Wish: other, Author: "developer"}))
	if err != nil {
		t.Fatal(err)
	}
	got := imported.Msg.GetTilasm()
	if got.GetId() == tilasm.GetId() || got.GetCode() != "L01" || got.GetTitle() != "Model" || len(got.GetVersions()) != 1 ||
		got.GetVersions()[0].GetAuthor() != "developer" || !slices.Equal(got.GetCites(), []string{azima.GetId()}) {
		t.Errorf("import into another wish = %v", got)
	}
	read, err := c.tilasms.Get(ctx, connect.NewRequest(&planv1.TilasmServiceGetRequest{Tilasm: ref("L01"), Wish: other}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read.Msg.GetText(), "v2") || !slices.Equal(read.Msg.GetFiles(), []string{"css/a.css", "index.html"}) {
		t.Errorf("imported tilasm = %v", read.Msg)
	}
	// Two wishes have an L01 now: a code alone is ambiguous.
	if _, err := c.tilasms.Get(ctx, connect.NewRequest(&planv1.TilasmServiceGetRequest{Tilasm: ref("L01")})); code(err) != connect.CodeFailedPrecondition {
		t.Errorf("an ambiguous code: %v, want failed_precondition", err)
	}

	// On another machine, the tilasm keeps its identifier, so that its links still name it.
	dst := serve(t, WithHome(t.TempDir()))
	there := dst.wish(t)
	moved, err := dst.tilasms.Import(ctx, connect.NewRequest(&planv1.TilasmServiceImportRequest{File: file, Wish: there}))
	if err != nil {
		t.Fatal(err)
	}
	if got := moved.Msg.GetTilasm(); got.GetId() != tilasm.GetId() || len(got.GetCites()) != 0 {
		t.Errorf("import elsewhere = %v, want its identifier and no cite, T29 not being there", got)
	}

	// A .zip that is no tilasm export says how to put it.
	if _, err := c.tilasms.Import(ctx, connect.NewRequest(&planv1.TilasmServiceImportRequest{
		File: zipOf(t, map[string]string{"index.html": "x"}), Wish: wish,
	})); code(err) != connect.CodeInvalidArgument || !strings.Contains(err.Error(), "djinn tilasm put") {
		t.Errorf("a .zip without manifest: %v", err)
	}
}

func TestWishExportCarriesTilasms(t *testing.T) {
	ctx := t.Context()
	srcHome := t.TempDir()
	c := serve(t, WithHome(srcHome))
	wish := c.wish(t)
	tasks(t, c, wish, "W1")
	putTilasm(t, c, &planv1.TilasmServicePutRequest{Path: folder(t, map[string]string{"index.html": page("Model", "first version")}), Wish: wish, Cites: []string{"W1"}})
	tilasm := putTilasm(t, c, &planv1.TilasmServicePutRequest{
		Path: folder(t, map[string]string{"index.html": page("Model", "second version"), "img/x.png": "png"}), Wish: wish, Code: "L01",
	}).GetTilasm()

	file := filepath.Join(t.TempDir(), "wish.djinn")
	data := export(t, c, wish, file)
	exp, err := decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(exp.GetTilasms()) != 1 || len(exp.GetTilasms()[0].GetFiles()) != 3 || exp.GetTilasms()[0].GetCiteCodes()[0] != "W1" {
		t.Fatalf("the export carries %v", exp.GetTilasms())
	}
	if bytes.Contains(data, []byte(srcHome)) {
		t.Error("the export names the data folder")
	}

	dstHome := t.TempDir()
	dst := serve(t, WithHome(dstHome))
	if _, err := dst.wishes.Import(ctx, connect.NewRequest(&planv1.WishServiceImportRequest{File: file})); err != nil {
		t.Fatal(err)
	}
	if text := getTilasm(t, dst, "L01", 1).GetText(); !strings.Contains(text, "first version") {
		t.Errorf("version 1 after import = %q", text)
	}
	got := getTilasm(t, dst, "L01", 0)
	if got.GetTilasm().GetId() != tilasm.GetId() || got.GetVersion() != 2 || !strings.Contains(got.GetText(), "second version") ||
		len(got.GetFiles()) != 2 || len(got.GetTilasm().GetCites()) != 1 {
		t.Errorf("tilasm after import = %v", got)
	}
	// The journal keeps what was imported, but not the files: they are in the data folder.
	store.Commands(ctx, dst.store, func(cmd store.Command) bool {
		if bytes.Contains(cmd.Request, []byte("second version")) {
			t.Errorf("%s journals the files of the tilasm", cmd.Method)
		}
		return false
	})
	// The snapshot of the window shows the manifests, without the files.
	snap, err := dst.wishes.Snapshot(ctx, connect.NewRequest(&planv1.WishServiceSnapshotRequest{WishId: wish}))
	if err != nil {
		t.Fatal(err)
	}
	if ts := snap.Msg.GetExport().GetTilasms(); len(ts) != 1 || len(ts[0].GetFiles()) != 0 {
		t.Errorf("snapshot tilasms = %v", ts)
	}

	// Replaced by an import, the wish's tilasms are those of the file, and nothing else stays.
	putTilasm(t, dst, &planv1.TilasmServicePutRequest{Path: folder(t, map[string]string{"index.html": page("Extra", "x")}), Wish: wish})
	if _, err := dst.wishes.Import(ctx, connect.NewRequest(&planv1.WishServiceImportRequest{File: file, Replace: true})); err != nil {
		t.Fatal(err)
	}
	list, err := dst.tilasms.List(ctx, connect.NewRequest(&planv1.TilasmServiceListRequest{Wish: wish}))
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Msg.GetTilasms()) != 1 || getTilasm(t, dst, "L01", 0).GetVersion() != 2 {
		t.Errorf("tilasms after a replace = %v", list.Msg.GetTilasms())
	}
	entries, _ := os.ReadDir(filepath.Join(dstHome, "tilasms"))
	if len(entries) != 1 || entries[0].Name() != tilasm.GetId() {
		t.Errorf("the tilasms' folder holds %v, want the one tilasm", entries)
	}

	// Deleted with its wish, a tilasm's folder goes too.
	if _, err := dst.wishes.Delete(ctx, connect.NewRequest(&planv1.WishServiceDeleteRequest{WishId: wish})); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(TilasmDir(dstHome, tilasm.GetId())); !os.IsNotExist(err) {
		t.Errorf("the folder of a deleted wish's tilasm: %v", err)
	}
}

func TestHTMLText(t *testing.T) {
	title, text := htmlText(strings.NewReader(`<html><head><title> Model </title><script>x()</script></head>
<body><h1>Entities</h1><p>A <b>wish</b>   holds
tasks.</p><ul><li>Task</li><li>Tilasm &amp; its versions</li></ul><table><tr><td>id</td><td>UUID</td></tr></table></body></html>`))
	want := "Entities\nA wish holds tasks.\nTask\nTilasm & its versions\nid UUID"
	if title != "Model" || text != want {
		t.Errorf("htmlText = %q, %q; want %q, %q", title, text, "Model", want)
	}
}

func TestTilasmOpenAndLinks(t *testing.T) {
	ctx := t.Context()
	var shown []string
	window := true
	c := serve(t, WithHome(t.TempDir()),
		WithShowTilasm(func(wishID, tilasmID string) bool { shown = append(shown, wishID+"/"+tilasmID); return window }),
		WithTilasmURL(func(id string) string { return "http://127.0.0.1:1234/tilasm/" + id + "/@key/" }))
	wish := c.wish(t)
	tilasm := putTilasm(t, c, &planv1.TilasmServicePutRequest{
		Path: folder(t, map[string]string{"index.html": page("A concept", "drawn")}), Wish: wish,
	}).GetTilasm()

	// get gives its two links: djinn:// for anywhere, the local http address for a browser or an agent.
	got := getTilasm(t, c, "L01", 0)
	if got.GetLink() != "djinn://tilasm/"+tilasm.GetId() || got.GetUrl() != "http://127.0.0.1:1234/tilasm/"+tilasm.GetId()+"/@key/" {
		t.Fatalf("get: link %q, url %q", got.GetLink(), got.GetUrl())
	}
	if l, err := link.Parse(got.GetLink()); err != nil || l.ID != tilasm.GetId() || l.Kind != link.Tilasm {
		t.Fatalf("the link read back: %v, %v", l, err)
	}

	// open shows it in its wish's Tilasms tab, by its code or its identifier.
	for _, r := range []*planv1.TilasmRef{ref("l01"), {Ref: &planv1.TilasmRef_Id{Id: tilasm.GetId()}}} {
		res, err := c.tilasms.Open(ctx, connect.NewRequest(&planv1.TilasmServiceOpenRequest{Tilasm: r}))
		if err != nil || res.Msg.GetTilasm().GetId() != tilasm.GetId() || res.Msg.GetLink() != got.GetLink() || !res.Msg.GetWindow() {
			t.Fatalf("open %v: %v, %v", r, res, err)
		}
	}
	if want := wish + "/" + tilasm.GetId(); len(shown) != 2 || shown[0] != want || shown[1] != want {
		t.Fatalf("shown %v, want %s twice", shown, want)
	}
	if _, err := c.tilasms.Open(ctx, connect.NewRequest(&planv1.TilasmServiceOpenRequest{Tilasm: ref("L09")})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("open L09: %v", err)
	}

	// A link resolves to the wish it shows; one not on this machine says so.
	for _, c2 := range []struct {
		l    link.Link
		wish string
		code connect.Code
	}{
		{link.Link{Kind: link.Tilasm, ID: tilasm.GetId()}, wish, 0},
		{link.Link{Kind: link.Wish, ID: wish}, wish, 0},
		{link.Link{Kind: link.Tilasm, ID: store.NewID()}, "", connect.CodeNotFound},
		{link.Link{Kind: link.Wish, ID: store.NewID()}, "", connect.CodeNotFound},
	} {
		got, err := Linked(ctx, c.store, c2.l)
		if got != c2.wish || (c2.code == 0) != (err == nil) || err != nil && connect.CodeOf(err) != c2.code {
			t.Errorf("Linked(%v) = %q, %v", c2.l, got, err)
		}
		if err != nil && !strings.Contains(err.Error(), "on this machine") {
			t.Errorf("Linked(%v): %v, want it to say it is not on this machine", c2.l, err)
		}
	}

	// Without a window, open says so; without http, get gives no address.
	bare := serve(t, WithHome(t.TempDir()))
	wish = bare.wish(t)
	putTilasm(t, bare, &planv1.TilasmServicePutRequest{Path: folder(t, map[string]string{"index.html": page("B", "b")}), Wish: wish})
	if _, err := bare.tilasms.Open(ctx, connect.NewRequest(&planv1.TilasmServiceOpenRequest{Tilasm: ref("L01")})); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("open without a window: %v", err)
	}
	if got := getTilasm(t, bare, "L01", 0); got.GetUrl() != "" || !strings.HasPrefix(got.GetLink(), "djinn://tilasm/") {
		t.Fatalf("get without http: %v", got)
	}
}

package docsite

import (
	"bytes"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/empowill/djinn/internal/cli"
	"github.com/empowill/djinn/locales"
)

// TestFS checks the site as FS makes it: index.html with its markers filled and no page in it, a script per page of
// Nav in pages/, the Command line's with the commands, in ASCII; each seeks, as http.FileServerFS needs, is listed for
// a walk, as os.CopyFS walks, and is served.
func TestFS(t *testing.T) {
	fsys := FS(embedded(t))
	b, err := fs.ReadFile(fsys, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{NavMarker, PagesMarker, LabelsMarker} {
		if bytes.Contains(b, []byte(m)) {
			t.Errorf("%s is left in index.html", m)
		}
	}
	if bytes.Contains(b, []byte(`id="cmd-wish-make"`)) || bytes.Contains(b, []byte(`id="wish-granting-a-wish"`)) {
		t.Error("index.html holds the pages: they are read when opened")
	}
	f, err := fsys.Open("index.html")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.(io.Seeker); !ok {
		t.Error("index.html does not seek: http.FileServerFS would refuse it")
	}
	f.Close()
	script, err := fs.ReadFile(fsys, "pages/commands.js")
	if err != nil {
		t.Fatal(err)
	}
	if slug, html := pageOf(t, script); slug != "commands" || html != string(Commands()) {
		t.Errorf("pages/commands.js hands %s %.80q", slug, html)
	}
	wish, err := fs.ReadFile(fsys, "pages/wish.js")
	if err != nil {
		t.Fatal(err)
	}
	if slug, html := pageOf(t, wish); slug != "wish" || !strings.Contains(html, `<h1 id="wish-title">Wish</h1>`) {
		t.Errorf("pages/wish.js hands %s %.200q", slug, html)
	}
	for i, c := range append(script, wish...) {
		if c >= 0x80 {
			t.Fatalf("a script is not ASCII at byte %d", i)
		}
	}
	if _, err := fsys.Open("pages/nothing.js"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("pages/nothing.js: %v, want not exist", err)
	}
	// What a walk finds, as os.CopyFS walks: the site, index.html made, each page's script.
	out := t.TempDir()
	if err := os.CopyFS(out, fsys); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string][]byte{"index.html": b, "pages/wish.js": wish, "pages/commands.js": script} {
		if copied, err := os.ReadFile(filepath.Join(out, name)); err != nil || !bytes.Equal(copied, want) {
			t.Errorf("copied %s: %v, %d bytes of %d", name, err, len(copied), len(want))
		}
	}
	if err := fstest.TestFS(fsys, "index.html", "site.css", "pages/wish.js", "pages/commands.js"); err != nil {
		t.Error(err)
	}
	for name, want := range map[string][]byte{"/": b, "/pages/wish.js": wish} {
		rec := httptest.NewRecorder()
		http.FileServerFS(fsys).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, name, nil))
		if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), want) {
			t.Errorf("%s served %d, %.80q", name, rec.Code, rec.Body.String())
		}
	}
}

// embedded is the site as djinn up embeds it (assets.go): its files in memory, a folder listed as it is opened. Read
// from the disk, fstest.TestFS checks what the file system says of a folder, not the Grimoire: on the Windows runner,
// the site's fonts and pages folders are listed with size 0 and opened with size 4096 (W270).
func embedded(t *testing.T) fstest.MapFS {
	t.Helper()
	disk := os.DirFS(filepath.Join("..", "..", "docs", "site"))
	site := fstest.MapFS{}
	err := fs.WalkDir(disk, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(disk, name)
		site[name] = &fstest.MapFile{Data: b, Mode: 0o444}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return site
}

// pageOf reads a page's script: the slug and the HTML it hands to site.js.
func pageOf(t *testing.T, script []byte) (slug, html string) {
	t.Helper()
	m := regexp.MustCompile(`(?s)^grimoire\.page\(("[^"]*"), (".*")\);\n$`).FindSubmatch(script)
	if m == nil {
		t.Fatalf("not a page's script: %.80q", script)
	}
	if err := json.Unmarshal(m[1], &slug); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(m[2], &html); err != nil {
		t.Fatal(err)
	}
	return slug, html
}

// TestCommands checks the Command line page: every command of cli.Reference has its entry, under a unique anchor that
// the table of contents links to, with its usage, and every argument and flag in its table.
func TestCommands(t *testing.T) {
	page := string(Commands())
	ids := map[string]int{}
	for _, m := range regexp.MustCompile(` id="([^"]+)"`).FindAllStringSubmatch(page, -1) {
		ids[m[1]]++
	}
	for id, n := range ids {
		if n > 1 {
			t.Errorf("id %s is given %d times", id, n)
		}
	}
	for _, m := range regexp.MustCompile(` href="#([^"]+)"`).FindAllStringSubmatch(page, -1) {
		if ids[m[1]] == 0 {
			t.Errorf("#%s is linked and no element has it", m[1])
		}
	}
	count := 0
	for _, g := range cli.Reference() {
		if ids[anchor("group", g.Name)] == 0 {
			t.Errorf("group %s has no section", g.Name)
		}
		for _, c := range g.Commands {
			count++
			id := anchor("cmd", c.Name)
			start := strings.Index(page, `id="`+id+`"`)
			if start < 0 {
				t.Errorf("djinn %s has no entry", c.Name)
				continue
			}
			entry, _, _ := strings.Cut(page[start:], "</article>")
			if !strings.Contains(entry, ">"+escaped(c.Usage)+"</code>") || !strings.Contains(page, `href="#`+id+`"`) {
				t.Errorf("djinn %s: no usage, or not in the table of contents", c.Name)
			}
			for _, p := range append(c.Args, c.Flags...) {
				if !strings.Contains(entry, "<code>"+escaped(p.Name)+"</code>") {
					t.Errorf("djinn %s: %s is not in its table", c.Name, p.Name)
				}
			}
		}
	}
	if count < 50 {
		t.Errorf("only %d commands", count)
	}
	for _, want := range []string{"or djinn talisman", "MCP tool wish_make", "$DJINN_TASK_ID", "<code>yes</code>"} {
		if !strings.Contains(page, want) {
			t.Errorf("the page does not say %q", want)
		}
	}
}

// escaped is s as html/template writes it in text.
func escaped(s string) string {
	var b strings.Builder
	if err := template.Must(template.New("").Parse("{{.}}")).Execute(&b, s); err != nil {
		panic(err)
	}
	return b.String()
}

// TestSiteHasTheMarkers checks that the site's page takes the Grimoire's navigation, shells and words, each once.
func TestSiteHasTheMarkers(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "site", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{NavMarker, PagesMarker, LabelsMarker} {
		if n := strings.Count(string(b), m); n != 1 {
			t.Errorf("docs/site/index.html has %s %d times, want 1", m, n)
		}
	}
}

// TestGrimoire checks the site as djinn serves it: a page for every entry of the navigation and an entry for every page,
// Introduction first, no code on any page, ids given once in the whole book, every link inside the site leading
// somewhere, every id of a page found in it by site.js before the page is filled (owner), and the navigation's words in
// every language.
func TestGrimoire(t *testing.T) {
	site := os.DirFS(filepath.Join("..", "..", "docs", "site"))
	b, err := fs.ReadFile(FS(site), "index.html")
	if err != nil {
		t.Fatal(err)
	}
	index := string(b)
	if first := Nav[0].Pages[0].Slug; first != "introduction" {
		t.Errorf("the first page is %s, want introduction", first)
	}
	idsOf := func(html string) []string {
		var ids []string
		for _, m := range regexp.MustCompile(` id="([^"]+)"`).FindAllStringSubmatch(html, -1) {
			ids = append(ids, m[1])
		}
		return ids
	}
	ids := map[string]int{}
	for _, id := range idsOf(index) {
		ids[id]++
	}
	book := index
	for _, p := range all() {
		html, err := PageHTML(site, p.Slug)
		if err != nil {
			t.Fatal(err)
		}
		book += string(html)
		for _, id := range idsOf(string(html)) {
			ids[id]++
			if got := owner(id); got != p.Slug {
				t.Errorf("#%s is on %s, site.js would look for it on %q", id, p.Slug, got)
			}
		}
		// Djinn is used without code: its pages explain with words.
		for _, tag := range []string{"<pre", "<code"} {
			if p.Slug != commandsSlug && strings.Contains(string(html), tag) {
				t.Errorf("page %s shows code (%s): explain it with words", p.Slug, tag)
			}
		}
	}
	for id, n := range ids {
		if n > 1 {
			t.Errorf("id %s is given %d times", id, n)
		}
	}
	for _, m := range regexp.MustCompile(` href="#([^"]*)"`).FindAllStringSubmatch(book, -1) {
		if ids[m[1]] == 0 {
			t.Errorf("#%s is linked and no element has it", m[1])
		}
	}
	files, err := fs.Glob(site, pagesDir+"/*.md")
	if err != nil {
		t.Fatal(err)
	}
	inNav := map[string]bool{}
	for _, g := range Nav {
		if locales.T(locales.Source, g.Key, nil) == g.Key {
			t.Errorf("group %s has no text in locales/en.json", g.Key)
		}
		for _, p := range g.Pages {
			inNav[p.Slug] = true
			if locales.T(locales.Source, p.Key, nil) == p.Key {
				t.Errorf("page %s: %s has no text in locales/en.json", p.Slug, p.Key)
			}
			if !strings.Contains(index, `<section id="`+p.Slug+`" class="page`) {
				t.Errorf("page %s has no section", p.Slug)
			}
			if !strings.Contains(index, `<a href="#`+p.Slug+`" data-text="`+p.Key+`">`) {
				t.Errorf("page %s is not in the navigation", p.Slug)
			}
		}
	}
	for _, f := range files {
		if slug := strings.TrimSuffix(path.Base(f), ".md"); !inNav[slug] {
			t.Errorf("%s is in no group of Nav", f)
		}
	}
	var labels map[string]map[string]string
	start := strings.Index(index, `<script type="application/json" id="labels">`)
	if start < 0 {
		t.Fatal("no labels")
	}
	raw, _, _ := strings.Cut(index[start+len(`<script type="application/json" id="labels">`):], "</script>")
	if err := json.Unmarshal([]byte(raw), &labels); err != nil {
		t.Fatalf("labels: %v", err)
	}
	if got, want := labels["fr"]["docs.title"], locales.T("fr", "docs.title", nil); got != want || got == labels["en"]["docs.title"] {
		t.Errorf("fr docs.title %q, want %q apart from the English", got, want)
	}
	for _, key := range regexp.MustCompile(`data-(?:text|title|label)="([^"]+)"`).FindAllStringSubmatch(book, -1) {
		if _, ok := labels["en"][key[1]]; !ok {
			t.Errorf("%s is shown and not in the labels", key[1])
		}
	}
}

// owner is the page site.js looks on for an id its page has not filled yet (owner in docs/site/site.js): the
// Command line's for a command, a group or the global flags, else the longest slug the id starts with, and a hyphen.
func owner(id string) string {
	if strings.HasPrefix(id, "cmd-") || strings.HasPrefix(id, "group-") || id == "global-flags" {
		return commandsSlug
	}
	best := ""
	for _, p := range all() {
		if strings.HasPrefix(id, p.Slug+"-") && len(p.Slug) > len(best) {
			best = p.Slug
		}
	}
	return best
}

// TestPageSections checks how a page reads: its title out of its body, and its sections with ids of its own.
func TestPageSections(t *testing.T) {
	w := written{Page: Page{Slug: "wish", Key: "docs.page_wish"}}
	if err := w.render([]byte("# Wish\n\nWhat you want.\n\n## Paused *wishes*\n\nText.\n\n## Paused wishes\n")); err != nil {
		t.Fatal(err)
	}
	if w.Title != "Wish" || strings.Contains(string(w.Body), "<h1") {
		t.Errorf("title %q, body %s", w.Title, w.Body)
	}
	want := []section{{"wish-paused-wishes", "Paused wishes"}, {"wish-paused-wishes-2", "Paused wishes"}}
	if !slices.Equal(w.Sections, want) {
		t.Errorf("sections %v, want %v", w.Sections, want)
	}
	if err := w.render([]byte("No title\n")); err == nil {
		t.Error("a page without a title renders")
	}
}

package docsite

import (
	"bytes"
	"encoding/json"
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

func TestFSFillsTheCommands(t *testing.T) {
	site := fstest.MapFS{
		"index.html":    {Data: []byte("<main>" + Marker + "</main>")},
		"site.css":      {Data: []byte("body{}")},
		"fonts/a.woff2": {Data: []byte("font")},
	}
	fsys := FS(site)
	b, err := fs.ReadFile(fsys, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if want := "<main>" + string(Commands()) + "</main>"; string(b) != want {
		t.Errorf("index.html %.200q", b)
	}
	f, err := fsys.Open("index.html")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.(io.Seeker); !ok {
		t.Error("index.html does not seek: http.FileServerFS would refuse it")
	}
	f.Close()
	// What a walk finds, as os.CopyFS walks: the site, index.html with its commands.
	out := t.TempDir()
	if err := os.CopyFS(out, fsys); err != nil {
		t.Fatal(err)
	}
	if copied, err := os.ReadFile(filepath.Join(out, "index.html")); err != nil || !bytes.Equal(copied, b) {
		t.Errorf("copied index.html: %v, %d bytes of %d", err, len(copied), len(b))
	}
	if err := fstest.TestFS(fsys, "index.html", "site.css", "fonts/a.woff2"); err != nil {
		t.Error(err)
	}
	rec := httptest.NewRecorder()
	http.FileServerFS(fsys).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != string(b) {
		t.Errorf("served %d, %.80q", rec.Code, rec.Body.String())
	}
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

// TestSiteHasTheMarkers checks that the site's page takes the commands and the Grimoire, each once.
func TestSiteHasTheMarkers(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "site", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{Marker, NavMarker, PagesMarker, LabelsMarker} {
		if n := strings.Count(string(b), m); n != 1 {
			t.Errorf("docs/site/index.html has %s %d times, want 1", m, n)
		}
	}
}

// TestGrimoire checks the site as djinn serves it: a page for every entry of the navigation and an entry for every page,
// Introduction first, no code on any page, ids given once, every link inside the site leading somewhere, and the
// navigation's words in every language.
func TestGrimoire(t *testing.T) {
	site := os.DirFS(filepath.Join("..", "..", "docs", "site"))
	b, err := fs.ReadFile(FS(site), "index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(b)
	for _, m := range []string{Marker, NavMarker, PagesMarker, LabelsMarker} {
		if strings.Contains(page, m) {
			t.Errorf("%s is left in the page", m)
		}
	}
	if first := Nav[0].Pages[0].Slug; first != "introduction" {
		t.Errorf("the first page is %s, want introduction", first)
	}
	ids := map[string]int{}
	for _, m := range regexp.MustCompile(` id="([^"]+)"`).FindAllStringSubmatch(page, -1) {
		ids[m[1]]++
	}
	for id, n := range ids {
		if n > 1 {
			t.Errorf("id %s is given %d times", id, n)
		}
	}
	for _, m := range regexp.MustCompile(` href="#([^"]*)"`).FindAllStringSubmatch(page, -1) {
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
			if !strings.Contains(page, `<section id="`+p.Slug+`" class="page`) {
				t.Errorf("page %s has no section", p.Slug)
			}
			if !strings.Contains(page, `<a href="#`+p.Slug+`" data-text="`+p.Key+`">`) {
				t.Errorf("page %s is not in the navigation", p.Slug)
			}
		}
	}
	for _, f := range files {
		if slug := strings.TrimSuffix(path.Base(f), ".md"); !inNav[slug] {
			t.Errorf("%s is in no group of Nav", f)
		}
	}
	// Djinn is used without code: its pages explain with words.
	pages, err := Pages(site)
	if err != nil {
		t.Fatal(err)
	}
	for _, tag := range []string{"<pre", "<code"} {
		if strings.Contains(string(pages), tag) {
			t.Errorf("a page shows code (%s): explain it with words", tag)
		}
	}
	var labels map[string]map[string]string
	start := strings.Index(page, `<script type="application/json" id="labels">`)
	if start < 0 {
		t.Fatal("no labels")
	}
	raw, _, _ := strings.Cut(page[start+len(`<script type="application/json" id="labels">`):], "</script>")
	if err := json.Unmarshal([]byte(raw), &labels); err != nil {
		t.Fatalf("labels: %v", err)
	}
	if got, want := labels["fr"]["docs.title"], locales.T("fr", "docs.title", nil); got != want || got == labels["en"]["docs.title"] {
		t.Errorf("fr docs.title %q, want %q apart from the English", got, want)
	}
	for _, key := range regexp.MustCompile(`data-(?:text|title|label)="([^"]+)"`).FindAllStringSubmatch(page, -1) {
		if _, ok := labels["en"][key[1]]; !ok {
			t.Errorf("%s is shown and not in the labels", key[1])
		}
	}
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

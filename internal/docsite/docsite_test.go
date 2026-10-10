package docsite

import (
	"bytes"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/empowill/djinn/internal/cli"
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

// TestSiteHasTheMarker checks that the site's page takes the commands, once.
func TestSiteHasTheMarker(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "site", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(b), Marker); n != 1 {
		t.Errorf("docs/site/index.html has the marker %d times, want 1", n)
	}
}

// TestCommands checks the Command line tab: every command of cli.Reference has its entry, under a unique anchor that
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

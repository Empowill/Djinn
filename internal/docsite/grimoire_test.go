package docsite

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/empowill/djinn/locales"
)

// TestNav checks the navigation against the site as djinn serves it: Introduction first, a page for every entry and an
// entry for every page, its words in locales/en.json, a section and a link in index.html for each page.
func TestNav(t *testing.T) {
	t.Parallel()
	index, _ := book(t)
	if first := Nav[0].Pages[0].Slug; first != "introduction" {
		t.Errorf("the first page is %s, want introduction", first)
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
}

// TestPageHTML checks the pages as djinn serves them: no code on any page, ids given once in the whole book, every
// link inside the site leading somewhere, every id of a page found in it by site.js before the page is filled (owner).
func TestPageHTML(t *testing.T) {
	t.Parallel()
	index, pages := book(t)
	ids := map[string]int{}
	for _, id := range idsOf(index) {
		ids[id]++
	}
	all := index
	for _, p := range pages {
		all += p.html
		for _, id := range idsOf(p.html) {
			ids[id]++
		}
	}
	for _, p := range pages {
		t.Run(p.slug, func(t *testing.T) {
			t.Parallel()
			for _, id := range idsOf(p.html) {
				if got := owner(id); got != p.slug {
					t.Errorf("#%s is on %s, site.js would look for it on %q", id, p.slug, got)
				}
			}
			// Djinn is used without code: its pages explain with words.
			for _, tag := range []string{"<pre", "<code"} {
				if p.slug != commandsSlug && strings.Contains(p.html, tag) {
					t.Errorf("page %s shows code (%s): explain it with words", p.slug, tag)
				}
			}
		})
	}
	t.Run("ids once", func(t *testing.T) {
		t.Parallel()
		for id, n := range ids {
			if n > 1 {
				t.Errorf("id %s is given %d times", id, n)
			}
		}
	})
	t.Run("links lead somewhere", func(t *testing.T) {
		t.Parallel()
		for _, m := range regexp.MustCompile(` href="#([^"]*)"`).FindAllStringSubmatch(all, -1) {
			if ids[m[1]] == 0 {
				t.Errorf("#%s is linked and no element has it", m[1])
			}
		}
	})
	t.Run("unknown page", func(t *testing.T) {
		t.Parallel()
		if _, err := PageHTML(site, "nothing"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("an unknown page: %v, want not exist", err)
		}
	})
}

// TestLabels checks the words index.html takes: the navigation's in every language, and every key a page shows.
func TestLabels(t *testing.T) {
	t.Parallel()
	index, pages := book(t)
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
	all := index
	for _, p := range pages {
		all += p.html
	}
	for _, key := range regexp.MustCompile(`data-(?:text|title|label)="([^"]+)"`).FindAllStringSubmatch(all, -1) {
		if _, ok := labels["en"][key[1]]; !ok {
			t.Errorf("%s is shown and not in the labels", key[1])
		}
	}
}

// TestWritten_render checks how a page reads: its title out of its body, and its sections with ids of its own.
func TestWritten_render(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, src string
		title     string // "" when the page must not render.
		sections  []section
	}{
		{"title and sections", "# Wish\n\nWhat you want.\n\n## Paused *wishes*\n\nText.\n\n## Paused wishes\n", "Wish",
			[]section{{"wish-paused-wishes", "Paused wishes"}, {"wish-paused-wishes-2", "Paused wishes"}}},
		{"no title", "No title\n", "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := written{Page: Page{Slug: "wish", Key: "docs.page_wish"}}
			err := w.render([]byte(tc.src))
			if tc.title == "" {
				if err == nil {
					t.Error("a page without a title renders")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if w.Title != tc.title || strings.Contains(string(w.Body), "<h1") {
				t.Errorf("title %q, body %s", w.Title, w.Body)
			}
			if !slices.Equal(w.Sections, tc.sections) {
				t.Errorf("sections %v, want %v", w.Sections, tc.sections)
			}
		})
	}
}

// page is a page of the book as PageHTML renders it.
type page struct{ slug, html string }

// book is the site as djinn serves it: index.html, as FS makes it, and every page of Nav. The tests share one
// reading.
func book(t *testing.T) (index string, pages []page) {
	t.Helper()
	b, err := readBook()
	if err != nil {
		t.Fatal(err)
	}
	return b.index, b.pages
}

var readBook = sync.OnceValues(func() (b struct {
	index string
	pages []page
}, err error) {
	index, err := fs.ReadFile(FS(site), "index.html")
	if err != nil {
		return b, err
	}
	b.index = string(index)
	for _, p := range all() {
		html, err := PageHTML(site, p.Slug)
		if err != nil {
			return b, err
		}
		b.pages = append(b.pages, page{p.Slug, string(html)})
	}
	return b, nil
})

// site is the site's folder.
var site = os.DirFS(filepath.Join("..", "..", "docs", "site"))

// idsOf are the ids given in html.
func idsOf(html string) []string {
	var ids []string
	for _, m := range regexp.MustCompile(` id="([^"]+)"`).FindAllStringSubmatch(html, -1) {
		ids = append(ids, m[1])
	}
	return ids
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

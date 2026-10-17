package docsite

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"strings"
	"unicode"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"

	"github.com/empowill/djinn/locales"
)

// The Grimoire is the documentation: a navigation on the left, and beside it the splash page, then the page the reader
// opens. Each page is a Markdown file of docs/site/pages, written in English, with no code: Djinn is used without code.
// The navigation's words are locale keys, shown in the window's language (site.js reads ?lang=).
//
// index.html holds the splash, the navigation and an empty shell per page, nothing more: the pages, 9,000 elements with
// the Command line, took four seconds to read in the window's engine before the splash showed. Each page is a script
// of its own, pages/<slug>.js, that hands its HTML to site.js: loaded when the reader opens the page, the others in idle
// time, and put in the page only when it is opened. A script, not a fetch: the site opens from a file too, where a
// fetch is refused and a script loads.
const (
	// NavMarker is where index.html takes the navigation.
	NavMarker = "<!-- djinn:nav -->"
	// PagesMarker is where index.html takes the pages' shells, before the Command line's.
	PagesMarker = "<!-- djinn:pages -->"
	// LabelsMarker is where index.html takes the navigation's words in every language, as JSON.
	LabelsMarker = "<!-- djinn:labels -->"
	// pagesDir holds one Markdown file per page, named by its slug, and, as djinn serves the site, its script.
	pagesDir = "pages"
	// scriptExt ends the name of a page's script: pages/<slug>.js.
	scriptExt = ".js"
	// commandsSlug is the Command line's page: the reference of the commands, built by Commands, not a Markdown file.
	commandsSlug = "commands"
)

// Group is a part of the navigation: its title's key, and its pages in reading order.
type Group struct {
	Key   string
	Pages []Page
}

// Page is one page of the Grimoire: its slug, which is its anchor and its file, pages/<slug>.md, and its title's key in
// the navigation.
type Page struct {
	Slug string
	Key  string
}

// Nav is the Grimoire's navigation: Introduction first, the page shown when the reader leaves the splash, then every
// concept of Djinn, then the command line. A concept added to Djinn takes its page here, its file, and its key in every
// locale.
var Nav = []Group{
	{"docs.group_start", []Page{
		{"introduction", "docs.page_introduction"},
		{"getting-started", "docs.page_getting_started"},
	}},
	{"docs.group_plan", []Page{
		{"wish", "docs.page_wish"},
		{"azima", "docs.page_azima"},
		{"task-and-worker", "docs.page_task_and_worker"},
		{"dependencies", "docs.page_dependencies"},
		{"watcher", "docs.page_watcher"},
		{"investigation", "docs.page_investigation"},
		{"mockup", "docs.page_mockup"},
		{"decision", "docs.page_decision"},
		{"block", "docs.page_block"},
	}},
	{"docs.group_code", []Page{
		{"project-and-settings", "docs.page_project_and_settings"},
		{"integration-branch", "docs.page_integration_branch"},
		{"merges-and-corrections", "docs.page_merges_and_corrections"},
		{"push", "docs.page_push"},
		{"push-strategy", "docs.page_push_strategy"},
		{"gates", "docs.page_gates"},
		{"pull-requests", "docs.page_pull_requests"},
	}},
	{"docs.group_djinn", []Page{
		{"the-djinn", "docs.page_the_djinn"},
		{"question-and-move", "docs.page_question_and_move"},
		{"enlighten", "docs.page_enlighten"},
		{"tilasm", "docs.page_tilasm"},
		{"lead-notes", "docs.page_lead_notes"},
		{"skills-and-templates", "docs.page_skills_and_templates"},
		{"ifrit", "docs.page_ifrit"},
	}},
	{"docs.group_window", []Page{
		{"the-window", "docs.page_the_window"},
		{"inbox", "docs.page_inbox"},
		{"review", "docs.page_review"},
		{"rights", "docs.page_rights"},
		{"machine-and-load", "docs.page_machine_and_load"},
		{"usage-and-cost", "docs.page_usage_and_cost"},
		{"sharing", "docs.page_sharing"},
		{"restarts-and-updates", "docs.page_restarts_and_updates"},
	}},
	{"docs.group_reference", []Page{
		{commandsSlug, "docs.page_commands"},
	}},
}

// chrome are the keys of the site's own words, outside the navigation's groups and pages: index.html marks each element
// that shows one with data-text (its text) or data-title (its tooltip).
var chrome = []string{"docs.title", "docs.subtitle", "docs.on_this_page", "docs.previous", "docs.next"}

//go:embed grimoire.html
var grimoireTemplate string

var grimoire = template.Must(template.New("grimoire").Funcs(template.FuncMap{
	"en": func(key string) string { return locales.T(locales.Source, key, nil) },
}).Parse(grimoireTemplate))

var markdown = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithParserOptions(parser.WithAutoHeadingID()),
)

// written is a page as the site shows it: its title, its HTML, its sections for "On this page", and its neighbours.
type written struct {
	Page
	Title    string
	Body     template.HTML
	Sections []section
	Prev     *Page
	Next     *Page
}

type section struct {
	ID    string
	Title string
}

// all is every page of Nav, in reading order.
func all() []Page {
	var pages []Page
	for _, g := range Nav {
		pages = append(pages, g.Pages...)
	}
	return pages
}

// Shells are the pages' empty sections, in index.html: what the navigation links to, filled by site.js from each
// page's script when the reader opens it. The Command line's is index.html's own.
func Shells() []byte {
	var shells []Page
	for _, p := range all() {
		if p.Slug != commandsSlug {
			shells = append(shells, p)
		}
	}
	var b bytes.Buffer
	if err := grimoire.ExecuteTemplate(&b, "shells", shells); err != nil {
		panic(err) // The template and its data are fixed: a test executes it.
	}
	return b.Bytes()
}

// PageHTML is what a page's shell takes: the page rendered from site's pages folder, with what is on it and the pages
// around it; the Command line's reference for its own. An unknown slug is fs.ErrNotExist.
func PageHTML(site fs.FS, slug string) ([]byte, error) {
	if slug == commandsSlug {
		return Commands(), nil
	}
	pages := all()
	for i, p := range pages {
		if p.Slug != slug {
			continue
		}
		w := written{Page: p}
		if i > 0 {
			w.Prev = &pages[i-1]
		}
		if i+1 < len(pages) {
			w.Next = &pages[i+1]
		}
		src, err := fs.ReadFile(site, pagesDir+"/"+p.Slug+".md")
		if err != nil {
			return nil, err
		}
		if err := w.render(src); err != nil {
			return nil, fmt.Errorf("%s/%s.md: %w", pagesDir, p.Slug, err)
		}
		var b bytes.Buffer
		err = grimoire.ExecuteTemplate(&b, "page", w)
		return b.Bytes(), err
	}
	return nil, fs.ErrNotExist
}

// PageScript is a page's script, pages/<slug>.js: it hands the page's HTML to site.js. Its text is ASCII: a browser may
// read a script with no charset as Latin-1.
func PageScript(site fs.FS, slug string) ([]byte, error) {
	page, err := PageHTML(site, slug)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.WriteString("grimoire.page(")
	for _, v := range []string{slug, string(page)} {
		e := json.NewEncoder(&b)
		e.SetEscapeHTML(false) // A file of its own: no script element to close.
		if err := e.Encode(v); err != nil {
			return nil, err
		}
		b.Truncate(b.Len() - 1) // Encode ends with a newline.
		b.WriteString(", ")
	}
	b.Truncate(b.Len() - 2)
	b.WriteString(");\n")
	return ascii(b.Bytes()), nil
}

// ascii writes every character of a script outside ASCII as a JavaScript escape: they are all inside strings.
func ascii(b []byte) []byte {
	var out bytes.Buffer
	for _, r := range string(b) {
		switch {
		case r < 0x80:
			out.WriteRune(r)
		case r <= 0xffff:
			fmt.Fprintf(&out, `\u%04x`, r)
		default:
			r -= 0x10000
			fmt.Fprintf(&out, `\u%04x\u%04x`, 0xd800+(r>>10), 0xdc00+(r&0x3ff))
		}
	}
	return out.Bytes()
}

// render reads a page's Markdown: its first heading is its title, its second-level headings its sections, each with
// an id that starts with the page's slug, so that no two pages give one id.
func (w *written) render(src []byte) error {
	ctx := parser.NewContext(parser.WithIDs(&ids{prefix: w.Slug, seen: map[string]bool{}}))
	doc := markdown.Parser().Parse(text.NewReader(src), parser.WithContext(ctx))
	if h, ok := doc.FirstChild().(*ast.Heading); ok && h.Level == 1 {
		w.Title = plain(h, src)
		doc.RemoveChild(doc, h)
	} else {
		return fmt.Errorf("no title: the page starts with a first-level heading")
	}
	err := ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if h, ok := n.(*ast.Heading); ok && entering && h.Level == 2 {
			id, _ := h.AttributeString("id")
			w.Sections = append(w.Sections, section{ID: string(id.([]byte)), Title: plain(h, src)})
		}
		return ast.WalkContinue, nil
	})
	if err != nil {
		return err
	}
	var b bytes.Buffer
	if err := markdown.Renderer().Render(&b, src, doc); err != nil {
		return err
	}
	w.Body = template.HTML(b.String()) //nolint:gosec // goldmark leaves raw HTML and dangerous links out.
	return nil
}

// plain is the text of a heading, without its marks.
func plain(n ast.Node, src []byte) string {
	var b strings.Builder
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if t, ok := c.(*ast.Text); ok && entering {
			b.Write(t.Segment.Value(src))
		}
		return ast.WalkContinue, nil
	})
	return b.String()
}

// ids gives each heading of a page an id made of the page's slug and the heading's words: wish-paused-wishes.
type ids struct {
	prefix string
	seen   map[string]bool
}

func (s *ids) Generate(value []byte, _ ast.NodeKind) []byte {
	base := s.prefix + "-" + slug(string(value))
	id := base
	for i := 2; s.seen[id]; i++ {
		id = fmt.Sprintf("%s-%d", base, i)
	}
	s.seen[id] = true
	return []byte(id)
}

func (s *ids) Put(value []byte) { s.seen[string(value)] = true }

// slug is words in lower case, joined by hyphens.
func slug(words string) string {
	var b strings.Builder
	for _, f := range strings.FieldsFunc(strings.ToLower(words), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if b.Len() > 0 {
			b.WriteByte('-')
		}
		b.WriteString(f)
	}
	return b.String()
}

// NavHTML is the navigation, in English: site.js puts the reader's language in place.
func NavHTML() []byte {
	var b bytes.Buffer
	if err := grimoire.ExecuteTemplate(&b, "nav", Nav); err != nil {
		panic(err) // The template and its data are fixed: a test executes it.
	}
	return b.Bytes()
}

// Labels are the navigation's and the site's words in every language Djinn speaks, by language then key, as JSON that
// stays text inside a script element.
func Labels() []byte {
	keys := append([]string{}, chrome...)
	for _, g := range Nav {
		keys = append(keys, g.Key)
		for _, p := range g.Pages {
			keys = append(keys, p.Key)
		}
	}
	all := map[string]map[string]string{}
	for _, language := range locales.Languages() {
		texts := map[string]string{}
		for _, k := range keys {
			texts[k] = locales.T(language, k, nil)
		}
		all[language] = texts
	}
	b, err := json.Marshal(all) // It escapes <, > and &: nothing closes the script element.
	if err != nil {
		panic(err)
	}
	return b
}

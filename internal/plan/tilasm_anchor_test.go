package plan

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

const (
	anchored  = planv1.TilasmAnchorState_TILASM_ANCHOR_STATE_ANCHORED
	changed   = planv1.TilasmAnchorState_TILASM_ANCHOR_STATE_CHANGED
	missing   = planv1.TilasmAnchorState_TILASM_ANCHOR_STATE_MISSING
	ambiguous = planv1.TilasmAnchorState_TILASM_ANCHOR_STATE_AMBIGUOUS
)

// TestRenderedText: the rendered text is the Markdown without its markup, its blocks one space apart, mapped back to
// the source of each character.
func TestRenderedText(t *testing.T) {
	for _, c := range []struct{ name, md, want string }{
		{"marks", "Some **bold**, *em* and ~~struck~~ text.", "Some bold, em and struck text."},
		{"links and images", "See [the docs](http://x.org \"t\") ![a diagram](d.svg) and <http://y.org>.", "See the docs and http://y.org."},
		{"code span", "Run `go test ./...` now.", "Run go test ./... now."},
		{"escapes and entities", `A \*star\* &amp; &#233;t&eacute;`, "A *star* & été"},
		{"headings and paragraphs", "# Title #\n\nFirst\nline.\n\n## Part\nSecond.", "Title First line. Part Second."},
		{"hard break", "One  \nTwo\\\nThree", "One Two Three"},
		{"lists", "- one\n- two\n  1. nested\n\n- [ ] task", "one two nested task"},
		{"table", "| a | b \\| c |\n|---|---|\n| `1` | **2** |", "a b | c 1 2"},
		{"code block", "Before\n\n```go\nfunc x() {\n\treturn\n}\n```\n\n    indented\n\nAfter", "Before func x() { return } indented After"},
		{"raw html and rules", "<div>\nhidden\n</div>\n\nShown <b>bold</b>\n\n---\n\nEnd", "Shown bold End"},
		{"quote", "> Quoted\n> text", "Quoted text"},
		{"empty", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := renderMarkdown([]byte(c.md))
			if r.text != c.want {
				t.Errorf("text = %q, want %q", r.text, c.want)
			}
			if len(r.start) != len(r.text) || len(r.end) != len(r.text) {
				t.Fatalf("%d bytes of text, %d starts, %d ends", len(r.text), len(r.start), len(r.end))
			}
		})
	}
}

// TestRenderedTextParity: the cases the window's rendered text must match too (tests/tilasm-anchor.test.mjs reads the
// same file): a live HTML block gives no text, any other code block its literal text.
func TestRenderedTextParity(t *testing.T) {
	data, err := os.ReadFile("testdata/tilasm_rendered_text.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct{ Name, Markdown, Text string }
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			if got := renderMarkdown([]byte(c.Markdown)).text; got != c.Text {
				t.Errorf("text = %q, want %q", got, c.Text)
			}
		})
	}
}

// TestResolveAnchor: an anchor resolves to its passage's Markdown source, markup inside it included, around it left
// out; through edits before, after or inside it; and never to an identical passage elsewhere.
func TestResolveAnchor(t *testing.T) {
	type want struct {
		state    planv1.TilasmAnchorState
		original string // the source it resolves to
	}
	for _, c := range []struct {
		name   string
		md     string
		anchor *planv1.TilasmAnchor
		strict bool
		want   want
	}{
		{"plain", "We compare two stores.", &planv1.TilasmAnchor{Quote: "two stores"}, false, want{anchored, "two stores"}},
		{"inside a mark", "We compare **two stores** today.", &planv1.TilasmAnchor{Quote: "two stores"}, false, want{anchored, "two stores"}},
		{"across marks", "We **compare** two *stores* today.", &planv1.TilasmAnchor{Quote: "compare two stores"}, false, want{anchored, "compare** two *stores"}},
		{"link text", "Read [the guide](http://g.org) first.", &planv1.TilasmAnchor{Quote: "the guide first"}, false, want{anchored, "the guide](http://g.org) first"}},
		{"code span", "Run `make all` here.", &planv1.TilasmAnchor{Quote: "make all"}, false, want{anchored, "make all"}},
		{"heading", "# The brief\n\nText.", &planv1.TilasmAnchor{Quote: "The brief"}, false, want{anchored, "The brief"}},
		{"list item", "- alpha\n- beta\n", &planv1.TilasmAnchor{Quote: "beta"}, false, want{anchored, "beta"}},
		{"table cell", "| key | value |\n|---|---|\n| id | a UUID |\n", &planv1.TilasmAnchor{Quote: "a UUID"}, false, want{anchored, "a UUID"}},
		{"across blocks", "First para.\n\nSecond para.", &planv1.TilasmAnchor{Quote: "para.\n\nSecond"}, false, want{anchored, "para.\n\nSecond"}},
		{"escaped", `Use \*args\* here.`, &planv1.TilasmAnchor{Quote: "*args*"}, false, want{anchored, `\*args\*`}},
		{"entity", "Tom &amp; Jerry", &planv1.TilasmAnchor{Quote: "Tom & Jerry"}, false, want{anchored, "Tom &amp; Jerry"}},
		{"blanks differ", "One\ntwo  three", &planv1.TilasmAnchor{Quote: "One two\tthree"}, false, want{anchored, "One\ntwo  three"}},
		{
			"repeated, the prefix says which", "It works. Then it works. Done.",
			&planv1.TilasmAnchor{Quote: "it works", Prefix: "Then "}, false, want{anchored, "it works"},
		},
		{
			"repeated, the suffix says which", "Cache it. Cache it later.",
			&planv1.TilasmAnchor{Quote: "Cache it", Suffix: " later"}, false, want{anchored, "Cache it"},
		},
		{"repeated, nothing says which", "Cache it. Cache it.", &planv1.TilasmAnchor{Quote: "Cache it"}, false, want{ambiguous, ""}},
		{
			"repeated, both contexts the same", "A x B. A x B.",
			&planv1.TilasmAnchor{Quote: "x", Prefix: "A ", Suffix: " B"}, false, want{ambiguous, ""},
		},
		{
			"edit before the passage", "New intro. It works. Then it works. Done.",
			&planv1.TilasmAnchor{Quote: "it works", Prefix: "Then ", Suffix: ". Done."}, true, want{anchored, "it works"},
		},
		{
			"edit after the passage", "It works. Then it works. Done, and more.",
			&planv1.TilasmAnchor{Quote: "it works", Prefix: "It works. Then ", Suffix: ". Done."}, true, want{anchored, "it works"},
		},
		{
			"edit inside the passage", "Intro. We compare three stores. Outro.",
			&planv1.TilasmAnchor{Quote: "compare two stores", Prefix: "Intro. We ", Suffix: ". Outro."}, true,
			want{changed, "compare three stores"},
		},
		{
			"deleted", "Intro. Outro.",
			&planv1.TilasmAnchor{Quote: "We compare.", Prefix: "Intro. ", Suffix: " Outro."}, true, want{missing, ""},
		},
		{
			"rewritten beyond recognition", "Something else entirely.",
			&planv1.TilasmAnchor{Quote: "two stores", Prefix: "We compare ", Suffix: " today."}, true, want{missing, ""},
		},
		{
			"changed where its context repeats", "A one B. A two B.",
			&planv1.TilasmAnchor{Quote: "zero", Prefix: "A ", Suffix: " B"}, true, want{ambiguous, ""},
		},
		{
			"strict: an empty prefix is the start", "Cache it. Cache it.",
			&planv1.TilasmAnchor{Quote: "Cache it", Suffix: ". Cache it."}, true, want{anchored, "Cache it"},
		},
		{
			"edit just before, the quote repeated after", "Brief Intro. We cache it. Then we cache it again. one store two stores The end. Then we cache it later.",
			&planv1.TilasmAnchor{Quote: "cache it", Prefix: "Brief We cache it. Then we ", Suffix: " again. one store two stores The"}, true,
			want{anchored, "cache it"},
		},
		{
			"edited inside, the quote still elsewhere", "Brief Intro. We cache it. Then we store it again. one store two stores The end. Then we cache it later.",
			&planv1.TilasmAnchor{Quote: "cache it", Prefix: "Brief We cache it. Then we ", Suffix: " again. one store two stores The"}, true,
			want{changed, "store it"},
		},
		{
			"deleted, an identical sentence elsewhere", "We cache it. A long paragraph of other things. The end.",
			&planv1.TilasmAnchor{Quote: "We cache it.", Prefix: "of other things.", Suffix: "The end."}, true, want{missing, ""},
		},
		{
			"one of two twins deleted", "Alpha says it works. Omega.",
			&planv1.TilasmAnchor{Quote: "it works", Prefix: "Alpha says it works. Beta says ", Suffix: ". Omega."}, true, want{ambiguous, ""},
		},
		{
			"caret after a word", "Alpha beta.",
			&planv1.TilasmAnchor{Prefix: "Alpha", Suffix: " beta."}, false, want{anchored, ""},
		},
		{"caret in an empty document", "", &planv1.TilasmAnchor{}, true, want{anchored, ""}},
		{"caret nowhere", "Text.", &planv1.TilasmAnchor{}, true, want{ambiguous, ""}},
		{"caret gone", "Text.", &planv1.TilasmAnchor{Prefix: "abc", Suffix: "def"}, true, want{missing, ""}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := renderMarkdown([]byte(c.md))
			got := r.resolve(c.anchor, c.strict)
			if got.state != c.want.state {
				t.Fatalf("state = %v, want %v (text %q)", got.state, c.want.state, r.text)
			}
			if o := r.original(got); o != c.want.original {
				t.Errorf("original = %q, want %q", o, c.want.original)
			}
		})
	}
}

// TestResolveCaret: a place to insert at lands at the end of its prefix, or after the blank that follows it when the
// prefix ends with one: at the start of the next block.
func TestResolveCaret(t *testing.T) {
	md := "# Title\n\nFirst para.\n\nSecond para."
	r := renderMarkdown([]byte(md))
	end := r.resolve(&planv1.TilasmAnchor{Prefix: "First para.", Suffix: "Second"}, true)
	if end.state != anchored || md[:end.src] != "# Title\n\nFirst para." {
		t.Errorf("caret at the end of a block: %v at %d", end.state, end.src)
	}
	start := r.resolve(&planv1.TilasmAnchor{Prefix: "First para. ", Suffix: "Second"}, true)
	if start.state != anchored || md[start.src:] != "Second para." {
		t.Errorf("caret at the start of a block: %v at %d", start.state, start.src)
	}
	last := r.resolve(&planv1.TilasmAnchor{Prefix: "Second para."}, true)
	if last.state != anchored || last.src != len(md) {
		t.Errorf("caret at the end: %v at %d", last.state, last.src)
	}
}

// TestAnchorAt: an anchor taken from a place keeps 32 characters of context, more when an identical passage has the
// same, so that it resolves to its place alone.
func TestAnchorAt(t *testing.T) {
	same := strings.Repeat("filler ", 10)
	md := same + "target " + same + "\n\n" + same + "target " + same + "end"
	r := renderMarkdown([]byte(md))
	second := strings.LastIndex(r.text, "target")
	a := r.anchorAt(r.found(anchored, second, second+len("target")), 3)
	if a.GetQuote() != "target" || a.GetRevision() != 3 {
		t.Fatalf("anchor = %v", a)
	}
	if len(a.GetPrefix()) <= anchorContext {
		t.Errorf("prefix %q: want more than %d characters to tell the passages apart", a.GetPrefix(), anchorContext)
	}
	got := r.resolve(a, true)
	if got.state != anchored || got.at != second {
		t.Errorf("resolves to %v at %d, want the second at %d", got.state, got.at, second)
	}
	first := strings.Index(r.text, "target")
	if a := r.anchorAt(r.found(anchored, first, first+6), 1); r.resolve(a, true).at != first {
		t.Errorf("the first one resolves elsewhere: %v", a)
	}
}

// TestParagraphs: the paragraphs of a place are the source of the blocks it touches.
func TestParagraphs(t *testing.T) {
	md := "# Title\n\nOne **bold** line.\n\nTwo.\n\nThree."
	r := renderMarkdown([]byte(md))
	p := r.resolve(&planv1.TilasmAnchor{Quote: "line. Two"}, false)
	if got := r.paragraphs(p); got != "One **bold** line.\n\nTwo." {
		t.Errorf("paragraphs = %q", got)
	}
}

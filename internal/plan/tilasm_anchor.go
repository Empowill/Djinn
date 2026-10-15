package plan

import (
	"bytes"
	"html"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// anchorContext is how much text, in characters, an anchor keeps on each side of its passage, at least: more when
// that is not enough to tell its passage from an identical one.
const anchorContext = 32

// maxAnchorContext is the most an anchor keeps on each side, as its proto allows.
const maxAnchorContext = 1000

// anchorParser parses a tilasm's Markdown as the window's editor reads it: CommonMark with GitHub's tables, strikethrough,
// task lists and bare links.
var anchorParser = goldmark.New(goldmark.WithExtensions(extension.GFM)).Parser()

// rendered is a Markdown document's text as a reader sees it (TilasmAnchor), each of its bytes mapped back to the
// source bytes it comes from.
type rendered struct {
	source []byte
	text   string
	// start and end are, for each byte of text, the source range of the character it belongs to. A space that stands
	// for a run of white space or for the end of a block spans the source where the run starts, empty.
	start, end []int32
	blocks     []textBlock
}

// textBlock is a block of the document that holds text: a paragraph, a heading, a table cell, a code block.
type textBlock struct {
	src, srcEnd int // its source, markup around it excluded
}

// entity is an HTML character reference, which Markdown reads as its character.
var entity = regexp.MustCompile(`^&(?:#[0-9]{1,7}|#[xX][0-9a-fA-F]{1,6}|[A-Za-z][A-Za-z0-9]{1,31});`)

// renderMarkdown reads the rendered text of a Markdown document.
func renderMarkdown(source []byte) *rendered {
	r := &rendered{source: source}
	b := &renderer{r: r}
	doc := anchorParser.Parse(text.NewReader(source))
	b.block(doc)
	r.text = b.text.String()
	return r
}

type renderer struct {
	r     *rendered
	text  strings.Builder
	space bool  // white space waits to be written, before the next character
	at    int32 // where that white space starts in the source
}

func (b *renderer) block(n ast.Node) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch c.Kind() {
		case ast.KindParagraph, ast.KindTextBlock, ast.KindHeading, east.KindTableCell:
			b.textBlock(c, func() { b.inline(c) })
		case ast.KindFencedCodeBlock, ast.KindCodeBlock:
			b.textBlock(c, func() {
				lines := c.Lines()
				for i := range lines.Len() {
					seg := lines.At(i)
					b.literal(seg.Start, seg.Stop, false)
				}
			})
		case ast.KindHTMLBlock, ast.KindThematicBreak:
			// Raw HTML and rules hold no text a reader sees.
		default:
			b.block(c)
		}
	}
}

// textBlock writes a block's text, between two blank ends.
func (b *renderer) textBlock(n ast.Node, write func()) {
	write()
	src, srcEnd := blockSource(n, b.r.source)
	b.r.blocks = append(b.r.blocks, textBlock{src: src, srcEnd: srcEnd})
	b.blank(int32(srcEnd))
}

// blockSource is the source a block's lines span, without the blanks at its ends.
func blockSource(n ast.Node, source []byte) (int, int) {
	lines := n.Lines()
	if lines.Len() == 0 {
		p := max(n.Pos(), 0)
		return p, p
	}
	start, end := lines.At(0).Start, lines.At(lines.Len()-1).Stop
	for start < end && isBlank(source[start]) {
		start++
	}
	for end > start && isBlank(source[end-1]) {
		end--
	}
	return start, end
}

func isBlank(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

func (b *renderer) inline(n ast.Node) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch c := c.(type) {
		case *ast.Text:
			b.literal(c.Segment.Start, c.Segment.Stop, !c.IsRaw())
			if c.SoftLineBreak() || c.HardLineBreak() {
				b.blank(int32(c.Segment.Stop))
			}
		case *ast.String:
			// A string a parser made has no source of its own: it stands where the text before it ends.
			b.chars(string(c.Value), b.lastEnd(), b.lastEnd())
		case *ast.AutoLink:
			label := c.Label(b.r.source)
			from := int(b.lastEnd())
			if i := bytes.Index(b.r.source[from:], label); i >= 0 {
				b.literal(from+i, from+i+len(label), false)
			}
		case *ast.Image, *ast.RawHTML:
			// An image shows no text, and raw HTML is not read.
		default:
			b.inline(c)
		}
	}
}

// lastEnd is where the source of the last character written ends.
func (b *renderer) lastEnd() int32 {
	if n := len(b.r.end); n > 0 {
		return b.r.end[n-1]
	}
	return 0
}

// blank notes white space at source offset at: one space before the next character.
func (b *renderer) blank(at int32) {
	if !b.space {
		b.space, b.at = true, at
	}
}

// literal writes the source from start to end as the reader sees it; unescape reads backslash escapes and character
// references, as Markdown does outside code.
func (b *renderer) literal(start, end int, unescape bool) {
	src := b.r.source
	for i := start; i < end; {
		if unescape && src[i] == '\\' && i+1 < end && escapable(src[i+1]) {
			b.chars(string(src[i+1]), int32(i), int32(i+2))
			i += 2
			continue
		}
		if unescape && src[i] == '&' {
			if m := entity.Find(src[i:end]); m != nil {
				if s := html.UnescapeString(string(m)); s != string(m) {
					b.chars(s, int32(i), int32(i+len(m)))
					i += len(m)
					continue
				}
			}
		}
		r, size := utf8.DecodeRune(src[i:end])
		if unicode.IsSpace(r) {
			b.blank(int32(i))
		} else {
			b.chars(string(src[i:i+size]), int32(i), int32(i+size))
		}
		i += size
	}
}

// escapable tells an ASCII punctuation character, which a backslash escapes in Markdown.
func escapable(c byte) bool {
	return c < utf8.RuneSelf && (unicode.IsPunct(rune(c)) || unicode.IsSymbol(rune(c)))
}

// chars writes the characters s, each from the source start to end; white space in s is a blank.
func (b *renderer) chars(s string, start, end int32) {
	for _, r := range s {
		if unicode.IsSpace(r) {
			b.blank(start)
			continue
		}
		if b.space {
			if b.text.Len() > 0 {
				b.text.WriteByte(' ')
				b.r.start = append(b.r.start, b.at)
				b.r.end = append(b.r.end, b.at)
			}
			b.space = false
		}
		n, _ := b.text.WriteRune(r)
		for range n {
			b.r.start = append(b.r.start, start)
			b.r.end = append(b.r.end, end)
		}
	}
}

// squeeze is s as anchors compare it: every run of white space one space.
func squeeze(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	if space && b.Len() > 0 {
		b.WriteByte(' ')
	}
	return b.String()
}

// anchorPlace is where an anchor stands in a document.
type anchorPlace struct {
	state planv1.TilasmAnchorState
	// at and end are its range in the rendered text; equal for a place to insert at.
	at, end int
	// src and srcEnd are its range in the Markdown source; equal for a place to insert at.
	src, srcEnd int
	// count is how many places matched, for an ambiguous anchor.
	count int
}

// resolvable tells a place found: anchored, or changed.
func (p anchorPlace) resolvable() bool {
	return p.state == planv1.TilasmAnchorState_TILASM_ANCHOR_STATE_ANCHORED ||
		p.state == planv1.TilasmAnchorState_TILASM_ANCHOR_STATE_CHANGED
}

// resolve finds an anchor in the document. strict reads an empty prefix as the document's start and an empty suffix
// as its end, as Djinn stores anchors; otherwise they say nothing, as an agent may name a passage by its quote alone.
func (r *rendered) resolve(a *planv1.TilasmAnchor, strict bool) anchorPlace {
	q := strings.TrimSpace(squeeze(a.GetQuote()))
	rawPrefix := squeeze(a.GetPrefix())
	p := strings.TrimSpace(rawPrefix)
	s := strings.TrimSpace(squeeze(a.GetSuffix()))
	if q == "" {
		return r.caret(p, s, strings.HasSuffix(rawPrefix, " "), strict)
	}
	occ := indexAll(r.text, q)
	var fits []int
	for _, at := range occ {
		if r.before(at, p, strict) && r.after(at+len(q), s, strict) {
			fits = append(fits, at)
		}
	}
	switch {
	case len(fits) == 1:
		return r.found(planv1.TilasmAnchorState_TILASM_ANCHOR_STATE_ANCHORED, fits[0], fits[0]+len(q))
	case len(fits) > 1:
		return anchorPlace{state: planv1.TilasmAnchorState_TILASM_ANCHOR_STATE_AMBIGUOUS, count: len(fits)}
	}
	// No occurrence stands in its whole context: where the context still stands says where the passage is, and
	// whether it changed or went.
	// Context of a few characters may stand anywhere: it says where the passage went only when its quote is nowhere.
	spans, level := r.locate(p, s, len(q), strict)
	if len(spans) == 1 && (level >= specificContext || len(occ) == 0 || strings.Contains(r.text[spans[0].at:spans[0].end], q)) {
		sp := spans[0]
		inside := indexAll(r.text[sp.at:sp.end], q)
		switch {
		case len(inside) == 1:
			at := sp.at + inside[0]
			return r.found(planv1.TilasmAnchorState_TILASM_ANCHOR_STATE_ANCHORED, at, at+len(q))
		case len(inside) > 1:
			return anchorPlace{state: planv1.TilasmAnchorState_TILASM_ANCHOR_STATE_AMBIGUOUS, count: len(inside)}
		case sp.at == sp.end:
			return anchorPlace{state: planv1.TilasmAnchorState_TILASM_ANCHOR_STATE_MISSING} // Deleted.
		}
		return r.found(planv1.TilasmAnchorState_TILASM_ANCHOR_STATE_CHANGED, sp.at, sp.end)
	}
	switch len(occ) {
	case 0:
		if len(spans) > 1 {
			return anchorPlace{state: planv1.TilasmAnchorState_TILASM_ANCHOR_STATE_AMBIGUOUS, count: len(spans)}
		}
		return anchorPlace{state: planv1.TilasmAnchorState_TILASM_ANCHOR_STATE_MISSING}
	case 1:
		if strings.Contains(p, q) || strings.Contains(s, q) {
			// It had an identical twin beside it, and one of the two went: which one cannot be told.
			return anchorPlace{state: planv1.TilasmAnchorState_TILASM_ANCHOR_STATE_AMBIGUOUS, count: 1}
		}
		return r.found(planv1.TilasmAnchorState_TILASM_ANCHOR_STATE_ANCHORED, occ[0], occ[0]+len(q))
	}
	// Several occurrences, their context edited: the one whose context agrees by far the most.
	best, second, at := -1, -1, 0
	for _, o := range occ {
		score := commonSuffix(strings.TrimRight(r.text[:o], " "), p) + commonPrefix(strings.TrimLeft(r.text[o+len(q):], " "), s)
		switch {
		case score > best:
			best, second, at = score, best, o
		case score > second:
			second = score
		}
	}
	if best-second >= minContextLead {
		return r.found(planv1.TilasmAnchorState_TILASM_ANCHOR_STATE_ANCHORED, at, at+len(q))
	}
	return anchorPlace{state: planv1.TilasmAnchorState_TILASM_ANCHOR_STATE_AMBIGUOUS, count: len(occ)}
}

// minContextLead is how many more characters of context one occurrence of a passage must share with its anchor than
// any other, when none has it whole, to be the passage.
const minContextLead = 16

func commonSuffix(a, b string) int {
	n := 0
	for n < len(a) && n < len(b) && a[len(a)-1-n] == b[len(b)-1-n] {
		n++
	}
	return n
}

func commonPrefix(a, b string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

// before tells whether the rendered text before at ends with p, blanks aside.
func (r *rendered) before(at int, p string, strict bool) bool {
	head := strings.TrimRight(r.text[:at], " ")
	if p == "" {
		return !strict || head == ""
	}
	return strings.HasSuffix(head, p)
}

// after tells whether the rendered text from at starts with s, blanks aside.
func (r *rendered) after(at int, s string, strict bool) bool {
	tail := strings.TrimLeft(r.text[at:], " ")
	if s == "" {
		return !strict || tail == ""
	}
	return strings.HasPrefix(tail, s)
}

// textSpan is a range of the rendered text.
type textSpan struct{ at, end int }

// locate finds where a passage stands by its context alone: between its prefix and its suffix, close together, the
// blanks at the ends left out. When the whole context is found nowhere, the characters nearest the passage are looked
// for, fewer and fewer: an edit near the passage leaves them. strict reads an empty prefix as the document's start and
// an empty suffix as its end; otherwise a passage without both is not located. level is how many characters of each
// side were found.
func (r *rendered) locate(p, s string, quoted int, strict bool) (spans []textSpan, level int) {
	if p == "" && s == "" || !strict && (p == "" || s == "") {
		return nil, 0
	}
	gap := 2*quoted + 200
	for _, n := range []int{maxAnchorContext, specificContext, 8} {
		pk, sk := lastRunes(p, n), firstRunes(s, n)
		var starts []int
		if pk == "" {
			starts = []int{0}
		} else {
			for _, at := range indexAll(r.text, pk) {
				starts = append(starts, at+len(pk))
			}
		}
		var found []textSpan
		for _, at := range starts {
			end := len(r.text)
			if sk != "" {
				i := strings.Index(r.text[at:], sk)
				if i < 0 {
					continue
				}
				end = at + i
			}
			if end-at > gap {
				continue
			}
			for at < end && r.text[at] == ' ' {
				at++
			}
			for end > at && r.text[end-1] == ' ' {
				end--
			}
			found = append(found, textSpan{at, end})
		}
		if len(found) > 0 {
			return found, n
		}
	}
	return nil, 0
}

// specificContext is how many characters of context, on each side, tell a place even when the quote is elsewhere too.
const specificContext = 16

// caret finds a place to insert at: after its prefix and before its suffix. afterBlank puts it after the white space
// between them, at the start of what follows, rather than at the end of what precedes.
func (r *rendered) caret(p, s string, afterBlank, strict bool) anchorPlace {
	if p == "" && s == "" {
		if strings.TrimSpace(r.text) == "" {
			return anchorPlace{state: planv1.TilasmAnchorState_TILASM_ANCHOR_STATE_ANCHORED, src: len(r.source), srcEnd: len(r.source)}
		}
		return anchorPlace{state: planv1.TilasmAnchorState_TILASM_ANCHOR_STATE_AMBIGUOUS}
	}
	type cut struct{ before, after int } // the end of the prefix, the start of the suffix
	var cuts []cut
	if p != "" {
		for _, at := range indexAll(r.text, p) {
			end := at + len(p)
			next := end
			for next < len(r.text) && r.text[next] == ' ' {
				next++
			}
			if r.after(end, s, strict) {
				cuts = append(cuts, cut{end, next})
			}
		}
	} else {
		for _, at := range indexAll(r.text, s) {
			prev := at
			for prev > 0 && r.text[prev-1] == ' ' {
				prev--
			}
			if r.before(at, "", strict) {
				cuts = append(cuts, cut{prev, at})
			}
		}
	}
	switch len(cuts) {
	case 0:
		// The prefix or the suffix alone may still say where it was.
		if p != "" && s != "" {
			ps, ss := indexAll(r.text, p), indexAll(r.text, s)
			if len(ps) == 1 || len(ss) == 1 {
				at := ss[0]
				if len(ps) == 1 {
					at = ps[0] + len(p)
				}
				return r.point(planv1.TilasmAnchorState_TILASM_ANCHOR_STATE_CHANGED, at)
			}
		}
		return anchorPlace{state: planv1.TilasmAnchorState_TILASM_ANCHOR_STATE_MISSING}
	case 1:
		at := cuts[0].before
		if afterBlank || p == "" {
			at = cuts[0].after
		}
		return r.point(planv1.TilasmAnchorState_TILASM_ANCHOR_STATE_ANCHORED, at)
	}
	return anchorPlace{state: planv1.TilasmAnchorState_TILASM_ANCHOR_STATE_AMBIGUOUS, count: len(cuts)}
}

// point is a place to insert at, at offset at of the rendered text: after the character before it, or before the
// first one.
func (r *rendered) point(state planv1.TilasmAnchorState, at int) anchorPlace {
	src := len(r.source)
	switch {
	case at > 0 && r.text[at-1] != ' ':
		src = int(r.end[at-1])
	case at < len(r.text):
		src = int(r.start[at])
	case at > 0:
		src = int(r.end[at-1])
	}
	return anchorPlace{state: state, at: at, end: at, src: src, srcEnd: src}
}

// found is the place of the rendered text from at to end.
func (r *rendered) found(state planv1.TilasmAnchorState, at, end int) anchorPlace {
	return anchorPlace{state: state, at: at, end: end, src: int(r.start[at]), srcEnd: int(r.end[end-1])}
}

// indexAll are the offsets of every occurrence of sub in s, overlapping ones included.
func indexAll(s, sub string) []int {
	var out []int
	if sub == "" {
		return nil
	}
	for from := 0; from <= len(s)-len(sub); {
		i := strings.Index(s[from:], sub)
		if i < 0 {
			break
		}
		out = append(out, from+i)
		_, size := utf8.DecodeRuneInString(s[from+i:])
		from += i + size
	}
	return out
}

// current is the rendered text at a place found.
func (r *rendered) current(p anchorPlace) string {
	if !p.resolvable() {
		return ""
	}
	return r.text[p.at:p.end]
}

// original is the Markdown source at a place found.
func (r *rendered) original(p anchorPlace) string {
	if !p.resolvable() {
		return ""
	}
	return string(r.source[p.src:p.srcEnd])
}

// paragraphs is the Markdown source of the blocks a place found spans, or touches.
func (r *rendered) paragraphs(p anchorPlace) string {
	if !p.resolvable() {
		return ""
	}
	first, last := -1, -1
	for i, b := range r.blocks {
		if b.srcEnd < p.src || b.src > p.srcEnd {
			continue
		}
		if first < 0 {
			first = i
		}
		last = i
	}
	if first < 0 {
		return ""
	}
	return string(r.source[r.blocks[first].src:r.blocks[last].srcEnd])
}

// anchorAt is the anchor of a place found, with the context that tells it from any other: anchorContext characters
// on each side, more while an identical passage elsewhere has the same.
func (r *rendered) anchorAt(p anchorPlace, revision int64) *planv1.TilasmAnchor {
	var a *planv1.TilasmAnchor
	for n := anchorContext; ; n *= 2 {
		n = min(n, maxAnchorContext)
		a = &planv1.TilasmAnchor{
			Quote: r.text[p.at:p.end], Prefix: lastRunes(r.text[:p.at], n), Suffix: firstRunes(r.text[p.end:], n),
			Revision: revision,
		}
		got := r.resolve(a, true)
		if got.state == planv1.TilasmAnchorState_TILASM_ANCHOR_STATE_ANCHORED && got.at == p.at || n == maxAnchorContext {
			return a
		}
	}
}

func lastRunes(s string, n int) string {
	i := len(s)
	for k := 0; k < n && i > 0; k++ {
		_, size := utf8.DecodeLastRuneInString(s[:i])
		i -= size
	}
	return s[i:]
}

func firstRunes(s string, n int) string {
	i := 0
	for k := 0; k < n && i < len(s); k++ {
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
	}
	return s[:i]
}

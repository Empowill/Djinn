package plan

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/link"
	"github.com/empowill/djinn/internal/store"
)

// Tilasm links in a question's text. An agent writes the tilasm it cites by its code, djinn://tilasm/L70, which the
// window cannot open: a link names a tilasm by its id. The developer, 07/11 (W678), got "Detail and curve:
// djinn://tilasm/L70" as plain text. Djinn rewrites such a link as it stores the text: a bare link becomes a Markdown
// link titled with the tilasm's title, a Markdown link keeps its text and gets the id. Code blocks and code spans are
// left as written.

var (
	// tilasmLinkRe is a tilasm link as an agent writes it, the reference after the kind: a code, an id, or anything
	// else, which then names no tilasm. An autolink's angle brackets come with it.
	tilasmLinkRe = regexp.MustCompile(`(?i)<?djinn://(?:tilasm|talisman)/([0-9a-z-]+)/?>?`)
	// tilasmCodeRe is a tilasm's code, case ignored.
	tilasmCodeRe = regexp.MustCompile(`(?i)^L[0-9]{2,3}$`)
	// refDefRe is the start of a Markdown link reference definition, "[name]: ", whose target is not text.
	refDefRe = regexp.MustCompile(`^ {0,3}\[[^\]]+\]:\s*$`)
	// titleEscaper escapes what would end a Markdown link's text.
	titleEscaper = strings.NewReplacer(`\`, `\\`, `[`, `\[`, `]`, `\]`)
)

// linkedTilasm is the tilasm a link's reference names: ok false when it names none.
type linkedTilasm struct {
	id, title string
	ok        bool
}

// rewriteTilasmLinks rewrites the tilasm links of text, a question's Markdown, by what resolve says of each
// reference, a code or an id; outside code blocks and code spans. A bare link becomes [title](djinn://tilasm/<id>);
// a Markdown link's target, or a reference definition's, becomes djinn://tilasm/<id> and its text stays. A link
// already in that form is left as is: rewriting twice changes nothing. unknown is the references that name no
// tilasm, in the order met, each once; their links stay as written.
func rewriteTilasmLinks(text string, resolve func(ref string) linkedTilasm) (out string, unknown []string) {
	seen := map[string]bool{}
	miss := func(ref string) {
		if !seen[ref] {
			seen[ref] = true
			unknown = append(unknown, ref)
		}
	}
	rewrite := func(s string) string {
		var b strings.Builder
		last := 0
		for _, m := range tilasmLinkRe.FindAllStringSubmatchIndex(s, -1) {
			start, end, ref := m[0], m[1], s[m[2]:m[3]]
			opened, closed := s[start] == '<', s[end-1] == '>'
			if opened != closed { // A bracket of the text around it, not of an autolink.
				if opened {
					start++
				} else {
					end--
				}
			}
			t := resolve(ref)
			if !t.ok {
				miss(ref)
				continue
			}
			url := link.Of(link.Tilasm, t.id)
			lineStart := strings.LastIndexByte(s[:start], '\n') + 1
			with := "[" + titleEscaper.Replace(t.title) + "](" + url + ")"
			if strings.HasSuffix(s[:start], "](") || refDefRe.MatchString(s[lineStart:start]) {
				with = url // A Markdown link's target: its text stays.
				if opened && closed {
					with = "<" + url + ">"
				}
			}
			b.WriteString(s[last:start])
			b.WriteString(with)
			last = end
		}
		if last == 0 {
			return s
		}
		b.WriteString(s[last:])
		return b.String()
	}
	return eachProse(text, rewrite), unknown
}

// eachProse is text with fn applied to its prose, the parts outside fenced code blocks and code spans; a fence is
// the one ContextWarnings reads.
func eachProse(text string, fn func(string) string) string {
	var b strings.Builder
	var prose strings.Builder
	flush := func() {
		b.WriteString(fn(prose.String()))
		prose.Reset()
	}
	fence := ""
	for _, line := range strings.SplitAfter(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if fence != "" {
			if strings.HasPrefix(trimmed, fence) && strings.Trim(trimmed, fence[:1]) == "" {
				fence = ""
			}
			b.WriteString(line)
			continue
		}
		if m := fenceRe.FindStringSubmatch(trimmed); m != nil {
			flush()
			fence = m[1]
			b.WriteString(line)
			continue
		}
		for line != "" {
			i := strings.IndexByte(line, '`')
			if i < 0 {
				prose.WriteString(line)
				break
			}
			n := i
			for n < len(line) && line[n] == '`' {
				n++
			}
			run := line[i:n]
			end := closingRun(line[n:], len(run))
			if end < 0 { // No closing run on the line: the backticks are text.
				prose.WriteString(line[:n])
				line = line[n:]
				continue
			}
			prose.WriteString(line[:i])
			flush()
			b.WriteString(line[i : n+end+len(run)])
			line = line[n+end+len(run):]
		}
	}
	flush()
	return b.String()
}

// closingRun is where s holds a run of exactly n backticks, which closes a code span; -1 when it holds none.
func closingRun(s string, n int) int {
	for i := 0; i < len(s); {
		if s[i] != '`' {
			i++
			continue
		}
		j := i
		for j < len(s) && s[j] == '`' {
			j++
		}
		if j-i == n {
			return i
		}
		i = j
	}
	return -1
}

// linkTilasms rewrites the tilasm links of q's text, context, options and recommendation in tx (rewriteTilasmLinks),
// a code resolved in q's wish, an id in any. It returns a warning for each reference that names no tilasm, whose
// link stays as written.
func linkTilasms(ctx context.Context, tx *store.Tx, q *planv1.Question) ([]string, error) {
	cache := map[string]linkedTilasm{}
	var failed error
	resolve := func(ref string) linkedTilasm {
		key := strings.ToLower(ref)
		if t, ok := cache[key]; ok {
			return t
		}
		t, err := resolveTilasm(ctx, tx, q.GetWishId(), ref)
		if err != nil && failed == nil {
			failed = err
		}
		cache[key] = t
		return t
	}
	var unknown []string
	seen := map[string]bool{}
	field := func(text *string) {
		var missed []string
		*text, missed = rewriteTilasmLinks(*text, resolve)
		for _, ref := range missed {
			if !seen[ref] {
				seen[ref] = true
				unknown = append(unknown, ref)
			}
		}
	}
	field(&q.Text)
	field(&q.Context)
	for i := range q.Options {
		field(&q.Options[i])
	}
	field(&q.Recommendation)
	if failed != nil {
		return nil, failed
	}
	warnings := make([]string, 0, len(unknown))
	for _, ref := range unknown {
		warnings = append(warnings, fmt.Sprintf("djinn://tilasm/%s names no tilasm of the wish: the link is left as "+
			"written and opens nothing; link a tilasm the wish has, by its code or its id (djinn tilasm list)", ref))
	}
	return warnings, nil
}

// resolveTilasm is the tilasm ref names: a code within wishID, or an id; ok false when there is none.
func resolveTilasm(ctx context.Context, r store.Reader, wishID, ref string) (linkedTilasm, error) {
	var (
		t   *planv1.Tilasm
		err error
	)
	switch {
	case tilasmCodeRe.MatchString(ref):
		t, err = tilasmByCode(ctx, r, wishID, strings.ToUpper(ref))
	default:
		l, ok := tilasmID(ref)
		if !ok {
			return linkedTilasm{}, nil
		}
		t, err = store.Get[*planv1.Tilasm](ctx, r, l.ID)
		if errors.Is(err, store.ErrNotFound) {
			return linkedTilasm{}, nil
		}
	}
	if err != nil || t == nil {
		return linkedTilasm{}, err
	}
	title := strings.TrimSpace(t.GetTitle())
	if title == "" {
		title = t.GetCode()
	}
	return linkedTilasm{id: t.GetId(), title: title, ok: true}, nil
}

// tilasmID is the link ref makes when it is a tilasm's id; ok false when it is not one.
func tilasmID(ref string) (link.Link, bool) {
	l, err := link.Parse(link.Scheme + "://tilasm/" + ref)
	return l, err == nil
}

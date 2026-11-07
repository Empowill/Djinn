package plan

import (
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// TestRewriteTilasmLinks (W678): a tilasm link a question cites by code becomes a link to its id; a bare one takes
// the tilasm's title, a Markdown one keeps its text; code and unknown references stay as written; rewriting twice
// changes nothing.
func TestRewriteTilasmLinks(t *testing.T) {
	t.Parallel()
	const id = "01a1b65f-c717-7eea-9a82-a78d0017c770"
	const url = "djinn://tilasm/" + id
	resolve := func(ref string) linkedTilasm {
		if strings.EqualFold(ref, "L70") || ref == id {
			return linkedTilasm{id: id, title: "Load [curve]", ok: true}
		}
		return linkedTilasm{}
	}
	for _, c := range []struct {
		name, in, want string
		unknown        []string
	}{
		{"bare code", "Detail and curve: djinn://tilasm/L70", "Detail and curve: [Load \\[curve\\]](" + url + ")", nil},
		{"lower case", "See DJINN://Tilasm/l70.", "See [Load \\[curve\\]](" + url + ").", nil},
		{"Markdown", "See [the curve](djinn://tilasm/L70).", "See [the curve](" + url + ").", nil},
		{"Markdown angle", "See [the curve](<djinn://tilasm/L70>).", "See [the curve](<" + url + ">).", nil},
		{"autolink", "See <djinn://tilasm/L70>.", "See [Load \\[curve\\]](" + url + ").", nil},
		{"reference", "[c]: djinn://tilasm/L70", "[c]: " + url, nil},
		{"bare id", "See " + url + ".", "See [Load \\[curve\\]](" + url + ").", nil},
		{"right already", "See [x](" + url + ").", "See [x](" + url + ").", nil},
		{"unknown", "See djinn://tilasm/L99 and [x](djinn://tilasm/L99).", "See djinn://tilasm/L99 and [x](djinn://tilasm/L99).", []string{"L99"}},
		{"fence", "~~~\necho djinn://tilasm/L70\n~~~\nthen djinn://tilasm/L70", "~~~\necho djinn://tilasm/L70\n~~~\nthen [Load \\[curve\\]](" + url + ")", nil},
		{"backtick fence", "```md\ndjinn://tilasm/L99\n```", "```md\ndjinn://tilasm/L99\n```", nil},
		{"code span", "Write `djinn://tilasm/L70` or ``a ` djinn://tilasm/L70``, not djinn://tilasm/L70",
			"Write `djinn://tilasm/L70` or ``a ` djinn://tilasm/L70``, not [Load \\[curve\\]](" + url + ")", nil},
		{"lone backtick", "a ` b djinn://tilasm/L70", "a ` b [Load \\[curve\\]](" + url + ")", nil},
		{"none", "No link here.", "No link here.", nil},
	} {
		got, unknown := rewriteTilasmLinks(c.in, resolve)
		if got != c.want || !slices.Equal(unknown, c.unknown) {
			t.Errorf("%s: %q gives %q, unknown %q; want %q, %q", c.name, c.in, got, unknown, c.want, c.unknown)
		}
		if again, _ := rewriteTilasmLinks(got, resolve); again != got {
			t.Errorf("%s: rewritten twice %q, want %q", c.name, again, got)
		}
	}
}

// TestAskReviseLinkTilasms (W678): ask and revise store a question's tilasm links by id, its code resolved in its
// wish, in its text, context, options and recommendation; a code the wish lacks stays as written, with a warning.
func TestAskReviseLinkTilasms(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	c := serve(t)
	wish, other := c.wish(t), c.wish(t)
	l70 := &planv1.Tilasm{Id: store.NewID(), WishId: wish, Code: "L70", Title: "Memory of the night"}
	elsewhere := &planv1.Tilasm{Id: store.NewID(), WishId: other, Code: "L71", Title: "Another wish's"}
	putAll(t, c, l70, elsewhere)
	link := "[Memory of the night](djinn://tilasm/" + l70.GetId() + ")"

	ask, err := c.questions.Ask(ctx, connect.NewRequest(&planv1.QuestionServiceAskRequest{
		WishId: wish, Text: "Cap jaeger? djinn://tilasm/L70", Context: "Detail and curve: djinn://tilasm/L70",
		Options:        []string{"A: cap it, [as drawn](djinn://tilasm/L70)", "B: leave it"},
		Recommendation: "A: see djinn://tilasm/L71",
	}))
	if err != nil {
		t.Fatal(err)
	}
	q := ask.Msg.GetQuestion()
	if q.GetText() != "Cap jaeger? "+link || q.GetContext() != "Detail and curve: "+link ||
		q.GetOptions()[0] != "A: cap it, [as drawn](djinn://tilasm/"+l70.GetId()+")" ||
		q.GetRecommendation() != "A: see djinn://tilasm/L71" {
		t.Errorf("ask stored %q, %q, %q, %q", q.GetText(), q.GetContext(), q.GetOptions(), q.GetRecommendation())
	}
	if w := ask.Msg.GetWarnings(); len(w) != 1 || !strings.HasPrefix(w[0], "djinn://tilasm/L71 names no tilasm") {
		t.Errorf("ask warnings %q, want one on L71", w)
	}

	ref := &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: q.GetCode()}}
	rev, err := c.questions.Revise(ctx, connect.NewRequest(&planv1.QuestionServiceReviseRequest{
		Question: ref, WishId: wish, Context: "~~~\n# opens djinn://tilasm/L70\n~~~\n\nNow `djinn://tilasm/L70` is djinn://tilasm/l70",
		Recommendation: "A: " + link,
	}))
	if err != nil {
		t.Fatal(err)
	}
	q = rev.Msg.GetQuestion()
	if want := "~~~\n# opens djinn://tilasm/L70\n~~~\n\nNow `djinn://tilasm/L70` is " + link; q.GetContext() != want ||
		q.GetRecommendation() != "A: "+link || q.GetText() != "Cap jaeger? "+link {
		t.Errorf("revise stored %q, %q, %q", q.GetText(), q.GetContext(), q.GetRecommendation())
	}
	if w := rev.Msg.GetWarnings(); len(w) > 0 {
		t.Errorf("revise warnings %q, want none", w)
	}
}

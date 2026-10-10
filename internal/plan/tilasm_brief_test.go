package plan

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// TestBriefListsTilasms: the brief lists the wish's tilasms, each with its code, title, link and what it explains; its
// azima graph says which tilasms explain each azima; and its rules tell the lead to make a tilasm to explain a concept,
// and to cite it by its link.
func TestBriefListsTilasms(t *testing.T) {
	home := t.TempDir()
	c := serve(t, WithHome(home))
	wish := c.wish(t)
	tasks(t, c, wish, "T29", "T30", "W1")

	model := putTilasm(t, c, &planv1.TilasmServicePutRequest{
		Path: folder(t, map[string]string{"index.html": page("The objects in the database", "A wish holds tasks.")}),
		Wish: wish, Cites: []string{"T29", "W1"},
	}).GetTilasm()
	alone := putTilasm(t, c, &planv1.TilasmServicePutRequest{
		Path: folder(t, map[string]string{"index.html": page("A comparison", "Two ways.")}), Wish: wish,
	}).GetTilasm()

	brief, err := BuildBrief(t.Context(), c.store, home, wish)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"\n## Tilasms\n",
		"- **L01** The objects in the database: djinn://tilasm/" + model.GetId() + ", explains T29, W1\n",
		"- **L02** A comparison: djinn://tilasm/" + alone.GetId() + "\n",
	} {
		if !strings.Contains(brief.Moving, want) {
			t.Errorf("the brief lacks %q:\n%s", want, brief.Moving)
		}
	}
	lines := strings.Split(brief.Moving, "\n")
	line := func(prefix string) string {
		if i := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, prefix) }); i >= 0 {
			return lines[i]
		}
		return ""
	}
	if l := line("- **T29**"); !strings.HasSuffix(l, "; explained by L01") {
		t.Errorf("the azima graph: %q, want T29 explained by L01", l)
	}
	if l := line("- **T30**"); l == "" || strings.Contains(l, "explained by") {
		t.Errorf("the azima graph: %q, want T30 explained by none", l)
	}
	for _, want := range []string{"To explain a concept, make a tilasm", "talisman", "`djinn tilasm put <folder> --wish <wish>",
		"inline SVG laid out by hand", "never Mermaid", "reaches no network", "by its link, `djinn://tilasm/<id>`", "`--tilasm L01`"} {
		if !strings.Contains(brief.Stable, want) {
			t.Errorf("the rules lack %q", want)
		}
	}
	if strings.Contains(brief.Text(), home) {
		t.Error("the brief names Djinn's data folder")
	}
}

// TestTilasmCitationsBothWays: a tilasm cites the azimas and tasks it explains, and each of them shows the tilasms
// that cite it, in code order.
func TestTilasmCitationsBothWays(t *testing.T) {
	home := t.TempDir()
	c := serve(t, WithHome(home))
	wish := c.wish(t)
	ts := tasks(t, c, wish, "T29", "W1", "W2")
	for _, cites := range [][]string{{"T29", "W1"}, {"W1"}} {
		putTilasm(t, c, &planv1.TilasmServicePutRequest{
			Path: folder(t, map[string]string{"index.html": page("A concept", "Text.")}), Wish: wish, Cites: cites,
		})
	}
	all := []*planv1.Task{ts["T29"], ts["W1"], ts["W2"]}
	if err := FillTilasms(t.Context(), c.store, all); err != nil {
		t.Fatal(err)
	}
	for code, want := range map[string][]string{"T29": {"L01"}, "W1": {"L01", "L02"}, "W2": nil} {
		if got := ts[code].GetTilasms(); !slices.Equal(got, want) {
			t.Errorf("%s shows tilasms %v, want %v", code, got, want)
		}
	}
	if got := getTilasm(t, c, "L02", 0).GetTilasm().GetCites(); !slices.Equal(got, []string{ts["W1"].GetId()}) {
		t.Errorf("L02 cites %v, want W1", got)
	}
}

// TestTilasmContext: what a worker's first prompt holds of the tilasms it is given, each once; a tilasm of another
// wish, or none, is refused.
func TestTilasmContext(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	c := serve(t, WithHome(home))
	wish, other := c.wish(t), c.wish(t)
	tasks(t, c, wish, "T29")
	put := putTilasm(t, c, &planv1.TilasmServicePutRequest{
		Path: folder(t, map[string]string{"index.html": page("The objects", "A wish holds tasks."), "img/model.svg": "<svg/>"}),
		Wish: wish, Cites: []string{"T29"},
	})
	id := put.GetTilasm().GetId()
	foreign := putTilasm(t, c, &planv1.TilasmServicePutRequest{
		Path: folder(t, map[string]string{"index.html": page("Elsewhere", "Not here.")}), Wish: other,
	}).GetTilasm()

	text, err := TilasmContext(ctx, c.store, home, wish, []string{"l01, " + id, "L01"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"# Tilasms given as context\n",
		"\n## L01 · The objects\n\n- Link: djinn://tilasm/" + id + "\n- Explains: T29\n",
		"- Files, version 1: " + filepath.Join(home, "tilasms", id, "v1") + "\n  (img/model.svg, index.html)\n",
		"A wish holds tasks.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the context lacks %q:\n%s", want, text)
		}
	}
	if strings.Count(text, "## L01") != 1 || strings.Contains(text, "secret") {
		t.Errorf("the tilasm is given once, without its scripts:\n%s", text)
	}
	if text, err := TilasmContext(ctx, c.store, home, wish, nil); err != nil || text != "" {
		t.Errorf("no tilasm: %q, %v; want nothing", text, err)
	}
	for _, name := range []string{"L02", foreign.GetId(), "L01x"} {
		if _, err := TilasmContext(ctx, c.store, home, wish, []string{name}); code(err) != connect.CodeInvalidArgument {
			t.Errorf("tilasm %s: %v, want invalid argument", name, err)
		}
	}
}

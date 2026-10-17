package wishicons

import (
	"regexp"
	"strings"
	"testing"
)

// The bank holds the 108 icons of L09 v6, each named once, with a duo, words in both languages, a line and a gilding.
func TestBank(t *testing.T) {
	icons := All()
	if len(icons) != 108 {
		t.Fatalf("%d icons, want 108", len(icons))
	}
	names := map[string]bool{}
	families := map[string]int{}
	for _, icon := range icons {
		if names[icon.Name] {
			t.Errorf("%s: named twice", icon.Name)
		}
		names[icon.Name] = true
		families[icon.Family]++
		if duos := map[string]bool{"S": true, "L": true, "E": true, "M": true}; !duos[icon.Duo] {
			t.Errorf("%s: duo %q is none of S, L, E, M", icon.Name, icon.Duo)
		}
		if icon.En == "" || icon.Fr == "" || icon.Line == "" || icon.Gilding == "" {
			t.Errorf("%s: words, line or gilding missing", icon.Name)
		}
		if !Known(icon.Name) {
			t.Errorf("%s: not known", icon.Name)
		}
	}
	if len(families) != 12 {
		t.Errorf("%d families, want 12: %v", len(families), families)
	}
	if Known("") || Known("hand") {
		t.Error("an empty name or a name outside the bank is known")
	}
}

// A drawing is plain SVG shapes: no id (a copy of an icon never refers to another one), no script, no link, no style.
var (
	element   = regexp.MustCompile(`<(/?)([a-z]+)((?:\s+[a-z-]+="[^"]*")*)\s*/?>`)
	attribute = regexp.MustCompile(`([a-z-]+)="([^"]*)"`)
	shapes    = map[string]bool{"path": true, "circle": true, "ellipse": true, "rect": true, "g": true}
	allowed   = map[string]bool{
		"d": true, "cx": true, "cy": true, "r": true, "rx": true, "ry": true, "x": true, "y": true, "width": true,
		"height": true, "fill": true, "stroke": true, "stroke-width": true, "fill-opacity": true, "transform": true,
	}
)

func TestDrawingsAreShapes(t *testing.T) {
	for _, icon := range All() {
		for _, part := range []string{icon.Line, icon.Filigree, icon.Gilding} {
			if rest := element.ReplaceAllString(part, ""); strings.TrimSpace(rest) != "" {
				t.Errorf("%s: %q is not an element", icon.Name, rest)
			}
			for _, m := range element.FindAllStringSubmatch(part, -1) {
				if !shapes[m[2]] {
					t.Errorf("%s: <%s> is not a shape", icon.Name, m[2])
				}
				for _, a := range attribute.FindAllStringSubmatch(m[3], -1) {
					if !allowed[a[1]] || strings.ContainsAny(a[2], "<>()#:") && a[1] != "transform" {
						t.Errorf("%s: %s=%q is not allowed", icon.Name, a[1], a[2])
					}
				}
			}
		}
	}
}

func TestSearch(t *testing.T) {
	names := func(icons []Icon) []string {
		var out []string
		for _, icon := range icons {
			out = append(out, icon.Name)
		}
		return out
	}
	if got := len(Search("  ")); got != 108 {
		t.Errorf("a blank search finds %d icons, want 108", got)
	}
	for text, want := range map[string]string{
		"theme":                          "palette",
		"Th" + string(rune(0xe8)) + "me": "palette", // French, with its accent
		"donnees sql":                    "database",
		"wick lamp":                      "",
		"tales lamp":                     "lamp",
	} {
		got := strings.Join(names(Search(text)), ",")
		if got != want {
			t.Errorf("Search(%q) = %q, want %q", text, got, want)
		}
	}
}

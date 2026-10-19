package wishicons

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// TestLoad: a bank file that does not parse panics, naming the bank.
func TestLoad(t *testing.T) {
	t.Parallel()
	t.Run("a bank file reads its icons", func(t *testing.T) {
		t.Parallel()
		if got := load([]byte(`{"icons": [{"name": "lamp"}]}`)); len(got) != 1 || got[0].Name != "lamp" {
			t.Errorf("load = %v", got)
		}
	})
	t.Run("a broken bank file panics", func(t *testing.T) {
		t.Parallel()
		defer func() {
			if got := fmt.Sprint(recover()); !strings.HasPrefix(got, "wishicons/bank.json: ") {
				t.Errorf("panic %q, want it to name wishicons/bank.json", got)
			}
		}()
		load([]byte(`{"icons": [`))
	})
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

// TestAll: the bank holds the 108 icons of L09 v6 in 12 families, each named once, with a duo, words in both
// languages, a line and a gilding, drawn in plain shapes.
func TestAll(t *testing.T) {
	t.Parallel()
	icons := All()
	if len(icons) != 108 {
		t.Fatalf("%d icons, want 108", len(icons))
	}
	t.Run("every icon is named once and complete", func(t *testing.T) {
		t.Parallel()
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
		}
		if len(families) != 12 {
			t.Errorf("%d families, want 12: %v", len(families), families)
		}
	})
	t.Run("drawings are plain shapes", func(t *testing.T) {
		t.Parallel()
		for _, icon := range icons {
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
	})
	t.Run("a copy leaves the bank alone", func(t *testing.T) {
		t.Parallel()
		All()[0].Name = "changed"
		if All()[0].Name == "changed" {
			t.Error("changing what All gives changes the bank")
		}
	})
}

// TestKnown: every icon of the bank is known by its name, and nothing else.
func TestKnown(t *testing.T) {
	t.Parallel()
	t.Run("every icon of the bank", func(t *testing.T) {
		t.Parallel()
		for _, icon := range All() {
			if !Known(icon.Name) {
				t.Errorf("%s: not known", icon.Name)
			}
		}
	})
	for _, name := range []string{"", "hand"} {
		t.Run(fmt.Sprintf("%q is not an icon", name), func(t *testing.T) {
			t.Parallel()
			if Known(name) {
				t.Errorf("Known(%q) = true", name)
			}
		})
	}
}

func TestSearch(t *testing.T) {
	t.Parallel()
	t.Run("a blank search finds every icon", func(t *testing.T) {
		t.Parallel()
		if got := len(Search("  ")); got != 108 {
			t.Errorf("a blank search finds %d icons, want 108", got)
		}
	})
	for _, c := range []struct{ name, text, want string }{
		{"an English word", "theme", "palette"},
		{"a French word with its accent, in capitals", "Th" + string(rune(0xe8)) + "me", "palette"},
		{"French words without accents", "donnees sql", "database"},
		{"every word must be found", "wick lamp", ""},
		{"two words of one icon", "tales lamp", "lamp"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var names []string
			for _, icon := range Search(c.text) {
				names = append(names, icon.Name)
			}
			if got := strings.Join(names, ","); got != c.want {
				t.Errorf("Search(%q) = %q, want %q", c.text, got, c.want)
			}
		})
	}
}

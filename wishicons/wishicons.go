// Package wishicons is the bank of wish icons (L09 v6), shared by the server and the interface as the locales are:
// bank.json holds each icon's name, family, duo, search words in English and in French, and its drawing. The server
// keeps a wish's icon by its name (Wish.icon) and lets the Djinn search the bank to choose one; the window draws it
// (src/wish-icon.tsx) in the colours of its duo, tokens of src/review.css.
package wishicons

import (
	_ "embed"
	"encoding/json"
	"slices"
	"strings"
)

//go:embed bank.json
var data []byte

// Icon is one icon of the bank.
type Icon struct {
	Name   string `json:"name"`
	Family string `json:"family"`
	// Duo names its two colours, the line's then the gilding's: S emerald and saffron, L saffron and emerald, E ink and
	// saffron, M ink and mint.
	Duo string `json:"duo"`
	// En and Fr are the words a search reads, in English and in French.
	En string `json:"en"`
	Fr string `json:"fr"`
	// The drawing on a 24 grid: the line, its filigree and its gilding, SVG elements without an id.
	Line     string `json:"line"`
	Filigree string `json:"filigree"`
	Gilding  string `json:"gilding"`
}

var bank = load()

func load() []Icon {
	var file struct {
		Icons []Icon `json:"icons"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		panic("wishicons/bank.json: " + err.Error()) // The tests catch it before any build ships.
	}
	return file.Icons
}

// All is the bank, in its order: family by family.
func All() []Icon {
	return slices.Clone(bank)
}

// Known says whether name is an icon of the bank.
func Known(name string) bool {
	return slices.ContainsFunc(bank, func(icon Icon) bool { return icon.Name == name })
}

// Search is the icons whose name, family or words, in English or in French, hold every word of text, in the bank's
// order; all of them when text is blank. Case and accents do not count: "theme" or "Theme" finds the palette.
func Search(text string) []Icon {
	words := strings.Fields(fold(text))
	var found []Icon
	for _, icon := range bank {
		in := fold(strings.Join([]string{icon.Name, icon.Family, icon.En, icon.Fr}, " "))
		if !slices.ContainsFunc(words, func(word string) bool { return !strings.Contains(in, word) }) {
			found = append(found, icon)
		}
	}
	return found
}

// fold lowers text and takes the accents off the letters the bank's French words use, named by their code points.
func fold(text string) string {
	return strings.Map(func(r rune) rune {
		if plain, ok := accents[r]; ok {
			return plain
		}
		return r
	}, strings.ToLower(text))
}

var accents = map[rune]rune{
	0xe0: 'a', 0xe2: 'a', 0xe7: 'c', 0xe8: 'e', 0xe9: 'e', 0xea: 'e', 0xeb: 'e', 0xee: 'i', 0xef: 'i', 0xf4: 'o',
	0xf9: 'u', 0xfb: 'u', 0xfc: 'u',
}

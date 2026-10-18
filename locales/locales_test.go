package locales

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

// pluralForms are the suffixes of a text with plural forms (CLDR categories), used through its base key.
var pluralForms = []string{".zero", ".one", ".two", ".few", ".many", ".other"}

func pluralBase(key string) (string, bool) {
	for _, form := range pluralForms {
		if base, ok := strings.CutSuffix(key, form); ok {
			return base, true
		}
	}
	return "", false
}

func placeholders(text string) []string {
	var names []string
	for _, match := range placeholder.FindAllStringSubmatch(text, -1) {
		names = append(names, match[1])
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// TestCatalogs checks that every language translates every key of the source, and nothing else,
// with the same placeholders.
func TestCatalogs(t *testing.T) {
	t.Parallel()
	source := catalogs[Source]
	if len(source) == 0 {
		t.Fatal("locales/en.json is empty")
	}
	for language, texts := range catalogs {
		for key, text := range source {
			translated, ok := texts[key]
			if !ok {
				t.Errorf("locales/%s.json: missing key %q (en: %q)", language, key, text)
				continue
			}
			if strings.TrimSpace(translated) == "" {
				t.Errorf("locales/%s.json: empty text for %q", language, key)
			}
			if _, plural := pluralBase(key); plural {
				continue // A plural form may drop the count: "one hour".
			}
			if want, got := placeholders(text), placeholders(translated); !slices.Equal(want, got) {
				t.Errorf("locales/%s.json: %q has placeholders %v, en has %v", language, key, got, want)
			}
		}
		for key := range texts {
			if _, ok := source[key]; !ok {
				t.Errorf("locales/%s.json: key %q is not in en.json", language, key)
			}
		}
	}
}

var (
	goUse = regexp.MustCompile(`locales\.T\(\s*[^,]+,\s*"([^"]+)"`)
	tsUse = regexp.MustCompile("\\bt\\(\\s*[\"'`]([^\"'`$]+)[\"'`]")
)

// TestKeysInCode checks that the code only uses keys of en.json, and that en.json holds no key
// the code never names: a key is used when its quoted name appears in a source file.
func TestKeysInCode(t *testing.T) {
	t.Parallel()
	source := catalogs[Source]
	exists := func(key string) bool {
		if _, ok := source[key]; ok {
			return true
		}
		_, ok := source[key+".other"]
		return ok
	}
	// quoted holds every text between two quote marks that follow each other in the code: a key is used when its
	// quoted name appears in a source file. A set read once, not a search of all the code per key: that took most of
	// the test's time.
	quoted := map[string]bool{}
	walk := func(dir string, exts ...string) {
		err := filepath.WalkDir(dir, func(name string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if base := entry.Name(); base == "node_modules" || base == "vendor" || strings.HasPrefix(base, ".") && name != dir {
					return filepath.SkipDir
				}
				return nil
			}
			if !slices.Contains(exts, filepath.Ext(name)) || strings.HasSuffix(name, "_test.go") {
				return nil
			}
			data, err := os.ReadFile(name)
			if err != nil {
				return err
			}
			uses := tsUse
			if filepath.Ext(name) == ".go" {
				uses = goUse
			}
			for _, match := range uses.FindAllStringSubmatch(string(data), -1) {
				if !exists(match[1]) {
					t.Errorf("%s: key %q is not in locales/en.json", filepath.ToSlash(name), match[1])
				}
			}
			for text := string(data); ; {
				_, after, ok := strings.Cut(text, `"`)
				if !ok {
					break
				}
				between, _, ok := strings.Cut(after, `"`)
				if !ok {
					break
				}
				quoted[between] = true
				text = after
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	walk(filepath.Join("..", "src"), ".ts", ".tsx")
	walk("..", ".go")
	for key := range source {
		name := key
		if base, ok := pluralBase(key); ok {
			name = base
		}
		if !quoted[name] {
			t.Errorf("locales/en.json: key %q is used nowhere in src/ or the Go code", key)
		}
	}
}

// TestLoad: a folder of JSON files gives one catalog per language, named after its file; a file that is no catalog
// panics, naming it.
func TestLoad(t *testing.T) {
	t.Parallel()
	t.Run("one catalog per file", func(t *testing.T) {
		t.Parallel()
		got := load(fstest.MapFS{
			"en.json": {Data: []byte(`{"common.cancel": "Cancel"}`)},
			"de.json": {Data: []byte(`{"common.cancel": "Abbrechen"}`)},
		})
		if len(got) != 2 || got["en"]["common.cancel"] != "Cancel" || got["de"]["common.cancel"] != "Abbrechen" {
			t.Errorf("load = %v", got)
		}
	})
	for _, c := range []struct {
		name   string
		folder fstest.MapFS
		want   string
	}{
		{"a file that is no JSON panics", fstest.MapFS{"en.json": {Data: []byte(`{"common.cancel": `)}}, "locales/en.json: "},
		{"a file that is not read panics", fstest.MapFS{"en.json/x": {}}, "en.json"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			defer func() {
				if got := fmt.Sprint(recover()); !strings.Contains(got, c.want) {
					t.Errorf("panic %q, want it to hold %q", got, c.want)
				}
			}()
			load(c.folder)
		})
	}
}

// TestLanguages lists a language per catalog file, sorted.
func TestLanguages(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("*.json")
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, file := range files {
		want = append(want, strings.TrimSuffix(file, ".json"))
	}
	slices.Sort(want)
	if got := Languages(); !slices.Equal(got, want) || !slices.Contains(got, Source) {
		t.Errorf("Languages() = %v, want %v, with %s", got, want, Source)
	}
}

func TestMatch(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		tags []string
		want string
	}{
		{"the first translated tag wins", []string{"de-DE", "fr-CA;q=0.8"}, "fr"},
		{"an underscore splits like a dash", []string{" FR_fr "}, "fr"},
		{"no translated tag gives the source", []string{"de"}, Source},
		{"no tag gives the source", nil, Source},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := Match(c.tags...); got != c.want {
				t.Errorf("Match(%q) = %q, want %q", c.tags, got, c.want)
			}
		})
	}
}

func TestT(t *testing.T) {
	t.Parallel()
	if french := catalogs["fr"]["common.cancel"]; french == "" || french == "Cancel" {
		t.Fatalf("fr.json translates common.cancel as %q", french)
	}
	for _, c := range []struct {
		name, language, key string
		params              map[string]string
		want                string
	}{
		{"a language reads its catalog", "fr", "common.cancel", nil, catalogs["fr"]["common.cancel"]},
		{"an unknown language falls back to English", "xx", "common.cancel", nil, "Cancel"},
		{"an unknown key falls back to itself", "fr", "no.such.key", nil, "no.such.key"},
		{"placeholders take their values", "en", "settings.language_system", map[string]string{"language": "English"}, "System (English)"},
		{"a placeholder without a value stays", "en", "settings.language_system", map[string]string{"other": "x"}, "System ({language})"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := T(c.language, c.key, c.params); got != c.want {
				t.Errorf("T(%s, %s) = %q, want %q", c.language, c.key, got, c.want)
			}
		})
	}
}

func TestMeans(t *testing.T) {
	t.Parallel()
	// Every language's yes, in capitals between spaces, means yes, and only yes.
	for language, texts := range catalogs {
		t.Run(language+" yes means yes only", func(t *testing.T) {
			t.Parallel()
			if word := " " + strings.ToUpper(texts["answer.yes"]) + " "; !Means(word, "answer.yes") || Means(word, "answer.no") {
				t.Errorf("%s: %q means yes, and only yes", language, word)
			}
		})
	}
	// Only a whole word of the catalogs means something.
	for _, c := range []struct{ name, word, key string }{
		{"a part of a word means nothing", "y", "answer.yes"},
		{"an empty word means nothing", "", "answer.yes"},
		{"an unknown key means nothing", "yes", "no.such.key"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if Means(c.word, c.key) {
				t.Errorf("Means(%q, %q) = true", c.word, c.key)
			}
		})
	}
}

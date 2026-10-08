package locales

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
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
	source := catalogs[Source]
	exists := func(key string) bool {
		if _, ok := source[key]; ok {
			return true
		}
		_, ok := source[key+".other"]
		return ok
	}
	var code strings.Builder
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
			code.Write(data)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	walk(filepath.Join("..", "src"), ".ts", ".tsx")
	walk("..", ".go")
	all := code.String()
	for key := range source {
		name := key
		if base, ok := pluralBase(key); ok {
			name = base
		}
		if !strings.Contains(all, `"`+name+`"`) {
			t.Errorf("locales/en.json: key %q is used nowhere in src/ or the Go code", key)
		}
	}
}

func TestT(t *testing.T) {
	if got := T("fr", "common.cancel", nil); got != "Annuler" {
		t.Errorf("T(fr, common.cancel) = %q", got)
	}
	if got := T("xx", "common.cancel", nil); got != "Cancel" {
		t.Errorf("an unknown language falls back to English, got %q", got)
	}
	if got := T("fr", "no.such.key", nil); got != "no.such.key" {
		t.Errorf("an unknown key falls back to itself, got %q", got)
	}
	if got := T("en", "settings.language_system", map[string]string{"language": "English"}); got != "System (English)" {
		t.Errorf("placeholders: got %q", got)
	}
	if got := Match("de-DE", "fr-CA;q=0.8"); got != "fr" {
		t.Errorf("Match = %q, want fr", got)
	}
	if got := Match("de"); got != Source {
		t.Errorf("Match = %q, want the source language", got)
	}
}

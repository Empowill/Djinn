// Package locales holds the texts Djinn shows to people, one JSON catalog per language, shared by
// the Go server and the interface (src/i18n.ts). en.json is the source; every other file
// translates all of its keys. Whoever adds or changes a text writes the English source and its
// translations in the same change: there is no translation platform.
package locales

import (
	"embed"
	"encoding/json"
	"path"
	"regexp"
	"sort"
	"strings"
)

// Source is the language every key is written in first, and the fallback of the others.
const Source = "en"

//go:embed *.json
var files embed.FS

// catalogs maps a language to its texts, loaded once from the embedded files.
var catalogs = load()

func load() map[string]map[string]string {
	entries, err := files.ReadDir(".")
	if err != nil {
		panic(err) // The embedded directory always exists.
	}
	all := map[string]map[string]string{}
	for _, entry := range entries {
		data, err := files.ReadFile(entry.Name())
		if err != nil {
			panic(err)
		}
		texts := map[string]string{}
		if err := json.Unmarshal(data, &texts); err != nil {
			panic("locales/" + entry.Name() + ": " + err.Error()) // The tests catch it before any build ships.
		}
		all[strings.TrimSuffix(entry.Name(), path.Ext(entry.Name()))] = texts
	}
	return all
}

// Languages lists the languages Djinn is translated into, sorted.
func Languages() []string {
	languages := make([]string, 0, len(catalogs))
	for language := range catalogs {
		languages = append(languages, language)
	}
	sort.Strings(languages)
	return languages
}

// Match returns the best translated language for a list of BCP 47 tags (an Accept-Language header
// split on commas works), or the source language when none matches.
func Match(tags ...string) string {
	for _, tag := range tags {
		tag, _, _ = strings.Cut(strings.TrimSpace(tag), ";")
		base, _, _ := strings.Cut(strings.ToLower(tag), "-")
		base, _, _ = strings.Cut(base, "_")
		if _, ok := catalogs[base]; ok {
			return base
		}
	}
	return Source
}

var placeholder = regexp.MustCompile(`\{(\w+)\}`)

// T returns the text of key in language, falling back to English, then to the key itself.
// `{name}` placeholders take their value from params.
func T(language, key string, params map[string]string) string {
	text, ok := catalogs[language][key]
	if !ok {
		text, ok = catalogs[Source][key]
	}
	if !ok {
		return key
	}
	if params == nil {
		return text
	}
	return placeholder.ReplaceAllStringFunc(text, func(match string) string {
		if value, ok := params[match[1:len(match)-1]]; ok {
			return value
		}
		return match
	})
}

// Means reports whether word is the text of key in one of Djinn's languages, case and spaces ignored: "Oui" means
// "answer.yes".
func Means(word, key string) bool {
	word = strings.TrimSpace(word)
	for _, texts := range catalogs {
		if text, ok := texts[key]; ok && strings.EqualFold(word, text) {
			return true
		}
	}
	return false
}

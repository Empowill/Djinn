// The check that the repository is in English, and the signs of French it reads. It has no french.go beside it: it
// tests the files of the repository, not code of the package, and it lives here because French belongs in
// locales/fr.json only.
package locales

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

// French is found by two signs on a line: a letter or a quote mark English does not use, or two different words
// French uses and English does not. A heuristic: it misses a lone French word, and it is enough to keep the
// repository in English.
var (
	frenchLetter = regexp.MustCompile(`[àâæçéèêëîïôœùûüÿÀÂÆÇÉÈÊËÎÏÔŒÙÛÜŸ«»]`)
	frenchWord   = wordSet("les des une est avec dans qui que sur pas aux cette ces sont nous vous mais leur leurs tous " +
		"toutes chez il ils elle elles je ne ont sera peut doit faut aussi alors donc depuis encore toujours jamais rien " +
		"avoir faire fait dont lorsque quand puis sinon parce voici ici oui")
	// An elision is one of these words, a quote mark, then a vowel: l'a, d'abord, qu'il.
	frenchElided = wordSet("l d qu n c j")
	elisionNext  = "aeiouhyéèêàâîô"
)

func wordSet(words string) map[string]bool {
	set := map[string]bool{}
	for _, word := range strings.Fields(words) {
		set[word] = true
	}
	return set
}

// frenchAllowed are the files that hold French on purpose, with why.
var frenchAllowed = map[string]string{
	"locales/fr.json":                   "the French catalog, the one home of French",
	"locales/french_test.go":            "this test, which names the signs of French",
	"internal/harness/worktree.go":      "folds accented letters out of branch names",
	"internal/harness/worktree_test.go": "checks that folding on an accented title",
	"wishicons/bank.json":               "the wish icons' search words in French, as a wish's title may be (L09)",
	"lead/tones/light.md":               "the solar Djinn's tone quotes its French turns, for questions in French",
	"lead/tones/dark.md":                "the demon's tone quotes its French turns, for questions in French",
}

// frenchQuoted are the words a file may hold anywhere: a person's name, the French words a plan decides on.
var frenchQuoted = map[string][]string{
	"*":      {"Clément"},
	"plan/*": {"« souhait »", "« invoquer »"},
}

// skipFrench are the files that are not text the project writes: images, fonts, and the vendored bundles.
var skipFrench = regexp.MustCompile(`\.(png|ico|icns|jpg|jpeg|gif|webp|woff2?|ttf|binpb|webm)$|^src/vendor/|(^|/)package-lock\.json$|^go\.sum$`)

// TestNoFrench checks that French lives in locales/fr.json only: every other file of the repository, tracked or new,
// is in English. A text a person reads in French is a key of the catalogs; a test of the French interface reads its
// text from locales/fr.json.
func TestNoFrench(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git lists the files of the repository: not found")
	}
	out, err := exec.Command("git", "-C", "..", "ls-files", "--cached", "--others", "--exclude-standard", "-z").Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	// The files are read in parallel: the regular expressions take most of a second on one core.
	names := strings.Split(strings.TrimRight(string(out), "\x00"), "\x00")
	found := make([][]string, len(names))
	next := make(chan int)
	var wg sync.WaitGroup
	for range runtime.GOMAXPROCS(0) {
		wg.Go(func() {
			for i := range next {
				found[i] = frenchIn(names[i])
			}
		})
	}
	for i := range names {
		next <- i
	}
	close(next)
	wg.Wait()
	for _, lines := range found {
		for _, line := range lines {
			t.Error(line)
		}
	}
}

// frenchIn are the lines of French in the file name of the repository, one message each.
func frenchIn(name string) []string {
	if _, ok := frenchAllowed[name]; ok || skipFrench.MatchString(name) {
		return nil
	}
	target := filepath.Join("..", filepath.FromSlash(name))
	data, err := os.ReadFile(target)
	if os.IsNotExist(err) {
		return nil // Deleted, not yet staged.
	}
	if err != nil {
		if fi, statErr := os.Stat(target); statErr == nil && fi.IsDir() {
			return nil
		}
		return []string{err.Error()}
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return nil // Binary.
	}
	var quoted []string
	for pattern, words := range frenchQuoted {
		if ok, _ := path.Match(pattern, name); ok || pattern == "*" {
			quoted = append(quoted, words...)
		}
	}
	var found []string
	lines := bufio.NewScanner(bytes.NewReader(data))
	lines.Buffer(nil, len(data)+1)
	for number := 1; lines.Scan(); number++ {
		if sign := french(lines.Text(), quoted); sign != "" {
			found = append(found, fmt.Sprintf("%s:%d: French (%s); English here, French in locales/fr.json", name, number, sign))
		}
	}
	return found
}

// french is the sign of French on a line, or "" for none.
func french(line string, quoted []string) string {
	for _, word := range quoted {
		line = strings.ReplaceAll(line, word, "")
	}
	if letter := frenchLetter.FindString(line); letter != "" {
		return "the letter " + letter
	}
	words := map[string]bool{}
	for _, word := range frenchWords(line) {
		words[word] = true
	}
	if len(words) >= 2 {
		var found []string
		for word := range words {
			found = append(found, word)
		}
		slices.Sort(found)
		return "the words " + strings.Join(found, ", ")
	}
	return ""
}

// frenchWords are the French words of a line, in order. A word is a run of ASCII letters, digits and underscores, as
// \b reads it; an elision counts with its next letter (l'a, l'i, the curly quote read as a straight one) and takes
// the word it starts. Words cut by hand, not by a regular expression: the backtracking of one alternation of sixty
// words took most of the test's time, ten seconds on four cores.
func frenchWords(line string) []string {
	var found []string
	for i := 0; i < len(line); i++ {
		if !wordByte(line[i]) || i > 0 && wordByte(line[i-1]) {
			continue
		}
		end := i + 1
		for end < len(line) && wordByte(line[end]) {
			end++
		}
		word := line[i:end]
		if frenchWord[word] {
			found = append(found, word)
		} else if frenchElided[word] {
			rest, ok := strings.CutPrefix(line[end:], "'")
			if !ok {
				rest, ok = strings.CutPrefix(line[end:], "’")
			}
			if next, size := utf8.DecodeRuneInString(rest); ok && size > 0 && strings.ContainsRune(elisionNext, next) {
				found = append(found, word+"'"+string(next))
				end = len(line) - len(rest) + size
			}
		}
		i = end - 1
	}
	return found
}

// wordByte is a byte of a word as \b reads it: an ASCII letter, a digit or an underscore.
func wordByte(b byte) bool {
	return 'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z' || '0' <= b && b <= '9' || b == '_'
}

// TestFrench: a line with a French letter or two French words is French; English with a lone French-looking word,
// or with a quoted French name, is not.
func TestFrench(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, line string
		french     bool
	}{
		{"a French comment", "// Le souhait est prêt.", true},
		{"a French title", "title: \"Ajouter les tâches\"", true},
		{"an accented verb", "// Vérifie que le worker est arrêté", true},
		{"a French expectation", "expect(text).toBe(\"Il faut une réponse dans la question\")", true},
		{"two elisions", "// d'abord l'interface", true},
		{"an English comment", "// The wish is ready: its lead pours the oil.", false},
		{"one French-looking word", "font-family: sans-serif; // la la la", false},
		{"an English identifier", "des := cipher.NewDES(key) // a test of it", false},
		{"a quoted name", "Clément's tokens", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if sign := french(c.line, []string{"Clément"}); (sign != "") != c.french {
				t.Errorf("%q: French %v, want %v (%s)", c.line, sign != "", c.french, sign)
			}
		})
	}
}

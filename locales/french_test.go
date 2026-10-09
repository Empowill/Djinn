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
)

// French is found by two signs on a line: a letter or a quote mark English does not use, or two different words
// French uses and English does not. A heuristic: it misses a lone French word, and it is enough to keep the
// repository in English.
var (
	frenchLetter = regexp.MustCompile(`[àâæçéèêëîïôœùûüÿÀÂÆÇÉÈÊËÎÏÔŒÙÛÜŸ«»]`)
	frenchWord   = regexp.MustCompile(`\b(?:les|des|une|est|avec|dans|qui|que|sur|pas|aux|cette|ces|sont|nous|vous|mais|leur|leurs|tous|toutes|chez|il|ils|elle|elles|je|ne|ont|sera|peut|doit|faut|aussi|alors|donc|depuis|encore|toujours|jamais|rien|avoir|faire|fait|dont|lorsque|quand|puis|sinon|parce|voici|ici|oui)\b|\b(?:l|d|qu|n|c|j)['’][aeiouhyéèêàâîô]`)
)

// frenchAllowed are the files that hold French on purpose, with why.
var frenchAllowed = map[string]string{
	"locales/fr.json":                   "the French catalog, the one home of French",
	"locales/french_test.go":            "this test, which names the signs of French",
	"internal/harness/worktree.go":      "folds accented letters out of branch names",
	"internal/harness/worktree_test.go": "checks that folding on an accented title",
}

// frenchQuoted are the words a file may hold anywhere: a person's name, the French words a plan decides on.
var frenchQuoted = map[string][]string{
	"*":      {"Clément"},
	"plan/*": {"« souhait »", "« invoquer »"},
}

// skipFrench are the files that are not text the project writes: images, fonts, and the vendored bundles.
var skipFrench = regexp.MustCompile(`\.(png|ico|icns|jpg|jpeg|gif|webp|woff2?|ttf|binpb|webm)$|^src/vendor/|^docs/site/vendor/|(^|/)package-lock\.json$|^go\.sum$`)

// TestNoFrench checks that French lives in locales/fr.json only: every other file of the repository, tracked or new,
// is in English. A text a person reads in French is a key of the catalogs; a test of the French interface reads its
// text from locales/fr.json.
func TestNoFrench(t *testing.T) {
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
	data, err := os.ReadFile(filepath.Join("..", filepath.FromSlash(name)))
	if os.IsNotExist(err) {
		return nil // Deleted, not yet staged.
	}
	if err != nil {
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
	for _, word := range frenchWord.FindAllString(line, -1) {
		words[strings.ReplaceAll(word, "’", "'")] = true // An elision counts with its next letter: l'a, l'i.
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

func TestFrenchSigns(t *testing.T) {
	for _, line := range []string{
		"// Le souhait est prêt.",
		"title: \"Ajouter les tâches\"",
		"// Vérifie que le worker est arrêté",
		"expect(text).toBe(\"Il faut une réponse dans la question\")",
		"// d'abord l'interface",
	} {
		if french(line, nil) == "" {
			t.Errorf("%q: French not found", line)
		}
	}
	for _, line := range []string{
		"// The wish is ready: its lead pours the oil.",
		"font-family: sans-serif; // la la la",
		"des := cipher.NewDES(key) // a test of it",
		"Clément's tokens",
	} {
		if sign := french(line, []string{"Clément"}); sign != "" {
			t.Errorf("%q: English taken for French (%s)", line, sign)
		}
	}
}

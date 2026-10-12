package notes

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

// TestIndexMatchesFolder: the notes built into djinn are the folder's, each read whole, and the index has a line per
// note, its name and description only.
func TestIndexMatchesFolder(t *testing.T) {
	all, err := Notes()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, e := range entries {
		if name, ok := strings.CutSuffix(e.Name(), ".md"); ok && !e.IsDir() {
			files = append(files, name)
		}
	}
	var names []string
	for _, n := range all {
		names = append(names, n.Name)
	}
	if len(files) == 0 || !slices.Equal(names, files) {
		t.Fatalf("embedded notes %v, the folder holds %v", names, files)
	}
	onDisk, err := read(os.DirFS("."))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(onDisk, all) {
		t.Errorf("the embedded notes differ from the folder's")
	}

	index, err := Index()
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(index, "\n"), "\n")
	if len(lines) != len(all) {
		t.Fatalf("index has %d lines for %d notes:\n%s", len(lines), len(all), index)
	}
	for i, n := range all {
		if want := "- `" + n.Name + "`: " + n.Description; lines[i] != want {
			t.Errorf("index line %d = %q, want %q", i, lines[i], want)
		}
		if first, _, _ := strings.Cut(n.Body, "\n"); strings.Contains(index, first) {
			t.Errorf("the index holds the text of %s: %q", n.Name, first)
		}
	}
}

// TestLinksName: a [[name]] in a note names another note.
func TestLinksName(t *testing.T) {
	all, err := Notes()
	if err != nil {
		t.Fatal(err)
	}
	link := regexp.MustCompile(`\[\[([^\]]+)\]\]`)
	for _, n := range all {
		for _, m := range link.FindAllStringSubmatch(n.Body, -1) {
			if _, ok := Find(m[1]); !ok {
				t.Errorf("%s links to [[%s]], which is no note", n.Name, m[1])
			}
		}
	}
}

func TestFind(t *testing.T) {
	n, ok := Find("Push-Hold-Deadlock.md")
	if !ok || n.Name != "push-hold-deadlock" || !strings.Contains(n.Body, "**Why:**") {
		t.Fatalf("Find(Push-Hold-Deadlock.md) = %+v, %v", n, ok)
	}
	if text := n.Text(); !strings.HasPrefix(text, "# push-hold-deadlock\n\n"+n.Description+"\n\n") || !strings.HasSuffix(text, n.Body) {
		t.Errorf("Text() = %q", text)
	}
	if _, ok := Find("no-such-note"); ok {
		t.Error("Find found a note that does not exist")
	}
}

func TestReadRefuses(t *testing.T) {
	note := func(head, body string) *fstest.MapFile {
		return &fstest.MapFile{Data: []byte("---\n" + head + "---\n" + body)}
	}
	good := "name: a\ndescription: what a is about\ntype: feedback\n"
	for _, tc := range []struct {
		name string
		file *fstest.MapFile
		want string
	}{
		{"no front matter", &fstest.MapFile{Data: []byte("name: a\n")}, "no front matter"},
		{"unended", &fstest.MapFile{Data: []byte("---\n" + good)}, "never ends"},
		{"other name", note(strings.Replace(good, "name: a", "name: b", 1), "text"), `its name is "b"`},
		{"no description", note("name: a\ntype: feedback\n", "text"), "description is not one line"},
		{"bad type", note(strings.Replace(good, "feedback", "user", 1), "text"), `its type "user"`},
		{"unknown field", note(good+"metadata: {}\n", "text"), "front matter"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := read(fstest.MapFS{"a.md": tc.file})
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "a.md") {
				t.Errorf("read: %v, want an error on a.md with %q", err, tc.want)
			}
		})
	}
	got, err := read(fstest.MapFS{"a.md": note(good, "\nThe text.\r\n")})
	if err != nil || len(got) != 1 || got[0].Body != "The text.\n" || got[0].Type != "feedback" {
		t.Errorf("read a good note: %+v, %v", got, err)
	}
}

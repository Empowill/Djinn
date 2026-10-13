// Package notes holds the lead's notes: how to lead wishes with Djinn, learnt from past wishes, whatever the agent
// that leads. One note per Markdown file of this folder, built into djinn and read only at run time: a contributor
// changes them by pull request, like any doc (lead/README.md). The brief gives every lead the index; djinn lead notes
// <name> prints one note.
package notes

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
	"sync"

	"go.yaml.in/yaml/v3"
)

//go:embed *.md
var files embed.FS

// Note is a note of the lead: its front matter, then its text.
type Note struct {
	// Name is its file's name without .md: how the index and djinn lead notes name it.
	Name string `yaml:"name"`
	// Description says in a line what it is about, for a lead to tell whether to open it.
	Description string `yaml:"description"`
	// Type is feedback (how the developer wants to be led), project (a fact of the work) or reference (where to look).
	Type string `yaml:"type"`
	// Body is the text after the front matter.
	Body string `yaml:"-"`
}

// Types are the kinds a note may be.
var Types = []string{"feedback", "project", "reference"}

// Notes are the lead's notes, by name. An error names the file that does not read: a test reads them all.
var Notes = sync.OnceValues(func() ([]Note, error) { return read(files) })

func read(fsys fs.FS) ([]Note, error) {
	paths, err := fs.Glob(fsys, "*.md")
	if err != nil {
		return nil, err
	}
	slices.Sort(paths)
	notes := make([]Note, 0, len(paths))
	for _, p := range paths {
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return nil, err
		}
		n, err := parse(b)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		if want := strings.TrimSuffix(path.Base(p), ".md"); n.Name != want {
			return nil, fmt.Errorf("%s: its name is %q, not its file's %q", p, n.Name, want)
		}
		notes = append(notes, n)
	}
	return notes, nil
}

// parse reads a note: a front matter between two lines of ---, then its text.
func parse(b []byte) (Note, error) {
	b = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
	rest, ok := bytes.CutPrefix(b, []byte("---\n"))
	if !ok {
		return Note{}, fmt.Errorf("it starts with no front matter (---)")
	}
	head, body, ok := bytes.Cut(rest, []byte("\n---\n"))
	if !ok {
		return Note{}, fmt.Errorf("its front matter never ends")
	}
	var n Note
	dec := yaml.NewDecoder(bytes.NewReader(head))
	dec.KnownFields(true)
	if err := dec.Decode(&n); err != nil {
		return Note{}, fmt.Errorf("its front matter: %w", err)
	}
	switch {
	case n.Name == "":
		return Note{}, fmt.Errorf("it has no name")
	case strings.TrimSpace(n.Description) == "" || strings.Contains(n.Description, "\n"):
		return Note{}, fmt.Errorf("its description is not one line")
	case !slices.Contains(Types, n.Type):
		return Note{}, fmt.Errorf("its type %q is none of %s", n.Type, strings.Join(Types, ", "))
	}
	n.Body = strings.TrimSpace(string(body)) + "\n"
	return n, nil
}

// Index is a line per note, its name and description, never its text: what a lead reads to know which note to open.
func Index() (string, error) {
	notes, err := Notes()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, n := range notes {
		fmt.Fprintf(&b, "- `%s`: %s\n", n.Name, n.Description)
	}
	return b.String(), nil
}

// Find is the note named name, case ignored.
func Find(name string) (Note, bool) {
	notes, err := Notes()
	if err != nil {
		return Note{}, false
	}
	for _, n := range notes {
		if strings.EqualFold(n.Name, strings.TrimSuffix(name, ".md")) {
			return n, true
		}
	}
	return Note{}, false
}

// Text is the note as djinn lead notes prints it: its name and description as a title, then its text.
func (n Note) Text() string {
	return fmt.Sprintf("# %s\n\n%s\n\n%s", n.Name, n.Description, n.Body)
}

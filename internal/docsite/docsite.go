// Package docsite is the Grimoire, Djinn's documentation site, docs/site: its book's navigation and pages rendered from
// docs/site/pages (grimoire.go), and its Command line filled in from the command tree of this very djinn
// (cli.Reference). It is what `go tool task docs` writes into bin/docs, and what djinn up serves at /docs/. Nothing is
// generated ahead and committed, so the site cannot lag behind its pages or the commands.
package docsite

import (
	"bytes"
	_ "embed"
	"html/template"
	"io"
	"io/fs"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/empowill/djinn/internal/cli"
)

const index = "index.html"

//go:embed commands.html
var commandsTemplate string

var commands = template.Must(template.New("commands").Funcs(template.FuncMap{
	"anchor":     anchor,
	"paragraphs": func(s string) []string { return strings.Split(s, "\n\n") },
	"short": func(group, name string) string {
		return strings.TrimPrefix(name, group+" ")
	},
	"own": func(group string) bool { return group == cli.Own },
}).Parse(commandsTemplate))

// anchor is the id of a command, or of a group with the prefix "group-": cmd-wish-make.
func anchor(prefix, name string) string {
	return prefix + "-" + strings.ReplaceAll(name, " ", "-")
}

// Commands is the HTML of the Command line page: a table of contents, then every command of djinn by group, with its
// usage, its help, its arguments and its flags.
func Commands() []byte {
	var b bytes.Buffer
	if err := commands.Execute(&b, struct {
		Groups []cli.Group
		Global []cli.Param
	}{cli.Reference(), cli.GlobalFlags}); err != nil {
		panic(err) // The template and its data are fixed: a test executes it.
	}
	return b.Bytes()
}

// FS is the site with the Grimoire made from it: index.html with the navigation, the pages' shells and the
// navigation's words in place of their markers, and a script per page, pages/<slug>.js (PageScript), the Command line's
// with the commands. Each is made once, when first asked: djinn up renders a page when a reader opens it.
func FS(site fs.FS) fs.FS {
	return &grimoireFS{site: site, scripts: map[string][]byte{}}
}

type grimoireFS struct {
	site  fs.FS
	once  sync.Once
	index []byte
	err   error

	mu      sync.Mutex
	scripts map[string][]byte // by slug
}

// page is index.html with the navigation, the pages' shells and the words.
func (s *grimoireFS) page() ([]byte, error) {
	s.once.Do(func() {
		var b []byte
		if b, s.err = fs.ReadFile(s.site, index); s.err != nil {
			return
		}
		b = bytes.Replace(b, []byte(PagesMarker), Shells(), 1)
		b = bytes.Replace(b, []byte(NavMarker), NavHTML(), 1)
		s.index = bytes.Replace(b, []byte(LabelsMarker), Labels(), 1)
	})
	return s.index, s.err
}

// script is a page's script, made once.
func (s *grimoireFS) script(slug string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b, ok := s.scripts[slug]; ok {
		return b, nil
	}
	b, err := PageScript(s.site, slug)
	if err == nil {
		s.scripts[slug] = b
	}
	return b, err
}

// scriptSlug is the page whose script name is, pages/<slug>.js, if it names one.
func scriptSlug(name string) (string, bool) {
	dir, file := path.Split(name)
	slug, ok := strings.CutSuffix(file, scriptExt)
	if !ok || dir != pagesDir+"/" {
		return "", false
	}
	return slug, slices.ContainsFunc(all(), func(p Page) bool { return p.Slug == slug })
}

func (s *grimoireFS) Open(name string) (fs.File, error) {
	made := s.page
	if name != index {
		slug, ok := scriptSlug(name)
		if !ok {
			return s.open(name)
		}
		made = func() ([]byte, error) { return s.script(slug) }
	}
	b, err := made()
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: err}
	}
	return &madeFile{Reader: bytes.NewReader(b), info: madeInfo{path.Base(name), int64(len(b))}}, nil
}

// open opens a file of the site; the root and the pages folder list what is made in them, with its size.
func (s *grimoireFS) open(name string) (fs.File, error) {
	f, err := s.site.Open(name)
	if err != nil || (name != "." && name != pagesDir) {
		return f, err
	}
	dir, ok := f.(fs.ReadDirFile)
	if !ok {
		return f, nil
	}
	made := []string{index}
	if name == pagesDir {
		made = nil
		for _, p := range all() {
			made = append(made, pagesDir+"/"+p.Slug+scriptExt)
		}
	}
	return &madeDir{ReadDirFile: dir, fsys: s, made: made}, nil
}

// madeDir is a folder of the site, opened, that lists what the Grimoire makes in it: index.html, with the size it has
// once made, in place of the file with its markers; each page's script beside its Markdown. A walk of the site, as
// os.CopyFS does, copies them.
type madeDir struct {
	fs.ReadDirFile
	fsys    *grimoireFS
	made    []string
	entries []fs.DirEntry
	read    bool
}

func (d *madeDir) ReadDir(n int) ([]fs.DirEntry, error) {
	if !d.read {
		d.read = true
		entries, err := d.ReadDirFile.ReadDir(-1)
		if err != nil {
			return nil, err
		}
		for _, name := range d.made {
			f, err := d.fsys.Open(name)
			if err != nil {
				return nil, err
			}
			info, _ := f.Stat()
			f.Close()
			entries = slices.DeleteFunc(entries, func(e fs.DirEntry) bool { return e.Name() == info.Name() })
			entries = append(entries, fs.FileInfoToDirEntry(info))
		}
		slices.SortFunc(entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
		d.entries = entries
	}
	if n <= 0 {
		all := d.entries
		d.entries = nil
		return all, nil
	}
	if len(d.entries) == 0 {
		return nil, io.EOF
	}
	n = min(n, len(d.entries))
	part := d.entries[:n]
	d.entries = d.entries[n:]
	return part, nil
}

// madeFile is a file the Grimoire makes, opened. It seeks, as http.FileServerFS needs.
type madeFile struct {
	*bytes.Reader
	info madeInfo
}

func (f *madeFile) Stat() (fs.FileInfo, error) { return f.info, nil }
func (f *madeFile) Close() error               { return nil }

var _ io.ReadSeeker = (*madeFile)(nil)

type madeInfo struct {
	name string
	size int64
}

func (i madeInfo) Name() string     { return i.name }
func (i madeInfo) Size() int64      { return i.size }
func (madeInfo) Mode() fs.FileMode  { return 0o444 }
func (madeInfo) ModTime() time.Time { return time.Time{} }
func (madeInfo) IsDir() bool        { return false }
func (madeInfo) Sys() any           { return nil }

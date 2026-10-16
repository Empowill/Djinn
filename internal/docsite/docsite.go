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
	"strings"
	"sync"
	"time"

	"github.com/empowill/djinn/internal/cli"
)

// Marker is where index.html takes the reference of the command line.
const Marker = "<!-- djinn:commands -->"

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

// FS is the site with the reference of the command line in index.html, in place of Marker.
func FS(site fs.FS) fs.FS {
	return &withCommands{site: site}
}

type withCommands struct {
	site  fs.FS
	once  sync.Once
	index []byte
	err   error
}

// page is index.html with the commands and the Grimoire's navigation, pages and words, made once, when first asked.
func (s *withCommands) page() ([]byte, error) {
	s.once.Do(func() {
		var b []byte
		if b, s.err = fs.ReadFile(s.site, index); s.err != nil {
			return
		}
		b = bytes.Replace(b, []byte(Marker), Commands(), 1)
		if bytes.Contains(b, []byte(PagesMarker)) {
			var pages []byte
			if pages, s.err = Pages(s.site); s.err != nil {
				return
			}
			b = bytes.Replace(b, []byte(PagesMarker), pages, 1)
		}
		b = bytes.Replace(b, []byte(NavMarker), NavHTML(), 1)
		s.index = bytes.Replace(b, []byte(LabelsMarker), Labels(), 1)
	})
	return s.index, s.err
}

func (s *withCommands) Open(name string) (fs.File, error) {
	if name == index {
		b, err := s.page()
		if err != nil {
			return nil, &fs.PathError{Op: "open", Path: name, Err: err}
		}
		return &pageFile{Reader: bytes.NewReader(b), size: int64(len(b))}, nil
	}
	f, err := s.site.Open(name)
	if dir, ok := f.(fs.ReadDirFile); ok && name == "." {
		return &root{ReadDirFile: dir, page: s.page}, nil
	}
	return f, err
}

// root is the site's root directory, opened: it gives index.html the size of the page with its commands, as a walk
// of the site, as os.CopyFS does, reads it.
type root struct {
	fs.ReadDirFile
	page func() ([]byte, error)
}

func (d *root) ReadDir(n int) ([]fs.DirEntry, error) {
	entries, err := d.ReadDirFile.ReadDir(n)
	for i, e := range entries {
		if e.Name() == index {
			b, perr := d.page()
			if perr != nil {
				return entries[:i], perr
			}
			entries[i] = fs.FileInfoToDirEntry(pageInfo(len(b)))
		}
	}
	return entries, err
}

// pageFile is index.html with its commands, opened. It seeks, as http.FileServerFS needs.
type pageFile struct {
	*bytes.Reader
	size int64
}

func (f *pageFile) Stat() (fs.FileInfo, error) { return pageInfo(f.size), nil }
func (f *pageFile) Close() error               { return nil }

var _ io.ReadSeeker = (*pageFile)(nil)

type pageInfo int64

func (pageInfo) Name() string       { return index }
func (i pageInfo) Size() int64      { return int64(i) }
func (pageInfo) Mode() fs.FileMode  { return 0o444 }
func (pageInfo) ModTime() time.Time { return time.Time{} }
func (pageInfo) IsDir() bool        { return false }
func (pageInfo) Sys() any           { return nil }

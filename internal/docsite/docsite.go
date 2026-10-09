// Package docsite is Djinn's documentation site, docs/site, with the OpenAPI document as the script its API tab
// reads: what `go tool task docs` writes into bin/docs, and what djinn up serves at /docs/.
package docsite

import (
	"bytes"
	"io"
	"io/fs"
	"slices"
	"strings"
	"time"
)

// ScriptName is the file of the script, beside index.html.
const ScriptName = "openapi.js"

// Script wraps the OpenAPI document as a script: a page opened from a file may load a script, never fetch a file.
func Script(spec []byte) []byte {
	return slices.Concat(
		[]byte("// The OpenAPI document of Djinn, docs/openapi.json, for the API tab of the documentation.\n"),
		[]byte("window.DJINN_OPENAPI = "), bytes.TrimSpace(spec), []byte(";\n"),
	)
}

// FS is the site with Script(spec) beside its index.html.
func FS(site fs.FS, spec []byte) fs.FS {
	return withScript{site: site, script: Script(spec)}
}

type withScript struct {
	site   fs.FS
	script []byte
}

func (s withScript) Open(name string) (fs.File, error) {
	if name == ScriptName {
		return &scriptFile{Reader: bytes.NewReader(s.script), size: int64(len(s.script))}, nil
	}
	f, err := s.site.Open(name)
	if dir, ok := f.(fs.ReadDirFile); ok && name == "." {
		return &root{ReadDirFile: dir, script: fs.FileInfoToDirEntry(scriptInfo(int64(len(s.script))))}, nil
	}
	return f, err
}

// root is the site's root directory, opened: it lists the script too, so that a walk of the site, as os.CopyFS
// does, finds it.
type root struct {
	fs.ReadDirFile
	script  fs.DirEntry
	entries []fs.DirEntry
	read    bool
}

func (d *root) ReadDir(n int) ([]fs.DirEntry, error) {
	if !d.read {
		all, err := d.ReadDirFile.ReadDir(-1)
		if err != nil {
			return nil, err
		}
		all = slices.DeleteFunc(all, func(e fs.DirEntry) bool { return e.Name() == ScriptName })
		d.entries = append(all, d.script)
		slices.SortFunc(d.entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
		d.read = true
	}
	if n <= 0 {
		out := d.entries
		d.entries = nil
		return out, nil
	}
	if len(d.entries) == 0 {
		return nil, io.EOF
	}
	out := d.entries[:min(n, len(d.entries))]
	d.entries = d.entries[len(out):]
	return out, nil
}

// scriptFile is the script, opened. It seeks, as http.FileServerFS needs.
type scriptFile struct {
	*bytes.Reader
	size int64
}

func (f *scriptFile) Stat() (fs.FileInfo, error) { return scriptInfo(f.size), nil }
func (f *scriptFile) Close() error               { return nil }

type scriptInfo int64

func (scriptInfo) Name() string       { return ScriptName }
func (i scriptInfo) Size() int64      { return int64(i) }
func (scriptInfo) Mode() fs.FileMode  { return 0o444 }
func (scriptInfo) ModTime() time.Time { return time.Time{} }
func (scriptInfo) IsDir() bool        { return false }
func (scriptInfo) Sys() any           { return nil }

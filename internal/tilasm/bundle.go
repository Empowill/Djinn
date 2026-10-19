// Package tilasm builds what a tilasm publishes on claude.ai: its page with the files beside it, at their paths, as the
// window serves them. Djinn builds it, never a model: the same folder gives the same bytes.
//
// The Artifact tool publishes no page its session has not read whole, and Read takes 256 KB at a time, lines of 2,000
// characters (W493): a page with its images inlined, megabytes of base64, cannot be published. So the page goes as it
// is, its files beside it, and the images and fonts a page holds inlined, as data: addresses, go out of it into files.
package tilasm

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/net/html"
)

// The limits of a claude.ai artifact, as its Artifact tool states them: a page of 16 MB at most; beside it at most
// 255 files, 16 MB each for a text file and 15 MB for any other, 64 MB per publish.
const (
	PageLimit       = 16 << 20
	TextFileLimit   = 16 << 20
	BinaryFileLimit = 15 << 20
	SendLimit       = 64 << 20
	MaxFiles        = 255
)

// Index is the page a tilasm opens on.
const Index = "index.html"

// Outside is the folder the data: addresses of a page go to, by their content: outside/<hash>.png.
const Outside = "outside"

// outsideFrom is the length of base64 from which a data: address goes out of the page: Read cuts a line at 2,000
// characters.
const outsideFrom = 1500

// served are the extensions claude.ai serves beside a page, as its Artifact tool lists them (claude 2.1.303, W493):
// a file of another type fails the whole publish. Each with the media type a data: address of it says.
var served = map[string]string{
	".html": "text/html", ".css": "text/css", ".js": "text/javascript", ".mjs": "text/javascript",
	".wasm": "application/wasm", ".json": "application/json", ".map": "application/json",
	".geojson": "application/geo+json", ".webmanifest": "application/manifest+json", ".txt": "text/plain",
	".md": "text/markdown", ".csv": "text/csv", ".xml": "application/xml", ".svg": "image/svg+xml",
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp",
	".avif": "image/avif", ".ico": "image/x-icon", ".woff": "font/woff", ".woff2": "font/woff2", ".ttf": "font/ttf",
	".otf": "font/otf", ".mp3": "audio/mpeg", ".wav": "audio/wav", ".ogg": "audio/ogg", ".mp4": "video/mp4",
	".webm": "video/webm", ".pdf": "application/pdf",
}

// Bundle is a tilasm made ready to publish, in a folder of its own.
type Bundle struct {
	// Dir holds the page, Index, and the files beside it.
	Dir string
	// Files are the files beside the page, relative to Dir, with forward slashes, sorted.
	Files []string
	// Left are the tilasm's files left out, of a type claude.ai does not serve: their links lead nowhere there.
	Left []string
	// Outside counts the data: addresses that went out of the page into files of Outside.
	Outside int
	// Size is what is sent, in bytes: the page and the files beside it.
	Size int64
}

// TooLargeError is why a tilasm cannot be published: it does not fit an artifact's limits.
type TooLargeError struct{ Why string }

func (e *TooLargeError) Error() string { return e.Why }

// Build makes the tilasm's version folder src ready to publish, in dst, a folder that exists and is empty: its page,
// its large data: addresses moved out into files, and every file of a type claude.ai serves, at its path. A tilasm
// over an artifact's limits is refused with a TooLargeError error.
func Build(src, dst string) (*Bundle, error) {
	page, err := os.ReadFile(filepath.Join(src, Index))
	if err != nil {
		return nil, fmt.Errorf("read the tilasm's page: %w", err)
	}
	b := &Bundle{Dir: dst}
	out := &outsider{dst: dst, taken: map[string]bool{}}
	if page, err = out.page(page); err != nil {
		return nil, err
	}
	if len(page) > PageLimit {
		return nil, &TooLargeError{Why: fmt.Sprintf("its page weighs %s, over the %s of a page on claude.ai", mib(int64(len(page))), mib(PageLimit))}
	}
	if err := os.WriteFile(filepath.Join(dst, Index), page, 0o600); err != nil {
		return nil, err
	}
	b.Size, b.Outside = int64(len(page))+out.size, len(out.files)
	b.Files = append(b.Files, out.files...)
	var copies []string // The tilasm's files that go, relative to src, checked before any is copied.
	err = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)
		switch {
		case name == Index:
			return nil
		case !d.Type().IsRegular() || served[strings.ToLower(path.Ext(name))] == "" || out.taken[name]:
			b.Left = append(b.Left, name)
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if limit, kind := fileLimit(name); info.Size() > limit {
			return &TooLargeError{Why: fmt.Sprintf("%s weighs %s, over the %s of a %s on claude.ai", name, mib(info.Size()), mib(limit), kind)}
		}
		b.Size += info.Size()
		b.Files = append(b.Files, name)
		copies = append(copies, rel)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(b.Files)
	sort.Strings(b.Left)
	switch {
	case len(b.Files) > MaxFiles:
		return nil, &TooLargeError{Why: fmt.Sprintf("it holds %d files beside its page, over the %d of an artifact", len(b.Files), MaxFiles)}
	case b.Size > SendLimit:
		return nil, &TooLargeError{Why: fmt.Sprintf("its files weigh %s, over the %s of a publish on claude.ai", mib(b.Size), mib(SendLimit))}
	}
	for _, rel := range copies {
		if err := copyFile(filepath.Join(src, rel), filepath.Join(dst, rel)); err != nil {
			return nil, err
		}
	}
	return b, nil
}

// fileLimit is the most a file beside the page may weigh, and what kind of file the limit is for.
func fileLimit(name string) (int64, string) {
	t := served[strings.ToLower(path.Ext(name))]
	if strings.HasPrefix(t, "text/") || t == "image/svg+xml" || strings.HasPrefix(t, "application/") && strings.Contains(t, "json") {
		return TextFileLimit, "text file"
	}
	return BinaryFileLimit, "file"
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// mib writes n bytes in MiB, one decimal: 16.0 MiB.
func mib(n int64) string { return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20)) }

// outsider moves the large data: addresses of a page out into files of the folder dst.
type outsider struct {
	dst   string
	files []string        // the files written, relative to dst
	taken map[string]bool // the same, as a set: a file of the tilasm at one of these paths is left out
	size  int64
}

// page is the page with its large data: addresses of a served type moved out: in its tags' attributes and in its style
// sheets, never in a script, which may read one as it is. Everything else stays as written, byte for byte.
func (o *outsider) page(page []byte) ([]byte, error) {
	var out bytes.Buffer
	z := html.NewTokenizer(bytes.NewReader(page))
	style := false // In a <style>, whose text is a style sheet.
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			if err := z.Err(); !errors.Is(err, io.EOF) {
				return nil, fmt.Errorf("read the tilasm's page: %w", err)
			}
			return out.Bytes(), nil
		case html.TextToken:
			if style {
				css, err := o.css(string(z.Raw()))
				if err != nil {
					return nil, err
				}
				out.WriteString(css)
				continue
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			raw := clone(z.Raw())
			tok := z.Token()
			style = tt == html.StartTagToken && tok.Data == "style"
			changed, err := o.attrs(&tok)
			if err != nil {
				return nil, err
			}
			if changed {
				out.WriteString(tok.String())
			} else {
				out.Write(raw)
			}
			continue
		case html.EndTagToken:
			style = false
		}
		out.Write(z.Raw())
	}
}

// clone copies b: Token lowercases the tag's name in the tokenizer's buffer.
func clone(b []byte) []byte { return append([]byte(nil), b...) }

// attrs moves the data: addresses of a tag's attributes out; changed tells whether one moved.
func (o *outsider) attrs(tok *html.Token) (changed bool, err error) {
	for i, a := range tok.Attr {
		v := a.Val
		switch {
		case a.Key == "style":
			v, err = o.css(v)
		case a.Key == "srcset":
			v, err = o.srcset(v)
		case strings.HasPrefix(a.Val, "data:"):
			v, err = o.data(v)
		}
		if err != nil {
			return false, err
		}
		if v != a.Val {
			tok.Attr[i].Val, changed = v, true
		}
	}
	return changed, nil
}

// cssData is a data: address in a style sheet's url(), quoted or not.
var cssData = regexp.MustCompile(`url\(\s*(["']?)(data:[^"')]+)(["']?)\s*\)`)

// css is a style sheet, or a style attribute, its large data: addresses moved out.
func (o *outsider) css(css string) (string, error) {
	var err error
	return cssData.ReplaceAllStringFunc(css, func(m string) string {
		sub := cssData.FindStringSubmatch(m)
		ref, e := o.data(sub[2])
		if e != nil {
			err = e
		}
		if ref == sub[2] {
			return m
		}
		return `url("` + ref + `")`
	}), err
}

// srcsetData is a data: candidate of a srcset: a comma within it, none after its payload.
var srcsetData = regexp.MustCompile(`data:[^\s,]+,[A-Za-z0-9+/=]+`)

// srcset is a srcset attribute, each data: candidate moved out.
func (o *outsider) srcset(v string) (string, error) {
	var err error
	return srcsetData.ReplaceAllStringFunc(v, func(m string) string {
		ref, e := o.data(m)
		if e != nil {
			err = e
		}
		return ref
	}), err
}

// dataURL is a base64 data: address: its media type, then its payload.
var dataURL = regexp.MustCompile(`^data:([a-zA-Z0-9.+-]+/[a-zA-Z0-9.+-]+)(?:;[^,;]+=[^,;]*)*;base64,([A-Za-z0-9+/=\s]+)$`)

// data is the path of the file a large base64 data: address of a served type went to, outside/<hash><ext>; else ref
// as it is.
func (o *outsider) data(ref string) (string, error) {
	m := dataURL.FindStringSubmatch(ref)
	if m == nil || len(m[2]) < outsideFrom {
		return ref, nil
	}
	ext := extension(strings.ToLower(m[1]))
	if ext == "" {
		return ref, nil
	}
	b, bad := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(m[2]), ""))
	if bad != nil {
		return ref, nil //nolint:nilerr // A payload badly encoded stays in the page, as written.
	}
	sum := sha256.Sum256(b)
	name := Outside + "/" + hex.EncodeToString(sum[:8]) + ext
	if !o.taken[name] {
		if err := os.MkdirAll(filepath.Join(o.dst, Outside), 0o700); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(o.dst, filepath.FromSlash(name)), b, 0o600); err != nil {
			return "", err
		}
		o.taken[name] = true
		o.files = append(o.files, name)
		o.size += int64(len(b))
	}
	return name, nil
}

// extension is the served extension of a media type, the first in a stable order; empty when none is served.
func extension(mediaType string) string {
	var exts []string
	for ext, t := range served {
		if t == mediaType {
			exts = append(exts, ext)
		}
	}
	sort.Strings(exts)
	if len(exts) == 0 {
		return ""
	}
	return exts[0]
}

package tilasm

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/empowill/djinn/internal/testx"
)

func TestMain(m *testing.M) { testx.Main(m) }

// tree writes files into a new folder and returns it; a size writes that many bytes, sparse.
func tree(t *testing.T, files map[string]any) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		switch c := content.(type) {
		case string:
			if err := os.WriteFile(p, []byte(c), 0o600); err != nil {
				t.Fatal(err)
			}
		case int64:
			f, err := os.Create(p)
			if err != nil {
				t.Fatal(err)
			}
			if err := errors.Join(f.Truncate(c), f.Close()); err != nil {
				t.Fatal(err)
			}
		}
	}
	return dir
}

// png is a data: address of a PNG of n bytes, its first bytes a PNG's.
func png(n int) (data string, raw []byte) {
	raw = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{7}, n)...)
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw), raw
}

func TestBuild(t *testing.T) {
	t.Parallel()
	big, bigRaw := png(4000)
	other, _ := png(5000)
	small, _ := png(20)
	cases := []struct {
		name  string
		files map[string]any
		// page is what the published page must be; empty: the tilasm's page as it is.
		page      string
		files2    []string // the files beside the page
		left      []string
		outside   int
		tooLarge  string // a part of the refusal; empty: no refusal
		outsideIs []byte // what the first file of outside/ holds
	}{
		{
			name:   "a page alone goes alone",
			files:  map[string]any{"index.html": "<!doctype html><title>Alone</title><p>Hi</p>"},
			files2: nil,
		},
		{
			name: "the files of a folder go beside the page, the page as it is",
			files: map[string]any{
				"index.html":         `<link rel="stylesheet" href="css/a.css"><img src="img/one.png"><a href="other.html">More</a>`,
				"css/a.css":          `body{background:url(../img/one.png)}`,
				"img/one.png":        "png",
				"other.html":         "<p>Other</p>",
				"fonts/a.woff2":      "font",
				"fonts/OFL-font.txt": "license",
			},
			files2: []string{"css/a.css", "fonts/OFL-font.txt", "fonts/a.woff2", "img/one.png", "other.html"},
		},
		{
			name: "a spreadsheet, a document, a script source are left out: claude.ai serves none",
			files: map[string]any{
				"index.html":           `<a href="files/budget.xlsx">Budget</a>`,
				"files/budget.xlsx":    "xlsx",
				"files/note.docx":      "docx",
				"spec.ts":              "test()",
				"data/numbers.csv":     "a,b",
				"compagnon/report.pdf": "%PDF",
			},
			files2: []string{"compagnon/report.pdf", "data/numbers.csv"},
			left:   []string{"files/budget.xlsx", "files/note.docx", "spec.ts"},
		},
		{
			name: "large data: addresses go out of the page into files, small ones and scripts' stay",
			files: map[string]any{
				"index.html": `<img src="` + big + `" alt="x"><img src="` + small + `">` +
					`<style>.a{background:url('` + big + `')}</style>` +
					`<div style="background-image: url(` + other + `)"></div>` +
					`<img srcset="` + big + ` 1x, img/b.png 2x">` +
					`<script>const keep = "` + big + `";</script>`,
				"img/b.png": "png",
			},
			page: `<img src="outside/%[1]s.png" alt="x"><img src="` + small + `">` +
				`<style>.a{background:url("outside/%[1]s.png")}</style>` +
				`<div style="background-image: url(&#34;outside/%[2]s.png&#34;)"></div>` +
				`<img srcset="outside/%[1]s.png 1x, img/b.png 2x">` +
				`<script>const keep = "` + big + `";</script>`,
			files2:    []string{"img/b.png", "outside/%[1]s.png", "outside/%[2]s.png"},
			outside:   2,
			outsideIs: bigRaw,
		},
		{
			name:     "a file over the limit of a file is refused, naming it",
			files:    map[string]any{"index.html": "<p>Video</p>", "media/film.mp4": int64(BinaryFileLimit + 1)},
			tooLarge: "media/film.mp4 weighs 15.0 MiB, over the 15.0 MiB of a file",
		},
		{
			name:     "a page over the limit of a page is refused",
			files:    map[string]any{"index.html": int64(PageLimit + 1)},
			tooLarge: "its page weighs 16.0 MiB, over the 16.0 MiB of a page",
		},
		{
			name: "more than a publish holds is refused",
			files: map[string]any{"index.html": "<p>Films</p>", "a.mp4": int64(14 << 20), "b.mp4": int64(14 << 20),
				"c.mp4": int64(14 << 20), "d.mp4": int64(14 << 20), "e.mp4": int64(14 << 20)},
			tooLarge: "its files weigh 70.0 MiB, over the 64.0 MiB of a publish",
		},
		{
			name: "more files than an artifact holds are refused",
			files: func() map[string]any {
				files := map[string]any{"index.html": "<p>Many</p>"}
				for i := range MaxFiles + 1 {
					files[fmt.Sprintf("img/%03d.png", i)] = "png"
				}
				return files
			}(),
			tooLarge: "it holds 256 files beside its page, over the 255 of an artifact",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			src := tree(t, c.files)
			dst := t.TempDir()
			b, err := Build(src, dst)
			if c.tooLarge != "" {
				var large *TooLarge
				if !errors.As(err, &large) || !strings.Contains(err.Error(), c.tooLarge) {
					t.Fatalf("Build = %v, %v; want a refusal saying %q", b, err, c.tooLarge)
				}
				if entries, _ := os.ReadDir(dst); len(entries) > 1 {
					t.Errorf("a refused tilasm copied %d files: none is copied before the limits are checked", len(entries))
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var hashes []any
			for _, f := range b.Files {
				if name, ok := strings.CutPrefix(f, Outside+"/"); ok {
					hashes = append(hashes, strings.TrimSuffix(name, ".png"))
				}
			}
			want := c.page
			if want == "" {
				want, _ = c.files["index.html"].(string)
			} else {
				want = fmt.Sprintf(want, hashes...)
			}
			got, err := os.ReadFile(filepath.Join(dst, Index))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != want {
				t.Errorf("page =\n%.400s\nwant\n%.400s", got, want)
			}
			var files []string
			for _, f := range c.files2 {
				if strings.Contains(f, "%") {
					f = fmt.Sprintf(f, hashes...)
				}
				files = append(files, f)
			}
			slices.Sort(files)
			if !slices.Equal(b.Files, files) || !slices.Equal(b.Left, c.left) || b.Outside != c.outside {
				t.Errorf("files %v, left %v, outside %d; want %v, %v, %d", b.Files, b.Left, b.Outside, files, c.left, c.outside)
			}
			var size int64
			for _, f := range append([]string{Index}, b.Files...) {
				info, err := os.Stat(filepath.Join(dst, filepath.FromSlash(f)))
				if err != nil {
					t.Fatalf("%s is not in the bundle: %v", f, err)
				}
				size += info.Size()
			}
			if size != b.Size {
				t.Errorf("size = %d, want %d, the bytes of the page and its files", b.Size, size)
			}
			if c.outsideIs != nil {
				if raw, _ := os.ReadFile(filepath.Join(dst, Outside, hashes[0].(string)+".png")); !bytes.Equal(raw, c.outsideIs) {
					t.Errorf("outside file holds %d bytes, want the %d of the data: address", len(raw), len(c.outsideIs))
				}
			}
			again := t.TempDir()
			if _, err := Build(src, again); err != nil {
				t.Fatal(err)
			}
			if second, _ := os.ReadFile(filepath.Join(again, Index)); !bytes.Equal(second, got) {
				t.Error("a second build of the same folder gave other bytes")
			}
		})
	}
}

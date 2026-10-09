package docsite

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestFSAddsTheScript(t *testing.T) {
	site := fstest.MapFS{
		"index.html":    {Data: []byte("<html></html>")},
		"site.css":      {Data: []byte("body{}")},
		"fonts/a.woff2": {Data: []byte("font")},
	}
	fsys := FS(site, []byte("{\"openapi\": \"3.1.0\"}\n"))
	b, err := fs.ReadFile(fsys, ScriptName)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "window.DJINN_OPENAPI = {\"openapi\": \"3.1.0\"};\n") {
		t.Errorf("script %q", b)
	}
	f, err := fsys.Open(ScriptName)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.(io.Seeker); !ok {
		t.Error("the script does not seek: http.FileServerFS would refuse it")
	}
	f.Close()
	// What a walk finds, as os.CopyFS walks: the site and the script, nothing else.
	out := t.TempDir()
	if err := os.CopyFS(out, fsys); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"index.html", "site.css", "fonts/a.woff2", ScriptName} {
		if _, err := os.Stat(filepath.Join(out, name)); err != nil {
			t.Error(err)
		}
	}
	if err := fstest.TestFS(fsys, "index.html", "site.css", "fonts/a.woff2", ScriptName); err != nil {
		t.Error(err)
	}
}

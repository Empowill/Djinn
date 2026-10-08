package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fixture lays out a source root with the notices, and a release folder holding one binary.
func fixture(t *testing.T, binary string) (root, dir string) {
	t.Helper()
	root = t.TempDir()
	for _, name := range notices {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name+" text\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir = filepath.Join(t.TempDir(), "djinn_test_amd64")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, binary), []byte("binary"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, dir
}

func TestPackTarGz(t *testing.T) {
	root, dir := fixture(t, "djinn")
	out, err := pack(dir, root)
	if err != nil {
		t.Fatal(err)
	}
	if out != dir+".tar.gz" {
		t.Fatalf("archive %s, want %s.tar.gz", out, dir)
	}
	f, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got[hdr.Name] = hdr.Mode
	}
	want := map[string]int64{
		"djinn_test_amd64/djinn":                  0o755,
		"djinn_test_amd64/LICENSE":                0o644,
		"djinn_test_amd64/NOTICE":                 0o644,
		"djinn_test_amd64/THIRD_PARTY_NOTICES.md": 0o644,
	}
	if len(got) != len(want) {
		t.Fatalf("entries %v, want %v", got, want)
	}
	for name, m := range want {
		if got[name] != m {
			t.Errorf("%s: mode %o, want %o (entries %v)", name, got[name], m, got)
		}
	}
}

func TestPackZipForWindows(t *testing.T) {
	root, dir := fixture(t, "djinn.exe")
	out, err := pack(dir, root)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	slices.Sort(names)
	want := []string{"djinn_test_amd64/LICENSE", "djinn_test_amd64/NOTICE", "djinn_test_amd64/THIRD_PARTY_NOTICES.md",
		"djinn_test_amd64/djinn.exe"}
	if !slices.Equal(names, want) {
		t.Fatalf("entries %v, want %v", names, want)
	}
}

func TestPackNeedsNotices(t *testing.T) {
	root, dir := fixture(t, "djinn")
	if err := os.Remove(filepath.Join(root, "NOTICE")); err != nil {
		t.Fatal(err)
	}
	if _, err := pack(dir, root); err == nil {
		t.Fatal("pack without NOTICE succeeded, want an error: a release never ships without its notices")
	}
}

func TestSums(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{"b.zip": "bee", "a.tar.gz": "ay", SumsFile: "stale"}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "djinn_linux_amd64"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := sums(dir); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, SumsFile))
	if err != nil {
		t.Fatal(err)
	}
	hash := func(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
	want := hash("ay") + "  a.tar.gz\n" + hash("bee") + "  b.zip\n"
	if string(b) != want {
		t.Fatalf("SHA256SUMS:\n%s\nwant:\n%s", b, want)
	}
	if strings.Contains(string(b), "djinn_linux_amd64") {
		t.Fatal("a folder was summed")
	}
}

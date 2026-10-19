package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
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
		file := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(name+" text\n"), 0o644); err != nil {
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

// TestRun: the command line packs or sums a folder, and says how it is used otherwise.
func TestRun(t *testing.T) {
	t.Parallel()
	release := t.TempDir()
	if err := os.WriteFile(filepath.Join(release, "a.tar.gz"), []byte("ay"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name         string
		args         []string
		code         int
		stdout, errs string
	}{
		{name: "no command", code: 2, errs: "usage: releasepack pack <folder> | releasepack sums <folder>"},
		{name: "unknown command", args: []string{"unpack", release}, code: 2, errs: "usage:"},
		{name: "pack without a folder", args: []string{"pack"}, code: 2, errs: "usage:"},
		// The notices are read from the working folder, the module's root under go run: here, none.
		{name: "pack away from the module", args: []string{"pack", release}, code: 1, errs: "releasepack: "},
		{name: "sums a folder", args: []string{"sums", release}},
		{name: "sums a missing folder", args: []string{"sums", filepath.Join(release, "none")}, code: 1, errs: "releasepack: "},
	} {
		t.Run(c.name, func(t *testing.T) {
			var stdout, stderr strings.Builder
			if code := run(c.args, &stdout, &stderr); code != c.code {
				t.Fatalf("exit code %d, want %d; stderr: %s", code, c.code, stderr.String())
			}
			if stdout.String() != c.stdout || !strings.Contains(stderr.String(), c.errs) || (c.errs == "") != (stderr.Len() == 0) {
				t.Fatalf("stdout %q, stderr %q; want %q and %q", stdout.String(), stderr.String(), c.stdout, c.errs)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(release, SumsFile)); err != nil {
		t.Errorf("sums wrote no %s: %v", SumsFile, err)
	}
}

// TestPack: the notices join the binary, in a .tar.gz that keeps the executable bit, or a .zip for Windows.
func TestPack(t *testing.T) {
	t.Parallel()
	t.Run("tar.gz", func(t *testing.T) {
		t.Parallel()
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
	})
	t.Run("zip for Windows", func(t *testing.T) {
		t.Parallel()
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
		modes := map[string]fs.FileMode{}
		for _, f := range zr.File {
			names = append(names, f.Name)
			modes[f.Name] = f.Mode()
		}
		slices.Sort(names)
		want := []string{"djinn_test_amd64/LICENSE", "djinn_test_amd64/NOTICE", "djinn_test_amd64/THIRD_PARTY_NOTICES.md",
			"djinn_test_amd64/djinn.exe"}
		if !slices.Equal(names, want) {
			t.Fatalf("entries %v, want %v", names, want)
		}
		if modes["djinn_test_amd64/djinn.exe"] != 0o755 || modes["djinn_test_amd64/NOTICE"] != 0o644 {
			t.Errorf("modes %v, want djinn.exe 0755 and the notices 0644", modes)
		}
	})
	t.Run("needs the notices", func(t *testing.T) {
		t.Parallel()
		root, dir := fixture(t, "djinn")
		if err := os.Remove(filepath.Join(root, "NOTICE")); err != nil {
			t.Fatal(err)
		}
		if _, err := pack(dir, root); err == nil {
			t.Fatal("pack without NOTICE succeeded, want an error: a release never ships without its notices")
		}
	})
	t.Run("needs the folder", func(t *testing.T) {
		t.Parallel()
		root, dir := fixture(t, "djinn")
		if _, err := pack(filepath.Join(dir, "none"), root); err == nil {
			t.Fatal("pack of a missing folder succeeded")
		}
	})
}

// TestWrite: a failure, to create the file or to fill it, leaves no file behind.
func TestWrite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, c := range []struct {
		name, path string
		fill       func(io.Writer) error
	}{
		{name: "fill fails", path: filepath.Join(dir, "half.tar.gz"), fill: func(w io.Writer) error {
			_, _ = io.WriteString(w, "half")
			return errors.New("no more")
		}},
		{name: "create fails", path: filepath.Join(dir, "none", "a.tar.gz"), fill: func(io.Writer) error { return nil }},
	} {
		t.Run(c.name, func(t *testing.T) {
			if err := write(c.path, c.fill); err == nil {
				t.Fatal("write succeeded")
			}
			if _, err := os.Stat(c.path); !os.IsNotExist(err) {
				t.Fatalf("%s is left: %v", c.path, err)
			}
		})
	}
}

// TestTarFiles: a file gone between the walk and the archive fails it.
func TestTarFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := tarFiles(io.Discard, dir, []string{filepath.Join(dir, "gone")}); err == nil {
		t.Fatal("tarFiles took a missing file")
	}
}

// TestZipFiles: a file gone between the walk and the archive fails it.
func TestZipFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := zipFiles(io.Discard, dir, []string{filepath.Join(dir, "gone")}); err == nil {
		t.Fatal("zipFiles took a missing file")
	}
}

// TestSums: SHA256SUMS lists the files of the folder, sorted, as sha256sum prints them; never itself, nor a folder.
func TestSums(t *testing.T) {
	t.Parallel()
	t.Run("sorted files only", func(t *testing.T) {
		t.Parallel()
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
		want := hash("ay") + "  a.tar.gz\n" + hash("bee") + "  b.zip\n"
		if string(b) != want {
			t.Fatalf("SHA256SUMS:\n%s\nwant:\n%s", b, want)
		}
		if strings.Contains(string(b), "djinn_linux_amd64") {
			t.Fatal("a folder was summed")
		}
	})
	t.Run("nothing to sum", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := sums(dir); err == nil || !strings.Contains(err.Error(), "holds no file to sum") {
			t.Fatalf("sums of an empty folder: %v", err)
		}
		if _, err := os.Stat(filepath.Join(dir, SumsFile)); !os.IsNotExist(err) {
			t.Fatalf("an empty %s was written", SumsFile)
		}
	})
	t.Run("missing folder", func(t *testing.T) {
		t.Parallel()
		if err := sums(filepath.Join(t.TempDir(), "none")); err == nil {
			t.Fatal("sums of a missing folder succeeded")
		}
	})
}

func TestSha256File(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "a")
	if err := os.WriteFile(p, []byte("ay"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := sha256File(p); err != nil || got != hash("ay") {
		t.Fatalf("sha256File = %s, %v; want %s", got, err, hash("ay"))
	}
	if _, err := sha256File(filepath.Join(dir, "none")); err == nil {
		t.Fatal("sha256File of a missing file succeeded")
	}
}

// hash is the hexadecimal SHA-256 of s.
func hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

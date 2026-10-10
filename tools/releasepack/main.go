// Command releasepack packs release archives and their checksums. It is a build tool for `task release-build`,
// kept apart from the djinn command, and portable where tar, zip and sha256sum are not (Windows).
//
//	go run ./tools/releasepack pack bin/release/djinn_linux_amd64   # → bin/release/djinn_linux_amd64.tar.gz
//	go run ./tools/releasepack sums bin/release                     # → bin/release/SHA256SUMS
//
// pack adds the licence notices to the folder, then archives it whole: a .zip when it holds djinn.exe, a .tar.gz
// otherwise. sums writes the SHA-256 of every file of a folder, in the format of sha256sum.
package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// Notices travel with every binary: Apache-2.0 asks for the licence and NOTICE, and the adapted code its credits.
// notices are the licence notices, by their path in the module; each goes at the archive's top, by its name.
var notices = []string{"LICENSE", "NOTICE", "docs/THIRD_PARTY_NOTICES.md"}

// SumsFile is the name of the checksum file that sums writes.
const SumsFile = "SHA256SUMS"

func main() {
	var err error
	switch {
	case len(os.Args) == 3 && os.Args[1] == "pack":
		var out string
		out, err = pack(os.Args[2], ".")
		if err == nil {
			fmt.Println(out)
		}
	case len(os.Args) == 3 && os.Args[1] == "sums":
		err = sums(os.Args[2])
	default:
		fmt.Fprintln(os.Stderr, "usage: releasepack pack <folder> | releasepack sums <folder>")
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "releasepack:", err)
		os.Exit(1)
	}
}

// pack copies the notices found in root into dir, then archives dir beside it, under its own name. It returns the
// archive's path.
func pack(dir, root string) (string, error) {
	dir = filepath.Clean(dir)
	for _, name := range notices {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(dir, path.Base(name)), b, 0o644); err != nil {
			return "", err
		}
	}
	var files []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			files = append(files, p)
		}
		return err
	})
	if err != nil {
		return "", err
	}
	slices.Sort(files)
	if _, err := os.Stat(filepath.Join(dir, "djinn.exe")); err == nil {
		return dir + ".zip", write(dir+".zip", func(w io.Writer) error { return zipFiles(w, dir, files) })
	}
	return dir + ".tar.gz", write(dir+".tar.gz", func(w io.Writer) error { return tarFiles(w, dir, files) })
}

// write creates path and fills it with fill; a failure leaves no half-written file.
func write(path string, fill func(io.Writer) error) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := errors.Join(fill(f), f.Close()); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

// entry is the name of p inside the archive: the folder's own name, then the path below it, with forward slashes.
func entry(dir, p string) (string, error) {
	rel, err := filepath.Rel(filepath.Dir(dir), p)
	return filepath.ToSlash(rel), err
}

// mode keeps the executable bit of the binaries, whatever the file system that built them (Windows has none).
func mode(p string) int64 {
	if base := filepath.Base(p); base == "djinn" || strings.HasSuffix(base, ".exe") {
		return 0o755
	}
	return 0o644
}

func tarFiles(w io.Writer, dir string, files []string) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	for _, p := range files {
		name, err := entry(dir, p)
		if err != nil {
			return err
		}
		info, err := os.Stat(p)
		if err != nil {
			return err
		}
		hdr := &tar.Header{Name: name, Mode: mode(p), Size: info.Size(), ModTime: info.ModTime(), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if err := copyInto(tw, p); err != nil {
			return err
		}
	}
	return errors.Join(tw.Close(), gz.Close())
}

func zipFiles(w io.Writer, dir string, files []string) error {
	zw := zip.NewWriter(w)
	for _, p := range files {
		name, err := entry(dir, p)
		if err != nil {
			return err
		}
		info, err := os.Stat(p)
		if err != nil {
			return err
		}
		hdr, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		hdr.Name, hdr.Method = name, zip.Deflate
		hdr.SetMode(fs.FileMode(mode(p)))
		fw, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		if err := copyInto(fw, p); err != nil {
			return err
		}
	}
	return zw.Close()
}

func copyInto(w io.Writer, p string) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}

// sums writes dir/SHA256SUMS: one line per file of dir (not its sub-folders), sorted by name, as sha256sum prints
// them, so that `sha256sum -c` and `shasum -a 256 -c` check them.
func sums(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var b strings.Builder
	for _, e := range entries { // ReadDir sorts by name.
		if !e.Type().IsRegular() || e.Name() == SumsFile {
			continue
		}
		sum, err := sha256File(filepath.Join(dir, e.Name()))
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "%s  %s\n", sum, e.Name())
	}
	if b.Len() == 0 {
		return fmt.Errorf("%s holds no file to sum", dir)
	}
	return os.WriteFile(filepath.Join(dir, SumsFile), []byte(b.String()), 0o644)
}

func sha256File(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

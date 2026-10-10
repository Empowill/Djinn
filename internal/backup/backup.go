// Package backup writes Djinn's data folder into one archive, and puts it back. The archive holds a consistent copy
// of the database and the files beside it: the pages, the window's state, the folders of the tasks. It leaves out
// what is rebuilt or held elsewhere: the worktrees (Git holds them), the socket, the server's address, the logs,
// and any environment file. Djinn stores no secret, so the archive holds none.
package backup

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	backupv1 "github.com/empowill/djinn/gen/go/backup/v1"
	"github.com/empowill/djinn/internal/fsx"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/server"
	"github.com/empowill/djinn/internal/store"
)

const (
	// ManifestFile opens every archive: a backupv1.Manifest in JSON.
	ManifestFile = "djinn-backup.json"
	// format is the layout of the archives this Djinn writes, and the only one it restores.
	format = 1
)

// Snapshot copies the database, consistently, to dst, a file that does not exist yet.
type Snapshot func(ctx context.Context, dst string) error

// DefaultFile is a new archive in the Downloads folder, named after now: .zip on Windows, .tar.gz elsewhere.
func DefaultFile(now time.Time) (string, error) {
	dir, err := plan.Downloads()
	if err != nil {
		return "", fmt.Errorf("find the Downloads folder: %w", err)
	}
	ext := ".tar.gz"
	if runtime.GOOS == "windows" {
		ext = ".zip"
	}
	name := "djinn-backup-" + now.Format("2006-01-02-150405")
	p := filepath.Join(dir, name+ext)
	for i := 2; exists(p); i++ {
		p = filepath.Join(dir, fmt.Sprintf("%s-%d%s", name, i, ext))
	}
	return p, nil
}

// Create writes the data folder home into the archive file, replacing it, and returns what it wrote. snapshot
// copies the database; the other files are read as they are. The archive appears whole or not at all.
func Create(ctx context.Context, home, file, version string, snapshot Snapshot) (*backupv1.BackupServiceCreateResponse, error) {
	return create(ctx, home, file, version, snapshot, fsx.OS())
}

func create(
	ctx context.Context, home, file, version string, snapshot Snapshot, r fsx.Renamer,
) (*backupv1.BackupServiceCreateResponse, error) {
	zipped, err := zipFile(file)
	if err != nil {
		return nil, err
	}
	if home, err = filepath.Abs(home); err != nil {
		return nil, err
	}
	if file, err = filepath.Abs(file); err != nil {
		return nil, err
	}
	if info, err := os.Stat(home); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("no data folder at %s", home)
	}
	dir := filepath.Dir(file)
	work, err := os.MkdirTemp(dir, ".djinn-backup-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)
	db := filepath.Join(work, store.File)
	if err := snapshot(ctx, db); err != nil {
		return nil, err
	}
	partial, err := os.CreateTemp(dir, ".djinn-backup-*.partial")
	if err != nil {
		return nil, err
	}
	defer os.Remove(partial.Name()) // Gone once renamed.
	w := newWriter(partial, zipped)
	files, err := write(ctx, w, home, db, version, []string{file, work, partial.Name()})
	if err = errors.Join(err, w.Close()); err == nil {
		err = partial.Sync()
	}
	if err = errors.Join(err, partial.Close()); err != nil {
		return nil, fmt.Errorf("write %s: %w", file, err)
	}
	if err := r.Rename(partial.Name(), file); err != nil {
		return nil, err
	}
	info, err := os.Stat(file)
	if err != nil {
		return nil, err
	}
	return &backupv1.BackupServiceCreateResponse{File: file, Size: info.Size(), Files: int32(files)}, nil
}

// write adds the manifest, the database copy db, then every file of home but the excluded ones and those in
// skip, and returns how many files it added.
func write(ctx context.Context, w writer, home, db, version string, skip []string) (int, error) {
	manifest, err := protojson.MarshalOptions{Multiline: true, UseProtoNames: true}.Marshal(&backupv1.Manifest{
		Format: format, Version: version, CreateTime: timestamppb.Now(),
	})
	if err != nil {
		return 0, err
	}
	if err := w.file(ManifestFile, int64(len(manifest)), time.Now(), strings.NewReader(string(manifest))); err != nil {
		return 0, err
	}
	if err := addFile(w, store.File, db); err != nil {
		return 0, err
	}
	files := 2
	err = filepath.WalkDir(home, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil // Removed while we walk: a page rewritten, a folder cleaned.
			}
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(home, p)
		if err != nil || rel == "." {
			return err
		}
		rel = filepath.ToSlash(rel)
		for _, s := range skip {
			if p == s {
				return skipped(d)
			}
		}
		if rel == store.File || rel == ManifestFile || excluded(rel, d.IsDir()) {
			return skipped(d)
		}
		if d.IsDir() {
			return w.dir(rel)
		}
		if !d.Type().IsRegular() {
			return nil // A link, a socket: nothing Djinn writes.
		}
		if err := addFile(w, rel, p); errors.Is(err, fs.ErrNotExist) {
			return nil
		} else if err != nil {
			return err
		}
		files++
		return nil
	})
	return files, err
}

func skipped(d fs.DirEntry) error {
	if d.IsDir() {
		return filepath.SkipDir
	}
	return nil
}

// excluded tells whether a path of the data folder, relative and with slashes, stays out of a backup and out of a
// restore: the database's own side files, the socket and the address of a running djinn, the logs of its
// processes, the worktrees, and any environment file.
func excluded(rel string, dir bool) bool {
	parts := strings.Split(rel, "/")
	// A task's worktree: projects/<project>/worktrees/<task> (harness.worktreeDir). Git holds it.
	if len(parts) >= 3 && parts[0] == "projects" && parts[2] == "worktrees" {
		return true
	}
	if dir {
		return false
	}
	name := strings.ToLower(parts[len(parts)-1])
	if strings.HasPrefix(name, ".env") || strings.HasSuffix(name, ".env") {
		return true
	}
	if len(parts) > 1 {
		return false
	}
	return strings.HasPrefix(name, store.File+"-") || name == server.SocketFile || name == server.AddrFile ||
		strings.HasSuffix(name, ".log")
}

func addFile(w writer, name, p string) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	return w.file(name, info.Size(), info.ModTime(), f)
}

// zipFile tells the format of an archive by its name: zip, or tar compressed with gzip.
func zipFile(file string) (bool, error) {
	lower := strings.ToLower(file)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return true, nil
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return false, nil
	}
	return false, fmt.Errorf("%s: expected a name ending in .tar.gz, .tgz or .zip", file)
}

// writer writes the entries of an archive.
type writer interface {
	dir(name string) error
	file(name string, size int64, mod time.Time, r io.Reader) error
	Close() error
}

func newWriter(out io.Writer, zipped bool) writer {
	if zipped {
		return zipWriter{zip.NewWriter(out)}
	}
	gz := gzip.NewWriter(out)
	return tarWriter{tar.NewWriter(gz), gz}
}

type tarWriter struct {
	tw *tar.Writer
	gz *gzip.Writer
}

func (t tarWriter) dir(name string) error {
	return t.tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: name + "/", Mode: 0o700, ModTime: time.Now()})
}

func (t tarWriter) file(name string, size int64, mod time.Time, r io.Reader) error {
	if err := t.tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: 0o600, Size: size, ModTime: mod}); err != nil {
		return err
	}
	// The size read first is the size written: a file that grows meanwhile is cut, one that shrinks fails.
	if _, err := io.CopyN(t.tw, r, size); err != nil {
		return fmt.Errorf("%s changed while it was read: %w", name, err)
	}
	return nil
}

func (t tarWriter) Close() error { return errors.Join(t.tw.Close(), t.gz.Close()) }

type zipWriter struct{ zw *zip.Writer }

func (z zipWriter) dir(name string) error {
	h := &zip.FileHeader{Name: name + "/", Modified: time.Now()}
	h.SetMode(fs.ModeDir | 0o700)
	_, err := z.zw.CreateHeader(h)
	return err
}

func (z zipWriter) file(name string, _ int64, mod time.Time, r io.Reader) error {
	h := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: mod}
	h.SetMode(0o600)
	w, err := z.zw.CreateHeader(h)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, r)
	return err
}

func (z zipWriter) Close() error { return z.zw.Close() }

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

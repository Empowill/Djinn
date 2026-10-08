package backup

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	backupv1 "github.com/empowill/djinn/gen/go/backup/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/cli"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/server"
	"github.com/empowill/djinn/internal/store"
)

// ErrRunning is returned by Restore when a djinn runs on the data folder.
var ErrRunning = errors.New("djinn is running on this data folder")

const (
	// actor and methodRestore journal what a restore changed in the database it puts back.
	actor         = "backup"
	methodRestore = "backup/restore" // a project's folder is missing on this machine; the request is the project
	// maxManifest bounds the manifest a restore reads.
	maxManifest = 1 << 20
)

// Restored is what Restore did.
type Restored struct {
	// Home is the data folder restored.
	Home string
	// Aside is where the previous data folder was moved; empty when there was none.
	Aside string
	// Files restored, the database included.
	Files int
	// Detached are the projects whose folder is missing on this machine: djinn project add <folder> attaches each.
	Detached []string
}

// Restore puts the archive back as the data folder home, while no djinn runs on it. The previous folder is kept
// aside, next to it; its worktrees move into the restored folder, at the same paths, so Git still finds them. A
// project whose folder this machine lacks loses it, so that djinn project add attaches it, as after an import.
func Restore(ctx context.Context, home, archive string, now time.Time) (*Restored, error) {
	home, err := filepath.Abs(home)
	if err != nil {
		return nil, err
	}
	if addr, err := server.ReadAddr(home); err == nil && cli.Alive(addr) {
		return nil, fmt.Errorf("%w (%s): close its window, or stop djinn up (Ctrl+C where it runs, or end its "+
			"process), then restore again", ErrRunning, home)
	}
	parent := filepath.Dir(home)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return nil, err
	}
	// Extracted next to home, so that it moves into place in one rename.
	tmp, err := os.MkdirTemp(parent, "."+filepath.Base(home)+".restore-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp) // Gone once renamed.
	out := &Restored{Home: home}
	if out.Files, err = extract(archive, tmp); err != nil {
		return nil, fmt.Errorf("read %s: %w", archive, err)
	}
	db := filepath.Join(tmp, store.File)
	if err := store.Check(ctx, db); err != nil {
		return nil, fmt.Errorf("read %s: the database: %w", archive, err)
	}
	if out.Detached, err = detach(ctx, db); err != nil {
		return nil, err
	}
	if !exists(home) {
		if err := os.Rename(tmp, home); err != nil {
			return nil, err
		}
		return out, nil
	}
	out.Aside = home + ".before-restore-" + now.Format("2006-01-02-150405")
	for i := 2; exists(out.Aside); i++ {
		out.Aside = fmt.Sprintf("%s.before-restore-%s-%d", home, now.Format("2006-01-02-150405"), i)
	}
	if err := os.Rename(home, out.Aside); err != nil {
		return nil, fmt.Errorf("move %s aside (close what uses it, then restore again): %w", home, err)
	}
	if err := os.Rename(tmp, home); err != nil {
		return nil, errors.Join(fmt.Errorf("put the restored folder in place: %w", err), os.Rename(out.Aside, home))
	}
	return out, moveWorktrees(out.Aside, home)
}

// extract writes the entries of the archive into dir, which is empty, and returns how many files it wrote. It
// refuses an archive without a manifest of a known format or without a database, and an entry outside dir; it
// skips what a backup leaves out.
func extract(archive, dir string) (int, error) {
	f, err := os.Open(archive)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	head, _ := bufio.NewReader(f).Peek(4)
	var files int
	var manifest []byte
	put := func(name string, isDir bool, r io.Reader) error {
		clean := path.Clean(strings.TrimSuffix(name, "/"))
		if strings.Contains(name, `\`) || !filepath.IsLocal(filepath.FromSlash(clean)) {
			return fmt.Errorf("unsafe entry %q", name)
		}
		if clean == ManifestFile {
			b, err := io.ReadAll(io.LimitReader(r, maxManifest))
			manifest = b
			return err
		}
		if excluded(clean, isDir) {
			return nil
		}
		dst := filepath.Join(dir, filepath.FromSlash(clean))
		if isDir {
			return os.MkdirAll(dst, 0o700)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		w, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		_, err = io.Copy(w, r)
		if err = errors.Join(err, w.Close()); err != nil {
			return err
		}
		files++
		return nil
	}
	switch {
	case bytes.HasPrefix(head, []byte("PK\x03\x04")):
		err = readZip(archive, put)
	case bytes.HasPrefix(head, []byte{0x1f, 0x8b}):
		if _, err = f.Seek(0, io.SeekStart); err == nil {
			err = readTar(f, put)
		}
	default:
		err = errors.New("not a backup archive: expected .tar.gz or .zip")
	}
	if err != nil {
		return 0, err
	}
	if manifest == nil {
		return 0, fmt.Errorf("not a backup of Djinn: no %s", ManifestFile)
	}
	m := &backupv1.Manifest{}
	if err := protojson.Unmarshal(manifest, m); err != nil {
		return 0, fmt.Errorf("%s: %w", ManifestFile, err)
	}
	if m.GetFormat() != format {
		return 0, fmt.Errorf("a backup of format %d, this djinn reads format %d: restore it with djinn %s",
			m.GetFormat(), format, m.GetVersion())
	}
	if !exists(filepath.Join(dir, store.File)) {
		return 0, fmt.Errorf("no %s in the archive", store.File)
	}
	return files, nil
}

func readTar(r io.Reader, put func(string, bool, io.Reader) error) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			err = put(h.Name, true, nil)
		case tar.TypeReg:
			err = put(h.Name, false, tr)
		default:
			continue // A link or a device: never written by a backup.
		}
		if err != nil {
			return err
		}
	}
}

func readZip(archive string, put func(string, bool, io.Reader) error) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		mode := f.Mode()
		if mode.IsDir() {
			if err := put(f.Name, true, nil); err != nil {
				return err
			}
			continue
		}
		if !mode.IsRegular() {
			continue
		}
		r, err := f.Open()
		if err != nil {
			return err
		}
		err = put(f.Name, false, r)
		if err = errors.Join(err, r.Close()); err != nil {
			return err
		}
	}
	return nil
}

// detach removes from the database at db the folders of the projects this machine lacks, and returns their
// names.
func detach(ctx context.Context, db string) ([]string, error) {
	s, err := store.Open(ctx, db, plan.Entities()...)
	if err != nil {
		return nil, fmt.Errorf("open the restored database: %w", err)
	}
	defer s.Close()
	projects, err := store.List[*planv1.Project](ctx, s, nil)
	if err != nil {
		return nil, err
	}
	var names []string
	err = s.Tx(ctx, func(tx *store.Tx) error {
		for _, p := range projects {
			if p.GetDirectory() == "" {
				continue
			}
			if info, err := os.Stat(p.GetDirectory()); err == nil && info.IsDir() {
				continue
			}
			if err := tx.Journal(actor, methodRestore, p); err != nil {
				return err
			}
			p.Directory = ""
			if err := tx.Put(p); err != nil {
				return err
			}
			names = append(names, p.GetName())
		}
		return nil
	})
	return names, err
}

// moveWorktrees moves the worktrees of the folder put aside to the same paths in home: Git records them there.
func moveWorktrees(aside, home string) error {
	entries, err := os.ReadDir(filepath.Join(aside, "projects"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range entries {
		src := filepath.Join(aside, "projects", e.Name(), "worktrees")
		if !e.IsDir() || !exists(src) {
			continue
		}
		dst := filepath.Join(home, "projects", e.Name(), "worktrees")
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := os.Rename(src, dst); err != nil {
			errs = append(errs, fmt.Errorf("the worktrees stay in %s: %w", src, err))
		}
	}
	return errors.Join(errs...)
}

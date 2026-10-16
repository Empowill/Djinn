// Package fsx holds the file system helpers several packages share: a rename that Windows does not refuse for a
// moment, a write that a reader never sees half done, and the sweep of what such a write left when a kill cut it.
package fsx

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"time"
)

// retryEvery is the pause between two tries of a refused rename.
const retryEvery = 20 * time.Millisecond

// Renamer moves a path over another with Func, trying again every 20 ms while it fails, Wait in all. Windows names the
// cause of a held file loosely (access denied, sharing violation): any error is tried again.
type Renamer struct {
	Func func(oldpath, newpath string) error
	Wait time.Duration
	// Sleep pauses between two tries; nil: time.Sleep. A test passes its own, and no time goes by.
	Sleep func(time.Duration)
}

// OS renames with os.Rename. Windows refuses to move a file another process holds open ("Access is denied"), or a
// folder with such a file: a browser reading a page, an antivirus scanning settings.json or a tilasm just written,
// each for a moment; there it tries again for 500 ms. Elsewhere a rename replaces an open file, and fails for good when
// it fails.
func OS() Renamer {
	r := Renamer{Func: os.Rename}
	if runtime.GOOS == "windows" {
		r.Wait = 500 * time.Millisecond
	}
	return r
}

// Rename moves oldpath over newpath.
func (r Renamer) Rename(oldpath, newpath string) error {
	sleep := r.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	for waited := time.Duration(0); ; waited += retryEvery {
		err := r.Func(oldpath, newpath)
		if err == nil || waited >= r.Wait {
			return err
		}
		sleep(retryEvery)
	}
}

// tempMark names, between the file's name and the random part, a temporary file WriteAtomic leaves: Sweep tells it
// from any other file by it.
const tempMark = ".djinn-"

// tempName matches the temporary files of WriteAtomic: "." + the file's name + tempMark + digits + ".tmp".
var tempName = regexp.MustCompile(`^\..+` + regexp.QuoteMeta(tempMark) + `[0-9]+\.tmp$`)

// WriteAtomic writes data at path with the permissions perm, as os.WriteFile does, but through a temporary file
// next to it, synced, then renamed over path, the folder synced last: a reader sees the old content or the new one,
// never a part of it, and neither a kill nor a power cut leaves the file empty or cut. A kill before the rename
// leaves the temporary file, which Sweep removes. The rename tries again a moment on Windows, where an antivirus
// scanning the file refuses it.
func WriteAtomic(path string, data []byte, perm fs.FileMode) error {
	return OS().WriteAtomic(path, data, perm)
}

// WriteAtomicNoSync writes data at path as WriteAtomic does, but syncs nothing: a kill never leaves the file cut, a
// power cut may. For a file that matters only while Djinn's processes run, which a power cut ends too.
func WriteAtomicNoSync(path string, data []byte, perm fs.FileMode) error {
	return OS().WriteAtomicNoSync(path, data, perm)
}

// WriteAtomic writes data at path as the package's WriteAtomic does, renaming with r.
func (r Renamer) WriteAtomic(path string, data []byte, perm fs.FileMode) error {
	return r.write(path, data, perm, true)
}

// WriteAtomicNoSync writes data at path as the package's WriteAtomicNoSync does, renaming with r.
func (r Renamer) WriteAtomicNoSync(path string, data []byte, perm fs.FileMode) error {
	return r.write(path, data, perm, false)
}

func (r Renamer) write(path string, data []byte, perm fs.FileMode, sync bool) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+tempMark+"*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // Fails harmlessly once renamed.
	if err := tmp.Chmod(perm); err != nil && runtime.GOOS != "windows" {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if sync {
		if err := tmp.Sync(); err != nil {
			tmp.Close()
			return err
		}
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := r.Rename(tmp.Name(), path); err != nil {
		return err
	}
	if sync {
		return SyncDir(dir)
	}
	return nil
}

// SyncDir syncs the folder dir, so that a rename or a removal in it outlives a power cut. Windows opens no folder
// to sync it, and its file system keeps a rename on its own: there it does nothing.
func SyncDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}

// Sweep removes from the folder dir the temporary files WriteAtomic left there, cut before their rename, and
// returns their paths. It looks at dir only, not into its folders, and leaves any other file alone. A folder that
// does not exist holds none. In a folder another process may write in at the same time, minAge spares the files of
// writes still on their way: those written less than minAge ago; 0 sweeps them all.
func Sweep(dir string, minAge time.Duration) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var removed []string
	var errs []error
	for _, e := range entries {
		if !e.Type().IsRegular() || !tempName.MatchString(e.Name()) {
			continue
		}
		if minAge > 0 {
			if info, err := e.Info(); err != nil || time.Since(info.ModTime()) < minAge {
				continue
			}
		}
		path := filepath.Join(dir, e.Name())
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
			continue
		}
		removed = append(removed, path)
	}
	return removed, errors.Join(errs...)
}

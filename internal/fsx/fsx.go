// Package fsx holds the file system helpers several packages share: a rename that Windows does not refuse for a
// moment, and a write that a reader never sees half done.
package fsx

import (
	"os"
	"path/filepath"
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

// WriteAtomic writes a temporary file next to path, readable by the owner only, then renames it over path: a
// reader sees the old content or the new one, never a part of it. The rename tries again a moment on Windows,
// where an antivirus scanning the file refuses it.
func WriteAtomic(path string, data []byte) error {
	return OS().WriteAtomic(path, data)
}

// WriteAtomic writes data at path as the package's WriteAtomic does, renaming with r.
func (r Renamer) WriteAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // Fails harmlessly once renamed.
	if err := tmp.Chmod(0o600); err != nil && runtime.GOOS != "windows" {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return r.Rename(tmp.Name(), path)
}

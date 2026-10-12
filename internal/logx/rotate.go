package logx

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"
)

// Rotating is a file that rolls over once it holds max bytes: path becomes path.1, path.1 becomes path.2, and so on;
// past keep older files, the oldest is removed. All of them hold at most (keep+1) × max bytes, a line longer than max
// aside. It closes the file before it renames it, as Windows requires, and needs no logrotate.
type Rotating struct {
	mu   sync.Mutex
	path string
	max  int64
	keep int
	f    *os.File
	size int64
}

// OpenRotating opens path to append to it, rolled over at size bytes with keep older files.
func OpenRotating(path string, size int64, keep int) (*Rotating, error) {
	r := &Rotating{path: path, max: size, keep: keep}
	if err := r.open(os.O_APPEND); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Rotating) open(mode int) error {
	f, err := os.OpenFile(r.path, os.O_WRONLY|os.O_CREATE|mode, 0o600)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	r.f, r.size = f, info.Size()
	return nil
}

// Write writes p whole to the current file, after rolling it over when p would take it past max.
func (r *Rotating) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return 0, os.ErrClosed
	}
	if r.size > 0 && r.size+int64(len(p)) > r.max {
		if err := r.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

// rotate shifts the older files by one and starts path empty. When a rename fails (on Windows, another process
// holding a file open), path starts empty all the same: the cap holds, at the cost of what it held.
func (r *Rotating) rotate() error {
	if err := r.f.Close(); err != nil {
		return err
	}
	r.f = nil
	failed := Shift(r.path, r.keep)
	if err := r.open(os.O_TRUNC); err != nil {
		return errors.Join(failed, err)
	}
	if failed != nil {
		fmt.Fprintf(r.f, "logx: %s could not roll over, its lines are lost: %v\n", r.path, failed)
	}
	return nil
}

// Close closes the current file.
func (r *Rotating) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	return err
}

// Shift moves path to path.1 after moving each path.i to path.i+1, removing path.keep first; with keep 0 it removes
// path. A file missing on the way is no error.
func Shift(path string, keep int) error {
	if keep <= 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.Remove(fmt.Sprintf("%s.%d", path, keep)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for i := keep - 1; i >= 0; i-- {
		from := path
		if i > 0 {
			from = fmt.Sprintf("%s.%d", path, i)
		}
		if err := os.Rename(from, fmt.Sprintf("%s.%d", path, i+1)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

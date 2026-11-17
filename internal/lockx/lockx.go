// Package lockx holds an exclusive lock on a file, the one a djinn up takes on its data folder: a second djinn up on
// the folder, whatever its version and even run under wine, sees it held before it writes anything there. The lock
// goes with the process: a crash, a kill, release it as a clean stop does.
//
// Linux takes an open file description lock (fcntl F_OFD_SETLK), macOS a POSIX record lock (fcntl F_SETLK), Windows
// LockFileEx. Never flock: wine maps LockFileEx onto fcntl locks, which a flock does not see (W83, tilasm L15).
package lockx

import (
	"errors"
	"os"
)

// ErrHeld is what Hold returns when another process, or another Hold, holds the lock.
var ErrHeld = errors.New("held by another process")

// Lock is a lock Hold took; Release lets it go.
type Lock struct {
	f *os.File
}

// Hold takes the exclusive lock of the file at path, creating it empty, without waiting: ErrHeld when it is held.
func Hold(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lock(f); err != nil {
		f.Close()
		return nil, err
	}
	return &Lock{f: f}, nil
}

// Release lets the lock go; the file stays, for the next Hold.
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	err := l.f.Close() // Closing the file releases its lock, on every system.
	l.f = nil
	return err
}

//go:build unix && !linux

package lockx

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// lock takes a POSIX record lock on the whole of f: macOS has no open file description lock. It belongs to the
// process, and any close of the file by the process lets it go: only Hold opens it.
func lock(f *os.File) error {
	lk := unix.Flock_t{Type: unix.F_WRLCK, Whence: 0, Start: 0, Len: 0}
	for {
		err := unix.FcntlFlock(f.Fd(), unix.F_SETLK, &lk)
		switch {
		case errors.Is(err, unix.EINTR):
			continue
		case errors.Is(err, unix.EAGAIN), errors.Is(err, unix.EACCES):
			return ErrHeld
		}
		return err
	}
}

package lockx

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// lock takes an open file description lock on the whole of f: it belongs to this open file, not to the process, so
// that another open of the same file, in this process or another, conflicts with it.
func lock(f *os.File) error {
	lk := unix.Flock_t{Type: unix.F_WRLCK, Whence: 0, Start: 0, Len: 0}
	for {
		err := unix.FcntlFlock(f.Fd(), unix.F_OFD_SETLK, &lk)
		switch {
		case errors.Is(err, unix.EINTR):
			continue
		case errors.Is(err, unix.EAGAIN), errors.Is(err, unix.EACCES):
			return ErrHeld
		}
		return err
	}
}

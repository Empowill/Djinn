package lockx

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// lock takes LockFileEx on the first byte of f, exclusive and without waiting. Under wine it is a fcntl lock on the
// same byte, which the lock of a Linux djinn up holds.
func lock(f *os.File) error {
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, &windows.Overlapped{})
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		return ErrHeld
	}
	return err
}

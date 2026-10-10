// Package fsx holds the file system helpers several packages share: for now, a rename that Windows does not refuse
// for a moment.
package fsx

import (
	"os"
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

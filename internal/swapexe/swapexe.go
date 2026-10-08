// Package swapexe puts a freshly built executable in place of an installed one, in one rename, without disturbing a
// copy that runs. `task install` uses it through tools/swapexe, and djinn itself when it installs a release.
//
// On macOS and Linux a rename replaces the path while the running process keeps the old file. Windows refuses to
// replace a running executable, but lets it be renamed: the old one moves aside first, and is removed at a later
// install once nothing runs it.
package swapexe

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// AsidePrefix starts the name of an old executable moved aside, in the folder of the installed one.
const AsidePrefix = ".swapexe-old-"

// Install renames src over dst, the way this system allows: moving a running dst aside first on Windows.
func Install(src, dst string) error {
	return Swap(src, dst, runtime.GOOS == "windows")
}

// Swap renames src over dst; both must be in the same folder. With aside, a dst that exists moves aside first.
func Swap(src, dst string, aside bool) error {
	if filepath.Clean(filepath.Dir(src)) != filepath.Clean(filepath.Dir(dst)) {
		return errors.New("the new executable must be in the folder of the installed one: a rename is atomic only there")
	}
	if _, err := os.Stat(src); err != nil {
		return err
	}
	if !aside {
		return os.Rename(src, dst)
	}
	removeAside(filepath.Dir(dst))
	if _, err := os.Stat(dst); err != nil {
		return os.Rename(src, dst) // Nothing installed yet.
	}
	old := filepath.Join(filepath.Dir(dst),
		AsidePrefix+strconv.FormatInt(time.Now().UnixNano(), 10)+"-"+filepath.Base(dst))
	if err := os.Rename(dst, old); err != nil {
		return fmt.Errorf("move the running executable aside: %w", err)
	}
	if err := os.Rename(src, dst); err != nil {
		return errors.Join(err, os.Rename(old, dst)) // Put the old one back: the path never stays empty.
	}
	return nil
}

// removeAside removes the old executables moved aside by earlier installs; one still running stays.
func removeAside(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), AsidePrefix) {
			_ = os.Remove(filepath.Join(dir, e.Name())) // Fails while it runs: the next install tries again.
		}
	}
}

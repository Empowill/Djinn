// Command swapexe puts a freshly built executable in place of an installed one, in one rename, without disturbing a
// copy that runs: it is how `task install` updates the Djinn in use. The running copy keeps its old file, and notices
// the new one at its path (see cmd/djinn/update.go).
//
// On macOS and Linux a rename replaces the path while the running process keeps the old file. Windows refuses to
// replace a running executable, but lets it be renamed: the old one moves aside first, and is removed at a later
// install once nothing runs it.
package main

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

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: swapexe <new executable> <installed executable>")
		os.Exit(2)
	}
	if err := swap(os.Args[1], os.Args[2], runtime.GOOS == "windows"); err != nil {
		fmt.Fprintln(os.Stderr, "swapexe:", err)
		os.Exit(1)
	}
}

// asidePrefix starts the name of an old executable moved aside, in the folder of the installed one.
const asidePrefix = ".swapexe-old-"

// swap renames src over dst; both must be in the same folder. With aside, a dst that exists moves aside first.
func swap(src, dst string, aside bool) error {
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
		asidePrefix+strconv.FormatInt(time.Now().UnixNano(), 10)+"-"+filepath.Base(dst))
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
		if strings.HasPrefix(e.Name(), asidePrefix) {
			_ = os.Remove(filepath.Join(dir, e.Name())) // Fails while it runs: the next install tries again.
		}
	}
}

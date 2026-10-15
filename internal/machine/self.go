package machine

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
)

// SelfDir is the folder, in Djinn's data folder home, that holds a djinn command running this very executable: the
// workers and the leads find it first on their PATH (PathFirst). A development build is bin/djinn-dev, not on any
// PATH, and a djinn found on the PATH may be another Djinn, the developer's own, with another data folder: a worker
// must never reach it ("Djinn builds Djinn").
func SelfDir(home string) string { return filepath.Join(home, "bin") }

// LinkSelf puts in SelfDir(home) a djinn command that runs this executable, its symbolic links resolved, and returns
// the folder. It writes the command beside its place and renames it there, so that a worker never finds half of one.
// On macOS and Linux the command is a symbolic link: a rebuilt executable is found through it. On Windows, where a
// symbolic link needs a privilege or the developer mode, it is a hard link, or a copy when the executable is on
// another volume; one already there that is the same file is kept, since Windows cannot replace a program that
// runs.
func LinkSelf(home string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("this djinn's executable: %w", err)
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return "", fmt.Errorf("this djinn's executable: %w", err)
	}
	return linkSelf(runtime.GOOS, SelfDir(home), exe)
}

func linkSelf(goos, dir, exe string) (string, error) {
	name := "djinn"
	if goos == "windows" {
		name += ".exe"
	}
	file := filepath.Join(dir, name)
	if same(goos, file, exe) {
		return dir, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp := filepath.Join(dir, "."+name+"-"+strconv.Itoa(os.Getpid()))
	_ = os.Remove(tmp)
	var err error
	if goos == "windows" {
		if err = os.Link(exe, tmp); err != nil {
			err = copyFile(exe, tmp)
		}
	} else {
		err = os.Symlink(exe, tmp)
	}
	if err == nil {
		err = os.Rename(tmp, file)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("the djinn command of the workers, %s: %w", file, err)
	}
	return dir, nil
}

// same tells that file already runs exe: a symbolic link to it, or on Windows the same file.
func same(goos, file, exe string) bool {
	if goos != "windows" {
		target, err := os.Readlink(file)
		return err == nil && target == exe
	}
	a, err := os.Stat(file)
	if err != nil {
		return false
	}
	b, err := os.Stat(exe)
	return err == nil && os.SameFile(a, b)
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// PathFirst is the PATH entry of a process Djinn starts with dir first, then Djinn's own PATH as it is now (after
// ExtendPath): appended to the process's environment, it replaces the PATH it inherits. Empty when dir is.
func PathFirst(dir string) string {
	if dir == "" {
		return ""
	}
	return "PATH=" + joinPaths(dir, os.Getenv("PATH"))
}

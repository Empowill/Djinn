package logx

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/pprof"
	"slices"
	"strings"
	"time"
)

const (
	// DumpPrefix starts the name of a goroutine dump in the log folder; its time follows, then ".txt".
	DumpPrefix = "goroutines-"
	// DumpsKept is how many goroutine dumps the log folder keeps: the oldest go first.
	DumpsKept = 5
	// DumpRequest is the file whose presence in the log folder asks a djinn up for a dump, on any system: it removes
	// it, then dumps (djinn debug dump, when the server does not answer).
	DumpRequest = "dump.request"
)

// Dump writes the stacks of every goroutine of the process, with how long each waits, to a new file of dir, logs it
// with why, and removes the oldest dumps past DumpsKept. It returns the file's path.
func Dump(dir, why string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, DumpPrefix+time.Now().UTC().Format("20060102T150405.000000000Z")+".txt")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(f, "djinn: goroutines of pid %d at %s, %s\n\n", os.Getpid(), time.Now().Format(time.RFC3339Nano), why)
	if err := pprof.Lookup("goroutine").WriteTo(f, 2); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	L().Warn("goroutines dumped", "why", why, "file", path)
	pruneDumps(dir)
	return path, nil
}

// Dumps lists the goroutine dumps of dir, oldest first.
func Dumps(dir string) []string {
	entries, _ := os.ReadDir(dir)
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), DumpPrefix) && strings.HasSuffix(e.Name(), ".txt") {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	slices.Sort(out) // Their names sort by time.
	return out
}

func pruneDumps(dir string) {
	dumps := Dumps(dir)
	for len(dumps) > DumpsKept {
		os.Remove(dumps[0])
		dumps = dumps[1:]
	}
}

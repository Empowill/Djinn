package fsx

import (
	"os"
	"path/filepath"
)

// NameMax is the longest name of a file or folder the file system that holds path takes, as it says (statfs on Linux,
// pathconf on macOS): 143 on eCryptfs, 255 on most others. When path does not exist yet, the closest folder above it
// that does answers: what is made under it lands on its file system, unless a mount point comes between. 0 when
// unknown: on Windows, or when nothing answers.
func NameMax(path string) int {
	if path == "" {
		return 0
	}
	for dir := filepath.Clean(path); ; {
		if _, err := os.Stat(dir); err == nil {
			return nameMax(dir)
		}
		up := filepath.Dir(dir)
		if up == dir {
			return 0
		}
		dir = up
	}
}

package fsx

import "golang.org/x/sys/unix"

// pcNameMax is _PC_NAME_MAX of macOS's <sys/unistd.h>: 4 in xnu's bsd/sys/unistd.h, and 0x4 in the constants
// tklauser/go-sysconf generates from the SDK by cgo (zsysconf_defs_darwin.go). golang.org/x/sys/unix names no _PC_
// constant for any system (v0.48.0), so it stays here (W38).
const pcNameMax = 4

// nameMax is the _PC_NAME_MAX of pathconf on the folder dir; 0 when it fails.
func nameMax(dir string) int {
	n, err := unix.Pathconf(dir, pcNameMax)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

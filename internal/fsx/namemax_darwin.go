package fsx

import "golang.org/x/sys/unix"

// pcNameMax is _PC_NAME_MAX of macOS's <sys/unistd.h>, which golang.org/x/sys/unix does not name.
const pcNameMax = 4

// nameMax is the _PC_NAME_MAX of pathconf on the folder dir; 0 when it fails.
func nameMax(dir string) int {
	n, err := unix.Pathconf(dir, pcNameMax)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

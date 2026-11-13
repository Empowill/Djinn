package fsx

import "golang.org/x/sys/unix"

// nameMax is the f_namelen of statfs on the folder dir; 0 when it fails.
func nameMax(dir string) int {
	var s unix.Statfs_t
	if err := unix.Statfs(dir, &s); err != nil || s.Namelen <= 0 {
		return 0
	}
	return int(s.Namelen)
}

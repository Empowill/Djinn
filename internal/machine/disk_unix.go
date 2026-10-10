//go:build linux || darwin

package machine

import "golang.org/x/sys/unix"

// ReadDisk reads the space of the file system that holds dir, through statfs.
func ReadDisk(dir string) (*Disk, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return nil, err
	}
	block := uint64(st.Bsize)
	return &Disk{Path: dir, Total: st.Blocks * block, Available: st.Bavail * block}, nil
}

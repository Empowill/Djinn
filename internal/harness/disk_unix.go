//go:build unix

package harness

import (
	"io/fs"
	"syscall"
)

// onDisk is what a file takes on disk: its blocks, which count what the file system adds to it (eCryptfs: a header
// and the rounding up to a block, which doubles a small file) and leave out the holes of a sparse file, as du counts.
func onDisk(info fs.FileInfo) uint64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Blocks) * 512
	}
	return uint64(info.Size())
}

// sameDisk says whether a and b are on file systems of the same size, as statfs reads them: eCryptfs reports the disk
// below it, so the home and /var of one disk are the same, and a second disk is not. Unread: false.
func sameDisk(a, b string) bool {
	var sa, sb syscall.Statfs_t
	if syscall.Statfs(a, &sa) != nil || syscall.Statfs(b, &sb) != nil {
		return false
	}
	return uint64(sa.Blocks)*uint64(sa.Bsize) == uint64(sb.Blocks)*uint64(sb.Bsize) //nolint:unconvert // Their types differ per OS.
}

// device is the file system a file is on, to keep a walk on one.
func device(info fs.FileInfo) (uint64, bool) {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Dev), true //nolint:unconvert // int32 on macOS.
	}
	return 0, false
}

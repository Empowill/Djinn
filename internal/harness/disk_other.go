//go:build !unix

package harness

import "io/fs"

// onDisk is what a file takes on disk: its length, where the file system's blocks are not read.
func onDisk(info fs.FileInfo) uint64 {
	return uint64(info.Size())
}

// sameDisk is false where statfs is not read: the slow pass measures no system folder there.
func sameDisk(string, string) bool { return false }

// device is unknown where the file system's id is not read: a walk may cross into another one.
func device(fs.FileInfo) (uint64, bool) { return 0, false }

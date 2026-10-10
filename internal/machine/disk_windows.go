package machine

import "golang.org/x/sys/windows"

// ReadDisk reads the space of the volume that holds dir, through GetDiskFreeSpaceEx.
func ReadDisk(dir string) (*Disk, error) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return nil, err
	}
	var available, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(p, &available, &total, &free); err != nil {
		return nil, err
	}
	return &Disk{Path: dir, Total: total, Available: available}, nil
}

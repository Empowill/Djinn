//go:build !linux && !darwin && !windows

package machine

import "errors"

// ReadDisk does not read the disk on a system Djinn does not read further.
func ReadDisk(string) (*Disk, error) { return nil, errors.New("the disk is not read on this system") }

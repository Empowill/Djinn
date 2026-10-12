//go:build !linux && !darwin && !windows

package harness

import (
	"errors"
	"syscall"
)

// dieWithParent does nothing here: the sentinel stops the children once Djinn ends.
func dieWithParent(*syscall.SysProcAttr) {}

// procStart is unknown here: no child of a previous djinn up is ever taken for a leftover, so none is touched.
func procStart(int) (string, error) {
	return "", errors.New("process start times are not read on this system")
}

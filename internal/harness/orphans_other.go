//go:build !linux && !darwin && !windows

package harness

import (
	"errors"
	"os/exec"
	"syscall"
)

// dieWithParent does nothing here: the sentinel stops the children once Djinn ends.
func dieWithParent(*syscall.SysProcAttr) {}

// procStart is unknown here: no child of a previous djinn up is ever taken for a leftover, so none is touched.
func procStart(int) (string, error) {
	return "", errors.New("process start times are not read on this system")
}

// procsOf finds no process here: what a worker left outside its group is not found.
func procsOf(string, int, string) []identity { return nil }

// holdExit is false here.
func holdExit(*exec.Cmd) bool { return false }

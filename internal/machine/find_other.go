//go:build !linux && !darwin

package machine

import "errors"

// listCommands lists no process: the command lines are not read here.
func listCommands() ([]proc, map[int]string, error) {
	return nil, nil, errors.New("command lines not read here")
}

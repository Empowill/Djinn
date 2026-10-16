//go:build !linux && !darwin && !windows

package harness

// listeners finds no port here: only the ports a serve declares are checked.
func listeners([]int, []int) []listener { return nil }

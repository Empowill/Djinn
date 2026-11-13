//go:build !linux && !darwin

package fsx

// nameMax is unknown here: 0.
func nameMax(string) int { return 0 }

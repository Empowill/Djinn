//go:build !linux && !darwin

package machine

import (
	"errors"
	"runtime"
)

// NotMeasured is why the workers' CPU and memory are not measured here; empty: they are.
const NotMeasured = "the workers' CPU and memory are not measured on " + runtime.GOOS + " yet"

func listProcs() ([]proc, error) { return nil, errors.New(NotMeasured) }

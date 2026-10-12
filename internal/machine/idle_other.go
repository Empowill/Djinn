//go:build !linux && !darwin && !windows

package machine

import "time"

// DefaultIdleSource returns an IdleSource returning (0, false) on platforms without a desktop idle source.
func DefaultIdleSource() IdleSource {
	return func() (time.Duration, bool) {
		return 0, false
	}
}

package plan

import "time"

// SetWatchInterval sets the least time between two messages of a Watch stream, for a test of another package, and
// returns what puts it back.
func SetWatchInterval(d time.Duration) (restore func()) {
	old := watchInterval
	watchInterval = d
	return func() { watchInterval = old }
}

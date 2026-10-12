package machine

import "time"

// Operating load Auto mode thresholds.
const (
	// DefaultAutoHysteresis is the minimum time that must elapse between notch changes in auto mode.
	DefaultAutoHysteresis = 1 * time.Minute

	// DefaultActiveThreshold is the maximum idle time before the user is considered actively working.
	// Recent keyboard or mouse activity (< 30s) drops the operating load notch to Minimal.
	DefaultActiveThreshold = 30 * time.Second

	// DefaultIdleThreshold is the minimum idle time before the user is considered at rest.
	// Inactivity for 3 minutes or more with little host load allows the operating load to climb.
	DefaultIdleThreshold = 3 * time.Minute
)

// IdleSource returns the duration of user inactivity (time since last keyboard or mouse input)
// and whether idle time could be determined on the current platform/session.
// When ok is false, idle time is unavailable (headless, SSH, or unsupported desktop),
// and auto mode falls back to host CPU and memory load alone.
type IdleSource func() (idle time.Duration, ok bool)

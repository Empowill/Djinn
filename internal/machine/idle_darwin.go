//go:build darwin

package machine

import (
	"sync"
	"time"

	"github.com/ebitengine/purego"
)

var (
	darwinIdleOnce sync.Once
	darwinIdleFn   func(sourceState uint32, eventType uint32) float64
	darwinIdleErr  error
)

// DarwinSessionIdleSource returns an IdleSource for macOS using CoreGraphics.
// It queries CGEventSourceSecondsSinceLastEventType dynamically via purego without requiring CGO.
func DarwinSessionIdleSource() IdleSource {
	darwinIdleOnce.Do(func() {
		lib, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			lib, err = purego.Dlopen("CoreGraphics.framework/CoreGraphics", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
			if err != nil {
				darwinIdleErr = err
				return
			}
		}
		purego.RegisterLibFunc(&darwinIdleFn, lib, "CGEventSourceSecondsSinceLastEventType")
	})

	return func() (time.Duration, bool) {
		if darwinIdleErr != nil || darwinIdleFn == nil {
			return 0, false
		}
		// kCGEventSourceStateHIDSystemState = 1
		// kCGAnyInputEventType = ~uint32(0) (0xffffffff)
		sec := darwinIdleFn(1, 0xffffffff)
		if sec < 0 {
			return 0, false
		}
		return time.Duration(sec * float64(time.Second)), true
	}
}

// DefaultIdleSource returns the default macOS idle source.
func DefaultIdleSource() IdleSource {
	return DarwinSessionIdleSource()
}

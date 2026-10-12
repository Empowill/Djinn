//go:build windows

package machine

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modUser32            = windows.NewLazySystemDLL("user32.dll")
	procGetLastInputInfo = modUser32.NewProc("GetLastInputInfo")

	modKernel32      = windows.NewLazySystemDLL("kernel32.dll")
	procGetTickCount = modKernel32.NewProc("GetTickCount")
)

type lastInputInfo struct {
	cbSize uint32
	dwTime uint32
}

// WindowsSessionIdleSource returns an IdleSource for Windows using GetLastInputInfo.
func WindowsSessionIdleSource() IdleSource {
	return func() (time.Duration, bool) {
		if err := modUser32.Load(); err != nil {
			return 0, false
		}
		if err := modKernel32.Load(); err != nil {
			return 0, false
		}

		var lii lastInputInfo
		lii.cbSize = uint32(unsafe.Sizeof(lii))

		r1, _, _ := procGetLastInputInfo.Call(uintptr(unsafe.Pointer(&lii)))
		if r1 == 0 {
			return 0, false
		}

		now, _, _ := procGetTickCount.Call()
		// Unsigned 32-bit subtraction properly handles 49.7 day wrap-around
		elapsedMs := uint32(now) - lii.dwTime
		return time.Duration(elapsedMs) * time.Millisecond, true
	}
}

// DefaultIdleSource returns the default Windows idle source.
func DefaultIdleSource() IdleSource {
	return WindowsSessionIdleSource()
}

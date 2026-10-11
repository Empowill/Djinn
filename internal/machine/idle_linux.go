//go:build linux

package machine

import (
	"errors"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

// LinuxSessionIdleSource returns an IdleSource for Linux desktop sessions over D-Bus.
// It queries GNOME Mutter IdleMonitor on the session bus, falling back to
// org.freedesktop.ScreenSaver.GetSessionIdleTime if available.
//
// Note on KDE Plasma:
// On KDE Plasma (Wayland), KWin does not expose an unprivileged synchronous D-Bus polling
// method for session idle time (org.freedesktop.ScreenSaver only supports inhibition, and
// KIdleTime uses the Wayland client protocol ext-idle-notify-v1 / org_kde_kwin_idle).
// When running under KDE, GetIdletime is not found, and LinuxSessionIdleSource returns (0, false),
// causing Auto mode to gracefully fall back to host CPU and memory load alone.
func LinuxSessionIdleSource() IdleSource {
	var (
		mu   sync.Mutex
		conn *dbus.Conn
	)

	return func() (time.Duration, bool) {
		mu.Lock()
		if conn == nil {
			c, err := dbus.ConnectSessionBus()
			if err != nil {
				mu.Unlock()
				return 0, false
			}
			conn = c
		}
		c := conn
		mu.Unlock()

		// 1. GNOME Mutter IdleMonitor: org.gnome.Mutter.IdleMonitor.GetIdletime returns uint64 milliseconds.
		obj := c.Object("org.gnome.Mutter.IdleMonitor", "/org/gnome/Mutter/IdleMonitor/Core")
		var ms uint64
		call := obj.Call("org.gnome.Mutter.IdleMonitor.GetIdletime", 0)
		if call.Err == nil && call.Store(&ms) == nil {
			return time.Duration(ms) * time.Millisecond, true
		}

		// 2. Fallback: org.freedesktop.ScreenSaver.GetSessionIdleTime (supported by Cinnamon, MATE, XFCE).
		ssObj := c.Object("org.freedesktop.ScreenSaver", "/org/freedesktop/ScreenSaver")
		var ssMs uint32
		ssCall := ssObj.Call("org.freedesktop.ScreenSaver.GetSessionIdleTime", 0)
		if ssCall.Err == nil && ssCall.Store(&ssMs) == nil {
			return time.Duration(ssMs) * time.Millisecond, true
		}

		// If D-Bus connection disconnected, reset so future calls can reconnect.
		if call.Err != nil && (errors.Is(call.Err, dbus.ErrClosed) || !c.Connected()) {
			mu.Lock()
			if conn == c {
				_ = conn.Close()
				conn = nil
			}
			mu.Unlock()
		}

		return 0, false
	}
}

// DefaultIdleSource returns the default Linux idle source.
func DefaultIdleSource() IdleSource {
	return LinuxSessionIdleSource()
}

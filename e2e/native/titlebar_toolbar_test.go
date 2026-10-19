//go:build !headless && (cgo || windows)

package native

import (
	"fmt"
	"os"
	"runtime"
	"testing"
	"time"
)

// TestNativeToolbarControls exercises the persistent toolbar through Wails' animated MCP pointer/mouse sequence.
// The macOS geometry check keeps the sidebar's toggle below the native drag strip; this test does not claim to
// synthesize an AppKit NSEvent. The toolbar holds no Back until a page needs one.
func TestNativeToolbarControls(t *testing.T) {
	binary := os.Getenv("DJINN_E2E_NATIVE")
	if binary == "" {
		t.Skip("opens a native window: run it with go tool task e2e-native")
	}
	if runtime.GOOS != "darwin" {
		t.Skip("macOS titlebar geometry applies only on Darwin")
	}
	d := start(t, binary)

	eventually(t, 30*time.Second, func() error {
		var ready struct {
			App    bool `json:"app"`
			Toggle bool `json:"toggle"`
			New    bool `json:"newMission"`
		}
		if err := d.eval(`return {
			app: !!document.querySelector(".wish-app"),
			toggle: !!document.querySelector(".sidebar-toggle"),
			newMission: !!document.querySelector(".sidebar .new-mission"),
		};`, &ready); err != nil {
			return err
		}
		if !ready.App || !ready.Toggle || !ready.New {
			return fmt.Errorf("native toolbar not ready: %+v", ready)
		}
		return nil
	})

	var geometry struct {
		Toggle map[string]float64 `json:"toggle"`
	}
	if err := d.eval(`
		const box = (selector) => { const r = document.querySelector(selector)?.getBoundingClientRect(); return r && {
			x: r.x, y: r.y, width: r.width, height: r.height,
		}; };
		return { toggle: box(".sidebar-toggle") };
	`, &geometry); err != nil {
		t.Fatal(err)
	}
	if geometry.Toggle["y"] < 28 || geometry.Toggle["width"] != 30 || geometry.Toggle["height"] != 30 {
		t.Fatalf("unexpected native toggle geometry: %+v", geometry.Toggle)
	}

	click := func(selector string) {
		t.Helper()
		if _, err := d.call("mouse_click", map[string]any{"selector": selector}); err != nil {
			t.Fatalf("mouse click %s: %v", selector, err)
		}
	}

	click(".sidebar-toggle")
	eventually(t, 5*time.Second, func() error {
		var state struct {
			Collapsed bool   `json:"collapsed"`
			Expanded  string `json:"expanded"`
		}
		if err := d.eval(`const app = document.querySelector(".wish-app"); const toggle = document.querySelector(".sidebar-toggle"); return {
			collapsed: app?.classList.contains("sidebar-collapsed") ?? false,
			expanded: toggle?.getAttribute("aria-expanded") ?? "",
		};`, &state); err != nil {
			return err
		}
		if !state.Collapsed || state.Expanded != "false" {
			return fmt.Errorf("toolbar did not collapse sidebar: %+v", state)
		}
		return nil
	})
	click(".sidebar-toggle")
	eventually(t, 5*time.Second, func() error {
		var state struct {
			Collapsed bool   `json:"collapsed"`
			Expanded  string `json:"expanded"`
		}
		if err := d.eval(`const app = document.querySelector(".wish-app"); const toggle = document.querySelector(".sidebar-toggle"); return {
			collapsed: app?.classList.contains("sidebar-collapsed") ?? false,
			expanded: toggle?.getAttribute("aria-expanded") ?? "",
		};`, &state); err != nil {
			return err
		}
		if state.Collapsed || state.Expanded != "true" {
			return fmt.Errorf("toolbar did not expand sidebar: %+v", state)
		}
		return nil
	})
}

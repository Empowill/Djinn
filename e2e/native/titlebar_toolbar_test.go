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
// The macOS geometry check keeps both controls below the native drag strip; this test does not claim to synthesize an
// AppKit NSEvent.
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
			toggle: !!document.querySelector(".app-toolbar-toggle"),
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
		Back   map[string]float64 `json:"back"`
		Toggle map[string]float64 `json:"toggle"`
	}
	if err := d.eval(`
		const box = (selector) => { const r = document.querySelector(selector)?.getBoundingClientRect(); return r && {
			x: r.x, y: r.y, width: r.width, height: r.height,
		}; };
		return { back: box(".app-toolbar-back"), toggle: box(".app-toolbar-toggle") };
	`, &geometry); err != nil {
		t.Fatal(err)
	}
	if geometry.Back["x"] != 82 || geometry.Back["y"] != 6 || geometry.Back["width"] != 20 || geometry.Back["height"] != 20 {
		t.Fatalf("unexpected native back geometry: %+v", geometry.Back)
	}
	if geometry.Toggle["x"] != 106 || geometry.Toggle["y"] != 6 || geometry.Toggle["width"] != 20 || geometry.Toggle["height"] != 20 {
		t.Fatalf("unexpected native toggle geometry: %+v", geometry.Toggle)
	}

	click := func(selector string) {
		t.Helper()
		if _, err := d.call("mouse_click", map[string]any{"selector": selector}); err != nil {
			t.Fatalf("mouse click %s: %v", selector, err)
		}
	}

	click(".app-toolbar-toggle")
	eventually(t, 5*time.Second, func() error {
		var state struct {
			Collapsed bool   `json:"collapsed"`
			Expanded  string `json:"expanded"`
		}
		if err := d.eval(`const app = document.querySelector(".wish-app"); const toggle = document.querySelector(".app-toolbar-toggle"); return {
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
	click(".app-toolbar-toggle")
	eventually(t, 5*time.Second, func() error {
		var state struct {
			Collapsed bool   `json:"collapsed"`
			Expanded  string `json:"expanded"`
		}
		if err := d.eval(`const app = document.querySelector(".wish-app"); const toggle = document.querySelector(".app-toolbar-toggle"); return {
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

	click(".sidebar .new-mission")
	eventually(t, 30*time.Second, func() error {
		var creation struct {
			Mounted bool `json:"mounted"`
		}
		if err := d.eval(`return { mounted: !!document.querySelector(".wish-creation") };`, &creation); err != nil {
			return err
		}
		if !creation.Mounted {
			return fmt.Errorf("creation surface has not mounted")
		}
		return nil
	})
	click(".app-toolbar-back")
	eventually(t, 10*time.Second, func() error {
		var creation struct {
			Mounted bool `json:"mounted"`
		}
		if err := d.eval(`return { mounted: !!document.querySelector(".wish-creation") };`, &creation); err != nil {
			return err
		}
		if creation.Mounted {
			return fmt.Errorf("native back did not close creation")
		}
		return nil
	})
}

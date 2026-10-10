package native

import (
	"fmt"
	"os"
	"testing"
	"time"
)

// TestNativeSmokeGeometry records the native WebKit text surface at two window heights. It deliberately does not
// submit a wish or call a provider: this is a layout probe for the smoke surface only.
func TestNativeSmokeGeometry(t *testing.T) {
	binary := os.Getenv("DJINN_E2E_NATIVE")
	if binary == "" {
		t.Skip("opens a native window: run it with go tool task e2e-native")
	}
	d := start(t, binary)

	eventually(t, 30*time.Second, func() error {
		var ready struct {
			Bridge bool `json:"bridge"`
			New    bool `json:"newMission"`
		}
		if err := d.eval(`return {
			bridge: !!document.querySelector(".wish-app"),
			newMission: !!document.querySelector(".sidebar .new-mission"),
		};`, &ready); err != nil {
			return err
		}
		if !ready.Bridge || !ready.New {
			return fmt.Errorf("native page not ready: %+v", ready)
		}
		return nil
	})

	if _, err := d.call("window_control", map[string]any{
		"action": "set_size",
		"width":  1280,
		"height": 900,
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.eval(`document.querySelector(".sidebar .new-mission")?.click(); return { clicked: true };`, &struct {
		Clicked bool `json:"clicked"`
	}{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, 30*time.Second, func() error {
		var page struct {
			Creation bool `json:"creation"`
		}
		if err := d.eval(`return { creation: !!document.querySelector(".wish-creation") };`, &page); err != nil {
			return err
		}
		if !page.Creation {
			return fmt.Errorf("creation surface has not mounted")
		}
		return nil
	})

	// Measure only after the worker has committed a complete visible HTML
	// frame. The initial layer contains a short blank placeholder while native
	// WebKit brings up the worker-backed WebGL surface.
	eventually(t, 45*time.Second, func() error {
		var smoke struct {
			Visible       bool `json:"visible"`
			Ready         bool `json:"ready"`
			TextLength    int  `json:"textLength"`
			LineCount     int  `json:"lineCount"`
			NonWhitespace int  `json:"nonWhitespace"`
		}
		if err := d.eval(`
			const layers = [...document.querySelectorAll(".wish-smoke-ascii pre")];
			const text = layers.map((layer) => layer.textContent || "").join("\n");
			const canvas = document.querySelector(".wish-smoke-canvas");
			return {
				visible: canvas?.dataset.visible === "true",
				ready: canvas?.dataset.ready === "true",
				textLength: text.length,
				lineCount: Math.max(...layers.map((layer) => (layer.textContent || "").split("\n").length), 0),
				nonWhitespace: (text.match(/\S/g) ?? []).length,
			};`, &smoke); err != nil {
			return err
		}
		if !smoke.Visible || !smoke.Ready || smoke.TextLength < 1000 || smoke.LineCount < 10 || smoke.NonWhitespace == 0 {
			return fmt.Errorf("smoke frame not committed: %+v", smoke)
		}
		return nil
	})

	for _, size := range []struct {
		width  int
		height int
	}{{1280, 900}, {1280, 1200}, {2560, 1080}} {
		if _, err := d.call("window_control", map[string]any{
			"action": "set_size",
			"width":  size.width,
			"height": size.height,
		}); err != nil {
			t.Fatal(err)
		}
		// Resize commits a new native PRE asynchronously. Wait for the new
		// committed frame instead of sampling the old frame after set_size.
		eventually(t, 15*time.Second, func() error {
			var smoke struct {
				Visible       bool    `json:"visible"`
				Ready         bool    `json:"ready"`
				TextLength    int     `json:"textLength"`
				LineCount     int     `json:"lineCount"`
				NonWhitespace int     `json:"nonWhitespace"`
				PreWidth      float64 `json:"preWidth"`
				PreHeight     float64 `json:"preHeight"`
				PreBottom     float64 `json:"preBottom"`
				SmokeWidth    float64 `json:"smokeWidth"`
				SmokeHeight   float64 `json:"smokeHeight"`
				SmokeBottom   float64 `json:"smokeBottom"`
				LineHeight    float64 `json:"lineHeight"`
				CharWidth     float64 `json:"charWidth"`
			}
			if err := d.eval(`
				const layers = [...document.querySelectorAll(".wish-smoke-ascii pre")];
				const text = layers.map((layer) => layer.textContent || "").join("\n");
				const pre = layers[0];
				const smoke = document.querySelector(".wish-smoke");
				const preBox = pre?.getBoundingClientRect();
				const smokeBox = smoke?.getBoundingClientRect();
				const style = pre ? getComputedStyle(pre) : null;
				const columns = Math.max(...layers.map((layer) => Math.max(...(layer.textContent || "").split("\n").map((line) => line.length), 0)), 0);
				const canvas = document.querySelector(".wish-smoke-canvas");
				return {
					visible: canvas?.dataset.visible === "true",
					ready: canvas?.dataset.ready === "true",
					textLength: text.length,
					lineCount: Math.max(...layers.map((layer) => (layer.textContent || "").split("\n").length), 0),
					nonWhitespace: (text.match(/\S/g) ?? []).length,
					preWidth: preBox?.width || 0,
					preHeight: preBox?.height || 0,
					preBottom: preBox?.bottom || 0,
					smokeWidth: smokeBox?.width || 0,
					smokeHeight: smokeBox?.height || 0,
					smokeBottom: smokeBox?.bottom || 0,
					lineHeight: style ? parseFloat(style.lineHeight) : 0,
					charWidth: columns ? (preBox?.width || 0) / columns : 0,
				};`, &smoke); err != nil {
				return err
			}
			if !smoke.Visible || !smoke.Ready || smoke.TextLength < 1000 || smoke.LineCount < 10 || smoke.NonWhitespace == 0 {
				return fmt.Errorf("resized smoke frame not committed: %+v", smoke)
			}
			if smoke.PreHeight <= 0 || smoke.SmokeHeight <= 0 {
				return fmt.Errorf("resized smoke geometry is empty: %+v", smoke)
			}
			widthGap := smoke.SmokeWidth - smoke.PreWidth
			heightGap := smoke.SmokeHeight - smoke.PreHeight
			if smoke.CharWidth <= 0 || smoke.LineHeight <= 0 || widthGap < -1 || widthGap > smoke.CharWidth+1 || heightGap < -1 || heightGap > smoke.LineHeight+1 || smoke.SmokeBottom-smoke.PreBottom < -1 || smoke.SmokeBottom-smoke.PreBottom > 1 {
				return fmt.Errorf("resized smoke geometry is stale or misaligned: %+v", smoke)
			}
			return nil
		})
		var metrics struct {
			Window struct {
				Width  float64 `json:"width"`
				Height float64 `json:"height"`
			} `json:"window"`
			Surface struct {
				Width  float64 `json:"width"`
				Height float64 `json:"height"`
				Bottom float64 `json:"bottom"`
			} `json:"surface"`
			Smoke struct {
				Width  float64 `json:"width"`
				Height float64 `json:"height"`
				Bottom float64 `json:"bottom"`
			} `json:"smoke"`
			Ascii struct {
				Width  float64 `json:"width"`
				Height float64 `json:"height"`
				Bottom float64 `json:"bottom"`
			} `json:"ascii"`
			Pre struct {
				Width  float64 `json:"width"`
				Height float64 `json:"height"`
				Bottom float64 `json:"bottom"`
			} `json:"pre"`
			FontSize       float64 `json:"fontSize"`
			LineHeight     float64 `json:"lineHeight"`
			LineCount      int     `json:"lineCount"`
			TextLength     int     `json:"textLength"`
			NonWhitespace  int     `json:"nonWhitespace"`
			RangeRectCount int     `json:"rangeRectCount"`
			LastRow        struct {
				Top    float64 `json:"top"`
				Bottom float64 `json:"bottom"`
				Height float64 `json:"height"`
			} `json:"lastRow"`
			LastNonemptyLayer int `json:"lastNonemptyLayer"`
			LastNonemptyRow   int `json:"lastNonemptyRow"`
			LastNonemptyRange struct {
				Top    float64 `json:"top"`
				Bottom float64 `json:"bottom"`
				Height float64 `json:"height"`
			} `json:"lastNonemptyRange"`
			StatusbarDisplay string `json:"statusbarDisplay"`
		}
		if err := d.eval(`
			const rect = (element) => {
				if (!element) return {width: 0, height: 0, bottom: 0};
				const box = element.getBoundingClientRect();
				return {width: box.width, height: box.height, bottom: box.bottom};
			};
			const surface = document.querySelector(".wish-creation");
			const smoke = document.querySelector(".wish-smoke");
			const ascii = document.querySelector(".wish-smoke-ascii");
			const layers = [...document.querySelectorAll(".wish-smoke-ascii pre")];
			const pre = layers[0];
			const text = pre?.textContent ?? "";
			const lines = text.split("\n");
			const lineRanges = (element) => {
				if (!element) return [];
				const range = document.createRange();
				range.selectNodeContents(element);
				const boxes = [...range.getClientRects()].filter((box) => box.height).map((box) => ({top: box.top, bottom: box.bottom, height: box.height}));
				range.detach();
				const rows = [];
				for (const box of boxes) {
					const row = rows[rows.length - 1];
					if (row && Math.abs(row.top - box.top) < 0.5) row.bottom = Math.max(row.bottom, box.bottom);
					else rows.push({...box});
				}
				return rows;
			};
			const fullRows = lineRanges(pre);
			const lastRow = fullRows[fullRows.length - 1] || {top: 0, bottom: 0, height: 0};
			let lastNonemptyLayer = -1;
			let lastNonemptyRow = -1;
			for (let layerIndex = 0; layerIndex < layers.length; layerIndex++) {
				const layerLines = (layers[layerIndex].textContent || "").split("\n");
				for (let rowIndex = 0; rowIndex < layerLines.length; rowIndex++) {
					if (
						/\S/.test(layerLines[rowIndex]) &&
						rowIndex >= lastNonemptyRow
					) {
						lastNonemptyLayer = layerIndex;
						lastNonemptyRow = rowIndex;
					}
				}
			}
			const nonemptyRows = lineRanges(layers[lastNonemptyLayer]);
			const lastNonemptyRange = nonemptyRows[lastNonemptyRow] || nonemptyRows[nonemptyRows.length - 1] || {top: 0, bottom: 0, height: 0};
			const style = pre ? getComputedStyle(pre) : null;
			return {
				window: {width: window.innerWidth, height: window.innerHeight},
				surface: rect(surface), smoke: rect(smoke), ascii: rect(ascii), pre: rect(pre),
				fontSize: style ? parseFloat(style.fontSize) : 0,
				lineHeight: style ? parseFloat(style.lineHeight) : 0,
				lineCount: lines.length, textLength: text.length,
				nonWhitespace: (text.match(/\S/g) ?? []).length,
				rangeRectCount: fullRows.length, fullRangeRowCount: fullRows.length,
				lastRow, lastNonemptyLayer, lastNonemptyRow, lastNonemptyRange,
				statusbarDisplay: getComputedStyle(document.querySelector(".app-statusbar") ?? document.body).display,
			};`, &metrics); err != nil {
			t.Fatal(err)
		}
		logicalGap := metrics.Pre.Bottom - metrics.LastRow.Bottom
		t.Logf("native smoke geometry window=%gx%g surface=%gx%g smoke=%gx%g bottom=%g ascii=%gx%g bottom=%g pre=%gx%g bottom=%g font=%g line=%g lines=%d chars=%d nonspace=%d rangeRects=%d lastRow=%+v logicalGap=%g lastNonempty=layer%d/row%d range=%+v nonemptyGap=%g statusbar=%s",
			metrics.Window.Width, metrics.Window.Height,
			metrics.Surface.Width, metrics.Surface.Height,
			metrics.Smoke.Width, metrics.Smoke.Height, metrics.Smoke.Bottom,
			metrics.Ascii.Width, metrics.Ascii.Height, metrics.Ascii.Bottom,
			metrics.Pre.Width, metrics.Pre.Height, metrics.Pre.Bottom,
			metrics.FontSize, metrics.LineHeight, metrics.LineCount, metrics.TextLength,
			metrics.NonWhitespace, metrics.RangeRectCount, metrics.LastRow, logicalGap,
			metrics.LastNonemptyLayer, metrics.LastNonemptyRow, metrics.LastNonemptyRange,
			metrics.Pre.Bottom-metrics.LastNonemptyRange.Bottom, metrics.StatusbarDisplay)
		if metrics.LastRow.Height <= 0 || logicalGap < -1 || logicalGap > metrics.LineHeight+1 {
			t.Errorf("native smoke last logical row leaves a bottom gap: preBottom=%g lastRowBottom=%g lineHeight=%g row=%+v", metrics.Pre.Bottom, metrics.LastRow.Bottom, metrics.LineHeight, metrics.LastRow)
		}
	}
}

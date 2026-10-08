//go:build !headless && (cgo || windows)

package main

import (
	"context"
	"net/http"
	"runtime"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/empowill/djinn"
)

// hasWindow tells that this build opens a native window: Wails needs CGO on macOS and Linux, not on Windows.
const hasWindow = true

// openWindow shows the interface in a native window, and returns once the user quits or ctx is done. With assets,
// the window loads it from the Wails internal asset server (wails://), with no port; without, it loads url, the
// loopback server. The window adds nothing of its own: no JS bindings; native features are Connect methods.
//
// Closing the window does not quit Djinn: the lead in its terminal and the workers go on. On Linux and Windows the
// window is minimised, so that it stays in the dock or the task bar; on macOS it is hidden, as macOS apps do, and the
// Dock shows it again. Quitting is explicit: "Quit Djinn" in the tray menu, Ctrl+Q (Cmd+Q on macOS) in the window,
// or stopping djinn up. Each value received on raise brings the window back to the front.
func openWindow(ctx context.Context, url string, assets http.Handler, raise <-chan struct{}) error {
	opts := application.Options{
		Name: "Djinn",
		Icon: djinn.Icon,
		// The window never closes, it only goes out of sight: these keep the app running whatever happens to it.
		Mac:     application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: false},
		Linux:   application.LinuxOptions{DisableQuitOnLastWindowClosed: true, ProgramName: "djinn"},
		Windows: application.WindowsOptions{DisableQuitOnLastWindowClosed: true},
	}
	if assets != nil {
		opts.Assets = application.AssetOptions{Handler: assets}
		url = "/"
	}
	app := application.New(opts)
	quit := func() { app.Quit() }
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "Djinn",
		Width:  1440,
		Height: 1000,
		URL:    url,
		KeyBindings: map[string]func(application.Window){
			"CmdOrCtrl+q": func(application.Window) { quit() },
		},
	})
	show := func() {
		window.UnMinimise()
		window.Show()
		window.Focus()
	}
	// The close button, and anything that closes the window: out of sight, never gone.
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		e.Cancel()
		if runtime.GOOS == "darwin" {
			window.Hide()
		} else {
			window.Minimise()
		}
	})
	if runtime.GOOS == "darwin" {
		// A click on the Dock icon brings the hidden window back.
		app.Event.OnApplicationEvent(events.Mac.ApplicationShouldHandleReopen, func(*application.ApplicationEvent) { show() })
	}
	// The tray says Djinn runs, and is the way to quit it where the dock only closes windows (GNOME).
	menu := application.NewMenu()
	menu.Add("Show Djinn").OnClick(func(*application.Context) { show() })
	menu.AddSeparator()
	menu.Add("Quit Djinn").OnClick(func(*application.Context) { quit() })
	tray := app.SystemTray.New()
	tray.SetIcon(djinn.Icon)
	tray.SetTooltip("Djinn")
	tray.SetMenu(menu)
	if runtime.GOOS != "darwin" {
		tray.OnClick(show) // On macOS a click opens the menu, as a menu bar item does.
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				quit()
				return
			case <-raise:
				show()
			}
		}
	}()
	return app.Run()
}

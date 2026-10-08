//go:build !headless && (cgo || windows)

package main

import (
	"context"
	"log"
	"net/http"
	"runtime"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"

	"github.com/empowill/djinn"
	"github.com/empowill/djinn/internal/ui"
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
//
// While the window runs, notices show the questions as system notifications.
func openWindow(ctx context.Context, url string, assets http.Handler, raise <-chan struct{}, notices *ui.Notices) error {
	opts := application.Options{
		Name: "Djinn",
		Icon: appIcon(),
		// The window never closes, it only goes out of sight: these keep the app running whatever happens to it.
		Mac:     application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: false},
		Linux:   application.LinuxOptions{DisableQuitOnLastWindowClosed: true, ProgramName: "djinn"},
		Windows: application.WindowsOptions{DisableQuitOnLastWindowClosed: true},
		Services: []application.Service{
			application.NewService(&noticeService{svc: notifications.New(), notices: notices, ctx: ctx}),
		},
	}
	if assets != nil {
		opts.Assets = application.AssetOptions{Handler: windowAssets(assets)}
		url = "/"
	}
	app := application.New(opts)
	quit := func() { app.Quit() }
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  windowTitle,
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

// appIcon is the icon of the app, and on Linux of its window: there GTK 3 drops an icon of 512 px or more, so the
// window gets the 256 px one.
func appIcon() []byte {
	if runtime.GOOS == "linux" {
		return djinn.Icon256
	}
	return djinn.Icon
}

// noticeService starts the notification service of Wails with the app, then shows the notices through it. A system
// that cannot show notifications (no session bus on Linux, an app without a bundle on macOS) leaves them off, and
// the window opens all the same. It has no exported method of its own: the page cannot call it.
type noticeService struct {
	svc     *notifications.NotificationService
	notices *ui.Notices
	ctx     context.Context

	mu         sync.Mutex
	started    bool
	categories map[string]bool // registered, by identifier
}

func (n *noticeService) ServiceStartup(ctx context.Context, opts application.ServiceOptions) error {
	if err := n.svc.ServiceStartup(ctx, opts); err != nil {
		log.Printf("djinn: no system notifications: %v", err)
		return nil
	}
	n.mu.Lock()
	n.started = true
	n.mu.Unlock()
	n.svc.OnNotificationResponse(func(res notifications.NotificationResult) {
		if res.Error != nil {
			log.Printf("djinn: a notification's response: %v", res.Error)
			return
		}
		r := ui.Response{QuestionID: res.Response.ID}
		r.WishID, _ = res.Response.UserInfo["wish_id"].(string)
		if action := res.Response.ActionIdentifier; action != notifications.DefaultActionIdentifier {
			r.Action = action
		}
		go n.notices.Respond(n.ctx, r)
	})
	n.notices.Use(wailsNotifier{n})
	return nil
}

func (n *noticeService) ServiceShutdown() error {
	n.notices.Use(nil)
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.started {
		return nil
	}
	n.started = false
	return n.svc.ServiceShutdown()
}

// wailsNotifier shows a notice through the service: its buttons are a category of the service, one per set of
// buttons, registered once.
type wailsNotifier struct{ n *noticeService }

func (w wailsNotifier) Notify(note ui.Notification) error {
	opts := notifications.NotificationOptions{
		ID: note.ID, Title: note.Title, Body: note.Body,
		Data: map[string]any{"wish_id": note.WishID},
	}
	if len(note.Actions) == 0 {
		return w.n.svc.SendNotification(opts)
	}
	category := notifications.NotificationCategory{ID: "djinn-answer"}
	for _, a := range note.Actions {
		category.ID += "-" + a.ID
		category.Actions = append(category.Actions, notifications.NotificationAction{ID: a.ID, Title: a.Title})
	}
	category.ID = strings.ToLower(category.ID)
	w.n.mu.Lock()
	if !w.n.categories[category.ID] {
		if err := w.n.svc.RegisterNotificationCategory(category); err != nil {
			w.n.mu.Unlock()
			return err
		}
		if w.n.categories == nil {
			w.n.categories = map[string]bool{}
		}
		w.n.categories[category.ID] = true
	}
	w.n.mu.Unlock()
	opts.CategoryID = category.ID
	return w.n.svc.SendNotificationWithActions(opts)
}

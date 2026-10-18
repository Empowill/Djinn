//go:build !headless && (cgo || windows)

package main

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"

	"github.com/empowill/djinn"
	"github.com/empowill/djinn/internal/logx"
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
// or stopping djinn up; each names itself to quit first. Each value received on raise brings the window back to the
// front.
//
// While the window runs, notices show what waits for the developer as system notifications, except for the wish the
// window shows while it is in front, and shortcuts takes the global shortcut that brings the window forward. A
// djinn:// link the system gives the app goes to links: macOS sends it to Djinn.app, and starts the app for it when it
// does not run; Linux and Windows run djinn open instead.
//
// With a watch, the window's page is watched from the start, its load events logged: silent or dead, it loads again
// once, then the window opens again once with a new view, the server and the workers going on. The new view draws in
// software rendering (WebKit's hardware acceleration policy NEVER) only after a GPU hang: the environment cannot do
// it here, WebKitGTK reads WEBKIT_DISABLE_DMABUF_RENDERER once per process, at its first view (W354's probe), and the
// next start of Djinn renders on the GPU again (decision Q89).
func openWindow(ctx context.Context, cfg windowConfig) error {
	notices, shortcuts := cfg.notices, cfg.shortcuts
	opts := application.Options{
		Name: "Djinn",
		Icon: appIcon(),
		// Wails reports here what goes wrong outside a call, such as a global shortcut that the portal of a Wayland
		// desktop refuses, or cannot take, after it was asked; a fatal error, after which Wails exits.
		ErrorHandler: func(err error) {
			var fatal *application.FatalError
			if errors.As(err, &fatal) {
				logx.L().Error("window: fatal error, djinn up stops", "err", err)
			} else {
				logx.L().Warn("window", "err", err)
			}
			if shortcutRefusal(err) {
				shortcuts.Refused(err)
			}
		},
		WarningHandler: func(msg string) { logx.L().Warn("window", "warning", msg) },
		// A panic in a callback of the window (an event, a menu, a call on the main thread) is recovered by Wails: it
		// is logged with its stack, and the window goes on, where Wails would exit.
		PanicHandler: func(p *application.PanicDetails) {
			logx.L().Error("panic recovered", "in", "window", "panic", p.Error, "stack", p.FullStackTrace)
		},
		// The window never closes, it only goes out of sight: these keep the app running whatever happens to it.
		Mac:     application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: false},
		Linux:   application.LinuxOptions{DisableQuitOnLastWindowClosed: true, ProgramName: "djinn"},
		Windows: application.WindowsOptions{DisableQuitOnLastWindowClosed: true},
		Services: []application.Service{
			application.NewService(&noticeService{svc: notifications.New(), notices: notices, ctx: ctx}),
		},
	}
	url := cfg.url
	if cfg.assets != nil {
		opts.Assets = application.AssetOptions{Handler: windowAssets(cfg.assets)}
		url = "/"
	}
	app := application.New(opts)
	quit := func(source string) {
		if cfg.quit != nil {
			cfg.quit(source)
		}
		app.Quit()
	}
	var current atomic.Pointer[application.WebviewWindow]
	show := func() {
		window := current.Load()
		window.UnMinimise()
		window.Show()
		window.Focus()
	}
	// open makes the window, on the GPU or in software rendering.
	open := func(software bool) *shownWindow {
		gpu := application.WebviewGpuPolicyAlways
		if software {
			gpu = application.WebviewGpuPolicyNever
		}
		window := app.Window.NewWithOptions(application.WebviewWindowOptions{
			Title:  windowTitle,
			Width:  1440,
			Height: 1000,
			URL:    url,
			Mac: application.MacWindow{
				// Extend the webview beneath a transparent titlebar while keeping the native
				// traffic lights. Keep only a 6px native drag strip: the frontend extends
				// the official wails:drag protocol through a transparent zone below it,
				// while the persistent toolbar starts at y=6 and remains clickable.
				TitleBar:                application.MacTitleBarHidden,
				InvisibleTitleBarHeight: 6,
			},
			Linux: application.LinuxWindow{WebviewGpuPolicy: gpu},
			KeyBindings: map[string]func(application.Window){
				"CmdOrCtrl+q": func(application.Window) { quit(stopCtrlQ) },
			},
		})
		// No notification for the wish the window shows while it is in front: the developer sees it already.
		window.OnWindowEvent(events.Common.WindowFocus, func(*application.WindowEvent) { notices.Focus(true) })
		for _, e := range []events.WindowEventType{events.Common.WindowLostFocus, events.Common.WindowHide, events.Common.WindowMinimise} {
			window.OnWindowEvent(e, func(*application.WindowEvent) { notices.Focus(false) })
		}
		// The close button, and anything that closes the window: out of sight, never gone, until it is replaced.
		shown := &shownWindow{window: window}
		// Each load event of the page goes to the log, at every start: a black start names where its load stopped.
		if cfg.watch != nil {
			for e, name := range map[events.WindowEventType]string{
				events.Linux.WindowLoadStarted: "started", events.Linux.WindowLoadRedirected: "redirected",
				events.Linux.WindowLoadCommitted: "committed", events.Linux.WindowLoadFinished: "finished",
			} {
				window.OnWindowEvent(e, func(*application.WindowEvent) {
					if !shown.retired.Load() {
						cfg.watch.Loaded(name)
					}
				})
			}
		}
		window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
			if shown.retired.Load() {
				return
			}
			e.Cancel()
			if runtime.GOOS == "darwin" {
				window.Hide()
			} else {
				window.Minimise()
			}
		})
		current.Store(window)
		return shown
	}
	first := open(false)
	// The web process of a window's page that ends, at its start or any time after, tells the watch, and a load that
	// fails goes to its log; not once the window is replaced, which ends them. The page's console goes to djinn up's
	// output. Connected on the main thread, once the window is native.
	watchPage := func(shown *shownWindow) {
		if cfg.watch == nil {
			return
		}
		application.InvokeSync(func() {
			watchView(shown.window.NativeWindow(), viewHooks{
				ended: func(reason string) {
					if shown.retired.Load() {
						return
					}
					go func() {
						defer logx.Crash("window: web process ended")
						cfg.watch.Ended(reason)
					}()
				},
				failed: func(text string) {
					if !shown.retired.Load() {
						cfg.watch.Loaded("failed: " + text)
					}
				},
			})
		})
	}
	if cfg.watch != nil {
		var mu sync.Mutex
		shown := first
		// A silent page loads again in its view first, then the window gets a new view and a new web process.
		cfg.watch.Reload = func() error {
			mu.Lock()
			defer mu.Unlock()
			shown.window.SetURL(url)
			return nil
		}
		cfg.watch.Recreate = func(software bool) error {
			mu.Lock()
			defer mu.Unlock()
			next := open(software)
			watchPage(next)
			shown.retired.Store(true)
			shown.window.Close()
			shown = next
			show()
			return nil
		}
	}
	// The global shortcut is taken once the app runs: Wails then answers whether the system took it. The window's
	// page has from then on to call the server.
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		shortcuts.Use(wailsShortcuts{app})
		if cfg.watch != nil {
			cfg.watch.Opened()
			go func() {
				defer logx.Crash("window: watch the page")
				watchPage(first)
			}()
		}
	})
	defer shortcuts.Use(nil)
	app.Event.OnApplicationEvent(events.Common.ApplicationLaunchedWithUrl, func(e *application.ApplicationEvent) {
		if raw := e.Context().URL(); raw != "" {
			go func() {
				defer logx.Crash("window: open a link")
				cfg.links(raw)
			}()
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
	menu.Add("Quit Djinn").OnClick(func(*application.Context) { quit(stopTray) })
	tray := app.SystemTray.New()
	tray.SetIcon(djinn.Icon)
	tray.SetTooltip("Djinn")
	tray.SetMenu(menu)
	if runtime.GOOS != "darwin" {
		tray.OnClick(show) // On macOS a click opens the menu, as a menu bar item does.
	}

	go func() {
		defer logx.Crash("window: raise")
		for {
			select {
			case <-ctx.Done():
				app.Quit() // Its source is named already.
				return
			case <-cfg.raise:
				show()
			}
		}
	}()
	return app.Run()
}

// viewHooks is what the watch hears of a window's view, beside its load events (watchView).
type viewHooks struct {
	// ended is called with the reason when the page's web process ends: "crashed", "memory" or "terminated".
	ended func(reason string)
	// failed is called with the URI and the error when a load fails.
	failed func(text string)
}

// shownWindow is a window openWindow made, retired once another replaces it: then it closes for good, and the end
// of its web process is no news.
type shownWindow struct {
	window  *application.WebviewWindow
	retired atomic.Bool
}

// chooseFolder opens the system's folder dialog over the window, titled title and open in directory: one folder, a
// new one may be made there. It returns the folder chosen, or empty when the user cancelled.
func chooseFolder(title, directory string) (string, error) {
	app := application.Get()
	if app == nil {
		return "", errors.New("the window is not open")
	}
	return app.Dialog.OpenFileWithOptions(&application.OpenFileDialogOptions{
		CanChooseDirectories: true,
		CanCreateDirectories: true,
		Title:                title,
		Directory:            directory,
		Window:               app.Window.Current(), // The window the click came from; none leaves the dialog free.
	}).PromptForSingleSelection()
}

// wailsShortcuts takes global shortcuts through Wails: Carbon hot keys on macOS, RegisterHotKey on Windows, XGrabKey
// on X11, and on Wayland the GlobalShortcuts portal of the desktop, which asks the user and may bind other keys.
type wailsShortcuts struct{ app *application.App }

func (w wailsShortcuts) Register(chord string, pressed func()) error {
	return w.app.GlobalShortcut.Register(chord, pressed)
}

func (w wailsShortcuts) Unregister(chord string) error { return w.app.GlobalShortcut.Unregister(chord) }

// shortcutRefusal tells that an error Wails reports on its own is about the global shortcuts.
func shortcutRefusal(err error) bool {
	text := err.Error()
	return strings.HasPrefix(text, "global shortcuts:") || strings.HasPrefix(text, "failed to register global shortcut")
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
// the window opens all the same. On macOS nothing shows until the user allows it: the service asks at start, which
// shows the system's dialog the first time only. It has no exported method of its own: the page cannot call it.
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
		logx.L().Warn("no system notifications", "err", err)
		return nil
	}
	n.mu.Lock()
	n.started = true
	n.mu.Unlock()
	n.svc.OnNotificationResponse(func(res notifications.NotificationResult) {
		if res.Error != nil {
			logx.L().Warn("a notification's response", "err", res.Error)
			return
		}
		r := ui.Response{}
		r.WishID, _ = res.Response.UserInfo["wish_id"].(string)
		r.QuestionID, _ = res.Response.UserInfo["question_id"].(string)
		r.Target, _ = res.Response.UserInfo["target"].(string)
		if action := res.Response.ActionIdentifier; action != notifications.DefaultActionIdentifier {
			r.Action = action
		}
		go func() {
			defer logx.Crash("notices: respond", "wish", r.WishID)
			n.notices.Respond(n.ctx, r)
		}()
	})
	n.notices.Use(wailsNotifier{n})
	go func() {
		defer logx.Crash("notices: permission")
		// Without the user's permission, macOS refuses every notification (UNErrorDomain error 1).
		if allowed, err := n.svc.RequestNotificationAuthorization(); err != nil || !allowed {
			logx.L().Warn("system notifications are not allowed: see Djinn's settings", "err", err)
		}
	}()
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

func (w wailsNotifier) Allowed() (bool, error) { return w.n.svc.CheckNotificationAuthorization() }

func (w wailsNotifier) Allow() (bool, error) { return w.n.svc.RequestNotificationAuthorization() }

func (w wailsNotifier) Notify(note ui.Notification) error {
	opts := notifications.NotificationOptions{
		ID: note.ID, Title: note.Title, Body: note.Body,
		Data: map[string]any{"wish_id": note.WishID, "question_id": note.QuestionID, "target": note.Target},
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

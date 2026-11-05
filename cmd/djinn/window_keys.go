//go:build !headless && (cgo || windows)

package main

import "github.com/wailsapp/wails/v3/pkg/application"

// windowKeyBindings are the window's own keys: Ctrl+Q quits. None on macOS: Wails names a key press there by its
// place on a US keyboard, so the key that types A on a French keyboard reads as Q, and Cmd+A in a field would quit
// Djinn. Cmd+Q is the application menu's there (appMenu), which AppKit matches on what the layout types.
func windowKeyBindings(goos string, quit func(source string)) map[string]func(application.Window) {
	if goos == "darwin" {
		return nil
	}
	return map[string]func(application.Window){
		"CmdOrCtrl+q": func(application.Window) { quit(stopCtrlQ) },
	}
}

// appMenu is the application menu of macOS: Hide Djinn on Cmd+H, Hide Others on Cmd+Option+H, Quit Djinn on Cmd+Q,
// which names itself as Ctrl+Q does, and the Edit and Window menus, whose Select All, Copy, Paste or Minimize a field
// and the window answer. The Hide items carry their role, which AppKit runs, but their own label: the role's
// constructor reads the running application's name, and the menu is built before it runs.
func appMenu(quit func(source string)) *application.Menu {
	menu := application.NewMenu()
	app := menu.AddSubmenu("Djinn")
	app.Add("Hide Djinn").SetAccelerator("CmdOrCtrl+h").SetRole(application.Hide)
	app.Add("Hide Others").SetAccelerator("CmdOrCtrl+OptionOrAlt+h").SetRole(application.HideOthers)
	app.AddSeparator()
	app.Add("Quit Djinn").SetAccelerator("CmdOrCtrl+q").OnClick(func(*application.Context) { quit(stopCtrlQ) })
	menu.AddRole(application.EditMenu)
	menu.AddRole(application.WindowMenu)
	return menu
}

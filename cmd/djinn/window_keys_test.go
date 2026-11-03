package main

import (
	"strings"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// On macOS Wails names a key press by its place on a US keyboard: Cmd+A on a French keyboard reads as Cmd+Q. The
// window takes no key there, so Cmd+A reaches the field, and Cmd+Q is the application menu's.
func TestWindowKeyBindingsLeaveCmdAToTheFieldOnMacOS(t *testing.T) {
	if keys := windowKeyBindings("darwin", func(string) {}); len(keys) != 0 {
		t.Fatalf("the window takes %d keys on macOS, want none: Cmd+A on an AZERTY keyboard would quit Djinn", len(keys))
	}
	var source string
	keys := windowKeyBindings("linux", func(s string) { source = s })
	quit, ok := keys["CmdOrCtrl+q"]
	if !ok {
		t.Fatalf("Ctrl+Q is not bound on Linux: %v", keys)
	}
	quit(nil)
	if source != stopCtrlQ {
		t.Fatalf("Ctrl+Q quits as %q, want %q", source, stopCtrlQ)
	}
}

func TestAppMenuQuitsOnCmdQAndSelectsAll(t *testing.T) {
	menu := appMenu(func(string) {})
	quit := menu.FindByLabel("Quit Djinn")
	// Wails writes CmdOrCtrl as the platform's modifier: Cmd+Q on macOS, Ctrl+Q elsewhere.
	if quit == nil || !strings.HasSuffix(quit.GetAccelerator(), "+Q") {
		t.Fatalf("Quit Djinn in the application menu: %v, want it on CmdOrCtrl+Q", quit)
	}
	if menu.FindByRole(application.SelectAll) == nil {
		t.Fatal("no Select All in the application menu: Cmd+A selects nothing in a field")
	}
}

// W2's menu replaced Wails' default one and dropped its Hide items: Cmd+H hid nothing. The Djinn menu holds them
// again, before Quit, and Cmd+A stays Select All's, not Quit's.
func TestAppMenuHidesOnCmdHBeforeQuit(t *testing.T) {
	menu := appMenu(func(string) {})
	hide := menu.FindByRole(application.Hide)
	if hide == nil || !strings.HasSuffix(hide.GetAccelerator(), "+H") || strings.Contains(hide.GetAccelerator(), "Option") {
		t.Fatalf("Hide Djinn in the application menu: %v, want it on CmdOrCtrl+H", hide)
	}
	others := menu.FindByRole(application.HideOthers)
	if others == nil || !strings.HasSuffix(others.GetAccelerator(), "+H") || !strings.Contains(others.GetAccelerator(), "Option") {
		t.Fatalf("Hide Others in the application menu: %v, want it on CmdOrCtrl+Option+H", others)
	}
	quit := menu.FindByLabel("Quit Djinn")
	if quit == nil {
		t.Fatal("no Quit Djinn in the application menu")
	}
	app := menu.FindByLabel("Djinn")
	if app == nil || app.GetSubmenu() == nil {
		t.Fatal("no Djinn submenu in the application menu")
	}
	var order []*application.MenuItem
	for i := 0; ; i++ {
		item := app.GetSubmenu().ItemAt(i)
		if item == nil {
			break
		}
		order = append(order, item)
	}
	if len(order) == 0 || order[0] != hide || order[1] != others || order[len(order)-1] != quit {
		t.Fatalf("the Djinn submenu holds %d items, want Hide Djinn, Hide Others, then Quit Djinn last", len(order))
	}
	if strings.HasSuffix(quit.GetAccelerator(), "+A") {
		t.Fatalf("Quit Djinn on %q: Cmd+A would quit Djinn", quit.GetAccelerator())
	}
}

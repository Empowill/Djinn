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

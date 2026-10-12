package main

import (
	"path/filepath"
	"runtime"
	"testing"
)

// TestBundled: djinn knows it runs from an app bundle by its path alone.
func TestBundled(t *testing.T) {
	for exe, want := range map[string]bool{
		"/Applications/Djinn.app/Contents/MacOS/djinn":                               true,
		"/Users/me/Applications/Djinn.app/Contents/MacOS/djinn":                      true,
		"/Users/me/Downloads/Djinn 2.app/Contents/MacOS/djinn":                       true,
		"/private/var/folders/x/AppTranslocation/y/d/Djinn.APP/Contents/MacOS/djinn": true,
		"/usr/local/bin/djinn":                             false,
		"/Users/me/go/bin/djinn":                           false,
		"/Applications/Djinn.app/Contents/MacOS/djinn-app": false,
		"/Applications/Djinn.app/Contents/Resources/djinn": false,
		"/Applications/Djinn/Contents/MacOS/djinn":         false,
		"/Applications/Djinn.app/MacOS/djinn":              false,
		"djinn":                                            false,
	} {
		if got := bundled(filepath.FromSlash(exe)); got != want {
			t.Errorf("bundled(%q) = %v, want %v", exe, got, want)
		}
	}
}

// TestFromFinder: no argument, or the process serial number older macOS gives, is an app opened from Finder;
// anything else is a command.
func TestFromFinder(t *testing.T) {
	for _, args := range [][]string{nil, {}, {"-psn_0_12345"}} {
		if !fromFinder(args) {
			t.Errorf("fromFinder(%q) = false", args)
		}
	}
	for _, args := range [][]string{{"up"}, {"version"}, {"-psn_0_12345", "up"}, {"--help"}} {
		if fromFinder(args) {
			t.Errorf("fromFinder(%q) = true", args)
		}
	}
	// Anywhere but in a bundle on macOS, the arguments stay.
	if got := fromBundle(nil); runtime.GOOS != "darwin" && got != nil {
		t.Errorf("fromBundle(nil) = %q on %s", got, runtime.GOOS)
	}
}

package main

import (
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

// TestBundled: djinn knows it runs from an app bundle by its path alone.
func TestBundled(t *testing.T) {
	t.Parallel()
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
		t.Run(exe, func(t *testing.T) {
			if got := bundled(filepath.FromSlash(exe)); got != want {
				t.Errorf("bundled(%q) = %v, want %v", exe, got, want)
			}
		})
	}
}

// TestFromFinder: no argument, or the process serial number older macOS gives, is an app opened from Finder;
// anything else is a command.
func TestFromFinder(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		args []string
		want bool
	}{
		{nil, true}, {[]string{}, true}, {[]string{"-psn_0_12345"}, true},
		{[]string{"up"}, false}, {[]string{"version"}, false}, {[]string{"-psn_0_12345", "up"}, false}, {[]string{"--help"}, false},
	} {
		t.Run(fmt.Sprintf("%q", c.args), func(t *testing.T) {
			if got := fromFinder(c.args); got != c.want {
				t.Errorf("fromFinder(%q) = %v, want %v", c.args, got, c.want)
			}
		})
	}
}

// TestFromBundle: anywhere but in an app bundle on macOS, which the test binary is not, the arguments stay.
func TestFromBundle(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{nil, {"up"}, {"version", "--help"}} {
		t.Run(fmt.Sprintf("%q", args), func(t *testing.T) {
			if got := fromBundle(args); !slices.Equal(got, args) {
				t.Errorf("fromBundle(%q) = %q on %s", args, got, runtime.GOOS)
			}
		})
	}
}

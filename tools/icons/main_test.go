package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const root = "../.."

// The icons in build/ are the drawing's: run `go run ./tools/icons gen` after changing build/icon.png.
func TestBuildIconsFollowTheDrawing(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "build"), 0o755); err != nil {
		t.Fatal(err)
	}
	drawing, err := os.ReadFile(filepath.Join(root, source))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, source), drawing, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := gen(tmp); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"icon.ico", "icon.icns", "icon-256.png"} {
		want, err := os.ReadFile(filepath.Join(tmp, "build", name))
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(root, "build", name))
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("build/%s is not made from build/icon.png: run go run ./tools/icons gen", name)
		}
	}
}

func TestICOAndICNSHoldEverySize(t *testing.T) {
	t.Parallel()
	img, err := load(root)
	if err != nil {
		t.Fatal(err)
	}
	ico, err := makeICO(img)
	if err != nil {
		t.Fatal(err)
	}
	if n := int(ico[4]) | int(ico[5])<<8; n != len(icoSizes) {
		t.Fatalf("the .ico holds %d images, want %d", n, len(icoSizes))
	}
	for i, size := range icoSizes {
		e := ico[6+16*i:]
		offset := int(e[12]) | int(e[13])<<8 | int(e[14])<<16 | int(e[15])<<24
		if got := decodeSize(t, ico[offset:]); got != size {
			t.Errorf(".ico entry %d is %d px, want %d", i, got, size)
		}
	}
	icns, err := makeICNS(img)
	if err != nil {
		t.Fatal(err)
	}
	if string(icns[:4]) != "icns" || int(be32(icns[4:])) != len(icns) {
		t.Fatal("the .icns header does not give its length")
	}
	at := 8
	for _, want := range icnsTypes {
		if kind := string(icns[at : at+4]); kind != want.kind {
			t.Fatalf(".icns entry %s, want %s", kind, want.kind)
		}
		if got := decodeSize(t, icns[at+8:]); got != want.size {
			t.Errorf(".icns %s is %d px, want %d", want.kind, got, want.size)
		}
		at += int(be32(icns[at+4:]))
	}
	if at != len(icns) {
		t.Errorf(".icns entries end at %d of %d bytes", at, len(icns))
	}
}

func TestDesktopPutsTheIconAndTheEntryInTheUsersFolder(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the desktop entry is for Linux (the command does nothing elsewhere), and its paths are POSIX ones")
	}
	dir := t.TempDir()
	if err := desktop(root, dir, "/opt/my tools/djinn"); err != nil {
		t.Fatal(err)
	}
	for _, size := range linuxSizes {
		data, err := os.ReadFile(filepath.Join(dir, "icons", "hicolor", fmt.Sprintf("%dx%d", size, size), "apps", "djinn.png"))
		if err != nil {
			t.Fatal(err)
		}
		if got := decodeSize(t, data); got != size {
			t.Errorf("the %d px icon is %d px", size, got)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "applications", "djinn.desktop"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{
		"Name=Djinn", `Exec="/opt/my tools/djinn" open %u`, "Icon=djinn", "StartupWMClass=djinn",
		"MimeType=x-scheme-handler/djinn;",
	} {
		if !strings.Contains(string(data), "\n"+line+"\n") {
			t.Errorf("the entry lacks %q:\n%s", line, data)
		}
	}
	// The entry follows the Desktop Entry Specification, as desktop-file-validate reads it, where it is installed.
	if validate, err := exec.LookPath("desktop-file-validate"); err == nil {
		if out, err := exec.Command(validate, filepath.Join(dir, "applications", "djinn.desktop")).CombinedOutput(); err != nil || len(out) > 0 {
			t.Errorf("desktop-file-validate: %v\n%s", err, out)
		}
	}
}

// TestDesktopEntryHandlesTheLinks: xdg-mime makes the entry the handler of djinn:// links, in the mimeapps.list of
// the user's folders, here the test's own.
func TestDesktopEntryHandlesTheLinks(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("xdg-mime"); runtime.GOOS != "linux" || err != nil {
		t.Skip("xdg-mime is for Linux, and comes with xdg-utils")
	}
	data, config := t.TempDir(), t.TempDir()
	if err := desktop(root, data, "/opt/djinn"); err != nil {
		t.Fatal(err)
	}
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "XDG_") && !strings.HasPrefix(kv, "KDE_") && !strings.HasPrefix(kv, "GNOME_") &&
			!strings.HasPrefix(kv, "DESKTOP_SESSION=") {
			env = append(env, kv)
		}
	}
	env = append(env, "XDG_DATA_HOME="+data, "XDG_CONFIG_HOME="+config, "XDG_DATA_DIRS="+data)
	if err := handleLinks(filepath.Join(data, "applications"), env); err != nil {
		t.Fatal(err)
	}
	list, err := os.ReadFile(filepath.Join(config, "mimeapps.list"))
	if err != nil || !strings.Contains(string(list), "x-scheme-handler/djinn=djinn.desktop") {
		t.Fatalf("mimeapps.list: %v\n%s", err, list)
	}
	query := exec.Command("xdg-mime", "query", "default", "x-scheme-handler/djinn")
	query.Env = env
	if out, err := query.Output(); err != nil || strings.TrimSpace(string(out)) != "djinn.desktop" {
		t.Fatalf("xdg-mime query default: %q, %v", out, err)
	}
}

func TestExecArgQuotesAsTheSpecificationSays(t *testing.T) {
	t.Parallel()
	for path, want := range map[string]string{
		"/home/ada/go/bin/djinn": "/home/ada/go/bin/djinn",
		"/home/ada/my bin/djinn": `"/home/ada/my bin/djinn"`,
		`/home/a"b/$x/djinn`:     `"/home/a\\"b/\\$x/djinn"`,
		"/home/ada/100%/djinn":   "/home/ada/100%%/djinn",
	} {
		if got := execArg(path); got != want {
			t.Errorf("execArg(%q) = %s, want %s", path, got, want)
		}
	}
}

func TestScaleKeepsColoursAndTransparency(t *testing.T) {
	t.Parallel()
	img := image.NewNRGBA(image.Rect(0, 0, 100, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			if x < 50 {
				img.SetNRGBA(x, y, color.NRGBA{200, 100, 50, 255})
			}
		}
	}
	out := scale(img, 10)
	if got := out.NRGBAAt(2, 5); got != (color.NRGBA{200, 100, 50, 255}) {
		t.Errorf("an opaque area is %v", got)
	}
	if got := out.NRGBAAt(8, 5); got.A != 0 {
		t.Errorf("a transparent area is %v", got)
	}
}

func decodeSize(t *testing.T, data []byte) int {
	t.Helper()
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width != cfg.Height {
		t.Fatalf("an icon of %dx%d", cfg.Width, cfg.Height)
	}
	return cfg.Width
}

func be32(b []byte) uint32 {
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

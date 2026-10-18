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

// xdgEnv is the test's environment, with the user's folders in data and config, and no desktop session: xdg-mime
// then writes the test's own mimeapps.list, never the user's.
func xdgEnv(data, config string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "XDG_") && !strings.HasPrefix(kv, "KDE_") && !strings.HasPrefix(kv, "GNOME_") &&
			!strings.HasPrefix(kv, "DESKTOP_SESSION=") {
			env = append(env, kv)
		}
	}
	return append(env, "XDG_DATA_HOME="+data, "XDG_CONFIG_HOME="+config, "XDG_DATA_DIRS="+data)
}

// drawing is a small drawing, half opaque: what the icons are made from, cheap to scale (the real one, 1024 px, takes
// seconds under the race detector; TestGen scales it).
func drawing() image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 32; x++ {
			img.SetNRGBA(x, y, color.NRGBA{200, 100, 50, 255})
		}
	}
	return img
}

// smallRoot is a module root whose build/icon.png is drawing.
func smallRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "build"), 0o755); err != nil {
		t.Fatal(err)
	}
	var data bytes.Buffer
	if err := png.Encode(&data, drawing()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, source), data.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestRun: the command line makes the icons or puts them in the user's folders, and says how it is used otherwise.
func TestRun(t *testing.T) {
	t.Parallel()
	const usageLine = "usage: icons gen | icons desktop --exec <djinn binary>\n"
	for _, c := range []struct {
		name         string
		root         string
		args         []string
		code         int
		stdout, errs string
	}{
		{name: "no command", code: 2, errs: usageLine},
		{name: "unknown command", args: []string{"draw"}, code: 2, errs: usageLine},
		{name: "desktop without its binary", args: []string{"desktop"}, code: 2, errs: usageLine},
		{name: "desktop with an unknown flag", args: []string{"desktop", "--icon"}, code: 2, errs: "flag provided but not defined: -icon"},
		{name: "desktop help", args: []string{"desktop", "-h"}, errs: "-exec string"},
		{name: "gen without the drawing", root: t.TempDir(), args: []string{"gen"}, code: 1, errs: "icons: "},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr strings.Builder
			if code := run(c.root, c.args, nil, &stdout, &stderr); code != c.code {
				t.Errorf("exit code %d, want %d; stderr: %s", code, c.code, stderr.String())
			}
			if stdout.String() != c.stdout || !strings.Contains(stderr.String(), c.errs) || (c.errs == "") != (stderr.Len() == 0) {
				t.Errorf("stdout %q, stderr %q; want %q and %q", stdout.String(), stderr.String(), c.stdout, c.errs)
			}
		})
	}
	// On Linux, desktop puts the icon and the entry in $XDG_DATA_HOME, then makes the entry the handler of the links
	// (TestHandleLinks); without xdg-mime it says the links will not open Djinn, and succeeds. Elsewhere desktop
	// writes the user's registry (Windows) or does nothing (macOS): not here.
	t.Run("desktop on Linux", func(t *testing.T) {
		t.Parallel()
		if runtime.GOOS != "linux" {
			t.Skip("desktop writes the user's folders on Linux only")
		}
		data, config := t.TempDir(), t.TempDir()
		var stdout, stderr strings.Builder
		if code := run(smallRoot(t), []string{"desktop", "--exec", "/opt/djinn"}, xdgEnv(data, config), &stdout, &stderr); code != 0 {
			t.Fatalf("exit code %d; stderr: %s", code, stderr.String())
		}
		if want := "Djinn's icon and menu entry are in " + data + ".\n"; stdout.String() != want {
			t.Errorf("stdout %q, want %q", stdout.String(), want)
		}
		if _, err := exec.LookPath("xdg-mime"); (err != nil) != strings.Contains(stderr.String(), "djinn:// links will not open Djinn") {
			t.Errorf("xdg-mime: %v, stderr: %q", err, stderr.String())
		}
		if _, err := os.Stat(filepath.Join(data, "applications", entryFile)); err != nil {
			t.Error(err)
		}
	})
	t.Run("desktop without the drawing", func(t *testing.T) {
		t.Parallel()
		if runtime.GOOS != "linux" {
			t.Skip("desktop writes the user's folders on Linux only")
		}
		var stdout, stderr strings.Builder
		env := xdgEnv(t.TempDir(), t.TempDir())
		if code := run(t.TempDir(), []string{"desktop", "--exec", "/opt/djinn"}, env, &stdout, &stderr); code != 1 ||
			!strings.HasPrefix(stderr.String(), "icons: ") {
			t.Fatalf("exit code %d, stderr %q; want 1 and the error", code, stderr.String())
		}
	})
}

func TestDataHome(t *testing.T) {
	t.Parallel()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(t.TempDir(), "data")
	for _, c := range []struct {
		name    string
		environ []string
		want    string
	}{
		{name: "XDG_DATA_HOME", environ: []string{"HOME=/elsewhere", "XDG_DATA_HOME=" + data}, want: data},
		// The XDG Base Directory Specification ignores a relative path.
		{name: "relative XDG_DATA_HOME", environ: []string{"XDG_DATA_HOME=data"}, want: filepath.Join(home, ".local", "share")},
		{name: "no XDG_DATA_HOME", want: filepath.Join(home, ".local", "share")},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got, err := dataHome(c.environ); err != nil || got != c.want {
				t.Fatalf("dataHome = %q, %v; want %q", got, err, c.want)
			}
		})
	}
}

func TestGen(t *testing.T) {
	t.Parallel()
	// The icons in build/ are the drawing's: run `go run ./tools/icons gen` after changing build/icon.png. It scales
	// the real drawing, 1024 px, to 18 sizes: 0.5 s, and some 20 s under the race detector, which slows the pixel
	// loops of scale forty times; the other tests scale a small drawing.
	t.Run("build icons follow the drawing", func(t *testing.T) {
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
	})
	t.Run("a drawing that is no PNG", func(t *testing.T) {
		t.Parallel()
		tmp := t.TempDir()
		if err := os.MkdirAll(filepath.Join(tmp, "build"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tmp, source), []byte("<svg/>"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := gen(tmp); err == nil {
			t.Fatal("gen took a drawing that is no PNG")
		}
	})
}

func TestMakeICO(t *testing.T) {
	t.Parallel()
	img := drawing()
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
}

func TestMakeICNS(t *testing.T) {
	t.Parallel()
	img := drawing()
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

func TestDesktop(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the desktop entry is for Linux (the command does nothing elsewhere), and its paths are POSIX ones")
	}
	t.Run("puts the icon and the entry in the user's folder", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := desktop(smallRoot(t), dir, "/opt/my tools/djinn"); err != nil {
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
	})
	t.Run("without the drawing", func(t *testing.T) {
		t.Parallel()
		if err := desktop(t.TempDir(), t.TempDir(), "/opt/djinn"); err == nil {
			t.Fatal("desktop without the drawing succeeded")
		}
	})
	t.Run("a data folder that is a file", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(dir, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := desktop(smallRoot(t), dir, "/opt/djinn"); err == nil {
			t.Fatal("desktop into a file succeeded")
		}
	})
}

// TestHandleLinks: xdg-mime makes the entry the handler of djinn:// links, in the mimeapps.list of the user's
// folders, here the test's own.
func TestHandleLinks(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("xdg-mime"); runtime.GOOS != "linux" || err != nil {
		t.Skip("xdg-mime is for Linux, and comes with xdg-utils")
	}
	data, config := t.TempDir(), t.TempDir()
	apps := filepath.Join(data, "applications")
	if err := os.MkdirAll(apps, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(apps, entryFile), []byte(entry("/opt/djinn")), 0o644); err != nil {
		t.Fatal(err)
	}
	env := xdgEnv(data, config)
	if err := handleLinks(apps, env); err != nil {
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

// TestExecArg: the Exec key quotes a path as the Desktop Entry Specification says.
func TestExecArg(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ name, path, want string }{
		{"plain", "/home/ada/go/bin/djinn", "/home/ada/go/bin/djinn"},
		{"space", "/home/ada/my bin/djinn", `"/home/ada/my bin/djinn"`},
		{"quote and dollar", `/home/a"b/$x/djinn`, `"/home/a\\"b/\\$x/djinn"`},
		{"percent", "/home/ada/100%/djinn", "/home/ada/100%%/djinn"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := execArg(c.path); got != c.want {
				t.Errorf("execArg(%q) = %s, want %s", c.path, got, c.want)
			}
		})
	}
}

// TestScale keeps the colours and the transparency.
func TestScale(t *testing.T) {
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

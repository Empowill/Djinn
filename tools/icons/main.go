// Command icons makes Djinn's icon for each system from one drawing, build/icon.png: the logo of the window, which
// the window, the dock and the tray already show (assets.go).
//
//	icons gen                        writes build/icon.ico (Windows), build/icon.icns (macOS) and build/icon-256.png
//	                                 (the window's on Linux: GTK 3 drops a window icon of 512 px or more)
//	icons desktop --exec <djinn>     on Linux, puts the icon and a .desktop file in the user's folders, the handler of
//	                                 djinn:// links; on Windows, registers the links (link.Register)
//
// desktop needs no sudo: it writes $XDG_DATA_HOME (~/.local/share by default), icons/hicolor/<size>/apps/djinn.png
// and applications/djinn.desktop, then makes that entry the user's handler of djinn:// links with xdg-mime. On
// Windows it writes the handler of the links in the user's registry, HKEY_CURRENT_USER. macOS takes the icon and the
// links from the build (Djinn.app's Info.plist).
//
// build/icon.png is build/icon.svg, drawn by hand, rendered at 1024 px; the interface shows the SVG itself
// (src/frame.tsx). After a change to the SVG, render it with any SVG renderer, then run icons gen; with Inkscape
// through a pipe, since its snap reads no hidden folder:
//
//	inkscape --pipe --export-type=png --export-filename=- -w 1024 -h 1024 < build/icon.svg > build/icon.png
package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/empowill/djinn/internal/link"
)

// source is the drawing, relative to the root of the module.
const source = "build/icon.png"

// linuxSizes are the sizes of the hicolor theme a dock, a window switcher or a menu asks for.
var linuxSizes = []int{16, 24, 32, 48, 64, 128, 256, 512}

// icoSizes are the sizes Windows asks an .ico for, from the title bar to a large icon in Explorer.
var icoSizes = []int{16, 24, 32, 48, 64, 128, 256}

// icnsTypes are the PNG entries of an .icns, by type and size.
var icnsTypes = []struct {
	kind string
	size int
}{
	{"icp4", 16}, {"icp5", 32}, {"icp6", 64}, {"ic07", 128}, {"ic08", 256}, {"ic09", 512},
	{"ic10", 1024}, {"ic11", 32}, {"ic12", 64}, {"ic13", 256}, {"ic14", 512},
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "gen":
		err = gen(".")
	case "desktop":
		flags := flag.NewFlagSet("desktop", flag.ExitOnError)
		exe := flags.String("exec", "", "the djinn binary the menu entry starts")
		_ = flags.Parse(os.Args[2:])
		if *exe == "" {
			usage()
		}
		switch runtime.GOOS {
		case "windows":
			// The icon comes with the build; the links are the user's registry's.
			var abs string
			if abs, err = filepath.Abs(*exe); err == nil {
				err = link.Register(abs)
			}
			if err == nil {
				fmt.Printf("djinn:// links open %s.\n", abs)
			}
		case "linux":
			var dir string
			if dir, err = dataHome(); err == nil {
				err = desktop(".", dir, *exe)
			}
			if err == nil {
				fmt.Printf("Djinn's icon and menu entry are in %s.\n", dir)
				if err := handleLinks(filepath.Join(dir, "applications"), os.Environ()); err != nil {
					fmt.Fprintf(os.Stderr, "icons: djinn:// links will not open Djinn: %v\n", err)
				}
				err = nil
			}
		}
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "icons:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: icons gen | icons desktop --exec <djinn binary>")
	os.Exit(2)
}

// dataHome is $XDG_DATA_HOME, or ~/.local/share.
func dataHome() (string, error) {
	if dir := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(dir) {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share"), nil
}

func load(root string) (image.Image, error) {
	f, err := os.Open(filepath.Join(root, source))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return png.Decode(f)
}

// gen writes build/icon.ico, build/icon.icns and build/icon-256.png from the drawing.
func gen(root string) error {
	img, err := load(root)
	if err != nil {
		return err
	}
	small, err := pngAt(img, 256)
	if err != nil {
		return err
	}
	ico, err := makeICO(img)
	if err != nil {
		return err
	}
	icns, err := makeICNS(img)
	if err != nil {
		return err
	}
	return errors.Join(
		os.WriteFile(filepath.Join(root, "build", "icon.ico"), ico, 0o644),
		os.WriteFile(filepath.Join(root, "build", "icon.icns"), icns, 0o644),
		os.WriteFile(filepath.Join(root, "build", "icon-256.png"), small, 0o644),
	)
}

// desktop puts the icon at each size and the menu entry under dir, the user's data folder.
func desktop(root, dir, exe string) error {
	img, err := load(root)
	if err != nil {
		return err
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		return err
	}
	theme := filepath.Join(dir, "icons", "hicolor")
	for _, size := range linuxSizes {
		data, err := pngAt(img, size)
		if err != nil {
			return err
		}
		apps := filepath.Join(theme, fmt.Sprintf("%dx%d", size, size), "apps")
		if err := os.MkdirAll(apps, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(apps, "djinn.png"), data, 0o644); err != nil {
			return err
		}
	}
	// A theme folder with a cache is read from the cache only: a new icon stays unseen until it is rebuilt.
	if _, err := os.Stat(filepath.Join(theme, "icon-theme.cache")); err == nil {
		if tool, err := exec.LookPath("gtk-update-icon-cache"); err == nil {
			_ = exec.Command(tool, "--force", "--quiet", "--ignore-theme-index", theme).Run()
		}
	}
	apps := filepath.Join(dir, "applications")
	if err := os.MkdirAll(apps, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(apps, entryFile), []byte(entry(exe)), 0o644)
}

// handleLinks makes the entry djinn.desktop the user's handler of djinn:// links, as env says where the user's folders
// are: xdg-mime writes it in the user's mimeapps.list (no sudo), and update-desktop-database refreshes the cache of
// the types the entries in apps take, when it is installed.
func handleLinks(apps string, env []string) error {
	xdgMime, err := exec.LookPath("xdg-mime")
	if err != nil {
		return fmt.Errorf("xdg-mime (xdg-utils) is not installed: %w", err)
	}
	if tool, err := exec.LookPath("update-desktop-database"); err == nil {
		cmd := exec.Command(tool, "--quiet", apps)
		cmd.Env = env
		_ = cmd.Run()
	}
	cmd := exec.Command(xdgMime, "default", entryFile, linkType)
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("xdg-mime default: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// entryFile is the name of Djinn's menu entry, in the applications folder.
const entryFile = "djinn.desktop"

// linkType is the type of the djinn:// links for the desktop: the entry that takes it opens them.
const linkType = "x-scheme-handler/" + link.Scheme

// entry is the menu entry that starts exe, and that the desktop runs for a djinn:// link: djinn open <link>, which
// hands it to the running Djinn; from the menu, with no link, djinn open is djinn up. StartupWMClass is the window's WM_CLASS, and its Wayland app_id: the
// program name the window sets (Linux.ProgramName in cmd/djinn/window.go). The dock matches the window to the entry
// by it, and shows the entry's icon.
func entry(exe string) string {
	return "[Desktop Entry]\n" +
		"Type=Application\n" +
		"Name=Djinn\n" +
		"Comment=A wisp of smoke to work your will\n" +
		"Exec=" + execArg(exe) + " open %u\n" +
		"Icon=djinn\n" +
		"Terminal=false\n" +
		"Categories=Development;\n" +
		"StartupWMClass=djinn\n" +
		"MimeType=" + linkType + ";\n"
}

// execArg quotes a path for the Exec key, as the Desktop Entry Specification says: in double quotes when it holds a
// reserved character, with ", `, $ and \ escaped; then every \ doubled, as in any string value; and % doubled.
func execArg(path string) string {
	if strings.ContainsAny(path, " \t\n\"'\\><~|&;$*?#()`") {
		var b strings.Builder
		b.WriteByte('"')
		for _, r := range path {
			if strings.ContainsRune("\"`$\\", r) {
				b.WriteByte('\\')
			}
			b.WriteRune(r)
		}
		b.WriteByte('"')
		path = strings.ReplaceAll(b.String(), `\`, `\\`)
	}
	return strings.ReplaceAll(path, "%", "%%")
}

// pngAt encodes the drawing at size × size.
func pngAt(img image.Image, size int) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, scale(img, size)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// pngsAt encodes the drawing at each of sizes, side by side.
func pngsAt(img image.Image, sizes []int) ([][]byte, error) {
	out := make([][]byte, len(sizes))
	errs := make([]error, len(sizes))
	var wg sync.WaitGroup
	for i, size := range sizes {
		wg.Go(func() { out[i], errs[i] = pngAt(img, size) })
	}
	wg.Wait()
	return out, errors.Join(errs...)
}

// scale shrinks img to size × size by averaging the area each pixel covers, in premultiplied colour: sharp at
// every size, with no halo on the transparent corners.
func scale(img image.Image, size int) *image.NRGBA {
	b := img.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, size, size))
	fx := float64(b.Dx()) / float64(size)
	fy := float64(b.Dy()) / float64(size)
	// The same premultiplied colour as At, without a value boxed per pixel: the drawing is large.
	at := func(x, y int) (r, g, b, a uint32) { return img.At(x, y).RGBA() }
	if fast, ok := img.(image.RGBA64Image); ok {
		at = func(x, y int) (r, g, b, a uint32) {
			c := fast.RGBA64At(x, y)
			return uint32(c.R), uint32(c.G), uint32(c.B), uint32(c.A)
		}
	}
	for y := 0; y < size; y++ {
		y0, y1 := float64(y)*fy, float64(y+1)*fy
		for x := 0; x < size; x++ {
			x0, x1 := float64(x)*fx, float64(x+1)*fx
			var r, g, bl, a, w float64
			for sy := int(y0); float64(sy) < y1 && sy < b.Dy(); sy++ {
				wy := min(y1, float64(sy+1)) - max(y0, float64(sy))
				for sx := int(x0); float64(sx) < x1 && sx < b.Dx(); sx++ {
					wx := min(x1, float64(sx+1)) - max(x0, float64(sx))
					cr, cg, cb, ca := at(b.Min.X+sx, b.Min.Y+sy) // premultiplied, 16 bits
					k := wx * wy
					r, g, bl, a, w = r+k*float64(cr), g+k*float64(cg), bl+k*float64(cb), a+k*float64(ca), w+k
				}
			}
			if a == 0 {
				continue
			}
			unmul := func(c float64) uint8 { return uint8(min(255, c/a*255+0.5)) }
			out.SetNRGBA(x, y, color.NRGBA{unmul(r), unmul(g), unmul(bl), uint8(a/w/257 + 0.5)})
		}
	}
	return out
}

// makeICO writes a Windows icon whose entries are PNG images (Windows Vista and later).
func makeICO(img image.Image) ([]byte, error) {
	var head, body bytes.Buffer
	_ = binary.Write(&head, binary.LittleEndian, [3]uint16{0, 1, uint16(len(icoSizes))})
	offset := 6 + 16*len(icoSizes)
	pngs, err := pngsAt(img, icoSizes)
	if err != nil {
		return nil, err
	}
	for i, size := range icoSizes {
		data := pngs[i]
		side := uint8(size % 256) // 0 means 256
		_ = binary.Write(&head, binary.LittleEndian, struct {
			Width, Height, Colors, Reserved uint8
			Planes, Bits                    uint16
			Size, Offset                    uint32
		}{side, side, 0, 0, 1, 32, uint32(len(data)), uint32(offset + body.Len())})
		body.Write(data)
	}
	return append(head.Bytes(), body.Bytes()...), nil
}

// makeICNS writes a macOS icon whose entries are PNG images (macOS 10.7 and later).
func makeICNS(img image.Image) ([]byte, error) {
	var body bytes.Buffer
	sizes := make([]int, len(icnsTypes))
	for i, t := range icnsTypes {
		sizes[i] = t.size
	}
	pngs, err := pngsAt(img, sizes)
	if err != nil {
		return nil, err
	}
	for i, t := range icnsTypes {
		data := pngs[i]
		body.WriteString(t.kind)
		_ = binary.Write(&body, binary.BigEndian, uint32(8+len(data)))
		body.Write(data)
	}
	var out bytes.Buffer
	out.WriteString("icns")
	_ = binary.Write(&out, binary.BigEndian, uint32(8+body.Len()))
	out.Write(body.Bytes())
	return out.Bytes(), nil
}

// Command macapp lays out Djinn.app, the macOS application bundle, around a djinn binary built for macOS, and zips
// it. It is a build tool for `task release-macos-app`, portable: it runs and is tested on Linux too.
//
//	go run ./tools/macapp bundle --binary <djinn> --version v0.1.0 bin/release/.app   # → bin/release/.app/Djinn.app
//	go run ./tools/macapp zip bin/release/.app/Djinn.app bin/release/djinn_darwin_universal_app.zip
//
// The bundle gives macOS what a bare binary lacks: an identifier, which system notifications need, a name and an icon
// for Finder, Launchpad and the Dock. Its layout:
//
//	Djinn.app/Contents/Info.plist          the identifier, the version from the tag, the icon's name, the djinn:// links
//	Djinn.app/Contents/PkgInfo
//	Djinn.app/Contents/MacOS/djinn         the binary, the same as in the release's .tar.gz, and the bundle's executable
//	Djinn.app/Contents/Resources/djinn.icns    build/icon.icns, made from the logo by tools/icons
//	Djinn.app/Contents/Resources/LICENSE, NOTICE, THIRD_PARTY_NOTICES.md
//
// Finder starts the app with no arguments: djinn knows it runs from a bundle by its path, runs up, and takes the PATH
// of the user's login shell (cmd/djinn/bundle.go). No launcher script stands before it: one binary to sign.
//
// The zip holds Djinn.app at its root: Finder unzips it into the app, ready to drag into Applications.
package main

import (
	"archive/zip"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"golang.org/x/mod/semver"
)

const (
	// AppName is the bundle's folder and the name macOS shows.
	AppName = "Djinn.app"
	// Identifier names the app to macOS: notifications, their permission, and the app's settings follow it. It
	// follows the module path. Changing it once released makes macOS ask for the notifications again.
	Identifier = "io.github.empowill.djinn"
	// Binary is djinn itself, the bundle's executable. cmd/djinn knows the bundle by this name.
	Binary = "djinn"
	// Icon is the icon's file in Resources.
	Icon = "djinn.icns"
	// MinSystem is the oldest macOS the app runs on: Go 1.26 asks for macOS 12, as MACOSX_DEPLOYMENT_TARGET does in
	// the release-build-macos task.
	MinSystem = "12.0"
)

// iconSource is the macOS icon, relative to the root of the module; tools/icons makes it from build/icon.png.
const iconSource = "build/icon.icns"

// notices travel with the app, as with every archive of the release (tools/releasepack).
var notices = []string{"LICENSE", "NOTICE", "THIRD_PARTY_NOTICES.md"}

func main() {
	var err error
	switch {
	case len(os.Args) > 1 && os.Args[1] == "bundle":
		flags := flag.NewFlagSet("bundle", flag.ExitOnError)
		binary := flags.String("binary", "", "the djinn binary built for macOS")
		version := flags.String("version", "", "the release's tag, such as v0.1.0")
		root := flags.String("root", ".", "the root of the module, where build/ and the notices are")
		_ = flags.Parse(os.Args[2:])
		if *binary == "" || *version == "" || flags.NArg() != 1 {
			usage()
		}
		var app string
		app, err = bundle(*root, *binary, *version, flags.Arg(0))
		if err == nil {
			fmt.Println(app)
		}
	case len(os.Args) == 4 && os.Args[1] == "zip":
		err = zipApp(os.Args[2], os.Args[3])
		if err == nil {
			fmt.Println(os.Args[3])
		}
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "macapp:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: macapp bundle --binary <djinn> --version <vX.Y.Z> <folder> | macapp zip <Djinn.app> <file.zip>")
	os.Exit(2)
}

// bundleVersion is the version macOS reads from a tag: three numbers, with no v and no pre-release.
func bundleVersion(tag string) (string, error) {
	if !semver.IsValid(tag) || semver.Build(tag) != "" {
		return "", fmt.Errorf("version %q is not a release tag such as v0.1.0", tag)
	}
	canonical := semver.Canonical(tag) // v1.2 → v1.2.0
	return strings.TrimPrefix(strings.TrimSuffix(canonical, semver.Prerelease(tag)), "v"), nil
}

// file is a file of the bundle: data, or a copy of src when data is nil.
type file struct {
	dst, src string
	data     []byte
	mode     fs.FileMode
}

// bundle lays out dir/Djinn.app around binary, from the icon and the notices found in root. A bundle already there
// is replaced. It returns the bundle's path.
func bundle(root, binary, tag, dir string) (string, error) {
	version, err := bundleVersion(tag)
	if err != nil {
		return "", err
	}
	app := filepath.Join(dir, AppName)
	if err := os.RemoveAll(app); err != nil {
		return "", err
	}
	contents := filepath.Join(app, "Contents")
	macos := filepath.Join(contents, "MacOS")
	resources := filepath.Join(contents, "Resources")
	for _, d := range []string{macos, resources} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return "", err
		}
	}
	files := []file{
		{dst: filepath.Join(contents, "Info.plist"), data: []byte(infoPlist(version)), mode: 0o644},
		{dst: filepath.Join(contents, "PkgInfo"), data: []byte("APPL????"), mode: 0o644},
		{dst: filepath.Join(macos, Binary), src: binary, mode: 0o755},
		{dst: filepath.Join(resources, Icon), src: filepath.Join(root, filepath.FromSlash(iconSource)), mode: 0o644},
	}
	for _, name := range notices {
		files = append(files, file{dst: filepath.Join(resources, name), src: filepath.Join(root, name), mode: 0o644})
	}
	for _, f := range files {
		data := f.data
		if data == nil {
			if data, err = os.ReadFile(f.src); err != nil {
				return "", err
			}
		}
		if err := os.WriteFile(f.dst, data, f.mode); err != nil {
			return "", err
		}
		// WriteFile's mode goes through the umask, and keeps an older file's: set it.
		if err := os.Chmod(f.dst, f.mode); err != nil {
			return "", err
		}
	}
	return app, nil
}

// plistEntry is a key of a property list and its value: a string, a bool, a []string, or a []plistEntries.
type plistEntry struct {
	key   string
	value any
}

// plistEntries is a dictionary of a property list, its keys in order.
type plistEntries []plistEntry

// URLScheme is the scheme of Djinn's links, djinn://tilasm/<id>: macOS opens Djinn.app for one clicked anywhere, and
// sends it the link (cmd/djinn/window.go).
const URLScheme = "djinn"

// plistKeys are the keys of Info.plist, in order.
func plistKeys(version string) plistEntries {
	return plistEntries{
		{"CFBundleDevelopmentRegion", "en"},
		{"CFBundleDisplayName", "Djinn"},
		{"CFBundleExecutable", Binary},
		{"CFBundleIconFile", Icon},
		{"CFBundleIdentifier", Identifier},
		{"CFBundleInfoDictionaryVersion", "6.0"},
		{"CFBundleName", "Djinn"},
		{"CFBundlePackageType", "APPL"},
		{"CFBundleShortVersionString", version},
		{"CFBundleSignature", "????"},
		{"CFBundleURLTypes", []plistEntries{{
			{"CFBundleTypeRole", "Viewer"},
			{"CFBundleURLName", Identifier},
			{"CFBundleURLSchemes", []string{URLScheme}},
		}}},
		{"CFBundleVersion", version},
		{"LSApplicationCategoryType", "public.app-category.developer-tools"},
		{"LSMinimumSystemVersion", MinSystem},
		{"NSHighResolutionCapable", true},
	}
}

// infoPlist is Contents/Info.plist, in Apple's XML property list format.
func infoPlist(version string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
`)
	writeDict(&b, plistKeys(version), "\t")
	b.WriteString("</dict>\n</plist>\n")
	return b.String()
}

// writeDict writes the entries of a dictionary, each line after indent.
func writeDict(b *strings.Builder, d plistEntries, indent string) {
	for _, e := range d {
		fmt.Fprintf(b, "%s<key>%s</key>\n", indent, escape(e.key))
		writeValue(b, e.value, indent)
	}
}

func writeValue(b *strings.Builder, v any, indent string) {
	switch v := v.(type) {
	case bool:
		fmt.Fprintf(b, "%s<%t/>\n", indent, v)
	case string:
		fmt.Fprintf(b, "%s<string>%s</string>\n", indent, escape(v))
	case []string:
		fmt.Fprintf(b, "%s<array>\n", indent)
		for _, s := range v {
			writeValue(b, s, indent+"\t")
		}
		fmt.Fprintf(b, "%s</array>\n", indent)
	case []plistEntries:
		fmt.Fprintf(b, "%s<array>\n", indent)
		for _, d := range v {
			fmt.Fprintf(b, "%s\t<dict>\n", indent)
			writeDict(b, d, indent+"\t\t")
			fmt.Fprintf(b, "%s\t</dict>\n", indent)
		}
		fmt.Fprintf(b, "%s</array>\n", indent)
	default:
		panic(fmt.Sprintf("Info.plist: a value of type %T", v))
	}
}

func escape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// zipApp writes the bundle at app into the zip file out, under its own name, with its folders and the executable
// bit of what Contents/MacOS holds, whatever the file system that built it (Windows has none). A failure leaves no
// half-written file.
//
// A signed bundle keeps part of its signature in extended attributes, which this zip drops: once the release signs
// the app, it zips it with `ditto -c -k --keepParent` instead.
func zipApp(app, out string) error {
	app = filepath.Clean(app)
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(f)
	err = filepath.WalkDir(app, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(filepath.Dir(app), p)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		hdr := &zip.FileHeader{Name: name, Modified: info.ModTime()}
		switch {
		case d.IsDir():
			hdr.Name += "/"
			hdr.SetMode(fs.ModeDir | 0o755)
			_, err = zw.CreateHeader(hdr)
			return err
		case !d.Type().IsRegular():
			return fmt.Errorf("%s is not a regular file", p)
		}
		hdr.Method = zip.Deflate
		hdr.SetMode(fileMode(name))
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		return copyInto(w, p)
	})
	if err = errors.Join(err, zw.Close(), f.Close()); err != nil {
		_ = os.Remove(out)
		return err
	}
	return nil
}

// fileMode is the mode of a file of the bundle, by its name in the zip: what Contents/MacOS holds runs.
func fileMode(name string) fs.FileMode {
	if path.Base(path.Dir(name)) == "MacOS" {
		return 0o755
	}
	return 0o644
}

func copyInto(w io.Writer, p string) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}

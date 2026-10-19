package main

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

const root = "../.."

// fakeBinary writes a stand-in for djinn and returns its path.
func fakeBinary(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "djinn")
	if err := os.WriteFile(p, []byte("a macOS binary"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// plistDict reads the top dictionary of an XML property list into its keys and values; a boolean reads "true" or
// "false". It fails on a property list that is not well formed.
func plistDict(t *testing.T, data []byte) (keys []string, values map[string]string) {
	t.Helper()
	var doc struct {
		XMLName xml.Name `xml:"plist"`
		Version string   `xml:"version,attr"`
		Dict    struct {
			Items []struct {
				XMLName xml.Name
				Text    string `xml:",chardata"`
			} `xml:",any"`
		} `xml:"dict"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("Info.plist is not well formed: %v", err)
	}
	if doc.Version != "1.0" {
		t.Fatalf("plist version %q", doc.Version)
	}
	values = map[string]string{}
	items := doc.Dict.Items
	if len(items)%2 != 0 {
		t.Fatalf("Info.plist has a key without a value")
	}
	for i := 0; i < len(items); i += 2 {
		k, v := items[i], items[i+1]
		if k.XMLName.Local != "key" {
			t.Fatalf("item %d is a <%s>, want a <key>", i, k.XMLName.Local)
		}
		switch v.XMLName.Local {
		case "string":
			values[k.Text] = v.Text
		case "true", "false", "array":
			values[k.Text] = v.XMLName.Local
		default:
			t.Fatalf("%s holds a <%s>", k.Text, v.XMLName.Local)
		}
		keys = append(keys, k.Text)
	}
	return keys, values
}

// TestRun: the command line bundles or zips the app, and says how it is used otherwise.
func TestRun(t *testing.T) {
	t.Parallel()
	binary := fakeBinary(t)
	dir := t.TempDir()
	app := filepath.Join(dir, AppName)
	out := filepath.Join(dir, "app.zip")
	const usageLine = "usage: macapp bundle --binary <djinn> --version <vX.Y.Z> <folder> | macapp zip <Djinn.app> <file.zip>\n"
	// In order, one after the other: zip takes the app that bundle made, and the zip of a missing app removes the zip
	// it started, which zip wrote before.
	for _, c := range []struct {
		name         string
		args         []string
		code         int
		stdout, errs string
	}{
		{name: "no command", code: 2, errs: usageLine},
		{name: "unknown command", args: []string{"sign", app}, code: 2, errs: usageLine},
		{name: "bundle without a version", args: []string{"bundle", "--binary", binary, dir}, code: 2, errs: usageLine},
		{name: "bundle without a folder", args: []string{"bundle", "--binary", binary, "--version", "v0.1.0"}, code: 2, errs: usageLine},
		{name: "bundle with an unknown flag", args: []string{"bundle", "--sign"}, code: 2, errs: "flag provided but not defined: -sign"},
		{name: "bundle help", args: []string{"bundle", "-h"}, errs: "-binary string"},
		{name: "bundle a version that is no tag", args: []string{"bundle", "--binary", binary, "--version", "dev", "--root", root, dir},
			code: 1, errs: `macapp: version "dev" is not a release tag such as v0.1.0`},
		{name: "bundle", args: []string{"bundle", "--binary", binary, "--version", "v0.1.0", "--root", root, dir}, stdout: app + "\n"},
		{name: "zip without its file", args: []string{"zip", app}, code: 2, errs: usageLine},
		{name: "zip", args: []string{"zip", app, out}, stdout: out + "\n"},
		{name: "zip a missing app", args: []string{"zip", filepath.Join(dir, "none.app"), out}, code: 1, errs: "macapp: "},
	} {
		t.Run(c.name, func(t *testing.T) {
			var stdout, stderr strings.Builder
			if code := run(c.args, &stdout, &stderr); code != c.code {
				t.Errorf("exit code %d, want %d; stderr: %s", code, c.code, stderr.String())
			}
			if stdout.String() != c.stdout || !strings.Contains(stderr.String(), c.errs) || (c.errs == "") != (stderr.Len() == 0) {
				t.Errorf("stdout %q, stderr %q; want %q and %q", stdout.String(), stderr.String(), c.stdout, c.errs)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(app, "Contents", "Info.plist")); err != nil {
		t.Errorf("bundle made no app: %v", err)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("a failed zip left %s: %v", out, err)
	}
}

func TestBundleVersion(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ tag, want string }{
		{"v0.1.0", "0.1.0"},
		{"v1.2.3-rc.1", "1.2.3"},
		{"v0.0.0-dryrun", "0.0.0"},
		{"v2.0", "2.0.0"},
		// Refused: the empty want.
		{"", ""},
		{"dev", ""},
		{"0.1.0", ""},
		{"v1.2.3+meta", ""},
		{"local-abc1234", ""},
	} {
		t.Run(c.tag, func(t *testing.T) {
			t.Parallel()
			got, err := bundleVersion(c.tag)
			if c.want == "" {
				if err == nil {
					t.Fatalf("bundleVersion(%q) took it: %q", c.tag, got)
				}
				return
			}
			if err != nil || got != c.want {
				t.Fatalf("bundleVersion(%q) = %q, %v; want %q", c.tag, got, err, c.want)
			}
		})
	}
}

func TestBundle(t *testing.T) {
	t.Parallel()
	t.Run("layout", func(t *testing.T) {
		t.Parallel()
		binary := fakeBinary(t)
		dir := t.TempDir()
		app, err := bundle(root, binary, "v1.2.3-rc.1", dir)
		if err != nil {
			t.Fatal(err)
		}
		if app != filepath.Join(dir, "Djinn.app") {
			t.Fatalf("bundle at %s", app)
		}
		var got []string
		err = filepath.WalkDir(app, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				rel, _ := filepath.Rel(app, p)
				got = append(got, filepath.ToSlash(rel))
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		want := []string{
			"Contents/Info.plist", "Contents/MacOS/djinn", "Contents/PkgInfo",
			"Contents/Resources/LICENSE", "Contents/Resources/NOTICE", "Contents/Resources/THIRD_PARTY_NOTICES.md",
			"Contents/Resources/djinn.icns",
		}
		if !slices.Equal(got, want) {
			t.Fatalf("bundle holds %v, want %v", got, want)
		}

		read := func(rel string) []byte {
			data, err := os.ReadFile(filepath.Join(app, filepath.FromSlash(rel)))
			if err != nil {
				t.Fatal(err)
			}
			return data
		}
		if !bytes.Equal(read("Contents/MacOS/djinn"), []byte("a macOS binary")) {
			t.Error("Contents/MacOS/djinn is not the binary given")
		}
		icon, err := os.ReadFile(filepath.Join(root, "build", "icon.icns"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(read("Contents/Resources/djinn.icns"), icon) || string(icon[:4]) != "icns" {
			t.Error("the icon is not build/icon.icns")
		}
		if string(read("Contents/PkgInfo")) != "APPL????" {
			t.Error("PkgInfo")
		}
		if runtime.GOOS != "windows" {
			info, err := os.Stat(filepath.Join(app, "Contents", "MacOS", "djinn"))
			if err != nil || info.Mode().Perm() != 0o755 {
				t.Errorf("Contents/MacOS/djinn is not executable: %v", info.Mode())
			}
		}
		// Info.plist gives the version without its pre-release (TestInfoPlist reads the rest); what it names is in
		// the bundle.
		plistData := read("Contents/Info.plist")
		if string(plistData) != infoPlist("1.2.3") {
			t.Error("Contents/Info.plist is not infoPlist(1.2.3)")
		}
		_, plist := plistDict(t, plistData)
		if _, err := os.Stat(filepath.Join(app, "Contents", "MacOS", plist["CFBundleExecutable"])); err != nil {
			t.Error("CFBundleExecutable names no file of Contents/MacOS")
		}
		if _, err := os.Stat(filepath.Join(app, "Contents", "Resources", plist["CFBundleIconFile"])); err != nil {
			t.Error("CFBundleIconFile names no file of Contents/Resources")
		}
	})
	// A second bundle replaces the first, leaving nothing of it.
	t.Run("replaces the bundle there", func(t *testing.T) {
		t.Parallel()
		binary := fakeBinary(t)
		dir := t.TempDir()
		app, err := bundle(root, binary, "v1.2.3", dir)
		if err != nil {
			t.Fatal(err)
		}
		stray := filepath.Join(app, "Contents", "stray")
		if err := os.WriteFile(stray, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := bundle(root, binary, "v1.2.4", dir); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(stray); !os.IsNotExist(err) {
			t.Error("a second bundle kept a file of the first")
		}
	})
	// macOS ignores case by default (APFS): two names of one folder that differ only by case would be one file.
	t.Run("names differ beyond case", func(t *testing.T) {
		t.Parallel()
		app, err := bundle(root, fakeBinary(t), "v0.1.0", t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		err = filepath.WalkDir(app, func(p string, d fs.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return err
			}
			entries, err := os.ReadDir(p)
			if err != nil {
				return err
			}
			seen := map[string]string{}
			for _, e := range entries {
				if other, ok := seen[strings.ToLower(e.Name())]; ok {
					t.Errorf("%s holds %s and %s", p, other, e.Name())
				}
				seen[strings.ToLower(e.Name())] = e.Name()
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	})
	t.Run("refuses what is missing", func(t *testing.T) {
		t.Parallel()
		for _, c := range []struct {
			name, root, binary, version string
		}{
			{name: "a missing binary", root: root, binary: filepath.Join(t.TempDir(), "none"), version: "v0.1.0"},
			{name: "a root without the icon and the notices", root: t.TempDir(), binary: fakeBinary(t), version: "v0.1.0"},
			{name: "a version that is no tag", root: root, binary: fakeBinary(t), version: "dev"},
		} {
			if _, err := bundle(c.root, c.binary, c.version, t.TempDir()); err == nil {
				t.Errorf("%s made a bundle", c.name)
			}
		}
	})
}

// TestInfoPlist: Info.plist is a well formed property list, its keys sorted, that names the app, its version, its
// icon, and the djinn:// links it opens.
func TestInfoPlist(t *testing.T) {
	t.Parallel()
	data := []byte(infoPlist("1.2.3"))
	keys, plist := plistDict(t, data)
	for key, value := range map[string]string{
		"CFBundleIdentifier":         "io.github.empowill.djinn",
		"CFBundleExecutable":         "djinn", // djinn itself, no launcher: cmd/djinn knows the bundle by this path.
		"CFBundleName":               "Djinn",
		"CFBundlePackageType":        "APPL",
		"CFBundleIconFile":           "djinn.icns",
		"CFBundleShortVersionString": "1.2.3",
		"CFBundleVersion":            "1.2.3",
		"LSMinimumSystemVersion":     "12.0",
		"NSHighResolutionCapable":    "true",
	} {
		if plist[key] != value {
			t.Errorf("Info.plist %s = %q, want %q", key, plist[key], value)
		}
	}
	if !slices.IsSorted(keys) {
		t.Errorf("Info.plist keys are not sorted: %v", keys)
	}
	// djinn:// links open the app: one URL type, the scheme djinn.
	var types struct {
		Dicts []struct {
			Items []struct {
				XMLName xml.Name
				Text    string   `xml:",chardata"`
				Strings []string `xml:"string"`
			} `xml:",any"`
		} `xml:"dict>array>dict"`
	}
	if err := xml.Unmarshal(data, &types); err != nil || plist["CFBundleURLTypes"] != "array" || len(types.Dicts) != 1 {
		t.Fatalf("CFBundleURLTypes: %v, %d types", err, len(types.Dicts))
	}
	urlType := map[string]string{}
	items := types.Dicts[0].Items
	for i := 0; i+1 < len(items); i += 2 {
		urlType[items[i].Text] = items[i+1].Text
		if items[i+1].XMLName.Local == "array" {
			urlType[items[i].Text] = strings.Join(items[i+1].Strings, ",")
		}
	}
	if urlType["CFBundleURLSchemes"] != "djinn" || urlType["CFBundleURLName"] != "io.github.empowill.djinn" ||
		urlType["CFBundleTypeRole"] != "Viewer" {
		t.Errorf("CFBundleURLTypes = %v", urlType)
	}
}

// TestWriteValue: a value of a type a property list cannot hold is a mistake of plistKeys.
func TestWriteValue(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Error("writeValue took an int")
		}
	}()
	writeValue(&strings.Builder{}, 1, "")
}

func TestZipApp(t *testing.T) {
	t.Parallel()
	t.Run("the bundle under its name", func(t *testing.T) {
		t.Parallel()
		app, err := bundle(root, fakeBinary(t), "v0.1.0", t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(t.TempDir(), "djinn_darwin_universal_app.zip")
		if err := zipApp(app, out); err != nil {
			t.Fatal(err)
		}
		zr, err := zip.OpenReader(out)
		if err != nil {
			t.Fatal(err)
		}
		defer zr.Close()
		modes := map[string]fs.FileMode{}
		for _, f := range zr.File {
			if !strings.HasPrefix(f.Name, "Djinn.app/") {
				t.Errorf("%s is outside Djinn.app/", f.Name)
			}
			modes[f.Name] = f.Mode()
			if f.FileInfo().IsDir() {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(filepath.Join(filepath.Dir(app), filepath.FromSlash(f.Name)))
			if err != nil || !bytes.Equal(got, want) {
				t.Errorf("%s differs from the bundle's file", f.Name)
			}
		}
		for name, want := range map[string]fs.FileMode{
			"Djinn.app/":                          fs.ModeDir | 0o755,
			"Djinn.app/Contents/MacOS/":           fs.ModeDir | 0o755,
			"Djinn.app/Contents/MacOS/djinn":      0o755,
			"Djinn.app/Contents/Info.plist":       0o644,
			"Djinn.app/Contents/Resources/NOTICE": 0o644,
		} {
			if got, ok := modes[name]; !ok || got != want {
				t.Errorf("%s: mode %v (in the zip: %v), want %v", name, got, ok, want)
			}
		}
		if len(modes) != 11 { // 4 folders and 7 files
			t.Errorf("the zip holds %d entries, want 11: %v", len(modes), modes)
		}
	})
	// A failure leaves no half-written zip.
	t.Run("refuses a link", func(t *testing.T) {
		t.Parallel()
		app := filepath.Join(t.TempDir(), AppName)
		if err := os.MkdirAll(filepath.Join(app, "Contents"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("elsewhere", filepath.Join(app, "Contents", "link")); err != nil {
			t.Skipf("no symbolic link here: %v", err)
		}
		out := filepath.Join(t.TempDir(), "app.zip")
		if err := zipApp(app, out); err == nil || !strings.Contains(err.Error(), "is not a regular file") {
			t.Fatalf("zipApp of a link: %v", err)
		}
		if _, err := os.Stat(out); !os.IsNotExist(err) {
			t.Fatalf("a failed zip left %s", out)
		}
	})
	t.Run("cannot write the zip", func(t *testing.T) {
		t.Parallel()
		if err := zipApp(t.TempDir(), filepath.Join(t.TempDir(), "none", "app.zip")); err == nil {
			t.Fatal("zipApp into a missing folder succeeded")
		}
	})
}

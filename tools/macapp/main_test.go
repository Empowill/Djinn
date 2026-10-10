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

func TestBundleVersion(t *testing.T) {
	for tag, want := range map[string]string{
		"v0.1.0":        "0.1.0",
		"v1.2.3-rc.1":   "1.2.3",
		"v0.0.0-dryrun": "0.0.0",
		"v2.0":          "2.0.0",
	} {
		got, err := bundleVersion(tag)
		if err != nil || got != want {
			t.Errorf("bundleVersion(%q) = %q, %v; want %q", tag, got, err, want)
		}
	}
	for _, tag := range []string{"", "dev", "0.1.0", "v1.2.3+meta", "local-abc1234"} {
		if _, err := bundleVersion(tag); err == nil {
			t.Errorf("bundleVersion(%q) took it", tag)
		}
	}
}

func TestBundleLayout(t *testing.T) {
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

	keys, plist := plistDict(t, read("Contents/Info.plist"))
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
	if err := xml.Unmarshal(read("Contents/Info.plist"), &types); err != nil || plist["CFBundleURLTypes"] != "array" ||
		len(types.Dicts) != 1 {
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
	// What Info.plist names is in the bundle.
	if _, err := os.Stat(filepath.Join(app, "Contents", "MacOS", plist["CFBundleExecutable"])); err != nil {
		t.Error("CFBundleExecutable names no file of Contents/MacOS")
	}
	if _, err := os.Stat(filepath.Join(app, "Contents", "Resources", plist["CFBundleIconFile"])); err != nil {
		t.Error("CFBundleIconFile names no file of Contents/Resources")
	}

	// A second bundle replaces the first, leaving nothing of it.
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
}

// macOS ignores case by default (APFS): two names of one folder that differ only by case would be one file.
func TestBundleNamesDifferBeyondCase(t *testing.T) {
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
}

func TestBundleRefusesWhatIsMissing(t *testing.T) {
	if _, err := bundle(root, filepath.Join(t.TempDir(), "none"), "v0.1.0", t.TempDir()); err == nil {
		t.Error("a missing binary made a bundle")
	}
	if _, err := bundle(t.TempDir(), fakeBinary(t), "v0.1.0", t.TempDir()); err == nil {
		t.Error("a root without the icon and the notices made a bundle")
	}
	if _, err := bundle(root, fakeBinary(t), "dev", t.TempDir()); err == nil {
		t.Error("a version that is no tag made a bundle")
	}
}

func TestZipApp(t *testing.T) {
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
}

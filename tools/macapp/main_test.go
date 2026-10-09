package main

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
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
		case "true", "false":
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
		"Contents/Info.plist", "Contents/MacOS/djinn", "Contents/MacOS/djinn-app", "Contents/PkgInfo",
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
		for _, exe := range []string{"djinn", "djinn-app"} {
			info, err := os.Stat(filepath.Join(app, "Contents", "MacOS", exe))
			if err != nil || info.Mode().Perm() != 0o755 {
				t.Errorf("Contents/MacOS/%s is not executable: %v", exe, info.Mode())
			}
		}
	}

	keys, plist := plistDict(t, read("Contents/Info.plist"))
	for key, value := range map[string]string{
		"CFBundleIdentifier":         "io.github.empowill.djinn",
		"CFBundleExecutable":         "djinn-app",
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
		"Djinn.app/Contents/MacOS/djinn-app":  0o755,
		"Djinn.app/Contents/Info.plist":       0o644,
		"Djinn.app/Contents/Resources/NOTICE": 0o644,
	} {
		if got, ok := modes[name]; !ok || got != want {
			t.Errorf("%s: mode %v (in the zip: %v), want %v", name, got, ok, want)
		}
	}
	if len(modes) != 12 { // 4 folders and 8 files
		t.Errorf("the zip holds %d entries, want 12: %v", len(modes), modes)
	}
}

// The launcher starts the binary beside it with `up`, in the same process, with the PATH a login shell sets. sh
// reads ~/.profile as a login shell: the test gives it a HOME whose profile adds a folder to PATH.
func TestLauncherStartsDjinnUp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the launcher is a shell script, for macOS")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	tmp := t.TempDir()
	out := filepath.Join(tmp, "out")
	djinn := "#!/bin/sh\nprintf '%s\\n%s\\n%s\\n' \"$$\" \"$*\" \"$PATH\" > \"$DJINN_TEST_OUT\"\n"
	binary := filepath.Join(tmp, "djinn")
	if err := os.WriteFile(binary, []byte(djinn), 0o755); err != nil {
		t.Fatal(err)
	}
	// A folder with a space: the launcher quotes its paths.
	app, err := bundle(root, binary, "v0.1.0", filepath.Join(tmp, "Applications folder"))
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(tmp, "home")
	if err := os.Mkdir(home, 0o755); err != nil {
		t.Fatal(err)
	}
	profile := "PATH=\"$PATH:/from/the/login/profile\"\nexport PATH\n"
	if err := os.WriteFile(filepath.Join(home, ".profile"), []byte(profile), 0o644); err != nil {
		t.Fatal(err)
	}
	sh, _ := exec.LookPath("sh")
	cmd := exec.Command(filepath.Join(app, "Contents", "MacOS", "djinn-app"), "-psn_0_12345")
	cmd.Env = []string{"HOME=" + home, "SHELL=" + sh, "PATH=/usr/bin:/bin", "DJINN_TEST_OUT=" + out, "ENV="}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the launcher: %v\n%s", err, output)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("djinn printed %q", data)
	}
	if pid, _ := strconv.Atoi(lines[0]); pid != cmd.Process.Pid {
		t.Errorf("djinn ran as process %s, the launcher as %d: it must exec", lines[0], cmd.Process.Pid)
	}
	if lines[1] != "up" {
		t.Errorf("djinn got %q, want up (Finder's own arguments dropped)", lines[1])
	}
	if !strings.HasSuffix(lines[2], ":/from/the/login/profile") {
		t.Errorf("djinn's PATH %q lacks the login profile's", lines[2])
	}
}

package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	wails "github.com/wailsapp/wails/v3/pkg/updater"
)

// fakeRelease imitates the GitHub API and the files of one release of Djinn: the archive of one variant and its
// SHA256SUMS. It counts what it serves.
type fakeRelease struct {
	srv *httptest.Server

	mu         sync.Mutex
	tag        string // empty: no release yet
	prerelease bool
	archive    string // asset name
	body       []byte // the archive
	sums       string // SHA256SUMS
	checks     int    // reads of the release
	downloads  int    // downloads of the archive
}

func newFakeRelease(t *testing.T) *fakeRelease {
	t.Helper()
	f := &fakeRelease{}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

// publish makes tag the latest release, with exe packed in the archive named after variant. sum, when not nil,
// rewrites SHA256SUMS.
func (f *fakeRelease) publish(t *testing.T, tag, variant string, exe []byte, sum func(string) string) {
	t.Helper()
	archive, body := packRelease(t, variant, exe)
	digest := sha256.Sum256(body)
	sums := hex.EncodeToString(digest[:]) + "  " + archive + "\n"
	if sum != nil {
		sums = sum(sums)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tag, f.archive, f.body, f.sums = tag, archive, body, sums
}

func (f *fakeRelease) counts() (checks, downloads int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.checks, f.downloads
}

func (f *fakeRelease) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.URL.Path {
	case "/repos/" + releaseRepo + "/releases/latest", "/repos/" + releaseRepo + "/releases":
		f.checks++
		if f.tag == "" {
			http.NotFound(w, r)
			return
		}
		rel := map[string]any{
			"tag_name": f.tag, "name": "Djinn " + f.tag, "prerelease": f.prerelease,
			"html_url": f.srv.URL + "/notes/" + f.tag,
			"assets": []map[string]any{
				{"id": 1, "name": f.archive, "size": len(f.body), "browser_download_url": f.srv.URL + "/download/" + f.archive},
				{"id": 2, "name": sumsFile, "size": len(f.sums), "browser_download_url": f.srv.URL + "/download/" + sumsFile},
			},
		}
		var out any = rel
		if strings.HasSuffix(r.URL.Path, "/releases") { // The list, which pre-releases read.
			out = []any{rel}
		}
		_ = json.NewEncoder(w).Encode(out)
	case "/download/" + f.archive:
		f.downloads++
		_, _ = w.Write(f.body)
	case "/download/" + sumsFile:
		_, _ = w.Write([]byte(f.sums))
	default:
		http.NotFound(w, r)
	}
}

// packRelease packs exe as tools/releasepack does: a top folder named after the variant, holding djinn and a notice.
func packRelease(t *testing.T, variant string, exe []byte) (name string, archive []byte) {
	t.Helper()
	files := []struct {
		name string
		body []byte
	}{{variant + "/" + exeName(), exe}, {variant + "/LICENSE", []byte("Apache-2.0")}}
	var b bytes.Buffer
	b.Grow(len(exe) + 1<<16) // Once: the test binary is large.
	if runtime.GOOS == "windows" {
		zw := zip.NewWriter(&b)
		for _, f := range files {
			w, err := zw.Create(f.name)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = w.Write(f.body)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		return variant + ".zip", b.Bytes()
	}
	// Stored, not compressed: a gzip reader reads any level, and the test binary takes seconds to deflate.
	gz, err := gzip.NewWriterLevel(&b, gzip.NoCompression)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(gz)
	for _, f := range files {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o755, Size: int64(len(f.body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write(f.body)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return variant + ".tar.gz", b.Bytes()
}

// useFakeRelease points the release source at f for this test, as a binary of the variant djinn_test.
func useFakeRelease(t *testing.T, f *fakeRelease) {
	t.Helper()
	api, asset, check := releaseAPI, releaseAsset, checkReleases
	releaseAPI, releaseAsset, checkReleases = f.srv.URL, "djinn_test", true
	t.Cleanup(func() { releaseAPI, releaseAsset, checkReleases = api, asset, check })
}

// TestGithubSource covers githubSource: it finds a newer release without downloading it, fetches it on demand, and
// refuses an archive whose sum does not match, a release with no sum for it, and a release without this variant; a
// pre-release follows the pre-releases, which only the list of releases shows. Both change the release source of the
// package (useFakeRelease): not in parallel.
func TestGithubSource(t *testing.T) {
	t.Run("latest and fetch", func(t *testing.T) {
		fake := newFakeRelease(t)
		useFakeRelease(t, fake)
		exe := []byte("the new djinn")
		src, err := releaseSource("v1.0.0")
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := src.(*githubSource); !ok {
			t.Fatalf("a release binary follows %T, want the releases", src)
		}
		ctx := t.Context()

		// No release, then the same version: nothing to offer.
		if r, err := src.latest(ctx); r != nil || err != nil {
			t.Fatalf("no release: %v, %v", r, err)
		}
		fake.publish(t, "v1.0.0", "djinn_test", exe, nil)
		if r, err := src.latest(ctx); r != nil || err != nil {
			t.Fatalf("the same version: %v, %v", r, err)
		}

		// A newer one is offered, with its notes; nothing is downloaded.
		fake.publish(t, "v1.1.0", "djinn_test", exe, nil)
		r, err := src.latest(ctx)
		if err != nil || r == nil || r.Version != "v1.1.0" || r.Notes != fake.srv.URL+"/notes/v1.1.0" {
			t.Fatalf("a newer release: %+v, %v", r, err)
		}
		if _, downloads := fake.counts(); downloads != 0 {
			t.Fatalf("the offer downloaded the archive %d times", downloads)
		}

		// The click: the binary lands in the folder, executable.
		dir := t.TempDir()
		path, version, err := src.fetch(ctx, dir)
		if err != nil || version != "v1.1.0" || filepath.Dir(path) != dir {
			t.Fatalf("fetch: %q, %q, %v", path, version, err)
		}
		if b, err := os.ReadFile(path); err != nil || !bytes.Equal(b, exe) {
			t.Fatalf("fetched %q, %v", b, err)
		}
		if info, _ := os.Stat(path); runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
			t.Fatalf("not executable: %v", info.Mode())
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}

		// A wrong sum: refused, nothing left in the folder.
		fake.publish(t, "v1.2.0", "djinn_test", exe, func(s string) string { return strings.Repeat("0", 64) + s[64:] })
		if _, _, err := src.fetch(ctx, dir); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
			t.Fatalf("a wrong sum: %v", err)
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Fatalf("a refused release left %v", entries)
		}

		// No sum for the archive: not even offered.
		fake.publish(t, "v1.3.0", "djinn_test", exe, func(string) string { return "" })
		if r, err := src.latest(ctx); r != nil || err == nil || !strings.Contains(err.Error(), "lists no SHA-256") {
			t.Fatalf("no sum: %v, %v", r, err)
		}

		// Another variant only: no update for this one.
		fake.publish(t, "v1.4.0", "djinn_test_gtk4", exe, nil)
		if r, err := src.latest(ctx); r != nil || err == nil || !strings.Contains(err.Error(), "no asset") {
			t.Fatalf("another variant: %v, %v", r, err)
		}
	})
	t.Run("a pre-release", func(t *testing.T) {
		fake := newFakeRelease(t)
		useFakeRelease(t, fake)
		fake.publish(t, "v0.0.2-test", "djinn_test", []byte("x"), nil)
		fake.mu.Lock()
		fake.prerelease = true
		fake.mu.Unlock()
		src, err := releaseSource("v0.0.1-test")
		if err != nil {
			t.Fatal(err)
		}
		if r, err := src.latest(t.Context()); err != nil || r == nil || r.Version != "v0.0.2-test" {
			t.Fatalf("a newer pre-release: %+v, %v", r, err)
		}
	})
}

// TestReleaseSource: a release binary follows the releases, a go install the module proxy, a build from a checkout the
// releases too, a development build nothing, nor a binary when releases are off. The djinn of the e2e, built by CI
// from a checkout without tags, names itself by a pseudo-version older than any release; it offered v0.1.0 from the
// module proxy, and the banner covered the load slider the specs click (CI run 38248154434): stamped a test build, it
// looks for no release.
func TestReleaseSource(t *testing.T) {
	asset, check, stamped := releaseAsset, checkReleases, stampedTestBuild
	t.Cleanup(func() { releaseAsset, checkReleases, stampedTestBuild = asset, check, stamped })
	const ci = "v0.0.0-20261012122000-97dd90d8abcd"
	for _, c := range []struct {
		name, version, asset string
		off, stamped         bool
		want                 string
	}{
		{name: "a release binary", version: "v1.0.0", asset: "djinn_linux_amd64", want: "*main.githubSource"},
		{name: "go install", version: "v1.0.0", want: "main.proxySource"},
		{name: "a build from a checkout", version: "local-ab12cd3-dirty", want: "*main.githubSource"},
		{name: "a development build", version: "dev", want: "<nil>"},
		{name: "releases off", version: "v1.0.0", asset: "djinn_linux_amd64", off: true, want: "<nil>"},
		{name: "a go build of a checkout without tags", version: ci, want: "main.proxySource"},
		{name: "the e2e build", version: ci, stamped: true, want: "<nil>"},
	} {
		t.Run(c.name, func(t *testing.T) {
			releaseAsset, checkReleases = c.asset, !c.off
			stampedTestBuild = func() bool { return c.stamped }
			src, err := releaseSource(c.version)
			if got := fmt.Sprintf("%T", src); err != nil || got != c.want {
				t.Errorf("version %q, asset %q: %s, %v; want %s", c.version, c.asset, got, err, c.want)
			}
		})
	}
}

// TestProxySource: a Djinn installed with go install asks the module proxy GOPROXY names, and updates with go install
// of the newer version, with its own build tags, into its folder. It changes goCommand and the environment: not in
// parallel.
func TestProxySource(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake go is a shell script")
	}
	latest := "v1.2.0"
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+modulePath+"/@latest" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"Version":"`+latest+`","Time":"2026-10-01T00:00:00Z"}`)
	}))
	defer proxy.Close()

	// A fake go: go env GOPROXY prints $FAKE_GOPROXY; go install notes its arguments and writes $GOBIN/djinn.
	bin := t.TempDir()
	calls := filepath.Join(t.TempDir(), "calls")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = env ]; then echo \"$FAKE_GOPROXY\"; exit 0; fi\n" +
		"echo \"$@\" >> " + calls + "\n" +
		"printf 'built %s' \"$*\" > \"$GOBIN/djinn\"\n"
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	command := goCommand
	goCommand = filepath.Join(bin, "go")
	t.Cleanup(func() { goCommand = command })
	t.Setenv("FAKE_GOPROXY", proxy.URL+"/,direct")

	ctx := t.Context()
	src := proxySource{version: "v1.1.0"}
	r, err := src.latest(ctx)
	if err != nil || r == nil || r.Version != "v1.2.0" {
		t.Fatalf("a newer version: %+v, %v", r, err)
	}
	if b, _ := os.ReadFile(calls); len(b) != 0 {
		t.Fatalf("the offer ran go install: %s", b)
	}
	dir := t.TempDir()
	path, version, err := src.fetch(ctx, dir)
	if err != nil || version != "v1.2.0" || filepath.Dir(path) != dir {
		t.Fatalf("fetch: %q, %q, %v", path, version, err)
	}
	want := "install -tags headless " + modulePath + "/cmd/djinn@v1.2.0"
	if b, _ := os.ReadFile(path); string(b) != "built "+want {
		t.Fatalf("installed %q, want built %q", b, want)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("left in the folder: %v", entries)
	}

	// Up to date, or no proxy to ask: nothing.
	latest = "v1.1.0"
	if r, err := src.latest(ctx); r != nil || err != nil {
		t.Fatalf("the same version: %v, %v", r, err)
	}
	t.Setenv("FAKE_GOPROXY", "off")
	if r, err := src.latest(ctx); r != nil || err != nil {
		t.Fatalf("GOPROXY=off: %v, %v", r, err)
	}
}

// TestLocalBuild reads the commit a build from a checkout comes from in its version, as go tool task install stamps it.
func TestLocalBuild(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		version, commit string
		dirty, ok       bool
	}{
		{"local-1a2b3c4", "1a2b3c4", false, true},
		{"local-1a2b3c4-dirty", "1a2b3c4", true, true},
		{"local-v0.1.0-3-g1a2b3c4", "1a2b3c4", false, true},
		{"local-v0.1.0-3-g1a2b3c4-dirty", "1a2b3c4", true, true},
		{"local-", "", false, false},
		{"local-v0.1.0", "", false, false},
		{"v0.1.0", "", false, false},
		{"dev", "", false, false},
	} {
		t.Run(c.version, func(t *testing.T) {
			commit, dirty, ok := localBuild(c.version)
			if ok != c.ok || ok && (commit != c.commit || dirty != c.dirty) {
				t.Errorf("localBuild(%q) = %q, %v, %v; want %q, %v, %v", c.version, commit, dirty, ok, c.commit, c.dirty, c.ok)
			}
		})
	}
}

// TestTagOf: the tag of a release the GitHub provider found is the one it names, else its version with the v Djinn's
// versions carry.
func TestTagOf(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		rel  *wails.Release
		want string
	}{
		{"named", &wails.Release{Version: "1.2.0", Metadata: map[string]any{"github.release.tag": "v1.2.0-rc1"}}, "v1.2.0-rc1"},
		{"from its version", &wails.Release{Version: "1.2.0"}, "v1.2.0"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := tagOf(c.rel); got != c.want {
				t.Errorf("tagOf = %q, want %q", got, c.want)
			}
		})
	}
}

// TestCopyExecutable: the release's binary lands in a new file of the folder, executable; a release without it, or a
// folder that is not there, is an error that leaves nothing behind.
func TestCopyExecutable(t *testing.T) {
	t.Parallel()
	src := filepath.Join(t.TempDir(), exeName())
	if err := os.WriteFile(src, []byte("the new djinn"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, src, dir, err string
	}{
		{name: "copied", src: src, dir: t.TempDir()},
		{name: "no binary", src: src + ".gone", dir: t.TempDir(), err: "the release holds no " + filepath.Base(src) + ".gone"},
		{name: "no folder", src: src, dir: filepath.Join(t.TempDir(), "gone"), err: "gone"},
	} {
		t.Run(c.name, func(t *testing.T) {
			path, err := copyExecutable(c.src, c.dir)
			entries, _ := os.ReadDir(c.dir)
			if c.err != "" {
				if err == nil || !strings.Contains(err.Error(), c.err) || len(entries) != 0 {
					t.Errorf("copyExecutable = %q, %v, left %v; want %q", path, err, entries, c.err)
				}
				return
			}
			b, rerr := os.ReadFile(path)
			if err != nil || rerr != nil || string(b) != "the new djinn" || filepath.Dir(path) != c.dir || len(entries) != 1 {
				t.Fatalf("copyExecutable = %q, %v: %q, %v", path, err, b, rerr)
			}
			if info, _ := os.Stat(path); runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
				t.Errorf("not executable: %v", info.Mode())
			}
		})
	}
}

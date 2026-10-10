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
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
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

// TestGitHubSource finds a newer release without downloading it, fetches it on demand, and refuses an archive whose
// sum does not match, a release with no sum for it, and a release without this variant.
func TestGitHubSource(t *testing.T) {
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
}

// TestGitHubSourcePrerelease: a pre-release follows the pre-releases, which only the list of releases shows.
func TestGitHubSourcePrerelease(t *testing.T) {
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
}

// TestReleaseSource: a release binary follows the releases, a go install the module proxy, a build from a checkout the
// releases too, a development build nothing.
func TestReleaseSource(t *testing.T) {
	asset, check := releaseAsset, checkReleases
	t.Cleanup(func() { releaseAsset, checkReleases = asset, check })
	checkReleases = true
	for _, c := range []struct {
		version, asset, want string
	}{
		{"v1.0.0", "djinn_linux_amd64", "*main.githubSource"},
		{"v1.0.0", "", "main.proxySource"},
		{"local-ab12cd3-dirty", "", "*main.githubSource"},
		{"dev", "", "<nil>"},
	} {
		releaseAsset = c.asset
		src, err := releaseSource(c.version)
		if got := fmt.Sprintf("%T", src); err != nil || got != c.want {
			t.Errorf("version %q, asset %q: %s, %v; want %s", c.version, c.asset, got, err, c.want)
		}
	}
	checkReleases = false
	if src, _ := releaseSource("v1.0.0"); src != nil {
		t.Errorf("releases off: %T", src)
	}
}

// TestLocalBuild reads the commit a build from a checkout comes from in its version, as go tool task install stamps it.
func TestLocalBuild(t *testing.T) {
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
		commit, dirty, ok := localBuild(c.version)
		if ok != c.ok || ok && (commit != c.commit || dirty != c.dirty) {
			t.Errorf("localBuild(%q) = %q, %v, %v; want %q, %v, %v", c.version, commit, dirty, ok, c.commit, c.dirty, c.ok)
		}
	}
}

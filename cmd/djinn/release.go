package main

// Where a newer Djinn comes from, besides a binary installed at the path of the running one (update.go). A Djinn
// installed from a release asks the releases of its repository; one installed with go install asks the Go module
// proxy. Either only reads a version until the person clicks "Update": then it downloads, verifies, and puts the new
// binary beside the running one, ready to swap. A Djinn built from a checkout (go tool task install) asks the releases
// too, of the variant it would have been; where its checkout stands decides what a release found means (update.go).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	wails "github.com/wailsapp/wails/v3/pkg/updater"
	"github.com/wailsapp/wails/v3/pkg/updater/providers/github"
	"golang.org/x/mod/semver"
)

var (
	// releaseAsset names the release archive this binary came from, without its extension, such as
	// djinn_linux_amd64_gtk4: `task release-build` stamps it (-X main.releaseAsset=…), so that an update brings the
	// same variant. Empty for any other build.
	releaseAsset = ""
	// releaseRepo is the GitHub repository whose releases a release binary follows.
	releaseRepo = "Empowill/Djinn"
	// releaseAPI is the GitHub API. Tests point it at a fake release.
	releaseAPI = "https://api.github.com"
	// modulePath is the Go module go install fetches.
	modulePath = "github.com/empowill/djinn"
	// releaseCheck is how often a running Djinn looks for a newer release, after once at start-up.
	releaseCheck = 6 * time.Hour
	// goCommand installs a newer version of a Djinn installed with go install.
	goCommand = "go"
	// checkReleases is off in the tests that serve no fake release: they never reach the network.
	checkReleases = true
)

// sumsFile is the checksum file of a release (tools/releasepack): no line for the archive, no update.
const sumsFile = "SHA256SUMS"

// release is a newer Djinn found by a source.
type release struct {
	Version string // the tag, such as v0.2.0
	Notes   string // where its release notes are; empty when the source has none
}

// source finds a newer Djinn, and fetches it on demand.
type source interface {
	// latest returns the newest release when it is newer than the running Djinn; nil when none is.
	latest(ctx context.Context) (*release, error)
	// fetch downloads the newest release, verified, as an executable file in dir, the folder of the running binary:
	// a rename puts it in place. It returns the file and the version it holds.
	fetch(ctx context.Context, dir string) (path, version string, err error)
}

// releaseSource is where a Djinn of version looks for a newer one: the releases for a release binary, the module
// proxy for a version go install built, the releases again for a build from a checkout, any release being newer to
// it; nothing for a development build.
func releaseSource(version string) (source, error) {
	_, _, local := localBuild(version)
	switch {
	case !checkReleases:
		return nil, nil
	case releaseAsset != "":
		return newGitHubSource(releaseAsset, version)
	case semver.IsValid(version):
		return proxySource{version: version}, nil
	case local:
		return newGitHubSource(localAsset(), "v0.0.0")
	}
	return nil, nil
}

// localBuild is the commit a Djinn built from a checkout comes from, as go tool task install stamps its version
// (local-<git describe --always --dirty>: local-1a2b3c4, local-v0.1.0-3-g1a2b3c4-dirty), and whether its tree had
// changes not committed; ok is false for any other version.
func localBuild(version string) (commit string, dirty, ok bool) {
	rest, ok := strings.CutPrefix(version, "local-")
	if !ok {
		return "", false, false
	}
	rest, dirty = strings.CutSuffix(rest, "-dirty")
	if i := strings.LastIndex(rest, "-g"); i >= 0 && isHex(rest[i+2:]) {
		rest = rest[i+2:]
	}
	return rest, dirty, len(rest) >= 4 && isHex(rest)
}

// isHex tells whether s is made of lowercase hexadecimal digits only.
func isHex(s string) bool {
	return s != "" && strings.Trim(s, "0123456789abcdef") == ""
}

// localAsset names the release archive of the variant this binary would be, built from a checkout: what
// task release-build names it for the same system, architecture and window (Taskfile.yml).
func localAsset() string {
	switch runtime.GOOS {
	case "darwin":
		return "djinn_darwin_universal"
	case "windows":
		return "djinn_windows_" + runtime.GOARCH
	}
	name := "djinn_" + runtime.GOOS + "_" + runtime.GOARCH
	tags, cgo := buildSettings()
	switch {
	case cgo != "1":
		return name + "_browser"
	case runtime.GOOS == "linux" && !slices.Contains(strings.Split(tags, ","), "gtk3"):
		return name + "_gtk4"
	}
	return name
}

// exeName is the name of djinn inside a release archive and in GOBIN.
func exeName() string {
	if runtime.GOOS == "windows" {
		return "djinn.exe"
	}
	return "djinn"
}

// githubSource follows the releases of the repository, through the updater of Wails: its GitHub provider finds the
// release and the archive of this variant, and the updater downloads it, checks its SHA-256 against SHA256SUMS while
// it streams, and unpacks it safely. Djinn swaps and restarts on its own: the helper of Wails relaunches the binary
// without its arguments, and only in a build with a window.
type githubSource struct {
	up *wails.Updater
}

// newGitHubSource follows the releases of the variant asset, for a Djinn of version.
func newGitHubSource(asset, version string) (*githubSource, error) {
	archive := asset + ".tar.gz"
	if runtime.GOOS == "windows" {
		archive = asset + ".zip"
	}
	provider, err := github.New(github.Config{
		Repository: releaseRepo,
		BaseURL:    releaseAPI,
		// A pre-release follows the pre-releases; a release, the releases only.
		Prerelease:    semver.Prerelease(version) != "",
		ChecksumAsset: sumsFile,
		AssetMatcher: func(_ wails.CheckRequest, assets []github.ReleaseAsset) int {
			for i, a := range assets {
				if a.Name == archive {
					return i
				}
			}
			return -1
		},
	})
	if err != nil {
		return nil, err
	}
	up := wails.New(quietHost{})
	err = up.Init(wails.Config{
		CurrentVersion: version,
		Providers:      []wails.Provider{summed{provider}},
		Window:         wails.WindowNone,
	})
	if err != nil {
		return nil, err
	}
	return &githubSource{up: up}, nil
}

func (s *githubSource) latest(ctx context.Context) (*release, error) {
	rel, err := s.up.Check(ctx)
	if err != nil || rel == nil {
		return nil, err
	}
	notes, _ := rel.Metadata["github.release.htmlURL"].(string)
	return &release{Version: tagOf(rel), Notes: notes}, nil
}

func (s *githubSource) fetch(ctx context.Context, dir string) (string, string, error) {
	// Ask again: the click may come hours after the offer.
	rel, err := s.up.Check(ctx)
	if err != nil {
		return "", "", err
	}
	if rel == nil {
		return "", "", errors.New("no newer release")
	}
	if err := s.up.DownloadAndInstall(ctx); err != nil {
		return "", "", err
	}
	unpacked := s.up.DownloadedPath()
	// The staging folder of the updater, in the temporary folder: removed once the binary is copied out.
	if staging := filepath.Dir(unpacked); unpacked != "" && strings.HasPrefix(filepath.Base(staging), "wails-update-") {
		defer os.RemoveAll(staging)
	}
	path, err := copyExecutable(filepath.Join(unpacked, exeName()), dir)
	return path, tagOf(rel), err
}

// tagOf is the tag of a release found by the GitHub provider: its version keeps the v Djinn's versions carry.
func tagOf(rel *wails.Release) string {
	if tag, ok := rel.Metadata["github.release.tag"].(string); ok && tag != "" {
		return tag
	}
	return "v" + rel.Version
}

// copyExecutable copies src into a new file of dir, executable: the temporary folder is often on another file system,
// where no rename reaches.
func copyExecutable(src, dir string) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", fmt.Errorf("the release holds no %s: %w", filepath.Base(src), err)
	}
	defer in.Close()
	out, err := os.CreateTemp(dir, ".djinn-new-*")
	if err != nil {
		return "", err
	}
	_, err = io.Copy(out, in)
	err = errors.Join(err, out.Sync(), out.Close(), os.Chmod(out.Name(), 0o755))
	if err != nil {
		_ = os.Remove(out.Name())
		return "", err
	}
	return out.Name(), nil
}

// summed refuses a release that lists no SHA-256 for its archive: the GitHub provider would install it unchecked.
type summed struct{ wails.Provider }

func (s summed) Check(ctx context.Context, req wails.CheckRequest) (*wails.Release, error) {
	rel, err := s.Provider.Check(ctx, req)
	if err != nil || rel == nil {
		return rel, err
	}
	if rel.Verification == nil || len(rel.Verification.Digest) == 0 {
		return nil, fmt.Errorf("release %s lists no SHA-256 for %s in %s", tagOf(rel), rel.Artifact.Filename, sumsFile)
	}
	return rel, nil
}

// quietHost is the application the updater of Wails reports to: Djinn has its own banner, so nothing listens.
type quietHost struct{}

func (quietHost) Emit(string, ...any) bool                          { return false }
func (quietHost) OnEvent(string, func(any)) func()                  { return func() {} }
func (quietHost) OpenWindow(wails.WindowOptions) wails.WindowHandle { return nil }
func (quietHost) Quit()                                             {}

// proxySource follows the versions of the module that the Go module proxy knows, for a Djinn installed with go
// install: the update is go install of the newer version, with the build tags this one was built with. The Go
// checksum database verifies the source it downloads.
type proxySource struct {
	version string
}

func (p proxySource) latest(ctx context.Context) (*release, error) {
	if _, err := exec.LookPath(goCommand); err != nil {
		return nil, errors.New("go is not on the PATH: a Djinn installed with go install updates with go install")
	}
	proxy, err := goProxy(ctx)
	if err != nil || proxy == "" {
		return nil, err // GOPROXY off or direct: no proxy to ask.
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, proxy+"/"+modulePath+"/@latest", nil)
	if err != nil {
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound || res.StatusCode == http.StatusGone {
		return nil, nil // No version published yet.
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", req.URL, res.Status)
	}
	var info struct{ Version string }
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<16)).Decode(&info); err != nil {
		return nil, fmt.Errorf("%s: %w", req.URL, err)
	}
	if !semver.IsValid(info.Version) || semver.Compare(info.Version, p.version) <= 0 {
		return nil, nil
	}
	return &release{Version: info.Version}, nil
}

func (p proxySource) fetch(ctx context.Context, dir string) (string, string, error) {
	r, err := p.latest(ctx)
	if err != nil {
		return "", "", err
	}
	if r == nil {
		return "", "", errors.New("no newer version")
	}
	bin, err := os.MkdirTemp(dir, ".djinn-update-")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(bin)
	args := []string{"install"}
	tags, cgo := buildSettings()
	if tags != "" {
		args = append(args, "-tags", tags)
	}
	cmd := exec.CommandContext(ctx, goCommand, append(args, modulePath+"/cmd/djinn@"+r.Version)...)
	cmd.Env = append(os.Environ(), "GOBIN="+bin)
	if cgo != "" {
		cmd.Env = append(cmd.Env, "CGO_ENABLED="+cgo)
	}
	detach(cmd) // No console flashes on Windows.
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", "", fmt.Errorf("%s: %w\n%s", strings.Join(cmd.Args, " "), err, strings.TrimSpace(string(out)))
	}
	f, err := os.CreateTemp(dir, ".djinn-new-*")
	if err != nil {
		return "", "", err
	}
	f.Close()
	if err := os.Rename(filepath.Join(bin, exeName()), f.Name()); err != nil {
		_ = os.Remove(f.Name())
		return "", "", err
	}
	return f.Name(), r.Version, nil
}

// goProxy is the first proxy of `go env GOPROXY`, without its trailing slash; empty when the list starts with off or
// direct, or with anything but HTTP.
func goProxy(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, goCommand, "env", "GOPROXY")
	detach(cmd)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("go env GOPROXY: %w", err)
	}
	first, _, _ := strings.Cut(strings.TrimSpace(string(out)), ",")
	first, _, _ = strings.Cut(first, "|")
	if !strings.HasPrefix(first, "https://") && !strings.HasPrefix(first, "http://") {
		return "", nil
	}
	return strings.TrimRight(first, "/"), nil
}

// buildSettings are the build tags and CGO_ENABLED this binary was built with, as Go recorded them.
func buildSettings() (tags, cgo string) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", ""
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "-tags":
			tags = s.Value
		case "CGO_ENABLED":
			cgo = s.Value
		}
	}
	return tags, cgo
}

package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The install script (scripts/install.sh) is tested here against fake releases that this packer builds, served by a
// local HTTP server: the layout it reads is the one pack and sums write. Never against GitHub.

// release lays out, under root/<at>, a release holding one archive per asset, whose djinn is the given script,
// and its SHA256SUMS. at is "latest/download" or "download/<tag>", as GitHub serves them.
func release(t *testing.T, root, at string, assets map[string]string) {
	t.Helper()
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(root, filepath.FromSlash(at))
	for name, script := range assets {
		dir := filepath.Join(out, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "djinn"), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := pack(dir, repo); err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
	}
	if err := sums(out); err != nil {
		t.Fatal(err)
	}
}

type install struct {
	dir    string // where djinn lands
	goLog  string // what the fake go was asked, if it ran
	out    string // stdout and stderr
	failed bool
}

// runInstall runs scripts/install.sh against the releases under root, with env added. A fake go, first on the
// PATH, records its arguments and installs a stub, unless noGo hides every go.
func runInstall(t *testing.T, root string, noGo bool, env ...string) install {
	t.Helper()
	for _, tool := range []string{"sh", "tar", "curl", "awk"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("the install script needs %s", tool)
		}
	}
	server := httptest.NewServer(http.FileServer(http.Dir(root)))
	t.Cleanup(server.Close)

	tmp := t.TempDir()
	res := install{dir: filepath.Join(tmp, "bin"), goLog: filepath.Join(tmp, "go.log")}
	fakeBin := filepath.Join(tmp, "path")
	if err := os.Mkdir(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	if !noGo {
		goScript := `#!/bin/sh
echo "$* CGO_ENABLED=$CGO_ENABLED GOBIN=$GOBIN" > "` + res.goLog + `"
printf '#!/bin/sh\necho djinn from-go\n' > "$GOBIN/djinn"
chmod 755 "$GOBIN/djinn"
`
		if err := os.WriteFile(filepath.Join(fakeBin, "go"), []byte(goScript), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// The PATH holds links to the tools the script uses, and the fake go: never a real go.
	for _, tool := range []string{"curl", "tar", "gzip", "awk", "sha256sum", "shasum", "openssl", "mktemp", "uname",
		"cut", "cp", "chmod", "mv", "mkdir", "rm", "ldconfig"} {
		if p, err := exec.LookPath(tool); err == nil {
			if err := os.Symlink(p, filepath.Join(fakeBin, tool)); err != nil {
				t.Fatal(err)
			}
		}
	}
	cmd := exec.Command("sh", "../../scripts/install.sh")
	cmd.Env = append([]string{
		"PATH=" + fakeBin,
		"HOME=" + tmp,
		"DJINN_RELEASES=" + server.URL,
		"DJINN_INSTALL_DIR=" + res.dir,
	}, env...)
	out, err := cmd.CombinedOutput()
	res.out, res.failed = string(out), err != nil
	return res
}

func (r install) version(t *testing.T) string {
	t.Helper()
	out, err := exec.Command(filepath.Join(r.dir, "djinn")).Output()
	if err != nil {
		t.Fatalf("installed djinn: %v\ninstall output:\n%s", err, r.out)
	}
	return strings.TrimSpace(string(out))
}

func skipOnWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("install.sh is for macOS and Linux; install.ps1 is for Windows")
	}
}

func TestInstallLatest(t *testing.T) {
	skipOnWindows(t)
	root := t.TempDir()
	release(t, root, "latest/download", map[string]string{"djinn_test": "echo djinn v1.0.0", "djinn_other": "exit 1"})
	r := runInstall(t, root, false, "DJINN_ASSET=djinn_test")
	if r.failed {
		t.Fatalf("install failed:\n%s", r.out)
	}
	if got := r.version(t); got != "djinn v1.0.0" {
		t.Fatalf("installed %q, want djinn v1.0.0", got)
	}
	if _, err := os.Stat(r.goLog); err == nil {
		t.Fatal("go ran although a binary fitted")
	}
	if entries, _ := os.ReadDir(r.dir); len(entries) != 1 {
		t.Fatalf("install folder holds %v, want djinn alone", entries)
	}
}

func TestInstallVersion(t *testing.T) {
	skipOnWindows(t)
	root := t.TempDir()
	release(t, root, "latest/download", map[string]string{"djinn_test": "echo djinn v2.0.0"})
	release(t, root, "download/v1.5.0", map[string]string{"djinn_test": "echo djinn v1.5.0"})
	r := runInstall(t, root, false, "DJINN_ASSET=djinn_test", "DJINN_VERSION=v1.5.0")
	if r.failed {
		t.Fatalf("install failed:\n%s", r.out)
	}
	if got := r.version(t); got != "djinn v1.5.0" {
		t.Fatalf("installed %q, want djinn v1.5.0", got)
	}
}

// TestInstallRefusesATamperedArchive: a download that does not match its sum stops the install, with no fallback.
func TestInstallRefusesATamperedArchive(t *testing.T) {
	skipOnWindows(t)
	root := t.TempDir()
	release(t, root, "latest/download", map[string]string{"djinn_test": "echo djinn v1.0.0"})
	archive := filepath.Join(root, "latest", "download", "djinn_test.tar.gz")
	b, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	b[len(b)/2] ^= 0xff
	if err := os.WriteFile(archive, b, 0o644); err != nil {
		t.Fatal(err)
	}
	r := runInstall(t, root, false, "DJINN_ASSET=djinn_test")
	if !r.failed || !strings.Contains(r.out, "does not match its SHA-256 sum") {
		t.Fatalf("install of a tampered archive: failed=%v, output:\n%s", r.failed, r.out)
	}
	if _, err := os.Stat(filepath.Join(r.dir, "djinn")); err == nil {
		t.Fatal("a tampered djinn was installed")
	}
	if _, err := os.Stat(r.goLog); err == nil {
		t.Fatal("go ran after a tampered download")
	}
}

// TestInstallFallsBackOnGo: a binary that does not start here (a missing WebKitGTK), or no release at all, leads to
// go install without CGO, at the same version.
func TestInstallFallsBackOnGo(t *testing.T) {
	skipOnWindows(t)
	cases := []struct {
		name, version, want string
		asset               string
	}{
		{name: "binary does not start", asset: "djinn_test", want: "@latest"},
		{name: "no binary for this system", asset: "djinn_elsewhere", want: "@latest"},
		{name: "no release", version: "v9.9.9", asset: "djinn_test", want: "@v9.9.9"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			release(t, root, "latest/download", map[string]string{"djinn_test": "exit 127"})
			env := []string{"DJINN_ASSET=" + c.asset}
			if c.version != "" {
				env = append(env, "DJINN_VERSION="+c.version)
			}
			r := runInstall(t, root, false, env...)
			if r.failed {
				t.Fatalf("install failed:\n%s", r.out)
			}
			log, err := os.ReadFile(r.goLog)
			if err != nil {
				t.Fatalf("go did not run:\n%s", r.out)
			}
			want := "install github.com/empowill/djinn/cmd/djinn" + c.want + " CGO_ENABLED=0 GOBIN=" + r.dir
			if got := strings.TrimSpace(string(log)); got != want {
				t.Fatalf("go was asked %q, want %q", got, want)
			}
			if got := r.version(t); got != "djinn from-go" {
				t.Fatalf("installed %q, want the one go built", got)
			}
		})
	}
}

func TestInstallWithoutGoNorBinary(t *testing.T) {
	skipOnWindows(t)
	root := t.TempDir()
	release(t, root, "latest/download", map[string]string{"djinn_test": "echo djinn v1.0.0"})
	r := runInstall(t, root, true, "DJINN_ASSET=djinn_elsewhere")
	if !r.failed || !strings.Contains(r.out, "Go is not installed") {
		t.Fatalf("install with nothing to install: failed=%v, output:\n%s", r.failed, r.out)
	}
}

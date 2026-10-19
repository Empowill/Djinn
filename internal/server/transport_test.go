package server_test

import (
	"bytes"
	"encoding/binary"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	demov1 "github.com/empowill/djinn/gen/go/demo/v1"
	"github.com/empowill/djinn/internal/server"
)

func TestTransportFor(t *testing.T) {
	t.Parallel()
	for goos, want := range map[string]server.Transport{
		"windows": server.HTTP,
		"darwin":  server.Wails,
		"linux":   server.Wails,
		"freebsd": server.Wails,
	} {
		t.Run(goos, func(t *testing.T) {
			t.Parallel()
			if got := server.TransportFor(goos); got != want {
				t.Errorf("TransportFor(%q) = %q, want %q", goos, got, want)
			}
		})
	}
}

// shortDir returns a fresh directory with a short path: a Unix socket path is limited to about 100 bytes, and
// t.TempDir is longer than that on macOS.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "dj")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// ownerOnly checks that path is readable and writable by its owner only. Windows has no such mode bits.
func ownerOnly(t *testing.T, path string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("%s: mode %o, want 600", path, perm)
	}
}

func TestListenUnix(t *testing.T) {
	t.Parallel()

	t.Run("refuses a second listener", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(shortDir(t), "djinn.sock")
		ln, err := server.ListenUnix(path)
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		ownerOnly(t, path)
		if _, err := server.ListenUnix(path); err == nil || !strings.Contains(err.Error(), "already running") {
			t.Fatalf("second listener: %v, want already running", err)
		}
	})

	// A socket file nobody listens on, as a crash leaves it, is replaced.
	t.Run("replaces a stale socket", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(shortDir(t), "djinn.sock")
		stale, err := net.Listen("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		stale.(*net.UnixListener).SetUnlinkOnClose(false)
		stale.Close()
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("the stale socket should remain: %v", err)
		}
		ln, err := server.ListenUnix(path)
		if err != nil {
			t.Fatalf("stale socket: %v", err)
		}
		ln.Close()
	})

	t.Run("refuses a long path", func(t *testing.T) {
		t.Parallel()
		long := filepath.Join(shortDir(t), strings.Repeat("d", 100), "djinn.sock")
		if _, err := server.ListenUnix(long); err == nil || !strings.Contains(err.Error(), "DJINN_HOME") {
			t.Fatalf("long path: %v, want a hint to shorten DJINN_HOME", err)
		}
	})
}

func TestWriteAddr(t *testing.T) {
	t.Parallel()
	const addr = "http://127.0.0.1:4000/?token=s3cret"

	// A file left with loose permissions is replaced, not reused; remove deletes it.
	t.Run("replaces a loose file", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		path := filepath.Join(home, server.AddrFile)
		if err := os.WriteFile(path, []byte("stale"), 0o644); err != nil {
			t.Fatal(err)
		}
		remove, err := server.WriteAddr(home, addr)
		if err != nil {
			t.Fatal(err)
		}
		ownerOnly(t, path)
		if got, err := server.ReadAddr(home); err != nil || got != addr {
			t.Fatalf("ReadAddr = %q, %v, want %q", got, err, addr)
		}
		remove()
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("after remove: %v, want no file", err)
		}
	})

	// A server that started since owns the file: the first one leaves it.
	t.Run("leaves a newer address", func(t *testing.T) {
		t.Parallel()
		home := filepath.Join(t.TempDir(), "home") // Created by WriteAddr.
		remove, err := server.WriteAddr(home, addr)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := server.WriteAddr(home, "unix:///elsewhere/djinn.sock"); err != nil {
			t.Fatal(err)
		}
		remove()
		if got, err := server.ReadAddr(home); err != nil || got != "unix:///elsewhere/djinn.sock" {
			t.Fatalf("after the first server stops: %q, %v, want the second address", got, err)
		}
	})
}

func TestReadAddr(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		home    func(t *testing.T) string
		want    string
		wantErr string
	}{
		{"no file says to run djinn up", func(t *testing.T) string { return t.TempDir() }, "", "djinn up"},
		{"no data directory", func(*testing.T) string { return "" }, "", "data directory is empty"},
		{"empty file", func(t *testing.T) string {
			home := t.TempDir()
			if err := os.WriteFile(filepath.Join(home, server.AddrFile), []byte("\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			return home
		}, "", "is empty"},
		{"address on its line", func(t *testing.T) string {
			home := t.TempDir()
			if err := os.WriteFile(filepath.Join(home, server.AddrFile), []byte("unix:///d/djinn.sock\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			return home
		}, "unix:///d/djinn.sock", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := server.ReadAddr(tc.home(t))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("ReadAddr = %q, %v, want an error with %q", got, err, tc.wantErr)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("ReadAddr = %q, %v, want %q", got, err, tc.want)
			}
		})
	}
}

// TestWholeWrites checks that through WholeWrites, as the window's wails:// handler serves, each message of a Connect
// stream reaches the writer in one Write: on macOS, WebKit held back a payload written after its prefix.
func TestWholeWrites(t *testing.T) {
	t.Parallel()
	body, err := proto.Marshal(&demov1.CountRequest{UpTo: 3})
	if err != nil {
		t.Fatal(err)
	}
	stream := func(h http.Handler) [][]byte {
		req := httptest.NewRequest(http.MethodPost, "/demo.v1.DemoService/Count",
			bytes.NewReader(append([]byte{0, 0, 0, 0, byte(len(body))}, body...)))
		req.Header.Set("Content-Type", "application/connect+proto")
		rec := &writes{ResponseRecorder: httptest.NewRecorder()}
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rec.Code, rec.Body)
		}
		return rec.chunks
	}

	// Without it, connect-go writes a prefix on its own: the test still sees what it guards against.
	if chunks := stream(echoHandler()); !slices.ContainsFunc(chunks, func(c []byte) bool { return len(c) == 5 }) {
		t.Fatalf("connect-go no longer writes a prefix on its own: %d writes", len(chunks))
	}

	// With it: the three values and the end of the stream, each whole.
	chunks := stream(server.WholeWrites(echoHandler()))
	if len(chunks) != 4 {
		t.Fatalf("%d writes, want 4 (three values, then the end): %q", len(chunks), chunks)
	}
	for i, c := range chunks {
		if len(c) < 5 || int(binary.BigEndian.Uint32(c[1:5])) != len(c)-5 {
			t.Errorf("write %d is not one whole message: %q", i, c)
		}
	}
}

// writes records each Write and each Flush.
type writes struct {
	*httptest.ResponseRecorder
	chunks [][]byte
}

func (w *writes) Write(b []byte) (int, error) {
	w.chunks = append(w.chunks, bytes.Clone(b))
	return w.ResponseRecorder.Write(b)
}

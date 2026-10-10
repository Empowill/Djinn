package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/empowill/djinn/internal/server"
)

// TestRelay: the page's calls reach the djinn of the moment with its token, never from another page.
func TestRelay(t *testing.T) {
	home := t.TempDir()
	const origin = "http://127.0.0.1:4317"
	r := httptest.NewServer(relay(home, origin))
	defer r.Close()
	call := func(from string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, r.URL+"/ui.v1.UiService/GetEnvironment", nil)
		if from != "" {
			req.Header.Set("Origin", from)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}

	if code, _ := call(origin); code != http.StatusServiceUnavailable {
		t.Fatalf("no djinn: %d", code)
	}

	// A djinn behind its guard, as djinn up --browser serves it.
	var seen *http.Request
	djinn := httptest.NewServer(nil)
	djinn.Config.Handler = server.Guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r
		_, _ = io.WriteString(w, "answered "+r.URL.Path)
	}), "s3cret", djinn.URL)
	defer djinn.Close()
	remove, err := server.WriteAddr(home, djinn.URL+"/?token=s3cret")
	if err != nil {
		t.Fatal(err)
	}
	defer remove()

	if code, body := call(origin); code != http.StatusOK || body != "answered /ui.v1.UiService/GetEnvironment" {
		t.Fatalf("from the page: %d %q", code, body)
	}
	if seen.Header.Get("Origin") != "" {
		t.Fatalf("djinn saw the origin %q", seen.Header.Get("Origin"))
	}
	if code, _ := call(""); code != http.StatusOK {
		t.Fatalf("without an origin: %d", code)
	}
	seen = nil
	if code, _ := call("http://evil.example"); code != http.StatusForbidden || seen != nil {
		t.Fatalf("from another page: %d, reached djinn %v", code, seen != nil)
	}
}

// TestDevHome: the loop never runs on the data of the Djinn in use.
func TestDevHome(t *testing.T) {
	config, err := os.UserConfigDir()
	if err != nil {
		t.Skip(err)
	}
	if _, err := devHome(filepath.Join(config, "djinn")); err == nil {
		t.Fatal("devHome accepted the data of the Djinn in use")
	}
	dir := filepath.Join(t.TempDir(), "dev-home")
	if got, err := devHome(dir); err != nil || got != dir {
		t.Fatalf("devHome(%q) = %q, %v", dir, got, err)
	}
}

// TestChanged sees a Go file change, and ignores a test file.
func TestChanged(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if err := os.MkdirAll(filepath.Join("internal", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join("internal", "x", name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("x.go", "package x")
	seen := snapshot()
	if changed(&seen) {
		t.Fatal("changed with nothing changed")
	}
	write("x_test.go", "package x")
	if changed(&seen) {
		t.Fatal("a test file rebuilds djinn")
	}
	write("x.go", "package x // edited")
	if !changed(&seen) {
		t.Fatal("an edited Go file went unseen")
	}
	if changed(&seen) {
		t.Fatal("the same change seen twice")
	}
}

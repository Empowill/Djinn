package cli

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"connectrpc.com/connect"

	"github.com/empowill/djinn/internal/ui"
)

// Dial returns the HTTP client and the base URL that reach the server at addr, for a typed Connect client.
func Dial(addr string) (connect.HTTPClient, string, error) { return dial(addr) }

// Alive tells whether a server listens at addr: its socket, or its loopback port, takes a connection. A server
// that crashed leaves its address behind, and nothing answers it.
func Alive(addr string) bool {
	network, address := "unix", strings.TrimPrefix(addr, "unix://")
	if strings.HasPrefix(addr, "unix://") {
		if ui.InTest() && ui.IsRealHome(filepath.Dir(address)) {
			return false
		}
	} else {
		u, err := url.Parse(addr)
		if err != nil || u.Host == "" {
			return false
		}
		network, address = "tcp", u.Host
	}
	c, err := net.DialTimeout(network, address, time.Second)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// dial returns the HTTP client and the base URL that reach the server at addr: unix:///path/to/djinn.sock, or
// an http URL whose token parameter, if any, goes in an "Authorization: Bearer" header instead of the URL.
func dial(addr string) (connect.HTTPClient, string, error) {
	if socket, ok := strings.CutPrefix(addr, "unix://"); ok {
		if ui.InTest() && ui.IsRealHome(filepath.Dir(socket)) {
			return nil, "", fmt.Errorf("refusing to dial real data directory (%s) under test: without an address and a data folder given by the test harness, start a Djinn of your own (a DJINN_HOME of its own, djinn up on its own socket)", filepath.Dir(socket))
		}
		var d net.Dialer
		transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return d.DialContext(ctx, "unix", socket)
		}}
		// The host is never resolved: every connection goes to the socket. HTTP/1.1, although the socket also
		// speaks h2c: it is faster for a call and a server stream (docs/transport.md). A bidirectional stream will
		// need h2c.
		return &http.Client{Transport: workerHeaders{transport}}, "http://djinn", nil
	}
	u, err := url.Parse(addr)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, "", usageError{fmt.Errorf("address %q: expected unix:///path/to/djinn.sock or http://host:port", addr)}
	}
	token := u.Query().Get("token")
	base := strings.TrimRight((&url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}).String(), "/")
	if token == "" {
		return &http.Client{Transport: workerHeaders{http.DefaultTransport}}, base, nil
	}
	return &http.Client{Transport: workerHeaders{bearer{token, http.DefaultTransport}}}, base, nil
}

// bearer sends the token of the server with every request.
type bearer struct {
	token string
	base  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(r)
}

// workerHeaders sends the worker token if set in the environment, and who runs the command (harness.CallerHeader):
// the developer's own commands keep their ifrits lit.
type workerHeaders struct {
	base http.RoundTripper
}

func (w workerHeaders) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	token := os.Getenv("DJINN_WORKER_TOKEN")
	if token != "" {
		r.Header.Set("X-Djinn-Worker-Token", token)
	}
	r.Header.Set("X-Djinn-Caller", caller(token))
	return w.base.RoundTrip(r)
}

// caller is who runs this command, as harness.Touching reads it: the lead in its terminal ($DJINN_LEAD=1), a process
// Djinn started (a worker's token, a watcher's $DJINN_TASK_ID, a check's $DJINN_CALLER), else the developer.
func caller(token string) string {
	switch {
	case os.Getenv("DJINN_LEAD") == "1":
		return "lead"
	case token != "" || os.Getenv("DJINN_TASK_ID") != "" || os.Getenv("DJINN_CALLER") == "djinn":
		return "djinn"
	}
	return "you"
}

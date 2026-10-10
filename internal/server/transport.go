package server

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// Transport is how the native window reaches the server.
type Transport string

const (
	// Wails serves the window through the Wails internal asset server (wails://): no port, no token, since only
	// the webview reaches it. The command line joins the server over a Unix socket.
	Wails Transport = "wails"
	// HTTP serves the window, a browser and the command line on the loopback, guarded by a token.
	HTTP Transport = "http"
)

// TransportFor returns the transport of the native window on goos (a runtime.GOOS value). WebView2, on Windows,
// only hands a custom scheme response to the page once it is complete, so a stream would arrive in one burst:
// Windows goes through loopback HTTP. WebKit, on macOS and Linux, streams it.
func TransportFor(goos string) Transport {
	if goos == "windows" {
		return HTTP
	}
	return Wails
}

const (
	// SocketFile is the Unix socket the command line joins, in the data directory.
	SocketFile = "djinn.sock"
	// AddrFile holds the address of the running server, in the data directory: unix:///…/djinn.sock, or
	// http://127.0.0.1:PORT/?token=… whose token the command line sends as a bearer token.
	AddrFile = "server.addr"
)

// ListenUnix listens on the Unix socket at path, readable and writable by the owner only. A socket left by a
// crashed server is replaced; a socket that answers means djinn is already running.
func ListenUnix(path string) (net.Listener, error) {
	// sun_path holds 104 bytes on macOS, 108 on Linux, with the final zero.
	if len(path) > 103 {
		return nil, fmt.Errorf("socket path too long (%d bytes, at most 103): set DJINN_HOME to a shorter directory: %s",
			len(path), path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if c, err := net.Dial("unix", path); err == nil {
		c.Close()
		return nil, fmt.Errorf("djinn is already running: %s answers", path)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	return ln, nil
}

// WriteAddr writes addr to the address file of home, readable by the owner only. The returned function removes
// it, unless another server has written its own address since.
func WriteAddr(home, addr string) (remove func(), err error) {
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(home, AddrFile)
	// Remove then create: the permissions of a new file are the ones we ask for, whatever the old file had.
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	if _, err := f.WriteString(addr + "\n"); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	return func() {
		if b, err := os.ReadFile(path); err == nil && bytes.Equal(bytes.TrimSpace(b), []byte(addr)) {
			os.Remove(path)
		}
	}, nil
}

// ReadAddr returns the address the running server wrote in home.
func ReadAddr(home string) (string, error) {
	path := filepath.Join(home, AddrFile)
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("djinn is not running: start it with djinn up, or pass --addr (no %s)", path)
	}
	if err != nil {
		return "", err
	}
	addr := strings.TrimSpace(string(b))
	if addr == "" {
		return "", fmt.Errorf("%s is empty", path)
	}
	return addr, nil
}

package plan

import (
	"bufio"
	"context"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// remoteOf returns the URL of the Git remote of dir, origin or else the first one, without credentials; empty
// outside Git, without a remote, or without git on this machine.
func remoteOf(dir string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "remote").Output()
	if err != nil {
		return ""
	}
	names := strings.Fields(string(out))
	if len(names) == 0 {
		return ""
	}
	name := names[0]
	for _, n := range names {
		if n == "origin" {
			name = n
		}
	}
	out, err = exec.CommandContext(ctx, "git", "-C", dir, "remote", "get-url", name).Output()
	if err != nil {
		return ""
	}
	return cleanRemote(strings.TrimSpace(string(out)))
}

// cleanRemote removes the credentials a remote URL may hold: the password, and over HTTP the user name too, which
// is often a token. An scp-like address (git@host:path) keeps its user, which names no one.
func cleanRemote(remote string) string {
	if !strings.Contains(remote, "://") {
		return remote
	}
	u, err := url.Parse(remote)
	if err != nil {
		// Unreadable: keep nothing rather than a secret.
		return ""
	}
	if u.User != nil {
		if _, has := u.User.Password(); has || u.Scheme == "http" || u.Scheme == "https" {
			u.User = nil
		}
	}
	return u.String()
}

// remoteKey reduces a remote URL to what identifies the repository, so that its HTTPS and SSH forms match:
// https://github.com/Acme/Api.git and git@github.com:acme/api are both github.com/acme/api.
func remoteKey(remote string) string {
	r := strings.ToLower(strings.TrimSpace(remote))
	if r == "" {
		return ""
	}
	if i := strings.Index(r, "://"); i >= 0 {
		host, path, _ := strings.Cut(r[i+3:], "/")
		if at := strings.LastIndex(host, "@"); at >= 0 {
			host = host[at+1:]
		}
		// A port names a way in, not the repository.
		if h, port, ok := strings.Cut(host, ":"); ok && strings.Trim(port, "0123456789") == "" {
			host = h
		}
		r = host + "/" + path
	} else if colon, slash := strings.Index(r, ":"), strings.Index(r, "/"); colon >= 0 && (slash < 0 || colon < slash) {
		// scp-like: [user@]host:path.
		host := r[:colon]
		if at := strings.LastIndex(host, "@"); at >= 0 {
			host = host[at+1:]
		}
		r = host + "/" + r[colon+1:]
	}
	r = strings.TrimRight(r, "/")
	r = strings.TrimSuffix(r, ".git")
	return strings.TrimRight(r, "/")
}

// sameRemote tells whether two remote URLs name the same repository; an empty one names none.
func sameRemote(a, b string) bool {
	ka, kb := remoteKey(a), remoteKey(b)
	return ka != "" && ka == kb
}

// downloads returns the folder where files the user asked for land: the XDG download folder on Linux, which may
// have a translated name, else Downloads in the home folder, else the home folder itself.
func downloads() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	isDir := func(p string) bool {
		info, err := os.Stat(p)
		return err == nil && info.IsDir()
	}
	if runtime.GOOS != "windows" && runtime.GOOS != "darwin" {
		if dir := xdgDownload(home); dir != "" && isDir(dir) {
			return dir, nil
		}
	}
	if dir := filepath.Join(home, "Downloads"); isDir(dir) {
		return dir, nil
	}
	return home, nil
}

// xdgDownload reads XDG_DOWNLOAD_DIR from user-dirs.dirs, as xdg-user-dir does.
func xdgDownload(home string) string {
	config := os.Getenv("XDG_CONFIG_HOME")
	if config == "" {
		config = filepath.Join(home, ".config")
	}
	f, err := os.Open(filepath.Join(config, "user-dirs.dirs"))
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		value, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "XDG_DOWNLOAD_DIR=")
		if !ok {
			continue
		}
		value = strings.Trim(value, `"`)
		value = strings.Replace(value, "$HOME", home, 1)
		if filepath.IsAbs(value) {
			return value
		}
	}
	return ""
}

// fileName turns a title into a file name that every system accepts: letters and digits kept, the rest a dash.
func fileName(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
		if b.Len() >= 80 {
			break
		}
	}
	name := strings.Trim(b.String(), "-")
	if name == "" {
		name = "wish"
	}
	return name
}

// freePath returns dir/name+ext, or dir/name-2+ext and so on when the file exists: an export never overwrites a
// file it was not asked to.
func freePath(dir, name, ext string) string {
	p := filepath.Join(dir, name+ext)
	for i := 2; ; i++ {
		if _, err := os.Lstat(p); os.IsNotExist(err) {
			return p
		}
		p = filepath.Join(dir, name+"-"+strconv.Itoa(i)+ext)
	}
}

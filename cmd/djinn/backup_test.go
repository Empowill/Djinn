package main

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"

	backupv1 "github.com/empowill/djinn/gen/go/backup/v1"
	"github.com/empowill/djinn/gen/go/backup/v1/backupv1connect"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/server"
	"github.com/empowill/djinn/internal/store"
)

// fakeBackups is the backup service of a running djinn: it writes nothing, and answers with res or err.
type fakeBackups struct {
	res *backupv1.BackupServiceCreateResponse
	err error
}

func (f fakeBackups) Create(context.Context, *connect.Request[backupv1.BackupServiceCreateRequest]) (*connect.Response[backupv1.BackupServiceCreateResponse], error) {
	if f.err != nil {
		return nil, f.err
	}
	return connect.NewResponse(f.res), nil
}

// TestRunBackup covers runBackup, djinn backup and djinn backup restore: a data folder no djinn runs on is backed up,
// then restored over itself; a running djinn writes the backup, unless it is older than backups; the usage.
func TestRunBackup(t *testing.T) {
	written := &backupv1.BackupServiceCreateResponse{File: "/backups/b.zip", Size: 3 << 19, Files: 3}
	for _, c := range []struct {
		name  string
		djinn *fakeBackups // a djinn runs on the data folder
		db    bool         // the data folder holds a database
		args  func(home, archive string) []string
		code  int
		out   []string
	}{
		{name: "no djinn runs", db: true, args: func(_, archive string) []string { return []string{"--file", archive} },
			out: []string{"djinn backup: wrote ", "(2 files"}},
		{name: "restore", db: true, args: func(home, archive string) []string {
			if code := runBackup([]string{"--file=" + archive}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
				t.Fatalf("djinn backup = %d", code)
			}
			return []string{"restore", archive}
		}, out: []string{"1 file restored", "kept in "}},
		{name: "a djinn writes it", djinn: &fakeBackups{res: written}, args: func(string, string) []string { return nil },
			out: []string{"djinn backup: wrote /backups/b.zip (3 files, 1.5 MB)"}},
		{name: "a djinn older than backups", db: true, djinn: &fakeBackups{err: connect.NewError(connect.CodeUnimplemented, errors.New("no"))},
			args: func(_, archive string) []string { return []string{"--file", archive} }, out: []string{"(2 files"}},
		{name: "a djinn fails", djinn: &fakeBackups{err: connect.NewError(connect.CodeInternal, errors.New("disk full"))},
			args: func(string, string) []string { return nil }, code: 1, out: []string{"djinn backup: internal: disk full"}},
		{name: "help", args: func(string, string) []string { return []string{"--help"} }, out: []string{"Usage: djinn backup"}},
		{name: "restore's help", args: func(string, string) []string { return []string{"restore", "-h"} }, out: []string{"Usage: djinn backup restore"}},
		{name: "restore without an archive", args: func(string, string) []string { return []string{"restore"} }, code: 2,
			out: []string{"<archive> is required", "Usage: djinn backup restore"}},
		{name: "restore a missing archive", args: func(_, archive string) []string { return []string{"restore", archive} }, code: 1,
			out: []string{"djinn backup restore: "}},
		{name: "an unknown flag", args: func(string, string) []string { return []string{"--bogus"} }, code: 2, out: []string{"Usage: djinn backup"}},
		{name: "two archives", args: func(string, string) []string { return []string{"restore", "a", "b"} }, code: 2, out: []string{`unexpected argument "b"`}},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "h")
			t.Setenv("DJINN_HOME", home)
			if c.db {
				s, err := store.Open(t.Context(), filepath.Join(home, store.File), plan.Entities()...)
				if err != nil {
					t.Fatal(err)
				}
				s.Close()
			}
			if c.djinn != nil {
				_, h := backupv1connect.NewBackupServiceHandler(*c.djinn)
				srv := httptest.NewServer(h)
				t.Cleanup(srv.Close)
				if _, err := server.WriteAddr(home, srv.URL); err != nil {
					t.Fatal(err)
				}
			}
			archive := filepath.Join(t.TempDir(), "b.zip")
			var out bytes.Buffer
			code := runBackup(c.args(home, archive), &out, &out)
			ok := code == c.code
			for _, want := range c.out {
				ok = ok && strings.Contains(out.String(), want)
			}
			if !ok {
				t.Fatalf("djinn backup %q = %d\n%s", c.args(home, archive), code, out.String())
			}
			if c.db {
				if _, err := os.Stat(filepath.Join(home, store.File)); err != nil {
					t.Error(err)
				}
			}
		})
	}
}

func TestSize(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		n    int64
		want string
	}{
		{"bytes", 512, "512 B"},
		{"kB", 1536, "1.5 kB"},
		{"MB", 5 << 20, "5.0 MB"},
		{"GB", 3 << 29, "1.5 GB"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := size(c.n); got != c.want {
				t.Errorf("size(%d) = %q, want %q", c.n, got, c.want)
			}
		})
	}
}

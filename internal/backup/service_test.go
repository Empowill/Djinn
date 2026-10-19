package backup

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"

	backupv1 "github.com/empowill/djinn/gen/go/backup/v1"
	"github.com/empowill/djinn/gen/go/backup/v1/backupv1connect"
)

// TestService_Create: the djinn that holds the database backs its data folder up into the file asked; an archive
// of an unknown kind is an invalid argument, a data folder gone an internal error.
func TestService_Create(t *testing.T) {
	t.Parallel()
	home, s := newHome(t)
	writeFile(t, home, "state.json", "{}")
	client := func(t *testing.T, home string) backupv1connect.BackupServiceClient {
		_, h := Handler(s, home, "test")
		mux := httptest.NewServer(h)
		t.Cleanup(mux.Close)
		return backupv1connect.NewBackupServiceClient(mux.Client(), mux.URL)
	}
	for _, tc := range []struct {
		name      string
		home      string
		file      string
		wantCode  connect.Code
		wantFiles int32
	}{
		{name: "file asked", home: home, file: "backup.zip", wantFiles: 3},
		{name: "unknown archive kind", home: home, file: "backup.zip.rar", wantCode: connect.CodeInvalidArgument},
		{name: "no data folder", home: filepath.Join(t.TempDir(), "missing"), file: "backup.zip",
			wantCode: connect.CodeInternal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			file := filepath.Join(t.TempDir(), tc.file)
			res, err := client(t, tc.home).Create(context.Background(),
				connect.NewRequest(&backupv1.BackupServiceCreateRequest{File: file}))
			if tc.wantCode != 0 {
				if connect.CodeOf(err) != tc.wantCode {
					t.Errorf("Create = %v, want %v", err, tc.wantCode)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if res.Msg.GetFile() != file || res.Msg.GetFiles() != tc.wantFiles {
				t.Errorf("Create = %v, want %s with %d files", res.Msg, file, tc.wantFiles)
			}
		})
	}
}

package store

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

func TestSnapshot(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	for _, tc := range []struct {
		name      string
		preCreate bool
		wantErr   string
	}{
		{
			name: "consistent copy with uncommitted WAL frames",
		},
		{
			name:      "destination already exists",
			preCreate: true,
			wantErr:   "already exists",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := open(t)
			p := &planv1.Project{Id: NewID(), Name: "alpha", Directory: "/tmp/alpha"}
			if err := put(ctx, s, p); err != nil {
				t.Fatal(err)
			}

			dst := filepath.Join(t.TempDir(), "snap.db")
			if tc.preCreate {
				if err := os.WriteFile(dst, []byte("existing"), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			err := s.Snapshot(ctx, dst)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Snapshot = %v, want error containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Snapshot: %v", err)
			}
			if err := Check(ctx, dst); err != nil {
				t.Fatalf("Check snapshot: %v", err)
			}

			snapStore, err := Open(ctx, dst, &planv1.Project{})
			if err != nil {
				t.Fatalf("Open snapshot: %v", err)
			}
			defer snapStore.Close()
			got, err := Get[*planv1.Project](ctx, snapStore, p.GetId())
			if err != nil {
				t.Fatalf("Get from snapshot: %v", err)
			}
			if got.GetName() != "alpha" {
				t.Errorf("got name %q, want alpha", got.GetName())
			}
		})
	}
}

func TestSnapshotFile(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	src := filepath.Join(t.TempDir(), "src.db")
	s, err := Open(ctx, src, &planv1.Project{})
	if err != nil {
		t.Fatal(err)
	}
	if err := put(ctx, s, &planv1.Project{Id: NewID(), Name: "beta", Directory: "/tmp/beta"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name      string
		src       string
		preCreate bool
		wantErr   string
		wantIs    error // an error the system words its own way: "no such file" on Unix, not on Windows
	}{
		{
			name: "copies closed database",
			src:  src,
		},
		{
			name:   "missing source file",
			src:    filepath.Join(t.TempDir(), "missing.db"),
			wantIs: fs.ErrNotExist,
		},
		{
			name:      "destination already exists",
			src:       src,
			preCreate: true,
			wantErr:   "already exists",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dst := filepath.Join(t.TempDir(), "snap.db")
			if tc.preCreate {
				if err := os.WriteFile(dst, []byte("existing"), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			err := SnapshotFile(ctx, tc.src, dst)
			if tc.wantIs != nil {
				if !errors.Is(err, tc.wantIs) {
					t.Fatalf("SnapshotFile = %v, want %v", err, tc.wantIs)
				}
				return
			}
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("SnapshotFile = %v, want error containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("SnapshotFile: %v", err)
			}
			if err := Check(ctx, dst); err != nil {
				t.Fatalf("Check: %v", err)
			}
		})
	}
}

func TestCheck(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	valid := filepath.Join(t.TempDir(), "valid.db")
	s := open(t)
	if err := s.Snapshot(ctx, valid); err != nil {
		t.Fatal(err)
	}
	corrupt := filepath.Join(t.TempDir(), "corrupt.db")
	if err := os.WriteFile(corrupt, []byte("not a sqlite database at all"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name    string
		path    string
		wantErr bool
	}{
		{
			name: "valid database",
			path: valid,
		},
		{
			name:    "missing file",
			path:    filepath.Join(t.TempDir(), "missing.db"),
			wantErr: true,
		},
		{
			name:    "corrupt file",
			path:    corrupt,
			wantErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := Check(ctx, tc.path)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Check = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

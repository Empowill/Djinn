package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"connectrpc.com/connect"

	backupv1 "github.com/empowill/djinn/gen/go/backup/v1"
	"github.com/empowill/djinn/gen/go/backup/v1/backupv1connect"
	"github.com/empowill/djinn/internal/backup"
	"github.com/empowill/djinn/internal/cli"
	"github.com/empowill/djinn/internal/server"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/ui"
)

// runBackup is djinn backup and djinn backup restore. Restoring runs while no djinn does, so neither is generated
// from the protos; a backup asks the running djinn, which holds the database, and copies it itself otherwise.
// Their help is cli.Backup and cli.Restore. It returns the exit code.
func runBackup(args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	home, err := ui.Home()
	if err != nil {
		fmt.Fprintln(stderr, "djinn backup:", err)
		return 1
	}
	var file string
	restore := false
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "-h" || a == "--help":
			backupHelp(restore).WriteHelp(stdout)
			return 0
		case i == 0 && a == "restore":
			restore = true
		case a == "--file" && !restore && i+1 < len(args):
			i++
			file = args[i]
		case strings.HasPrefix(a, "--file=") && !restore:
			file = strings.TrimPrefix(a, "--file=")
		case restore && file == "" && !strings.HasPrefix(a, "-"):
			file = a
		default:
			fmt.Fprintf(stderr, "error: unexpected argument %q\n", a)
			backupHelp(restore).WriteHelp(stderr)
			return 2
		}
	}
	if restore {
		if file == "" {
			fmt.Fprintln(stderr, "error: <archive> is required")
			cli.Restore.WriteHelp(stderr)
			return 2
		}
		return runRestore(ctx, home, file, stdout, stderr)
	}
	if file != "" {
		if file, err = filepath.Abs(file); err != nil {
			fmt.Fprintln(stderr, "djinn backup:", err)
			return 1
		}
	}
	res, err := createBackup(ctx, home, file)
	if err != nil {
		fmt.Fprintln(stderr, "djinn backup:", err)
		return 1
	}
	fmt.Fprintf(stdout, "djinn backup: wrote %s (%s, %s)\n", res.GetFile(), files(int(res.GetFiles())), size(res.GetSize()))
	return 0
}

// backupHelp is the help of djinn backup, or of djinn backup restore.
func backupHelp(restore bool) cli.Command {
	if restore {
		return cli.Restore
	}
	return cli.Backup
}

// createBackup asks the djinn running on home to write the backup; without one, or with a djinn older than
// backups, it copies the database itself, which is safe while another process writes it.
func createBackup(ctx context.Context, home, file string) (*backupv1.BackupServiceCreateResponse, error) {
	if addr, err := server.ReadAddr(home); err == nil && cli.Alive(addr) {
		client, base, err := cli.Dial(addr)
		if err != nil {
			return nil, err
		}
		res, err := backupv1connect.NewBackupServiceClient(client, base).Create(ctx,
			connect.NewRequest(&backupv1.BackupServiceCreateRequest{File: file}))
		if err == nil {
			return res.Msg, nil
		}
		if connect.CodeOf(err) != connect.CodeUnimplemented {
			return nil, err
		}
	}
	if file == "" {
		var err error
		if file, err = backup.DefaultFile(time.Now()); err != nil {
			return nil, err
		}
	}
	db := filepath.Join(home, store.File)
	return backup.Create(ctx, home, file, version, func(ctx context.Context, dst string) error {
		return store.SnapshotFile(ctx, db, dst)
	})
}

func runRestore(ctx context.Context, home, archive string, stdout, stderr io.Writer) int {
	res, err := backup.Restore(ctx, home, archive, time.Now())
	if res == nil {
		fmt.Fprintln(stderr, "djinn backup restore:", err)
		return 1
	}
	fmt.Fprintf(stdout, "djinn backup restore: %s restored in %s\n", files(res.Files), res.Home)
	if res.Aside != "" {
		fmt.Fprintf(stdout, "  the previous data folder is kept in %s: delete it once all is well\n", res.Aside)
	}
	for _, name := range res.Detached {
		fmt.Fprintf(stdout, "  project %s: its folder is not on this machine; djinn project add <folder> attaches it\n", name)
	}
	if err != nil {
		fmt.Fprintln(stderr, "djinn backup restore:", err)
		return 1
	}
	return 0
}

// files writes a number of files.
func files(n int) string {
	if n == 1 {
		return "1 file"
	}
	return fmt.Sprintf("%d files", n)
}

// size writes a number of bytes for a person.
func size(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f kB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

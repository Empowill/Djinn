package gate

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestRun covers Run: holding the gate while the command runs, waiting under pressure, releasing when the command fails
// or its caller disconnects, recording CPU/duration/peak-memory costs, and recording the git tree when the worktree is
// unchanged.
func TestRun(t *testing.T) {
	// djinn gate run holds the gate while the command runs, says why it waits, and gives the gate back when the
	// command fails, cannot start, or its caller goes away.
	t.Run("waits under pressure, runs command, and releases on disconnect", func(t *testing.T) {
		p := calm()
		p.squeeze(true)
		g := New(p, nil, nil)
		g.tick = 10 * time.Millisecond
		client := server(t, g)
		t.Setenv(helperEnv, "exit3")
		var out, notice bytes.Buffer
		go func() { time.Sleep(100 * time.Millisecond); p.squeeze(false) }()
		code, err := Run(t.Context(), client, "codegen", "", Command{Args: []string{os.Args[0]}, Stdout: &out, Stderr: &out, Notice: &notice})
		if err != nil || code != 3 || out.String() != "working\n" {
			t.Errorf("run = %d, %v, output %q", code, err, out.String())
		}
		if !strings.Contains(notice.String(), "djinn: gate codegen: waiting: "+squeezed) {
			t.Errorf("notice = %q", notice.String())
		}
		waitFree(t, g)

		if _, err := Run(t.Context(), client, "codegen", "", Command{Args: []string{"djinn-no-such-command"}, Notice: &notice}); err == nil {
			t.Error("ran a command that does not exist")
		}
		waitFree(t, g)

		// The caller goes away while the command runs: the server gives the gate back.
		t.Setenv(helperEnv, "sleep")
		ctx, cancel := context.WithCancel(t.Context())
		stream, err := client.Hold(ctx, connectRequest("codegen"))
		if err != nil {
			t.Fatal(err)
		}
		if !stream.Receive() {
			t.Fatal(stream.Err())
		}
		if l := g.List(); len(l) != 1 || l[0].Holder == "" {
			t.Fatalf("held = %+v", l)
		}
		cancel()
		waitFree(t, g)
	})

	// djinn gate run measures what the command cost, its children included, and records it for its task; not when it
	// was interrupted.
	t.Run("records CPU, duration, and peak memory cost, not when interrupted", func(t *testing.T) {
		client := server(t, New(nil, nil, nil))
		rec := &costs{}
		t.Setenv(helperEnv, "burn")
		var notice bytes.Buffer
		code, err := Run(t.Context(), client, "codegen", "", Command{Args: []string{os.Args[0], "-test.run=^$"}, Notice: &notice, Costs: rec})
		if err != nil || code != 0 || len(rec.got) != 1 {
			t.Fatalf("run = %d, %v, recorded %v, notice %q", code, err, rec.got, notice.String())
		}
		got := rec.got[0]
		dir, _ := os.Getwd()
		if got.GetCommand() != os.Args[0]+" -test.run=^$" || got.GetDirectory() != dir || got.GetExitCode() != 0 {
			t.Errorf("recorded %v", got)
		}
		if got.GetCpuSeconds() < 0.1 || got.GetSeconds() < 0.3 || got.GetSeconds() < got.GetCpuSeconds()/float64(runtime.NumCPU()) {
			t.Errorf("CPU %.3f s, duration %.3f s", got.GetCpuSeconds(), got.GetSeconds())
		}
		switch peak := got.GetPeakMemoryBytes(); runtime.GOOS {
		case "linux", "darwin", "windows": // Windows through a Job Object (watch_windows.go)
			if peak < 64<<20 {
				t.Errorf("peak memory %d", peak)
			}
		default:
			if peak != 0 {
				t.Errorf("peak memory %d where the system does not give it", peak)
			}
		}

		// Interrupted (Ctrl-C reaches the command and djinn alike): nothing recorded, even if the command ends well.
		t.Setenv(helperEnv, "nap")
		ctx, cancel := context.WithCancel(t.Context())
		time.AfterFunc(100*time.Millisecond, cancel)
		if code, err := Run(ctx, client, "codegen", "", Command{Args: []string{os.Args[0]}, Notice: &notice, Costs: rec}); code != 0 || err != nil {
			t.Errorf("interrupted run = %d, %v", code, err)
		}
		if len(rec.got) != 1 {
			t.Errorf("an interrupted command recorded: %v", rec.got[1:])
		}
	})

	// djinn gate run records the git tree its command ran on, the worktree's changes and files not tracked included,
	// its index left as it was; none when the command changed the worktree, or ran outside any.
	t.Run("records git tree when worktree unchanged", func(t *testing.T) {
		client := server(t, New(nil, nil, nil))
		repo := t.TempDir()
		gitT(t, repo, "init", "--quiet")
		if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("ignored.txt\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, "ignored.txt"), []byte("i\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		run := func(helper, dir string) string {
			t.Helper()
			t.Setenv(helperEnv, helper)
			rec := &costs{}
			var notice bytes.Buffer
			code, err := Run(t.Context(), client, "lint", "", Command{Args: []string{os.Args[0]}, Dir: dir, Notice: &notice, Costs: rec})
			if err != nil || len(rec.got) != 1 {
				t.Fatalf("run %s = %d, %v, recorded %v, notice %q", helper, code, err, rec.got, notice.String())
			}
			return rec.got[0].GetTree()
		}

		tree := run("exit3", repo) // Its exit code changes nothing to the tree it ran on.
		if tree == "" {
			t.Fatal("no tree recorded for a command run in a worktree")
		}
		if files := gitT(t, repo, "ls-tree", "--name-only", tree); files != ".gitignore\na.txt" {
			t.Errorf("the tree holds %q; want the files not tracked, the ignored one left out", files)
		}
		if status := gitT(t, repo, "status", "--porcelain"); status != "?? .gitignore\n?? a.txt" {
			t.Errorf("the worktree's index changed: %q", status)
		}
		// Committed as it stands, the worktree's tree is the one recorded.
		gitT(t, repo, "add", "--all")
		if got := gitT(t, repo, "write-tree"); got != tree {
			t.Errorf("tree %s; the worktree committed holds %s", tree, got)
		}

		if tree := run("write", repo); tree != "" {
			t.Errorf("a command that changed the worktree recorded the tree %s", tree)
		}
		if tree := run("exit3", t.TempDir()); tree != "" {
			t.Errorf("a command outside any worktree recorded the tree %s", tree)
		}
	})
}

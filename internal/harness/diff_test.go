package harness

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/cli"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

func TestDiff(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	ctx := t.Context()

	// 1. Set up a real git repo with base files.
	repo := gitRepo(t)
	mainGo := filepath.Join(repo, "main.go")
	if err := os.WriteFile(mainGo, []byte("package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldGo := filepath.Join(repo, "old.go")
	if err := os.WriteFile(oldGo, []byte("package main\n\nfunc Old() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"add", "."},
		{"commit", "--quiet", "-m", "Base commit"},
	} {
		if _, err := git(ctx, repo, args...); err != nil {
			t.Fatal(err)
		}
	}

	baseBranch := plan.CheckedOutBranch(ctx, repo)

	// 2. Create task branch with added, modified, renamed, generated, lock, binary, and large files.
	branch := "djinn/W1-diff-task"
	if _, err := git(ctx, repo, "checkout", "-b", branch); err != nil {
		t.Fatal(err)
	}

	// Added file
	addedGo := filepath.Join(repo, "added.go")
	if err := os.WriteFile(addedGo, []byte("package main\n\nfunc Added() string {\n\treturn \"added\"\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Modified file
	if err := os.WriteFile(mainGo, []byte("package main\n\nfunc main() {\n\tprintln(\"modified world\")\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Renamed file
	if _, err := git(ctx, repo, "mv", "old.go", "renamed.go"); err != nil {
		t.Fatal(err)
	}

	// Generated file (under gen/)
	genDir := filepath.Join(repo, "gen")
	if err := os.MkdirAll(genDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(genDir, "client.go"), []byte("package gen\n\n// Code generated. DO NOT EDIT.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Lock file
	if err := os.WriteFile(filepath.Join(repo, "package-lock.json"), []byte("{\n  \"name\": \"app\",\n  \"lockfileVersion\": 3\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Binary file
	if err := os.WriteFile(filepath.Join(repo, "image.png"), []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00}, 0o600); err != nil {
		t.Fatal(err)
	}

	// Large file (2,500 lines)
	var largeBuf bytes.Buffer
	for i := 1; i <= 2500; i++ {
		fmt.Fprintf(&largeBuf, "Line %d of a very large diff\n", i)
	}
	if err := os.WriteFile(filepath.Join(repo, "large.txt"), largeBuf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{
		{"add", "."},
		{"commit", "--quiet", "-m", "Task changes"},
	} {
		if _, err := git(ctx, repo, args...); err != nil {
			t.Fatal(err)
		}
	}

	// Switch back to base branch
	if _, err := git(ctx, repo, "checkout", baseBranch); err != nil {
		t.Fatal(err)
	}

	// 3. Start test harness environment
	e := up(t, t.TempDir())
	wishID, projectID := e.wish(t, repo)

	taskID := "01a12863-c9d3-7852-99ce-ac3cff3fae5c"
	err := e.db.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal("local", "test", &planv1.Task{}); err != nil {
			return err
		}
		return tx.Put(&planv1.Task{
			Id:         taskID,
			WishId:     wishID,
			ProjectId:  projectID,
			Code:       "W1",
			Title:      "Diff inspection task",
			Branch:     branch,
			Status:     planv1.TaskStatus_TASK_STATUS_DONE,
			CreateTime: timestamppb.Now(),
		})
	})
	if err != nil {
		t.Fatal(err)
	}

	// 4. Test TaskService.Diff file list (req.File == "")
	diffReq := &planv1.TaskServiceDiffRequest{
		Task:   &planv1.TaskRef{Ref: &planv1.TaskRef_Code{Code: "W1"}},
		WishId: wishID,
	}
	res, err := e.tasks.Diff(ctx, connect.NewRequest(diffReq))
	if err != nil {
		t.Fatalf("TaskService.Diff files list failed: %v", err)
	}
	files := res.Msg.GetFiles()
	if len(files) == 0 {
		t.Fatalf("expected changed files, got 0")
	}

	byPath := make(map[string]*planv1.TaskDiffFile)
	for _, f := range files {
		byPath[f.GetPath()] = f
	}

	// Verify added file
	if f, ok := byPath["added.go"]; !ok {
		t.Errorf("missing added.go")
	} else {
		if f.GetStatus() != planv1.DiffStatus_DIFF_STATUS_ADDED {
			t.Errorf("added.go status = %v, want ADDED", f.GetStatus())
		}
		if f.GetAdded() == 0 {
			t.Errorf("added.go additions = 0, want > 0")
		}
	}

	// Verify modified file
	if f, ok := byPath["main.go"]; !ok {
		t.Errorf("missing main.go")
	} else {
		if f.GetStatus() != planv1.DiffStatus_DIFF_STATUS_MODIFIED {
			t.Errorf("main.go status = %v, want MODIFIED", f.GetStatus())
		}
		if f.GetAdded() == 0 || f.GetDeleted() == 0 {
			t.Errorf("main.go additions/deletions = +%d/-%d, want both > 0", f.GetAdded(), f.GetDeleted())
		}
	}

	// Verify renamed file
	if f, ok := byPath["renamed.go"]; !ok {
		t.Errorf("missing renamed.go")
	} else {
		if f.GetStatus() != planv1.DiffStatus_DIFF_STATUS_RENAMED {
			t.Errorf("renamed.go status = %v, want RENAMED", f.GetStatus())
		}
		if f.GetOldPath() != "old.go" {
			t.Errorf("renamed.go old_path = %q, want old.go", f.GetOldPath())
		}
	}

	// Verify generated file
	if f, ok := byPath["gen/client.go"]; !ok {
		t.Errorf("missing gen/client.go")
	} else {
		if !f.GetGenerated() {
			t.Errorf("gen/client.go generated = false, want true")
		}
		if !f.GetFolded() {
			t.Errorf("gen/client.go folded = false, want true")
		}
	}

	// Verify lock file
	if f, ok := byPath["package-lock.json"]; !ok {
		t.Errorf("missing package-lock.json")
	} else {
		if !f.GetLock() {
			t.Errorf("package-lock.json lock = false, want true")
		}
		if !f.GetFolded() {
			t.Errorf("package-lock.json folded = false, want true")
		}
	}

	// Verify binary file
	if f, ok := byPath["image.png"]; !ok {
		t.Errorf("missing image.png")
	} else {
		if !f.GetBinary() {
			t.Errorf("image.png binary = false, want true")
		}
		if !f.GetFolded() {
			t.Errorf("image.png folded = false, want true")
		}
	}

	// 5. Test single file diff for main.go (with Chroma tokens)
	fileDiffReq := &planv1.TaskServiceDiffRequest{
		Task:   &planv1.TaskRef{Ref: &planv1.TaskRef_Code{Code: "W1"}},
		File:   "main.go",
		WishId: wishID,
	}
	resFile, err := e.tasks.Diff(ctx, connect.NewRequest(fileDiffReq))
	if err != nil {
		t.Fatalf("TaskService.Diff single file failed: %v", err)
	}
	d := resFile.Msg.GetFile()
	if d == nil {
		t.Fatalf("expected Diff message, got nil")
	}
	if d.GetPath() != "main.go" {
		t.Errorf("diff path = %q, want main.go", d.GetPath())
	}
	if len(d.GetHunks()) == 0 {
		t.Fatalf("expected hunks in main.go, got 0")
	}
	hasTokens := false
	for _, h := range d.GetHunks() {
		for _, l := range h.GetLines() {
			if len(l.GetTokens()) > 0 {
				hasTokens = true
				break
			}
		}
	}
	if !hasTokens {
		t.Errorf("expected syntax tokens in main.go diff lines")
	}

	// 6. Test large diff truncation for large.txt
	largeDiffReq := &planv1.TaskServiceDiffRequest{
		Task:   &planv1.TaskRef{Ref: &planv1.TaskRef_Code{Code: "W1"}},
		File:   "large.txt",
		WishId: wishID,
	}
	resLarge, err := e.tasks.Diff(ctx, connect.NewRequest(largeDiffReq))
	if err != nil {
		t.Fatalf("TaskService.Diff large file failed: %v", err)
	}
	dLarge := resLarge.Msg.GetFile()
	if dLarge == nil {
		t.Fatalf("expected Diff for large.txt")
	}
	if !dLarge.GetTooLarge() {
		t.Errorf("large.txt too_large = false, want true")
	}
	if dLarge.GetTotalLines() < 2000 {
		t.Errorf("large.txt total_lines = %d, want >= 2000", dLarge.GetTotalLines())
	}
	if !strings.Contains(dLarge.GetCommand(), "git diff") {
		t.Errorf("large.txt command = %q, want containing git diff", dLarge.GetCommand())
	}

	// 7. Test CLI: djinn task diff W1 and djinn task diff W1 --file main.go
	var stdout, stderr bytes.Buffer
	cliCfg := cli.Config{
		Version: "test",
		Addr:    e.srv.URL,
		HTTP:    e.srv.Client(),
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	code := cli.Run(ctx, []string{"task", "diff", "W1", "--wish-id", wishID}, cliCfg)
	if code != 0 {
		t.Fatalf("cli task diff W1 exited %d: %s", code, stderr.String())
	}
	outStr := stdout.String()
	if !strings.Contains(outStr, "main.go") || !strings.Contains(outStr, "added.go") {
		t.Errorf("cli task diff W1 stdout missing files: %s", outStr)
	}

	stdout.Reset()
	stderr.Reset()
	code = cli.Run(ctx, []string{"task", "diff", "W1", "--file", "main.go", "--wish-id", wishID}, cliCfg)
	if code != 0 {
		t.Fatalf("cli task diff W1 --file main.go exited %d: %s", code, stderr.String())
	}
	fileOutStr := stdout.String()
	if !strings.Contains(fileOutStr, "main.go") {
		t.Errorf("cli task diff W1 --file stdout missing main.go: %s", fileOutStr)
	}

	// 8. Test integrated task diff (from integration.sha against sha^1)
	mergeCommitSha, err := git(ctx, repo, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	mergeCommitSha = strings.TrimSpace(mergeCommitSha)

	err = e.db.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal("local", "test", &planv1.Task{}); err != nil {
			return err
		}
		return tx.Put(&planv1.Task{
			Id:        taskID,
			WishId:    wishID,
			ProjectId: projectID,
			Code:      "W1",
			Title:     "Integrated diff task",
			Branch:    branch,
			Status:    planv1.TaskStatus_TASK_STATUS_DONE,
			Integration: &planv1.TaskIntegration{
				Sha: mergeCommitSha,
			},
			CreateTime: timestamppb.Now(),
		})
	})
	if err != nil {
		t.Fatal(err)
	}

	resIntegrated, err := e.tasks.Diff(ctx, connect.NewRequest(diffReq))
	if err != nil {
		t.Fatalf("integrated task diff failed: %v", err)
	}
	if len(resIntegrated.Msg.GetFiles()) == 0 {
		t.Errorf("integrated task diff expected changed files, got 0")
	}
}

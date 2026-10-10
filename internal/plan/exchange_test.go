package plan

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/store"
)

// resolvedTempDir is a temporary folder named as djinn stores a project's once added: Windows names the temporary
// folder with a short 8.3 name (RUNNER~1), which djinn resolves to the long one.
func resolvedTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// gitRepo makes a Git repository in a new folder, with remote as its origin.
func gitRepo(t *testing.T, name, remote string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := filepath.Join(resolvedTempDir(t), name)
	for _, args := range [][]string{{"init", "-q", dir}, {"-C", dir, "remote", "add", "origin", remote}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func TestRemote(t *testing.T) {
	for in, want := range map[string]string{
		"https://x-access-token:s3cret@github.com/Acme/Api.git": "https://github.com/Acme/Api.git",
		"https://s3cret@github.com/Acme/Api":                    "https://github.com/Acme/Api",
		"ssh://git:s3cret@host:2222/acme/api":                   "ssh://host:2222/acme/api",
		"ssh://git@host/acme/api":                               "ssh://git@host/acme/api",
		"git@github.com:Acme/Api.git":                           "git@github.com:Acme/Api.git",
	} {
		if got := cleanRemote(in); got != want {
			t.Errorf("cleanRemote(%q) = %q, want %q", in, got, want)
		}
	}
	same := []string{
		"https://github.com/Acme/Api.git", "git@github.com:acme/api", "ssh://git@github.com:22/Acme/Api/",
		"https://github.com/acme/api",
	}
	for _, r := range same {
		if !sameRemote(same[0], r) {
			t.Errorf("%s and %s should name the same repository", same[0], r)
		}
	}
	if sameRemote("https://github.com/acme/api", "https://github.com/acme/web") || sameRemote("", "") {
		t.Error("different or empty remotes should not match")
	}
}

func TestBlocks(t *testing.T) {
	ctx := t.Context()
	c := serve(t)
	wish := c.wish(t)
	put := func(req *planv1.BlockServicePutRequest) *planv1.Block {
		t.Helper()
		res, err := c.blocks.Put(ctx, connect.NewRequest(req))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg.GetBlock()
	}
	first := put(&planv1.BlockServicePutRequest{WishId: wish, Kind: "section", Title: "Why", Content: "# Why"})
	second := put(&planv1.BlockServicePutRequest{WishId: wish, Kind: "log", Title: "Started"})
	between := put(&planv1.BlockServicePutRequest{WishId: wish, Kind: "Diagram", Content: "{}", MediaType: "application/json", Position: 1500})
	if first.GetPosition() != 1000 || second.GetPosition() != 2000 {
		t.Errorf("positions = %d, %d; want 1000, 2000", first.GetPosition(), second.GetPosition())
	}
	changed := put(&planv1.BlockServicePutRequest{WishId: wish, Id: first.GetId(), Content: "# Why not"})
	if changed.GetTitle() != "Why" || changed.GetContent() != "# Why not" || changed.GetKind() != "section" {
		t.Errorf("a change keeps the fields not given: %v", changed)
	}
	list := func(kind string) []string {
		res, err := c.blocks.List(ctx, connect.NewRequest(&planv1.BlockServiceListRequest{WishId: wish, Kind: kind}))
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, b := range res.Msg.GetBlocks() {
			ids = append(ids, b.GetId())
		}
		return ids
	}
	if got := list(""); strings.Join(got, ",") != strings.Join([]string{first.GetId(), between.GetId(), second.GetId()}, ",") {
		t.Errorf("blocks in order = %v", got)
	}
	if got := list("diagram"); len(got) != 1 || got[0] != between.GetId() {
		t.Errorf("blocks of a kind, case ignored = %v", got)
	}
	if _, err := c.blocks.Put(ctx, connect.NewRequest(&planv1.BlockServicePutRequest{WishId: c.wish(t), Id: first.GetId()})); code(err) != connect.CodeInvalidArgument {
		t.Errorf("a block moved to another wish: %v, want invalid_argument", err)
	}
	if _, err := c.blocks.Delete(ctx, connect.NewRequest(&planv1.BlockServiceDeleteRequest{Id: second.GetId()})); err != nil {
		t.Fatal(err)
	}
	if got := list(""); len(got) != 2 {
		t.Errorf("after a delete: %v", got)
	}
	if _, err := c.blocks.Delete(ctx, connect.NewRequest(&planv1.BlockServiceDeleteRequest{Id: second.GetId()})); code(err) != connect.CodeNotFound {
		t.Errorf("deleting twice: %v, want not_found", err)
	}
}

// source builds a wish worth exporting: two projects, one a Git repository whose remote holds a token, a
// question answered by code, blocks, and a task whose worker left local traces.
func source(t *testing.T) (clients, *planv1.Wish, string) {
	t.Helper()
	ctx := t.Context()
	c := serve(t)
	repo := gitRepo(t, "api", "https://x-access-token:s3cret@github.com/Acme/Api.git")
	notes := filepath.Join(t.TempDir(), "notes")
	if err := os.MkdirAll(notes, 0o700); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, dir := range []string{repo, notes} {
		res, err := c.projects.Add(ctx, connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: dir}))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, res.Msg.GetProject().GetId())
	}
	made, err := c.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{Title: "Ship the API", ProjectIds: ids}))
	if err != nil {
		t.Fatal(err)
	}
	wish := made.Msg.GetWish()
	asked, err := c.questions.Ask(ctx, connect.NewRequest(&planv1.QuestionServiceAskRequest{
		Text: "Which store?", Options: []string{"SQLite", "Files"}, WishId: wish.GetId(),
		Context: "It decides the backups.", Recommendation: "A: one file, one transaction.",
	}))
	if err != nil {
		t.Fatal(err)
	}
	c.ask(t, wish.GetId())
	_, err = c.questions.Answer(ctx, connect.NewRequest(&planv1.QuestionServiceAnswerRequest{
		Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: asked.Msg.GetQuestion().GetCode()}}, Choice: planv1.Choice_CHOICE_A,
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range []*planv1.BlockServicePutRequest{
		{WishId: wish.GetId(), Kind: "decision", Title: "No CGO", Content: "Pure Go only."},
		{WishId: wish.GetId(), Kind: "log", Title: "23:10", Content: "Go."},
	} {
		if _, err := c.blocks.Put(ctx, connect.NewRequest(b)); err != nil {
			t.Fatal(err)
		}
	}
	// A task as the harness writes it, with what only makes sense on this machine.
	task := &planv1.Task{
		Id: store.NewID(), WishId: wish.GetId(), ProjectId: ids[0], Code: "W1", Title: "Write the store",
		Status: planv1.TaskStatus_TASK_STATUS_RUNNING, Provider: planv1.Provider_PROVIDER_CLAUDE,
		Worktree: filepath.Join(repo, ".worktrees", "w1"), Branch: "djinn/w1", SessionId: "session-42",
		CreateTime: timestamppb.Now(), StartTime: timestamppb.Now(),
	}
	err = c.store.Tx(ctx, func(tx *store.Tx) error {
		spawn := &planv1.TaskServiceSpawnRequest{WishId: wish.GetId(), Title: task.GetTitle(), Prompt: "Work in " + repo + "/internal"}
		if err := tx.Journal(actor, planv1connect.TaskServiceSpawnProcedure, spawn); err != nil {
			return err
		}
		if err := tx.Put(task); err != nil {
			return err
		}
		for i, e := range []*planv1.TaskEvent{
			{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_TEXT, Text: "Reading " + repo + "/go.mod"},
			{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_CALL, Text: "Write {\"content\":\"package store\"}\nfunc secretCode() {}"},
			{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_RESULT, Text: "package store // the code itself", Raw: `{"raw":"line"}`},
		} {
			e.Id, e.TaskId, e.Seq, e.CreateTime = store.NewID(), task.GetId(), int64(i+1), timestamppb.Now()
			if err := tx.Put(e); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return c, wish, repo
}

func export(t *testing.T, c clients, wishID, file string) []byte {
	t.Helper()
	res, err := c.wishes.Export(t.Context(), connect.NewRequest(&planv1.WishServiceExportRequest{WishId: wishID, File: file}))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(res.Msg.GetFile())
	if err != nil || int64(len(data)) != res.Msg.GetSize() {
		t.Fatalf("read the export: %v (%d bytes, %d said)", err, len(data), res.Msg.GetSize())
	}
	return data
}

func TestExportImport(t *testing.T) {
	ctx := t.Context()
	src, wish, repo := source(t)
	dir := t.TempDir()
	data := export(t, src, wish.GetId(), filepath.Join(dir, "wish.djinn"))
	asJSON := export(t, src, wish.GetId(), filepath.Join(dir, "wish.json"))

	// Nothing of this machine leaves it: no folder, no token, no session, no code.
	home, _ := os.UserHomeDir()
	for _, file := range [][]byte{data, asJSON} {
		for _, leak := range []string{repo, filepath.Dir(repo), home, "s3cret", "session-42", "secretCode", "package store", "the code itself", `"raw"`} {
			if bytes.Contains(file, []byte(leak)) {
				t.Errorf("the export holds %q", leak)
			}
		}
	}
	if !bytes.HasPrefix(bytes.TrimSpace(asJSON), []byte("{")) || !bytes.Contains(asJSON, []byte(`"Which store?"`)) {
		t.Errorf("the JSON export is not readable JSON:\n%.300s", asJSON)
	}
	exp, err := decode(data)
	if err != nil {
		t.Fatal(err)
	}
	methods := map[string]int{}
	for _, cmd := range exp.GetCommands() {
		methods[cmd.GetMethod()]++
	}
	for _, m := range []string{
		planv1connect.WishServiceMakeProcedure, planv1connect.QuestionServiceAskProcedure,
		planv1connect.QuestionServiceAnswerProcedure, planv1connect.BlockServicePutProcedure,
		planv1connect.TaskServiceSpawnProcedure,
	} {
		if methods[m] == 0 {
			t.Errorf("the journal of the export lacks %s: %v", m, methods)
		}
	}
	if methods[planv1connect.ProjectServiceAddProcedure] != 0 {
		t.Error("the export holds the commands that added the projects, with their folders")
	}
	if got := exp.GetProjects(); len(got) != 2 || got[0].GetRemote() != "https://github.com/Acme/Api.git" || !got[0].GetGit() || got[1].GetRemote() != "" {
		t.Errorf("project references = %v", got)
	}

	// Into an empty base: everything comes back with its identifiers, and the projects wait for a folder.
	dst := serve(t)
	res, err := dst.wishes.Import(ctx, connect.NewRequest(&planv1.WishServiceImportRequest{File: filepath.Join(dir, "wish.djinn")}))
	if err != nil {
		t.Fatal(err)
	}
	if res.Msg.GetWish().GetId() != wish.GetId() || len(res.Msg.GetProjects()) != 2 {
		t.Fatalf("import = %v", res.Msg)
	}
	for _, m := range res.Msg.GetProjects() {
		if m.GetMatch() != planv1.ProjectMatchKind_PROJECT_MATCH_KIND_NEW || m.GetProject().GetDirectory() != "" || !strings.Contains(m.GetNote(), "djinn project add") {
			t.Errorf("project %s: %v, want new and waiting for a folder", m.GetProject().GetName(), m)
		}
	}
	snap, err := dst.wishes.Snapshot(ctx, connect.NewRequest(&planv1.WishServiceSnapshotRequest{WishId: wish.GetId()}))
	if err != nil {
		t.Fatal(err)
	}
	got := snap.Msg.GetExport()
	if len(got.GetQuestions()) != 2 || len(got.GetBlocks()) != 2 || len(got.GetTasks()) != 1 || len(got.GetEvents()) != 3 {
		t.Fatalf("snapshot after import = %d questions, %d blocks, %d tasks, %d events",
			len(got.GetQuestions()), len(got.GetBlocks()), len(got.GetTasks()), len(got.GetEvents()))
	}
	q := got.GetQuestions()[0]
	if q.GetCode() != "Q01" || q.GetAnswer().GetChoice() != planv1.Choice_CHOICE_A || q.GetRecommendation() == "" || len(q.GetOptions()) != 2 {
		t.Errorf("question after import = %v", q)
	}
	task := got.GetTasks()[0]
	if task.GetStatus() != planv1.TaskStatus_TASK_STATUS_INTERRUPTED || task.GetWorktree() != "" || task.GetBranch() != "djinn/w1" ||
		task.GetProjectId() != res.Msg.GetProjects()[0].GetProject().GetId() {
		t.Errorf("task after import = %v", task)
	}
	if !proto.Equal(got.GetBlocks()[0], exp.GetBlocks()[0]) {
		t.Errorf("block after import = %v, want %v", got.GetBlocks()[0], exp.GetBlocks()[0])
	}

	// The same wish twice is refused, unless asked to replace it.
	_, err = dst.wishes.ImportData(ctx, connect.NewRequest(&planv1.WishServiceImportDataRequest{Data: data}))
	if code(err) != connect.CodeAlreadyExists || !strings.Contains(err.Error(), "--replace") {
		t.Errorf("a second import: %v, want already_exists naming --replace", err)
	}
	if _, err := dst.wishes.ImportData(ctx, connect.NewRequest(&planv1.WishServiceImportDataRequest{Data: asJSON, Replace: true})); err != nil {
		t.Errorf("replace, from JSON: %v", err)
	}
	if all, _ := store.List[*planv1.Project](ctx, dst.store, nil); len(all) != 2 {
		t.Errorf("projects after a replace = %d, want the same 2", len(all))
	}

	// Adding the folder of the repository attaches the project waiting for it.
	clone := gitRepo(t, "my-api", "git@github.com:acme/api.git")
	added, err := dst.projects.Add(ctx, connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: clone}))
	if err != nil {
		t.Fatal(err)
	}
	if p := added.Msg.GetProject(); p.GetId() != task.GetProjectId() || p.GetName() != "api" || p.GetDirectory() == "" {
		t.Errorf("attached project = %v, want the imported one, with its folder", p)
	}

	// Exported again from here, the wish carries the journal of the first machine, not the import itself.
	again, err := decode(export(t, dst, wish.GetId(), filepath.Join(dir, "again.djinn")))
	if err != nil {
		t.Fatal(err)
	}
	for _, cmd := range again.GetCommands() {
		if cmd.GetMethod() == planv1connect.WishServiceImportDataProcedure {
			t.Error("a second export nests the import")
		}
	}
	if len(again.GetCommands()) != len(exp.GetCommands()) {
		t.Errorf("commands exported again = %d, want the %d of the first machine", len(again.GetCommands()), len(exp.GetCommands()))
	}
}

func TestImportFindsProjects(t *testing.T) {
	ctx := t.Context()
	src, wish, _ := source(t)
	data := export(t, src, wish.GetId(), filepath.Join(t.TempDir(), "wish.djinn"))

	dst := serve(t)
	clone := gitRepo(t, "elsewhere", "git@github.com:acme/api.git")
	notes := filepath.Join(t.TempDir(), "NOTES")
	if err := os.MkdirAll(notes, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{clone, notes} {
		if _, err := dst.projects.Add(ctx, connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: d})); err != nil {
			t.Fatal(err)
		}
	}
	res, err := dst.wishes.ImportData(ctx, connect.NewRequest(&planv1.WishServiceImportDataRequest{Data: data}))
	if err != nil {
		t.Fatal(err)
	}
	m := res.Msg.GetProjects()
	if len(m) != 2 || m[0].GetMatch() != planv1.ProjectMatchKind_PROJECT_MATCH_KIND_REMOTE || m[0].GetProject().GetName() != "elsewhere" ||
		m[1].GetMatch() != planv1.ProjectMatchKind_PROJECT_MATCH_KIND_NAME || m[1].GetProject().GetName() != "NOTES" {
		t.Errorf("matches = %v; want api by its remote, notes by its name", m)
	}
	if len(res.Msg.GetWish().GetProjectIds()) != 2 || res.Msg.GetWish().GetProjectIds()[0] != m[0].GetProject().GetId() {
		t.Errorf("wish projects = %v", res.Msg.GetWish().GetProjectIds())
	}
}

func TestImportRefusesBadFiles(t *testing.T) {
	ctx := t.Context()
	c := serve(t)
	wishID := store.NewID()
	good := func() *planv1.WishExport {
		return &planv1.WishExport{Version: 1, Wish: &planv1.Wish{Id: wishID, Title: "W"}}
	}
	for name, exp := range map[string]*planv1.WishExport{
		"unknown version": {Version: 2, Wish: &planv1.Wish{Id: wishID, Title: "W"}},
		"no wish":         {Version: 1},
		"a task of another": func() *planv1.WishExport {
			e := good()
			e.Tasks = []*planv1.Task{{Id: store.NewID(), WishId: store.NewID(), Code: "T1"}}
			return e
		}(),
		"a bad code": func() *planv1.WishExport {
			e := good()
			e.Questions = []*planv1.Question{{Id: store.NewID(), WishId: wishID, Code: "X1"}}
			return e
		}(),
		"an unknown project": func() *planv1.WishExport { e := good(); e.Wish.ProjectIds = []string{"p"}; return e }(),
		"an orphan event": func() *planv1.WishExport {
			e := good()
			e.Events = []*planv1.TaskEvent{{Id: store.NewID(), TaskId: store.NewID(), Seq: 1}}
			return e
		}(),
	} {
		data, _ := proto.Marshal(exp)
		_, err := c.wishes.ImportData(ctx, connect.NewRequest(&planv1.WishServiceImportDataRequest{Data: data}))
		if code(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: %v, want invalid_argument", name, err)
		}
	}
	if _, err := c.wishes.ImportData(ctx, connect.NewRequest(&planv1.WishServiceImportDataRequest{Data: []byte("not a wish")})); code(err) != connect.CodeInvalidArgument {
		t.Errorf("garbage: %v, want invalid_argument", err)
	}
	// Nothing was written, not even a journal entry.
	if all, _ := store.Commands(ctx, c.store, nil); len(all) != 0 {
		t.Errorf("journal after refused imports = %d entries", len(all))
	}
}

// TestExportKeepsClosure: a task marked done by hand keeps who closed it, when and why, through an export and an
// import.
func TestExportKeepsClosure(t *testing.T) {
	ctx := t.Context()
	src := serve(t)
	made, err := src.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{Title: "Close tasks"}))
	if err != nil {
		t.Fatal(err)
	}
	wish := made.Msg.GetWish()
	closed := &planv1.Closure{Actor: planv1.Closer_CLOSER_DEVELOPER, CreateTime: timestamppb.Now(), Note: "merged by hand"}
	task := &planv1.Task{
		Id: store.NewID(), WishId: wish.GetId(), Code: "W1", Title: "Old work", Status: planv1.TaskStatus_TASK_STATUS_DONE,
		CreateTime: timestamppb.Now(), EndTime: closed.GetCreateTime(), Closed: closed,
	}
	if err := src.store.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal("test", "put", task); err != nil {
			return err
		}
		return tx.Put(task)
	}); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "wish.djinn")
	export(t, src, wish.GetId(), file)
	dst := serve(t)
	if _, err := dst.wishes.Import(ctx, connect.NewRequest(&planv1.WishServiceImportRequest{File: file})); err != nil {
		t.Fatal(err)
	}
	snap, err := dst.wishes.Snapshot(ctx, connect.NewRequest(&planv1.WishServiceSnapshotRequest{WishId: wish.GetId()}))
	if err != nil {
		t.Fatal(err)
	}
	if tasks := snap.Msg.GetExport().GetTasks(); len(tasks) != 1 || !proto.Equal(tasks[0].GetClosed(), closed) ||
		tasks[0].GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Errorf("tasks after import = %v, want W1 done with %v", tasks, closed)
	}
}

package plan

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// TestAzimaRoundTrip: a task's kind, its azima and its plan file come back from the store as they were put; what an
// azima computes is filled on a clone, never on the store's message.
func TestAzimaRoundTrip(t *testing.T) {
	ctx := t.Context()
	s, err := store.Open(ctx, "", Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	wishID := store.NewID()
	azima := &planv1.Task{
		Id: store.NewID(), WishId: wishID, Code: "T07", Title: "The orchestrator", Kind: planv1.TaskKind_TASK_KIND_AZIMA,
		Status: planv1.TaskStatus_TASK_STATUS_PENDING, PlanFile: "plan/8e8d3d76-orchestrator.md", Phase: "2",
	}
	part := &planv1.Task{
		Id: store.NewID(), WishId: wishID, Code: "W1", Title: "Schedule", Kind: planv1.TaskKind_TASK_KIND_WORK,
		Status: planv1.TaskStatus_TASK_STATUS_RUNNING, PartOf: azima.GetId(),
	}
	err = s.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal(actor, "test/put", azima); err != nil {
			return err
		}
		return errorsJoin(tx.Put(azima), tx.Put(part))
	})
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := store.List[*planv1.Task](ctx, s, store.Where{"wish_id": wishID})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 || !proto.Equal(tasks[0], azima) || !proto.Equal(tasks[1], part) {
		t.Fatalf("read back: %v", tasks)
	}
	filled := WithAzimas(tasks)
	if tasks[0].GetAzima() != nil {
		t.Error("WithAzimas changed the store's message")
	}
	want := &planv1.Azima{State: planv1.AzimaState_AZIMA_STATE_IN_PROGRESS, Ready: true, Parts: 1, PartsRunning: 1}
	if !proto.Equal(filled[0].GetAzima(), want) || filled[1].GetAzima() != nil {
		t.Errorf("azima: %v, part: %v", filled[0].GetAzima(), filled[1].GetAzima())
	}
}

func errorsJoin(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// TestFillAzimas: an azima is done when marked done; in progress when a part runs or is done, or an azima part of it is
// under way; open otherwise; ready when every task it depends on is done.
func TestFillAzimas(t *testing.T) {
	task := func(code string, kind planv1.TaskKind, status planv1.TaskStatus, partOf string, deps ...string) *planv1.Task {
		return &planv1.Task{Id: code, Code: code, Kind: kind, Status: status, PartOf: partOf, DependsOn: deps}
	}
	const plan, work = planv1.TaskKind_TASK_KIND_AZIMA, planv1.TaskKind_TASK_KIND_UNSPECIFIED
	pending, done, failed := planv1.TaskStatus_TASK_STATUS_PENDING, planv1.TaskStatus_TASK_STATUS_DONE, planv1.TaskStatus_TASK_STATUS_FAILED
	tasks := []*planv1.Task{
		task("T1", plan, done, ""),
		task("T2", plan, pending, "", "T1"),
		task("T3", plan, pending, "", "T1", "T2"),
		task("T4", plan, pending, "T3"),
		task("W1", work, done, "T2"),
		task("W2", work, failed, "T2"),
		task("W3", work, planv1.TaskStatus_TASK_STATUS_PAUSED, "T4"),
		task("W4", work, failed, "T5"),
		task("T5", plan, pending, "", "W2"),
	}
	FillAzimas(tasks)
	for code, want := range map[string]*planv1.Azima{
		"T1": {State: planv1.AzimaState_AZIMA_STATE_DONE, Ready: true},
		"T2": {State: planv1.AzimaState_AZIMA_STATE_IN_PROGRESS, Ready: true, Parts: 2, PartsDone: 1},
		// Its part T4 is under way, through W3.
		"T3": {State: planv1.AzimaState_AZIMA_STATE_IN_PROGRESS, Parts: 1},
		"T4": {State: planv1.AzimaState_AZIMA_STATE_IN_PROGRESS, Ready: true, Parts: 1, PartsRunning: 1},
		// A failed part is not progress.
		"T5": {State: planv1.AzimaState_AZIMA_STATE_OPEN, Parts: 1},
	} {
		i := slices.IndexFunc(tasks, func(x *planv1.Task) bool { return x.GetCode() == code })
		if got := tasks[i].GetAzima(); !proto.Equal(got, want) {
			t.Errorf("%s: %v, want %v", code, got, want)
		}
	}
	if tasks[4].GetAzima() != nil {
		t.Error("work has an azima state")
	}
}

// TestAzimaFiles: a plan file's front matter and title are read, the README's absence of one skips it; its after line
// is written after its status, replaced, and removed, a file already right left as it is.
func TestAzimaFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, PlanDir), 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(name, text string) string {
		path := filepath.Join(dir, PlanDir, name)
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	write("README.md", "# Plan\n\n- [ ] T07\n")
	orch := write("8e8d3d76-orchestrator.md", "---\nid: 01a1184f-cf1b-7a9e-90f4-3c598e8d3d76\ncode: T07\nphase: 2\nstatus: in-progress\n---\n\n# T07 · The orchestrator\n\nBody.\n")
	write("later.md", "---\ncode: T15\nphase: later\nstatus: done\nafter: T07, T17\n---\n# T15 - Work spread over trusted machines\n")
	files, err := ReadAzimaFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []AzimaFile{
		{Path: "plan/8e8d3d76-orchestrator.md", ID: "01a1184f-cf1b-7a9e-90f4-3c598e8d3d76", Code: "T07", Phase: "2", Status: "in-progress", Title: "The orchestrator"},
		{Path: "plan/later.md", Code: "T15", Phase: "later", Status: "done", Title: "Work spread over trusted machines", After: []string{"T07", "T17"}},
	}
	if len(files) != len(want) {
		t.Fatalf("files: %+v", files)
	}
	for i := range want {
		if f, w := files[i], want[i]; f.Path != w.Path || f.ID != w.ID || f.Code != w.Code || f.Phase != w.Phase ||
			f.Status != w.Status || f.Title != w.Title || !slices.Equal(f.After, w.After) {
			t.Errorf("file %d: %+v, want %+v", i, f, w)
		}
	}
	if !files[1].Done() || files[0].Done() {
		t.Error("done")
	}

	check := func(codes []string, changed bool, front string) {
		t.Helper()
		got, err := WriteAfter(orch, codes)
		if err != nil || got != changed {
			t.Fatalf("WriteAfter(%v) = %v, %v", codes, got, err)
		}
		data, err := os.ReadFile(orch)
		if err != nil {
			t.Fatal(err)
		}
		if want := "---\nid: 01a1184f-cf1b-7a9e-90f4-3c598e8d3d76\ncode: T07\nphase: 2\nstatus: in-progress\n" + front +
			"---\n\n# T07 · The orchestrator\n\nBody.\n"; string(data) != want {
			t.Errorf("file:\n%s\nwant\n%s", data, want)
		}
	}
	check([]string{"T08", "T17"}, true, "after: T08 T17\n")
	check([]string{"T08", "T17"}, false, "after: T08 T17\n")
	check([]string{"T17"}, true, "after: T17\n")
	check(nil, true, "")
	check(nil, false, "")
}

func TestCompareCodes(t *testing.T) {
	codes := []string{"T10", "W2", "T2", "t3", "T01", "W10"}
	slices.SortFunc(codes, CompareCodes)
	if want := []string{"T01", "T2", "t3", "T10", "W2", "W10"}; !slices.Equal(codes, want) {
		t.Errorf("%v, want %v", codes, want)
	}
}

// TestBriefAzimas: the brief shows the plan as a graph, the ready azimas first (the one under way before the open one),
// then the blocked ones with what they wait for, the done ones on one line; no azima is in the running, waiting or
// finished work, and work says its azima.
func TestBriefAzimas(t *testing.T) {
	ctx := t.Context()
	c := serve(t)
	made, err := c.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{Title: "Plan the lamp"}))
	if err != nil {
		t.Fatal(err)
	}
	wishID := made.Msg.GetWish().GetId()
	day := timestamppb.New(time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC))
	azima := func(code, title string, status planv1.TaskStatus, deps ...string) *planv1.Task {
		return &planv1.Task{Id: store.NewID(), WishId: wishID, Code: code, Title: title, Kind: planv1.TaskKind_TASK_KIND_AZIMA,
			Status: status, CreateTime: day, DependsOn: deps}
	}
	pending, done := planv1.TaskStatus_TASK_STATUS_PENDING, planv1.TaskStatus_TASK_STATUS_DONE
	t1 := azima("T1", "The data", done)
	t2 := azima("T2", "The protos", done)
	t3 := azima("T3", "The window", pending, t1.GetId())
	t4 := azima("T4", "The orchestrator", pending, t1.GetId(), t2.GetId())
	t5 := azima("T5", "Spread the work", pending, t2.GetId(), t3.GetId(), t4.GetId())
	t10 := azima("T10", "Releases", pending, t4.GetId())
	running := &planv1.Task{Id: store.NewID(), WishId: wishID, Code: "W1", Title: "Schedule", PartOf: t4.GetId(),
		Status: planv1.TaskStatus_TASK_STATUS_RUNNING, CreateTime: day, StartTime: day, Provider: planv1.Provider_PROVIDER_CLAUDE}
	planned := &planv1.Task{Id: store.NewID(), WishId: wishID, Code: "W2", Title: "Draw it", PartOf: t3.GetId(),
		Status: pending, CreateTime: day, Scheduled: true}
	if err := c.store.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal(actor, "test/put", t1); err != nil {
			return err
		}
		for _, task := range []*planv1.Task{t1, t2, t3, t4, t5, t10, running, planned} {
			if err := tx.Put(task); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	brief, err := BuildBrief(ctx, c.store, t.TempDir(), wishID)
	if err != nil {
		t.Fatal(err)
	}
	_, azimas, ok := strings.Cut(brief.Moving, "## Azimas\n\n")
	if !ok {
		t.Fatalf("no azimas:\n%s", brief.Moving)
	}
	azimas, rest, _ := strings.Cut(azimas, "\n## ")
	want := "The plan as a graph, the ready azimas first. Spawn their work `--part-of <azima>`.\n\n" +
		"- **T4** The orchestrator: ready, in progress, 0 of 1 parts done, 1 running\n" +
		"- **T3** The window: ready, open, 0 of 1 parts done\n" +
		"- **T5** Spread the work: waits for T3, T4 (after T2, T3, T4); open\n" +
		"- **T10** Releases: waits for T4; open\n" +
		"- Done: T1, T2.\n"
	if azimas != want {
		t.Errorf("azimas =\n%s\nwant\n%s", azimas, want)
	}
	if !strings.Contains(rest, "- **W1** Schedule (claude, part of T4, since") ||
		!strings.Contains(rest, "- **W2** Draw it (part of T3): planned") {
		t.Errorf("work:\n%s", rest)
	}
	for _, code := range []string{"**T1**", "**T3**", "**T5**"} {
		if strings.Contains(rest, code) {
			t.Errorf("an azima is listed as work: %s\n%s", code, rest)
		}
	}
	if !strings.Contains(brief.Stable, "--part-of <azima>") || !strings.Contains(brief.Stable, "djinn plan sync <wish>") {
		t.Errorf("the rules leave the azimas out:\n%s", brief.Stable)
	}
}

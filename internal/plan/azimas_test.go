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

// TestAwaitingProof: an azima under way awaits its proof once its work is finished (done, stopped, or cut short for
// good), its azimas done or awaiting theirs, and its plan file leaves only boxes that need a proof no worker can give.
// Work left, a part failed or resuming, a box without needs, or nothing under way keeps it where it was. A closed azima
// with a part failed is in progress, not done. The push takes the same rule for an azima's end (TestPushDue).
func TestAwaitingProof(t *testing.T) {
	needs := []*planv1.ProofNeed{{Box: "Opened on a Mac.", Needs: "a Mac", Provers: []planv1.Prover{planv1.Prover_PROVER_MAC}}}
	azima := func(code, partOf string, proof bool) *planv1.Task {
		a := &planv1.Task{Id: code, Code: code, Kind: planv1.TaskKind_TASK_KIND_AZIMA, Status: planv1.TaskStatus_TASK_STATUS_PENDING, PartOf: partOf}
		if proof {
			a.ProofNeeds = needs
		}
		return a
	}
	work := func(code, partOf string, status planv1.TaskStatus) *planv1.Task {
		return &planv1.Task{Id: code, Code: code, Status: status, PartOf: partOf}
	}
	const (
		done, stopped, cut = planv1.TaskStatus_TASK_STATUS_DONE, planv1.TaskStatus_TASK_STATUS_STOPPED, planv1.TaskStatus_TASK_STATUS_INTERRUPTED
		failed, resuming   = planv1.TaskStatus_TASK_STATUS_FAILED, planv1.TaskStatus_TASK_STATUS_RESUMING
		pending, running   = planv1.TaskStatus_TASK_STATUS_PENDING, planv1.TaskStatus_TASK_STATUS_RUNNING
	)
	const (
		open, under = planv1.AzimaState_AZIMA_STATE_OPEN, planv1.AzimaState_AZIMA_STATE_IN_PROGRESS
		proof       = planv1.AzimaState_AZIMA_STATE_AWAITING_PROOF
	)
	tasks := []*planv1.Task{
		// Its work done, stopped, or cut short for good.
		azima("T1", "", true), work("W1", "T1", done), work("W2", "T1", stopped), work("W3", "T1", cut),
		// Work left: planned, running, failed or resuming.
		azima("T2", "", true), work("W4", "T2", done), work("W5", "T2", pending),
		azima("T3", "", true), work("W6", "T3", done), work("W7", "T3", running),
		azima("T4", "", true), work("W8", "T4", done), work("W9", "T4", failed),
		azima("T5", "", true), work("W10", "T5", done), work("W11", "T5", resuming),
		// A box without needs: its work done, it is to validate all the same; the person checks the box.
		azima("T6", "", false), work("W12", "T6", done),
		// Nothing under way: open, whatever its file says.
		azima("T7", "", true), work("W13", "T7", stopped),
		// Its azima part awaits its proof too: it does; one with work left holds the other back.
		azima("T8", "", true), azima("T9", "T8", true), work("W14", "T9", done),
		azima("T10", "", true), azima("T11", "T10", true), work("W15", "T11", done), work("W16", "T11", pending),
		// Done stays done.
		{Id: "T12", Code: "T12", Kind: planv1.TaskKind_TASK_KIND_AZIMA, Status: done, ProofNeeds: needs},
		// Closed, its plan file saying done, but work part of it runs or waits: in progress until it ends.
		{Id: "T13", Code: "T13", Kind: planv1.TaskKind_TASK_KIND_AZIMA, Status: done}, work("W17", "T13", done),
		work("W18", "T13", running),
		{Id: "T14", Code: "T14", Kind: planv1.TaskKind_TASK_KIND_AZIMA, Status: done}, work("W19", "T14", pending),
		// Closed, with a part failed: in progress, as with the push (harness.azimaEnded); one cut short for good is
		// finished, and it is done.
		{Id: "T15", Code: "T15", Kind: planv1.TaskKind_TASK_KIND_AZIMA, Status: done}, work("W20", "T15", done),
		work("W21", "T15", failed),
		{Id: "T16", Code: "T16", Kind: planv1.TaskKind_TASK_KIND_AZIMA, Status: done}, work("W22", "T16", done),
		work("W23", "T16", cut),
	}
	FillAzimas(tasks)
	for code, want := range map[string]planv1.AzimaState{
		"T1": proof, "T2": under, "T3": under, "T4": under, "T5": under, "T6": proof, "T7": open,
		"T8": proof, "T9": proof, "T10": under, "T11": under, "T12": planv1.AzimaState_AZIMA_STATE_DONE,
		"T13": under, "T14": under, "T15": under, "T16": planv1.AzimaState_AZIMA_STATE_DONE,
	} {
		i := slices.IndexFunc(tasks, func(x *planv1.Task) bool { return x.GetCode() == code })
		if got := tasks[i].GetAzima().GetState(); got != want {
			t.Errorf("%s: %v, want %v", code, got, want)
		}
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
// then the blocked ones with what they wait for, those awaiting their proof apart with who gives it, the done ones on
// one line; no azima is in the running, waiting or
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
	t11 := azima("T11", "Windows", pending)
	t11.ProofNeeds = []*planv1.ProofNeed{
		{Box: "Opens on Windows 11.", Needs: "a Windows 11 machine", Provers: []planv1.Prover{planv1.Prover_PROVER_WINDOWS}},
		{Box: "Reviewed.", Needs: "Clément's review", Provers: []planv1.Prover{planv1.Prover_PROVER_REVIEW}, Reviewer: "Clément"},
	}
	t12 := azima("T12", "Updates", pending)
	t12.ProofNeeds = []*planv1.ProofNeed{{Box: "Updated.", Needs: "a published release, then a person",
		Provers: []planv1.Prover{planv1.Prover_PROVER_RELEASE, planv1.Prover_PROVER_PERSON}}}
	built := &planv1.Task{Id: store.NewID(), WishId: wishID, Code: "W3", Title: "Build it", PartOf: t11.GetId(),
		Status: done, CreateTime: day}
	updated := &planv1.Task{Id: store.NewID(), WishId: wishID, Code: "W4", Title: "Update it", PartOf: t12.GetId(),
		Status: done, CreateTime: day}
	dropped := &planv1.Task{Id: store.NewID(), WishId: wishID, Code: "W5", Title: "Try it", PartOf: t12.GetId(),
		Status: planv1.TaskStatus_TASK_STATUS_STOPPED, CreateTime: day}
	running := &planv1.Task{Id: store.NewID(), WishId: wishID, Code: "W1", Title: "Schedule", PartOf: t4.GetId(),
		Status: planv1.TaskStatus_TASK_STATUS_RUNNING, CreateTime: day, StartTime: day, Provider: planv1.Provider_PROVIDER_CLAUDE}
	planned := &planv1.Task{Id: store.NewID(), WishId: wishID, Code: "W2", Title: "Draw it", PartOf: t3.GetId(),
		Status: pending, CreateTime: day, Scheduled: true}
	if err := c.store.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal(actor, "test/put", t1); err != nil {
			return err
		}
		for _, task := range []*planv1.Task{t1, t2, t3, t4, t5, t10, t11, t12, running, planned, built, updated, dropped} {
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
		"- Work done, waiting for its proof: T11 needs a Windows machine, Clément's review; T12 needs a release, a person.\n" +
		"- Done: T1, T2.\n"
	if azimas != want {
		t.Errorf("azimas =\n%s\nwant\n%s", azimas, want)
	}
	if !strings.Contains(rest, "- **W1** Schedule (claude, part of T4, since") ||
		!strings.Contains(rest, "- **W2** Draw it (part of T3): planned") {
		t.Errorf("work:\n%s", rest)
	}
	for _, code := range []string{"**T1**", "**T3**", "**T5**", "**T11**"} {
		if strings.Contains(rest, code) {
			t.Errorf("an azima is listed as work: %s\n%s", code, rest)
		}
	}
	if !strings.Contains(brief.Stable, "--part-of <azima>") || !strings.Contains(brief.Stable, "djinn plan sync <wish>") {
		t.Errorf("the rules leave the azimas out:\n%s", brief.Stable)
	}
}

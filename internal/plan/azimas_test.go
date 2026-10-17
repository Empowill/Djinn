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
	want := &planv1.Azima{
		State: planv1.AzimaState_AZIMA_STATE_IN_PROGRESS, Ready: true, Parts: 1, PartsRunning: 1,
		Branch: "djinn/T07-the-orchestrator",
	}
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
		{Id: "T6", Code: "T6", Kind: plan, Status: pending, Draft: true},
		task("T7", plan, done, ""),
		{Id: "W5", Code: "W5", Kind: work, Status: done, PartOf: "T7", Integration: &planv1.TaskIntegration{State: planv1.IntegrationState_INTEGRATION_STATE_PENDING}},
		task("T8", plan, pending, "", "T7"),
		// A cancelled part is no work left: out of the total, listed all the same.
		task("T9", plan, pending, ""),
		task("W6", work, done, "T9"),
		task("W7", work, planv1.TaskStatus_TASK_STATUS_CANCELLED, "T9"),
		task("T10", plan, pending, ""),
		task("W8", work, planv1.TaskStatus_TASK_STATUS_CANCELLED, "T10"),
		task("T11", plan, done, ""),
		task("W9", work, planv1.TaskStatus_TASK_STATUS_CANCELLED, "T11"),
	}
	FillAzimas(tasks)
	// No branch is based on another's: an azima's branch is cut from main, whatever it waits for.
	for code, want := range map[string]*planv1.Azima{
		"T1": {State: planv1.AzimaState_AZIMA_STATE_DONE, Ready: true, Branch: "djinn/T1"},
		"T2": {State: planv1.AzimaState_AZIMA_STATE_IN_PROGRESS, Ready: true, Parts: 2, PartsDone: 1, Branch: "djinn/T2"},
		// Its part T4 is under way, through W3.
		"T3": {State: planv1.AzimaState_AZIMA_STATE_IN_PROGRESS, Parts: 1, Branch: "djinn/T3"},
		"T4": {State: planv1.AzimaState_AZIMA_STATE_IN_PROGRESS, Ready: true, Parts: 1, PartsRunning: 1, Branch: "djinn/T4"},
		// A failed part is not progress.
		"T5": {State: planv1.AzimaState_AZIMA_STATE_OPEN, Parts: 1, Branch: "djinn/T5"},
		"T6": {State: planv1.AzimaState_AZIMA_STATE_DRAFT, Ready: true, Branch: "djinn/T6"},
		// T7 has a done part whose work is merging: in progress, partsRunning=1, partsDone=0.
		"T7": {State: planv1.AzimaState_AZIMA_STATE_IN_PROGRESS, Ready: true, Parts: 1, PartsRunning: 1, PartsDone: 0, Branch: "djinn/T7"},
		// T8 waits for T7, which is not done yet: Ready is false.
		"T8": {State: planv1.AzimaState_AZIMA_STATE_OPEN, Ready: false, Branch: "djinn/T8"},
		// T9's work is done, its other part cancelled: 1/1, its proof awaited.
		"T9": {State: planv1.AzimaState_AZIMA_STATE_AWAITING_PROOF, Ready: true, Parts: 1, PartsDone: 1, Branch: "djinn/T9"},
		// T10's only part is cancelled: nothing to count, nothing waiting, nothing under way.
		"T10": {State: planv1.AzimaState_AZIMA_STATE_OPEN, Ready: true, Branch: "djinn/T10"},
		// T11 is closed, its only part cancelled: done, and it folds.
		"T11": {State: planv1.AzimaState_AZIMA_STATE_DONE, Ready: true, Branch: "djinn/T11"},
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

// TestFillAzimasKeepsTheCut: what Djinn records of an azima's branch once it cut it from main stays on every read:
// its name, even once its title changed, the main it lands in, the commit it was cut from, where it stands against
// main and its pull request. A base branch without a cut, as an old read computed it, is left out.
func TestFillAzimasKeepsTheCut(t *testing.T) {
	const sha = "0123456789abcdef0123456789abcdef01234567"
	main := &planv1.WishMain{ProjectId: "p", Held: "main holds no release the branch lacks"}
	pr := &planv1.AzimaPr{Url: "https://example.com/pull/7", State: planv1.AzimaPrState_AZIMA_PR_STATE_OPEN}
	cut := &planv1.Task{
		Id: "T1", Code: "T1", Title: "A new title", Kind: planv1.TaskKind_TASK_KIND_AZIMA, Status: planv1.TaskStatus_TASK_STATUS_PENDING,
		Azima: &planv1.Azima{Branch: "djinn/T1-the-first-title", BaseBranch: "main", CutFrom: sha, Main: main, Pr: pr},
	}
	old := &planv1.Task{
		Id: "T2", Code: "T2", Title: "Stacked", Kind: planv1.TaskKind_TASK_KIND_AZIMA, Status: planv1.TaskStatus_TASK_STATUS_PENDING,
		Azima: &planv1.Azima{Branch: "djinn/T2-stacked", BaseBranch: "djinn/T1-the-first-title"},
	}
	if got := AzimaBranchName(cut); got != "djinn/T1-the-first-title" {
		t.Errorf("AzimaBranchName = %q, want the recorded branch", got)
	}
	FillAzimas([]*planv1.Task{cut, old})
	want := &planv1.Azima{
		State: planv1.AzimaState_AZIMA_STATE_OPEN, Ready: true, Branch: "djinn/T1-the-first-title", BaseBranch: "main", CutFrom: sha,
		Main: main, Pr: pr,
	}
	if got := cut.GetAzima(); !proto.Equal(got, want) {
		t.Errorf("cut: %v, want %v", got, want)
	}
	want = &planv1.Azima{State: planv1.AzimaState_AZIMA_STATE_OPEN, Ready: true, Branch: "djinn/T2-stacked"}
	if got := old.GetAzima(); !proto.Equal(got, want) {
		t.Errorf("old: %v, want %v", got, want)
	}
}

// TestAzimaDependencies: an azima waits for another itself or through its work; work inside one azima waiting for
// work of the same, or for work of no azima, makes no azima wait. RefuseAzimaDependencies refuses only what a change
// adds; RefuseSwitchToAzima refuses any, naming them in a line.
func TestAzimaDependencies(t *testing.T) {
	task := func(code string, kind planv1.TaskKind, partOf string, deps ...string) *planv1.Task {
		return &planv1.Task{Id: code, Code: code, Kind: kind, PartOf: partOf, DependsOn: deps}
	}
	const azima, work = planv1.TaskKind_TASK_KIND_AZIMA, planv1.TaskKind_TASK_KIND_WORK
	tasks := []*planv1.Task{
		task("T1", azima, ""),
		task("T2", azima, "", "T1"),
		task("T3", azima, ""),
		task("W1", work, "T1"),
		task("W2", work, "T1", "W1"), // inside T1
		task("W3", work, "T3", "W1"), // T3 through its work
		task("W4", work, ""),
		task("W5", work, "T3", "W4"), // work of no azima
		task("T10", azima, "", "T2"),
	}
	if got, want := AzimaDependenciesText(AzimaDependencies(tasks)), "T2 after T1; T3 after T1; T10 after T2"; got != want {
		t.Errorf("AzimaDependencies = %q, want %q", got, want)
	}
	if err := RefuseSwitchToAzima(tasks); connect.CodeOf(err) != connect.CodeFailedPrecondition ||
		!strings.Contains(err.Error(), "per azima refused: azimas depend on each other (T2 after T1; T3 after T1; T10 after T2)") {
		t.Errorf("switch: %v", err)
	}
	if err := RefuseSwitchToAzima(tasks[2:8]); err != nil {
		t.Errorf("switch with work waiting inside one azima: %v", err)
	}
	// What a change adds is refused, what was there stays.
	if err := RefuseAzimaDependencies(tasks, tasks); err != nil {
		t.Errorf("nothing added: %v", err)
	}
	graph := slices.Clone(tasks)
	graph[2] = task("T3", azima, "", "T2")
	err := RefuseAzimaDependencies(tasks, graph)
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "(T3 after T2)") {
		t.Errorf("T3 after T2 added: %v", err)
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

// TestAzimaWatching: an azima whose only live work is a watcher (the babysit of its pull request) is watching, not in
// progress: closed or not, its work finished, its azimas done, to validate or watching in their turn. A work part that
// runs, waits, or fails keeps it in progress, a watcher beside it or not; a watcher that no longer runs, or not yet,
// leaves it where its work puts it. The push leaves its watchers out the same way (W58, harness TestPushDue).
func TestAzimaWatching(t *testing.T) {
	const (
		done, stopped, failed = planv1.TaskStatus_TASK_STATUS_DONE, planv1.TaskStatus_TASK_STATUS_STOPPED, planv1.TaskStatus_TASK_STATUS_FAILED
		pending, running      = planv1.TaskStatus_TASK_STATUS_PENDING, planv1.TaskStatus_TASK_STATUS_RUNNING
		paused                = planv1.TaskStatus_TASK_STATUS_PAUSED
	)
	azima := func(code, partOf string, status planv1.TaskStatus) *planv1.Task {
		return &planv1.Task{Id: code, Code: code, Kind: planv1.TaskKind_TASK_KIND_AZIMA, Status: status, PartOf: partOf}
	}
	work := func(code, partOf string, status planv1.TaskStatus) *planv1.Task {
		return &planv1.Task{Id: code, Code: code, Status: status, PartOf: partOf}
	}
	watcher := func(code, partOf string, status planv1.TaskStatus) *planv1.Task {
		return &planv1.Task{Id: code, Code: code, Status: status, PartOf: partOf, Provider: planv1.Provider_PROVIDER_WATCH}
	}
	merging := work("W14", "T9", done)
	merging.Integration = &planv1.TaskIntegration{State: planv1.IntegrationState_INTEGRATION_STATE_INTEGRATING}
	tasks := []*planv1.Task{
		// Its work done, its watcher running: watching, closed or not; paused, the watcher still holds its place.
		azima("T1", "", pending), work("W1", "T1", done), watcher("B1", "T1", running),
		azima("T2", "", done), work("W2", "T2", done), work("W3", "T2", stopped), watcher("B2", "T2", running),
		azima("T3", "", pending), watcher("B3", "T3", paused),
		// Work left beside the watcher: in progress.
		azima("T4", "", pending), work("W4", "T4", done), work("W5", "T4", running), watcher("B4", "T4", running),
		azima("T5", "", pending), work("W6", "T5", pending), watcher("B5", "T5", running),
		azima("T6", "", done), work("W7", "T6", failed), watcher("B6", "T6", running),
		// Its azima part watching: it watches too; its azima part with work left keeps it in progress.
		azima("T7", "", pending), work("W8", "T7", done), azima("T8", "T7", pending), work("W9", "T8", done),
		watcher("B7", "T8", running),
		azima("T9", "", pending), work("W10", "T9", done), watcher("B8", "T9", running), merging,
		// The watcher ended, or has not started: the existing states.
		azima("T10", "", done), work("W11", "T10", done), watcher("B9", "T10", done),
		azima("T11", "", pending), work("W12", "T11", done), watcher("B10", "T11", stopped),
		azima("T12", "", pending), work("W13", "T12", done), watcher("B11", "T12", pending),
	}
	FillAzimas(tasks)
	const (
		under, watching = planv1.AzimaState_AZIMA_STATE_IN_PROGRESS, planv1.AzimaState_AZIMA_STATE_WATCHING
		proof           = planv1.AzimaState_AZIMA_STATE_AWAITING_PROOF
	)
	for code, want := range map[string]planv1.AzimaState{
		"T1": watching, "T2": watching, "T3": watching, "T4": under, "T5": under, "T6": under, "T7": watching,
		"T8": watching, "T9": under, "T10": planv1.AzimaState_AZIMA_STATE_DONE, "T11": proof, "T12": under,
	} {
		i := slices.IndexFunc(tasks, func(x *planv1.Task) bool { return x.GetCode() == code })
		if got := tasks[i].GetAzima().GetState(); got != want {
			t.Errorf("%s: %v, want %v", code, got, want)
		}
	}
	// What it counts stays: the watcher is a part that runs.
	if e := tasks[0].GetAzima(); e.GetParts() != 2 || e.GetPartsDone() != 1 || e.GetPartsRunning() != 1 {
		t.Errorf("T1 counts %d parts, %d done, %d running; want 2, 1, 1", e.GetParts(), e.GetPartsDone(), e.GetPartsRunning())
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

// TestDraftAzimaFile: a plan file with status: draft is parsed as a draft, its description holding its goal and body;
// WriteDescription updates its markdown body, and WriteStatus updates its status.
func TestDraftAzimaFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, PlanDir), 0o700); err != nil {
		t.Fatal(err)
	}
	content := "---\ncode: T31\nstatus: draft\nafter: T07\n---\n\n# T31 · Spreading work over machines\n\n## Goal\nAnticipate and research.\n\n## Design\nDetails here.\n"
	path := filepath.Join(dir, PlanDir, "t31-draft.md")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	files, err := ReadAzimaFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	f := files[0]
	if !f.Draft() {
		t.Errorf("Draft() = false, want true")
	}
	if f.Done() || f.Closes() {
		t.Errorf("Done()=%v, Closes()=%v, want false", f.Done(), f.Closes())
	}
	if f.Code != "T31" || f.Title != "Spreading work over machines" {
		t.Errorf("Code=%q, Title=%q", f.Code, f.Title)
	}
	if !slices.Equal(f.After, []string{"T07"}) {
		t.Errorf("After=%v, want [T07]", f.After)
	}
	wantDesc := "## Goal\nAnticipate and research.\n\n## Design\nDetails here."
	if f.Description != wantDesc {
		t.Errorf("Description = %q, want %q", f.Description, wantDesc)
	}

	// WriteDescription replaces the description while preserving the front matter and title.
	newDesc := "## Goal\nRevised goal.\n\n## Research\nSome research."
	changed, err := WriteDescription(path, newDesc)
	if err != nil || !changed {
		t.Fatalf("WriteDescription: changed=%v, err=%v", changed, err)
	}
	files, err = ReadAzimaFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if files[0].Description != newDesc {
		t.Errorf("updated description = %q, want %q", files[0].Description, newDesc)
	}
	if !files[0].Draft() || files[0].Code != "T31" {
		t.Errorf("front matter was damaged: draft=%v, code=%q", files[0].Draft(), files[0].Code)
	}

	// WriteStatus changes status from draft to open.
	changed, err = WriteStatus(path, "open")
	if err != nil || !changed {
		t.Fatalf("WriteStatus: changed=%v, err=%v", changed, err)
	}
	files, err = ReadAzimaFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if files[0].Draft() {
		t.Errorf("after WriteStatus(open), Draft() is still true")
	}
	if files[0].Status != "open" {
		t.Errorf("Status = %q, want open", files[0].Status)
	}
	if files[0].Description != newDesc {
		t.Errorf("WriteStatus damaged description: %q", files[0].Description)
	}
}

func TestCompareCodes(t *testing.T) {
	codes := []string{"T10", "W2", "T2", "t3", "T01", "W10"}
	slices.SortFunc(codes, CompareCodes)
	if want := []string{"T01", "T2", "t3", "T10", "W2", "W10"}; !slices.Equal(codes, want) {
		t.Errorf("%v, want %v", codes, want)
	}
}

// TestBriefAzimas: the brief shows the plan as a graph in rank order (W134), the ranked azimas first, then the others
// by code, ready or with what they wait for, those awaiting their proof apart with who gives it, the done ones on one
// line; no azima is in the running, waiting or finished work, and work says its azima.
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
	t5.Rank, t10.Rank = 2, 1
	t11 := azima("T11", "Windows", pending)
	t11.ProofNeeds = []*planv1.ProofNeed{
		{Box: "Opens on Windows 11.", Needs: "a Windows 11 machine", Provers: []planv1.Prover{planv1.Prover_PROVER_WINDOWS}},
		{Box: "Reviewed.", Needs: "Clément's review", Provers: []planv1.Prover{planv1.Prover_PROVER_REVIEW}, Reviewer: "Clément"},
	}
	t12 := azima("T12", "Updates", pending)
	t12.ProofNeeds = []*planv1.ProofNeed{{Box: "Updated.", Needs: "a published release, then a person",
		Provers: []planv1.Prover{planv1.Prover_PROVER_RELEASE, planv1.Prover_PROVER_PERSON}}}
	t31 := azima("T31", "Spreading work", pending)
	t31.Draft = true
	l1 := &planv1.Tilasm{
		Id: store.NewID(), WishId: wishID, Code: "L01", Title: "Work across machines",
		Cites: []string{t31.GetId()}, CreateTime: day,
	}
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
	// Its work done, the babysit of its pull request runs: watching, under way with the ones in progress.
	t13 := azima("T13", "Babysat", pending)
	merged := &planv1.Task{Id: store.NewID(), WishId: wishID, Code: "W6", Title: "Ship it", PartOf: t13.GetId(),
		Status: done, CreateTime: day}
	babysit := &planv1.Task{Id: store.NewID(), WishId: wishID, Code: "W7", Title: "Babysit the PR", PartOf: t13.GetId(),
		Status: planv1.TaskStatus_TASK_STATUS_RUNNING, CreateTime: day, StartTime: day, Provider: planv1.Provider_PROVIDER_WATCH}
	if err := c.store.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal(actor, "test/put", t1); err != nil {
			return err
		}
		for _, task := range []*planv1.Task{t1, t2, t3, t4, t5, t10, t11, t12, t31, running, planned, built, updated, dropped, t13, merged, babysit} {
			if err := tx.Put(task); err != nil {
				return err
			}
		}
		return tx.Put(l1)
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
	want := "The plan as a graph, in rank order: Djinn starts the work of the first azimas first " +
		"(`djinn task rank <azima> --top`, `--before T07`, `--after T07`). Spawn their work `--part-of <azima>`.\n\n" +
		"- **T10** Releases: waits for T4; open\n" +
		"- **T5** Spread the work: waits for T3, T4 (after T2, T3, T4); open\n" +
		"- **T3** The window: ready, open, 0 of 1 parts done\n" +
		"- **T4** The orchestrator: ready, in progress, 0 of 1 parts done, 1 running\n" +
		"- **T13** Babysat: ready, watching (only a watcher runs), 1 of 2 parts done, 1 running\n" +
		"- Work done, waiting for its proof: T11 needs a Windows machine, Clément's review; T12 needs a release, a person.\n" +
		"- Done: T1, T2.\n" +
		"- Drafts: T31 Spreading work — open with djinn task open T31 (explained by L01).\n"
	if azimas != want {
		t.Errorf("azimas =\n%s\nwant\n%s", azimas, want)
	}
	if !strings.Contains(rest, "- **W1** Schedule (claude, part of T4, since") ||
		!strings.Contains(rest, "- **W2** Draw it (part of T3): planned") {
		t.Errorf("work:\n%s", rest)
	}
	for _, code := range []string{"**T1**", "**T3**", "**T5**", "**T11**", "**T31**"} {
		if strings.Contains(rest, code) {
			t.Errorf("an azima is listed as work: %s\n%s", code, rest)
		}
	}
	if !strings.Contains(brief.Stable, "--part-of <azima>") || !strings.Contains(brief.Stable, "djinn plan sync <wish>") {
		t.Errorf("the rules leave the azimas out:\n%s", brief.Stable)
	}
}

// TestHasUnfinishedParts: tells whether an azima has unfinished parts or nested azimas with unfinished parts.
func TestHasUnfinishedParts(t *testing.T) {
	azima := func(id, partOf string, status planv1.TaskStatus) *planv1.Task {
		return &planv1.Task{Id: id, Code: id, Kind: planv1.TaskKind_TASK_KIND_AZIMA, Status: status, PartOf: partOf}
	}
	work := func(id, partOf string, status planv1.TaskStatus) *planv1.Task {
		return &planv1.Task{Id: id, Code: id, Kind: planv1.TaskKind_TASK_KIND_WORK, Status: status, PartOf: partOf}
	}
	const (
		done    = planv1.TaskStatus_TASK_STATUS_DONE
		pending = planv1.TaskStatus_TASK_STATUS_PENDING
		running = planv1.TaskStatus_TASK_STATUS_RUNNING
		stopped = planv1.TaskStatus_TASK_STATUS_STOPPED
		failed  = planv1.TaskStatus_TASK_STATUS_FAILED
	)
	tasks := []*planv1.Task{
		azima("T1", "", done),
		work("W1", "T1", done),
		work("W2", "T1", stopped),

		azima("T2", "", done),
		work("W3", "T2", done),
		work("W4", "T2", running),

		azima("T3", "", done),
		azima("T3_sub", "T3", done),
		work("W5", "T3_sub", done),

		azima("T4", "", done),
		azima("T4_sub", "T4", done),
		work("W6", "T4_sub", pending),

		azima("T5", "", done),
		azima("T5_sub", "T5", pending),
		work("W7", "T5_sub", done),

		azima("T6", "", done),
		work("W8", "T6", failed),

		azima("T7", "", done),
		{Id: "W9", Code: "W9", Kind: planv1.TaskKind_TASK_KIND_WORK, Status: done, PartOf: "T7", Integration: &planv1.TaskIntegration{State: planv1.IntegrationState_INTEGRATION_STATE_PENDING}},

		azima("T8", "", done),
		{Id: "W10", Code: "W10", Kind: planv1.TaskKind_TASK_KIND_WORK, Status: done, PartOf: "T8", Integration: &planv1.TaskIntegration{State: planv1.IntegrationState_INTEGRATION_STATE_CONFLICT}},

		azima("T9", "", done),
		{Id: "W11", Code: "W11", Kind: planv1.TaskKind_TASK_KIND_WORK, Status: done, PartOf: "T9", Integration: &planv1.TaskIntegration{State: planv1.IntegrationState_INTEGRATION_STATE_COMMITTED}},
	}

	if HasUnfinishedParts("T1", tasks) {
		t.Errorf("T1 has unfinished parts, want false")
	}
	if !HasUnfinishedParts("T2", tasks) {
		t.Errorf("T2 has no unfinished parts, want true (W4 running)")
	}
	if HasUnfinishedParts("T3", tasks) {
		t.Errorf("T3 has unfinished parts, want false")
	}
	if !HasUnfinishedParts("T4", tasks) {
		t.Errorf("T4 has no unfinished parts, want true (W6 pending)")
	}
	if !HasUnfinishedParts("T5", tasks) {
		t.Errorf("T5 has no unfinished parts, want true (T5_sub pending)")
	}
	if !HasUnfinishedParts("T6", tasks) {
		t.Errorf("T6 has no unfinished parts, want true (W8 failed)")
	}
	if !HasUnfinishedParts("T7", tasks) {
		t.Errorf("T7 has no unfinished parts, want true (W9 merging)")
	}
	if !HasUnfinishedParts("T8", tasks) {
		t.Errorf("T8 has no unfinished parts, want true (W10 merge conflict)")
	}
	if HasUnfinishedParts("T9", tasks) {
		t.Errorf("T9 has unfinished parts, want false (W11 committed)")
	}

	FillAzimas(tasks)
	for id, wantState := range map[string]planv1.AzimaState{
		"T1":     planv1.AzimaState_AZIMA_STATE_DONE,
		"T2":     planv1.AzimaState_AZIMA_STATE_IN_PROGRESS,
		"T3":     planv1.AzimaState_AZIMA_STATE_DONE,
		"T3_sub": planv1.AzimaState_AZIMA_STATE_DONE,
		"T4":     planv1.AzimaState_AZIMA_STATE_IN_PROGRESS,
		"T4_sub": planv1.AzimaState_AZIMA_STATE_IN_PROGRESS,
		"T5":     planv1.AzimaState_AZIMA_STATE_IN_PROGRESS,
		"T5_sub": planv1.AzimaState_AZIMA_STATE_AWAITING_PROOF,
		"T6":     planv1.AzimaState_AZIMA_STATE_IN_PROGRESS,
		"T7":     planv1.AzimaState_AZIMA_STATE_IN_PROGRESS,
		"T8":     planv1.AzimaState_AZIMA_STATE_IN_PROGRESS,
		"T9":     planv1.AzimaState_AZIMA_STATE_DONE,
	} {
		i := slices.IndexFunc(tasks, func(x *planv1.Task) bool { return x.GetId() == id })
		if got := tasks[i].GetAzima().GetState(); got != wantState {
			t.Errorf("%s state: %v, want %v", id, got, wantState)
		}
	}
}

func TestWriteWish(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "task.md")
	content := `---
id: 01a1184f-cf15-74c4-bd3f-4d72fe63ed30
code: T01
status: draft
---
# T01 · Test Task

Description here.
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// Add wish
	changed, err := WriteWish(path, "Collaborative Djinn")
	if err != nil {
		t.Fatalf("WriteWish err: %v", err)
	}
	if !changed {
		t.Errorf("WriteWish changed = false, want true")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f, ok := parseAzimaFile(data)
	if !ok || f.Wish != "Collaborative Djinn" {
		t.Errorf("parsed wish = %q, want %q", f.Wish, "Collaborative Djinn")
	}

	// Idempotent
	changed, err = WriteWish(path, "Collaborative Djinn")
	if err != nil || changed {
		t.Errorf("WriteWish idempotent changed = %v, err = %v", changed, err)
	}

	// Quoting when special chars
	changed, err = WriteWish(path, "Wish: Special #1")
	if err != nil || !changed {
		t.Fatalf("WriteWish special err = %v, changed = %v", err, changed)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f, ok = parseAzimaFile(data)
	if !ok || f.Wish != "Wish: Special #1" {
		t.Errorf("parsed wish special = %q, want %q", f.Wish, "Wish: Special #1")
	}

	// Clear wish
	changed, err = WriteWish(path, "")
	if err != nil || !changed {
		t.Fatalf("WriteWish clear err = %v, changed = %v", err, changed)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f, ok = parseAzimaFile(data)
	if !ok || f.Wish != "" {
		t.Errorf("parsed wish after clear = %q, want empty", f.Wish)
	}
}

func TestWriteCode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "task.md")
	content := `---
id: 01a1184f-cf15-74c4-bd3f-4d72fe63ed30
code: T01
status: draft
---
# T01 · Test Task

Description here.
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, err := WriteCode(path, "T05")
	if err != nil {
		t.Fatalf("WriteCode err: %v", err)
	}
	if !changed {
		t.Errorf("WriteCode changed = false, want true")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f, ok := parseAzimaFile(data)
	if !ok || f.Code != "T05" {
		t.Errorf("parsed code = %q, want T05", f.Code)
	}
	if f.Title != "Test Task" {
		t.Errorf("parsed title = %q, want Test Task", f.Title)
	}
	if !strings.Contains(string(data), "# T05 · Test Task") {
		t.Errorf("file body does not contain updated heading:\n%s", string(data))
	}

	// Idempotent
	changed, err = WriteCode(path, "T05")
	if err != nil || changed {
		t.Errorf("WriteCode idempotent changed = %v, err = %v", changed, err)
	}
}

// TestWriteTitle: the title goes into the heading, its code and separator kept; a heading without the code is the
// title alone; a file without a heading gets one; line endings stay; the title reads back as it was written.
func TestWriteTitle(t *testing.T) {
	cases := []struct{ name, before, after string }{
		{"code and separator kept",
			"---\ncode: T36\n---\n\n# T36: Old\n\nGoal.\n",
			"---\ncode: T36\n---\n\n# T36: feat/x reaches main: PR #3 merged\n\nGoal.\n"},
		{"heading without the code",
			"---\ncode: T36\n---\n# Old\nGoal.\n",
			"---\ncode: T36\n---\n# feat/x reaches main: PR #3 merged\nGoal.\n"},
		{"heading of the code alone",
			"---\ncode: T36\n---\n# T36\n",
			"---\ncode: T36\n---\n# T36 · feat/x reaches main: PR #3 merged\n"},
		{"no heading",
			"---\ncode: T36\n---\n\nGoal.\n",
			"---\ncode: T36\n---\n\n# T36 · feat/x reaches main: PR #3 merged\n\nGoal.\n"},
		{"CRLF",
			"---\r\ncode: T36\r\n---\r\n\r\n# T36 · Old\r\n\r\nGoal.\r\n",
			"---\r\ncode: T36\r\n---\r\n\r\n# T36 · feat/x reaches main: PR #3 merged\r\n\r\nGoal.\r\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "t36.md")
			if err := os.WriteFile(path, []byte(c.before), 0o644); err != nil {
				t.Fatal(err)
			}
			changed, err := WriteTitle(path, "T36", " feat/x reaches main: PR #3 merged ")
			if err != nil || !changed {
				t.Fatalf("WriteTitle: changed %v, err %v", changed, err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != c.after {
				t.Errorf("file:\n%q\nwant:\n%q", data, c.after)
			}
			if f, ok := parseAzimaFile(data); !ok || f.Title != "feat/x reaches main: PR #3 merged" {
				t.Errorf("title read back: %q", f.Title)
			}
			if changed, err := WriteTitle(path, "T36", "feat/x reaches main: PR #3 merged"); err != nil || changed {
				t.Errorf("WriteTitle again: changed %v, err %v", changed, err)
			}
		})
	}
}

func TestReadAzimaFilesSubdirectories(t *testing.T) {
	dir := t.TempDir()
	planDir := filepath.Join(dir, PlanDir)
	subDir := filepath.Join(planDir, "collab-wish")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	f1 := filepath.Join(planDir, "one.md")
	f2 := filepath.Join(subDir, "two.md")
	c1 := "---\ncode: T01\n---\n# T01 · One\n"
	c2 := "---\ncode: T02\n---\n# T02 · Two\n"
	if err := os.WriteFile(f1, []byte(c1), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f2, []byte(c2), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := ReadAzimaFiles(dir)
	if err != nil {
		t.Fatalf("ReadAzimaFiles err: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("ReadAzimaFiles len = %d, want 2", len(files))
	}
	byCode := map[string]AzimaFile{}
	for _, f := range files {
		byCode[f.Code] = f
	}
	if byCode["T01"].Wish != "" {
		t.Errorf("T01 wish = %q, want empty", byCode["T01"].Wish)
	}
	if byCode["T02"].Wish != "collab-wish" {
		t.Errorf("T02 wish = %q, want collab-wish", byCode["T02"].Wish)
	}
}

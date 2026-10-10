package harness

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
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// azima makes an azima of the wish, waiting for deps.
func (e *env) azima(t *testing.T, wishID, title string, deps ...string) *planv1.Task {
	t.Helper()
	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: title, Kind: planv1.TaskKind_TASK_KIND_AZIMA, DependsOn: deps,
	}))
	if err != nil {
		t.Fatal(err)
	}
	return res.Msg.GetTask()
}

func (e *env) group(t *testing.T, task *planv1.Task, azima string) (*planv1.Task, error) {
	t.Helper()
	res, err := e.tasks.Group(t.Context(), connect.NewRequest(&planv1.TaskServiceGroupRequest{TaskId: task.GetId(), PartOf: azima}))
	if err != nil {
		return nil, err
	}
	return res.Msg.GetTask(), nil
}

// TestAzimasAreNeverScheduled: an azima is never started nor said to wait; work part of an azima that is not ready
// runs at once, its azima under way; work that depends on an azima waits until it is marked done.
func TestAzimasAreNeverScheduled(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	ctx := t.Context()
	e := up(t, t.TempDir(), WithTick(5*time.Millisecond))
	wishID, _ := e.wish(t, gitRepo(t))

	first := e.azima(t, wishID, "Lay the ground")
	second := e.azima(t, wishID, "Build on it", first.GetCode())
	if first.GetCode() != "T1" || second.GetCode() != "T2" || second.GetKind() != planv1.TaskKind_TASK_KIND_AZIMA ||
		second.GetScheduled() || second.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING {
		t.Fatalf("azimas: %v, %v", first, second)
	}
	// Part of T2, which waits for T1: the work runs all the same.
	part := e.mustSpawn(t, wishID, "A part", "text part", &planv1.TaskServiceSpawnRequest{PartOf: "t2"})
	if part.GetPartOf() != second.GetId() {
		t.Fatalf("part of: %q", part.GetPartOf())
	}
	e.until(t, part.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_DONE))
	got := e.get(t, second.GetId())
	// Its only part done, nothing is left for Djinn: it is to validate.
	if s := got.GetAzima(); s.GetState() != planv1.AzimaState_AZIMA_STATE_AWAITING_PROOF || s.GetReady() || s.GetParts() != 1 ||
		s.GetPartsDone() != 1 {
		t.Errorf("T2 with its part done: %v", s)
	}
	if s := e.get(t, first.GetId()).GetAzima(); s.GetState() != planv1.AzimaState_AZIMA_STATE_OPEN || !s.GetReady() {
		t.Errorf("T1: %v", s)
	}

	// Work that depends on T1 waits for it to be marked done.
	after := e.mustSpawn(t, wishID, "After the ground", "text after", &planv1.TaskServiceSpawnRequest{DependsOn: []string{"T1"}})
	if after.GetWaitReason() != "waits for the azima T1 to be done" {
		t.Errorf("waits: %q", after.GetWaitReason())
	}
	if _, err := e.tasks.Done(ctx, connect.NewRequest(&planv1.TaskServiceDoneRequest{TaskId: first.GetId(), Note: "laid"})); err != nil {
		t.Fatal(err)
	}
	e.until(t, after.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_DONE))
	if s := e.get(t, second.GetId()).GetAzima(); !s.GetReady() {
		t.Errorf("T2 once T1 is done: %v", s)
	}

	// Through every pass, the scheduler never touched an azima: no worker, no reason, no event.
	for _, ep := range []*planv1.Task{e.get(t, second.GetId())} {
		if ep.GetStartTime() != nil || ep.GetWaitReason() != "" || ep.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING {
			t.Errorf("azima %s: %v", ep.GetCode(), ep)
		}
		if events := e.storedEvents(t, ep.GetId()); len(events) != 0 {
			t.Errorf("azima %s has events: %v", ep.GetCode(), events)
		}
	}
	listed := e.list(t, wishID)
	if i := slices.IndexFunc(listed, func(x *planv1.Task) bool { return x.GetId() == second.GetId() }); listed[i].GetAzima() == nil {
		t.Error("the list leaves the azima's state out")
	}
	if _, err := e.tasks.Stop(ctx, connect.NewRequest(&planv1.TaskServiceStopRequest{TaskId: second.GetId()})); err == nil {
		t.Error("an azima was stopped")
	}

	// What only a worker uses is refused for an azima; a task is part of an azima, never of work.
	_, err := e.tasks.Spawn(ctx, connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Odd", Kind: planv1.TaskKind_TASK_KIND_AZIMA, Prompt: "do it",
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("an azima with a prompt: %v", err)
	}
	if _, err := e.spawnReq(t, wishID, "Odd", "text odd", &planv1.TaskServiceSpawnRequest{PartOf: part.GetCode()}); connect.CodeOf(err) != connect.CodeInvalidArgument ||
		!strings.Contains(err.Error(), "not an azima") {
		t.Errorf("part of work: %v", err)
	}
}

// TestGroupRefusesCycles: a task's azima is set and cleared after it was made; one that would close a cycle through
// what the tasks depend on and the azimas they are part of is refused, naming it, and so is Depend's.
func TestGroupRefusesCycles(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	// No slot: the work only waits, nothing runs.
	e := up(t, t.TempDir(), WithCapacity((&limit{slots: 0}).capacity))
	wishID, _ := e.wish(t, gitRepo(t))
	t1, t2 := e.azima(t, wishID, "One"), e.azima(t, wishID, "Two")
	w := e.mustSpawn(t, wishID, "Work", "text work", &planv1.TaskServiceSpawnRequest{Later: true})

	got, err := e.group(t, w, "T1")
	if err != nil || got.GetPartOf() != t1.GetId() {
		t.Fatalf("W1 part of T1: %v, %v", got, err)
	}
	// T1 waiting for its own part would close T1 → W1 → T1.
	_, err = e.tasks.Depend(t.Context(), connect.NewRequest(&planv1.TaskServiceDependRequest{TaskId: t1.GetId(), DependsOn: []string{w.GetCode()}}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "T1 → W1 → T1") {
		t.Errorf("T1 on W1: %v", err)
	}
	// T2 part of T1, then T1 waiting for T2: T1 → T2 → T1.
	if _, err := e.group(t, t2, t1.GetId()); err != nil {
		t.Fatal(err)
	}
	_, err = e.tasks.Depend(t.Context(), connect.NewRequest(&planv1.TaskServiceDependRequest{TaskId: t1.GetId(), DependsOn: []string{"T2"}}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "T1 → T2 → T1") {
		t.Errorf("T1 on T2: %v", err)
	}
	// T1 part of T2 closes the same cycle, from the other side; an azima is never part of itself, nor of work.
	if _, err := e.group(t, t1, "T2"); connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "no cycle") {
		t.Errorf("T1 part of T2: %v", err)
	}
	if _, err := e.group(t, t1, "T1"); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("T1 part of itself: %v", err)
	}
	if _, err := e.group(t, t2, w.GetCode()); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("T2 part of work: %v", err)
	}
	// None takes the task out of its azima.
	if got, err := e.group(t, w, ""); err != nil || got.GetPartOf() != "" {
		t.Errorf("W1 out of T1: %v, %v", got, err)
	}
}

// TestMigrateAzimas: djinn up marks as azimas the T tasks a wish imported from its plan before tasks had kinds; a task
// a worker ran stays work.
func TestMigrateAzimas(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	home := t.TempDir()
	db, err := store.Open(t.Context(), filepath.Join(home, store.File), plan.Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	wishID := store.NewID()
	imported := &planv1.Task{Id: store.NewID(), WishId: wishID, Code: "T07", Title: "The orchestrator", Status: planv1.TaskStatus_TASK_STATUS_PENDING}
	ran := &planv1.Task{
		Id: store.NewID(), WishId: wishID, Code: "T08", Title: "Ran once", Status: planv1.TaskStatus_TASK_STATUS_DONE,
		Provider: planv1.Provider_PROVIDER_CLAUDE, StartTime: timestamppb.Now(),
	}
	work := &planv1.Task{Id: store.NewID(), WishId: wishID, Code: "W1", Title: "Work", Status: planv1.TaskStatus_TASK_STATUS_DONE}
	err = db.Tx(t.Context(), func(tx *store.Tx) error {
		if err := tx.Journal("local", "test", imported); err != nil {
			return err
		}
		for _, task := range []*planv1.Task{imported, ran, work} {
			if err := tx.Put(task); err != nil {
				return err
			}
		}
		return nil
	})
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	e := up(t, home)
	for task, want := range map[*planv1.Task]planv1.TaskKind{
		imported: planv1.TaskKind_TASK_KIND_AZIMA, ran: planv1.TaskKind_TASK_KIND_UNSPECIFIED, work: planv1.TaskKind_TASK_KIND_UNSPECIFIED,
	} {
		if got := e.get(t, task.GetId()).GetKind(); got != want {
			t.Errorf("%s: %v, want %v", task.GetCode(), got, want)
		}
	}
}

// copyPlan copies the repository's plan files into a new project folder.
func copyPlan(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, plan.PlanDir), 0o700); err != nil {
		t.Fatal(err)
	}
	names, err := filepath.Glob(filepath.Join("..", "..", plan.PlanDir, "*.md"))
	if err != nil || len(names) == 0 {
		t.Fatalf("plan files: %v, %v", names, err)
	}
	for _, name := range names {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, plan.PlanDir, filepath.Base(name)), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestSyncPlan: on a copy of the repository's plan, the azimas a wish imported before kinds (T01…T20, without the
// last ones) become azimas and keep the graph the store holds; the missing ones are made, one taking its file's after
// line; every file then says what its azima depends on; a file's status closes and opens its azima; a second sync
// changes nothing; the boxes that wait for a proof go on their azima; a file whose boxes are all checked closes its
// azima whatever its status says, and is reported.
func TestSyncPlan(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	ctx := t.Context()
	dir := copyPlan(t)
	files, err := plan.ReadAzimaFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	newer := []string{"T25", "T26"}
	// A made azima takes its file's after line, once.
	t26 := slices.IndexFunc(files, func(f plan.AzimaFile) bool { return f.Code == "T26" })
	if _, err := plan.WriteAfter(filepath.Join(dir, files[t26].Path), []string{"T13"}); err != nil {
		t.Fatal(err)
	}
	e := up(t, t.TempDir())
	wishID, _ := e.wish(t, dir)
	// T01…T20, as a plan import left them before kinds: tasks with the files' ids, which no worker ran.
	err = e.db.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal("local", "test", &planv1.Task{}); err != nil {
			return err
		}
		for _, f := range files {
			if slices.Contains(newer, f.Code) {
				continue
			}
			err := tx.Put(&planv1.Task{
				Id: f.ID, WishId: wishID, Code: f.Code, Title: f.Title, Status: planv1.TaskStatus_TASK_STATUS_PENDING,
				CreateTime: timestamppb.Now(),
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	id := func(code string) string {
		t.Helper()
		i := slices.IndexFunc(files, func(f plan.AzimaFile) bool { return f.Code == code })
		return files[i].ID
	}
	depend := func(code string, on ...string) {
		t.Helper()
		_, err := e.tasks.Depend(ctx, connect.NewRequest(&planv1.TaskServiceDependRequest{TaskId: id(code), DependsOn: on}))
		if err != nil {
			t.Fatal(err)
		}
	}
	depend("T07", "T02", "T14")
	depend("T20", "T02", "T07", "T13", "T14", "T18")
	depend("T13")

	sync := func() *planv1.PlanServiceSyncResponse {
		t.Helper()
		res, err := e.plans.Sync(ctx, connect.NewRequest(&planv1.PlanServiceSyncRequest{WishId: wishID}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg
	}
	res := sync()
	if !slices.Equal(res.GetMade(), newer) || len(res.GetAzimas()) != len(files) {
		t.Fatalf("made %v, %d azimas", res.GetMade(), len(res.GetAzimas()))
	}
	byCode := map[string]*planv1.Task{}
	codes := map[string]string{}
	for _, task := range e.list(t, wishID) {
		byCode[task.GetCode()], codes[task.GetId()] = task, task.GetCode()
	}
	after := func(code string) []string {
		var out []string
		for _, d := range byCode[code].GetDependsOn() {
			out = append(out, codes[d])
		}
		slices.SortFunc(out, plan.CompareCodes)
		return out
	}
	for _, f := range files {
		task := byCode[f.Code]
		if task.GetKind() != planv1.TaskKind_TASK_KIND_AZIMA || task.GetPlanFile() != f.Path || task.GetPhase() != f.Phase ||
			task.GetTitle() != f.Title || task.GetAzima() == nil {
			t.Errorf("%s: %v", f.Code, task)
		}
		// A file that says done, or checks every box, marks its azima done, closed by its file.
		if done := task.GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE; done != f.Closes() ||
			done && task.GetClosed().GetActor() != planv1.Closer_CLOSER_PLAN_FILE {
			t.Errorf("%s: %v, its file says %s", f.Code, task.GetStatus(), f.Status)
		}
	}
	// The graph is the store's, the made azima took its file's line.
	if got := after("T07"); !slices.Equal(got, []string{"T02", "T14"}) {
		t.Errorf("T07 after %v", got)
	}
	if got := after("T20"); !slices.Equal(got, []string{"T02", "T07", "T13", "T14", "T18"}) {
		t.Errorf("T20 after %v", got)
	}
	if got := after("T26"); !slices.Equal(got, []string{"T13"}) {
		t.Errorf("T26 after %v", got)
	}
	// Every file says what its azima depends on, none for T13.
	again, err := plan.ReadAzimaFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range again {
		if !slices.Equal(f.After, after(f.Code)) {
			t.Errorf("%s says after %v, the store %v", f.Path, f.After, after(f.Code))
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "plan", "8e8d3d76-orchestrator.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "---\nid: 01a1184f-cf1b-7a9e-90f4-3c598e8d3d76\ncode: T07\nphase: 2\nstatus: in-progress\nafter: T02 T14\n---\n\n# T07") {
		t.Errorf("T07's file:\n%s", data[:200])
	}

	// A second sync changes nothing.
	if res := sync(); len(res.GetMade())+len(res.GetChanged())+len(res.GetWritten()) != 0 {
		t.Errorf("second sync: %v", res)
	}
	// The boxes waiting for a proof no worker can give go on the azima, those of every merged part; a box without needs
	// leaves it none, as do boxes all checked (T02). Which files hold work left changes with the plan: any one will do.
	proofs, work := 0, 0
	for _, f := range files {
		got := byCode[f.Code].GetProofNeeds()
		if len(got) != len(f.DoneWhen.Needs) || !slices.EqualFunc(got, f.DoneWhen.Needs, func(a, b *planv1.ProofNeed) bool { return proto.Equal(a, b) }) {
			t.Errorf("%s needs %v, its file %v", f.Code, got, f.DoneWhen.Needs)
		}
		if len(got) > 0 {
			proofs++
		}
		if f.DoneWhen.Unchecked > 0 && len(got) == 0 {
			work++
		}
	}
	if proofs == 0 || work == 0 || len(byCode["T02"].GetProofNeeds()) != 0 {
		t.Errorf("%d azimas wait for a proof, %d hold work; T02 %v", proofs, work, byCode["T02"].GetProofNeeds())
	}

	// A file that says in-progress with every box checked is reported, and its azima stays done.
	t02 := slices.IndexFunc(files, func(f plan.AzimaFile) bool { return f.Code == "T02" })
	path := filepath.Join(dir, files[t02].Path)
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Replace(string(data), "status: done", "status: in-progress", 1)
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	if res := sync(); !slices.Equal(res.GetAllChecked(), []string{files[t02].Path}) || len(res.GetChanged()) != 0 {
		t.Errorf("T02 all checked: %v", res)
	}
	if got := e.get(t, id("T02")); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Errorf("T02: %v", got)
	}
	// A box unchecked again opens the azima the file closed.
	if err := os.WriteFile(path, []byte(strings.Replace(text, "- [x]", "- [ ]", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if res := sync(); !slices.Equal(res.GetChanged(), []string{"T02"}) || len(res.GetAllChecked()) != 0 {
		t.Errorf("T02 reopened: %v", res)
	}
	if got := e.get(t, id("T02")); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING || got.GetClosed() != nil {
		t.Errorf("T02: %v", got)
	}
}

// TestAnAzimaWithPartsStays: an azima with tasks part of it is not deleted, which are named; once they are regrouped
// it goes, and no task can be grouped into it any more. Its code is never given again, nor a deleted task's.
func TestAnAzimaWithPartsStays(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	ctx := t.Context()
	e := up(t, t.TempDir(), WithCapacity((&limit{slots: 0}).capacity)) // No slot: the work only waits.
	wishID, _ := e.wish(t, gitRepo(t))
	t1 := e.azima(t, wishID, "Templates")
	w1 := e.mustSpawn(t, wishID, "Work", "text work", &planv1.TaskServiceSpawnRequest{Later: true, PartOf: "T1"})
	w2 := e.mustSpawn(t, wishID, "More", "text more", &planv1.TaskServiceSpawnRequest{Later: true, PartOf: "T1"})
	del := func(task *planv1.Task) error {
		_, err := e.tasks.Delete(ctx, connect.NewRequest(&planv1.TaskServiceDeleteRequest{TaskId: task.GetId()}))
		return err
	}

	if err := del(t1); connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "W1, W2") ||
		!strings.Contains(err.Error(), "djinn task group") {
		t.Fatalf("delete T1 with its parts: %v", err)
	}
	if e.get(t, t1.GetId()).GetCode() != "T1" {
		t.Fatal("T1 is gone")
	}
	for _, w := range []*planv1.Task{w1, w2} {
		if _, err := e.group(t, w, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := del(t1); err != nil {
		t.Fatalf("delete T1 regrouped: %v", err)
	}
	if _, err := e.group(t, w1, "T1"); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("W1 into T1, gone: %v", err)
	}

	// T1 and W2 deleted: the next azima is T2, the next work W3.
	if err := del(w2); err != nil {
		t.Fatal(err)
	}
	if t2 := e.azima(t, wishID, "Releases"); t2.GetCode() != "T2" {
		t.Errorf("the next azima is %s, want T2", t2.GetCode())
	}
	if w3 := e.mustSpawn(t, wishID, "Last", "text last", &planv1.TaskServiceSpawnRequest{Later: true}); w3.GetCode() != "W3" {
		t.Errorf("the next work is %s, want W3", w3.GetCode())
	}
	wish, err := store.Get[*planv1.Wish](ctx, e.db, wishID)
	if err != nil || !slices.Equal(wish.GetRetiredCodes(), []string{"T1", "W2"}) {
		t.Errorf("retired %q, %v", wish.GetRetiredCodes(), err)
	}
}

// writeAzimaFile writes a plan file of the azima code in dir's plan folder.
func writeAzimaFile(t *testing.T, dir, name, code string) {
	t.Helper()
	writeFile(t, dir, filepath.Join(plan.PlanDir, name), "---\ncode: "+code+"\nstatus: in-progress\n---\n\n# "+code+" · "+name+"\n")
}

// TestSyncKeepsTheAzimas: a plan file removed leaves its azima, and the tasks part of it, as they were; a file that
// gives the code of an azima deleted makes no azima: a code is never given twice.
func TestSyncKeepsTheAzimas(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	ctx := t.Context()
	dir := t.TempDir()
	writeAzimaFile(t, dir, "one.md", "T1")
	writeAzimaFile(t, dir, "two.md", "T2")
	e := up(t, t.TempDir(), WithCapacity((&limit{slots: 0}).capacity))
	wishID, _ := e.wish(t, dir)
	sync := func() error {
		_, err := e.plans.Sync(ctx, connect.NewRequest(&planv1.PlanServiceSyncRequest{WishId: wishID}))
		return err
	}
	if err := sync(); err != nil {
		t.Fatal(err)
	}
	w1 := e.mustSpawn(t, wishID, "Work", "text work", &planv1.TaskServiceSpawnRequest{Later: true, PartOf: "T2"})

	// two.md absorbed into one.md: T2 stays, W1 with it.
	if err := os.Remove(filepath.Join(dir, plan.PlanDir, "two.md")); err != nil {
		t.Fatal(err)
	}
	if err := sync(); err != nil {
		t.Fatal(err)
	}
	t2 := e.get(t, w1.GetId()).GetPartOf()
	if got, err := store.Get[*planv1.Task](ctx, e.db, t2); err != nil || got.GetCode() != "T2" {
		t.Fatalf("W1 part of %q: %v, %v", t2, got, err)
	}

	// T2 deleted once W1 is regrouped: a new file with its code makes nothing.
	if _, err := e.group(t, w1, "T1"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.tasks.Delete(ctx, connect.NewRequest(&planv1.TaskServiceDeleteRequest{TaskId: t2})); err != nil {
		t.Fatal(err)
	}
	writeAzimaFile(t, dir, "again.md", "T2")
	if err := sync(); connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "never given twice") ||
		!strings.Contains(err.Error(), "T3 is free") {
		t.Errorf("a file with a retired code: %v", err)
	}
	for _, task := range e.list(t, wishID) {
		if task.GetCode() == "T2" {
			t.Errorf("T2 given again: %v", task)
		}
	}
}

// TestRecoverUngroupsOrphans: djinn up finds tasks part of an azima that is gone (deleted before Delete refused it):
// part of none from then on, journaled, the work saying so in its events; a task part of an azima that is still
// there keeps it.
func TestRecoverUngroupsOrphans(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	home := t.TempDir()
	db, err := store.Open(t.Context(), filepath.Join(home, store.File), plan.Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	wishID, gone := store.NewID(), store.NewID()
	azima := &planv1.Task{Id: store.NewID(), WishId: wishID, Code: "T1", Kind: planv1.TaskKind_TASK_KIND_AZIMA,
		Status: planv1.TaskStatus_TASK_STATUS_PENDING}
	kept := &planv1.Task{Id: store.NewID(), WishId: wishID, Code: "W1", PartOf: azima.GetId(), Status: planv1.TaskStatus_TASK_STATUS_DONE}
	orphan := &planv1.Task{Id: store.NewID(), WishId: wishID, Code: "W2", PartOf: gone, Status: planv1.TaskStatus_TASK_STATUS_DONE}
	sub := &planv1.Task{Id: store.NewID(), WishId: wishID, Code: "T2", Kind: planv1.TaskKind_TASK_KIND_AZIMA, PartOf: gone,
		Status: planv1.TaskStatus_TASK_STATUS_PENDING}
	err = db.Tx(t.Context(), func(tx *store.Tx) error {
		if err := tx.Journal("local", "test", azima); err != nil {
			return err
		}
		for _, task := range []*planv1.Task{azima, kept, orphan, sub} {
			if err := tx.Put(task); err != nil {
				return err
			}
		}
		return nil
	})
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	e := up(t, home)
	for task, want := range map[*planv1.Task]string{kept: azima.GetId(), orphan: "", sub: ""} {
		if got := e.get(t, task.GetId()).GetPartOf(); got != want {
			t.Errorf("%s part of %q, want %q", task.GetCode(), got, want)
		}
	}
	if texts := storedTexts(t, e.db, orphan.GetId()); !slices.Equal(texts, []string{
		"part of no azima: its azima " + gone[:8] + " is no longer one of the wish's"}) {
		t.Errorf("W2's events %q", texts)
	}
	ungrouped, err := store.Commands(t.Context(), e.db, func(c store.Command) bool { return c.Method == methodUngroup })
	if err != nil || len(ungrouped) != 2 {
		t.Errorf("%d journaled, %v; want W2 and T2", len(ungrouped), err)
	}
}

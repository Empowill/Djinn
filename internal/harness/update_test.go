package harness

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/cli"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

type spyProvider struct {
	mu    sync.Mutex
	specs []Spec
}

func (s *spyProvider) Start(ctx context.Context, spec Spec) (Worker, error) {
	s.mu.Lock()
	s.specs = append(s.specs, spec)
	s.mu.Unlock()
	return Fake{}.Start(ctx, spec)
}

func (s *spyProvider) lastSpec() Spec {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.specs) == 0 {
		return Spec{}
	}
	return s.specs[len(s.specs)-1]
}

func TestUpdate(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	ctx := t.Context()

	l := &limit{slots: 0}
	fakeSpy := &spyProvider{}
	claudeSpy := &spyProvider{}
	agySpy := &spyProvider{}
	providers := map[planv1.Provider]Provider{
		planv1.Provider_PROVIDER_FAKE:        fakeSpy,
		planv1.Provider_PROVIDER_CLAUDE:      claudeSpy,
		planv1.Provider_PROVIDER_ANTIGRAVITY: agySpy,
		planv1.Provider_PROVIDER_WATCH:       fastWatch,
	}
	e := upWith(t, t.TempDir(), providers, WithCapacity(l.capacity), WithTick(time.Hour))
	rootT := t
	wish0, projectID := e.wish(t, nativeFolder(t))
	lastWish := wish0
	lastHasSlots := false
	newWish := func(t *testing.T, slots int) string {
		t.Helper()
		hasSlots := slots > 0
		if e.last == lastWish && hasSlots == lastHasSlots {
			l.set(slots, "")
			return lastWish
		}
		if e.last != "" {
			if _, err := e.wishes.Pause(ctx, connect.NewRequest(&planv1.WishServicePauseRequest{WishId: e.last})); err != nil {
				t.Fatal(err)
			}
		}
		l.set(slots, "")
		w, err := e.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{
			Title: "Run Djinn on itself", ProjectIds: []string{projectID},
		}))
		if err != nil {
			t.Fatal(err)
		}
		e.last = w.Msg.GetWish().GetId()
		lastWish = e.last
		lastHasSlots = hasSlots
		return e.last
	}

	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "planned_all_fields",
			run: func(t *testing.T) {
				wishID := newWish(t, 0)
				second := e.mustSpawn(t, wishID, "Second task", "text second", nil)

				azRes, err := e.tasks.Spawn(ctx, connect.NewRequest(&planv1.TaskServiceSpawnRequest{
					WishId: wishID,
					Title:  "Phase 1 Azima",
					Kind:   planv1.TaskKind_TASK_KIND_AZIMA,
				}))
				if err != nil {
					t.Fatal(err)
				}
				azima := azRes.Msg.GetTask()

				blocks := planv1connect.NewBlockServiceClient(e.srv.Client(), e.srv.URL)
				bRes, err := blocks.Put(ctx, connect.NewRequest(&planv1.BlockServicePutRequest{
					WishId: wishID,
					Kind:   "decision",
					Title:  "Choose DB",
				}))
				if err != nil {
					t.Fatal(err)
				}
				decisionID := bRes.Msg.GetBlock().GetId()

				initialPrompt := "text initial prompt"
				first := e.mustSpawn(t, wishID, "Initial title", initialPrompt, &planv1.TaskServiceSpawnRequest{
					Provider: planv1.Provider_PROVIDER_FAKE,
				})
				dep := e.mustSpawn(t, wishID, "Dependent", "text dependent", &planv1.TaskServiceSpawnRequest{
					After: []string{first.GetCode()},
				})

				newTitle := "Updated Title"
				newPrompt := "text updated prompt"
				newProvider := planv1.Provider_PROVIDER_ANTIGRAVITY
				newModel := "gemini-3.8-flash-high"
				partOf := azima.GetCode()
				after := []string{second.GetCode()}
				budget := 3.50
				scopes := []string{"doc", "src"}

				res, err := e.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{
					TaskId:       first.GetId(),
					Title:        &newTitle,
					Prompt:       &newPrompt,
					Provider:     &newProvider,
					Model:        &newModel,
					PartOf:       &partOf,
					After:        after,
					Decision:     &decisionID,
					MaxBudgetUsd: &budget,
					WriteScopes:  scopes,
				}))
				if err != nil {
					t.Fatalf("Update: %v", err)
				}

				task := res.Msg.GetTask()
				if task.GetId() != first.GetId() || task.GetCode() != first.GetCode() {
					t.Errorf("task ID/Code changed: id %q vs %q, code %q vs %q", task.GetId(), first.GetId(), task.GetCode(), first.GetCode())
				}
				if task.GetTitle() != newTitle || task.GetPrompt() != newPrompt {
					t.Errorf("title/prompt: %q/%q, want %q/%q", task.GetTitle(), task.GetPrompt(), newTitle, newPrompt)
				}
				if task.GetProvider() != newProvider || task.GetModel() != newModel {
					t.Errorf("provider/model: %v/%s, want %v/%s", task.GetProvider(), task.GetModel(), newProvider, newModel)
				}
				if task.GetPartOf() != azima.GetId() || !slices.Contains(task.GetDependsOn(), second.GetId()) {
					t.Errorf("partOf/dependsOn: %q/%v", task.GetPartOf(), task.GetDependsOn())
				}
				if task.GetDecision() != decisionID || task.GetMaxBudgetUsd() != budget || !slices.Equal(task.GetWriteScopes(), scopes) {
					t.Errorf("decision/budget/scopes: %q/%v/%v", task.GetDecision(), task.GetMaxBudgetUsd(), task.GetWriteScopes())
				}
				if got := e.get(t, first.GetId()); got.GetPrompt() != newPrompt {
					t.Errorf("get prompt: %q, want %q", got.GetPrompt(), newPrompt)
				}

				events, err := store.List[*planv1.TaskEvent](ctx, e.db, store.Where{"task_id": first.GetId()})
				if err != nil {
					t.Fatal(err)
				}
				var promptTexts []string
				for _, ev := range events {
					if ev.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_PROMPT {
						promptTexts = append(promptTexts, ev.GetText())
					}
				}
				if len(promptTexts) != 2 || promptTexts[0] != initialPrompt || promptTexts[1] != newPrompt {
					t.Errorf("prompt history: %v, want [%q, %q]", promptTexts, initialPrompt, newPrompt)
				}
				if depGot := e.get(t, dep.GetId()); !slices.Contains(depGot.GetDependsOn(), first.GetId()) {
					t.Errorf("dep depends on: %v, want to contain %s", depGot.GetDependsOn(), first.GetId())
				}
			},
		},
		{
			name: "planned_only_given_fields_change",
			run: func(t *testing.T) {
				wishID := newWish(t, 0)
				firstPrompt := "text first"
				task := e.mustSpawn(t, wishID, "Initial Title", firstPrompt, &planv1.TaskServiceSpawnRequest{
					Provider: planv1.Provider_PROVIDER_ANTIGRAVITY,
					Model:    "gemini-3.8-flash-high",
				})

				newTitle := "Changed Title"
				res, err := e.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{
					TaskId: task.GetId(),
					Title:  &newTitle,
				}))
				if err != nil {
					t.Fatalf("Update title: %v", err)
				}
				if got := res.Msg.GetTask(); got.GetTitle() != newTitle || got.GetPrompt() != firstPrompt ||
					got.GetProvider() != planv1.Provider_PROVIDER_ANTIGRAVITY || got.GetModel() != "gemini-3.8-flash-high" {
					t.Errorf("after title update: %v", got)
				}

				secondPrompt := "text second prompt"
				res, err = e.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{
					TaskId: task.GetId(),
					Prompt: &secondPrompt,
				}))
				if err != nil {
					t.Fatalf("Update prompt: %v", err)
				}
				if got := res.Msg.GetTask(); got.GetPrompt() != secondPrompt || got.GetTitle() != newTitle {
					t.Errorf("after prompt update: %v", got)
				}

				customModel := "custom-model-v1"
				res, err = e.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{
					TaskId: task.GetId(),
					Model:  &customModel,
				}))
				if err != nil || res.Msg.GetTask().GetModel() != customModel {
					t.Fatalf("Update model: %v, got %q", err, res.Msg.GetTask().GetModel())
				}

				emptyModel := ""
				res, err = e.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{
					TaskId: task.GetId(),
					Model:  &emptyModel,
				}))
				if err != nil || res.Msg.GetTask().GetModel() != "" {
					t.Fatalf("Update empty model: %v, got %q", err, res.Msg.GetTask().GetModel())
				}

				claude := planv1.Provider_PROVIDER_CLAUDE
				res, err = e.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{
					TaskId:   task.GetId(),
					Provider: &claude,
				}))
				if err != nil {
					t.Fatalf("Update provider: %v", err)
				}
				if got := res.Msg.GetTask(); got.GetProvider() != planv1.Provider_PROVIDER_CLAUDE || got.GetModel() != "claude-sonnet-5-5" {
					t.Errorf("after provider update: provider=%v model=%q", got.GetProvider(), got.GetModel())
				}
			},
		},
		{
			name: "nothing_given_cycle_and_draft_azima",
			run: func(t *testing.T) {
				wishID := newWish(t, 0)
				taskA := e.mustSpawn(t, wishID, "Task A", "text A", nil)
				taskB := e.mustSpawn(t, wishID, "Task B", "text B", &planv1.TaskServiceSpawnRequest{
					After: []string{taskA.GetCode()},
				})

				_, err := e.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{TaskId: taskA.GetId()}))
				if connect.CodeOf(err) != connect.CodeInvalidArgument || !strings.Contains(err.Error(), "nothing to update: give at least one field") {
					t.Errorf("nothing given: %v", err)
				}

				_, err = e.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{
					TaskId: taskA.GetId(),
					After:  []string{taskA.GetCode()},
				}))
				if connect.CodeOf(err) != connect.CodeInvalidArgument || !strings.Contains(err.Error(), "task "+taskA.GetCode()+" cannot wait for itself") {
					t.Errorf("self-wait: %v", err)
				}

				_, err = e.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{
					TaskId: taskA.GetId(),
					After:  []string{taskB.GetCode()},
				}))
				if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "the tasks of a wish form no cycle") {
					t.Errorf("cycle: %v", err)
				}

				azRes, err := e.tasks.Spawn(ctx, connect.NewRequest(&planv1.TaskServiceSpawnRequest{
					WishId: wishID,
					Title:  "Draft Azima",
					Kind:   planv1.TaskKind_TASK_KIND_AZIMA,
					Draft:  true,
				}))
				if err != nil {
					t.Fatal(err)
				}
				draftCode := azRes.Msg.GetTask().GetCode()
				_, err = e.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{
					TaskId: taskA.GetId(),
					PartOf: &draftCode,
				}))
				wantDraftErr := "azima " + draftCode + " is a draft: open it first with djinn task open " + draftCode
				if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), wantDraftErr) {
					t.Errorf("draft azima: %v, want %q", err, wantDraftErr)
				}
			},
		},
		{
			name: "running_and_paused_rules",
			run: func(t *testing.T) {
				wishID := newWish(t, 2)
				task := e.spawn(t, wishID, "wait")
				if task.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
					t.Fatalf("task status: %v, want running", task.GetStatus())
				}

				newTitle := "Running Title Updated"
				res, err := e.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{
					TaskId: task.GetId(),
					Title:  &newTitle,
				}))
				if err != nil || res.Msg.GetTask().GetTitle() != newTitle {
					t.Fatalf("update title while running: %v, got %q", err, res.Msg.GetTask().GetTitle())
				}

				p := "new prompt"
				_, err = e.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{
					TaskId: task.GetId(),
					Prompt: &p,
				}))
				if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "task "+task.GetCode()+" is running: use djinn task send") {
					t.Errorf("update prompt while running: %v", err)
				}

				claude := planv1.Provider_PROVIDER_CLAUDE
				_, err = e.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{
					TaskId:   task.GetId(),
					Provider: &claude,
				}))
				if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "task "+task.GetCode()+" is running: the rest after it ends") {
					t.Errorf("update provider while running: %v", err)
				}

				if _, err := e.tasks.Pause(ctx, connect.NewRequest(&planv1.TaskServicePauseRequest{TaskId: task.GetId()})); err != nil {
					t.Fatalf("pause: %v", err)
				}

				pausedTitle := "Paused Title Updated"
				res, err = e.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{
					TaskId: task.GetId(),
					Title:  &pausedTitle,
				}))
				if err != nil || res.Msg.GetTask().GetTitle() != pausedTitle {
					t.Fatalf("update title while paused: %v, got %q", err, res.Msg.GetTask().GetTitle())
				}

				_, err = e.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{
					TaskId: task.GetId(),
					Prompt: &p,
				}))
				if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "task "+task.GetCode()+" is paused: use djinn task send") {
					t.Errorf("update prompt while paused: %v", err)
				}

				_, err = e.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{
					TaskId:   task.GetId(),
					Provider: &claude,
				}))
				if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "task "+task.GetCode()+" is paused: the rest after it ends") {
					t.Errorf("update provider while paused: %v", err)
				}

				if _, err := e.tasks.Stop(ctx, connect.NewRequest(&planv1.TaskServiceStopRequest{TaskId: task.GetId()})); err != nil {
					t.Fatalf("stop: %v", err)
				}
				e.ended(t, task.GetId())
			},
		},
		{
			name: "finished_task_rules_and_continue",
			run: func(t *testing.T) {
				wishID := newWish(t, 2)
				azRes, err := e.tasks.Spawn(ctx, connect.NewRequest(&planv1.TaskServiceSpawnRequest{
					WishId: wishID,
					Title:  "Phase 2 Azima",
					Kind:   planv1.TaskKind_TASK_KIND_AZIMA,
				}))
				if err != nil {
					t.Fatal(err)
				}
				azima := azRes.Msg.GetTask()
				anotherTask := e.mustSpawn(t, wishID, "Another", "text", &planv1.TaskServiceSpawnRequest{Later: true, WriteScopes: []string{"s1"}})
				first := e.mustSpawn(t, wishID, "Failed task", "text first run\nfail tests broke", &planv1.TaskServiceSpawnRequest{WriteScopes: []string{"s2"}})
				e.ended(t, anotherTask.GetId())
				first = e.ended(t, first.GetId())
				if first.GetStatus() != planv1.TaskStatus_TASK_STATUS_FAILED {
					t.Fatalf("task status: %v, want failed", first.GetStatus())
				}

				_, err = e.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{
					TaskId: first.GetId(),
					After:  []string{anotherTask.GetCode()},
				}))
				if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "task "+first.GetCode()+" has run: never its dependencies once it ran") {
					t.Errorf("update dependencies on finished task: %v", err)
				}

				budget := 10.0
				_, err = e.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{
					TaskId:       first.GetId(),
					MaxBudgetUsd: &budget,
				}))
				if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "task "+first.GetCode()+" has run: only title, prompt, provider, model and azima may change") {
					t.Errorf("update budget on finished task: %v", err)
				}

				newTitle := "Failed Task Renamed"
				updatedPrompt := "text continued run\nusage 100 10 0.01"
				partOf := azima.GetCode()
				if res, err := e.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{
					TaskId: anotherTask.GetId(),
					PartOf: &partOf,
				})); err != nil || res.Msg.GetTask().GetPartOf() != azima.GetId() {
					t.Fatalf("update finished task partOf: err=%v partOf=%q", err, res.Msg.GetTask().GetPartOf())
				}

				res, err := e.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{
					TaskId: first.GetId(),
					Title:  &newTitle,
					Prompt: &updatedPrompt,
				}))
				if err != nil {
					t.Fatalf("update finished task allowed fields: %v", err)
				}
				if task := res.Msg.GetTask(); task.GetTitle() != newTitle || task.GetPrompt() != updatedPrompt {
					t.Errorf("updated finished task: %v", task)
				}

				cont, err := e.tasks.Continue(ctx, connect.NewRequest(&planv1.TaskServiceContinueRequest{TaskId: first.GetId()}))
				if err != nil {
					t.Fatalf("Continue: %v", err)
				}
				if cont.Msg.GetTask().GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
					t.Errorf("status after continue: %v, want running", cont.Msg.GetTask().GetStatus())
				}
				done := e.ended(t, first.GetId())
				if done.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
					t.Errorf("status: %v, want done", done.GetStatus())
				}
				if spec := fakeSpy.lastSpec(); !strings.Contains(spec.Prompt, "text continued run") {
					t.Errorf("worker Prompt: %q, want to contain 'text continued run'", spec.Prompt)
				}
				events := e.watch(ctx, t, first.GetId(), 0)
				var promptCount int
				for _, ev := range events {
					if ev.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_PROMPT {
						promptCount++
					}
				}
				if promptCount != 2 {
					t.Errorf("PROMPT events count: %d, want 2", promptCount)
				}
			},
		},
		{
			name: "failed_continued_starts_new_session_and_provider_model_rules",
			run: func(t *testing.T) {
				wishID := newWish(t, 2)
				firstPrompt := "text hello from claude\nfail tests broke"
				first := e.mustSpawn(t, wishID, "Failed task", firstPrompt, &planv1.TaskServiceSpawnRequest{
					Provider:    planv1.Provider_PROVIDER_CLAUDE,
					WriteScopes: []string{"s1"},
				})
				streamed := e.mustSpawn(t, wishID, "Antigravity task", "model gemini-3.8-flash-high\ntext working\nfail Antigravity failed", &planv1.TaskServiceSpawnRequest{
					Provider:    planv1.Provider_PROVIDER_ANTIGRAVITY,
					WriteScopes: []string{"s2"},
				})
				first = e.ended(t, first.GetId())
				streamed = e.ended(t, streamed.GetId())

				agy := planv1.Provider_PROVIDER_ANTIGRAVITY
				model := "gemini-3.8-flash-high"
				if _, err := e.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{
					TaskId:   first.GetId(),
					Provider: &agy,
					Model:    &model,
				})); err != nil {
					t.Fatalf("Update: %v", err)
				}

				contPrompt := "text continue prompt"
				cont, err := e.tasks.Continue(ctx, connect.NewRequest(&planv1.TaskServiceContinueRequest{
					TaskId: first.GetId(),
					Prompt: &contPrompt,
				}))
				if err != nil {
					t.Fatalf("continue: %v", err)
				}
				if cont.Msg.GetTask().GetProvider() != planv1.Provider_PROVIDER_ANTIGRAVITY {
					t.Errorf("continued provider: %v, want antigravity", cont.Msg.GetTask().GetProvider())
				}
				done := e.ended(t, first.GetId())

				spec := agySpy.lastSpec()
				if spec.Resume != "" || !strings.Contains(spec.Prompt, "text hello from claude") ||
					strings.Contains(spec.Prompt, "text continue prompt") || spec.Model != model {
					t.Errorf("antigravity spec: resume=%q model=%q prompt=%q", spec.Resume, spec.Model, spec.Prompt)
				}

				events := e.watch(ctx, t, done.GetId(), 0)
				wantEvent := "provider changed: claude → antigravity, starts from its first prompt"
				var foundChangeText bool
				for _, ev := range events {
					if strings.Contains(ev.GetText(), wantEvent) {
						foundChangeText = true
						break
					}
				}
				if !foundChangeText {
					t.Errorf("events lack %q", wantEvent)
				}

				// Provider change without --model resets recorded stream model to new provider's default; with --model keeps it.
				if streamed.GetModel() != "gemini-3.8-flash-high" {
					t.Fatalf("task recorded model: %q", streamed.GetModel())
				}
				claude := planv1.Provider_PROVIDER_CLAUDE
				res, err := e.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{
					TaskId:   streamed.GetId(),
					Provider: &claude,
				}))
				if err != nil {
					t.Fatalf("Update provider without model: %v", err)
				}
				if res.Msg.GetTask().GetProvider() != planv1.Provider_PROVIDER_CLAUDE || res.Msg.GetTask().GetModel() != "claude-sonnet-5-5" {
					t.Errorf("after provider reset: %v", res.Msg.GetTask())
				}

				explicitModel := "claude-3-5-haiku-20241022"
				res, err = e.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{
					TaskId:   streamed.GetId(),
					Provider: &claude,
					Model:    &explicitModel,
				}))
				if err != nil || res.Msg.GetTask().GetModel() != explicitModel || e.get(t, streamed.GetId()).GetModel() != explicitModel {
					t.Errorf("explicit model: err=%v model=%q", err, res.Msg.GetTask().GetModel())
				}
			},
		},
		{
			name: "continue_with_foreign_model_sanitizes",
			run: func(t *testing.T) {
				wishID := newWish(t, 2)
				first := e.ended(t, e.mustSpawn(t, wishID, "Failed claude task", "fail broke", &planv1.TaskServiceSpawnRequest{
					Provider: planv1.Provider_PROVIDER_CLAUDE,
				}).GetId())

				task := e.get(t, first.GetId())
				task.Model = "gemini-3.8-flash-high"
				if err := e.db.Tx(ctx, func(tx *store.Tx) error {
					if err := tx.Journal("test", "test/put", task); err != nil {
						return err
					}
					return tx.Put(task)
				}); err != nil {
					t.Fatal(err)
				}

				p := "text continue"
				cont, err := e.tasks.Continue(ctx, connect.NewRequest(&planv1.TaskServiceContinueRequest{
					TaskId: first.GetId(),
					Prompt: &p,
				}))
				if err != nil {
					t.Fatalf("continue: %v", err)
				}
				if got, want := cont.Msg.GetTask().GetModel(), "claude-sonnet-5-5"; got != want {
					t.Errorf("continued response model: %q, want %q", got, want)
				}
				e.ended(t, first.GetId())
				if got, want := claudeSpy.lastSpec().Model, "claude-sonnet-5-5"; got != want {
					t.Errorf("worker spec.Model: %q, want %q", got, want)
				}
			},
		},
		{
			name: "watcher_and_azima_rules",
			run: func(t *testing.T) {
				l.set(0, "")
				dir := rootT.TempDir()
				writeFile(t, dir, "AGENTS.md", "# Rules\n")
				file := filepath.Join(dir, plan.PlanDir, "t1.md")
				writeFile(t, dir, filepath.Join(plan.PlanDir, "t1.md"),
					"---\ncode: T1\nstatus: in-progress\n---\n\n# T1 · Old title\n\nThe goal.\n\n## Done when\n\n- [ ] it ships\n")
				wishID, _ := e.wish(t, dir)

				watcher := e.mustSpawn(t, wishID, "Watcher task", "echo hi", &planv1.TaskServiceSpawnRequest{
					Provider: planv1.Provider_PROVIDER_WATCH,
				})
				claude := planv1.Provider_PROVIDER_CLAUDE
				_, err := e.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{
					TaskId:   watcher.GetId(),
					Provider: &claude,
				}))
				if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "task "+watcher.GetCode()+" is a watcher: no agent runs it") {
					t.Errorf("update watcher provider: %v", err)
				}

				res, err := e.tasks.Spawn(ctx, connect.NewRequest(&planv1.TaskServiceSpawnRequest{
					WishId: wishID,
					Title:  "Phase 1 Azima",
					Kind:   planv1.TaskKind_TASK_KIND_AZIMA,
				}))
				if err != nil {
					t.Fatal(err)
				}
				azima := res.Msg.GetTask()

				var out, errs bytes.Buffer
				code := cli.Run(ctx, []string{"task", "update", azima.GetId(), "--title", "Phase 1 reaches main: PR #3 merged"},
					cli.Config{Version: "test", Addr: e.srv.URL, HTTP: e.srv.Client(), Stdout: &out, Stderr: &errs})
				if code != 0 || !strings.Contains(out.String(), "title: Phase 1 reaches main: PR #3 merged") {
					t.Errorf("djinn task update --title on an azima: code %d\nstdout:\n%s\nstderr:\n%s", code, out.String(), errs.String())
				}
				if got := e.get(t, azima.GetId()); got.GetTitle() != "Phase 1 reaches main: PR #3 merged" || !plan.IsAzima(got) {
					t.Errorf("stored azima: title %q, kind %v", got.GetTitle(), got.GetKind())
				}

				prompt := "text do it"
				newTitle := "Renamed with a prompt"
				_, err = e.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{
					TaskId: azima.GetId(),
					Title:  &newTitle,
					Prompt: &prompt,
				}))
				wantAzimaErr := "task " + azima.GetCode() + " is an azima of the plan: no worker runs it; only its title, or its branch, changes"
				if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), wantAzimaErr) {
					t.Errorf("update azima prompt: %v", err)
				}
				twoLines := "One\ntwo"
				if _, err := e.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{TaskId: azima.GetId(), Title: &twoLines})); connect.CodeOf(err) != connect.CodeInvalidArgument {
					t.Errorf("two-line azima title: %v", err)
				}

				// Plan file azima rename and sync.
				sync := func() {
					t.Helper()
					if _, err := e.plans.Sync(ctx, connect.NewRequest(&planv1.PlanServiceSyncRequest{WishId: wishID})); err != nil {
						t.Fatal(err)
					}
				}
				sync()
				var t1 *planv1.Task
				for _, task := range e.list(t, wishID) {
					if task.GetCode() == "T1" {
						t1 = task
					}
				}
				if t1 == nil || t1.GetTitle() != "Old title" {
					t.Fatalf("T1 after sync: %v", t1)
				}
				out.Reset()
				errs.Reset()
				code = cli.Run(ctx, []string{"task", "update", t1.GetId(), "--title", "feat/x reaches main: PR #3 merged"},
					cli.Config{Version: "test", Addr: e.srv.URL, HTTP: e.srv.Client(), Stdout: &out, Stderr: &errs})
				if code != 0 || !strings.Contains(out.String(), "title: feat/x reaches main: PR #3 merged") {
					t.Errorf("djinn task update --title on plan azima: code %d\nstdout:\n%s\nstderr:\n%s", code, out.String(), errs.String())
				}
				data, err := os.ReadFile(file)
				if err != nil {
					t.Fatal(err)
				}
				want := "---\ncode: T1\nstatus: in-progress\n---\n\n# T1 · feat/x reaches main: PR #3 merged\n\nThe goal.\n\n## Done when\n\n- [ ] it ships\n"
				if string(data) != want {
					t.Errorf("plan file:\n%s\nwant:\n%s", data, want)
				}
				sync()
				if got := e.get(t, t1.GetId()); got.GetTitle() != "feat/x reaches main: PR #3 merged" || got.GetDescription() != "The goal.\n\n## Done when\n\n- [ ] it ships" {
					t.Errorf("T1 after a sync: title %q, description %q", got.GetTitle(), got.GetDescription())
				}
			},
		},
		{
			name: "journal_cli_and_alias",
			run: func(t *testing.T) {
				wishID := newWish(t, 0)
				firstPrompt := "text hello CLI"
				task := e.mustSpawn(t, wishID, "CLI test task", firstPrompt, &planv1.TaskServiceSpawnRequest{
					Provider: planv1.Provider_PROVIDER_FAKE,
				})

				beforeCmds, err := store.Commands(ctx, e.db, func(c store.Command) bool {
					return c.Method == planv1connect.TaskServiceUpdateProcedure
				})
				if err != nil {
					t.Fatal(err)
				}

				newTitle := "Journaled Title"
				newPrompt := "text journaled prompt"
				if _, err := e.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{
					TaskId: task.GetId(),
					Title:  &newTitle,
					Prompt: &newPrompt,
				})); err != nil {
					t.Fatalf("Update: %v", err)
				}
				afterCmds, err := store.Commands(ctx, e.db, func(c store.Command) bool {
					return c.Method == planv1connect.TaskServiceUpdateProcedure
				})
				if err != nil || len(afterCmds)-len(beforeCmds) != 1 {
					t.Errorf("journaled %d Update commands (%v), want 1", len(afterCmds)-len(beforeCmds), err)
				}

				var out, errs bytes.Buffer
				code := cli.Run(ctx, []string{"task", "get", task.GetId()},
					cli.Config{Version: "test", Addr: e.srv.URL, HTTP: e.srv.Client(), Stdout: &out, Stderr: &errs})
				if code != 0 || !strings.Contains(out.String(), "prompt: "+newPrompt) {
					t.Errorf("djinn task get: code %d\nstdout:\n%s\nstderr:\n%s", code, out.String(), errs.String())
				}

				out.Reset()
				errs.Reset()
				code = cli.Run(ctx, []string{"task", "update", task.GetId(), "--title", "CLI Updated Title", "--prompt", "text updated prompt CLI"},
					cli.Config{Version: "test", Addr: e.srv.URL, HTTP: e.srv.Client(), Stdout: &out, Stderr: &errs})
				if code != 0 || !strings.Contains(out.String(), "title: CLI Updated Title") || !strings.Contains(out.String(), "prompt: text updated prompt CLI") {
					t.Errorf("djinn task update: code %d\nstdout:\n%s\nstderr:\n%s", code, out.String(), errs.String())
				}

				out.Reset()
				errs.Reset()
				code = cli.Run(ctx, []string{"task", "set-agent", task.GetId(), "--provider", "antigravity", "--model", "gemini-3.8-flash-high"},
					cli.Config{Version: "test", Addr: e.srv.URL, HTTP: e.srv.Client(), Stdout: &out, Stderr: &errs})
				if code != 0 || !strings.Contains(out.String(), "provider: antigravity") || !strings.Contains(out.String(), "model: gemini-3.8-flash-high") {
					t.Errorf("djinn task set-agent: code %d\nstdout:\n%s\nstderr:\n%s", code, out.String(), errs.String())
				}

				out.Reset()
				errs.Reset()
				code = cli.Run(ctx, []string{"task", "set-agent", task.GetId(), "--model", ""},
					cli.Config{Version: "test", Addr: e.srv.URL, HTTP: e.srv.Client(), Stdout: &out, Stderr: &errs})
				if code != 0 {
					t.Errorf("djinn task set-agent --model \"\": code %d\nstdout:\n%s\nstderr:\n%s", code, out.String(), errs.String())
				}
				stored, err := store.Get[*planv1.Task](ctx, e.db, task.GetId())
				if err != nil {
					t.Fatal(err)
				}
				if stored.GetModel() != "" {
					t.Errorf("model after clearing: %q, want empty", stored.GetModel())
				}

				out.Reset()
				errs.Reset()
				code = cli.Run(ctx, []string{"task", "set-agent", task.GetId(), "--provider", "claude"},
					cli.Config{Version: "test", Addr: e.srv.URL, HTTP: e.srv.Client(), Stdout: &out, Stderr: &errs})
				if code != 0 || !strings.Contains(out.String(), "provider: claude") || !strings.Contains(out.String(), "model: claude-sonnet-5-5") {
					t.Errorf("djinn task set-agent --provider claude: code %d\nstdout:\n%s\nstderr:\n%s", code, out.String(), errs.String())
				}
				if got := e.get(t, task.GetId()); got.GetModel() != "claude-sonnet-5-5" {
					t.Errorf("model after provider change: %q, want %q", got.GetModel(), "claude-sonnet-5-5")
				}
			},
		},
		{
			name: "integration_none_and_unmerged_branch",
			run: func(t *testing.T) {
				l.set(0, "")
				repo := gitRepo(rootT)
				mainBranch := plan.CheckedOutBranch(ctx, repo)
				wishID, projectID := e.wish(t, repo)

				b := "branch-with-commits"
				if _, err := git(ctx, repo, "checkout", "-b", b); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(repo, "app", "test.txt"), []byte("unmerged\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if _, err := git(ctx, repo, "add", "."); err != nil {
					t.Fatal(err)
				}
				if _, err := git(ctx, repo, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "unmerged commit"); err != nil {
					t.Fatal(err)
				}
				if _, err := git(ctx, repo, "checkout", mainBranch); err != nil {
					t.Fatal(err)
				}

				task := &planv1.Task{
					Id:        store.NewID(),
					WishId:    wishID,
					ProjectId: projectID,
					Code:      "W1",
					Title:     "Task with unmerged commits",
					Status:    planv1.TaskStatus_TASK_STATUS_DONE,
					Branch:    b,
					Integration: &planv1.TaskIntegration{
						State: planv1.IntegrationState_INTEGRATION_STATE_INTEGRATING,
						Sha:   "deadbeef",
					},
					CreateTime: timestamppb.Now(),
				}
				putTask := func(tk *planv1.Task) {
					if err := e.db.Tx(ctx, func(tx *store.Tx) error {
						_ = tx.Journal("test", "put", tk)
						return tx.Put(tk)
					}); err != nil {
						t.Fatal(err)
					}
				}
				putTask(task)

				none := "none"
				// 1. Refused while integrating.
				if _, err := e.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{
					TaskId:      task.GetId(),
					Integration: &none,
				})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
					t.Fatalf("while integrating, got %v, want FailedPrecondition", err)
				}

				// 2. Committed on unmerged branch refused without --force, allowed with --force.
				task.Integration.State = planv1.IntegrationState_INTEGRATION_STATE_COMMITTED
				putTask(task)

				if _, err := e.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{
					TaskId:      task.GetId(),
					Integration: &none,
				})); connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "--force") {
					t.Fatalf("expected FailedPrecondition with --force, got: %v", err)
				}

				var out, errs bytes.Buffer
				code := cli.Run(ctx, []string{"task", "update", task.GetId(), "--integration", "none"},
					cli.Config{Version: "test", Addr: e.srv.URL, HTTP: e.srv.Client(), Stdout: &out, Stderr: &errs})
				if code == 0 || !strings.Contains(errs.String(), "--force") {
					t.Fatalf("cli run should fail without --force, got code %d, errs: %s", code, errs.String())
				}

				out.Reset()
				errs.Reset()
				code = cli.Run(ctx, []string{"task", "update", task.GetId(), "--integration", "none", "--force"},
					cli.Config{Version: "test", Addr: e.srv.URL, HTTP: e.srv.Client(), Stdout: &out, Stderr: &errs})
				if code != 0 {
					t.Fatalf("cli run with --force failed with code %d: %s", code, errs.String())
				}
				if after := e.get(t, task.GetId()); after.GetIntegration() != nil {
					t.Fatalf("task integration after update --integration none --force: got %v, want nil", after.GetIntegration())
				}

				// 3. Without unmerged branch, --integration none succeeds without --force.
				task.Branch = ""
				task.Integration = &planv1.TaskIntegration{
					State: planv1.IntegrationState_INTEGRATION_STATE_COMMITTED,
					Sha:   "deadbeef",
				}
				putTask(task)
				out.Reset()
				errs.Reset()
				code = cli.Run(ctx, []string{"task", "update", task.GetId(), "--integration", "none"},
					cli.Config{Version: "test", Addr: e.srv.URL, HTTP: e.srv.Client(), Stdout: &out, Stderr: &errs})
				if code != 0 || e.get(t, task.GetId()).GetIntegration() != nil {
					t.Fatalf("djinn task update --integration none failed: code=%d errs=%s", code, errs.String())
				}
			},
		},
		{
			name: "planned_reflected_in_list_and_watch",
			run: func(t *testing.T) {
				wishID := newWish(t, 0)
				dep := e.mustSpawn(t, wishID, "Dependency task", "text dep", nil)
				first := e.mustSpawn(t, wishID, "Initial title", "text initial prompt", &planv1.TaskServiceSpawnRequest{
					Provider: planv1.Provider_PROVIDER_FAKE,
				})

				wishes := planv1connect.NewWishServiceClient(e.srv.Client(), e.srv.URL)
				watchCtx, cancelWatch := context.WithCancel(ctx)
				defer cancelWatch()
				stream, err := wishes.Watch(watchCtx, connect.NewRequest(&planv1.WishServiceWatchRequest{}))
				if err != nil {
					t.Fatalf("Watch: %v", err)
				}
				if !stream.Receive() {
					t.Fatalf("initial watch receive: %v", stream.Err())
				}

				onlyPrompt := "text prompt-only rewrite"
				if _, err := e.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{
					TaskId: first.GetId(),
					Prompt: &onlyPrompt,
				})); err != nil {
					t.Fatalf("Update prompt only: %v", err)
				}

				var watchedPromptOnly *planv1.Task
				for stream.Receive() {
					msg := stream.Msg()
					if msg.GetWishId() != wishID {
						continue
					}
					for _, ct := range msg.GetChanged().GetTasks() {
						if ct.GetId() == first.GetId() {
							watchedPromptOnly = ct
							break
						}
					}
					if watchedPromptOnly != nil {
						break
					}
				}
				if watchedPromptOnly == nil || watchedPromptOnly.GetPrompt() != onlyPrompt {
					t.Fatalf("watched task after prompt-only update: got %v, want prompt %q", watchedPromptOnly, onlyPrompt)
				}

				newTitle := "Rewritten Title"
				newPrompt := "text rewritten prompt"
				newProvider := planv1.Provider_PROVIDER_ANTIGRAVITY
				newModel := "gemini-3.8-flash-high"
				after := []string{dep.GetCode()}

				if _, err := e.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{
					TaskId:   first.GetId(),
					Title:    &newTitle,
					Prompt:   &newPrompt,
					Provider: &newProvider,
					Model:    &newModel,
					After:    after,
				})); err != nil {
					t.Fatalf("Update: %v", err)
				}

				var watchedTask *planv1.Task
				for stream.Receive() {
					msg := stream.Msg()
					if msg.GetWishId() != wishID {
						continue
					}
					for _, ct := range msg.GetChanged().GetTasks() {
						if ct.GetId() == first.GetId() {
							watchedTask = ct
							break
						}
					}
					if watchedTask != nil {
						break
					}
				}
				if watchedTask == nil {
					t.Fatalf("watch stream ended without updated task: %v", stream.Err())
				}
				if watchedTask.GetPrompt() != newPrompt || watchedTask.GetTitle() != newTitle ||
					watchedTask.GetProvider() != newProvider || watchedTask.GetModel() != newModel ||
					!slices.Equal(watchedTask.GetDependsOn(), []string{dep.GetId()}) {
					t.Errorf("watched task after update: %v", watchedTask)
				}

				listRes, err := e.tasks.List(ctx, connect.NewRequest(&planv1.TaskServiceListRequest{WishId: wishID}))
				if err != nil {
					t.Fatalf("List: %v", err)
				}
				var listedTask *planv1.Task
				for _, lt := range listRes.Msg.GetTasks() {
					if lt.GetId() == first.GetId() {
						listedTask = lt
						break
					}
				}
				if listedTask == nil || listedTask.GetPrompt() != newPrompt || listedTask.GetTitle() != newTitle ||
					listedTask.GetProvider() != newProvider || listedTask.GetModel() != newModel ||
					!slices.Equal(listedTask.GetDependsOn(), []string{dep.GetId()}) {
					t.Errorf("listed task after update: %v", listedTask)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, tc.run)
	}
}

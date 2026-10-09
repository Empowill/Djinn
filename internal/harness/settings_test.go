package harness

import (
	"strings"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
)

func TestSpawnTakesTheProjectSettings(t *testing.T) {
	home, dir := t.TempDir(), t.TempDir()
	writeFile(t, dir, ".agents/settings.txtpb", "provider: PROVIDER_FAKE\nmodel: \"team-model\"\nmax_budget_usd: 2\n")
	e := up(t, home)
	wishID, projectID := e.wish(t, dir)
	devFile := plan.DeveloperSettingsFile(home, projectID)
	devFileName := "projects/" + projectID + "/settings.txtpb"
	spawn := func(req *planv1.TaskServiceSpawnRequest) (*planv1.Task, error) {
		t.Helper()
		req.WishId, req.Title, req.Prompt = wishID, "Try the settings", "result ok"
		res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(req))
		if err != nil {
			return nil, err
		}
		e.watch(t.Context(), t, res.Msg.GetTask().GetId(), 0) // Outside Git, the next task waits for this one.
		return res.Msg.GetTask(), nil
	}
	check := func(name string, req *planv1.TaskServiceSpawnRequest, provider planv1.Provider, model string, budget float64) {
		t.Helper()
		task, err := spawn(req)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if task.GetProvider() != provider || task.GetModel() != model || task.GetMaxBudgetUsd() != budget {
			t.Errorf("%s: %s, %q, $%v; want %s, %q, $%v", name, task.GetProvider(), task.GetModel(), task.GetMaxBudgetUsd(),
				provider, model, budget)
		}
	}
	fake := planv1.Provider_PROVIDER_FAKE

	check("the repository's", &planv1.TaskServiceSpawnRequest{}, fake, "team-model", 2)
	writeFile(t, home, devFileName, "model: \"my-model\"\n")
	check("the developer's win", &planv1.TaskServiceSpawnRequest{}, fake, "my-model", 2)
	check("a task's flags win over both", &planv1.TaskServiceSpawnRequest{Model: "flag", MaxBudgetUsd: 5}, fake, "flag", 5)
	writeFile(t, home, devFileName, "provider: PROVIDER_CODEX\nmodel: \"gpt-5.1-codex\"\nmax_budget_usd: 0\n")
	check("a model goes to its own provider only", &planv1.TaskServiceSpawnRequest{Provider: fake}, fake, "", 0)

	// A malformed file refuses the project's tasks, with why; Djinn still starts.
	writeFile(t, home, devFileName, "max_budget_usd: lots\n")
	if _, err := spawn(&planv1.TaskServiceSpawnRequest{Provider: fake}); connect.CodeOf(err) != connect.CodeFailedPrecondition ||
		!strings.Contains(err.Error(), devFile) {
		t.Errorf("a malformed file: %v; want failed_precondition naming %s", err, devFile)
	}
	writeFile(t, dir, ".agents/settings.txtpb", "api_key: \"abc\"\n")
	if _, err := spawn(&planv1.TaskServiceSpawnRequest{Provider: fake}); connect.CodeOf(err) != connect.CodeFailedPrecondition ||
		!strings.Contains(err.Error(), "holds no secret") {
		t.Errorf("a secret in the repository's file: %v; want failed_precondition", err)
	}
	e.down()
	e = up(t, home)
	if _, err := e.project.List(t.Context(), connect.NewRequest(&planv1.ProjectServiceListRequest{})); err != nil {
		t.Errorf("djinn up with a malformed settings file: %v", err)
	}
}

package plan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// writeSettings writes content at path, creating its folders.
func writeSettings(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReadSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.txtpb")
	if s, err := ReadSettings(path); s != nil || err != nil {
		t.Errorf("no file: %v, %v", s, err)
	}
	writeSettings(t, path, "# proto-message: plan.v1.ProjectSettings\nprovider: PROVIDER_CODEX\nmodel: \"gpt-5.1-codex\"\n"+
		"max_budget_usd: 2.5\n")
	s, err := ReadSettings(path)
	if err != nil || s.GetProvider() != planv1.Provider_PROVIDER_CODEX || s.GetModel() != "gpt-5.1-codex" || s.GetMaxBudgetUsd() != 2.5 {
		t.Errorf("valid file: %v, %v", s, err)
	}
	for _, model := range []string{"claude-sonnet-4-5-20250929", "us.anthropic.claude-opus-4-1-20250805-v1:0", "opus[1m]"} {
		writeSettings(t, path, "model: \""+model+"\"\n")
		if s, err := ReadSettings(path); err != nil || s.GetModel() != model {
			t.Errorf("model %s: %v, %v", model, s, err)
		}
	}
	for _, bad := range []string{
		"provider: PROVIDER_CLAUDE\nprovider: \n",
		"unknown_field: 1\n",
		"provider: PROVIDER_WATCH\n",
		"provider: PROVIDER_UNSPECIFIED\n",
		"max_budget_usd: -1\n",
		"model: \"opus; rm -rf /\"\n",
	} {
		writeSettings(t, path, bad)
		if s, err := ReadSettings(path); err == nil || !strings.Contains(err.Error(), path) {
			t.Errorf("%q: %v, %v; want an error naming the file", bad, s, err)
		}
	}
}

func TestSettingsRefuseSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.txtpb")
	for _, secret := range []string{
		"api_key: \"abc\"\n",
		"  ANTHROPIC_AUTH_TOKEN: \"abc\"\n",
		"model: \"sk-ant-api03-AbCdEfGhIjKlMnOp\"\n",
		"# the team's token: ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ012345\nprovider: PROVIDER_CLAUDE\n",
		"model: \"aZ09bY18cX27dW36eV45fU54gT63hS72\"\n",
		"model: \"AKIAIOSFODNN7EXAMPLE\"\n",
	} {
		writeSettings(t, path, secret)
		s, err := ReadSettings(path)
		if err == nil || !strings.Contains(err.Error(), "holds no secret") {
			t.Errorf("%q: %v, %v; want it refused as a secret", secret, s, err)
			continue
		}
		for _, w := range word.FindAllString(secret, -1) {
			if len(w) >= 16 && strings.ContainsAny(w, "0123456789") && strings.Contains(err.Error(), w) {
				t.Errorf("%q: the error repeats the secret: %v", secret, err)
			}
		}
	}
}

func TestResolveSettings(t *testing.T) {
	const (
		def  = planv1.SettingSource_SETTING_SOURCE_DEFAULT
		repo = planv1.SettingSource_SETTING_SOURCE_REPOSITORY
		dev  = planv1.SettingSource_SETTING_SOURCE_DEVELOPER
	)
	claude, codex := planv1.Provider_PROVIDER_CLAUDE, planv1.Provider_PROVIDER_CODEX
	team := &planv1.ProjectSettings{Provider: &claude, Model: proto.String("opus"), MaxBudgetUsd: proto.Float64(3)}
	for _, c := range []struct {
		name      string
		repo, dev *planv1.ProjectSettings
		want      Settings
	}{
		{"neither file", nil, nil, Settings{Provider: claude, ProviderFrom: def, ModelFrom: def, BudgetFrom: def}},
		{"the repository's only", team, nil, Settings{
			Provider: claude, Model: "opus", MaxBudgetUSD: 3, ProviderFrom: repo, ModelFrom: repo, BudgetFrom: repo,
		}},
		{"the developer's win, setting by setting", team, &planv1.ProjectSettings{Model: proto.String("sonnet")}, Settings{
			Provider: claude, Model: "sonnet", MaxBudgetUSD: 3, ProviderFrom: repo, ModelFrom: dev, BudgetFrom: repo,
		}},
		{"a provider brings its model, not the other file's", team, &planv1.ProjectSettings{Provider: &codex}, Settings{
			Provider: codex, MaxBudgetUSD: 3, ProviderFrom: dev, ModelFrom: dev, BudgetFrom: repo,
		}},
		{"an explicit zero lifts the team's budget", team, &planv1.ProjectSettings{MaxBudgetUsd: proto.Float64(0)}, Settings{
			Provider: claude, Model: "opus", ProviderFrom: repo, ModelFrom: repo, BudgetFrom: dev,
		}},
		{"the developer's only", nil, &planv1.ProjectSettings{Provider: &codex, Model: proto.String("gpt-5.1-codex")}, Settings{
			Provider: codex, Model: "gpt-5.1-codex", ProviderFrom: dev, ModelFrom: dev, BudgetFrom: def,
		}},
	} {
		if got := ResolveSettings(c.repo, c.dev); got != c.want {
			t.Errorf("%s: %+v; want %+v", c.name, got, c.want)
		}
	}
}

func TestProjectShow(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	c := serve(t, WithHome(home))
	dir := t.TempDir()
	add, err := c.projects.Add(ctx, connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: dir, Name: "app"}))
	if err != nil {
		t.Fatal(err)
	}
	project := add.Msg.GetProject()
	show := func() *planv1.ProjectServiceShowResponse {
		t.Helper()
		res, err := c.projects.Show(ctx, connect.NewRequest(&planv1.ProjectServiceShowRequest{Project: "APP"}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg
	}
	rows := func(res *planv1.ProjectServiceShowResponse) string {
		var b []string
		for _, s := range res.GetSettings() {
			b = append(b, s.GetName()+"="+s.GetValue()+" "+strings.TrimPrefix(s.GetSource().String(), "SETTING_SOURCE_"))
		}
		return strings.Join(b, ", ")
	}

	res := show()
	repoFile := filepath.Join(project.GetDirectory(), ".agents", "settings.txtpb")
	devFile := filepath.Join(home, "projects", project.GetId(), "settings.txtpb")
	if res.GetRepositoryFile() != repoFile || res.GetDeveloperFile() != devFile || len(res.GetProblems()) > 0 {
		t.Errorf("files = %s, %s, %v; want %s, %s", res.GetRepositoryFile(), res.GetDeveloperFile(), res.GetProblems(), repoFile, devFile)
	}
	if got, want := rows(res), "provider=claude DEFAULT, model= DEFAULT, max_budget_usd= DEFAULT"; got != want {
		t.Errorf("no file: %s; want %s", got, want)
	}

	writeSettings(t, repoFile, "provider: PROVIDER_CLAUDE\nmodel: \"opus\"\nmax_budget_usd: 3\n")
	writeSettings(t, devFile, "model: \"sonnet\"\n")
	if got, want := rows(show()), "provider=claude REPOSITORY, model=sonnet DEVELOPER, max_budget_usd=3 REPOSITORY"; got != want {
		t.Errorf("both files: %s; want %s", got, want)
	}

	// A malformed file is a problem shown, not a failure: the other file still counts.
	writeSettings(t, devFile, "model: sonnet\n")
	res = show()
	if got, want := rows(res), "provider=claude REPOSITORY, model=opus REPOSITORY, max_budget_usd=3 REPOSITORY"; got != want {
		t.Errorf("a malformed developer file: %s; want %s", got, want)
	}
	if len(res.GetProblems()) != 1 || !strings.Contains(res.GetProblems()[0], devFile) {
		t.Errorf("problems = %v; want one naming %s", res.GetProblems(), devFile)
	}
	if _, err := LoadSettings(home, project); err == nil || !strings.Contains(err.Error(), devFile) {
		t.Errorf("LoadSettings = %v; want an error naming %s", err, devFile)
	}

	if _, err := c.projects.Show(ctx, connect.NewRequest(&planv1.ProjectServiceShowRequest{Project: "nope"})); code(err) != connect.CodeNotFound {
		t.Errorf("an unknown project: %v, want not_found", err)
	}
}

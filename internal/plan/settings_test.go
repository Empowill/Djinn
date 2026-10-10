package plan

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

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
		"max_budget_usd: 2.5\nbranch: \"djinn/{code}-{slug}-{uuid8}\"\n")
	s, err := ReadSettings(path)
	if err != nil || s.GetProvider() != planv1.Provider_PROVIDER_CODEX || s.GetModel() != "gpt-5.1-codex" || s.GetMaxBudgetUsd() != 2.5 ||
		s.GetBranch() != "djinn/{code}-{slug}-{uuid8}" {
		t.Errorf("valid file: %v, %v", s, err)
	}
	for _, branch := range []string{"{uuid8}", "feature/{slug}_{uuid8}", "T.{code}.{uuid8}", "{code}{uuid8}"} {
		writeSettings(t, path, "branch: \""+branch+"\"\n")
		if s, err := ReadSettings(path); err != nil || s.GetBranch() != branch {
			t.Errorf("branch %s: %v, %v", branch, s, err)
		}
	}
	for _, model := range []string{"claude-sonnet-4-5-20250929", "us.anthropic.claude-opus-4-1-20250805-v1:0", "opus[1m]"} {
		writeSettings(t, path, "model: \""+model+"\"\n")
		if s, err := ReadSettings(path); err != nil || s.GetModel() != model {
			t.Errorf("model %s: %v, %v", model, s, err)
		}
	}
	writeSettings(t, path, "setup: \"npm ci\"\nchecks { name: \"lint\" command: \"go tool task lint\" when: [CHECK_WHEN_COMMIT, CHECK_WHEN_PUSH] }\n"+
		"checks { name: \"test\" command: \"go tool task test\" when: CHECK_WHEN_PUSH }\n")
	if s, err := ReadSettings(path); err != nil || s.GetSetup() != "npm ci" ||
		ChecksText(s.GetChecks()) != "lint: go tool task lint (commit, push); test: go tool task test (push)" {
		t.Errorf("checks: %v, %v", s, err)
	}
	for _, bad := range []string{
		"checks { name: \"lint\" command: \"a\" when: CHECK_WHEN_COMMIT }\nchecks { name: \"LINT\" command: \"b\" when: CHECK_WHEN_PUSH }\n",
		"checks { name: \"lint\" command: \"a\" }\n",
		"checks { name: \"lint\" command: \"a\" when: CHECK_WHEN_UNSPECIFIED }\n",
		"checks { name: \"lint\" command: \"a\" when: [CHECK_WHEN_PUSH, CHECK_WHEN_PUSH] }\n",
		"checks { name: \"lint\" when: CHECK_WHEN_PUSH }\n",
		"checks { name: \"../lint\" command: \"a\" when: CHECK_WHEN_PUSH }\n",
		"provider: PROVIDER_CLAUDE\nprovider: \n",
		"unknown_field: 1\n",
		"provider: PROVIDER_WATCH\n",
		"provider: PROVIDER_UNSPECIFIED\n",
		"max_budget_usd: -1\n",
		"model: \"opus; rm -rf /\"\n",
		"branch: \"\"\n",
		"branch: \"{code}-{slug}\"\n",
		"branch: \"{code}-{ticket}-{uuid8}\"\n",
		"branch: \"feature//{uuid8}\"\n",
		"branch: \"../{uuid8}\"\n",
		"branch: \"-{uuid8}\"\n",
		"branch: \"{uuid8}/\"\n",
		"branch: \"a b-{uuid8}\"\n",
		"branch: \"{uuid8}@{1}\"\n",
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
	team := &planv1.ProjectSettings{
		Provider: &claude, Model: proto.String("opus"), MaxBudgetUsd: proto.Float64(3), Branch: proto.String("djinn/{code}-{uuid8}"),
	}
	for _, c := range []struct {
		name      string
		repo, dev *planv1.ProjectSettings
		want      Settings
	}{
		{"neither file", nil, nil, Settings{
			Provider: claude, Branch: DefaultBranch, ProviderFrom: def, ModelFrom: def, BudgetFrom: def, BranchFrom: def, GeneratedFrom: def, GenerateFrom: def, SetupFrom: def, ChecksFrom: def, InstallFrom: def, AttemptsFrom: def, CorrectionAttempts: 2,
		}},
		{"the repository's only", team, nil, Settings{
			Provider: claude, Model: "opus", MaxBudgetUSD: 3, Branch: "djinn/{code}-{uuid8}",
			ProviderFrom: repo, ModelFrom: repo, BudgetFrom: repo, BranchFrom: repo, GeneratedFrom: def, GenerateFrom: def, SetupFrom: def, ChecksFrom: def, InstallFrom: def, AttemptsFrom: def, CorrectionAttempts: 2,
		}},
		{"the developer's win, setting by setting", team, &planv1.ProjectSettings{Model: proto.String("sonnet")}, Settings{
			Provider: claude, Model: "sonnet", MaxBudgetUSD: 3, Branch: "djinn/{code}-{uuid8}",
			ProviderFrom: repo, ModelFrom: dev, BudgetFrom: repo, BranchFrom: repo, GeneratedFrom: def, GenerateFrom: def, SetupFrom: def, ChecksFrom: def, InstallFrom: def, AttemptsFrom: def, CorrectionAttempts: 2,
		}},
		{"a provider brings its model, not the other file's", team, &planv1.ProjectSettings{Provider: &codex}, Settings{
			Provider: codex, MaxBudgetUSD: 3, Branch: "djinn/{code}-{uuid8}",
			ProviderFrom: dev, ModelFrom: dev, BudgetFrom: repo, BranchFrom: repo, GeneratedFrom: def, GenerateFrom: def, SetupFrom: def, ChecksFrom: def, InstallFrom: def, AttemptsFrom: def, CorrectionAttempts: 2,
		}},
		{"an explicit zero lifts the team's budget", team, &planv1.ProjectSettings{MaxBudgetUsd: proto.Float64(0)}, Settings{
			Provider: claude, Model: "opus", Branch: "djinn/{code}-{uuid8}",
			ProviderFrom: repo, ModelFrom: repo, BudgetFrom: dev, BranchFrom: repo, GeneratedFrom: def, GenerateFrom: def, SetupFrom: def, ChecksFrom: def, InstallFrom: def, AttemptsFrom: def, CorrectionAttempts: 2,
		}},
		{"the developer's branch wins", team, &planv1.ProjectSettings{Branch: proto.String("me/{slug}-{uuid8}")}, Settings{
			Provider: claude, Model: "opus", MaxBudgetUSD: 3, Branch: "me/{slug}-{uuid8}",
			ProviderFrom: repo, ModelFrom: repo, BudgetFrom: repo, BranchFrom: dev, GeneratedFrom: def, GenerateFrom: def, SetupFrom: def, ChecksFrom: def, InstallFrom: def, AttemptsFrom: def, CorrectionAttempts: 2,
		}},
		{"the developer's only", nil, &planv1.ProjectSettings{Provider: &codex, Model: proto.String("gpt-5.1-codex")}, Settings{
			Provider: codex, Model: "gpt-5.1-codex", Branch: DefaultBranch,
			ProviderFrom: dev, ModelFrom: dev, BudgetFrom: def, BranchFrom: def, GeneratedFrom: def, GenerateFrom: def, SetupFrom: def, ChecksFrom: def, InstallFrom: def, AttemptsFrom: def, CorrectionAttempts: 2,
		}},
		{"the integration's, the developer's winning", &planv1.ProjectSettings{
			Generated: []string{"gen/**", "docs/schema.json"}, Generate: proto.String("go tool task gen"), Test: proto.String("go tool task test"),
			Install: proto.String("go tool task install"),
		}, &planv1.ProjectSettings{Test: proto.String("go tool task test-go")}, Settings{
			Provider: claude, Branch: DefaultBranch, Generated: []string{"gen/**", "docs/schema.json"}, Generate: "go tool task gen",
			Checks: []*planv1.ProjectCheck{atCommit("test", "go tool task test-go")}, ProviderFrom: def, ModelFrom: def,
			BudgetFrom: def, BranchFrom: def, GeneratedFrom: repo, GenerateFrom: repo, SetupFrom: def, ChecksFrom: dev,
			InstallFrom: repo, Install: "go tool task install", CorrectionAttempts: 2, AttemptsFrom: def,
		}},
		{"the checks of one file, its test among them", &planv1.ProjectSettings{
			Setup: proto.String("npm ci"), Test: proto.String("go tool task test"),
			Checks: []*planv1.ProjectCheck{atCommit("lint", "go tool task lint")},
		}, nil, Settings{
			Provider: claude, Branch: DefaultBranch, Setup: "npm ci",
			Checks:       []*planv1.ProjectCheck{atCommit("lint", "go tool task lint"), atCommit("test", "go tool task test")},
			ProviderFrom: def, ModelFrom: def, BudgetFrom: def, BranchFrom: def, GeneratedFrom: def, GenerateFrom: def,
			SetupFrom: repo, ChecksFrom: repo, InstallFrom: def, CorrectionAttempts: 2, AttemptsFrom: def,
		}},
		{"a file that sets checks sets them all", &planv1.ProjectSettings{
			Checks: []*planv1.ProjectCheck{atCommit("lint", "go tool task lint")}, Test: proto.String("go tool task test"),
		}, &planv1.ProjectSettings{Checks: []*planv1.ProjectCheck{atCommit("test", "make check")}, Test: proto.String("make test")}, Settings{
			Provider: claude, Branch: DefaultBranch, Checks: []*planv1.ProjectCheck{atCommit("test", "make check")},
			ProviderFrom: def, ModelFrom: def, BudgetFrom: def, BranchFrom: def, GeneratedFrom: def, GenerateFrom: def,
			SetupFrom: def, ChecksFrom: dev, InstallFrom: def, CorrectionAttempts: 2, AttemptsFrom: def,
		}},
		{"no correction worker", nil, &planv1.ProjectSettings{CorrectionAttempts: proto.Int32(0)}, Settings{
			Provider: claude, Branch: DefaultBranch, ProviderFrom: def, ModelFrom: def, BudgetFrom: def, BranchFrom: def,
			GeneratedFrom: def, GenerateFrom: def, SetupFrom: def, ChecksFrom: def, InstallFrom: def, AttemptsFrom: dev,
		}},
	} {
		got := ResolveSettings(c.repo, c.dev)
		// The question workers' settings are TestQuestionSettings'.
		got.AnswerWorkers, got.EnlightenWorkers, got.QuestionWorkers = false, false, false
		got.QuestionProvider, got.QuestionModel, got.QuestionBudgetUSD = 0, "", 0
		got.AnswerWorkersFrom, got.EnlightenWorkersFrom, got.QuestionWorkersFrom = 0, 0, 0
		got.QuestionProviderFrom, got.QuestionModelFrom, got.QuestionBudgetFrom = 0, 0, 0
		// Main's are TestMainSettings'.
		got.MainBranch, got.MergeMain, got.MergeMainEvery, got.InstallReleases = "", 0, 0, false
		got.MainBranchFrom, got.MergeMainFrom, got.MergeMainEveryFrom, got.InstallReleasesFrom = 0, 0, 0, 0
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %+v; want %+v", c.name, got, c.want)
		}
	}
}

// TestQuestionSettings: enlighten workers are on by default and answer workers off, on a cheaper model for claude
// and the provider's own default for another, with a budget; either file changes them, and a file that sets the
// provider resets the model.
func TestQuestionSettings(t *testing.T) {
	codex := planv1.Provider_PROVIDER_CODEX
	antigravity := planv1.Provider_PROVIDER_ANTIGRAVITY
	fake := planv1.Provider_PROVIDER_FAKE
	type q struct {
		answers   bool
		enlighten bool
		questions bool
		provider  planv1.Provider
		model     string
		budget    float64
	}
	for _, c := range []struct {
		name      string
		repo, dev *planv1.ProjectSettings
		want      q
	}{
		{"neither file", nil, nil, q{false, true, false, planv1.Provider_PROVIDER_UNSPECIFIED, DefaultQuestionModel, DefaultQuestionBudgetUSD}},
		{"another provider", &planv1.ProjectSettings{Provider: &codex}, nil, q{false, true, false, planv1.Provider_PROVIDER_UNSPECIFIED, "", DefaultQuestionBudgetUSD}},
		{"antigravity falls back to sonnet", &planv1.ProjectSettings{Provider: &antigravity}, nil, q{false, true, false, planv1.Provider_PROVIDER_UNSPECIFIED, DefaultQuestionModel, DefaultQuestionBudgetUSD}},
		{"deprecated question_workers sets both", &planv1.ProjectSettings{
			QuestionWorkers: proto.Bool(true), QuestionProvider: &fake, QuestionModel: proto.String("haiku"), QuestionBudgetUsd: proto.Float64(0.5),
		}, nil, q{true, true, true, fake, "haiku", 0.5}},
		{"specific answer_workers and enlighten_workers", &planv1.ProjectSettings{
			AnswerWorkers: proto.Bool(true), EnlightenWorkers: proto.Bool(false),
		}, nil, q{true, false, false, planv1.Provider_PROVIDER_UNSPECIFIED, DefaultQuestionModel, DefaultQuestionBudgetUSD}},
		{"the developer's win", &planv1.ProjectSettings{QuestionWorkers: proto.Bool(false), QuestionProvider: &fake, QuestionModel: proto.String("haiku")},
			&planv1.ProjectSettings{AnswerWorkers: proto.Bool(true), QuestionProvider: &codex, QuestionModel: proto.String("")}, q{true, false, false, codex, "", DefaultQuestionBudgetUSD}},
		{"the developer's deprecated question_workers win", &planv1.ProjectSettings{AnswerWorkers: proto.Bool(true), EnlightenWorkers: proto.Bool(false)},
			&planv1.ProjectSettings{QuestionWorkers: proto.Bool(true)}, q{true, true, true, planv1.Provider_PROVIDER_UNSPECIFIED, DefaultQuestionModel, DefaultQuestionBudgetUSD}},
		{"a provider resets the model", &planv1.ProjectSettings{QuestionModel: proto.String("haiku")},
			&planv1.ProjectSettings{Provider: &codex}, q{false, true, false, planv1.Provider_PROVIDER_UNSPECIFIED, "", DefaultQuestionBudgetUSD}},
		{"a question_provider resets the model", &planv1.ProjectSettings{QuestionModel: proto.String("haiku")},
			&planv1.ProjectSettings{QuestionProvider: &codex}, q{false, true, false, codex, "", DefaultQuestionBudgetUSD}},
	} {
		s := ResolveSettings(c.repo, c.dev)
		if got := (q{s.AnswerWorkers, s.EnlightenWorkers, s.QuestionWorkers, s.QuestionProvider, s.QuestionModel, s.QuestionBudgetUSD}); got != c.want {
			t.Errorf("%s: %+v; want %+v", c.name, got, c.want)
		}
	}
}

// mainRows are the rows of the settings that keep a wish's integration branch up with main: TestMainSettings'.
var mainRows = []string{"main_branch", "merge_main", "merge_main_minutes", "install_releases"}

// TestMainSettings: by default Djinn merges the remote's default branch at each release, fetched at most hourly, and
// a release of Djinn installs by itself on main; either file changes them, the developer's winning, and the rows say
// where each comes from.
func TestMainSettings(t *testing.T) {
	const (
		def  = planv1.SettingSource_SETTING_SOURCE_DEFAULT
		repo = planv1.SettingSource_SETTING_SOURCE_REPOSITORY
		dev  = planv1.SettingSource_SETTING_SOURCE_DEVELOPER
	)
	commit, off := planv1.MergeMain_MERGE_MAIN_COMMIT, planv1.MergeMain_MERGE_MAIN_OFF
	type m struct {
		branch  string
		when    planv1.MergeMain
		every   time.Duration
		install bool
		from    [4]planv1.SettingSource
	}
	for _, c := range []struct {
		name      string
		repo, dev *planv1.ProjectSettings
		want      m
		rows      string
	}{
		{"neither file", nil, nil, m{"", planv1.MergeMain_MERGE_MAIN_RELEASE, time.Hour, true, [4]planv1.SettingSource{def, def, def, def}},
			"main_branch=, merge_main=release, merge_main_minutes=60, install_releases=true"},
		{"the team's", &planv1.ProjectSettings{
			MainBranch: proto.String("trunk"), MergeMain: &commit, MergeMainMinutes: proto.Int32(15), InstallReleases: proto.Bool(false),
		}, nil, m{"trunk", commit, 15 * time.Minute, false, [4]planv1.SettingSource{repo, repo, repo, repo}},
			"main_branch=trunk, merge_main=commit, merge_main_minutes=15, install_releases=false"},
		{"the developer's win", &planv1.ProjectSettings{MainBranch: proto.String("trunk"), MergeMain: &commit},
			&planv1.ProjectSettings{MergeMain: &off, InstallReleases: proto.Bool(false)},
			m{"trunk", off, time.Hour, false, [4]planv1.SettingSource{repo, dev, def, dev}},
			"main_branch=trunk, merge_main=off, merge_main_minutes=60, install_releases=false"},
	} {
		s := ResolveSettings(c.repo, c.dev)
		got := m{s.MainBranch, s.MergeMain, s.MergeMainEvery, s.InstallReleases,
			[4]planv1.SettingSource{s.MainBranchFrom, s.MergeMainFrom, s.MergeMainEveryFrom, s.InstallReleasesFrom}}
		if got != c.want {
			t.Errorf("%s: %+v; want %+v", c.name, got, c.want)
		}
		var rows []string
		for _, r := range s.Rows() {
			if slices.Contains(mainRows, r.GetName()) {
				rows = append(rows, r.GetName()+"="+r.GetValue())
			}
		}
		if got := strings.Join(rows, ", "); got != c.rows {
			t.Errorf("%s: rows %s; want %s", c.name, got, c.rows)
		}
	}
	// A main branch Git would refuse, a cadence under a minute, or no merge_main are refused when the file is read.
	for _, text := range []string{"main_branch: \"ma in\"\n", "merge_main_minutes: 0\n", "merge_main: MERGE_MAIN_UNSPECIFIED\n"} {
		path := filepath.Join(t.TempDir(), "settings.txtpb")
		writeSettings(t, path, text)
		if _, err := ReadSettings(path); err == nil {
			t.Errorf("%q: read without error", text)
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
	checks := func(res *planv1.ProjectServiceShowResponse) string {
		return "setup " + res.GetSetup() + "; " + ChecksText(res.GetChecks())
	}
	rows := func(res *planv1.ProjectServiceShowResponse) string {
		var b []string
		for _, s := range res.GetSettings() {
			if strings.HasPrefix(s.GetName(), "question_") || s.GetName() == "answer_workers" || s.GetName() == "enlighten_workers" || slices.Contains(mainRows, s.GetName()) {
				continue // The question workers' and main's are checked apart.
			}
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
	if got, want := rows(res), "provider=claude DEFAULT, model= DEFAULT, max_budget_usd= DEFAULT, branch={code}-{slug}-{uuid8} DEFAULT, "+
		"generated= DEFAULT, generate= DEFAULT, setup= DEFAULT, checks= DEFAULT, correction_attempts=2 DEFAULT, install= DEFAULT"; got != want {
		t.Errorf("no file: %s; want %s", got, want)
	}
	var question []string
	for _, s := range res.GetSettings() {
		if strings.HasPrefix(s.GetName(), "question_") || s.GetName() == "answer_workers" || s.GetName() == "enlighten_workers" {
			question = append(question, s.GetName()+"="+s.GetValue())
		}
	}
	if got, want := strings.Join(question, ", "), "answer_workers=false, enlighten_workers=true, question_provider=, question_model=sonnet, question_budget_usd=2"; got != want {
		t.Errorf("no file, the question workers: %s; want %s", got, want)
	}

	writeSettings(t, repoFile, "provider: PROVIDER_CLAUDE\nmodel: \"opus\"\nmax_budget_usd: 3\nbranch: \"djinn/{code}-{uuid8}\"\n"+
		"generated: \"gen/**\"\ngenerated: \"docs/schema.json\"\ngenerate: \"go tool task gen\"\ntest: \"go tool task test\"\ncorrection_attempts: 3\ninstall: \"go tool task install\"\n")
	writeSettings(t, devFile, "model: \"sonnet\"\nbranch: \"me/{slug}-{uuid8}\"\ntest: \"go tool task test-go\"\n")
	if got, want := rows(show()), "provider=claude REPOSITORY, model=sonnet DEVELOPER, max_budget_usd=3 REPOSITORY, "+
		"branch=me/{slug}-{uuid8} DEVELOPER, generated=gen/**,docs/schema.json REPOSITORY, generate=go tool task gen REPOSITORY, "+
		"setup= DEFAULT, checks=test: go tool task test-go (commit) DEVELOPER, correction_attempts=3 REPOSITORY, "+
		"install=go tool task install REPOSITORY"; got != want {
		t.Errorf("both files: %s; want %s", got, want)
	}

	// A malformed file is a problem shown, not a failure: the other file still counts.
	writeSettings(t, devFile, "model: sonnet\n")
	res = show()
	if got, want := rows(res), "provider=claude REPOSITORY, model=opus REPOSITORY, max_budget_usd=3 REPOSITORY, "+
		"branch=djinn/{code}-{uuid8} REPOSITORY, generated=gen/**,docs/schema.json REPOSITORY, "+
		"generate=go tool task gen REPOSITORY, setup= DEFAULT, checks=test: go tool task test (commit) REPOSITORY, correction_attempts=3 REPOSITORY, "+
		"install=go tool task install REPOSITORY"; got != want {
		t.Errorf("a malformed developer file: %s; want %s", got, want)
	}
	if len(res.GetProblems()) != 1 || !strings.Contains(res.GetProblems()[0], devFile) {
		t.Errorf("problems = %v; want one naming %s", res.GetProblems(), devFile)
	}
	if _, err := LoadSettings(home, project); err == nil || !strings.Contains(err.Error(), devFile) {
		t.Errorf("LoadSettings = %v; want an error naming %s", err, devFile)
	}

	// The setup and the checks, as Djinn runs them; their last runs are the project's.
	writeSettings(t, devFile, "setup: \"npm ci\"\nchecks { name: \"lint\" command: \"go tool task lint\" when: CHECK_WHEN_COMMIT }\n"+
		"checks { name: \"test\" command: \"go tool task test\" when: CHECK_WHEN_PUSH }\n")
	res = show()
	if got, want := checks(res), "setup npm ci; lint: go tool task lint (commit); test: go tool task test (push)"; got != want {
		t.Errorf("the checks: %s; want %s", got, want)
	}

	if _, err := c.projects.Show(ctx, connect.NewRequest(&planv1.ProjectServiceShowRequest{Project: "nope"})); code(err) != connect.CodeNotFound {
		t.Errorf("an unknown project: %v, want not_found", err)
	}
}

// atCommit is a check run before each commit.
func atCommit(name, command string) *planv1.ProjectCheck {
	return &planv1.ProjectCheck{Name: name, Command: command, When: []planv1.CheckWhen{planv1.CheckWhen_CHECK_WHEN_COMMIT}}
}

// TestChecksBrief: the lead's brief and each worker's first prompt say which checks Djinn runs and when, how to run
// them through their gates, and what a fresh worktree needs first; nothing for a project without checks.
func TestChecksBrief(t *testing.T) {
	if got := (Settings{}).ChecksBrief(); got != "" {
		t.Errorf("no check: %q", got)
	}
	s := Settings{Setup: "npm ci", Checks: []*planv1.ProjectCheck{
		atCommit("lint", "go tool task lint"),
		{Name: "test", Command: "go tool task test", When: []planv1.CheckWhen{planv1.CheckWhen_CHECK_WHEN_PUSH}},
	}}
	want := "Djinn checks this project's work before it commits a task's work, with `djinn gate run lint -- go tool task lint`; " +
		"before it pushes, with `djinn gate run test -- go tool task test`. A worker runs the commit checks before it ends, " +
		"and fixes what they find. A fresh worktree needs `npm ci` first."
	if got := s.ChecksBrief(); got != want {
		t.Errorf("brief:\n%s\nwant:\n%s", got, want)
	}
	push := Settings{Checks: []*planv1.ProjectCheck{{Name: "lint", Command: "make lint", When: []planv1.CheckWhen{planv1.CheckWhen_CHECK_WHEN_PUSH}}}}
	if got, want := push.ChecksBrief(), "Djinn checks this project's work before it pushes, with `djinn gate run lint -- make lint`."; got != want {
		t.Errorf("push only: %q; want %q", got, want)
	}
}

// TestDjinnsOwnSettings: Djinn's repository names its setup and its checks, the lint before each commit and the tests
// before each push, in a file Djinn reads.
func TestDjinnsOwnSettings(t *testing.T) {
	s, err := ReadSettings(filepath.Join("..", "..", SettingsFile))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ChecksText(s.GetChecks()), "lint: go tool task lint (commit); test: go tool task test (push)"; s.GetSetup() != "npm ci" || got != want {
		t.Errorf("setup %q, checks %q; want npm ci, %q", s.GetSetup(), got, want)
	}
}

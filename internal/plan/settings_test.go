package plan

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
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

// TestReadSettings: a settings file reads as its settings, none when there is no file; what Djinn would refuse to run
// is an error naming the file, and a secret is refused without being repeated.
func TestReadSettings(t *testing.T) {
	t.Parallel()
	t.Run("valid", func(t *testing.T) {
		t.Parallel()
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
		for _, model := range []string{
			"claude-sonnet-4-5-20250929", "us.anthropic.claude-opus-4-1-20250805-v1:0", "opus[1m]",
			"Gemini 4 Argon (High)", "gemini-4-argon-high",
		} {
			writeSettings(t, path, "model: \""+model+"\"\nquestion_model: \""+model+"\"\n")
			if s, err := ReadSettings(path); err != nil || s.GetModel() != model || s.GetQuestionModel() != model {
				t.Errorf("model %s: %v, %v", model, s, err)
			}
		}
		writeSettings(t, path, "setup: \"npm ci\"\nchecks { name: \"lint\" command: \"go tool task lint\" when: [CHECK_WHEN_COMMIT, CHECK_WHEN_PUSH] }\n"+
			"checks { name: \"test\" command: \"go tool task test\" when: CHECK_WHEN_PUSH }\n")
		if s, err := ReadSettings(path); err != nil || s.GetSetup() != "npm ci" ||
			ChecksText(s.GetChecks()) != "lint: go tool task lint (commit, push); test: go tool task test (push)" {
			t.Errorf("checks: %v, %v", s, err)
		}
		for _, tc := range []struct {
			text string
			read func(*planv1.ProjectSettings) bool
		}{
			{"babysit: false\n", func(s *planv1.ProjectSettings) bool { return s.Babysit != nil && !s.GetBabysit() }},
			{"install_builds: false\n", func(s *planv1.ProjectSettings) bool { return s.InstallBuilds != nil && !s.GetInstallBuilds() }},
			// Install questions read in lowercase too.
			{"install_questions: when_needed\n", func(s *planv1.ProjectSettings) bool {
				return s.GetInstallQuestions() == planv1.InstallQuestions_INSTALL_QUESTIONS_WHEN_NEEDED
			}},
			{"install_questions: never\n", func(s *planv1.ProjectSettings) bool {
				return s.GetInstallQuestions() == planv1.InstallQuestions_INSTALL_QUESTIONS_NEVER
			}},
			{"install_questions: always\n", func(s *planv1.ProjectSettings) bool {
				return s.GetInstallQuestions() == planv1.InstallQuestions_INSTALL_QUESTIONS_ALWAYS
			}},
			{"install_questions: INSTALL_QUESTIONS_WHEN_NEEDED\n", func(s *planv1.ProjectSettings) bool {
				return s.GetInstallQuestions() == planv1.InstallQuestions_INSTALL_QUESTIONS_WHEN_NEEDED
			}},
			// A comment mentioning a removed key is not refused.
			{"# answer_workers was removed\nenlighten_workers: true\n", func(s *planv1.ProjectSettings) bool { return s.GetEnlightenWorkers() }},
		} {
			writeSettings(t, path, tc.text)
			if s, err := ReadSettings(path); err != nil || s == nil || !tc.read(s) {
				t.Errorf("%q read as %v (%v)", tc.text, s, err)
			}
		}
	})

	t.Run("refused", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "settings.txtpb")
		for _, bad := range []string{
			"checks { name: \"lint\" command: \"a\" when: CHECK_WHEN_COMMIT }\nchecks { name: \"LINT\" command: \"b\" when: CHECK_WHEN_PUSH }\n",
			"checks { name: \"lint\" command: \"a\" }\n",
			"checks { name: \"lint\" command: \"a\" when: CHECK_WHEN_UNSPECIFIED }\n",
			"checks { name: \"lint\" command: \"a\" when: [CHECK_WHEN_PUSH, CHECK_WHEN_PUSH] }\n",
			"checks { name: \"lint\" when: CHECK_WHEN_PUSH }\n",
			"checks { name: \"../lint\" command: \"a\" when: CHECK_WHEN_PUSH }\n",
			"provider: PROVIDER_CLAUDE\nprovider: \n",
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
			// A main branch Git would refuse, a cadence under a minute, or no merge_main.
			"main_branch: \"ma in\"\n",
			"merge_main_minutes: 0\n",
			"merge_main: MERGE_MAIN_UNSPECIFIED\n",
		} {
			writeSettings(t, path, bad)
			if s, err := ReadSettings(path); err == nil || !strings.Contains(err.Error(), path) {
				t.Errorf("%q: %v, %v; want an error naming the file", bad, s, err)
			}
		}
	})

	t.Run("secrets", func(t *testing.T) {
		t.Parallel()
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
	})

	t.Run("removed keys", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "settings.txtpb")
		for _, tc := range []struct {
			content  string
			wantKey  string
			wantLine int
		}{
			{"answer_workers: true\n", "answer_workers", 1},
			{"answer_workers: false\n", "answer_workers", 1},
			{"question_workers: true\n", "question_workers", 1},
			{"question_workers: false\n", "question_workers", 1},
			{"# a comment\nanswer_workers: true\n", "answer_workers", 2},
			{"provider: PROVIDER_CLAUDE\nquestion_workers: true\n", "question_workers", 2},
			{"  answer_workers: true\n", "answer_workers", 1},
		} {
			writeSettings(t, path, tc.content)
			s, err := ReadSettings(path)
			if err == nil {
				t.Errorf("%q: got %v, want refused", tc.content, s)
				continue
			}
			wantMsg := fmt.Sprintf("line %d names %s: this key was removed; delete the line", tc.wantLine, tc.wantKey)
			if !strings.Contains(err.Error(), wantMsg) {
				t.Errorf("%q: got error %v, want substring %q", tc.content, err, wantMsg)
			}
		}
	})

	// Djinn's repository names its setup and its checks, the lint before each commit and the tests before each push.
	t.Run("djinn's own", func(t *testing.T) {
		t.Parallel()
		s, err := ReadSettings(filepath.Join("..", "..", SettingsFile))
		if err != nil {
			t.Fatal(err)
		}
		if got, want := ChecksText(s.GetChecks()), "lint: go tool task lint (commit); test: go tool task test (push)"; s.GetSetup() != "npm ci" || got != want {
			t.Errorf("setup %q, checks %q; want npm ci, %q", s.GetSetup(), got, want)
		}
	})
}

// TestReadSettingsWithWarnings: an unknown field is read past, and named in a warning.
func TestReadSettingsWithWarnings(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "settings.txtpb")
	writeSettings(t, path, "provider: PROVIDER_CODEX\nunknown_setting: true\n")
	s, warnings, err := ReadSettingsWithWarnings(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.GetProvider() != planv1.Provider_PROVIDER_CODEX {
		t.Errorf("provider: got %v, want PROVIDER_CODEX", s.GetProvider())
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "unknown_setting") || !strings.Contains(warnings[0], "a newer Djinn knows it") {
		t.Errorf("warnings: got %v, want warning for unknown_setting", warnings)
	}
}

// TestLoadSettings: a project's settings carry the warnings of its file, and what the developer saved for it.
func TestLoadSettings(t *testing.T) {
	t.Parallel()
	t.Run("unknown field", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		repo := t.TempDir()
		writeSettings(t, filepath.Join(repo, ".agents", "settings.txtpb"), "provider: PROVIDER_CODEX\nunknown_feature: true\n")
		p := &planv1.Project{Id: "p1", Directory: repo, Git: true}
		s, err := LoadSettings(home, p)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if s.Provider != planv1.Provider_PROVIDER_CODEX {
			t.Errorf("provider: got %v, want PROVIDER_CODEX", s.Provider)
		}
		if len(s.Warnings) != 1 || !strings.Contains(s.Warnings[0], "unknown_feature") || !strings.Contains(s.Warnings[0], "a newer Djinn knows it") {
			t.Errorf("warnings: got %v, want warning for unknown_feature", s.Warnings)
		}
	})

	// SaveDeveloperPush, SaveDeveloperPushStrategy and SaveDeveloperInstallQuestions write the developer's choice,
	// which LoadSettings reads back as the developer's; each needs a home and a project.
	const projectID = "01946059-e68b-7cb8-8c1d-6b586e3f4ea2"
	for _, tc := range []struct {
		name string
		save func(home, projectID string) error
		read func(Settings) (string, planv1.SettingSource)
		want string
	}{
		{
			"push",
			func(home, id string) error {
				return SaveDeveloperPush(home, id, planv1.ProjectPush_PROJECT_PUSH_ON_DEMAND)
			},
			func(s Settings) (string, planv1.SettingSource) { return s.Push.String(), s.PushFrom },
			planv1.ProjectPush_PROJECT_PUSH_ON_DEMAND.String(),
		},
		{
			"push strategy",
			func(home, id string) error {
				return SaveDeveloperPushStrategy(home, id, planv1.PushStrategy_PUSH_STRATEGY_AZIMA)
			},
			func(s Settings) (string, planv1.SettingSource) { return s.PushStrategy.String(), s.PushStrategyFrom },
			planv1.PushStrategy_PUSH_STRATEGY_AZIMA.String(),
		},
		{
			"install questions",
			func(home, id string) error {
				return SaveDeveloperInstallQuestions(home, id, planv1.InstallQuestions_INSTALL_QUESTIONS_WHEN_NEEDED)
			},
			func(s Settings) (string, planv1.SettingSource) {
				return s.InstallQuestions.String(), s.InstallQuestionsFrom
			},
			planv1.InstallQuestions_INSTALL_QUESTIONS_WHEN_NEEDED.String(),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			if err := tc.save(home, projectID); err != nil {
				t.Fatal(err)
			}
			s, err := LoadSettings(home, &planv1.Project{Id: projectID})
			if err != nil {
				t.Fatal(err)
			}
			if got, from := tc.read(s); got != tc.want || from != planv1.SettingSource_SETTING_SOURCE_DEVELOPER {
				t.Errorf("saved: %s from %v; want %s from the developer", got, from, tc.want)
			}
			if err := tc.save("", projectID); err == nil {
				t.Error("saved without home: want error")
			}
			if err := tc.save(home, ""); err == nil {
				t.Error("saved without project ID: want error")
			}
		})
	}
}

// TestResolveSettings: each setting comes from the developer's file, else the repository's, else Djinn's default, and
// says where it comes from; its row in Rows shows both.
func TestResolveSettings(t *testing.T) {
	t.Parallel()
	const (
		def  = planv1.SettingSource_SETTING_SOURCE_DEFAULT
		repo = planv1.SettingSource_SETTING_SOURCE_REPOSITORY
		dev  = planv1.SettingSource_SETTING_SOURCE_DEVELOPER
	)
	claude, codex := planv1.Provider_PROVIDER_CLAUDE, planv1.Provider_PROVIDER_CODEX

	t.Run("whole", func(t *testing.T) {
		t.Parallel()
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
			// The settings below have their own subtests.
			got.EnlightenWorkers = false
			got.QuestionProvider, got.QuestionModel, got.QuestionBudgetUSD = 0, "", 0
			got.EnlightenWorkersFrom = 0
			got.QuestionProviderFrom, got.QuestionModelFrom, got.QuestionBudgetFrom = 0, 0, 0
			got.MainBranch, got.MergeMain, got.MergeMainEvery, got.InstallReleases = "", 0, 0, false
			got.MainBranchFrom, got.MergeMainFrom, got.MergeMainEveryFrom, got.InstallReleasesFrom = 0, 0, 0, 0
			got.Push, got.PushFrom = 0, 0
			got.PushStrategy, got.PushStrategyFrom = 0, 0
			got.TestOne, got.TestOneFrom = "", 0
			got.InstallQuestions, got.InstallQuestionsFrom = 0, 0
			got.Babysit, got.BabysitFrom = false, 0
			got.WatchCommands, got.WatchCommandsFrom = nil, 0
			got.ServeCommands, got.ServeCommandsFrom = nil, 0
			got.InstallBuilds, got.InstallBuildsFrom = false, 0
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("%s: %+v; want %+v", c.name, got, c.want)
			}
		}
	})

	// One setting at a time: its value (the enum and the word for it), its source, and its row. Babysitting what tasks
	// open, and installing a build of the running Djinn's line, are on by default; Djinn pushes at the standard cadence
	// and integrates per wish; it asks to install builds; a project may name its targeted-test command (test_one).
	type step struct {
		name      string
		repo, dev *planv1.ProjectSettings
		want      string // the value, as its row says it
		from      planv1.SettingSource
	}
	onDemand, standard := planv1.ProjectPush_PROJECT_PUSH_ON_DEMAND, planv1.ProjectPush_PROJECT_PUSH_STANDARD
	wishStrat, azimaStrat := planv1.PushStrategy_PUSH_STRATEGY_WISH, planv1.PushStrategy_PUSH_STRATEGY_AZIMA
	always := planv1.InstallQuestions_INSTALL_QUESTIONS_ALWAYS
	whenNeeded := planv1.InstallQuestions_INSTALL_QUESTIONS_WHEN_NEEDED
	never := planv1.InstallQuestions_INSTALL_QUESTIONS_NEVER
	for _, k := range []struct {
		row   string
		value func(Settings) (string, planv1.SettingSource) // the field, as its row says it, and its source
		enum  func(Settings) string                         // the enum behind the word, if any
		steps []step
	}{
		{
			row:   "babysit",
			value: func(s Settings) (string, planv1.SettingSource) { return strconv.FormatBool(s.Babysit), s.BabysitFrom },
			steps: []step{
				{"neither file", nil, nil, "true", def},
				{"the repository turns it off", &planv1.ProjectSettings{Babysit: proto.Bool(false)}, nil, "false", repo},
				{"the developer turns it on again", &planv1.ProjectSettings{Babysit: proto.Bool(false)},
					&planv1.ProjectSettings{Babysit: proto.Bool(true)}, "true", dev},
			},
		},
		{
			row: "install_builds",
			value: func(s Settings) (string, planv1.SettingSource) {
				return strconv.FormatBool(s.InstallBuilds), s.InstallBuildsFrom
			},
			steps: []step{
				{"neither file", nil, nil, "true", def},
				{"the repository turns it off", &planv1.ProjectSettings{InstallBuilds: proto.Bool(false)}, nil, "false", repo},
				{"the developer turns it on again", &planv1.ProjectSettings{InstallBuilds: proto.Bool(false)},
					&planv1.ProjectSettings{InstallBuilds: proto.Bool(true)}, "true", dev},
			},
		},
		{
			row:   "test_one",
			value: func(s Settings) (string, planv1.SettingSource) { return s.TestOne, s.TestOneFrom },
			steps: []step{
				{"neither file", nil, nil, "", def},
				{"the repository's", &planv1.ProjectSettings{TestOne: proto.String("go tool task test-pkg -- {pkg}")}, nil,
					"go tool task test-pkg -- {pkg}", repo},
				{"the developer's win", &planv1.ProjectSettings{TestOne: proto.String("go tool task test-pkg -- {pkg}")},
					&planv1.ProjectSettings{TestOne: proto.String("npm test -- {spec}")}, "npm test -- {spec}", dev},
			},
		},
		{
			row:   "push",
			value: func(s Settings) (string, planv1.SettingSource) { return ProjectPushWord(s.Push), s.PushFrom },
			enum:  func(s Settings) string { return strings.ToLower(strings.TrimPrefix(s.Push.String(), "PROJECT_PUSH_")) },
			steps: []step{
				{"neither file", nil, nil, "standard", def},
				{"the team's on demand", &planv1.ProjectSettings{Push: &onDemand}, nil, "on_demand", repo},
				{"the developer overrides to standard", &planv1.ProjectSettings{Push: &onDemand}, &planv1.ProjectSettings{Push: &standard}, "standard", dev},
			},
		},
		{
			row: "push_strategy",
			value: func(s Settings) (string, planv1.SettingSource) {
				return PushStrategyWord(s.PushStrategy), s.PushStrategyFrom
			},
			enum: func(s Settings) string {
				return strings.ToLower(strings.TrimPrefix(s.PushStrategy.String(), "PUSH_STRATEGY_"))
			},
			steps: []step{
				{"neither file", nil, nil, "wish", def},
				{"the team's azima", &planv1.ProjectSettings{PushStrategy: &azimaStrat}, nil, "azima", repo},
				{"the developer overrides to wish", &planv1.ProjectSettings{PushStrategy: &azimaStrat},
					&planv1.ProjectSettings{PushStrategy: &wishStrat}, "wish", dev},
			},
		},
		{
			row: "install_questions",
			value: func(s Settings) (string, planv1.SettingSource) {
				return InstallQuestionsWord(s.InstallQuestions), s.InstallQuestionsFrom
			},
			enum: func(s Settings) string {
				return strings.ToLower(strings.TrimPrefix(s.InstallQuestions.String(), "INSTALL_QUESTIONS_"))
			},
			steps: []step{
				{"neither file", nil, nil, "always", def},
				{"repo sets when_needed", &planv1.ProjectSettings{InstallQuestions: &whenNeeded}, nil, "when_needed", repo},
				{"repo sets never", &planv1.ProjectSettings{InstallQuestions: &never}, nil, "never", repo},
				{"dev overrides to always", &planv1.ProjectSettings{InstallQuestions: &whenNeeded},
					&planv1.ProjectSettings{InstallQuestions: &always}, "always", dev},
				{"dev overrides to never", &planv1.ProjectSettings{InstallQuestions: &whenNeeded},
					&planv1.ProjectSettings{InstallQuestions: &never}, "never", dev},
			},
		},
	} {
		t.Run(k.row, func(t *testing.T) {
			t.Parallel()
			for _, c := range k.steps {
				s := ResolveSettings(c.repo, c.dev)
				if got, from := k.value(s); got != c.want || from != c.from {
					t.Errorf("%s: %s=%q from %v; want %q from %v", c.name, k.row, got, from, c.want, c.from)
				}
				if k.enum != nil && k.enum(s) != c.want {
					t.Errorf("%s: %s is %s; want %s", c.name, k.row, k.enum(s), c.want)
				}
				i := slices.IndexFunc(s.Rows(), func(r *planv1.ProjectSetting) bool { return r.GetName() == k.row })
				if i < 0 || s.Rows()[i].GetValue() != c.want || s.Rows()[i].GetSource() != c.from {
					t.Errorf("%s: no row says %s=%s from %v", c.name, k.row, c.want, c.from)
				}
			}
		})
	}

	// Enlighten workers are on by default and answer workers off, on a cheaper model for claude and the provider's own
	// default for another, with a budget; either file changes them, and a file that sets the provider resets the model.
	t.Run("question workers", func(t *testing.T) {
		t.Parallel()
		antigravity := planv1.Provider_PROVIDER_ANTIGRAVITY
		fake := planv1.Provider_PROVIDER_FAKE
		type q struct {
			enlighten bool
			provider  planv1.Provider
			model     string
			budget    float64
		}
		for _, c := range []struct {
			name      string
			repo, dev *planv1.ProjectSettings
			want      q
		}{
			{"neither file", nil, nil, q{true, planv1.Provider_PROVIDER_UNSPECIFIED, DefaultQuestionModel, DefaultQuestionBudgetUSD}},
			{"another provider", &planv1.ProjectSettings{Provider: &codex}, nil, q{true, planv1.Provider_PROVIDER_UNSPECIFIED, "", DefaultQuestionBudgetUSD}},
			{"antigravity falls back to sonnet", &planv1.ProjectSettings{Provider: &antigravity}, nil, q{true, planv1.Provider_PROVIDER_UNSPECIFIED, DefaultQuestionModel, DefaultQuestionBudgetUSD}},
			{"specific enlighten_workers", &planv1.ProjectSettings{
				EnlightenWorkers: proto.Bool(false),
			}, nil, q{false, planv1.Provider_PROVIDER_UNSPECIFIED, DefaultQuestionModel, DefaultQuestionBudgetUSD}},
			{"the developer's win", &planv1.ProjectSettings{EnlightenWorkers: proto.Bool(true), QuestionProvider: &fake, QuestionModel: proto.String("haiku")},
				&planv1.ProjectSettings{EnlightenWorkers: proto.Bool(false), QuestionProvider: &codex, QuestionModel: proto.String("")}, q{false, codex, "", DefaultQuestionBudgetUSD}},
			{"a provider resets the model", &planv1.ProjectSettings{QuestionModel: proto.String("haiku")},
				&planv1.ProjectSettings{Provider: &codex}, q{true, planv1.Provider_PROVIDER_UNSPECIFIED, "", DefaultQuestionBudgetUSD}},
			{"a question_provider resets the model", &planv1.ProjectSettings{QuestionModel: proto.String("haiku")},
				&planv1.ProjectSettings{QuestionProvider: &codex}, q{true, codex, "", DefaultQuestionBudgetUSD}},
			{"inherits configured strong model", &planv1.ProjectSettings{Model: proto.String("opus")},
				nil, q{true, planv1.Provider_PROVIDER_UNSPECIFIED, "opus", DefaultQuestionBudgetUSD}},
			{"inherits configured codex model", &planv1.ProjectSettings{Provider: &codex, Model: proto.String("gpt-5.1-codex")},
				nil, q{true, planv1.Provider_PROVIDER_UNSPECIFIED, "gpt-5.1-codex", DefaultQuestionBudgetUSD}},
		} {
			s := ResolveSettings(c.repo, c.dev)
			if got := (q{s.EnlightenWorkers, s.QuestionProvider, s.QuestionModel, s.QuestionBudgetUSD}); got != c.want {
				t.Errorf("%s: %+v; want %+v", c.name, got, c.want)
			}
		}
	})

	// By default Djinn merges the remote's default branch at each release, fetched at most hourly, and a release of
	// Djinn installs by itself on main; either file changes them, the developer's winning, and the rows say where each
	// comes from.
	t.Run("main", func(t *testing.T) {
		t.Parallel()
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
	})
}

// mainRows are the rows of the settings that keep a wish's integration branch up with main.
var mainRows = []string{"main_branch", "merge_main", "merge_main_minutes", "install_releases"}

// TestEffectiveWishPushStrategy: a wish's explicit push strategy overrides its projects', which come from the built-in
// default, then the repository's file, then the developer's.
func TestEffectiveWishPushStrategy(t *testing.T) {
	t.Parallel()
	const (
		def  = planv1.SettingSource_SETTING_SOURCE_DEFAULT
		repo = planv1.SettingSource_SETTING_SOURCE_REPOSITORY
		dev  = planv1.SettingSource_SETTING_SOURCE_DEVELOPER
		wish = planv1.SettingSource_SETTING_SOURCE_WISH
	)
	wishStrat, azimaStrat := planv1.PushStrategy_PUSH_STRATEGY_WISH, planv1.PushStrategy_PUSH_STRATEGY_AZIMA
	home := t.TempDir()

	// Wish resolution: an unspecified strategy falls back to project settings; an explicit strategy overrides.
	pSettings := Settings{PushStrategy: wishStrat, PushStrategyFrom: def}
	wUnspecified := &planv1.Wish{PushStrategy: planv1.PushStrategy_PUSH_STRATEGY_UNSPECIFIED}
	if gotStrat, gotFrom := ResolveWishPushStrategy(wUnspecified, pSettings); gotStrat != wishStrat || gotFrom != def {
		t.Errorf("unspecified wish strategy: %v from %v; want %v from %v", gotStrat, gotFrom, wishStrat, def)
	}

	wAzima := &planv1.Wish{PushStrategy: azimaStrat}
	if gotStrat, gotFrom := ResolveWishPushStrategy(wAzima, pSettings); gotStrat != azimaStrat || gotFrom != wish {
		t.Errorf("explicit azima wish strategy: %v from %v; want %v from %v", gotStrat, gotFrom, azimaStrat, wish)
	}

	pSettingsAzima := Settings{PushStrategy: azimaStrat, PushStrategyFrom: dev}
	wWish := &planv1.Wish{PushStrategy: wishStrat}
	if gotStrat, gotFrom := ResolveWishPushStrategy(wWish, pSettingsAzima); gotStrat != wishStrat || gotFrom != wish {
		t.Errorf("explicit wish override: %v from %v; want %v from %v", gotStrat, gotFrom, wishStrat, wish)
	}

	// Resolution order with store: built-in default < repository < developer < wish override.
	sStore, err := store.Open(t.Context(), "", Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	defer sStore.Close()

	projDir := t.TempDir()
	pEntity := &planv1.Project{Id: store.NewID(), Directory: projDir, Name: "myproj"}
	if err := sStore.Tx(t.Context(), func(tx *store.Tx) error {
		if err := tx.Journal("test", "/test", &planv1.WishServiceListRequest{}); err != nil {
			return err
		}
		return tx.Put(pEntity)
	}); err != nil {
		t.Fatal(err)
	}

	w := &planv1.Wish{Id: store.NewID(), ProjectIds: []string{pEntity.GetId()}}

	// (a) built-in default: wish, default
	if strat, src, err := EffectiveWishPushStrategy(t.Context(), sStore, home, w); err != nil || strat != wishStrat || src != def {
		t.Errorf("EffectiveWishPushStrategy built-in = %v, %v; want wish, default", strat, src)
	}

	// (b) repository default: azima, repository
	repoSettingsFile := filepath.Join(projDir, ".agents", "settings.txtpb")
	writeSettings(t, repoSettingsFile, "push_strategy: PUSH_STRATEGY_AZIMA\n")
	if strat, src, err := EffectiveWishPushStrategy(t.Context(), sStore, home, w); err != nil || strat != azimaStrat || src != repo {
		t.Errorf("EffectiveWishPushStrategy repo = %v, %v; want azima, repository", strat, src)
	}

	// (c) developer default overrides repository: wish, developer
	if err := SaveDeveloperPushStrategy(home, pEntity.GetId(), wishStrat); err != nil {
		t.Fatal(err)
	}
	if strat, src, err := EffectiveWishPushStrategy(t.Context(), sStore, home, w); err != nil || strat != wishStrat || src != dev {
		t.Errorf("EffectiveWishPushStrategy dev = %v, %v; want wish, developer", strat, src)
	}

	// (d) wish override overrides developer default: azima, wish
	w.PushStrategy = azimaStrat
	if strat, src, err := EffectiveWishPushStrategy(t.Context(), sStore, home, w); err != nil || strat != azimaStrat || src != wish {
		t.Errorf("EffectiveWishPushStrategy wish override = %v, %v; want azima, wish", strat, src)
	}
}

// TestWatchCommandsSettings (Q257 B): the watcher commands a developer allows come from their own file only, never
// from the repository's, which is warned of; djinn project allow --watch adds one to their file, once, takes it back,
// and refuses one that looks like a secret, which would make Djinn refuse the whole file. djinn project show lists them
// with their source.
func TestWatchCommandsSettings(t *testing.T) {
	t.Parallel()
	const (
		def = planv1.SettingSource_SETTING_SOURCE_DEFAULT
		dev = planv1.SettingSource_SETTING_SOURCE_DEVELOPER
	)
	repo := &planv1.ProjectSettings{WatchCommands: []string{"rm -rf"}}
	if s := ResolveSettings(repo, nil); len(s.WatchCommands) != 0 || s.WatchCommandsFrom != def {
		t.Errorf("the repository's: %q from %v, want none", s.WatchCommands, s.WatchCommandsFrom)
	}
	if s := ResolveSettings(repo, &planv1.ProjectSettings{WatchCommands: []string{"mrwatch -mr"}}); !slices.Equal(s.WatchCommands, []string{"mrwatch -mr"}) ||
		s.WatchCommandsFrom != dev {
		t.Errorf("the developer's: %q from %v", s.WatchCommands, s.WatchCommandsFrom)
	}

	ctx := t.Context()
	home := t.TempDir()
	c := serve(t, WithHome(home))
	dir := t.TempDir()
	add, err := c.projects.Add(ctx, connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: dir, Name: "dolmen"}))
	if err != nil {
		t.Fatal(err)
	}
	devFile := DeveloperSettingsFile(home, add.Msg.GetProject().GetId())
	allow := func(watch *string, revoke bool) (*planv1.ProjectServiceAllowResponse, error) {
		res, err := c.projects.Allow(ctx, connect.NewRequest(&planv1.ProjectServiceAllowRequest{Project: "dolmen", Watch: watch, Revoke: revoke}))
		if err != nil {
			return nil, err
		}
		return res.Msg, nil
	}
	if res, err := allow(nil, false); err != nil || len(res.GetWatchCommands()) != 0 || res.GetDeveloperFile() != devFile {
		t.Errorf("none yet: %v, %v", res, err)
	}
	mrwatch := "go run ./.agents/skills/babysit-mr-empowill/mrwatch"
	for range 2 {
		if res, err := allow(proto.String(" go  run ./.agents/skills/babysit-mr-empowill/mrwatch "), false); err != nil ||
			!slices.Equal(res.GetWatchCommands(), []string{mrwatch}) {
			t.Errorf("allowed: %v, %v; want it once, its words joined by one space", res, err)
		}
	}
	if res, err := allow(proto.String("glab mr view"), false); err != nil || len(res.GetWatchCommands()) != 2 {
		t.Errorf("a second one: %v, %v", res, err)
	}
	show, err := c.projects.Show(ctx, connect.NewRequest(&planv1.ProjectServiceShowRequest{Project: "dolmen"}))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(show.Msg.GetWatchCommands(), []string{mrwatch, "glab mr view"}) {
		t.Errorf("show: %q", show.Msg.GetWatchCommands())
	}
	i := slices.IndexFunc(show.Msg.GetSettings(), func(s *planv1.ProjectSetting) bool { return s.GetName() == "watch_commands" })
	if i < 0 || show.Msg.GetSettings()[i].GetValue() != mrwatch+"; glab mr view" || show.Msg.GetSettings()[i].GetSource() != dev {
		t.Errorf("show's row: %v", show.Msg.GetSettings())
	}
	if res, err := allow(proto.String(mrwatch), true); err != nil || !slices.Equal(res.GetWatchCommands(), []string{"glab mr view"}) {
		t.Errorf("taken back: %v, %v", res, err)
	}
	if _, err := allow(proto.String(mrwatch), true); code(err) != connect.CodeFailedPrecondition {
		t.Errorf("taken back twice: %v", err)
	}
	if _, err := allow(nil, true); code(err) != connect.CodeInvalidArgument {
		t.Errorf("--revoke without --watch: %v", err)
	}
	if _, err := allow(proto.String("watch --token sk-abcdefghijklmnopqrstuvwxyz"), false); code(err) != connect.CodeFailedPrecondition ||
		strings.Contains(err.Error(), "sk-abc") {
		t.Errorf("a secret: %v", err)
	}
	if dev, err := ReadSettings(devFile); err != nil || !slices.Equal(dev.GetWatchCommands(), []string{"glab mr view"}) {
		t.Errorf("the developer's file: %v, %v", dev, err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".agents")); !os.IsNotExist(err) {
		t.Errorf("the repository was touched: %v", err)
	}
	// An ifrit's serve commands (djinn project allow --serve), beside the watcher's.
	serveRes, err := c.projects.Allow(ctx, connect.NewRequest(&planv1.ProjectServiceAllowRequest{Project: "dolmen", Serve: proto.String("npm  run dev")}))
	if err != nil || !slices.Equal(serveRes.Msg.GetServeCommands(), []string{"npm run dev"}) ||
		!slices.Equal(serveRes.Msg.GetWatchCommands(), []string{"glab mr view"}) {
		t.Errorf("a serve command allowed: %v, %v", serveRes, err)
	}
	if _, err := c.projects.Allow(ctx, connect.NewRequest(&planv1.ProjectServiceAllowRequest{
		Project: "dolmen", Serve: proto.String("npm run dev"), Watch: proto.String("glab mr view"),
	})); code(err) != connect.CodeInvalidArgument {
		t.Errorf("--serve and --watch at once: %v", err)
	}
	if s := ResolveSettings(&planv1.ProjectSettings{ServeCommands: []string{"rm -rf"}}, nil); len(s.ServeCommands) != 0 {
		t.Errorf("the repository's serve commands: %q, want none", s.ServeCommands)
	}

	writeSettings(t, filepath.Join(dir, SettingsFile), "watch_commands: \"rm -rf\"\n")
	show, err = c.projects.Show(ctx, connect.NewRequest(&planv1.ProjectServiceShowRequest{Project: "dolmen"}))
	if err != nil || !slices.ContainsFunc(show.Msg.GetNotices(), func(n *planv1.ProjectNotice) bool {
		return n.GetAction() == planv1.ProjectNoticeAction_PROJECT_NOTICE_ACTION_FIX_FILE &&
			strings.Contains(n.GetDetail(), "watch_commands is ignored")
	}) ||
		slices.Contains(show.Msg.GetWatchCommands(), "rm -rf") {
		t.Errorf("the repository's watch_commands: %v, %v", show.Msg, err)
	}
}

func TestProjectShow(t *testing.T) {
	t.Parallel()
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
			if strings.HasPrefix(s.GetName(), "question_") || s.GetName() == "enlighten_workers" || slices.Contains(mainRows, s.GetName()) {
				continue // TestResolveSettings checks the question workers' and main's.
			}
			b = append(b, s.GetName()+"="+s.GetValue()+" "+strings.TrimPrefix(s.GetSource().String(), "SETTING_SOURCE_"))
		}
		return strings.Join(b, ", ")
	}

	res := show()
	repoFile := filepath.Join(project.GetDirectory(), ".agents", "settings.txtpb")
	devFile := filepath.Join(home, "projects", project.GetId(), "settings.txtpb")
	if res.GetRepositoryFile() != repoFile || res.GetDeveloperFile() != devFile || len(res.GetNotices()) > 0 {
		t.Errorf("files = %s, %s, %v; want %s, %s", res.GetRepositoryFile(), res.GetDeveloperFile(), res.GetNotices(), repoFile, devFile)
	}
	if got, want := rows(res), "provider=claude DEFAULT, model= DEFAULT, max_budget_usd= DEFAULT, branch={code}-{slug}-{uuid8} DEFAULT, "+
		"generated= DEFAULT, generate= DEFAULT, setup= DEFAULT, checks= DEFAULT, test_one= DEFAULT, correction_attempts=2 DEFAULT, install= DEFAULT, install_questions=always DEFAULT, install_builds=true DEFAULT, push=standard DEFAULT, push_strategy=wish DEFAULT, babysit=true DEFAULT, watch_commands= DEFAULT, serve_commands= DEFAULT"; got != want {
		t.Errorf("no file: %s; want %s", got, want)
	}
	var question []string
	for _, s := range res.GetSettings() {
		if strings.HasPrefix(s.GetName(), "question_") || s.GetName() == "enlighten_workers" {
			question = append(question, s.GetName()+"="+s.GetValue())
		}
	}
	if got, want := strings.Join(question, ", "), "enlighten_workers=true, question_provider=, question_model=sonnet, question_budget_usd=2"; got != want {
		t.Errorf("no file, the question workers: %s; want %s", got, want)
	}

	writeSettings(t, repoFile, "provider: PROVIDER_CLAUDE\nmodel: \"opus\"\nmax_budget_usd: 3\nbranch: \"djinn/{code}-{uuid8}\"\n"+
		"generated: \"gen/**\"\ngenerated: \"docs/schema.json\"\ngenerate: \"go tool task gen\"\ntest: \"go tool task test\"\ncorrection_attempts: 3\ninstall: \"go tool task install\"\n")
	writeSettings(t, devFile, "model: \"sonnet\"\nbranch: \"me/{slug}-{uuid8}\"\ntest: \"go tool task test-go\"\n")
	if got, want := rows(show()), "provider=claude REPOSITORY, model=sonnet DEVELOPER, max_budget_usd=3 REPOSITORY, "+
		"branch=me/{slug}-{uuid8} DEVELOPER, generated=gen/**,docs/schema.json REPOSITORY, generate=go tool task gen REPOSITORY, "+
		"setup= DEFAULT, checks=test: go tool task test-go (commit) DEVELOPER, test_one= DEFAULT, correction_attempts=3 REPOSITORY, "+
		"install=go tool task install REPOSITORY, install_questions=always DEFAULT, install_builds=true DEFAULT, push=standard DEFAULT, push_strategy=wish DEFAULT, babysit=true DEFAULT, watch_commands= DEFAULT, serve_commands= DEFAULT"; got != want {
		t.Errorf("both files: %s; want %s", got, want)
	}

	// A malformed file is a problem shown, not a failure: the other file still counts.
	writeSettings(t, devFile, "model: sonnet\n")
	res = show()
	if got, want := rows(res), "provider=claude REPOSITORY, model=opus REPOSITORY, max_budget_usd=3 REPOSITORY, "+
		"branch=djinn/{code}-{uuid8} REPOSITORY, generated=gen/**,docs/schema.json REPOSITORY, "+
		"generate=go tool task gen REPOSITORY, setup= DEFAULT, checks=test: go tool task test (commit) REPOSITORY, test_one= DEFAULT, correction_attempts=3 REPOSITORY, "+
		"install=go tool task install REPOSITORY, install_questions=always DEFAULT, install_builds=true DEFAULT, push=standard DEFAULT, push_strategy=wish DEFAULT, babysit=true DEFAULT, watch_commands= DEFAULT, serve_commands= DEFAULT"; got != want {
		t.Errorf("a malformed developer file: %s; want %s", got, want)
	}
	if n := res.GetNotices(); len(n) != 1 || n[0].GetKind() != planv1.ProjectNoticeKind_PROJECT_NOTICE_KIND_TO_FIX ||
		n[0].GetFile() != devFile || !strings.Contains(n[0].GetDetail(), devFile) || res.GetProject().GetToFix() != 1 {
		t.Errorf("notices = %v, to fix %d; want one naming %s", n, res.GetProject().GetToFix(), devFile)
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

// TestIfritChecksBrief (T51, Q11): an ifrit's first prompt says which checks Djinn runs, but that it runs none after
// a change: Djinn runs the commit checks once it is finished; a test the developer asks for has its command.
func TestIfritChecksBrief(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		s    Settings
		want string
	}{
		{"no check", Settings{}, ""},
		{"both", Settings{Setup: "npm ci", TestOne: "go tool task test-pkg -- {pkg}", Checks: []*planv1.ProjectCheck{
			atCommit("lint", "go tool task lint"),
			{Name: "test", Command: "go tool task test", When: []planv1.CheckWhen{planv1.CheckWhen_CHECK_WHEN_PUSH}},
		}}, "Djinn checks this project's work before it merges a task's work, with `djinn gate run lint -- go tool task lint`; " +
			"before it pushes, with `djinn gate run test -- go tool task test`. Run neither after a change: Djinn runs the " +
			"commit checks once you are finished, the push checks before it pushes. A test the developer asks for runs with " +
			"`go tool task test-pkg -- {pkg}`. A fresh worktree needs `npm ci` first."},
		{"commit only", Settings{Checks: []*planv1.ProjectCheck{atCommit("test", "make test")}},
			"Djinn checks this project's work before it merges a task's work, with `djinn gate run test -- make test`. " +
				"Do not run them after a change: Djinn runs them once you are finished."},
		{"push only", Settings{Checks: []*planv1.ProjectCheck{{Name: "lint", Command: "make lint", When: []planv1.CheckWhen{planv1.CheckWhen_CHECK_WHEN_PUSH}}}},
			"Djinn checks this project's work before it pushes, with `djinn gate run lint -- make lint`. Do not run them: " +
				"Djinn runs them before it pushes."},
	} {
		got := tc.s.IfritChecksBrief()
		if got != tc.want {
			t.Errorf("%s:\n%s\nwant:\n%s", tc.name, got, tc.want)
		}
		if strings.Contains(got, "A worker runs the commit checks") {
			t.Errorf("%s: an ifrit is told to run the commit checks: %s", tc.name, got)
		}
	}
}

// TestChecksBrief: the lead's brief and each worker's first prompt say which checks Djinn runs and when, how to run
// them through their gates, and what a fresh worktree needs first; nothing for a project without checks.
func TestChecksBrief(t *testing.T) {
	t.Parallel()
	lintAndTest := []*planv1.ProjectCheck{
		atCommit("lint", "go tool task lint"),
		{Name: "test", Command: "go tool task test", When: []planv1.CheckWhen{planv1.CheckWhen_CHECK_WHEN_PUSH}},
	}
	for _, tc := range []struct {
		name string
		s    Settings
		want string
	}{
		{"no check", Settings{}, ""},
		{"commit and push", Settings{Setup: "npm ci", Checks: lintAndTest},
			"Djinn checks this project's work before it merges a task's work, with `djinn gate run lint -- go tool task lint`; " +
				"before it pushes, with `djinn gate run test -- go tool task test`. A worker runs the commit checks before it ends, " +
				"and fixes what they find, then only the tests that cover what it changed; do not run the push checks (`djinn gate run test -- go tool task test`): " +
				"Djinn runs them before pushing. A fresh worktree needs `npm ci` first."},
		{"with test_one", Settings{Setup: "npm ci", Checks: lintAndTest, TestOne: "go tool task test-pkg -- {pkg}"},
			"Djinn checks this project's work before it merges a task's work, with `djinn gate run lint -- go tool task lint`; " +
				"before it pushes, with `djinn gate run test -- go tool task test`. A worker runs the commit checks before it ends, " +
				"and fixes what they find, then only the tests that cover what it changed (`go tool task test-pkg -- {pkg}`); " +
				"do not run the push checks (`djinn gate run test -- go tool task test`): Djinn runs them before pushing. A fresh worktree needs `npm ci` first."},
		// A commit check named test is the whole suite.
		{"whole suite at commit", Settings{Checks: []*planv1.ProjectCheck{atCommit("test", "make test")}},
			"Djinn checks this project's work before it merges a task's work, with `djinn gate run test -- make test`. " +
				"A worker runs the commit checks before it ends, and fixes what they find (the commit check is the whole suite)."},
		{"push only", Settings{Checks: []*planv1.ProjectCheck{{Name: "lint", Command: "make lint", When: []planv1.CheckWhen{planv1.CheckWhen_CHECK_WHEN_PUSH}}}},
			"Djinn checks this project's work before it pushes, with `djinn gate run lint -- make lint`. " +
				"A worker runs only the tests that cover what it changed; do not run the push checks (`djinn gate run lint -- make lint`): Djinn runs them before pushing."},
	} {
		if got := tc.s.ChecksBrief(); got != tc.want {
			t.Errorf("%s:\n%s\nwant:\n%s", tc.name, got, tc.want)
		}
	}
}

// TestProjectSetDefaults (W383): ProjectService.SetDefaults writes the developer's default provider, model, and
// question_model, validates provider and model before writing, and ProjectService.Show reflects it at once.
func TestProjectSetDefaults(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st, err := store.Open(ctx, "", Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	home := t.TempDir()
	repo := t.TempDir()
	writeSettings(t, filepath.Join(repo, ".agents", "settings.txtpb"),
		"provider: PROVIDER_CLAUDE\nmodel: \"sonnet\"\nquestion_model: \"haiku\"\n")

	projects := &Projects{Store: st, Home: home}
	added, err := projects.Add(ctx, connect.NewRequest(&planv1.ProjectServiceAddRequest{
		Directory: repo, Name: "lamp",
	}))
	if err != nil {
		t.Fatal(err)
	}
	projectID := added.Msg.GetProject().GetId()

	settingMap := func(rows []*planv1.ProjectSetting) map[string]*planv1.ProjectSetting {
		m := make(map[string]*planv1.ProjectSetting, len(rows))
		for _, r := range rows {
			m[r.GetName()] = r
		}
		return m
	}

	// 1. Set Antigravity with "Gemini 4 Argon (High)" and question_model "sonnet".
	agy := planv1.Provider_PROVIDER_ANTIGRAVITY
	res, err := projects.SetDefaults(ctx, connect.NewRequest(&planv1.ProjectServiceSetDefaultsRequest{
		Project:       "lamp",
		Provider:      &agy,
		Model:         proto.String("Gemini 4 Argon (High)"),
		QuestionModel: proto.String("sonnet"),
	}))
	if err != nil {
		t.Fatalf("SetDefaults(antigravity, Gemini 4 Argon (High)): %v", err)
	}
	if res.Msg.GetDeveloperFile() != DeveloperSettingsFile(home, projectID) {
		t.Errorf("developer_file = %q, want %q", res.Msg.GetDeveloperFile(), DeveloperSettingsFile(home, projectID))
	}
	devSettings, err := ReadSettings(res.Msg.GetDeveloperFile())
	if err != nil {
		t.Fatalf("ReadSettings(developer_file): %v", err)
	}
	if devSettings.GetProvider() != agy || devSettings.GetModel() != "Gemini 4 Argon (High)" || devSettings.GetQuestionModel() != "sonnet" {
		t.Errorf("written developer file = %v", devSettings)
	}

	// ProjectService.Show reflects it at once.
	shown, err := projects.Show(ctx, connect.NewRequest(&planv1.ProjectServiceShowRequest{Project: "lamp"}))
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	rows := settingMap(shown.Msg.GetSettings())
	if got := rows["provider"]; got.GetValue() != "antigravity" || got.GetSource() != planv1.SettingSource_SETTING_SOURCE_DEVELOPER {
		t.Errorf("show provider = %v", got)
	}
	if got := rows["model"]; got.GetValue() != "Gemini 4 Argon (High)" || got.GetSource() != planv1.SettingSource_SETTING_SOURCE_DEVELOPER {
		t.Errorf("show model = %v", got)
	}
	if got := rows["question_model"]; got.GetValue() != "sonnet" || got.GetSource() != planv1.SettingSource_SETTING_SOURCE_DEVELOPER {
		t.Errorf("show question_model = %v", got)
	}

	// 2. Switching provider without --model resets model to "" (provider's own default).
	codex := planv1.Provider_PROVIDER_CODEX
	res, err = projects.SetDefaults(ctx, connect.NewRequest(&planv1.ProjectServiceSetDefaultsRequest{
		Project:  "lamp",
		Provider: &codex,
	}))
	if err != nil {
		t.Fatalf("SetDefaults(codex): %v", err)
	}
	rows = settingMap(res.Msg.GetSettings())
	if got := rows["provider"]; got.GetValue() != "codex" || got.GetSource() != planv1.SettingSource_SETTING_SOURCE_DEVELOPER {
		t.Errorf("codex provider = %v", got)
	}
	if got := rows["model"]; got.GetValue() != "" || got.GetSource() != planv1.SettingSource_SETTING_SOURCE_DEVELOPER {
		t.Errorf("codex model = %v, want empty from developer", got)
	}

	// 3. Refusal of unknown, foreign, or ill-formed models and invalid flags.
	claude := planv1.Provider_PROVIDER_CLAUDE
	watch := planv1.Provider_PROVIDER_WATCH
	fake := planv1.Provider_PROVIDER_FAKE
	for _, bad := range []*planv1.ProjectServiceSetDefaultsRequest{
		{Project: "lamp"},
		{Project: "lamp", Provider: &claude, Unset: true},
		{Project: "lamp", Provider: &watch},
		{Project: "lamp", Provider: &fake},
		{Project: "lamp", Provider: &claude, Model: proto.String("unknown-model-xyz")},
		{Project: "lamp", Provider: &claude, Model: proto.String("gpt-5.5")},
		{Project: "lamp", Provider: &claude, Model: proto.String("Gemini 4 Argon (High)")},
		{Project: "lamp", Provider: &agy, Model: proto.String("opus")},
		{Project: "lamp", Provider: &codex, Model: proto.String("sonnet")},
		{Project: "lamp", Provider: &claude, Model: proto.String("opus; rm -rf /")},
		{Project: "lamp", Provider: &claude, Model: proto.String("   ")},
		{Project: "lamp", Provider: &claude, QuestionModel: proto.String("gpt-5.5")},
		{Project: "lamp", Provider: &claude, QuestionModel: proto.String("not-a-model")},
	} {
		if _, err := projects.SetDefaults(ctx, connect.NewRequest(bad)); err == nil {
			t.Errorf("SetDefaults(%v): succeeded, want error", bad)
		}
	}
	// File remains valid and unchanged after refused requests.
	if s, err := ReadSettings(DeveloperSettingsFile(home, projectID)); err != nil || s.GetProvider() != codex {
		t.Errorf("after refused calls: %v, %v", s, err)
	}

	// 4. --unset restores repository settings.
	res, err = projects.SetDefaults(ctx, connect.NewRequest(&planv1.ProjectServiceSetDefaultsRequest{
		Project: "lamp",
		Unset:   true,
	}))
	if err != nil {
		t.Fatalf("SetDefaults(--unset): %v", err)
	}
	rows = settingMap(res.Msg.GetSettings())
	if got := rows["provider"]; got.GetValue() != "claude" || got.GetSource() != planv1.SettingSource_SETTING_SOURCE_REPOSITORY {
		t.Errorf("after unset provider = %v", got)
	}
	if got := rows["model"]; got.GetValue() != "sonnet" || got.GetSource() != planv1.SettingSource_SETTING_SOURCE_REPOSITORY {
		t.Errorf("after unset model = %v", got)
	}
	if got := rows["question_model"]; got.GetValue() != "haiku" || got.GetSource() != planv1.SettingSource_SETTING_SOURCE_REPOSITORY {
		t.Errorf("after unset question_model = %v", got)
	}
}

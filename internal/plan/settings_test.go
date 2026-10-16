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

func TestReadSettingsUnknownField(t *testing.T) {
	ResetWarnedUnknownFields()
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

func TestLoadSettingsUnknownField(t *testing.T) {
	ResetWarnedUnknownFields()
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

func TestSettingsRefuseRemovedKeys(t *testing.T) {
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

	// A comment mentioning a removed key is not refused.
	writeSettings(t, path, "# answer_workers was removed\nenlighten_workers: true\n")
	s, err := ReadSettings(path)
	if err != nil {
		t.Errorf("comment with removed key refused: %v", err)
	}
	if s == nil || !s.GetEnlightenWorkers() {
		t.Errorf("got %v, want enlighten_workers: true", s)
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
		got.EnlightenWorkers = false
		got.QuestionProvider, got.QuestionModel, got.QuestionBudgetUSD = 0, "", 0
		got.EnlightenWorkersFrom = 0
		got.QuestionProviderFrom, got.QuestionModelFrom, got.QuestionBudgetFrom = 0, 0, 0
		// Main's are TestMainSettings'.
		got.MainBranch, got.MergeMain, got.MergeMainEvery, got.InstallReleases = "", 0, 0, false
		got.MainBranchFrom, got.MergeMainFrom, got.MergeMainEveryFrom, got.InstallReleasesFrom = 0, 0, 0, 0
		// Push is TestPushSettings', push strategy is TestPushStrategySettings', test_one is TestTestOneSettings'.
		got.Push, got.PushFrom = 0, 0
		got.PushStrategy, got.PushStrategyFrom = 0, 0
		got.TestOne, got.TestOneFrom = "", 0
		// Install questions is TestInstallQuestionsSettings'.
		got.InstallQuestions, got.InstallQuestionsFrom = 0, 0
		// Babysit is TestBabysitSettings'.
		got.Babysit, got.BabysitFrom = false, 0
		// Watch commands are TestWatchCommandsSettings'.
		got.WatchCommands, got.WatchCommandsFrom = nil, 0
		got.ServeCommands, got.ServeCommandsFrom = nil, 0
		// Install builds is TestInstallBuildsSettings'.
		got.InstallBuilds, got.InstallBuildsFrom = false, 0
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %+v; want %+v", c.name, got, c.want)
		}
	}
}

// TestBabysitSettings: babysitting what tasks open is on by default; either file turns it off, the developer's
// winning, read from a settings file as babysit: false.
func TestBabysitSettings(t *testing.T) {
	t.Parallel()
	const (
		def  = planv1.SettingSource_SETTING_SOURCE_DEFAULT
		repo = planv1.SettingSource_SETTING_SOURCE_REPOSITORY
		dev  = planv1.SettingSource_SETTING_SOURCE_DEVELOPER
	)
	for _, c := range []struct {
		name      string
		repo, dev *planv1.ProjectSettings
		want      bool
		from      planv1.SettingSource
	}{
		{"neither file", nil, nil, true, def},
		{"the repository turns it off", &planv1.ProjectSettings{Babysit: proto.Bool(false)}, nil, false, repo},
		{"the developer turns it on again", &planv1.ProjectSettings{Babysit: proto.Bool(false)},
			&planv1.ProjectSettings{Babysit: proto.Bool(true)}, true, dev},
	} {
		if s := ResolveSettings(c.repo, c.dev); s.Babysit != c.want || s.BabysitFrom != c.from {
			t.Errorf("%s: babysit %v from %v, want %v from %v", c.name, s.Babysit, s.BabysitFrom, c.want, c.from)
		}
	}
	path := filepath.Join(t.TempDir(), "settings.txtpb")
	writeSettings(t, path, "babysit: false\n")
	if s, err := ReadSettings(path); err != nil || s.Babysit == nil || s.GetBabysit() {
		t.Errorf("babysit: false read as %v (%v)", s.Babysit, err)
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
	if err != nil || !slices.ContainsFunc(show.Msg.GetWarnings(), func(w string) bool { return strings.Contains(w, "watch_commands is ignored") }) ||
		slices.Contains(show.Msg.GetWatchCommands(), "rm -rf") {
		t.Errorf("the repository's watch_commands: %v, %v", show.Msg, err)
	}
}

// TestInstallBuildsSettings: a build of the running Djinn's line installs by itself by default; either file turns it
// off, the developer's winning, read from a settings file as install_builds: false, and its row says so.
func TestInstallBuildsSettings(t *testing.T) {
	t.Parallel()
	const (
		def  = planv1.SettingSource_SETTING_SOURCE_DEFAULT
		repo = planv1.SettingSource_SETTING_SOURCE_REPOSITORY
		dev  = planv1.SettingSource_SETTING_SOURCE_DEVELOPER
	)
	for _, c := range []struct {
		name      string
		repo, dev *planv1.ProjectSettings
		want      bool
		from      planv1.SettingSource
	}{
		{"neither file", nil, nil, true, def},
		{"the repository turns it off", &planv1.ProjectSettings{InstallBuilds: proto.Bool(false)}, nil, false, repo},
		{"the developer turns it on again", &planv1.ProjectSettings{InstallBuilds: proto.Bool(false)},
			&planv1.ProjectSettings{InstallBuilds: proto.Bool(true)}, true, dev},
	} {
		s := ResolveSettings(c.repo, c.dev)
		if s.InstallBuilds != c.want || s.InstallBuildsFrom != c.from {
			t.Errorf("%s: install_builds %v from %v, want %v from %v", c.name, s.InstallBuilds, s.InstallBuildsFrom, c.want, c.from)
		}
		i := slices.IndexFunc(s.Rows(), func(r *planv1.ProjectSetting) bool { return r.GetName() == "install_builds" })
		if i < 0 || s.Rows()[i].GetValue() != strconv.FormatBool(c.want) || s.Rows()[i].GetSource() != c.from {
			t.Errorf("%s: no row says install_builds=%v", c.name, c.want)
		}
	}
	path := filepath.Join(t.TempDir(), "settings.txtpb")
	writeSettings(t, path, "install_builds: false\n")
	if s, err := ReadSettings(path); err != nil || s.InstallBuilds == nil || s.GetInstallBuilds() {
		t.Errorf("install_builds: false read as %v (%v)", s.InstallBuilds, err)
	}
}

// TestTestOneSettings: a project may name its targeted-test command in its settings (test_one),
// either file sets it, and the developer's file overrides the repository's.
func TestTestOneSettings(t *testing.T) {
	def, repo, dev := planv1.SettingSource_SETTING_SOURCE_DEFAULT, planv1.SettingSource_SETTING_SOURCE_REPOSITORY, planv1.SettingSource_SETTING_SOURCE_DEVELOPER

	// 1. Neither file: default empty
	s := ResolveSettings(nil, nil)
	if s.TestOne != "" || s.TestOneFrom != def {
		t.Errorf("default test_one: %q from %v; want empty from %v", s.TestOne, s.TestOneFrom, def)
	}

	// 2. Repository file sets test_one
	repoSettings := &planv1.ProjectSettings{TestOne: proto.String("go tool task test-pkg -- {pkg}")}
	s = ResolveSettings(repoSettings, nil)
	if s.TestOne != "go tool task test-pkg -- {pkg}" || s.TestOneFrom != repo {
		t.Errorf("repo test_one: %q from %v; want template from %v", s.TestOne, s.TestOneFrom, repo)
	}

	// 3. Developer file overrides repository file
	devSettings := &planv1.ProjectSettings{TestOne: proto.String("npm test -- {spec}")}
	s = ResolveSettings(repoSettings, devSettings)
	if s.TestOne != "npm test -- {spec}" || s.TestOneFrom != dev {
		t.Errorf("dev test_one override: %q from %v; want template from %v", s.TestOne, s.TestOneFrom, dev)
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
			if strings.HasPrefix(s.GetName(), "question_") || s.GetName() == "enlighten_workers" || slices.Contains(mainRows, s.GetName()) {
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
		"and fixes what they find, then only the tests that cover what it changed; do not run the push checks (`djinn gate run test -- go tool task test`): " +
		"Djinn runs them before pushing. A fresh worktree needs `npm ci` first."
	if got := s.ChecksBrief(); got != want {
		t.Errorf("brief:\n%s\nwant:\n%s", got, want)
	}

	// With test_one template
	sTestOne := Settings{
		Setup: "npm ci",
		Checks: []*planv1.ProjectCheck{
			atCommit("lint", "go tool task lint"),
			{Name: "test", Command: "go tool task test", When: []planv1.CheckWhen{planv1.CheckWhen_CHECK_WHEN_PUSH}},
		},
		TestOne: "go tool task test-pkg -- {pkg}",
	}
	wantTestOne := "Djinn checks this project's work before it commits a task's work, with `djinn gate run lint -- go tool task lint`; " +
		"before it pushes, with `djinn gate run test -- go tool task test`. A worker runs the commit checks before it ends, " +
		"and fixes what they find, then only the tests that cover what it changed (`go tool task test-pkg -- {pkg}`); " +
		"do not run the push checks (`djinn gate run test -- go tool task test`): Djinn runs them before pushing. A fresh worktree needs `npm ci` first."
	if got := sTestOne.ChecksBrief(); got != wantTestOne {
		t.Errorf("brief with test_one:\n%s\nwant:\n%s", got, wantTestOne)
	}

	// When a commit check is the whole suite (name "test")
	sWhole := Settings{Checks: []*planv1.ProjectCheck{atCommit("test", "make test")}}
	wantWhole := "Djinn checks this project's work before it commits a task's work, with `djinn gate run test -- make test`. " +
		"A worker runs the commit checks before it ends, and fixes what they find (the commit check is the whole suite)."
	if got := sWhole.ChecksBrief(); got != wantWhole {
		t.Errorf("brief whole suite at commit:\n%s\nwant:\n%s", got, wantWhole)
	}

	push := Settings{Checks: []*planv1.ProjectCheck{{Name: "lint", Command: "make lint", When: []planv1.CheckWhen{planv1.CheckWhen_CHECK_WHEN_PUSH}}}}
	wantPush := "Djinn checks this project's work before it pushes, with `djinn gate run lint -- make lint`. " +
		"A worker runs only the tests that cover what it changed; do not run the push checks (`djinn gate run lint -- make lint`): Djinn runs them before pushing."
	if got, want := push.ChecksBrief(), wantPush; got != want {
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

// TestPushSettings: by default Djinn pushes at the standard cadence; either file changes it, the developer's
// winning, and SaveDeveloperPush writes the developer's choice.
func TestPushSettings(t *testing.T) {
	const (
		def  = planv1.SettingSource_SETTING_SOURCE_DEFAULT
		repo = planv1.SettingSource_SETTING_SOURCE_REPOSITORY
		dev  = planv1.SettingSource_SETTING_SOURCE_DEVELOPER
	)
	standard, onDemand := planv1.ProjectPush_PROJECT_PUSH_STANDARD, planv1.ProjectPush_PROJECT_PUSH_ON_DEMAND
	for _, c := range []struct {
		name      string
		repo, dev *planv1.ProjectSettings
		wantPush  planv1.ProjectPush
		wantFrom  planv1.SettingSource
		wantWord  string
	}{
		{"neither file", nil, nil, standard, def, "standard"},
		{"the team's on demand", &planv1.ProjectSettings{Push: &onDemand}, nil, onDemand, repo, "on_demand"},
		{"the developer overrides to standard", &planv1.ProjectSettings{Push: &onDemand}, &planv1.ProjectSettings{Push: &standard}, standard, dev, "standard"},
	} {
		s := ResolveSettings(c.repo, c.dev)
		if s.Push != c.wantPush || s.PushFrom != c.wantFrom {
			t.Errorf("%s: push=%v from=%v; want %v from=%v", c.name, s.Push, s.PushFrom, c.wantPush, c.wantFrom)
		}
		if got := ProjectPushWord(s.Push); got != c.wantWord {
			t.Errorf("%s: word=%s; want %s", c.name, got, c.wantWord)
		}
	}

	home := t.TempDir()
	projectID := "01946059-e68b-7cb8-8c1d-6b586e3f4ea2"
	project := &planv1.Project{Id: projectID}

	if err := SaveDeveloperPush(home, projectID, onDemand); err != nil {
		t.Fatalf("SaveDeveloperPush: %v", err)
	}
	s, err := LoadSettings(home, project)
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if s.Push != onDemand || s.PushFrom != dev {
		t.Errorf("saved developer push: %v from %v; want on_demand from developer", s.Push, s.PushFrom)
	}

	if err := SaveDeveloperPush("", projectID, onDemand); err == nil {
		t.Error("SaveDeveloperPush without home: want error")
	}
	if err := SaveDeveloperPush(home, "", onDemand); err == nil {
		t.Error("SaveDeveloperPush without project ID: want error")
	}
}

// TestPushStrategySettings: by default Djinn integrates per wish; either file changes it, the developer's
// winning, SaveDeveloperPushStrategy writes the developer's choice, and a wish's explicit strategy overrides both.
func TestPushStrategySettings(t *testing.T) {
	const (
		def  = planv1.SettingSource_SETTING_SOURCE_DEFAULT
		repo = planv1.SettingSource_SETTING_SOURCE_REPOSITORY
		dev  = planv1.SettingSource_SETTING_SOURCE_DEVELOPER
		wish = planv1.SettingSource_SETTING_SOURCE_WISH
	)
	wishStrat, azimaStrat := planv1.PushStrategy_PUSH_STRATEGY_WISH, planv1.PushStrategy_PUSH_STRATEGY_AZIMA
	for _, c := range []struct {
		name         string
		repo, dev    *planv1.ProjectSettings
		wantStrategy planv1.PushStrategy
		wantFrom     planv1.SettingSource
		wantWord     string
	}{
		{"neither file", nil, nil, wishStrat, def, "wish"},
		{"the team's azima", &planv1.ProjectSettings{PushStrategy: &azimaStrat}, nil, azimaStrat, repo, "azima"},
		{"the developer overrides to wish", &planv1.ProjectSettings{PushStrategy: &azimaStrat}, &planv1.ProjectSettings{PushStrategy: &wishStrat}, wishStrat, dev, "wish"},
	} {
		s := ResolveSettings(c.repo, c.dev)
		if s.PushStrategy != c.wantStrategy || s.PushStrategyFrom != c.wantFrom {
			t.Errorf("%s: push_strategy=%v from=%v; want %v from=%v", c.name, s.PushStrategy, s.PushStrategyFrom, c.wantStrategy, c.wantFrom)
		}
		if got := PushStrategyWord(s.PushStrategy); got != c.wantWord {
			t.Errorf("%s: word=%s; want %s", c.name, got, c.wantWord)
		}
	}

	home := t.TempDir()
	projectID := "01946059-e68b-7cb8-8c1d-6b586e3f4ea2"
	project := &planv1.Project{Id: projectID}

	if err := SaveDeveloperPushStrategy(home, projectID, azimaStrat); err != nil {
		t.Fatalf("SaveDeveloperPushStrategy: %v", err)
	}
	s, err := LoadSettings(home, project)
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if s.PushStrategy != azimaStrat || s.PushStrategyFrom != dev {
		t.Errorf("saved developer push strategy: %v from %v; want azima from developer", s.PushStrategy, s.PushStrategyFrom)
	}

	if err := SaveDeveloperPushStrategy("", projectID, azimaStrat); err == nil {
		t.Error("SaveDeveloperPushStrategy without home: want error")
	}
	if err := SaveDeveloperPushStrategy(home, "", azimaStrat); err == nil {
		t.Error("SaveDeveloperPushStrategy without project ID: want error")
	}

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

// TestInstallQuestionsSettings: by default Djinn asks to install builds; either file changes it, the developer's
// winning, and SaveDeveloperInstallQuestions writes the developer's choice.
func TestInstallQuestionsSettings(t *testing.T) {
	const (
		def  = planv1.SettingSource_SETTING_SOURCE_DEFAULT
		repo = planv1.SettingSource_SETTING_SOURCE_REPOSITORY
		dev  = planv1.SettingSource_SETTING_SOURCE_DEVELOPER
	)
	always := planv1.InstallQuestions_INSTALL_QUESTIONS_ALWAYS
	whenNeeded := planv1.InstallQuestions_INSTALL_QUESTIONS_WHEN_NEEDED
	never := planv1.InstallQuestions_INSTALL_QUESTIONS_NEVER

	for _, c := range []struct {
		name          string
		repo, dev     *planv1.ProjectSettings
		wantQuestions planv1.InstallQuestions
		wantFrom      planv1.SettingSource
		wantWord      string
	}{
		{"neither file", nil, nil, always, def, "always"},
		{"repo sets when_needed", &planv1.ProjectSettings{InstallQuestions: &whenNeeded}, nil, whenNeeded, repo, "when_needed"},
		{"repo sets never", &planv1.ProjectSettings{InstallQuestions: &never}, nil, never, repo, "never"},
		{"dev overrides to always", &planv1.ProjectSettings{InstallQuestions: &whenNeeded}, &planv1.ProjectSettings{InstallQuestions: &always}, always, dev, "always"},
		{"dev overrides to never", &planv1.ProjectSettings{InstallQuestions: &whenNeeded}, &planv1.ProjectSettings{InstallQuestions: &never}, never, dev, "never"},
	} {
		s := ResolveSettings(c.repo, c.dev)
		if s.InstallQuestions != c.wantQuestions || s.InstallQuestionsFrom != c.wantFrom {
			t.Errorf("%s: install_questions=%v from=%v; want %v from=%v", c.name, s.InstallQuestions, s.InstallQuestionsFrom, c.wantQuestions, c.wantFrom)
		}
		if got := InstallQuestionsWord(s.InstallQuestions); got != c.wantWord {
			t.Errorf("%s: word=%s; want %s", c.name, got, c.wantWord)
		}
	}

	// Reading normalized settings from file (lowercase values)
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.txtpb")
	for _, tc := range []struct {
		content string
		want    planv1.InstallQuestions
	}{
		{"install_questions: when_needed\n", whenNeeded},
		{"install_questions: never\n", never},
		{"install_questions: always\n", always},
		{"install_questions: INSTALL_QUESTIONS_WHEN_NEEDED\n", whenNeeded},
	} {
		if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
			t.Fatal(err)
		}
		ps, err := ReadSettings(path)
		if err != nil {
			t.Fatalf("ReadSettings(%q): %v", tc.content, err)
		}
		if ps.GetInstallQuestions() != tc.want {
			t.Errorf("ReadSettings(%q) = %v, want %v", tc.content, ps.GetInstallQuestions(), tc.want)
		}
	}

	home := t.TempDir()
	projectID := "01946059-e68b-7cb8-8c1d-6b586e3f4ea2"
	project := &planv1.Project{Id: projectID}

	if err := SaveDeveloperInstallQuestions(home, projectID, whenNeeded); err != nil {
		t.Fatalf("SaveDeveloperInstallQuestions: %v", err)
	}
	s, err := LoadSettings(home, project)
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if s.InstallQuestions != whenNeeded || s.InstallQuestionsFrom != dev {
		t.Errorf("saved developer install_questions: %v from %v; want when_needed from developer", s.InstallQuestions, s.InstallQuestionsFrom)
	}

	if err := SaveDeveloperInstallQuestions("", projectID, whenNeeded); err == nil {
		t.Error("SaveDeveloperInstallQuestions without home: want error")
	}
	if err := SaveDeveloperInstallQuestions(home, "", whenNeeded); err == nil {
		t.Error("SaveDeveloperInstallQuestions without project ID: want error")
	}
}

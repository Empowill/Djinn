package plan

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
	"github.com/empowill/djinn/internal/fsx"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/store"
)

// StaleSettingsFile is where a project's repository kept the settings its team shares, in text protobuf, before they
// moved under settings: in its djinn.yaml (TeamSettingsFile, Q55): Djinn no longer reads it, and says so when it finds
// one where it reads the project's configuration.
var StaleSettingsFile = filepath.Join(".agents", "settings.txtpb")

// staleSettingsWarned holds the folders whose stale settings file is logged: once each, not on every read.
var staleSettingsWarned sync.Map

// TeamSettingsFile is the file of the project laid out in dir that holds the settings its team shares, under
// settings: (plan.v1.ProjectSettings), beside the rest of Djinn's configuration (ProjectConfigFiles): the first of
// them dir holds, else .agents/djinn.yaml, where a project writes it.
func TeamSettingsFile(dir string) string {
	for _, f := range ProjectConfigFiles {
		if exists(filepath.Join(dir, f)) {
			return filepath.Join(dir, f)
		}
	}
	return filepath.Join(dir, ProjectConfigFiles[1])
}

// DeveloperSettingsFile is where the developer keeps their own settings of a project, in Djinn's data folder home.
func DeveloperSettingsFile(home, projectID string) string {
	return filepath.Join(home, "projects", projectID, "settings.txtpb")
}

// ReadSettings reads the settings of the file at path: nil, and no error, when there is none. A file that holds
// something that looks like a secret is refused, comments included, without repeating it.
func ReadSettings(path string) (*planv1.ProjectSettings, error) {
	s, _, err := ReadSettingsWithWarnings(path)
	return s, err
}

// ReadSettingsWithWarnings reads the settings of the file at path: nil, and no error, when there is none. A file that holds
// something that looks like a secret is refused, comments included, without repeating it. Any unknown fields are discarded,
// logged once as warnings, and returned in warnings.
func ReadSettingsWithWarnings(path string) (*planv1.ProjectSettings, []string, error) {
	return readSettingsNamed(path, path)
}

// readSettingsNamed is ReadSettingsWithWarnings, its messages naming the file name: where Djinn read it from, a
// reference branch's tree.
func readSettingsNamed(path, name string) (*planv1.ProjectSettings, []string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if line, why := secretIn(b); line > 0 {
		return nil, nil, fmt.Errorf("%s: line %d %s; this file holds no secret: remove it", name, line, why)
	}
	if line, key := removedKeyIn(b); line > 0 {
		return nil, nil, fmt.Errorf("%s: line %d names %s: this key was removed; delete the line", name, line, key)
	}
	s := &planv1.ProjectSettings{}
	normalized := normalizeInstallQuestions.ReplaceAllFunc(b, func(match []byte) []byte {
		parts := normalizeInstallQuestions.FindSubmatch(match)
		if len(parts) == 3 {
			prefix := parts[1]
			val := strings.ToUpper(string(parts[2]))
			return append(prefix, []byte("INSTALL_QUESTIONS_"+val)...)
		}
		return match
	})
	unknowns := FindUnknownFields(normalized, s)
	var warnings []string
	for _, u := range unknowns {
		warnings = append(warnings, WarnUnknownField(name, u))
	}
	opts := prototext.UnmarshalOptions{DiscardUnknown: true}
	if err := opts.Unmarshal(normalized, s); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", name, err)
	}
	if err := protovalidate.Validate(s); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", name, err)
	}
	if err := uniqueCheckNames(s); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", name, err)
	}
	return s, warnings, nil
}

// uniqueCheckNames refuses settings s with two checks of one name: names ignore case.
func uniqueCheckNames(s *planv1.ProjectSettings) error {
	seen := map[string]bool{}
	for _, c := range s.GetChecks() {
		lower := strings.ToLower(c.GetName())
		if seen[lower] {
			return fmt.Errorf("two checks are named %s: names ignore case", c.GetName())
		}
		seen[lower] = true
	}
	return nil
}

var (
	// secretField is a field name that says it holds a secret.
	secretField = regexp.MustCompile(`(?i)^\s*([a-z0-9_]*(key|token|secret|password|passwd|credential|auth)[a-z0-9_]*)\s*[:{]`)
	// secretPrefixes start the keys of well-known services.
	secretPrefixes = []string{
		"sk-", "sk_live_", "rk_live_", "ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_", "glpat-", "xoxb-", "xoxp-",
		"xoxa-", "AKIA", "ASIA", "AIza", "-----BEGIN",
	}
	// word is what a key is made of, separators included; run is a stretch without separators.
	word = regexp.MustCompile(`[A-Za-z0-9_-]+`)
	run  = regexp.MustCompile(`[A-Za-z0-9]{32,}`)

	// normalizeInstallQuestions matches lowercase enum values for install_questions so prototext can parse them.
	normalizeInstallQuestions = regexp.MustCompile(`(?m)^(\s*install_questions\s*:\s*)["']?(never|when_needed|always)["']?`)
	// removedSettingsField matches setting keys that have been removed from ProjectSettings.
	removedSettingsField = regexp.MustCompile(`(?i)^\s*(answer_workers|question_workers)\b`)
)

// removedKeyIn returns the first line of b that names a removed key, and the key; 0 when none does.
func removedKeyIn(b []byte) (int, string) {
	lines := bufio.NewScanner(bytes.NewReader(b))
	for n := 1; lines.Scan(); n++ {
		line := lines.Text()
		if idx := strings.IndexByte(line, '#'); idx >= 0 {
			line = line[:idx]
		}
		if m := removedSettingsField.FindStringSubmatch(line); m != nil {
			return n, strings.ToLower(m[1])
		}
	}
	return 0, ""
}

// secretIn returns the first line of b that looks like it holds a secret, and why; 0 when none does: a field name
// that says so, a word that starts like a well-known key, or a long run of letters and digits.
func secretIn(b []byte) (int, string) {
	lines := bufio.NewScanner(bytes.NewReader(b))
	for n := 1; lines.Scan(); n++ {
		line := lines.Text()
		if m := secretField.FindStringSubmatch(line); m != nil {
			return n, fmt.Sprintf("names a secret (%s)", m[1])
		}
		for _, w := range word.FindAllString(line, -1) {
			for _, p := range secretPrefixes {
				if len(w) >= 20 && strings.HasPrefix(w, p) {
					return n, "looks like a key"
				}
			}
		}
		for _, r := range run.FindAllString(line, -1) {
			if strings.ContainsAny(r, "0123456789") && strings.ContainsFunc(r, func(c rune) bool { return c > '9' }) {
				return n, "looks like a key"
			}
		}
	}
	return 0, ""
}

// DefaultBranch is the branch template of a project whose files set none.
const DefaultBranch = "{code}-{slug}-{uuid8}"

// DefaultCorrectionAttempts is how many correction workers Djinn starts for a failed integration, one after the other,
// before it asks the person, in a project whose files set none.
const DefaultCorrectionAttempts = 2

// Defaults of keeping a wish's integration branch up with the project's main branch: merged at each release, main
// fetched at most hourly, and a release of Djinn installed by itself on main.
const (
	DefaultMergeMain        = planv1.MergeMain_MERGE_MAIN_RELEASE
	DefaultMergeMainEvery   = time.Hour
	DefaultInstallQuestions = planv1.InstallQuestions_INSTALL_QUESTIONS_ALWAYS
)

// Settings are a project's settings as its workers get them, each with where it comes from.
type Settings struct {
	Provider     planv1.Provider
	Model        string
	MaxBudgetUSD float64
	Branch       string // a template: {code}, {slug}, {uuid8}
	// How Djinn integrates the project's finished work (T07): the generated files as globs, the command that makes
	// them, the one that makes a fresh worktree ready, and the one that installs its new build; empty when not set.
	Generated                []string
	Generate, Setup, Install string
	TestOne                  string
	// The checks Djinn runs before it merges a task's work and before it pushes, in the order the file names them.
	Checks []*planv1.ProjectCheck
	// How many correction workers Djinn starts for a batch that conflicts in code or tests red, before it asks.
	CorrectionAttempts int

	// EnlightenWorkers says whether Djinn starts an investigator after a request to investigate ("Enlighten me").
	EnlightenWorkers  bool
	QuestionProvider  planv1.Provider
	QuestionModel     string
	QuestionBudgetUSD float64

	// How Djinn keeps each wish's integration branch up with the project's main branch: the branch ("" for the
	// remote's default), when it merges it, how often it fetches it at most; and whether a release of Djinn installs by
	// itself while the person's checkout is on main.
	MainBranch       string
	MergeMain        planv1.MergeMain
	MergeMainEvery   time.Duration
	InstallReleases  bool
	Push             planv1.ProjectPush
	PushStrategy     planv1.PushStrategy
	InstallQuestions planv1.InstallQuestions
	// Babysit says whether Djinn babysits the pull or merge request a task opens, with the project's babysit skill.
	Babysit bool
	// WatchCommands are the commands, or their starts, the developer lets a watcher run in the project besides those
	// its .agents/permissions.txtpb lists: from their own file only.
	WatchCommands []string
	// ServeCommands are the commands, or their starts, the developer lets an ifrit's serve run in the project besides
	// those its .agents/permissions.txtpb lists: from their own file only.
	ServeCommands []string
	// GatesOff are the gates the project declares in its djinn.yaml that the developer turns off on their machine:
	// from their own file only.
	GatesOff []string
	// InstallBuilds says whether a build of the running Djinn's line, once pushed, installs by itself (W554).
	InstallBuilds bool
	Warnings      []string

	ProviderFrom, ModelFrom, BudgetFrom, BranchFrom, GeneratedFrom, GenerateFrom, SetupFrom            planv1.SettingSource
	ChecksFrom, AttemptsFrom, InstallFrom, TestOneFrom                                                 planv1.SettingSource
	EnlightenWorkersFrom                                                                               planv1.SettingSource
	QuestionProviderFrom, QuestionModelFrom, QuestionBudgetFrom                                        planv1.SettingSource
	MainBranchFrom, MergeMainFrom, MergeMainEveryFrom, InstallReleasesFrom, PushFrom, PushStrategyFrom planv1.SettingSource
	InstallQuestionsFrom, BabysitFrom, WatchCommandsFrom, InstallBuildsFrom                            planv1.SettingSource
	ServeCommandsFrom, GatesOffFrom                                                                    planv1.SettingSource
}

// CanReadOnly tells whether a provider can run a question worker (read-only).
func CanReadOnly(p planv1.Provider) bool {
	switch p {
	case planv1.Provider_PROVIDER_CLAUDE, planv1.Provider_PROVIDER_CODEX, planv1.Provider_PROVIDER_FAKE:
		return true
	default:
		return false
	}
}

// CanIfrit tells whether a provider can run an ifrit (keeps its input open between turns).
func CanIfrit(p planv1.Provider) bool {
	switch p {
	case planv1.Provider_PROVIDER_CLAUDE, planv1.Provider_PROVIDER_ANTIGRAVITY, planv1.Provider_PROVIDER_FAKE:
		return true
	default:
		return false
	}
}

// CanResume reports whether a provider can resume an existing session.
func CanResume(p planv1.Provider) bool {
	switch p {
	case planv1.Provider_PROVIDER_CLAUDE,
		planv1.Provider_PROVIDER_CODEX,
		planv1.Provider_PROVIDER_ANTIGRAVITY,
		planv1.Provider_PROVIDER_FAKE,
		planv1.Provider_PROVIDER_UNSPECIFIED:
		return true
	default:
		return false
	}
}

// Defaults of the question workers: a cheaper model is enough to read and revise a question; and a cap, so that one
// never runs long.
const (
	DefaultQuestionModel     = "sonnet" // for claude; another provider's own default
	DefaultQuestionBudgetUSD = 2.0
)

// ResolveSettings merges the repository's settings and the developer's, either nil when there is none. The
// developer's win, setting by setting; a file that sets the provider sets the model with it, so that a model never
// goes to a provider it is not one of.
func ResolveSettings(repo, dev *planv1.ProjectSettings) Settings {
	def := planv1.SettingSource_SETTING_SOURCE_DEFAULT
	s := Settings{
		Provider: planv1.Provider_PROVIDER_CLAUDE, Branch: DefaultBranch, CorrectionAttempts: DefaultCorrectionAttempts,
		ProviderFrom: def, ModelFrom: def, BudgetFrom: def, BranchFrom: def, GeneratedFrom: def, GenerateFrom: def,
		SetupFrom: def, ChecksFrom: def, InstallFrom: def, TestOneFrom: def, AttemptsFrom: def,
		EnlightenWorkers: true, EnlightenWorkersFrom: def,
		QuestionProviderFrom: def, QuestionBudgetUSD: DefaultQuestionBudgetUSD,
		QuestionModelFrom: def, QuestionBudgetFrom: def,
		MergeMain: DefaultMergeMain, MergeMainEvery: DefaultMergeMainEvery, InstallReleases: true,
		Push:             planv1.ProjectPush_PROJECT_PUSH_STANDARD,
		PushStrategy:     planv1.PushStrategy_PUSH_STRATEGY_WISH,
		InstallQuestions: DefaultInstallQuestions,
		MainBranchFrom:   def, MergeMainFrom: def, MergeMainEveryFrom: def, InstallReleasesFrom: def, PushFrom: def, PushStrategyFrom: def,
		InstallQuestionsFrom: def, Babysit: true, BabysitFrom: def, WatchCommandsFrom: def, ServeCommandsFrom: def, GatesOffFrom: def, InstallBuilds: true, InstallBuildsFrom: def,
	}
	questionModel := false // a file set it
	for _, f := range []struct {
		settings *planv1.ProjectSettings
		from     planv1.SettingSource
	}{{repo, planv1.SettingSource_SETTING_SOURCE_REPOSITORY}, {dev, planv1.SettingSource_SETTING_SOURCE_DEVELOPER}} {
		if f.settings == nil {
			continue
		}
		if f.settings.Provider != nil {
			s.Provider, s.ProviderFrom = f.settings.GetProvider(), f.from
			s.Model, s.ModelFrom = "", f.from
			s.QuestionModel, s.QuestionModelFrom, questionModel = "", def, false
		}
		if f.settings.Model != nil {
			s.Model, s.ModelFrom = f.settings.GetModel(), f.from
		}
		if f.settings.MaxBudgetUsd != nil {
			s.MaxBudgetUSD, s.BudgetFrom = f.settings.GetMaxBudgetUsd(), f.from
		}
		if f.settings.Branch != nil {
			s.Branch, s.BranchFrom = f.settings.GetBranch(), f.from
		}
		if len(f.settings.GetGenerated()) > 0 {
			s.Generated, s.GeneratedFrom = f.settings.GetGenerated(), f.from
		}
		if f.settings.Generate != nil {
			s.Generate, s.GenerateFrom = f.settings.GetGenerate(), f.from
		}
		if f.settings.Setup != nil {
			s.Setup, s.SetupFrom = f.settings.GetSetup(), f.from
		}
		if checks := fileChecks(f.settings); len(checks) > 0 {
			s.Checks, s.ChecksFrom = checks, f.from
		}
		if f.settings.CorrectionAttempts != nil {
			s.CorrectionAttempts, s.AttemptsFrom = int(f.settings.GetCorrectionAttempts()), f.from
		}
		if f.settings.Install != nil {
			s.Install, s.InstallFrom = f.settings.GetInstall(), f.from
		}
		if f.settings.EnlightenWorkers != nil {
			s.EnlightenWorkers, s.EnlightenWorkersFrom = f.settings.GetEnlightenWorkers(), f.from
		}
		if f.settings.QuestionProvider != nil {
			s.QuestionProvider, s.QuestionProviderFrom = f.settings.GetQuestionProvider(), f.from
			s.QuestionModel, s.QuestionModelFrom, questionModel = "", def, false
		}
		if f.settings.QuestionModel != nil {
			s.QuestionModel, s.QuestionModelFrom, questionModel = f.settings.GetQuestionModel(), f.from, true
		}
		if f.settings.QuestionBudgetUsd != nil {
			s.QuestionBudgetUSD, s.QuestionBudgetFrom = f.settings.GetQuestionBudgetUsd(), f.from
		}
		if f.settings.MainBranch != nil {
			s.MainBranch, s.MainBranchFrom = f.settings.GetMainBranch(), f.from
		}
		if f.settings.MergeMain != nil {
			s.MergeMain, s.MergeMainFrom = f.settings.GetMergeMain(), f.from
		}
		if f.settings.MergeMainMinutes != nil {
			s.MergeMainEvery, s.MergeMainEveryFrom = time.Duration(f.settings.GetMergeMainMinutes())*time.Minute, f.from
		}
		if f.settings.InstallReleases != nil {
			s.InstallReleases, s.InstallReleasesFrom = f.settings.GetInstallReleases(), f.from
		}
		if f.settings.InstallBuilds != nil {
			s.InstallBuilds, s.InstallBuildsFrom = f.settings.GetInstallBuilds(), f.from
		}
		if f.settings.Push != nil {
			s.Push, s.PushFrom = f.settings.GetPush(), f.from
		}
		if f.settings.PushStrategy != nil {
			s.PushStrategy, s.PushStrategyFrom = f.settings.GetPushStrategy(), f.from
		}
		if f.settings.TestOne != nil {
			s.TestOne, s.TestOneFrom = f.settings.GetTestOne(), f.from
		}
		if f.settings.InstallQuestions != nil {
			s.InstallQuestions, s.InstallQuestionsFrom = f.settings.GetInstallQuestions(), f.from
		}
		if f.settings.Babysit != nil {
			s.Babysit, s.BabysitFrom = f.settings.GetBabysit(), f.from
		}
		// A repository never allows its own watchers: it lists its commands in .agents/permissions.txtpb.
		if len(f.settings.GetWatchCommands()) > 0 && f.from == planv1.SettingSource_SETTING_SOURCE_DEVELOPER {
			s.WatchCommands, s.WatchCommandsFrom = f.settings.GetWatchCommands(), f.from
		}
		if len(f.settings.GetServeCommands()) > 0 && f.from == planv1.SettingSource_SETTING_SOURCE_DEVELOPER {
			s.ServeCommands, s.ServeCommandsFrom = f.settings.GetServeCommands(), f.from
		}
		// A repository declares its gates in its djinn.yaml: it never turns them off.
		if len(f.settings.GetGatesOff()) > 0 && f.from == planv1.SettingSource_SETTING_SOURCE_DEVELOPER {
			s.GatesOff, s.GatesOffFrom = f.settings.GetGatesOff(), f.from
		}
	}
	effective := s.QuestionProvider
	if effective == planv1.Provider_PROVIDER_UNSPECIFIED {
		if CanReadOnly(s.Provider) {
			effective = s.Provider
		} else {
			effective = planv1.Provider_PROVIDER_CLAUDE
		}
	}
	if !questionModel {
		if effective == s.Provider && s.Model != "" {
			s.QuestionModel, s.QuestionModelFrom = s.Model, s.ModelFrom
		} else if effective == planv1.Provider_PROVIDER_CLAUDE {
			s.QuestionModel = DefaultQuestionModel
		}
	}
	return s
}

// settingsFiles are the paths of project's files: the repository's, empty when the project has no folder here,
// and the developer's in home, empty without home.
func settingsFiles(home string, project *planv1.Project) (repo, dev string) {
	if project.GetDirectory() != "" {
		repo = TeamSettingsFile(project.GetDirectory())
	}
	if home != "" && project.GetId() != "" {
		dev = DeveloperSettingsFile(home, project.GetId())
	}
	return repo, dev
}

// LoadSettings reads the settings of project (nil outside any project): its repository's, then the developer's in
// Djinn's data folder home. A file that cannot be read is an error that names it.
func LoadSettings(home string, project *planv1.Project) (Settings, error) {
	repo, dev, warnings, problems := loadSettings(home, project)
	s := ResolveSettings(repo, dev)
	s.Warnings = warnings
	return s, errors.Join(problems...)
}

// loadSettings reads both files of project, and why each one that could not be read was not.
func loadSettings(home string, project *planv1.Project) (repo, dev *planv1.ProjectSettings, warnings []string, problems []error) {
	return loadSettingsFiles(readSettingsFiles(home, project))
}

func loadSettingsFiles(files []settingsRead) (repo, dev *planv1.ProjectSettings, warnings []string, problems []error) {
	for _, f := range files {
		if f.err != nil {
			problems = append(problems, f.err)
			continue
		}
		if f.team {
			repo = f.settings
		} else {
			dev = f.settings
		}
		for _, warn := range f.warnings {
			if !slices.Contains(warnings, warn.text) {
				warnings = append(warnings, warn.text)
			}
		}
	}
	return repo, dev, warnings, problems
}

// settingsRead is one settings file of a project as Djinn read it.
type settingsRead struct {
	// path is the file; team tells the repository's from the developer's.
	path string
	team bool
	// file is the team's file, relative to the project, with slashes: .agents/djinn.yaml or djinn.yaml.
	file string
	// settings are what it holds; nil without the file, or when it cannot be read: err says why.
	settings *planv1.ProjectSettings
	err      error
	// warnings are what Djinn ignored in it.
	warnings []settingsWarning
}

// settingsWarning is a thing a settings file holds that Djinn ignores; ignored says a line to remove, not a field a
// newer Djinn knows; stale says the team's settings file Djinn no longer reads (StaleSettingsFile); elsewhere says a
// field of djinn.yaml, which the notices of djinn.yaml already say (projectConfigNotices); file is the stale file, in
// the project's folder.
type settingsWarning struct {
	text      string
	ignored   bool
	stale     bool
	elsewhere bool
	file      string
}

// readTeamSettings reads the settings project's team shares, where Djinn reads its configuration (config): the
// settings: section of its djinn.yaml (ReadProjectConfig). A djinn.yaml Djinn cannot read is an error that names it,
// with the reference branch it was read on; a field of the section it does not know is a warning. A settings.txtpb
// left in .agents is not read: a warning says where its settings go, and the log says it once.
func readTeamSettings(project *planv1.Project, config Config) settingsRead {
	f := settingsRead{team: true}
	read, err := ReadProjectConfig(config.Dir)
	f.file = cmp.Or(read.File, filepath.ToSlash(ProjectConfigFiles[1]))
	f.path = filepath.Join(project.GetDirectory(), filepath.FromSlash(f.file))
	name := config.Named(filepath.Join(config.Dir, filepath.FromSlash(f.file)))
	if err != nil {
		if config.FromReference() {
			err = fmt.Errorf("%s:%w", config.Ref, err) // the error names the file: origin/main:.agents/djinn.yaml: ...
		}
		f.err = err
	} else {
		f.settings = read.Settings
		for _, u := range read.Unknown {
			if field, ok := strings.CutPrefix(u, settingsSection+"."); ok {
				f.warnings = append(f.warnings, settingsWarning{text: WarnUnknownField(name, field), elsewhere: true})
			}
		}
	}
	if stale := filepath.Join(config.Dir, StaleSettingsFile); exists(stale) {
		staleName := config.Named(stale)
		if _, seen := staleSettingsWarned.LoadOrStore(stale, true); !seen {
			logx.L().Warn("stale team settings file: Djinn no longer reads it, its settings go under settings: in "+
				filepath.ToSlash(ProjectConfigFiles[1]), "file", staleName)
		}
		f.warnings = append(f.warnings, settingsWarning{
			stale: true, file: filepath.Join(project.GetDirectory(), StaleSettingsFile),
			text: staleName + ": Djinn no longer reads this file: the team's settings go under settings: in " + f.file,
		})
	}
	return f
}

// readSettingsFiles reads the files of project: the repository's, then the developer's; a file project has no path
// for is left out.
func readSettingsFiles(home string, project *planv1.Project) []settingsRead {
	if project == nil {
		return nil
	}
	return readSettingsFilesConfig(home, project, ProjectConfig(home, project))
}

func readSettingsFilesConfig(home string, project *planv1.Project, config Config) []settingsRead {
	if project == nil {
		return nil
	}
	repoFile, devFile := settingsFiles(home, project)
	var out []settingsRead
	for _, f := range []settingsRead{{path: repoFile, team: true}, {path: devFile}} {
		if f.path == "" {
			continue
		}
		if f.team {
			f = readTeamSettings(project, config)
		} else {
			var w []string
			f.settings, w, f.err = readSettingsNamed(f.path, f.path)
			for _, text := range w {
				f.warnings = append(f.warnings, settingsWarning{text: text})
			}
		}
		name := f.path
		if f.team {
			name = config.Named(filepath.Join(config.Dir, filepath.FromSlash(f.file)))
		}
		if f.err == nil && f.team && config.TeamBranch != "" && f.settings.GetMainBranch() == "" {
			// The default branch's tree named the reference branch, whose own tree does not: it stays the reference.
			if f.settings == nil {
				f.settings = &planv1.ProjectSettings{}
			}
			f.settings = proto.CloneOf(f.settings)
			f.settings.MainBranch = &config.TeamBranch
		}
		if f.err == nil && f.team {
			if len(f.settings.GetWatchCommands()) > 0 {
				f.warnings = append(f.warnings, settingsWarning{ignored: true, text: name + ": watch_commands is " +
					"ignored: a developer allows a watcher command in their own file (djinn project allow --watch), the " +
					"repository lists its commands in .agents/permissions.txtpb"})
			}
			if len(f.settings.GetServeCommands()) > 0 {
				f.warnings = append(f.warnings, settingsWarning{ignored: true, text: name + ": serve_commands is " +
					"ignored: a developer allows a serve command in their own file (djinn project allow --serve), the " +
					"repository lists its commands in .agents/permissions.txtpb"})
			}
			if len(f.settings.GetGatesOff()) > 0 {
				f.warnings = append(f.warnings, settingsWarning{ignored: true, text: name + ": gates_off is " +
					"ignored: a developer turns a gate off in their own file (djinn project gates --off), the " +
					"repository declares its gates in its djinn.yaml"})
			}
		}
		out = append(out, f)
	}
	return out
}

// Rows are the settings as djinn project show lists them.
func (s Settings) Rows() []*planv1.ProjectSetting {
	usd := func(v float64) string {
		if v > 0 {
			return strconv.FormatFloat(v, 'f', -1, 64)
		}
		return ""
	}
	return []*planv1.ProjectSetting{
		{Name: "provider", Value: providerName(s.Provider), Source: s.ProviderFrom},
		{Name: "model", Value: s.Model, Source: s.ModelFrom},
		{Name: "max_budget_usd", Value: usd(s.MaxBudgetUSD), Source: s.BudgetFrom},
		{Name: "branch", Value: s.Branch, Source: s.BranchFrom},
		{Name: "generated", Value: strings.Join(s.Generated, ","), Source: s.GeneratedFrom},
		{Name: "generate", Value: s.Generate, Source: s.GenerateFrom},
		{Name: "setup", Value: s.Setup, Source: s.SetupFrom},
		{Name: "checks", Value: ChecksText(s.Checks), Source: s.ChecksFrom},
		{Name: "test_one", Value: s.TestOne, Source: s.TestOneFrom},
		{Name: "correction_attempts", Value: strconv.Itoa(s.CorrectionAttempts), Source: s.AttemptsFrom},
		{Name: "install", Value: s.Install, Source: s.InstallFrom},
		{Name: "install_questions", Value: InstallQuestionsWord(s.InstallQuestions), Source: s.InstallQuestionsFrom},
		{Name: "install_builds", Value: strconv.FormatBool(s.InstallBuilds), Source: s.InstallBuildsFrom},
		{Name: "enlighten_workers", Value: strconv.FormatBool(s.EnlightenWorkers), Source: s.EnlightenWorkersFrom},
		{Name: "question_provider", Value: questionProviderName(s.QuestionProvider), Source: s.QuestionProviderFrom},
		{Name: "question_model", Value: s.QuestionModel, Source: s.QuestionModelFrom},
		{Name: "question_budget_usd", Value: usd(s.QuestionBudgetUSD), Source: s.QuestionBudgetFrom},
		{Name: "main_branch", Value: s.MainBranch, Source: s.MainBranchFrom},
		{Name: "merge_main", Value: MergeMainWord(s.MergeMain), Source: s.MergeMainFrom},
		{Name: "merge_main_minutes", Value: strconv.Itoa(int(s.MergeMainEvery / time.Minute)), Source: s.MergeMainEveryFrom},
		{Name: "install_releases", Value: strconv.FormatBool(s.InstallReleases), Source: s.InstallReleasesFrom},
		{Name: "push", Value: ProjectPushWord(s.Push), Source: s.PushFrom},
		{Name: "push_strategy", Value: PushStrategyWord(s.PushStrategy), Source: s.PushStrategyFrom},
		{Name: "babysit", Value: strconv.FormatBool(s.Babysit), Source: s.BabysitFrom},
		{Name: "watch_commands", Value: strings.Join(s.WatchCommands, "; "), Source: s.WatchCommandsFrom},
		{Name: "serve_commands", Value: strings.Join(s.ServeCommands, "; "), Source: s.ServeCommandsFrom},
		{Name: "gates_off", Value: strings.Join(s.GatesOff, ", "), Source: s.GatesOffFrom},
	}
}

// SettingRows are the settings Djinn retains from the repository's file repo and the developer's dev, either nil when
// there is none, each with its value in each file alone: unset where that file does not set it.
func SettingRows(repo, dev *planv1.ProjectSettings) []*planv1.ProjectSetting {
	rows := ResolveSettings(repo, dev).Rows()
	byName := make(map[string]*planv1.ProjectSetting, len(rows))
	for _, r := range rows {
		byName[r.GetName()] = r
	}
	if repo != nil {
		for _, r := range ResolveSettings(repo, nil).Rows() {
			if r.GetSource() == planv1.SettingSource_SETTING_SOURCE_REPOSITORY {
				byName[r.GetName()].Repository = proto.String(r.GetValue())
			}
		}
	}
	if dev != nil {
		for _, r := range ResolveSettings(nil, dev).Rows() {
			if r.GetSource() == planv1.SettingSource_SETTING_SOURCE_DEVELOPER {
				byName[r.GetName()].Developer = proto.String(r.GetValue())
			}
		}
	}
	return rows
}

// questionProviderName is the name of the question provider, as the settings write it: "" when not set.
func questionProviderName(p planv1.Provider) string {
	if p == planv1.Provider_PROVIDER_UNSPECIFIED {
		return ""
	}
	return strings.ToLower(strings.TrimPrefix(p.String(), "PROVIDER_"))
}

// fileChecks are the checks a file sets: its checks, and its former test command as the check test at commit, unless
// a check has that name.
func fileChecks(f *planv1.ProjectSettings) []*planv1.ProjectCheck {
	checks := f.GetChecks()
	if f.Test == nil || f.GetTest() == "" ||
		slices.ContainsFunc(checks, func(c *planv1.ProjectCheck) bool { return strings.EqualFold(c.GetName(), "test") }) {
		return checks
	}
	return append(slices.Clone(checks), &planv1.ProjectCheck{
		Name: "test", Command: f.GetTest(), When: []planv1.CheckWhen{planv1.CheckWhen_CHECK_WHEN_COMMIT},
	})
}

// Integrates tells whether Djinn integrates the project's finished work by itself: once a check is set.
func (s Settings) Integrates() bool { return len(s.Checks) > 0 }

// ChecksAt are the checks Djinn runs at when: before a commit, or before a push.
func (s Settings) ChecksAt(when planv1.CheckWhen) []*planv1.ProjectCheck {
	var out []*planv1.ProjectCheck
	for _, c := range s.Checks {
		if slices.Contains(c.GetWhen(), when) {
			out = append(out, c)
		}
	}
	return out
}

// MergeMainWord is when Djinn merges main, as the settings say it: release, commit, off.
func MergeMainWord(m planv1.MergeMain) string {
	return strings.ToLower(strings.TrimPrefix(m.String(), "MERGE_MAIN_"))
}

// ProjectPushWord is when Djinn pushes, as the settings say it: standard, on_demand.
func ProjectPushWord(p planv1.ProjectPush) string {
	switch p {
	case planv1.ProjectPush_PROJECT_PUSH_ON_DEMAND:
		return "on_demand"
	default:
		return "standard"
	}
}

// InstallQuestionsWord is when Djinn asks to install builds, as the settings say it: always, when_needed, never.
func InstallQuestionsWord(q planv1.InstallQuestions) string {
	switch q {
	case planv1.InstallQuestions_INSTALL_QUESTIONS_NEVER:
		return "never"
	case planv1.InstallQuestions_INSTALL_QUESTIONS_WHEN_NEEDED:
		return "when_needed"
	default:
		return "always"
	}
}

// SaveDeveloperInstallQuestions writes the developer's install questions setting into their settings file in home.
func SaveDeveloperInstallQuestions(home, projectID string, questions planv1.InstallQuestions) error {
	_, err := saveDeveloper(home, projectID, func(s *planv1.ProjectSettings) error {
		s.InstallQuestions = questions.Enum()
		return nil
	})
	return err
}

// SaveDeveloperPush writes the developer's push setting into their settings file in home.
func SaveDeveloperPush(home, projectID string, push planv1.ProjectPush) error {
	_, err := saveDeveloper(home, projectID, func(s *planv1.ProjectSettings) error {
		s.Push = push.Enum()
		return nil
	})
	return err
}

// saveDeveloper changes the developer's settings file of the project in home with change, and writes it: the
// settings written.
func saveDeveloper(home, projectID string, change func(*planv1.ProjectSettings) error) (*planv1.ProjectSettings, error) {
	if home == "" || projectID == "" {
		return nil, errors.New("cannot save developer settings: home or project ID is missing")
	}
	path := DeveloperSettingsFile(home, projectID)
	s, err := ReadSettings(path)
	if err != nil {
		return nil, err
	}
	if s == nil {
		s = &planv1.ProjectSettings{}
	}
	if err := change(s); err != nil {
		return nil, err
	}
	if err := protovalidate.Validate(s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	b, err := prototext.MarshalOptions{Multiline: true, Indent: "  "}.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if line, why := secretIn(b); line > 0 {
		// Djinn would refuse the whole file at its next read.
		return nil, fmt.Errorf("%s: what would be written at line %d %s; this file holds no secret", path, line, why)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return s, fsx.WriteAtomic(path, b, 0o600)
}

// AllowWatch adds command to the watcher commands the developer allows in the project, in their settings file in
// home, or takes it back with revoke: the commands allowed then. Its words are joined by single spaces, as a
// watcher's command is compared.
func AllowWatch(home, projectID, command string, revoke bool) ([]string, error) {
	s, err := saveDeveloper(home, projectID, func(s *planv1.ProjectSettings) error {
		return allowIn(&s.WatchCommands, "watcher", command, revoke)
	})
	return s.GetWatchCommands(), err
}

// AllowServe is AllowWatch for the commands an ifrit's serve runs (djinn serve run).
func AllowServe(home, projectID, command string, revoke bool) ([]string, error) {
	s, err := saveDeveloper(home, projectID, func(s *planv1.ProjectSettings) error {
		return allowIn(&s.ServeCommands, "serve", command, revoke)
	})
	return s.GetServeCommands(), err
}

// GatesOff turns the gate name off for the developer in the project, in their settings file in home, or back on
// with on: the gates turned off then.
func GatesOff(home, projectID, name string, on bool) ([]string, error) {
	s, err := saveDeveloper(home, projectID, func(s *planv1.ProjectSettings) error {
		at := slices.Index(s.GatesOff, name)
		switch {
		case on && at < 0:
			return fmt.Errorf("you did not turn the gate %s off", name)
		case on:
			s.GatesOff = slices.Delete(s.GatesOff, at, at+1)
		case at < 0:
			s.GatesOff = append(s.GatesOff, name)
		}
		return nil
	})
	return s.GetGatesOff(), err
}

// allowIn adds command, its words joined by single spaces, to the commands of what (watcher, serve), or takes it
// back with revoke.
func allowIn(commands *[]string, what, command string, revoke bool) error {
	command = strings.Join(strings.Fields(command), " ")
	at := slices.Index(*commands, command)
	switch {
	case command == "":
		return fmt.Errorf("a %s command has words", what)
	case revoke && at < 0:
		return fmt.Errorf("%q is not a %s command you allow", command, what)
	case revoke:
		*commands = slices.Delete(*commands, at, at+1)
	case at < 0:
		*commands = append(*commands, command)
	}
	return nil
}

// PushStrategyWord is how a wish integrates its work, as the settings say it: wish, azima.
func PushStrategyWord(p planv1.PushStrategy) string {
	switch p {
	case planv1.PushStrategy_PUSH_STRATEGY_AZIMA:
		return "azima"
	default:
		return "wish"
	}
}

// SaveDeveloperPushStrategy writes the developer's push strategy setting into their settings file in home.
func SaveDeveloperPushStrategy(home, projectID string, strategy planv1.PushStrategy) error {
	_, err := saveDeveloper(home, projectID, func(s *planv1.ProjectSettings) error {
		s.PushStrategy = strategy.Enum()
		return nil
	})
	return err
}

// SaveDeveloperMainBranch writes branch as the main_branch setting of the developer's settings file in home, or
// removes it when branch is empty: the team's file names the reference branch again, else Djinn finds it.
func SaveDeveloperMainBranch(home, projectID, branch string) error {
	_, err := saveDeveloper(home, projectID, func(s *planv1.ProjectSettings) error {
		s.MainBranch = nil
		if branch != "" {
			s.MainBranch = &branch
		}
		return nil
	})
	return err
}

// ValidWorkerProvider reports whether p is a provider a project's workers may run by default.
func ValidWorkerProvider(p planv1.Provider) bool {
	switch p {
	case planv1.Provider_PROVIDER_CLAUDE, planv1.Provider_PROVIDER_CODEX, planv1.Provider_PROVIDER_ANTIGRAVITY:
		return true
	default:
		return false
	}
}

// IsClaudeModel reports whether m is a model identifier of Claude.
func IsClaudeModel(m string) bool {
	m = strings.ToLower(strings.TrimSpace(m))
	return m == "claude" || strings.HasPrefix(m, "claude-") ||
		strings.HasPrefix(m, "claude/") ||
		strings.HasPrefix(m, "anthropic/") ||
		strings.Contains(m, ".claude-") ||
		m == "fable" || strings.HasPrefix(m, "fable-") || strings.HasPrefix(m, "fable/") || strings.HasPrefix(m, "fable[") ||
		m == "sonnet" || strings.HasPrefix(m, "sonnet-") || strings.HasPrefix(m, "sonnet/") || strings.HasPrefix(m, "sonnet[") ||
		m == "opus" || strings.HasPrefix(m, "opus-") || strings.HasPrefix(m, "opus/") || strings.HasPrefix(m, "opus[") ||
		m == "haiku" || strings.HasPrefix(m, "haiku-") || strings.HasPrefix(m, "haiku/") || strings.HasPrefix(m, "haiku[")
}

// IsAntigravityModel reports whether m is a model identifier of Antigravity.
func IsAntigravityModel(m string) bool {
	m = strings.ToLower(strings.TrimSpace(m))
	return m == "gemini" || strings.HasPrefix(m, "gemini-") ||
		strings.HasPrefix(m, "gemini/") ||
		strings.HasPrefix(m, "google/") ||
		strings.Contains(m, ".gemini-") ||
		m == "argon" || strings.Contains(m, "argon")
}

// IsCodexModel reports whether m is a model identifier of Codex.
func IsCodexModel(m string) bool {
	m = strings.ToLower(strings.TrimSpace(m))
	return m == "codex" || strings.HasPrefix(m, "codex-") ||
		strings.HasPrefix(m, "gpt-") ||
		strings.HasPrefix(m, "openai/") ||
		strings.Contains(m, ".gpt-") ||
		strings.HasSuffix(m, "-codex") ||
		strings.HasPrefix(m, "code-") ||
		m == "o1" || strings.HasPrefix(m, "o1-") || strings.HasPrefix(m, "o1.") || strings.HasPrefix(m, "o1/") ||
		m == "o3" || strings.HasPrefix(m, "o3-") || strings.HasPrefix(m, "o3.") || strings.HasPrefix(m, "o3/") ||
		m == "o4" || strings.HasPrefix(m, "o4-") || strings.HasPrefix(m, "o4.") || strings.HasPrefix(m, "o4/")
}

// ValidateProviderModel checks that model is empty (the provider's own default) or a well-formed model of provider p.
func ValidateProviderModel(p planv1.Provider, model string) error {
	if !ValidWorkerProvider(p) {
		return fmt.Errorf("provider %s cannot be a project's default: choose claude, codex or antigravity", providerName(p))
	}
	if model == "" {
		return nil
	}
	if strings.TrimSpace(model) == "" {
		return fmt.Errorf("model %q is blank", model)
	}
	if err := protovalidate.Validate(&planv1.ProjectSettings{Model: &model}); err != nil {
		return fmt.Errorf("model %q: %w", model, err)
	}
	switch p {
	case planv1.Provider_PROVIDER_CLAUDE:
		if IsAntigravityModel(model) || IsCodexModel(model) {
			return fmt.Errorf("model %q belongs to another provider, not claude", model)
		}
		if !IsClaudeModel(model) {
			return fmt.Errorf("unknown model %q for provider claude", model)
		}
	case planv1.Provider_PROVIDER_CODEX:
		if IsClaudeModel(model) || IsAntigravityModel(model) {
			return fmt.Errorf("model %q belongs to another provider, not codex", model)
		}
		if !IsCodexModel(model) {
			return fmt.Errorf("unknown model %q for provider codex", model)
		}
	case planv1.Provider_PROVIDER_ANTIGRAVITY:
		if IsClaudeModel(model) || IsCodexModel(model) {
			return fmt.Errorf("model %q belongs to another provider, not antigravity", model)
		}
		if !IsAntigravityModel(model) {
			return fmt.Errorf("unknown model %q for provider antigravity", model)
		}
	}
	return nil
}

// ValidateTaskModel normalizes a task's model (expanding human labels and short aliases such as "Gemini 4 Argon" or
// "argon") and checks that it is valid for provider p.
func ValidateTaskModel(p planv1.Provider, model string) (string, error) {
	if model == "" || p == planv1.Provider_PROVIDER_WATCH {
		return "", nil
	}
	if strings.TrimSpace(model) == "" {
		return "", fmt.Errorf("model %q is blank", model)
	}
	norm := strings.TrimSpace(model)
	if p == planv1.Provider_PROVIDER_FAKE {
		if err := protovalidate.Validate(&planv1.ProjectSettings{Model: &norm}); err != nil {
			return "", fmt.Errorf("model %q: %w", model, err)
		}
		return norm, nil
	}
	norm = NormalizeCircleModel(norm)
	if err := ValidateProviderModel(p, norm); err != nil {
		return "", err
	}
	if err := validateSharedModel(norm); err != nil {
		return "", err
	}
	return norm, nil
}

// ValidateQuestionModel checks that model is empty or a valid model for the project's question workers.
func ValidateQuestionModel(workerProvider, questionProvider planv1.Provider, model string) error {
	if model == "" {
		return nil
	}
	eff := questionProvider
	if eff == planv1.Provider_PROVIDER_UNSPECIFIED {
		if CanReadOnly(workerProvider) {
			eff = workerProvider
		} else {
			eff = planv1.Provider_PROVIDER_CLAUDE
		}
	}
	if err := ValidateProviderModel(workerProvider, model); err == nil {
		return nil
	}
	if eff != workerProvider {
		if err := ValidateProviderModel(eff, model); err == nil {
			return nil
		}
	}
	return ValidateProviderModel(workerProvider, model)
}

var devModelLine = regexp.MustCompile(`(?m)^[ \t]*(provider|model|question_provider|question_model)[ \t]*:.*(?:\r?\n|$)`)

// migrateDeveloperSettingsFile moves the per-project provider/model choices of devFile, a developer's settings file,
// to the Summoning Circle in home when it is not configured yet, normalizing labels to canonical model IDs; then it
// drops those lines from devFile. A value that cannot move (a provider or model this build refuses, or a question model
// whose provider cannot run read-only) is dropped all the same, and so is every line once the circle is configured (a
// lead seat or a row in settings.json, a row set to "Same as lead" included: Q67): the circle chooses the workers'
// seats then, and the stale line only showed a wrong provider in the project's settings (L20, Q56 A). The rows the
// lines do not fill are written "Same as lead" (fillCircle). No worker changes model. It returns an error, devFile left as it is,
// when devFile cannot be read as settings (a secret, a removed key, a line protobuf refuses) or the circle cannot be
// written. A file without those lines is not written: running it twice changes nothing.
func migrateDeveloperSettingsFile(home, devFile string) error {
	b, err := os.ReadFile(devFile)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !devModelLine.Match(b) {
		return nil
	}
	if line, _ := secretIn(b); line > 0 {
		return fmt.Errorf("%s: line %d looks like a secret", devFile, line)
	}
	if line, key := removedKeyIn(b); line > 0 {
		return fmt.Errorf("%s: line %d holds the removed key %s", devFile, line, key)
	}
	s := &planv1.ProjectSettings{}
	normalized := normalizeInstallQuestions.ReplaceAllFunc(b, func(match []byte) []byte {
		parts := normalizeInstallQuestions.FindSubmatch(match)
		if len(parts) == 3 {
			prefix := parts[1]
			val := strings.ToUpper(string(parts[2]))
			return append(prefix, []byte("INSTALL_QUESTIONS_"+val)...)
		}
		return match
	})
	opts := prototext.UnmarshalOptions{DiscardUnknown: true}
	if err := opts.Unmarshal(normalized, s); err != nil {
		return fmt.Errorf("%s: %w", devFile, err)
	}
	hasWorker := s.Provider != nil || s.Model != nil
	hasQuestion := s.QuestionProvider != nil || s.QuestionModel != nil
	if circleConfigured(readSettings(home)) {
		// The circle chooses every worker's seat already (ResolveWorkerSeat): the lines are dropped, and no worker
		// changes model.
		logx.L().Info("the stale seats of a project's settings are dropped: the circle is configured", "file", devFile)
		hasWorker, hasQuestion = false, false
	}

	var workerProvider planv1.Provider
	var workerModel string
	if hasWorker {
		workerProvider = s.GetProvider()
		workerModel = NormalizeCircleModel(s.GetModel())
		if workerProvider == planv1.Provider_PROVIDER_UNSPECIFIED && workerModel != "" {
			workerProvider = ProviderForCircleModel(workerModel)
		}
		if workerProvider != planv1.Provider_PROVIDER_UNSPECIFIED && workerModel == "" {
			workerModel = DefaultCircleModel(workerProvider)
		}
		if !ValidWorkerProvider(workerProvider) || ValidateProviderModel(workerProvider, workerModel) != nil {
			logx.L().Info("a stale worker seat of a project's settings is dropped, not moved to the circle",
				"file", devFile, "provider", s.GetProvider().String(), "model", s.GetModel())
			hasWorker = false
		}
	}

	var qProvider planv1.Provider
	var qModel string
	if hasQuestion {
		qProvider = s.GetQuestionProvider()
		qModel = NormalizeCircleModel(s.GetQuestionModel())
		if qProvider == planv1.Provider_PROVIDER_UNSPECIFIED && qModel != "" {
			qProvider = ProviderForCircleModel(qModel)
		}
		if qProvider == planv1.Provider_PROVIDER_UNSPECIFIED {
			if CanReadOnly(workerProvider) {
				qProvider = workerProvider
			} else {
				qProvider = planv1.Provider_PROVIDER_CLAUDE
			}
		}
		if qModel == "" {
			qModel = DefaultCircleModel(qProvider)
		}
		if !CanReadOnly(qProvider) || ValidateProviderModel(qProvider, qModel) != nil {
			logx.L().Info("a stale question seat of a project's settings is dropped, not moved to the circle",
				"file", devFile, "question_provider", s.GetQuestionProvider().String(), "question_model", s.GetQuestionModel())
			hasQuestion = false
		}
	}

	if hasWorker || hasQuestion {
		err := changeSettings(home, func(settings *uiv1.Settings) error {
			setIfMissing := func(kind planv1.WorkerKind, p planv1.Provider, m string) {
				// A written row is configured, "Same as lead" included: the project's value never fills it (Q67).
				if slices.ContainsFunc(settings.Circle, func(r *planv1.WorkerSeat) bool { return r.GetKind() == kind }) {
					return
				}
				settings.Circle = setCircleRowSlice(settings.Circle, &planv1.WorkerSeat{
					Kind:     kind,
					Provider: p,
					Model:    m,
				})
			}
			if hasWorker {
				for _, kind := range []planv1.WorkerKind{
					planv1.WorkerKind_WORKER_KIND_INVESTIGATION,
					planv1.WorkerKind_WORKER_KIND_MOCKUP,
					planv1.WorkerKind_WORKER_KIND_IFRIT,
					planv1.WorkerKind_WORKER_KIND_WORK,
					planv1.WorkerKind_WORKER_KIND_CORRECTION,
					planv1.WorkerKind_WORKER_KIND_REVIEW,
				} {
					if kind == planv1.WorkerKind_WORKER_KIND_IFRIT && !CanIfrit(workerProvider) {
						continue
					}
					setIfMissing(kind, workerProvider, workerModel)
				}
			}
			if hasQuestion {
				setIfMissing(planv1.WorkerKind_WORKER_KIND_QUESTION, qProvider, qModel)
			}
			return nil
		})
		if err != nil {
			// The lines stay: the circle did not take them, and the next start tries again.
			return fmt.Errorf("write the circle: %w", err)
		}
	}
	return fsx.WriteAtomic(devFile, devModelLine.ReplaceAll(b, nil), 0o600)
}

// ResolveWishPushStrategy resolves the push strategy and its source for a wish, falling back to the project settings.
func ResolveWishPushStrategy(wish *planv1.Wish, settings Settings) (planv1.PushStrategy, planv1.SettingSource) {
	if wish != nil && wish.GetPushStrategy() != planv1.PushStrategy_PUSH_STRATEGY_UNSPECIFIED {
		return wish.GetPushStrategy(), planv1.SettingSource_SETTING_SOURCE_WISH
	}
	return settings.PushStrategy, settings.PushStrategyFrom
}

// EffectiveWishPushStrategy resolves the effective push strategy for wish in store r with home folder home.
func EffectiveWishPushStrategy(ctx context.Context, r store.Reader, home string, wish *planv1.Wish) (planv1.PushStrategy, planv1.SettingSource, error) {
	if wish != nil && wish.GetPushStrategy() != planv1.PushStrategy_PUSH_STRATEGY_UNSPECIFIED {
		return wish.GetPushStrategy(), planv1.SettingSource_SETTING_SOURCE_WISH, nil
	}
	if wish == nil || len(wish.GetProjectIds()) == 0 {
		return planv1.PushStrategy_PUSH_STRATEGY_WISH, planv1.SettingSource_SETTING_SOURCE_DEFAULT, nil
	}
	project, err := store.Get[*planv1.Project](ctx, r, wish.GetProjectIds()[0])
	if err != nil {
		return planv1.PushStrategy_PUSH_STRATEGY_WISH, planv1.SettingSource_SETTING_SOURCE_DEFAULT, err
	}
	settings, _ := LoadSettings(home, project)
	return settings.PushStrategy, settings.PushStrategyFrom, nil
}

// WhenWord is when a check runs, as the settings and the brief say it: commit, push.
func WhenWord(w planv1.CheckWhen) string {
	return strings.ToLower(strings.TrimPrefix(w.String(), "CHECK_WHEN_"))
}

// ChecksText lists checks on one line: "lint: go tool task lint (commit, push); test: go tool task test (push)".
func ChecksText(checks []*planv1.ProjectCheck) string {
	parts := make([]string, len(checks))
	for i, c := range checks {
		words := make([]string, len(c.GetWhen()))
		for j, w := range c.GetWhen() {
			words[j] = WhenWord(w)
		}
		parts[i] = fmt.Sprintf("%s: %s (%s)", c.GetName(), c.GetCommand(), strings.Join(words, ", "))
	}
	return strings.Join(parts, "; ")
}

// CommitGates lists how an agent runs the commit checks, each through its gate: "`djinn gate run lint -- make lint`".
func (s Settings) CommitGates() string {
	var gates []string
	for _, c := range s.ChecksAt(planv1.CheckWhen_CHECK_WHEN_COMMIT) {
		gates = append(gates, "`"+GateCommand(c)+"`")
	}
	return strings.Join(gates, ", ")
}

// GateCommand is how an agent runs a check of the project: through the gate of its name.
func GateCommand(c *planv1.ProjectCheck) string {
	return "djinn gate run " + c.GetName() + " -- " + c.GetCommand()
}

// ChecksBrief says, in a sentence or two, which checks Djinn runs on the project's work and when, how a worker
// runs the commit checks and targeted tests (never push checks), and what a fresh worktree needs first; "" for a
// project that sets none. The lead's brief and each worker's first prompt carry it.
func (s Settings) ChecksBrief() string {
	if !s.Integrates() {
		return ""
	}
	var b strings.Builder
	commit, push := s.checksWhen(&b)
	commitHasTest := slices.ContainsFunc(s.ChecksAt(planv1.CheckWhen_CHECK_WHEN_COMMIT), func(c *planv1.ProjectCheck) bool {
		return strings.EqualFold(c.GetName(), "test")
	})
	if commit != "" {
		b.WriteString(" A worker runs the commit checks before it ends, and fixes what they find")
		if commitHasTest {
			b.WriteString(" (the commit check is the whole suite)")
		} else {
			b.WriteString(", then only the tests that cover what it changed")
			if s.TestOne != "" {
				b.WriteString(" (`" + s.TestOne + "`)")
			}
		}
		if push != "" {
			b.WriteString("; do not run the push checks (" + push + "): Djinn runs them before pushing.")
		} else {
			b.WriteString(".")
		}
	} else if push != "" {
		b.WriteString(" A worker runs only the tests that cover what it changed")
		if s.TestOne != "" {
			b.WriteString(" (`" + s.TestOne + "`)")
		}
		b.WriteString("; do not run the push checks (" + push + "): Djinn runs them before pushing.")
	}
	s.setupBrief(&b)
	return b.String()
}

// IfritChecksBrief is ChecksBrief for an ifrit's first prompt (T51): which checks Djinn runs and when, but an ifrit
// runs none of them after a change: Djinn runs the commit checks once the developer finishes it. "" for a project
// that sets none.
func (s Settings) IfritChecksBrief() string {
	if !s.Integrates() {
		return ""
	}
	var b strings.Builder
	commit, push := s.checksWhen(&b)
	switch {
	case commit != "" && push != "":
		b.WriteString(" Run neither after a change: Djinn runs the commit checks once you are finished, the push checks " +
			"before it pushes.")
	case commit != "":
		b.WriteString(" Do not run them after a change: Djinn runs them once you are finished.")
	case push != "":
		b.WriteString(" Do not run them: Djinn runs them before it pushes.")
	}
	if s.TestOne != "" {
		b.WriteString(" A test the developer asks for runs with `" + s.TestOne + "`.")
	}
	s.setupBrief(&b)
	return b.String()
}

// checksWhen writes to b which checks Djinn runs on the project's work, before it merges a task's work and before it
// pushes, and returns them, each list "" when there is none.
func (s Settings) checksWhen(b *strings.Builder) (commit, push string) {
	list := func(when planv1.CheckWhen) string {
		var names []string
		for _, c := range s.ChecksAt(when) {
			names = append(names, "`"+GateCommand(c)+"`")
		}
		return strings.Join(names, ", ")
	}
	b.WriteString("Djinn checks this project's work")
	commit, push = list(planv1.CheckWhen_CHECK_WHEN_COMMIT), list(planv1.CheckWhen_CHECK_WHEN_PUSH)
	if commit != "" {
		b.WriteString(" before it merges a task's work, with " + commit)
	}
	if push != "" {
		if commit != "" {
			b.WriteString(";")
		}
		b.WriteString(" before it pushes, with " + push)
	}
	b.WriteString(".")
	return commit, push
}

// setupBrief writes to b what a fresh worktree needs first, if anything.
func (s Settings) setupBrief(b *strings.Builder) {
	if s.Setup != "" {
		b.WriteString(" A fresh worktree needs `" + s.Setup + "` first.")
	}
}

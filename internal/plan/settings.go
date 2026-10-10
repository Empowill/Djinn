package plan

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/encoding/prototext"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// SettingsFile is where a project's repository keeps the settings its team shares: a plan.v1.ProjectSettings in
// text protobuf, next to the permissions in .agents.
var SettingsFile = filepath.Join(".agents", "settings.txtpb")

// DeveloperSettingsFile is where the developer keeps their own settings of a project, in Djinn's data folder home.
func DeveloperSettingsFile(home, projectID string) string {
	return filepath.Join(home, "projects", projectID, "settings.txtpb")
}

// ReadSettings reads the settings of the file at path: nil, and no error, when there is none. A file that holds
// something that looks like a secret is refused, comments included, without repeating it.
func ReadSettings(path string) (*planv1.ProjectSettings, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if line, why := secretIn(b); line > 0 {
		return nil, fmt.Errorf("%s: line %d %s; this file holds no secret: remove it", path, line, why)
	}
	s := &planv1.ProjectSettings{}
	if err := prototext.Unmarshal(b, s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := protovalidate.Validate(s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	seen := map[string]bool{}
	for _, c := range s.GetChecks() {
		name := strings.ToLower(c.GetName())
		if seen[name] {
			return nil, fmt.Errorf("%s: two checks are named %s: names ignore case", path, c.GetName())
		}
		seen[name] = true
	}
	return s, nil
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
)

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
	DefaultMergeMain      = planv1.MergeMain_MERGE_MAIN_RELEASE
	DefaultMergeMainEvery = time.Hour
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
	// The checks Djinn runs before it commits a task's work and before it pushes, in the order the file names them.
	Checks []*planv1.ProjectCheck
	// How many correction workers Djinn starts for a batch that conflicts in code or tests red, before it asks.
	CorrectionAttempts int

	// AnswerWorkers says whether Djinn starts a question worker after an answer to turn it into tasks.
	// EnlightenWorkers says whether Djinn starts an investigator after a request to investigate ("Enlighten me").
	// QuestionWorkers is deprecated: when set in settings, it sets both.
	AnswerWorkers     bool
	EnlightenWorkers  bool
	QuestionWorkers   bool
	QuestionProvider  planv1.Provider
	QuestionModel     string
	QuestionBudgetUSD float64

	// How Djinn keeps each wish's integration branch up with the project's main branch: the branch ("" for the
	// remote's default), when it merges it, how often it fetches it at most; and whether a release of Djinn installs by
	// itself while the person's checkout is on main.
	MainBranch      string
	MergeMain       planv1.MergeMain
	MergeMainEvery  time.Duration
	InstallReleases bool

	ProviderFrom, ModelFrom, BudgetFrom, BranchFrom, GeneratedFrom, GenerateFrom, SetupFrom planv1.SettingSource
	ChecksFrom, AttemptsFrom, InstallFrom                                                   planv1.SettingSource
	AnswerWorkersFrom, EnlightenWorkersFrom                                                 planv1.SettingSource
	QuestionWorkersFrom, QuestionProviderFrom, QuestionModelFrom, QuestionBudgetFrom        planv1.SettingSource
	MainBranchFrom, MergeMainFrom, MergeMainEveryFrom, InstallReleasesFrom                  planv1.SettingSource
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

// Defaults of the question workers: a cheaper model is enough to turn a decision into tasks, or to read and revise a
// question; and a cap, so that one never runs long.
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
		SetupFrom: def, ChecksFrom: def, InstallFrom: def, AttemptsFrom: def,
		AnswerWorkers: false, AnswerWorkersFrom: def,
		EnlightenWorkers: true, EnlightenWorkersFrom: def,
		QuestionWorkers: false, QuestionWorkersFrom: def,
		QuestionProviderFrom: def, QuestionBudgetUSD: DefaultQuestionBudgetUSD,
		QuestionModelFrom: def, QuestionBudgetFrom: def,
		MergeMain: DefaultMergeMain, MergeMainEvery: DefaultMergeMainEvery, InstallReleases: true,
		MainBranchFrom: def, MergeMainFrom: def, MergeMainEveryFrom: def, InstallReleasesFrom: def,
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
		if f.settings.QuestionWorkers != nil {
			v := f.settings.GetQuestionWorkers()
			s.AnswerWorkers, s.AnswerWorkersFrom = v, f.from
			s.EnlightenWorkers, s.EnlightenWorkersFrom = v, f.from
			s.QuestionWorkers, s.QuestionWorkersFrom = v, f.from
		}
		if f.settings.AnswerWorkers != nil {
			s.AnswerWorkers, s.AnswerWorkersFrom = f.settings.GetAnswerWorkers(), f.from
		}
		if f.settings.EnlightenWorkers != nil {
			s.EnlightenWorkers, s.EnlightenWorkersFrom = f.settings.GetEnlightenWorkers(), f.from
		}
		s.QuestionWorkers = s.AnswerWorkers && s.EnlightenWorkers
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
	}
	effective := s.QuestionProvider
	if effective == planv1.Provider_PROVIDER_UNSPECIFIED {
		if CanReadOnly(s.Provider) {
			effective = s.Provider
		} else {
			effective = planv1.Provider_PROVIDER_CLAUDE
		}
	}
	if !questionModel && effective == planv1.Provider_PROVIDER_CLAUDE {
		s.QuestionModel = DefaultQuestionModel
	}
	return s
}

// settingsFiles are the paths of project's files: the repository's, empty when the project has no folder here,
// and the developer's in home, empty without home.
func settingsFiles(home string, project *planv1.Project) (repo, dev string) {
	if project.GetDirectory() != "" {
		repo = filepath.Join(project.GetDirectory(), SettingsFile)
	}
	if home != "" && project.GetId() != "" {
		dev = DeveloperSettingsFile(home, project.GetId())
	}
	return repo, dev
}

// LoadSettings reads the settings of project (nil outside any project): its repository's, then the developer's in
// Djinn's data folder home. A file that cannot be read is an error that names it.
func LoadSettings(home string, project *planv1.Project) (Settings, error) {
	repo, dev, problems := loadSettings(home, project)
	return ResolveSettings(repo, dev), errors.Join(problems...)
}

// loadSettings reads both files of project, and why each one that could not be read was not.
func loadSettings(home string, project *planv1.Project) (repo, dev *planv1.ProjectSettings, problems []error) {
	if project == nil {
		return nil, nil, nil
	}
	repoFile, devFile := settingsFiles(home, project)
	var err error
	if repoFile != "" {
		if repo, err = ReadSettings(repoFile); err != nil {
			problems = append(problems, err)
		}
	}
	if devFile != "" {
		if dev, err = ReadSettings(devFile); err != nil {
			problems = append(problems, err)
		}
	}
	return repo, dev, problems
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
		{Name: "correction_attempts", Value: strconv.Itoa(s.CorrectionAttempts), Source: s.AttemptsFrom},
		{Name: "install", Value: s.Install, Source: s.InstallFrom},
		{Name: "answer_workers", Value: strconv.FormatBool(s.AnswerWorkers), Source: s.AnswerWorkersFrom},
		{Name: "enlighten_workers", Value: strconv.FormatBool(s.EnlightenWorkers), Source: s.EnlightenWorkersFrom},
		{Name: "question_provider", Value: questionProviderName(s.QuestionProvider), Source: s.QuestionProviderFrom},
		{Name: "question_model", Value: s.QuestionModel, Source: s.QuestionModelFrom},
		{Name: "question_budget_usd", Value: usd(s.QuestionBudgetUSD), Source: s.QuestionBudgetFrom},
		{Name: "main_branch", Value: s.MainBranch, Source: s.MainBranchFrom},
		{Name: "merge_main", Value: MergeMainWord(s.MergeMain), Source: s.MergeMainFrom},
		{Name: "merge_main_minutes", Value: strconv.Itoa(int(s.MergeMainEvery / time.Minute)), Source: s.MergeMainEveryFrom},
		{Name: "install_releases", Value: strconv.FormatBool(s.InstallReleases), Source: s.InstallReleasesFrom},
	}
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

// ChecksBrief says, in a sentence or two, which checks Djinn runs on the project's work and when, and what a fresh
// worktree needs first; "" for a project that sets none. The lead's brief and each worker's first prompt carry it.
func (s Settings) ChecksBrief() string {
	if !s.Integrates() {
		return ""
	}
	list := func(when planv1.CheckWhen) string {
		var names []string
		for _, c := range s.ChecksAt(when) {
			names = append(names, "`"+GateCommand(c)+"`")
		}
		return strings.Join(names, ", ")
	}
	var b strings.Builder
	b.WriteString("Djinn checks this project's work")
	commit, push := list(planv1.CheckWhen_CHECK_WHEN_COMMIT), list(planv1.CheckWhen_CHECK_WHEN_PUSH)
	if commit != "" {
		b.WriteString(" before it commits a task's work, with " + commit)
	}
	if push != "" {
		if commit != "" {
			b.WriteString(";")
		}
		b.WriteString(" before it pushes, with " + push)
	}
	b.WriteString(".")
	if commit != "" {
		b.WriteString(" A worker runs the commit checks before it ends, and fixes what they find.")
	}
	if s.Setup != "" {
		b.WriteString(" A fresh worktree needs `" + s.Setup + "` first.")
	}
	return b.String()
}

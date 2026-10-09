package plan

// Wish templates. A request that comes back (babysit a pull request, run the QA of a feature) opens a wish that
// already knows how to work: a skill declares it in the front matter of its SKILL.md, under metadata.djinn.wish.
// Routing proposes it when a request matches, the new wish starts its watcher and its lead starts with the skill,
// and the watcher's done line asks the developer whether to grant the wish. Nothing to code per template.

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"go.yaml.in/yaml/v3"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/locales"
)

// Template is the wish template of a skill, read from its SKILL.md.
type Template struct {
	// Skill is the skill's name; Dir its folder.
	Skill, Dir string
	// ProjectID is the project that holds the skill or summons it: the watcher runs in its folder.
	ProjectID string
	// Title is the new wish's title, Watch the watcher's command line, both with placeholders: {name} is the group
	// name of Match. DoneWhen is a watcher line that means done.
	Title, Watch, DoneWhen string
	// Restart starts the watcher's command again after each exit, until its done line.
	Restart bool
	// Match picks the template for a request, and fills the placeholders with its named groups.
	Match *regexp.Regexp
}

// templateYAML is a wish template as SKILL.md writes it.
type templateYAML struct {
	Title    string `yaml:"title"`
	Match    string `yaml:"match"`
	Watch    string `yaml:"watch"`
	DoneWhen string `yaml:"done_when"`
	Restart  bool   `yaml:"restart"`
}

// placeholders are the {name} of a title or a command line.
var placeholders = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// ReadTemplate reads the wish template of the skill in dir: nil without one, an error when it cannot be used.
func ReadTemplate(dir string) (*Template, error) {
	front, err := frontMatter(filepath.Join(dir, SkillFile))
	if err != nil || front == "" {
		return nil, err
	}
	// Agent Skills lets a skill hold extra metadata; Djinn reads its own key only, and ignores the rest.
	var doc struct {
		Metadata map[string]yaml.Node `yaml:"metadata"`
	}
	if err := yaml.Unmarshal([]byte(front), &doc); err != nil {
		return nil, fmt.Errorf("%s: its front matter is not YAML: %w", SkillFile, err)
	}
	node, ok := doc.Metadata["djinn"]
	if !ok {
		return nil, nil
	}
	var djinn struct {
		Wish *templateYAML `yaml:"wish"`
	}
	if err := node.Decode(&djinn); err != nil {
		return nil, fmt.Errorf("%s: metadata.djinn: %w", SkillFile, err)
	}
	if djinn.Wish == nil {
		return nil, nil
	}
	return parseTemplate(filepath.Base(dir), dir, *djinn.Wish)
}

// parseTemplate checks a template: a title and a match are required, the match must compile, every placeholder
// must be a group of the match, and a done line needs a watcher.
func parseTemplate(skill, dir string, y templateYAML) (*Template, error) {
	t := &Template{
		Skill: skill, Dir: dir, Title: strings.TrimSpace(y.Title), Watch: strings.TrimSpace(y.Watch),
		DoneWhen: strings.TrimSpace(y.DoneWhen), Restart: y.Restart,
	}
	switch {
	case t.Title == "":
		return nil, errors.New("metadata.djinn.wish has no title")
	case strings.TrimSpace(y.Match) == "":
		return nil, errors.New("metadata.djinn.wish has no match")
	case (t.DoneWhen != "" || t.Restart) && t.Watch == "":
		return nil, errors.New("metadata.djinn.wish has a done_when or a restart but no watch: they are a watcher's")
	}
	var err error
	if t.Match, err = regexp.Compile(y.Match); err != nil {
		return nil, fmt.Errorf("metadata.djinn.wish.match: %w", err)
	}
	for field, s := range map[string]string{"title": t.Title, "watch": t.Watch} {
		for _, m := range placeholders.FindAllStringSubmatch(s, -1) {
			if t.Match.SubexpIndex(m[1]) < 0 {
				return nil, fmt.Errorf("metadata.djinn.wish.%s: {%s} is no named group of its match", field, m[1])
			}
		}
	}
	return t, nil
}

// frontMatter is the front matter of the file path, between its first two lines of ---; empty without one.
func frontMatter(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() || strings.TrimSpace(strings.TrimPrefix(sc.Text(), "\ufeff")) != "---" {
		return "", nil
	}
	var b strings.Builder
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) == "---" {
			return b.String(), nil
		}
		b.WriteString(sc.Text() + "\n")
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("%s: its front matter never ends", SkillFile)
}

// Fill fills the template for request: the title, and the watcher's command line, each value one word of it. False
// when the request does not match.
func (t *Template) Fill(request string) (title, watch string, ok bool) {
	m := t.Match.FindStringSubmatch(request)
	if m == nil {
		return "", "", false
	}
	value := func(name string) string { return strings.TrimSpace(m[t.Match.SubexpIndex(name)]) }
	title = placeholders.ReplaceAllStringFunc(t.Title, func(p string) string { return oneLine(value(p[1 : len(p)-1])) })
	watch = placeholders.ReplaceAllStringFunc(t.Watch, func(p string) string { return quoteWord(value(p[1 : len(p)-1])) })
	return title, watch, true
}

// quoteWord quotes s so that a watcher's command line reads it as one word, whatever it holds: a value from a
// request never adds an argument (harness.splitCommand reads quotes as a shell does).
func quoteWord(s string) string {
	if s != "" && !strings.ContainsFunc(s, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("-_.,:/@+=#!%", r))
	}) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Templates are the wish templates of the skills the projects use, their own then those they summon, in the
// projects' order then by name. A template that cannot be used is left out.
func Templates(ctx context.Context, r store.Reader, projects []*planv1.Project) ([]*Template, error) {
	var out []*Template
	for _, p := range projects {
		dirs := ProjectSkills(p.GetDirectory())
		summoned, err := SummonedSkills(ctx, r, p)
		if err != nil {
			return nil, err
		}
		for _, s := range summoned {
			if s.Missing == "" {
				dirs = append(dirs, s.SkillDir)
			}
		}
		for _, s := range dirs {
			if t, err := ReadTemplate(s.Dir); err == nil && t != nil {
				t.ProjectID = p.GetId()
				out = append(out, t)
			}
		}
	}
	return out, nil
}

// matchTemplate is the first template a request matches, filled: nil when none does.
func matchTemplate(templates []*Template, request string) (*Template, *planv1.WishTemplate, string) {
	for _, t := range templates {
		if title, watch, ok := t.Fill(request); ok {
			return t, &planv1.WishTemplate{
				Skill: t.Skill, ProjectId: t.ProjectID, Watch: watch, DoneWhen: t.DoneWhen, Restart: t.Restart,
			}, title
		}
	}
	return nil, nil, ""
}

// DoneLine is the first line of text that says the template's work is done: a line that is done_when, or starts
// with it before a character that is not a letter nor a digit. Empty when none does.
func DoneLine(t *planv1.WishTemplate, text string) string {
	done := t.GetDoneWhen()
	if done == "" {
		return ""
	}
	for line := range strings.Lines(text) {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, done); ok {
			if r := []rune(rest); len(r) == 0 || !isWordRune(r[0]) {
				return line
			}
		}
	}
	return ""
}

// Watched reads a paragraph the watcher task printed, and says whether it holds the done line of its wish's
// template: Djinn then asks the developer whether to grant the wish, once while the question is open. It never
// grants the wish itself. A wish granted already, or without a template, is left as it is.
func (w *Wishes) Watched(ctx context.Context, task *planv1.Task, text string) (done bool, err error) {
	wish, err := store.Get[*planv1.Wish](ctx, w.Store, task.GetWishId())
	if err != nil {
		return false, err
	}
	line := DoneLine(wish.GetTemplate(), text)
	if line == "" || wish.GetState() == planv1.WishState_WISH_STATE_GRANTED {
		return line != "", nil
	}
	lang := cmp.Or(w.Language, locales.Source)
	params := map[string]string{"line": clipRunes(line, 200), "task": task.GetCode(), "wish": wish.GetTitle(),
		"skill": wish.GetTemplate().GetSkill()}
	ask := &planv1.QuestionServiceAskRequest{
		WishId: wish.GetId(), Text: locales.T(lang, "template.grant_question", params),
		Options:        []string{locales.T(lang, "template.grant", nil), locales.T(lang, "template.keep", nil)},
		Context:        locales.T(lang, "template.grant_context", params),
		Recommendation: "A: " + locales.T(lang, "template.grant_why", params),
		Icon:           "🏁",
	}
	return true, Status(w.Store.Tx(ctx, func(tx *store.Tx) error {
		asked, err := store.List[*planv1.Question](ctx, tx, store.Where{"wish_id": wish.GetId()})
		if err != nil {
			return err
		}
		if slices.ContainsFunc(asked, func(q *planv1.Question) bool { return q.GetGrant() && q.GetAnswer() == nil }) {
			return nil
		}
		if err := tx.Journal(actor, planv1connect.QuestionServiceAskProcedure, ask); err != nil {
			return err
		}
		return Ask(ctx, tx, &planv1.Question{
			Id: store.NewID(), WishId: ask.GetWishId(), Text: ask.GetText(), Options: ask.GetOptions(),
			Context: ask.GetContext(), Recommendation: ask.GetRecommendation(), CreateTime: timestamppb.Now(), Grant: true,
			Icon: ask.GetIcon(),
		})
	}))
}

// settleGrant grants the wish of a grant question answered A, in the transaction that stores the answer, journaled
// as djinn wish grant. B keeps the wish open.
func settleGrant(ctx context.Context, tx *store.Tx, q *planv1.Question) error {
	if !q.GetGrant() || q.GetAnswer().GetChoice() != planv1.Choice_CHOICE_A {
		return nil
	}
	wish, err := store.Get[*planv1.Wish](ctx, tx, q.GetWishId())
	if err != nil || wish.GetState() == planv1.WishState_WISH_STATE_GRANTED {
		return err
	}
	grant := &planv1.WishServiceGrantRequest{WishId: wish.GetId()}
	if err := tx.Journal(actor, planv1connect.WishServiceGrantProcedure, grant); err != nil {
		return err
	}
	return grantWish(ctx, tx, wish)
}

// SpawnWatcher starts the watcher task of a wish, in the project projectID, on the command line watch, and gives
// its code. djinn up gives the harness's (harness.SpawnWatcher).
type SpawnWatcher func(ctx context.Context, wishID, projectID, title, watch string, restart bool) (code string, err error)

// WithWatchers lets a wish made from a template start its watcher.
func WithWatchers(spawn SpawnWatcher) Option { return func(o *options) { o.watchers = spawn } }

// startTemplate starts the watcher of a wish made from a template, and says, for the first line of its lead, which
// skill to follow and what the watcher does; or why it did not start.
func (w *Wishes) startTemplate(ctx context.Context, wish *planv1.Wish) string {
	t := wish.GetTemplate()
	if t == nil {
		return ""
	}
	skill := fmt.Sprintf("the skill %s", t.GetSkill())
	if p, err := store.Get[*planv1.Project](ctx, w.Store, t.GetProjectId()); err == nil {
		if dir := usedSkill(ctx, w.Store, p, t.GetSkill()); dir != "" {
			skill = fmt.Sprintf("the skill %s (%s)", t.GetSkill(), filepath.Join(dir, SkillFile))
		}
	}
	line := fmt.Sprintf(" This wish follows %s: read it first, and follow it.", skill)
	if t.GetWatch() == "" {
		return line
	}
	title := locales.T(cmp.Or(w.Language, locales.Source), "template.watch_title", map[string]string{"skill": t.GetSkill()})
	spawn := fmt.Sprintf("djinn task spawn %s %q --project-id %s --provider watch --prompt %q", wish.GetId(), title,
		t.GetProjectId(), t.GetWatch())
	if t.GetRestart() {
		spawn += " --restart"
	}
	if w.Watchers == nil {
		return line + " Start its watcher: " + spawn + "."
	}
	code, err := w.Watchers(ctx, wish.GetId(), t.GetProjectId(), title, t.GetWatch(), t.GetRestart())
	if err != nil {
		return line + fmt.Sprintf(" Its watcher did not start (%v): start it with %s.", err, spawn)
	}
	line += fmt.Sprintf(" Its watcher %s runs %s and tells you what changes.", code, t.GetWatch())
	if t.GetDoneWhen() != "" {
		line += fmt.Sprintf(" When it prints %s, Djinn asks the developer whether to grant the wish.", t.GetDoneWhen())
	}
	return line
}

// usedSkill is the folder of the skill name that project uses, its own or summoned; empty when it is not found.
func usedSkill(ctx context.Context, r store.Reader, project *planv1.Project, name string) string {
	if s, err := FindSkill(project.GetDirectory(), name); err == nil {
		return s.Dir
	}
	summoned, _ := SummonedSkills(ctx, r, project)
	for _, s := range summoned {
		if s.Missing == "" && strings.EqualFold(s.Name, name) {
			return s.Dir
		}
	}
	return ""
}

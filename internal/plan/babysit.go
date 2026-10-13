package plan

// Babysitting. A project names, in one of its own skills, how its pull or merge requests are babysat: the watcher
// command, under metadata.djinn.babysit. When a task of a wish opens one, Djinn starts that watcher in the same wish,
// and its lead follows the skill each time the watcher speaks (harness.babysitOpened).

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// Forges a babysit declaration may name.
const (
	ForgeGitHub = "github"
	ForgeGitLab = "gitlab"
)

// Babysit is how a project babysits its pull or merge requests, as a skill of it declares it.
type Babysit struct {
	// Skill is the skill's name; Dir its folder.
	Skill, Dir string
	// Watch is the watcher's command line: {pr} and {mr} are the request's number, {state} a file of the watcher's
	// own under Djinn's data folder.
	Watch string
	// DoneWhen is a watcher line that ends it, done; empty: its command's exit does.
	DoneWhen string
	// Restart starts the command again after each exit, until its done line.
	Restart bool
	// Forge is the forge the declaration is for, ForgeGitHub or ForgeGitLab; empty for any.
	Forge string
}

// babysitYAML is a babysit declaration as SKILL.md writes it.
type babysitYAML struct {
	Watch    string `yaml:"watch"`
	DoneWhen string `yaml:"done_when"`
	Restart  bool   `yaml:"restart"`
	Forge    string `yaml:"forge"`
}

// babysitPlaceholders are the placeholders a babysit's command may hold.
var babysitPlaceholders = []string{"pr", "mr", "state"}

// ReadBabysit reads the babysit declaration of the skill in dir: nil without one, an error when it cannot be used.
func ReadBabysit(dir string) (*Babysit, error) {
	djinn, err := readDjinn(dir)
	if err != nil || djinn.Babysit == nil {
		return nil, err
	}
	y := djinn.Babysit
	b := &Babysit{
		Skill: filepath.Base(dir), Dir: dir, Watch: strings.TrimSpace(y.Watch), DoneWhen: strings.TrimSpace(y.DoneWhen),
		Restart: y.Restart, Forge: strings.ToLower(strings.TrimSpace(y.Forge)),
	}
	if b.Watch == "" {
		return nil, errors.New("metadata.djinn.babysit has no watch: the watcher's command line")
	}
	if b.Forge != "" && b.Forge != ForgeGitHub && b.Forge != ForgeGitLab {
		return nil, fmt.Errorf("metadata.djinn.babysit.forge: %s is neither %s nor %s", y.Forge, ForgeGitHub, ForgeGitLab)
	}
	number := false
	for _, m := range placeholders.FindAllStringSubmatch(b.Watch, -1) {
		switch m[1] {
		case "pr", "mr":
			number = true
		case "state":
		default:
			return nil, fmt.Errorf("metadata.djinn.babysit.watch: {%s} is none of {%s}", m[1],
				strings.Join(babysitPlaceholders, "}, {"))
		}
	}
	if !number {
		return nil, errors.New("metadata.djinn.babysit.watch has no {pr} nor {mr}: the request's number")
	}
	return b, nil
}

// ProjectBabysit is the babysit declaration of the project's own skills for forge (ForgeGitHub, ForgeGitLab): the
// first by name that names this forge, else the first that names none. nil without one. A summoned skill does not
// count: its watcher would run in the folder of the project that holds it, not in the request's repository.
func ProjectBabysit(project *planv1.Project, forge string) *Babysit {
	var anyForge *Babysit
	for _, s := range ProjectSkills(project.GetDirectory()) {
		b, err := ReadBabysit(s.Dir)
		switch {
		case err != nil || b == nil:
		case b.Forge == forge:
			return b
		case b.Forge == "" && anyForge == nil:
			anyForge = b
		}
	}
	return anyForge
}

// Fill is the watcher's command line for the request number, its state in the file state.
func (b *Babysit) Fill(number int, state string) string {
	return FillBabysit(b.Watch, number, state)
}

// FillBabysit fills the placeholders of a babysit's command line watch: {pr} and {mr} with number, {state} with the
// file state, each one word of it.
func FillBabysit(watch string, number int, state string) string {
	return placeholders.ReplaceAllStringFunc(watch, func(p string) string {
		switch p[1 : len(p)-1] {
		case "pr", "mr":
			return strconv.Itoa(number)
		case "state":
			return quoteWord(state)
		}
		return p
	})
}

// shellWord matches one word of a command line: a quoted one, as quoteWord writes it, or one without blanks.
const shellWord = `(?:'(?:[^']|'\\'')*'|\S+)`

// quotedWord matches one word as quoteWord writes it: quoted, or plain.
const quotedWord = `(?:'(?:[^']|'\\'')*'|[\p{L}\p{N}\-_.,:/@+=#!%]+)`

// filledPattern matches the whole command line watch, trimmed, its placeholders filled by word, each placeholder's
// pattern by name.
func filledPattern(watch string, word func(name string) string) *regexp.Regexp {
	watch = strings.TrimSpace(watch)
	var re strings.Builder
	re.WriteString("^")
	last := 0
	for _, m := range placeholders.FindAllStringSubmatchIndex(watch, -1) {
		re.WriteString(regexp.QuoteMeta(watch[last:m[0]]))
		re.WriteString(word(watch[m[2]:m[3]]))
		last = m[1]
	}
	re.WriteString(regexp.QuoteMeta(watch[last:]) + "$")
	return regexp.MustCompile(re.String())
}

// Watches tells whether the command line command babysits the request number: the declaration's command, {pr} and
// {mr} filled with number, {state} any one word, both trimmed. A watcher spawned by hand on it runs that command.
func (b *Babysit) Watches(command string, number int) bool {
	return filledPattern(b.Watch, func(name string) string {
		switch name {
		case "pr", "mr":
			return strconv.Itoa(number)
		case "state":
			return shellWord
		}
		return regexp.QuoteMeta("{" + name + "}")
	}).MatchString(strings.TrimSpace(command))
}

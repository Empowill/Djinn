package plan

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// topicChange is the part of a notice drawn from the journal of configuration changes.
const topicChange = planv1.ProjectNoticeTopic_PROJECT_NOTICE_TOPIC_CHANGE

// configLines are what changed from the configuration in old to the one in next, two folders laid out as a project's
// (ConfigFolders), a sentence each: the team's settings, the permissions, each skill, the other files of .agents.
// plugged tells whether the inbox source of a skill of the project is plugged in on this machine: a change of its
// command then asks for your look.
func configLines(old, next string, plugged func(skill string) bool) []*planv1.ConfigChangeLine {
	var out []*planv1.ConfigChangeLine
	out = append(out, settingsLines(old, next)...)
	out = append(out, permissionLines(old, next)...)
	out = append(out, skillLines(old, next, plugged)...)
	if files := otherFiles(old, next); len(files) > 0 {
		out = append(out, &planv1.ConfigChangeLine{
			Topic: topicSettings, Subject: ".agents",
			Text: "Other files of .agents change: " + strings.Join(files, ", ") + ".",
		})
	}
	return out
}

// line is a ConfigChangeLine.
func line(topic planv1.ProjectNoticeTopic, subject string, asks bool, format string, args ...any) *planv1.ConfigChangeLine {
	return &planv1.ConfigChangeLine{Topic: topic, Subject: subject, Text: fmt.Sprintf(format, args...), Asks: asks}
}

// settingsLines say what changed in the team's settings file from old to new: the file itself, its checks, its setup,
// the reference branch it names, then the names of the other settings it changes.
func settingsLines(old, next string) []*planv1.ConfigChangeLine {
	subject := filepath.ToSlash(SettingsFile)
	oldPath, newPath := filepath.Join(old, SettingsFile), filepath.Join(next, SettingsFile)
	o, oErr := ReadSettings(oldPath)
	n, nErr := ReadSettings(newPath)
	oHas, nHas := exists(oldPath), exists(newPath)
	settings := func(format string, args ...any) *planv1.ConfigChangeLine {
		return line(topicSettings, subject, false, format, args...)
	}
	switch {
	case !oHas && !nHas, oErr == nil && nErr == nil && proto.Equal(o, n) && oHas == nHas:
		return nil
	case nErr != nil:
		// A notice of the project says so until it is fixed.
		return []*planv1.ConfigChangeLine{settings("Djinn can no longer read the team's settings: the project's tasks " +
			"do not spawn until the file is fixed.")}
	case oErr != nil:
		return []*planv1.ConfigChangeLine{settings("Djinn can read the team's settings again.")}
	case !nHas:
		return []*planv1.ConfigChangeLine{settings("The team's settings file is gone: Djinn's defaults apply, and " +
			"no check runs.")}
	}
	var out []*planv1.ConfigChangeLine
	if !oHas {
		out = append(out, settings("The team's settings file appears."))
	}
	out = append(out, checkLines(o.GetChecks(), n.GetChecks())...)
	switch {
	case o.GetSetup() == n.GetSetup():
	case n.GetSetup() == "":
		out = append(out, settings("The project has no setup any more."))
	default:
		out = append(out, settings("The setup is now %s.", n.GetSetup()))
	}
	switch {
	case o.GetMainBranch() == n.GetMainBranch():
	case n.GetMainBranch() == "":
		out = append(out, line(topicBranch, "", false, "The team's file no longer names a reference branch."))
	default:
		out = append(out, line(topicBranch, n.GetMainBranch(), false,
			"The team's file names the reference branch %s: Djinn follows it, unless your own file names another.",
			n.GetMainBranch()))
	}
	if names := changedFields(o, n, "checks", "setup", "main_branch"); len(names) > 0 {
		out = append(out, settings("The team's settings change %s.", strings.Join(names, ", ")))
	}
	return out
}

// checkLines say which checks are new, gone or changed from old to next, by name.
func checkLines(old, next []*planv1.ProjectCheck) []*planv1.ConfigChangeLine {
	var out []*planv1.ConfigChangeLine
	find := func(checks []*planv1.ProjectCheck, name string) *planv1.ProjectCheck {
		i := slices.IndexFunc(checks, func(c *planv1.ProjectCheck) bool { return c.GetName() == name })
		if i < 0 {
			return nil
		}
		return checks[i]
	}
	for _, c := range next {
		switch was := find(old, c.GetName()); {
		case was == nil:
			out = append(out, line(topicSettings, c.GetName(), false, "New check %s %s: %s.", c.GetName(),
				checkWhen(c), c.GetCommand()))
		case !proto.Equal(was, c):
			out = append(out, line(topicSettings, c.GetName(), false, "The check %s changes: %s, %s.", c.GetName(),
				c.GetCommand(), checkWhen(c)))
		}
	}
	for _, c := range old {
		if find(next, c.GetName()) == nil {
			out = append(out, line(topicSettings, c.GetName(), false, "The check %s is gone.", c.GetName()))
		}
	}
	return out
}

// checkWhen says when check c runs: before a commit, before a push, or both.
func checkWhen(c *planv1.ProjectCheck) string {
	var words []string
	for _, w := range c.GetWhen() {
		words = append(words, "a "+WhenWord(w))
	}
	return "before " + strings.Join(words, " and ")
}

// changedFields are the names of the fields of a and b, two messages of the same type, that differ, but those of
// skip.
func changedFields(a, b proto.Message, skip ...string) []string {
	ra, rb := a.ProtoReflect(), b.ProtoReflect()
	var out []string
	fields := ra.Descriptor().Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		name := string(fd.Name())
		if slices.Contains(skip, name) || ra.Has(fd) == rb.Has(fd) && fieldEqual(fd, ra.Get(fd), rb.Get(fd)) {
			continue
		}
		out = append(out, name)
	}
	return out
}

// fieldEqual tells whether a and b, two values of the field fd, are the same.
func fieldEqual(fd protoreflect.FieldDescriptor, a, b protoreflect.Value) bool {
	if fd.IsList() {
		la, lb := a.List(), b.List()
		if la.Len() != lb.Len() {
			return false
		}
		for i := range la.Len() {
			if !singleEqual(fd, la.Get(i), lb.Get(i)) {
				return false
			}
		}
		return true
	}
	return singleEqual(fd, a, b)
}

func singleEqual(fd protoreflect.FieldDescriptor, a, b protoreflect.Value) bool {
	if fd.Kind() == protoreflect.MessageKind || fd.Kind() == protoreflect.GroupKind {
		return proto.Equal(a.Message().Interface(), b.Message().Interface())
	}
	return a.Equal(b)
}

// permissionLines say what changed in the project's permissions from old to new. What gives the workers more asks
// for your look: a command allowed, a command no longer denied, editing, the network, the auto mode.
func permissionLines(old, next string) []*planv1.ConfigChangeLine {
	subject := filepath.ToSlash(PermissionsFile)
	o, _, oErr := ReadPermissions(old)
	n, _, nErr := ReadPermissions(next)
	perm := func(asks bool, format string, args ...any) *planv1.ConfigChangeLine {
		return line(topicPermissions, subject, asks, format, args...)
	}
	switch {
	case nErr != nil:
		if oErr != nil {
			return nil
		}
		return []*planv1.ConfigChangeLine{perm(false, "Djinn can no longer read the project's permissions: its "+
			"workers, watchers and sources do not start until the file is fixed.")}
	case oErr != nil:
		o = nil // read as none: what it grants now is new
	}
	if o == nil {
		o = &djinnv1.Permissions{}
	}
	if n == nil {
		n = &djinnv1.Permissions{}
	}
	var out []*planv1.ConfigChangeLine
	if oErr != nil {
		out = append(out, perm(false, "Djinn can read the project's permissions again."))
	}
	if more := missing(n.GetCommands(), o.GetCommands()); len(more) > 0 {
		out = append(out, perm(true, "Permissions: %s the workers may run: %s.", commandsWord(len(more), "more "),
			strings.Join(more, ", ")))
	}
	if fewer := missing(o.GetCommands(), n.GetCommands()); len(fewer) > 0 {
		out = append(out, perm(false, "Permissions: %s fewer: %s.", commandsWord(len(fewer), ""), strings.Join(fewer, ", ")))
	}
	if freed := missing(o.GetDeniedCommands(), n.GetDeniedCommands()); len(freed) > 0 {
		out = append(out, perm(true, "Permissions: %s no longer denied: %s.", commandsWord(len(freed), ""),
			strings.Join(freed, ", ")))
	}
	if denied := missing(n.GetDeniedCommands(), o.GetDeniedCommands()); len(denied) > 0 {
		out = append(out, perm(false, "Permissions: %s denied: %s.", commandsWord(len(denied), "more "),
			strings.Join(denied, ", ")))
	}
	if o.GetEdit() != n.GetEdit() {
		if n.GetEdit() {
			out = append(out, perm(true, "Permissions: the workers may now edit the project's files."))
		} else {
			out = append(out, perm(false, "Permissions: the workers may no longer edit the project's files."))
		}
	}
	if o.GetNetwork() != n.GetNetwork() {
		if n.GetNetwork() {
			out = append(out, perm(true, "Permissions: the agents' web tools may now reach the network."))
		} else {
			out = append(out, perm(false, "Permissions: the agents' web tools may no longer reach the network."))
		}
	}
	if oAuto, nAuto := o.GetMode() == djinnv1.Mode_MODE_AUTO, n.GetMode() == djinnv1.Mode_MODE_AUTO; oAuto != nAuto {
		if nAuto {
			out = append(out, perm(true, "Permissions: an action not listed is now left to the agent's own auto mode."))
		} else {
			out = append(out, perm(false, "Permissions: an action not listed is now refused."))
		}
	}
	return out
}

// missing are the items of a that b lacks, in a's order.
func missing(a, b []string) []string {
	var out []string
	for _, s := range a {
		if !slices.Contains(b, s) {
			out = append(out, s)
		}
	}
	return out
}

// commandsWord is "1 command" or "n commands", more before the noun.
func commandsWord(n int, more string) string {
	if n == 1 {
		return "1 " + more + "command"
	}
	return fmt.Sprintf("%d %scommands", n, more)
}

// skillState is what Djinn reads of a skill: what it declares for Djinn, or why it cannot read it.
type skillState struct {
	dir   string
	djinn djinnYAML
	err   error
}

// readSkillState reads the skill in dir.
func readSkillState(dir string) skillState {
	s := skillState{dir: dir}
	d, err := readDjinn(dir)
	var unreadable *UnreadableSkillError
	if errors.As(err, &unreadable) {
		s.err = err
		return s
	}
	s.djinn = d // what it declares; an error of one part leaves the others
	return s
}

// skillLines say which skills are new, gone or changed from old to next, and what each change makes or runs for
// Djinn: a wish template or a babysit new or changed asks for your look, as does the command of a source plugged
// in on this machine (plugged). A new source asks through its own notice, until it is plugged in or said no to.
func skillLines(old, next string, plugged func(skill string) bool) []*planv1.ConfigChangeLine {
	oldSkills, newSkills := ProjectSkills(old), ProjectSkills(next)
	find := func(skills []SkillDir, name string) *SkillDir {
		i := slices.IndexFunc(skills, func(s SkillDir) bool { return strings.EqualFold(s.Name, name) })
		if i < 0 {
			return nil
		}
		return &skills[i]
	}
	var out []*planv1.ConfigChangeLine
	for _, s := range newSkills {
		was := find(oldSkills, s.Name)
		if was == nil {
			out = append(out, newSkillLine(s.Name, readSkillState(s.Dir)))
			continue
		}
		out = append(out, changedSkillLines(s.Name, readSkillState(was.Dir), readSkillState(s.Dir), plugged)...)
	}
	for _, s := range oldSkills {
		if find(newSkills, s.Name) != nil {
			continue
		}
		text := "The skill %s is gone."
		if readSkillState(s.Dir).djinn.Source != nil && plugged(s.Name) {
			text = "The skill %s is gone: its inbox source, plugged in here, no longer runs."
		}
		out = append(out, line(topicSkill, s.Name, false, text, s.Name))
	}
	return out
}

// newSkillLine says a skill is new, and what it is to Djinn.
func newSkillLine(name string, s skillState) *planv1.ConfigChangeLine {
	if s.err != nil {
		return line(topicSkill, name, false, "New skill %s, which Djinn cannot read.", name)
	}
	var roles []string
	asks := false
	if s.djinn.Wish != nil {
		roles, asks = append(roles, fmt.Sprintf("a wish template (%s)", s.djinn.Wish.Title)), true
	}
	if s.djinn.Source != nil {
		roles = append(roles, "an inbox source")
	}
	if s.djinn.Babysit != nil {
		roles, asks = append(roles, "the babysit of the project's pull requests"), true
	}
	if len(roles) == 0 {
		return line(topicSkill, name, false, "New skill %s: know-how for the workers.", name)
	}
	return line(topicSkill, name, asks, "New skill %s, with %s.", name, joinAnd(roles))
}

// changedSkillLines say what changed in the skill name from was to is.
func changedSkillLines(name string, was, is skillState, plugged func(skill string) bool) []*planv1.ConfigChangeLine {
	skill := func(asks bool, format string, args ...any) *planv1.ConfigChangeLine {
		return line(topicSkill, name, asks, format, args...)
	}
	var out []*planv1.ConfigChangeLine
	switch {
	case is.err != nil && was.err != nil:
	case is.err != nil:
		// A notice of the project says so until it is fixed.
		return []*planv1.ConfigChangeLine{skill(false, "Djinn can no longer read the skill %s: it ignores its wish "+
			"template, inbox source and babysit.", name)}
	case was.err != nil:
		out = append(out, skill(false, "Djinn can read the skill %s again.", name))
	}
	o, n := was.djinn, is.djinn
	switch {
	case reflect.DeepEqual(o.Wish, n.Wish):
	case n.Wish == nil:
		out = append(out, skill(false, "The skill %s no longer offers a wish template.", name))
	case o.Wish == nil:
		out = append(out, skill(true, "The skill %s now offers a wish template: %s.", name, n.Wish.Title))
	default:
		out = append(out, skill(true, "The wish template of the skill %s changes its %s.", name,
			joinAnd(yamlChanges(*o.Wish, *n.Wish))))
	}
	switch {
	case reflect.DeepEqual(o.Source, n.Source):
	case n.Source == nil:
		out = append(out, skill(false, "The skill %s no longer offers an inbox source.", name))
	case o.Source == nil:
		out = append(out, skill(false, "The skill %s now offers an inbox source.", name))
	default:
		changes := yamlChanges(*o.Source, *n.Source)
		runs := slices.Contains(changes, "watch") || slices.Contains(changes, "every")
		text := "The inbox source of the skill %s changes its %s."
		if runs && plugged(name) {
			text = "The inbox source of the skill %s, plugged in here, changes its %s."
		}
		out = append(out, skill(runs && plugged(name), text, name, joinAnd(changes)))
	}
	switch {
	case reflect.DeepEqual(o.Babysit, n.Babysit):
	case n.Babysit == nil:
		out = append(out, skill(false, "The skill %s no longer babysits the project's pull requests.", name))
	case o.Babysit == nil:
		out = append(out, skill(true, "The skill %s now babysits the project's pull requests: %s.", name, n.Babysit.Watch))
	default:
		out = append(out, skill(true, "The babysit of the skill %s changes its %s.", name,
			joinAnd(yamlChanges(*o.Babysit, *n.Babysit))))
	}
	var parts []string
	if Description(was.dir) != Description(is.dir) {
		parts = append(parts, "its description")
	}
	if skillBody(was.dir) != skillBody(is.dir) {
		parts = append(parts, "its instructions")
	}
	if files := slices.DeleteFunc(changedFiles(was.dir, is.dir), func(f string) bool { return f == SkillFile }); len(files) > 0 {
		parts = append(parts, "its files "+strings.Join(files, ", "))
	}
	if len(parts) > 0 {
		out = append(out, skill(false, "The skill %s changes %s.", name, joinAnd(parts)))
	}
	if len(out) == 0 && len(changedFiles(was.dir, is.dir)) > 0 {
		out = append(out, skill(false, "The skill %s changes its front matter.", name))
	}
	return out
}

// yamlChanges are the YAML names of the fields that differ between a and b, two structs of the same type.
func yamlChanges[T any](a, b T) []string {
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	var out []string
	for i := range va.NumField() {
		if !reflect.DeepEqual(va.Field(i).Interface(), vb.Field(i).Interface()) {
			name, _, _ := strings.Cut(va.Type().Field(i).Tag.Get("yaml"), ",")
			out = append(out, cmp.Or(name, strings.ToLower(va.Type().Field(i).Name)))
		}
	}
	return out
}

// joinAnd joins words as a sentence does: a, b and c.
func joinAnd(words []string) string {
	switch len(words) {
	case 0:
		return ""
	case 1:
		return words[0]
	}
	return strings.Join(words[:len(words)-1], ", ") + " and " + words[len(words)-1]
}

// skillBody is the SKILL.md of the skill in dir past its front matter; empty without the file.
func skillBody(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, SkillFile))
	if err != nil {
		return ""
	}
	text := strings.TrimPrefix(string(b), "\ufeff")
	if rest, ok := strings.CutPrefix(text, "---"); ok {
		if _, body, found := strings.Cut(rest, "\n---"); found {
			return strings.TrimSpace(body)
		}
	}
	return strings.TrimSpace(text)
}

// changedFiles are the files, relative and with slashes, that differ between the folders a and b: new, gone or
// changed. A folder that does not exist holds none.
func changedFiles(a, b string) []string {
	fa, fb := filesIn(a), filesIn(b)
	var out []string
	for path, content := range fb {
		if was, ok := fa[path]; !ok || !bytes.Equal(was, content) {
			out = append(out, path)
		}
	}
	for path := range fa {
		if _, ok := fb[path]; !ok {
			out = append(out, path)
		}
	}
	slices.Sort(out)
	return out
}

// filesIn are the files under dir by path, relative with slashes, and their content; a link by its target. A folder
// that cannot be read holds none.
func filesIn(dir string) map[string][]byte {
	out := map[string][]byte{}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return out
	}
	defer root.Close()
	_ = fs.WalkDir(root.FS(), ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return nil //nolint:nilerr // a folder that cannot be read holds nothing
		}
		if d.Type()&fs.ModeSymlink != 0 {
			target, _ := root.Readlink(path)
			out[path] = []byte("link:" + target)
		} else if content, err := fs.ReadFile(root.FS(), path); err == nil {
			out[path] = content
		}
		return nil
	})
	return out
}

// otherFiles are the files of .agents, besides the settings, the permissions and the skills, that differ between the
// configurations in old and next, with slashes.
func otherFiles(old, next string) []string {
	skip := []string{filepath.ToSlash(SettingsFile), filepath.ToSlash(PermissionsFile)}
	var out []string
	for _, f := range changedFiles(filepath.Join(old, ".agents"), filepath.Join(next, ".agents")) {
		path := ".agents/" + f
		if slices.Contains(skip, path) || strings.HasPrefix(f, "skills/") {
			continue
		}
		out = append(out, path)
	}
	return out
}

// exists tells whether path is there.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

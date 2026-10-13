package harness

// What a watcher may run. A watcher has no reviewer: where its worker's permissions list commands (the project's
// .agents/permissions.txtpb, a wish's allowance over it), its command must be one of them, or one of two more. The
// command Djinn builds from a declaration of a skill of the project itself (metadata.djinn.babysit,
// metadata.djinn.wish): the repository declares it, so it runs, permissions file or not. And a command the developer
// allows in the project for themselves (djinn project allow --watch), kept in their own settings, never in the
// repository. The project's denied_commands win over both (Q257).

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	"google.golang.org/protobuf/proto"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// watchPermissions are the command line the watcher t runs, its own with Djinn's variables expanded (watchCommand),
// and perms with that command allowed, when a skill of the project declares it or the developer allows it, and how,
// for its start event; perms as they are when they let it run already. What is judged is the command that runs. An
// error says why it cannot run, and how to let it.
func (h *Harness) watchPermissions(
	ctx context.Context, t *planv1.Task, project *planv1.Project, perms *djinnv1.Permissions, command string,
) (string, *djinnv1.Permissions, string, error) {
	command, err := h.watchCommand(t, command)
	if err != nil {
		return "", nil, "", err
	}
	if perms == nil || project == nil || commandAllowed(perms, command) {
		return command, perms, "", nil
	}
	if !matchesPrefix(command, perms.GetDeniedCommands()) {
		how := ""
		if skill := h.declaredWatch(ctx, t, project, command); skill != "" {
			how = "; its command is the one the skill " + skill + " declares"
		} else if settings, _ := plan.LoadSettings(h.home, project); matchesPrefix(command, settings.WatchCommands) {
			how = "; its command is allowed by your own settings of " + project.GetName()
		}
		if how != "" {
			perms = proto.CloneOf(perms)
			perms.Commands = append(perms.Commands, command)
			return command, perms, how, nil
		}
	}
	return "", nil, "", watchRefusal(project, command, perms)
}

// declaredWatch is the skill of the project that declares the command of the watcher t, as Djinn builds it from the
// declaration then expands it (watchCommand); empty when none does. A babysit's: Djinn started t on a request
// (Task.babysit), and its command is the skill's watch with {pr} and {mr} that request's number and {state} t's own
// file. A template's: t watches for the wish made from it in the project, its command is the one the wish holds, and
// the skill's watch makes it.
func (h *Harness) declaredWatch(ctx context.Context, t *planv1.Task, project *planv1.Project, command string) string {
	if b := t.GetBabysit(); b.GetNumber() > 0 {
		if s, err := plan.FindSkill(project.GetDirectory(), b.GetSkill()); err == nil {
			n := int(b.GetNumber())
			if decl, err := plan.ReadBabysit(s.Dir); err == nil && decl != nil {
				if built, err := h.watchCommand(t, decl.Fill(n, babysitStatePath(h.home, t.GetId(), decl.Skill, n))); err == nil && command == built {
					return decl.Skill
				}
			}
		}
	}
	wish, err := store.Get[*planv1.Wish](ctx, h.store, t.GetWishId())
	tmpl := wish.GetTemplate()
	if err != nil || tmpl.GetProjectId() != project.GetId() {
		return ""
	}
	if built, err := h.watchCommand(t, tmpl.GetWatch()); err != nil || command != built {
		return ""
	}
	if s, err := plan.FindSkill(project.GetDirectory(), tmpl.GetSkill()); err == nil {
		if decl, err := plan.ReadTemplate(s.Dir); err == nil && decl != nil && decl.Builds(tmpl.GetWatch()) {
			return decl.Skill
		}
	}
	return ""
}

// watchRefusal says why a watcher cannot run command in project under perms, and how it could: from the declaration
// of a skill of the project (A), or allowed by the developer for themselves (B), with the exact command.
func watchRefusal(project *planv1.Project, command string, perms *djinnv1.Permissions) error {
	name, file := project.GetName(), filepath.ToSlash(PermissionsFile)
	if matchesPrefix(command, perms.GetDeniedCommands()) {
		return fmt.Errorf("%s's %s denies %q, and its denied commands win over any allowance: a watcher cannot run it",
			name, file, command)
	}
	var b strings.Builder
	if declared, _ := LoadPermissions(project.GetDirectory()); declared != nil {
		fmt.Fprintf(&b, "%s's %s does not list %q among the commands its workers run without review", name, file, command)
	} else {
		fmt.Fprintf(&b, "%s lists no command its workers run without review (it has no %s, and a wish's allowance adds "+
			"none), so it does not let them run %q", name, file, command)
	}
	b.WriteString(", and a watcher has no reviewer. ")
	if declared := projectWatches(project); len(declared) > 0 {
		fmt.Fprintf(&b, "Djinn runs a command a skill of %s declares, as it builds it, without review: %s; this "+
			"command is not one of them. ", name, strings.Join(declared, "; "))
	} else {
		fmt.Fprintf(&b, "No skill of %s declares it: one that does, under metadata.djinn.babysit or metadata.djinn.wish "+
			"(docs/wish-templates.md), lets Djinn run the command it builds from it. ", name)
	}
	fmt.Fprintf(&b, "Or allow it for yourself in %s, in your own settings, never in the repository: djinn project "+
		"allow %s --watch %s", name, shellQuote(name), shellQuote(watchPrefix(command)))
	return fmt.Errorf("%s", b.String())
}

// projectWatches lists the watcher commands the project's own skills declare, each with how Djinn starts it.
func projectWatches(project *planv1.Project) []string {
	var out []string
	for _, s := range plan.ProjectSkills(project.GetDirectory()) {
		if b, err := plan.ReadBabysit(s.Dir); err == nil && b != nil {
			out = append(out, fmt.Sprintf("%q, the babysit of the skill %s, on a request Djinn finds (a task opens it, "+
				"or djinn task spawn --babysit)", b.Watch, s.Name))
		}
		if t, err := plan.ReadTemplate(s.Dir); err == nil && t != nil && t.Watch != "" {
			out = append(out, fmt.Sprintf("%q, the watcher of a wish made from the template of the skill %s", t.Watch, s.Name))
		}
	}
	return out
}

// watchPrefix is the start of command a developer would allow: its first plain words, a program and what it runs,
// before the first option, number, quote or variable; the whole command when that start is a single word, which would
// allow any command of the program.
func watchPrefix(command string) string {
	words := strings.Fields(command)
	n := 0
	for _, w := range words {
		plain := !strings.HasPrefix(w, "-") && strings.ContainsFunc(w, unicode.IsLetter) &&
			!strings.ContainsFunc(w, func(r rune) bool {
				return !(unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("-_./:@+", r))
			})
		if !plain {
			break
		}
		n++
	}
	if n < 2 {
		n = len(words)
	}
	return strings.Join(words[:n], " ")
}

// shellQuote quotes s for a POSIX shell, when it needs it.
func shellQuote(s string) string {
	if s != "" && !strings.ContainsFunc(s, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("-_./:@+=", r))
	}) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

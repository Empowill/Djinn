package gate

import (
	"path"
	"strings"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
)

// A project declares gates in its djinn.yaml (djinn.v1.ProjectGate, L17): the commands of its workers that rebuild a
// shared resource hold it alone, those that use it share it. Djinn matches each admitted command against them on its
// side (Admit has the command's key): the hook does not change. A declared gate goes by name@project, so that one
// project's stack is not another's; djinn gate run with its name, in the project, takes the same gate. Held alone, it
// is a true exclusivity: it waits for those that share it too, and one that comes to share it after one that waits
// to hold it alone goes behind it. The machine's bounds hold as for any command: its pressure, its memory, the turn of
// the heavy commands, Djinn's merges first.

// Declared are the gates a project declares, as Djinn applies them on this machine (plan.LoadProjectGates).
type Declared struct {
	// Project is the project's name, which the gates' names carry: stack@dolmen.
	Project string
	// File is the file that declares them, relative to the project: .agents/djinn.yaml.
	File string
	// Gates are the gates applied, in the file's order.
	Gates []*djinnv1.ProjectGate
}

// Declare sets what finds the gates declared by the project of a task, or of a folder outside any task: djinn up
// reads them on the project's reference branch, never in a task's worktree. Without it, no gate is declared.
func (g *Gates) Declare(fn func(taskID, dir string) Declared) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.declared = fn
}

// named is the gate d declares under name; nil when none.
func (d Declared) named(name string) *djinnv1.ProjectGate {
	if d.Project == "" {
		return nil
	}
	for _, gt := range d.Gates {
		if gt.GetName() == name {
			return gt
		}
	}
	return nil
}

// Match is the first gate of d whose patterns name the command what, and whether it holds it alone: a gate's alone
// patterns before its beside ones. ok is false when none does.
func (d Declared) Match(what string) (gt *djinnv1.ProjectGate, alone, ok bool) {
	if d.Project == "" {
		return nil, false, false
	}
	for _, gt := range d.Gates {
		if alone, ok := mode(gt, what); ok {
			return gt, alone, true
		}
	}
	return nil, false, false
}

// mode says whether a pattern of gt names what, and whether one of its alone patterns does.
func mode(gt *djinnv1.ProjectGate, what string) (alone, ok bool) {
	for _, p := range gt.GetAlone() {
		if Matches(p, what) {
			return true, true
		}
	}
	for _, p := range gt.GetBeside() {
		if Matches(p, what) {
			return false, true
		}
	}
	return false, false
}

// Matches says whether the pattern names command: whether its words, compared word by word, start one of the parts of
// command (split: at &&, ||, ;, | and new lines, quotes removed), past the variables the part sets and the words that
// run their arguments (env, time, nohup…). In the pattern, * stands for zero or more words; the program is compared
// by its base name (/usr/bin/task is task). A command split cannot read is named by no pattern.
func Matches(pattern, command string) bool {
	want := strings.Fields(pattern)
	if len(want) == 0 {
		return false
	}
	parts, ok := split(command)
	if !ok {
		return false
	}
	for _, words := range parts {
		for len(words) > 0 && (assignment(words[0]) || stripped[words[0]]) {
			words = words[1:]
		}
		if len(words) == 0 {
			continue
		}
		words = append([]string{path.Base(words[0])}, words[1:]...)
		if matchWords(want, words) {
			return true
		}
	}
	return false
}

// matchWords says whether the pattern's words want start words.
func matchWords(want, words []string) bool {
	if len(want) == 0 {
		return true
	}
	if want[0] == "*" {
		for i := 0; i <= len(words); i++ {
			if matchWords(want[1:], words[i:]) {
				return true
			}
		}
		return false
	}
	return len(words) > 0 && words[0] == want[0] && matchWords(want[1:], words[1:])
}

// declaredGate is the gate a request is for, as its project declares it: its name (name@project), whether it holds it
// alone, and how a waiter's reason names it, "stack (dolmen, .agents/djinn.yaml)"; ok is false when the project
// declares none for it. An admission takes the first gate whose patterns name its command; a gate asked by its name
// (djinn gate run stack, an ifrit's serve --gate stack) takes the one declared under it, alone when an alone pattern
// names its command, or when none of its patterns does and it would not be shared (shared, by Q17 B). Djinn's merges
// and pushes take no declared gate.
func (g *Gates) declaredGate(r Request, name string, shared bool) (full string, alone bool, label string, ok bool) {
	g.mu.Lock()
	find := g.declared
	g.mu.Unlock()
	if find == nil || r.First {
		return "", false, "", false
	}
	d := find(r.TaskID, r.Dir)
	var gt *djinnv1.ProjectGate
	if r.Shared {
		if gt, alone, ok = d.Match(r.What); !ok {
			return "", false, "", false
		}
	} else {
		if gt = d.named(name); gt == nil {
			return "", false, "", false
		}
		var named bool
		if alone, named = mode(gt, r.What); !named {
			alone = !shared
		}
	}
	label = gt.GetName() + " (" + d.Project
	if d.File != "" {
		label += ", " + d.File
	}
	return gt.GetName() + "@" + d.Project, alone, label + ")", true
}

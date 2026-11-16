package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// A worker nobody approves anything for (incident W1 of wish Dew, 12/10/2026: access native in a project whose agent
// settings allow few commands) has every other call refused, djinn's own included, and may still end done: the lead
// hears how many calls were refused and which, once, at the run's end, and decides (Q273): continue the task
// otherwise, run it again with a right the wish or project grants already, or drop it. The window never shows them:
// rights stay explicit, and a new one is the developer's to grant, asked by the lead in a --move question.

// denials are the calls a run's permissions refused.
type denials = planv1.TaskRefusals

// refusedShown is how many refused names the lead's line gives: a digest line keeps 160 characters.
const refusedShown = 3

// refuse counts the call what, as the worker's permissions refused it (permissionDenied), in the run's refusals.
func (r *run) refuse(what string) {
	if r.refusals == nil {
		r.refusals = &denials{}
	}
	r.refusals.Count++
	name := refusedName(what)
	switch {
	case slices.Contains(r.refusals.Names, name):
	case name == "djinn": // The one the worker needs to report: first.
		r.refusals.Names = slices.Insert(r.refusals.Names, 0, name)
	default:
		r.refusals.Names = append(r.refusals.Names, name)
	}
}

// refusedName is what the refused call what ran: the command's name for a shell call, the tool's otherwise.
func refusedName(what string) string {
	tool, input, _ := strings.Cut(strings.TrimSpace(what), " ")
	switch tool {
	case "Bash", "PowerShell":
		var call struct {
			Command string `json:"command"`
		}
		if json.Unmarshal([]byte(input), &call) == nil && call.Command != "" {
			input = call.Command
		}
		if name := commandName(input); name != "" {
			return name
		}
	case "commandExecution": // Codex's.
		if name := commandName(input); name != "" {
			return name
		}
	}
	return clipRunes(tool, 40)
}

// shellAssignment is a variable set before a command: SILAE_CLIENT=bardahl task test-pkg.
var shellAssignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// commandName is the name of the first command a shell line runs, past the variables it sets and the folders it goes
// to: "cd silae && ls -la" runs ls, "R=/x; git -C $R log" git; "" when it runs none.
func commandName(line string) string {
	segments := strings.FieldsFunc(line, func(c rune) bool { return strings.ContainsRune("&|;\n()", c) })
	for _, s := range segments {
		for _, w := range strings.Fields(s) {
			if shellAssignment.MatchString(w) || w == "env" || w == "sudo" {
				continue
			}
			if w == "cd" {
				break
			}
			return filepath.Base(strings.Trim(w, `"'{}`))
		}
	}
	return ""
}

// pathPermissions are the names of refused calls a path decides, not a command: agy's file tools refused a path
// outside the folders its project grants (agyDeniedPath). No mode grants them: auto judges commands only (W360).
var pathPermissions = []string{"read_file", "write_file"}

// pathsOnly tells whether every call t's worker had refused was refused for its path (pathPermissions).
func pathsOnly(t *planv1.Task) bool {
	names := t.GetRefused().GetNames()
	return len(names) > 0 && !slices.ContainsFunc(names, func(n string) bool { return !slices.Contains(pathPermissions, n) })
}

// refusedClause says the calls t's worker had refused in its last run, naming shown of them at most, and the
// command that would grant them, for the lead; "" when none was refused. An edit allowance adds no command
// (allowedBy): only auto lets the agent judge them, and no command grants a path (pathsOnly). refusedEnd ends the
// sentence.
func refusedClause(t *planv1.Task, wish *planv1.Wish, shown int) string {
	r := t.GetRefused()
	if r.GetCount() == 0 {
		return ""
	}
	noun := "commands"
	switch {
	case pathsOnly(t) && r.GetCount() == 1:
		noun = "file access"
	case pathsOnly(t):
		noun = "file accesses"
	case r.GetCount() == 1:
		noun = "command"
	}
	clause := fmt.Sprintf("%d %s refused (permission)", r.GetCount(), noun)
	if names := r.GetNames(); shown > 0 {
		clause += ": " + strings.Join(names[:min(len(names), shown)], ", ")
		if len(names) > shown {
			clause += ", …"
		}
	}
	if pathsOnly(t) {
		return clause
	}
	clause += "; djinn wish allow " + t.GetWishId() + " --mode auto"
	if len(wish.GetProjectIds()) > 1 && t.GetProjectId() != "" {
		clause += " --project-id " + t.GetProjectId()
	}
	return clause
}

// refusedRight ends the sentence of refusedClause: the command is a right, the developer's to grant.
const refusedRight = " would let the agent judge them, a right only the developer grants."

// refusedPaths ends the sentence of refusedClause for paths only: no mode grants them.
const refusedPaths = ", paths outside the folders Djinn opens to the worker: no mode grants them."

// refusedEnd ends the sentence of refusedClause for t.
func refusedEnd(t *planv1.Task) string {
	if pathsOnly(t) {
		return refusedPaths
	}
	return refusedRight
}

// tellsDone tells whether the lead hears of t's end as done from tellDone: a work worker, not a watcher, a question
// worker, an azima, nor a correction or a review worker, whose end the integration tells.
func tellsDone(t *planv1.Task) bool {
	return t.GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE && t.GetWishId() != "" && !watching(t) && !light(t) &&
		!plan.IsAzima(t) && t.GetCorrection() == nil && t.GetReview() == nil
}

// toldDoneAtEnd tells whether the lead heard of the work worker t's end when it ended (tellDone): its work goes to no
// integration, or its calls were refused. Otherwise the integration tells it: merged, or done when it brought nothing.
func toldDoneAtEnd(t *planv1.Task, integrate bool) bool {
	return !integrate || t.GetRefused().GetCount() > 0
}

// tellDone tells the lead of t's wish that its work worker ended done, with the first line of its final note and the
// calls its permissions refused, its final note whole apart; why, when not empty, says what came of its work
// ("nothing to merge"); stopped, when not empty, why Djinn stopped its worker (silent.go).
func (h *Harness) tellDone(ctx context.Context, t *planv1.Task, why, stopped string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	wish, _ := store.Get[*planv1.Wish](ctx, h.store, t.GetWishId())
	note := h.finalNote(ctx, t.GetId())
	line := doneLine(t, wish, firstLine(note), why, stopped)
	if err := h.toLeadNote(ctx, planv1.LeadEventKind_LEAD_EVENT_KIND_ENDED, t.GetWishId(), t.GetCode(), line,
		note); err != nil && !errors.Is(err, plan.ErrNoLead) {
		logx.L().Warn("tell the lead a task is done", "wish", t.GetWishId(), "task", t.GetCode(), "err", err)
	}
}

// doneLine is the line that tells a lead the work worker t ended done: why Djinn stopped it when it did, what was
// refused first, with as many names as let the digest keep the whole command, then its note; why, when nothing was
// refused, says what came of its work.
func doneLine(t *planv1.Task, wish *planv1.Wish, note, why, stopped string) string {
	line := "Djinn: " + t.GetCode() + " done"
	if stopped != "" {
		line += " (" + stopped + ": Djinn stopped it)"
	}
	if t.GetRefused().GetCount() > 0 {
		shown := refusedShown
		for shown > 0 && !plan.DigestKeeps(&planv1.LeadEvent{
			Kind: planv1.LeadEventKind_LEAD_EVENT_KIND_ENDED, Code: t.GetCode(), Text: line + ", " + refusedClause(t, wish, shown),
		}) {
			shown--
		}
		line += ", " + refusedClause(t, wish, shown) + refusedEnd(t)
	} else if why != "" {
		line += ", " + why + "."
	}
	switch {
	case note == "":
		return strings.TrimSuffix(line, ".") + ". It left no final note."
	case strings.HasSuffix(line, "."):
		return line + " Its final note: " + note
	}
	return line + ": " + note
}

// finalNote is the last thing the worker of the task wrote, where it says what it did, whole; "" when it wrote
// nothing.
func (h *Harness) finalNote(ctx context.Context, taskID string) string {
	var last string
	err := store.Latest(ctx, h.store, store.Where{"task_id": taskID}, func(e *planv1.TaskEvent) bool {
		if e.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_TEXT && strings.TrimSpace(e.GetText()) != "" {
			last = e.GetText()
		}
		return last == ""
	})
	if err != nil {
		return ""
	}
	return strings.TrimSpace(last)
}

// spawnNote says what the spawner of t should know of where it runs: with its agent's own configuration (access
// native), a claude worker whose settings do not allow djinn cannot run it, nor report with it, and nobody approves
// a call headless. "" when nothing is to say, or Djinn cannot tell (another agent's configuration).
func (h *Harness) spawnNote(ctx context.Context, t *planv1.Task) string {
	if t.GetProjectId() == "" || t.GetProvider() != planv1.Provider_PROVIDER_CLAUDE || light(t) || watching(t) {
		return ""
	}
	wish, err1 := store.Get[*planv1.Wish](ctx, h.store, t.GetWishId())
	project, err2 := store.Get[*planv1.Project](ctx, h.store, t.GetProjectId())
	if err1 != nil || err2 != nil {
		return ""
	}
	access, _, err := decideAccess(h.home, project, t.GetProvider(), plan.AllowanceOf(wish, project.GetId()))
	if err != nil || access != planv1.TaskAccess_TASK_ACCESS_NATIVE {
		return ""
	}
	home, _ := os.UserHomeDir()
	if claudeAllowsDjinn(project.GetDirectory(), home) {
		return ""
	}
	allow := "djinn wish allow " + wish.GetId()
	if len(wish.GetProjectIds()) > 1 {
		allow += " --project-id " + project.GetId()
	}
	return fmt.Sprintf("%s runs with claude's own settings in %s (access native), which allow no djinn command: nobody "+
		"approves a call headless, so it cannot report with djinn; %s --mode auto lets the agent judge its calls.",
		t.GetCode(), project.GetName(), allow)
}

// claudeAllowsDjinn tells whether claude's settings, the project's in dir (the ones a worktree holds, not the local
// ones Git ignores) or the user's in home, let a worker run djinn without asking: a rule for Bash or for djinn, or a
// mode that asks nobody.
func claudeAllowsDjinn(dir, home string) bool {
	paths := []string{filepath.Join(dir, ".claude", "settings.json")}
	if home != "" {
		paths = append(paths, filepath.Join(home, ".claude", "settings.json"))
	}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var s struct {
			Permissions struct {
				Allow       []string `json:"allow"`
				DefaultMode string   `json:"defaultMode"`
			} `json:"permissions"`
		}
		if json.Unmarshal(b, &s) != nil {
			continue
		}
		if m := s.Permissions.DefaultMode; m == "auto" || m == "bypassPermissions" {
			return true
		}
		for _, rule := range s.Permissions.Allow {
			if rule == "Bash" || rule == "Bash(*)" || strings.HasPrefix(rule, "Bash(djinn") {
				return true
			}
		}
	}
	return false
}

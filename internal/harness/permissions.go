package harness

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// PermissionsFile is where a project declares, for every agent, what an agent may do in it: a djinn.v1.Permissions
// in text protobuf, under the .agents folder that Codex and Antigravity already read for skills.
var PermissionsFile = filepath.Join(".agents", "permissions.txtpb")

// LoadPermissions reads the permissions a project declares in dir. It returns nil, and no error, when the project
// declares none.
func LoadPermissions(dir string) (*djinnv1.Permissions, error) {
	b, err := os.ReadFile(filepath.Join(dir, PermissionsFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p := &djinnv1.Permissions{}
	if err := prototext.Unmarshal(b, p); err != nil {
		return nil, fmt.Errorf("%s: %w", PermissionsFile, err)
	}
	if err := protovalidate.Validate(p); err != nil {
		return nil, fmt.Errorf("%s: %w", PermissionsFile, err)
	}
	return p, nil
}

// effectiveMode is the mode the agent gets: AUTO only with edit, since a reviewer could approve a command that
// writes; LISTED otherwise.
func effectiveMode(p *djinnv1.Permissions) djinnv1.Mode {
	if p.GetMode() == djinnv1.Mode_MODE_AUTO && p.GetEdit() {
		return djinnv1.Mode_MODE_AUTO
	}
	return djinnv1.Mode_MODE_LISTED
}

// editOnly is what a yes to a task's edit question gives: editing the project's files, no command, no network.
func editOnly() *djinnv1.Permissions {
	return &djinnv1.Permissions{Edit: true, Mode: djinnv1.Mode_MODE_LISTED}
}

// djinnCommands are the commands a question worker may run: djinn's, to read the wish and to plan from a question,
// and Git's to read the history. No gate, no build, no task stopped or continued.
var djinnCommands = []string{
	"djinn wish brief", "djinn task list", "djinn task get", "djinn question list", "djinn block list",
	"djinn mark list", "djinn tilasm list", "djinn tilasm get", "djinn task spawn", "djinn question ask",
	"djinn question revise", "djinn block put",
	"git log", "git show", "git diff", "git status",
}

// djinnOnly is what a question worker gets (TASK_ACCESS_DJINN): reading, and djinnCommands; no edit, no network.
func djinnOnly() *djinnv1.Permissions {
	return &djinnv1.Permissions{Commands: djinnCommands, Mode: djinnv1.Mode_MODE_LISTED}
}

// commitCommands are what a worker Djinn starts to commit work, a correction or a review worker, may run besides what
// the project lists: it commits what belongs to the task in its worktree, and drops the rest. Other workers never
// commit. The project's denied_commands still win.
var commitCommands = []string{"git status", "git diff", "git log", "git add", "git commit", "git restore", "git rm", "git clean"}

// withCommit is perms with commitCommands for t when it is a correction or a review worker that may edit; perms as it
// is otherwise.
func withCommit(t *planv1.Task, perms *djinnv1.Permissions) *djinnv1.Permissions {
	if perms == nil || !perms.GetEdit() || t.GetCorrection() == nil && t.GetReview() == nil {
		return perms
	}
	perms = proto.CloneOf(perms)
	for _, c := range commitCommands {
		if !slices.Contains(perms.GetCommands(), c) {
			perms.Commands = append(perms.Commands, c)
		}
	}
	return perms
}

// matchesPrefix tells whether command starts with one of the prefixes, on a word boundary.
func matchesPrefix(command string, prefixes []string) bool {
	command = strings.Join(strings.Fields(command), " ")
	return slices.ContainsFunc(prefixes, func(p string) bool {
		return command == p || strings.HasPrefix(command, p+" ")
	})
}

// commandAllowed tells whether p lets an agent run command without review: listed, and not denied.
func commandAllowed(p *djinnv1.Permissions, command string) bool {
	return matchesPrefix(command, p.GetCommands()) && !matchesPrefix(command, p.GetDeniedCommands())
}

// agentFiles are the files that configure an agent in a project, by provider, besides the ones every agent reads
// (neutralAgentFiles). Their presence is what counts, at the root of the project's folder, without regard to case:
// whatever they say, the project was set up for agents, and the agent's own configuration decides.
var agentFiles = map[planv1.Provider][]string{
	planv1.Provider_PROVIDER_CLAUDE: {
		"CLAUDE.md", "CLAUDE.local.md", ".claude/CLAUDE.md", ".claude/settings.json", ".claude/settings.local.json",
	},
	planv1.Provider_PROVIDER_CODEX:       {"AGENTS.override.md", ".codex"},
	planv1.Provider_PROVIDER_ANTIGRAVITY: {"GEMINI.md"},
}

// neutralAgentFiles configure every agent: AGENTS.md, which Claude, Codex and Antigravity read, and the .agents
// folder (skills, permissions).
var neutralAgentFiles = []string{"AGENTS.md", ".agents"}

// hasAgentConfig tells whether the project in dir holds a file that configures the agent kind, or every agent.
func hasAgentConfig(dir string, kind planv1.Provider) bool {
	for _, name := range append(slices.Clone(neutralAgentFiles), agentFiles[kind]...) {
		if existsFold(dir, name) {
			return true
		}
	}
	return false
}

// existsFold tells whether the relative path name exists under dir, comparing each element without regard to
// case.
func existsFold(dir, name string) bool {
	for part := range strings.SplitSeq(name, "/") {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return false
		}
		i := slices.IndexFunc(entries, func(e fs.DirEntry) bool { return strings.EqualFold(e.Name(), part) })
		if i < 0 {
			return false
		}
		dir = filepath.Join(dir, entries[i].Name())
	}
	return true
}

// decideAccess says where the rights of a worker of kind come from in project (nil outside any project), under
// the allowance its wish gives in that project, and the permissions Djinn translates for it, if any. The order
// (docs/providers.md): outside any project, read-only; the wish's allowance; the project's .agents/permissions.txtpb
// when it has one; the agent's own configuration in a Git repository, or in a folder holding agent configuration
// files; otherwise, read-only until the developer allows editing.
func decideAccess(project *planv1.Project, kind planv1.Provider, allowance planv1.Allowance) (planv1.TaskAccess, *djinnv1.Permissions, error) {
	if project == nil {
		return planv1.TaskAccess_TASK_ACCESS_READ_ONLY, nil, nil
	}
	p, err := LoadPermissions(project.GetDirectory())
	switch {
	case err != nil:
		return planv1.TaskAccess_TASK_ACCESS_UNSPECIFIED, nil, err
	case allowance == planv1.Allowance_ALLOWANCE_EDIT || allowance == planv1.Allowance_ALLOWANCE_AUTO:
		access, perms := allowedBy(p, allowance)
		return access, perms, nil
	case p != nil:
		return planv1.TaskAccess_TASK_ACCESS_AGENTS, p, nil
	case project.GetGit(), hasAgentConfig(project.GetDirectory(), kind):
		return planv1.TaskAccess_TASK_ACCESS_NATIVE, nil, nil
	}
	return planv1.TaskAccess_TASK_ACCESS_ASKING, nil, nil
}

// allowedBy are the permissions a wish's allowance gives, over the project's declared ones (nil when it declares
// none): the allowance decides editing and the mode; the commands, denied commands and network stay the project's.
// An allowance never adds a command nor the network.
func allowedBy(declared *djinnv1.Permissions, allowance planv1.Allowance) (planv1.TaskAccess, *djinnv1.Permissions) {
	p := &djinnv1.Permissions{}
	if declared != nil {
		p = proto.CloneOf(declared)
	}
	p.Edit = true
	if allowance == planv1.Allowance_ALLOWANCE_AUTO {
		p.Mode = djinnv1.Mode_MODE_AUTO
		return planv1.TaskAccess_TASK_ACCESS_WISH_AUTO, p
	}
	p.Mode = djinnv1.Mode_MODE_LISTED
	return planv1.TaskAccess_TASK_ACCESS_WISH_EDIT, p
}

// accessSpec is what a task's access gives its worker's spec: read-only, or the permissions to translate (nil
// when the agent's own configuration decides). perms are the ones decideAccess gave, for AGENTS and the wish's
// allowances.
func accessSpec(access planv1.TaskAccess, perms *djinnv1.Permissions) (readOnly bool, out *djinnv1.Permissions) {
	switch access {
	case planv1.TaskAccess_TASK_ACCESS_AGENTS, planv1.TaskAccess_TASK_ACCESS_WISH_EDIT, planv1.TaskAccess_TASK_ACCESS_WISH_AUTO:
		return false, proto.CloneOf(perms)
	case planv1.TaskAccess_TASK_ACCESS_NATIVE:
		return false, nil
	case planv1.TaskAccess_TASK_ACCESS_EDIT_GRANTED:
		return false, editOnly()
	case planv1.TaskAccess_TASK_ACCESS_DJINN:
		return false, djinnOnly()
	}
	return true, nil
}

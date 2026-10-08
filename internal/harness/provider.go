// Package harness runs workers: one agent process per task, in the task's own worktree, followed and recorded in
// the store as it goes. A provider (Claude, a fake for tests) knows how to start its agent and read what it says;
// the harness does the rest the same way for all of them.
package harness

import (
	"context"
	"errors"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// Provider starts the workers of one kind of agent.
type Provider interface {
	// Start starts a worker. Cancelling ctx stops it, as Worker.Stop does.
	Start(ctx context.Context, spec Spec) (Worker, error)
}

// Spec is what a worker is started with.
type Spec struct {
	// TaskID is the task the worker works on.
	TaskID string
	// Dir is the folder the worker runs in: its project's worktree or folder, or an empty folder of its own.
	Dir string
	// ReadOnly says the worker may read documents, never change a file nor run a command or code: outside any
	// project, or in a folder outside Git without agent configuration until the developer allows editing.
	ReadOnly bool
	// Permissions, when set, are what the worker may do in its project, the same for every agent: the provider
	// translates them for its agent at launch, never allowing more, and writes nothing in the project. Nil in a
	// project lets the agent's own configuration of the project decide, and Djinn passes no permission setting.
	// Ignored when ReadOnly.
	Permissions *djinnv1.Permissions
	// Prompt is the first message to the agent.
	Prompt string
	// Model is the agent's model; empty for the provider's default.
	Model string
	// MaxBudgetUSD caps what the worker may spend, when the provider can enforce it; 0 for no cap.
	MaxBudgetUSD float64
	// Resume is a session to continue instead of starting a new one. Not used yet: warm workers.
	Resume string
	// Fork starts a new session from Resume instead of continuing it. Not used yet: see Q28.
	Fork bool
	// Env holds variables added to the environment Djinn passes on to the worker. Djinn never reads the
	// environment it passes, nor records it.
	Env []string
	// Skills are the skills the project summons from other projects, found on this machine. The provider shows
	// them to its agent by the agent's own path, and writes nothing in the project nor in the user's folders.
	Skills []Skill
	// SkillsDir is a folder of Djinn's own holding .claude/skills/<name> and .agents/skills/<name>, a link to each
	// skill's folder in its source project; empty without skills, or when the links could not be made.
	SkillsDir string
}

// Skill is a skill summoned from another project: its folder stays in its source, and the worker reads it there.
type Skill struct {
	// Name is the skill's folder name.
	Name string
	// Source is where it comes from: <project>/<skill>.
	Source string
	// Dir is its folder in the source project, absolute.
	Dir string
	// Description is what it does, from its SKILL.md.
	Description string
}

// Worker is one running agent.
type Worker interface {
	// Events are what the worker says, in order. The channel closes when the worker ends.
	Events() <-chan Event
	// Send gives the agent another message, when its provider keeps the agent open between turns. It returns
	// ErrClosed once the worker no longer takes any.
	Send(text string) error
	// Stop asks the worker to stop, and kills it if it has not after the grace delay. It does not wait.
	Stop()
	// Wait blocks until the worker has ended and its events are all read, then says how it ended.
	Wait() Result
}

// ErrReadOnly is returned by Start when the provider cannot keep its agent from writing: it cannot run a
// read-only worker.
var ErrReadOnly = errors.New("it cannot be kept from writing")

// ErrClosed is returned by Send when the worker takes no more messages.
var ErrClosed = errors.New("the worker takes no more messages")

// Event is something a worker said. Text and Raw stay free; Kind is what Djinn makes of it.
type Event struct {
	Kind planv1.TaskEventKind
	// Text is the event for a reader.
	Text string
	// Raw is the provider's line as it came, on the first event made from it.
	Raw string
	// Usage is what the worker spent so far in this run, on a usage event.
	Usage *planv1.Usage
	// SessionID is the agent's session, when the event tells it.
	SessionID string
}

// Result is how a worker ended.
type Result struct {
	// ExitCode is the exit code of the process; -1 when it was killed by a signal.
	ExitCode int
	// Err is why the worker failed, when it did: its process could not run, or the agent reported an error.
	Err error
}

# Djinn agent protocol

How agents work with Djinn. The lead drives a wish through the `djinn` command. Workers are agent processes Djinn
starts and reads. No agent writes in Djinn's data folder, and no model decides what Djinn computes.

## The lead: the command line

The lead is the agent the developer talks to. It changes the plan with `djinn`, never by hand.

- **Every command comes from a proto.** Each public method of `api/` is a command: `QuestionService.Answer` is
  `djinn question answer`. Arguments, help and validation follow the [convention](cli-convention.md). Exit codes:
  `0` success, `1` the call failed, `2` the command line is wrong.
- **The brief says how to lead.** `djinn wish brief <wish>` prints Djinn's rules, the projects' rules and where the
  wish stands. Djinn writes it from its store (`internal/plan/brief.go`), without a model.
- **A lead runs in a project, never in the home folder.** `djinn wish resume` resumes the lead's session in the
  folder it was recorded in (`djinn wish set-lead --directory`): claude finds a session only from the folder it was
  made in. A new lead starts in the wish's first project, else the first of Djinn's projects as the window lists them.
  A session recorded in the home folder, or a folder above it, is not resumed: a new lead starts from the brief, in a
  project. With no project, no lead starts, and the error says to create one (`internal/plan/lead.go`).
- **Questions.** `djinn question ask` offers up to four options, answered by letter, or none for a yes. An answered
  question is a decision. On options that say yes and no, `yes` and `no` pick them too. Start a recommendation with
  its option's letter (`B: …`): the developer applies it in one click, "Rub the lamp".
- **Investigations.** "Enlighten me" in the window (`djinn question enlighten <question> --note …`) asks the lead to
  find out more before deciding: the question waits for the lead, and the brief lists it under "To investigate". The
  lead answers with `djinn question revise <question> --context … --recommendation …`; the question keeps each round,
  dated, and waits for the developer again. A decision may take several rounds.
- **Marks.** What the developer read or approved as it is, from the window: `djinn mark list <wish>`, and the brief's
  "Marked by the developer". An approved block or decision is a go.
- **An answer reaches the lead.** When the developer answers a question, Djinn types one line in the terminal of
  the wish's lead, then Enter: `Djinn: Q43 answered B — "<option>". Note: "<note>". Act on it: djinn wish brief
  <wish> has the context.` The agent reads it as a message, queued while it works. The line waits until the
  developer has not typed in that terminal for three seconds, and lines go out in order. A lead that does not run is
  reopened on its session first, as `djinn wish resume` does, without taking the window; only an active wish's.
  A wish without a lead session keeps the answer in its brief's decisions (`internal/plan/tell.go`).
- **Tasks.** `djinn task spawn` starts a worker. A task that cannot start yet waits, and says why.
  `djinn task watch <task>` follows its events; `djinn task stop <task>` stops it.
- **The plan is a graph of azimas.** An azima (Arabic ʿazīma, the incantation that binds and commands a djinn) is a
  part of the plan, coded `T07`: a task of kind `AZIMA` that no worker runs, the scheduler never starts, and that never
  waits for the developer. `djinn plan sync <wish>` reads them from the projects' `plan/*.md` and writes each one's
  `after:` line back from the store; `djinn task spawn <wish> --kind azima --title "…"` makes one. The lead plans work
  as a graph, never as a line:
  - spawn each task `--part-of <azima>`: the azima it belongs to, a grouping and never a wait;
  - give it `--depends-on` only the tasks whose result it needs, several if need be: two tasks that do not need each
    other run side by side;
  - work on the ready azimas first, the brief's "Azimas" section: an azima is ready once every task it depends on is
    done, in progress once a part runs or is done, done once marked done (`djinn task done`) or its file says so;
  - re-sequence as the plan learns: `djinn task depend <task> --depends-on …` replaces what a task waits for, `djinn
    task group <task> --part-of T07` its azima. Djinn refuses a cycle, through dependencies and azimas together,
    naming it.
- **Watchers.** To wait on something outside, the lead spawns a watcher instead of polling:
  `djinn task spawn <wish> --title … --provider watch --prompt "<command>"` runs the command, with no agent, no model
  and no slot, and each new paragraph it prints wakes the lead. `--restart` starts again a command that exits on each
  change ([watch](providers.md#watch-a-command-no-agent)).
- **Every request finds its wish.** A request that is not about the wish goes through
  `djinn wish route "<request>" --wish-id <wish> --ask`: a card asks the developer to open a new wish, whose lead
  starts on it, to file it in an existing wish, or to keep it in this one; then the lead does its work. The new lead reads the request once, on its first line; its brief leaves
  the request block out of its latest blocks. A skill's [wish template](wish-templates.md) (`metadata.djinn.wish`)
  makes the new wish follow the skill, its watcher started; the brief tells the lead to propose one for a request
  that comes back.
- **Blocks.** What Djinn does not compute (an analysis, a hand-off, a decision taken outside a question) is a block:
  `djinn block put`. Djinn shows it as written.
- **The page.** `djinn wish sync <wish>` renders the wish's page in Go and prints its file, kept up to date. The lead
  republishes that file as it is; it never writes the HTML.
- **The developer's word.** `djinn wish grant <wish>` says a wish is done. Only the developer says it.

## Workers: processes Djinn starts

A worker is one agent process per task: Claude Code, Codex or Antigravity, in the task's own worktree. The commands
Djinn runs, what a worker may do, and how each agent signs in: [`providers.md`](providers.md).

Djinn reads each worker's stream into the same task events, stored with the task and streamed by
`djinn task watch` (`--raw` adds the provider's own line):

| Event         | What it holds                                                    |
| ------------- | ---------------------------------------------------------------- |
| `PROMPT`      | What Djinn asked the worker.                                     |
| `STATUS`      | The worker started, ended or changed state.                      |
| `TEXT`        | Text the agent wrote.                                            |
| `TOOL_CALL`   | A tool the agent called, and its input.                          |
| `TOOL_RESULT` | What the tool returned.                                          |
| `USAGE`       | What the worker spent so far.                                    |
| `ERROR`       | An error the agent or its process reported.                      |
| `LOG`         | A line of the process's error output.                            |
| `GATE`        | The task waits for a shared resource, takes it or gives it back. |
| `OTHER`       | Anything else the provider sent, kept raw.                       |

The kinds are typed in `api/plan/v1/plan.proto` (`TaskEventKind`); their content stays free text. Tests never call
a model: every provider case is a recorded stream, replayed through the real process path.

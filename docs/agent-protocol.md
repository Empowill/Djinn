# Djinn agent protocol

How agents work with Djinn. The lead drives a wish through the `djinn` command. Workers are agent processes Djinn
starts and reads. No agent writes in Djinn's data folder, and no model decides what Djinn computes.

## The lead: the command line

The lead is the agent the developer talks to. It changes the plan with `djinn`, never by hand.

- **Every command comes from a proto.** Each public method of `api/` is a command: `QuestionService.Answer` is
  `djinn question answer`. Arguments, help and validation follow the [convention](cli-convention.md). Exit codes:
  `0` success, `1` the call failed, `2` the command line is wrong.
- **The brief says where the wish stands, and how to lead it.** `djinn wish brief <wish>` is a status on its own,
  computed by Djinn from its store (`internal/plan/brief.go`), without a model: the wish's description first, then its
  azimas and their states, the tasks running and waiting, the open questions, the latest decisions and blocks, and
  the last lead (its agent, its session, when a lead last acted through Djinn); then Djinn's rules and the projects'
  rules. Reconstructing the state is not the lead's job.
- **A wish describes itself.** Its description is a few lines: what it is for, its scope, where it goes.
  `djinn wish describe <wish> --text "…"` sets it; the developer edits it in the wish's head, under its title (a
  click, saved when the field is left). Until someone writes one, the title stands for it.
- **The lead asks through Djinn, and delegates.** Every question for the developer, the lead's own included, goes
  through `djinn question ask`: one written in the lead's terminal reaches neither the window, nor the decision log,
  nor the question workers. An analysis goes in a block, a decision taken in the terminal in a block of kind decision;
  the work goes to tasks. Djinn's rules in the brief say it, and so does every lead's first message.
- **Every lead starts the same way, whatever its agent.** Its first message is one line, the same for claude, codex
  and antigravity: run `djinn wish brief <wish>`, then continue the wish from what it says (`StartLine` in
  `internal/plan/newlead.go`). Nothing in it is about one agent or the wish's state: a lead of another agent than the
  last one takes the wish over from the brief, without the old session. In the window, the arrow beside **Lead** lists
  the agents found on this machine (`UiService.GetEnvironment`: `claude`, `codex`, `agy`), those missing shown
  disabled; picking the recorded lead's agent resumes its session, another starts a new lead of that agent in the
  lead's terminal (`djinn wish resume <wish> --provider antigravity`). A new claude lead becomes the wish's lead; a
  codex one once `djinn wish set-lead … --provider codex` gives its session; an antigravity one leaves the record as
  it is. While the lead's terminal runs a program, nothing starts, and Djinn says to exit it there.
- **A lead runs in a project, never in the home folder.** `djinn wish resume` resumes the lead's session in the
  folder it was recorded in (`djinn wish set-lead --directory`): claude finds a session only from the folder it was
  made in. A new lead starts in the wish's first project, else the first of Djinn's projects as the window lists them.
  A session recorded in the home folder, or a folder above it, is not resumed: a new lead starts from the brief, in a
  project. With no project, no lead starts, and the error says to create one (`internal/plan/lead.go`).
- **Questions.** `djinn question ask` offers up to four options, answered by letter, or none for a yes. An answered
  question is a decision. On options that say yes and no, `yes` and `no` pick them too. Start a recommendation with
  its option's letter (`B: …`): the developer applies it in one click, "Rub the lamp". `--before "before the merge"`
  says what the answer is needed before; without it the question can wait. A question a waiting task needs is
  blocking whatever it says: the window, the page and the brief list blocking, then before X, then can wait.
- **Investigations.** "Enlighten me" in the window (`djinn question enlighten <question> --note …`) asks to find out
  more before deciding: the question waits for a revision, and the brief lists it under "To investigate". Its
  question worker (below), or else the lead, answers with `djinn question revise <question> --context …
  --recommendation …`; the question keeps each round, dated, with who revised it, and waits for the developer again.
  A decision may take several rounds.
- **Marks.** What the developer read or approved as it is, from the window: `djinn mark list <wish>`, and the brief's
  "Marked by the developer". An approved block or decision is a go.
- **Question workers act on the developer's word; the lead stays informed.** When the developer answers a question
  ("Rub the lamp", or an answer) that Djinn does not settle itself (an edit question, a question on work that failed
  to integrate, a routed request, a grant), Djinn starts a small task in the wish, `Q43 → tasks`, a *converter*: its
  prompt holds the question, its options, the answer, the developer's note and where the wish stands, and one job, to
  turn the decision into tasks (`djinn task spawn … --decision Q43`, with `--part-of`, `--after`, a clear title and a
  self-contained prompt each), or to ask what the answer leaves open (`djinn question ask`: an answered question is a
  decision, it is not revised). "Enlighten me" starts `Q43: enlighten`, an *investigator*: it reads the code and the
  brief, then revises the question. A question worker writes no code and runs no gate: Djinn gives it
  `TASK_ACCESS_DJINN`, reading plus a short list of `djinn` and read-only `git` commands
  (`internal/harness/permissions.go`), in its project's folder, without a worktree. It takes no slot of the machine,
  like a watcher, but waits while the machine is under pressure or its memory would not hold it, like any agent. One
  works on a question at a time: a second answer while the converter works is sent to it (`djinn task send`), not
  given to a second one. Its model and budget are project settings, `question_model` (default `sonnet` for claude, the
  provider's own default otherwise) and `question_budget_usd` (default $2); `question_workers: false` in a project's
  settings, or `djinn up --question-workers=false` (`DJINN_QUESTION_WORKERS=off`), turns them off
  (`docs/team-settings.md`).
- **An answer reaches the lead.** When the developer answers a question, Djinn types one line in the terminal of
  the wish's lead, then Enter: `Djinn: Q43 answered B — "<option>". Note: "<note>". W12 turns it into tasks; you
  will hear when it ends.` When the converter ends, a second line says what it did: `Djinn: W12 (Q43 → tasks) ended:
  spawned W13, W14 from Q43; asked Q44.`, or what is left to the lead when it did nothing or failed. A question
  worker that ends without spawning, asking or revising anything after its access refused a call has failed, the
  refusal named, and Djinn starts it again once, told what was refused: `Djinn: W12 (Q43 → tasks) failed: its command
  was refused: djinn task list … | grep …. Djinn starts it again: W13, …`. The lead acts only if the retry gives up
  too. Without question
  workers the first line ends `Act on it: djinn wish brief <wish> has the context.` The agent reads each line as a
  message, queued while it works. The line waits until the
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
  - give it what comes before it at its spawn, `--after W1,W2`, only the tasks whose result it needs: two tasks
    that do not need each other run side by side;
  - insert a new task before a planned one with `--blocks W5`: W5 waits for it from the same step, so no pass of the
    scheduler starts W5 in between. Never spawn, then depend: that leaves the gap. `--blocks` is refused on a task
    that has started (running, paused, done…), saying which, and on one that would close a cycle;
  - work on the ready azimas first, the brief's "Azimas" section: an azima is ready once every task it depends on is
    done, in progress once a part runs or is done, done once marked done (`djinn task done`), its file says so, or
    every Done-when box of its file is checked (`djinn plan sync` reports a file whose status line lags);
  - an azima under way awaits its proof (`AWAITING_PROOF`) once its work is finished (done, stopped, or cut short for
    good) and every unchecked Done-when box of its file says `(needs: …)`, or holds boxes that do: a person, a
    machine, a release or a real model gives the proof, never a worker. The brief lists these apart ("Work done,
    waiting for its proof: T01 needs a Mac, a person"); spawn no work for them. A box without needs is work left, and
    keeps its azima in progress;
  - re-sequence as the plan learns: `djinn task depend <task> --after … --also W6=W5,W3` replaces what several
    tasks wait for at once, all or none, `djinn task group <task> --part-of T07` its azima. Djinn refuses a cycle,
    through dependencies and azimas together, naming it, and then changes nothing. `--depends-on` is the former
    name of `--after`, still read.
- **Watchers.** To wait on something outside, the lead spawns a watcher instead of polling:
  `djinn task spawn <wish> --title … --provider watch --prompt "<command>"` runs the command, with no agent, no model
  and no slot, and each new paragraph it prints wakes the lead. `--restart` starts again a command that exits on each
  change ([watch](providers.md#watch-a-command-no-agent)).
- **Every request finds its wish.** A request that is not about the wish goes through
  `djinn wish route "<request>" --wish-id <wish> --ask`: a card asks the developer to open a new wish, whose lead
  starts on it, to file it in an existing wish, or to keep it in this one; then the lead does its work. The new lead
  reads the request on its first line, before the line that sends it to the brief. A skill's [wish template](wish-templates.md) (`metadata.djinn.wish`)
  makes the new wish follow the skill, its watcher started; the brief tells the lead to propose one for a request
  that comes back.
- **Blocks.** What Djinn does not compute (an analysis, a decision taken outside a question) is a block:
  `djinn block put`. Djinn shows it as written.
- **Who did it.** `djinn block put`, `djinn question ask` and `djinn question revise` take `--task-id`, by default
  `$DJINN_TASK_ID`, which Djinn sets for every worker (`(djinn.v1.env)` in the protos): a worker's decision block
  reads "By W12" in the decision log, its revision "Revised by W12" on the page; the lead's, run without the
  variable, stay the lead's.
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

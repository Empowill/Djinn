# Djinn agent protocol

How agents work with Djinn. Djinn's lead, the Djinn, drives a wish through the `djinn` command: the app is Djinn,
the lead is the Djinn, and workers are its hands, agent processes Djinn starts and reads. No agent writes in Djinn's
data folder, and no model decides what Djinn computes.

## The Djinn: the command line

The Djinn is the agent the developer talks to. It changes the plan with `djinn`, never by hand.

- **Every command comes from a proto.** Each public method of `api/` is a command: `QuestionService.Answer` is
  `djinn question answer`. Arguments, help and validation follow the [convention](cli-convention.md). Exit codes:
  `0` success, `1` the call failed, `2` the command line is wrong.
- **The brief says where the wishes stand, and how to lead them.** `djinn brief` gives the Djinn every active
  wish at once (each wish's title, description, open questions, what runs and waits, its azimas), then Djinn's rules
  once; paused and granted wishes in one line each. `djinn wish brief <wish>` gives one wish's detail (latest decisions,
  blocks, and the Djinn's last session). Reconstructing the state is not the Djinn's job. The brief starts with the rule
  on direct commands and ends with a line per active wish and that rule again: an agent whose tool keeps only the end
  of a long output (Antigravity's `run_command`) still sees both. `djinn brief --wishes` prints where the wishes stand
  without the rules.
- **The Djinn runs each command alone.** A pipe, a chain (`&&`, `||`, `;`), a loop, a redirection or a substitution
  around a `djinn` command makes the developer approve it by hand: the first message, the brief and the note
  `direct-commands` forbid them, and flags replace them (`djinn brief --wishes`, `djinn lead notes a,b` or `--all`,
  `djinn task list --short`, `djinn task watch --last 5`).
- **A wish describes itself.** Its description is a few lines: what it is for, its scope, where it goes.
  `djinn wish describe <wish> --text "…"` sets it; the developer edits it in the wish's head, under its title (a
  click, saved when the field is left). Until someone writes one, the title stands for it.
- **A wish made from its request keeps it whole.** `djinn wish make "<title>"` still makes a wish from a title.
  `djinn wish make --prompt "<Markdown>"` makes it from the developer's whole request, under a provisional title
  ("New wish", in the developer's language) unless a title comes with it. The request is a `creation` block, written
  in the transaction that makes the wish and journals the command, whose identifier the wish takes: its export carries
  exactly that creation. The brief shows it once, whole, under "Original request", outside the latest blocks and their
  limits, scrubbed like the rest. A `request` event is queued for the Djinn: it names a provisional wish with
  `djinn wish rename <wish> "<title>"`, in the developer's language, then plans it. Once renamed, the brief stops asking.
- **The Djinn asks through the window, and delegates.** Everything for the developer is a question, the Djinn's own
  included: `djinn question ask`, with options and a recommendation, or with none when the Djinn really does not know
  what to think, the analysis behind it in its `--context`. One written in the Djinn's terminal reaches neither the
  window, nor the decision log, nor the question workers. A decision taken in the terminal goes in a block of kind
  decision; the work goes to tasks. A resolved question is resolved: the Djinn does not bring it back to the
  developer, and asks again, a new question, only on a real doubt; an answer it does not fully understand (the
  choice, its note, how they fit) is one, and no task starts from it. Djinn's rules in the brief say it, and so does
  the first message of every session of the Djinn.
- **The Djinn says nothing in its terminal, ever.** The developer never reads it, and every word written there costs
  output tokens for nothing: the Djinn's turns end with no text at all, no recap, no question codes, no "done". Every report, finding, status or "done as you asked" is a question in
  the wish concerned, often a yes/no acknowledgement with "OK, nothing to do" as an option; a real wait on the
  developer is a `--move` question that says so. Never two questions for one gesture: the Djinn withdraws a question
  the moment a task or Djinn does what it asked. A line typed in the Djinn's terminal is a request like any other,
  answered in the window (`briefRules` in `internal/plan/brief.go`).
- **A question's title starts with its emoji.** One emoji tells its subject at a glance, then a space and the title
  (🚀 a merge or a release, 🔎 a review, 🔐 a right or a permission, 🧪 a check or a test, 📦 a build, 🌿 a branch,
  🧭 an organisation choice, 🪵 logs, ⚠️ an incident); the same emoji goes in `--icon`.
- **The Djinn's notes stay true.** `djinn lead notes` gives how to lead, learnt from past wishes (`lead/notes/`, built
  into Djinn); `djinn lead notes a,b` or `--all` prints several in one command. When a task fixes what a note
  describes, the Djinn updates or removes that note in the same breath, by a task that edits `lead/notes/`; no cleanup
  waits for later.
- **The Djinn starts the same way, whatever its agent.** Its first message is one line, the same for claude, codex
  and antigravity: run `djinn brief`, then continue from what it says (`StartLine` in
  `internal/plan/newlead.go`). Nothing in it is about one agent or a wish's state: the Djinn on another agent than the
  last one takes over from the brief, without the old session. In the window, the arrow beside **Talk to the Djinn** lists
  the agents found on this machine (`UiService.GetEnvironment`: `claude`, `codex`, `agy`), those missing shown
  disabled; picking the agent the Djinn last ran on resumes it (claude by session id, antigravity with `agy --continue` in the
  Djinn's folder as the folder's most recent conversation), another starts the Djinn anew on that agent in its
  terminal (`djinn lead resume --provider antigravity`). Resuming the Djinn resumes that session; starting it on
  another provider replaces the session. A new claude or antigravity session becomes the Djinn; a codex one once
  `djinn lead set … --provider codex` gives its session. While the Djinn's terminal runs a program, nothing starts, and the window says to exit it there.
- **The Djinn runs in a project, never in the home folder.** `djinn lead resume` resumes the Djinn's session in the
  folder it was recorded in (`djinn lead set --directory`): claude finds a session only from the folder it was
  made in. A new session of the Djinn starts in the first of Djinn's projects as the window lists them.
  A session recorded in the home folder, or a folder above it, is not resumed: a new session starts from the brief, in a
  project. With no project, the Djinn does not start, and the error says to create one (`internal/plan/lead.go`).
- **Questions.** `djinn question ask` offers up to four options, answered by letter, or none for a yes. An answered
  question is a decision. On options that say yes and no, `yes` and `no` pick them too. Start a recommendation with
  its option's letter (`B: …`): the developer applies it in one click, "Rub the lamp". `--before "before the merge"`
  says what the answer is needed before; without it the question can wait. A question a waiting task needs is
  blocking whatever it says: the window, the page and the brief list blocking, then before X, then can wait.
  `djinn question move <question> --wish <wish> [--follow]` moves an open question to another wish (retiring its code
  in this wish, taking the next code in the other; `--follow` moves tasks naming this question as their decision along
  with it). `djinn question withdraw <question> [--note "…"]` withdraws an open question without an answer.
  `djinn question later <question> [--clear]` puts an open question off (the window's Later): Djinn stores the time
  (`later_time`), and the window, the page and the brief list a wish's open questions by the later of when each was
  asked and when it was put off (`render.Place`, `byAsked`), oldest first; `--clear` gives it its place back.
- **Moves only the developer can make.** Granting a right, pushing a tag, opening or merging a PR, starting a
  release, checking on a Mac or Windows, installing and restarting: the Djinn asks them through `djinn question ask`,
  with the exact command in the question. In a project with `install_questions: never` (such as Djinn itself), Djinn
  never asks a question to install or restart (the window proposes each new build, to install or skip); with
  `install_questions: when_needed`, ask the developer to install or restart only when necessary, and say why in the
  question; never as routine.
- **Never ask the developer to check (Q246).** A check, a measure, a log to read, a CI result to look at, a command to
  run is never asked of the developer. A worker does it itself when it can, or says in its final note what remains to
  verify; the Djinn then spawns a task that runs it and investigates, or lets a watcher report it (a CI result comes
  from the PR's or MR's babysit watcher). A question to the developer is only for a decision or a move only a person
  can make (a right, a merge, a release, a machine Djinn does not have); then the exact command goes in the code
  block at the top of its `--context`, never in a sentence. Every worker that edits a worktree reads the rule at the
  end of its first prompt (`internal/harness/harness.go`, `workerRules`); the Djinn in its brief.
- **A question reads at a glance (W575).** Its context keeps one shape: every command the developer must run in one
  fenced code block at the top, in run order, a `# …` comment on what each prints; then a one-line why, a few bullets
  of facts (verified or not), the decision with its options; no command, flag, path or hash inside a sentence, no wall
  of text. The Djinn reads it in its brief, a worker at the end of its first prompt, an investigator in its own: one
  text, `plan.QuestionShape`. `djinn question ask` and `djinn question revise` store the context as given and print a
  `warning:` line for each break they spot (`plan.ContextWarnings`). The lead note `readable-questions` shows a
  context rewritten in this shape.
- **Operational decisions are the Djinn's (Decision Q196).** Djinn's operational questions (a merge still failing after its
  corrections, a held push, a conflict, a main merge that fails, uncommitted review failures) go to the Djinn as an event,
  never as a question to the developer. Obvious cases (work already in the integration branch, a task dropped or cancelled,
  or a correction stopped by the Djinn) are settled silently without raising questions. The Djinn decides: retry a
  correction, drop the work (`djinn task done --drop` or `djinn task cancel`), re-sequence the graph (`djinn task depend`),
  or leave it waiting. Questions to the developer are reserved for moves only a person can make (a tag, a PR, a release,
  a right) or a real choice. Djinn acts on a yes only to the move questions it asked itself, as each records its move
  (`djinn_move`: install the build it named, push the tag it named, open as a draft the pull request it proposed, or
  spawn a worker that opens it with the project's skill for it, mark an azima whose proof it asked for done), never
  by the words of a question; an answer to a worker's or the Djinn's move question is only noted (`Djinn noted move`).
- **When Djinn misbehaves.** When the app misbehaves while the developer is away, the Djinn may stop the damage without
  touching code: redirect the wish's integration to another branch, stop workers, withdraw useless questions. The Djinn
  may also revert a faulty merge on the integration branch, telling the developer at once; every other commit on the
  integration branch waits for the developer's agreement, asked first (`djinn question ask`).
- **Investigations.** "Enlighten me" in the window (`djinn question enlighten <question> --note …`) asks to find out
  more before deciding: the question waits for a revision, and the brief lists it under "To investigate". Its
  question worker (below), or else the Djinn, answers with `djinn question revise <question> --context …
  --recommendation …`; the question keeps each round, dated, with who revised it, and waits for the developer again.
  A decision may take several rounds.
- **Marks.** The open questions the developer marked read, from the window: `djinn mark list <wish>`, and the
  brief's "Marked by the developer". Approving an open question (`djinn mark put Q03 approved`) answers it with
  its recommendation. A task the developer reviewed, from the window's Review tab, is marked reviewed (`djinn mark put
  W3 reviewed`); a task takes no other mark. A block takes no mark: Djinn refuses one.
- **Investigators clarify questions; the Djinn turns answers into tasks.** Djinn does not turn answers into tasks
  with workers: the DAG is the Djinn's responsibility (keeping it free of conflicts so workers do not collide along an
  iterative and de-risked path), and the Djinn turns each answer into tasks itself, in one pass,
  placing them in the graph (`azimas`, `--after`, `--blocks`), and checks what an investigator revised.
  "Enlighten me" starts `Q43: enlighten` by default, an *investigator*: it reads the code and the brief, then revises
  the question, keeping the Djinn's context clean. The investigator starts quietly without typing to the Djinn's terminal
  (recorded in the journal and the window); the Djinn is told when it ends.
  An investigator writes no code and runs no gate: Djinn gives it `TASK_ACCESS_DJINN`, reading plus a short list of
  `djinn` and read-only `git` commands (`internal/harness/permissions.go`), in its project's folder, without a worktree.
  It takes a slot like any agent (the load bar counts every agent that runs, W577): on a full machine it takes the slot
  of the newest work task, which it pauses until it ends, or waits for a slot when none can be paused; it waits while
  the machine is under pressure or its memory would not hold it, like any agent. Its model and budget are project settings, `question_model` (default `sonnet` for claude,
  the provider's own default otherwise) and `question_budget_usd` (default $2). In settings, `enlighten_workers`
  (default `true`) controls them (`docs/team-settings.md`).
- **What reaches the Djinn (Decision Q173).** Djinn tells its lead, the Djinn, only what changes something:
  - a real merge where the branch moved (a no-op batch, its work already in the branch, tells no merge line;
    repeated merges of a pass go out as one coalesced event: `W12, W14, W15 merged into feat/foo as 1a2b3c4d`);
  - moves only when a question was actually asked on that merge (`: push tag v1.0.0`, `: start a release`);
  - an actionable failure (a work worker failed or was cut short; an integration failed; push checks stay red; a main merge failed);
  - an answer or question for the Djinn to act on (developer answers, an "Enlighten me" when enlighten workers are
    disabled, a filed request or inbox item, a wish made from a request, an azima opened to plan its tasks);
  - the developer's approval of a decision the Djinn or a worker took (a question they answered); approving an open
    question is its answer, and approving one of the developer's own decisions, or one approved already, tells nothing;
  - a task ending without failure (the Djinn's cue to check its final note, revised questions, or spawned tasks),
    once: a work worker's end is its merge line; with nothing to merge, or in a project that integrates nothing, an
    `ended` event says it, with the first line of its final note (`W1 done: …`, incident W1 of wish Dew,
    12/10/2026);
  - the calls a worker's permissions refused in its run (its `permission denied: …` events, counted on the task's
    `refused`), in its `ended` or `failed` event, with the command that would grant them, whole in the digest: `W1 done,
    6 commands refused (permission): djinn, ls, git, …; djinn wish allow <wish> --mode auto would let the agent judge
    them, a right only the developer grants.` They go to the Djinn alone, never to the window (Decision Q273): the Djinn
    decides, continuing the task otherwise, closing it as seen (`djinn task done`; until then its wish is not proposed
    for the grant) and spawning it again where a right the wish or project already grants covers them. Only auto mode
    lets the agent judge a command (an edit allowance adds none). Rights stay explicit: no broad right by default, and a
    new one is asked of the developer in a `--move` question naming exactly that right, the command that grants it, and
    why. A claude worker started with its own settings (`TASK_ACCESS_NATIVE`) that allow no djinn command cannot report:
    its spawn answers a `note` saying so;
  - a watcher waking the Djinn;
  - Djinn restarted: `Djinn: restarted on version v1.4.0: 2 workers interrupted (W12, W14), resumed by Djinn: W12; not
    resumed, the move is yours: W14.`
  Everything else goes to the journal and the window.
- **The Djinn's events wait in a queue; the Djinn watches it (Decision Q191).** Each of these is a `LeadEvent` in the
  store, with an id (a UUIDv7), a time, a kind (`ended`, `failed`, `merged`, `answer`, `enlighten`, `move`, `restart`,
  `watch`, `request`, `approved`), the wish and the code it is about, and its line; one queue for the Djinn, whatever the wish
  (`internal/plan/leadwatch.go`). `djinn lead watch [--after <cursor>] [--timeout 1h]` blocks until an event waits,
  lets the events settle (2 seconds of quiet, 10 at most: the lines of one integration pass, or a failure and the
  correction Djinn starts for it, come within a second or two, and one digest is one turn of the Djinn instead of
  several), then prints a digest and exits:

  ```
  2 events for the lead:
  - W12 failed: (Build parser) failed (claude): exit 1. A correction worker started.
  - Q43 answer: answered B — "Paraffin". Turn it into tasks: you hold the plan's graph (djinn wish brief <wish>).
  Details: djinn task watch <task>, djinn wish brief <wish>.
  Cursor 01a1…. Next: djinn lead watch --after 01a1… (acknowledges these, waits for the next).
  ```

  A line per event (its code, its kind, its line cut to 160 characters, and its wish's title when the events span
  several), 30 at most (the rest come with the next watch, at once). Called again with `--after` the cursor, the watch
  acknowledges every event up to it for the session that watches, then waits: an event never acknowledged (the Djinn
  crashed, its session was lost) comes back on the next watch, at least once, never lost. With nothing in an hour (or
  `--timeout`, at most 6 hours) it prints "No event…" and the command to run again; `--timeout 0s` returns at once.
  The watch knows its session from the variable its agent sets for its commands (`CLAUDE_CODE_SESSION_ID`,
  `CODEX_THREAD_ID`, `ANTIGRAVITY_CONVERSATION_ID`), else the Djinn's recorded one: a session other than the
  Djinn's is refused (`djinn lead set` records it), and so is a worker. Acknowledged events are kept 7 days.
- **Djinn types one line only when no watch waits.** While a watch of the Djinn's session waits, or within a minute of
  one returning (the Djinn runs the next one first thing), nothing is typed in the Djinn's terminal. Otherwise, two
  seconds after the events settle, it types one short line, `Djinn: 3 events waiting, run djinn lead watch`, at most
  once until the Djinn watches again (or ten minutes passed); the Djinn, if it does not run, is reopened on its session first,
  as `djinn lead resume` does, without taking the window. djinn up does the same a minute after it starts, so that the
  Djinn hears of the restart if its watch died with the last djinn up. The line waits until the developer has not typed
  in that terminal for three seconds, and goes only to the Djinn's prompt (`Terminal.Say`, `internal/terminal/say.go`),
  read on the screen as it shows now (a small model of the terminal, `screen.go`), not on what it once drew:
  - **Never into a choice.** A hint that ends a line, or a part of it between `·`, with a key, `to` and what it does
    (`Esc to cancel`, `Enter to confirm`, `Esc to exit`, `Press enter to confirm or esc to cancel`) is a choice: a
    permission prompt or an approval of Claude Code or Codex, the folder trust, a first run's onboarding. The line
    waits until the developer answers it: a digit in it would pick an option, its Enter confirm one, usually "Yes".
    A choice covered since holds nothing, nor a sentence about one that ends with a period.
  - **Never joined to a line under way.** Where the agent shows its prompt (Claude Code's `❯`, Codex's `›`), text
    there that is not its faint placeholder holds the line until the developer sends or clears it, however long
    they think. Elsewhere Djinn follows the keys: what is typed holds the line until it is sent or cleared (Enter,
    Ctrl+C, Ctrl+U, Esc twice, a word erased by Ctrl+W or Alt+Backspace), or for 30 seconds after the last key. What
    the window's terminal sends on its own (answers to the agent's queries, the mouse, the focus) is no key.
  - **As a paste** when the agent turned bracketed paste on, so that it takes the line whole, then Enter on its own.
- **Each agent is woken up by its finished watch** (recorded in `internal/plan/testdata/wake`):
  - **Claude Code** runs it with the Bash tool and `run_in_background: true`; when it exits, Claude Code adds a
    `<task-notification>` and the agent takes a turn, even idle (Claude Code 2.1.296; an interactive session sets no
    time limit on background commands).
  - **Antigravity** runs it with `run_command` and `WaitMsBeforeAsync: 500`: it goes to the background as a task, and
    agy adds a system message, `Task id "…" finished with result`, which starts a turn (agy 1.3.3).
  - **Codex** is not woken up by a finished command (openai/codex rust-v0.162.1: a process still running after
    `yield_time_ms` must be polled with `write_stdin`, and its exit only shows in the interface). The Djinn on codex keeps
    no watch running; the line is typed even while a codex watch waits, and the Djinn runs
    `djinn lead watch --after <cursor> --timeout 0s` in the foreground, then ends its turn.
- **An answer reaches the Djinn.** When the developer answers a question, it is queued for the Djinn:
  `Djinn: Q43 answered B — "<option>". Note: "<note>". Turn it into tasks: you hold the plan's graph (djinn wish brief <wish>).`
  Answers Djinn settles itself (an edit question, a question on work that failed to integrate, a routed request, a
  grant) say what Djinn did. Answers given by the Djinn itself are not echoed back. An answer is never typed in the
  Djinn's terminal, so it never lands in the developer's prompt box, and never lost: it waits in the queue. If the Djinn
  does not run, the question is marked as waiting for it (`waiting_lead: true`) until the Djinn's watch
  acknowledges it. The Djinn's global brief lists these answers under `## Open answers without a lead`
  (`internal/plan/tell.go`).
- **Tasks.** `djinn task spawn` starts a worker. A task that cannot start yet waits, and says why.
  `djinn task watch <task>` follows its events; `djinn task stop <task>` stops it.
  A task no longer useful is cancelled, never marked done: `djinn task cancel <task> --note "…"` stops its worker,
  removes its worktree (its branch stays) and drops its work from integration; the window shows it Cancelled.
  `djinn task update <task-id>` (or `djinn task set-agent`) updates a task: only the given fields change.
  Before starting (planned, waiting), any field may change; while running or paused, only its title;
  once finished (done, failed, stopped, cut short), title, provider, model, prompt (for its next continue),
  azima, and integration (`--integration none` clears it, refused while merging; `--integration pending` puts a done
  task's work without one back in line); never its dependencies once it ran.
  An azima changes only its title or its own branch. `djinn task update <azima> --title "…"` renames it, whatever its
  state, and writes the title into its plan file's heading when it has one (`djinn plan sync` reads it back); the
  developer renames it from its card in the window too. `djinn task update <azima> --branch <name>` names its branch,
  adopting a branch that exists as it is, or cutting one from main under that name (refused on a work task and while a
  merge runs into it).
  When writing a worker's prompt, ask for the commit checks and the targeted tests, never "run the whole test suite"; the push checks are Djinn's.
- **The plan is a graph of azimas.** An azima (Arabic ʿazīma, the incantation that binds and commands a djinn) is a
  part of the plan, coded `T07`: a task of kind `AZIMA` that no worker runs, the scheduler never starts, and that never
  waits for the developer. `djinn plan sync <wish>` reads them from the projects' `plan/*.md` and writes each one's
  `after:` line back from the store; `djinn task spawn <wish> --kind azima --title "…"` makes one. An azima can also
  be created as a draft (`DRAFT`), or opened into active work (`djinn task open <azima>`) which marks it open, ready
  for parts to be spawned, and notifies the Djinn. An azima (a draft, or an azima no worker runs with parts
  that have not started) can be moved to another wish with `djinn task move <azima> --wish <wish>`.
  The Djinn plans work as a graph, never as a line:
  - give each azima one clear goal, one sentence a user reads as a feature, a will of its own; when a task extends
    it, rephrase its goal (its plan file's **Goal** and its title, then `djinn plan sync`) rather than let it drift;
  - before adding tasks or azimas, an answer turned into tasks included, look for the azima they belong to: group
    what goes together, merge duplicates, open a new azima only for a will no existing one carries, never mix what
    does not belong together;
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
    through dependencies and azimas together, naming it, and then changes nothing. An azima absorbed into another is
    deleted (`djinn task delete`) only once its parts are regrouped, and a task only once no task waits for it (rewire
    them first, in one `--also`): Djinn names them; its code is never given again. `--depends-on` is the former
    name of `--after`, still read.
- **Keep the graph free of conflicts.** The Djinn keeps the graph of tasks and azimas so that workers do not collide,
  along an iterative and de-risked path. Tasks that touch the same files, protos, generated files or the same tests go
  one after the other (`--after`, or `--write-scopes` when the project uses them), never side by side, but never chain
  tasks in a naive sequential line (`W1 -> W2 -> W3 -> W4`) just to be safe: estimate impacted packages and files at a
  high level (without reading code) and maximize parallelism in the DAG so multiple workers run concurrently, adding
  `--after` only between tasks that directly target the same file or proto (Djinn already has merge conflict resolution
  for minor overlaps). Large work is cut into small steps, each merged and green before the next starts. A task starts
  from the integrated branch: never spawn while the wish's integration points at another branch (a quarantine), since a
  task started there carries that branch into the next merge (W399 brought the replay sink's node_modules link into
  feat/wails-go on 11/10). A conflict or a red merge is a signal to re-sequence, not only to correct.
- **Watchers.** To wait on something outside, the Djinn spawns a watcher instead of polling:
  `djinn task spawn <wish> --title … --provider watch --prompt "<command>"` runs the command, with no agent, no model
  and no slot, and each new paragraph it prints wakes the Djinn. `--restart` starts again a command that exits on each
  change ([watch](providers.md#watch-a-command-no-agent)).
- **Babysitting.** A pull or merge request a task opens is watched in the same wish, with the watcher a skill of
  the project declares (`metadata.djinn.babysit`); the Djinn hears `W5 opened MR !12: W7 babysits it` and follows that
  skill. `djinn task spawn <wish> --title … --babysit --after W5` does it by hand
  ([wish templates](wish-templates.md#babysitting-what-a-task-opens)).
- **Every request finds its wish.** A request that is not about the wish goes through
  `djinn wish route "<request>" --wish-id <wish> --ask`: a card asks the developer to open a new wish, to file it in
  an existing wish, or to keep it in this one. Answering NEW makes the wish and tells the Djinn without starting a second one.
  A skill's [wish template](wish-templates.md) (`metadata.djinn.wish`) makes the new wish follow the skill, its watcher started;
  the brief tells the Djinn to propose one for a request that comes back.
- **Blocks are for agents.** A hand-off, a reference, a decision taken outside a question (kind `decision`, which
  the decision log shows) is a block: `djinn block put`. The developer does not read the others: the window keeps them
  in a discreet last tab, **For agents**, folded, with no mark. What the developer must see is a question.
- **Who did it.** `djinn block put`, `djinn question ask` and `djinn question revise` take `--task-id`, by default
  `$DJINN_TASK_ID`, which Djinn sets for every worker (`(djinn.v1.env)` in the protos): a worker's decision block
  reads "By W12" in the decision log, its revision "Revised by W12" on the page. The Djinn's terminal is marked
  with `$DJINN_LEAD=1`: `djinn question answer` and `djinn question withdraw` take `--lead` from it,
  attributing them to the Djinn and not echoing them back; a plain shell has neither variable, attributing answers
  to the developer so they reach the Djinn as a message.
- **The page.** `djinn wish sync <wish>` renders the wish's page in Go and prints its file, kept up to date. The Djinn
  republishes that file as it is; it never writes the HTML.
- **The developer's word.** `djinn wish grant <wish>` says a wish is done. Only the developer says it.

## Workers: processes Djinn starts

A worker is one agent process per task: Claude Code, Codex or Antigravity, in the task's own worktree. The commands
Djinn runs, what a worker may do, and how each agent signs in: [`providers.md`](providers.md).

Djinn reads each worker's stream into the same task events, stored with the task and streamed by
`djinn task watch` (`--raw` adds the provider's own line, `--last 5` starts at the last five events stored):

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

The scheduler starts planned and resuming tasks in a strict 5-tier dispatch order (investigators and converters first,
then corrections and reviews, resumed work tasks, work tasks unblocking others on the critical path, and other work tasks).
When slots are full or memory is short, investigators preempt the lowest-priority work task: pausing it or interrupting
it to resume later without spending a resume attempt. Details: [scheduler order and preemption](providers.md#the-schedulers-order-and-preemption).

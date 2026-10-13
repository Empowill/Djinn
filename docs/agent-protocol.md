# Djinn agent protocol

How agents work with Djinn. The lead drives a wish through the `djinn` command. Workers are agent processes Djinn
starts and reads. No agent writes in Djinn's data folder, and no model decides what Djinn computes.

## The lead: the command line

The lead is the agent the developer talks to. It changes the plan with `djinn`, never by hand.

- **Every command comes from a proto.** Each public method of `api/` is a command: `QuestionService.Answer` is
  `djinn question answer`. Arguments, help and validation follow the [convention](cli-convention.md). Exit codes:
  `0` success, `1` the call failed, `2` the command line is wrong.
- **The original request stays whole.** `djinn wish make "<title>"` still creates a title-only wish.
  `djinn wish make --prompt "<Markdown>"` creates one immediately with a neutral, localized provisional title;
  an explicit title may accompany the prompt. The complete Markdown is a `creation` block, written in the same
  durable transaction as the wish and its journal entry, including for `--paused`. It travels through export and
  import and appears once in the brief, outside the normal latest-block count and text limits, with the same
  local-path and URL-credential scrubbing as other portable content. At startup, the lead of a prompt-only wish
  writes a concise title from that full request with `djinn wish rename <wish> "<title>"`. Rename validates a
  nonblank line of at most 500 characters, journals the change and notifies readers; it leaves the request intact.
  A wish already renamed, or made with an explicit title, keeps that title when another lead starts.

- **The brief says where the wish stands, and how to lead it.** `djinn wish brief <wish>` is a status on its own,
  computed by Djinn from its store (`internal/plan/brief.go`), without a model: the wish's description first, then its
  azimas and their states, the tasks running and waiting, the open questions, the latest decisions and blocks, and
  the last lead (its agent, its session, when a lead last acted through Djinn); then Djinn's rules and the projects'
  rules. Reconstructing the state is not the lead's job.
- **A wish describes itself.** Its description is a few lines: what it is for, its scope, where it goes.
  `djinn wish describe <wish> --text "…"` sets it; the developer edits it in the wish's head, under its title (a
  click, saved when the field is left). Until someone writes one, the title stands for it.
- **The lead asks through Djinn, and delegates.** Everything for the developer is a question, the lead's own
  included: `djinn question ask`, with options and a recommendation, or with none when the lead really does not know
  what to think, the analysis behind it in its `--context`. One written in the lead's terminal reaches neither the
  window, nor the decision log, nor the question workers. A decision taken in the terminal goes in a block of kind
  decision; the work goes to tasks. A resolved question is resolved: the lead does not bring it back to the
  developer, and asks again, a new question, only on a real doubt. Djinn's rules in the brief say it, and so does
  every lead's first message.
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
- **Marks.** The open questions the developer marked read, from the window: `djinn mark list <wish>`, and the
  brief's "Marked by the developer". Approving an open question (`djinn mark put Q03 approved`) answers it with
  its recommendation. A block takes no mark: Djinn refuses one.
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
- **Developer instructions.** `djinn instruction send <wish> "<full text>"` persists a durable instruction, with
  code I01, I02 and so on within the wish. It commits the instruction and the journal atomically before notifying
  the lead. Submission works while the lead is offline; it is available in full in the next brief. An optional
  `--request-id <uuid>` makes retries return the original instruction, without a second notification. Reusing it with different text or different image content is rejected atomically. Repeat
  `--attachment-paths <local-image>` for up to eight PNG, JPEG or GIF files; paths are made absolute and resolved
  before Djinn reads them. An image-only instruction is valid, while an empty text-only instruction remains invalid.
  The window uses the internal typed `InstructionService.SendData` RPC with `AttachmentInput{name, media_type, data}`
  for the same operation; binary data never enters a journal row. Each stored instruction attachment carries its UUID,
  type, byte size, SHA-256 and decoded dimensions. Attachments are bounded to 5 MiB each, 10,000 pixels per side and
  40 million pixels, and are exported with their bytes and remapped to fresh IDs on import.
  After reload, the window retrieves a thumbnail or provider payload with the internal
  `InstructionService.GetAttachment{wish_id, instruction_id, attachment_id}` RPC. It returns the matching validated
  `Attachment` metadata and bounded bytes only after checking that the instruction belongs to the supplied wish; it
  accepts no filesystem path.
  Task composer messages use the internal `TaskService.SendData{task_id, text, attachments, attachment_metadata}`
  RPC when they include images. The window leaves `attachment_metadata` empty; the server rejects caller-supplied
  metadata, validates and stages each image, then creates a task event whose `attachments` hold the canonical
  metadata. The bytes are delivered to a running worker through the provider's native image input. The event and its
  blobs survive restart, export and import, with attachment IDs remapped on import. The `SendData` response carries
  the explicit image delivery state. After reload, `TaskService.GetEvent{task_id, event_id}` returns the event and
  bounded `AttachmentExport` bytes after checking event ownership; text-only task messages keep their existing
  behavior.
  **W21 TypeScript handoff.** Use the generated `InstructionServiceSendDataRequest` with
  `{wishId, text, requestId, attachments: [{name, mediaType, data: Uint8Array}], attachmentMetadata: []}`;
  `attachmentMetadata` is server-owned and must be empty in a window request. Image-only submissions are accepted.
  Its `InstructionServiceSendDataResponse` returns `{instruction, imageDelivery, imageDeliveryNote}`: the instruction
  is durable even when delivery is `DEFERRED`, `UNSUPPORTED` or `FAILED`, while `QUEUED` means a native image
  transport accepted it and `UNSPECIFIED` covers text-only requests or an existing retry. Reload bytes with
  `InstructionServiceGetAttachmentRequest{wishId, instructionId, attachmentId}`; the response is
  `{attachment, data: Uint8Array}` and the server checks the instruction's wish and attachment ownership.
  `djinn instruction list <wish>` reads every instruction and its full text.
  The lead runs `djinn instruction reflect I01 --wish-id <wish>` (pending → reflecting), delegates to an existing
  worker or creates one, then `djinn instruction assign I01 W1 --wish-id <wish>` (reflecting → processing).
  Both references also accept UUIDs; an instruction code without a wish must be globally unambiguous.
  Assignment requires a task of the same wish. A processing instruction may be reassigned to another such worker.
  The lead verifies the successful worker result, then runs `djinn instruction complete I01 --wish-id <wish>`
  (processing → done). Completion requires a successful DONE task; a failed, stopped or waiting task cannot
  complete it. Worker completion alone never completes the instruction. Repeating the same transition is safe;
  other transitions fail atomically. Processing and done always keep a worker task reference; a referenced task
  cannot be deleted. Lifecycle commands journal resolved instruction/wish IDs so their history stays unambiguous.
  `djinn instruction reply I01 "…" --wish-id <wish>` appends a concise informational reply (at most 2000 characters)
  without changing the instruction's status or worker assignment. The instruction UUID or I01 code may be used;
  `--wish-id` is optional for a UUID, and required when a code is shared by several wishes. Replies are appended in
  chronological order, journaled with the resolved instruction and wish in the same transaction, and retained in
  snapshots, exports, imports and after restart with portable path and credential scrubbing. A blank, malformed,
  overlong or wrong-wish reply is rejected atomically; repeating a reply appends another entry. Instructions travel in
  snapshots, exports and imports (with portable text scrubbing), change the Watch stream and the synced page, and
  prevent readiness while unfinished. Only the developer grants a wish.

- **Tasks.** `djinn task spawn` starts a worker. A task that cannot start yet waits, and says why.
  `djinn task watch <task>` follows its events; `djinn task stop <task>` stops it.
- **The plan is a graph of azimas.** An azima (Arabic ʿazīma, the incantation that binds and commands a djinn) is a
  part of the plan, coded `T07`: a task of kind `AZIMA` that no worker runs, the scheduler never starts, and that never
  waits for the developer. `djinn plan sync <wish>` reads them from the projects' `plan/*.md` and writes each one's
  `after:` line back from the store; `djinn task spawn <wish> --kind azima --title "…"` makes one. The lead plans work
  as a graph, never as a line:
  - give each azima one clear goal, one sentence a user reads as a feature, a will of its own; when a task extends
    it, rephrase its goal (its plan file's **Goal** and its title, then `djinn plan sync`) rather than let it drift;
  - before adding tasks or azimas, an answer turned into tasks included, look for the azima they belong to: group
    what goes together, merge duplicates, open a new azima only for a will no existing one carries, never mix what
    does not belong together. A question worker, which edits no file, says in its last line when a goal needs
    rephrasing;
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
    deleted (`djinn task delete`) only once its parts are regrouped: Djinn names them; its code is never given again. `--depends-on` is the former
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
- **Blocks are for agents.** A hand-off, a reference, a decision taken outside a question (kind `decision`, which
  the decision log shows) is a block: `djinn block put`. The developer does not read the others: the window keeps them
  in a discreet last tab, **For agents**, folded, with no mark. What the developer must see is a question.
- **Who did it.** `djinn block put`, `djinn question ask` and `djinn question revise` take `--task-id`, by default
  `$DJINN_TASK_ID`, which Djinn sets for every worker (`(djinn.v1.env)` in the protos): a worker's decision block
  reads "By W12" in the decision log, its revision "Revised by W12" on the page; the lead's, run without the
  variable, stay the lead's.
- **The page.** `djinn wish sync <wish>` renders the wish's page in Go and prints its file, kept up to date. The lead
  republishes that file as it is; it never writes the HTML.
- **The developer's word.** `djinn wish grant <wish>` says a wish is done. Only the developer says it.

### Writing to the lead

`WishService.Tell` stays generic and transient: worker-to-lead reports never become developer instructions.
Use `InstructionService.Send` for developer instructions. A notification is best effort after durable acceptance:
if the lead is offline or exits, the persisted instruction remains pending for the next brief. Retry identifiers
prevent duplicate instructions when the response to a committed submission is lost.

Djinn types into the lead's terminal as the developer would: the news of the wish (`Q02 answered: A. Continue.`) and
generic messages sent with `djinn wish tell`. A durable instruction notification names its code and identifier,
with explicit read/reflect/assign guidance; the lead reads the full text from the store before delegating. Its terminal reads on the screen as it shows now, not on what it
once drew:

An image instruction is delivered to a lead only through a native image-capable transport. A terminal that implements
only the text `Leads.Tell` receives the ordinary durable lifecycle notice and returns an `UNSUPPORTED` image delivery
state; the instruction remains durably stored. Djinn never pastes a filename as a substitute and never restarts a live lead to make room for an image. Supported
worker providers resolve the stored attachment by UUID and send it as a typed image block. The PTY lead path and
Antigravity stream currently have no proven image-block protocol.

- **Never into a choice.** A hint that ends a line, or a part of it between `·`, with a key, `to` and what it does
  (`Esc to cancel`, `Enter to confirm`, `Esc to exit`, `Press Enter to continue`) is a choice: a permission prompt, the folder trust, a first run's
  onboarding. The text waits until the developer answers it: a digit in it would pick an option, its Enter confirm
  one. Trusting a folder stays the developer's ([providers](providers.md)). A choice covered since holds nothing, nor
  a sentence about one that ends with a period.
- **Never into a line under way.** What the developer types holds the text until it is sent or cleared (Enter,
  Ctrl+C, Ctrl+U, Esc twice, a word erased by Ctrl+W or Alt+Backspace), or until 30 seconds without a key: Djinn
  follows the keys, not the prompt, and may lose track of an edit.
- **Only to a lead.** The terminal must run the line Djinn starts a lead with (`claude`, `codex`, `agy` through the
  shell). A shell the window opened under the lead's name gets nothing: the text would run as a command. Close it,
  then `djinn wish resume`.

**Who may write.** Djinn does not know who calls it. Any process of the developer's that reaches Djinn's socket, a
worker included, may run `djinn wish tell` and speak to the lead as the developer, as it may run `djinn wish grant`:
the same trust, the local user's. Nothing in code keeps a worker from it.
A simple guard, not in place: the command line could refuse `wish tell` and `wish grant` when `DJINN_TASK_ID` is set,
as it is for every worker. It stops a mistake, not a worker that clears its environment or calls the socket itself.

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

When a provider does not surface queued task messages, a worker explicitly reads them with `djinn task watch <task>`
(or `--after-seq <last-read>`). Do not infer that no new guidance exists from a quiet provider stream.

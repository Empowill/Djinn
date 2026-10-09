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
- **The original request stays whole.** `djinn wish make "<title>"` still creates a title-only wish.
  `djinn wish make --prompt "<Markdown>"` creates one immediately with a neutral, localized provisional title;
  an explicit title may accompany the prompt. The complete Markdown is a `creation` block, written in the same
  durable transaction as the wish and its journal entry, including for `--paused`. It travels through export and
  import and appears once in the brief, outside the normal latest-block count and text limits, with the same
  local-path and URL-credential scrubbing as other portable content. At startup, the lead of a prompt-only wish
  writes a concise title from that full request with `djinn wish rename <wish> "<title>"`. Rename validates a
  nonblank line of at most 500 characters, journals the change and notifies readers; it leaves the request intact.
  A wish already renamed, or made with an explicit title, keeps that title when another lead starts.
- **Questions.** `djinn question ask` offers up to four options, answered by letter, or none for a yes. An answered
  question is a decision. On options that say yes and no, `yes` and `no` pick them too. Start a recommendation with
  its option's letter (`B: …`): the developer applies it in one click, "Rub the lamp".
- **Investigations.** "Enlighten me" in the window (`djinn question enlighten <question> --note …`) asks the lead to
  find out more before deciding: the question waits for the lead, and the brief lists it under "To investigate". The
  lead answers with `djinn question revise <question> --context … --recommendation …`; the question keeps each round,
  dated, and waits for the developer again. A decision may take several rounds.
- **Marks.** What the developer read or approved as it is, from the window: `djinn mark list <wish>`, and the brief's
  "Marked by the developer". An approved block or decision is a go.
- **Lead discipline.** The lead never authors source or test changes, including small fixes, review findings or
  failed checks. Delegate every implementation to an existing worker (`djinn task send`) or a new task
  (`djinn task spawn`). The lead frames, coordinates, reviews, integrates and verifies their work.
- **Developer instructions.** `djinn instruction send <wish> "<full text>"` persists a durable instruction, with
  code I01, I02 and so on within the wish. It commits the instruction and the journal atomically before notifying
  the lead. Submission works while the lead is offline; it is available in full in the next brief. An optional
  `--request-id <uuid>` makes retries return the original instruction, without a second notification. Reusing it with different text is rejected atomically.
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
  Instructions travel in snapshots, exports and imports (with portable text scrubbing), change the Watch stream
  and the synced page, and prevent readiness while unfinished. Only the developer grants a wish.
- **Tasks.** `djinn task spawn` starts a worker. A task that cannot start yet waits, and says why.
  `djinn task watch <task>` follows its events; `djinn task stop <task>` stops it.
- **Blocks.** What Djinn does not compute (an analysis, a hand-off, a decision taken outside a question) is a block:
  `djinn block put`. Djinn shows it as written.
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

# The data model

What Djinn keeps, where, and who writes and reads it. A map for a review: what each model is for, and where it
could be simpler. The protos in `api/` are the source of truth; this page follows them, and
`TestDataModelDoc` (`internal/plan/datamodel_test.go`) fails when a message of the protos is missing here.

Requests and responses (`…Request`, `…Response`) and enums stay out of the diagrams: they carry data, they do not
keep it. An enum shows as a field's type.

## Where data lives

| Where | What | Written by |
| --- | --- | --- |
| `djinn.db`, SQLite, in the data folder | One table per stored message, plus the journal `command` | `internal/store`, in a transaction |
| Repository of a project | `.agents/settings.txtpb` (`ProjectSettings`), `.agents/permissions.txtpb` (`Permissions`), skills (`SKILL.md`) | The team, by hand |
| Data folder | `projects/<id>/settings.txtpb` (`ProjectSettings`), `settings.json` (`ui.v1.Settings`), `state.json` (the window's workspace, a JSON string), `restart.json` (leads to reopen), worktrees | Djinn, or the developer |
| Memory of `djinn up` | `Gate`, `Machine`, `Terminal`, the scheduler's queue | Djinn |
| Files you carry | A wish export (`WishExport`, `.djinn`), a backup (zip with `Manifest`), the wish's page (HTML) | Djinn, on request |

**The store.** `plan.Entities()` lists the stored messages: `Project`, `Wish`, `Task`, `TaskEvent`, `Question`,
`Block`, `InboxItem`, `CommandCost`. Each gets a table named after it: `id` as primary key, every singular string
field named `*_id` as an indexed column, each `(djinn.v1.unique)` group as a unique index (case ignored), and the
whole message as a binary `payload`. Any other field lives only in the payload: adding one needs no migration.
Nothing is ever dropped. Every write goes through `Store.Tx`, which writes the command that caused it into the
`command` journal in the same transaction.

## Flows

Who writes, who reads, through which service, down to the store and the workers.

```mermaid
%%{init: {"flowchart": {"nodeSpacing": 24, "rankSpacing": 36}}}%%
flowchart TB
  window["Window<br/>React"]
  cli["Command line<br/>and MCP"]
  agents["Lead and<br/>workers"]
  svc["djinn up<br/>Connect services"]
  harness["Harness<br/>scheduler, gates"]
  db[("djinn.db<br/>tables, journal")]
  files["Files<br/>data folder, .agents/"]
  pages["Page, brief,<br/>notices"]
  providers["Provider CLIs<br/>and models"]
  out["Page HTML, export,<br/>notifications"]
  window -- "Connect" --> svc
  agents --> cli
  cli -- "socket" --> svc
  svc -- "Watch" --> window
  svc --> db
  svc --> harness
  harness --> db
  harness -- "reads" --> files
  harness -- "starts" --> agents
  agents --> providers
  db -- "OnCommit" --> pages
  pages --> out
```

- **One writer.** Every change goes through `djinn up`, a service or the harness, then `Store.Tx`. The command
  line, MCP and the window are clients of the same Connect services; `djinn mcp` serves the commands of the command
  line as tools. The window talks over `wails://` or loopback HTTP, the command line over a Unix socket or loopback
  HTTP ([transport](transport.md)).
- **The window copies, never computes.** `src/data/store.ts` lists what it shows, then follows
  `WishService.Watch`, which says only what changed (`Change`); the store reads it again. Task events come by
  `TaskService.Watch`, for the tasks it follows.
- **The harness** is the only writer of `TaskEvent`, and of a task's run fields (status, usage, times, session).
  The scheduler (`internal/dispatch`) picks the next tasks from the store, with the slots the machine monitor
  allows. At launch the harness reads the project's `.agents/` (permissions, settings, skills) and the developer's
  settings, and starts the provider's command line in the task's worktree. Workers call back through the command
  line: `djinn gate run` (held in memory by `internal/gate`, its cost kept as `CommandCost`), `djinn task send`.
- **Derived views.** On each commit the store calls its hooks: the pages of synced wishes are rendered again
  (`internal/render`), the window's watch streams are told, and the window's notices read new questions. The lead's
  brief (`internal/plan/brief.go`) is read from the store when the lead starts or asks.
- **What leaves the machine.** Prompts go to the providers' command lines. The wish's page is an HTML file `djinn
  wish sync` keeps current; the lead republishes it. An export or a backup is a file you ask for. Nothing else.

## Wishes and blocks

How to read the diagrams. `<<table x>>` is a stored message and its table; a class without a stereotype lives
inside its parent's payload. `PK` primary key, `idx` indexed column, `uq` part of a unique index. A diamond is a
message held inside another; `→ Wish` in a field is a reference by id. The diagrams run left to right so that they
stay readable in the window's column.

```mermaid
classDiagram
  direction LR
  class Wish {
    <<table wish>>
    string id PK
    string title
    string[] project_ids → Project
    Timestamp create_time
    ProjectAllowance[] allowances
    Lead lead
    WishState state
    int32 rank
    Timestamp grant_time
    bool ready, computed
    WishTemplate template
  }
  class Lead {
    Provider provider
    string session_id
    string directory
  }
  class ProjectAllowance {
    string project_id → Project
    Allowance allowance
  }
  class WishTemplate {
    string skill
    string project_id → Project
    string watch
    string done_when
    bool restart
  }
  class Block {
    <<table block>>
    string id PK
    string wish_id → Wish, idx
    string task_id → Task, idx
    string kind
    string title
    int64 position
    string media_type
    string content
    Timestamp create_time
    Timestamp update_time
    Mark[] marks
    string icon
  }
  class Mark {
    MarkKind kind
    string actor
    Timestamp create_time
  }
  Wish *-- Lead
  Wish *-- ProjectAllowance
  Wish *-- WishTemplate
  Block *-- Mark
```


## Questions and decisions

```mermaid
classDiagram
  direction LR
  class Question {
    <<table question>>
    string id PK
    string code, uq
    string wish_id → Wish, idx, uq
    string text
    string[] options
    Timestamp create_time
    Answer answer
    string context
    string recommendation
    Mark[] marks
    Round[] rounds
    int32 revision
    Route route
    bool grant
    string icon
  }
  class Answer {
    Choice choice
    string note
    Timestamp create_time
  }
  class Round {
    RoundKind kind
    string actor
    Timestamp create_time
    string note
    string context
    string[] options
    string recommendation
  }
  class Mark {
    MarkKind kind
    string actor
    Timestamp create_time
  }
  class Route {
    string request
    string from_wish_id → Wish
    RouteOption[] options
  }
  Question *-- Answer
  Question *-- Round
  Question *-- Mark
  Question *-- Route
```

```mermaid
classDiagram
  direction LR
  class Route {
    <<in Question, InboxItem>>
    string request
    string from_wish_id → Wish
    RouteOption[] options
  }
  class RouteOption {
    RouteKind kind
    string wish_id → Wish
    string title
    string[] project_ids → Project
    string pause_wish_id → Wish
    int32 score
    string reason
    WishTemplate template
  }
  Route *-- RouteOption
```

A decision is a question with an `answer`, or a block of kind `decision`. There is no decision table.

## Tasks, events, closures and usage

```mermaid
classDiagram
  direction LR
  class Task {
    <<table task>>
    string id PK
    string wish_id → Wish, idx, uq
    string project_id → Project, idx
    string code, uq
    string title
    TaskStatus status
    Provider provider
    string worktree
    string branch
    string session_id, idx
    Usage usage
    int32 exit_code
    string error
    Timestamp create_time
    Timestamp start_time
    Timestamp end_time
    TaskAccess access
    string edit_question_id → Question, idx
    string model
    double max_budget_usd
    string[] depends_on → Task
    string[] write_scopes
    bool scheduled
    string wait_reason
    string fork_session
    string fork_of → Task code
    Timestamp resume_after
    int32 resumes
    bool restart
    string last_line
    Closure closed
    bool continuing
    string decision → Q code or Block
  }
  class Usage {
    int64 input_tokens
    int64 output_tokens
    int64 cache_read_tokens
    int64 cache_write_tokens
    double cost_usd
  }
  class Closure {
    Closer actor
    Timestamp create_time
    string note
    string continued_in → Task code
  }
  class TaskEvent {
    <<table task_event>>
    string id PK
    string task_id → Task, idx, uq
    int64 seq, uq
    TaskEventKind kind
    string text
    Usage usage
    string raw
    Timestamp create_time
  }
  Task *-- Usage
  Task *-- Closure
  TaskEvent *-- Usage
```

## Projects, settings, permissions, skills, templates and inbox

```mermaid
classDiagram
  direction LR
  class Project {
    <<table project>>
    string id PK
    string name, uq
    string directory, uq
    bool git
    Timestamp create_time
    string remote
    Summon[] summons
  }
  class Summon {
    string project_id → Project
    string skill
    Timestamp create_time
  }
  class ProjectSettings {
    <<file settings.txtpb>>
    Provider provider
    string model
    double max_budget_usd
  }
  class ProjectSetting {
    <<computed>>
    string name
    string value
    SettingSource source
  }
  class Permissions {
    <<file permissions.txtpb>>
    bool edit
    string[] commands
    string[] denied_commands
    bool network
    Mode mode
  }
  Project *-- Summon
  ProjectSettings ..> ProjectSetting : merged
```

```mermaid
classDiagram
  direction LR
  class Skill {
    <<computed from SKILL.md>>
    string project
    string name
    string source
    string directory
    string description
    string missing
    string template
    string template_error
    string inbox_source
    string inbox_source_error
  }
  class WishTemplate {
    <<copied into Wish>>
    string skill
    string project_id → Project
    string watch
    string done_when
    bool restart
  }
  class InboxItem {
    <<table inbox_item>>
    string id PK
    string source
    string project_id → Project, idx
    string key, uq
    string text
    InboxState state
    Timestamp create_time
    Route route
    string wish_id → Wish, idx
    Timestamp settle_time
  }
  Skill ..> WishTemplate : wish
  Skill ..> InboxItem : source
```

A wish template is not stored on its own: a skill declares it in its `SKILL.md`, and a wish made from it keeps a
copy (`Wish.template`).

## Machine, gates, costs and the journal

```mermaid
classDiagram
  direction LR
  class CommandCost {
    <<table command_cost>>
    string id PK
    string project_id → Project, idx, uq
    string command, uq
    int32 runs
    double cpu_seconds
    double seconds
    uint64 peak_memory_bytes
    double last_cpu_seconds
    double last_seconds
    uint64 last_peak_memory_bytes
    int32 last_exit_code
    Timestamp last_time
  }
  class command {
    <<table, the journal>>
    int seq PK
    string id, uq
    string actor
    string at
    string method
    bytes request
  }
  class Machine {
    <<computed on read>>
    string os
    string arch
    int32 cores
    uint64 memory_total_bytes
    uint64 memory_available_bytes
    double load1
    Pressure cpu_pressure
    Pressure memory_pressure
    int32 workers
    string workers_rule
    int32 running
    string pressure
    Timestamp read_time
  }
  class Pressure {
    double some_avg10
    double full_avg10
  }
  class Gate {
    <<memory>>
    string name
    string holder
    string holder_task_id → Task
    Timestamp since
    string[] waiting
  }
  Machine *-- Pressure
```

The journal is a table of its own, not a message: `request` is the binary request of the method it names. An
export carries the wish's entries as `Command` messages, with the request as an `Any`.

## Window, terminals, backup and exchange

```mermaid
classDiagram
  direction LR
  class Settings {
    <<file settings.json>>
    string shortcut
  }
  class Shortcut {
    <<computed>>
    string chord
    string default_chord
    bool available
    string problem
  }
  class Provider {
    <<computed, ui.v1>>
    string id
    string name
    bool available
    string command
  }
  class Terminal {
    <<memory>>
    string id
    string name
    string[] command
    string directory
    uint32 cols
    uint32 rows
    bool exited
    int32 exit_code
  }
  class Manifest {
    <<file in a backup>>
    int32 format
    string version
    Timestamp create_time
  }
  Settings ..> Shortcut
```

```mermaid
classDiagram
  direction LR
  class WishExport {
    <<file .djinn>>
    int32 version
    Timestamp create_time
    Wish wish
    ProjectRef[] projects
    Task[] tasks
    TaskEvent[] events
    Question[] questions
    Block[] blocks
    Command[] commands
  }
  class ProjectRef {
    string id
    string name
    string remote
    bool git
  }
  class Command {
    string id
    string actor
    Timestamp at
    string method
    Any request
  }
  WishExport *-- ProjectRef
  WishExport *-- Command
```

The window's workspace (open tabs, layout) is one JSON string in `state.json`, opaque to Go
(`UiService.LoadState`, `SaveState`). `restart.json` lists the lead terminals to reopen; it is a Go struct, not a
proto.

## The models explained

One line each: what it is for, who writes it, who reads it.

**Stored, in `djinn.db`**

| Message | For | Written by | Read by |
| --- | --- | --- | --- |
| `Project` | A folder Djinn works in, its remote, the skills it summons | `ProjectService.Add`, import, `SkillService` | Everything |
| `Wish` | What you ask for: title, projects, state and rank, rights, lead | `WishService` (make, route, import, allow, pause, activate, move, grant, set-lead), the inbox | Window, page, brief, scheduler, notices |
| `Task` | One item of the plan and its worker's run | `TaskService` (spawn, done, continue, pause), the harness, the scheduler (`wait_reason`) | Window, page, brief, scheduler |
| `TaskEvent` | What a worker said and did, in order | The harness only | `TaskService.Watch` (window, `djinn task watch`), page, export |
| `Question` | A question to the developer; answered, a decision | `QuestionService`, Djinn itself (edit, route, grant questions), `MarkService` | Window, page, brief, notices, shortcut |
| `Block` | Free content of a wish: the smoke | `BlockService`, a routed request, `MarkService` | Window, page, brief, routing (latest blocks) |
| `InboxItem` | A paragraph a skill's source printed, proposed as a request | The inbox, `InboxService` | Window's inbox |
| `CommandCost` | What a command cost in a project, summed | `CommandService.Record`, from `djinn gate run` | `djinn command list` |
| journal `command` | Every write, with its request | `Store.Tx` | Export and the window's journal: requests of the API only |

**Held inside a stored message:** `Lead`, `ProjectAllowance`, `WishTemplate` (in `Wish`); `Usage`, `Closure` (in
`Task`, `Usage` also in `TaskEvent`); `Answer`, `Round`, `Mark`, `Route`, `RouteOption` (in `Question`; `Mark`
also in `Block`, `Route` also in `InboxItem`); `Summon` (in `Project`).

**In files:** `ProjectSettings` (team and developer defaults of the workers), `Permissions` (what a worker may run),
`ui.v1.Settings` (the global shortcut), `Manifest` (a backup's header), `WishExport`, `ProjectRef`, `Command` (a
wish carried to another machine).

**Computed, never kept:** `Machine`, `Pressure` (read from the system), `Gate` (in memory), `Terminal` (in memory),
`Skill` (from `SKILL.md`), `ProjectSetting` (the merge of the two settings files), `ui.v1.Provider`, `Shortcut`,
`Wish.ready`.

**Parts of requests and responses:** `QuestionRef`, `MarkTarget`, `Marked`, `ProjectMatch`. `djinn.v1.Unique` is an
option of the protos, read by the store.

## Simplification candidates

Facts are proven by the code cited. What a change would cost is an estimate.

1. **Each worker event is written twice.** The harness writes a `TaskEvent` row and, in the same transaction, a
   `harness/event` journal entry whose request is the same event (`internal/harness/harness.go`, `writeAs`). No code
   reads a `harness/*` entry back: `plan/exchange.go` `request()` finds no service named `harness` and skips it.
   The journal grows as fast as the events. *Cost:* journal the event's `(task_id, seq)` instead, or nothing for an
   event; the rule "every write has its journal entry" holds with a small entry. Old rows stay.
2. **`TaskEvent.raw` keeps each provider line as it came**, up to 64 KiB, next to the `text` made from it. Read only
   by `djinn task watch --raw` (`internal/harness/service.go`); an export drops it. *Cost:* lose the raw view for
   debugging, or keep it only for errors and the last events of a task.
3. **`Mark.actor` and `Round.actor` are always `local`** (`const actor` in `internal/plan/plan.go`) and no code reads
   them. *Cost:* none to stop writing; keep the field number reserved. Keep them if agents get an identity soon.
4. **`Question.revision` counts the `REVISE` rounds**: `internal/plan/rounds.go` appends the round and increments
   it together. *Cost:* count the rounds in three readers (brief, page, window).
5. **`InboxItem.settle_time` is written, never read** (`internal/plan/inbox.go`). *Cost:* none; or show it.
6. **`UiService.Watch` is declared, not served, never called**: `internal/ui` embeds the unimplemented handler and
   no client calls it. Its two messages go with it. *Cost:* none.
7. **`DemoService` is served by `djinn up`** and called by no client of the window or the command line. *Cost:* none,
   unless a measure of the transport needs it.
8. **`Task.session_id` is an indexed column** only because its name ends in `_id`; no query filters on it. *Cost:*
   an index per task write, small; renaming the rule's exception is more code than it saves.
9. **`Wish.ready` is computed on every read but sits in the stored message.** It is reset before each read and on
   export. *Cost:* moving it to the responses changes the window's `Wish` type in many places; low value.
10. **`Task` holds both the plan and the run** (33 fields): what to do (title, dependencies, scopes, decision) and
    what the worker did (worktree, session, usage, error, resumes). *Cost:* a split into two tables touches the
    harness, the scheduler, the page, the window and the export format. Not worth it before a second kind of run.

Seen and kept: `Task.access` repeats the answer of the edit question, but every launch needs it without a join;
`Closure.continued_in` and the fork's `fork_of` are the two ends of one link, and the scheduler reads the first
(`internal/dispatch/dispatch.go`); `Wish.rank` is 0 exactly when the wish is not active, and the state still tells
paused from granted.

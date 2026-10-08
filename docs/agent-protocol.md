# Djinn agent protocol

Djinn uses a persistent local Codex app-server or the configured Claude CLI and turns its output into a small event
stream for the renderer. The renderer receives events through
`window.djinn.onEvent(callback)`.

Every event has this envelope:

```json
{
  "runId": "run-uuid",
  "taskId": "task-id",
  "stepId": "stable-workflow-step-id",
  "type": "text",
  "timestamp": "2026-10-05T12:00:00.000Z",
  "data": {}
}
```

The `type` is one of `text`, `tool`, `question`, `artifact`, `agent`, `phase`,
`note`, `mission_metadata`, `next_step`, `action`, `work_item`, `report`, `step_result`,
`permission_requested`, `permission_resolved`, `status`, `error`, `auth`, `guidance`, or
`notification_clicked`.
Lifecycle status data uses `running`,
`completed`, `stopping`, `cancelled`, and `error`. `stopping` retains the native
writer lock until actual process close or turn completion. Provider session identifiers are kept in
`data.providerRunId` when the CLI reports one.

Authentication progress uses `type: "auth"` with `data.provider`,
`data.status`, and streamed `data.text`/`data.message`. A URL is surfaced as
`data.url` only when it is an HTTP(S) URL found in provider output; opening it
still requires the renderer to call the explicit `openExternal` bridge method.

## Native structured interactions

New Codex threads register these dynamic tools on `thread/start`:

- `publish_question`: full non-empty context, optional choices or free text,
  stable id and affected work item. Workers default to `blockingScope:"agent"`;
  independent workers continue. Lead questions can block the mission.
- `update_task`: stable work item id, title, status, owner, ticket and optional
  branch/worktree. Update only meaningful changes.
- `publish_test_action`: independently testable recipe, expected result and
  `workItemId`/`target`. A package script is a name, never an arbitrary command.
- `publish_report`: worker status, summary, completed, remaining and evidence.
- `publish_step_report`: same required fields for the real lead's stage result.
  An automatic agent-defined stage requires an explicit ready report.
- `publish_artifact`: typed content; `markdown` is normalized to `document`.
- `inspect_test_environment`: fixed local Docker listing, package script names
  and explicitly selected loopback roots. Bounded and briefly cached, with no
  provider-supplied shell, arguments or environment.

The main process validates the current thread/turn ancestry, constrains authors
to their actual role, and persists interaction events before acknowledging a
successful publication. `userData/mission-structured` provides a bounded atomic
projection alongside the journal. `getMissionInteractions(taskId)` recovers it
without starting work or approving stages. Human decisions remain authoritative.

Existing Codex sessions migrate once to a versioned native-tools session key
because `thread/resume` cannot attach dynamic tools. The mission context and
acquired decisions carry forward; older thread references remain stored.

Mission renders a compact global recipe list. Starting a recipe records
`testStartedAt`; human passed/problem/deferred results clear that marker. The
latest published recipe version wins, and a recipe result does not approve the
whole mission stage. Restored servers require native readiness confirmation.

## Legacy model emitted events

An agent can send a structured UI update by emitting one JSON object on one
line with the `DJINN_EVENT:` prefix:

```text
DJINN_EVENT:{"type":"note","data":{"title":"Progress","detail":"The brief is ready."}}
```

These model event types are accepted within the native run scope:

- `question`: `data` may include `id`, `title`, `context`, `recommendation`,
  `options`, `blocking`, `unlocks`, and `agentId`.
- `artifact`: `data` may include `id`, `title`, `type`, and textual `content`.
  Documents use Markdown. `visualization` contains standalone HTML rendered in
  an iframe with scripts allowed, no same-origin permission, no native bridge,
  and a restrictive CSP (no network, forms, nested frames or external resources).
  Bounded height messages require the actual iframe sender and a native token.
- `mission_metadata`: `data.title` proposes a mission title. Only the real
  chief may emit it. A human rename is authoritative and survives later runs.
- `agent`: `data` may include `id`, `name`, `role`, `status`, `model`, and
  `summary`. Model events are queued proposals, never native running evidence.
  A worker cannot create another agent or launch a process.
- `phase`: compatibility proposal converted to a note; it cannot move workflow
  stages, approve results, or bypass blocking decisions.
- `next_step`: the chief may propose `{type,title,objective,reason}` for the
  smallest useful continuation in a flexible workflow. Types include
  `specification`, a plan/read-only discussion. The native runtime assigns the
  current `stepId`; workers, fixed workflows and enforced project workflows
  cannot propose a continuation. A proposal never adds, starts or approves a
  stage. After human result validation, the human may append it, change it, or
  end the mission without another stage.
- `note`: `data` may include `title` and `detail`.
- `action`: `data` may propose a `server`, `link`, or `manual` action. It may
  include `id`, `title`, `detail`, `script`, `directory`, `url`, and `agentId`.
  `script` is a package script name such as `dev` or `start`; it is never a
  shell command. `directory` is a relative path to a nested app package.

Malformed markers and unsupported event types remain ordinary model text;
recognized events with invalid fields produce explicit validation errors. The
main process never executes model fields and never opens a URL because an agent
mentioned one. External links can only be opened through the explicit bridge
method, which accepts HTTP(S) URLs without credentials.

Action proposals are stored in a native per-task registry and emitted again
with `status`, `createdAt`, and `updatedAt` fields. The renderer reconciles its
cards with `window.djinn.getActions(taskId)`. A link or manual action remains
pending until the user clicks it or marks it complete.

After a successful `execute` lead run, when no worker failed, was cancelled, or
blocked on a question, Djinn scans the selected project and at most two levels
of child directories for a package.json development script named `dev`,
`start`, `serve`, or `preview` (including namespaced variants). With one
candidate it starts that script automatically. With multiple candidates it
creates pending server cards and waits for the user to choose one. Plan,
review, child, import, restore, and failed runs never auto-start a server.

The native runtime resolves the package manager from package.json and lockfiles,
then spawns fixed argv such as `npm run dev` with `shell: false`. It never
installs dependencies and never interprets a model-provided command, argv,
environment, or cwd. A server is `ready` only after an HTTP(S) health probe
succeeds on a loopback URL found in its output or its selected framework port.
Servers already owned by Djinn, or explicitly selected by a persisted
loopback URL, are reused; a cold action never claims an unrelated process on a
conventional port. External processes are never killed. Server `stop` sends a
process-group signal only to servers Djinn started; app shutdown also
terminates those managed groups.

The renderer may call:

```ts
window.djinn.getActions(taskId);
window.djinn.performAction({
  taskId,
  cwd,
  action,
  operation: "run" | "stop" | "complete" | "open",
});
```

## Run modes and delegation

`startRun` accepts `mode: "plan" | "execute" | "review"`, `stepId`, and
`concurrency` from 1 to 16. The native boundary loads the saved mission and
binds the actual step, configured provider/model, project snapshot and agent
limit. The step type determines permissions: exploration/reflection use plan,
prototype/implementation use execute, review/delivery use read-only review.

Plan/review use Codex's read-only sandbox. Claude retains its permission mode
and is restricted to `Read,Glob,Grep` tools. Execute workers declare `writeScope`
as literal relative file/directory paths (no glob; `*` is exclusive), `dependsOn`
as known acyclic worker IDs, and optionally `readOnly: true`. Canonical disjoint
ownership permits parallel writers up to the configured simultaneous ceiling.
Absent/empty ownership reserves the entire root. Symlink escapes and dangling
links are rejected. Directory/file overlaps serialize. Native queued events
include `waitKind`, `waitReason`, and `waitingForAgentIds` for ownership conflicts,
dependencies or capacity. Slots refill on each completion, without a wave barrier;
unrelated workers continue if another prerequisite fails. Codex sandbox writable
roots use those prepared paths; Claude receives the ownership contract in its
prompt and keeps its configured permissions. This does not add per-file OS
enforcement to Claude. Read-only workers may run alongside writers. The chief
supervises and converses in a separate read-only thread while workers run;
its integration pass starts after the actual writers have finished. A native
mission/directory lock prevents a second pipeline in the same, parent or child
directory, including during cancellation. Targeted reprises follow the same
ownership scheduler; integration is interrupted and finishes closing before a
finished worker can revisit its scope.

`getRuntimeSnapshot()` reads current native ownership only and never launches a
provider. Its runs include actual IDs, step scope, start/last activity, phase,
active passages and a bounded replay cache (250 events / 2 million characters).
Every event has a native `eventId`; persisted IDs deduplicate snapshot/live replay.
The renderer reconnects only to a matching saved mission step. Restored active
agents come from actual native passages, even when start events were already saved.

In 0.2.1, the complete native history is separately archived in
`userData/mission-journal` as ordered JSONL, with paged reads and a flush on
normal shutdown. The export combines that archive with the mission's human
decisions and validations; it does not replace the human history with the
bounded reconnect cache. Prompt context remains bounded independently.

Codex uses local JSON-RPC `app-server --listen stdio://`, with persistent
threads per mission step and agent, and a separate supervisor thread. The
client initializes once; subsequent passages use `turn/start` or saved
`thread/resume`. `thread/resume` passes `excludeTurns: true`: Codex keeps the history and resumes the
same thread without sending all its turns back. Each JSON-RPC line is assembled on its own, even
when it arrives in fragments, and a record is capped at 32 MiB of UTF-8; going over stops the
process and keeps the cause. `turn/steer` includes the expected active turn ID. No provider
or model is silently substituted. Unsupported client tools/permission expansion
are declined. A missing app-server is an explicit runtime error.

Each native passage has a distinct `runId` and actual `stepId`; model fields
cannot replace them. Agent lifecycle events and activity lists come from actual
turn/process starts and completions. Codex message deltas update one stable
`messageId` until finalized, without parsing incomplete protocol lines. Stderr
is a warning note; explicit provider failures and unsuccessful completion
remain errors. Silence is elapsed time since observed activity, never a blocker.

Read-only proposals emitted by the active chief in plan/review join that
passage immediately. They use the same dependency, concurrency and resource
exclusion scheduler as execute workers, with read-only permission forced by
the native runtime. Resource exclusions are independent of write ownership;
CPU and memory validation have separate bounds.

Provider-created Codex children are observed from `subAgentActivity`,
`collabAgentToolCall` recipient states, and child thread/turn notifications.
Their stable card ID is `codex:<threadId>` and `origin:codex` distinguishes them
from scheduled Djinn workers. Known scheduled threads enrich the existing
card. Observed records never enter `pendingAgents` or start another provider.
Unknown child models remain unknown. Snapshot records preserve these cards,
and the end of a parent passage marks observations as no longer live without
inventing completion. Observed child messages can be read; human steering
continues through the chief rather than an unsupported direct child channel.

`steerRun({runId,id,text,agentId?})` delivers a human indication to its actual
target. Without an agent target it addresses the chief; the chief may forward
the original text to an existing worker through a note with `guidanceId` and
`forwardToAgentId`. Targeted worker messages also inform the supervisor without
changing ownership. Active Codex turns receive direct steering; completed targets
are queued for their own next passage. Claude uses controlled interruption and
reprise of the recipient only, preserving output context and waiting for actual
close before any replacement writer. Claude supervision uses the restricted
read-only CLI; it does not currently retain a native provider thread.

Guidance receipts are `queued`, `transmitted`, `consumed` (handed to the actual
recipient, not proof of semantic agreement), or `prevented` with a reason.
Unknown targets, unanswered blockers and cancelled runs produce explicit reasons.
Already acknowledged history does not consume the live queue limit. Prompt
context is bounded; complete data stays in the saved mission.

The renderer starts the current stage on a human message and automatically
resumes after all its blocking decisions are answered, including an answer
arriving during interruption. A completed AI passage yields a consultable result
awaiting human approval; it never approves itself. Legacy stages without a
`validation` field retain human approval. In 0.2.1, implementation/prototype
stages explicitly marked `validation:automatic` can continue to the next stage
after successful execution. Review and delivery always require human approval.
Selecting an already-started stage changes
navigation only; future stages remain disabled. Code delivery requires a subsequent
human-approved review. Imported/restored sessions launch no providers or servers.

For flexible missions, `workflow_amended` replaces only the pending future
suffix and records the reason; active and past stages retain their identity
and result. `next_step` is a proposal and neither starts nor approves a stage.

`notifyQuestion({taskId,questionId,title,body})` asks the native Electron
notification service to show a new blocking question. It returns
`{shown:true}` only after Electron reports the native `show` event. Unsupported
platforms, native failures, early close, and a bounded show timeout return
`{shown:false,reason:...}` instead. Djinn retains the native notification until
it closes so click delivery is not lost. Clicking the notification focuses
Djinn and emits `notification_clicked` with the task and question identifiers;
no shell or external URL is involved. The response reports dispatch state,
not that the operating system or user visibly received the notification.

Successful native step results also use this channel. Their target is encoded
as `questionId: "step:<stepId>"`; the renderer selects the owning mission and
that historical step, then opens its result/review. Live `running` transitions
to `completed` or `awaiting_human` produce one request. Boot/import, failures,
pauses, demonstration data and approval of an already announced result do not
replay notifications. A genuinely rerun step can announce its new result.

`images` may contain up to five `{id,title,dataUrl}` entries. PNG, JPEG, and
WebP data URLs are checked against their bytes (4 MiB per image and 12 MiB in
total). Codex receives materialized, private files through repeated
`--image` arguments. Claude receives a JSONL `stream-json` user message with
base64 image blocks; no directory permission flag is added. Image files live
under the run's user-data directory and are removed when that run finishes.

The provider commands are intentionally direct child processes:

```text
codex exec --json --sandbox workspace-write --skip-git-repo-check [--model MODEL] PROMPT
claude -p --output-format stream-json --verbose [--model MODEL] PROMPT
```

For `plan` and `review`, the Codex sandbox value is `read-only` and the
Claude invocation carries the read-only system instruction.

The command is resolved from the inherited PATH plus common Finder-launch
locations, including the bundled Codex CLI inside ChatGPT.app. Version checks,
auth checks, login, and runs all use that same resolved executable. The Claude
command keeps its default permission mode. Djinn does not pass a
bypass-permissions flag. Cancellation sends a signal to the detached process
group (or uses `taskkill /T /F` on Windows) after a short grace period.

## Portable sessions

Exported sessions use version 2:

```json
{
  "format": "djinn-session",
  "version": 2,
  "exportedAt": "2026-10-05T12:00:00.000Z",
  "tasks": [],
  "projects": []
}
```

For compatibility, imports also accept
`{"format":"djinn-session","version":1,"task":{...}}` and the persisted
state shape `{"version":1,"tasks":[]}`. Import validation only returns data;
it does not start a provider, launch an agent, or execute a command.

The Electron `exportSession` method opens a native save dialog and writes the
validated JSON atomically. `importSession` opens the matching native file
picker and returns the validated portable envelope to the renderer.

## Projects, sessions and recovery

Workspace/session version 2 persists reusable projects, dated convention and
workflow snapshots, stable steps, per-step records and native conversation IDs.
Renderer and Electron validators derive from the same TypeScript workflow/session
sources; the unit suite checks generated CommonJS parity. Native project targets
are relative paths and symlink-aware checked against the actual project root.
Text conventions are context only, never shell commands. Imports clear machine
provider sessions and require the real local path to pass validation before a run.

Version 1 migrates to four compatible stages without inventing completion times
or human approvals. Unprovable records remain an explicit legacy history. Native
persistence validates the migration before saving and keeps `state.json.v1.backup`.
Migration is idempotent. Invalid saved bytes are preserved and autosave is suspended
with an error visible in the renderer. Import/export is atomic and never executes
the imported contents.

The transport is implemented against the installed CLI schema and the official
[Codex app-server protocol](https://learn.chatgpt.com/docs/app-server).

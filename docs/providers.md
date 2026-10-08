# Providers

A provider is the agent a task's worker runs: Claude Code, Codex, Antigravity, or the fake one. Each lives in
`internal/harness/` (`claude.go`, `codex.go`, `agy.go`, `fake.go`) and turns what its agent says into the same
task events: `STATUS`, `TEXT`, `TOOL_CALL`, `TOOL_RESULT`, `USAGE`, `ERROR`, `LOG`, `OTHER`.

> **The rule of this file.** Every real case met while trying a provider with a real model becomes a fixture in
> `internal/harness/testdata/<provider>/` and a row of that provider's table below, with the test that replays
> it. A fixture says at its top whether it was captured or written by hand, and from what.

Tests never call a model. `TestCatalog` (`internal/harness/providers_test.go`) replays every fixture through the
real process path: the test binary plays the provider (`fakeproc_test.go`), Djinn starts it, writes to it, reads
it, and the test checks the events and how the worker ended. A fixture whose file is in no row fails the test.

**Fixture format.** One line of the agent's output per line; lines starting with `#` are comments. A
`<case>.stderr` file next to it holds the agent's error output. For Codex, the lines are the app-server's side of
the JSON-RPC session: the replayer gives each answer the id of Djinn's next request, and waits for Djinn's reply
after a request of the app-server.

## What a worker may do

**Cross-agent first: a project says once, in `.agents/permissions.txtpb`, what any agent may do in it, and Djinn
translates it for the agent it starts. A task outside any project only reads.**

### The order of decision

Djinn decides when it spawns a task, and records the outcome in the task's `access` (`TaskAccess`, in
`api/plan/v1/plan.proto`). The first rule that applies wins:

1. **Outside any project** (a wish without project): read-only, in an empty folder of its own (`READ_ONLY`).
2. **The wish's allowance** for that project (`djinn wish allow <wish> --project-id <p> --mode edit|auto|none`,
   stored in `Wish.allowances`): an explicit decision of the developer, for every task of that wish in that project
   and no other wish. `edit` lets the worker edit (`WISH_EDIT`); `auto` lets it edit in its agent's auto mode
   (`WISH_AUTO`). It weighs over the project's configuration: when the project has `.agents/permissions.txtpb`,
   the allowance decides editing and the mode, and the commands, denied commands and network stay the file's;
   an allowance never adds a command nor the network.
3. **`.agents/permissions.txtpb`** in the project's folder: Djinn translates it for the agent (`AGENTS`). The
   agent's own configuration files still apply on top, as the agent applies them by itself; what Djinn passes
   never allows more than the file. Djinn reads the file in the project's folder, not in the task's worktree: a
   worker that edits its own copy does not change its rights. An invalid file refuses the spawn, and says why.
4. **The agent's own configuration** (`NATIVE`, decision Q34): in a Git repository, or in a folder that holds an
   agent configuration file. Djinn passes no permission setting. In a Git repository without any configuration,
   the agent's defaults decide: Claude, Codex and Antigravity then refuse edits that would need an approval,
   since nobody answers one.
5. **A folder outside Git without any agent configuration** (`ASKING`): the worker starts read-only, and the task
   asks the developer, as a question of the wish: "May the worker of task W.. change the files of <project>?",
   options A (yes) and B (no). Without an answer nothing is edited; when the read-only worker ends, the task is
   `WAITING`. **No** (`EDIT_REFUSED`): it stays read-only, and a waiting task is done. **Yes** (`EDIT_GRANTED`):
   the worker starts again, resuming its session, allowed to edit the project's files and to run no command; a
   worker still running is stopped first, and its watchers follow both in one stream. A yes never grants the auto
   mode. Only the first answer counts; a task stopped on request stays stopped. An agent that cannot run
   read-only (Antigravity) does not start before the answer.

**What counts as agent configuration** (rule 4), at the root of the project's folder, file names compared without
regard to case: for every agent, `AGENTS.md` and an `.agents` folder; for Claude, `CLAUDE.md`, `CLAUDE.local.md`,
`.claude/CLAUDE.md`, `.claude/settings.json`, `.claude/settings.local.json`; for Codex, `AGENTS.override.md` and a
`.codex` folder; for Antigravity, `GEMINI.md`. Their presence counts, whatever they say: whether a Markdown file
"describes the tools" is for a model to judge, and the lamp decides without one. Antigravity keeps its project
settings in its own store, not in the folder, so they cannot be seen.

### The `.agents/permissions.txtpb` format

A `djinn.v1.Permissions` message (`api/djinn/v1/agents.proto`) in text protobuf, like Djinn's own configuration,
read straight into the message and validated. It lives in `.agents/`, the folder Codex and Antigravity already read
for skills. Djinn's own: [`.agents/permissions.txtpb`](../.agents/permissions.txtpb).

| Field             | Meaning                                                                                                                                      |
| ----------------- | -------------------------------------------------------------------------------------------------------------------------------------------- |
| `edit`            | May create, change and delete files in the project.                                                                                          |
| `commands`        | Prefixes of whole words the agent may run without review: `go tool task test` allows `go tool task test -- -run X`, not `go tool task test-go`. |
| `denied_commands` | Prefixes never run, in any mode where the agent can enforce it; they win over `commands`.                                                    |
| `network`         | May the agent's own web tools, and its sandbox, reach the network. Its model's API is always reached.                                        |
| `mode`            | `MODE_LISTED` (the default): what is not listed is refused. `MODE_AUTO`: the agent's own auto mode decides, as its provider documents it. AUTO needs `edit`: without it, the mode is LISTED, since a reviewer could approve a command that writes. |

Reading is always allowed. A word holds no wildcard nor shell operator (the file is refused otherwise), so a
prefix never widens into a pattern.

### How each agent receives it

| Field             | Claude (`--settings` inline, `--permission-mode`)                    | Codex (`thread/start`, `turn/start`, approvals)                                                   | Antigravity (command line)                          |
| ----------------- | -------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------- | --------------------------------------------------- |
| `edit`            | allow, or deny, `Edit`, `Write`, `NotebookEdit`                      | `sandbox: workspace-write` and turn `sandboxPolicy: workspaceWrite`, else `read-only`; file-change approvals accepted only with `edit`, inside the workspace | `--mode accept-edits`; without `edit`, refused      |
| `commands`        | allow `Bash(<p> *)` and `PowerShell(<p> *)`                          | LISTED: `approvalPolicy: untrusted`, and Djinn accepts a command approval when the command is listed | **lost**: none at launch                           |
| `denied_commands` | deny `Bash(<p> *)` and `PowerShell(<p> *)`                           | LISTED: Djinn declines them. AUTO: **lost**, codex's reviewer decides                              | **lost**                                            |
| `network`         | allow, or deny, `WebFetch` and `WebSearch`                           | `networkAccess` of the sandbox policy; a command approval asking for the network is declined without it | off: `--sandbox`, which only restricts more      |
| `mode`            | LISTED: `--permission-mode dontAsk`; AUTO: `--permission-mode auto`  | AUTO: `approvalPolicy: on-request`, `approvalsReviewer: auto_review`                              | AUTO: **lost**, runs as `accept-edits`              |

What is verified and what is supposed:

- **Claude**, from `claude --help` (2.1.293) and its documentation (`code.claude.com/docs/en/permissions`,
  `/permission-modes`, read 2026-10-08): `--settings` takes inline JSON and adds to the settings files; deny rules
  win wherever they come from; `Bash(p *)` matches `p` alone and `p` followed by arguments, not `pX`, and every
  part of a compound command must match; an `Edit` rule covers every tool that edits files; `dontAsk` refuses
  what no rule allows; `auto` has a classifier review what no rule decides, and auto-approves edits in the working
  folder, so `edit: false` is a deny rule. In a `-p` run, when the classifier blocks repeatedly, the action does
  not run and the session goes on. **Lost:** network off does not stop a command that reaches the network (only a
  sandbox would); in AUTO the classifier may approve a command that is not listed. Auto mode needs a supported
  model and may be turned off by the organization: Claude then starts in its default mode, and refuses what would
  prompt.
- **Codex**, from the generated protocol schema (`codex-rs/app-server-protocol/schema/typescript/v2`, main, read
  2026-10-08): `sandbox` (`read-only`, `workspace-write`), `approvalPolicy` (`untrusted`, `on-request`, `never`),
  `approvalsReviewer` (`auto_review`: "a carefully prompted subagent ... before approving or denying the
  request"), the `workspaceWrite` policy and its `networkAccess`, and the approval answers (`accept`, `decline`).
  **Supposed:** that the `command` of an approval is the command line as the agent wrote it (a command wrapped in
  `bash -lc` does not match a prefix, and is declined); that an accepted command may run outside the sandbox when
  codex asked for that. **Lost:** codex takes no list of commands at launch: in LISTED, Djinn answers its
  approvals; in AUTO, its reviewer decides every approval, denied commands included.
- **Antigravity**, from `agy --help` (1.3.0) and the documentation in its binary: `--mode` takes `accept-edits` or
  `plan`; `--sandbox` restricts the terminal. **Supposed:** that `--sandbox` blocks the network; that agy 1.3.0 has
  no auto mode reachable from its command line (its binary mentions one, without a flag). **Lost:** commands (its
  headless runs do not apply allow rules: "Settings allow-rules do not apply"), denied commands, AUTO.

### Instructions: every agent reads `AGENTS.md`

| Agent       | What it reads, and how                                                                                                                                                         |
| ----------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Claude      | `CLAUDE.md`, or `AGENTS.md` when the project has no `CLAUDE.md` (claude 2.1.277 and later). Djinn passes `instructionFiles: claude-md-and-agents-md` to its AGENTS.md plugin in `--settings`, so a worker reads both, an import read once. Djinn's `CLAUDE.md` imports `AGENTS.md`. Verified: `code.claude.com/docs/en/memory`. |
| Codex       | `AGENTS.md` from the repository's root down to the working folder, by itself (its documentation).                                                                              |
| Antigravity | `GEMINI.md` and `AGENTS.md`, walking up from the working folder to the repository's root, by itself (the rules documentation in the agy 1.3.0 binary).                          |

### Outside any project

|             | Outside any project (read-only)                                                                                                                 |
| ----------- | ----------------------------------------------------------------------------------------------------------------------------------------------- |
| Claude      | `--restricted --tools Read,Glob,Grep --permission-mode plan --strict-mcp-config`: reading tools only, no settings files, no MCP server, no hook |
| Codex       | `sandbox: read-only`, `approvalPolicy: never`, turn `sandboxPolicy: readOnly` without network; it reads with commands confined there (Q37)      |
| Antigravity | refused: plan mode is not read-only and what `--sandbox` blocks is undocumented; allowed once a real capture proves it                          |

The same applies to a worker of rule 5 before the answer. In a project without `.agents/permissions.txtpb` nor
allowance, nothing above is passed: Claude gets `--permission-prompts none` (anything that would prompt is denied),
codex a thread without `sandbox` nor `approvalPolicy` (an approval it asks for is declined), agy no `--mode` (a tool
needing approval is soft-denied). Supposed, for Claude read-only: that `--restricted` with `--tools` leaves nothing
able to write or run code. For agy: that plan mode writes nothing in headless mode, and the sandbox blocks every
command, are to check on a real run.

## Claude

**Command.** `claude -p --input-format stream-json --output-format stream-json --verbose
--permission-prompts none`, then in a project `--settings <json>` (and `--permission-mode` with permissions; see
above), then `--session-id <task id>` (or `--resume <session>`, `--fork-session`),
`--model`, `--max-budget-usd`. The prompt is a stream-json user message on the input; the input stays open for
more messages and is closed once each has its result, which ends the process.

**Stream.** One JSON message per line: `system/init` (session, model), `assistant` and `user` messages made of
blocks (`text`, `tool_use`, `tool_result`, `thinking`), and one `result` per turn with the cost.

**Verified** on real runs (claude 2.1.293, Opus 5.5 and Haiku 5.5): the stream above; the usage is read from
`modelUsage` (summed over the models), the root `usage` only when `modelUsage` is absent; `--max-budget-usd` is
checked after a model call. Djinn drops on purpose: `rate_limit_event` while its status is `allowed` (another
status becomes a `STATUS`), `system/thinking_tokens` (an estimate the result counts), and an assistant message
holding only thinking whose text is withheld. A thinking block with text is kept as `OTHER`.
**Supposed:** the exit code after an error result (1), the text of a refused tool, the hand-written cases below.

| Case                                          | Fixture                          | Source                                      | What Djinn records                                                                                                  |
| --------------------------------------------- | -------------------------------- | ------------------------------------------- | ------------------------------------------------------------------------------------------------------------------- |
| Simple success                                | `claude/success.jsonl`           | by hand                                     | session, text, usage                                                                                                |
| Tool calls                                    | `claude/tool-call.jsonl`         | by hand                                     | tool calls and results, thinking as other                                                                           |
| Success with thinking and a rate limit notice | `claude/success-haiku.jsonl`     | **real**, Haiku 5.5                         | the withheld thinking, `thinking_tokens` and the `allowed` rate limit are dropped; usage from `modelUsage`          |
| Error during the turn                         | `claude/error.jsonl`             | by hand                                     | error; task failed                                                                                                  |
| Budget reached                                | `claude/budget-reached.jsonl`    | by hand                                     | error, usage with the cost; task failed                                                                             |
| Cap passed on the first call                  | `claude/budget-first-call.jsonl` | **real**, Opus 5.5, `--max-budget-usd 0.10` | the cap is checked after the call: $0.18 spent for $0.10 allowed, mostly a 22,160-token cache write on a cold start |
| Root usage all zeros                          | `claude/budget-first-call.jsonl` | **real**                                    | tokens read from `modelUsage`, not from the root `usage`                                                            |
| Permission denied                             | `claude/permission-denied.jsonl` | by hand                                     | the tool result as an error, then `permission denied: Bash {…}` from `permission_denials`                           |
| Session resumed                               | `claude/resume.jsonl`            | by hand                                     | `--resume`, the resumed session                                                                                     |
| Unknown lines                                 | `claude/unknown-line.jsonl`      | by hand                                     | each kept as `OTHER`, raw                                                                                           |
| Process dies mid-turn                         | `claude/process-dies.jsonl`      | by hand                                     | error "claude ended before the end of its turn"; task failed                                                        |
| Second message                                | `claude/two-turns.jsonl`         | by hand                                     | two turns in one process                                                                                            |

## Codex

**Command.** `codex app-server --listen stdio://`: JSON-RPC 2.0, one message per line on stdio. Djinn sends
`initialize` (client `djinn`), then `initialized`, then `thread/start` with the worktree as `cwd` (or
`thread/resume` / `thread/fork` with `excludeTurns`), then one `turn/start` per message, in order: a message sent
during a turn waits for the next one. Once the last turn has completed, Djinn closes the input; an app-server
still running after the grace delay is stopped, and that is no failure.

**Stream.** Answers to Djinn's requests; notifications `item/started` and `item/completed` (items
`agentMessage`, `commandExecution`, `fileChange`, `mcpToolCall`, `reasoning`…), `thread/tokenUsage/updated`,
`error` (with `willRetry`), `turn/completed` (status `completed`, `failed`, `interrupted`); and requests of the
app-server. Djinn answers `item/commandExecution/requestApproval` and `item/fileChange/requestApproval` from the
worker's permissions (`accept` what they cover, `permission granted: …`), and declines them otherwise
(`{"decision":"decline"}`, `permission denied: …`); it refuses any other request (JSON-RPC error -32601). Dropped on purpose: deltas
(their item comes whole once completed), `turn/started`, `thread/started`, and the user's message item.

**Usage.** Tokens only, no cost: `thread/tokenUsage/updated` gives the thread's total. Djinn counts the cached
input apart (`input = inputTokens − cachedInputTokens`), which supposes that `inputTokens` includes the cached
ones, as in the OpenAI API.

**Verified:** the message shapes, against the TypeScript schema generated in the `openai/codex` repository
(`codex-rs/app-server-protocol/schema/typescript/v2`, main, read 2026-10-08), and the command line and handshake,
against the code that drove a real codex before Djinn's Go rewrite. **Supposed:** that the app-server ends when
its input closes; the fields of the `initialize` answer; that a resumed thread's usage counts its earlier turns
too. Nothing has been captured from a real codex yet: see "Codex: to check" below.

| Case                       | Fixture                         | Source  | What Djinn records                                                                     |
| -------------------------- | ------------------------------- | ------- | -------------------------------------------------------------------------------------- |
| Simple success             | `codex/success.jsonl`           | by hand | session, text, usage, `turn completed`                                                 |
| Tool calls                 | `codex/tool-call.jsonl`         | by hand | command and file change as tool calls and results, reasoning summary as other          |
| Error after a retry        | `codex/error.jsonl`             | by hand | `retrying: …`, then the error once; task failed                                        |
| Usage limit                | `codex/limit.jsonl`             | by hand | `usageLimitExceeded: …`; task failed                                                   |
| Permission denied          | `codex/permission-denied.jsonl` | by hand | the approval declined, `permission denied: commandExecution …`, the command `declined` |
| Listed command approved    | `codex/permission-denied.jsonl` | by hand | with `.agents` listing `go test`: `untrusted`, `workspace-write`, the approval accepted |
| Thread resumed             | `codex/resume.jsonl`            | by hand | `thread/resume` with `excludeTurns`                                                    |
| Thread to resume unknown   | `codex/resume-missing.jsonl`    | by hand | the request's error; task failed                                                       |
| Unknown lines and requests | `codex/unknown-line.jsonl`      | by hand | kept as `OTHER`; an unknown request refused                                            |
| Process dies mid-turn      | `codex/process-dies.jsonl`      | by hand | error "codex ended before the end of its turn"                                         |
| Second message             | `codex/two-turns.jsonl`         | by hand | a second `turn/start` once the first completed                                         |

### Codex: to check

On a machine with `codex` signed in, in an empty Git folder, capture the app-server's side of a short session,
then turn it into fixtures:

```sh
mkdir -p /tmp/codex-try && cd /tmp/codex-try && git init -q && echo "# Try" > README.md
codex --version
printf '%s\n' \
  '{"id":1,"method":"initialize","params":{"clientInfo":{"name":"djinn","title":"Djinn","version":"0.3.0"}}}' \
  '{"method":"initialized"}' \
  "{\"id\":2,\"method\":\"thread/start\",\"params\":{\"cwd\":\"$PWD\"}}" \
  | codex app-server --listen stdio:// > thread.jsonl 2> thread.stderr
```

That run opens a thread without a turn: no model call. For one cheap turn, the `turn/start` needs the thread id
the app-server answered, so it goes through Djinn itself once this branch is merged:
`djinn task spawn <wish> --provider codex --prompt "Reply with the word apple, nothing else." --model <the
cheapest model>`, then `djinn task watch <task> --raw --json > turn.jsonl`. A second run asking to run
`go test ./...` in that folder shows a permission request.

## Antigravity

**Command.** `agy --input-format stream-json --output-format stream-json`, then `--conversation <id>` to resume,
`--model`; outside a project, refused until a real capture proves a read-only mode. No `-p`: it takes the prompt as its value, and stream-json
input replaces it. Each message on the input is `{"event":"user","message":{"content":"…"}}` and runs one turn;
the input is closed once every message has its result. agy cannot fork a conversation, and has no spending cap.

**Stream.** `{"event":"init","conversation_id":…}`, then `step_update` lines (`step_type` `user_input`,
`agent_response` with `text_delta`, `tool` with `tool_info` {name, parameters, output}, `checkpoint`), and one
`result` per turn (`status` `SUCCESS`, `ERROR`, `CANCELED`, `INTERRUPTED`, `INVALID`, `WAITING`; `usage` with
`input_tokens`, `output_tokens`, `thinking_tokens`, `cache_read_tokens`, cumulative over the conversation). On a
model or agent failure, agy writes `AGY_ERROR: {…}` on its error output and exits with code 3. Djinn gathers the
text deltas of a step and says them once the step is done (a response cut by the end of the process is said
with `[cut]`), counts thinking tokens as written ones, reads `AGY_ERROR` as an error and the soft-denial notice
as `permission denied: …`. Tokens only, no cost.

**Verified:** the command line, from `agy --help` (1.3.0); the stream's shapes, from the official headless
documentation (`antigravity.google/docs/cli/headless`, read 2026-10-08); the field names `event`, `step_update`,
`text_delta`, `tool_info`, `subagent_info`, `short_error`, `retryable`, `http_status`, and the soft-denial notice,
found in the agy 1.3.0 binary; exit code 3 and `AGY_ERROR`, from `agy changelog`. **Supposed:** every fixture
(none is captured), the order of the steps, whether a tool step is seen `ACTIVE` before `DONE`, how a soft-denied
tool looks in the stream, the other fields of `AGY_ERROR`, and whether `cache_read_tokens` is part of
`input_tokens`.

### Signing in

Djinn passes its environment on to agy and nothing else: it never reads agy's credentials nor the Application
Default Credentials file, and sets no sign-in variable itself. A variable the user sets where `djinn up` runs
reaches agy.

**First choice: Application Default Credentials on the team's Google Cloud project.** Each developer signs in
with their own Google identity; the model use is billed to the team's project, and its audit logs say who made
each call.

1. Once for the team, an administrator of the project enables Vertex AI and grants each developer the right to
   call it:
   `gcloud services enable aiplatform.googleapis.com --project <project>`, then
   `gcloud projects add-iam-policy-binding <project> --member user:<developer email> --role roles/aiplatform.user`.
2. Each developer installs the Google Cloud CLI, then signs in:
   `gcloud auth application-default login --project <project>`. A browser opens; the credentials stay on the
   developer's machine.
3. In the shell profile that starts `djinn up`, the developer sets `export AGY_ADC_AUTH=true` (and, if agy asks
   which project to use, `export GOOGLE_CLOUD_PROJECT=<project>`), then restarts `djinn up`.
4. Check, outside Djinn, without calling a model: `agy models` should list the models of the project.

**Second choice: agy's own sign-in, with a Google account.** For a developer without the team's project, or to
use a personal or licensed account.

1. Run `agy` once in a terminal. It opens a browser to sign in (over SSH it prints a link to open elsewhere and
   asks for the code it gives). With a business account, pick the license, linked to its Google Cloud project.
2. agy keeps the session in the system's keyring (Keychain, Secret Service, Credential Manager); `/logout` in agy
   removes it. Leave `AGY_ADC_AUTH` unset.
3. Check with `agy models`, as above.

| Case                    | Fixture                                           | Source                | What Djinn records                                                 |
| ----------------------- | ------------------------------------------------- | --------------------- | ------------------------------------------------------------------ |
| Simple success          | `antigravity/success.jsonl`                       | by hand, **supposed** | session, the deltas as one text, usage                             |
| Tool calls              | `antigravity/tool-call.jsonl`                     | by hand, **supposed** | a call per tool step, its output as the result                     |
| Model error             | `antigravity/error.jsonl` + `.stderr`             | by hand, **supposed** | the cut text, `AGY_ERROR`, the result's error; exit 3, task failed |
| Quota exhausted         | `antigravity/limit.jsonl` + `.stderr`             | by hand, **supposed** | `… (HTTP 429), retryable`; task failed                             |
| Permission soft-denied  | `antigravity/permission-denied.jsonl` + `.stderr` | by hand, **supposed** | `permission denied: the run_command tool(s) …`; the task goes on   |
| Conversation resumed    | `antigravity/resume.jsonl`                        | by hand, **supposed** | `--conversation`, the resumed conversation                         |
| Unknown lines           | `antigravity/unknown-line.jsonl`                  | by hand, **supposed** | each kept as `OTHER`, raw                                          |
| Process dies mid-answer | `antigravity/process-dies.jsonl`                  | by hand, **supposed** | the cut text, error "agy ended before the end of its turn"         |
| Second message          | `antigravity/two-turns.jsonl`                     | by hand, **supposed** | two turns in one process                                           |

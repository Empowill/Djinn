# Providers

A provider is the agent a task's worker runs: Claude Code, Codex, Antigravity, or the fake one. Each lives in
`internal/harness/` (`claude.go`, `codex.go`, `agy.go`, `fake.go`) and turns what its agent says into the same
task events: `STATUS`, `TEXT`, `TOOL_CALL`, `TOOL_RESULT`, `USAGE`, `ERROR`, `LOG`, `OTHER`. A
[watcher](#watch-a-command-no-agent) (`watch.go`) runs a command instead of an agent.

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

## How a worker ends

A task is `DONE` only when its agent finished its turn normally. Any stop on an error ends `FAILED`, with the
reason in the task's error, whatever the provider. Two places decide:

- **The provider** turns how its agent ended into a result: an error the agent reports (a result with `is_error` or
  an error subtype, a failed or unknown turn status, a JSON-RPC error, a denial that ends the turn), the process's
  exit code, a stream that ends before the turn's result ("… ended before the end of its turn").
- **The harness** gives the final status (`end` in `harness.go`): stopped on request, interrupted by `djinn up`
  stopping, then failed on the result's error, on an exit code other than 0, or on any `ERROR` event the worker
  said on the way, even when its process then exited 0. Only then is it done.

A refused permission is not an error in itself: Claude and Codex tell the agent, which goes on and ends its turn
(`permission denied: …` is a `STATUS`). Antigravity's denial ends its turn: the task fails (below).

## A task's seal

A task's seal is the set of constraints Djinn gives the worker of a task: what it may spend, what it may touch, how
it shares the machine. Each part comes from a default of Djinn, the project's settings (`.agents/settings.txtpb`,
the developer's file over the repository's), the wish (its allowance), the task's spawn request, or Djinn's own
flags; none of it is the worker's to change.

| Part | What it bounds | Where it comes from |
| ---- | -------------- | ------------------- |
| Silence limit | 15 minutes without an event, no process busy, no gate held or awaited: the Lamplighter ends the worker, then [wakes it](#when-djinn-resumes-a-worker); never while an [ifrit](#ifrit-an-agent-whose-input-stays-open) listens | Djinn's default (`SilenceLimit`) |
| Wall time | 2 hours of work over all its runs, gate waits included, an ifrit's listening aside; past it the task is stopped, "budget exceeded", and stays stopped | Djinn's default (`TaskMaxWallTime`) |
| An investigation's limits | 25 minutes of work and $10 by default; at either, Djinn sends it a message asking it to publish its tilasm and question (`Task.investigation.publish_time`), and stops it 10 minutes later; its provider's cap is 1.5 times its budget, so that the last turn is paid; Claude's spending is estimated from its token counts as it goes (`Event.Spent`) | the spawn request (`minutes`, `max_budget_usd`), else Djinn's defaults (`DefaultInvestigationMinutes`, `DefaultInvestigationBudgetUSD`) |
| Model turns | 500 turns in one run (`Task.turns`, shown `37/500`); past it the run is stopped; `djinn task continue` gives 500 again | Djinn's default (`TaskMaxTurns`) |
| Wakes and resumes | 3 wakes in a row, 3 resumes on usage limits, 2 replays after an environment failure | Djinn's defaults |
| Spending cap | US dollars over its runs; Claude alone enforces it (`--max-budget-usd`), Codex and Antigravity have none | the spawn request (`max_budget_usd`), else the project's `max_budget_usd`; a question worker's, `question_budget_usd` (2 by default) |
| Access | editing, the auto mode, its commands and the network ([what a worker may do](#what-a-worker-may-do)), applied by each agent's own sandbox | the wish's allowance (`djinn wish allow`), else `.agents/permissions.txtpb`, else the agent's own configuration, else the developer's answer |
| No push | every `git push` of the worker goes nowhere ([no worker pushes](#no-worker-pushes)) | always |
| Its calls to Djinn | what it may call with its token (`DJINN_WORKER_TOKEN`): its own wish's questions, blocks and tilasms, its own task, reads; never a new wish | always (`plan.WorkerGuard`) |
| Write scopes | outside Git, two tasks whose scopes overlap never run together | the spawn request (`write_scopes`) |
| CPU priority | `nice 10` at the Minimal to Medium load notches ([operating load](#operating-load-runtime-adjustments)) | the machine's load notch |
| Memory and slots | a memory ceiling in its own scope, a slot among the workers, room to start | `djinn up --workers`, `--worker-memory`, `--worker-memory-guard`, `--worker-cpu`, and the load notch |
| Gates | one holder per gate, held an hour at most unless its holder states another timeout | Djinn's defaults ([gates](user-guide.md#gates)) |

The task carries only some of it: its turns and their limit (`turns`), its spending cap, its access, its write
scopes. The other parts are Djinn's, and the same for every task.

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
3. **`.agents/permissions.txtpb`** of the project: Djinn translates it for the agent (`AGENTS`). The
   agent's own configuration files still apply on top, as the agent applies them by itself; what Djinn passes
   never allows more than the file. Djinn reads the file on the project's reference branch, at the last fetch (in
   the project's folder outside Git or without a remote: see [team settings](team-settings.md#where-djinn-reads-it)),
   never in the task's worktree: a worker that edits its own copy does not change its rights. An invalid file refuses the spawn, and says why.
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

**A question worker** (`Task.role`, the investigator Djinn starts on a question, see the [agent
protocol](agent-protocol.md)) skips these rules: its access is `DJINN`, the same everywhere. It reads, in its
project's folder without a worktree (or an empty folder of its own outside any project), searches the web and fetches
pages (`WebFetch` and `WebSearch`, `read_url(*)` and `search_web`, `networkAccess`), and runs only djinn's
commands to read the wish and plan from the question (`wish brief`, `task list`, `task get`, `question list`, `block
list`, `mark list`, `tilasm list`, `tilasm get`, `task spawn`, `question ask`, `question revise`, `block put`) and
Git's to read the history; no edit, no gate, in the listed mode (Claude's `dontAsk`). Djinn translates
this list as it does a project's file. Claude allows each command with its arguments (`Bash(djinn task spawn *)`, the
same as `Bash(djinn task spawn:*)`), and `dontAsk` still runs file reads in the working folder, the project's, so
Read, Grep and Glob read it (`code.claude.com/docs/en/permissions`, read 2026-10-10). But every part of a compound
command must be allowed: `djinn task list … | grep …` is refused whole. Its prompt says so: each `djinn` command alone
in its own Bash call, files read with Read, Grep and Glob, the web searched with its web tools, and a refusal is of
that form, not of Bash. One that ends without doing anything after a refusal fails, and Djinn starts it again once
(`internal/harness/question.go`). Claude Code also refuses, whatever its allow rules, a quoted argument where a
line starts with `#`, indented or not (its "newline-hash" check, 2.1.300 to 2.1.302 at least): the shape of a
question puts a `# …` comment above each command, and W80 of wish Dew 13 oct could not revise Q32 (W153). So a Claude
question worker writes in one folder, its own under Djinn's data folder (`tasks/<id>`, `Spec.WriteDirs`, translated to
`Edit(//…/**)` and `Write(//…/**)` with the edit denials dropped; `dontAsk` refuses a write anywhere else), and its
prompt says to write the context whole in `context.md` there, then revise with `--context-file`. Verified on
2026-10-17 with claude 2.1.302 in `dontAsk`: the write and the revision run, the inline `--context` form is refused,
a write in the project or beside the folder is refused. A Codex question worker gets no such folder (`writesDirs`):
Codex's workspace-write sandbox would open its workspace, the project, with it; it revises with `--context`.

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

#### No worker pushes

Pushing is the orchestrator's. A denied `git push` is only a first line: a prefix, it lets `sh -c "git push"` or
`git -C . push` through, under Claude's rules as under agy's deny grants. So Djinn makes the push impossible by
environment, for every worker whatever its provider (Claude, Codex, Antigravity, the fake; question workers
alike): their environment gives Git a command-line configuration (`GIT_CONFIG_COUNT`, after the user's own)
that includes, in the project's repository only (`includeIf.gitdir` on its `.git` and its worktrees), Djinn's
`git/no-push.gitconfig`. It holds one empty `pushInsteadOf`, a prefix of every URL: any push from the worker, to a
remote, to one it added, to a path or URL on the command line, `--no-verify` or not, goes to the
`djinn-workers-do-not-push://` transport, which Git does not have, and fails. Fetching, committing, merging,
rebasing, stashing and worktrees work as before, and the project's own hooks still run. Other repositories (the
ones a project's tests make) push as before. The orchestrator's push from the integration worktree runs in Djinn's
own environment, which never holds the guard. What the guard does not stop: a command that clears Git's environment
first, or a remote given an explicit `pushurl` (which `pushInsteadOf` leaves alone).
`TestGitGuardKeepsWorkersFromPushing` (`internal/harness/guard_test.go`) proves each case.

Next to it, `.agents/settings.txtpb` gives the project's workers their default agent, model, budget and branch
name, shared by the team, your own file winning: [team settings](team-settings.md).

### How each agent receives it

| Field             | Claude (`--settings` inline, `--permission-mode`)                    | Codex (`thread/start`, `turn/start`, approvals)                                                   | Antigravity (command line, Djinn's agy project)     |
| ----------------- | -------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------- | --------------------------------------------------- |
| `edit`            | allow, or deny, `Edit`, `Write`, `NotebookEdit`                      | `sandbox: workspace-write` and turn `sandboxPolicy: workspaceWrite`, else `read-only`; file-change approvals accepted only with `edit`, inside the workspace | `--mode accept-edits`, and `write_file` grants of the worker's folder and Git's folders; without `edit`, refused |
| `commands`        | allow `Bash(<p> *)` and `PowerShell(<p> *)`                          | LISTED: `approvalPolicy: untrusted`, and Djinn accepts a command approval when the command is listed | LISTED: `command(<p>)` grants; AUTO: `command(*)`, every command |
| `denied_commands` | deny `Bash(<p> *)` and `PowerShell(<p> *)`                           | LISTED: Djinn declines them. AUTO: **lost**, codex's reviewer decides                              | deny `command(<p>)` grants; AUTO adds `git push`   |
| `network`         | allow, or deny, `WebFetch` and `WebSearch`                           | `networkAccess` of the sandbox policy; a command approval asking for the network is declined without it | not cut: no `--sandbox`, which would cut Djinn's own socket (`djinn gate`); the developer's choice, Q66 (agy 1.3.3, 2026-10-10) |
| `mode`            | LISTED: `--permission-mode dontAsk`; AUTO: `--permission-mode auto`  | AUTO: `approvalPolicy: on-request`, `approvalsReviewer: auto_review`                              | AUTO: runs as `accept-edits`, with `command(*)` above and `read_url(*)` (Q143) |

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
- **Antigravity**, from `agy --help` (1.3.1), the documentation in its binary, its public documentation (terminal
  sandbox, permissions, headless mode, read 2026-10-08) and three real runs (2026-10-08): `--mode` takes only
  `accept-edits` or `plan` (`--mode auto` is refused with a warning); `--sandbox` turns the sandbox on, off by
  default in the CLI (`enableTerminalSandbox: false`). On Linux it is kernel namespaces, seccomp and a filtering
  HTTP proxy. **Verified in the sandbox, with `--dangerously-skip-permissions`:** the home folder is read-only,
  `~/.ssh` is hidden, `~/.gitconfig` is readable, `/tmp` is the host's and writable, host processes are hidden, DNS
  and raw TCP are cut, but HTTPS through its proxy went out (`curl https://example.com` gave 200, `git ls-remote
  https://github.com/...` worked); and a command run with `BypassSandbox: true` ran on the host without any prompt (a
  file created in the home folder). So `--dangerously-skip-permissions` approves both the network asks and the way
  out of the sandbox: with it, `--sandbox` is no limit. **Supposed:** that `toolPermission: proceed-in-sandbox` in
  agy's settings runs sandboxed commands headless and soft-denies the way out (documented, not tried).
  **Verified on real runs (agy 1.3.3, 2026-10-10, without `--dangerously-skip-permissions`):** agy takes no
  permission at launch, but `--project <id>` runs it in one of its projects, a JSON file in
  `~/.gemini/config/projects/`, whose `permissionGrants` it applies headless on top of the user's settings, untouched
  (see [Antigravity](#antigravity)). So Djinn writes one project of its own per worktree. In AUTO it grants
  `command(*)` (any command runs, in the sandbox, but the denied ones) and `read_url(*)` (any URL fetched with
  `read_url`, Decision Q143; real runs, agy 1.3.3, 2026-10-10). **Lost:**
  a review of what runs (agy has no auto mode: `command(*)` approves every command, where Claude's classifier reviews
  them); a deny grant is a prefix, so `sh -c "git push"` or `git -C . push` gets past it (as past Claude's rules), and
  only Djinn's environment then keeps the push from happening ([no worker pushes](#no-worker-pushes));
  in LISTED, a command neither listed nor granted by the user's settings is denied, and the denial ends the turn;
  in both, a command that asks to leave the sandbox (`unsandboxed`) is denied, and the denial ends the turn.

### Instructions: every agent reads `AGENTS.md`

| Agent       | What it reads, and how                                                                                                                                                         |
| ----------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Claude      | `CLAUDE.md`, or `AGENTS.md` when the project has no `CLAUDE.md` (claude 2.1.277 and later). Djinn passes `instructionFiles: claude-md-and-agents-md` to its AGENTS.md plugin in `--settings`, so a worker reads both, an import read once. Djinn's `CLAUDE.md` imports `AGENTS.md`. Verified: `code.claude.com/docs/en/memory`. |
| Codex       | `AGENTS.md` from the repository's root down to the working folder, by itself (its documentation).                                                                              |
| Antigravity | `GEMINI.md` and `AGENTS.md`, walking up from the working folder to the repository's root, by itself (the rules documentation in the agy 1.3.0 binary).                          |

### Skills summoned from another project

`djinn skill summon app/babysit-mr --into infra` records that project infra uses the skill babysit-mr of project app
(`Project.summons`, in `api/plan/v1/plan.proto`). **Nothing is copied, nothing is written in either project, nor in
the user's folders.** At each worker's launch in infra, Djinn finds the skill in app's `.agents/skills` or
`.claude/skills` as it is now, and shows it to the agent by the agent's own path. `djinn skill list` shows each
project's own skills and those it summons, a missing source included; `djinn skill unsummon app/babysit-mr --from
infra` stops it for the next workers.

For each task, Djinn makes a folder of its own, `<data folder>/skills/<task id>`, holding `.claude/skills/<skill>` and
`.agents/skills/<skill>`, each a symbolic link to the skill's folder in its source. It is made anew at each start and
removed when the task ends. A skill whose source is gone is left out: the worker starts, and the task says why in a
`STATUS` event. Where links cannot be made (Windows without the right to create symbolic links), codex still gets
the skills, Claude and agy do not, and an event says so.

| Agent       | How it gets them                                                                                                                                                   |
| ----------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Claude      | `--add-dir <folder>`; in `--settings`, allow `Read(//<skill folder>/**)`, deny `Edit` on the folder of links and on each skill's folder, whatever the mode. A read-only worker gets `--add-dir` only. |
| Codex       | `developerInstructions` on `thread/start`, `thread/resume` and `thread/fork`: each skill's name, source, description and the path of its `SKILL.md`, read where it is. |
| Antigravity | `--add-dir <folder>`: a workspace folder, whose `.agents` is a customization root.                                                                                   |

What is verified and what is supposed:

- **Claude**, from its documentation (`code.claude.com/docs/en/skills` and `/permissions`, read 2026-10-08) and
  `claude --help` (2.1.293): skills load from `.claude/skills` of a folder passed with `--add-dir`, which Claude
  watches, so a change in the source shows mid-session; a skill folder may be a symbolic link; an allow rule on a
  path through a link must match both the link and its target, a deny rule either one; `//` starts an absolute path,
  and Windows paths read `//c/...`. **Supposed:** that a read-only worker (`--restricted`) loads them, since the
  skills of an added folder depend on the project setting source, and that it reads a skill's other files, since
  `--restricted` confines the file tools to the working folders and the link leads out of them. **Lost:** a command
  that writes through the link is not denied by a rule; its target is outside the working folders, so it should be
  refused like any write there, which no real run has shown yet.
- **Codex**, from its skills documentation (`learn.chatgpt.com/docs/build-skills`, read 2026-10-08), its config code
  (`codex-rs/config/src/skills_config.rs`, main) and the app-server schema (`ThreadStartParams`, `ThreadResumeParams`,
  `ThreadForkParams`, main): codex finds skills only in `.agents/skills` from the working folder up to the
  repository's root, `$HOME/.agents/skills`, `/etc/codex/skills` and its own; `[[skills.config]]` only enables or
  disables a skill, it adds none; moving `$HOME` or `$CODEX_HOME` would move its sign-in and configuration. So codex
  is told where the skills are, the way it lists its own. **Supposed:** that `developerInstructions` replaces a
  `developer_instructions` of the user's `config.toml` for that thread, as other thread parameters override the
  configuration; that codex reads outside its workspace (its sandboxes restrict writing, not reading). A summoned
  skill is not one of codex's own: no `$skill` mention, the model reads the file by itself. The source stays out of
  the `workspace-write` sandbox's writable roots.
- **Antigravity**, from `agy --help` (1.3.0) and the documentation in its binary: `--add-dir` adds a folder to the
  workspace; a workspace root's `.agents` is a customization root, with skills in `skills/<name>/SKILL.md`; its
  changelog says skills are discovered on `/add-dir`. **Supposed:** that a folder given with `--add-dir` at launch is
  a customization root too, and that agy follows a linked skill folder. **Lost:** nothing keeps agy from editing the
  source through the link, as `--mode accept-edits` covers the workspace. Not used: `.agents/skills.json` (written in
  the project) and `~/.gemini/config/skills.json` (the user's).

**Following the source.** A summoned skill is the source's folder as it is: its uncommitted changes, and whatever
branch the source project has checked out. A skill can carry scripts: summoning one trusts its project from then on.

### Outside any project

|             | Outside any project (read-only)                                                                                                                 |
| ----------- | ----------------------------------------------------------------------------------------------------------------------------------------------- |
| Claude      | `--restricted --tools Read,Glob,Grep --permission-mode plan --strict-mcp-config`: reading tools only, no settings files, no MCP server, no hook |
| Codex       | `sandbox: read-only`, `approvalPolicy: never`, turn `sandboxPolicy: readOnly` without network; it reads with commands confined there (Q37)      |
| Antigravity | refused: plan mode is not read-only, and `--sandbox` lets HTTPS out and the agent leave it once permissions are skipped; allowed once proven    |

The same applies to a worker of rule 5 before the answer. In a project without `.agents/permissions.txtpb` nor
allowance, nothing above is passed: Claude gets `--permission-prompts none` (anything that would prompt is denied),
codex a thread without `sandbox` nor `approvalPolicy` (an approval it asks for is declined), agy no `--mode` (a tool
needing approval is soft-denied). Supposed, for Claude read-only: that `--restricted` with `--tools` leaves nothing
able to write or run code. For agy: that plan mode writes nothing in headless mode, and the sandbox blocks every
command, are to check on a real run.

## When Djinn resumes a worker

Djinn resumes a worker by itself, in the same task (status `resuming`), in four cases:

- **djinn up stopped or crashed while it ran.** At the next start the task is interrupted, then resumed, unless a
  person stopped it, it was imported, its worktree is gone, its wish is granted, or another task was forked from it
  (it reads "resumed as W47").
- **Its provider's usage limit stopped it.** The task waits for the reset (status "waiting for the limit", with
  "the account's session limit, resets at 07:20"), then resumes. Without a reset time, it waits 15 minutes, then 30,
  doubling up to 2 hours. While a limit holds, no new worker of that provider starts. The scheduler notes the
  account the agent was signed in with when the limit stopped it (`TaskHealth.account`: a hash of what its status
  command prints of it, `claude auth status`'s email and organization, `codex login status`'s line; never the account
  itself) and reads it again once a minute while the task waits: another account, and the task resumes at once.
  agy documents no status command: its limit waits for the reset.
- **Preemption for an investigator worker.** When memory is short and an investigator worker ("Qxx: enlighten")
  cannot start, Djinn makes room: it interrupts the running work task of lowest priority so that it resumes later in
  its session or conversation (Claude, Antigravity), RESUMING with the wait reason "made room for Qxx: enlighten",
  without consuming a resume attempt.
- **The Lamplighter wakes it.** The Lamplighter is Djinn's watchdog (`internal/harness/lamplighter.go`; the
  lamplighter woke the street's lamps one by one, and lit again the ones the wind blew out). It wakes before it
  fails: a worker silent past the silence limit with no process busy and no gate held or awaited, a worker whose
  stream went idle, or a task running without a process (a lost process) is ended, then continued on its session
  ("continue where you stopped…"), its run ending `wake` (`TaskRun.reason`). A paused worker is none of these: the
  Lamplighter skips it, and its stream's idle timer (`StreamIdleTimeout`, 5 minutes) and quiet timer stop while it is
  held still and start again from zero when it resumes (W226). Messages sent to a worker that ended before it read
  them (no text nor tool call after them) are told to the worker started again, after its prompt, whatever started
  it (a wake, a restart, a preemption, a continue), and are received once it says something. Only after 3 wakes in a row does the
  task fail, with "silent for 15 min; the Lamplighter woke it 3 times in a row and gives up", its lead told once; a
  run that ends otherwise (done, a limit, a restart) ends the row. Wakes are counted apart from usage-limit
  resumes: a worker that waited for its limits is woken all the same, and a wake spends no resume. A budget of
  the task's [seal](#a-tasks-seal) is no wake: a task stopped past its wall time or its turns stays stopped.

Beside these, the scheduler judges each work task's health on every pass (`internal/harness/health.go`,
`Task.health`): a worker silent for `StallSilence` (10 minutes; `StallAfterTurn`, 30, after its turn's result) whose
processes used under 5% of a core on average and never 25% in a reading, holding and waiting for no gate, is
`stalled`; a task failed or cut short for a reason a new turn may get past (its agent or stream broke, a stop of
Djinn, a usage limit since lifted) and that Djinn does not resume by itself is `retriable`. Both are flagged: the
lead hears it once per change of state, in one `health` event with the command that relaunches the task
(`djinn task continue`, which ends a stalled worker and continues it on its session as a wake), and the window
marks the worker. Without readings of its processes (Windows, the fake agent), a worker is never stalled. The
Lamplighter is unchanged: it ends a silent worker only when its processes used no CPU at all.

The scheduler starts a resumed task like a planned one: slots, pressure and gates still apply. After 3
resumes on usage limits without finishing (unspent by preemption, pauses, grants or wakes), the task fails, saying
so. What each agent gets:

| Agent  | Resumed with                                                                                                       |
| ------ | ------------------------------------------------------------------------------------------------------------------ |
| Claude | `--resume <session>`, and "Djinn restarted while you worked; your worktree is as you left it. Continue your task." (or "Djinn interrupted your worker to make room for an investigator…") |
| Codex  | `thread/resume` on its thread, and the same line                                                                   |
| agy    | `--conversation <session>`, and the resume line (restarted, limit, answer, continue, preempted), like claude; from the first prompt only when no conversation id is known |

The limits Djinn recognizes, in the failure a worker ends with (`internal/harness/limit.go`): Claude's
`rate_limit_event` with status `rejected` (its `rateLimitType` and `resetsAt`) and its messages "You've hit your
session limit · resets …" or "Claude AI usage limit reached|<time>"; Codex's `usageLimitExceeded`; agy's
`RESOURCE_EXHAUSTED` (supposed); a rate limit or HTTP 429 from any of them.

## The scheduler's order and preemption

The scheduler (`internal/dispatch`) starts planned and resuming tasks in a strict 5-tier order (Q117, A):

1. **Investigator workers** (`Enlighten me`), followed by answer workers (`Rub the lamp`). Question workers skip the worker slot count, but wait if the machine is under pressure or memory is short.
2. **Correction workers**, then **review workers** (finished work waits on them to enter the branch, and its dependents wait with it).
3. **Resumed work tasks** (`RESUMING`: limits, restarts, preemption).
4. **Work tasks that unblock others**: the critical path, sorted descending by the transitive count of planned and waiting work tasks that depend on them.
5. **Other work tasks**.

Within each level, tasks are ordered by the wish's rank, then the oldest creation time (`create_time`). Watchers and inbox sources stay outside the slot and model queue.

### Memory queue and wait reasons
When memory is short for a task at the front of the queue, subsequent work tasks wait behind it with explicit wait reasons:
- Behind an investigator: `"an investigator goes first: <reason>"`
- Behind an answer worker: `"an answer worker goes first: <reason>"`
- Behind a correction worker: `"a correction goes first: <reason>"`
- Behind a review worker: `"a review goes first: <reason>"`
- Behind a work task: `"<code|a work task> goes first: <reason>"`

An investigator is never held behind a waiting work task. If memory is short, it goes first in the queue, and work tasks wait behind it. Under real machine pressure, an investigator waits saying `"the machine is under pressure: <pressure>"`.

### Preemption for investigators
When an investigator cannot start, Djinn makes room:
- **No free slot**: Djinn pauses the running work task of lowest priority (the last wish by rank, then the most recently started), starts the investigator, and resumes the paused task as soon as the investigator ends. A correction or a review (a merge in conflict, red checks, work left not committed) does the same (W233): it goes before the paused workers that resume, takes the slot of the work task it pauses, and lets it resume when it ends; under pressure, or when no work task can be paused, it waits.
- **Memory short**: A pause frees the slot and CPU but not the memory held by the stopped process. When memory blocks an investigator, Djinn interrupts the running work task of lowest priority so that it resumes later in its session or conversation (`plan.CanResume`), entering status `RESUMING` with the reason `"made room for Qxx: enlighten"`, without spending a resume attempt.
- **Exclusions**: Djinn never preempts a task holding or waiting for a gate, never a correction or review worker, never an urgent task, never a provider that cannot resume, and never pauses on Windows where process tree suspension is refused. At most one task is preempted per investigator. Each preemption records an event on both tasks and an entry in the journal (`harness/preempt`).

### Memory a worker is engaged at

A worker is engaged at a percentile of its memory readings that falls as the notch rises (Minimal p95, Medium p80,
High p70, Max p60, Overclock p50), plus the notch's margin (512 MiB up to Medium, 384 MiB at High, 256 MiB at Max and
Overclock): a running worker past its warm-up at that percentile of its own series (or what it holds now, if more), a
new one at the median of the latest 10 finished workers' value at it. Another worker starts when what the workers are
engaged at, plus its forecast, stays within the notch's share of what Djinn could use with no worker running (the
memory available plus what the workers running hold, less the 2 GiB reserve), and the memory available, less what the
workers running may still take beyond what they hold, holds its forecast. The pressure check (PSI), the memory free,
the worker memory guardrail and the gates stay the safety net.

### Operating load runtime adjustments

When the operating load notch changes, Djinn immediately adjusts running workers without waiting for subsequent scheduler decisions:

- **Priority at runtime (Q120 A)**: Every running worker's process tree is updated at once in both directions. Normal priority is the one Djinn itself runs at, not the system default: a worker runs as Djinn was started (`nice djinn`, a launcher that raised it), as no unprivileged process can go below its own nice level. Low priority is `nice 10` / `BELOW_NORMAL_PRIORITY_CLASS`, below the developer's applications, or Djinn's own when Djinn already runs lower. On Linux/Unix, Djinn applies `setpriority` (`nice 10` for low priority, Djinn's own nice level for normal) to all processes in the worker's process tree, and updates `cpu.weight` (10 for low, 100 for normal) on its systemd scope / cgroup when available. Child processes spawned subsequently inherit this priority. A worker at low priority starts under `nice`, at its level before its first instruction: a `setpriority` once it started can be lost on macOS when the program execs another at once (`/bin/sh` execs bash, `env` execs node). Without the privilege to lower a nice level (`ulimit -e` is 0 by default), raising a worker back from `nice 10` fails: its `cpu.weight` alone raises it, in a scope. On Windows, `SetPriorityClass` dynamically switches process priority between `BELOW_NORMAL_PRIORITY_CLASS` (low) and Djinn's own class (normal), and a worker starts in its class.
- **Worker count at runtime (Q121 A & Q123 A, W577)**:
  - What the load bar says a notch runs is what the scheduler lets run there: the load service sends each notch's slots (`LoadNotchSlots`, `machine.Monitor.NotchSlots`), computed as the scheduler's capacity is (`Policy.Slots`), and the window never computes them apart. Every agent takes a slot, an investigator's included; a watcher takes none. A merge waiting (a task done, its work not yet merged) is no worker: the window shows it as merging, apart.
  - When a notch change reduces capacity below the number of running workers, Djinn pauses the extra workers in the order of Q121 (Q117 reversed: last wish by rank, most recently started, descending task code). Djinn never pauses an investigator, a correction, a review, or a worker started or resumed by hand: those finish their run, new tasks waiting meanwhile. A worker holding or waiting for a gate pauses once it lets the gate go. The count weighed is what the notch itself lets run (`Policy.BarSlots`, its share of the CPU rule), not the memory's share: pausing frees no memory, and a dip of the memory available never pauses a worker.
  - When the notch is raised and capacity increases, Djinn resumes paused workers first in Q123 order (Q117's strict order: highest wish rank, earliest start time, ascending task code) before starting any newly planned tasks; a work task paused to give its slot to an investigator resumes first the same way once the investigator ends.
  - `djinn task start` and `djinn task resume` run a worker beyond the slots, as the developer asked: it takes a slot all the same (the next tasks wait), pushes no other worker out, and the bar counts it ("1 started by hand", `forced_workers`).
  - The window says what lowering does (`lower_pauses`): the newest workers above the limit pause, or, in Auto and on Windows, they finish their run.
  - *Windows limitation*: Windows cannot suspend processes (process tree pause is refused with `errNoPause`), so capacity reduction through process pausing is only supported on POSIX systems (Linux, macOS).
- **Memory shortage for paused workers (Q122 A)**: A paused worker keeps its memory. If memory is short for a higher-priority worker that must start, Djinn interrupts the paused worker so that it can resume later in its session or conversation (`plan.CanResume`), entering `TASK_STATUS_RESUMING` with wait reason `"made room for <task>"` without spending a resume attempt. Djinn never interrupts a provider that cannot resume.
- **Auto mode (Q124 C)**: In Auto mode, Djinn lowers priority and stops starting new workers when the machine is under pressure, but never pauses running workers (`Policy.Auto == true`).
- **Events and journal**: Every pause, resume, or interruption triggered by the notch emits an event on the task (e.g. `"paused: operating load lowered to Minimal"`, `"resumed: a slot is free again"`) and a line in the journal (`actorHarness`, procedure `harness/notch`).


## Watch: a command, no agent

`djinn task spawn <wish> --title "Watch !41" --provider watch --prompt "mrwatch -watch 41" --restart` runs the command
in the project's folder: no agent, no model, no token. It runs without a shell: quotes group words as in a shell, and Djinn
expands `$DJINN_HOME`, `$DJINN_TASK_ID` and `$DJINN_WISH_ID` (or `${…}`) outside single quotes, so that a file of the
watcher's own state folder reads as a lead writes it in a terminal:

```
djinn task spawn <wish> --title "Watch !2855" --provider watch --restart \
  --prompt 'mrwatch -mr 2855 -watch -state "$DJINN_HOME/watchers/$DJINN_TASK_ID/mr-2855"'
```

Any other variable (`$HOME`) is refused at spawn: nothing would expand it. Write its value, or put it in single quotes
for the command to read it as it is. What the project's permissions and the developer's `--watch` allowances judge is
the command once expanded, the one that runs ([what a watcher may run](wish-templates.md#what-a-watcher-may-run)). Each paragraph it prints (output, then a second of silence,
or its exit; 200 lines at most) is a `TEXT` event, its raw line kept with its colours, and its first line wakes the
wish's lead, typed in the lead's terminal as an answer is:

```
Djinn: W70's watcher says: New thread on !41. mrwatch -watch 41 is still watching.
```

- **Supervised by the Lamplighter.** A watcher is no worker: the Lamplighter, Djinn's watchdog, supervises it
  apart, as long as its wish needs it. No silence limit, no wall-time budget and no turn budget apply to it; what counts is that its command lives
  while its task runs. A command that dies (an error, an exit code other than 0, a kill) starts again, in the same
  task: one second later, doubling up to five minutes, the backoff starting over after a run that lived a minute.
  Its state folder, `$DJINN_HOME/watchers/<task>` (Djinn sets `DJINN_HOME` for it, and makes the folder before
  the command starts), stays as the command left it,
  so that it does not say again what it said. After five deaths in a row, the task fails, and the lead is told once
  why, with the command's last error lines: "Djinn: W70's watcher failed: its command died 5 times in a row; the
  last time: exit code 4; its error output: gh: not logged in". A watcher whose process is lost (its task running
  in the store, no run in Djinn) starts again too. Stopped, it stays stopped.
- **Its loop.** `--restart` keeps one meaning: the command starts again after an exit 0 too, until the task is
  stopped or its done line comes: a command that exits on each change (`mrwatch -watch`) becomes a lasting watcher.
  It starts again at once after exit 0 (a second apart at least). Without `--restart`, an exit 0 ends the task,
  done, and the lead reads "… has ended" (or "Djinn: W70's watcher: <command> has ended.", when its last paragraph
  came before its exit); a command that dies still starts again.
- **What the lead is told.** The first line of each paragraph, without colours nor control characters, 200
  characters at most. A paragraph that says again what the last one said is recorded, and wakes no one; nor does
  the last one of a watcher stopped on request. A wish without a lead keeps the paragraphs in the task's events.
  The task keeps the last first line (`last_line`), which the window shows under the task.
- **No slot.** A watcher sleeps until its command prints: it takes no worker slot, does not count as running for
  the machine, starts under pressure, and holds no write scope. Its wish and its dependencies still hold it.
- **No shell.** The command line is split into words: quotes group them, a backslash keeps a quote, a backslash or
  a space; a pipe or a `&&` is only a word. Run `sh -c "…"` for a shell, where the project allows it.
- **What it may run.** A watcher needs a project. Where its permissions list commands (the project's
  `.agents/permissions.txtpb`, a wish's allowance over it, which lists none without that file), the command must be
  one of them, or the command Djinn builds from a skill of the project
  ([declared](wish-templates.md#what-a-watcher-may-run)), or one you allow in the project for yourself (`djinn
  project allow <project> --watch '<command>'`): a watcher has no reviewer to approve it. The project's denied
  commands win over all three. A folder outside Git without agent configuration runs no watcher.
- **Resumed.** A watcher `djinn up` cut short starts again at the next start, on its command, in its project's
  folder, however many times (the limit of 3 resumes is for agents). The lead's restart line counts the watchers
  apart from the workers: "Djinn: restarted on version v1.2.0: 1 watcher resumed: W9." It reads no message (`djinn task send`
  refuses); `djinn task pause` holds its command still, and the next run waits.

A skill's [wish template](wish-templates.md) starts a watcher with the wish it makes; its done line ends the
watcher, done, and asks whether to grant the wish.

Tests: `TestWatchParagraphs`, `TestWatcher` (no slot, the restart loop, the line to the lead, pause),
`TestWatcherEnds`, `TestWatcherResumes` (and the restart line), `TestWatcherPermissions` (`watch_test.go`, the test
binary plays the command), `TestWatcherLife`, `TestLamplighterWatcherOutlivesBudgets`,
`TestWatcherStartsAgainWhenItDies`, `TestWatcherGivesUp`, `TestLamplighterLostWatcherStartsAgain`
(`lamplighter_test.go`), and `TestWatcherTakesNoSlot` (`internal/dispatch`).

## Ifrit: an agent whose input stays open

`djinn task spawn <wish> --kind ifrit` (`TASK_KIND_IFRIT`, decision Q11) runs an agent that stays lit while the
developer is in Djinn ([the user's side](user-guide.md#an-ifrit-a-worker-that-stays-lit-while-you-are-there)). It
needs a project, and runs Claude or Antigravity: Djinn does not keep Codex's input open yet, and refuses it, as it
refuses `--provider watch`.

- **Its input stays open** (`Spec.Listen`, `ifrit.go`, `stream.go`). A worker closes its agent's input once every
  message has its result, and the agent ends; an ifrit's stays open. Once a turn has its result its worker says it
  listens (`Task.ifrit.state` `LISTENING`): the process is blocked on its input, no CPU, no model call. A message
  (`djinn task send`) starts the next turn (`WORKING`); one sent during a turn is absorbed by it, as for any worker.
- **Its seal.** It starts at once, beyond the slots, like `djinn task start`: no load notch pauses it, no investigator
  preempts it. It takes a slot only while it works on a turn. The Lamplighter leaves its silence alone while it
  listens, and its wall time counts its turns only (`Task.ifrit.listened` is taken off).
- **Its first prompt** carries the ifrit's rules in place of the wait in the foreground every other worker reads
  (`ifritRule`, `harness.go`): after each request, make the change, say in three lines at most what changed and how to
  see it, then end the turn; run every durable process through `djinn serve run`, never `&` nor `nohup`; never wait in
  a loop nor sleep; run only the tests a request names. Its checks sentence (`Settings.IfritChecksBrief`) names the
  project's checks but tells it to run none after a change: Djinn runs the commit checks once it is finished. An ifrit
  outside Git, with no branch, still reads its rules.
- **Its serves** (`serve.go`, `djinn serve run|list|logs|stop`) run in its worktree, each in a process group of its
  own, in its systemd scope where Djinn has one, logged under the data folder. Djinn reads the ports they listen on
  every 5 seconds and checks those declared with `--port`; a serve stops when the ifrit's run ends, its `--stop`
  command first. A serve's command runs when the ifrit's own permissions allow it (the wish's allowance, else the
  project's `.agents/permissions.txtpb`) or the developer's `serve_commands` list it, the project's denied commands
  winning; where neither declares permissions, the agent's own configuration decides, as for its worker.
- **Going out** (`presence.go`, `ifritTimers`). The developer's activity in Djinn (`Presence`: the window's
  `UiService.Touch` and actions, a terminal's keys, a `djinn` command they type) pushes back its out time; the lead's,
  the workers' and the watchers' calls do not. Past the idle delay (`--idle-minutes`, else `djinn up --ifrit-idle`,
  else 30 minutes), once it listens, Djinn closes its input: its serves stop, its gates and slot go back, the task is
  `STOPPED` with `Task.ifrit.state` `OUT` and why, and the lead hears a `LEAD_EVENT_KIND_OUT` line. A warning comes 5
  minutes before. A restart of Djinn brings a lit ifrit back out (`putOut`).
- **Lighting it again** (`relight`). `djinn task send` to an ifrit out goes through `djinn task continue`: its next
  worker resumes its session in its worktree, its serves started first, its turn the message, told how long it was
  out.
- **Finishing it** (`doneIfrit`). `djinn task done` on a lit ifrit closes its input once the turn under way has its
  result: its agent ends, and the task ends `DONE`, its work integrated like any task's.

Tests: `TestIfritListensBetweenMessages` (the `two-turns` Claude fixture), `TestIfritTakesNoSlotWhileListening`,
`TestLamplighterLeavesAListeningIfrit`, `TestSpawnIfrit`, `TestIfritIdle`, `TestIfritPromptRules` (`move_test.go`);
`TestIfritWarnsThenGoesOut`, `TestYourActivityKeepsItLit`, `TestLeadAndWorkersAreNoActivity`,
`TestIfritOutLetsTheTurnEnd`, `TestSendRelightsAnIfrit`, `TestIfritOutAfterARestart` (`presence_test.go`);
`TestServeRunsAndStops`, `TestServeStopCommandRunsWhenItGoesOut`, `TestServesComeBackOnRelight`,
`TestServeFollowsTheWatchersRule` (`serve_test.go`); `TestIfritKeepsItsGate`, `TestIfritGateGoesBackWhenItGoesOut`
(`internal/gate`); `TestIfritChecksBrief` (`internal/plan`).

## Claude

**Command.** `claude -p --input-format stream-json --output-format stream-json --verbose
--permission-prompts none`, then in a project `--settings <json>` (and `--permission-mode` with permissions; see
above), then `--session-id <task id>` (or `--resume <session>`; a fork adds `--fork-session` and keeps
`--session-id <task id>` for its new session), `--model`, `--max-budget-usd`. The prompt is a stream-json user
message on the input; the input stays open for more messages and is closed once each has its result, which ends
the process. A message sent while a turn runs is folded into that turn: claude writes one result for both, so
Djinn does not count one result per message but takes the result's `queued_turn_count` (the messages still held
for later turns) as the number still waiting; at 0 it closes the input. A result without that field (older
versions, and agy) counts one message. If messages still wait after such a result and the agent writes nothing
for `harness.Quiet` (10 minutes), Djinn takes them as answered, says so in a `STATUS`, and closes the input.

**Background work.** Once its input is closed, `claude -p` waits for the background work its worker started before
it exits, and the task stays `RUNNING` meanwhile. By default it stops a Monitor watch, a background subagent or a
workflow still running 10 minutes after the last turn; a background command the worker started holds the run open
until its own time limit (30 minutes, or the `timeout` the worker gives, 2 hours at most, with stream-json input).
In W31 of wish Will retour beta the 10 minutes stopped a paid run a monitor watched, and the task ended done. Djinn
starts every Claude worker with `CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS=0`, over the value of its own environment:
claude waits however long, and the task's life bounds the wait, since a stopped or ended task stops everything its
worker started (`stopLeft`; `TestClaudeWaitsForItsBackgroundWork`, `TestTaskEndStopsWhatItsWorkerStarted`). Every
worker's instructions also say never to end a turn while a long run is going, but to wait in the foreground.
**Verified** in Claude Code's docs (`env-vars.md`, `headless.md` "Background tasks at exit", `tools-reference.md`
"Time limit for background commands"), 16/10/2026: the variable needs claude v2.1.182 or later. **Supposed:** the
wait on a real run, not replayed.

**Finding claude.** An app started from the Finder or a desktop menu gets a bare PATH: the shell's startup files
that add `~/.local/bin` (where Claude Code's installer puts `claude`) are never read, not even by the login shell
that runs a lead (`zsh -l -c` reads `.zprofile`, not `.zshrc`). One resolver, `machine.ExtendPath`, appends to
Djinn's own PATH, after the inherited folders, which keep their place: the PATH of the user's interactive login
shell, the folders that exist among `~/.local/bin`, `/opt/homebrew/bin`, `/usr/local/bin`, `~/.npm-global/bin`,
`~/.volta/bin` and `~/.bun/bin` (on Windows `%LOCALAPPDATA%\agy\bin`, `%APPDATA%\npm` and `~/.local/bin`, which an
installer adds to the user's PATH after Djinn started), and the folder of a `codex` only the ChatGPT or Codex
application ships. The login shell is asked once, as `djinn up` starts from a launcher (`machine.AskLoginShell`; a
`djinn up` started from a terminal has its PATH already); the folders are looked at again before each worker and
each lead starts, so an agent installed in a folder created meanwhile is found without a restart
(`TestExtendPathAfterAnInstall`). Gates inherit it from the worker that runs them.

**The lead.** A lead runs `claude` interactive in the window's terminal, not `-p`. Claude Code skips its workspace
trust dialog only with `-p` (`claude --help`), so a lead started in a folder Claude Code has not trusted yet (the
lead starts in the first project's folder) first asks to trust it, there, in the window.
Djinn never answers it: trusting a folder is the user's decision. A first run of interactive Claude Code on a
machine also shows its onboarding (theme, sign-in) once.

**The driven lead (decision Q31, W131).** With `"leadDriven": true` in `settings.json`, Djinn runs no lead in a
terminal: `harness.LeadDriver` starts a worker-like `claude -p` for each batch of the lead's events, in Djinn's own
`lead` folder (no project's `CLAUDE.md` nor memory), with `--session-id` new each time, `--max-turns 60`,
`--max-budget-usd 5`, the rules of the driven lead in `--append-system-prompt` and
`--exclude-dynamic-system-prompt-sections` (the per-machine sections move to the first message, so the system prompt
is the same from one session to the next and its cache serves them all). Its permissions are the lead's: `djinn`
and the read-only Git commands, writing in its folder (plan files, tilasms), reading Djinn's data folder; it gets
`DJINN_LEAD=1` and an empty `DJINN_TASK_ID`, `DJINN_WISH_ID`, `DJINN_WORKER_TOKEN`: a `djinn up` started from a
worker's shell gave its session the worker's identity, and every write of the lead was refused (found on the first
real run). Not `--bare`: it would not use the subscription's sign-in. **Verified** in Claude Code's docs
(`headless.md`, `cli-reference.md`, 16/10/2026): `--max-turns` is print mode only and ends with `error_max_turns`;
`--bare` needs an API key. **Verified** on real runs (claude 2.1.301, Opus 5.5, 16/10/2026): `--max-turns`, hidden
from `claude --help`, is taken; a fresh session reads 18,626 tokens (system prompt, tools, rules) from the cache the
previous batch's session wrote, and writes its own message and turn to the cache for an hour. Measured per batch: a
cold start $0.21 (26,522 tokens written), a one-command batch $0.16, a question with two plan files $0.22, as
claude counts them; L10 estimated $0.12 to $0.21. The price table's estimate of the $0.16 batch is $0.11: it prices
a cache write at 5 minutes (5 $ a million tokens), `claude -p` writes them for an hour (8 $).

**The Djinn's tone (decision Q36, W156).** The page sends the theme it shows with `UiService.View` (`ShownTheme`,
`system` resolved by the page), Djinn keeps the last one in `settings.json` (`shownTheme`), and `plan.LeadPacket`
reads it as the batch starts: a `## Your tone` section with the one tone of that theme, from `lead/tones/light.md` or
`lead/tones/dark.md`, one line each. The limits of every tone are a rule of `plan.DrivenRules` (version 2), the same
text whatever the theme, so the system prompt's cache still serves every session. **Measured** on real calls (claude
-p, Opus 5.5, 17/10/2026, the difference in input tokens with and without the text): the rule 283 tokens, the light
section 127, the dark one 145. Per batch: the section written once to the hour cache (127 to 145 × 8 $ a million,
about $0.001) and read by the session's later calls; the rule read from the cache by each call (283 × 0.20 $ a
million, about $0.00006 a call). Under $0.002 a batch, about 1 % of the $0.16 batch. No theme said: no section.

**Stream.** One JSON message per line: `system/init` (session, model), `assistant` and `user` messages made of
blocks (`text`, `tool_use`, `tool_result`, `thinking`), and one `result` per turn with the cost.

**Usage.** Claude is the only provider whose stream gives costs directly: its turn `result` reports
`total_cost_usd` (or per-model cost in `modelUsage`). Djinn preserves this reported cost labelled "reported",
alongside an estimated cost computed from the price table. Its stream reports `input_tokens`, `output_tokens`,
`cache_read_input_tokens`, and `cache_creation_input_tokens`.

**Verified** on real runs (claude 2.1.293, Opus 5.5 and Haiku 5.5): the stream above; the usage is read from
`modelUsage` (summed over the models), the root `usage` only when `modelUsage` is absent; `--max-budget-usd` is
checked after a model call. Djinn drops on purpose: `rate_limit_event` while its status is `allowed` (another
status becomes a `STATUS`), `system/thinking_tokens` (an estimate the result counts), and an assistant message
holding only thinking whose text is withheld. A thinking block with text is kept as `OTHER`.
**Supposed:** the exit code after an error result (1), the text of a refused tool, the hand-written cases below. An
error subtype (`error_max_turns`, `error_during_execution`…) fails the turn even when `is_error` is false.

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
| Max turns reached, `is_error` false           | `claude/max-turns.jsonl`         | by hand                                     | error `error_max_turns` from the subtype; task failed, though claude exits 0                                        |
| Turns counted as they come                    | `claude/turns-mid-run.jsonl`     | by hand, from `tool-call.jsonl`             | a model turn per assistant message id, on its first event; a subagent's line (`parent_tool_use_id`) is none       |
| Second message                                | `claude/two-turns.jsonl`         | by hand                                     | two turns in one process                                                                                            |
| Message sent during the turn                  | `claude/mid-turn-message.jsonl`  | by hand, from W59's real result             | one result with `queued_turn_count` 0 answers both messages: the input is closed and the worker ends               |
| A batch of the driven lead                    | `claude/lead-batch.jsonl`        | **real**, claude 2.1.301, Opus 5.5          | the rules in the system prompt, turns capped, the lead's folder writable without a deny of every edit; the batch's cost, $0.16, kept in the lead's journal (`TestLeadBatchCostOnRecordedStream`) |
| Comments sent on a tilasm                     | `claude/lead-comments.jsonl`     | **real**, claude 2.1.302, Opus 5.5          | the driven lead reads the send in one command, acts in the same batch and replies to each comment once (`TestLeadRepliesToEachCommentOnRecordedStream`); $0.62 |
| Session limit reached                         | `claude/session-limit.jsonl`     | **real**, claude 2.1.293                    | `rate_limit_event` `rejected` gives the limit and its `resetsAt`; error "You've hit your session limit · resets 7:20am (Europe/Paris)"; the task waits for the reset, then resumes |

## Codex

**Command.** `codex app-server --listen stdio://`: JSON-RPC 2.0, one message per line on stdio. Djinn sends
`initialize` (client `djinn`), then `initialized`, then `thread/start` with the worktree as `cwd` (or
`thread/resume` / `thread/fork` with `excludeTurns`), then one `turn/start` per message, in order: a message sent
during a turn waits for the next one. Once the last turn has completed, Djinn closes the input; an app-server
still running after the grace delay is stopped, and that is no failure.

**Stream.** Answers to Djinn's requests; notifications `item/started` and `item/completed` (items
`agentMessage`, `commandExecution`, `fileChange`, `mcpToolCall`, `reasoning`…), `thread/tokenUsage/updated`,
`error` (with `willRetry`), `turn/completed` (status `completed`, `failed`, `interrupted`; any other fails the
turn, and so does an `error` not retried before a `completed`); and requests of the
app-server. Djinn answers `item/commandExecution/requestApproval` and `item/fileChange/requestApproval` from the
worker's permissions (`accept` what they cover, `permission granted: …`), and declines them otherwise
(`{"decision":"decline"}`, `permission denied: …`); it refuses any other request (JSON-RPC error -32601). Dropped on purpose: deltas
(their item comes whole once completed), `turn/started`, `thread/started`, and the user's message item.

**Usage.** Tokens only, no cost: `thread/tokenUsage/updated` gives the thread's total input, output, cached input,
and reasoning tokens (mapped to thinking tokens in Djinn). Djinn counts the cached input apart
(`input = inputTokens − cachedInputTokens`), which supposes that `inputTokens` includes the cached ones, as in the
OpenAI API. Djinn estimates its cost from the embedded price table.

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
| Usage limit                | `codex/limit.jsonl`             | by hand | `usageLimitExceeded: …`; the task waits for the limit (a backoff), then resumes        |
| Permission denied          | `codex/permission-denied.jsonl` | by hand | the approval declined, `permission denied: commandExecution …`, the command `declined` |
| Listed command approved    | `codex/permission-denied.jsonl` | by hand | with `.agents` listing `go test`: `untrusted`, `workspace-write`, the approval accepted |
| Thread resumed             | `codex/resume.jsonl`            | by hand | `thread/resume` with `excludeTurns`                                                    |
| Thread to resume unknown   | `codex/resume-missing.jsonl`    | by hand | the request's error; task failed                                                       |
| Unknown lines and requests | `codex/unknown-line.jsonl`      | by hand | kept as `OTHER`; an unknown request refused                                            |
| Process dies mid-turn      | `codex/process-dies.jsonl`      | by hand | error "codex ended before the end of its turn"                                         |
| Error, then turn completed | `codex/error-completed.jsonl`   | by hand | the error not retried; task failed, though the turn says `completed`                   |
| Unknown turn status        | `codex/unknown-status.jsonl`    | by hand | "codex turn cancelled": only `completed` is done; task failed                          |
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

**The lead.** A Codex lead runs `codex` interactive in the window's terminal, without permission flags: its own
configuration decides. Codex's `--approve-for-me` (its Auto) selects `workspace-write` and the automatic review of
approvals by itself, and Codex refuses it beside `--sandbox` or `--ask-for-approval`. `codex --help` returns before it
checks these conflicts: only a start of the installed CLI shows them, as a lead that ends at once. Djinn keeps how
the lead's program ended, with its output, until Lead starts it again (`TestExitedCodexLeadIsRetainedUntilExplicitResume`).

## Antigravity

**Command.** `agy --input-format stream-json --output-format stream-json`, then, with permissions, `--mode
accept-edits` and `--project <Djinn's agy project>` (run without `--sandbox`: developer choice Decision Q66, as agy's sandbox
blocks UNIX domain sockets needed for `djinn gate run lint`, which triggers an `unsandboxed` request that headless mode denies);
`--conversation <id>` to resume, `--model`; outside a project, refused until a real capture proves a read-only mode. No `-p`: it takes the prompt as its value, and stream-json
input replaces it. Each message on the input is `{"event":"user","message":{"content":"…"}}` and runs one turn;
the input is closed once every message has its result. agy cannot fork a conversation, and has no spending cap.
 
**Resume.** A worker's worktree is its own isolated workspace. When resumed (after a restart, a session limit, an answer, or `djinn task continue`), Djinn resumes an agy worker with `--conversation <id>` and its resume line, falling back to the first prompt only when no conversation id is known. An Antigravity lead starts interactively with `agy -i '<start line>'` in the wish's project directory; because interactive `agy` does not expose its conversation id on launch, Djinn records the lead by provider and folder (without a session id), and resumes it with `agy --continue` in the lead's folder, documented as "the folder's most recent conversation".

**Stream.** `{"event":"init","conversation_id":…}`, then `step_update` lines (`step_type` `user_input`,
`agent_response` with `text_delta`, `tool` with `tool_info` {name, parameters, output}, `checkpoint`), and one
`result` per turn (`status` `SUCCESS`, `ERROR`, `CANCELED`, `INTERRUPTED`, `INVALID`, `WAITING`; `usage` with
`input_tokens`, `output_tokens`, `thinking_tokens`, `cache_read_tokens`, cumulative over the conversation;
`denied_actions` when it refused a permission). On a
model or agent failure, agy writes `AGY_ERROR: {…}` on its error output and exits with code 3. Djinn gathers the
text deltas of a step and says them once the step is done (a response cut by the end of the process is said
with `[cut]`), counts thinking tokens as written ones, reads `AGY_ERROR` as an error and the denial notice
as `permission denied: …`. Tokens only, no cost.

**Usage.** Tokens only, no cost: `step_update.usage` reports `input_tokens`, `output_tokens`, `thinking_tokens`,
and `cache_read_tokens`. Djinn estimates cost from the embedded price table. A turn with `denied_actions`, or a run whose error output gave the
denial notice, fails: "agy stopped: it cannot run commands headless (<the command>). Run this task with claude or
codex." A tool step that ends in `ERROR` does not end the turn: refused by a deny grant (its `tool_info.error` says
"… Matches user-configured deny rule."), it is a `permission denied: <the command>` status, as Claude's; else its
error is the tool's result.

**Real case, 2026-10-08, agy 1.3.0 headless.** Three workers (W35, W36, W38) ran in a project with
`.agents/permissions.txtpb` (so `--mode accept-edits`). Each read a few files, then stopped at a shell command:
`git grep`, `go tool task --list`, `find`. agy denied the command ("jetski: no output produced — a tool required
the "command" permission that headless mode cannot prompt for, so it was auto-denied…" on its error output), and
ended its turn at once: the denied step without output, a result with status `SUCCESS`, an empty `response`,
`denied_actions: [{"action":"command","display_name":"RunCommand"}]`, exit code 0. Djinn marked the three tasks
done with no change in their worktrees. Such a run now fails. In W36, `git status` and `cat .git` ran before
`go tool task --list` was denied: which commands agy lets through is its own decision, listed in `.agents/` or not.

**Real case, 2026-10-10, agy 1.3.2.** W198 and W207 (gemini-3.8-flash-high) failed at their first command, `git
status`: "agy stopped: headless, it cannot ask for the "unsandboxed" permission (git status)". `git status` was
allowed by the user's agy settings; the sandbox was the cause.

**What agy's sandbox lets through (real runs, agy 1.3.3, 2026-10-10).** In a scratch repository under the home
folder, with a linked worktree beside it as Djinn makes them, agy started as Djinn starts it (`--mode accept-edits
--sandbox`, stream-json, never `--dangerously-skip-permissions`), each prompt one line ("Run git status, then git
log -1, then stop."), the mount table read from inside the sandbox (`grep … /proc/self/mountinfo`):

- The sandbox mounts the workspace **read-only** for commands (agy's own file tools still write), and every `.git`
  read-only (its changelog: "read-only rather than writable access to a Git repository's `.git` directory"). The
  repository of a linked worktree, outside the workspace, is not mounted at all: `git status` gives `fatal: not a
  git repository: …/.git/worktrees/w1`, then agy asks for `unsandboxed`, which a headless run denies.
- `--add-dir <repository>/.git` (or its `worktrees/<name>` and `objects`, or the repository's root) mounts it
  **read-only**: `git status` and `git log` work, `git add` fails on `index.lock: Read-only file system`. A
  workspace listed in `trustedWorkspaces` stays read-only. `GIT_DIR` and `GIT_COMMON_DIR` only tell git where to
  look: what the mount table shows absent or read-only stays so (reasoned, not run). In a plain repository (no worktree), `git add` fails the same way on its own `.git`.
- A `write_file(<folder>)` grant mounts the folder **writable** in the sandbox, `.git` included. A
  `command(<prefix>)` grant lets a command starting with the prefix run without approval; a deny grant wins over
  any allow, the user's settings' included, and its command fails (step `ERROR`) without ending the turn.
- Grants are read from the user's `settings.json` (`permissions.allow`), or from an agy project:
  `~/.gemini/config/projects/<id>.json` with `{"id", "name", "permissionGrants": {"permissionGrants": {"allow": [],
  "deny": []}}}` (field names from the project descriptor in the binary), chosen with `--project <id>`; agy's log
  says `ApplyProjectPermissionGrants: stored N allow, M deny grants`. A project's `settings` (`autoExecutionPolicy:
  …PROCEED_IN_SANDBOX`) are not applied by the command line, and `ANTIGRAVITY_PERM_GRANTS` does nothing to it.

**So Djinn writes an agy project per worktree** (`agyProject`, `internal/harness/agy.go`), for a worker with
permissions: id `djinn-<16 hex digits of the folder's SHA-256>`, the same for every worker of the folder, as agy
resumes a conversation in the project it began in; rewritten at each start, removed with the worktree. Its grants:
`write_file` of the worker's folder and of Git's folders for it (the `.git` folder, or a linked worktree's folder in
the repository and the repository's common `.git`, read from the `.git` and `commondir` files, without running
git), `command(<p>)` for each listed command in LISTED, `command(*)` and `read_url(*)` in AUTO (Decision Q143, A,
11/10/2026), and deny `command(<p>)` for each denied one, with `git push` always denied in AUTO, since pushing is the
orchestrator's (a first line: see [no worker pushes](#no-worker-pushes)).

**Reads and denials (W254, W269, 2026-10-10).** By default headless agy cannot prompt for `read_file` outside the
workspace, so reading Go's sources (`/usr/local/go/src/…`), caches, or external files failed. Djinn grants
`read_file(<dir>)` for what a worker needs to read: the repository (its main checkout and `.git`), Go's root
(`go env GOROOT`), Go module and build caches (`go env GOMODCACHE GOCACHE`), npm's cache (`npm config get cache`),
`/usr` and `/etc` read-only (on non-Windows), and the user's home folder (`~`). In agy, a deny grant wins over any
allow grant (prefix matching). Djinn adds deny `read_file(<dir>)` for secrets (`~/.ssh`, `~/.gnupg`, `~/.config/gh`,
`~/.config/gcloud`, `~/.aws`, `~/.netrc`, `~/.git-credentials`) and Djinn's own data folder (`~/.config/djinn` or
`$DJINN_HOME`), recursively denying sibling projects, sibling worktrees, and database files while keeping only the
task's own worktree accessible.

**Model retry prompts (W258, W281, 2026-10-10).** agy sometimes ends its turn on its own internal retry prompts to
its model (improperly formatted function call, exceeding the output token limit, or transient model retry errors)
instead of an agent answer or fatal error. When agy's result is one of these retry prompts, Djinn continues the
conversation itself in the same session with a short prompt ("continue where you stopped, keep answers and edits short"),
up to 3 times per task, recording each continuation as a `STATUS` event of the task; if it repeats 3 times in a row,
the task fails with the error. Djinn writes nothing else of agy's,
reads nothing of it, and leaves the user's settings alone. The run recorded in `testdata/antigravity/commit.jsonl`
wrote a file, then ran `git status`, `git add`, `git commit`, `git diff` and `git log` in the worktree, all
sandboxed, nothing denied, no network granted.

**AUTO: every command but the denied ones (real runs, agy 1.3.3, 2026-10-10, W226).** Before, AUTO granted only Git's
reading and commit commands, and replayed workers stopped on `git grep`, `which …; echo …` and `npm ci`. Same
scratch setup as above, the project granting `write_file` on the worktree and the repository's `.git`, `command(*)`,
and denying `command(git push)`, the user's settings untouched (`wc`, `echo`, `printenv`, `npm` are not in them):

- Without `command(*)`, `wc -l a.txt` is denied headless and ends the turn; with it, it runs. `command(*)` is
  accepted (agy's log: `stored 3 allow, 1 deny grants`); agy's own guidance for skills calls it overly generic, but
  it is a valid grant.
- Pipes, `;`, `&&` and env prefixes run: `echo hello | wc -c`, `echo one; echo two`, `true && echo ok`, `FOO=bar
  printenv FOO`, `env FOO=baz printenv FOO`.
- The deny grant wins: `git push origin w1`, `echo x && git push origin w1` and `GIT_TRACE=0 git push origin w1` end
  in `ERROR`, "Permission denied for command(…). Matches user-configured deny rule.", and the turn goes on to the
  next command (recorded in `testdata/antigravity/auto.jsonl`). It is a prefix: `sh -c "git push origin w1"` and
  `git -C . push origin w1` ran, and failed only for want of a remote; Djinn's environment now refuses them all
  ([no worker pushes](#no-worker-pushes)).
- The network stays off under `--sandbox`: `curl https://example.com` and `git ls-remote https://github.com/…` gave
  "Could not resolve host". `command(*)` does not let a command out of the sandbox: asked to run `touch` outside the
  worktree with `BypassSandbox: true`, agy asked for `unsandboxed`, which headless denied (ending the turn), and no
  file was made. `npm ci` ran in the sandbox from npm's cache (its package was there); a package missing from the
  cache would need the network, so a project whose workers install must allow it.

**read_url in AUTO (Decision Q143, A, 11/10/2026).** In AUTO, an agy worker already reaches the internet through
`curl` under `command(*)`, consistent with Q66. Without an explicit grant, agy 1.3.3 refused the `read_url` tool
headless with "Do not attempt to circumvent this denial ... curl" (W371), after which the worker fetched the same
page by curl and Python, which its rights allowed. Djinn grants `read_url(*)` in AUTO next to `command(*)`,
keeping deny `command(git push)`.

**So today an agy worker runs without `--sandbox` (Decision Q66, agy 1.3.3); in AUTO it runs any command via `command(*)` but the denied ones, `git push` always, and may read any URL (`read_url(*)`, Q143);** in
LISTED, a command neither listed nor allowed by the user's own agy settings is still denied, and ends its turn:
list what its tasks run, `git add` and `git commit` among them.

**Verified:** the command line, from `agy --help` (1.3.0); the stream's shapes, from the official headless
documentation (`antigravity.google/docs/cli/headless`, read 2026-10-08); the field names `event`, `step_update`,
`text_delta`, `tool_info`, `subagent_info`, `short_error`, `retryable`, `http_status`, and the denial notice,
found in the agy 1.3.0 binary; exit code 3 and `AGY_ERROR`, from `agy changelog`. On real runs (2026-10-08): the
`init`, tool steps seen `ACTIVE` then `DONE`, the `result` of a turn ended by a denial, and the denial notice.
**Supposed:** the fixtures written by hand, the other fields of `AGY_ERROR`, and whether `cache_read_tokens` is
part of `input_tokens`.

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
| Quota exhausted         | `antigravity/limit.jsonl` + `.stderr`             | by hand, **supposed** | `… (HTTP 429), retryable`; the task waits (a backoff), then resumes |
| Command denied          | `antigravity/permission-denied.jsonl` + `.stderr` | **real**, W36         | two commands run, the third denied: "agy stopped: … (go tool task --list) …"; task failed |
| First command denied    | `antigravity/permission-denied-first.jsonl` + `.stderr` | **real**, W35   | the denied step never ends: "agy stopped: … (git grep …) …"; task failed |
| Denial on stderr only   | `antigravity/permission-denied-notice.jsonl` + `.stderr` | by hand, from W35 | no `denied_actions`: the notice alone fails the task, even read after the result |
| Conversation resumed    | `antigravity/resume.jsonl`                        | by hand, **supposed** | `--conversation`, the resumed conversation                         |
| Unknown lines           | `antigravity/unknown-line.jsonl`                  | by hand, **supposed** | each kept as `OTHER`, raw                                          |
| Process dies mid-answer | `antigravity/process-dies.jsonl`                  | by hand, **supposed** | the cut text, error "agy ended before the end of its turn"         |
| Second message          | `antigravity/two-turns.jsonl`                     | by hand, **supposed** | two turns in one process                                           |
| Commit in a worktree    | `antigravity/commit.jsonl`                        | **real**, W208        | LISTED: a file written, Git run in the sandbox, nothing denied     |
| AUTO, a command denied  | `antigravity/auto.jsonl`                          | **real**, W226        | compound commands run; `git push` is a `permission denied: …` status, the turn goes on, the task done |
| Malformed call retry    | `antigravity/malformed-call.jsonl`                | by hand, from W258    | retry prompt continued; done on second turn                         |
| Token limit retry       | `antigravity/token-limit.jsonl`                   | by hand, from W281    | partial output `[cut]`, token limit prompt continued; done on second turn |
| Three retries failed    | `antigravity/retry-failed.jsonl`                  | by hand               | three retry prompts continued in a row; fourth fails the task       |

## The lead's seat and the accounts

The lead Djinn drives runs on a **seat**: an agent, a model and an account, chosen by the developer and kept in
`settings.json` (`ui.v1.Settings.lead`) until changed (W132, decision Q31). Each batch reads it as it starts, so a move
takes effect at the next batch, which carries the lead's journal like any other; the batch records its agent, model and
account. The moves: `djinn lead move --provider codex --model <model> --account <name>` (the whole seat: an empty model
is the agent's default, an empty account its own sign-in; without `--provider`, the current agent), or the lead's menu
in the window (the arrow beside **Talk to the Djinn**), which lists each agent on its own sign-in then on each account,
and the model.

**Which agents may lead.** Claude: its rules in `--append-system-prompt` (W131). Codex: its rules as the thread's
`developerInstructions`, and the lead's folder, without `edit`, as its `workspace-write` sandbox (the other folders it
may write in as `writableRoots`, a file change inside them accepted); Djinn answers its command approvals by the lead's
commands, as for a worker. **Built, not tried on a real run:** a Codex batch; `--max-turns` has no Codex equivalent,
the 30-minute timeout guards it. Antigravity cannot lead: agy takes no instructions beside the prompt and refuses a
session that may not edit every file of its folder; `djinn lead move --provider antigravity` is refused.

**A usage limit is the developer's move.** A batch that hits a limit (Claude's `rate_limit_event` `rejected`, or a
limit in its error, as for a worker) is not tried again on the same seat: Djinn moves the lead nowhere by itself. It
shows the limit beside **Talk to the Djinn** and first in the attention bar, with a button per seat it may move to: the
other accounts of the same agent first (its model kept), then the other agents on their own sign-in and on each account,
only those installed whose status command does not say signed out. A click is `LeadService.Move`: the waiting events
go to the new seat at once. Without a move, the events, newer ones too, wait for the reset the agent gave (15 minutes
when it gave none), then go to the same seat; **Talk to the Djinn** tries the same seat at once.

### Several subscriptions at once

An **account** is a folder of the agent's own where it keeps its configuration, sessions and login, beside its
default one: `djinn lead add-account work --provider claude [--label Work] [--config-directory <folder>]` names it,
makes the folder (`~/.claude-work` or `~/.codex-work` by default; never Djinn's data folder, the home folder, nor the
agent's default folder), and prints the command that signs it in there once. `djinn lead accounts` lists them;
`djinn lead remove-account work` forgets one and leaves its folder and its login as they are (refused while the lead
runs on it). The window's agents setup shows each account under its agent, where it stands, and runs its sign-in in a
terminal of the panel. Djinn passes the folder's variable to the sessions it starts on the account and to the status
check; it never reads nor stores a token.

| Agent       | Variable            | What it moves, by its documentation (read 17/10/2026)                                                                                                                                                                                                                                     | Sign in, status                                                       |
| ----------- | ------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------- |
| Claude Code | `CLAUDE_CONFIG_DIR` | "Each directory has its own settings, session history, and claude.ai login or API key" (`code.claude.com/docs/en/authentication`, Log in with multiple accounts); on Linux its `.credentials.json`, on macOS a Keychain entry keyed to the folder                                          | `claude auth login`; `claude auth status` exits 0 signed in, 1 not    |
| Codex       | `CODEX_HOME`        | "Codex stores its local state under CODEX_HOME" with `config.toml` and `auth.json` (`developers.openai.com/codex/config-advanced`); its keyring entry is keyed to the folder's path (`codex-rs/login/src/auth/storage.rs`, source only)                                                     | `codex login`; `codex login status` exits 0 when credentials are present |
| Antigravity | none documented     | its settings in `~/.gemini/antigravity-cli`, its token in the system's keyring (a file on Linux without a session bus); no variable moves them (`antigravity.google/docs`, `agy --help` 1.3.5)                                                                                              | no account: changing `HOME` would move everything, untried             |

**Caveats, from the same documentation.** For Claude, `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN` and
`CLAUDE_CODE_OAUTH_TOKEN` win over a folder's login: a session or a check on an account clears them. A Claude Console
sign-in without an API key is kept outside the folder: two Console sign-ins are not kept apart. Codex's IDE extension
and desktop app share the default folder's login. **Verified** in tests on fake agents only: the variable reaches each
session and each status check; no real sign-in was made.

## Tokens and cost estimates across providers

Every task, wish, and the flying carpet show token counts (input, output, cache read, thinking) and an estimated cost computed from an embedded price table (`internal/usage/prices.yaml`), marked "estimated, prices of <table date>".

| Provider | Stream tokens | Stream cost | Estimated cost |
| -------- | ------------- | ----------- | -------------- |
| **Claude** | Input, output, cache read, cache write | Yes (`total_cost_usd` on turn `result`) | Computed from price table; stream cost displayed beside it labelled "reported" |
| **Codex** | Input, output, cached input, reasoning (thinking) | No (tokens only) | Computed from price table |
| **Antigravity** | Input, output, thinking, cache read | No (tokens only) | Computed from price table |

- **Conversion at a date**: Cost estimates are calculated using the price table's current date (e.g. `2026-10-11`), never varying by the time of the run, providing a stable conversion across tasks (similar to currency conversion at a fixed date).
- **Reported costs**: When a stream reports an actual monetary cost (Claude Code), Djinn keeps and displays this value beside the estimate, labelled "reported".
- **Comparable in tokens**: Usage views offer a switch between currency (USD) and tokens. In token view, comparisons across different providers remain informative even when a model has no price entry in the table.
- **Models missing from table**: A model whose price cannot be sourced from its provider's public pricing page stays out of the table; its tasks show tokens and "no price in the table". Totals per wish and provider report how many tasks are without a price.
- **Local overrides**: The embedded table loader leaves a clear seam for local overrides from `~/.config/djinn/prices.yaml` (or `$DJINN_HOME/prices.yaml`) without requiring configuration UI or CLI flags today.


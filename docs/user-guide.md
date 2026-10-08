# Djinn user guide

A local desktop app to frame a wish, follow Codex or Claude Code agents, make decisions, annotate
artifacts and hand the whole context over in a single file.

The interface follows the system language (English or French); you can change it in
**Connections & preferences**.

## Launch

```sh
npm install
npm run desktop
```

Built version: `npm run build`, then `npm start`.
macOS app: `npm run package` (in `release/`).
Separate local version: `npm run package:local` (in `release/v<version>/`). This path keeps the
bundle of an app that is already open: quit it at the end of the current run, then open the new
`Djinn.app`.

## First steps

The **Projects space** wish is an interactive demonstration: no model and no project code is run.
Answer the decisions, start the demo, then try the review and sharing.

To work on a real project:

1. Pick a saved project in the sidebar, or create one (see
   [Setting up a project](#setting-up-a-project)). Describe the result you want in the large
   prompt; extra guidance is optional and folded away. The folder, conventions, references and
   preferences come from the project. The harness proposes a title; a rename by a person always
   wins.
2. The agent defines the whole timeline from your intent, in a separate read-only run. The
   preparation screen keeps your request, any questions, pause and resume. A request for a
   specification can stay a specification, with no code and no prototype. Set 1 to 16 sub-agents
   working at once; writers with distinct declared scopes work in parallel.
3. Creating the wish or sending a message starts the current run. Answer the blocking questions:
   work resumes on its own as soon as every needed decision is filled in.
4. Read the result card and validate it yourself. In a flexible workflow, Djinn proposes the
   smallest useful next step: add it to the timeline, adapt it, or stop here. Adding it starts no
   process. Future steps are disabled; visiting a past step starts nothing.
5. Discuss the Markdown artifacts and the isolated interactive visualizations. A thinking step can
   ask for `grill-me`; the harness must check that the skill is available before announcing it.
6. After code, validate a review before delivery. Export the v2 session, the summary and the merge
   request draft. Delivery needs your explicit validation.

A project's conventions are saved for new wishes. Each wish keeps a dated copy: later changes to
the project never silently replace its context.

The **project sources of truth** name reference documents by relative path and state their role.
In the artifacts, **Set as source of truth** also marks an artifact as canonical. These artifacts
come first in the context budget, with their revision and latest human edits, and are passed on to
the next steps. An agent refresh keeps this designation; a competing proposal never replaces a
human edit.

Codex uses its durable local app-server: the lead keeps a read-only conversation while the workers
run. Talking to the lead does not restart the writers. A targeted message joins an active Codex
turn directly; Claude uses a controlled interrupt and resume of the recipient, once it has actually
closed. The configured model and safeguards are kept. The tracking view shows the agents actually
running, their task, the lead waiting or answering, and the last timestamped activity. Silence is a
duration; a stderr warning does not become a fatal error.

The timeline keeps the real runs, with access to each agent's chat. Older items with no provable
step are shown in **History v1**. Documents render as GitHub-flavored Markdown; a human edit keeps
its revision, and a competing agent refresh becomes a separate proposal.

The actions and the artifacts produced are gathered in **Results**, in the wish and in the tab of
the same name. Each native turn has its own accordion, newest first. A new turn opens and closes
the older ones, even before it produces a result.

Sub-agents declare their relative files or folders with `writeScope`, their prerequisites with
`dependsOn`, and read-only work with `readOnly`. A writer with no declared scope reserves the whole
project. Paths are compared after resolving symbolic links. The scheduler uses each freed slot at
once: independent work never waits for a whole wave. The lead integrates once the writers have
actually finished. Codex receives the prepared write roots in its sandbox; Claude keeps its
permissions and gets the scope contract in its prompt.

After the interface reloads, Djinn reattaches to the runs actually present in its native runtime
and shows their active agents and last activity again. It replays a bounded, deduplicated journal;
reconnecting starts no provider. A saved identifier or an outside Codex conversation is not an
active run.

At the end of a successful implementation, Djinn looks for the project's development script and
starts its local server when exactly one candidate fits. With several apps, the proposed actions
let you choose which one to start. The action cards give access to the preview, stopping and
restarting it, and to the checks the agent proposes. Djinn checks that the server answers before
calling it ready, and manages the processes it started. A restored session never declares an old
server running without a native check.

The floating bar is folded by default. Guidance gets a receipt: transmitted, delivered to its
recipient, or prevented with the reason; it stays in the context of later resumes. New questions
and actions open a persistent alert in Djinn; the bell finds them again. A notification is sent for
each successful step; clicking it opens the result. Projects and wishes list the most recent
first. Connections shows the dispatch result and failures of native notifications; the **Test**
button checks the system settings. The browser preview can turn notifications on with **Enable**.

Known Codex startup diagnostics do not fail a run. The conversation hides older warnings and stdin
messages recorded as errors, without erasing the journal or hiding real provider errors.

Pause stops the processes started. A resume includes the decisions, feedback, edited artifacts and
latest reports. Code changes stay subject to the provider's permissions; Djinn never turns its
safeguards off.

## Setting up a project

Two fields stay visible: name and folder. A button starts an analysis with the configured provider,
Claude or Codex. The report shows the knowledge, scripts, skills, references and prototyping apps
it found. Details and advanced settings are folded away. Applying the suggestions does not save the
project: you can check them before saving.

The local scan is bounded in files, bytes, time and number of excerpts. It skips symbolic links,
dependencies, build output and secret files. The agent only receives the collected excerpts and a
local Djinn history limited to the project folder; native Claude or Codex conversations and other
projects' histories are never read. The observations shown come from the local scan: the agent's
interpretation cannot rewrite that evidence. No detected script is run by the scan.

If the agent does not answer or returns an invalid proposal, the local scan stays visible as such,
with the reason for the fallback. Changing the folder or the provider, or closing the settings,
discards the result in progress.

## Shortcuts

| Action          | macOS            | Windows / Linux     |
| --------------- | ---------------- | ------------------- |
| Give guidance   | ⌘ J              | Ctrl J              |
| Review feedback | ⌘ ⇧ J            | Ctrl ⇧ J            |
| Send            | ⌘ Enter or Enter | Ctrl Enter or Enter |
| New line        | ⇧ Enter          | ⇧ Enter             |
| Fold / close    | Esc              | Esc                 |
| Switch view     | ⌘ 1–5            | Ctrl 1–5            |
| Commands        | ⌘ K              | Ctrl K              |
| New wish        | ⌘ N              | Ctrl N              |

## Connections

- Codex: `npm install -g @openai/codex`, then `codex login`.
- Claude Code: install its CLI and run `claude auth login`.

Djinn asks for no API key and never copies authentication tokens into sessions. Whether a model is
available depends on your provider and your subscription.

## Data and sharing

The workspace is saved atomically in the app's user data folder. Version 1 sessions migrate to
version 2, with a recoverable native backup `state.json.v1.backup`. Imports never reuse native
conversations from another machine. A corrupted state is kept, and automatic saving is suspended.
The `.djinn.json` file holds the projects, the steps and their context, the decisions, agents,
events, artifacts and annotations. Importing runs no command. Check the project path on the machine
that resumes the work.

Tool output shown in the history is bounded so that it does not swamp the save; the wish journal
keeps the full output, and large old saves are compacted, with a backup copy, before they are
replaced.

The file can include code and screenshots of your project: choose who receives it. There is no
cloud infrastructure in this version.

## Checking

```sh
go tool task test
```

The tests use a temporary data folder and a fake provider that plays a script, so the flows are
checked without calling a paid model.

## Current scope

Working: the desktop app, CLI connections, local execution, typed events, the time-based timeline,
decisions with history, end actions, managed local servers, editable artifacts, annotations, pause
and resume, import and export, and local deliverables.

Visual review happens in Djinn, through the artifacts or imported screenshots. A browser extension,
cloud collaboration, automatic merge request publishing and automatic project video recording are
not part of this version. The merge request draft can be exported, then published with your usual
tools.

The engine is built around CLI adapters and an isolated bridge, studied from
[T3 Code](https://github.com/pingdotgg/t3code) (MIT). The model picker and the Claude catalog are
adapted from T3 Code; the notices give their origin. Visual answers take up the idea of HTML pages
in the conversation, with an original demo and Djinn's own isolated engine. The machine and signal
illustrations are original SVGs. The animated avatars use
[Thinking Orbs](https://libraries.dev/orbs), under the MIT license, with an animation and a color
of their own for each agent.

Agent protocol: [agent-protocol.md](agent-protocol.md).

The sources and `dist/` do not update a Djinn app that is already installed. Running the built
version from this repository uses its new sources; producing an installer is a separate step.

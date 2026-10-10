---
id: 01a118ac-4d4c-7def-9720-c8fc9b71f059
code: T20
phase: 3
status: in-progress
after: T02 T07 T13 T14 T18
---

# T20 · Your wishes follow you

**Goal.** Your wishes follow you: backed up on your server and shared online.

Djinn keeps everything on the machine, and closing it loses nothing. Losing the
machine loses everything, unless you chose to back it up. Anyone who wants their backups on a
central server finds the procedure written down, and later a command that does it.

## Decided
- **Closing Djinn loses nothing.** Every write is a SQLite transaction with its journal entry,
  in WAL mode with `synchronous(FULL)`: a committed write survives a crash of Djinn and a power
  cut. Workers running at shutdown are stopped and their tasks marked interrupted, ready to
  resume (T07).
- **Decentralized by default.** A backup server is an option someone sets up, never a
  requirement and never a service Djinn runs for you.
- **No secret in Djinn** (rule in `AGENTS.md`): the credentials of the backup server stay with
  the tool that uses them (an SSH agent, the backup tool's own keyring or environment).
- **No sudo** (rule in `AGENTS.md`): scheduling uses the user's own scheduler (a systemd user
  timer on Linux, a launchd user agent on macOS, a user task on Windows), or Djinn itself while
  it runs.

## What we want
1. **A documented procedure first** (`docs/backup.md`): what to back up (the data folder: the
   database, the project folders, the flight plans; never the worktrees, which Git holds), how
   to take a consistent copy while Djinn runs, how to send it to a server, how to restore.
2. **Then a command**: `djinn backup` writes a consistent snapshot of the database
   (`VACUUM INTO`, safe while Djinn runs) and the data folder into one archive;
   `djinn backup restore <archive>` puts it back. Sending it elsewhere is left to a tool that
   does it well, called by the user's scheduler.
3. **Encrypted before it leaves the machine**: a wish holds plans, decisions and journals that
   may be private, even without secrets.

## Done when
- [x] `docs/backup.md` explains backup and restore to a remote server, step by step, without
  sudo.
- [x] `djinn backup` and `djinn backup restore` round-trip a database while Djinn runs.
- [x] A restore on a new machine finds its projects or asks for them, as an import does (T13).

## Decided along the way
- **A raw copy, not an export.** A backup is the data folder, not a wish: an export (T13) carries one wish
  between machines, a backup puts a whole machine back. One archive, `.tar.gz` or `.zip` (the default on
  Windows), opened by a `Manifest` (`djinn-backup.json`, format 1). A restore refuses an archive without it.
- **Snapshots, not replication.** `djinn backup` takes one; the user's scheduler takes it again. Litestream
  would be a service to run, against "decentralized by default".
- **The djinn that holds the database copies it.** `BackupService.Create` (internal: the command line is
  written by hand, like `djinn up`) runs `VACUUM INTO` on its own connection. Without a running djinn, or with
  one older than backups, the command copies the file itself: safe in WAL mode.
- **What stays out.** The worktrees (`projects/*/worktrees`, Git holds them), `djinn.db-wal` and `-shm`, the
  socket, `server.addr` (it may hold a token), the logs at the top of the folder, and any environment file
  (`.env*`, `*.env`), anywhere. A restore skips them too.
- **A restore never overwrites.** It refuses while a djinn answers on the folder, and says how to stop it. It
  extracts next to the folder, checks the database (`integrity_check`), then swaps in two renames. The old
  folder stays aside (`<folder>.before-restore-<date>`); its worktrees move into the restored folder, at the
  same paths, so Git still finds them.
- **Missing projects are detached.** A project whose folder this machine lacks loses it, journaled as
  `backup/restore`; the restore lists it, and `djinn project add <folder>` attaches it, as after an import.
- **Sending is the user's tool.** `docs/backup.md` shows restic (encrypted, deduplicated) and rsync over SSH
  after `age` to the user's SSH key. Djinn holds none of their credentials.

## Open questions
- Should Djinn take a backup itself on a schedule while it runs, for those who set no scheduler?
- How does a backup relate to work spread over trusted machines (T15): a backup is a copy, not
  a sync.

## From T24 · A wish online: sync now, collaborate later

**Goal.** A wish has a page anyone can open, kept up to date as the wish moves: one person works, the other reads.

### Decided
- **The word is `sync`.** `djinn wish sync <wish>`. One way today (Djinn to the page), both ways
  once people collaborate.
- **Djinn renders, the model does not.** Djinn writes the page from its store, in Go, on every
  change of the wish: no token spent. Empty sections are not rendered (the lamp and the smoke).
- **The lead only publishes.** When Djinn's lead is Claude, it republishes the rendered file as
  an artifact at the same address, in one tool call, without reading the page. With another
  agent, the page is a file to share.
- **Nothing leaves without a go.** The first publish of a wish asks; then every sync goes to
  the same page. No secret, no local path on the page.

### Later: collaborate on a wish
moved to T29, a draft

### Decided along the way
- **One file, in the data folder.** `djinn wish sync` writes `<data>/wishes/<id>/page.html` and prints its path. A
  page is synced while its file exists: `djinn up` takes back the pages it finds when it starts, and deleting the
  file stops it. No new state in the store. `djinn wish render` writes the page once, to Downloads or `--file`.
- **A commit hook, not a poll.** The store calls back after each commit with the entities it changed; the pages
  mark the synced wishes they touch, and render them at most once a second.
- **Order of the page:** what matters now first. A bar pinned to the top while something waits for the user (each
  blocking question a line, the other questions a line each while they are two at most, else one line naming them;
  the tasks cut short in one line; a project to attach; a ready wish), the most urgent first. Header with the tasks
  counted by status and contents (pills naming only the sections present, coloured as their most urgent item). To
  decide (open questions, a card each: the recommendation boxed and first, then the options, then the context; a
  question a waiting task needs is red, open and first; then those needed before something, orange under their
  `before` words; then those that can wait, grey; folded, their recommendation in sight; a lone question open), waiting for you, who runs now (the running workers as cards, with their last event; the
  finished work folded below as a table), tasks (waiting, failed and cut short in clear, with their dependencies;
  planned folded as a table with why each waits), decisions (the latest first, a table: when, the question and the
  choice in bold, why; past 15, folded), notes (the blocks in their order, each under its title; a long block
  folded; a run of more than three of one kind gathered in one card, a folded line each, eight in sight), journal
  (commands and `log` blocks, the latest first, a compact table; past 10, folded), worker events (what they said,
  their status changes and errors, the latest first; past 10, folded). Empty sections are not rendered.
- **One colour language, never colour alone.** Each state has a colour, an icon and a word: done green ✓, running
  blue with a live dot, waiting for you orange ?, planned grey ○, failed red ✕, interrupted amber ↺, paused indigo ‖,
  stopped grey ■; and for what waits: blocking red !, waiting for you orange ?, can wait neutral ◷. Used by the
  tasks, the workers, the bar and the pills. A test checks every colour at 4.5:1 at least on its soft colour and on a
  card, in light and dark.
- **A Mermaid diagram shows as its source**, with a line that says so: the page runs no script.
- **"Waiting for you" comes from the lamp only:** a task waiting on its edit question, a task cut short by a stop,
  a project not on this machine. No block kind is read as an action.
- **Same scrubbing as the export,** plus the data folder (`djinn-data`). Tool calls and results stay out.
- **Markdown by goldmark (MIT), raw HTML left out**, dangerous links emptied. A block of another media type shows
  as text. No script, no external font: the page opens offline.
- **The page's texts are translated** (`page.*` keys), in the language of the user's locale.

### Done when
- [ ] `djinn wish sync` renders the page in Go (done), and the lead republishes it in one call.
  - [x] The lead's instructions say so. (d3881ed: the stable brief, `internal/plan/brief.go`: republish the
    printed file as it is, in one call, without reading or rewriting it, no HTML by hand; `TestBrief` checks it)
  - [ ] Seen publishing. (needs: a real lead session)
- [x] A change in the wish updates the page without the model writing HTML.
- [x] The page reads at a glance: a bar of what waits, questions first with their recommendation boxed, one colour
  language with icons and words, who runs now, compact tables for the rest, light and dark, 375 px without
  horizontal scroll. (w37: `TestBar`, `TestColourLanguage`, `TestContrast`, `TestEvents`, `TestDiagram`,
  `TestBusyPage`; screenshots of an imported wish rendered by a test djinn, light, dark and 375 px)
- [ ] A hand-off: the other person opens the link and sees the wish as it is. (needs: two people)

### Open questions
- Can Djinn publish without a Claude session (an API for artifacts)? Not known today.
- With workers running, `djinn wish sync` rewrites its file every second, and a publish of it fails: "the source file
  changed between approval and publish" (08/10/2026). The lead then published a snapshot, `djinn wish render <wish>
  --file <snapshot>`, while the brief's rule still says to republish the sync file as it is. Make the rule the
  snapshot, or have sync hold the file still while a publish reads it?
## From T15 · Work spread over trusted machines

moved to T28, a draft

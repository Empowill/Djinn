---
id: 01a118ac-4d4c-7def-9720-c8fc9b71f059
code: T20
phase: 3
status: done
---

# T20 · Backups, on a server of your choice

**Goal.** Djinn keeps everything on the machine, and closing it loses nothing. Losing the
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

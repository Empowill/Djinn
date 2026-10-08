---
id: 01a118ac-4d4c-7def-9720-c8fc9b71f059
code: T20
phase: 3
status: open
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
- [ ] `docs/backup.md` explains backup and restore to a remote server, step by step, without
  sudo.
- [ ] `djinn backup` and `djinn backup restore` round-trip a database while Djinn runs.
- [ ] A restore on a new machine finds its projects or asks for them, as an import does (T13).

## Open questions
- Which tool to recommend for sending: `rsync` over SSH, `restic` (encrypted, deduplicated),
  `rclone`, or continuous replication of SQLite (Litestream)?
- Snapshots on a schedule, or continuous replication?
- Is a backup a kind of export (T13), with the same format, or a raw copy of the folder?
- How does a backup relate to work spread over trusted machines (T15): a backup is a copy, not
  a sync.

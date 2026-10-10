# Backups

Djinn keeps everything on your machine. Closing it loses nothing; losing the machine loses everything.
A backup is an option you set up, on a server you choose. Djinn imposes nothing and runs no service for you.

## What to back up

The data folder, nothing else:

| System  | Data folder                           |
| ------- | ------------------------------------- |
| Linux   | `~/.config/djinn`                     |
| macOS   | `~/Library/Application Support/djinn` |
| Windows | `%AppData%\djinn`                     |

`DJINN_HOME` moves it. It holds:

- `djinn.db`: the database. Wishes, tasks, questions, decisions, the journal.
- `wishes/`: the pages of the wishes.
- `projects/` and `tasks/`: the folders of the tasks.
- `settings.json`: the window's settings (the global shortcut).

Leave out the worktrees (`projects/*/worktrees`): Git holds them. Push the branches you care about.
Leave out `djinn.sock`, `server.addr` and the logs: they belong to a running Djinn.

Djinn stores no secret, so a backup holds none. It still holds your plans and decisions: encrypt it
before it leaves the machine.

## Take a copy

```sh
djinn backup                                # A new archive in your Downloads folder.
djinn backup --file ~/djinn-backup.tar.gz   # Or .zip.
```

It works while Djinn runs. The running Djinn copies its own database (`VACUUM INTO`): the copy is
consistent, even mid-write. Without a running Djinn, the command copies it itself. The archive leaves
out the worktrees, the socket, the logs and any environment file (`.env`, `.envrc`, `*.env`).

Never copy `djinn.db` alone while Djinn runs: recent writes sit in `djinn.db-wal`.

## Send it to a server

Pick a tool that does it well. Its credentials stay with it: your SSH agent, its own password file.
Two examples.

**restic** encrypts, deduplicates, and keeps a history. Once:

```sh
restic -r sftp:me@backup.example.org:djinn init
```

Then, each time:

```sh
djinn backup --file ~/.cache/djinn-backup.tar.gz
restic -r sftp:me@backup.example.org:djinn backup ~/.cache/djinn-backup.tar.gz
restic -r sftp:me@backup.example.org:djinn forget --keep-daily 7 --keep-weekly 8 --prune
```

restic reads its password from `RESTIC_PASSWORD_FILE` or `RESTIC_PASSWORD_COMMAND`, never from Djinn.

**rsync over SSH**, encrypted first with [age](https://age-encryption.org) to your own SSH key:

```sh
djinn backup --file ~/.cache/djinn-backup.tar.gz
age -R ~/.ssh/id_ed25519.pub -o ~/.cache/djinn-backup.tar.gz.age ~/.cache/djinn-backup.tar.gz
rsync -a ~/.cache/djinn-backup.tar.gz.age me@backup.example.org:djinn/
```

## Restore

1. Stop Djinn: close its window, or Ctrl+C where `djinn up` runs. A restore refuses a running Djinn.
2. Fetch the archive:
   - restic: `restic -r sftp:me@backup.example.org:djinn restore latest --target ~/restore`
   - rsync: `rsync me@backup.example.org:djinn/djinn-backup.tar.gz.age .`, then
     `age -d -i ~/.ssh/id_ed25519 -o djinn-backup.tar.gz djinn-backup.tar.gz.age`
3. Put it back:

   ```sh
   djinn backup restore djinn-backup.tar.gz
   ```

   The previous data folder moves aside, next to it (`djinn.before-restore-<date>`). Its worktrees move
   into the restored folder, at the same paths: Git still finds them. Delete the old folder once all is
   well.

4. Start Djinn. On a new machine, a project whose folder is missing is listed; attach it with
   `djinn project add <folder>`, as after an import.

## Schedule it, without sudo

Put the steps of [Send it to a server](#send-it-to-a-server) in a script, for example
`~/.local/bin/djinn-backup`, made executable. Give the full path of `djinn` (`command -v djinn`):
a scheduler does not read your shell's `PATH`.

**Linux**, a systemd user timer. `~/.config/systemd/user/djinn-backup.service`:

```ini
[Service]
Type=oneshot
ExecStart=%h/.local/bin/djinn-backup
```

`~/.config/systemd/user/djinn-backup.timer`:

```ini
[Timer]
OnCalendar=daily
Persistent=true

[Install]
WantedBy=timers.target
```

```sh
systemctl --user enable --now djinn-backup.timer
```

**macOS**, a launchd user agent. `~/Library/LaunchAgents/org.djinn.backup.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>org.djinn.backup</string>
  <key>ProgramArguments</key><array><string>/Users/me/.local/bin/djinn-backup</string></array>
  <key>StartCalendarInterval</key><dict><key>Hour</key><integer>12</integer><key>Minute</key><integer>30</integer></dict>
</dict>
</plist>
```

```sh
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/org.djinn.backup.plist
```

**Windows**, a task of your own. Put the steps in `%USERPROFILE%\djinn-backup.ps1`, with an archive
ending in `.zip`, then:

```bat
schtasks /Create /SC DAILY /ST 12:30 /TN "Djinn backup" /TR "powershell -NoProfile -File %USERPROFILE%\djinn-backup.ps1"
```

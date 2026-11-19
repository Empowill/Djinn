---
name: install-from-branch
description: How the window offers to install a wish's integration branch from source; the install setting comes from djinn.yaml on the reference branch, else the developer's own settings.txtpb; the install is the developer's move (Q104, Q107)
type: reference
---

The window offers "install from source" for a branch Djinn pushed only when the project's `install` setting is set.
Djinn reads it, setting by setting, first in the developer's own file (`projects/<project id>/settings.txtpb` in
Djinn's data folder), then in the team's `.agents/djinn.yaml` read on the reference branch (`origin/main`), never on
the branch checked out or installed. A reference branch without `djinn.yaml` (or with only the old
`.agents/settings.txtpb`, no longer read since W101) leaves `install` empty: nothing is offered.

To have it offered without waiting for `djinn.yaml` on main, the developer's own file gets these two lines, its other
lines kept:

```
install: "go tool task install"
install_questions: INSTALL_QUESTIONS_NEVER
```

The command is the project's own (`go tool task install` on djinn). Check it with `djinn project show <project>`: the
`install` entry shows `source: developer` and the command, `install_questions` shows `never`. A worker may append the
lines when it can write Djinn's data folder; otherwise ask the developer to paste them (a `--move` question).

The install itself is the developer's move, never run by the lead nor a worker: once the branch is pushed, ask a
`djinn question ask --move` naming the branch (`djinn/gates` say) and saying to click install in the window.

**Why:** Q104: the window stopped offering djinn/gates builds because origin/main had no `.agents/djinn.yaml`, so
install, checks and setup came from default (tilasm L36). Q107: the developer asked that every lead know how to get a
Djinn installed from source on a branch.

**How to apply:** when a wish's branch should be installed and the window offers nothing, run `djinn project show` and
read the `install` entry's source before anything else; if it is `default`, spawn a task (or ask a move) for the two
lines above, then ask the install as a `--move` question.

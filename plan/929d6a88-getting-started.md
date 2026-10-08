---
id: 01a1184f-cf18-7776-a9d7-c82e929d6a88
code: T04
phase: 1
status: open
---

# T04 · Getting started

**Goal.** Anyone installs and runs Djinn in a few minutes, without wanting to contribute.
The top of the main README is for them; contributors come after.

## Decided
- **Release tags carry the built interface** (`dist/`), so `go install …@latest` works while
  `main` stays free of built files. `go tool task release VERSION=vX.Y.Z` prepares that tag. No
  per-OS release pipeline. Updates from inside the app build on this: see T12.
- One line: `go install github.com/empowill/djinn/cmd/djinn@latest`, then `djinn up`; the native
  window is in the default build (`-tags gtk3` on Ubuntu 22.04).
- Or ask your agent: a short prompt to paste into Claude Code or any coding agent, which
  installs the prerequisites, installs Djinn and opens it.
- `djinn project add .` registers a folder in seconds, with no model call: it indexes what
  agents should read first (instructions, skills, task files).
- Configuration lives in files: one main file and one folder per project, as text protobuf,
  typed and validated. Team conventions (branch names, ticket references) are project
  settings, never built in.
- **No sudo for a user, to install, run or update.** A contributor may need system packages to
  build the window; a user never does.
- **`go install` works everywhere**, and is an intermediate target until releases ship binaries
  (T19). With CGO and the window's system libraries, the install is better: the native window.
  Without them, Djinn still builds (no system library at all) and `djinn up` opens the same
  interface in the browser, over local HTTP. The install instructions say so in one line.
## Done when
- [ ] A fresh account with no administrator rights installs and runs Djinn on Linux, macOS and
  Windows.
- [ ] From a machine without Djinn, the one-line install and the agent prompt both work.
- [ ] The time from `go install` to an open window on a real project is measured.
- [ ] The README top is kept in sync by every change to the install.

## Open questions
- What does the prompt for your agent say, word for word? *To write once `go install` works.*

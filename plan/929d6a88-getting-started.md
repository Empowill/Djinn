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

## Decided along the way
- **The one-line install** is `scripts/install.sh` (macOS, Linux) and `scripts/install.ps1` (Windows), attached to
  each release, so the line in the README always takes the script of the latest release. No sudo: `~/.local/bin`, or
  `%LOCALAPPDATA%\Programs\djinn` added to the user `PATH`. It downloads the archive for the system, checks it
  against `SHA256SUMS` (a mismatch stops everything), checks that the binary starts, then renames it over the old
  one. On Linux it picks the GTK 3 or GTK 4 build from the WebKitGTK it finds. With no binary that fits, no release,
  or no WebKitGTK, it falls back on `go install …` without CGO, at the same version. `DJINN_VERSION`,
  `DJINN_INSTALL_DIR`, `DJINN_ASSET`, `DJINN_RELEASES` change the defaults.
- **Tested against fake releases, never GitHub.** `tools/releasepack` tests run `install.sh` against releases it packs
  itself, served on a local port: latest, a given version, a tampered archive, a binary that does not start, no
  release, no Go. `install.ps1` was run by hand in PowerShell 7 on Linux (download, sum, refusal of a tampered
  archive, fallback); its install on a real Windows is still to run.
- **There is now a per-OS release pipeline** (T19), the "no per-OS pipeline" above is superseded: binaries first,
  `go install` as the fallback.
- **The README line says "coming"** until the first release is published.

## Done when
- [ ] A fresh account with no administrator rights installs and runs Djinn on Linux, macOS and
  Windows. (needs: a published release, then a person on each system)
- [ ] From a machine without Djinn, the one-line install and the agent prompt both work. (needs: a published
  release, and the agent prompt, not written yet)
- [ ] The time from `go install` to an open window on a real project is measured. (needs: a release tag, and a
  person to time it)
- [ ] The README top is kept in sync by every change to the install. (needs: a test or a review rule that ties the
  README lines to `scripts/install.*`; an agent can write the test)

## Open questions
- What does the prompt for your agent say, word for word? *To write once `go install` works.*
- *Answered:* a `go install` build now reads its module version (`debug.ReadBuildInfo`), so it is a release, not
  `dev`: it keeps its data in `djinn` and can be offered updates.

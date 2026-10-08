---
id: 01a11865-73d2-7c81-8e78-bfa31aa20487
code: T12
phase: 1
status: in-progress
---

# T12 · Updates from inside the app

**Goal.** Djinn notices a new release, offers it in the app, installs it and restarts. No
release pipeline per operating system: the update reuses `go install`.

## How it works
- **Releases are Git tags that carry the built interface.** A release commit adds `dist/` on top
  of `main` and is tagged `vX.Y.Z`; `main` itself never holds built files. `go install
  github.com/empowill/djinn/cmd/djinn@latest` then builds a complete binary.
- **Check.** At start-up, then every few hours, Djinn asks the Go module proxy for
  `github.com/empowill/djinn/@latest` (it honours `GOPROXY`) and compares the answer with its own
  version, read from the build information (`runtime/debug.ReadBuildInfo`). A build from a
  source checkout reports `(devel)` and never updates itself.
- **Offer.** The window shows "Djinn vX.Y.Z is available", with its release notes and an
  "Install and restart" button. Nothing installs without that click.
- **Install.** Djinn runs `go install` for that exact version, with the build tags it was built
  with (also in the build information), into the folder of the running binary. The Go checksum
  database verifies the downloaded source: integrity comes for free.
- **Restart.** The state is already on disk. Djinn waits until no worker is running (or asks to
  pause them), starts the new binary on the same project, and exits.
  - Windows cannot overwrite a running executable: Djinn renames itself first (allowed on a
    running file), installs, then starts the new one.
- **The command line**: `djinn update` does the same, for people who live in the terminal.

## Two paths, by how Djinn was installed
- **Installed with `go install`** (the intermediate target): the update above, by `go install` of
  the new tag, then a restart.
- **Installed from a release binary** (T19, the real target): the binary lives in the user's own
  folders, so it replaces itself without sudo. Wails v3 ships an updater (`app.Updater`,
  `pkg/updater`, read in beta.28) that does most of it: a GitHub releases provider; a check on a
  timer; a download verified by its digest, and by a signature against a public key pinned in the
  binary at build time (the release feed cannot substitute its own); an atomic swap by a helper
  process once Djinn has quit, with a rollback from a backup if the new version does not start;
  on Windows, the running executable is renamed aside instead of overwritten; on macOS, a
  dedicated path for the app bundle. Only a build with the native window has it: a build without
  CGO keeps the `go install` path. To check before relying on it: a real update on each OS,
  and that a signed, notarized macOS bundle stays valid after the swap.

## Done when
- [x] The Djinn in use updates from a checkout on one click, without losing the session: `go tool task install`
      leaves the running Djinn alone, which offers the new one; "Update" (or `djinn update`) restarts on it and
      reopens the lead terminals. Linux (tested, and run by hand on real binaries); Windows vetted, not run.
- [ ] A test release `v0.0.1-test` installs with one `go install` line on Linux. (needs: a maintainer to push the
  tag; the repository has no tag yet)
- [ ] A newer tag shows the offer in the window; the click installs and restarts on Linux,
      macOS and Windows. (needs: the release check, not built (only the local path is watched); then a Mac and
      a Windows machine)
- [x] A `(devel)` build never offers an update. (`moduleVersion` keeps a checkout build `dev`, `TestModuleVersion`;
      `newUpdater` returns no updater for `dev`, `cmd/djinn/update.go`)

## Decided along the way
- **The local install comes first.** The Djinn we use is built from a checkout (`go tool task install`), so the
  update starts there; a release only changes where the new binary comes from.
- **Install beside, then rename.** `task install` builds `.djinn-new` in the destination folder, then
  `tools/swapexe` renames it over `djinn`: one atomic step, and the running Djinn keeps its old file. On Linux a plain
  `go build -o` over a running binary also works (no `ETXTBSY`: Go writes a new file), but not in one step; on
  macOS rewriting a running signed binary in place gets it killed. On Windows `swapexe` moves the running executable
  aside (`.swapexe-old-*`, removed by a later install) and puts the new one in its place. `TO=<folder>` installs
  elsewhere, for tests.
- **Detect by the path.** `djinn up` (not a `dev` build) looks at `os.Executable()` every 3 s: when the file there
  is no longer the one it started from, it runs `<path> version` and offers it if the version differs. No Go
  toolchain needed at run time. The version is `local-<git describe --dirty>`: two installs of the same dirty tree
  share it, and are not offered.
- **`UiService.WatchUpdate` and `UiService.Update`** (internal): the window's banner ("A new version of Djinn is
  ready — Update") and `djinn update`, a top-level command like `djinn up`, outside the generated ones. An agent never
  needs it. `Update` fails while nothing newer waits.
- **The restart** writes `restart.json` in the data directory (each running terminal: name, command, folder; and
  what the window showed), stops as Djinn quits (terminals hung up, workers interrupted and recovered), then starts the
  new binary detached with the same `up` flags. The new one runs each terminal again and shows the same lead. A
  terminal that does not start is listed in the banner and by `djinn update` (exit 1); its wish keeps the lead, for
  `djinn wish resume`. If the new binary does not start, `restart.json` stays for the next `djinn up`.

## Open questions
- Installing with `go install` needs Go, a C compiler and the webview headers on the user's
  machine. Fine for developers, the first audience; prebuilt binaries can come later without
  changing the update check.

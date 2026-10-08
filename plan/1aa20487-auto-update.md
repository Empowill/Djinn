---
id: 01a11865-73d2-7c81-8e78-bfa31aa20487
code: T12
phase: 1
status: open
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
- [ ] A test release `v0.0.1-test` installs with one `go install` line on Linux.
- [ ] A newer tag shows the offer in the window; the click installs and restarts on Linux,
      macOS and Windows.
- [ ] A `(devel)` build never offers an update.

## Open questions
- Installing with `go install` needs Go, a C compiler and the webview headers on the user's
  machine. Fine for developers, the first audience; prebuilt binaries can come later without
  changing the update check.

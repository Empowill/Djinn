---
id: 01a1184f-cf18-7776-a9d7-c82e929d6a88
code: T04
phase: 1
status: in-progress
after: T19
---

# T04 · Install and stay up to date

**Goal.** Djinn installs in a few minutes, and updates from inside the app.

Anyone installs and runs Djinn in a few minutes, without wanting to contribute.
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
- [x] The README top is kept in sync by every change to the install. (b09fb6b: `TestReadmeInstallLines` in
  `tools/releasepack`: each README line takes a script of `scripts/`, from the releases it defaults to, says what
  its usage says, and `release.yml` ships it; a renamed script fails it)
- [x] Once installed, the user guide takes over: `docs/user-guide.md` describes the Go app as it is, from `djinn up`
  to granting a wish, and stays so. (10/10, W139, porting W90 onto the azimas, tilasms, terminal tabs, the lead's
  agent, pause, resume and delete of a wish: `TestUserGuide` in `cmd/djinn/guide_test.go`: every `djinn` command the
  guide names exists under its full name with the flags it gives, `djinn up`'s parsed by `djinn up` itself, and every
  label it names in bold is a text of `locales/en.json`; a renamed command, flag or button fails it)

## Open questions
- What does the prompt for your agent say, word for word? *To write once `go install` works.*
- *Answered:* a `go install` build now reads its module version (`debug.ReadBuildInfo`), so it is a release, not
  `dev`: it keeps its data in `djinn` and can be offered updates.

## From T12 · Updates from inside the app

**Goal.** Djinn notices a new release, offers it in the app, installs it and restarts. No
release pipeline per operating system: the update reuses `go install`.

### How it works
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

### Two paths, by how Djinn was installed
- **Installed with `go install`** (the intermediate target): the update above, by `go install` of
  the new tag, then a restart.
- **Installed from a release binary** (T19, the real target): the binary lives in the user's own
  folders, so it replaces itself without sudo. It downloads the archive of its own variant, checks
  it, and swaps it in. Wails v3 ships an updater (`pkg/updater`, read in beta.28); Djinn uses its
  GitHub provider and its verified download, not its window nor its helper (see "Decided along the
  way"). To check before relying on it: a real update on each OS, and that a signed, notarized
  macOS binary stays valid after the swap.

### Done when
- [x] The Djinn in use updates from a checkout on one click, without losing the session: `go tool task install`
      leaves the running Djinn alone, which offers the new one; "Update" (or `djinn update`) restarts on it and
      reopens the lead terminals. Linux (tested, and run by hand on real binaries); Windows vetted, not run.
- [ ] A test release `v0.0.1-test` installs with one `go install` line on Linux. (needs: a maintainer to push the
  tag; the repository has no tag yet)
- [x] A Djinn installed from a release offers a newer one without downloading it, and on the click downloads it,
      checks its SHA-256, swaps it in and restarts; a wrong sum is refused and changes nothing. (against a fake release
      served by the test, Linux: `TestUpdateFromRelease` runs the binary, `TestGitHubSource` the source; the `go
      install` path: `TestProxySource`, with a fake proxy and a fake `go`)
- [x] The offer of a release links its release notes; the system's browser opens them. (`TestUpdateFromRelease`: the
      `notes_url` of `UiService.WatchUpdate`, empty before and after; `tests/screens.test.mjs`: the banner shows the
      link, never one that is not http(s), none for a local install)
- [ ] A newer tag shows the offer in the window; the click installs and restarts on Linux,
      macOS and Windows. (needs: a published release, then a person on each system)
- [x] A `(devel)` build never offers an update. (`moduleVersion` keeps a checkout build `dev`, `TestModuleVersion`;
      `newUpdater` returns no updater for `dev`, `cmd/djinn/update.go`)

### Decided along the way
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
  `djinn wish resume`. If the new binary does not start, `restart.json` stays for the next `djinn up`. While Djinn
  runs, the same file notes the leads, for a crash (T21).

- **Two sources, by how Djinn was installed** (`cmd/djinn/release.go`). A release binary knows the archive it came
  from (`-X main.releaseAsset=djinn_linux_amd64_gtk4`, stamped by `task release-build`): it follows the GitHub
  releases and takes the same variant. A binary `go install` built carries a module version: it asks the first proxy
  of `go env GOPROXY` for `@latest` (none when `off` or `direct`), and needs `go` on the PATH. A checkout build
  (`dev`, `local-…`) asks nothing. A pre-release follows the pre-releases.
- **Check, never download.** At start-up, then every 6 hours, Djinn reads one version. A newer one shows in the same
  banner as a local install ("A new version of Djinn is ready — Update"); a newer binary at the path wins over it.
- **The click downloads.** `Update` (or `djinn update`, which now waits up to 5 minutes) fetches the release beside the
  running binary, runs `<new> version` (it must print the tag), renames it over the running one (`internal/swapexe`,
  shared with `tools/swapexe`), then restarts as above, leads reopened. Any failure leaves the binary as it was and
  shows in the banner. The `go install` path builds with the same `-tags` and `CGO_ENABLED`, into a temporary `GOBIN`
  in the same folder.
- **The Wails updater, in part.** Taken, for the release path: its GitHub provider (the archive by its exact name), the
  download hashed while it streams and checked against `SHA256SUMS`, the safe unpack (no path out of the folder, size
  caps), and the signature check against a key pinned at build, ready for when releases are signed. `pkg/updater` is
  plain Go, so a build without CGO has it too. Left: its window (Djinn has its banner) and its helper, which restarts
  the binary without its arguments and lives in `application.New` (absent from a build without the window): it
  would lose the leads. Djinn closes one gap: a release whose `SHA256SUMS` has no line for the archive is not offered
  at all; the provider alone would install it unchecked.
- **Release notes, a link.** The GitHub source reads the release's page (`html_url`); `WatchUpdate` carries it
  (`notes_url`) and the banner links it, "Release notes", opened by the system's browser. A link, not an excerpt: the
  notes stay where they are written, and the banner stays one line. A local install and the proxy have none.
- **No test reaches the network.** The tests turn the release check off unless they serve a fake release
  (`DJINN_TEST_RELEASE_API`, read only by the test binary).

### Open questions
- Signatures: the GitHub provider fetches none. Signing `SHA256SUMS` (minisign or ed25519, the key pinned with
  `-X`) needs a small source of our own that reads `SHA256SUMS.sig`; the updater checks it. Who holds the key?
- An opt-out for machines that must not reach GitHub (an environment variable, or a setting)?
- Windows: `os.Executable` after the running binary was moved aside is not checked; the restart may start the old
  file. To try on Windows with the first release.
- Installing with `go install` needs Go, a C compiler and the webview headers on the user's
  machine. Fine for developers, the first audience; prebuilt binaries can come later without
  changing the update check.

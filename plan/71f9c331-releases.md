---
id: 01a118aa-6f2a-7cba-a7ed-f16071f9c331
code: T19
phase: 3
status: in-progress
after: T11
---

# T19 · Releases: binaries for every target

**Goal.** Every release ships ready-made binaries for each target, built by a CI (GitHub
Actions or an equivalent), so that nobody needs a compiler, CGO or a system package to get the
native window. `go install` keeps working everywhere, without CGO, as the fallback.

## Decided
- **Djinn installs without sudo** (rule in `AGENTS.md`). A binary lands in the user's own
  folders (`~/.local/bin`, `%LOCALAPPDATA%`), never in a system one.
- **CGO only on the CI.** The C compiler and the development packages stay on the build
  machine. A binary still loads the web engine of the system when it starts:
  - **macOS**: WebKit is part of the system, nothing to install.
  - **Windows**: WebView2 ships with Windows 11 and comes to Windows 10 through its updates;
    no CGO is needed to build.
  - **Linux**: needs the runtime libraries of GTK and WebKitGTK (`libwebkit2gtk-4.1`, or
    `webkitgtk-6.0` for the GTK4 build), often present on a desktop, not always. They are too
    big to go inside the binary.
- **Tags carry the built interface** (`dist/`), as decided in T04: `go install` at a tag needs
  no Node.
- **Updates follow the same path as the install** (T12): a user who installed a binary gets a
  binary.

## Targets
| OS | Arch | Build | Window |
| -- | ---- | ----- | ------ |
| Linux | amd64, arm64 | CGO, `-tags gtk3` (WebKitGTK 4.1) | yes, with the runtime libraries |
| Linux | amd64, arm64 | CGO, GTK4 (WebKitGTK 6.0) | yes, on recent systems; the only one left after Wails 3.1 |
| macOS | universal (arm64 + amd64) | CGO, signed and notarized; bare, and as `Djinn.app` | yes |
| Windows | amd64, arm64 | no CGO, cross-compiled from Linux | yes |
| Linux | amd64, arm64 | no CGO (`_browser`) | no: the browser, for a Linux without WebKitGTK |
| any | any | `go install`, no CGO | Windows only; the browser elsewhere |

## What we want
- A release workflow triggered by a tag: build every target, attach the binaries, their
  checksums and the licence notices (`NOTICE`, `docs/THIRD_PARTY_NOTICES.md`) to the release.
- An install one-liner that picks the right binary, checks its checksum, and falls back to
  `go install` without CGO when no binary fits or the Linux libraries are missing.
- The same release runnable by hand on a developer's machine (`go tool task release`), so the
  CI is a convenience, not a dependency.

## Done when
- [ ] A tag produces binaries for every target in the table, with checksums. (needs: a maintainer to push a `v*`
  tag; `release.yml` exists, never run)
  - [x] The Linux jobs run by hand give the archives and the sums the workflow would (2026-10-10, W187, Ubuntu 22.04
    amd64, like the `ubuntu-22.04` runner, Go 1.26.7, WebKitGTK 2.50.4). From a checkout, after `npm ci && go tool task
    ui` in place of a tag's `dist/`, each step of `release.yml` as written: job `check`, `CGO_ENABLED=0 go build -o
    $RUNNER_TEMP/djinn ./cmd/djinn`; job `linux` (amd64, gtk3), `go tool task release-build VERSION=v0.0.0-dryrun`,
    which ran `CGO_ENABLED=1 go build -trimpath -tags 'gtk3' -ldflags "-s -w -X main.version=v0.0.0-dryrun -X
    main.releaseAsset=djinn_linux_amd64"` and `releasepack pack`, then its version loop; job `linux-browser`, both
    `CGO=0` builds and its version check; job `publish`, the archives alone in a fresh `bin/release/` (as
    `download-artifact` merges them), `cp LICENSE NOTICE docs/THIRD_PARTY_NOTICES.md scripts/install.sh
    scripts/install.ps1 bin/release/`, `go tool task release-sums`, then `sha256sum -c SHA256SUMS`: 8 files OK.
    `djinn_linux_amd64.tar.gz` 14.1 MB (binary 40.5 MB), one top folder `djinn_linux_amd64/` with `djinn` 0755 and the
    three notices; the browser builds are static, the arm64 one an aarch64 ELF.
  - [ ] Not reproduced here: the arm64 gtk3 build, both GTK 4 builds (`libwebkitgtk-6.0-dev` is absent on Ubuntu
    22.04), the macOS and Windows jobs, and `gh release create --draft`. (needs: an arm64 machine or runner, an Ubuntu
    24.04, and a pushed `v*` tag)
- [ ] A fresh account with no administrator rights installs a binary and opens the window on
  Linux (with the runtime libraries), macOS and Windows. (needs: a published release, a person on each system)
  - [x] Linux amd64, from the local release above (2026-10-10, W187): the README's line with the release served from
    a folder, `env -i HOME=<empty temp> DJINN_HOME=<temp> PATH=/usr/local/bin:/usr/bin:/bin
    DJINN_RELEASES=file://<folder> sh -c 'curl -fsSL "$DJINN_RELEASES/latest/download/install.sh" | sh'` (no `go`, no
    `~/go/bin`, no sudo): it took `djinn_linux_amd64`, checked its sum, put in `~/.local/bin/djinn` the very binary of
    the archive (`cmp`), said to add that folder to `PATH`, and `djinn version` printed `djinn v0.0.0-dryrun`. The same
    release with one byte added to the archive: refused, "does not match its SHA-256 sum", nothing installed.
    `djinn up --browser --port 0` (GitHub kept away by `HTTPS_PROXY=http://127.0.0.1:9`): the token URL redirects
    once to `/` with its cookie, which serves `<title>Djinn</title>` and its script (200); `ProjectService/List`
    answers 200 with the cookie, 401 without; `djinn project list` answers through `server.addr`; SIGINT stops it
    with exit 0, removes `server.addr` and closes the port. The failed release check only logs a line. `djinn up` on
    the X11 desktop (`:1`, GNOME): the window "Djinn" (`WM_CLASS djinn, Djinn`) maps at 2880x2000, the CLI answers
    through `djinn.sock`, the page shows the empty home ("Make a wish", "Live", "Workers 0/8"; captured by `xwd` with
    `WEBKIT_DISABLE_COMPOSITING_MODE=1`, since `xwd` reads a blank surface from WebKit's GPU compositing); SIGINT
    closes the window, exit 0, socket and address removed. The global shortcut Ctrl+Alt+Space was taken by the
    Djinn already running on that desktop: logged, not fatal. Not a second account, and not a machine without the
    build packages: see the libraries below.
  - [x] What the Linux binary loads matches what the README and `install.sh` ask for: `readelf -d` lists
    `libwebkit2gtk-4.1.so.0`, `libjavascriptcoregtk-4.1.so.0`, `libsoup-3.0.so.0`, `libgtk-3.so.0`, `libgdk-3.so.0`,
    `libgdk_pixbuf-2.0.so.0`, `libgio/gobject/glib-2.0.so.0`, `libX11.so.6`, `libc.so.6`, and the Ubuntu package
    `libwebkit2gtk-4.1-0` depends on every one of them (`dpkg -S`, `apt-cache depends`): WebKitGTK 4.1 is the whole
    runtime need. Newest glibc symbol `GLIBC_2.34`, as decided.
  - [ ] Open, seen on that desktop: with `HTTP_PROXY` set to a dead proxy and no `NO_PROXY`, the window never
    reached "Live" (twice: once "Reconnecting to Djinn...", once a dark page); with `NO_PROXY=localhost,127.0.0.1`,
    or without `HTTP_PROXY`, it did. The window uses `wails://`, so the cause is not established. (needs: a look at
    what WebKitGTK sends through the proxy; a user behind a proxy without `no_proxy` for localhost may see it)
- [ ] An update from inside the app replaces the binary in place (T12). (needs: a published release; the release
  check of T12 is built and tested against a fake release)
  - [x] Against the local release above (2026-10-10, W187): a binary built by release-build's own command with
    `-X main.version=v0.0.0-alpha -X main.releaseAPI=<a fake GitHub API on 127.0.0.1>`, installed in a fresh
    `~/.local/bin`, ran `djinn up --browser`; the fake API listed the files of `bin/release/` as the assets of the
    pre-release `v0.0.0-dryrun`. `djinn update --yes` (the `UiService.Update` call the window's banner makes) read the
    release list and `SHA256SUMS`, downloaded `djinn_linux_amd64.tar.gz`, then: "restarting on v0.0.0-dryrun, with 0
    terminals", "now running v0.0.0-dryrun", exit 0. `~/.local/bin/djinn` is now the archive's binary (`cmp`), alone in
    its folder, and the restarted `djinn up --browser` answers; SIGINT stops it. A test-only build flag points the
    check away from GitHub; the published binary asks api.github.com.
- [ ] macOS gets `Djinn.app`, a bundle with an identifier: Finder, Launchpad, the Dock's icon and the system
  notifications need it.
  - [x] `tools/macapp` lays out the bundle around the universal binary and zips it, portable, tested on Linux.
    (`go tool task test-pkg -- ./tools/macapp/...`: `TestBundleLayout`, `TestBundleVersion`,
    `TestBundleNamesDifferBeyondCase`, `TestBundleRefusesWhatIsMissing`, `TestZipApp`; `TestBundled`,
    `TestFromFinder`, `TestLoginShell`, `TestLoginPath` in `cmd/djinn`; `go tool task release-macos-app
    VERSION=v0.0.0-dryrun BINARY=<a GOOS=darwin build>` on Linux wrote `bin/release/djinn_darwin_universal_app.zip`,
    11 entries, `djinn` 0755)
  - [x] The release workflow builds it next to the bare archive, checks it and ships it with the sums.
    (`release.yml`, job `macos`; `actionlint` v1.7.12 passes; never run)
  - [ ] The dry run passes on GitHub: the bundle is well formed and opens. (needs: the lead to put `release.yml` on
    the default branch, then `gh workflow run release.yml --ref <branch> -f version=v0.0.0-dryrun`)
  - [ ] Opened on a Mac from Finder: the window, the icon in the Dock and Launchpad, a notification. (needs: a Mac)

## Decided along the way
- **The CI is GitHub Actions.** `.github/workflows/ci.yml`, on every pull request and every push to `main`: `go tool
  task lint` and `go tool task test` (Go, interface, end-to-end in headless Chromium) on Linux; `test-go` on macOS and
  Windows. Windows reports without blocking until it is green once (T11).
- **A tag builds, a person publishes.** `.github/workflows/release.yml`, on a `v*` tag: it checks that the tag carries
  `dist/` and that Djinn builds without CGO (the `go install` fallback), builds every target, and opens a **draft**
  release with the archives, `SHA256SUMS`, `LICENSE`, `NOTICE`, `THIRD_PARTY_NOTICES.md` and the install scripts. A
  maintainer reads it and publishes it; only then is it the latest. A tag with a `-` (`v0.0.1-test`) is a pre-release.
- **Every step runs by hand, the CI adds nothing.** `go tool task release-build VERSION=… [GOOS= GOARCH= CGO=]`
  builds one archive into `bin/release/` (Windows from Linux too), `release-build-macos` the universal one (`lipo`,
  macOS only), `release-sums` the sums. `tools/releasepack` packs and sums, portable. They use the `dist/` already
  there: a tag carries it, a checkout runs `go tool task ui` first.
- **Linux without WebKitGTK gets a binary too.** `djinn_linux_<arch>_browser.tar.gz`, built without CGO (11 MB):
  no window, `djinn up` opens the browser. `install.sh` tries the window builds whose WebKitGTK it finds, then this
  one, then `go install`; it says how to get the window. A release without it (an older one) still ends on `go
  install`. Tested in `tools/releasepack/install_test.go`.
- **Asset names carry no version**: `djinn_<os>_<arch>[_gtk4|_browser].tar.gz`, `djinn_windows_<arch>.zip`,
  `djinn_darwin_universal.tar.gz`, each with a top folder holding `djinn` and the notices; and
  `djinn_darwin_universal_app.zip`, which holds `Djinn.app` alone (see below). So
  `releases/latest/download/<name>` always works, with no API call. The version is the tag, and `djinn version`
  prints it (`-X main.version`).
- **Linux GTK 3 builds on Ubuntu 22.04**, the oldest supported: the binary needs glibc 2.34 or later. **GTK 4 builds on
  Ubuntu 24.04, as an experiment**: never built locally yet, it may fail without failing the release. arm64 uses
  GitHub's arm runners, no cross-compiler.
- **macOS ships unsigned for now.** The steps to sign and notarize are written, commented, in the workflow and in
  `release-build-macos`, with the secrets they need. A binary downloaded by `curl` gets no quarantine flag, so
  `install.sh` works unsigned; a browser download is blocked until allowed in System Settings.
- **macOS gets Djinn.app too**, beside the bare binary, which `install.sh` and the command line keep using. One
  universal bundle serves arm64 and amd64 Macs, around the very binary of `djinn_darwin_universal.tar.gz`.
  `tools/macapp` lays it out and zips it; `go tool task release-macos-app VERSION=… [BINARY=…]` runs it anywhere, and
  `release-build-macos` calls it. The bundle:
  - `Contents/Info.plist`: `CFBundleIdentifier` `io.github.empowill.djinn` (it follows the module path; changing it
    once released makes macOS ask for the notifications again), the version from the tag as three numbers
    (`v1.2.3-rc.1` → `1.2.3`), `LSMinimumSystemVersion` 12.0 like the build, the icon's name.
  - `Contents/Resources/djinn.icns`: `build/icon.icns`, which `tools/icons` makes from the logo, as `go-winres` makes
    the `.exe` icon from `build/icon.ico`. The notices sit beside it.
  - `Contents/MacOS/djinn`, the bundle's executable, is the binary itself (W67's question, B): no launcher script, a
    weaker thing to sign and notarize. Finder starts an app with no arguments (older macOS with a `-psn_…` one), and
    `djinn` alone prints its help: `djinn` knows it runs from a bundle by its path, `…/<name>.app/Contents/MacOS/djinn`,
    and runs `up` (`cmd/djinn/bundle.go`, macOS only). An app started by macOS gets a bare `PATH`
    (`/usr/bin:/bin:/usr/sbin:/sbin`), without the agents' commands: `djinn` first asks the user's shell (bash,
    zsh, ksh or sh from `$SHELL`; zsh otherwise), interactive and login (`-i -l -c`, as VS Code does), for its
    `PATH`, which is a terminal's, and takes it: a login shell alone skips `~/.zshrc`, where nvm and many agent
    installers add to `PATH`. A mark picks the `PATH` out of what the profiles print (`TestLoginPath`). A shell that
    fails, hangs past 10 s or prints no `PATH` leaves the bare one, and `djinn` says so. The process macOS started is
    `djinn up`, and the bundle around it gives it its identifier. The same binary run from a terminal by that path with
    no arguments starts `up` too.
  - The zip holds `Djinn.app` at its root, with the executable bits: Finder unzips it into the app, ready to drag
    into Applications. Once signed, the bundle must be zipped by `ditto` (its signature lives partly in extended
    attributes); the commands are written, commented, in `release-macos-app` and the workflow.
  - The workflow unzips it with `ditto`, lints `Info.plist` (`plutil`), reads its keys (`PlistBuddy`), checks that
    the bundle's `djinn` is the archive's and prints the tag, then opens it with `open` and asks Launch Services for
    `io.github.empowill.djinn` (this last step does not block a release until it has passed once).
  - **A dry run without a release**: `release.yml` also runs on `workflow_dispatch`, with a `version` input
    (`v0.0.0-dryrun` by default). Only the macOS job runs: it builds the interface (a branch has no `dist/`), the
    archive and the bundle, checks them, and keeps them as the run's artifact; nothing is published. GitHub offers it
    once `release.yml` with its `workflow_dispatch` is on the default branch:
    `gh workflow run release.yml --ref <branch> -f version=v0.0.0-dryrun`, then `gh run watch` and
    `gh run download <run-id> -n darwin-universal`.
  - **Still to do around it**: an update from inside the app (T12) swaps `Contents/MacOS/djinn` for the archive's
    binary, as for the bare one; `Info.plist` keeps the older version, and a signed bundle's seal breaks. `install.sh`
    could put `Djinn.app` in `~/Applications` (no sudo, and `curl` sets no quarantine flag).
- **What Gatekeeper still lacks**, for the bare binary and `Djinn.app` alike: a Developer ID Application signature
  with the hardened runtime and a timestamp, then notarization by Apple, and the ticket stapled to the app. Until
  then, an app downloaded by a browser carries the quarantine flag: macOS 15 refuses to open it, with no Control-click
  way round; the user allows it in System Settings › Privacy & Security › "Open Anyway", or runs
  `xattr -dr com.apple.quarantine Djinn.app`. Opened from Downloads while quarantined, macOS runs it from a
  read-only copy elsewhere (App Translocation), where an update cannot write: move it to Applications first. Apple
  silicon runs only signed code: the Go linker signs the arm64 half ad hoc, which is enough without quarantine. Wails
  says its macOS notifications need a bundled and signed app: whether the ad hoc signature suffices is to see on a
  Mac. A maintainer holds the Apple account and its secrets (open question below).
- **A release built by hand installs from its folder.** `install.sh` takes `DJINN_RELEASES=file:///path` (with
  curl), the folder laid out as GitHub serves it (`latest/download/…`, `download/<tag>/…`): how W187 tried the Linux
  archive before any release. Tested in `TestInstallFromAFolder`.
- **Actions are pinned by commit**, with their version in a comment. `actionlint` (with `shellcheck`) checks the
  workflows: `go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12`.
- **Sizes**, built locally at `-s -w`: Linux amd64 gtk3 36.8 MB (archive 12.9 MB), Windows amd64 37.3 MB (13.1 MB),
  Windows arm64 35.3 MB (12.1 MB), without CGO (`go install`) 33.1 MB on Linux amd64 (11.5 MB).

## Open questions
- Linux window without sudo: a binary per release, or a package that carries WebKitGTK
  (question Q31 of the flight plan).
- Signing: macOS notarization needs an Apple developer account; its secrets live in the CI,
  never in Djinn. Who holds it?
- Which equivalent if we leave GitHub Actions (a self-hosted runner, a forge's own CI)?
- Signatures beyond checksums (Sigstore, minisign)?

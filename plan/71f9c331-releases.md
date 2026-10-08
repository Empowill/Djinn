---
id: 01a118aa-6f2a-7cba-a7ed-f16071f9c331
code: T19
phase: 3
status: open
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
| macOS | universal (arm64 + amd64) | CGO, signed and notarized | yes |
| Windows | amd64, arm64 | no CGO, cross-compiled from Linux | yes |
| Linux | amd64, arm64 | no CGO (`_browser`) | no: the browser, for a Linux without WebKitGTK |
| any | any | `go install`, no CGO | Windows only; the browser elsewhere |

## What we want
- A release workflow triggered by a tag: build every target, attach the binaries, their
  checksums and the licence notices (`NOTICE`, `THIRD_PARTY_NOTICES.md`) to the release.
- An install one-liner that picks the right binary, checks its checksum, and falls back to
  `go install` without CGO when no binary fits or the Linux libraries are missing.
- The same release runnable by hand on a developer's machine (`go tool task release`), so the
  CI is a convenience, not a dependency.

## Done when
- [ ] A tag produces binaries for every target in the table, with checksums.
- [ ] A fresh account with no administrator rights installs a binary and opens the window on
  Linux (with the runtime libraries), macOS and Windows.
- [ ] An update from inside the app replaces the binary in place (T12).

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
  `djinn_darwin_universal.tar.gz`, each with a top folder holding `djinn` and the notices. So
  `releases/latest/download/<name>` always works, with no API call. The version is the tag, and `djinn version`
  prints it (`-X main.version`).
- **Linux GTK 3 builds on Ubuntu 22.04**, the oldest supported: the binary needs glibc 2.34 or later. **GTK 4 builds on
  Ubuntu 24.04, as an experiment**: never built locally yet, it may fail without failing the release. arm64 uses
  GitHub's arm runners, no cross-compiler.
- **macOS ships unsigned for now.** The steps to sign and notarize are written, commented, in the workflow and in
  `release-build-macos`, with the secrets they need. A binary downloaded by `curl` gets no quarantine flag, so
  `install.sh` works unsigned; a browser download is blocked until allowed in System Settings.
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

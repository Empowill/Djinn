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
| Windows | amd64, arm64 | no CGO | yes |
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

## Open questions
- Linux window without sudo: a binary per release, or a package that carries WebKitGTK
  (question Q31 of the flight plan).
- Signing: macOS notarization needs an Apple developer account; its secrets live in the CI,
  never in Djinn. Who holds it?
- Which equivalent if we leave GitHub Actions (a self-hosted runner, a forge's own CI)?
- Signatures beyond checksums (Sigstore, minisign)?

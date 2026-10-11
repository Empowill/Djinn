# Handoff: Testing Djinn on macOS with Codex

A practical guide for Clément to test Djinn's new native window and recent features on macOS using Codex.

## 1. Install on a Mac

There are two ways to get Djinn running on macOS:

### (a) Unsigned Djinn.app (Release dry run)

Download the pre-packaged universal macOS application bundle from the release dry run of `feat/wails-go`:
- **Run**: GitHub Actions run [38148001105](https://github.com/empowill/djinn/actions/runs/38148001105) of `release.yml`.
- **Artifact**: `djinn_darwin_universal_app.zip`.
- **Install**: Unzip the archive and place `Djinn.app` into `/Applications` (or any working folder).
- **Opening unsigned app**: macOS Gatekeeper will block opening the unsigned bundle directly on first launch with a warning that the developer cannot be verified. To open it anyway, **right-click (or Control-click) Djinn.app in Finder and choose Open**, then click **Open** in the confirmation dialog (or run `xattr -d com.apple.quarantine /Applications/Djinn.app`). Checking whether macOS accepts the ad hoc signature and opens cleanly is itself one of the open verification boxes in T02.

### (b) From source

To build and install from a Git checkout:
1. **Prerequisites** (see [`CONTRIBUTING.md`](../CONTRIBUTING.md#getting-set-up)):
   - Xcode Command Line Tools (`xcode-select --install`; verify with `xcode-select -p`).
   - Go 1.25+ with CGO enabled (required for native macOS WebKit bindings).
   - Node.js 22 and npm.
2. **Build and install**:
   ```sh
   git checkout feat/wails-go # commit 62f6f9a or later
   npm ci
   go tool task install
   ```
   This compiles the native binary and installs `djinn` into your Go bin directory (`~/go/bin/djinn` or `$GOPATH/bin`).

## 2. Run it with Codex

### Provider configuration

Configure your project to run worker tasks with Codex by adding the provider override:

```protobuf
provider: PROVIDER_CODEX
```

in your personal local override file:
`~/.config/djinn/projects/<project id>/settings.txtpb`

- **Find the project ID**: run `djinn project list` or `djinn project show <project>` (which prints its folder path and the exact location of its settings file; see [`docs/team-settings.md`](team-settings.md#your-own-file)).
- **Scope**: this override is per-machine and per-person, is never committed, and takes precedence over `.agents/settings.txtpb`.

### Codex authentication

Make sure the `codex` command-line tool is installed and signed in on your Mac:
```sh
codex --version
```
Ensure you have active credentials (via `codex login` or existing authentication).

### What to watch & capture procedure

Djinn has never run against a real Codex instance in production; its current Codex test fixtures are hand-written (see [`docs/providers.md`](providers.md#codex-to-check)).

Please run the capture procedure below in an empty Git repository to capture the raw protocol handshake and thread startup from the live `codex` app-server:

```sh
mkdir -p /tmp/codex-try && cd /tmp/codex-try && git init -q && echo "# Try" > README.md
codex --version
printf '%s\n' \
  '{"id":1,"method":"initialize","params":{"clientInfo":{"name":"djinn","title":"Djinn","version":"0.3.0"}}}' \
  '{"method":"initialized"}' \
  "{\"id\":2,\"method\":\"thread/start\",\"params\":{\"cwd\":\"$PWD\"}}" \
  | codex app-server --listen stdio:// > thread.jsonl 2> thread.stderr
```

To record a real turn through Djinn:
```sh
djinn task spawn <wish> --provider codex --prompt "Reply with the word apple, nothing else." --model <cheapest model>
djinn task watch <task> --raw --json > turn.jsonl
```

Send the resulting `.jsonl` and `.stderr` files to the developer (or commit them as fixtures on a branch). In addition, note and report any Codex errors or unexpected behaviors Djinn shows in the window or terminal.

## 3. What needs a Mac

The following table lists all open plan boxes marked `(needs: a Mac)` across [`plan/*.md`](../plan/README.md) and how to verify each one:

| Plan / Azima | Open plan box (`needs: a Mac`) | How to check |
| :--- | :--- | :--- |
| **T05** ([`plan/b6a680bf-testing.md`](../plan/b6a680bf-testing.md)) | `- [ ] The same scenario runs on macOS. (needs: a Mac, go tool task e2e-native once)` | Run `go tool task e2e-native` once from the repository. Verifies that the native window end-to-end test passes on macOS (Wails MCP server, DOM rendering, typing `whoami` in the terminal, window minimize/hide). |
| **T01** ([`plan/fe63ed30-native-window.md`](../plan/fe63ed30-native-window.md)) | `- [ ] djinn up opens the window on Linux and macOS, rendering compared. (needs: a Mac, and a person to compare the rendering with Linux)` | Run `djinn up` (or launch `Djinn.app`). Check that the native window opens cleanly and compare its visual layout, fonts, and controls with Linux. |
| **T01** ([`plan/fe63ed30-native-window.md`](../plan/fe63ed30-native-window.md)) | `- [ ] Same under WKWebView (macOS) and WebView2 (Windows). (needs: go tool task check-window on a Mac and on Windows)` | Run `go tool task check-window`. Verifies that server streaming Connect responses reach the native window under WKWebView over the internal asset server without buffering, and measures latency. |
| **T19** ([`plan/71f9c331-releases.md`](../plan/71f9c331-releases.md)) | `- [ ] Opened on a Mac from Finder: the window, the icon in the Dock and Launchpad, a notification. (needs: a Mac)` | Open `Djinn.app` directly from Finder. Confirm that the window opens, the Djinn application icon appears in both the Dock and Launchpad, and a native macOS notification appears when an active wish asks a question. |
| **T02** ([`plan/c30479be-api-and-cli.md`](../plan/c30479be-api-and-cli.md)) | `- [ ] macOS: the Dock shows the icon at run time; Finder and Launchpad need an .app bundle with build/icon.icns (T19). The release builds it: djinn_darwin_universal_app.zip, Djinn.app with Contents/Resources/djinn.icns (tools/macapp). (needs: a Mac, to open it from Finder)` | Launch `Djinn.app` from Finder or run `djinn up` from the terminal and check that the application icon is shown in the Dock while running. |
| **T02** ([`plan/c30479be-api-and-cli.md`](../plan/c30479be-api-and-cli.md)) | `- [ ] macOS: needs the .app bundle and its identifier. T19 builds Djinn.app, identifier io.github.empowill.djinn; Wails asks for a signed app too, and the bundle is unsigned. (needs: a Mac, to see whether the ad hoc signature suffices)` | Test whether the ad hoc signature in the unsigned release bundle suffices for Wails native features (like desktop notifications) or whether macOS Gatekeeper restricts them. |
| **T02** / **T25** ([`plan/4a699d0f-review-at-a-glance.md`](../plan/4a699d0f-review-at-a-glance.md)) | `- [ ] djinn://tilasm/<id> opens the app on the tilasm from outside, Djinn running or not, on Linux (a desktop entry for x-scheme-handler/djinn), macOS (CFBundleURLTypes in Djinn.app) and Windows (the registry, by the installer). (needs: a Mac, and a Windows machine)` | Click or open a `djinn://tilasm/<id>` link from outside (e.g. via Terminal `open djinn://tilasm/<id>` or from a browser). Verify that it starts Djinn.app or brings a running Djinn forward and opens the tilasm in its wish's Tilasms tab. |
| **T02** ([`plan/c30479be-api-and-cli.md`](../plan/c30479be-api-and-cli.md)) | `- [ ] Pressed by hand on macOS and on Windows. (needs: a Mac, and a Windows machine)` | Press the macOS global shortcut (`Ctrl+Cmd+J` by default, or as configured in settings) anywhere on the desktop. Verify by hand that it brings the Djinn window forward to the active wish with the newest open question (or flight plan). |

## 4. New since PR #1 worth trying in passing

- **Operating load cursor with five notches and Overclock**: A slider in the status bar (`minimal`, `light`, `medium`, `high`, `max`, and `overclock` / auto) dynamically controlling worker slots, pressure stall thresholds, gate margins, and committable memory (`djinn load get/set`).
- **Memory engaged by each worker's 95th percentile**: The scheduler calculates engaged memory forecasts from workers' 95th percentile resident memory (`p95_memory_bytes`) sampled every 5 seconds instead of raw peak spikes, with an optional dynamic memory guardrail (`--worker-memory-guard`).
- **The consumption panel**: A collapsible resource consumption panel under the flight plan showing real-time CPU and memory graphs, gate wait/held stats (p50, p95), sparklines, and active worker breakdown.
- **Finished tasks read Merging until committed**: When a worker finishes, its badge and azima display a distinct amber "Merging" state until Djinn completes integration checks and commits the work.
- **Keyboard shortcuts on question cards**: Quick keyboard shortcuts on open question cards allow answering, asking for enlightenment, or rubbing the lamp without mouse clicks.
- **Wishes renamed from the command line**: Rename any wish directly from the CLI with `djinn wish rename <wish> <new title>` or through the inline UI editor.
- **Questions moved or withdrawn**: Relocate questions to another wish or withdraw outdated questions via CLI (`djinn question move`, `djinn question withdraw`) and the window interface.
- **Every task shows its model, its real work time and its tokens and cost**: Every task card displays the worker's selected model, actual execution duration, token counts (input, cached, thinking, output), and estimated cost calculated from official provider pricing tables.
- **The window refreshes at once on plan changes**: Plan file modifications, task status transitions, and watch stream mutations propagate instantly to refresh all derived UI views without manual reloads.
- **SVG diagrams in blocks**: Architectural and wish diagrams render as clean, scalable vector graphics (SVG) with isolated styling (`svg-diagrams.css`) instead of fallback text.
- **Red push checks start a correction worker**: When automated pre-push checks fail on the integration branch, Djinn automatically spawns an autonomous correction worker to diagnose and fix the failure.
- **A task waits until its dependencies' work is in the integration branch**: The scheduler strictly blocks dependent tasks from starting until their upstream dependencies' commits are verified and merged into the integration branch.
- **The update banner after each push**: After each successful push, an update banner notifies you of changes, folding task summaries and proposing to install new builds if configured.

## 5. How to report

- **Proof questions in the window**: Answer Djinn's proof questions directly in the window (**Verified** / **Not yet**) when they appear for azimas awaiting proof, or tick nothing yourself in the plan files.
- **Other feedback and issues**: Report anything else as a GitHub issue or message the developer, including the Djinn version (`djinn version`), macOS version/architecture, and a screenshot of the window, Dock, or terminal.

## 6. Known pitfalls

- **Antigravity-specific notes do not apply**: Documentation regarding Antigravity (such as sandbox permissions and `agy` flags) is specific to that environment and does not apply to Codex.
- **Unknown settings fields (W377)**: If a settings file refuses an unknown field after pulling changes, update Djinn to the latest commit on `feat/wails-go`.
- **First build needs `npm ci`**: When building from source, always run `npm ci` before building with Task to ensure frontend assets and tools are available.

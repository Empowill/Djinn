# Handoff: Testing Djinn on macOS with Codex

A practical guide for Clément to test Djinn's new native window and recent features on macOS using Codex.

## 1. Before you start

### Your git identity

Your commits so far carry your Mac's local address (`clementfauvelle@macbook-air-de-alexandre-3.home`), which GitHub
links to no account. In your Djinn checkout, before you commit:

```sh
git config user.name "Clément Fauvelle"
git config user.email "44783512+clemzer0o@users.noreply.github.com" # your GitHub noreply address, or your account's
git log -1 --format='%an <%ae>' # after your next commit: shows the name and address above
```

### A fresh data folder for `feat/wails-go`

Wishes made on your branches (`feat/harness-friendly`, `feat/wish-minimal-workspace`) store fields that read
differently on `feat/wails-go`. Before you switch, start Djinn from a fresh data folder: quit Djinn and move
`~/Library/Application Support/djinn` aside (`djinn project show <project>` prints your settings file, inside that
folder), or point `DJINN_HOME` at an empty folder.

Your work is not lost: Djinn ports it onto `feat/wails-go` commit by commit, under your name, on branch
`feat/clement-port`.

## 2. Install on a Mac

There are two ways to get Djinn running on macOS:

### (a) Unsigned Djinn.app (Release dry run)

Download the pre-packaged universal macOS application bundle from the release dry run of `feat/wails-go`:
- **Run**: GitHub Actions run [38148001105](https://github.com/empowill/djinn/actions/runs/38148001105) of `release.yml`.
- **Artifact**: `darwin-universal` (`djinn_darwin_universal_app.zip` is inside).
- **Install**: Unzip the artifact, extract `Djinn.app` from `djinn_darwin_universal_app.zip`, and place `Djinn.app` into `/Applications` (or any working folder).
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

## 3. Run it with Codex

### Provider configuration

Configure your project to run worker tasks with Codex by adding the provider override:

```protobuf
provider: PROVIDER_CODEX
```

in your personal local override file:
`~/Library/Application Support/djinn/projects/<project id>/settings.txtpb` (`~/.config/djinn/…` on Linux)

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

## 4. What needs a Mac

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
| **T02** ([`plan/c30479be-api-and-cli.md`](../plan/c30479be-api-and-cli.md)) | `- [ ] Pressed by hand on macOS and on Windows. (needs: a Mac, and a Windows machine)` | Press the macOS global shortcut (`Ctrl+Cmd+J` by default, or as configured in settings) anywhere on the desktop. Verify by hand that it brings the Djinn window forward to the active wish with the newest open question (or flying carpet). |

## 5. Runs on your Mac as your work is ported

Each check below comes as its port lands on `feat/clement-port`, then on `feat/wails-go`:

- **`go tool task e2e-native`**: the live updates, the titlebar and toolbar test (`e2e/native/titlebar_toolbar_test.go`),
  and the smoke geometry test (`e2e/native/smoke_geometry_test.go`) once ported.
- **Djinn.app opened from the Finder** finds `claude` and `codex` and shows both signed in.
- **Notifications**: macOS asks for the permission, and a click on a notification lands on its card.
- **The window**: the titlebar, the sidebar toggle, and dragging the window.

The developer has no Mac: the checks below go to you. Each runs on `feat/clement-port`, where your ports are merged
(`git checkout feat/clement-port && git pull && npm ci`); its `grep` prints a line once the port is there. Report each
one as **pass** or **fail** with its number (Q05, Q06, Q07, Q234) and what you saw, as in [section 7](#7-how-to-report).

### Q05: a question asked from the command line shows live (P1, your `70ca727`)

**Proves** that `server.WholeWrites` hands each stream message to the `wails://` handler in one write: macOS WebKit
sometimes held a payload back after its 5-byte prefix, and the window stayed frozen until a reload. On your branch the
native e2e failed 5 runs in 6 without it, 0 in 13 with it; Linux passed before the fix too, so only a Mac tells.

```sh
grep -n 'server.WholeWrites(h)' cmd/djinn/up.go
for i in 1 2 3 4 5 6; do go tool task e2e-native -- -count=1 -v -run 'TestNativeWindow/a_question_asked_by_the_command_line'; done
```

**Look at**: each of the 6 runs ends `--- PASS: TestNativeWindow/a_question_asked_by_the_command_line_shows_in_the_window`.
Optional, to see the bug: replace `server.WholeWrites(h)` with `h` in that line, run the loop again (some runs should fail
with "no question … in the window"), then `git checkout cmd/djinn/up.go`.

### Q06: the macOS titlebar (P16)

**Proves** the Wails options of `cmd/djinn/window.go` (`MacTitleBarHidden`, `InvisibleTitleBarHeight: 16`); Linux proves
only the CSS.

```sh
grep -n 'InvisibleTitleBarHeight' cmd/djinn/window.go
go tool task build && DJINN_HOME=$(mktemp -d) ./bin/djinn
```

**Look at**: 1) the three traffic lights sit over the top of the sidebar, above the logo (28 px padding), with no grey
titlebar; 2) dragging the top 16 px moves the window, over the main view too; 3) the logo button, **New wish** and the
topbar buttons all answer a click (none lost under the strip); 4) the sidebar spans the whole height, the terminal sits
under the main view only.

### Q07: Djinn.app from the Finder finds the agents (P3, one agent resolver)

**Proves** the real launch: launchd gives an app no `~/.local/bin` and no Codex.app on its PATH. Linux proves the logic
only, with fake folders.

```sh
grep -n 'TestExtendPathAfterAnInstall' internal/machine/path_test.go
go tool task build
go run ./tools/macapp bundle --binary bin/djinn --version v0.0.1 bin/app
open -R bin/app/Djinn.app # shows it in the Finder: open it from there, not from a terminal
```

**Look at**: 1) a task with the Claude agent starts (`claude` in `~/.local/bin`); 2) with Codex.app installed and no
other `codex` on the PATH, a task with the Codex agent starts; 3) with `~/.local/bin/claude` moved aside before Djinn
starts, install it while Djinn runs (`curl -fsSL https://claude.ai/install.sh | bash` in a Terminal) and start a task:
it is found without restarting Djinn. If one fails, the Finder shows no stderr: quit Djinn, run
`env -i HOME=$HOME bin/app/Djinn.app/Contents/MacOS/djinn up </dev/null` in a Terminal (an environment as bare as
launchd's), and send the line that starts "djinn: the PATH of the login shell: …".

### Q234: Djinn logs what it does, and a stop that hangs dumps then exits (W511, on a Mac and on Windows)

**Proves** `internal/logx` where Linux cannot: Windows renames only a closed file (the log rolls over in the process),
has no `SIGUSR2` (`djinn debug dump` falls back to `logs/dump.request`), and a stop must end on both systems. W511 is on
`feat/wails-go`; `feat/clement-port` has it once its grep prints a line. Run it on the Mac, then on Windows in
PowerShell (`$env:DJINN_HOME = "$env:TEMP\djinn-q234"`, `$L = "$env:DJINN_HOME\logs"`, `.\bin\djinn.exe`, `dir`).

```sh
grep -n 'HungExit = 3' internal/logx/shutdown.go
go tool task build && export DJINN_HOME=$(mktemp -d) L=$DJINN_HOME/logs && ./bin/djinn
```

**Look at**, one line per check, Djinn running unless said (commands from a second terminal, same `DJINN_HOME` and `L`):
1. **The log fills**: add a project and start a task; `grep -E 'msg=(call|command|"process started")' $L/djinn.log`
   shows `msg=call method=/…` lines, `msg=command argv="git …" dir=… took=… exit=0`, and `msg="process started"`.
2. **Rotation at 10 MB keeps 4**: quit Djinn, `mkfile 10m $L/djinn.log` (Windows:
   `$f=[IO.File]::Open("$env:DJINN_HOME\logs\djinn.log",'OpenOrCreate'); $f.SetLength(10MB); $f.Close()`), start and
   quit Djinn; five times over, `ls -l $L` shows `djinn.log` and `djinn.log.1` to `djinn.log.4` (10 MB each), no `.5`.
3. **A dump through the server**: `./bin/djinn debug dump` prints `$L/goroutines-<time>.txt`, which starts
   `djinn: goroutines of pid …`; `djinn.log` gets `msg="goroutines dumped"`.
4. **A dump through `logs/dump.request`** (a server that does not answer; the only way on Windows): `./bin/djinn
   debug dump --addr 127.0.0.1:1` says `the server does not answer (…): asking through dump.request`, then prints a
   new `goroutines-<time>.txt` within a few seconds whose first line ends `asked by dump.request`; no `dump.request`
   is left.
5. **Quitting**: quit from the window; the last lines of `djinn.log` are `msg="shutdown begins"`, a
   `msg="shutdown step ended" step=…` per step, then `msg="shutdown ended"`.
6. **A stop that hangs** (no way to provoke one by hand): `go tool task test-pkg -- -count=1 -v -run
   'TestShutdown|TestRotating|TestDebugDump|TestDumpGoroutinesOnRequestFile' ./internal/logx ./cmd/djinn`
   ends `PASS` for both packages. If a real quit ever takes over 10 s: `msg="shutdown is slow"` and a
   `goroutines-<time>.txt` at 10 s, `msg="shutdown hangs: exiting" … code=3` at 30 s, and `echo $?` (Windows:
   `$LASTEXITCODE`) prints `3`; send both files.

### V1 to V6: the azimas awaiting proof, on the published v0.1.1 (W567)

**Proves** on a Mac what the developer validated on Linux on 13/10/2026: these run on the **published release
v0.1.1**, not on a branch. Each starts from a fresh data folder (`export DJINN_HOME=$(mktemp -d)`), never your own,
but V2: the Finder gives Djinn.app no `DJINN_HOME`, so it opens your usual data folder.
Report each as **pass** or **fail** with its number (V1 to V6), as in [section 7](#7-how-to-report).

**V1 · T04, T19: a new user installs v0.1.1.** In a macOS account without administrator rights, or a fresh `HOME`:

```sh
time (curl -fsSL https://github.com/Empowill/Djinn/releases/latest/download/install.sh | sh)
~/.local/bin/djinn version && ~/.local/bin/djinn up
```

**Look at**: `installed djinn v0.1.1 in …/.local/bin`, no password asked; `djinn v0.1.1`; the window opens and its
status bar reads **Live**. Send the time of the install and of the window (on Linux: 6 s, then under 1 s).

**V2 · T19: Djinn.app of v0.1.1, from the Finder.** Download `djinn_darwin_universal_app.zip` from the
[v0.1.1 release](https://github.com/Empowill/Djinn/releases/tag/v0.1.1), unzip it, put `Djinn.app` in `/Applications`
and open it from the Finder (right-click, **Open**, the first time). **Look at**: the window; the icon in the Dock and
in Launchpad; with the window in the background, a notification for, in a new Terminal,
`/Applications/Djinn.app/Contents/MacOS/djinn question ask "Mac check" <wish>` (a wish made with **New wish**); a
click on it lands on the question.

**V3 · T14, T33: each worker's CPU and memory on macOS** (its process group: no cgroup there). While a task with an
agent runs:

```sh
djinn machine show
```

**Look at**: `worker_uses` lists the task with `cpu_percent` and `memory_bytes` above zero, and the Hourglass in the
window draws its graphs.

**V4 · T35: no worker outlives Djinn on macOS** (no `PR_SET_PDEATHSIG` there: the sentinel stops the children). While
a task's worker runs:

```sh
pgrep -fl 'claude|codex'   # the worker's pid
pkill -9 -x djinn          # Djinn dies without its shutdown
sleep 5; pgrep -fl 'claude|codex'
```

**Look at**: the worker is gone after the kill; started again, Djinn resumes the task once, with one agent process per
running task, never two.

**V5 · T20: a backup restores on macOS.** With the test Djinn of V1 running and a wish made in it:

```sh
djinn backup --file /tmp/djinn-v5.tar.gz
```

Quit Djinn, then `DJINN_HOME=$(mktemp -d) djinn backup restore /tmp/djinn-v5.tar.gz` and `djinn up` with that same
`DJINN_HOME`. **Look at**: the wish is there (on Linux, W567 restored a wish and a project this way).

**V6 · T20: a shared wish page, seen by a second person** (any system). When the developer sends you the link of a
wish's published page, open it. **Look at**: the wish as it is (its questions, tasks and decisions); say what was
missing or wrong.

## 6. New since PR #1 worth trying in passing

- **Operating load cursor with five notches and Overclock**: A slider in the status bar (`minimal`, `light`, `medium`, `high`, `max`, and `overclock` / auto) dynamically controlling worker slots, pressure stall thresholds, gate margins, and committable memory (`djinn load get/set`).
- **Memory engaged by each worker's 95th percentile**: The scheduler calculates engaged memory forecasts from workers' 95th percentile resident memory (`p95_memory_bytes`) sampled every 5 seconds instead of raw peak spikes, with an optional dynamic memory guardrail (`--worker-memory-guard`).
- **The consumption panel**: A collapsible resource consumption panel under the flying carpet showing real-time CPU and memory graphs, gate wait/held stats (p50, p95), sparklines, and active worker breakdown.
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

## 7. How to report

- **Proof questions in the window**: Answer Djinn's proof questions directly in the window (**Verified** / **Not yet**) when they appear for azimas awaiting proof, or tick nothing yourself in the plan files.
- **Other feedback and issues**: Report anything else as a GitHub issue or message the developer, including the Djinn version (`djinn version`), macOS version/architecture, and a screenshot of the window, Dock, or terminal.

## 8. Known pitfalls

- **Antigravity-specific notes do not apply**: Documentation regarding Antigravity (such as sandbox permissions and `agy` flags) is specific to that environment and does not apply to Codex.
- **Unknown settings fields (W377)**: If a settings file refuses an unknown field after pulling changes, update Djinn to the latest commit on `feat/wails-go`.
- **First build needs `npm ci`**: When building from source, always run `npm ci` before building with Task to ensure frontend assets and tools are available.

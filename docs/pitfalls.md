# Known pitfalls

Traps Djinn's code has set for those who work on it, each learned the hard way by a task (the `W…` names it). One
entry each: what goes wrong, how to recognise it, what to do, where. Add one when an incident teaches you something
the code does not say by itself; remove one when a change makes it false.

## Go tests and CI

- **A test over the time limit is a failing test.** `test-go` fails a Go test over 10 s on Linux (15 s elsewhere), and
  the project counts one over 1 s as a bug. *Do:* a fake clock, a short tick, a wait on an event; keep git merges
  and spawned processes few. *Where:* `tools/slowtests`.
- **A slow test with an idle CPU waits on fsync.** Djinn's store syncs each commit (`synchronous(FULL)`), ~20 ms on a
  machine busy with workers: `TestEventsBesideTheRun` took 8.3 s alone, 0.8 s of it on the CPU (W124). Tests now
  open their stores with `synchronous(NORMAL)` (`durability`, under `go test` or `DJINN_TEST=1`). *Sign:* a CPU
  profile far shorter than the test; `-test.trace`, then `go tool trace -pprof=syscall`, shows `Tx.Commit`. `strace
  -c` hides it: it counts the CPU time of a syscall, not its wait. *Where:* `internal/store/store.go`.
- **The harness tests run in parallel; a few cannot.** W182 made 171 serial `internal/harness` tests parallel (the
  package from about 110 s to about 55 s, `-race` from 8 min to 5). Stay serial: a test that sets the environment
  (`t.Setenv` panics after `t.Parallel`: a fake `gh` on `PATH`, `HOME`), one that sets a hook every worker reaches
  (`setPriorityFn`), and one with parallel subtests: queued behind the whole package, `TestReleaseFit` took 17 s.
  *Sign:* passes alone, red in a loaded full run under `-race`: a wait on a duration, or a fake clock started before
  something made with the real time (an integration test's clock now starts once its wish is made: the first task
  merged looked older than the wish, and no push was due). *Where:* `internal/harness`.
- **The scheduler reads a copy of a run's task.** The pump changes `r.task` without a lock; a reader apart from the
  pump (`lowestWorkTask`, `resumeNotchPaused`, `lowestPausedWorkTask`) reads `r.shown`, the copy `writeAs` publishes
  under `h.mu` (W182: a data race under `-race`). *Do:* a new reader outside the pump reads `r.shown`; a test that
  changes what the scheduler sees replaces `r.shown` (`shownAs`), once the worker said its session. *Where:*
  `internal/harness/harness.go`.
- **`jq` is slow in test fakes.** jq 1.6 costs ~57 ms a run: a fake `gh`/`glab` that runs it per call made a test
  last 1.5 s (W148). *Do:* run jq once per command over all its data files (`input_filename` before each line) and
  cache the answers, or answer in Go with no jq at all. *Where:* `internal/harness/babysit_pr_test.go`,
  `fakeForge` in `internal/harness/babysit_test.go`.
- **French outside `locales/fr.json` fails the tests, not the lint.** `TestNoFrench` refuses any French letter in every
  tracked or new file, quoted blocks, plan files and Go test data included (W156, W162). *Sign:* lint green,
  `go tool task test` red on `French (…)`. *Do:* paraphrase in English; to show Unicode passes, use an em dash, not
  a French word; run `go test -tags headless ./locales/` (about 1 s) after writing non-ASCII text. *Where:*
  `locales/french_test.go`. Under a loaded full run it may report ~2 s although it takes 0.6 s alone: rerun it alone.
- **protojson output depends on the binary.** protojson adds a space after a colon, or not, from a hash of the
  running binary: a test binary built in another folder (Djinn's integration worktree) prints `"comments":  [`
  where yours printed `"comments": [` (W187). *Sign:* a test on JSON text green in your worktree, red in the push
  check. *Do:* print through `indentedJSON` (compact protojson, then `json.Indent`), or compare parsed JSON;
  reproduce by appending a few bytes to a `go test -c` binary. *Where:* `internal/cli/cli.go`.
- **A deadline on a watch ends it on the server first.** connect sends a context's deadline to the server, whose
  handler then ends the stream cleanly a moment before the client's own timer fires: a test that checks `ctx.Err()`
  after a watch with `context.WithTimeout` reads nil about one time in three (W201, `TestTaskWatchLastReturns`).
  *Sign:* a watch that "returned before the task ended" with no stream error, the test as long as its timeout. *Do:*
  `context.WithCancel` and `time.AfterFunc(d, cancel)`: a cancel stays on the client. *Where:* `internal/harness`.
- **A test goroutine that outlives its test kills the package.** A tick that called `t.Error` after the test returned
  hit a closed store, then panicked ("Log in goroutine after … has completed"), ending the test binary (W530,
  `TestLeadWatchSettleIsCapped`). *Sign:* `FAIL <package>` with no `--- FAIL` line; slowtests names the tests left
  running and prints the raw output. *Do:* wait for every goroutine that calls `t.*` before the test returns;
  reproduce with `go test -c`, then `-test.run X -test.count=150 -test.cpu=1`. A provider hook in harness tests runs
  on the harness goroutine: return an error there, never `t.Fatal` (`Harness.Close` then hangs to the package
  timeout).
- **A process a task's end stops can be gone before the test looks at it.** A done or failed task ends at once, and
  stops what its worker left (stopLeft): a test that polled `/proc/PID/environ` of such a job raced that end (W135,
  `TestTaskEndStopsWhatItsWorkerStarted`). *Do:* let the process write what the test checks to a file before its
  PID, and read that file. *Where:* `internal/harness/leftover_linux_test.go`.
- **`TestUpdateInstallsABuild` races under `-race`, before your change.** A test variable is written while Update's
  goroutine reads it; `test-go` and `test-pkg` run without `-race` and pass. Still true today. *Do:* run
  `go tool task test-race -- -skip TestUpdateInstallsABuild ./internal/ui`, and do not take it for your regression.
  *Where:* `internal/ui/ui_test.go`.
- **Go tests have no git identity.** `testx.Main`/`testx.Guard` hide the machine's (`HideGitIdentity`), as on a CI
  runner (W2). *Sign:* "empty ident name" on a commit in a test. *Do:* set a repo-local `user.name`/`user.email` in
  any test repository that commits or where Djinn merges (`gitRepo` does; worktrees share it). *Where:*
  `internal/testx/testx.go`, `internal/harness/worktree_test.go`.
- **Local git is older than CI's.** Git 2.34 resolves `refs/tags/local-g<hex>` as a describe name; 2.55 (CI runners)
  does not. *Sign:* a test around refs passes locally, fails on CI. *Do:* suspect the git version first.
- **Tests that read the home folder need a fake one.** Code that calls `os.UserHomeDir` (the plan scrubber turns the
  home into `~`) sees the CI runner's home, `C:\Users\runneradmin` whose short name is `RUNNER~1` (W494). *Do:*
  `t.Setenv("HOME", …)` and `t.Setenv("USERPROFILE", …)` (Windows reads only the latter), with a name no machine has.
  To run a test binary under another home, build it (`go test -c -tags headless -o /tmp/x.test ./internal/plan`)
  and run that: `go test` under a fake `HOME` fails (relative GOMODCACHE) and leaves a telemetry folder in the
  worktree. *Where:* `TestScrubberShortNames`, `internal/plan/exchange_test.go`.
- **CI builds a stamped version, a worktree builds `dev`.** CI's checkout has no tags: `go build ./cmd/djinn` gets a
  pseudo-version `v0.0.0-<time>-<sha>`, while a worktree's build is `dev` and has no updater. Code keyed on the
  version runs on CI only: the e2e djinn once offered a release whose banner covered a slider (W8). *Do:* test
  builds look for no release (`stampedTestBuild`, `cmd/djinn/release.go`); reproduce with
  `-ldflags "-X github.com/empowill/djinn/internal/ui.isTestBuild=true -X main.version=v0.0.0-20261012122000-97dd90d8abcd"`
  (check with `go version -m`).
- **The harness's run copy of a task overwrites fields stored while it ran.** `writeAs` and the sampler `Put` the
  run's `r.task`, taken when the worker started; `Task.comments` stored meanwhile were erased at the run's end (W7,
  W471). *Sign:* a field set by another call is gone after the worker ends; tests that read only the call's response
  pass. *Do:* a Task field written from outside the run goes through `carry(tx, t)`, and so does any new writer of
  `r.task`; its test reads the task after the worker ends. *Where:* `internal/harness/harness.go`,
  `internal/harness/measure.go`.
- **Harness integration tests: the clock does not move inside one pass.** In `in.pass`, a push records `now`, and
  `committedSince` keeps only work committed strictly after it: a task merged later in the same pass is invisible to
  the push rule, so a "must not push" case placed after a push passes vacuously (W153). *Do:* put it before the
  push, and run a new case once against the reverted fix. Each merged task costs ~0.3 s: keep merges few and move
  edge cases to unit tests (`TestPushDue`). Test repositories with a remote set `maintenance.auto false` and
  `gc.auto 0`. *Where:* `internal/harness/integrate_test.go`, `internal/harness/push.go`.
- **Harness integration tests: pushes run apart from the pass.** Since W180 a push due starts in a goroutine of its
  own (`pushApart`); `in.pass` waits for it (`waitPushes`), `in.passApart` does not. *Sign:* a test that calls
  `pushIfDue` or `pushPass` itself sees no push; a test that waits for a task to start sees it start before the push
  checks end, so the held push starts no correction yet (`workLeft`). *Do:* call `in.h.waitPushes()` after them; hold the
  push check with `in.testWait` to order a scheduler step before it. *Where:* `internal/harness/push_apart.go`.
- **A done fake worker now tells the lead, and may babysit.** Since W492 a work task done reaches the lead once: its
  MERGED line, or one ENDED (`tellDone`, `tellNothingMerged`); since W498 a project with no checks integrates into a
  wish's own branch, so such tests see MERGED, not ENDED; since W499 a task ending done in a project with a babysit
  skill looks for its PR (forge calls through `WithCommands`). *Do:* count lead events with that in mind, fake the
  forge with `fakeForge`, and record `main` on the wish after `git branch -M main` (`uncheckedDew`: `twoProjects`
  records the init branch, `master` on some machines). *Where:* `internal/harness/refusals.go`,
  `internal/harness/integrate.go`, `internal/harness/babysit.go`.
- **Lead digest lines are cut at 160 characters.** Each queued `LeadEvent` is printed cut to `digestLineMax`; two
  UUIDs and two commands overflow, and the lead loses the second (W24). *Do:* put the command that matters first,
  test the length (`plan.DigestKeeps`). Events are queued with `Notify` after the change commits, in their own
  transaction: a test reading `store.OnCommit` for "committed together" keeps the first commit. *Where:*
  `internal/plan/leadwatch.go`.
- **A watcher repeating itself wakes no one.** A paragraph equal to the last one (or, first after a start, to the
  task's `last_line`) is dropped. *Sign:* a check that fails again on a new push goes unreported. *Do:* make each
  paragraph name what it is about (head commit, job link). *Where:* `internal/harness/watch.go`.
- **The command line fills a field from one input only through a oneof of scalars.** A message member in that oneof
  silently breaks the command (help says "Expects an RFC 3339 time"); and any string field named `file`, `path`,
  `directory` (or `*_file`…) is made absolute from the caller's folder (W9). *Do:* add a scalar member with a
  validate pattern; check `djinn <cmd> --help` after adding a request field. *Where:* `ref` and `onMachine` in
  `internal/cli/args.go`.
- **A test that pushes must be a Go test.** In a worker, Bash refuses any command holding `git push`, and the
  worker's git config makes pushes from the project's repository and worktrees fail (W281); other repositories push
  fine. *Do:* prove push behaviour with a temporary repository in a Go test. *Where:* `internal/harness/guard.go`.

## Interface tests

- **A copy change breaks tests that quote it, HTML-escaped.** `tests/screens.test.mjs` matches rendered HTML, where
  an apostrophe is `&#x27;` (W457). *Do:* after editing `locales/en.json`, grep `e2e/`, `tests/` and Go tests for a
  fragment of the old text without its quote, and run `go tool task test-ui`.
- **A CSS rule no component uses fails `test-ui`.** "Classes no component uses: remove their rules"; lint and e2e
  pass regardless (W17). *Do:* drop a component's rules with the component. *Where:* `tests/styles.test.cjs`.
- **`node --test tests/data-store.test.mjs` never ends, silently.** A store retry loop that never stops keeps it
  alive: the fake server lacks some services. *Do:* a retry skips unimplemented calls and stops after the store's
  stop. *Where:* `src/data/store.ts`.

## End-to-end specs

- **All specs share one djinn.** `e2e/global-setup.ts` starts one `djinn up --browser`; specs leave wishes and
  terminals behind (W32). *Sign:* a spec passes alone and fails in the full run. *Do:* reproduce with the spec
  before it (`npx playwright test --config e2e/playwright.config.ts e2e/decision-log.spec.ts e2e/<spec>`), and make
  the spec create its own state (`djinn wish make --json` through `bin/djinn-e2e`).
- **A reload gets the last minute's show.** `ui.Service` replays the last `Present` (wish and terminal) to a window
  that starts watching within a minute; since W501 not one whose wish was deleted, but another spec's live wish
  still lands (W477). *Do:* `djinn open djinn://wish/<id>` before `page.goto`; to switch themes, set
  `localStorage djinn.theme` and `documentElement.dataset.theme` in one `page.evaluate` and await the
  `CSSTransition`s' `finished`, each with `.catch(() => {})`: a transition another one replaces is cancelled, its
  `finished` rejects, and the spec fails on "AbortError: The user aborted a request" (W263, `tilasms.spec.ts`). On a
  wrong page, read the error context's headings first ("Flight plan" has an attention bar and a Tasks tab too).
  *Where:* `replay` in `internal/ui/ui.go`.
- **Live comes before the wishes are loaded.** The status bar says Live once the watch answers; the store's `loaded`
  follows 30–200 ms later, and the main view is an empty `.wish-view[aria-busy]` until then (W496). *Do:* wait on
  the element you act on (`toBeVisible`, `toBeFocused`), never on Live; to catch a remount, record elements with a
  MutationObserver in `addInitScript`. *Where:* `src/wish-app.tsx`.
- **Playwright's trace makes the page look slow.** With `trace: "retain-on-failure"` every action snapshots the DOM:
  ~800 ms a step on a big diff, while the window rendered in ~230 ms (W8). *Do:* measure a budget inside one
  `page.evaluate` (act and poll with `requestAnimationFrame` there); profile with a CDP session
  (`Profiler.start/stop`) before blaming the interface. *Where:* `e2e/diff-chunks.spec.ts`.
- **The terminal panel covers the lower half.** `page.mouse` does not scroll: `scrollIntoView` first, and drag
  across `getClientRects()` (a wrapped span's box centre falls between lines). For screenshots, set a tall viewport
  (1366x2400) or the shot is cut (W497).
- **The e2e djinn has no git identity.** `GIT_CONFIG_GLOBAL` points at a file without one, as on a CI runner (W5).
  *Sign:* a task's integration stays PENDING with "git has no identity". *Do:* a spec whose task Djinn merges sets
  `user.name`/`user.email` in its project repository. *Where:* `e2e/global-setup.ts`, `noIdentity` in
  `internal/harness/worktree.go`.
- **With no agent ready, a setup dialog covers the window.** A CI runner has no claude, codex or agy; the developer's
  machine has one, so it never shows there (W487). *Sign:* "modal-backdrop intercepts pointer events", on CI only.
  *Do:* a spec that starts its own `djinn up` passes `withAgents(process.env)`; reproduce with
  `env -i HOME=$(mktemp -d) PATH=<node, go, /usr/bin, /bin> PLAYWRIGHT_BROWSERS_PATH=~/.cache/ms-playwright npx playwright test …`.
  *Where:* `e2e/agents.ts`.
- **A time budget measures the machine as much as Djinn.** The cases tagged `@budget` (the Grimoire's scroll, a
  terminal key, a typed note, a diff's chunks) fail when other workers load the machine, whatever the change (W177,
  W181): the push check leaves them out. *Do:* run them on a quiet machine, `go tool task e2e -- --grep @budget`, or
  with their spec named; a red one that passes alone on a quiet machine was the load.
- **A djinn under pressure starts no worker.** Over the notch's CPU or memory pressure, a djinn holds every new worker
  and gate ("the machine is under pressure" in the task's wait reason): the fake tasks of a spec stayed pending past
  its wait while other workers loaded the machine (W181). The e2e's djinns and `djinn up` in the Go tests read the
  machine calm (`machine.Calm`, `DJINN_TEST_MACHINE=calm` on a test build). *Do:* a spec or test that starts a djinn
  of its own keeps it; one that tests the pressure gives a fake reading instead.
- **Two Playwright runs in one worktree erase each other's traces.** `go tool task test` runs the e2e too; beside
  `go tool task e2e` (or a second agent in the same worktree) both write `test-results/`. *Sign:*
  `ENOENT … .playwright-artifacts-0/traces/*.trace`. *Do:* one run at a time.
- **The binary embeds `dist/`.** `go build ./cmd/djinn` serves whatever `dist/` holds: build the interface first
  (`go tool task ui`, which the tasks do), or the e2e binary serves a blank page.
- **A linked `node_modules` gets emptied.** In a scratch worktree, the tasks run `npm ci`, which empties a symlinked
  `node_modules` (yours) and installs a real one there (W29). *Do:* run `npm ci` in the scratch worktree itself (or
  `cp -al`), and count `ls node_modules | wc -l` in yours afterwards.

## Windows and macOS

- **Windows test time follows the processes a test spawns.** ~0.05 s a process on a normal runner, ~0.11 s on a slow
  one (runner speed varies 2x between runs), `sh` more; a test under 1 s on Linux can pass 15 s there (W523).
  *Do:* count them on Linux: build the test binary, run it under
  `strace -f -qq -e trace=execve -e signal=none -o out.s`, then `grep -c execve out.s`; cut spawns (`passMemo` keeps
  git answers for an integration pass). Mark tests that only check Djinn's logic portable (`testx.Portable`,
  [CONTRIBUTING.md](../CONTRIBUTING.md)).
- **A file without build tag may use helpers that do not build on Windows.** `prompt_test.go` used helpers of a
  `//go:build !windows` test file: the Windows test package did not compile, Linux and the lint saw nothing (W484).
  *Do:* after a merge or a test move, run `GOOS=windows go vet -tags headless ./...` and
  `GOOS=darwin go vet -tags headless ./...` (without `headless`, darwin fails in Wails' cgo: only macOS CI proves
  the window build).
- **On macOS a priority set after `Start` can be lost.** `/bin/sh` execs at once and xnu's exec copies the process
  while `setpriority` lands: the child kept the parent's nice (macOS CI, Linux cannot reproduce it). *Do:* set a
  child's priority before exec (`nice -n`, `lowCommand`); never combine `nice` with a `setpriority` on the same
  process. *Where:* `internal/harness/process_unix.go`.
- **On macOS `t.TempDir()` is not the path Djinn stores.** It gives `/var/folders/…`, a link to `/private/var/…`;
  on Windows CI it gives `C:\Users\RUNNER~1\…`, a short name. A project's folder is stored with its links resolved
  (`ProjectService.Add`), and a shell that finds `$PWD` wrong prints the resolved path from `pwd` (W159: a test
  compared a notice's file to the raw temporary folder; a serve's `pwd` printed `/private/var` until `startCommand`
  set `PWD`; W208: git names a worktree by its resolved path; an e2e spec's `realpathSync` keeps `RUNNER~1` where
  Djinn stores `runneradmin`). *Sign:* passes on Linux, fails on macOS and Windows with the same path written two
  ways. *Do:* compare to `project.GetDirectory()`, or the path Djinn gave; resolve a folder you make with
  `filepath.EvalSymlinks`; to reproduce on Linux, set `TMPDIR` to a symbolic link (only `/private/` gets the macOS
  treatment in `aliases`). In e2e, never put a path in a CSS attribute selector: a Windows path's backslashes are CSS
  escapes (`\A` is a newline), it never matches; use `getByTitle(…, { exact: true })`. *Where:*
  `internal/plan/plan.go`, `internal/plan/exchange.go`.

## Processes

- **A process just started reads 0 memory.** `/proc/<pid>/stat` rss is 0 right after `Start` about half the time
  (kernel 6.8), and always for a child stopped at exec with `SysProcAttr{Ptrace: true}` (a deterministic way to
  reproduce it; needs `runtime.LockOSThread`). `ReadWorker` returns an empty `Group` then (W537). *Do:* loop over
  fresh processes to reproduce races: `-count=N` in one process hides them behind the 1 s listing cache. *Where:*
  `internal/machine/group.go`.
- **A `djinn-sentinel` shell per running harness is expected.** Since W514 Djinn records each child under
  `$DJINN_HOME/run/`, stops a dead djinn's leftovers at start, and a `/bin/sh … djinn-sentinel` kills the groups
  when Djinn dies: it is not an orphan. *Where:* `internal/harness/orphans_unix.go`.
- **What a worker starts dies with it.** Since W68 the end of any process Djinn starts (a worker, each run of a
  watcher's command) stops what it left: its group, its scope's cgroup, and every process whose environment holds its
  `DJINN_MARK` (a `setsid` daemon). A test or a watcher that expects a background process to outlive its command
  fails: nothing may outlive it until `djinn serve` (W63) lands. On Linux the process stays a zombie (waitid
  WNOWAIT) until its leftovers are gone, so its group ID cannot be reused meanwhile. *Where:* `internal/harness/process_unix.go` (stopLeft), `orphans_linux.go` (procsOf).
- **A git that changes a repository must not stop half way.** A stop cancels the harness's context; a git killed
  mid-reset left its `index.lock` and every later reset there failed, and `moveBranch` moved a branch but not the
  checkout that follows it (W52). Since W84 `gitx.Command` stops git with SIGTERM (SIGKILL `WaitDelay` later), and
  steps that change a repository (merge and its abort, reset, worktree add, a branch move and its put back) run under
  `gitx.Uncut`, `Finish` at most past the stop. *Do:* wrap a new sequence of changing gits in `Uncut`, after checking
  `ctx.Err()`; reads stay cancellable. To hold a git mid-checkout in a test, `testx.HoldCheckout`. *Where:*
  `internal/gitx/gitx.go`, `internal/harness/locks.go`, `internal/harness/cut.go` (what the restart line says the stop
  cut).
- **Finding a freeze in a goroutine dump.** `djinn debug dump` (or `kill -USR2` on djinn up, found by its socket,
  not `pgrep`: from the desktop menu it runs as `djinn open`) writes the goroutines to the log folder. Every waiter
  shows `lockSlow(0xADDR)`; ADDR minus the base address of a `Harness` method's receiver is the field offset, which
  names the mutex. No holder outside `Lock` means a self-deadlock: a stack where one function appears twice on its
  way to `Lock`. `logx.Lock` logs "lock waited" for the scheduler's lock. *Where:* `cmd/djinn/debug.go`,
  `internal/logx/logx.go`.
- **Profile a copy, never the real data.** `djinn up --pprof` (or `DJINN_PPROF=on`) serves `/debug/pprof/`. Copy the
  database with sqlite's backup API, not `cp` (WAL); in the copy, set every project `Git=false` and point its
  `Directory` at a missing folder (Git=false alone still let the push checks make a worktree in the real repository,
  W504); run with its own `DJINN_HOME`, a fake agent first on `PATH` and `DJINN_QUESTION_WORKERS=off`.

## Agents' permissions

- **Claude refuses a `#` line inside a quoted argument, whatever Djinn allows.** Claude Code's "newline-hash" check
  refuses a Bash call where an argument holds a newline then `#`, indented or not; in `dontAsk` it reads "Permission
  to use Bash has been denied because Claude Code is running in don't ask mode", as a missing rule does, while
  `commandAllowed` says yes. A question's shape puts a `# …` comment above each command: W80 of wish Dew 13 oct could
  not revise Q32 (W153). *Do:* pass multi-line text through a file the agent writes (an investigator's
  `--context-file`, `questionContextFile`); try a rule for real with `claude -p --permission-mode dontAsk --settings
  …`, a fake `djinn` first on `PATH`, a scratch folder and Haiku: under a cent. *Where:*
  `internal/harness/question.go`, `internal/harness/question_context_test.go`.

## Store and sqlite

- **`Put` without `Journal` stores nothing.** `store.Tx` refuses a `Put` before `tx.Journal`: an ignored error there
  means nothing was written. *Where:* `internal/store`.
- **Read the real database read-only.** No `sqlite3` on the machine: a throwaway Go program with `modernc.org/sqlite`
  and `file:<path>?mode=ro`, decoding payloads with `gen/go/plan/v1`. The `command` table (the journal: seq, actor,
  at, method, request) tells a freeze (calls journaled, follow-ups missing) from a crash (`harness/recover` at the
  restart, runs `interrupted`) (W536).

## The window

- **A Get read beside a Watch that sends only changes.** The server's stream sends what changed since its own last
  message, within a beat: a value set and set back inside the beat sends nothing. A Get answered meanwhile shows the
  value in between, and kept, it stays on screen: the load gauge showed minimal for good while the notch was medium
  again (W248, operating-load.spec in a full e2e run only). *Do:* once the stream has spoken, drop a Get answered
  after it. *Where:* `LoadManager.start` in `src/operating-load.tsx`.
- **Chromium hides WebKit bugs.** The window is WebKitGTK on Linux (`wails://localhost`); Playwright here runs
  Chromium. WebKit reads CSP `'self'` in a sandboxed frame as the opaque origin: tilasms rendered in Chromium, not in
  the window, until the server's origin was named (W188). *Do:* probe the real engine with `python3 -I`, `gi`
  WebKit2 4.0 in a `Gtk.OffscreenWindow`, `register_uri_scheme("wails", …)` serving the CSP header, console messages
  to stdout, `get_snapshot`. For the native window itself, a throwaway test in `e2e/native`
  (`go tool task e2e-native -- -run TestX -v`). *Where:* `tilasmPolicy` in `internal/plan/tilasm_files.go`.
- **Some animations burn a core in WebKit.** At 4K, an infinite `box-shadow` animation cost ~40 % of a core, the same
  pulse by `transform`/`opacity` ~0; SVG child animations are not composited (~40 %) (W513). *Do:* animate
  `transform` and `opacity` only; measure in WebKit, not Chromium.
- **A store read that never returns freezes the window's writes.** Each write ends with `store.changed`, which reads
  `WishService/List` and `LeadService/Get` right after. *Sign:* in `djinn.log`, writes with no `WishService/List`
  after them for minutes: `flush()` waits behind a hung read. Every read has a 30 s `timeoutMs` since W543. *Where:*
  `src/data/store.ts`.
- **A full re-read of tasks drops the pages loaded later.** A reconnect, or a change without an entity, puts a wish's
  tasks back to the first page of 50, dropping what `loadMore` loaded. *Where:* `src/data/store.ts`.
- **The window approves nothing.** Its mark buttons toggle only the read mark; `MARK_KIND_APPROVED` reaches
  `MarkService.Put` only from `djinn mark put`. *Do:* test an approval through `MarkService`, not an e2e click.
  *Where:* `src/marks.tsx`.
- **Screen grabs of the native window are blank.** `xwd` reads an empty surface from WebKitGTK's GPU compositing:
  run djinn with `WEBKIT_DISABLE_COMPOSITING_MODE=1`. Setting `HTTP_PROXY` keeps the window from reaching Live (use
  `HTTPS_PROXY=http://127.0.0.1:9` to block GitHub instead).

## Releases

- **Publishing a release with a new tag fires two runs.** GitHub sends `release: published` and a push of the tag;
  `release.yml` has a concurrency group per tag and skips a release whose assets already hold `SHA256SUMS`.
- **A tag cut from main has no `dist/`.** `go install …@vX.Y.Z` then fails (`pattern all:dist: no matching files
  found`). *Do:* `go tool task release VERSION=…` and push its tag: it carries `dist/`.
- **A release page renders every newline.** `docs/releases/*.md` keep one line per paragraph; `TestReleaseNotes`
  (`tools/releasepack`) checks their shape.
- **Proving an update without GitHub.** `scripts/install.sh` installs from `DJINN_RELEASES=file://<dir>`; build an
  older binary with `-X main.version=v0.0.0-alpha -X main.releaseAPI=<fake>` and serve a fake
  `/repos/…/releases`; `djinn update` needs a running djinn, and `--yes` outside a terminal.

// The update banner (src/update-banner.tsx): the release, a build to install, how an install went. Run with `go tool
// task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { screens } from "./screens.mjs";

const { s, h } = await screens();

test("the update banner links the release notes of a newer release, and only a web link", () => {
  const banner = (state, phase = { kind: "idle" }) =>
    s.renderToStaticMarkup(
      h(s.UpdateBannerView, {
        state: { current: "v1.0.0", notResumed: [], ...state },
        phase,
        dismissed: false,
        onInstall() {},
        onDismiss() {},
        onNotes() {},
      }),
    );
  const release = banner({
    ready: "v2.0.0",
    notesUrl: "https://github.com/Empowill/Djinn/releases/tag/v2.0.0",
  });
  assert.match(release, /A new version of Djinn is ready/);
  assert.match(
    release,
    /<a href="https:\/\/github.com\/Empowill\/Djinn\/releases\/tag\/v2.0.0" target="_blank" rel="noopener noreferrer">Release notes<\/a>/,
  );
  // The djinn's mark heads it; the brass Update sits beside the title, the notes a quiet link after it, then the ×.
  assert.match(
    release,
    /^<div class="update-banner" role="status"><section class="update-banner-part"><div class="update-banner-head"><div class="update-banner-title" title="v1.0.0 → v2.0.0"><span class="brand small"><span class="brand-mark"><img/,
  );
  assert.match(
    release,
    /A new version of Djinn is ready<\/span><\/div><div class="update-banner-actions"><button type="button" class="update-banner-primary">Update<\/button><a [^>]*>Release notes<\/a><button type="button" class="update-banner-close" aria-label="Dismiss" title="Dismiss">×<\/button><\/div>/,
  );
  // While it restarts, the notes stay; the button goes.
  const restarting = banner(
    { ready: "v2.0.0", notesUrl: "https://example.com/v2.0.0" },
    { kind: "restarting" },
  );
  assert.match(restarting, /Restarting…/);
  assert.match(restarting, /Release notes/);
  assert.doesNotMatch(restarting, /<button/);
  // A local install has no notes; a link that is not a web one is not shown.
  assert.doesNotMatch(banner({ ready: "local-abc", notesUrl: "" }), /<a /);
  assert.doesNotMatch(
    banner({ ready: "v2.0.0", notesUrl: "javascript:alert(1)" }),
    /<a /,
  );
  // After a restart, the terminals that did not start again: the mark heads them, Dismiss in violet beside the title.
  const lost = banner({ ready: "", notesUrl: "", notResumed: ["npm run dev"] });
  assert.match(
    lost,
    /<span class="brand small">.*<div class="update-banner-actions"><button type="button" class="update-banner-dismiss">Dismiss<\/button><\/div><\/div><div class="update-banner-body"><ul class="update-banner-terminals"><li>npm run dev<\/li>/,
  );
  // Nothing waits: no banner, whatever the notes.
  assert.equal(banner({ ready: "", notesUrl: "https://example.com" }), "");
});

test("the update banner proposes to install a build committed, on one line, what changed unfolded on demand", () => {
  const build = {
    wishTitle: "Run Djinn on itself",
    project: "djinn",
    branch: "feat/wails-go",
    sha: "1a2b3c4d5e6f",
    tasks: ["W5", "W6"],
    changes: ["Work of W6", "Work of W5"],
    summaries: [
      {
        code: "W5",
        title: "Work of W5",
        summary: "To check: the banner shows the build.",
      },
      {
        code: "W6",
        title: "Work of W6",
        summary: "Done: W6 finished.",
      },
    ],
    base: "",
    running: true,
    changesCount: 0,
    lacks: [],
    lacksCount: 0,
  };
  const banner = (
    phase = { kind: "idle" },
    dismissedBuilds = [],
    installing,
    open = [],
  ) =>
    s.renderToStaticMarkup(
      h(s.UpdateBannerView, {
        state: {
          current: "v1",
          ready: "",
          notResumed: [],
          notesUrl: "",
          builds: [build],
          installing,
        },
        phase,
        dismissed: false,
        dismissedBuilds,
        open,
        onInstall() {},
        onDismiss() {},
        onNotes() {},
      }),
    );
  const html = banner();
  // One line: the build named by its commit, branch and wish, the tasks it brings; its actions beside it, the
  // details folded.
  assert.match(
    html,
    /<span class="update-banner-text">Build <code>1a2b3c4d<\/code> of feat\/wails-go<span class="update-banner-quiet"> · Run Djinn on itself<\/span><span class="update-banner-quiet"> — W5, W6<\/span><\/span>/,
  );
  assert.match(
    html,
    /title="1a2b3c4d · feat\/wails-go · Run Djinn on itself · djinn\nW5, W6"/,
  );
  assert.match(html, /<span class="brand small"><span class="brand-mark"><img/);
  assert.match(
    html,
    /<div class="update-banner-actions"><button type="button" class="update-banner-primary">Install and restart<\/button><button type="button" class="update-banner-more" aria-expanded="false">Details<\/button><button type="button" class="update-banner-close" aria-label="Dismiss" title="Dismiss">×<\/button><\/div><\/div><\/section>/,
  );
  assert.doesNotMatch(html, /update-banner-body/);
  // Unfolded: what changed, each task with its worker's summary folded, then the commits folded.
  const unfolded = banner({ kind: "idle" }, [], undefined, [build.sha]);
  assert.match(
    unfolded,
    /aria-expanded="true">Less<\/button>.*<div class="update-banner-body"><strong>What changed<\/strong>/,
  );
  assert.match(
    unfolded,
    /<ul class="update-banner-tasks"><li><details class="update-banner-task"><summary><code>W5<\/code> Work of W5<\/summary><div class="update-banner-summary">To check: the banner shows the build\.<\/div><\/details><\/li><li><details class="update-banner-task"><summary><code>W6<\/code> Work of W6<\/summary><div class="update-banner-summary">Done: W6 finished\.<\/div><\/details><\/li><\/ul>/,
  );
  assert.match(
    unfolded,
    /<details class="update-banner-commits"><summary>commits<\/summary><ul><li>Work of W6<\/li><li>Work of W5<\/li><\/ul><\/details>/,
  );
  // While it installs, its buttons go; its details stay.
  const installing = banner({ kind: "installing", sha: build.sha });
  assert.match(installing, /Installing…/);
  assert.doesNotMatch(installing, /update-banner-primary|update-banner-close/);
  // Then it says where the install stands, as Djinn tells it, in this window or another: what it waits for, that it
  // builds, that Djinn restarts.
  const step = (step, waiting = "") =>
    banner({ kind: "idle" }, [], { sha: build.sha, step, waiting });
  const waits = step(s.InstallStep.WAITING, "gate install: held by W9 (Other)");
  assert.match(waits, /Waiting — gate install: held by W9 \(Other\)/);
  assert.doesNotMatch(waits, /update-banner-primary|update-banner-close/);
  assert.match(step(s.InstallStep.PREPARING), /Preparing the build…/);
  assert.match(step(s.InstallStep.BUILDING), /Building…/);
  assert.match(step(s.InstallStep.RESTARTING), /Restarting…/);
  // Dismissed, that build no longer shows.
  assert.equal(banner({ kind: "idle" }, [build.sha]), "");
  // A build that installed no newer Djinn says it is installed, and restarts nothing.
  assert.match(
    s.renderToStaticMarkup(
      h(s.UpdateBannerView, {
        state: {
          current: "v1",
          ready: "",
          notResumed: [],
          notesUrl: "",
          builds: [],
        },
        phase: { kind: "installed", sha: build.sha },
        dismissed: false,
        onInstall() {},
        onDismiss() {},
        onNotes() {},
      }),
    ),
    /Installed 1a2b3c4d/,
  );
});

test("the update banner names the build against the running Djinn: what it adds, what it lacks, the other branches folded", () => {
  const proposal = (branch, sha, wishTitle, extra = {}) => ({
    wishTitle,
    project: "djinn",
    branch,
    sha,
    tasks: [],
    changes: [],
    summaries: [],
    base: "abcdef0123",
    running: false,
    changesCount: 0,
    lacks: [],
    lacksCount: 0,
    ...extra,
  });
  const mine = proposal(
    "feat/wails-go",
    "aaaa1111bbbb",
    "Run Djinn on itself",
    {
      running: true,
      tasks: ["W466", "W467"],
      summaries: [
        { code: "W466", title: "The tasks tab", summary: "" },
        { code: "W467", title: "The diff", summary: "" },
      ],
      changes: ["ui: the diff", "ui: the tasks tab"],
      changesCount: 2,
    },
  );
  const side = proposal(
    "feat/clement-port",
    "e60f8fc00000",
    "Clément's branches",
    {
      tasks: ["W2"],
      summaries: [{ code: "W2", title: "Port Clément's work", summary: "" }],
      changes: ["port: Clément's work"],
      changesCount: 1,
      lacks: ["ui: the tasks tab", "ui: the diff", "harness: azimas"],
      lacksCount: 3,
    },
  );
  const other = proposal("feat/x", "cccc2222dddd", "", {
    changes: ["fix"],
    changesCount: 1,
  });
  const banner = (state, { dismissedBuilds = [], open = [] } = {}) =>
    s.renderToStaticMarkup(
      h(s.UpdateBannerView, {
        state: {
          current: "local-g abcdef0",
          ready: "",
          notResumed: [],
          notesUrl: "",
          builds: [],
          ...state,
        },
        phase: { kind: "idle" },
        dismissed: false,
        dismissedBuilds,
        open,
        onInstall() {},
        onDismiss() {},
        onNotes() {},
      }),
    );
  // The running Djinn's branch first, on its line; the other branches' builds folded under one line naming them.
  const html = banner({ builds: [mine, side, other] });
  assert.match(
    html,
    /Build <code>aaaa1111<\/code> of feat\/wails-go<span class="update-banner-quiet"> · Run Djinn on itself<\/span><span class="update-banner-quiet"> — adds 2 tasks<\/span>/,
  );
  assert.match(
    html,
    /Builds of 2 other branches: feat\/clement-port, feat\/x<\/span><\/div><div class="update-banner-actions"><button type="button" class="update-banner-more" aria-expanded="false">Details<\/button><\/div>/,
  );
  assert.doesNotMatch(html, /e60f8fc0/);
  assert.equal(html.match(/<img/g).length, 1);
  // Unfolded, each other branch on its line, with its own install and ×: what it adds, and that it is older than the
  // running Djinn.
  const others = banner({ builds: [mine, side, other] }, { open: ["others"] });
  assert.match(
    others,
    /<div class="update-banner-others"><section class="update-banner-part"><div class="update-banner-head"><div class="update-banner-title" title="e60f8fc0 · feat\/clement-port · Clément&#x27;s branches · djinn\nadds 1 task · older than the running Djinn on 3 commits"><span class="update-banner-text">Build <code>e60f8fc0<\/code> of feat\/clement-port<span class="update-banner-quiet"> · Clément&#x27;s branches<\/span><span class="update-banner-quiet"> — adds 1 task · older than the running Djinn on 3 commits<\/span><\/span><\/div><div class="update-banner-actions"><button type="button" class="update-banner-primary">Install and restart<\/button>/,
  );
  assert.match(others, /— adds 1 commit<\/span>/);
  assert.equal(others.match(/class="update-banner-close"/g).length, 3);
  // A build's details: the tasks merged since the running Djinn, the commits folded, and those it lacks folded.
  const details = banner({ builds: [side] }, { open: ["others", side.sha] });
  assert.match(
    details,
    /<strong>Since the running Djinn \(abcdef01\)<\/strong><ul class="update-banner-tasks"><li><div class="update-banner-task-line"><code>W2<\/code> Port Clément&#x27;s work<\/div><\/li><\/ul><details class="update-banner-commits"><summary>1 commit<\/summary><ul><li>port: Clément&#x27;s work<\/li><\/ul><\/details><details class="update-banner-commits update-banner-lacks"><summary>Older than the running Djinn on 3 commits<\/summary><ul><li>ui: the tasks tab<\/li>/,
  );
  // A dismissed build goes, the others stay; all dismissed, no banner.
  assert.doesNotMatch(
    banner({ builds: [mine, side] }, { dismissedBuilds: [mine.sha] }),
    /aaaa1111/,
  );
  assert.equal(
    banner({ builds: [mine, side] }, { dismissedBuilds: [mine.sha, side.sha] }),
    "",
  );

  // The newer Djinn ready is named the same way, with its own ×, dismissed by its version.
  const ready = banner({ ready: "local-ge60f8fc", readyBuild: side });
  assert.match(
    ready,
    /<div class="update-banner-title" title="local-g abcdef0 → local-ge60f8fc\ne60f8fc0 · feat\/clement-port · Clément&#x27;s branches · djinn\nadds 1 task · older than the running Djinn on 3 commits">/,
  );
  assert.match(
    ready,
    /New Djinn ready: <code>e60f8fc0<\/code> of feat\/clement-port<span class="update-banner-quiet"> · Clément&#x27;s branches<\/span><span class="update-banner-quiet"> — adds 1 task · older than the running Djinn on 3 commits<\/span><\/span><\/div><div class="update-banner-actions"><button type="button" class="update-banner-primary">Update<\/button><button type="button" class="update-banner-more" aria-expanded="false">Details<\/button><button type="button" class="update-banner-close" aria-label="Dismiss" title="Dismiss">×<\/button>/,
  );
  assert.equal(s.readyKey("local-ge60f8fc"), "ready:local-ge60f8fc");
  assert.equal(
    banner(
      { ready: "local-ge60f8fc", readyBuild: side },
      { dismissedBuilds: ["ready:local-ge60f8fc"] },
    ),
    "",
  );
  // Another version ready shows again.
  assert.match(
    banner(
      { ready: "local-g0123456", readyBuild: undefined },
      { dismissedBuilds: ["ready:local-ge60f8fc"] },
    ),
    /A new version of Djinn is ready<\/span>/,
  );
});

test("the update banner shows a build of the wish's moved branch as the update, a parallel branch as another (W527)", () => {
  const proposal = (branch, sha, wishTitle, extra = {}) => ({
    wishTitle,
    project: "djinn",
    branch,
    sha,
    tasks: [],
    changes: ["harness: no orphan"],
    summaries: [],
    base: "abcdef0123",
    running: false,
    continues: "",
    changesCount: 1,
    lacks: [],
    lacksCount: 0,
    ...extra,
  });
  // The wish moved its integration from feat/wails-go, which the running Djinn was built from, to feat/robustness,
  // cut at its tip: that build holds the running commit. feat/clement-port's does not.
  const moved = proposal("feat/robustness", "bbbb3333cccc", "Orchestrator", {
    continues: "feat/wails-go",
  });
  const parallel = proposal(
    "feat/clement-port",
    "e60f8fc00000",
    "Clément's branches",
    { lacks: ["ui: the diff"], lacksCount: 1 },
  );
  const html = s.renderToStaticMarkup(
    h(s.UpdateBannerView, {
      state: {
        current: "local-300b7fe",
        ready: "",
        notResumed: [],
        notesUrl: "",
        builds: [moved, parallel],
      },
      phase: { kind: "idle" },
      dismissed: false,
      onInstall() {},
      onDismiss() {},
      onNotes() {},
    }),
  );
  // The moved branch's build heads the banner, with the djinn's mark, as the update of the running line.
  assert.match(
    html,
    /<span class="brand small">.*<span class="update-banner-text">Build <code>bbbb3333<\/code> of feat\/robustness, continuing feat\/wails-go<span class="update-banner-quiet"> · Orchestrator<\/span><span class="update-banner-quiet"> — adds 1 commit<\/span><\/span><\/div><div class="update-banner-actions"><button type="button" class="update-banner-primary">Install and restart<\/button>/,
  );
  // Only the parallel branch is another branch, folded.
  assert.match(html, /A build of another branch: feat\/clement-port<\/span>/);
  assert.doesNotMatch(html, /another branch[^<]*feat\/robustness/);
  assert.doesNotMatch(html, /e60f8fc0/);
  assert.equal(html.match(/<img/g).length, 1);
});

test("the update banner says how an install failed, with its last lines, what installs by itself, and offers the restart of the newer Djinn it installed (W554)", () => {
  const line = {
    wishTitle: "Robustness",
    project: "djinn",
    branch: "feat/robustness",
    sha: "aaaa1111bbbb",
    tasks: [],
    changes: ["ui: the banner"],
    summaries: [],
    base: "abcdef0123",
    running: true,
    continues: "",
    changesCount: 1,
    lacks: [],
    lacksCount: 0,
    auto: true,
  };
  const banner = (state, phase = { kind: "idle" }, open = []) =>
    s.renderToStaticMarkup(
      h(s.UpdateBannerView, {
        state: {
          current: "local-abcdef0",
          ready: "",
          installed: false,
          notResumed: [],
          notesUrl: "",
          builds: [],
          ...state,
        },
        phase,
        dismissed: false,
        open,
        onInstall() {},
        onDismiss() {},
        onNotes() {},
      }),
    );
  const installing = (extra) => ({
    sha: line.sha,
    step: s.InstallStep.BUILDING,
    waiting: "",
    doing: "",
    auto: false,
    error: "",
    log: [],
    ...extra,
  });
  // Past its deadline: why, Try again in place of the spinner, the last lines its command wrote in its details; even
  // while the window still waits for its call's answer.
  const failed = installing({
    step: s.InstallStep.FAILED,
    error:
      "it did not end within 20m0s, at install: go build ./cmd/djinn: Djinn stopped it",
    log: ["task: [ui] npm run build", "task: [install] go build ./cmd/djinn"],
  });
  for (const phase of [
    { kind: "idle" },
    { kind: "installing", sha: line.sha, seen: true },
  ]) {
    const html = banner({ builds: [line], installing: failed }, phase, [
      line.sha,
    ]);
    assert.match(
      html,
      /Build aaaa1111 did not install: it did not end within 20m0s, at install: go build \.\/cmd\/djinn: Djinn stopped it<\/span>/,
    );
    assert.match(
      html,
      /<button type="button" class="update-banner-primary">Try again<\/button>/,
    );
    assert.doesNotMatch(html, /Installing…|Building…/);
    assert.match(
      html,
      /<pre class="update-banner-log">task: \[ui\] npm run build\ntask: \[install\] go build \.\/cmd\/djinn<\/pre>/,
    );
  }
  // A build of the running line installs by itself: it says so, and the step of its command that runs; it waits for
  // a free machine, saying why.
  assert.match(
    banner({
      builds: [line],
      installing: installing({ auto: true, doing: "ui: npm run build" }),
    }),
    /<span>Installing by itself: Building… \(ui: npm run build\)<\/span>/,
  );
  assert.match(
    banner({
      builds: [line],
      installing: installing({
        auto: true,
        step: s.InstallStep.WAITING,
        waiting: "gate test: held by W5",
      }),
    }),
    /<span>Installs by itself once free — gate test: held by W5<\/span>/,
  );
  // Installed: the newer Djinn is ready, with what it adds, and the click only restarts.
  const ready = banner({
    ready: "local-aaaa111",
    installed: true,
    readyBuild: { ...line, auto: false },
  });
  assert.match(
    ready,
    /Djinn local-aaaa111 is ready: <code>aaaa1111<\/code> of feat\/robustness<span class="update-banner-quiet"> · Robustness<\/span><span class="update-banner-quiet"> — adds 1 commit<\/span><\/span><\/div><div class="update-banner-actions"><button type="button" class="update-banner-primary">Restart<\/button>/,
  );
  assert.match(
    banner({ ready: "local-aaaa111", installed: true }),
    /Djinn local-aaaa111 is ready<\/span>.*>Restart<\/button>/,
  );
});

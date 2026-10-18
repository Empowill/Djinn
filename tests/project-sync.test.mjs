// The branches out of sync (src/project-sync.tsx): their lines, pushes and pulls. Run with `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { projectSync, screens } from "./screens.mjs";

const { s, h, live } = await screens();

test("the side panel lists each branch of a project out of sync, one short line each, with a small push button", () => {
  const project = {
    id: "p1",
    name: "lamp",
    directory: "/tmp/lamp",
    git: true,
    syncs: [
      projectSync("feat/x", 3, 1),
      projectSync("feat/clean", 0, 0, { wishTitle: "Gong" }),
      projectSync("djinn/T27-djinn-stays-fast", 2, 0, {
        azimaId: "a27",
        azimaCode: "T27",
        azimaTitle: "Djinn stays fast",
      }),
    ],
  };
  const sidebar = (extra) =>
    s.renderToStaticMarkup(
      h(s.WishSidebar, {
        wishes: [],
        projects: [project],
        selectedWishId: "",
        selectedProjectId: "",
        collapsed: false,
        onSelectWish() {},
        onSelectProject() {},
        onMove() {},
        onNewProject() {},
        onPush() {},
        ...extra,
      }),
    );
  const html = sidebar();
  // The branch first, then its counts with their symbols; never the long sentence nor origin/ in sight.
  assert.match(
    html,
    /<div class="sync-line" title="Integration branch of the wish Lamp\n3 commits ahead of origin\/feat\/x, 1 behind"><span class="sync-branch">feat\/x<\/span><span class="sync-counts"><span class="sync-ahead">↑3<\/span><span class="sync-behind">↓1<\/span><\/span><button type="button" class="icon-button sync-push" title="Push feat\/x" aria-label="Push feat\/x">/,
  );
  assert.match(
    html,
    /<div class="sync-line" title="Branch of the azima T27 Djinn stays fast, wish Lamp\n2 commits ahead of origin\/djinn\/T27-djinn-stays-fast"><span class="sync-branch">djinn\/T27-djinn-stays-fast<\/span><span class="sync-counts"><span class="sync-ahead">↑2<\/span><\/span>/,
  );
  // In sync, a branch does not show.
  assert.doesNotMatch(html, /feat\/clean/);
  assert.equal(html.match(/class="sync-line"/g).length, 2);
  assert.doesNotMatch(html, /sync-fold/);
  assert.doesNotMatch(html, /button accent/);
  assert.doesNotMatch(html, />[^<]*origin\/[^<]*</);

  // Collapsed, or nothing out of sync: no line.
  assert.doesNotMatch(sidebar({ collapsed: true }), /sync-line/);
  assert.doesNotMatch(
    s.renderToStaticMarkup(
      h(s.WishSidebar, {
        wishes: [],
        projects: [{ ...project, syncs: [projectSync("feat/x", 0, 0)] }],
        selectedWishId: "",
        selectedProjectId: "",
        collapsed: false,
        onSelectWish() {},
        onSelectProject() {},
        onMove() {},
        onNewProject() {},
      }),
    ),
    /sync-lines/,
  );

  // Its button pushes that branch of that project.
  const pushed = [];
  const lines = live(s.WishSidebar, {
    wishes: [],
    projects: [project],
    selectedWishId: "",
    selectedProjectId: "",
    collapsed: false,
    onSelectWish() {},
    onSelectProject() {},
    onMove() {},
    onNewProject() {},
    onPush: (id, sync) => pushed.push(`${id} ${sync.wishId} ${sync.branch}`),
  }).one(s.SyncLines);
  lines.onPush(project.syncs[2]);
  assert.deepEqual(pushed, ["p1 w1 djinn/T27-djinn-stays-fast"]);
});

test("more than three branches out of sync fold behind the first two; while one pushes, every button waits and says why", async () => {
  const syncs = [
    projectSync("feat/a", 1, 0),
    projectSync("feat/b", 0, 2),
    projectSync("feat/in-sync", 0, 0),
    projectSync("feat/c", 4, 1),
    projectSync("feat/d", 5, 0),
  ];
  let release;
  const pushed = [];
  const lines = live(s.SyncLines, {
    syncs,
    onPush: (sync) => {
      pushed.push(sync.branch);
      return new Promise((resolve) => {
        release = resolve;
      });
    },
  });
  const branches = () => lines.all(s.SyncLine).map((x) => x.props.sync.branch);
  assert.deepEqual(branches(), ["feat/a", "feat/b"]);
  const fold = lines.one("sync-fold");
  assert.equal(fold["aria-expanded"], false);
  assert.match(lines.html(), /<span>2 more branches<\/span>/);
  fold.onClick({ stopPropagation() {} });
  assert.deepEqual(branches(), ["feat/a", "feat/b", "feat/c", "feat/d"]);
  assert.match(lines.html(), /<span>Fewer branches<\/span>/);

  // A push runs: its own button says so, the others that one runs; none pushes again.
  lines.all(s.SyncLine)[2].props.onPush();
  assert.deepEqual(pushed, ["feat/c"]);
  const busy = lines.all(s.SyncLine).map((x) => x.props.busy);
  assert.deepEqual(busy, [
    "A push is running: wait for it to end",
    "A push is running: wait for it to end",
    "Pushing feat/c…",
    "A push is running: wait for it to end",
  ]);
  lines.all(s.SyncLine)[0].props.onPush();
  assert.deepEqual(pushed, ["feat/c"]);
  release();
  await lines.settle();
  assert.deepEqual(
    lines.all(s.SyncLine).map((x) => x.props.busy),
    ["", "", "", ""],
  );

  // The line itself: its button disabled with the reason as its title.
  const waiting = s.renderToStaticMarkup(
    h(s.SyncLine, {
      sync: syncs[3],
      busy: "A push is running: wait for it to end",
      onPush() {},
    }),
  );
  assert.match(
    waiting,
    /<button type="button" class="icon-button sync-push" disabled="" title="A push is running: wait for it to end" aria-label="A push is running: wait for it to end">/,
  );
  // Three lines never fold.
  assert.doesNotMatch(
    s.renderToStaticMarkup(h(s.SyncLines, { syncs: syncs.slice(0, 4) })),
    /sync-fold/,
  );
});

// W489: a push shows on its sync line until it ends, as the server records it (ProjectSync.push), whoever started it.
const pushRun = (stage, extra = {}) => ({
  branch: "feat/x",
  stage,
  check: "",
  note: "",
  startTime: { seconds: 1760000000n, nanos: 0 },
  ...extra,
});

test("a push shows on its sync line: a spinner while its checks and git push run, a check mark once pushed, a cross with why when held", () => {
  const now = 1760000100000;
  const line = (sync, extra = {}) =>
    s.renderToStaticMarkup(h(s.SyncLine, { sync, now, onPush() {}, ...extra }));
  // Its checks run: a spinner in the button, disabled, the check on hover.
  const checking = line(
    projectSync("feat/x", 3, 0, {
      push: pushRun(s.PushStage.CHECKING, { check: "test" }),
    }),
  );
  assert.match(
    checking,
    /<button type="button" class="icon-button sync-push" disabled="" title="Push checks running: test" aria-label="Push checks running: test"><svg[^>]*sync-spinner/,
  );
  assert.match(checking, /↑3/);
  // git push runs.
  assert.match(
    line(projectSync("feat/x", 3, 0, { push: pushRun(s.PushStage.PUSHING) })),
    /disabled="" title="Pushing feat\/x to origin…"/,
  );
  // Pushed a moment ago: a check mark, the branch in sync, no button to push nothing.
  const pushedSync = projectSync("feat/x", 0, 0, {
    push: pushRun(s.PushStage.PUSHED, {
      endTime: { seconds: 1760000098n, nanos: 0 },
    }),
  });
  const pushed = line(pushedSync);
  assert.match(
    pushed,
    /<span class="sync-status pushed" role="img" title="Pushed to origin\/feat\/x" aria-label="Pushed to origin\/feat\/x">/,
  );
  assert.doesNotMatch(pushed, /sync-push|sync-spinner/);
  // The list shows it a few seconds, then lets it go.
  assert.match(
    s.renderToStaticMarkup(h(s.SyncLines, { syncs: [pushedSync], now })),
    /sync-status pushed/,
  );
  assert.equal(
    s.renderToStaticMarkup(
      h(s.SyncLines, { syncs: [pushedSync], now: now + 10000 }),
    ),
    "",
  );
  // Held: a cross, the check that failed and its first lines, the correction Djinn started; the button pushes again.
  const held = line(
    projectSync("feat/x", 3, 0, {
      push: pushRun(s.PushStage.HELD, {
        endTime: { seconds: 1760000090n, nanos: 0 },
        note: "test: test exited 1\n--- FAIL: TestLogin",
      }),
      correction: "W12",
    }),
  );
  assert.match(
    held,
    /<span class="sync-status held" role="img" title="Push held: its push checks are red\n?test: test exited 1\n?--- FAIL: TestLogin\n?Djinn started W12 to correct them"/,
  );
  assert.match(
    held,
    /<button type="button" class="icon-button sync-push" title="Push feat\/x"/,
  );
  // Refused: a cross with git's words.
  assert.match(
    line(
      projectSync("feat/x", 3, 0, {
        push: pushRun(s.PushStage.FAILED, {
          endTime: { seconds: 1760000090n, nanos: 0 },
          note: "origin refused the push: its branch has commits that this one does not",
        }),
      }),
    ),
    /class="sync-status failed" role="img" title="The push failed\n?origin refused the push: its branch has commits that this one does not"/,
  );
  // Behind only: nothing to push, no button.
  assert.doesNotMatch(line(projectSync("feat/x", 0, 2)), /sync-push/);
});

test("a push running in Djinn makes every button of the list wait, as a reload shows it", () => {
  const lines = live(s.SyncLines, {
    syncs: [
      projectSync("feat/a", 2, 0),
      projectSync("feat/x", 3, 0, {
        push: pushRun(s.PushStage.CHECKING, { check: "test" }),
      }),
    ],
    onPush() {},
  });
  assert.deepEqual(
    lines.all(s.SyncLine).map((x) => x.props.busy),
    ["A push is running: wait for it to end", ""],
  );
});

test("regression W489: a stale ↑3 pushed again says 'up to date' quietly on its line, and an error stays on its line", async () => {
  const pushes = [];
  let answer = { upToDate: true };
  const lines = live(s.SyncLines, {
    syncs: [projectSync("feat/wails-go", 3, 0)],
    onPush: (sync) => {
      pushes.push(sync.branch);
      return answer instanceof Error
        ? Promise.reject(answer)
        : Promise.resolve(answer);
    },
  });
  lines.one(s.SyncLine).onPush();
  assert.equal(lines.one(s.SyncLine).pushing, true);
  await lines.settle();
  const quiet = lines.one(s.SyncLine);
  assert.equal(quiet.pushing, false);
  assert.equal(quiet.outcome.upToDate, true);
  const html = s.renderToStaticMarkup(h(s.SyncLine, quiet));
  assert.match(
    html,
    /<span class="sync-note" title="Nothing to push: origin\/feat\/wails-go holds this branch already">up to date<\/span>/,
  );
  assert.doesNotMatch(html, /sync-status/);

  // A failure the server does not explain: a cross on the line, the error on hover; no throw for a toast.
  answer = new Error("repository /tmp/app has no remote");
  lines.one(s.SyncLine).onPush();
  await lines.settle();
  assert.deepEqual(pushes, ["feat/wails-go", "feat/wails-go"]);
  assert.match(
    s.renderToStaticMarkup(h(s.SyncLine, lines.one(s.SyncLine))),
    /class="sync-status failed" role="img" title="The push failed\n?repository \/tmp\/app has no remote"/,
  );
});

test("W164: a branch behind its remote has a pull button; a refused pull says why with a cross on its line", async () => {
  const pulls = [];
  let answer = {};
  const lines = live(s.SyncLines, {
    syncs: [projectSync("feat/x", 0, 2), projectSync("feat/y", 3, 0)],
    onPush() {},
    onPull: (sync) => {
      pulls.push(sync.branch);
      return answer instanceof Error
        ? Promise.reject(answer)
        : Promise.resolve(answer);
    },
  });
  const first = () => lines.all(s.SyncLine)[0].props;
  const [behind, ahead] = lines.all(s.SyncLine).map((x) => x.props);
  const html = s.renderToStaticMarkup(h(s.SyncLine, behind));
  assert.match(
    html,
    /<button type="button" class="icon-button sync-push sync-pull" title="Pull feat\/x: bring it up to origin\/feat\/x, a fast-forward only"/,
  );
  // Ahead only: nothing to pull, no pull button.
  assert.doesNotMatch(
    s.renderToStaticMarkup(h(s.SyncLine, ahead)),
    /sync-pull/,
  );

  // While it pulls, every other button waits.
  behind.onPull();
  assert.equal(first().pulling, true);
  assert.equal(
    lines.all(s.SyncLine)[1].props.busy,
    "A pull is running: wait for it to end",
  );
  await lines.settle();
  assert.deepEqual(pulls, ["feat/x"]);
  assert.equal(first().refused, "");

  answer = new Error(
    "feat/x has diverged from origin/feat/x (ahead 1, behind 2): Djinn merges nothing into it",
  );
  first().onPull();
  await lines.settle();
  assert.match(
    s.renderToStaticMarkup(h(s.SyncLine, first())),
    /class="sync-status failed" role="img" title="The pull was refused\n?feat\/x has diverged from origin\/feat\/x \(ahead 1, behind 2\)/,
  );
});

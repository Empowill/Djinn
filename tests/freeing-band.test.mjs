// The cascade band of a granted wish (src/freeing-band.tsx, L28 variant A, Q80 A): its pace, its segments, what it says
// of each azima freed or kept, and its summary. Run with `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { screens } from "./screens.mjs";

const { s, h } = await screens();

const GB = 1e9;
const azima = (id, code, title) => ({ id, code, title, wishId: "w1" });

// pass is a freeing pass of wish w1 over azimas, each [id, total, done, freedBytes, keptItems].
const pass = (rows, extra = {}) => ({
  total: rows.reduce((n, r) => n + r[1], 0),
  done: rows.reduce((n, r) => n + r[2], 0),
  azimas: rows.map(([id, total, done, bytes, kept = []]) => ({
    azimaId: id,
    wishId: "w1",
    total,
    done,
    freedBytes: BigInt(bytes),
    kept: kept.length ? 1 : 0,
    keptItems: kept,
  })),
  wishIds: ["w1"],
  ...extra,
});

const dirty = {
  taskId: "t-w134",
  part: s.DiskFreeingPart.WORKTREE,
  reason: s.DiskFreeingReason.UNCOMMITTED,
  commits: 0,
  detail: "it holds changes not committed, /wt/w134",
};
const unpushed = {
  taskId: "t-w136",
  part: s.DiskFreeingPart.BRANCH,
  reason: s.DiskFreeingReason.UNPUSHED,
  commits: 3,
  detail: "3 commits found neither in djinn/gates nor on the remote",
};

const bigTasks = [
  azima("a1", "W131", "Declared gates: trial"),
  azima("a2", "W132", "Team settings panel removed"),
  azima("a3", "W133", "Fix the lint of PR 43"),
  azima("a4", "W134", "Memory gate"),
  azima("a5", "W135", "Hourglass: disk, residues"),
  azima("a6", "W136", "Gate statistics"),
  azima("a7", "W137", "Mockup of the cascade"),
  { id: "t-w134", code: "W1341", title: "worker" },
  { id: "t-w136", code: "W1361", title: "worker" },
];
const bigRows = [
  ["a1", 1, 1, 2.4 * GB],
  ["a2", 1, 1, 1.1 * GB],
  ["a3", 1, 1, 0.9 * GB],
  ["a4", 1, 1, 0, [dirty]],
  ["a5", 2, 2, 3.8 * GB],
  ["a6", 1, 1, 1.6 * GB, [unpushed]],
  ["a7", 1, 1, 0.02 * GB],
];

test("the band's pace (L28): 350 ms an azima at least, 2.5 s a cascade, the real pace past them", () => {
  assert.equal(s.MIN_STEP, 350);
  assert.equal(s.MIN_TOTAL, 2500);
  // A small wish: 2 azimas x 350 ms < 2.5 s, each stretched to 1.25 s.
  assert.equal(s.stepOf(2), 1250);
  assert.deepEqual(s.shownTimes([0, 0], 0), [1250, 2500]);
  // A big one: 7 x 350 ms = 2.45 s < 2.5 s, each ~357 ms.
  assert.ok(Math.abs(s.stepOf(7) - 357.14) < 0.01);
  const seven = s.shownTimes(Array(7).fill(0), 0);
  assert.ok(Math.abs(seven.at(-1) - 2500) < 1e-6);
  // Twelve azimas: the 350 ms floor wins.
  assert.equal(s.stepOf(12), 350);
  // An azima slow for real (a big worktree's rm) holds the next ones: never shown done before it is.
  const slow = s.shownTimes([100, 4000, 4000], 0);
  assert.deepEqual(slow.map(Math.round), [833, 4000, 4833]);
  // Not seen done yet: not shown done, nor any after it.
  assert.deepEqual(s.shownTimes([0, undefined, 0], 0).slice(1), [
    Infinity,
    Infinity,
  ]);

  const times = [1250, 2500];
  assert.deepEqual(s.shownAt(times, 0), {
    done: 0,
    now: 0,
    phase: "playing",
    next: 1250,
  });
  assert.deepEqual(s.shownAt(times, 1300), {
    done: 1,
    now: 1,
    phase: "playing",
    next: 2500,
  });
  assert.deepEqual(s.shownAt(times, 2500), {
    done: 2,
    now: -1,
    phase: "summary",
    next: 2500 + s.SUMMARY_MS,
  });
  assert.equal(s.shownAt(times, 2500 + s.SUMMARY_MS).phase, "folded");
});

test("the band's pace follows the pass as read: the cascade plays even when it was over at the first read, a stale one opens folded", () => {
  const tasks = [
    azima("a1", "W1", "Write hello.txt"),
    azima("a2", "W2", "Read hello.txt"),
  ];
  const pace = new s.CascadePace();
  // First read at 1000: W1 done, W2 still going.
  let segs = s.cascadeOf(
    pass([
      ["a1", 1, 1, 21e6],
      ["a2", 1, 0, 0],
    ]),
    "w1",
    tasks,
  );
  assert.deepEqual(pace.observe("p1", segs, undefined, 1000), {
    done: 0,
    now: 0,
    phase: "playing",
    next: 2250,
  });
  // W2 done at 1100 for real: the display keeps its pace.
  segs = s.cascadeOf(
    pass([
      ["a1", 1, 1, 21e6],
      ["a2", 1, 1, 17e6],
    ]),
    "w1",
    tasks,
  );
  assert.equal(pace.observe("p1", segs, 1100, 1100).done, 0);
  assert.equal(pace.observe("p1", segs, 1100, 2300).done, 1);
  assert.equal(pace.observe("p1", segs, 1100, 3500).phase, "summary");

  // A wish opened long after its pass: folded at once.
  const late = new s.CascadePace();
  assert.equal(late.observe("p2", segs, 0, s.STALE_MS + 1).phase, "folded");
  // A pass seen right after it ended still plays.
  const fresh = new s.CascadePace();
  assert.equal(fresh.observe("p3", segs, 0, 500).phase, "playing");
});

test("cascadeOf keeps only the wish's azimas, in the pass's order, the tasks of no azima as one more", () => {
  const freeing = pass([
    ["", 2, 2, 5e6],
    ["a1", 1, 1, 21e6],
  ]);
  freeing.azimas.push({
    azimaId: "x",
    wishId: "w2",
    total: 1,
    done: 1,
    freedBytes: 0n,
    kept: 0,
    keptItems: [],
  });
  const segs = s.cascadeOf(freeing, "w1", [
    azima("a1", "W1", "Write hello.txt"),
  ]);
  assert.deepEqual(
    segs.map((g) => [g.key, g.code, g.title]),
    [
      ["none", "", "Tasks of no azima"],
      ["a1", "W1", "Write hello.txt"],
    ],
  );
  assert.equal(s.cascadeOf(freeing, "w3", []), null);
  assert.equal(s.cascadeOf(undefined, "w1", []), null);
});

test("the band says what each kept and why: a worktree with changes not committed, a local branch not pushed", () => {
  assert.equal(
    s.keptText(dirty, "W134"),
    "W134: worktree, scratch and branch kept, the worktree holds changes not committed",
  );
  assert.equal(
    s.keptText(unpushed, "W136"),
    "W136: local branch kept, 3 commits not pushed, their only copy",
  );
  assert.equal(
    s.keptText(
      { ...unpushed, part: s.DiskFreeingPart.WORKTREE, commits: 1 },
      "W2",
    ),
    "W2: worktree, scratch and branch kept, 1 commit not pushed, its only copy",
  );
  assert.equal(
    s.keptText(
      {
        taskId: "t",
        part: s.DiskFreeingPart.SCRATCH,
        reason: s.DiskFreeingReason.ERROR,
        commits: 0,
        detail: "permission denied",
      },
      "W9",
    ),
    "W9: scratch kept, permission denied",
  );
});

// render draws the band in a theme, as the window does: the theme is the document's.
const render = (theme, props) => {
  const before = globalThis.document;
  globalThis.document = { documentElement: { dataset: { theme } } };
  try {
    return s.renderToStaticMarkup(
      h(s.FreeingBandView, { open: false, onToggle() {}, ...props }),
    );
  } finally {
    globalThis.document = before;
  }
};
const codes = new Map(bigTasks.map((task) => [task.id, task.code]));

test("a 7-azima cascade: one segment each, the kept ones hatched, the lines and the count while it plays, in dark and light", () => {
  const segments = s.cascadeOf(pass(bigRows), "w1", bigTasks);
  for (const theme of ["dark", "light"]) {
    const playing = render(theme, {
      segments,
      codes,
      shown: { done: 4, now: 4, phase: "playing", next: 0 },
    });
    assert.equal(playing.match(/class="freeing-seg[ "]/g).length, 7);
    assert.equal(playing.match(/freeing-seg on/g).length, 4);
    assert.match(playing, /freeing-seg on kept/); // W134, its worktree kept.
    assert.match(playing, /freeing-seg now/);
    assert.match(playing, /5 \/ 7/);
    assert.match(
      playing,
      /W1341: worktree, scratch and branch kept, the worktree holds changes not committed/,
    );
    // W136 not shown done yet: its reason waits.
    assert.doesNotMatch(playing, /local branch kept/);
    assert.doesNotMatch(playing, /freeing-sum/);
    assert.match(
      playing,
      /Memory gate<\/span><span class="freeing-amount">nothing freed/,
    );

    const summary = render(theme, {
      segments,
      codes,
      shown: { done: 7, now: -1, phase: "summary", next: 0 },
    });
    assert.equal(summary.match(/freeing-seg on kept/g).length, 2);
    assert.match(
      summary,
      /W1361: local branch kept, 3 commits not pushed, their only copy/,
    );
    assert.match(summary, /freed · 5 azimas cleared · 2 kept/);
  }
});

test("a 2-azima cascade folds into a one-line summary that opens the detail again", () => {
  const tasks = [
    azima("a1", "W1", "Write hello.txt"),
    azima("a2", "W2", "Read hello.txt"),
  ];
  const segments = s.cascadeOf(
    pass([
      ["a1", 1, 1, 21e6],
      ["a2", 1, 1, 17e6],
    ]),
    "w1",
    tasks,
  );
  const folded = { done: 2, now: -1, phase: "folded", next: Infinity };
  for (const theme of ["dark", "light"]) {
    const line = render(theme, { segments, codes: new Map(), shown: folded });
    assert.match(line, /freeing-band folded/);
    assert.match(line, /<button class="freeing-fold">/);
    assert.match(line, /freed · 2 azimas cleared/);
    assert.doesNotMatch(line, /kept|freeing-seg/);
    const opened = render(theme, {
      segments,
      codes: new Map(),
      shown: folded,
      open: true,
    });
    assert.equal(opened.match(/freeing-seg on/g).length, 2);
    assert.match(opened, /Write hello\.txt/);
  }
  let toggled = 0;
  const view = s.FreeingBandView({
    segments,
    codes: new Map(),
    shown: folded,
    open: false,
    onToggle: () => toggled++,
  });
  view.props.children.props.onClick();
  assert.equal(toggled, 1);
});

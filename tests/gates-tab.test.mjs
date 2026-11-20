// The Gates tab of the Hourglass (src/gates-tab.tsx, src/gates-flow.tsx): the gates now, the lanes of the gates under
// the workers that run and the Sankey of the named gates over an hour, 6 hours or a day, the commands admitted under a
// gate and the waits by gate. Run with `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { resources, screens } from "./screens.mjs";

const { s, h } = await screens();
const { at, t0, t3, lintGate, resourceCost, hourglassDjinn } = resources(s);
const gib = 1024n ** 3n;

const tasks = new Map([
  ["task-w1", { id: "task-w1", wishId: "wish-1", code: "W1", title: "Lint" }],
  ["task-w2", { id: "task-w2", wishId: "wish-1", code: "W2", title: "Test" }],
]);

// Runs of a command: its waits, the newest last, each a minute after the one before.
const runsOf = (gate, waits, extra = {}) =>
  waits.map((waitSeconds, i) => ({
    time: at(t0 + BigInt(i * 60)),
    seconds: 40,
    cpuSeconds: 30,
    peakMemoryBytes: gib,
    exitCode: 0,
    waitSeconds,
    heldSeconds: 41,
    gate,
    taskId: "task-w1",
    ...extra,
  }));

const costs = [
  resourceCost("p1", "go tool task lint", {
    id: "c-lint",
    runs: 15,
    asked: 16,
    seconds: 40,
    cpuSeconds: 170,
    peakMemoryBytes: 3n * gib,
    p50WaitSeconds: 0,
    p95WaitSeconds: 12,
    gate: "lint",
    recentRuns: runsOf("lint", [0, 0, 0, 1, 12]),
  }),
  resourceCost("p1", "go tool task test", {
    id: "c-test",
    runs: 4,
    asked: 4,
    seconds: 600,
    cpuSeconds: 900,
    peakMemoryBytes: gib,
    gate: "test",
    recentRuns: runsOf("test", [0, 7], { taskId: "task-w2", exitCode: 1 }),
  }),
  resourceCost("p2", "npm run build", {
    id: "c-build",
    runs: 2,
    asked: 2,
    seconds: 30,
  }),
];

const live = [
  {
    name: "lint",
    holder: "W1 (Lint): go tool task lint",
    holderTaskId: "task-w1",
    since: at(t3 - 98n),
    waiting: ["W2 (Test): go tool task lint", "a holder outside any task"],
    waitingTaskIds: ["task-w2"],
    project: "djinn",
    waitingProjects: ["djinn", ""],
    takesSlot: false,
    ifrit: false,
  },
];

// The summary the server sums over every command (machine.Summary): the figures and the waits by gate.
const summary = {
  asked: 22,
  ran: 21,
  holds: 7,
  p50WaitSeconds: 0,
  p90WaitSeconds: 12,
  maxWaitSeconds: 12,
  longWaits: 2,
  peak: { ...costs[0], recentRuns: [] },
  gates: [
    {
      gate: "lint",
      project: "djinn",
      holds: 5,
      p50Seconds: 0,
      p90Seconds: 12,
      maxSeconds: 12,
      long: 1,
      last: costs[0].recentRuns[4],
      lastCommand: "go tool task lint",
    },
    {
      gate: "test",
      project: "djinn",
      holds: 2,
      p50Seconds: 0,
      p90Seconds: 7,
      maxSeconds: 7,
      long: 1,
      last: costs[1].recentRuns[1],
      lastCommand: "go tool task test",
    },
  ],
};
// The page the server sends: the most total time first, without the runs.
const brief = (c) => ({ ...c, recentRuns: [] });
const page = [costs[1], costs[0], costs[2]].map(brief);

test("the Gates tab: its figures, the gates now with who holds and who waits, the last hour, the commands by total time, the waits by gate", () => {
  const html = s.renderToStaticMarkup(
    h(s.GatesTab, {
      tasks,
      onWorker: () => {},
      onTask: () => {},
      initialGates: live,
      initialCommands: page,
      initialTotal: 60,
      initialSummary: summary,
      initialHistory: {
        startTime: at(t3 - 3600n),
        readTime: at(t3),
        intervals: [lintGate],
        runs: [],
      },
    }),
  );
  const text = (part) =>
    part
      .replace(/<[^>]+>/g, " ")
      .replace(/\s+/g, " ")
      .trim();

  // Four figures: the gate's requests, the waits' p50 and p90, the long waits, the highest peak.
  const figures = [
    ...html.matchAll(
      /<div class="statistics-figure" data-card="(\w+)">([\s\S]*?)<\/div>/g,
    ),
  ].map(([, id, body]) => `${id}: ${text(body)}`);
  assert.deepEqual(figures, [
    "asked: Gate requests 22 21 ran to their end, 1 did not",
    "wait: Wait for a gate, p50 · p90 0s · 12s over the last 7 holds measured",
    "long: Waits over 5 s 2 29% of the holds; the longest 12s",
    "peak: Highest peak memory 3 GB go tool task lint",
  ]);

  // Now, by project: djinn holds lint and waits for it, the holder outside any task waits for it apart.
  const heads = [
    ...html.matchAll(
      /<header class="gates-project-head">([\s\S]*?)<\/header>/g,
    ),
  ].map(([, head]) => text(head));
  assert.deepEqual(heads, [
    "djinn 1 held · 1 waiting",
    "Outside any project 0 held · 1 waiting",
  ]);
  // Under djinn: lint held by W1, a link to its row, W2 waiting, named; test free, its last hold said.
  const lint = html.slice(
    html.indexOf('id="gate-lint"'),
    html.indexOf('id="gate-test"'),
  );
  assert.match(lint, /class="gates-gate is-held"/);
  assert.match(
    lint,
    /<button type="button" class="text-link gates-task-link agent-code" title="Lint">W1<\/button> W1 \(Lint\): go tool task lint, for /,
  );
  assert.match(text(lint), /1 waiting: W2 W2 \(Test\): go tool task lint <li/);
  const free = html.slice(
    html.indexOf('id="gate-test"'),
    html.indexOf('aria-label="Outside any project"'),
  );
  assert.match(free, /<span class="gates-gate-state">free<\/span>/);
  assert.match(
    text(free),
    /last held \d\d\/\d\d [^·]+: go tool task test · W2 · 1 GB/,
  );

  // Outside any project: the same lock, held by djinn's W1, its waiter second in the queue; no second target.
  const outside = text(
    html.slice(
      html.indexOf('aria-label="Outside any project"'),
      html.indexOf('class="statistics-card gates-card gates-flow"'),
    ),
  );
  assert.match(
    outside,
    /lint waited for held by W1 \(Lint\): go tool task lint \(djinn\) 1 waiting: a holder outside any task/,
  );
  assert.equal(html.match(/id="gate-lint"/g)?.length, 1);

  // The lanes of the last hour: the band of lint, its wait and its hold said, outside a task.
  assert.match(
    html,
    /class="gates-band gk-out" data-band="0" aria-label="Gate lint \(djinn\): go tool task lint\. Waited 6m40s, held 13m20s\."/,
  );
  assert.match(
    html,
    /<button type="button" class="active" aria-pressed="true">1 h<\/button>/,
  );

  // The commands, the most total time first, the top one marked; a command with no run kept stays closed.
  const rows = [
    ...html.matchAll(
      /<tr id="command-([\w-]+)" class="gates-command( is-top)?">([\s\S]*?)<\/tr>/g,
    ),
  ].map(([, id, top, row]) => `${id}${top ? " top" : ""}: ${text(row)}`);
  assert.deepEqual(rows, [
    "c-test top: go tool task test most total time djinn test 4 10m 40m 1 GB 15m 0s · 0s",
    "c-lint: go tool task lint djinn lint 15 40s 10m 3 GB 2m50s 0s · 12s",
    "c-build: npm run build old-project — 2 30s 1m — — 0s · 0s",
  ]);
  // The list's end: 3 of 60, the next page read as it comes into view, or by its button.
  assert.match(
    html,
    /<div class="gates-list-end"><span class="muted-text">3 of 60 commands<\/span><button type="button" class="text-link">Show 25 more<\/button><\/div>/,
  );

  // The waits by gate.
  const waits = [
    ...html.matchAll(/<tr class="gates-wait-row">([\s\S]*?)<\/tr>/g),
  ].map(([, row]) => text(row));
  assert.deepEqual(waits, [
    "lint djinn 5 0s 12s 12s 1",
    "test djinn 2 0s 7s 7s 1",
  ]);
});

test("the Gates tab says when nothing is known yet", () => {
  const html = s.renderToStaticMarkup(h(s.GatesTab, {}));
  assert.match(html, /No gate is held or waited for/);
  assert.match(html, /No gate was asked for over this period\./);
  assert.match(html, /No named gate was asked for over this period\./);
  assert.match(html, /No command has run under a gate yet\./);
  assert.match(html, /No run under a gate measured yet\./);
  assert.doesNotMatch(html, /gates-list-end/);
});

test("a command opens on its last 20 runs, then all on demand; one opened from the lanes off the page shows above it", () => {
  const many = {
    ...costs[0],
    runs: 30,
    recentRuns: runsOf("lint", Array(30).fill(0)),
  };
  const html = s.renderToStaticMarkup(
    h(s.GatesTab, {
      tasks,
      initialCommands: [brief(costs[1])],
      initialTotal: 2,
      initialOpened: many,
    }),
  );
  const rows = [
    ...html.matchAll(/<tr id="command-([\w-]+)" class="([^"]+)"/g),
  ].map(([, id, cls]) => `${id} ${cls}`);
  assert.deepEqual(rows, [
    "c-lint gates-command is-picked",
    "c-test gates-command is-top",
  ]);
  assert.match(html, /Its last 30 runs, the newest first/);
  assert.equal(html.match(/<tr><td>\d\d\/\d\d /g)?.length, 20);
  assert.match(
    html,
    /<button type="button" class="text-link gates-more">Show all 30 runs<\/button>/,
  );
});

test("past six free gates and eight gates' waits, the rest folds behind a link", () => {
  const gates = Array.from({ length: 11 }, (_, i) => ({
    gate: `g${String(i).padStart(2, "0")}`,
    holds: 20 - i,
    p50Seconds: 0,
    p90Seconds: 1,
    maxSeconds: 2,
    long: 0,
    lastCommand: "true",
  }));
  const html = s.renderToStaticMarkup(
    h(s.GatesTab, { initialSummary: { ...summary, gates } }),
  );
  assert.equal(
    html.match(/<span class="gates-gate-state">free<\/span>/g)?.length,
    6,
  );
  assert.match(html, />5 more free gates<\/button>/);
  assert.equal(html.match(/class="gates-wait-row"/g)?.length, 8);
  assert.match(html, />3 more gates<\/button>/);
});

test("a read of the history after the last one lays what changed over the window kept", () => {
  const ago = (seconds) => at(t3 - BigInt(seconds));
  const iv = (command, asked, release) => ({
    gate: "lint",
    command,
    taskId: "task-w1",
    askedTime: ago(asked),
    grantTime: ago(asked),
    releaseTime: release === null ? undefined : ago(release),
  });
  const run = (taskId, from, to) => ({
    taskId,
    startTime: ago(from),
    endTime: to === null ? undefined : ago(to),
  });
  const prev = {
    startTime: ago(3600),
    readTime: ago(60),
    intervals: [
      iv("gone", 4000, 3700),
      iv("kept", 3000, 2000),
      iv("again", 200, 90),
      iv("held", 100, null),
    ],
    runs: [
      run("ended", 3500, 3000),
      run("closed", 400, null),
      run("stopped", 300, null),
      run("old", 9000, 3650),
    ],
  };
  // Read after 2 minutes before the last read, 30 s later: the window moved 30 s.
  const next = {
    startTime: ago(3570),
    readTime: at(t3),
    intervals: [iv("again", 200, 90), iv("new", 50, 20), iv("held", 100, null)],
    runs: [run("closed", 400, 10)],
  };
  const merged = s.mergeHistory(prev, next);
  assert.deepEqual(
    merged.intervals.map((i) => i.command),
    ["kept", "again", "new", "held"],
  );
  assert.deepEqual(
    merged.runs.map((r) => `${r.taskId}${r.endTime ? "" : " open"}`),
    ["ended", "closed"],
  );
  assert.equal(merged.readTime, next.readTime);
});

// heavy is the load of a busy Djinn: 5,400 commands (the real store had 5,348 on 18/11/2026), 20 of them with 200
// runs; a day of 1,200 intervals over 7 gates and 320 worker runs; 6 gates held, 3 waiting each.
function heavy() {
  const end = Number(t3);
  const ts = (sec) => at(BigInt(Math.floor(sec)));
  const names = ["lint", "test", "e2e", "codegen", "stack", "paid", "fmt"];
  const all = Array.from({ length: 5400 }, (_, i) => ({
    id: `c${i}`,
    projectId: "p1",
    projectName: "djinn",
    command: `go tool task e2e -- e2e/spec-${i}.spec.ts`,
    runs: 1 + (i % 50),
    asked: 2 + (i % 50),
    seconds: 10 + (i % 600),
    cpuSeconds: 20,
    peakMemoryBytes: BigInt(1 + (i % 7)) * gib,
    p50WaitSeconds: 0,
    p95WaitSeconds: i % 9,
    gate: names[i % 7],
    recentRuns: runsOf(
      names[i % 7],
      Array(i < 20 ? 200 : i % 3 ? 0 : 1).fill(3),
    ),
  }));
  const intervals = Array.from({ length: 1200 }, (_, i) => {
    const r = end - i * 72;
    return {
      gate: names[i % 7],
      command: `go tool task ${names[i % 7]}`,
      project: "djinn",
      taskId: `t${i % 400}`,
      taskCode: `W${i % 400}`,
      taskKind: i % 5,
      askedTime: ts(r - 120),
      grantTime: ts(r - 100 + (i % 3) * 30),
      releaseTime: ts(r),
    };
  });
  const runs = Array.from({ length: 320 }, (_, i) => ({
    taskId: `t${i}`,
    code: `W${i}`,
    kind: i % 5,
    startTime: ts(end - 86400 + i * 250),
    endTime: ts(end - 86400 + i * 250 + 3600),
  }));
  const gates = names.slice(0, 6).map((name, i) => ({
    name,
    holder: `W${i} (Task): go tool task ${name}`,
    holderTaskId: "task-w1",
    since: ts(end - 60),
    waiting: ["W10: a", "W11: b", "outside"],
    waitingTaskIds: ["task-w2"],
    project: "djinn",
    waitingProjects: ["djinn", "dolmen", ""],
    takesSlot: false,
    ifrit: false,
  }));
  return {
    all,
    gates,
    history: { startTime: ts(end - 86400), readTime: ts(end), intervals, runs },
  };
}

// Under a heavy load the tab renders a page of the commands, never all: under 1.5 s and 0.6 MB of markup on a loaded
// machine (it rendered every command before W124: 1.3 to 2.3 s and 5.2 MB at load 17 to 30, 220 to 390 ms of which the
// lanes and the Sankey); under DJINN_TEST_BESIDE or in CI, beside the other suites, 3 s.
test("the Gates tab under a heavy load renders a page of the commands, under 1.5 s", () => {
  const { all, gates, history } = heavy();
  const props = {
    tasks,
    initialGates: gates,
    initialCommands: all.slice(0, s.PAGE).map(brief),
    initialTotal: all.length,
    initialSummary: { ...summary, gates: summary.gates },
    initialHistory: history,
    initialPeriod: 1440,
  };
  const times = [];
  let html = "";
  for (let i = 0; i < 3; i++) {
    const start = performance.now();
    html = s.renderToStaticMarkup(h(s.GatesTab, props));
    times.push(performance.now() - start);
  }
  const median = times.sort((a, b) => a - b)[1];
  assert.equal(html.match(/<tr id="command-/g)?.length, s.PAGE);
  assert.match(html, /25 of 5400 commands/);
  assert.ok(html.length < 600_000, `${html.length} characters of markup`);
  const limit = process.env.CI || process.env.DJINN_TEST_BESIDE ? 3000 : 1500;
  assert.ok(median < limit, `rendered in ${times.map(Math.round)} ms`);
});

test("a link to a gate opens the Hourglass on its Gates tab", () => {
  const { djinn } = hourglassDjinn();
  const html = s.renderToStaticMarkup(
    h(
      s.DjinnProvider,
      { djinn },
      h(s.Hourglass, { wishes: [], target: { target: "gate-lint" } }),
    ),
  );
  assert.match(
    html,
    /id="hourglass-tab-gates" aria-selected="true" class="active"/,
  );
  assert.match(html, /class="gates-tab"/);
  assert.doesNotMatch(html, /class="consumption-panel"/);
});

// The history of an hour ending at t3: W1's code waits for lint, W2's investigation and W3's mockup share test, W1's
// command is admitted after 3 s, a terminal holds lint outside any task; W1 and W2 run, a watcher runs still.
const ago = (seconds) => at(t3 - BigInt(seconds));
const held = (gate, code, kind, asked, grant, release, extra = {}) => ({
  gate,
  command: `go tool task ${gate}`,
  project: "djinn",
  taskId: code ? `task-${code.toLowerCase()}` : "",
  taskCode: code,
  taskKind: kind,
  askedTime: ago(asked),
  grantTime: ago(grant),
  releaseTime: release === null ? undefined : ago(release),
  ...extra,
});
const K = s.TaskKind;
const history = {
  startTime: ago(3600),
  readTime: at(t3),
  intervals: [
    held("lint", "W1", K.WORK, 3000, 2900, 2000),
    held("test", "W2", K.INVESTIGATION, 1500, 1500, 900),
    held("test", "W3", K.MOCKUP, 1400, 900, null),
    held("commands", "W1", K.WORK, 600, 597, 500, {
      mode: s.GateMode.SHARED,
    }),
    held("lint", "", K.UNSPECIFIED, 200, 200, 100, { outside: true }),
  ],
  runs: [
    {
      taskId: "task-w1",
      code: "W1",
      kind: K.WORK,
      startTime: ago(3500),
      endTime: ago(400),
    },
    {
      taskId: "task-w2",
      code: "W2",
      kind: K.INVESTIGATION,
      startTime: ago(1600),
      endTime: ago(800),
    },
    { taskId: "task-w4", code: "W4", kind: K.WATCHER, startTime: ago(3000) },
  ],
};
const end = Number(t3) * 1000;
const start = end - 3_600_000;

test("a task's kind gives its colour: code, correction and review in the workers' blue", () => {
  assert.deepEqual(
    [
      K.WORK,
      K.CORRECTION,
      K.REVIEW,
      K.INVESTIGATION,
      K.MOCKUP,
      K.WATCHER,
      K.IFRIT,
      K.AZIMA,
    ].map(s.kindGroup),
    ["work", "work", "work", "inv", "mock", "watch", "ifrit", "other"],
  );
  // A hold outside a running worker, or of no task, is outside a task.
  assert.deepEqual(
    s.holdsOf(history.intervals, end).map((h) => h.group),
    ["work", "inv", "mock", "work", "out"],
  );
});

test("one lane per gate: the named gates by name, the admissions last and only when asked, overlaps on lanes apart", () => {
  const holds = s.holdsOf(history.intervals, end);
  const rows = (admissions) =>
    s.gateRows(s.inScope(holds, admissions), start, end).map((r) => ({
      gate: r.gate,
      requests: r.requests,
      lanes: r.lanes,
      bands: r.bands.map(
        (b) => `${b.hold.iv.taskCode || "-"}:${b.hold.group}:${b.lane}`,
      ),
    }));
  assert.deepEqual(rows(false), [
    { gate: "lint", requests: 2, lanes: 1, bands: ["W1:work:0", "-:out:0"] },
    { gate: "test", requests: 2, lanes: 2, bands: ["W2:inv:0", "W3:mock:1"] },
  ]);
  assert.deepEqual(
    rows(true).map((r) => r.gate),
    ["lint", "test", "commands"],
  );
  // A hold still running ends now; the time held is counted within the window.
  const [, test] = s.gateRows(s.inScope(holds, false), start, end);
  assert.equal(test.heldMs, (600 + 900) * 1000);
});

test("the workers' strip: those running by kind, and the requests waiting a gate a second or more", () => {
  const holds = s.inScope(s.holdsOf(history.intervals, end), false);
  const { buckets, max } = s.workerStrip(history.runs, holds, start, end, 60);
  // Minute 0: no one runs; minute 30: W1 and the watcher; minute 40: W1, W2, the watcher, and W3 waits for test.
  assert.deepEqual(buckets[0], { running: {}, waiting: 0 });
  assert.deepEqual(buckets[30], { running: { work: 1, watch: 1 }, waiting: 0 });
  assert.deepEqual(buckets[40], {
    running: { work: 1, inv: 1, watch: 1 },
    waiting: 1,
  });
  assert.equal(max, 3);
  // At one moment: who runs, who holds each gate, who waits.
  const now = s.instant(history.runs, holds, end - 1_000_000, end);
  assert.deepEqual(now.running, [
    ["work", 1],
    ["inv", 1],
    ["watch", 1],
  ]);
  assert.deepEqual(now.holding, [["djinn · test", ["W2"]]]);
  assert.deepEqual(
    now.waiting.map((h) => h.iv.taskCode),
    ["W3"],
  );
});

test("the Sankey counts the named gates' requests: project to gate, by kind, to wait, never the admissions", () => {
  const flows = s.sankeyFlows(s.holdsOf(history.intervals, end), start);
  assert.equal(flows.total, 4);
  assert.deepEqual(flows.projects, [["djinn", 4]]);
  assert.deepEqual(flows.kinds, ["work", "inv", "mock", "out"]);
  assert.deepEqual(flows.gates, [
    ["djinn\tlint", 2],
    ["djinn\ttest", 2],
  ]);
  assert.deepEqual(
    flows.toGate.map((f) => `${f.from}>${f.to}:${f.kind}:${f.count}`).sort(),
    [
      "djinn>djinn\tlint:out:1",
      "djinn>djinn\tlint:work:1",
      "djinn>djinn\ttest:inv:1",
      "djinn>djinn\ttest:mock:1",
    ],
  );
  assert.deepEqual(flows.outcomes, [
    ["now", 2],
    ["long", 2],
  ]);
  assert.ok(!flows.toGate.some((f) => f.to === s.ADMISSIONS));
  assert.deepEqual(
    flows.toOutcome.map((f) => `${f.from}>${f.to}:${f.count}`).sort(),
    [
      "djinn\tlint>long:1",
      "djinn\tlint>now:1",
      "djinn\ttest>long:1",
      "djinn\ttest>now:1",
    ],
  );
});

test("the Gates tab draws the lanes coloured by kind, the workers' strip and the Sankey without the admissions", () => {
  const html = s.renderToStaticMarkup(
    h(s.GatesTab, { initialHistory: history, initialPeriod: 360 }),
  );
  const flow = html.slice(
    html.indexOf('class="statistics-card gates-card gates-flow"'),
    html.indexOf('class="statistics-card gates-card gates-sankey"'),
  );
  const sankey = html.slice(
    html.indexOf('class="statistics-card gates-card gates-sankey"'),
  );
  // The period asked is pressed.
  assert.match(
    flow,
    /<button type="button" class="active" aria-pressed="true">6 h<\/button>/,
  );
  assert.match(
    flow,
    /<button type="button" class="active" aria-pressed="true">Named gates<\/button>/,
  );
  // The legend: the kinds seen, then hatched, plain and the queue line.
  const legend = [...flow.matchAll(/<li class="gk-(\w+)">/g)].map(([, g]) => g);
  assert.deepEqual(legend, ["work", "inv", "mock", "watch", "out"]);
  // The project's title over its lanes; one row per named gate, its bands in their kind's colour, the task's code in
  // the hold; no lane for the admissions.
  assert.match(
    flow,
    /<div class="gates-flow-label gates-flow-project"[^>]*><span class="gates-flow-name">djinn<\/span><\/div>/,
  );
  assert.match(
    flow,
    /<div class="gates-row-project"[^>]*><span class="muted-text">4 requests · /,
  );
  const rows = [
    ...flow.matchAll(
      /<div class="gates-row" role="list" aria-label="([^"]+)"/g,
    ),
  ].map(([, g]) => g);
  assert.deepEqual(rows, ["djinn · lint", "djinn · test"]);
  const bands = [
    ...flow.matchAll(
      /class="gates-band (gk-\w+)"[^>]*>(?:<span class="gates-band-wait"[^>]*><\/span>)?<span class="gates-band-held">([\w ]*)<\/span>/g,
    ),
  ].map(([, cls, code]) => `${cls} ${code}`);
  assert.deepEqual(bands, [
    "gk-work W1",
    "gk-out Outside a task",
    "gk-inv W2",
    "gk-mock W3",
  ]);
  assert.match(
    flow,
    /<code class="gates-flow-name">test<\/code><span class="gates-flow-sub muted-text">2 requests · 25m<\/span>/,
  );
  // W160: the label says its lines whole on hover, the count cut with an ellipsis rather than wrapped over the name.
  assert.match(
    flow,
    /<div class="gates-flow-label" style="height:[^"]+" title="test\n2 requests · 25m">/,
  );
  // The strip: the stacks by kind and the queue's line.
  assert.match(flow, /<path class="gk-watch" d="M/);
  assert.match(flow, /<path class="gates-strip-queue" d="M0,/);
  // The Sankey: four requests, its nodes the project, its named gates and the waits, the admissions never; the kinds
  // in its legend.
  assert.match(sankey, /The 4 requests for a named gate/);
  const nodes = [
    ...sankey.matchAll(
      /<g class="gates-sankey-node [^"]+">.*?<text[^>]*>([^<]+)<\/text><\/g>/g,
    ),
  ].map(([, n]) => n);
  assert.deepEqual(nodes, [
    "djinn 4",
    "lint 2",
    "test 2",
    "at once (&lt; 1 s) 2",
    "waits over 5 s 2",
  ]);
  const svg = sankey.slice(sankey.indexOf("<svg"), sankey.indexOf("</svg>"));
  assert.doesNotMatch(svg, /commands/);
  assert.match(
    svg,
    /<title>djinn → djinn · test: 1 request \(25%\) · Mockups<\/title>/,
  );
  assert.deepEqual(
    [...sankey.matchAll(/<li class="gk-(\w+)">/g)].map(([, g]) => g),
    ["work", "inv", "mock", "out"],
  );
});

// Two projects share the machine's lint; dolmen declares its own stack (stack@dolmen); a terminal outside any project
// holds e2e.
const shared = {
  startTime: ago(3600),
  readTime: at(t3),
  intervals: [
    held("lint", "W1", K.WORK, 3000, 2900, 2000),
    held("lint", "W5", K.WORK, 2950, 2000, 1500, { project: "dolmen" }),
    held("stack@dolmen", "W6", K.WORK, 1000, 1000, 500, { project: "dolmen" }),
    held("e2e", "", K.UNSPECIFIED, 300, 300, 100, {
      project: "",
      outside: true,
    }),
  ],
  runs: [],
};

test("a gate a project declares is its own; one of the machine counts under the project that asked", () => {
  assert.deepEqual(s.gateIn("stack@dolmen", "dolmen"), {
    name: "stack",
    project: "dolmen",
    declared: true,
  });
  assert.deepEqual(s.gateIn("lint", "djinn"), {
    name: "lint",
    project: "djinn",
    declared: false,
  });
  assert.deepEqual(s.gateIn("lint", undefined), {
    name: "lint",
    project: "",
    declared: false,
  });
  // The lanes: djinn, dolmen, then outside any project last; dolmen's lint apart from djinn's.
  const rows = s.gateRows(s.holdsOf(shared.intervals, end), start, end);
  assert.deepEqual(
    rows.map((r) => `${r.project}/${r.gate}`),
    ["djinn/lint", "dolmen/lint", "dolmen/stack", "/e2e"],
  );
  assert.deepEqual(
    s.projectSections(rows).map((p) => `${p.project}:${p.requests}`),
    ["djinn:1", "dolmen:2", ":1"],
  );
  // The Sankey: the busiest project first, its gates under it, outside any project last.
  const flows = s.sankeyFlows(s.holdsOf(shared.intervals, end), start);
  assert.deepEqual(flows.projects, [
    ["dolmen", 2],
    ["djinn", 1],
    ["", 1],
  ]);
  assert.deepEqual(
    flows.gates.map(([k]) => k),
    ["dolmen\tlint", "dolmen\tstack", "djinn\tlint", "\te2e"],
  );
  const html = s.renderToStaticMarkup(
    h(s.GatesTab, { initialHistory: shared }),
  );
  const names = [
    ...html.matchAll(/<span class="gates-flow-name">([^<]+)<\/span>/g),
  ].map(([, n]) => n);
  assert.deepEqual(names, [
    "Running",
    "djinn",
    "dolmen",
    "Outside any project",
  ]);
});

test("now, by project: a gate of the machine under its holder's project and each project waiting for it", () => {
  const gate = (name, holder, project, waitingProjects) => ({
    name,
    holder,
    holderTaskId: "",
    project,
    waiting: waitingProjects.map((p, i) => `W${i + 10}: ${p}`),
    waitingTaskIds: [],
    waitingProjects,
    takesSlot: false,
    ifrit: false,
  });
  const waits = (gate, project) => ({
    gate,
    project,
    holds: 3,
    p50Seconds: 0,
    p90Seconds: 1,
    maxSeconds: 2,
    long: 0,
    lastCommand: "true",
  });
  const groups = s.nowGroups(
    [
      gate("lint", "W1: lint", "djinn", ["dolmen", "djinn", "dolmen"]),
      gate("stack@dolmen", "", "", ["dolmen"]),
    ],
    [waits("lint", "dolmen"), waits("codegen", "dolmen"), waits("fmt", "zz")],
  );
  assert.deepEqual(
    groups.map((g) => ({
      project: g.project,
      live: g.live.map(
        (e) =>
          `${e.name}${e.declared ? "*" : ""} ${e.holds ? "holds" : "-"} ${e.waiting}`,
      ),
      free: g.free.map((w) => w.gate),
      held: g.held,
      waiting: g.waiting,
    })),
    [
      {
        project: "dolmen",
        live: ["lint - 0,2", "stack* - 0"],
        free: ["codegen"],
        held: 0,
        waiting: 3,
      },
      {
        project: "djinn",
        live: ["lint holds 1"],
        free: [],
        held: 1,
        waiting: 1,
      },
      { project: "zz", live: [], free: ["fmt"], held: 0, waiting: 0 },
    ],
  );
});

test("the commands read as the list scrolls: a page laid over those read before, each command once", () => {
  const row = (id) => ({ id, command: id });
  const ids = (p) => p.costs.map((c) => c.id).join(" ");
  let page = { costs: [], total: 0 };
  page = s.mergePage(page, 0, [row("a"), row("b")], 5);
  page = s.mergePage(page, 2, [row("c"), row("d")], 5);
  assert.equal(ids(page), "a b c d");
  // The first page read again: d climbed to the top; it shows once.
  page = s.mergePage(page, 0, [row("d"), row("a")], 5);
  assert.equal(ids(page), "d a c");
  // Fewer commands in all: the list is cut to them.
  page = s.mergePage(page, 0, [row("d")], 1);
  assert.equal(ids(page), "d");
});

// W160 (Q97): one gate busy, the others asked once or twice: their nodes are thin, but their labels keep a line apart.
test("the Sankey keeps a line between two nodes' labels, however thin the nodes", () => {
  const intervals = [];
  for (let i = 0; i < 60; i++)
    intervals.push(
      held(
        "lint",
        `W${i}`,
        K.WORK,
        3000 - i * 40,
        3000 - i * 40,
        2990 - i * 40,
      ),
    );
  for (const [g, n] of [
    ["e2e", 1],
    ["fmt", 2],
    ["paid", 1],
    ["stack", 1],
    ["codegen", 1],
  ])
    for (let i = 0; i < n; i++)
      intervals.push(
        held(g, `W${g}${i}`, K.WORK, 600 - i * 10, 600 - i * 10, 590 - i * 10, {
          project: "dolmen",
        }),
      );
  const html = s.renderToStaticMarkup(
    h(s.GatesTab, { initialHistory: { ...shared, intervals } }),
  );
  const sankey = html.slice(
    html.indexOf('class="statistics-card gates-card gates-sankey"'),
  );
  const texts = [
    ...sankey.matchAll(/<text x="([\d.]+)" y="([\d.]+)"[^>]*>([^<]+)<\/text>/g),
  ].map(([, x, y, text]) => ({ x: Number(x), y: Number(y), text }));
  assert.equal(texts.length, 2 + 6 + 1);
  // By column, the labels top to bottom: each a line (18 px) under the one above, and inside the plot.
  const height = Number(sankey.match(/<svg[^>]* height="(\d+)"/)[1]);
  const columns = Map.groupBy(texts, (t) => t.x);
  for (const column of columns.values())
    for (let i = 0; i < column.length; i++) {
      assert.ok(column[i].y - 12 >= 0 && column[i].y <= height, column[i].text);
      if (i > 0)
        assert.ok(
          column[i].y - column[i - 1].y >= 18 - 1e-9,
          `${column[i - 1].text} / ${column[i].text}`,
        );
    }
});

test("the frieze's readout goes over the pointer when what shows has no room below it", () => {
  // The tracks start at 100 px; what shows runs from 40 px to 500 px (the terminal panel below).
  const area = { top: 40, bottom: 500 };
  // Room below: 14 px under the pointer.
  assert.equal(s.readoutTop(100, 100, 120, area), 114);
  // A band at the bottom of what shows: the readout over the pointer, whole above it.
  assert.equal(s.readoutTop(380, 100, 120, area), 380 - 14 - 120);
  // Room neither below nor above: its top in view, as low as it goes.
  assert.equal(s.readoutTop(100, 100, 500, area), 40 + 4 - 100);
});

// W194 (L42 C): each gate of Now is a seal, the door in the loader's octagon; its state reads from the gate.
test("now, each gate a seal: free open, held shut, waited for with a flame per waiter, heavy barred", () => {
  const gate = (name, extra) => ({
    ...live[0],
    name,
    waiting: [],
    waitingTaskIds: [],
    waitingProjects: [],
    ...extra,
  });
  const html = s.renderToStaticMarkup(
    h(s.GatesTab, {
      tasks,
      initialGates: [
        gate("held"),
        gate("waited", {
          waiting: ["W2 (Test): go tool task lint", "W2 (Test): again"],
          waitingTaskIds: ["task-w2", "task-w2"],
          waitingProjects: ["djinn", "djinn"],
        }),
        gate("heavy", { takesSlot: true }),
        gate("crowded", {
          waiting: Array(7).fill("a holder outside any task"),
          waitingProjects: Array(7).fill("djinn"),
        }),
      ],
      initialSummary: summary,
    }),
  );
  const cards = Object.fromEntries(
    [
      ...html.matchAll(
        /<li id="gate-([\w-]+)" class="gates-gate[^"]*" data-state="(\w+)"><div class="gates-seal" aria-hidden="true">([\s\S]*?)<\/div><div class="gates-gate-head">/g,
      ),
    ].map(([, name, state, seal]) => [
      name,
      `${state} ${seal.match(/class="gates-seal-flame"/g)?.length ?? 0}`,
    ]),
  );
  // Five flames at most: the queue's count says the rest.
  assert.deepEqual(cards, {
    held: "held 0",
    waited: "wait 2",
    heavy: "heavy 0",
    crowded: "wait 5",
    lint: "free 0",
    test: "free 0",
  });
  assert.match(html, /takes a worker&#x27;s slot/);
});

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
    takesSlot: false,
    ifrit: false,
  },
];

test("the waits by gate: a median, a 90th percentile, the longest and those over 5 s, over the recent runs", () => {
  const waits = s.gateWaits(costs);
  assert.deepEqual(
    waits.map(({ gate, holds, p50, p90, max, long }) => ({
      gate,
      holds,
      p50,
      p90,
      max,
      long,
    })),
    [
      { gate: "lint", holds: 5, p50: 0, p90: 12, max: 12, long: 1 },
      { gate: "test", holds: 2, p50: 0, p90: 7, max: 7, long: 1 },
    ],
  );
  // The newest run under a gate is its last hold.
  assert.equal(waits[0].last.run.waitSeconds, 12);
});

test("the Gates tab: its figures, the gates now with who holds and who waits, the last hour, the commands by total time, the waits by gate", () => {
  const html = s.renderToStaticMarkup(
    h(s.GatesTab, {
      tasks,
      onWorker: () => {},
      onTask: () => {},
      initialGates: live,
      initialCommands: costs,
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

  // Now: lint held by W1, a link to its row, two waiting, W2 named; test free, its last hold said.
  const lint = html.slice(
    html.indexOf('id="gate-lint"'),
    html.indexOf('id="gate-test"'),
  );
  assert.match(lint, /class="gates-gate is-held"/);
  assert.match(
    lint,
    /<button type="button" class="text-link gates-task-link agent-code" title="Lint">W1<\/button> W1 \(Lint\): go tool task lint, for /,
  );
  assert.match(
    text(lint),
    /2 waiting: W2 W2 \(Test\): go tool task lint a holder outside any task/,
  );
  const free = html.slice(html.indexOf('id="gate-test"'));
  assert.match(free, /<span class="gates-gate-state">free<\/span>/);
  assert.match(
    text(free),
    /last held \d\d\/\d\d [^·]+: go tool task test · W2 · 1 GB/,
  );

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
  assert.match(
    html,
    /<button type="button" class="gates-command-open" aria-expanded="false" disabled="">/,
  );

  // The waits by gate.
  const waits = [
    ...html.matchAll(/<tr class="gates-wait-row">([\s\S]*?)<\/tr>/g),
  ].map(([, row]) => text(row));
  assert.deepEqual(waits, ["lint 5 0s 12s 12s 1", "test 2 0s 7s 7s 1"]);
});

test("the Gates tab says when nothing is known yet", () => {
  const html = s.renderToStaticMarkup(h(s.GatesTab, {}));
  assert.match(html, /No gate is held or waited for/);
  assert.match(html, /No gate was asked for over this period\./);
  assert.match(html, /No named gate was asked for over this period\./);
  assert.match(html, /No command has run under a gate yet\./);
  assert.match(html, /No run under a gate measured yet\./);
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
  assert.deepEqual(now.holding, [["test", ["W2"]]]);
  assert.deepEqual(
    now.waiting.map((h) => h.iv.taskCode),
    ["W3"],
  );
});

test("the Sankey counts the named gates' requests: kind to gate to wait, never the admissions", () => {
  const flows = s.sankeyFlows(s.holdsOf(history.intervals, end), start);
  assert.equal(flows.total, 4);
  assert.deepEqual(flows.groups, [
    ["work", 1],
    ["inv", 1],
    ["mock", 1],
    ["out", 1],
  ]);
  assert.deepEqual(flows.gates, [
    ["lint", 2],
    ["test", 2],
  ]);
  assert.deepEqual(flows.outcomes, [
    ["now", 2],
    ["long", 2],
  ]);
  assert.ok(!flows.toGate.some((f) => f.to === s.ADMISSIONS));
  assert.deepEqual(
    flows.toOutcome.map((f) => `${f.from}>${f.to}:${f.count}`).sort(),
    ["lint>long:1", "lint>now:1", "test>long:1", "test>now:1"],
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
  // One row per named gate, its bands in their kind's colour, the task's code in the hold; no lane for the admissions.
  const rows = [
    ...flow.matchAll(
      /<div class="gates-row" role="list" aria-label="([\w-]+)"/g,
    ),
  ].map(([, g]) => g);
  assert.deepEqual(rows, ["lint", "test"]);
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
    /<code class="gates-flow-name">test<\/code><span class="muted-text">2 requests · 25m<\/span>/,
  );
  // The strip: the stacks by kind and the queue's line.
  assert.match(flow, /<path class="gk-watch" d="M/);
  assert.match(flow, /<path class="gates-strip-queue" d="M0,/);
  // The Sankey: four requests, its nodes the kinds, the named gates and the waits, the admissions never.
  assert.match(sankey, /The 4 requests for a named gate/);
  const nodes = [
    ...sankey.matchAll(
      /<g class="gates-sankey-node [^"]+">.*?<text[^>]*>([^<]+)<\/text><\/g>/g,
    ),
  ].map(([, n]) => n);
  assert.deepEqual(nodes, [
    "Workers 1",
    "Investigations 1",
    "Mockups 1",
    "Outside a task 1",
    "lint 2",
    "test 2",
    "at once (&lt; 1 s) 2",
    "waits over 5 s 2",
  ]);
  const svg = sankey.slice(sankey.indexOf("<svg"), sankey.indexOf("</svg>"));
  assert.doesNotMatch(svg, /commands/);
  assert.match(svg, /<title>Mockups → test: 1 request \(25%\)<\/title>/);
});

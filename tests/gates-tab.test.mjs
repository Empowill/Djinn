// The Gates tab of the Hourglass (src/gates-tab.tsx): the gates now, the last hour, the commands admitted under a gate
// and the waits by gate. Run with `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { resources, screens } from "./screens.mjs";

const { s, h } = await screens();
const { at, t0, t3, resourceData, resourceCost, hourglassDjinn } = resources(s);
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
      initialSeries: resourceData(),
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

  // The last hour: the band of lint, its wait and its hold said.
  assert.match(
    html,
    /aria-label="Gate lint \(djinn\): go tool task lint\. Waited 6m40s, held 13m20s\."/,
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
  assert.match(html, /No gate was held in the last hour\./);
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

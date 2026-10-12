// The grouping of the commands in the resources panel, the merge of the series read since a point, and the scales of
// its charts. Run with `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { bundle } from "./bundle.mjs";

const s = await bundle(
  "commands",
  `export { commandGroup, groupCommands, mean } from "@/src/data/commands.ts";
export { mergeSeries, lastPointTime } from "@/src/data/series.ts";
export { scale, timeTicks, tickLabels, nearest } from "@/src/resource-chart.tsx";
export { timestampFromMs } from "@bufbuild/protobuf/wkt";`,
);

const group = (command) => {
  const { gate, head, args, key } = s.commandGroup(command);
  return { gate, head, args, key };
};

test("e2e alone is the whole suite; e2e with a spec is another line, the spec its argument", () => {
  assert.deepEqual(group("go tool task e2e"), {
    gate: "",
    head: "go tool task e2e",
    args: "",
    key: "go tool task e2e",
  });
  assert.deepEqual(group("go tool task e2e -- e2e/x.spec.ts"), {
    gate: "",
    head: "go tool task e2e",
    args: "-- e2e/x.spec.ts",
    key: "go tool task e2e …",
  });
  assert.equal(
    group("go tool task e2e -- e2e/y.spec.ts").key,
    group("go tool task e2e -- e2e/x.spec.ts").key,
  );
});

test("go test stops at its first flag or package", () => {
  assert.deepEqual(group("go test -tags headless ./internal/machine/"), {
    gate: "",
    head: "go test",
    args: "-tags headless ./internal/machine/",
    key: "go test …",
  });
  assert.equal(group("go test ./...").key, "go test …");
  assert.equal(
    group("go test -run TestCosts ./internal/machine").head,
    "go test",
  );
  assert.equal(group("go vet ./cmd/djinn").head, "go vet");
});

test("npm scripts keep their script name; their arguments fold in", () => {
  assert.equal(group("npm test").key, "npm test");
  assert.equal(group("npm ci").key, "npm ci");
  assert.equal(group("npm run build").key, "npm run build");
  assert.deepEqual(group("npm run lint -- --fix"), {
    gate: "",
    head: "npm run lint",
    args: "-- --fix",
    key: "npm run lint …",
  });
  assert.equal(
    group("npx playwright test --config e2e/playwright.config.ts").head,
    "npx playwright test",
  );
});

test("djinn gate run is unwrapped: its gate named, its command grouped", () => {
  assert.deepEqual(group("djinn gate run lint -- go tool task lint"), {
    gate: "lint",
    head: "go tool task lint",
    args: "",
    key: "go tool task lint",
  });
  assert.deepEqual(
    group(
      "/home/me/go/bin/djinn gate run e2e -- go tool task e2e -- e2e/a.spec.ts",
    ),
    {
      gate: "e2e",
      head: "go tool task e2e",
      args: "-- e2e/a.spec.ts",
      key: "go tool task e2e …",
    },
  );
});

test("a path, an assignment, a quote or a file name is an argument; four words at most; case is ignored", () => {
  assert.equal(group("./scripts/check.sh --fast").head, "./scripts/check.sh");
  // A word after the tool that is no argument is a subcommand, as for go or npm.
  assert.equal(group("./scripts/check.sh fast").key, "./scripts/check.sh fast");
  assert.equal(group("sh -c 'go tool task ui && npx vite build'").head, "sh");
  assert.equal(group("make test GOFLAGS=-race").head, "make test");
  assert.equal(group("node build.mjs").head, "node");
  assert.equal(
    group("echo one two three four five").head,
    "echo one two three",
  );
  assert.equal(group("Go Tool Task Lint").key, "go tool task lint");
  assert.equal(group("  go   test  ").key, "go test");
});

const run = (wait, held, gate) => ({
  waitSeconds: wait,
  heldSeconds: held,
  gate,
});
const cost = (projectId, command, extra = {}) => ({
  projectId,
  projectName: projectId === "p1" ? "djinn" : "other",
  command,
  runs: 0,
  asked: 0,
  seconds: 0,
  gate: "",
  p50WaitSeconds: 0,
  p50HeldSeconds: 0,
  recentRuns: [],
  ...extra,
});

test("the costs group by project, gate and command, the most time first; only the projects asked show", () => {
  const costs = [
    cost("p1", "go tool task e2e -- e2e/a.spec.ts", {
      runs: 2,
      asked: 3,
      seconds: 60,
      gate: "e2e",
      recentRuns: [run(10, 61, "e2e"), run(30, 61, "e2e")],
    }),
    cost("p1", "go tool task e2e -- e2e/b.spec.ts", {
      runs: 1,
      asked: 1,
      seconds: 30,
      gate: "e2e",
      recentRuns: [run(0, 31, "e2e")],
    }),
    cost("p1", "go tool task e2e", {
      runs: 1,
      asked: 1,
      seconds: 600,
      gate: "e2e",
      recentRuns: [run(5, 601, "e2e")],
    }),
    // Recorded before asks were counted: asked is its runs.
    cost("p1", "go tool task lint", {
      runs: 4,
      seconds: 20,
      gate: "lint",
      recentRuns: [run(1, 21), run(1, 21), run(1, 21), run(1, 21)],
    }),
    cost("p2", "npm test", { runs: 9, seconds: 999, gate: "test" }),
  ];
  const projects = s.groupCommands(costs, new Set(["p1"]));
  assert.equal(projects.length, 1, "the project of no active wish is left out");
  const [p] = projects;
  assert.equal(p.name, "djinn");
  assert.equal(p.seconds, 2 * 60 + 30 + 600 + 4 * 20);
  assert.deepEqual(
    p.gates.map((g) => g.gate),
    ["e2e", "lint"],
  );
  const [e2e, lint] = p.gates;
  assert.deepEqual(
    e2e.commands.map((c) => c.key),
    ["go tool task e2e", "go tool task e2e …"],
  );
  const specs = e2e.commands[1];
  assert.equal(specs.asked, 4);
  assert.equal(specs.ran, 3);
  assert.equal(specs.seconds, 150);
  assert.equal(specs.waitSeconds, 40);
  assert.equal(specs.heldSeconds, 153);
  assert.deepEqual(specs.variants, ["-- e2e/a.spec.ts", "-- e2e/b.spec.ts"]);
  assert.equal(s.mean(specs.seconds, specs.ran), 50);
  assert.equal(e2e.asked, 5);
  assert.equal(e2e.ran, 4);
  assert.equal(e2e.seconds, 750);
  assert.equal(lint.asked, 4);
  assert.equal(lint.waitSeconds, 4);
  assert.equal(s.mean(0, 0), 0);
  assert.equal(
    s.groupCommands(costs).length,
    2,
    "all projects without a filter",
  );
});

const ts = (ms) => s.timestampFromMs(ms);
const series = (machine, workers) => ({
  machine: {
    cores: 8,
    memoryTotalBytes: 16n << 30n,
    points: machine.map((ms) => ({ time: ts(ms), cpuPercent: ms / 1000 })),
  },
  workers: workers.map(([taskId, points, extra = {}]) => ({
    taskId,
    title: taskId,
    gates: [],
    peakCpuPercent: 0,
    peakMemoryBytes: 0n,
    paused: false,
    points: points.map((ms) => ({ time: ts(ms), cpuPercent: 1 })),
    ...extra,
  })),
  gates: [],
  workerMeasure: "",
});

test("the series read since a point append to the range; the oldest leave it; a worker with nothing new stays the same", () => {
  const was = series(
    [1000, 6000, 11000],
    [
      ["w1", [6000, 11000]],
      ["w2", [11000]],
    ],
  );
  assert.equal(Number(s.lastPointTime(was).seconds), 11);
  const next = series(
    [16000],
    [
      ["w1", [16000]],
      ["w2", []],
      ["w3", [16000]],
    ],
  );
  const merged = s.mergeSeries(was, next, 5000);
  assert.deepEqual(
    merged.machine.points.map((p) => Number(p.time.seconds)),
    [6, 11, 16],
  );
  assert.deepEqual(
    merged.workers.map((w) => [w.taskId, w.points.length]),
    [
      ["w1", 3],
      ["w2", 1],
      ["w3", 1],
    ],
  );
  assert.equal(merged.workers[1], was.workers[1], "w2 got nothing new");
  // A worker that ended is gone; a point read twice is kept once.
  const again = s.mergeSeries(merged, series([16000], [["w1", [16000]]]), 5000);
  assert.deepEqual(
    again.workers.map((w) => [w.taskId, w.points.length]),
    [["w1", 3]],
  );
  assert.equal(again.machine.points.length, 3);
});

test("the charts' scales: CPU by halves of 100% or more, memory in four steps at most, times on the local clock", () => {
  assert.deepEqual(s.scale("cpu", 40), { yMax: 100, ticks: [0, 50, 100] });
  assert.deepEqual(s.scale("cpu", 130), { yMax: 150, ticks: [0, 75, 150] });
  const gib = 1024 ** 3;
  assert.deepEqual(s.scale("memory", 16 * gib), {
    yMax: 16 * gib,
    ticks: [0, 4 * gib, 8 * gib, 12 * gib, 16 * gib],
  });
  assert.deepEqual(
    s.scale("memory", 300 * 1024 ** 2).ticks.map((v) => v / 1024 ** 2),
    [0, 128, 256, 384],
  );

  const day = new Date(2026, 9, 12, 14, 7, 30).getTime();
  const half = s.timeTicks(day - 30 * 60_000, day);
  assert.ok(
    half.length >= 3 && half.length <= 5,
    `30 min: ${half.length} ticks`,
  );
  assert.ok(half.every((at) => new Date(at).getMinutes() % 10 === 0));
  const week = s.timeTicks(day - 7 * 24 * 3600_000, day);
  assert.ok(week.every((at) => new Date(at).getHours() === 0));
  // A range across midnight says the date on the first tick of each day.
  const late = new Date(2026, 9, 12, 23, 0).getTime();
  const across = s.timeTicks(late, late + 2 * 3600_000);
  const labels = s.tickLabels(across, late, late + 2 * 3600_000);
  assert.match(labels[0], /12/);
  assert.equal(
    labels.filter((l) => /13/.test(l)).length,
    1,
    `the 13th once: ${labels}`,
  );
  assert.equal(s.nearest([{ t: 0 }, { t: 10 }, { t: 20 }], 14), 1);
  assert.equal(s.nearest([{ t: 0 }, { t: 10 }, { t: 20 }], 16), 2);
  assert.equal(s.nearest([], 3), -1);
});

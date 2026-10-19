// The Tasks tab's tree (src/task-tree.tsx), its dates' column (src/data/format.ts rowStamps), and the switch that
// chooses it, kept across wishes and restarts (src/task-tabs.tsx). Run with `go tool task test-ui`. Node reports
// English; the rendered tree reads the time zone of Paris, set before anything formats a date.
import test from "node:test";
import assert from "node:assert/strict";

import { bundle } from "./bundle.mjs";

process.env.TZ = "Europe/Paris";

const s = await bundle(
  "task-tree",
  `export { createElement } from "react";
export { renderToStaticMarkup } from "react-dom/server";
export { TaskTree } from "@/src/task-tree.tsx";
export { chosenLayout, LayoutToggle, setLayout } from "@/src/task-tabs.tsx";
export { rowStamps, stamp, taskDates } from "@/src/data/format.ts";
export * from "@/gen/ts/plan/v1/plan_pb.ts";`,
);

const h = s.createElement;
const at = (seconds) => ({ seconds: BigInt(seconds), nanos: 0 });
const task = (code, extra = {}) => ({
  id: `id-${code}`,
  code,
  title: `Task ${code}`,
  status: s.TaskStatus.DONE,
  kind: s.TaskKind.UNSPECIFIED,
  partOf: "",
  forkOf: "",
  dependsOn: [],
  projectId: "",
  ...extra,
});

// storage is a page's localStorage, in memory; refusing, one that throws, as a private window's may.
function storage(refusing = false) {
  const items = new Map();
  const no = () => {
    throw new Error("SecurityError");
  };
  return {
    items,
    getItem: refusing ? no : (k) => items.get(k) ?? null,
    setItem: refusing ? no : (k, v) => items.set(k, String(v)),
    removeItem: refusing ? no : (k) => items.delete(k),
  };
}

test("the tree is not the default; chosen, it is kept in the page's storage and read back after a restart", () => {
  const kept = storage();
  globalThis.window = { localStorage: kept };
  try {
    assert.equal(s.chosenLayout(), "list");
    s.setLayout("tree");
    assert.equal(kept.items.get("djinn.tasks.layout"), "tree");
    // A restart reads the storage again.
    assert.equal(s.chosenLayout(), "tree");
    s.setLayout("list");
    assert.equal(kept.items.has("djinn.tasks.layout"), false);
    assert.equal(s.chosenLayout(), "list");
    // Storage refused: the list, and no error.
    globalThis.window = { localStorage: storage(true) };
    assert.equal(s.chosenLayout(), "list");
    s.setLayout("tree");
  } finally {
    delete globalThis.window;
  }
});

test("the switch offers the list and the tree, the list pressed by default", () => {
  const html = s.renderToStaticMarkup(h(s.LayoutToggle));
  assert.match(html, /role="group" aria-label="How the tasks show"/);
  assert.match(html, /aria-pressed="true" aria-label="List: [^"]*"/);
  assert.match(html, /aria-pressed="false" aria-label="Tree: [^"]*"/);
});

test("the tree draws every task, finished ones included, the oldest at the bottom and the planned at the top", () => {
  const tasks = [
    task("T1", { kind: s.TaskKind.AZIMA, status: s.TaskStatus.PENDING }),
    task("W3", { status: s.TaskStatus.PENDING, createTime: at(50) }),
    task("W1", { startTime: at(10), endTime: at(20) }),
    task("W2", {
      status: s.TaskStatus.CANCELLED,
      startTime: at(30),
      endTime: at(40),
      partOf: "id-T1",
    }),
    task("W4", {
      startTime: at(45),
      endTime: at(48),
      correction: { failure: { taskIds: ["id-W1"] } },
    }),
  ];
  const html = s.renderToStaticMarkup(
    h(s.TaskTree, {
      tasks,
      projects: new Map(),
      codes: new Map(tasks.map((x) => [x.id, x.code])),
      focus: "",
      onOpen: () => {},
      onClose: () => {},
    }),
  );
  assert.match(
    html,
    /All the tasks, oldest at the bottom<span class="count">4<\/span>/,
  );
  // From the top of the page: the planned one, then the newest, down to the oldest. No row for the azima.
  const rows = [...html.matchAll(/data-task="(\w+)"/g)].map((m) => m[1]);
  const tops = Object.fromEntries(
    [
      ...html.matchAll(/style="top:([\d.]+)(?:px|rem)?" data-task="(\w+)"/g),
    ].map((m) => [m[2], Number(m[1])]),
  );
  assert.deepEqual(rows.sort(), ["W1", "W2", "W3", "W4"]);
  assert.ok(tops.W3 < tops.W4 && tops.W4 < tops.W2 && tops.W2 < tops.W1);
  // Its state's tone, its own muted one for the cancelled; Merge for the merge.
  assert.match(html, /class="tree-row tone-cancelled"[^>]*data-task="W2"/);
  assert.match(html, /class="tree-row tone-planned"[^>]*data-task="W3"/);
  assert.match(html, /data-task="W4"[^>]*>.*?class="merge-label"/);
  // No card until one is opened.
  assert.doesNotMatch(html, /task-tree-card/);
});

test("a task opened shows its card beside the tree, with a way to close it", () => {
  const tasks = [task("W1", { startTime: at(10), endTime: at(20) })];
  const html = s.renderToStaticMarkup(
    h(s.TaskTree, {
      tasks,
      projects: new Map(),
      codes: new Map(),
      focus: "id-W1",
      card: h("article", { className: "the-card" }, "W1's card"),
      onOpen: () => {},
      onClose: () => {},
    }),
  );
  assert.match(html, /class="tree-row tone-done focused"/);
  assert.match(
    html,
    /<aside class="task-tree-card">.*aria-label="Close".*the-card/,
  );
  const mockupTree = s.renderToStaticMarkup(
    h(s.TaskTree, {
      tasks: [
        task("W444", {
          status: s.TaskStatus.RUNNING,
          kind: s.TaskKind.MOCKUP,
          startTime: at(30),
        }),
      ],
      projects: new Map(),
      codes: new Map(),
      focus: "",
      onOpen: () => {},
      onClose: () => {},
    }),
  );
  assert.match(mockupTree, /class="tree-row tone-mockup"[^>]*data-task="W444"/);
  assert.match(mockupTree, /data-task="W444"[^>]*>.*?class="mockup-label"/);
});

// iso is a moment as a Date, and as a task's Timestamp.
const iso = (text) => new Date(text);
const ts = (text) => at(Date.parse(text) / 1000);

test("the dates' column: rows of the same day say their hour to the second, the day only on the top one", () => {
  assert.deepEqual(
    s.rowStamps(
      [
        iso("2026-10-17T08:42:07Z"),
        iso("2026-10-17T08:40:55Z"),
        iso("2026-10-17T06:01:00Z"),
      ],
      "fr",
      "Europe/Paris",
    ),
    ["17/10 10:42:07", "10:40:55", "08:01:00"],
  );
  // In English, the month first and the hour as English writes it.
  const [top, next] = s.rowStamps(
    [iso("2026-10-17T08:42:07Z"), iso("2026-10-17T08:40:55Z")],
    "en",
    "Europe/Paris",
  );
  assert.match(top, /^10\/17 10:42:07\sAM$/);
  assert.match(next, /^10:40:55\sAM$/);
});

test("the dates' column: a row says its day again when it is another than the row above's", () => {
  assert.deepEqual(
    s.rowStamps(
      [
        iso("2026-10-17T08:42:07Z"),
        iso("2026-10-16T21:59:58Z"),
        iso("2026-10-16T09:00:00Z"),
        undefined,
        iso("2026-10-15T09:00:00Z"),
        // Back to a day seen higher up: said again, the row above's is another.
        iso("2026-10-16T09:30:00Z"),
      ],
      "fr",
      "Europe/Paris",
    ),
    [
      "17/10 10:42:07",
      "16/10 23:59:58",
      "11:00:00",
      "",
      "15/10 11:00:00",
      "16/10 11:30:00",
    ],
  );
});

test("the dates' column: the day changes at the time zone's midnight, not at UTC's", () => {
  // Two seconds apart, around midnight in Paris (UTC+2 in October): one UTC day, two in Paris.
  const rows = [iso("2026-10-16T22:00:01Z"), iso("2026-10-16T21:59:59Z")];
  assert.deepEqual(s.rowStamps(rows, "fr", "Europe/Paris"), [
    "17/10 00:00:01",
    "16/10 23:59:59",
  ]);
  assert.deepEqual(s.rowStamps(rows, "fr", "UTC"), [
    "16/10 22:00:01",
    "21:59:59",
  ]);
  assert.deepEqual(s.rowStamps(rows, "fr", "America/New_York"), [
    "16/10 18:00:01",
    "17:59:59",
  ]);
  // Winter time: Paris is UTC+1 in January.
  assert.equal(
    s.stamp(iso("2027-01-10T12:00:00Z"), "fr", "Europe/Paris"),
    "10/01 13:00:00",
  );
});

test("a row's tooltip says which moment its date is, with its end, to the second", () => {
  const zone = (t) => s.taskDates(t, "fr", "Europe/Paris");
  assert.equal(
    zone(
      task("W1", {
        createTime: ts("2026-10-17T08:30:00Z"),
        firstStartTime: ts("2026-10-17T08:42:07Z"),
        startTime: ts("2026-10-17T09:10:00Z"),
        endTime: ts("2026-10-17T09:20:31Z"),
      }),
    ),
    "Started 17/10 10:42:07 · ended 17/10 11:20:31",
  );
  // Running: started, no end yet.
  assert.equal(
    zone(
      task("W2", {
        status: s.TaskStatus.RUNNING,
        createTime: ts("2026-10-17T08:30:00Z"),
        startTime: ts("2026-10-17T08:42:07Z"),
      }),
    ),
    "Started 17/10 10:42:07",
  );
  assert.equal(
    zone(
      task("W3", {
        status: s.TaskStatus.PENDING,
        createTime: ts("2026-10-17T08:30:00Z"),
      }),
    ),
    "Created 17/10 10:30:00, not started yet",
  );
  assert.equal(
    zone(
      task("W4", {
        status: s.TaskStatus.CANCELLED,
        createTime: ts("2026-10-16T08:30:00Z"),
        endTime: ts("2026-10-17T08:30:00Z"),
      }),
    ),
    "Created 16/10 10:30:00, never started · ended 17/10 10:30:00",
  );
});

test("the tree shows each row's date left of the lanes: its start, or its creation when it never started", () => {
  const tasks = [
    task("W1", {
      createTime: ts("2026-10-16T08:00:00Z"),
      startTime: ts("2026-10-16T08:10:05Z"),
      endTime: ts("2026-10-16T08:20:00Z"),
    }),
    task("W2", {
      createTime: ts("2026-10-16T08:00:00Z"),
      startTime: ts("2026-10-17T07:00:09Z"),
      endTime: ts("2026-10-17T07:30:00Z"),
    }),
    task("W3", {
      createTime: ts("2026-10-17T07:15:00Z"),
      startTime: ts("2026-10-17T07:20:00Z"),
      endTime: ts("2026-10-17T07:40:00Z"),
    }),
    task("W4", {
      status: s.TaskStatus.PENDING,
      createTime: ts("2026-10-17T08:00:30Z"),
    }),
  ];
  const html = s.renderToStaticMarkup(
    h(s.TaskTree, {
      tasks,
      projects: new Map(),
      codes: new Map(),
      focus: "",
      onOpen: () => {},
      onClose: () => {},
    }),
  );
  const dates = Object.fromEntries(
    [
      ...html.matchAll(
        /data-task="(\w+)"[^>]*><span class="tree-date">([^<]*)<\/span>/g,
      ),
    ].map((m) => [m[1], m[2]]),
  );
  // From the top: the planned one (its creation), then down to the oldest; the day said where it changes.
  assert.match(dates.W4, /^10\/17 10:00:30\sAM$/);
  assert.match(dates.W3, /^09:20:00\sAM$/);
  assert.match(dates.W2, /^09:00:09\sAM$/);
  assert.match(dates.W1, /^10\/16 10:10:05\sAM$/);
  // The column is as wide as its longest date, in rem; the lanes and the labels start past it.
  assert.match(html, /--tree-dates:[\d.]+rem/);
});

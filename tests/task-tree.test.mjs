// The Tasks tab's tree (src/task-tree.tsx) and the switch that chooses it, kept across wishes and restarts
// (src/task-tabs.tsx). Run with `go tool task test-ui`. Node reports English.
import test from "node:test";
import assert from "node:assert/strict";

import { bundle } from "./bundle.mjs";

const s = await bundle(
  "task-tree",
  `export { createElement } from "react";
export { renderToStaticMarkup } from "react-dom/server";
export { TaskTree } from "@/src/task-tree.tsx";
export { chosenLayout, LayoutToggle, setLayout } from "@/src/task-tabs.tsx";
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
});

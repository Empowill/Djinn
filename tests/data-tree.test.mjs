// The tree of a wish's tasks (src/data/tree.ts): the order of its rows, its lanes, branches and links. Run with
// `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { bundle } from "./bundle.mjs";

const f = await bundle(
  "tree",
  `export * from "@/src/data/tree.ts";
export * from "@/gen/ts/plan/v1/plan_pb.ts";`,
);

const at = (seconds) => ({ seconds: BigInt(seconds), nanos: 0 });
let made = 0;
const task = (code, extra = {}) => ({
  id: `id-${code}`,
  code,
  title: `Task ${code}`,
  status: f.TaskStatus.DONE,
  kind: f.TaskKind.UNSPECIFIED,
  partOf: "",
  forkOf: "",
  dependsOn: [],
  createTime: at(++made),
  ...extra,
});
const ran = (code, start, end, extra = {}) =>
  task(code, { startTime: at(start), endTime: at(end), ...extra });
const codes = (layout) => layout.nodes.map((n) => n.task.code);

test("the rows go bottom up by start time, finished ones included, the planned ones last by creation", () => {
  const tasks = [
    task("W5", { status: f.TaskStatus.PENDING, createTime: at(50) }),
    ran("W2", 20, 30),
    task("W4", { status: f.TaskStatus.PENDING, createTime: at(40) }),
    ran("W1", 10, 15, { status: f.TaskStatus.FAILED }),
    ran("W3", 25, 0, { status: f.TaskStatus.RUNNING, endTime: undefined }),
    // Cancelled before it ever ran: where it ended.
    task("W6", {
      status: f.TaskStatus.CANCELLED,
      endTime: at(22),
      createTime: at(5),
    }),
  ];
  const layout = f.treeLayout(tasks);
  assert.deepEqual(codes(layout), ["W1", "W2", "W6", "W3", "W4", "W5"]);
  assert.deepEqual(
    layout.nodes.map((n) => n.row),
    [0, 1, 2, 3, 4, 5],
  );
  // The same list, the same layout.
  assert.equal(f.treeLayout(tasks), layout);
});

test("the azimas are no rows: their work is a group, on the lanes it takes like any work", () => {
  const azima = task("T1", {
    kind: f.TaskKind.AZIMA,
    status: f.TaskStatus.PENDING,
  });
  const layout = f.treeLayout([
    azima,
    ran("W1", 10, 20),
    ran("W2", 30, 40, { partOf: azima.id }),
    ran("W3", 50, 60, { partOf: azima.id }),
    ran("W4", 70, 80),
  ]);
  assert.deepEqual(codes(layout), ["W1", "W2", "W3", "W4"]);
  assert.deepEqual(
    layout.nodes.map((n) => n.group),
    ["", azima.id, azima.id, ""],
  );
  // One after the other: all on the trunk.
  assert.deepEqual(
    layout.nodes.map((n) => n.lane),
    [0, 0, 0, 0],
  );
  assert.equal(layout.lanes, 1);
});

test("work running beside work opens a side lane where it starts, and closes it where it ends", () => {
  const layout = f.treeLayout([
    ran("W1", 10, 100),
    ran("W2", 20, 70),
    ran("W3", 30, 40),
    ran("W4", 50, 60),
    ran("W5", 200, 210),
    ran("W6", 205, 220),
  ]);
  const by = Object.fromEntries(layout.nodes.map((n) => [n.task.code, n]));
  // W1 still ran when W2, W3 and W4 started: its lane holds up to W4's row.
  assert.equal(by.W1.lane, 0);
  assert.equal(by.W1.top, 3);
  assert.equal(by.W2.lane, 1);
  assert.equal(by.W2.from, 0);
  assert.equal(by.W2.top, 3);
  // W3 opens beside W2, and closes back into it.
  assert.equal(by.W3.lane, 2);
  assert.equal(by.W3.from, 1);
  assert.equal(by.W3.to, 1);
  // W4 takes the lane W3 gave back.
  assert.equal(by.W4.lane, 2);
  // Once they ended, the smoke narrows back to the trunk.
  assert.equal(by.W5.lane, 0);
  assert.equal(by.W6.lane, 1);
  assert.equal(by.W6.from, 0);
  assert.equal(layout.lanes, 3);
});

test("a running task holds its lane up to the last task that started", () => {
  const layout = f.treeLayout([
    ran("W1", 10, 0, { status: f.TaskStatus.RUNNING, endTime: undefined }),
    ran("W2", 20, 25),
    ran("W3", 30, 35),
    task("W4", { status: f.TaskStatus.PENDING, createTime: at(40) }),
  ]);
  assert.equal(layout.nodes[0].top, 2);
  assert.equal(layout.nodes[3].top, 3);
});

test("links join a task to what it waits for, what it was forked from, and the merge that brings it in", () => {
  const w1 = ran("W1", 10, 20);
  const w2 = ran("W2", 30, 40, {
    status: f.TaskStatus.INTERRUPTED,
    dependsOn: [w1.id],
  });
  const w3 = ran("W3", 50, 60, { forkOf: "W2" });
  const merge = ran("W4", 70, 80, {
    correction: { failure: { taskIds: [w3.id] } },
  });
  const planned = task("W5", {
    status: f.TaskStatus.PENDING,
    dependsOn: [w3.id, "elsewhere"],
  });
  const layout = f.treeLayout([planned, merge, w3, w2, w1]);
  assert.deepEqual(codes(layout), ["W1", "W2", "W3", "W4", "W5"]);
  assert.deepEqual(
    layout.links.map((l) => [l.from, l.to, l.kind]),
    [
      [0, 1, "depends"],
      [1, 2, "fork"],
      [2, 3, "merge"],
      [2, 4, "depends"],
    ],
  );
});

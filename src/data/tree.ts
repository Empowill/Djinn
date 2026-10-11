// The tree of a wish's tasks (src/task-tree.tsx), drawn bottom up like a git graph: every task of work, finished ones
// included, one row each, the oldest at the bottom, on the trunk; the planned ones at the top, the tips. Lanes are
// columns: the trunk (lane 0) holds the work part of no azima; an azima opens a branch beside it where its first part
// starts and closes it where its last part ends; work running beside other work of the same branch opens a side lane,
// closed where that work ends. A task's lane is held from its row up to the last task that started while it ran.
import { type Timestamp, timestampMs } from "@bufbuild/protobuf/wkt";

import type { Task } from "../../gen/ts/plan/v1/plan_pb";
import { compareCodes, isAzima, mergeNest } from "./flight";
import { taskFinished } from "./format";

// TreeNode is a task in the tree. Rows count from the bottom: 0 is the oldest.
export interface TreeNode {
  task: Task;
  row: number;
  lane: number;
  // The last row its lane holds: the last task that started while it ran; its own row when it never ran.
  top: number;
  // The lane its lane opened from, its branch's; its own lane when it sits on it.
  from: number;
}

// TreeBranch is an azima's branch: its lane, from the row of its first part to the top of its last one.
export interface TreeBranch {
  azima: Task;
  lane: number;
  from: number;
  to: number;
}

// TreeLink joins two rows: a task and what it waits for (depends), was forked from (fork), or the merge that brings
// its work in (merge). From the older row to the newer one, as they are drawn.
export interface TreeLink {
  from: number;
  to: number;
  kind: "depends" | "fork" | "merge";
}

export interface TreeLayout {
  nodes: TreeNode[];
  branches: TreeBranch[];
  links: TreeLink[];
  // How many lanes the widest row needs.
  lanes: number;
}

const ms = (ts?: Timestamp) => (ts ? timestampMs(ts) : 0);

// moment is where a task sits in time: when it first started; one finished without ever starting (cancelled), when it
// ended; 0 for one that has not started yet, planned.
function moment(task: Task): number {
  const begun = ms(task.firstStartTime) || ms(task.startTime);
  if (begun) return begun;
  return taskFinished(task.status)
    ? ms(task.endTime) || ms(task.createTime) || 1
    : 0;
}

const layouts = new WeakMap<readonly Task[], TreeLayout>();

// treeLayout lays out a wish's tasks as a tree (the file's comment); the same list gives the same answer.
export function treeLayout(tasks: readonly Task[]): TreeLayout {
  const known = layouts.get(tasks);
  if (known) return known;
  const azimas = new Map(tasks.filter(isAzima).map((x) => [x.id, x]));
  const work = tasks.filter((x) => !isAzima(x));
  const started = work
    .filter((x) => moment(x) > 0)
    .sort((a, b) => moment(a) - moment(b) || compareCodes(a.code, b.code));
  const planned = work
    .filter((x) => moment(x) === 0)
    .sort(
      (a, b) =>
        ms(a.createTime) - ms(b.createTime) || compareCodes(a.code, b.code),
    );
  const order = [...started, ...planned];
  const starts = started.map(moment);
  // above is the first started row whose moment is at or after t.
  const above = (t: number) => {
    let lo = 0;
    let hi = starts.length;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      if (starts[mid] < t) lo = mid + 1;
      else hi = mid;
    }
    return lo;
  };
  const tops = order.map((x, row) => {
    if (row >= started.length) return row;
    const end = taskFinished(x.status)
      ? ms(x.endTime) || moment(x)
      : Number.POSITIVE_INFINITY;
    return Math.max(row, Math.min(above(end), started.length) - 1);
  });
  const group = (x: Task) => (azimas.has(x.partOf) ? x.partOf : "");
  const spans = new Map<string, number>();
  order.forEach((x, row) =>
    spans.set(group(x), Math.max(spans.get(group(x)) ?? 0, tops[row])),
  );

  // held: the last row a task holds each lane; kept: the last row a branch keeps it. The trunk is never given away.
  const held: number[] = [];
  const kept: number[] = [Number.POSITIVE_INFINITY];
  const free = (lane: number, row: number) =>
    (held[lane] ?? -1) < row && (kept[lane] ?? -1) < row;
  const firstFree = (row: number, after: number) => {
    let lane = after + 1;
    while (!free(lane, row)) lane++;
    return lane;
  };
  const home = new Map<string, number>([["", 0]]);
  const branches: TreeBranch[] = [];
  const nodes: TreeNode[] = [];
  let lanes = 1;
  order.forEach((task, row) => {
    const g = group(task);
    let h = home.get(g);
    if (h === undefined || (kept[h] ?? -1) < row) {
      h = firstFree(row, 0);
      kept[h] = spans.get(g) ?? row;
      home.set(g, h);
      branches.push({ azima: azimas.get(g)!, lane: h, from: row, to: kept[h] });
    }
    const lane = (held[h] ?? -1) < row ? h : firstFree(row, h);
    held[lane] = tops[row];
    lanes = Math.max(lanes, lane + 1);
    nodes.push({ task, row, lane, top: tops[row], from: h });
  });

  const rowOf = new Map(order.map((x, row) => [x.id, row]));
  const byCode = new Map(order.map((x, row) => [x.code, row]));
  const links: TreeLink[] = [];
  const link = (a: number | undefined, b: number, kind: TreeLink["kind"]) => {
    if (a === undefined || a === b) return;
    links.push({ from: Math.min(a, b), to: Math.max(a, b), kind });
  };
  const host = mergeNest(tasks).host;
  order.forEach((x, row) => {
    for (const id of x.dependsOn ?? []) link(rowOf.get(id), row, "depends");
    if (x.forkOf) link(byCode.get(x.forkOf), row, "fork");
    const served = host.get(x.id);
    if (served) link(rowOf.get(served), row, "merge");
  });

  const layout = { nodes, branches, links, lanes };
  layouts.set(tasks, layout);
  return layout;
}

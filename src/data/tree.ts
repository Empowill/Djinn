// The tree of a wish's tasks (src/task-tree.tsx), drawn bottom up like smoke from the lamp: every task of work,
// finished ones included, one row each, the oldest at the bottom on the trunk (lane 0), the planned ones at the top,
// the tips. Lanes are columns: a task's lane is held from its row up to the last task that started while it ran, and a
// task takes the lowest lane free; so the drawing widens only where work runs side by side, and narrows back where it
// ends. A side lane opens from the nearest lane held below it and closes into the nearest one held above it. A lane
// kept for each azima (a git graph's branches) drew the real wish as a comb of parallel lines, its azimas living as
// long as the wish: an azima is a group of rows instead.
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
  // The lane its lane opened from, and the lane it closes into; its own lane, and 0, on the trunk.
  from: number;
  to: number;
  // The azima it is part of, by id; "" for none.
  group: string;
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

// treeLayout lays out a wish's tasks (the file's comment); the same list gives the same answer.
export function treeLayout(tasks: readonly Task[]): TreeLayout {
  const known = layouts.get(tasks);
  if (known) return known;
  const azimas = new Set(tasks.filter(isAzima).map((x) => x.id));
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

  // held: the last row a task holds each lane.
  const held: number[] = [];
  const nodes: TreeNode[] = [];
  let lanes = 1;
  order.forEach((task, row) => {
    let lane = 0;
    while ((held[lane] ?? -1) >= row) lane++;
    held[lane] = tops[row];
    lanes = Math.max(lanes, lane + 1);
    nodes.push({
      task,
      row,
      lane,
      top: tops[row],
      from: lane,
      to: 0,
      group: group(task),
    });
  });
  const live = Array.from(
    { length: lanes },
    () => new Uint8Array(order.length + 1),
  );
  for (const node of nodes) live[node.lane].fill(1, node.row, node.top + 1);
  const nearest = (lane: number, row: number) => {
    for (let l = lane - 1; l > 0; l--) if (live[l][row]) return l;
    return 0;
  };
  for (const node of nodes) {
    if (!node.lane) continue;
    if (node.row) node.from = nearest(node.lane, node.row - 1);
    node.to = nearest(node.lane, node.top + 1);
  }

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

  const layout = { nodes, links, lanes };
  layouts.set(tasks, layout);
  return layout;
}

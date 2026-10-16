// The tree of a wish's tasks, the Tasks tab's other layout (src/task-tabs.tsx): every task, finished ones included, one
// row each, the oldest at the bottom on the trunk, the work rising and widening into a branch per azima and side lanes
// where work runs side by side (src/data/tree.ts). A row shows the task's state, its code and its title cut short; the
// rest on hover; a click opens its card beside the tree. Hand-drawn SVG, and only the rows on screen: a wish holds
// hundreds of tasks.
import {
  type CSSProperties,
  type ReactNode,
  memo,
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { X } from "lucide-react";

import type { Project, Task } from "../gen/ts/plan/v1/plan_pb";
import { useTaskPrompt } from "./data/djinn";
import { isMerge, isUrgent } from "./data/flight";
import {
  cancelledText,
  fixerOf,
  taskModelDisplay,
  taskStatusText,
  taskTime,
  taskTone,
  type Tone,
  useNow,
} from "./data/format";
import { type TreeLayout, type TreeNode, treeLayout } from "./data/tree";
import { t } from "./i18n";
import { ToneIcon } from "./status";
import { useTextScale } from "./text-size";
import { TaskUsage } from "./usage";
import { MergeLabel, promptPreview, UrgentLabel } from "./wish-task";
import "./task-tree.css";

// A row's height, a lane's width, and the room left of the first lane, in pixels at the default text size: the tree
// lays out in these units and draws in rem, so it follows the root font size (text-size.ts), and measures the screen
// with the text size's scale.
export const ROW = 24;
const LANE = 12;
const PAD = 10;
// Rows drawn beyond the screen, above and below: a scroll shows no gap before the next frame.
const OVERSCAN = 40;

const x = (lane: number) => PAD + lane * LANE;
const rem = (length: number) => `${length / 16}rem`;

// joint goes on from a point to another, rising: it leaves vertically and arrives vertically, a soft curve between, like
// smoke.
function joint(x1: number, y1: number, x2: number, y2: number): string {
  if (x1 === x2) return `V${y2}`;
  const mid = (y1 + y2) / 2;
  return `C${x1} ${mid} ${x2} ${mid} ${x2} ${y2}`;
}

const edge = (x1: number, y1: number, x2: number, y2: number) =>
  `M${x1} ${y1}${joint(x1, y1, x2, y2)}`;

// rise climbs its own lane, then joins the other in the last row: what a link draws.
function rise(x1: number, y1: number, x2: number, y2: number): string {
  if (y1 - y2 <= ROW || x1 === x2) return edge(x1, y1, x2, y2);
  return `M${x1} ${y1}V${y2 + ROW}${joint(x1, y2 + ROW, x2, y2)}`;
}

// Drawn is what the SVG holds for a range of rows: each piece carries the rows it spans, to keep only those on screen.
interface Piece {
  lo: number;
  hi: number;
  key: string;
  d: string;
  className: string;
  rows?: readonly number[];
}

// A link's look, by kind: what a task waits for, what it was forked from, the merge that brings it in.
const linkClass = {
  depends: "tree-link",
  fork: "tree-link tree-link-fork",
  merge: "tree-link tree-link-merge",
};

function pieces(layout: TreeLayout, tones: readonly string[]): Piece[] {
  const n = layout.nodes.length;
  const y = (row: number) => (n - 1 - row) * ROW + ROW / 2;
  const out: Piece[] = [];
  // The trunk: the wish, from its oldest task to its newest.
  out.push({
    lo: 0,
    hi: n - 1,
    key: "trunk",
    d: `M${x(0)} ${y(0)}V${y(n - 1)}`,
    className: "tree-trunk",
  });
  for (const node of layout.nodes) {
    const tone = tones[node.row];
    const side = node.lane !== node.from;
    let d = `M${x(node.lane)} ${y(node.row)}V${y(node.top)}`;
    if (side) {
      const below = Math.max(0, node.row - 1);
      d =
        edge(x(node.from), y(below), x(node.lane), y(node.row)) +
        `V${y(node.top)}`;
      if (node.top + 1 < n)
        d += joint(x(node.lane), y(node.top), x(node.to), y(node.top + 1));
    }
    if (!side && node.top === node.row) continue;
    out.push({
      lo: Math.max(0, node.row - 1),
      hi: Math.min(n - 1, node.top + 1),
      key: `run-${node.task.id}`,
      d,
      className: `tree-run tone-${tone}`,
      rows: [node.row],
    });
  }
  for (const l of layout.links) {
    const a = layout.nodes[l.from];
    const b = layout.nodes[l.to];
    const start = Math.min(a.top, b.row - 1);
    out.push({
      lo: l.from,
      hi: l.to,
      key: `link-${l.kind}-${l.from}-${l.to}`,
      d: rise(x(a.lane), y(Math.max(start, a.row)), x(b.lane), y(b.row)),
      className: linkClass[l.kind],
      rows: [l.from, l.to],
    });
  }
  return out;
}

// scrollerOf is the element whose scroll moves el: the wish's page.
function scrollerOf(el: HTMLElement): HTMLElement | null {
  for (let p = el.parentElement; p; p = p.parentElement) {
    const o = getComputedStyle(p).overflowY;
    if (o === "auto" || o === "scroll") return p;
  }
  return null;
}

export function TaskTree({
  tasks,
  projects,
  codes,
  focus,
  card,
  onOpen,
  onClose,
  aside,
}: {
  tasks: readonly Task[];
  projects: ReadonlyMap<string, Project>;
  codes: ReadonlyMap<string, string>;
  // The task whose card shows beside the tree, by id; "" for none.
  focus: string;
  // Its card, as the list draws it.
  card?: ReactNode;
  onOpen: (id: string) => void;
  onClose: () => void;
  // Beside the title: the layout's switch, what the tasks spent.
  aside?: ReactNode;
}) {
  const layout = treeLayout(tasks);
  const n = layout.nodes.length;
  // The task that took over one cut short, by the code of the one it took over (src/data/flight.ts forkedAs).
  const forked = useMemo(
    () => new Map(tasks.filter((x) => x.forkOf).map((x) => [x.forkOf, x.code])),
    [tasks],
  );
  const byId = useMemo(() => new Map(tasks.map((x) => [x.id, x])), [tasks]);
  // The correction task that fixes a task's failed merge while it works, by the id of the task (fixerOf).
  const fixed = useMemo(
    () =>
      new Map(
        tasks.flatMap((x) => {
          const fixer = fixerOf(x, tasks);
          return fixer ? [[x.id, fixer.code] as const] : [];
        }),
      ),
    [tasks],
  );
  const tones = useMemo(
    () =>
      layout.nodes.map((node) =>
        taskTone(
          node.task,
          forked.get(node.task.code) ?? "",
          fixed.get(node.task.id) ?? "",
        ),
      ),
    [layout, forked, fixed],
  );
  const drawn = useMemo(() => pieces(layout, tones), [layout, tones]);
  const width = PAD * 2 + (layout.lanes - 1) * LANE;
  const height = n * ROW;
  // A row's height on screen.
  const row = ROW * useTextScale();

  // The rows on screen, by the scroll of the page: the newest ones until it is measured.
  const ref = useRef<HTMLDivElement>(null);
  const [range, setRange] = useState<[number, number]>([
    n - 1 - 2 * OVERSCAN,
    n - 1,
  ]);
  const measure = useCallback(() => {
    const el = ref.current;
    if (!el) return;
    const scroller = scrollerOf(el);
    const box = el.getBoundingClientRect();
    const view = scroller
      ? scroller.getBoundingClientRect()
      : { top: 0, bottom: window.innerHeight };
    const top = Math.floor((view.top - box.top) / row);
    const bottom = Math.ceil((view.bottom - box.top) / row);
    const lo = Math.max(0, n - 1 - bottom - OVERSCAN);
    const hi = Math.min(n - 1, n - 1 - top + OVERSCAN);
    setRange((was) => (was[0] === lo && was[1] === hi ? was : [lo, hi]));
  }, [n, row]);
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    measure();
    const scroller = scrollerOf(el) ?? window;
    let frame = 0;
    const onScroll = () => {
      if (frame) return;
      frame = requestAnimationFrame(() => {
        frame = 0;
        measure();
      });
    };
    scroller.addEventListener("scroll", onScroll, { passive: true });
    window.addEventListener("resize", onScroll);
    return () => {
      cancelAnimationFrame(frame);
      scroller.removeEventListener("scroll", onScroll);
      window.removeEventListener("resize", onScroll);
    };
  }, [measure]);

  // A task brought into sight (a click, a link from elsewhere): its row in the middle of the screen.
  const rowOf = useMemo(
    () => new Map(layout.nodes.map((node) => [node.task.id, node.row])),
    [layout],
  );
  const focusRow = rowOf.get(focus);
  useEffect(() => {
    const el = ref.current;
    if (focusRow === undefined || !el) return;
    const scroller = scrollerOf(el);
    if (!scroller) return;
    const box = el.getBoundingClientRect();
    const view = scroller.getBoundingClientRect();
    const at = box.top + (n - 1 - focusRow) * row;
    // Under the attention bar is out of sight (src/azima.tsx useStickyRoom).
    const top =
      view.top +
      (parseFloat(
        getComputedStyle(scroller).getPropertyValue("--attention-height"),
      ) || 0);
    if (at >= top && at + row <= view.bottom) return;
    scroller.scrollTop += at - (top + view.bottom) / 2;
  }, [focusRow, n, row]);

  const [hover, setHover] = useState(-1);
  const lit = hover >= 0 ? hover : (focusRow ?? -1);
  const [lo, hi] = range;
  const shown = drawn.filter((p) => p.hi >= lo && p.lo <= hi);
  const rows = layout.nodes.slice(Math.max(0, lo), hi + 1);
  const hovered = hover >= 0 ? layout.nodes[hover] : undefined;
  // The rows of the hovered task's azima show as one group.
  const kin = hovered?.group ?? "";

  return (
    <div role="tabpanel" aria-labelledby="view-tab-tasks">
      <section className="wish-section tasks-tree" aria-label={t("tasks.all")}>
        <div className="section-title">
          <h2>
            {t("tasks.all")}
            <span className="count">{n}</span>
          </h2>
          {aside}
        </div>
        <div className={`task-tree-layout ${card ? "with-card" : ""}`}>
          <div
            className="task-tree"
            ref={ref}
            style={
              {
                height: rem(height),
                "--tree-lanes": rem(width),
              } as CSSProperties
            }
          >
            <svg
              className="task-tree-lines"
              width={width}
              height={height}
              style={{ width: rem(width), height: rem(height) }}
              viewBox={`0 0 ${width} ${height}`}
              aria-hidden="true"
            >
              {shown.map((p) => (
                <path
                  key={p.key}
                  d={p.d}
                  className={`${p.className}${p.rows?.includes(lit) ? " lit" : ""}`}
                />
              ))}
            </svg>
            {rows.map((node) => (
              <TreeRow
                key={node.task.id}
                node={node}
                n={n}
                tone={tones[node.row]}
                forkedAs={forked.get(node.task.code) ?? ""}
                fixedBy={fixed.get(node.task.id) ?? ""}
                codes={codes}
                focused={node.row === focusRow}
                kin={!!kin && node.group === kin && node.row !== hover}
                onOpen={onOpen}
                onHover={setHover}
              />
            ))}
            {hovered && (
              <TreeTip
                node={hovered}
                n={n}
                forkedAs={forked.get(hovered.task.code) ?? ""}
                fixedBy={fixed.get(hovered.task.id) ?? ""}
                project={projects.get(hovered.task.projectId)}
                azima={byId.get(hovered.group)}
              />
            )}
          </div>
          {card && (
            <aside className="task-tree-card">
              <button
                type="button"
                className="task-tree-close"
                aria-label={t("common.close")}
                title={t("common.close")}
                onClick={onClose}
              >
                <X size={14} aria-hidden="true" />
              </button>
              {card}
            </aside>
          )}
        </div>
      </section>
    </div>
  );
}

// TreeRow is a task's row: on its lane, its state; past the lanes, its code and its title cut short.
const TreeRow = memo(function TreeRow({
  node,
  n,
  tone,
  forkedAs,
  fixedBy,
  codes,
  focused,
  kin,
  onOpen,
  onHover,
}: {
  node: TreeNode;
  n: number;
  tone: Tone;
  forkedAs: string;
  fixedBy: string;
  codes: ReadonlyMap<string, string>;
  focused: boolean;
  // Part of the same azima as the row hovered.
  kin: boolean;
  onOpen: (id: string) => void;
  onHover: (row: number) => void;
}) {
  const { task } = node;
  return (
    <button
      type="button"
      className={`tree-row tone-${tone}${focused ? " focused" : ""}${kin ? " kin" : ""}`}
      style={{ top: rem((n - 1 - node.row) * ROW) }}
      data-task={task.code}
      aria-label={`${task.code} ${task.title}: ${taskStatusText(task, forkedAs, fixedBy)}`}
      onClick={() => onOpen(task.id)}
      onMouseEnter={() => onHover(node.row)}
      onMouseLeave={() => onHover(-1)}
      onFocus={() => onHover(node.row)}
      onBlur={() => onHover(-1)}
    >
      <span className="tree-dot" style={{ left: rem(x(node.lane)) }}>
        <ToneIcon tone={tone} size={12} />
      </span>
      <span className="tree-label">
        {isMerge(task) && <MergeLabel task={task} codes={codes} />}
        {isUrgent(task) && <UrgentLabel />}
        <span className="agent-code">{task.code}</span>
        <span className="tree-title">{task.title}</span>
      </span>
    </button>
  );
});

// TreeTip is what a row says on hover, as a card's heading does: its state, model, times, usage and prompt.
function TreeTip({
  node,
  n,
  forkedAs,
  fixedBy,
  project,
  azima,
}: {
  node: TreeNode;
  n: number;
  forkedAs: string;
  fixedBy: string;
  project?: Project;
  // The azima it is part of.
  azima?: Task;
}) {
  const { task } = node;
  const preview = promptPreview(useTaskPrompt(task));
  const model = taskModelDisplay(task, project);
  const time = taskTime(task, useNow(false));
  const why = cancelledText(task);
  return (
    <span
      className="task-tooltip tree-tip"
      role="tooltip"
      style={{ top: rem((n - node.row) * ROW) }}
    >
      <span className="task-tooltip-title">{task.title}</span>
      {azima && (
        <span className="task-tooltip-activity">
          {t("tasks.branch", { code: azima.code, title: azima.title })}
        </span>
      )}
      <span className="task-tooltip-activity">
        {[taskStatusText(task, forkedAs, fixedBy), why, model?.label, time.text]
          .filter(Boolean)
          .join(" · ")}
        {task.usage && (
          <>
            {" · "}
            <TaskUsage task={task} />
          </>
        )}
      </span>
      {preview ? <span className="task-tooltip-prompt">{preview}</span> : null}
    </span>
  );
}

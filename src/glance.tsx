// The glance (Decision Q08, mockup L04 v2): beside the questions column, each wish's workers, running now and to come,
// nothing else. A filled dot runs, blue; an investigator (the worker Enlighten me starts) runs violet, apart; a circle
// waits on a task, listed under it when that task is of the same wish and shown here, else it names it ("waits on W26
// · Djinn QoL"); a dotted circle is planned; a lit ifrit is a vermilion flame, with the word of its state. The watchers (watch providers, babysits) fold into one line per wish.
// Gold stays for what waits on you, in the questions column. It stays in sight while a long question scrolls; on a
// narrow view it comes above the questions, one line per wish. Read from the tasks the window already has: the
// details of the active wishes, which the window always reads.
import { Eye } from "lucide-react";
import { type CSSProperties, useMemo } from "react";

import {
  AzimaState,
  Provider,
  type Task,
  TaskRole,
  TaskStatus,
  type Wish,
} from "../gen/ts/plan/v1/plan_pb";
import { compareCodes, isAzima } from "./data/flight";
import { ifritLit, ifritWord } from "./data/ifrit";
import { type TextKey, t } from "./i18n";
import { ToneIcon } from "./status";

export type GlanceState = "run" | "investigate" | "wait" | "plan";

export interface GlanceRow {
  task: Task;
  state: GlanceState;
  // How deep it is listed under the task it waits on, of the same wish: 0 for none, 2 under a task that waits too.
  depth: number;
  // The task it waits on, by code, and that task's wish when it is another.
  after?: string;
  afterWish?: string;
}

export interface WishGlance {
  // Work running, then what waits or is planned, each waiting task under the one it waits on.
  rows: GlanceRow[];
  // Investigators, apart: running (violet) or about to.
  investigators: GlanceRow[];
  // Watchers and babysits, folded into one line.
  watchers: Task[];
}

// coming tells a task a worker will run later: planned, or resumed by Djinn once its limit resets.
function coming(task: Task): boolean {
  return (
    task.status === TaskStatus.PENDING || task.status === TaskStatus.RESUMING
  );
}

// holds tells whether a dependency still holds a task back, as Djinn's scheduler reads it: an azima until marked done,
// work until done; cancelled or stopped work holds nothing more (the task goes on, or fails with it).
function holds(dep: Task): boolean {
  if (isAzima(dep)) return dep.azima?.state !== AzimaState.DONE;
  return (
    dep.status !== TaskStatus.DONE &&
    dep.status !== TaskStatus.CANCELLED &&
    dep.status !== TaskStatus.STOPPED
  );
}

const byCode = (a: Task, b: Task) => compareCodes(a.code, b.code);

// glanceOf is one wish's glance, from its tasks. find gives a task of any wish read, by id (a dependency across
// wishes); wishTitle the title of a wish. A dependency no wish read holds nothing back here.
export function glanceOf(
  tasks: readonly Task[],
  find: (id: string) => Task | undefined,
  wishTitle: (id: string) => string | undefined,
): WishGlance {
  const watchers = tasks
    .filter(
      (x) =>
        x.provider === Provider.WATCH &&
        (x.status === TaskStatus.RUNNING || coming(x)),
    )
    .sort(byCode);
  const workers = tasks.filter(
    (x) => !isAzima(x) && x.provider !== Provider.WATCH,
  );
  const live = workers.filter(
    (x) => x.status === TaskStatus.RUNNING || coming(x),
  );
  const investigators = live
    .filter((x) => x.role === TaskRole.INVESTIGATOR)
    .sort(byCode)
    .map((task): GlanceRow => ({
      task,
      state: task.status === TaskStatus.RUNNING ? "investigate" : "plan",
      depth: 0,
    }));
  const work = live.filter((x) => x.role !== TaskRole.INVESTIGATOR);
  const shown = new Set(work.map((x) => x.id));
  const parentOf = new Map<string, string>();
  const rowOf = new Map<string, GlanceRow>();
  for (const task of work) {
    if (task.status === TaskStatus.RUNNING) {
      rowOf.set(task.id, { task, state: "run", depth: 0 });
      continue;
    }
    const open = (task.dependsOn ?? [])
      .map((id) => tasks.find((x) => x.id === id) ?? find(id))
      .filter((dep): dep is Task => !!dep && holds(dep));
    if (!open.length) {
      rowOf.set(task.id, { task, state: "plan", depth: 0 });
      continue;
    }
    const under = open.find(
      (dep) => dep.wishId === task.wishId && shown.has(dep.id),
    );
    const dep = under ?? open[0];
    if (under) parentOf.set(task.id, under.id);
    rowOf.set(task.id, {
      task,
      state: "wait",
      depth: 0,
      after: dep.code,
      afterWish:
        dep.wishId && dep.wishId !== task.wishId
          ? (wishTitle(dep.wishId) ?? "")
          : undefined,
    });
  }
  const kids = new Map<string, GlanceRow[]>();
  for (const [id, parent] of parentOf) {
    const list = kids.get(parent) ?? [];
    list.push(rowOf.get(id)!);
    kids.set(parent, list);
  }
  const rows: GlanceRow[] = [];
  const placed = new Set<string>();
  const place = (row: GlanceRow, depth: number) => {
    if (placed.has(row.task.id)) return;
    placed.add(row.task.id);
    rows.push(depth ? { ...row, depth } : row);
    for (const kid of (kids.get(row.task.id) ?? []).sort((a, b) =>
      byCode(a.task, b.task),
    ))
      place(kid, depth + 1);
  };
  const order: GlanceState[] = ["run", "wait", "plan"];
  const all = [...rowOf.values()].sort(
    (a, b) =>
      order.indexOf(a.state) - order.indexOf(b.state) || byCode(a.task, b.task),
  );
  for (const row of all) if (!parentOf.has(row.task.id)) place(row, 0);
  // A loop of dependencies has no root: its tasks still show.
  for (const row of all) place(row, 0);
  return { rows, investigators, watchers };
}

// forgeMark is how a watched request reads: !3 for a GitLab merge request, #12 for a pull request; empty until found.
export function forgeMark(task: Task): string {
  const number = task.babysit?.number;
  if (!number) return "";
  return `${task.babysit?.url.includes("/merge_requests/") ? "!" : "#"}${number}`;
}

// glanceCounts says, in words, what a wish's glance holds.
export function glanceCounts(glance: WishGlance): string {
  const ifrits = glance.rows.filter((r) => ifritLit(r.task)).length;
  const running = glance.rows.filter((r) => r.state === "run").length - ifrits;
  const investigating = glance.investigators.filter(
    (r) => r.state === "investigate",
  ).length;
  const later =
    glance.rows.length -
    running -
    ifrits +
    glance.investigators.length -
    investigating;
  const parts: string[] = [];
  if (ifrits) parts.push(t("wish.ifrits_lit", { count: ifrits }));
  if (running) parts.push(t("plan.running", { count: running }));
  if (investigating)
    parts.push(t("plan.investigating", { count: investigating }));
  if (later) parts.push(t("glance.coming", { count: later }));
  if (glance.watchers.length)
    parts.push(t("plan.watching", { count: glance.watchers.length }));
  return parts.join(" · ");
}

const stateWords: Record<GlanceState, () => string> = {
  run: () => t("task.status_running"),
  investigate: () => t("task.status_investigating"),
  wait: () => t("glance.waits"),
  plan: () => t("task.status_pending"),
};

const legendKeys: Record<GlanceState, TextKey> = {
  run: "glance.legend_run",
  investigate: "glance.legend_investigate",
  wait: "glance.legend_wait",
  plan: "glance.legend_plan",
};

// Dot is a worker's mark: its state's dot, or a lit ifrit's flame.
function Dot({
  state,
  title,
  task,
}: {
  state: GlanceState;
  title?: string;
  task?: Task;
}) {
  if (task && ifritLit(task))
    return (
      <span
        className="glance-flame tone-ifrit"
        role="img"
        aria-label={ifritWord(task)}
        title={title}
      >
        <ToneIcon tone="ifrit" size={12} />
      </span>
    );
  return (
    <span
      className={`glance-dot ${state}`}
      role="img"
      aria-label={stateWords[state]()}
      title={title}
    />
  );
}

function Row({ row }: { row: GlanceRow }) {
  const { task, state, after, afterWish } = row;
  const investigator = task.role === TaskRole.INVESTIGATOR;
  const word = ifritLit(task) ? ifritWord(task) : stateWords[state]();
  return (
    <li
      className={`glance-row ${state}${row.depth ? " child" : ""}`}
      title={`${word} · ${task.code} ${task.title}`}
      style={
        row.depth ? ({ "--depth": row.depth } as CSSProperties) : undefined
      }
    >
      <Dot state={state} task={task} />
      <span className="glance-code">{task.code}</span>
      <span className="glance-title">
        {task.title}
        {after && (
          <span className="glance-after">
            {afterWish
              ? t("glance.waits_across", { code: after, wish: afterWish })
              : t("glance.waits_on", { code: after })}
          </span>
        )}
        {investigator && task.question && (
          <span className="glance-after">
            {t("glance.investigates", { question: task.question })}
          </span>
        )}
      </span>
    </li>
  );
}

function Watchers({ watchers }: { watchers: Task[] }) {
  const marks = watchers.map(forgeMark).filter(Boolean);
  return (
    <li
      className="glance-watch"
      title={watchers.map((w) => `${w.code} ${w.title}`).join("\n")}
    >
      <Eye size={13} aria-hidden="true" />
      <span>
        {t("plan.watching", { count: watchers.length })}
        {marks.length > 0 && (
          <span className="glance-marks">
            {marks.map((mark) => (
              <span key={mark}>{mark}</span>
            ))}
          </span>
        )}
      </span>
    </li>
  );
}

function GlanceTree({ glance }: { glance: WishGlance }) {
  const { rows, investigators, watchers } = glance;
  if (!rows.length && !investigators.length && !watchers.length)
    return (
      <ul className="glance-list">
        <li className="glance-quiet">{t("glance.quiet")}</li>
      </ul>
    );
  return (
    <>
      {rows.length > 0 && (
        <ul className="glance-list">
          {rows.map((row) => (
            <Row key={row.task.id} row={row} />
          ))}
        </ul>
      )}
      {investigators.length > 0 && (
        <ul className="glance-list glance-investigators">
          {investigators.map((row) => (
            <Row key={row.task.id} row={row} />
          ))}
        </ul>
      )}
      {watchers.length > 0 && (
        <ul className="glance-list">
          <Watchers watchers={watchers} />
        </ul>
      )}
    </>
  );
}

// The narrow form: one line per wish, its name and a dot per worker.
function GlanceLine({
  wish,
  glance,
  onOpen,
}: {
  wish: Wish;
  glance: WishGlance;
  onOpen?: () => void;
}) {
  const dots = [...glance.rows, ...glance.investigators];
  const marks =
    dots.length + glance.watchers.length ? (
      <span className="glance-line-marks">
        {dots.map((row) => (
          <Dot
            key={row.task.id}
            state={row.state}
            task={row.task}
            title={`${row.task.code} ${row.task.title}`}
          />
        ))}
        {glance.watchers.length > 0 && (
          <span
            className="glance-line-watch"
            title={t("plan.watching", { count: glance.watchers.length })}
          >
            <Eye size={12} aria-hidden="true" />
            {glance.watchers.length}
          </span>
        )}
      </span>
    ) : (
      <span className="glance-line-marks zero">{t("glance.nothing")}</span>
    );
  const inside = (
    <>
      <span className="name">{wish.title}</span>
      {marks}
    </>
  );
  return onOpen ? (
    <button
      type="button"
      className="glance-line"
      onClick={onOpen}
      title={t("plan.open_wish")}
    >
      {inside}
    </button>
  ) : (
    <div className="glance-line">{inside}</div>
  );
}

// Glance shows the wishes given, by rank: on the flying carpet each under its title, which opens it (onOpenWish), with
// its counts; on a wish's page, the wish alone, without its title.
export function Glance({
  wishes: shown,
  tasksOf,
  all,
  onOpenWish,
}: {
  wishes: readonly Wish[];
  // A wish's tasks, of every wish read: the dependencies across wishes come from them.
  tasksOf: Readonly<Record<string, { tasks: readonly Task[] } | undefined>>;
  // Every wish, for the title of a wish another waits on.
  all: readonly Wish[];
  onOpenWish?: (wishId: string) => void;
}) {
  const glances = useMemo(() => {
    const byId = new Map<string, Task>();
    for (const detail of Object.values(tasksOf))
      for (const task of detail?.tasks ?? []) byId.set(task.id, task);
    const titles = new Map(all.map((w) => [w.id, w.title]));
    return shown.map((wish) => ({
      wish,
      glance: glanceOf(
        tasksOf[wish.id]?.tasks ?? [],
        (id) => byId.get(id),
        (id) => titles.get(id),
      ),
    }));
  }, [shown, tasksOf, all]);
  const headings = !!onOpenWish;
  const present = new Set<GlanceState | "watch">();
  for (const { glance } of glances) {
    for (const row of [...glance.rows, ...glance.investigators])
      present.add(row.state);
    if (glance.watchers.length) present.add("watch");
  }
  return (
    <section className="glance" aria-label={t("glance.label")}>
      <h2 className="glance-label">{t("glance.label")}</h2>
      <div className="glance-wide">
        {glances.map(({ wish, glance }) => (
          <div className="glance-wish" key={wish.id}>
            {headings && (
              <h3 className="glance-head">
                <button
                  type="button"
                  onClick={() => onOpenWish?.(wish.id)}
                  title={t("plan.open_wish")}
                >
                  {wish.title}
                </button>
                <span className="glance-count">{glanceCounts(glance)}</span>
              </h3>
            )}
            <GlanceTree glance={glance} />
          </div>
        ))}
        {present.size > 0 && (
          <p className="glance-legend">
            {(["run", "investigate", "wait", "plan"] as const)
              .filter((state) => present.has(state))
              .map((state) => (
                <span key={state}>
                  <span className={`glance-dot ${state}`} aria-hidden="true" />
                  {t(legendKeys[state])}
                </span>
              ))}
            {present.has("watch") && (
              <span className="glance-legend-watch">
                <Eye size={12} aria-hidden="true" />
                {t("glance.legend_watch")}
              </span>
            )}
          </p>
        )}
      </div>
      <div className="glance-narrow">
        {glances.map(({ wish, glance }) => (
          <GlanceLine
            key={wish.id}
            wish={wish}
            glance={glance}
            onOpen={onOpenWish ? () => onOpenWish(wish.id) : undefined}
          />
        ))}
      </div>
    </section>
  );
}

// The glance (Decision Q08, mockup L04 v2): beside the questions column, and on a wish's page, each wish's workers,
// running now, paused and to come, nothing else, in one order whatever their kind (compareGlance, W219): what is in
// progress on top, the one begun first above (a filled dot, blue; an investigator, the worker Enlighten me starts,
// violet; a lit ifrit a vermilion flame with the word of its state), then the paused ones (a circled pause, the reason
// on hover: stopped by you or the lead, cut short by a restart or a usage limit until Djinn resumes it), then what waits
// on a task (a circle, listed under that task when it waits too, else naming it: "waits on W26 · Djinn QoL"), then what
// is planned (a dotted circle). An urgent task carries its label, as in the tasks view. Below them the watchers
// (watch providers, babysits), a line each: what they said last and since when, and the request they watch, a click
// away in the browser.
// Below them, the work on its way into the wish's branch (W137): the batch being merged and the check it runs, with its
// gate and time, the merges waiting in line, the push checks running or a push they hold, and the repair workers
// (corrections of a merge, of main's merge or of the push checks, reviews of work left not committed), a wrench in
// place of their dot. Every line opens its task; a held push opens its failing output.
// Gold stays for what waits on you, in the questions column. It stays in sight while a long question scrolls; on a
// narrow view it comes above the questions, one line per wish. Read from the tasks the window already has: the
// details of the active wishes, which the window always reads.
import type { Timestamp } from "@bufbuild/protobuf/wkt";
import {
  CloudOff,
  CloudUpload,
  ExternalLink,
  Eye,
  GitMerge,
  Wrench,
} from "lucide-react";
import { type CSSProperties, type ReactNode, useMemo, useState } from "react";

import type { Gate } from "../gen/ts/machine/v1/machine_pb";
import {
  AzimaState,
  IntegrationState,
  Provider,
  PushStage,
  type Task,
  TaskRole,
  TaskStatus,
  type Wish,
  type WishPush,
} from "../gen/ts/plan/v1/plan_pb";
import { type ReadState, useDjinn, useOptionalData } from "./data/djinn";
import {
  compareCodes,
  compareWorkers,
  isAzima,
  isInvestigation,
  isUrgent,
} from "./data/flight";
import {
  date,
  firstLine,
  gitReason,
  liveWaitReason,
  span,
  takenOver,
  taskStatusText,
  useNow,
  workerPhase,
} from "./data/format";
import { ifritLit, ifritOut, ifritWord } from "./data/ifrit";
import { ModalFrame } from "./frame";
import { type TextKey, language, t } from "./i18n";
import { Link, linkLabel } from "./link";
import { ReadingLine } from "./reading-line";
import { ToneIcon } from "./status";
import { InvestigationLabel, UrgentLabel } from "./wish-task";

// A worker's state in the glance: at work (blue, or a lit ifrit's flame), investigating (violet), paused (its worker
// stopped, its work kept: workerPhase), waiting on a task, or planned.
export type GlanceState = "run" | "investigate" | "paused" | "wait" | "plan";

// glanceRank is the one order of the glance's workers, whatever their kind (work, investigation, investigator, ifrit,
// repair): what is in progress on top, then what is paused, then what waits on a task, then what is planned.
const glanceRank: Record<GlanceState, number> = {
  run: 0,
  investigate: 0,
  paused: 1,
  wait: 2,
  plan: 3,
};

// compareGlance orders two rows of the glance: by state (glanceRank), then the one begun first (compareWorkers).
export function compareGlance(
  a: { task: Task; state: GlanceState },
  b: { task: Task; state: GlanceState },
): number {
  return (
    glanceRank[a.state] - glanceRank[b.state] || compareWorkers(a.task, b.task)
  );
}

export interface GlanceRow {
  task: Task;
  state: GlanceState;
  // How deep it is listed under the task it waits on, waiting too, of the same wish: 0 for none, 2 under a task that
  // waits on another.
  depth: number;
  // The task it waits on, by code, and that task's wish when it is another.
  after?: string;
  afterWish?: string;
}

// What a repair worker repairs: a merge in conflict, red commit checks, main's merge, red push checks, or work its
// worker left not committed (a review).
export type RepairKind = "conflict" | "red" | "main" | "push" | "review";

export interface RepairRow {
  task: Task;
  kind: RepairKind;
  // At work, paused, waiting on a task, or planned.
  state: "run" | "paused" | "wait" | "plan";
  // The tasks whose work it repairs, by code.
  of: string[];
}

// WatchRow is a watcher (a babysit of a pull or merge request, any watch command): the request it watches, the first
// line of what it said last (GREEN, CI_FAILED, NEW_NOTES) and since when that line holds.
export interface WatchRow {
  task: Task;
  // Watching, or paused (its command stopped, kept), or planned.
  state: "watch" | "paused" | "plan";
  // The request's web address: the babysit's, else its azima's pull request; "" when none is known.
  url: string;
}

// MergeRow is a line of the work on its way into the wish's branch: a batch being merged, with the commit check it runs;
// the push checks running, or git push; a push the checks hold; a task's work waiting in line.
export type MergeRow =
  | {
      kind: "merging";
      branch: string;
      tasks: Task[];
      check: string;
      since?: Timestamp;
    }
  | {
      kind: "pushing";
      projectId: string;
      branch: string;
      check: string;
      since?: Timestamp;
    }
  | {
      kind: "held";
      projectId: string;
      branch: string;
      push: WishPush;
      // The tests its output names as failed.
      tests: string[];
      // The correction worker that makes the push checks pass, while it works.
      by?: Task;
    }
  | { kind: "queued"; branch: string; task: Task; reason: string };

export interface WishGlance {
  // Every worker but the watchers and the repair workers, in one order (compareGlance): at work, investigators, lit
  // ifrits and investigations included, then the paused ones, then what waits, each waiting task under the one it waits
  // on when that one waits too, then what is planned.
  rows: GlanceRow[];
  // Watchers and babysits, each on its line with its request, below the workers: they watch, they do no work.
  watchers: WatchRow[];
  // The work on its way into the wish's branch: merging, pushing, held, then waiting in line.
  merges: MergeRow[];
  // The repair workers, apart from the others, in the same order.
  repairs: RepairRow[];
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

// watchUrl is the web address of the request a watcher watches: its babysit's, found as it starts, else the pull
// request of the azima it is part of; "" when neither says.
function watchUrl(task: Task, byId: (id: string) => Task | undefined): string {
  if (task.babysit?.url) return task.babysit.url;
  const azima = task.partOf ? byId(task.partOf) : undefined;
  return azima?.azima?.pr?.url ?? "";
}

// glanceOf is one wish's glance, from its tasks. find gives a task of any wish read, by id (a dependency across
// wishes); wishTitle the title of a wish. A dependency no wish read holds nothing back here.
// wish, when given, brings its pushes: the push checks running, a held push, its corrections.
export function glanceOf(
  tasks: readonly Task[],
  find: (id: string) => Task | undefined,
  wishTitle: (id: string) => string | undefined,
  wish?: Wish,
): WishGlance {
  const byId = (id: string) => tasks.find((x) => x.id === id) ?? find(id);
  const taken = takenOver(tasks);
  const phase = (task: Task) => workerPhase(task, taken.has(task.code));
  const watchers = tasks
    .filter((x) => x.provider === Provider.WATCH && phase(x))
    .map((task): WatchRow => ({
      task,
      state:
        phase(task) === "run"
          ? "watch"
          : phase(task) === "paused"
            ? "paused"
            : "plan",
      url: watchUrl(task, byId),
    }))
    .sort(
      (a, b) =>
        Number(a.state !== "watch") - Number(b.state !== "watch") ||
        Number(a.state === "plan") - Number(b.state === "plan") ||
        compareWorkers(a.task, b.task),
    );
  const live = tasks.filter(
    (x) => !isAzima(x) && x.provider !== Provider.WATCH && phase(x),
  );
  const opened = (task: Task) =>
    (task.dependsOn ?? [])
      .map(byId)
      .filter((dep): dep is Task => !!dep && holds(dep));
  // A worker's state: at work (an investigator investigates), paused, or to come, waiting on a task or planned.
  const stateOf = (task: Task): GlanceState => {
    switch (phase(task)) {
      case "run":
        return task.role === TaskRole.INVESTIGATOR && !ifritLit(task)
          ? "investigate"
          : "run";
      case "paused":
        return "paused";
      default:
        return opened(task).length ? "wait" : "plan";
    }
  };
  const repairs: RepairRow[] = [];
  const work: Task[] = [];
  for (const task of live) {
    const kind = repairKind(task, wish);
    if (!kind) {
      work.push(task);
      continue;
    }
    const state = stateOf(task);
    repairs.push({
      task,
      kind,
      state: state === "investigate" ? "run" : state,
      of: (task.review?.taskIds ?? task.correction?.failure?.taskIds ?? [])
        .map((id) => byId(id)?.code)
        .filter((code): code is string => !!code),
    });
  }
  repairs.sort(compareGlance);
  const rowOf = new Map<string, GlanceRow>();
  for (const task of work) {
    const state = stateOf(task);
    if (state !== "wait") {
      rowOf.set(task.id, { task, state, depth: 0 });
      continue;
    }
    const open = opened(task);
    const dep = open[0];
    rowOf.set(task.id, {
      task,
      state,
      depth: 0,
      after: dep.code,
      afterWish:
        dep.wishId && dep.wishId !== task.wishId
          ? (wishTitle(dep.wishId) ?? "")
          : undefined,
    });
  }
  // A waiting task is listed under a task it waits on when that one is of its wish and waits too, so that nothing
  // waiting comes between the workers at work: one waiting on a running or planned task names it in its own place.
  const parentOf = new Map<string, string>();
  for (const row of rowOf.values()) {
    if (row.state !== "wait") continue;
    const under = opened(row.task).find(
      (dep) =>
        dep.wishId === row.task.wishId && rowOf.get(dep.id)?.state === "wait",
    );
    if (!under) continue;
    parentOf.set(row.task.id, under.id);
    row.after = under.code;
    row.afterWish = undefined;
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
    for (const kid of (kids.get(row.task.id) ?? []).sort(compareGlance))
      place(kid, depth + 1);
  };
  const all = [...rowOf.values()].sort(compareGlance);
  for (const row of all) if (!parentOf.has(row.task.id)) place(row, 0);
  // A loop of dependencies has no root: its tasks still show.
  for (const row of all) place(row, 0);
  return {
    rows,
    watchers,
    merges: mergesOf(tasks, wish),
    repairs,
  };
}

// repairKind says what a task repairs, for a repair worker; undefined for any other.
function repairKind(task: Task, wish?: Wish): RepairKind | undefined {
  if (task.review) return "review";
  if (wish?.pushes.some((p) => p.correctedBy === task.id)) return "push";
  if (
    task.correction?.failure?.mainSha ||
    wish?.mains.some((m) => m.correctedBy === task.id)
  )
    return "main";
  if (!task.correction) return undefined;
  return task.correction.failure?.state === IntegrationState.CONFLICT
    ? "conflict"
    : "red";
}

const msOf = (ts?: Timestamp) => date(ts)?.getTime() ?? 0;

// mergesOf is the work of the wish on its way into its branch: each batch being merged, by branch; each push running
// or held; then the work waiting in line, in the order Djinn merges it, the first ended first.
function mergesOf(tasks: readonly Task[], wish?: Wish): MergeRow[] {
  const done = tasks.filter((x) => x.status === TaskStatus.DONE);
  const batches = new Map<string, Task[]>();
  for (const task of done)
    if (task.integration?.state === IntegrationState.INTEGRATING) {
      const branch = task.integration.branch;
      batches.set(branch, [...(batches.get(branch) ?? []), task]);
    }
  const rows: MergeRow[] = [...batches].map(([branch, batch]) => {
    const checking = batch.find((x) => x.integration?.check);
    return {
      kind: "merging",
      branch,
      tasks: batch.sort(byCode),
      check: checking?.integration?.check ?? "",
      since: checking?.integration?.updateTime,
    };
  });
  for (const push of wish?.pushes ?? []) {
    const run = push.run;
    const branch =
      run?.branch ||
      push.last?.branch ||
      wish?.integrationBranches.find((b) => b.projectId === push.projectId)
        ?.branch ||
      "";
    if (run?.stage === PushStage.CHECKING || run?.stage === PushStage.PUSHING)
      rows.push({
        kind: "pushing",
        projectId: push.projectId,
        branch,
        check: run.stage === PushStage.CHECKING ? run.check : "",
        since: run.startTime,
      });
    else if (push.held && !push.unchecked)
      rows.push({
        kind: "held",
        projectId: push.projectId,
        branch,
        push,
        tests: failingTests(push.failure?.output || run?.note || ""),
        by: tasks.find((x) => x.id === push.correctedBy),
      });
  }
  const queued = done
    .filter((x) => x.integration?.state === IntegrationState.PENDING)
    .sort(
      (a, b) =>
        msOf(a.endTime) - msOf(b.endTime) || compareCodes(a.code, b.code),
    );
  for (const task of queued)
    rows.push({
      kind: "queued",
      branch: task.integration?.branch ?? "",
      task,
      reason: task.integration?.reason
        ? gitReason(task.integration.reason)
        : task.integration?.alone
          ? t("glance.merges_alone")
          : "",
    });
  return rows;
}

// failingTests names the tests a check's output says failed: the slow tests' last "Failed:" line, else Go's "--- FAIL:"
// lines and Playwright's failed specs, each once, in their order.
export function failingTests(output: string): string[] {
  const lines = output.split("\n");
  const failed = [...lines]
    .reverse()
    .find((line) => line.startsWith("Failed: "));
  if (failed)
    return failed
      .slice("Failed: ".length)
      .split(", ")
      .map((name) => name.trim())
      .filter(Boolean);
  const names = new Set<string>();
  for (const line of lines) {
    const named =
      /^\s*--- FAIL: (\S+)/.exec(line) ??
      /^\s*(?:✘\s+\d+|\d+\))\s+(?:\[[^\]]+\]\s+›\s+)?(\S+\.spec\.ts:\d+)/.exec(
        line,
      );
    if (named) names.add(named[1]);
  }
  return [...names];
}

// forgeMark is how a watched request reads: !3 for a GitLab merge request, #12 for a pull request; empty until found.
export function forgeMark(task: Task): string {
  const number = task.babysit?.number;
  if (!number) return "";
  return `${task.babysit?.url.includes("/merge_requests/") ? "!" : "#"}${number}`;
}

// glanceCounts says, in words, what a wish's glance holds: each worker counted once, in its state.
export function glanceCounts(glance: WishGlance): string {
  const ifrits = glance.rows.filter((r) => ifritLit(r.task)).length;
  const running = glance.rows.filter((r) => r.state === "run").length - ifrits;
  const investigating = glance.rows.filter(
    (r) => r.state === "investigate",
  ).length;
  const paused =
    glance.rows.filter((r) => r.state === "paused").length +
    glance.watchers.filter((r) => r.state === "paused").length;
  const later = glance.rows.filter(
    (r) => r.state === "wait" || r.state === "plan",
  ).length;
  const watching = glance.watchers.filter((r) => r.state === "watch").length;
  const merging = glance.merges.reduce(
    (n, m) => n + (m.kind === "merging" ? m.tasks.length : 0),
    0,
  );
  const queued = glance.merges.filter((m) => m.kind === "queued").length;
  const held = glance.merges.some((m) => m.kind === "held");
  const parts: string[] = [];
  if (ifrits) parts.push(t("wish.ifrits_lit", { count: ifrits }));
  if (running) parts.push(t("plan.running", { count: running }));
  if (investigating)
    parts.push(t("plan.investigating", { count: investigating }));
  if (paused) parts.push(t("glance.paused", { count: paused }));
  if (later) parts.push(t("glance.coming", { count: later }));
  if (watching) parts.push(t("plan.watching", { count: watching }));
  if (merging) parts.push(t("glance.merging", { count: merging }));
  if (queued) parts.push(t("glance.to_merge", { count: queued }));
  if (held) parts.push(t("glance.push_held"));
  if (glance.repairs.length)
    parts.push(t("glance.repairing", { count: glance.repairs.length }));
  return parts.join(" · ");
}

const stateWords: Record<GlanceState, () => string> = {
  run: () => t("task.status_running"),
  investigate: () => t("task.status_investigating"),
  paused: () => t("task.status_paused"),
  wait: () => t("glance.waits"),
  plan: () => t("task.status_pending"),
};

const legendKeys: Record<GlanceState, TextKey> = {
  run: "glance.legend_run",
  investigate: "glance.legend_investigate",
  paused: "glance.legend_paused",
  wait: "glance.legend_wait",
  plan: "glance.legend_plan",
};

// What a repair worker repairs, in words, from the codes of the work it repairs.
function repairWords(row: RepairRow): string {
  const codes = row.of.join(", ");
  switch (row.kind) {
    case "review":
      return codes
        ? t("glance.repair_review", { codes })
        : t("glance.repair_review_any");
    case "main":
      return t("glance.repair_main");
    case "push":
      return t("glance.repair_push");
    case "conflict":
      return codes
        ? t("glance.repair_conflict", { codes })
        : t("glance.repair_conflict_any");
    case "red":
      return codes
        ? t("glance.repair_red", { codes })
        : t("glance.repair_red_any");
  }
}

// pausedWord says where a paused worker stands: paused, stopped, put out, cut short, waiting for its limit.
function pausedWord(task: Task): string {
  return taskStatusText(task);
}

// pausedWhy says why a worker is paused, as Djinn recorded it: why it stopped, what it waits for, when its usage
// limit resets, or why its last run ended; "" when nothing says.
function pausedWhy(task: Task): string {
  const resume = date(task.resumeAfter);
  return (
    task.error ||
    liveWaitReason(task) ||
    (resume
      ? t("glance.resumes_at", {
          time: resume.toLocaleTimeString(language, {
            hour: "2-digit",
            minute: "2-digit",
          }),
        })
      : "") ||
    (task.runs ?? []).at(-1)?.reason ||
    ""
  );
}

// Pause is the mark of a paused worker, in Djinn's glyphs: a circled pause, or an ifrit's outlined flame once put out.
function Pause({ task, title }: { task?: Task; title?: string }) {
  const tone = task && ifritOut(task) ? "out" : "paused";
  return (
    <span
      className={`glance-pause tone-${tone}`}
      role={task ? "img" : undefined}
      aria-label={task ? pausedWord(task) : undefined}
      aria-hidden={task ? undefined : true}
      title={title}
    >
      <ToneIcon tone={tone} size={12} />
    </span>
  );
}

// Dot is a worker's mark: its state's dot, a lit ifrit's flame, or a paused worker's pause.
function Dot({
  state,
  title,
  task,
}: {
  state: GlanceState;
  title?: string;
  task?: Task;
}) {
  if (state === "paused") return <Pause task={task} title={title} />;
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

// Mark is the icon of a line of merges or repairs, in its state's colour: blue at work, grey waiting, salmon held.
function Mark({
  kind,
  state,
  label,
  title,
}: {
  kind: "merge" | "push" | "held" | "repair";
  state: "run" | "wait" | "held";
  // Its words for a reader; none beside its words, as in the legend.
  label?: string;
  title?: string;
}) {
  const Icon = {
    merge: GitMerge,
    push: CloudUpload,
    held: CloudOff,
    repair: Wrench,
  }[kind];
  return (
    <span
      className={`glance-mark ${state}`}
      role={label ? "img" : undefined}
      aria-label={label}
      aria-hidden={label ? undefined : true}
      title={title}
    >
      <Icon size={12} aria-hidden="true" />
    </span>
  );
}

// Line is a line of the glance: a button that opens what it says when it can, else plain.
function Line({
  className,
  title,
  style,
  onOpen,
  children,
}: {
  className: string;
  title: string;
  style?: CSSProperties;
  onOpen?: () => void;
  children: ReactNode;
}) {
  if (!onOpen)
    return (
      <li className={className} title={title} style={style}>
        {children}
      </li>
    );
  return (
    <li className="glance-item">
      <button
        type="button"
        className={className}
        title={title}
        style={style}
        onClick={onOpen}
      >
        {children}
      </button>
    </li>
  );
}

// Labels are a task's labels, as the tasks view shows them: urgent (or for quick feedback), an investigation.
function Labels({ task }: { task: Task }) {
  return (
    <>
      {isUrgent(task) && <UrgentLabel feedback={task.feedback} />}
      {isInvestigation(task) && <InvestigationLabel />}
    </>
  );
}

function Row({
  row,
  onOpenTask,
}: {
  row: GlanceRow;
  onOpenTask?: (task: Task) => void;
}) {
  const { task, state, after, afterWish } = row;
  const investigator = task.role === TaskRole.INVESTIGATOR;
  const word = ifritLit(task)
    ? ifritWord(task)
    : state === "paused"
      ? pausedWord(task)
      : stateWords[state]();
  const why = state === "paused" ? pausedWhy(task) : "";
  return (
    <Line
      className={`glance-row ${state}${row.depth ? " child" : ""}`}
      title={`${word} · ${task.code} ${task.title}${why ? `\n${why}` : ""}`}
      style={
        row.depth ? ({ "--depth": row.depth } as CSSProperties) : undefined
      }
      onOpen={onOpenTask && (() => onOpenTask(task))}
    >
      <Dot state={state} task={task} />
      <span className="glance-code">{task.code}</span>
      <span className="glance-title">
        <Labels task={task} />
        {task.title}
        {state === "paused" && (
          <span className="glance-after">{pausedWord(task)}</span>
        )}
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
    </Line>
  );
}

// Since is how long ago since was, kept current while on screen; nothing without it.
function Since({ since }: { since?: Timestamp }) {
  const at = msOf(since);
  const now = useNow(at > 0);
  if (!at) return null;
  return <span className="glance-since">{span(now - at)}</span>;
}

const NO_GATES: Gate[] = [];

// checkGate says where a batch's check stands with its gate, as the machine's gates last read: held for one of its
// tasks, since when; or waited for, behind whom. Undefined while the gates do not say.
function checkGate(
  check: string,
  batch: readonly Task[],
  gates: readonly Gate[],
): { since?: Timestamp } | { holder: string } | undefined {
  const gate = gates.find((g) => g.name === check.toLowerCase());
  if (!gate) return undefined;
  if (batch.some((x) => x.id === gate.holderTaskId))
    return { since: gate.since };
  const waits = gate.waiting.some((who) =>
    batch.some((x) => who.startsWith(`${x.code} (${x.title})`)),
  );
  return waits ? { holder: gate.holder } : undefined;
}

// CheckWords says the check a batch runs, its gate and how long it has run: since its gate was granted, else since it
// began; or that it waits for its gate.
function CheckWords({
  check,
  batch,
  since,
}: {
  check: string;
  batch: readonly Task[];
  since?: Timestamp;
}) {
  const gates = useOptionalData((s) => s.gates, NO_GATES);
  const gate = checkGate(check, batch, gates);
  if (gate && "holder" in gate)
    return (
      <span className="glance-after" title={gate.holder}>
        {t("glance.gate_waits", { check })}
      </span>
    );
  return (
    <span
      className="glance-after"
      title={t("glance.through_gate", { gate: check.toLowerCase() })}
    >
      {t("glance.checking", { check })}
      <Since since={gate?.since ?? since} />
    </span>
  );
}

function MergeItem({
  row,
  onOpenTask,
  onOpenHeld,
}: {
  row: MergeRow;
  onOpenTask?: (task: Task) => void;
  onOpenHeld: (row: MergeRow & { kind: "held" }) => void;
}) {
  switch (row.kind) {
    case "merging": {
      const codes = row.tasks.map((x) => x.code).join(", ");
      const words = t("glance.merging_into", { codes, branch: row.branch });
      return (
        <Line
          className="glance-row glance-merge merging"
          title={`${words}\n${row.tasks.map((x) => `${x.code} ${x.title}`).join("\n")}`}
          onOpen={onOpenTask && (() => onOpenTask(row.tasks[0]))}
        >
          <Mark kind="merge" state="run" label={t("glance.legend_merging")} />
          <span className="glance-title glance-span">
            {words}
            {row.check ? (
              <CheckWords
                check={row.check}
                batch={row.tasks}
                since={row.since}
              />
            ) : (
              <span className="glance-after">{t("glance.merging_now")}</span>
            )}
          </span>
        </Line>
      );
    }
    case "pushing": {
      const words = t("glance.push_of", { branch: row.branch });
      return (
        <Line className="glance-row glance-merge pushing" title={words}>
          <Mark kind="push" state="run" label={t("glance.legend_pushing")} />
          <span className="glance-title glance-span">
            {words}
            <span className="glance-after">
              {row.check
                ? t("glance.checking", { check: row.check })
                : t("glance.pushing_now")}
              <Since since={row.since} />
            </span>
          </span>
        </Line>
      );
    }
    case "held": {
      const why = firstLine(row.push.held);
      const words = t("glance.push_held_of", { branch: row.branch });
      return (
        <Line
          className="glance-row glance-merge held"
          title={`${words}\n${row.push.held}`}
          onOpen={() => onOpenHeld(row)}
        >
          <Mark kind="held" state="held" label={t("glance.legend_held")} />
          <span className="glance-title glance-span">
            {words}
            <span className="glance-after">
              {row.tests.length
                ? t("glance.held_tests", {
                    why: why.split(":")[0],
                    tests: row.tests.join(", "),
                  })
                : why}
            </span>
          </span>
        </Line>
      );
    }
    case "queued": {
      const words = t("glance.queued_into", {
        code: row.task.code,
        branch: row.branch,
      });
      return (
        <Line
          className="glance-row glance-merge queued"
          title={[`${row.task.code} ${row.task.title}`, row.reason]
            .filter(Boolean)
            .join("\n")}
          onOpen={onOpenTask && (() => onOpenTask(row.task))}
        >
          <Mark kind="merge" state="wait" label={t("glance.legend_queued")} />
          <span className="glance-title glance-span">
            {words}
            <span className="glance-after">
              {row.reason || t("glance.queued")}
            </span>
          </span>
        </Line>
      );
    }
  }
}

function RepairItem({
  row,
  onOpenTask,
}: {
  row: RepairRow;
  onOpenTask?: (task: Task) => void;
}) {
  const { task, state } = row;
  const word = state === "paused" ? pausedWord(task) : stateWords[state]();
  const why = state === "paused" ? pausedWhy(task) : "";
  const attempt = task.correction?.attempt ?? task.review?.attempt ?? 0;
  return (
    <Line
      className={`glance-row glance-repair ${state}`}
      title={`${word} · ${task.code} ${task.title}${why ? `\n${why}` : ""}`}
      onOpen={onOpenTask && (() => onOpenTask(task))}
    >
      <Mark
        kind="repair"
        state={state === "run" ? "run" : "wait"}
        label={`${t("glance.legend_repair")} · ${word}`}
      />
      <span className="glance-code">{task.code}</span>
      <span className="glance-title">
        <Labels task={task} />
        {repairWords(row)}
        <span className="glance-after">
          {attempt > 1 ? t("glance.attempt", { word, attempt }) : word}
        </span>
      </span>
    </Line>
  );
}

// HeldDialog shows why the push checks hold a push: the check that failed, its command and the end of its output,
// and the correction at work.
function HeldDialog({
  wish,
  row,
  onOpenTask,
  onClose,
}: {
  wish: Wish;
  row: MergeRow & { kind: "held" };
  onOpenTask?: (task: Task) => void;
  onClose: () => void;
}) {
  const { push, by } = row;
  const output = push.failure?.output || push.run?.note || "";
  return (
    <ModalFrame
      title={t("glance.push_held_of", { branch: row.branch })}
      eyebrow={wish.title}
      onClose={onClose}
      wide
    >
      <div className="glance-held">
        <p>{push.held}</p>
        {row.tests.length > 0 && (
          <p>{t("glance.held_failed", { tests: row.tests.join(", ") })}</p>
        )}
        {push.failure?.command && <code>$ {push.failure.command}</code>}
        {output && <pre className="glance-output">{output}</pre>}
        {by && (
          <p>
            {onOpenTask ? (
              <button
                type="button"
                className="text-button"
                onClick={() => {
                  onClose();
                  onOpenTask(by);
                }}
              >
                {t("glance.held_by", { code: by.code })}
              </button>
            ) : (
              t("glance.held_by", { code: by.code })
            )}
          </p>
        )}
      </div>
    </ModalFrame>
  );
}

// WatchItem is a watcher's line: its eye (a pause once stopped), its code, the first line of what it said last and since
// when it says so, and the request it watches, which opens in the browser. The line opens its task.
function WatchItem({
  row,
  onOpenTask,
}: {
  row: WatchRow;
  onOpenTask?: (task: Task) => void;
}) {
  const { task, state, url } = row;
  const word =
    state === "paused"
      ? pausedWord(task)
      : state === "plan"
        ? stateWords.plan()
        : t("task.status_watching");
  const why = state === "paused" ? pausedWhy(task) : "";
  const line = task.lastLine || task.title;
  const inside = (
    <>
      {state === "watch" ? (
        <span className="glance-eye tone-watching" role="img" aria-label={word}>
          <Eye size={12} aria-hidden="true" />
        </span>
      ) : state === "paused" ? (
        <Pause task={task} />
      ) : (
        <Dot state="plan" />
      )}
      <span className="glance-code">{task.code}</span>
      <span className="glance-title">
        <span className="glance-watch-line">{line}</span>
        {task.lastLine && (
          <Since since={task.lastLineTime ?? task.lastActivityTime} />
        )}
        {state === "paused" && (
          <span className="glance-after">{pausedWord(task)}</span>
        )}
      </span>
    </>
  );
  const title = `${word} · ${task.code} ${task.title}${why ? `\n${why}` : ""}`;
  return (
    <li className={`glance-item glance-watcher ${state}`}>
      {onOpenTask ? (
        <button
          type="button"
          className={`glance-row watch ${state}`}
          title={title}
          onClick={() => onOpenTask(task)}
        >
          {inside}
        </button>
      ) : (
        <span className={`glance-row watch ${state}`} title={title}>
          {inside}
        </span>
      )}
      {url && (
        <Link href={url} className="glance-link">
          {forgeMark(task) || linkLabel(url)}
          <ExternalLink size={11} aria-hidden="true" />
        </Link>
      )}
    </li>
  );
}

function mergeKey(row: MergeRow): string {
  switch (row.kind) {
    case "merging":
      return `merging-${row.branch}`;
    case "queued":
      return `queued-${row.task.id}`;
    default:
      return `${row.kind}-${row.projectId}`;
  }
}

function GlanceTree({
  glance,
  readState = "ready",
  onRetry,
  onOpenTask,
  onOpenHeld,
}: {
  glance: WishGlance;
  readState?: ReadState;
  onRetry?: () => void;
  onOpenTask?: (task: Task) => void;
  onOpenHeld: (row: MergeRow & { kind: "held" }) => void;
}) {
  const { rows, watchers, merges, repairs } = glance;
  if (!rows.length && !watchers.length && !merges.length && !repairs.length) {
    if (readState !== "ready")
      return (
        <ReadingLine kind="runs" state={readState} size="g" onRetry={onRetry} />
      );
    return (
      <ul className="glance-list">
        <li className="glance-quiet">{t("glance.quiet")}</li>
      </ul>
    );
  }
  return (
    <>
      {rows.length > 0 && (
        <ul className="glance-list">
          {rows.map((row) => (
            <Row key={row.task.id} row={row} onOpenTask={onOpenTask} />
          ))}
        </ul>
      )}
      {watchers.length > 0 && (
        <ul className="glance-list glance-watchers">
          {watchers.map((row) => (
            <WatchItem key={row.task.id} row={row} onOpenTask={onOpenTask} />
          ))}
        </ul>
      )}
      {merges.length + repairs.length > 0 && (
        <ul
          className="glance-list glance-flow"
          aria-label={t("glance.flow_label")}
        >
          {merges.map((row) => (
            <MergeItem
              key={mergeKey(row)}
              row={row}
              onOpenTask={onOpenTask}
              onOpenHeld={onOpenHeld}
            />
          ))}
          {repairs.map((row) => (
            <RepairItem key={row.task.id} row={row} onOpenTask={onOpenTask} />
          ))}
        </ul>
      )}
    </>
  );
}

// The narrow form: one line per wish, its name and a dot per worker, then a mark per repair worker, one for the work
// merging or waiting to, and one for a held push.
function GlanceLine({
  wish,
  glance,
  readState = "ready",
  onOpen,
}: {
  wish: Wish;
  glance: WishGlance;
  readState?: ReadState;
  onOpen?: () => void;
}) {
  const dots = glance.rows;
  const watching = glance.watchers.filter((w) => w.state === "watch").length;
  const merging = glance.merges.filter(
    (m) => m.kind === "merging" || m.kind === "pushing",
  ).length;
  const queued = glance.merges.filter((m) => m.kind === "queued").length;
  const held = glance.merges.some((m) => m.kind === "held");
  const any =
    dots.length +
    glance.watchers.length +
    glance.repairs.length +
    glance.merges.length;
  const marks = any ? (
    <span className="glance-line-marks">
      {dots.map((row) => (
        <Dot
          key={row.task.id}
          state={row.state}
          task={row.task}
          title={`${row.task.code} ${row.task.title}`}
        />
      ))}
      {watching > 0 && (
        <span
          className="glance-line-watch"
          title={t("plan.watching", { count: watching })}
        >
          <Eye size={12} aria-hidden="true" />
          {watching}
        </span>
      )}
      {glance.watchers
        .filter((w) => w.state === "paused")
        .map((row) => (
          <Pause
            key={row.task.id}
            task={row.task}
            title={`${row.task.code} ${row.task.title}`}
          />
        ))}
      {glance.repairs.map((row) => (
        <Mark
          key={row.task.id}
          kind="repair"
          state={row.state === "run" ? "run" : "wait"}
          label={t("glance.legend_repair")}
          title={`${row.task.code} ${repairWords(row)}`}
        />
      ))}
      {merging + queued > 0 && (
        <Mark
          kind="merge"
          state={merging ? "run" : "wait"}
          label={t("glance.legend_merging")}
          title={glanceCounts({
            ...glance,
            rows: [],
            watchers: [],
            repairs: [],
          })}
        />
      )}
      {held && (
        <Mark kind="held" state="held" label={t("glance.legend_held")} />
      )}
    </span>
  ) : readState !== "ready" ? (
    <span className="glance-line-marks zero unread">·</span>
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

type Present = GlanceState | "watch" | "merging" | "queued" | "held" | "repair";

// Glance shows the wishes given, by rank: on the flying carpet each under its title, which opens it (onOpenWish), with
// its counts; on a wish's page, the wish alone, without its title. onOpenTask opens a line's task, in the page's tasks.
export function Glance({
  wishes: shown,
  tasksOf,
  all,
  readState,
  onRetry,
  onOpenWish,
  onOpenTask,
}: {
  wishes: readonly Wish[];
  // A wish's tasks, of every wish read: the dependencies across wishes come from them.
  tasksOf: Readonly<Record<string, { tasks: readonly Task[] } | undefined>>;
  // Every wish, for the title of a wish another waits on.
  all: readonly Wish[];
  readState?: ReadState;
  onRetry?: () => void;
  onOpenWish?: (wishId: string) => void;
  onOpenTask?: (task: Task) => void;
}) {
  const djinn = useDjinn();
  useOptionalData((s) => s, undefined);
  const stateOf = (wishId: string): ReadState => {
    if (djinn) {
      const s = djinn.store.readState("tasks", wishId);
      if (s !== "ready" || readState === undefined) return s;
    }
    return readState ?? "ready";
  };
  const retryOf = (wishId: string) => () => {
    if (onRetry) onRetry();
    else djinn?.store.retryRead("tasks", wishId);
  };
  const overallState =
    readState ?? (djinn ? djinn.store.readState("tasks") : "ready");
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
        wish,
      ),
    }));
  }, [shown, tasksOf, all]);
  const [held, setHeld] = useState<{
    wish: Wish;
    row: MergeRow & { kind: "held" };
  }>();
  const headings = !!onOpenWish;
  const present = new Set<Present>();
  for (const { glance } of glances) {
    for (const row of glance.rows) present.add(row.state);
    for (const row of glance.watchers)
      present.add(row.state === "watch" ? "watch" : row.state);
    for (const row of glance.repairs)
      if (row.state === "paused") present.add("paused");
    for (const row of glance.merges)
      present.add(row.kind === "pushing" ? "merging" : row.kind);
    if (glance.repairs.length) present.add("repair");
  }
  return (
    <section className="glance" aria-label={t("glance.label")}>
      <h2 className="glance-label">{t("glance.label")}</h2>
      <div className="glance-wide">
        {glances.length === 0 && overallState !== "ready" && (
          <ReadingLine
            kind="runs"
            state={overallState}
            size="g"
            onRetry={onRetry ?? (() => djinn?.store.retryRead("tasks"))}
          />
        )}
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
            <GlanceTree
              glance={glance}
              readState={stateOf(wish.id)}
              onRetry={retryOf(wish.id)}
              onOpenTask={onOpenTask}
              onOpenHeld={(row) => setHeld({ wish, row })}
            />
          </div>
        ))}
        {present.size > 0 && (
          <p className="glance-legend">
            {(["run", "investigate", "paused", "wait", "plan"] as const)
              .filter((state) => present.has(state))
              .map((state) => (
                <span key={state}>
                  {state === "paused" ? (
                    <Pause />
                  ) : (
                    <span
                      className={`glance-dot ${state}`}
                      aria-hidden="true"
                    />
                  )}
                  {t(legendKeys[state])}
                </span>
              ))}
            {present.has("watch") && (
              <span className="glance-legend-watch">
                <Eye size={12} aria-hidden="true" />
                {t("glance.legend_watch")}
              </span>
            )}
            {present.has("merging") && (
              <span>
                <Mark kind="merge" state="run" />
                {t("glance.legend_merging")}
              </span>
            )}
            {present.has("queued") && (
              <span>
                <Mark kind="merge" state="wait" />
                {t("glance.legend_queued")}
              </span>
            )}
            {present.has("held") && (
              <span>
                <Mark kind="held" state="held" />
                {t("glance.legend_held")}
              </span>
            )}
            {present.has("repair") && (
              <span>
                <Mark kind="repair" state="run" />
                {t("glance.legend_repair")}
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
            readState={stateOf(wish.id)}
            onOpen={onOpenWish ? () => onOpenWish(wish.id) : undefined}
          />
        ))}
      </div>
      {held && (
        <HeldDialog
          wish={held.wish}
          row={held.row}
          onOpenTask={onOpenTask}
          onClose={() => setHeld(undefined)}
        />
      )}
    </section>
  );
}

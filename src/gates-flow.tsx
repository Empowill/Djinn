// The gates over time in the Hourglass's Gates tab (gates-tab.tsx), L13 v2 variant A (Q46 A): on top, the workers
// that run, stacked by their task's kind, and a line of those waiting for a gate; under them, the projects, each with
// one lane per gate its workers asked for, each band a request, hatched while it waits, plain while it holds, in the
// colour of its task's kind. Further down, a Sankey of the named gates' requests: project → its gates → what they
// waited, each flow in its task's kind. Both read GateService.History over an hour, 6 hours or a day; the kinds and the
// workers' runs come joined by the server.
//
// A gate a project declares goes by name@project and is that project's alone (gate.declaredGate); a gate named for
// the machine (lint, e2e) is one lock for every project: its requests count under the project of the worker that asked,
// so that one such gate shows under each project that used it (W158).
import {
  type RefObject,
  memo,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";

import type { GateInterval, WorkerRun } from "../gen/ts/machine/v1/machine_pb";
import { TaskKind } from "../gen/ts/plan/v1/plan_pb";
import { duration } from "./consumption-panel";
import { timeOf } from "./data/series";
import { type TextKey, language, t } from "./i18n";

// The gate every worker's command is admitted under (harness.AdmissionGate): not a named gate.
export const ADMISSIONS = "commands";
// The windows of the history, in minutes, and how often each is read again: a day costs the server 70 to 100 ms.
export const PERIODS = [60, 360, 1440] as const;
export type Period = (typeof PERIODS)[number];
export const PERIOD_EVERY: Record<Period, number> = {
  60: 5_000,
  360: 30_000,
  1440: 60_000,
};
// gateIn is a gate's own name and its project: a gate a project declares carries it (stack@dolmen); another is the
// machine's, counted under the project that asked for it (project).
export function gateIn(
  gate: string,
  project: string,
): { name: string; project: string; declared: boolean } {
  const at = gate.lastIndexOf("@");
  if (at > 0)
    return {
      name: gate.slice(0, at),
      project: gate.slice(at + 1),
      declared: true,
    };
  return { name: gate, project, declared: false };
}

// byProject orders projects by name, the requests outside any project last.
export function byProject(a: string, b: string): number {
  if (a === b) return 0;
  if (!a) return 1;
  if (!b) return -1;
  return a.localeCompare(b);
}

// projectName is a project's name as the tab shows it: outside any project when none.
export const projectName = (project: string) =>
  project || t("gates.no_project");

// A request waited when it waited a second or more: below, it passed at once.
const WAITED = 1_000;
// A wait longer than this counts apart, as in the tab's figures.
const LONG = 5_000;
// The lanes a gate shows at most; the requests beyond are counted.
const LANES = 4;
// One lane's height, the gap under a gate's lanes and a project's title over its gates, in rem.
const LANE = 1.125;
const GAP = 0.625;
const HEAD = 1.5;
const STRIP = 4.625;

// The kinds of task, as the window colours them: the workers in blue (code, correction, review), the watchers, the
// investigations, the mockups, the ifrits, what held a gate outside a task (a terminal, the lead, Djinn's merges).
export type KindGroup =
  "work" | "inv" | "mock" | "watch" | "ifrit" | "other" | "out";
export const GROUPS: readonly KindGroup[] = [
  "work",
  "inv",
  "mock",
  "watch",
  "ifrit",
  "other",
  "out",
];
const GROUP_KEY: Record<KindGroup, TextKey> = {
  work: "gates.kind_work",
  inv: "gates.kind_inv",
  mock: "gates.kind_mock",
  watch: "gates.kind_watch",
  ifrit: "gates.kind_ifrit",
  other: "gates.kind_other",
  out: "gates.kind_out",
};
const groupName = (g: KindGroup) => t(GROUP_KEY[g]);

// kindGroup is the colour of a task's kind: correction and review take the workers' blue.
export function kindGroup(kind: TaskKind): KindGroup {
  switch (kind) {
    case TaskKind.WORK:
    case TaskKind.CORRECTION:
    case TaskKind.REVIEW:
      return "work";
    case TaskKind.INVESTIGATION:
      return "inv";
    case TaskKind.MOCKUP:
      return "mock";
    case TaskKind.WATCHER:
      return "watch";
    case TaskKind.IFRIT:
      return "ifrit";
    default:
      return "other";
  }
}

// A request for a gate, in milliseconds: asked, granted, released (the end of the window while it holds); its gate's
// own name and its project (gateIn).
export interface Hold {
  iv: GateInterval;
  asked: number;
  grant: number;
  release: number;
  group: KindGroup;
  name: string;
  project: string;
}

// holdsOf is the intervals as requests, a hold still running ending at end. A hold outside a running worker, or of no
// task, is outside a task; a task gone since is of no known kind.
export function holdsOf(ivs: readonly GateInterval[], end: number): Hold[] {
  return ivs
    .filter((iv) => iv.grantTime || iv.askedTime)
    .map((iv) => {
      const grant = timeOf(iv.grantTime) || timeOf(iv.askedTime);
      const asked = Math.min(timeOf(iv.askedTime) || grant, grant);
      const release = iv.releaseTime
        ? Math.max(grant, timeOf(iv.releaseTime))
        : Math.max(grant, end);
      const group: KindGroup =
        iv.outside || !iv.taskId ? "out" : kindGroup(iv.taskKind);
      const { name, project } = gateIn(iv.gate, iv.project);
      return { iv, asked, grant, release, group, name, project };
    });
}

// inScope keeps the named gates, and the admissions of the workers' commands too when asked.
export function inScope(holds: readonly Hold[], admissions: boolean): Hold[] {
  return admissions
    ? [...holds]
    : holds.filter((h) => h.iv.gate !== ADMISSIONS);
}

export interface GateRow {
  // The row's key, project and gate, its project and its gate's own name.
  key: string;
  project: string;
  gate: string;
  // The requests of the window, and the time the gate was held within it.
  requests: number;
  heldMs: number;
  // Each request on its lane; those past the last lane are counted in over.
  bands: { hold: Hold; lane: number }[];
  lanes: number;
  over: number;
}

// gateRows lays the requests of the window out, one row per gate of each project, the projects by name (none last),
// in a project the named gates by name then the admissions; in a row, each request goes on the first lane free when it
// asks. A request shorter than minMs takes minMs on its lane, so that two short ones never draw over each other.
export function gateRows(
  holds: readonly Hold[],
  start: number,
  end: number,
  minMs = 0,
): GateRow[] {
  const byGate = new Map<string, Hold[]>();
  for (const h of holds) {
    if (h.release <= start || h.asked >= end) continue;
    const key = `${h.project}\n${h.name}`;
    const list = byGate.get(key);
    if (list) list.push(h);
    else byGate.set(key, [h]);
  }
  return [...byGate]
    .map(([key, list]) => ({ key, list, ...list[0] }))
    .sort(
      (a, b) =>
        byProject(a.project, b.project) ||
        (a.name === ADMISSIONS ? 1 : 0) - (b.name === ADMISSIONS ? 1 : 0) ||
        a.name.localeCompare(b.name),
    )
    .map(({ key, list, project, name: gate }) => {
      list.sort((a, b) => a.asked - b.asked || a.grant - b.grant);
      const ends: number[] = [];
      const bands: GateRow["bands"] = [];
      let over = 0;
      let heldMs = 0;
      for (const hold of list) {
        heldMs += Math.max(
          0,
          Math.min(hold.release, end) - Math.max(hold.grant, start),
        );
        let lane = ends.findIndex((e) => e <= hold.asked);
        if (lane < 0) lane = ends.push(0) - 1;
        ends[lane] = Math.max(hold.release, hold.asked + minMs);
        if (lane >= LANES) over++;
        else bands.push({ hold, lane });
      }
      return {
        key,
        project,
        gate,
        requests: list.length,
        heldMs,
        bands,
        lanes: Math.min(LANES, ends.length),
        over,
      };
    });
}

export interface StripBucket {
  // The workers running on average over the bucket, by kind.
  running: Partial<Record<KindGroup, number>>;
  // The requests waiting a gate on average over the bucket, whatever their kind.
  waiting: number;
}

// workerStrip cuts the window in buckets: the workers running in each, by kind, and the requests waiting for a gate a
// second or more; max is the top of the scale, the highest stack or line rounded up, one at least.
export function workerStrip(
  runs: readonly WorkerRun[],
  holds: readonly Hold[],
  start: number,
  end: number,
  count: number,
): { buckets: StripBucket[]; max: number } {
  const width = Math.max(1, (end - start) / count);
  const buckets: StripBucket[] = Array.from({ length: count }, () => ({
    running: {},
    waiting: 0,
  }));
  const spread = (
    from: number,
    to: number,
    add: (b: StripBucket, share: number) => void,
  ) => {
    const a = Math.max(start, from);
    const z = Math.min(end, to);
    if (z <= a) return;
    const first = Math.floor((a - start) / width);
    const last = Math.min(count - 1, Math.floor((z - start) / width));
    for (let i = first; i <= last; i++) {
      const b0 = start + i * width;
      const share = (Math.min(z, b0 + width) - Math.max(a, b0)) / width;
      if (share > 0) add(buckets[i], share);
    }
  };
  for (const r of runs) {
    const group = kindGroup(r.kind);
    spread(timeOf(r.startTime), r.endTime ? timeOf(r.endTime) : end, (b, s) => {
      b.running[group] = (b.running[group] ?? 0) + s;
    });
  }
  for (const h of holds)
    if (h.grant - h.asked >= WAITED)
      spread(h.asked, h.grant, (b, s) => {
        b.waiting += s;
      });
  let max = 1;
  for (const b of buckets) {
    const total = Object.values(b.running).reduce(
      (sum, v) => sum + (v ?? 0),
      0,
    );
    max = Math.max(max, total, b.waiting);
  }
  return { buckets, max: Math.ceil(max - 1e-9) };
}

export interface Instant {
  running: [KindGroup, number][];
  total: number;
  // The holders of each gate, by their task's code, "outside a task" for none; a gate named with its project.
  holding: [string, string[]][];
  // The requests waiting a gate at this instant.
  waiting: Hold[];
}

// holdLabel names a request's gate with its project: djinn · lint.
export const holdLabel = (h: Hold) =>
  h.project ? `${h.project} · ${h.name}` : h.name;

// instant is the window at one moment: who runs, who holds each gate, who waits.
export function instant(
  runs: readonly WorkerRun[],
  holds: readonly Hold[],
  at: number,
  end: number,
): Instant {
  const counts = new Map<KindGroup, number>();
  let total = 0;
  for (const r of runs) {
    const s = timeOf(r.startTime);
    const e = r.endTime ? timeOf(r.endTime) : end;
    if (s <= at && e > at) {
      const g = kindGroup(r.kind);
      counts.set(g, (counts.get(g) ?? 0) + 1);
      total++;
    }
  }
  const holding = new Map<string, string[]>();
  for (const h of holds)
    if (h.grant <= at && h.release > at) {
      const gate = holdLabel(h);
      const who = holding.get(gate) ?? [];
      who.push(h.iv.taskCode || t("gates.kind_out"));
      holding.set(gate, who);
    }
  return {
    running: GROUPS.filter((g) => counts.has(g)).map((g) => [
      g,
      counts.get(g)!,
    ]),
    total,
    holding: [...holding].sort(([a], [b]) => a.localeCompare(b)),
    waiting: holds.filter(
      (h) => h.asked <= at && h.grant > at && h.grant - h.asked >= WAITED,
    ),
  };
}

// What a request waited for its gate: nothing (under a second), 1 to 5 s, more.
export type Outcome = "now" | "short" | "long";
export const OUTCOMES: readonly Outcome[] = ["now", "short", "long"];
export function outcomeOf(h: Hold): Outcome {
  const w = h.grant - h.asked;
  return w < WAITED ? "now" : w <= LONG ? "short" : "long";
}

export interface Flows {
  total: number;
  // The projects, the most requests first, none last.
  projects: [string, number][];
  // The gates of each project, keyed by gateKey, in their projects' order, the most asked first within one.
  gates: [string, number][];
  outcomes: [Outcome, number][];
  // The kinds of task the requests came from, for the legend.
  kinds: KindGroup[];
  // From a project to one of its gates, a flow per kind of task.
  toGate: { from: string; to: string; kind: KindGroup; count: number }[];
  toOutcome: { from: string; to: Outcome; count: number }[];
}

// gateKey is a gate of a project as a node of the Sankey; gateOfKey gives its own name back.
export const gateKey = (project: string, gate: string) => `${project}\t${gate}`;
export const gateOfKey = (key: string) => key.slice(key.indexOf("\t") + 1);

// sankeyFlows counts the requests for a named gate asked within the window: from their project to its gate, a flow per
// kind of task, then to what they waited. The admissions are left out: nine requests in ten, almost all at once, they
// would crush the rest.
export function sankeyFlows(holds: readonly Hold[], start: number): Flows {
  const list = holds.filter((h) => h.name !== ADMISSIONS && h.asked >= start);
  const projects = new Map<string, number>();
  const gates = new Map<string, number>();
  const outcomes = new Map<Outcome, number>();
  const kinds = new Set<KindGroup>();
  const toGate = new Map<string, number>();
  const toOutcome = new Map<string, number>();
  const add = <K,>(m: Map<K, number>, k: K) => m.set(k, (m.get(k) ?? 0) + 1);
  for (const h of list) {
    const o = outcomeOf(h);
    const gate = gateKey(h.project, h.name);
    add(projects, h.project);
    add(gates, gate);
    add(outcomes, o);
    kinds.add(h.group);
    add(toGate, `${h.project}\n${gate}\n${h.group}`);
    add(toOutcome, `${gate}\n${o}`);
  }
  const projectList = [...projects].sort(([a, x], [b, y]) =>
    !a ? 1 : !b ? -1 : y - x || a.localeCompare(b),
  );
  const rank = new Map(projectList.map(([p], i) => [p, i]));
  const projectOf = (key: string) => key.slice(0, key.indexOf("\t"));
  return {
    total: list.length,
    projects: projectList,
    gates: [...gates].sort(
      ([a, x], [b, y]) =>
        rank.get(projectOf(a))! - rank.get(projectOf(b))! ||
        y - x ||
        a.localeCompare(b),
    ),
    outcomes: OUTCOMES.filter((o) => outcomes.has(o)).map((o) => [
      o,
      outcomes.get(o)!,
    ]),
    kinds: GROUPS.filter((g) => kinds.has(g)),
    toGate: [...toGate].map(([k, count]) => {
      const [from, to, kind] = k.split("\n") as [string, string, KindGroup];
      return { from, to, kind, count };
    }),
    toOutcome: [...toOutcome].map(([k, count]) => {
      const [from, to] = k.split("\n") as [string, Outcome];
      return { from, to, count };
    }),
  };
}

const minuteText = (ms: number) =>
  new Date(ms).toLocaleTimeString(language, {
    hour: "2-digit",
    minute: "2-digit",
  });

// The ticks of the time axis: every 10 minutes over an hour, every hour over 6, every 4 hours over a day.
function ticks(start: number, end: number): number[] {
  const span = end - start;
  const step =
    span <= 61 * 60_000
      ? 600_000
      : span <= 361 * 60_000
        ? 3_600_000
        : 4 * 3_600_000;
  const offset = new Date(start).getTimezoneOffset() * 60_000;
  const out: number[] = [];
  for (
    let at = Math.ceil((start - offset) / step) * step + offset;
    at <= end - step * 0.6;
    at += step
  )
    if (at > start) out.push(at);
  return out;
}

const pct = (v: number) => `${(Math.max(0, Math.min(1, v)) * 100).toFixed(2)}%`;

const periodKey: Record<Period, TextKey> = {
  60: "gates.period_1h",
  360: "gates.period_6h",
  1440: "gates.period_24h",
};

// The card of the lanes: the window's switches, the legend, the workers' strip, the gates' lanes and the time axis.
export const GatesFlow = memo(function GatesFlow({
  intervals,
  runs,
  start,
  end,
  period,
  onPeriod,
  admissions,
  onAdmissions,
  onPick,
}: {
  intervals: readonly GateInterval[];
  runs: readonly WorkerRun[];
  start: number;
  end: number;
  period: Period;
  onPeriod: (p: Period) => void;
  admissions: boolean;
  onAdmissions: (on: boolean) => void;
  onPick: (g: GateInterval) => void;
}) {
  const holds = useMemo(
    () => inScope(holdsOf(intervals, end), admissions),
    [intervals, end, admissions],
  );
  const span = Math.max(1, end - start);
  const rows = useMemo(
    () => gateRows(holds, start, end, span / 400),
    [holds, start, end, span],
  );
  const buckets = period === 60 ? 120 : period === 360 ? 240 : 288;
  const strip = useMemo(
    () => workerStrip(runs, holds, start, end, buckets),
    [runs, holds, start, end, buckets],
  );
  const used = new Set<KindGroup>([
    ...holds.map((h) => h.group),
    ...runs.map((r) => kindGroup(r.kind)),
  ]);
  // The rows of each project, under its title.
  const sections = useMemo(() => projectSections(rows), [rows]);
  const tracks = useRef<HTMLDivElement>(null);
  const at = (ms: number) => pct((ms - start) / span);
  const rowHeight = (r: GateRow) => `${Math.max(r.lanes, 2) * LANE + GAP}rem`;
  return (
    <section
      className="statistics-card gates-card gates-flow"
      aria-label={t("gates.flow")}
    >
      <header className="gates-flow-head">
        <h3>{t("gates.flow")}</h3>
        <div className="gates-flow-switches">
          <div
            className="segmented-switch"
            role="group"
            aria-label={t("gates.period")}
          >
            {PERIODS.map((p) => (
              <button
                key={p}
                type="button"
                className={period === p ? "active" : ""}
                aria-pressed={period === p}
                onClick={() => onPeriod(p)}
              >
                {t(periodKey[p])}
              </button>
            ))}
          </div>
          <div
            className="segmented-switch"
            role="group"
            aria-label={t("gates.scope")}
          >
            {[false, true].map((on) => (
              <button
                key={String(on)}
                type="button"
                className={admissions === on ? "active" : ""}
                aria-pressed={admissions === on}
                onClick={() => onAdmissions(on)}
              >
                {on ? t("gates.scope_all") : t("gates.scope_named")}
              </button>
            ))}
          </div>
        </div>
      </header>
      <p className="statistics-detail muted-text">{t("gates.flow_detail")}</p>
      <ul className="gates-legend" aria-label={t("gates.legend")}>
        {GROUPS.filter((g) => used.has(g)).map((g) => (
          <li key={g} className={`gk-${g}`}>
            <i className="gates-legend-swatch" aria-hidden="true" />
            {groupName(g)}
          </li>
        ))}
        <li>
          <i className="gates-legend-swatch is-wait" aria-hidden="true" />
          {t("gates.legend_wait")}
        </li>
        <li>
          <i className="gates-legend-swatch is-hold" aria-hidden="true" />
          {t("gates.legend_hold")}
        </li>
        <li>
          <i className="gates-legend-swatch is-queue" aria-hidden="true" />
          {t("gates.legend_queue")}
        </li>
      </ul>
      <div className="gates-flow-plot">
        <div className="gates-flow-labels">
          <div
            className="gates-flow-label"
            style={{ height: `${STRIP + GAP}rem` }}
          >
            <span className="gates-flow-name">{t("gates.running")}</span>
            <span className="muted-text">{t("gates.running_sub")}</span>
            <span className="gates-strip-scale" aria-hidden="true">
              <span>{strip.max}</span>
              <span>0</span>
            </span>
          </div>
          {sections.map((sec) => [
            <div
              key={`project-${sec.project}`}
              className="gates-flow-label gates-flow-project"
              style={{ height: `${HEAD}rem` }}
              title={t("gates.project_lanes", {
                project: projectName(sec.project),
                count: sec.requests,
                held: duration(sec.heldMs / 1000),
              })}
            >
              <span className="gates-flow-name">
                {projectName(sec.project)}
              </span>
            </div>,
            ...sec.rows.map((r) => (
              <div
                key={r.key}
                className="gates-flow-label"
                style={{ height: rowHeight(r) }}
              >
                <code className="gates-flow-name">{r.gate}</code>
                <span className="muted-text">
                  {t("gates.row_sub", {
                    count: r.requests,
                    held: duration(r.heldMs / 1000),
                  })}
                </span>
                {r.over > 0 && (
                  <span className="muted-text">
                    {t("gates.row_over", { count: r.over })}
                  </span>
                )}
              </div>
            )),
          ])}
        </div>
        <div className="gates-flow-tracks" ref={tracks}>
          <WorkerStrip strip={strip} />
          {rows.length === 0 && (
            <p className="statistics-empty muted-text">
              {t("gates.flow_none")}
            </p>
          )}
          {sections.map((sec) => [
            <div
              key={`project-${sec.project}`}
              className="gates-row-project"
              style={{ height: `${HEAD}rem` }}
            >
              <span className="muted-text">
                {t("gates.project_sub", {
                  count: sec.requests,
                  held: duration(sec.heldMs / 1000),
                })}
              </span>
            </div>,
            ...sec.rows.map((r) => (
              <div
                key={r.key}
                className="gates-row"
                role="list"
                aria-label={`${projectName(r.project)} · ${r.gate}`}
                data-row={r.key}
                style={{ height: rowHeight(r) }}
              >
                {r.bands.map(({ hold, lane }, i) => (
                  <Band
                    key={i}
                    hold={hold}
                    index={i}
                    lane={lane}
                    left={at(hold.asked)}
                    right={`calc(100% - ${at(hold.release)})`}
                    onPick={onPick}
                  />
                ))}
              </div>
            )),
          ])}
          <FlowHover
            tracks={tracks}
            rows={rows}
            runs={runs}
            holds={holds}
            start={start}
            end={end}
          />
        </div>
      </div>
      <div className="gates-flow-axis" aria-hidden="true">
        <span />
        <div className="gates-flow-ticks">
          {ticks(start, end).map((ms) => (
            <span key={ms} style={{ left: at(ms) }}>
              {minuteText(ms)}
            </span>
          ))}
          <span className="is-now">{t("gates.axis_now")}</span>
        </div>
      </div>
    </section>
  );
});

export interface ProjectSection {
  project: string;
  rows: GateRow[];
  requests: number;
  heldMs: number;
}

// projectSections groups the rows of the lanes under their projects, in the rows' order.
export function projectSections(rows: readonly GateRow[]): ProjectSection[] {
  const out: ProjectSection[] = [];
  for (const r of rows) {
    let sec = out.at(-1);
    if (!sec || sec.project !== r.project) {
      sec = { project: r.project, rows: [], requests: 0, heldMs: 0 };
      out.push(sec);
    }
    sec.rows.push(r);
    sec.requests += r.requests;
    sec.heldMs += r.heldMs;
  }
  return out;
}

// A request on its gate's lane: hatched while it waits, plain while it holds; it opens its command's runs.
function Band({
  hold,
  index,
  lane,
  left,
  right,
  onPick,
}: {
  hold: Hold;
  index: number;
  lane: number;
  left: string;
  right: string;
  onPick: (g: GateInterval) => void;
}) {
  const g = hold.iv;
  const span = Math.max(1, hold.release - hold.asked);
  const label = t("consumption.gate_band", {
    gate: g.gate,
    command: g.command || "—",
    project: g.project || "—",
    wait: duration((hold.grant - hold.asked) / 1000),
    held: duration((hold.release - hold.grant) / 1000),
  });
  return (
    <button
      type="button"
      role="listitem"
      className={`gates-band gk-${hold.group}`}
      data-band={index}
      aria-label={label}
      onClick={() => onPick(g)}
      style={{ top: `${lane * LANE}rem`, left, right }}
    >
      {hold.grant > hold.asked && (
        <span
          className="gates-band-wait"
          style={{ width: pct((hold.grant - hold.asked) / span) }}
        />
      )}
      <span className="gates-band-held">
        {g.taskCode || t("gates.kind_out")}
      </span>
    </button>
  );
}

// The workers running, stacked by kind, and the line of the requests waiting a gate, over the same time as the lanes.
const WorkerStrip = memo(function WorkerStrip({
  strip,
}: {
  strip: { buckets: StripBucket[]; max: number };
}) {
  const { buckets, max } = strip;
  const n = buckets.length;
  const H = 100;
  const y = (v: number) => H - (v / max) * H;
  // One path per kind, a step per bucket: adjacent buckets of one path draw no seam between them.
  const paths = new Map<KindGroup, string>();
  let line = "";
  buckets.forEach((b, i) => {
    let acc = 0;
    for (const g of GROUPS) {
      const v = b.running[g] ?? 0;
      if (v <= 0) continue;
      const top = y(acc + v).toFixed(2);
      const bottom = y(acc).toFixed(2);
      paths.set(
        g,
        `${paths.get(g) ?? ""}M${i},${bottom}V${top}H${i + 1}V${bottom}Z`,
      );
      acc += v;
    }
    const wy = y(b.waiting).toFixed(2);
    line += `${i ? "L" : "M"}${i},${wy}L${i + 1},${wy}`;
  });
  return (
    <div className="gates-strip" style={{ height: `${STRIP}rem` }}>
      <svg
        viewBox={`0 0 ${n} ${H}`}
        preserveAspectRatio="none"
        aria-hidden="true"
      >
        <line
          className="gates-strip-grid"
          x1={0}
          x2={n}
          y1={H / 2}
          y2={H / 2}
        />
        <line className="gates-strip-base" x1={0} x2={n} y1={H} y2={H} />
        {[...paths].map(([g, d]) => (
          <path key={g} className={`gk-${g}`} d={d} />
        ))}
        <path className="gates-strip-queue" d={line} />
      </svg>
    </div>
  );
});

// The pointer over the lanes: a line at its moment, and what runs, holds and waits then; over a band, that request.
function FlowHover({
  tracks,
  rows,
  runs,
  holds,
  start,
  end,
}: {
  tracks: RefObject<HTMLDivElement | null>;
  rows: readonly GateRow[];
  runs: readonly WorkerRun[];
  holds: readonly Hold[];
  start: number;
  end: number;
}) {
  const [point, setPoint] = useState<{
    x: number;
    y: number;
    width: number;
    hold?: Hold;
  } | null>(null);
  useEffect(() => {
    const el = tracks.current;
    if (!el) return;
    const move = (e: PointerEvent) => {
      const box = el.getBoundingClientRect();
      const target = e.target instanceof Element ? e.target : null;
      const band = target?.closest<HTMLElement>(".gates-band");
      const row = band?.parentElement;
      let hold: Hold | undefined;
      if (band && row) {
        const r = rows.find((x) => x.key === row.dataset.row);
        hold = r?.bands[Number(band.dataset.band)]?.hold;
      }
      setPoint({
        x: e.clientX - box.left,
        y: e.clientY - box.top,
        width: box.width,
        hold,
      });
    };
    const leave = () => setPoint(null);
    el.addEventListener("pointermove", move);
    el.addEventListener("pointerleave", leave);
    return () => {
      el.removeEventListener("pointermove", move);
      el.removeEventListener("pointerleave", leave);
    };
  }, [tracks, rows]);
  if (!point || point.width <= 0) return null;
  const at = start + (point.x / point.width) * (end - start);
  const left = Math.max(0, Math.min(point.x + 14, point.width - 300));
  return (
    <>
      <span className="gates-flow-cursor" style={{ left: point.x }} />
      <div
        className="gates-flow-readout"
        style={{ left, top: point.y + 14 }}
        role="status"
      >
        {point.hold ? (
          <HoldReadout hold={point.hold} />
        ) : (
          <InstantReadout at={at} now={instant(runs, holds, at, end)} />
        )}
      </div>
    </>
  );
}

function HoldReadout({ hold }: { hold: Hold }) {
  const g = hold.iv;
  return (
    <>
      <strong>
        <code>{holdLabel(hold)}</code> · {g.taskCode || t("gates.kind_out")}
      </strong>{" "}
      <span className="muted-text">({groupName(hold.group)})</span>
      <div>
        {t("gates.readout_hold", {
          asked: minuteText(hold.asked),
          wait: duration((hold.grant - hold.asked) / 1000),
          held: duration((hold.release - hold.grant) / 1000),
        })}
      </div>
      {g.command && <code className="gates-readout-command">{g.command}</code>}
    </>
  );
}

function InstantReadout({ at, now }: { at: number; now: Instant }) {
  return (
    <>
      <strong>{minuteText(at)}</strong>
      <div>{t("gates.readout_running", { count: now.total })}</div>
      {now.running.map(([g, n]) => (
        <div key={g} className={`gates-readout-kind gk-${g}`}>
          <i className="gates-legend-swatch" aria-hidden="true" />
          {t("gates.readout_kind", { kind: groupName(g), count: n })}
        </div>
      ))}
      {now.holding.length > 0 && (
        <div className="gates-readout-hold">
          {now.holding.map(([gate, who]) => (
            <div key={gate}>
              <code>{gate}</code>{" "}
              {who.length > 3
                ? t("gates.readout_requests", { count: who.length })
                : who.join(", ")}
            </div>
          ))}
        </div>
      )}
      <div className="gates-readout-queue">
        {now.waiting.length
          ? t("gates.readout_waiting", {
              count: now.waiting.length,
              who: now.waiting
                .slice(0, 6)
                .map(
                  (h) =>
                    `${h.iv.taskCode || t("gates.kind_out")} → ${holdLabel(h)}`,
                )
                .join(", "),
            })
          : t("gates.readout_none_waiting")}
      </div>
    </>
  );
}

const OUTCOME_KEY: Record<Outcome, TextKey> = {
  now: "gates.outcome_now",
  short: "gates.outcome_short",
  long: "gates.outcome_long",
};

// The Sankey of the named gates' requests over the window: project → its gates → what they waited, the flows from a
// project to a gate split by the kind of the task that asked and coloured by it. A flow's width is its number of
// requests; its tooltip says it.
export const GatesSankey = memo(function GatesSankey({
  intervals,
  start,
  end,
}: {
  intervals: readonly GateInterval[];
  start: number;
  end: number;
}) {
  const flows = useMemo(
    () => sankeyFlows(holdsOf(intervals, end), start),
    [intervals, start, end],
  );
  const plot = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(960);
  useEffect(() => {
    const el = plot.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(([entry]) => {
      const w = Math.round(entry.contentRect.width);
      if (w > 0) setWidth(w);
    });
    observer.observe(el);
    return () => observer.disconnect();
  }, []);
  return (
    <section
      className="statistics-card gates-card gates-sankey"
      aria-label={t("gates.sankey")}
    >
      <header>
        <h3>{t("gates.sankey")}</h3>
      </header>
      <p className="statistics-detail muted-text">
        {t("gates.sankey_detail", { count: flows.total })}
      </p>
      {flows.kinds.length > 0 && (
        <ul className="gates-legend" aria-label={t("gates.legend")}>
          {flows.kinds.map((g) => (
            <li key={g} className={`gk-${g}`}>
              <i className="gates-legend-swatch" aria-hidden="true" />
              {groupName(g)}
            </li>
          ))}
        </ul>
      )}
      <div className="gates-sankey-plot" ref={plot}>
        {flows.total === 0 ? (
          <p className="statistics-empty muted-text">
            {t("gates.sankey_none")}
          </p>
        ) : (
          <SankeyPlot flows={flows} width={width} />
        )}
      </div>
    </section>
  );
});

function SankeyPlot({ flows, width }: { flows: Flows; width: number }) {
  const N = flows.total;
  const H = Math.round(Math.max(200, Math.min(420, 60 + N * 0.6)));
  const PAD = 10;
  const NW = 12;
  const TOP = 8;
  const cols: [string, number][][] = [
    flows.projects,
    flows.gates,
    flows.outcomes,
  ];
  const most = Math.max(...cols.map((c) => c.length));
  const k = (H - TOP * 2 - PAD * (most - 1)) / N;
  const label = Math.min(150, width * 0.22);
  const X = [label, width / 2 - NW / 2, width - Math.min(190, width * 0.26)];
  const pos = cols.map((c) => {
    const total = N * k + PAD * (c.length - 1);
    let y = TOP + (H - TOP * 2 - total) / 2;
    const out = new Map<
      string,
      { y: number; h: number; out: number; in: number }
    >();
    for (const [name, count] of c) {
      const h = Math.max(2, count * k);
      out.set(name, { y, h, out: 0, in: 0 });
      y += h + PAD;
    }
    return out;
  });
  const name = (i: number, n: string) =>
    i === 0
      ? projectName(n)
      : i === 2
        ? t(OUTCOME_KEY[n as Outcome])
        : gateOfKey(n);
  // A gate in a tooltip says its project too.
  const full = (i: number, n: string) =>
    i === 1
      ? `${projectName(n.slice(0, n.indexOf("\t")))} · ${gateOfKey(n)}`
      : name(i, n);
  const links: { d: string; cls: string; title: string }[] = [];
  const link = (
    i: number,
    from: string,
    to: string,
    count: number,
    cls: string,
    kind?: KindGroup,
  ) => {
    const a = pos[i].get(from)!;
    const b = pos[i + 1].get(to)!;
    const h = count * k;
    const y0 = a.y + a.out;
    const y1 = b.y + b.in;
    a.out += h;
    b.in += h;
    const x0 = X[i] + NW;
    const x1 = X[i + 1];
    const m = (x0 + x1) / 2;
    const f = (v: number) => v.toFixed(1);
    links.push({
      d:
        `M${f(x0)},${f(y0)}C${f(m)},${f(y0)} ${f(m)},${f(y1)} ${f(x1)},${f(y1)}` +
        `L${f(x1)},${f(y1 + h)}C${f(m)},${f(y1 + h)} ${f(m)},${f(y0 + h)} ${f(x0)},${f(y0 + h)}Z`,
      cls,
      title:
        t("gates.sankey_flow", {
          from: full(i, from),
          to: full(i + 1, to),
          count,
          share: Math.round((count / N) * 100),
        }) + (kind ? ` · ${groupName(kind)}` : ""),
    });
  };
  // A project's flows in its gates' order, then the kinds' order within a gate: the bands never cross in a project.
  for (const [p] of flows.projects)
    for (const [gate] of flows.gates)
      for (const kind of flows.kinds) {
        const f = flows.toGate.find(
          (x) => x.from === p && x.to === gate && x.kind === kind,
        );
        if (f) link(0, p, gate, f.count, `gk-${kind}`, kind);
      }
  for (const [gate] of flows.gates)
    for (const [o] of flows.outcomes) {
      const f = flows.toOutcome.find((x) => x.from === gate && x.to === o);
      if (f) link(1, gate, o, f.count, `go-${o}`);
    }
  return (
    <svg
      className="gates-sankey-svg"
      width={width}
      height={H}
      viewBox={`0 0 ${width} ${H}`}
      role="img"
      aria-label={t("gates.sankey")}
    >
      {links.map((l, i) => (
        <path key={i} className={`gates-sankey-link ${l.cls}`} d={l.d}>
          <title>{l.title}</title>
        </path>
      ))}
      {cols.map((c, i) =>
        c.map(([n, count]) => {
          const p = pos[i].get(n)!;
          const cls = i === 0 ? "is-project" : i === 2 ? `go-${n}` : "is-gate";
          return (
            <g key={`${i}-${n}`} className={`gates-sankey-node ${cls}`}>
              <rect x={X[i]} y={p.y} width={NW} height={p.h} rx={2} />
              <text
                x={i === 2 ? X[i] + NW + 8 : X[i] - 8}
                y={p.y + p.h / 2 + 4}
                textAnchor={i === 2 ? "start" : "end"}
              >
                {`${name(i, n)} ${count}`}
              </text>
            </g>
          );
        }),
      )}
    </svg>
  );
}

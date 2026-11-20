// The Gates tab of the Hourglass (hourglass.tsx, L05 v3 built on what exists, Q12 B): what the gates do now, read live
// while the tab shows (GateService.List), by project; the gates over the last hour, 6 hours or day, one lane per gate
// of each project under the workers that run, then a Sankey of the named gates' requests, project → gate → wait
// (gates-flow.tsx, L13 v2 A, Q46 A: GateService.History); what each command admitted under a gate costs, the costliest
// first, read a page at a time as the list scrolls (CommandService.List); and the waits for each gate in each project,
// over the recent runs the store keeps of each command (up to 200). A gate is a named lock: one command holds it at a
// time, the others wait their turn. A gate a project declares is its own (stack@dolmen); one named for the machine (lint,
// e2e) is a single lock for every project, shown under each project whose workers hold it or wait for it (W158). What
// waits for a command's admission mode (I1) is not shown.
//
// Under load the tab stays light (W124): the figures and the waits come summed by the server (GatesSummary), the
// commands a page of PAGE at a time, the next one read when the list's end comes into view, without their runs until a
// row opens; the history is read whole once, then only what changed since its last read (mergeHistory). Long lists show
// their head and fold the rest.
import { timestampFromMs } from "@bufbuild/protobuf/wkt";
import { memo, useEffect, useMemo, useRef, useState } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";

import type {
  CommandCost,
  CommandRun,
  Gate,
  GateInterval,
  GateServiceHistoryResponse,
  GatesSummary,
  GateWaits,
  WorkerRun,
} from "../gen/ts/machine/v1/machine_pb";
import type { Task } from "../gen/ts/plan/v1/plan_pb";
import { jump } from "./attention";
import { duration } from "./consumption-panel";
import { useDjinn } from "./data/djinn";
import { date, dayText, secondText, span, useNow } from "./data/format";
import { timeOf } from "./data/series";
import {
  GatesFlow,
  GatesSankey,
  PERIOD_EVERY,
  type Period,
  byProject,
  gateIn,
  projectName,
} from "./gates-flow";
import { t } from "./i18n";
import { formatValue } from "./resource-chart";
import { keepLive } from "./stillness";
import "./gates-tab.css";

// How often the gates are read while the tab shows, and the commands' page; the history's pace is its window's.
const GATES_EVERY = 2_000;
const COMMANDS_EVERY = 15_000;
// A wait longer than this counts apart: the one a worker notices (machine.LongWait).
const LONG_WAIT = 5;
// The commands of a page, the free gates and the gates' waits shown before a fold, and the runs of an open row.
export const PAGE = 25;
const FREE_SHOWN = 6;
const WAITS_SHOWN = 8;
const RUNS_SHOWN = 20;
// A history read after another asks this much before the last read: an interval kept late is not missed.
const OVERLAP = 60_000;

// waitText says a wait: 0s when none, as the window says a duration elsewhere.
function waitText(seconds: number): string {
  return seconds > 0 ? duration(seconds) : t("time.s", { s: 0 });
}

// total is the time a command's runs took: its runs times their mean duration.
function total(cost: CommandCost): number {
  return cost.runs * cost.seconds;
}

function memoryText(bytes: bigint | number): string {
  const n = Number(bytes);
  return n > 0 ? formatValue("memory", n) : "—";
}

function whenText(d?: Date): string {
  return d ? `${dayText(d)} ${secondText(d)}` : "—";
}

// intervalKey names a request of a gate across two reads of the history.
const intervalKey = (iv: GateInterval) =>
  `${iv.gate}|${iv.taskId}|${iv.command}|${timeOf(iv.askedTime)}|${timeOf(iv.grantTime)}`;
const runKey = (r: WorkerRun) => `${r.taskId}|${timeOf(r.startTime)}`;

// mergeHistory lays a read of what changed (next, asked after a time) over the window read before (prev): the
// intervals prev kept released inside the new window, unless next has them again, then next's (released, then held
// now); the runs next has, then prev's ended inside the window that next has not. A run prev saw open that next has
// not ended with its task: it goes.
export function mergeHistory(
  prev: GateServiceHistoryResponse,
  next: GateServiceHistoryResponse,
): GateServiceHistoryResponse {
  const start = timeOf(next.startTime);
  const fresh = new Set(next.intervals.map(intervalKey));
  const intervals = prev.intervals
    .filter(
      (iv) =>
        iv.releaseTime &&
        timeOf(iv.releaseTime) > start &&
        !fresh.has(intervalKey(iv)),
    )
    .concat(next.intervals);
  const ran = new Set(next.runs.map(runKey));
  const runs = prev.runs
    .filter(
      (r) => r.endTime && timeOf(r.endTime) > start && !ran.has(runKey(r)),
    )
    .concat(next.runs)
    .sort((a, b) => timeOf(a.startTime) - timeOf(b.startTime));
  return { ...next, intervals, runs };
}

const EMPTY: GateInterval[] = [];

// The tasks a gate's holder or a run names: their codes, and the way to them.
type Tasks = ReadonlyMap<string, Task>;

// The commands read so far: the pages from the first one on, and how many there are in all.
export interface Page {
  costs: CommandCost[];
  total: number;
}

// mergePage lays a page read from row from over the rows read before: the rows before it kept, the page's, then the
// rows after it kept unless the page has them (a ranking that moved between two reads shows each command once).
export function mergePage(
  prev: Page,
  from: number,
  costs: readonly CommandCost[],
  total: number,
): Page {
  const fresh = new Set(costs.map((c) => c.id));
  const before = prev.costs.slice(0, from).filter((c) => !fresh.has(c.id));
  const after = prev.costs
    .slice(from + costs.length)
    .filter((c) => !fresh.has(c.id));
  return {
    costs: [...before, ...costs, ...after].slice(0, Math.max(total, 0)),
    total,
  };
}

export function GatesTab({
  tasks,
  onWorker,
  onTask,
  initialGates = [],
  initialCommands = [],
  initialTotal,
  initialSummary = null,
  initialOpened = null,
  initialHistory = null,
  initialPeriod = 60,
}: {
  // The tasks of the active wishes, by id: a holder or a run says its task's code.
  tasks?: Tasks;
  // Shows a running worker's row in Machine.
  onWorker?: (taskId: string) => void;
  // Shows a task in its wish.
  onTask?: (task: Task) => void;
  initialGates?: Gate[];
  // The first page of the commands, and how many there are in all (its length when not given).
  initialCommands?: CommandCost[];
  initialTotal?: number;
  initialSummary?: GatesSummary | null;
  // A command open on its runs.
  initialOpened?: CommandCost | null;
  initialHistory?: GateServiceHistoryResponse | null;
  initialPeriod?: Period;
}) {
  const djinn = useDjinn();
  const [gates, setGates] = useState<Gate[]>(initialGates);
  const [page, setPage] = useState<Page>({
    costs: initialCommands,
    total: initialTotal ?? initialCommands.length,
  });
  // How many rows the list asked so far: a page more each time its end comes into view.
  const [wanted, setWanted] = useState(PAGE);
  const [summary, setSummary] = useState<GatesSummary | null>(initialSummary);
  const [history, setHistory] = useState<GateServiceHistoryResponse | null>(
    initialHistory,
  );
  // The window of the lanes and the Sankey, and whether the lanes show the admissions of the workers' commands.
  const [period, setPeriod] = useState<Period>(initialPeriod);
  const [admissions, setAdmissions] = useState(false);
  // The command open on its runs, read whole when it opens.
  const [opened, setOpened] = useState<CommandCost | null>(initialOpened);

  // The gates now: live while the tab shows.
  useEffect(() => {
    if (!djinn) return;
    let active = true;
    const read = async () => {
      const res = await djinn.clients.gates.list({}).catch(() => null);
      if (active && res) setGates(res.gates);
    };
    void read();
    const stop = keepLive(() => void read(), GATES_EVERY);
    return () => {
      active = false;
      stop();
    };
  }, [djinn]);

  // The history of the window: whole when the window is chosen, then what changed since the last read, an hour every
  // 5 s, 6 hours every 30 s, a day every minute.
  useEffect(() => {
    if (!djinn) return;
    let active = true;
    let last: GateServiceHistoryResponse | null = null;
    const read = async () => {
      const res = await djinn.clients.gates
        .history({
          minutes: period,
          afterTime: last?.readTime
            ? timestampFromMs(timeOf(last.readTime) - OVERLAP)
            : undefined,
        })
        .catch(() => null);
      if (!active || !res) return;
      last = last ? mergeHistory(last, res) : res;
      setHistory(last);
    };
    void read();
    const stop = keepLive(() => void read(), PERIOD_EVERY[period]);
    return () => {
      active = false;
      stop();
    };
  }, [djinn, period]);

  // The first page of the commands, without their runs, and the summary of them all: read again while the tab shows;
  // the pages under it stay as read.
  useEffect(() => {
    if (!djinn) return;
    let active = true;
    const read = async () => {
      const res = await djinn.clients.commands
        .list({ byTotal: true, brief: true, summary: true, pageSize: PAGE })
        .catch(() => null);
      if (!active || !res) return;
      setPage((prev) => mergePage(prev, 0, res.costs, res.total));
      setSummary(res.summary ?? null);
    };
    void read();
    const stop = keepLive(() => void read(), COMMANDS_EVERY);
    return () => {
      active = false;
      stop();
    };
  }, [djinn]);

  // The next page, once the list asks past the rows it has: read once, from where they end.
  const have = page.costs.length;
  useEffect(() => {
    if (!djinn || wanted <= have || have >= page.total) return;
    let active = true;
    void djinn.clients.commands
      .list({
        byTotal: true,
        brief: true,
        pageSize: PAGE,
        pageToken: String(have),
      })
      .then((res) => {
        if (active)
          setPage((prev) => mergePage(prev, have, res.costs, res.total));
      })
      .catch(() => null);
    return () => {
      active = false;
    };
  }, [djinn, wanted, have, page.total]);

  // Opens a command on its runs, read whole; closes it when it is the one open.
  const open = async (cost: { id: string }) => {
    if (opened?.id === cost.id) {
      setOpened(null);
      return;
    }
    const res = await djinn?.clients.commands
      .list({ id: cost.id })
      .catch(() => null);
    if (res?.costs[0]) setOpened(res.costs[0]);
  };

  // The window drawn: the one the history was read over; until it is read, the one asked, ending when the tab opened.
  const [openedAt] = useState(() => Date.now());
  const end = history?.readTime ? timeOf(history.readTime) : openedAt;
  const start = history?.startTime
    ? timeOf(history.startTime)
    : end - period * 60_000;
  const intervals = history?.intervals ?? EMPTY;

  // A band of the lanes opens its command's runs in the table, whatever page shows.
  const pick = async (g: GateInterval) => {
    const res = await djinn?.clients.commands
      .list({ command: g.command, project: g.project })
      .catch(() => null);
    const cost = res?.costs[0];
    if (!cost) return;
    setOpened(cost);
    requestAnimationFrame(() => jump(`command-${cost.id}`));
  };

  return (
    <div className="gates-tab">
      <GatesFigures summary={summary} />
      <GatesNow
        gates={gates}
        waits={summary?.gates ?? []}
        tasks={tasks}
        onWorker={onWorker}
      />
      <GatesFlow
        intervals={intervals}
        runs={history?.runs ?? []}
        start={start}
        end={end}
        period={period}
        onPeriod={setPeriod}
        admissions={admissions}
        onAdmissions={setAdmissions}
        onPick={(g) => void pick(g)}
      />
      <GatesSankey intervals={intervals} start={start} end={end} />
      <CommandTable
        page={page}
        opened={opened}
        tasks={tasks}
        onOpen={(c) => void open(c)}
        onMore={() => setWanted(have + PAGE)}
        onTask={onTask}
      />
      <GateWaitsCard waits={summary?.gates ?? []} />
    </div>
  );
}

// The four figures of the tab, over the commands' measured runs, summed by the server.
function GatesFigures({ summary }: { summary: GatesSummary | null }) {
  const holds = summary?.holds ?? 0;
  const asked = summary?.asked ?? 0;
  const ran = summary?.ran ?? 0;
  const long = summary?.longWaits ?? 0;
  const peak = summary?.peak;
  const figures: [string, string, string, string][] = [
    [
      "asked",
      t("gates.figure_asked"),
      String(asked),
      t("gates.figure_asked_sub", { ran, left: asked - ran }),
    ],
    [
      "wait",
      t("gates.figure_wait"),
      holds
        ? `${waitText(summary?.p50WaitSeconds ?? 0)} · ${waitText(summary?.p90WaitSeconds ?? 0)}`
        : "—",
      t("gates.figure_wait_sub", { count: holds }),
    ],
    [
      "long",
      t("gates.figure_long"),
      String(long),
      t("gates.figure_long_sub", {
        share: holds ? Math.round((long / holds) * 100) : 0,
        max: waitText(summary?.maxWaitSeconds ?? 0),
      }),
    ],
    [
      "peak",
      t("gates.figure_peak"),
      peak && peak.peakMemoryBytes > 0n
        ? memoryText(peak.peakMemoryBytes)
        : "—",
      peak && peak.peakMemoryBytes > 0n
        ? peak.command
        : t("gates.none_measured"),
    ],
  ];
  return (
    <div className="statistics-figures gates-figures">
      {figures.map(([id, label, value, sub]) => (
        <div key={id} className="statistics-figure" data-card={id}>
          <span className="statistics-figure-label">{label}</span>
          <strong className="statistics-figure-value">{value}</strong>
          <span className="statistics-figure-sub" title={sub}>
            {sub}
          </span>
        </div>
      ))}
    </div>
  );
}

// More is the button that unfolds what a list left out.
function More({ text, onClick }: { text: string; onClick: () => void }) {
  return (
    <button type="button" className="text-link gates-more" onClick={onClick}>
      {text}
    </button>
  );
}

// TaskCode is a task's code, a link to it when the way is given; the holder's text when its task is not known.
function TaskCode({
  taskId,
  tasks,
  onShow,
}: {
  taskId: string;
  tasks?: Tasks;
  onShow?: (task: Task) => void;
}) {
  const task = taskId ? tasks?.get(taskId) : undefined;
  if (!task) return null;
  if (!onShow) return <span className="agent-code">{task.code}</span>;
  return (
    <button
      type="button"
      className="text-link gates-task-link agent-code"
      title={task.title}
      onClick={() => onShow(task)}
    >
      {task.code}
    </button>
  );
}

// What a project has of a gate now: its holder when one of its workers holds it, its waiters in their order.
export interface NowEntry {
  gate: Gate;
  // The gate's own name, and whether its project declares it (stack@dolmen).
  name: string;
  declared: boolean;
  // The holder is this project's.
  holds: boolean;
  // The places in the gate's queue of this project's waiters.
  waiting: number[];
}

// A project in Now: what its workers hold and wait for, then the gates its recent runs name, free.
export interface NowGroup {
  project: string;
  live: NowEntry[];
  free: GateWaits[];
  held: number;
  waiting: number;
}

// nowGroups lays the gates now out by project. A gate a project declares is that project's; a gate of the machine
// shows under the project of its holder, and under each project with a worker in its queue, held by another then. The
// free gates come from the recent runs' waits, under their project, unless held or waited for now. The projects with a
// gate held or waited for first, the busiest first, then the others by name; none last.
export function nowGroups(
  gates: readonly Gate[],
  waits: readonly GateWaits[],
): NowGroup[] {
  const groups = new Map<string, NowGroup>();
  const group = (project: string) => {
    let g = groups.get(project);
    if (!g) {
      g = { project, live: [], free: [], held: 0, waiting: 0 };
      groups.set(project, g);
    }
    return g;
  };
  for (const gate of gates) {
    const { name, project: own, declared } = gateIn(gate.name, gate.project);
    const entries = new Map<string, NowEntry>();
    const entry = (project: string) => {
      let e = entries.get(project);
      if (!e) {
        e = { gate, name, declared, holds: false, waiting: [] };
        entries.set(project, e);
      }
      return e;
    };
    if (gate.holder) entry(own).holds = true;
    gate.waiting.forEach((_, i) => {
      const project = declared ? own : (gate.waitingProjects[i] ?? "");
      entry(project).waiting.push(i);
    });
    for (const [project, e] of entries) {
      const g = group(project);
      g.live.push(e);
      if (e.holds) g.held++;
      g.waiting += e.waiting.length;
    }
  }
  const live = new Set(gates.map((g) => g.name));
  for (const w of waits)
    if (!live.has(w.gate))
      group(gateIn(w.gate, w.project).project).free.push(w);
  const busy = (g: NowGroup) => g.held + g.waiting;
  return [...groups.values()].sort(
    (a, b) =>
      (!a.project ? 1 : 0) - (!b.project ? 1 : 0) ||
      busy(b) - busy(a) ||
      byProject(a.project, b.project),
  );
}

// Now: by project, each gate its workers hold or wait for, live, then the gates its recent runs name, free, with their
// last hold; past FREE_SHOWN, a project's free gates fold. A gate's card is the target of its links (gate-<name>):
// the first of its cards, its holder's when held.
const GatesNow = memo(function GatesNow({
  gates,
  waits,
  tasks,
  onWorker,
}: {
  gates: readonly Gate[];
  waits: readonly GateWaits[];
  tasks?: Tasks;
  onWorker?: (taskId: string) => void;
}) {
  const busy = gates.some((g) => g.holder || g.waiting.length > 0);
  const now = useNow(busy);
  const [unfolded, setUnfolded] = useState<ReadonlySet<string>>(
    () => new Set(),
  );
  const groups = useMemo(() => nowGroups(gates, waits), [gates, waits]);
  // The card a gate's link leads to: its holder's, else its first.
  const target = new Map<string, NowEntry>();
  for (const g of groups)
    for (const e of g.live)
      if (e.holds || !target.has(e.gate.name)) target.set(e.gate.name, e);
  const showWorker = onWorker ? (task: Task) => onWorker(task.id) : undefined;
  // A free gate of the machine may show under several projects: the first card is its links' target.
  const named = new Set(target.keys());
  const freeId = (gate: string) => {
    if (named.has(gate)) return undefined;
    named.add(gate);
    return `gate-${gate}`;
  };
  return (
    <section className="statistics-card gates-card" aria-label={t("gates.now")}>
      <header>
        <h3>{t("gates.now")}</h3>
      </header>
      <p className="statistics-detail muted-text">{t("gates.now_detail")}</p>
      {groups.length === 0 && (
        <p className="statistics-empty muted-text">{t("gates.none")}</p>
      )}
      {groups.map((group) => {
        const open = unfolded.has(group.project);
        const shown = open ? group.free : group.free.slice(0, FREE_SHOWN);
        return (
          <section
            key={group.project}
            className="gates-project"
            aria-label={projectName(group.project)}
          >
            <header className="gates-project-head">
              <h4>{projectName(group.project)}</h4>
              <span className="muted-text">
                {group.held + group.waiting > 0
                  ? t("gates.project_now", {
                      held: group.held,
                      waiting: group.waiting,
                    })
                  : t("gates.project_free", { count: group.free.length })}
              </span>
            </header>
            <ul className="gates-now">
              {group.live.map((e) => (
                <LiveGate
                  key={e.gate.name}
                  entry={e}
                  id={
                    target.get(e.gate.name) === e
                      ? `gate-${e.gate.name}`
                      : undefined
                  }
                  now={now}
                  tasks={tasks}
                  onWorker={showWorker}
                />
              ))}
              {shown.map((w) => (
                <FreeGate
                  key={w.gate}
                  id={freeId(w.gate)}
                  waits={w}
                  tasks={tasks}
                />
              ))}
            </ul>
            {group.free.length > shown.length && (
              <More
                text={t("gates.free_more", {
                  count: group.free.length - shown.length,
                })}
                onClick={() =>
                  setUnfolded((prev) => new Set(prev).add(group.project))
                }
              />
            )}
          </section>
        );
      })}
    </section>
  );
});

// A gate a project's workers hold or wait for: its holder when it is the project's, else who holds it and for which
// project; the project's waiters.
function LiveGate({
  entry,
  id,
  now,
  tasks,
  onWorker,
}: {
  entry: NowEntry;
  id?: string;
  now: number;
  tasks?: Tasks;
  onWorker?: (task: Task) => void;
}) {
  const { gate: g, name, declared, holds, waiting } = entry;
  const held = g.holder && holds;
  const state = !g.holder
    ? "free"
    : held && g.takesSlot
      ? "heavy"
      : waiting.length > 0
        ? "wait"
        : "held";
  return (
    <li
      id={id}
      className={`gates-gate ${held ? "is-held" : "is-waited"}`}
      data-state={state}
    >
      <Seal waiters={waiting.length} />
      <div className="gates-gate-head">
        <code className="gates-gate-name">{name}</code>
        <span className="gates-gate-state">
          {held ? t("gates.held") : t("gates.waited")}
        </span>
        {declared && (
          <span className="gates-gate-note">{t("gates.declared")}</span>
        )}
        {g.ifrit && <span className="gates-gate-note">{t("gates.ifrit")}</span>}
        {g.takesSlot && held && (
          <span className="gates-gate-note">{t("gates.takes_slot")}</span>
        )}
      </div>
      {held && (
        <p className="gates-gate-holder">
          <TaskCode taskId={g.holderTaskId} tasks={tasks} onShow={onWorker} />{" "}
          {t("gates.held_by", {
            holder: g.holder,
            time: g.since ? span(now - timeOf(g.since)) : "—",
          })}
        </p>
      )}
      {g.holder && !holds && (
        <p className="gates-gate-line muted-text">
          {t("gates.held_elsewhere", {
            holder: g.holder,
            project: projectName(g.project),
          })}
        </p>
      )}
      {held && g.resources && (
        <p className="gates-gate-line muted-text">
          {t("gates.uses", {
            cpu: formatValue("cpu", g.resources.cpuPercent),
            memory: memoryText(g.resources.memoryBytes),
          })}
        </p>
      )}
      {held && g.expireTime && (
        <p className="gates-gate-line muted-text">
          {t("gates.expires", {
            time: secondText(date(g.expireTime) ?? new Date(0)),
          })}
        </p>
      )}
      {waiting.length > 0 && (
        <div className="gates-gate-queue">
          <span className="gates-gate-queue-title">
            {t("gates.queue", { count: waiting.length })}
          </span>
          <ol>
            {waiting.map((i) => (
              <li key={i} value={i + 1}>
                <TaskCode
                  taskId={waiterTask(g, i)}
                  tasks={tasks}
                  onShow={onWorker}
                />{" "}
                {g.waiting[i]}
              </li>
            ))}
          </ol>
        </div>
      )}
    </li>
  );
}

// The seal of a gate (L42 C): the door in the loader's octagon, its edge in the gate's state (data-state on the card:
// free, held, wait, heavy, gates-tab.css). Free, the door stands open in emerald light; held, it is shut, the keyhole
// in the holder's blue; waited for, a gold flame per waiter sits on the orbit; heavy (it takes a worker's slot), a bar
// crosses it. The orbits turn while the gate is held. Each loop is an element of its own moved by transform alone: WebKit
// repaints a whole SVG for any of its parts (W513), so the octagon and the door, which only change with the state,
// never repaint for a frame of the loops.
const SEAL_FLAMES = 5;
const ARCH = "M9 46V20.5C9 13 14 8.5 20 6C26 8.5 31 13 31 20.5V46Z";
const FLAME =
  "M12 2s7 7.5 7 14a7 7 0 0 1-14 0c0-5 4-9 4-9s-.6 4.3 2.4 6C10.6 8.6 12 2 12 2z";

const Seal = memo(function Seal({ waiters }: { waiters: number }) {
  const flames = Array.from(
    { length: Math.min(waiters, SEAL_FLAMES) },
    (_, i) => {
      // On the orbit's upper left, the next one first, each 28 degrees further: its centre, in the seal's 76 units.
      const a = ((-120 + i * 28) * Math.PI) / 180;
      return {
        left: `${((38 + 35 * Math.cos(a)) / 76) * 100}%`,
        top: `${((38 + 35 * Math.sin(a)) / 76) * 100}%`,
      };
    },
  );
  return (
    <div className="gates-seal" aria-hidden="true">
      <svg className="gates-seal-orbit" viewBox="0 0 76 76">
        <path className="gates-seal-arc" d="M38 3C55 3 70 15 73 31" />
        <path className="gates-seal-arc is-mint" d="M38 73C21 73 6 61 3 45" />
      </svg>
      <svg className="gates-seal-frame" viewBox="0 0 76 76">
        <polygon
          className="gates-seal-oct"
          points="38,11 46,19 57,19 57,30 65,38 57,46 57,57 46,57 38,65 30,57 19,57 19,46 11,38 19,30 19,19 30,19"
        />
      </svg>
      <svg className="gates-door" viewBox="0 0 40 48">
        <path className="gates-door-light" d={ARCH} />
        <path
          className="gates-door-spark"
          d="M20 18.5l1.1 2.9 2.9 1.1-2.9 1.1L20 26.5l-1.1-2.9-2.9-1.1 2.9-1.1z"
        />
        <g className="gates-door-leaf is-left">
          <path d="M9 46V20.5C9 13 14 8.5 20 6V46Z" />
          <path
            className="gates-door-fili"
            d="M12.5 43V22C12.5 17.5 15 13.8 17.5 12"
          />
          <path className="gates-door-fili" d="M16.5 43V20" />
        </g>
        <g className="gates-door-leaf is-right">
          <path d="M31 46V20.5C31 13 26 8.5 20 6V46Z" />
          <path
            className="gates-door-fili"
            d="M27.5 43V22C27.5 17.5 25 13.8 22.5 12"
          />
          <path className="gates-door-fili" d="M23.5 43V20" />
        </g>
        <path
          className="gates-door-key"
          d="M20 27.2a1.5 1.5 0 0 1 .7 2.8l.5 2.6h-2.4l.5-2.6a1.5 1.5 0 0 1 .7-2.8z"
        />
        <path className="gates-door-bolt" d="M7 31.5H33" />
        <path
          className="gates-door-frame"
          d="M6 46V20C6 11 13 5 20 2C27 5 34 11 34 20V46"
        />
        <path className="gates-door-sill" d="M4 46.6H36" />
      </svg>
      {flames.map((at, i) => (
        <svg
          key={i}
          className="gates-seal-flame"
          viewBox="0 0 24 30"
          style={{ ...at, animationDelay: `${-0.8 * i}s` }}
        >
          <path d={FLAME} />
        </svg>
      ))}
    </div>
  );
});

// How a waiter outside any task is named (gate.Take).
const OUTSIDE = "a holder outside any task";

// waiterTask is the task of the i-th waiter of a gate: waiting_task_ids leaves the waiters outside any task out, so the
// waiters before i that are outside any task do not count.
function waiterTask(g: Gate, i: number): string {
  if (g.waitingTaskIds.length === g.waiting.length) return g.waitingTaskIds[i];
  if (g.waiting[i].startsWith(OUTSIDE)) return "";
  const k = g.waiting.slice(0, i).filter((w) => !w.startsWith(OUTSIDE)).length;
  return g.waitingTaskIds[k] ?? "";
}

// A gate no one holds or waits for, with its last hold.
function FreeGate({
  id,
  waits,
  tasks,
}: {
  id?: string;
  waits: GateWaits;
  tasks?: Tasks;
}) {
  const last = waits.last;
  const code = last ? tasks?.get(last.taskId)?.code : undefined;
  const { name, declared } = gateIn(waits.gate, waits.project);
  return (
    <li id={id} className="gates-gate" data-state="free">
      <Seal waiters={0} />
      <div className="gates-gate-head">
        <code className="gates-gate-name">{name}</code>
        <span className="gates-gate-state">{t("gates.free")}</span>
        {declared && (
          <span className="gates-gate-note">{t("gates.declared")}</span>
        )}
      </div>
      {last && (
        <p className="gates-gate-line muted-text">
          {last.time
            ? t("gates.last_hold", {
                when: whenText(date(last.time)),
                command: waits.lastCommand,
              })
            : t("gates.last_hold_command", { command: waits.lastCommand })}
          {code && ` · ${code}`}
          {last.peakMemoryBytes > 0n &&
            ` · ${memoryText(last.peakMemoryBytes)}`}
        </p>
      )}
    </li>
  );
}

// The commands admitted under a gate, the most total time first: the one worth making faster on top. The list grows a
// page of PAGE rows at a time, as its end comes into view (or its button is pressed). A row opens on its recent runs; a
// command opened from the lanes, not read yet, shows above the list.
const CommandTable = memo(function CommandTable({
  page,
  opened,
  tasks,
  onOpen,
  onMore,
  onTask,
}: {
  page: Page;
  opened: CommandCost | null;
  tasks?: Tasks;
  onOpen: (cost: CommandCost) => void;
  // Asks the next page.
  onMore: () => void;
  onTask?: (task: Task) => void;
}) {
  const { costs } = page;
  // The first row is the costliest of all: its bar is the scale of every row.
  const scale = costs.length ? total(costs[0]) : 0;
  const rows =
    opened && !costs.some((c) => c.id === opened.id)
      ? [opened, ...costs]
      : costs;
  const more = page.total > costs.length;
  return (
    <section
      className="statistics-card gates-card"
      aria-label={t("gates.commands")}
    >
      <header>
        <h3>{t("gates.commands")}</h3>
      </header>
      <p className="statistics-detail muted-text">
        {t("gates.commands_detail")}
      </p>
      {rows.length === 0 ? (
        <p className="statistics-empty muted-text">{t("gates.no_commands")}</p>
      ) : (
        <div className="gates-table-scroll">
          <table className="gates-table">
            <thead>
              <tr>
                <th scope="col">{t("gates.col_command")}</th>
                <th scope="col">{t("gates.col_project")}</th>
                <th scope="col">{t("gates.col_gate")}</th>
                <th scope="col" className="num-col">
                  {t("gates.col_runs")}
                </th>
                <th scope="col" className="num-col">
                  {t("gates.col_mean")}
                </th>
                <th scope="col" className="num-col">
                  {t("gates.col_total")}
                </th>
                <th scope="col" className="num-col">
                  {t("gates.col_peak")}
                </th>
                <th scope="col" className="num-col">
                  {t("gates.col_cpu")}
                </th>
                <th scope="col" className="num-col">
                  {t("gates.col_wait")}
                </th>
              </tr>
            </thead>
            <tbody>
              {rows.map((c) => {
                const shown = opened?.id === c.id;
                return (
                  <CommandRows
                    key={c.id}
                    cost={c}
                    runs={shown ? opened.recentRuns : null}
                    top={c === costs[0] && total(c) > 0}
                    picked={c === opened && !costs.includes(c)}
                    share={scale > 0 ? total(c) / scale : 0}
                    tasks={tasks}
                    onToggle={() => onOpen(c)}
                    onTask={onTask}
                  />
                );
              })}
            </tbody>
          </table>
        </div>
      )}
      {costs.length > 0 && (
        <ListEnd
          shown={costs.length}
          total={page.total}
          more={more}
          onMore={onMore}
        />
      )}
    </section>
  );
});

// scroller is the nearest pane above el that scrolls vertically; null for the page itself.
function scroller(el: HTMLElement): HTMLElement | null {
  for (let p = el.parentElement; p; p = p.parentElement) {
    const y = getComputedStyle(p).overflowY;
    if (y === "auto" || y === "scroll") return p;
  }
  return null;
}

// The end of the commands' list: how many it shows of all, and, while some are left, what reads the next page once it
// comes into view (an IntersectionObserver over the scrolled page), or when its button is pressed.
function ListEnd({
  shown,
  total: all,
  more,
  onMore,
}: {
  shown: number;
  total: number;
  more: boolean;
  onMore: () => void;
}) {
  const end = useRef<HTMLDivElement>(null);
  const ask = useRef(onMore);
  ask.current = onMore;
  useEffect(() => {
    const el = end.current;
    if (!el || !more || typeof IntersectionObserver === "undefined") return;
    // A margin under the view of the pane that scrolls: the next page is read before the reader reaches the end.
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((e) => e.isIntersecting)) ask.current();
      },
      { root: scroller(el), rootMargin: "0px 0px 400px 0px" },
    );
    observer.observe(el);
    return () => observer.disconnect();
  }, [more, shown]);
  return (
    <div className="gates-list-end" ref={end}>
      <span className="muted-text">
        {t("gates.shown", { shown, total: all })}
      </span>
      {more && (
        <button type="button" className="text-link" onClick={onMore}>
          {t("gates.show_more", { count: Math.min(PAGE, all - shown) })}
        </button>
      )}
    </div>
  );
}

function CommandRows({
  cost,
  runs,
  top,
  picked,
  share,
  tasks,
  onToggle,
  onTask,
}: {
  cost: CommandCost;
  // Its recent runs when open, read whole; null when closed.
  runs: readonly CommandRun[] | null;
  top: boolean;
  // Opened from the lanes, off the page.
  picked: boolean;
  share: number;
  tasks?: Tasks;
  onToggle: () => void;
  onTask?: (task: Task) => void;
}) {
  const shown = runs !== null;
  return (
    <>
      <tr
        id={`command-${cost.id}`}
        className={`gates-command${top ? " is-top" : ""}${picked ? " is-picked" : ""}`}
      >
        <th scope="row" className="gates-command-name">
          <button
            type="button"
            className="gates-command-open"
            aria-expanded={shown}
            onClick={onToggle}
            disabled={cost.runs === 0}
          >
            {shown ? (
              <ChevronDown size={12} aria-hidden="true" />
            ) : (
              <ChevronRight size={12} aria-hidden="true" />
            )}
            <code>{cost.command}</code>
          </button>
          {top && <span className="gates-command-top">{t("gates.top")}</span>}
        </th>
        <td>{cost.projectName || "—"}</td>
        <td>
          <code>{cost.gate || "—"}</code>
        </td>
        <td
          className="num-col"
          title={t("gates.runs_title", { asked: cost.asked, ran: cost.runs })}
        >
          {cost.runs}
        </td>
        <td className="num-col">{duration(cost.seconds)}</td>
        <td className="num-col gates-command-total">
          {duration(total(cost))}
          <span className="gates-share" aria-hidden="true">
            <span
              style={{ width: `${Math.round(Math.min(1, share) * 100)}%` }}
            />
          </span>
        </td>
        <td className="num-col">{memoryText(cost.peakMemoryBytes)}</td>
        <td className="num-col">{duration(cost.cpuSeconds)}</td>
        <td className="num-col">
          {t("gates.p50_p95", {
            p50: waitText(cost.p50WaitSeconds),
            p95: waitText(cost.p95WaitSeconds),
          })}
        </td>
      </tr>
      {runs && (
        <tr className="gates-command-runs">
          <td colSpan={9}>
            <CommandRunList runs={runs} tasks={tasks} onTask={onTask} />
          </td>
        </tr>
      )}
    </>
  );
}

// The recent runs of an open command, the newest first: RUNS_SHOWN, then all on demand.
function CommandRunList({
  runs,
  tasks,
  onTask,
}: {
  runs: readonly CommandRun[];
  tasks?: Tasks;
  onTask?: (task: Task) => void;
}) {
  const [all, setAll] = useState(false);
  const sorted = useMemo(
    () => [...runs].sort((a, b) => timeOf(b.time) - timeOf(a.time)),
    [runs],
  );
  const shown = all ? sorted : sorted.slice(0, RUNS_SHOWN);
  return (
    <>
      <table className="gates-runs">
        <caption>{t("gates.runs", { count: sorted.length })}</caption>
        <thead>
          <tr>
            <th scope="col">{t("gates.col_when")}</th>
            <th scope="col">{t("gates.col_worker")}</th>
            <th scope="col" className="num-col">
              {t("gates.col_duration")}
            </th>
            <th scope="col" className="num-col">
              {t("gates.col_peak")}
            </th>
            <th scope="col" className="num-col">
              {t("gates.col_cpu_time")}
            </th>
            <th scope="col" className="num-col">
              {t("gates.col_waited")}
            </th>
            <th scope="col" className="num-col">
              {t("gates.col_exit")}
            </th>
          </tr>
        </thead>
        <tbody>
          {shown.map((r, i) => (
            <tr key={i}>
              <td>{whenText(date(r.time))}</td>
              <td>
                <TaskCode taskId={r.taskId} tasks={tasks} onShow={onTask} />
              </td>
              <td className="num-col">{duration(r.seconds)}</td>
              <td className="num-col">{memoryText(r.peakMemoryBytes)}</td>
              <td className="num-col">{duration(r.cpuSeconds)}</td>
              <td className="num-col">{waitText(r.waitSeconds)}</td>
              <td
                className={`num-col${r.exitCode !== 0 ? " gates-exit-failed" : ""}`}
              >
                {r.exitCode}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {sorted.length > shown.length && (
        <More
          text={t("gates.runs_more", { count: sorted.length })}
          onClick={() => setAll(true)}
        />
      )}
    </>
  );
}

// The waits for each gate in each project: a median and a 90th percentile, never a sum or a mean (Statistics' rule);
// the most held WAITS_SHOWN first, the rest on demand.
function GateWaitsCard({ waits }: { waits: readonly GateWaits[] }) {
  const [all, setAll] = useState(false);
  const shown = all ? waits : waits.slice(0, WAITS_SHOWN);
  return (
    <section
      className="statistics-card gates-card"
      aria-label={t("gates.waits")}
    >
      <header>
        <h3>{t("gates.waits")}</h3>
      </header>
      <p className="statistics-detail muted-text">{t("gates.waits_detail")}</p>
      {waits.length === 0 ? (
        <p className="statistics-empty muted-text">{t("gates.no_waits")}</p>
      ) : (
        <div className="gates-table-scroll">
          <table className="gates-table">
            <thead>
              <tr>
                <th scope="col">{t("gates.col_gate")}</th>
                <th scope="col">{t("gates.col_project")}</th>
                <th scope="col" className="num-col">
                  {t("gates.col_holds")}
                </th>
                <th scope="col" className="num-col">
                  p50
                </th>
                <th scope="col" className="num-col">
                  p90
                </th>
                <th scope="col" className="num-col">
                  {t("gates.col_max")}
                </th>
                <th scope="col" className="num-col">
                  {t("gates.col_long", { seconds: LONG_WAIT })}
                </th>
              </tr>
            </thead>
            <tbody>
              {shown.map((w) => (
                <tr key={`${w.gate}|${w.project}`} className="gates-wait-row">
                  <th scope="row">
                    <code>{gateIn(w.gate, w.project).name}</code>
                  </th>
                  <td>{projectName(gateIn(w.gate, w.project).project)}</td>
                  <td className="num-col">{w.holds}</td>
                  <td className="num-col">{waitText(w.p50Seconds)}</td>
                  <td className="num-col">{waitText(w.p90Seconds)}</td>
                  <td className="num-col">{waitText(w.maxSeconds)}</td>
                  <td className="num-col">{w.long}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {waits.length > shown.length && (
        <More
          text={t("gates.waits_more", { count: waits.length - shown.length })}
          onClick={() => setAll(true)}
        />
      )}
    </section>
  );
}

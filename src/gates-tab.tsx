// The Gates tab of the Hourglass (hourglass.tsx, L05 v3 built on what exists, Q12 B): what the gates do now, read live
// while the tab shows (GateService.List); the gates over the last hour, 6 hours or day, one lane per gate under the
// workers that run, then a Sankey of the named gates' requests (gates-flow.tsx, L13 v2 A, Q46 A: GateService.History);
// what each command admitted under a gate costs, the costliest first, a page at a time (CommandService.List); and the
// waits for each gate, over the recent runs the store keeps of each command (up to 200). A gate is a named lock: one
// command holds it at a time, the others wait their turn. What waits for a command's admission mode (I1) is not shown.
//
// Under load the tab stays light (W124): the figures and the waits come summed by the server (GatesSummary), the
// commands a page of PAGE, without their runs until a row opens; the history is read whole once, then only what
// changed since its last read (mergeHistory). Long lists show their head and fold the rest.
import { timestampFromMs } from "@bufbuild/protobuf/wkt";
import { memo, useEffect, useMemo, useState } from "react";
import { ChevronDown, ChevronLeft, ChevronRight } from "lucide-react";

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

// A page of the commands: its rows, where it starts, how many there are in all.
interface Page {
  costs: CommandCost[];
  from: number;
  total: number;
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
    from: 0,
    total: initialTotal ?? initialCommands.length,
  });
  // The first row of the page asked: the page shown follows once read.
  const [from, setFrom] = useState(0);
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

  // The page of the commands shown, without their runs, and the summary of them all.
  useEffect(() => {
    if (!djinn) return;
    let active = true;
    const read = async () => {
      const res = await djinn.clients.commands
        .list({
          byTotal: true,
          brief: true,
          summary: true,
          pageSize: PAGE,
          pageToken: from > 0 ? String(from) : "",
        })
        .catch(() => null);
      if (!active || !res) return;
      setPage({ costs: res.costs, from, total: res.total });
      setSummary(res.summary ?? null);
    };
    void read();
    const stop = keepLive(() => void read(), COMMANDS_EVERY);
    return () => {
      active = false;
      stop();
    };
  }, [djinn, from]);

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
        onPage={setFrom}
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

// Now: each gate held or waited for, live, then the gates the recent runs name, free, with their last hold; past
// FREE_SHOWN, the free gates fold.
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
  const [unfolded, setUnfolded] = useState(false);
  const live = new Set(gates.map((g) => g.name));
  const free = waits.filter((w) => !live.has(w.gate));
  const shown = unfolded ? free : free.slice(0, FREE_SHOWN);
  return (
    <section className="statistics-card gates-card" aria-label={t("gates.now")}>
      <header>
        <h3>{t("gates.now")}</h3>
      </header>
      <p className="statistics-detail muted-text">{t("gates.now_detail")}</p>
      {gates.length === 0 && free.length === 0 && (
        <p className="statistics-empty muted-text">{t("gates.none")}</p>
      )}
      <ul className="gates-now">
        {gates.map((g) => (
          <li
            key={g.name}
            id={`gate-${g.name}`}
            className={`gates-gate ${g.holder ? "is-held" : "is-waited"}`}
          >
            <div className="gates-gate-head">
              <code className="gates-gate-name">{g.name}</code>
              <span className="gates-gate-state">
                {g.holder ? t("gates.held") : t("gates.waited")}
              </span>
              {g.ifrit && (
                <span className="gates-gate-note">{t("gates.ifrit")}</span>
              )}
              {g.takesSlot && (
                <span className="gates-gate-note">{t("gates.takes_slot")}</span>
              )}
            </div>
            {g.holder && (
              <p className="gates-gate-holder">
                <TaskCode
                  taskId={g.holderTaskId}
                  tasks={tasks}
                  onShow={onWorker ? (task) => onWorker(task.id) : undefined}
                />{" "}
                {t("gates.held_by", {
                  holder: g.holder,
                  time: g.since ? span(now - timeOf(g.since)) : "—",
                })}
              </p>
            )}
            {g.resources && (
              <p className="gates-gate-line muted-text">
                {t("gates.uses", {
                  cpu: formatValue("cpu", g.resources.cpuPercent),
                  memory: memoryText(g.resources.memoryBytes),
                })}
              </p>
            )}
            {g.expireTime && (
              <p className="gates-gate-line muted-text">
                {t("gates.expires", {
                  time: secondText(date(g.expireTime) ?? new Date(0)),
                })}
              </p>
            )}
            {g.waiting.length > 0 && (
              <div className="gates-gate-queue">
                <span className="gates-gate-queue-title">
                  {t("gates.queue", { count: g.waiting.length })}
                </span>
                <ol>
                  {g.waiting.map((who, i) => (
                    <li key={i}>
                      <TaskCode
                        taskId={g.waitingTaskIds[i] ?? ""}
                        tasks={tasks}
                        onShow={
                          onWorker ? (task) => onWorker(task.id) : undefined
                        }
                      />{" "}
                      {who}
                    </li>
                  ))}
                </ol>
              </div>
            )}
          </li>
        ))}
        {shown.map((w) => (
          <FreeGate key={w.gate} waits={w} tasks={tasks} />
        ))}
      </ul>
      {free.length > shown.length && (
        <More
          text={t("gates.free_more", { count: free.length - shown.length })}
          onClick={() => setUnfolded(true)}
        />
      )}
    </section>
  );
});

// A gate no one holds or waits for, with its last hold.
function FreeGate({ waits, tasks }: { waits: GateWaits; tasks?: Tasks }) {
  const last = waits.last;
  const code = last ? tasks?.get(last.taskId)?.code : undefined;
  return (
    <li id={`gate-${waits.gate}`} className="gates-gate">
      <div className="gates-gate-head">
        <code className="gates-gate-name">{waits.gate}</code>
        <span className="gates-gate-state">{t("gates.free")}</span>
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

// The commands admitted under a gate, a page of PAGE rows, the most total time first: the one worth making faster on
// top. A row opens on its recent runs; a command opened from the lanes off the page shows above it.
const CommandTable = memo(function CommandTable({
  page,
  opened,
  tasks,
  onOpen,
  onPage,
  onTask,
}: {
  page: Page;
  opened: CommandCost | null;
  tasks?: Tasks;
  onOpen: (cost: CommandCost) => void;
  onPage: (from: number) => void;
  onTask?: (task: Task) => void;
}) {
  const { costs, from } = page;
  // The top of the first page is the costliest of all: its bar is the scale of every page.
  const [top, setTop] = useState(0);
  const first = from === 0 && costs.length ? total(costs[0]) : 0;
  if (first > 0 && first !== top) setTop(first);
  const scale = top || (costs.length ? total(costs[0]) : 0);
  const rows =
    opened && !costs.some((c) => c.id === opened.id)
      ? [opened, ...costs]
      : costs;
  const to = from + costs.length;
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
                    top={from === 0 && c === costs[0] && total(c) > 0}
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
      {page.total > costs.length && (
        <nav className="gates-pager" aria-label={t("gates.pages")}>
          <button
            type="button"
            className="gates-pager-step"
            aria-label={t("gates.page_prev")}
            title={t("gates.page_prev")}
            disabled={from === 0}
            onClick={() => onPage(Math.max(0, from - PAGE))}
          >
            <ChevronLeft size={12} aria-hidden="true" />
          </button>
          <span className="muted-text">
            {t("gates.page", {
              from: costs.length ? from + 1 : from,
              to,
              total: page.total,
            })}
          </span>
          <button
            type="button"
            className="gates-pager-step"
            aria-label={t("gates.page_next")}
            title={t("gates.page_next")}
            disabled={to >= page.total}
            onClick={() => onPage(from + PAGE)}
          >
            <ChevronRight size={12} aria-hidden="true" />
          </button>
        </nav>
      )}
    </section>
  );
});

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

// The waits for each gate: a median and a 90th percentile, never a sum or a mean (Statistics' rule); the most held
// WAITS_SHOWN first, the rest on demand.
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
                <tr key={w.gate} className="gates-wait-row">
                  <th scope="row">
                    <code>{w.gate}</code>
                  </th>
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

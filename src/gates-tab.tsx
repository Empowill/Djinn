// The Gates tab of the Hourglass (hourglass.tsx, L05 v3 built on what exists, Q12 B): what the gates do now, read live
// while the tab shows (GateService.List); the gates over the last hour, 6 hours or day, one lane per gate under the
// workers that run, then a Sankey of the named gates' requests (gates-flow.tsx, L13 v2 A, Q46 A: GateService.History);
// what each command admitted under a gate costs, the costliest first (CommandService.List); and the waits for each
// gate, over the recent runs the store keeps of each command (up to 200). A gate is a named lock: one command holds it
// at a time, the others wait their turn. What waits for a command's admission mode (I1) is not shown.
import { memo, useEffect, useMemo, useState } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";

import type {
  CommandCost,
  CommandRun,
  Gate,
  GateInterval,
  GateServiceHistoryResponse,
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

// How often the gates are read while the tab shows, and the commands' costs; the history's pace is its window's.
const GATES_EVERY = 2_000;
const COMMANDS_EVERY = 15_000;
// A wait longer than this counts apart: the one a worker notices.
const LONG_WAIT = 5;

// A run of a command under its gate, with the command it ran.
interface GateRun {
  gate: string;
  cost: CommandCost;
  run: CommandRun;
}

// runsOf is every recent run of the commands, each under its gate (the run's, else its command's); none without one.
function runsOf(costs: readonly CommandCost[]): GateRun[] {
  return costs.flatMap((cost) =>
    cost.recentRuns
      .map((run) => ({ gate: run.gate || cost.gate, cost, run }))
      .filter((r) => r.gate),
  );
}

// quantile is the nearest-rank quantile of values sorted ascending; 0 for none.
function quantile(sorted: readonly number[], q: number): number {
  if (sorted.length === 0) return 0;
  return sorted[
    Math.min(sorted.length - 1, Math.max(0, Math.ceil(q * sorted.length) - 1))
  ];
}

// waitText says a wait: 0s when none, as the window says a duration elsewhere.
function waitText(seconds: number): string {
  return seconds > 0 ? duration(seconds) : t("time.s", { s: 0 });
}

// keyOf names a command's line: its id, else its project and command, which the store keeps unique.
function keyOf(cost: CommandCost): string {
  return cost.id || `${cost.projectId} ${cost.command}`;
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

export interface GatesWaits {
  gate: string;
  holds: number;
  p50: number;
  p90: number;
  max: number;
  long: number;
  // The newest run under the gate.
  last?: GateRun;
}

// gateWaits is the waits for each gate over the recent runs, the most held gate first.
export function gateWaits(costs: readonly CommandCost[]): GatesWaits[] {
  const byGate = new Map<string, GateRun[]>();
  for (const r of runsOf(costs)) {
    const list = byGate.get(r.gate);
    if (list) list.push(r);
    else byGate.set(r.gate, [r]);
  }
  return [...byGate]
    .map(([gate, runs]) => {
      const waits = runs.map((r) => r.run.waitSeconds).sort((a, b) => a - b);
      let last: GateRun | undefined;
      for (const r of runs)
        if (!last || timeOf(r.run.time) > timeOf(last.run.time)) last = r;
      return {
        gate,
        holds: runs.length,
        p50: quantile(waits, 0.5),
        p90: quantile(waits, 0.9),
        max: waits.at(-1) ?? 0,
        long: waits.filter((w) => w > LONG_WAIT).length,
        last,
      };
    })
    .sort((a, b) => b.holds - a.holds || a.gate.localeCompare(b.gate));
}

const EMPTY: GateInterval[] = [];

// The tasks a gate's holder or a run names: their codes, and the way to them.
type Tasks = ReadonlyMap<string, Task>;

export function GatesTab({
  tasks,
  onWorker,
  onTask,
  initialGates = [],
  initialCommands = [],
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
  initialCommands?: CommandCost[];
  initialHistory?: GateServiceHistoryResponse | null;
  initialPeriod?: Period;
}) {
  const djinn = useDjinn();
  const [gates, setGates] = useState<Gate[]>(initialGates);
  const [costs, setCosts] = useState<CommandCost[]>(initialCommands);
  const [history, setHistory] = useState<GateServiceHistoryResponse | null>(
    initialHistory,
  );
  // The window of the lanes and the Sankey, and whether the lanes show the admissions of the workers' commands.
  const [period, setPeriod] = useState<Period>(initialPeriod);
  const [admissions, setAdmissions] = useState(false);
  // The command whose runs show, by id.
  const [open, setOpen] = useState("");

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

  // The history of the window, read whole each time: an hour every 5 s, 6 hours every 30 s, a day every minute.
  useEffect(() => {
    if (!djinn) return;
    let active = true;
    const read = async () => {
      const res = await djinn.clients.gates
        .history({ minutes: period })
        .catch(() => null);
      if (active && res) setHistory(res);
    };
    void read();
    const stop = keepLive(() => void read(), PERIOD_EVERY[period]);
    return () => {
      active = false;
      stop();
    };
  }, [djinn, period]);

  useEffect(() => {
    if (!djinn) return;
    let active = true;
    const read = async () => {
      const res = await djinn.clients.commands.list({}).catch(() => null);
      if (active && res) setCosts(res.costs);
    };
    void read();
    const stop = keepLive(() => void read(), COMMANDS_EVERY);
    return () => {
      active = false;
      stop();
    };
  }, [djinn]);

  const waits = useMemo(() => gateWaits(costs), [costs]);
  const sorted = useMemo(
    () => [...costs].sort((a, b) => total(b) - total(a)),
    [costs],
  );

  // The window drawn: the one the history was read over; until it is read, the one asked, ending when the tab opened.
  const [opened] = useState(() => Date.now());
  const end = history?.readTime ? timeOf(history.readTime) : opened;
  const start = history?.startTime
    ? timeOf(history.startTime)
    : end - period * 60_000;
  const intervals = history?.intervals ?? EMPTY;

  // A band of the lanes opens its command's runs in the table.
  const pick = (g: GateInterval) => {
    const command = g.command.toLowerCase();
    const cost = sorted.find(
      (c) =>
        c.command.toLowerCase() === command &&
        (!g.project ||
          g.project === c.projectName ||
          g.project === c.projectId),
    );
    if (!cost) return;
    setOpen(keyOf(cost));
    requestAnimationFrame(() => jump(`command-${keyOf(cost)}`));
  };

  return (
    <div className="gates-tab">
      <GatesFigures costs={costs} />
      <GatesNow gates={gates} waits={waits} tasks={tasks} onWorker={onWorker} />
      <GatesFlow
        intervals={intervals}
        runs={history?.runs ?? []}
        start={start}
        end={end}
        period={period}
        onPeriod={setPeriod}
        admissions={admissions}
        onAdmissions={setAdmissions}
        onPick={pick}
      />
      <GatesSankey intervals={intervals} start={start} end={end} />
      <CommandTable
        costs={sorted}
        tasks={tasks}
        open={open}
        onOpen={setOpen}
        onTask={onTask}
      />
      <GateWaitsCard waits={waits} />
    </div>
  );
}

// The four figures of the tab, over the commands' measured runs.
function GatesFigures({ costs }: { costs: readonly CommandCost[] }) {
  const runs = runsOf(costs);
  const waits = runs.map((r) => r.run.waitSeconds).sort((a, b) => a - b);
  const asked = costs.reduce((sum, c) => sum + Math.max(c.asked, c.runs), 0);
  const ran = costs.reduce((sum, c) => sum + c.runs, 0);
  const long = waits.filter((w) => w > LONG_WAIT).length;
  let peak: CommandCost | undefined;
  for (const c of costs)
    if (!peak || c.peakMemoryBytes > peak.peakMemoryBytes) peak = c;
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
      runs.length
        ? `${waitText(quantile(waits, 0.5))} · ${waitText(quantile(waits, 0.9))}`
        : "—",
      t("gates.figure_wait_sub", { count: runs.length }),
    ],
    [
      "long",
      t("gates.figure_long"),
      String(long),
      t("gates.figure_long_sub", {
        share: runs.length ? Math.round((long / runs.length) * 100) : 0,
        max: waitText(waits.at(-1) ?? 0),
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

// Now: each gate held or waited for, live, then the gates the recent runs name, free, with their last hold.
const GatesNow = memo(function GatesNow({
  gates,
  waits,
  tasks,
  onWorker,
}: {
  gates: readonly Gate[];
  waits: readonly GatesWaits[];
  tasks?: Tasks;
  onWorker?: (taskId: string) => void;
}) {
  const busy = gates.some((g) => g.holder || g.waiting.length > 0);
  const now = useNow(busy);
  const live = new Set(gates.map((g) => g.name));
  const free = waits.filter((w) => !live.has(w.gate));
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
        {free.map((w) => {
          const last = w.last;
          const code = last ? tasks?.get(last.run.taskId)?.code : undefined;
          return (
            <li key={w.gate} id={`gate-${w.gate}`} className="gates-gate">
              <div className="gates-gate-head">
                <code className="gates-gate-name">{w.gate}</code>
                <span className="gates-gate-state">{t("gates.free")}</span>
              </div>
              {last && (
                <p className="gates-gate-line muted-text">
                  {last.run.time
                    ? t("gates.last_hold", {
                        when: whenText(date(last.run.time)),
                        command: last.cost.command,
                      })
                    : t("gates.last_hold_command", {
                        command: last.cost.command,
                      })}
                  {code && ` · ${code}`}
                  {last.run.peakMemoryBytes > 0n &&
                    ` · ${memoryText(last.run.peakMemoryBytes)}`}
                </p>
              )}
            </li>
          );
        })}
      </ul>
    </section>
  );
});

// The commands admitted under a gate, one row each, the most total time first: the one worth making faster on top. A
// row opens on its recent runs.
const CommandTable = memo(function CommandTable({
  costs,
  tasks,
  open,
  onOpen,
  onTask,
}: {
  costs: readonly CommandCost[];
  tasks?: Tasks;
  open: string;
  onOpen: (id: string) => void;
  onTask?: (task: Task) => void;
}) {
  const top = costs.length ? total(costs[0]) : 0;
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
      {costs.length === 0 ? (
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
              {costs.map((c, i) => {
                const shown = open === keyOf(c);
                const share = top > 0 ? total(c) / top : 0;
                return (
                  <CommandRows
                    key={keyOf(c)}
                    cost={c}
                    first={i === 0 && total(c) > 0}
                    share={share}
                    shown={shown}
                    tasks={tasks}
                    onToggle={() => onOpen(shown ? "" : keyOf(c))}
                    onTask={onTask}
                  />
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
});

function CommandRows({
  cost,
  first,
  share,
  shown,
  tasks,
  onToggle,
  onTask,
}: {
  cost: CommandCost;
  first: boolean;
  share: number;
  shown: boolean;
  tasks?: Tasks;
  onToggle: () => void;
  onTask?: (task: Task) => void;
}) {
  const runs = useMemo(
    () =>
      shown
        ? [...cost.recentRuns].sort((a, b) => timeOf(b.time) - timeOf(a.time))
        : [],
    [cost.recentRuns, shown],
  );
  return (
    <>
      <tr
        id={`command-${keyOf(cost)}`}
        className={`gates-command${first ? " is-top" : ""}`}
      >
        <th scope="row" className="gates-command-name">
          <button
            type="button"
            className="gates-command-open"
            aria-expanded={shown}
            onClick={onToggle}
            disabled={cost.recentRuns.length === 0}
          >
            {shown ? (
              <ChevronDown size={12} aria-hidden="true" />
            ) : (
              <ChevronRight size={12} aria-hidden="true" />
            )}
            <code>{cost.command}</code>
          </button>
          {first && <span className="gates-command-top">{t("gates.top")}</span>}
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
            <span style={{ width: `${Math.round(share * 100)}%` }} />
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
      {shown && (
        <tr className="gates-command-runs">
          <td colSpan={9}>
            <table className="gates-runs">
              <caption>{t("gates.runs", { count: runs.length })}</caption>
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
                {runs.map((r, i) => (
                  <tr key={i}>
                    <td>{whenText(date(r.time))}</td>
                    <td>
                      <TaskCode
                        taskId={r.taskId}
                        tasks={tasks}
                        onShow={onTask}
                      />
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
          </td>
        </tr>
      )}
    </>
  );
}

// The waits for each gate: a median and a 90th percentile, never a sum or a mean (Statistics' rule).
function GateWaitsCard({ waits }: { waits: readonly GatesWaits[] }) {
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
              {waits.map((w) => (
                <tr key={w.gate} className="gates-wait-row">
                  <th scope="row">
                    <code>{w.gate}</code>
                  </th>
                  <td className="num-col">{w.holds}</td>
                  <td className="num-col">{waitText(w.p50)}</td>
                  <td className="num-col">{waitText(w.p90)}</td>
                  <td className="num-col">{waitText(w.max)}</td>
                  <td className="num-col">{w.long}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}

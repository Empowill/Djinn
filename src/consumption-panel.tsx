// The resources panel under the flying carpet: the machine's CPU and memory over time with Djinn's share and the load
// limit, then each running worker (its title line, the facts of its job, its CPU and memory over time, a link to
// its task in its wish), then where the time of the commands goes, by project and gate. The series are kept in the
// store (internal/machine/history.go): the panel reads the range once, then every 5 s only what came since.
import {
  Fragment,
  type ReactNode,
  memo,
  useCallback,
  useEffect,
  useMemo,
  useState,
} from "react";
import type { Timestamp } from "@bufbuild/protobuf/wkt";
import { ChevronDown, ChevronRight, Eye, LockKeyhole } from "lucide-react";

import { type Project, Provider, type Task } from "../gen/ts/plan/v1/plan_pb";
import type {
  CommandCost,
  GateInterval,
  MachinePoint,
  MachineSeries,
  MachineServiceSeriesResponse,
  WorkerSeries,
} from "../gen/ts/machine/v1/machine_pb";
import {
  type CommandLine,
  type GateLine,
  type ProjectLine,
  groupCommands,
  mean,
} from "./data/commands";
import { useDjinn } from "./data/djinn";
import {
  shortModel,
  span,
  taskModelDisplay,
  taskTime,
  useNow,
} from "./data/format";
import { lastPointTime, mergeSeries, timeOf } from "./data/series";
import { t } from "./i18n";
import {
  type ChartExtra,
  type ChartPoint,
  ResourceChart,
  formatValue,
} from "./resource-chart";
import { TooltipButton } from "./tooltip";
import { TaskTurns, TaskUsage } from "./usage";
import "./consumption-panel.css";

const providerNames: Record<Provider, string> = {
  [Provider.UNSPECIFIED]: "claude",
  [Provider.CLAUDE]: "claude",
  [Provider.FAKE]: "fake",
  [Provider.CODEX]: "codex",
  [Provider.ANTIGRAVITY]: "antigravity",
  [Provider.WATCH]: "watch",
};

const providerClasses: Record<Provider, string> = {
  [Provider.UNSPECIFIED]: "provider-claude",
  [Provider.CLAUDE]: "provider-claude",
  [Provider.FAKE]: "provider-fake",
  [Provider.CODEX]: "provider-codex",
  [Provider.ANTIGRAVITY]: "provider-antigravity",
  [Provider.WATCH]: "provider-watch",
};

// The ranges the panel shows, in minutes, and their labels.
const ranges = [
  [10, "consumption.range_10m"],
  [30, "consumption.range_30m"],
  [120, "consumption.range_120m"],
  [1440, "consumption.range_1440m"],
  [10080, "consumption.range_10080m"],
] as const;

// How often the series are read again, and the commands.
const SERIES_EVERY = 5_000;
const COMMANDS_EVERY = 15_000;

// duration says seconds as the window does elsewhere: 45s, 1m1s, 1h1m; "—" for none.
export function duration(seconds: number): string {
  if (!(seconds > 0)) return "—";
  if (seconds < 1) return t("consumption.under_second");
  return span(seconds * 1000);
}

function cpuPoints(
  points: readonly { time?: Timestamp; cpuPercent: number }[],
): ChartPoint[] {
  return points.map((p) => ({ t: timeOf(p.time), v: p.cpuPercent }));
}

function memoryPoints(
  points: readonly { time?: Timestamp; memoryBytes: bigint }[],
): ChartPoint[] {
  return points.map((p) => ({ t: timeOf(p.time), v: Number(p.memoryBytes) }));
}

// The machine's CPU and memory, with Djinn's share and the load limit, and the gates held over time.
const MachineBand = memo(function MachineBand({
  series,
  gates,
  tMin,
  tMax,
}: {
  series: MachineSeries;
  gates: readonly GateInterval[];
  tMin: number;
  tMax: number;
}) {
  const cpu = useMemo(() => cpuPoints(series.points), [series.points]);
  const memory = useMemo(() => memoryPoints(series.points), [series.points]);
  // Djinn with every process it started, and Djinn's own process alone.
  const extras = useMemo(() => {
    const of = (v: (p: MachinePoint) => number) =>
      series.points.map((p) => ({ t: timeOf(p.time), v: v(p) }));
    const all = t("consumption.djinn_all");
    const own = t("consumption.djinn_self");
    return {
      cpu: [
        { label: all, style: "dash", points: of((p) => p.djinnCpuPercent) },
        { label: own, style: "dot", points: of((p) => p.selfCpuPercent) },
      ] as ChartExtra[],
      memory: [
        {
          label: all,
          style: "dash",
          points: of((p) => Number(p.djinnMemoryBytes)),
        },
        {
          label: own,
          style: "dot",
          points: of((p) => Number(p.selfMemoryBytes)),
        },
      ] as ChartExtra[],
    };
  }, [series.points]);
  const total = Number(series.memoryTotalBytes);
  const lastCpu = cpu.at(-1)?.v ?? 0;
  const lastMemory = memory.at(-1)?.v ?? 0;
  const peakCpu = Math.max(0, ...cpu.map((p) => p.v));
  const limit = series.loadLimitShare;
  const limitMemory = Number(series.loadLimitMemoryBytes);
  return (
    <section
      className="resource-machine"
      aria-label={t("consumption.machine_title")}
    >
      <h4 className="resource-section-title">
        {t("consumption.machine_title")}
        <span className="resource-section-detail">
          {t("consumption.cores", { count: series.cores })}
          {total > 0 && ` · ${formatValue("memory", total)}`}
        </span>
      </h4>
      <div className="resource-machine-charts">
        <ResourceChart
          title={t("consumption.cpu")}
          unit="cpu"
          summary={t("consumption.now_peak", {
            now: formatValue("cpu", lastCpu),
            peak: formatValue("cpu", peakCpu),
          })}
          points={cpu}
          extras={extras.cpu}
          tMin={tMin}
          tMax={tMax}
          top={100}
          lines={
            limit > 0 && limit * 100 <= 100
              ? [
                  {
                    value: limit * 100,
                    label: t("consumption.limit_at", {
                      value: formatValue("cpu", limit * 100),
                    }),
                  },
                ]
              : []
          }
          height={210}
        />
        <ResourceChart
          title={t("consumption.memory")}
          unit="memory"
          summary={t("consumption.used_of", {
            used: formatValue("memory", lastMemory),
            total: formatValue("memory", total),
          })}
          points={memory}
          extras={extras.memory}
          tMin={tMin}
          tMax={tMax}
          top={total}
          memoryTotal={total}
          lines={[
            ...(total > 0
              ? [
                  {
                    value: total,
                    label: t("consumption.max_memory", {
                      value: formatValue("memory", total),
                    }),
                  },
                ]
              : []),
            ...(limitMemory > 0 && limitMemory < total
              ? [
                  {
                    value: limitMemory,
                    label: t("consumption.limit_at", {
                      value: formatValue("memory", limitMemory),
                    }),
                  },
                ]
              : []),
          ]}
          height={210}
        />
      </div>
      <ul className="resource-legend">
        <li>
          <span className="resource-legend-key" aria-hidden="true" />
          {t("consumption.legend_machine")}
        </li>
        <li>
          <span className="resource-legend-key dash" aria-hidden="true" />
          {t("consumption.legend_djinn_all")}
        </li>
        <li>
          <span className="resource-legend-key dot" aria-hidden="true" />
          {t("consumption.legend_djinn_self")}
        </li>
      </ul>
      <GatesTimeline gates={gates} tMin={tMin} tMax={tMax} />
    </section>
  );
});

// The gates held over time, one lane per overlap: the hatched part is the wait, the plain part the hold.
const GatesTimeline = memo(function GatesTimeline({
  gates,
  tMin,
  tMax,
}: {
  gates: readonly GateInterval[];
  tMin: number;
  tMax: number;
}) {
  const lanes = useMemo(() => {
    const visible = gates
      .filter((g) => {
        const release = g.releaseTime ? timeOf(g.releaseTime) : tMax;
        return g.grantTime && release >= tMin && timeOf(g.grantTime) <= tMax;
      })
      .sort((a, b) => timeOf(a.grantTime) - timeOf(b.grantTime));
    const ends: number[] = [];
    return visible.map((g) => {
      const asked = timeOf(g.askedTime) || timeOf(g.grantTime);
      const release = g.releaseTime ? timeOf(g.releaseTime) : tMax;
      let lane = ends.findIndex((end) => end <= asked);
      if (lane < 0) lane = ends.push(release) - 1;
      else ends[lane] = release;
      return { g, lane };
    });
  }, [gates, tMin, tMax]);
  if (lanes.length === 0) return null;
  const span = Math.max(1, tMax - tMin);
  const at = (ms: number) =>
    `${(Math.max(0, Math.min(1, (ms - tMin) / span)) * 100).toFixed(2)}%`;
  const count = Math.max(...lanes.map((x) => x.lane)) + 1;
  return (
    <div className="resource-gates">
      <h5 className="resource-gates-title">{t("consumption.gates_title")}</h5>
      <div
        className="resource-gates-lanes"
        style={{ height: count * 22 }}
        role="list"
      >
        {lanes.map(({ g, lane }, i) => {
          const asked = timeOf(g.askedTime) || timeOf(g.grantTime);
          const grant = timeOf(g.grantTime);
          const release = g.releaseTime ? timeOf(g.releaseTime) : tMax;
          const label = t("consumption.gate_band", {
            gate: g.gate,
            command: g.command || "—",
            project: g.project || "—",
            wait: duration((grant - asked) / 1000),
            held: duration((release - grant) / 1000),
          });
          return (
            <div
              key={i}
              role="listitem"
              className="resource-gates-band"
              title={label}
              aria-label={label}
              style={{
                top: lane * 22,
                left: at(asked),
                right: `calc(100% - ${at(release)})`,
              }}
            >
              {grant > asked && (
                <span
                  className="resource-gates-wait"
                  style={{
                    width: `${(((grant - asked) / Math.max(1, release - asked)) * 100).toFixed(2)}%`,
                  }}
                />
              )}
              <span className="resource-gates-held">{g.gate}</span>
            </div>
          );
        })}
      </div>
    </div>
  );
});

// What the job of a worker says at a glance: the facts of its task, as its card shows them, and what it holds.
function WorkerFacts({
  worker,
  task,
  project,
  memoryTotal,
}: {
  worker: WorkerSeries;
  task?: Task;
  project?: Project;
  memoryTotal: number;
}) {
  const running = !!task?.startTime && !worker.paused;
  const now = useNow(running);
  const time = task ? taskTime(task, now) : undefined;
  const model = task ? taskModelDisplay(task, project) : undefined;
  const last = worker.points.at(-1);
  const holding = worker.gates.find((g) => !g.releaseTime && g.grantTime);
  const held = worker.gates.reduce(
    (sum, g) =>
      sum +
      Math.max(
        0,
        (g.releaseTime ? timeOf(g.releaseTime) : now) - timeOf(g.grantTime),
      ),
    0,
  );
  const facts: [string, ReactNode][] = [];
  if (project?.name) facts.push([t("consumption.fact_project"), project.name]);
  if (model)
    facts.push([
      t("consumption.fact_model"),
      <span key="model" title={model.title}>
        {model.label}
      </span>,
    ]);
  else if (worker.model)
    facts.push([t("consumption.fact_model"), shortModel(worker.model)]);
  if (time?.text)
    facts.push([
      t("consumption.fact_time"),
      <span key="time" title={time.title}>
        {time.text}
      </span>,
    ]);
  if (task?.turns && task.turns.limit > 0)
    facts.push([
      t("consumption.fact_turns"),
      <TaskTurns key="turns" task={task} />,
    ]);
  if (task?.usage)
    facts.push([
      t("consumption.fact_spent"),
      <TaskUsage key="spent" task={task} />,
    ]);
  if (holding)
    facts.push([
      t("consumption.fact_gate"),
      t("consumption.gate_holding", {
        gate: holding.gate,
        time: span(now - timeOf(holding.grantTime)),
      }),
    ]);
  else if (held > 0)
    facts.push([
      t("consumption.fact_gate"),
      t("consumption.gate_held", {
        time: span(held),
        count: worker.gates.length,
      }),
    ]);
  facts.push([
    t("consumption.cpu"),
    t("consumption.now_peak", {
      now: last ? formatValue("cpu", last.cpuPercent) : "—",
      peak: formatValue("cpu", worker.peakCpuPercent),
    }),
  ]);
  facts.push([
    t("consumption.memory"),
    t("consumption.now_peak", {
      now: last
        ? formatValue("memory", Number(last.memoryBytes), memoryTotal)
        : "—",
      peak: formatValue("memory", Number(worker.peakMemoryBytes)),
    }),
  ]);
  return (
    <dl className="resource-worker-facts">
      {facts.map(([term, detail]) => (
        <div key={term} className="resource-worker-fact">
          <dt>{term}</dt>
          <dd>{detail}</dd>
        </div>
      ))}
    </dl>
  );
}

// A running worker: its title line (code, agent, the task's title in full, a link to the task in its wish), the facts
// of its job on the left, its CPU and memory over time beside them.
export const WorkerRow = memo(function WorkerRow({
  worker,
  task,
  project,
  memoryTotal,
  cpuTop,
  memoryTop,
  tMin,
  tMax,
  onOpen,
}: {
  worker: WorkerSeries;
  // The scale every worker's charts share, so that their rows compare: the highest peak of them all.
  cpuTop: number;
  memoryTop: number;
  task?: Task;
  project?: Project;
  memoryTotal: number;
  tMin: number;
  tMax: number;
  onOpen: (wishId: string, taskId: string) => void;
}) {
  const cpu = useMemo(() => cpuPoints(worker.points), [worker.points]);
  const memory = useMemo(() => memoryPoints(worker.points), [worker.points]);
  const lastCpu = cpu.at(-1)?.v ?? 0;
  const lastMemory = memory.at(-1)?.v ?? 0;
  const open = t("consumption.open_task", { code: worker.code });
  return (
    <article
      className={`resource-worker ${worker.paused ? "is-paused" : ""}`}
      aria-label={`${worker.code} ${worker.title}`}
    >
      <h4 className="resource-worker-title">
        <span className="agent-code">{worker.code}</span>
        <span className={`provider-badge ${providerClasses[worker.provider]}`}>
          {providerNames[worker.provider] ?? "claude"}
        </span>
        {worker.paused && (
          <span className="resource-worker-paused">
            {t("consumption.paused")}
          </span>
        )}
        <span className="resource-worker-name">{worker.title}</span>
        {worker.wishId && worker.taskId && (
          <TooltipButton
            className="icon-button resource-worker-open"
            label={open}
            onClick={() => onOpen(worker.wishId, worker.taskId)}
          >
            <Eye size={14} aria-hidden="true" />
          </TooltipButton>
        )}
      </h4>
      <div className="resource-worker-body">
        <WorkerFacts
          worker={worker}
          task={task}
          project={project}
          memoryTotal={memoryTotal}
        />
        <div className="resource-worker-charts">
          <ResourceChart
            title={t("consumption.cpu_one_core")}
            unit="cpu"
            summary={t("consumption.now_peak", {
              now: formatValue("cpu", lastCpu),
              peak: formatValue("cpu", worker.peakCpuPercent),
            })}
            points={cpu}
            tMin={tMin}
            tMax={tMax}
            top={cpuTop}
            gates={worker.gates}
          />
          <ResourceChart
            title={t("consumption.memory")}
            unit="memory"
            summary={t("consumption.now_peak", {
              now: formatValue("memory", lastMemory),
              peak: formatValue("memory", Number(worker.peakMemoryBytes)),
            })}
            points={memory}
            tMin={tMin}
            tMax={tMax}
            top={memoryTop}
            memoryTotal={memoryTotal}
            gates={worker.gates}
          />
        </div>
      </div>
    </article>
  );
});

function Times({ total, ran }: { total: number; ran: number }) {
  return (
    <>
      <td className="num-col">{duration(total)}</td>
      <td className="num-col">{duration(mean(total, ran))}</td>
    </>
  );
}

function CommandRow({ line, share }: { line: CommandLine; share: number }) {
  const shown = line.variants.slice(0, 3).join(", ");
  return (
    <tr className="resource-command-row">
      <th scope="row" className="resource-command-name">
        <code>{line.withArgs ? `${line.head} …` : line.head}</code>
        {line.withArgs && line.variants.length > 0 && (
          <span
            className="resource-command-args"
            title={line.variants.join("\n")}
          >
            {t("consumption.variants", {
              count: line.variants.length,
              shown: line.variants.length > 3 ? `${shown}, …` : shown,
            })}
          </span>
        )}
      </th>
      <td className="num-col">{line.asked}</td>
      <td className="num-col">{line.ran}</td>
      <td className="num-col resource-command-total">
        {duration(line.seconds)}
        <span className="resource-share" aria-hidden="true">
          <span style={{ width: `${Math.round(share * 100)}%` }} />
        </span>
        <span className="resource-share-text">
          {t("consumption.share", { share: Math.round(share * 100) })}
        </span>
      </td>
      <td className="num-col">{duration(mean(line.seconds, line.ran))}</td>
      <Times total={line.waitSeconds} ran={line.ran} />
      <Times total={line.heldSeconds} ran={line.ran} />
    </tr>
  );
}

function GateRows({ gate, total }: { gate: GateLine; total: number }) {
  return (
    <tbody className="resource-gate">
      <tr className="resource-gate-row">
        <th scope="rowgroup">
          <LockKeyhole size={12} aria-hidden="true" />
          {gate.gate
            ? t("consumption.gate_named", { gate: gate.gate })
            : t("consumption.no_gate")}
        </th>
        <td className="num-col">{gate.asked}</td>
        <td className="num-col">{gate.ran}</td>
        <td className="num-col">{duration(gate.seconds)}</td>
        <td className="num-col">{duration(mean(gate.seconds, gate.ran))}</td>
        <Times total={gate.waitSeconds} ran={gate.ran} />
        <Times total={gate.heldSeconds} ran={gate.ran} />
      </tr>
      {gate.commands.map((line) => (
        <CommandRow
          key={line.key}
          line={line}
          share={total > 0 ? line.seconds / total : 0}
        />
      ))}
    </tbody>
  );
}

// ProjectTop says where most of a project's time goes: its costliest command, under its gate, and its share.
function ProjectTop({ project }: { project: ProjectLine }) {
  let top: { line: CommandLine; gate: string } | undefined;
  for (const g of project.gates)
    for (const line of g.commands)
      if (!top || line.seconds > top.line.seconds) top = { line, gate: g.gate };
  if (!top || !(project.seconds > 0)) return null;
  return (
    <p className="resource-project-top">
      {t("consumption.project_top", {
        command: top.line.withArgs ? `${top.line.head} …` : top.line.head,
        gate: top.gate || "—",
        share: Math.round((top.line.seconds / project.seconds) * 100),
      })}
    </p>
  );
}

// Where the time of the commands goes, in the projects of the active wishes: by project, by gate, by command.
export const CommandGroups = memo(function CommandGroups({
  costs,
  projectIds,
  projectNames,
}: {
  costs: readonly CommandCost[];
  projectIds?: ReadonlySet<string>;
  projectNames?: ReadonlyMap<string, string>;
}) {
  const projects = useMemo(
    () => groupCommands(costs, projectIds, projectNames),
    [costs, projectIds, projectNames],
  );
  return (
    <section
      className="resource-commands"
      aria-label={t("consumption.commands_title")}
    >
      <h4 className="resource-section-title">
        {t("consumption.commands_title")}
      </h4>
      <p className="resource-commands-help">{t("consumption.commands_help")}</p>
      {projects.length === 0 && (
        <p className="muted-text">{t("consumption.no_commands")}</p>
      )}
      {projects.map((p) => (
        <section
          key={p.projectId}
          className="resource-project"
          aria-label={t("consumption.project", { project: p.name })}
        >
          <h5 className="resource-project-name">
            {t("consumption.project", { project: p.name })}
            <span className="resource-section-detail">
              {t("consumption.project_time", { time: duration(p.seconds) })}
            </span>
          </h5>
          <ProjectTop project={p} />
          <div className="resource-table-scroll">
            <table className="resource-commands-table">
              <thead>
                <tr>
                  <th scope="col" rowSpan={2}>
                    {t("consumption.col_what")}
                  </th>
                  <th scope="col" rowSpan={2} className="num-col">
                    {t("consumption.col_asked")}
                  </th>
                  <th scope="col" rowSpan={2} className="num-col">
                    {t("consumption.col_ran")}
                  </th>
                  <th
                    scope="colgroup"
                    colSpan={2}
                    className="resource-col-group"
                  >
                    {t("consumption.col_time")}
                  </th>
                  <th
                    scope="colgroup"
                    colSpan={2}
                    className="resource-col-group"
                  >
                    {t("consumption.col_wait")}
                  </th>
                  <th
                    scope="colgroup"
                    colSpan={2}
                    className="resource-col-group"
                  >
                    {t("consumption.col_held")}
                  </th>
                </tr>
                <tr>
                  {[0, 1, 2].map((i) => (
                    <Fragment key={i}>
                      <th scope="col" className="num-col">
                        {t("consumption.col_total")}
                      </th>
                      <th scope="col" className="num-col">
                        {t("consumption.col_mean")}
                      </th>
                    </Fragment>
                  ))}
                </tr>
              </thead>
              {p.gates.map((g) => (
                <GateRows key={g.gate} gate={g} total={p.seconds} />
              ))}
            </table>
          </div>
        </section>
      ))}
    </section>
  );
});

export function ConsumptionPanel({
  initialMinutes = 30,
  initialFolded = false,
  initialData = null,
  initialCommands = [],
  tasks,
  projects = [],
  projectIds,
}: {
  initialMinutes?: number;
  initialFolded?: boolean;
  initialData?: MachineServiceSeriesResponse | null;
  initialCommands?: CommandCost[];
  // The tasks of the active wishes, by id: a worker's row shows the facts of its job.
  tasks?: ReadonlyMap<string, Task>;
  projects?: readonly Project[];
  // The projects of the active wishes: only their commands show; all when undefined.
  projectIds?: ReadonlySet<string>;
} = {}) {
  const djinn = useDjinn();
  const [minutes, setMinutes] = useState<number>(initialMinutes);
  const [folded, setFolded] = useState<boolean>(initialFolded);
  const [data, setData] = useState<MachineServiceSeriesResponse | null>(
    initialData,
  );
  const [commands, setCommands] = useState<CommandCost[]>(initialCommands);

  // The range once, then what came since its last point.
  useEffect(() => {
    if (!djinn || folded) return;
    let active = true;
    let current: MachineServiceSeriesResponse | null = null;
    const read = async () => {
      const since = current ? lastPointTime(current) : undefined;
      const res = await djinn.clients.machine
        .series({ minutes, since })
        .catch(() => null);
      if (!active || !res) return;
      current =
        current && since
          ? mergeSeries(current, res, Date.now() - minutes * 60_000)
          : res;
      setData(current);
    };
    void read();
    const timer = setInterval(read, SERIES_EVERY);
    return () => {
      active = false;
      clearInterval(timer);
    };
  }, [djinn, folded, minutes]);

  useEffect(() => {
    if (!djinn || folded) return;
    let active = true;
    const read = async () => {
      const res = await djinn.clients.commands.list({}).catch(() => null);
      if (active && res) setCommands(res.costs);
    };
    void read();
    const timer = setInterval(read, COMMANDS_EVERY);
    return () => {
      active = false;
      clearInterval(timer);
    };
  }, [djinn, folded]);

  const open = useCallback(
    (wishId: string, taskId: string) =>
      djinn?.focus.show({ wishId, target: `task-${taskId}` }),
    [djinn],
  );

  // The range ends now, or at the last point when the points are not of today (a test's fixed clock).
  const { tMin, tMax } = useMemo(() => {
    const last = data ? timeOf(lastPointTime(data)) : 0;
    const now =
      last && Math.abs(Date.now() - last) > 24 * 3600 * 1000
        ? last
        : Math.max(Date.now(), last);
    return { tMin: now - minutes * 60_000, tMax: now };
  }, [data, minutes]);

  const projectById = useMemo(
    () => new Map(projects.map((p) => [p.id, p])),
    [projects],
  );
  const projectNames = useMemo(
    () => new Map(projects.map((p) => [p.id, p.name])),
    [projects],
  );
  const gates = useMemo(() => {
    const names = projectIds
      ? new Set([...projectIds].map((id) => projectById.get(id)?.name ?? id))
      : undefined;
    return (data?.gates ?? []).filter(
      (g) => !names || !g.project || names.has(g.project),
    );
  }, [data, projectIds, projectById]);
  const workers = useMemo(() => data?.workers ?? [], [data]);
  // One scale for every worker: the highest peak among them.
  const workerTops = useMemo(
    () => ({
      cpu: Math.max(100, ...workers.map((w) => w.peakCpuPercent)),
      memory:
        Math.max(
          0,
          ...workers.flatMap((w) => [
            Number(w.peakMemoryBytes),
            ...w.points.map((p) => Number(p.memoryBytes)),
          ]),
        ) * 1.1,
    }),
    [workers],
  );
  const memoryTotal = Number(data?.machine?.memoryTotalBytes ?? 0n);

  return (
    <section className="consumption-panel" aria-label={t("consumption.title")}>
      <header className="consumption-header">
        <button
          type="button"
          className="consumption-fold-btn"
          onClick={() => setFolded((f) => !f)}
          aria-expanded={!folded}
        >
          {folded ? <ChevronRight size={15} /> : <ChevronDown size={15} />}
          <span className="consumption-panel-heading">
            {t("consumption.title")}
          </span>
        </button>
        {!folded && (
          <div
            className="consumption-range-selector"
            role="group"
            aria-label={t("consumption.range")}
          >
            {ranges.map(([m, key]) => (
              <button
                key={m}
                type="button"
                className={`range-pill ${minutes === m ? "active" : ""}`}
                aria-pressed={minutes === m}
                onClick={() => setMinutes(m)}
              >
                {t(key)}
              </button>
            ))}
          </div>
        )}
      </header>

      {!folded && (
        <div className="consumption-body">
          {data?.workerMeasure && (
            <p className="consumption-notice muted-text">
              {t("resources.not_measured")}
              {data.workerMeasure !== "windows"
                ? ` (${data.workerMeasure})`
                : ""}
            </p>
          )}
          {data?.machine && (
            <MachineBand
              series={data.machine}
              gates={gates}
              tMin={tMin}
              tMax={tMax}
            />
          )}
          <section
            className="resource-workers"
            aria-label={t("consumption.workers_title")}
          >
            <h4 className="resource-section-title">
              {t("consumption.workers_title")}
            </h4>
            {workers.length > 1 && (
              <p className="resource-workers-scale">
                {t("consumption.workers_scale")}
              </p>
            )}
            {workers.length === 0 ? (
              <p className="muted-text">{t("consumption.no_workers")}</p>
            ) : (
              workers.map((w) => {
                const task = tasks?.get(w.taskId);
                return (
                  <WorkerRow
                    key={w.taskId || w.code}
                    worker={w}
                    task={task}
                    project={task ? projectById.get(task.projectId) : undefined}
                    memoryTotal={memoryTotal}
                    cpuTop={workerTops.cpu}
                    memoryTop={workerTops.memory}
                    tMin={tMin}
                    tMax={tMax}
                    onOpen={open}
                  />
                );
              })
            )}
          </section>
          <CommandGroups
            costs={commands}
            projectIds={projectIds}
            projectNames={projectNames}
          />
        </div>
      )}
    </section>
  );
}

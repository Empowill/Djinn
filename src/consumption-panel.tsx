// The resources panel, the Hourglass's machine (hourglass.tsx): the machine total at the very top, then three tabs —
// Resources (the whole machine's CPU and memory, CPU and memory worker heatmaps side by side, then the running workers
// grouped by wish with watchers last), Gates (the whole machine's CPU and memory stacked, the gates timeline and the
// commands grouped under their gates), and Disk (the main folders using the disk, biggest first). The series are kept
// in the store (internal/machine/history.go): the panel reads the range once, then every 5 s only what came since.
import {
  type CSSProperties,
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

import {
  type Project,
  Provider,
  type Task,
  type Wish,
} from "../gen/ts/plan/v1/plan_pb";
import {
  type CommandCost,
  type Disk,
  type DiskItem,
  DiskRisk,
  type GateInterval,
  type MachinePoint,
  type MachineSeries,
  type MachineServiceSeriesResponse,
  type WorkerSeries,
} from "../gen/ts/machine/v1/machine_pb";
import { jump } from "./attention";
import { CopyButton } from "./copy-command";
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
import { keepLive } from "./stillness";
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
        style={{ height: `${(count * 22) / 16}rem` }}
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
                top: `${(lane * 22) / 16}rem`,
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
      id={worker.taskId ? `worker-${worker.taskId}` : undefined}
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

export type ConsumptionTab = "resources" | "gates" | "disk";

// CPU and memory heatmaps on top of the Resources tab to spot at a glance the workers that consume: one cell per
// running worker, its code on hover, side by side and stacked under a narrow width.
const WorkerHeatmap = memo(function WorkerHeatmap({
  workers,
  cpuTop,
  memoryTop,
}: {
  workers: readonly WorkerSeries[];
  cpuTop: number;
  memoryTop: number;
}) {
  if (workers.length === 0) return null;
  const metrics = [
    ["cpu", t("consumption.cpu")],
    ["memory", t("consumption.memory")],
  ] as const;
  return (
    <div className="resource-heatmap">
      {metrics.map(([metric, metricLabel]) => (
        <div
          key={metric}
          className={`resource-heatmap-group ${metric}`}
          role="group"
          aria-label={metricLabel}
        >
          <span className="resource-heatmap-label">{metricLabel}</span>
          <div className="resource-heatmap-cells" role="list">
            {workers.map((w) => {
              const last = w.points.at(-1);
              const cpuVal = last?.cpuPercent ?? 0;
              const memVal = Number(last?.memoryBytes ?? 0n);
              const ratio =
                metric === "cpu"
                  ? cpuVal / Math.max(100, cpuTop)
                  : memVal / Math.max(1, memoryTop);
              const pct = Math.max(
                12,
                Math.min(100, Math.round(Math.max(0, ratio) * 100)),
              );
              const valueText =
                metric === "cpu"
                  ? formatValue("cpu", cpuVal)
                  : formatValue("memory", memVal);
              const label = `${w.code}: ${valueText}`;
              return (
                <div key={w.taskId || w.code} role="listitem">
                  <TooltipButton
                    className={`resource-heatmap-cell ${metric} ${w.paused ? "is-paused" : ""}`}
                    label={label}
                    data-code={w.code}
                    style={{ "--heat": `${pct}%` } as CSSProperties}
                    onClick={() => {
                      if (w.taskId) jump(`worker-${w.taskId}`);
                    }}
                  >
                    <span className="resource-heatmap-code">{w.code}</span>
                  </TooltipButton>
                </div>
              );
            })}
          </div>
        </div>
      ))}
    </div>
  );
});

const GIB_BYTES = 1024 ** 3;
const DISK_TRIM_BYTES = 30 * GIB_BYTES;
const DISK_FULL_BYTES = 10 * GIB_BYTES;

function diskItemTitle(item: DiskItem): string {
  switch (item.kind) {
    case "go-build":
      return t("consumption.disk_item_go_build");
    case "go-isolated":
      return t("consumption.disk_item_go_isolated", {
        count: item.count || 1,
      });
    case "go-mod":
      return t("consumption.disk_item_go_mod");
    case "docker-builder":
      return t("consumption.disk_item_docker_builder");
    case "docker-images":
      return t("consumption.disk_item_docker_images");
    case "docker-volumes":
      return t("consumption.disk_item_docker_volumes");
    case "jetbrains":
      return t("consumption.disk_item_jetbrains");
    case "pnpm":
      return t("consumption.disk_item_pnpm");
    case "pip":
      return t("consumption.disk_item_pip");
    case "uv":
      return t("consumption.disk_item_uv");
    case "npm":
      return t("consumption.disk_item_npm");
    default:
      return item.kind;
  }
}

function diskItemNote(item: DiskItem): string {
  switch (item.kind) {
    case "go-build":
      return t("consumption.disk_item_go_build_note");
    case "go-isolated":
      return t("consumption.disk_item_go_isolated_note");
    case "go-mod":
      return t("consumption.disk_item_go_mod_note");
    case "docker-builder":
      return t("consumption.disk_item_docker_builder_note");
    case "docker-images":
      return t("consumption.disk_item_docker_images_note");
    case "docker-volumes":
      return t("consumption.disk_item_docker_volumes_note");
    case "jetbrains":
      return t("consumption.disk_item_jetbrains_note");
    case "pnpm":
      return t("consumption.disk_item_pnpm_note");
    case "pip":
      return t("consumption.disk_item_pip_note");
    case "uv":
      return t("consumption.disk_item_uv_note");
    case "npm":
      return t("consumption.disk_item_npm_note");
    default:
      return "";
  }
}

function DiskRiskBadge({ risk }: { risk: DiskRisk }) {
  if (risk === DiskRisk.SAFE) {
    return (
      <span
        className="resource-disk-risk risk-safe"
        title={t("consumption.disk_risk_safe_sub")}
      >
        <svg
          className="resource-disk-icon"
          viewBox="0 0 16 16"
          aria-hidden="true"
        >
          <path d="M8 1.5 13.5 4v4c0 3-2.4 5.4-5.5 6.5C4.9 13.4 2.5 11 2.5 8V4z" />
          <path d="m5.5 8 1.8 1.8L10.8 6.3" />
        </svg>
        {t("consumption.disk_risk_safe")}
      </span>
    );
  }
  if (risk === DiskRisk.LOSS) {
    return (
      <span
        className="resource-disk-risk risk-loss"
        title={t("consumption.disk_risk_loss_sub")}
      >
        <svg
          className="resource-disk-icon"
          viewBox="0 0 16 16"
          aria-hidden="true"
        >
          <path d="M8 2 14.5 13.5h-13z" />
          <path d="M8 6.5v3.2M8 11.6v.1" />
        </svg>
        {t("consumption.disk_risk_loss")}
      </span>
    );
  }
  return (
    <span
      className="resource-disk-risk risk-rebuild"
      title={t("consumption.disk_risk_rebuild_sub")}
    >
      <svg
        className="resource-disk-icon"
        viewBox="0 0 16 16"
        aria-hidden="true"
      >
        <path d="M13 8a5 5 0 1 1-1.5-3.6" />
        <path d="M13 2.5v2.5h-2.5" />
      </svg>
      {t("consumption.disk_risk_rebuild")}
    </span>
  );
}

function formatReclaimable(bytes: number, unknown: boolean): string {
  if (unknown && bytes > 0) {
    return t("consumption.disk_frees_plus_unknown", {
      size: formatValue("memory", bytes),
    });
  }
  if (unknown) {
    return t("consumption.disk_frees_unknown");
  }
  return t("consumption.disk_frees", {
    size: formatValue("memory", bytes),
  });
}

function DiskItemRow({
  item,
  showRisk = false,
}: {
  item: DiskItem;
  showRisk?: boolean;
}) {
  const title = diskItemTitle(item);
  const note = diskItemNote(item);
  const size = Number(item.sizeBytes);
  const reclaim = Number(item.reclaimableBytes);
  const freedText = item.reclaimableUnknown
    ? "?"
    : formatValue("memory", reclaim);
  return (
    <div className="resource-disk-item-row" role="listitem" title={note}>
      <div className="resource-disk-item-main">
        <span className="resource-disk-item-name">{title}</span>
        <code className="resource-disk-item-path">{item.path}</code>
      </div>
      <span className="resource-disk-size">{formatValue("memory", size)}</span>
      <span className="resource-disk-freed">{freedText}</span>
      {showRisk && <DiskRiskBadge risk={item.risk} />}
      {item.command && (
        <div className="resource-disk-cmd">
          <code>{item.command}</code>
          <CopyButton text={item.command} />
        </div>
      )}
    </div>
  );
}

function isMachineFolderPath(path: string): boolean {
  return (
    path.endsWith("/.cache/go-build") ||
    path.endsWith("/.npm") ||
    path.endsWith("/go/pkg/mod")
  );
}

function sumIndependentFolders(
  folders: { path: string; sizeBytes: bigint }[],
): number {
  const paths = folders.map((f) => f.path.replace(/\/+$/, ""));
  let sum = 0;
  for (let i = 0; i < folders.length; i++) {
    const p = paths[i];
    // Skip parent aggregate folder (e.g. ~/.config/djinn) if any of its child folders is also listed.
    const hasChild = paths.some(
      (other, j) => j !== i && other.startsWith(`${p}/`),
    );
    if (!hasChild) {
      sum += Number(folders[i].sizeBytes);
    }
  }
  return sum;
}

// The whole disk bar, Djinn's folders and isolated Go caches, and the machine's caches grouped by cleanup risk (L48).
export const DiskSection = memo(function DiskSection({
  disk,
  initialView,
}: {
  disk?: Disk;
  initialView?: "djinn" | "machine";
}) {
  const items = useMemo(
    () =>
      [...(disk?.items ?? [])].sort((a, b) =>
        b.sizeBytes > a.sizeBytes
          ? 1
          : b.sizeBytes < a.sizeBytes
            ? -1
            : a.kind.localeCompare(b.kind),
      ),
    [disk?.items],
  );
  const allFolders = useMemo(
    () =>
      [...(disk?.folders ?? [])].sort((a, b) =>
        b.sizeBytes > a.sizeBytes
          ? 1
          : b.sizeBytes < a.sizeBytes
            ? -1
            : a.path.localeCompare(b.path),
      ),
    [disk?.folders],
  );
  const djinnItems = useMemo(() => items.filter((i) => i.djinn), [items]);
  const machineItems = useMemo(() => items.filter((i) => !i.djinn), [items]);
  const folders = useMemo(
    () =>
      machineItems.length > 0
        ? allFolders.filter((f) => !isMachineFolderPath(f.path))
        : allFolders,
    [allFolders, machineItems.length],
  );

  const [view, setView] = useState<"djinn" | "machine">(
    initialView ?? (machineItems.length > 0 ? "machine" : "djinn"),
  );
  const activeView =
    initialView ?? (machineItems.length === 0 ? "djinn" : view);

  const total = Number(disk?.totalBytes ?? 0n);
  const free = Number(disk?.availableBytes ?? 0n);
  const used = Math.max(0, total - free);

  const djinnFolderBytes = useMemo(
    () => sumIndependentFolders(folders),
    [folders],
  );
  const djinnItemBytes = useMemo(
    () => djinnItems.reduce((acc, i) => acc + Number(i.sizeBytes), 0),
    [djinnItems],
  );
  const djinnTotal = djinnFolderBytes + djinnItemBytes;
  const machineTotal = useMemo(
    () => machineItems.reduce((acc, i) => acc + Number(i.sizeBytes), 0),
    [machineItems],
  );

  const segments = useMemo(() => {
    if (total <= 0) return [];
    let goBytes = items
      .filter(
        (i) =>
          i.kind === "go-build" ||
          i.kind === "go-isolated" ||
          i.kind === "go-mod",
      )
      .reduce((acc, i) => acc + Number(i.sizeBytes), 0);
    if (goBytes === 0) {
      goBytes = allFolders
        .filter((f) => f.path.endsWith("/.cache/go-build"))
        .reduce((acc, f) => acc + Number(f.sizeBytes), 0);
    }
    const dockerBytes = items
      .filter((i) => i.kind.startsWith("docker-"))
      .reduce((acc, i) => acc + Number(i.sizeBytes), 0);
    const playwrightBytes = folders
      .filter((f) => f.path.includes("ms-playwright"))
      .reduce((acc, f) => acc + Number(f.sizeBytes), 0);
    const otherItemBytes = items
      .filter((i) => ["jetbrains", "pnpm", "pip", "uv", "npm"].includes(i.kind))
      .reduce((acc, i) => acc + Number(i.sizeBytes), 0);
    const otherBytes = otherItemBytes + playwrightBytes;
    const djinnOnlyBytes = Math.max(0, djinnFolderBytes - playwrightBytes);
    const measured = goBytes + dockerBytes + djinnOnlyBytes + otherBytes;
    const unmeasured = Math.max(0, used - measured);
    return [
      {
        id: "go",
        cls: "seg-go",
        bytes: goBytes,
        label: t("consumption.disk_seg_go", {
          size: formatValue("memory", goBytes),
        }),
      },
      {
        id: "docker",
        cls: "seg-docker",
        bytes: dockerBytes,
        label: t("consumption.disk_seg_docker", {
          size: formatValue("memory", dockerBytes),
        }),
      },
      {
        id: "djinn",
        cls: "seg-djinn",
        bytes: djinnOnlyBytes,
        label: t("consumption.disk_seg_djinn", {
          size: formatValue("memory", djinnOnlyBytes),
        }),
      },
      {
        id: "other",
        cls: "seg-other",
        bytes: otherBytes,
        label: t("consumption.disk_seg_other", {
          size: formatValue("memory", otherBytes),
        }),
      },
      {
        id: "unmeasured",
        cls: "seg-unmeasured",
        bytes: unmeasured,
        label: t("consumption.disk_seg_unmeasured", {
          size: formatValue("memory", unmeasured),
        }),
      },
      {
        id: "free",
        cls: "seg-free",
        bytes: free,
        label: t("consumption.disk_seg_free", {
          size: formatValue("memory", free),
        }),
      },
    ].filter((s) => s.bytes > 0);
  }, [allFolders, djinnFolderBytes, folders, free, items, total, used]);

  const riskGroups = useMemo(() => {
    const specs = [
      {
        risk: DiskRisk.SAFE,
        slug: "safe",
        label: t("consumption.disk_risk_safe"),
        sub: t("consumption.disk_risk_safe_sub"),
      },
      {
        risk: DiskRisk.REBUILD,
        slug: "rebuild",
        label: t("consumption.disk_risk_rebuild"),
        sub: t("consumption.disk_risk_rebuild_sub"),
      },
      {
        risk: DiskRisk.LOSS,
        slug: "loss",
        label: t("consumption.disk_risk_loss"),
        sub: t("consumption.disk_risk_loss_sub"),
      },
    ];
    return specs
      .map((spec) => {
        const groupItems = machineItems.filter((i) => i.risk === spec.risk);
        const reclaimBytes = groupItems.reduce(
          (acc, i) => acc + Number(i.reclaimableBytes),
          0,
        );
        const hasUnknown = groupItems.some((i) => i.reclaimableUnknown);
        return {
          ...spec,
          items: groupItems,
          totalLabel: formatReclaimable(reclaimBytes, hasUnknown),
        };
      })
      .filter((g) => g.items.length > 0);
  }, [machineItems]);

  const safeAllCommand = useMemo(() => {
    const cmds = machineItems
      .filter((i) => i.risk === DiskRisk.SAFE && i.command)
      .map((i) => i.command);
    return cmds.join(" && ");
  }, [machineItems]);

  const top = Math.max(1, Number(folders[0]?.sizeBytes ?? 0n));
  const empty = allFolders.length === 0 && items.length === 0;

  return (
    <section className="resource-disk" aria-label={t("consumption.disk_title")}>
      <h4 className="resource-section-title">
        {t("consumption.disk_title")}
        {total > 0 && (
          <span className="resource-section-detail">
            {t("consumption.disk_free", {
              available: formatValue("memory", free),
              total: formatValue("memory", total),
            })}
          </span>
        )}
      </h4>
      {total > 0 && (
        <>
          <div
            className="resource-disk-bar"
            role="img"
            aria-label={t("consumption.disk_used", {
              used: formatValue("memory", used),
              total: formatValue("memory", total),
            })}
          >
            {segments.map((seg) => (
              <span
                key={seg.id}
                className={`resource-disk-seg ${seg.cls}`}
                style={{ width: `${(seg.bytes / total) * 100}%` }}
                title={seg.label}
              />
            ))}
            {total > DISK_TRIM_BYTES && (
              <span
                className="resource-disk-mark mark-trim"
                style={{
                  left: `${((total - DISK_TRIM_BYTES) / total) * 100}%`,
                }}
                title={t("consumption.disk_trim_mark")}
              />
            )}
            {total > DISK_FULL_BYTES && (
              <span
                className="resource-disk-mark mark-full"
                style={{
                  left: `${((total - DISK_FULL_BYTES) / total) * 100}%`,
                }}
                title={t("consumption.disk_full_mark")}
              />
            )}
          </div>
          <div className="resource-disk-legend">
            {segments.map((seg) => (
              <span key={seg.id} className="resource-disk-legend-item">
                <i
                  className={`resource-disk-swatch ${seg.cls}`}
                  aria-hidden="true"
                />
                {seg.label}
              </span>
            ))}
          </div>
        </>
      )}
      {empty ? (
        <p className="muted-text">{t("consumption.no_disk")}</p>
      ) : (
        <>
          {machineItems.length > 0 && (
            <div className="resource-disk-subtabs" role="tablist">
              <button
                type="button"
                role="tab"
                className={`resource-disk-subtab${activeView === "djinn" ? " active" : ""}`}
                aria-selected={activeView === "djinn"}
                onClick={() => setView("djinn")}
              >
                {t("consumption.disk_tab_djinn")}{" "}
                <b>{formatValue("memory", djinnTotal)}</b>
              </button>
              <button
                type="button"
                role="tab"
                className={`resource-disk-subtab${activeView === "machine" ? " active" : ""}`}
                aria-selected={activeView === "machine"}
                onClick={() => setView("machine")}
              >
                {t("consumption.disk_tab_machine")}{" "}
                <b>{formatValue("memory", machineTotal)}</b>
              </button>
            </div>
          )}
          {activeView === "djinn" ? (
            <div className="resource-disk-djinn">
              <div className="resource-disk-list" role="list">
                {folders.map((f) => {
                  const size = Number(f.sizeBytes);
                  const share = Math.round((size / top) * 100);
                  return (
                    <div
                      key={f.path}
                      className="resource-disk-row"
                      role="listitem"
                    >
                      <code className="resource-disk-path">{f.path}</code>
                      <span className="resource-share" aria-hidden="true">
                        <span style={{ width: `${share}%` }} />
                      </span>
                      <span className="resource-disk-size">
                        {formatValue("memory", size)}
                      </span>
                    </div>
                  );
                })}
                {djinnItems.map((item) => (
                  <DiskItemRow key={item.kind} item={item} showRisk />
                ))}
              </div>
              <p className="resource-disk-hint muted-text">
                {t("consumption.disk_djinn_note")}
              </p>
            </div>
          ) : (
            <div className="resource-disk-machine">
              {riskGroups.map((group) => (
                <section
                  key={group.slug}
                  className={`resource-disk-group risk-${group.slug}`}
                  aria-label={group.label}
                >
                  <h5 className="resource-disk-group-head">
                    <DiskRiskBadge risk={group.risk} />
                    <span className="resource-disk-group-sub">{group.sub}</span>
                    <span className="resource-disk-group-total">
                      {group.totalLabel}
                    </span>
                  </h5>
                  <div className="resource-disk-group-list" role="list">
                    {group.items.map((item) => (
                      <DiskItemRow key={item.kind} item={item} />
                    ))}
                  </div>
                </section>
              ))}
              {safeAllCommand && (
                <div className="resource-disk-cmd resource-disk-safe-all">
                  <span className="resource-disk-safe-label">
                    {t("consumption.disk_safe_all")}
                  </span>
                  <code>{safeAllCommand}</code>
                  <CopyButton text={safeAllCommand} />
                </div>
              )}
            </div>
          )}
        </>
      )}
    </section>
  );
});

export function ConsumptionPanel({
  initialMinutes = 30,
  initialFolded = false,
  initialTab = "resources",
  initialData = null,
  initialCommands = [],
  wishes,
  tasks,
  projects = [],
  projectIds,
}: {
  initialMinutes?: number;
  initialFolded?: boolean;
  initialTab?: ConsumptionTab;
  initialData?: MachineServiceSeriesResponse | null;
  initialCommands?: CommandCost[];
  // The active wishes, in order: running workers are grouped by wish.
  wishes?: readonly Wish[];
  // The tasks of the active wishes, by id: a worker's row shows the facts of its job.
  tasks?: ReadonlyMap<string, Task>;
  projects?: readonly Project[];
  // The projects of the active wishes: only their commands show; all when undefined.
  projectIds?: ReadonlySet<string>;
} = {}) {
  const djinn = useDjinn();
  const [minutes, setMinutes] = useState<number>(initialMinutes);
  const [folded, setFolded] = useState<boolean>(initialFolded);
  const [tab, setTab] = useState<ConsumptionTab>(initialTab);
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
    // Unfocused, it reads on; hidden, seldom (keepLive in stillness.ts).
    void read();
    const stop = keepLive(() => void read(), SERIES_EVERY);
    return () => {
      active = false;
      stop();
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
    const stop = keepLive(() => void read(), COMMANDS_EVERY);
    return () => {
      active = false;
      stop();
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
  // Workers grouped by wish, watchers last in each group (they consume little).
  const wishGroups = useMemo(() => {
    const byWish = new Map<string, WorkerSeries[]>();
    for (const w of wishes ?? []) byWish.set(w.id, []);
    for (const w of workers) {
      const key = w.wishId || "";
      const list = byWish.get(key);
      if (list) list.push(w);
      else byWish.set(key, [w]);
    }
    const wishTitles = new Map((wishes ?? []).map((w) => [w.id, w.title]));
    const out: {
      wishId: string;
      title?: string;
      workers: WorkerSeries[];
    }[] = [];
    for (const [wishId, list] of byWish) {
      if (list.length === 0) continue;
      const sorted = [...list].sort(
        (a, b) =>
          Number(a.provider === Provider.WATCH) -
          Number(b.provider === Provider.WATCH),
      );
      out.push({
        wishId,
        title: wishTitles.get(wishId),
        workers: sorted,
      });
    }
    return out;
  }, [workers, wishes]);
  const orderedWorkers = useMemo(
    () => wishGroups.flatMap((g) => g.workers),
    [wishGroups],
  );
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
  const machinePoints = data?.machine?.points ?? [];
  const lastMachinePoint = machinePoints.at(-1);
  const lastMachineCpu = lastMachinePoint?.cpuPercent ?? 0;
  const peakMachineCpu = Math.max(0, ...machinePoints.map((p) => p.cpuPercent));
  const lastMachineMemory = Number(lastMachinePoint?.memoryBytes ?? 0n);

  return (
    <section className="consumption-panel" aria-label={t("consumption.title")}>
      <header className="consumption-header">
        <div className="consumption-header-lead">
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
          {!folded && data?.machine && (
            <div
              className="resource-machine-total"
              aria-label={t("consumption.machine_title")}
            >
              <span className="resource-machine-total-item">
                {t("consumption.cores", { count: data.machine.cores })}
                {memoryTotal > 0 && ` · ${formatValue("memory", memoryTotal)}`}
              </span>
              <span className="resource-machine-total-item">
                <strong>{t("consumption.cpu")}</strong>{" "}
                {t("consumption.now_peak", {
                  now: formatValue("cpu", lastMachineCpu),
                  peak: formatValue("cpu", peakMachineCpu),
                })}
              </span>
              {memoryTotal > 0 && (
                <span className="resource-machine-total-item">
                  <strong>{t("consumption.memory")}</strong>{" "}
                  {t("consumption.used_of", {
                    used: formatValue("memory", lastMachineMemory),
                    total: formatValue("memory", memoryTotal),
                  })}
                </span>
              )}
            </div>
          )}
        </div>
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
        <>
          <div className="view-tabs consumption-tabs" role="tablist">
            {(
              [
                ["resources", t("consumption.tab_resources")],
                ["gates", t("consumption.tab_gates")],
                ["disk", t("consumption.tab_disk")],
              ] as const
            ).map(([id, label]) => (
              <button
                key={id}
                type="button"
                role="tab"
                id={`consumption-tab-${id}`}
                aria-selected={tab === id}
                className={tab === id ? "active" : ""}
                onClick={() => setTab(id)}
              >
                {label}
              </button>
            ))}
          </div>

          <div
            className="consumption-body"
            role="tabpanel"
            aria-labelledby={`consumption-tab-${tab}`}
          >
            {tab === "resources" && data?.workerMeasure && (
              <p className="consumption-notice muted-text">
                {t("resources.not_measured")}
                {data.workerMeasure !== "windows"
                  ? ` (${data.workerMeasure})`
                  : ""}
              </p>
            )}
            {(tab === "resources" || tab === "gates") && data?.machine && (
              <MachineBand
                series={data.machine}
                gates={gates}
                tMin={tMin}
                tMax={tMax}
              />
            )}
            {tab === "resources" && (
              <section
                className="resource-workers"
                aria-label={t("consumption.workers_title")}
              >
                <div className="resource-workers-head">
                  <h4 className="resource-section-title">
                    {t("consumption.workers_title")}
                  </h4>
                  {workers.length > 1 && (
                    <p className="resource-workers-scale">
                      {t("consumption.workers_scale")}
                    </p>
                  )}
                </div>
                <WorkerHeatmap
                  workers={orderedWorkers}
                  cpuTop={workerTops.cpu}
                  memoryTop={workerTops.memory}
                />
                {workers.length === 0 ? (
                  <p className="muted-text">{t("consumption.no_workers")}</p>
                ) : (
                  wishGroups.map((group) => (
                    <div
                      key={group.wishId || "other"}
                      className="resource-wish-group"
                    >
                      {(group.title || wishGroups.length > 1) && (
                        <h5 className="resource-wish-title">
                          {group.title || group.wishId}
                        </h5>
                      )}
                      {group.workers.map((w) => {
                        const task = tasks?.get(w.taskId);
                        return (
                          <WorkerRow
                            key={w.taskId || w.code}
                            worker={w}
                            task={task}
                            project={
                              task ? projectById.get(task.projectId) : undefined
                            }
                            memoryTotal={memoryTotal}
                            cpuTop={workerTops.cpu}
                            memoryTop={workerTops.memory}
                            tMin={tMin}
                            tMax={tMax}
                            onOpen={open}
                          />
                        );
                      })}
                    </div>
                  ))
                )}
              </section>
            )}

            {tab === "gates" && (
              <CommandGroups
                costs={commands}
                projectIds={projectIds}
                projectNames={projectNames}
              />
            )}

            {tab === "disk" && <DiskSection disk={data?.disk} />}
          </div>
        </>
      )}
    </section>
  );
}

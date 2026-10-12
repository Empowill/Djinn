// Pure consumption panel under the flight plan: running workers with their CPU / memory series over time,
// machine total band with Djinn's share and operating load limit line, gate interval bands, and commands cost table.
import { memo, useCallback, useEffect, useId, useMemo, useState } from "react";
import { timestampDate } from "@bufbuild/protobuf/wkt";
import { ChevronDown, ChevronRight } from "lucide-react";

import { Provider } from "../gen/ts/plan/v1/plan_pb";
import type {
  CommandCost,
  GateInterval,
  MachineSeries,
  MachineServiceSeriesResponse,
  WorkerSeries,
} from "../gen/ts/machine/v1/machine_pb";
import { useDjinn } from "./data/djinn";
import { shortModel } from "./data/format";
import { t } from "./i18n";
import { onStillness, still } from "./stillness";
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

export function formatBytes(b: bigint | number): string {
  const bytes = Number(b);
  if (!bytes || bytes <= 0) return "—";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(0)} KiB`;
  if (bytes < 1024 * 1024 * 1024) {
    return `${(bytes / (1024 * 1024)).toFixed(0)} MiB`;
  }
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(1)} GiB`;
}

export function formatMib(b: bigint | number): string {
  const bytes = Number(b);
  if (!bytes || bytes <= 0) return "0 MiB";
  return `${Math.round(bytes / (1024 * 1024))} MiB`;
}

export function formatSec(s: number): string {
  if (s <= 0) return "—";
  return `${s.toFixed(1)}s`;
}

const Sparkline = memo(function Sparkline({ values }: { values?: number[] }) {
  if (!values || values.length === 0) {
    return <span className="sparkline-empty">—</span>;
  }
  const max = Math.max(...values, 0.001);
  const width = 64;
  const height = 16;
  const count = Math.min(values.length, 24);
  const recent = values.slice(-count);
  const barW = Math.max(1, Math.floor((width - (count - 1)) / count));

  return (
    <svg
      className="consumption-sparkline"
      viewBox={`0 0 ${width} ${height}`}
      width={width}
      height={height}
      aria-hidden="true"
    >
      {recent.map((v, i) => {
        const barH = Math.max(1, Math.round((v / max) * (height - 2)));
        const x = i * (barW + 1);
        const y = height - barH;
        return (
          <rect
            key={i}
            x={x}
            y={y}
            width={barW}
            height={barH}
            rx="0.5"
            className="sparkline-bar"
          >
            <title>{`${v.toFixed(1)}s`}</title>
          </rect>
        );
      })}
    </svg>
  );
});

interface ChartPoint {
  t: number;
  val: number;
}

const SVG_WIDTH = 340;
const SVG_HEIGHT = 68;
const PAD = { top: 8, right: 8, bottom: 16, left: 36 };
const PLOT_W = SVG_WIDTH - PAD.left - PAD.right;
const PLOT_H = SVG_HEIGHT - PAD.top - PAD.bottom;

function buildPath(
  pts: ChartPoint[],
  tMin: number,
  tMax: number,
  yMax: number,
): {
  line: string;
  area: string;
  last?: { x: number; y: number; val: number };
} {
  if (!pts || pts.length === 0) return { line: "", area: "" };
  const span = Math.max(1, tMax - tMin);
  const clampY = Math.max(1, yMax);

  const coords = pts.map((p) => {
    const x = PAD.left + Math.max(0, Math.min(1, (p.t - tMin) / span)) * PLOT_W;
    const y =
      PAD.top + (1 - Math.max(0, Math.min(clampY, p.val)) / clampY) * PLOT_H;
    return { x, y, val: p.val };
  });

  const last = coords[coords.length - 1];
  const yBase = PAD.top + PLOT_H;

  if (coords.length === 1) {
    const c = coords[0];
    return {
      line: `M ${PAD.left} ${c.y} L ${PAD.left + PLOT_W} ${c.y}`,
      area: `M ${PAD.left} ${yBase} L ${PAD.left} ${c.y} L ${PAD.left + PLOT_W} ${c.y} L ${PAD.left + PLOT_W} ${yBase} Z`,
      last,
    };
  }

  const lineD = coords.reduce(
    (acc, c, i) =>
      `${acc} ${i === 0 ? "M" : "L"} ${c.x.toFixed(1)} ${c.y.toFixed(1)}`,
    "",
  );
  const first = coords[0];
  const areaD = `M ${first.x.toFixed(1)} ${yBase} ${lineD} L ${last.x.toFixed(1)} ${yBase} Z`;

  return { line: lineD, area: areaD, last };
}

// Worker CPU & Memory Side-by-Side Charts
const WorkerChart = memo(function WorkerChart({
  title,
  unit,
  points,
  gates,
  tMin,
  tMax,
  yMax,
  peakVal,
  lastFormatted,
  strokeColor,
  fillColor,
}: {
  title: string;
  unit: string;
  points: ChartPoint[];
  gates: GateInterval[];
  tMin: number;
  tMax: number;
  yMax: number;
  peakVal?: number;
  lastFormatted: string;
  strokeColor: string;
  fillColor: string;
}) {
  const chartId = useId().replace(/:/g, "_");
  const hatchId = `hatch_${chartId}`;
  const gradId = `grad_${chartId}`;
  const span = Math.max(1, tMax - tMin);

  const { line, area, last } = useMemo(
    () => buildPath(points, tMin, tMax, yMax),
    [points, tMin, tMax, yMax],
  );

  const peakY =
    peakVal && peakVal > 0
      ? PAD.top +
        (1 - Math.max(0, Math.min(yMax, peakVal)) / Math.max(1, yMax)) * PLOT_H
      : undefined;

  return (
    <div className="consumption-chart-box">
      <div className="consumption-chart-header">
        <span className="consumption-chart-title">{title}</span>
        <span className="consumption-chart-stat">{lastFormatted}</span>
      </div>
      <svg
        className="consumption-chart-svg"
        viewBox={`0 0 ${SVG_WIDTH} ${SVG_HEIGHT}`}
        width="100%"
        height={SVG_HEIGHT}
        aria-hidden="true"
      >
        <defs>
          <pattern
            id={hatchId}
            width="6"
            height="6"
            patternTransform="rotate(45 0 0)"
            patternUnits="userSpaceOnUse"
          >
            <line
              x1="0"
              y1="0"
              x2="0"
              y2="6"
              stroke="var(--n-51)"
              strokeWidth="1.2"
            />
          </pattern>
          <linearGradient id={gradId} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor={fillColor} stopOpacity="0.28" />
            <stop offset="100%" stopColor={fillColor} stopOpacity="0.04" />
          </linearGradient>
        </defs>

        {/* Faint Grid */}
        <line
          x1={PAD.left}
          y1={PAD.top}
          x2={PAD.left + PLOT_W}
          y2={PAD.top}
          stroke="var(--n-2a)"
          strokeDasharray="2 3"
        />
        <line
          x1={PAD.left}
          y1={PAD.top + PLOT_H / 2}
          x2={PAD.left + PLOT_W}
          y2={PAD.top + PLOT_H / 2}
          stroke="var(--n-2a)"
          strokeDasharray="2 3"
        />
        <line
          x1={PAD.left}
          y1={PAD.top + PLOT_H}
          x2={PAD.left + PLOT_W}
          y2={PAD.top + PLOT_H}
          stroke="var(--n-30)"
        />

        {/* Y-axis labels */}
        <text
          x={PAD.left - 4}
          y={PAD.top + 7}
          textAnchor="end"
          fontSize="9"
          fill="var(--n-65)"
          className="chart-axis-label"
        >
          {unit === "%" ? "100%" : formatMib(yMax)}
        </text>
        <text
          x={PAD.left - 4}
          y={PAD.top + PLOT_H + 3}
          textAnchor="end"
          fontSize="9"
          fill="var(--n-65)"
          className="chart-axis-label"
        >
          0
        </text>

        {/* Gate intervals held by this worker */}
        {gates.map((g, idx) => {
          const tGrant = g.grantTime ? timestampDate(g.grantTime).getTime() : 0;
          const tRelease = g.releaseTime
            ? timestampDate(g.releaseTime).getTime()
            : tMax;
          const tAsked = g.askedTime
            ? timestampDate(g.askedTime).getTime()
            : tGrant;

          if (tRelease < tMin || tGrant > tMax) return null;

          const xAsked =
            PAD.left +
            Math.max(0, Math.min(1, (tAsked - tMin) / span)) * PLOT_W;
          const xGrant =
            PAD.left +
            Math.max(0, Math.min(1, (tGrant - tMin) / span)) * PLOT_W;
          const xRelease =
            PAD.left +
            Math.max(0, Math.min(1, (tRelease - tMin) / span)) * PLOT_W;

          const waitSec = Math.max(0, (tGrant - tAsked) / 1000);
          const heldSec = Math.max(0, (tRelease - tGrant) / 1000);

          const wWait = Math.max(1, xGrant - xAsked);
          const wHeld = Math.max(2, xRelease - xGrant);

          const tip = `${g.project ? g.project + " · " : ""}${g.gate}: ${g.command || g.gate}\nWait: ${waitSec.toFixed(1)}s · Held: ${heldSec.toFixed(1)}s${!g.releaseTime ? " (active)" : ""}`;

          return (
            <g key={idx} className="gate-band-group">
              {/* Hatched wait lead-in */}
              {waitSec > 0.05 && (
                <rect
                  x={xAsked}
                  y={PAD.top}
                  width={wWait}
                  height={PLOT_H}
                  fill={`url(#${hatchId})`}
                  className="gate-wait-rect"
                >
                  <title>{`${g.project ? g.project + " · " : ""}Wait for ${g.gate}: ${waitSec.toFixed(1)}s`}</title>
                </rect>
              )}
              {/* Solid held band */}
              <rect
                x={xGrant}
                y={PAD.top}
                width={wHeld}
                height={PLOT_H}
                className="gate-held-rect"
                rx="1"
              >
                <title>{tip}</title>
              </rect>
              {wHeld >= 28 && (
                <text
                  x={xGrant + 3}
                  y={PAD.top + 10}
                  fontSize="8"
                  className="gate-held-label"
                >
                  {g.gate}
                </text>
              )}
            </g>
          );
        })}

        {/* Peak dashed line if applicable */}
        {peakY !== undefined && (
          <line
            x1={PAD.left}
            y1={peakY}
            x2={PAD.left + PLOT_W}
            y2={peakY}
            stroke="var(--sand)"
            strokeDasharray="3 3"
            strokeOpacity="0.75"
          />
        )}

        {/* Area fill */}
        {area && <path d={area} fill={`url(#${gradId})`} />}

        {/* Curve stroke */}
        {line && (
          <path
            d={line}
            fill="none"
            stroke={strokeColor}
            strokeWidth="1.5"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        )}

        {/* Last value dot */}
        {last && (
          <circle
            cx={last.x}
            cy={last.y}
            r="3"
            fill={strokeColor}
            stroke="var(--n-14)"
            strokeWidth="1.5"
          />
        )}
      </svg>
    </div>
  );
});

// Single Running Worker Row
const WorkerRow = memo(function WorkerRow({
  worker,
  machineCores,
  tMin,
  tMax,
}: {
  worker: WorkerSeries;
  machineCores: number;
  tMin: number;
  tMax: number;
}) {
  const prov = providerNames[worker.provider] ?? "claude";
  const provClass = providerClasses[worker.provider] ?? "provider-claude";
  const modelShort = shortModel(worker.model);

  const cpuPoints: ChartPoint[] = useMemo(() => {
    return worker.points.map((p) => ({
      t: p.time ? timestampDate(p.time).getTime() : 0,
      val: p.cpuPercent,
    }));
  }, [worker.points]);

  const memPoints: ChartPoint[] = useMemo(() => {
    return worker.points.map((p) => ({
      t: p.time ? timestampDate(p.time).getTime() : 0,
      val: Number(p.memoryBytes),
    }));
  }, [worker.points]);

  const lastCpu =
    cpuPoints.length > 0 ? cpuPoints[cpuPoints.length - 1].val : 0;
  const lastMem =
    memPoints.length > 0 ? memPoints[memPoints.length - 1].val : 0;
  const machineShare = lastCpu / Math.max(1, machineCores);

  const yMaxCpu = Math.max(100, Math.ceil(worker.peakCpuPercent / 10) * 10);
  const yMaxMem = Math.max(
    Number(worker.peakMemoryBytes) * 1.1,
    lastMem * 1.1,
    1024 * 1024,
  );

  const cpuFormatted = `${lastCpu.toFixed(1)}% (${machineShare.toFixed(1)}% ${t("consumption.cpu_machine_share")}) · ${t("consumption.peak", { peak: `${worker.peakCpuPercent.toFixed(0)}%` })}`;
  const memFormatted = `${formatBytes(lastMem)} · ${t("consumption.peak", { peak: formatBytes(worker.peakMemoryBytes) })}`;

  return (
    <div
      className={`consumption-worker-row ${worker.paused ? "is-paused" : ""}`}
    >
      <div className="consumption-worker-meta">
        <div className="consumption-worker-head">
          <span className="agent-code">{worker.code}</span>
          {worker.paused && (
            <span className="consumption-worker-paused-badge">
              {t("consumption.paused")}
            </span>
          )}
          <span className={`provider-badge ${provClass}`}>{prov}</span>
          {modelShort && (
            <span className="consumption-worker-model">{modelShort}</span>
          )}
        </div>
        <div className="consumption-worker-title" title={worker.title}>
          {worker.title}
        </div>
      </div>

      <div className="consumption-worker-charts">
        <WorkerChart
          title={t("consumption.cpu_one_core")}
          unit="%"
          points={cpuPoints}
          gates={worker.gates}
          tMin={tMin}
          tMax={tMax}
          yMax={yMaxCpu}
          peakVal={worker.peakCpuPercent}
          lastFormatted={cpuFormatted}
          strokeColor="var(--term-blue, #3b82f6)"
          fillColor="var(--term-blue, #3b82f6)"
        />
        <WorkerChart
          title={t("consumption.memory")}
          unit="bytes"
          points={memPoints}
          gates={worker.gates}
          tMin={tMin}
          tMax={tMax}
          yMax={yMaxMem}
          peakVal={Number(worker.peakMemoryBytes)}
          lastFormatted={memFormatted}
          strokeColor="var(--sand, #cbbb87)"
          fillColor="var(--sand, #cbbb87)"
        />
      </div>
    </div>
  );
});

// Stacked Gates Timeline across the machine
const StackedGatesChart = memo(function StackedGatesChart({
  gates,
  tMin,
  tMax,
}: {
  gates: GateInterval[];
  tMin: number;
  tMax: number;
}) {
  const chartId = useId().replace(/:/g, "_");
  const hatchId = `hatch_stacked_${chartId}`;
  const span = Math.max(1, tMax - tMin);

  const { stacked, numLanes } = useMemo(() => {
    const visible = gates
      .filter((g) => {
        const grant = g.grantTime ? timestampDate(g.grantTime).getTime() : 0;
        const release = g.releaseTime
          ? timestampDate(g.releaseTime).getTime()
          : tMax;
        return release >= tMin && grant <= tMax;
      })
      .sort((a, b) => {
        const tA = a.grantTime ? timestampDate(a.grantTime).getTime() : 0;
        const tB = b.grantTime ? timestampDate(b.grantTime).getTime() : 0;
        return tA - tB;
      });

    const laneEndTimes: number[] = [];
    const res: { gate: GateInterval; lane: number }[] = [];

    for (const g of visible) {
      const asked = g.askedTime
        ? timestampDate(g.askedTime).getTime()
        : g.grantTime
          ? timestampDate(g.grantTime).getTime()
          : 0;
      const release = g.releaseTime
        ? timestampDate(g.releaseTime).getTime()
        : tMax;

      let lane = -1;
      for (let i = 0; i < laneEndTimes.length; i++) {
        if (laneEndTimes[i] <= asked) {
          lane = i;
          laneEndTimes[i] = release;
          break;
        }
      }
      if (lane === -1) {
        lane = laneEndTimes.length;
        laneEndTimes.push(release);
      }
      res.push({ gate: g, lane });
    }

    return { stacked: res, numLanes: Math.max(1, laneEndTimes.length) };
  }, [gates, tMin, tMax]);

  if (stacked.length === 0) return null;

  const laneHeight = 16;
  const laneGap = 3;
  const totalH = numLanes * (laneHeight + laneGap) + 4;

  return (
    <div className="consumption-stacked-gates">
      <div className="consumption-stacked-header">
        <span className="consumption-stacked-title">
          {t("consumption.gates")}
        </span>
      </div>
      <svg
        className="consumption-stacked-svg"
        viewBox={`0 0 ${SVG_WIDTH} ${totalH}`}
        width="100%"
        height={totalH}
        aria-hidden="true"
      >
        <defs>
          <pattern
            id={hatchId}
            width="6"
            height="6"
            patternTransform="rotate(45 0 0)"
            patternUnits="userSpaceOnUse"
          >
            <line
              x1="0"
              y1="0"
              x2="0"
              y2="6"
              stroke="var(--n-51)"
              strokeWidth="1.2"
            />
          </pattern>
        </defs>

        {stacked.map(({ gate: g, lane }, i) => {
          const tGrant = g.grantTime ? timestampDate(g.grantTime).getTime() : 0;
          const tRelease = g.releaseTime
            ? timestampDate(g.releaseTime).getTime()
            : tMax;
          const tAsked = g.askedTime
            ? timestampDate(g.askedTime).getTime()
            : tGrant;

          const xAsked =
            PAD.left +
            Math.max(0, Math.min(1, (tAsked - tMin) / span)) * PLOT_W;
          const xGrant =
            PAD.left +
            Math.max(0, Math.min(1, (tGrant - tMin) / span)) * PLOT_W;
          const xRelease =
            PAD.left +
            Math.max(0, Math.min(1, (tRelease - tMin) / span)) * PLOT_W;

          const y = lane * (laneHeight + laneGap) + 2;
          const wWait = Math.max(1, xGrant - xAsked);
          const wHeld = Math.max(2, xRelease - xGrant);

          const waitSec = Math.max(0, (tGrant - tAsked) / 1000);
          const heldSec = Math.max(0, (tRelease - tGrant) / 1000);

          const tip = `${g.project ? g.project + " · " : ""}${g.gate}: ${g.command || g.gate}\nWait: ${waitSec.toFixed(1)}s · Held: ${heldSec.toFixed(1)}s${!g.releaseTime ? " (active)" : ""}`;

          return (
            <g key={i} className="gate-band-group">
              {waitSec > 0.05 && (
                <rect
                  x={xAsked}
                  y={y}
                  width={wWait}
                  height={laneHeight}
                  fill={`url(#${hatchId})`}
                  className="gate-wait-rect"
                >
                  <title>{`${g.project ? g.project + " · " : ""}Wait for ${g.gate}: ${waitSec.toFixed(1)}s`}</title>
                </rect>
              )}
              <rect
                x={xGrant}
                y={y}
                width={wHeld}
                height={laneHeight}
                rx="2"
                className="gate-held-rect"
              >
                <title>{tip}</title>
              </rect>
              {wHeld >= 24 && (
                <text
                  x={xGrant + 4}
                  y={y + 11}
                  fontSize="9"
                  className="gate-held-label"
                >
                  {g.command ? `${g.gate} · ${g.command}` : g.gate}
                </text>
              )}
            </g>
          );
        })}
      </svg>
    </div>
  );
});

// Top Machine Band (CPU & Memory + Stacked Gates)
const MachineBand = memo(function MachineBand({
  series,
  gates,
  tMin,
  tMax,
}: {
  series: MachineSeries;
  gates: GateInterval[];
  tMin: number;
  tMax: number;
}) {
  const chartId = useId().replace(/:/g, "_");
  const cpuGradId = `mach_cpu_${chartId}`;
  const memGradId = `mach_mem_${chartId}`;

  const cpuPoints: ChartPoint[] = useMemo(() => {
    return series.points.map((p) => ({
      t: p.time ? timestampDate(p.time).getTime() : 0,
      val: p.cpuPercent,
    }));
  }, [series.points]);

  const djinnCpuPoints: ChartPoint[] = useMemo(() => {
    return series.points.map((p) => ({
      t: p.time ? timestampDate(p.time).getTime() : 0,
      val: p.djinnCpuPercent,
    }));
  }, [series.points]);

  const memPoints: ChartPoint[] = useMemo(() => {
    return series.points.map((p) => ({
      t: p.time ? timestampDate(p.time).getTime() : 0,
      val: Number(p.memoryBytes),
    }));
  }, [series.points]);

  const djinnMemPoints: ChartPoint[] = useMemo(() => {
    return series.points.map((p) => ({
      t: p.time ? timestampDate(p.time).getTime() : 0,
      val: Number(p.djinnMemoryBytes),
    }));
  }, [series.points]);

  const lastCpu =
    cpuPoints.length > 0 ? cpuPoints[cpuPoints.length - 1].val : 0;
  const lastDjinnCpu =
    djinnCpuPoints.length > 0
      ? djinnCpuPoints[djinnCpuPoints.length - 1].val
      : 0;
  const lastMem =
    memPoints.length > 0 ? memPoints[memPoints.length - 1].val : 0;
  const lastDjinnMem =
    djinnMemPoints.length > 0
      ? djinnMemPoints[djinnMemPoints.length - 1].val
      : 0;

  const totalMem = Number(series.memoryTotalBytes);
  const yMaxMem = Math.max(totalMem, 1024 * 1024);

  const {
    line: totalCpuLine,
    area: totalCpuArea,
    last: lastTotalCpu,
  } = useMemo(
    () => buildPath(cpuPoints, tMin, tMax, 100),
    [cpuPoints, tMin, tMax],
  );
  const { line: djinnCpuLine } = useMemo(
    () => buildPath(djinnCpuPoints, tMin, tMax, 100),
    [djinnCpuPoints, tMin, tMax],
  );

  const {
    line: totalMemLine,
    area: totalMemArea,
    last: lastTotalMem,
  } = useMemo(
    () => buildPath(memPoints, tMin, tMax, yMaxMem),
    [memPoints, tMin, tMax, yMaxMem],
  );
  const { line: djinnMemLine } = useMemo(
    () => buildPath(djinnMemPoints, tMin, tMax, yMaxMem),
    [djinnMemPoints, tMin, tMax, yMaxMem],
  );

  const limitShare = series.loadLimitShare;
  const limitLineY =
    limitShare > 0
      ? PAD.top + (1 - Math.min(100, limitShare * 100) / 100) * PLOT_H
      : undefined;

  const limitMemBytes = Number(series.loadLimitMemoryBytes);
  const limitMemY =
    limitMemBytes > 0
      ? PAD.top + (1 - Math.min(yMaxMem, limitMemBytes) / yMaxMem) * PLOT_H
      : undefined;

  return (
    <div className="consumption-machine-band">
      <div className="consumption-machine-stats">
        <span className="consumption-machine-name">
          {t("consumption.machine_title")}
        </span>
        <span className="consumption-machine-cores">
          {t("consumption.cores", { count: series.cores })}
        </span>
        <span className="consumption-stat-item">
          {t("consumption.cpu")}: <strong>{lastCpu.toFixed(1)}%</strong>{" "}
          <small>
            (
            {t("consumption.djinn_share", {
              share: `${lastDjinnCpu.toFixed(1)}%`,
            })}
            )
          </small>
        </span>
        <span className="consumption-stat-item">
          {t("consumption.memory")}:{" "}
          <strong>
            {formatBytes(lastMem)} / {formatBytes(totalMem)}
          </strong>{" "}
          <small>
            (
            {t("consumption.djinn_share", { share: formatBytes(lastDjinnMem) })}
            )
          </small>
        </span>
        {limitShare > 0 && (
          <span className="consumption-limit-badge">
            {t("consumption.limit_line")}: {(limitShare * 100).toFixed(0)}%
          </span>
        )}
      </div>

      <div className="consumption-machine-charts">
        {/* Machine Total CPU Chart */}
        <div className="consumption-chart-box">
          <div className="consumption-chart-header">
            <span className="consumption-chart-title">
              {t("consumption.cpu")} (
              {t("consumption.cores", { count: series.cores })})
            </span>
            <span className="consumption-chart-stat">
              {lastCpu.toFixed(1)}%
            </span>
          </div>
          <svg
            className="consumption-chart-svg"
            viewBox={`0 0 ${SVG_WIDTH} ${SVG_HEIGHT}`}
            width="100%"
            height={SVG_HEIGHT}
            aria-hidden="true"
          >
            <defs>
              <linearGradient id={cpuGradId} x1="0" y1="0" x2="0" y2="1">
                <stop
                  offset="0%"
                  stopColor="var(--term-blue, #3b82f6)"
                  stopOpacity="0.3"
                />
                <stop
                  offset="100%"
                  stopColor="var(--term-blue, #3b82f6)"
                  stopOpacity="0.04"
                />
              </linearGradient>
            </defs>

            {/* Grid */}
            <line
              x1={PAD.left}
              y1={PAD.top}
              x2={PAD.left + PLOT_W}
              y2={PAD.top}
              stroke="var(--n-2a)"
              strokeDasharray="2 3"
            />
            <line
              x1={PAD.left}
              y1={PAD.top + PLOT_H / 2}
              x2={PAD.left + PLOT_W}
              y2={PAD.top + PLOT_H / 2}
              stroke="var(--n-2a)"
              strokeDasharray="2 3"
            />
            <line
              x1={PAD.left}
              y1={PAD.top + PLOT_H}
              x2={PAD.left + PLOT_W}
              y2={PAD.top + PLOT_H}
              stroke="var(--n-30)"
            />
            <text
              x={PAD.left - 4}
              y={PAD.top + 7}
              textAnchor="end"
              fontSize="9"
              fill="var(--n-65)"
            >
              100%
            </text>
            <text
              x={PAD.left - 4}
              y={PAD.top + PLOT_H + 3}
              textAnchor="end"
              fontSize="9"
              fill="var(--n-65)"
            >
              0
            </text>

            {/* Limit Line */}
            {limitLineY !== undefined && (
              <line
                x1={PAD.left}
                y1={limitLineY}
                x2={PAD.left + PLOT_W}
                y2={limitLineY}
                stroke="var(--term-yellow, #c4a000)"
                strokeDasharray="4 2"
                strokeWidth="1.2"
              >
                <title>{`${t("consumption.limit_line")}: ${(limitShare * 100).toFixed(0)}%`}</title>
              </line>
            )}

            {/* Total CPU Area & Line */}
            {totalCpuArea && (
              <path d={totalCpuArea} fill={`url(#${cpuGradId})`} />
            )}
            {totalCpuLine && (
              <path
                d={totalCpuLine}
                fill="none"
                stroke="var(--term-blue, #3b82f6)"
                strokeWidth="1.5"
                strokeLinecap="round"
                strokeLinejoin="round"
              />
            )}
            {/* Djinn CPU Share line */}
            {djinnCpuLine && (
              <path
                d={djinnCpuLine}
                fill="none"
                stroke="var(--n-88, #888888)"
                strokeWidth="1.2"
                strokeDasharray="2 2"
              />
            )}
            {lastTotalCpu && (
              <circle
                cx={lastTotalCpu.x}
                cy={lastTotalCpu.y}
                r="3"
                fill="var(--term-blue, #3b82f6)"
                stroke="var(--n-14)"
                strokeWidth="1.5"
              />
            )}
          </svg>
        </div>

        {/* Machine Total Memory Chart */}
        <div className="consumption-chart-box">
          <div className="consumption-chart-header">
            <span className="consumption-chart-title">
              {t("consumption.memory")}
            </span>
            <span className="consumption-chart-stat">
              {formatBytes(lastMem)}
            </span>
          </div>
          <svg
            className="consumption-chart-svg"
            viewBox={`0 0 ${SVG_WIDTH} ${SVG_HEIGHT}`}
            width="100%"
            height={SVG_HEIGHT}
            aria-hidden="true"
          >
            <defs>
              <linearGradient id={memGradId} x1="0" y1="0" x2="0" y2="1">
                <stop
                  offset="0%"
                  stopColor="var(--sand, #cbbb87)"
                  stopOpacity="0.28"
                />
                <stop
                  offset="100%"
                  stopColor="var(--sand, #cbbb87)"
                  stopOpacity="0.04"
                />
              </linearGradient>
            </defs>

            {/* Grid */}
            <line
              x1={PAD.left}
              y1={PAD.top}
              x2={PAD.left + PLOT_W}
              y2={PAD.top}
              stroke="var(--n-2a)"
              strokeDasharray="2 3"
            />
            <line
              x1={PAD.left}
              y1={PAD.top + PLOT_H}
              x2={PAD.left + PLOT_W}
              y2={PAD.top + PLOT_H}
              stroke="var(--n-30)"
            />
            <text
              x={PAD.left - 4}
              y={PAD.top + 7}
              textAnchor="end"
              fontSize="9"
              fill="var(--n-65)"
            >
              {formatBytes(totalMem)}
            </text>
            <text
              x={PAD.left - 4}
              y={PAD.top + PLOT_H + 3}
              textAnchor="end"
              fontSize="9"
              fill="var(--n-65)"
            >
              0
            </text>

            {/* Limit Memory Line */}
            {limitMemY !== undefined && (
              <line
                x1={PAD.left}
                y1={limitMemY}
                x2={PAD.left + PLOT_W}
                y2={limitMemY}
                stroke="var(--term-yellow, #c4a000)"
                strokeDasharray="4 2"
                strokeWidth="1.2"
              >
                <title>{`${t("consumption.limit_line")}: ${formatBytes(limitMemBytes)}`}</title>
              </line>
            )}

            {/* Total Memory Area & Line */}
            {totalMemArea && (
              <path d={totalMemArea} fill={`url(#${memGradId})`} />
            )}
            {totalMemLine && (
              <path
                d={totalMemLine}
                fill="none"
                stroke="var(--sand, #cbbb87)"
                strokeWidth="1.5"
                strokeLinecap="round"
                strokeLinejoin="round"
              />
            )}
            {/* Djinn Memory Share line */}
            {djinnMemLine && (
              <path
                d={djinnMemLine}
                fill="none"
                stroke="var(--n-88, #888888)"
                strokeWidth="1.2"
                strokeDasharray="2 2"
              />
            )}
            {lastTotalMem && (
              <circle
                cx={lastTotalMem.x}
                cy={lastTotalMem.y}
                r="3"
                fill="var(--sand, #cbbb87)"
                stroke="var(--n-14)"
                strokeWidth="1.5"
              />
            )}
          </svg>
        </div>
      </div>

      {/* Stacked Gates held across the machine */}
      <StackedGatesChart gates={gates} tMin={tMin} tMax={tMax} />
    </div>
  );
});

// Commands & Gates Costs Table
type SortColumn =
  | "project"
  | "command"
  | "runs"
  | "heldP50"
  | "heldP95"
  | "heldLast"
  | "waitP50"
  | "waitP95"
  | "waitLast"
  | "cpu"
  | "peakMem"
  | "exit";

const CommandsTable = memo(function CommandsTable({
  commands,
}: {
  commands: CommandCost[];
}) {
  const [sortCol, setSortCol] = useState<SortColumn>("heldP50");
  const [sortAsc, setSortAsc] = useState<boolean>(false);

  const handleHeaderClick = useCallback(
    (col: SortColumn) => {
      if (sortCol === col) {
        setSortAsc((prev) => !prev);
      } else {
        setSortCol(col);
        setSortAsc(col === "project" || col === "command");
      }
    },
    [sortCol],
  );

  const sorted = useMemo(() => {
    return [...commands].sort((a, b) => {
      let diff = 0;
      switch (sortCol) {
        case "project": {
          const pA = a.projectName || a.projectId || "";
          const pB = b.projectName || b.projectId || "";
          diff = pA.localeCompare(pB);
          break;
        }
        case "command":
          diff = a.command.localeCompare(b.command);
          break;
        case "runs":
          diff = a.runs - b.runs;
          break;
        case "heldP50":
          diff = a.p50HeldSeconds - b.p50HeldSeconds;
          break;
        case "heldP95":
          diff = a.p95HeldSeconds - b.p95HeldSeconds;
          break;
        case "heldLast":
          diff = a.lastHeldSeconds - b.lastHeldSeconds;
          break;
        case "waitP50":
          diff = a.p50WaitSeconds - b.p50WaitSeconds;
          break;
        case "waitP95":
          diff = a.p95WaitSeconds - b.p95WaitSeconds;
          break;
        case "waitLast":
          diff = a.lastWaitSeconds - b.lastWaitSeconds;
          break;
        case "cpu":
          diff = a.cpuSeconds - b.cpuSeconds;
          break;
        case "peakMem":
          diff = Number(a.peakMemoryBytes - b.peakMemoryBytes);
          break;
        case "exit":
          diff = a.lastExitCode - b.lastExitCode;
          break;
      }
      return sortAsc ? diff : -diff;
    });
  }, [commands, sortCol, sortAsc]);

  if (commands.length === 0) return null;

  const renderSortIndicator = (col: SortColumn) => {
    if (sortCol !== col) return null;
    return <span className="sort-arrow">{sortAsc ? " ▴" : " ▾"}</span>;
  };

  return (
    <div className="consumption-table-wrap">
      <div className="consumption-table-heading">
        <h4>{t("consumption.commands_title")}</h4>
      </div>
      <div className="table-scroller">
        <table className="consumption-table">
          <thead>
            <tr>
              <th onClick={() => handleHeaderClick("project")}>
                {t("consumption.col_project")}
                {renderSortIndicator("project")}
              </th>
              <th onClick={() => handleHeaderClick("command")}>
                {t("consumption.col_command")}
                {renderSortIndicator("command")}
              </th>
              <th className="num-col" onClick={() => handleHeaderClick("runs")}>
                {t("consumption.col_runs")}
                {renderSortIndicator("runs")}
              </th>
              <th
                className="num-col"
                onClick={() => handleHeaderClick("heldP50")}
              >
                {t("consumption.col_held_p50")}
                {renderSortIndicator("heldP50")}
              </th>
              <th
                className="num-col"
                onClick={() => handleHeaderClick("heldP95")}
              >
                {t("consumption.col_held_p95")}
                {renderSortIndicator("heldP95")}
              </th>
              <th
                className="num-col"
                onClick={() => handleHeaderClick("heldLast")}
              >
                {t("consumption.col_held_last")}
                {renderSortIndicator("heldLast")}
              </th>
              <th
                className="num-col"
                onClick={() => handleHeaderClick("waitP50")}
              >
                {t("consumption.col_wait_p50")}
                {renderSortIndicator("waitP50")}
              </th>
              <th
                className="num-col"
                onClick={() => handleHeaderClick("waitP95")}
              >
                {t("consumption.col_wait_p95")}
                {renderSortIndicator("waitP95")}
              </th>
              <th
                className="num-col"
                onClick={() => handleHeaderClick("waitLast")}
              >
                {t("consumption.col_wait_last")}
                {renderSortIndicator("waitLast")}
              </th>
              <th className="num-col" onClick={() => handleHeaderClick("cpu")}>
                {t("consumption.col_cpu")}
                {renderSortIndicator("cpu")}
              </th>
              <th
                className="num-col"
                onClick={() => handleHeaderClick("peakMem")}
              >
                {t("consumption.col_peak_mem")}
                {renderSortIndicator("peakMem")}
              </th>
              <th className="num-col" onClick={() => handleHeaderClick("exit")}>
                {t("consumption.col_exit")}
                {renderSortIndicator("exit")}
              </th>
              <th className="sparkline-col">
                {t("consumption.col_sparkline")}
              </th>
            </tr>
          </thead>
          <tbody>
            {sorted.map((c, i) => {
              const proj = c.projectName || c.projectId || "—";
              return (
                <tr key={`${proj}-${c.command}-${i}`}>
                  <td className="col-proj" title={proj}>
                    {proj}
                  </td>
                  <td className="col-cmd" title={c.command}>
                    <code>{c.command}</code>
                  </td>
                  <td className="num-col">{c.runs}</td>
                  <td className="num-col">{formatSec(c.p50HeldSeconds)}</td>
                  <td className="num-col">{formatSec(c.p95HeldSeconds)}</td>
                  <td className="num-col">{formatSec(c.lastHeldSeconds)}</td>
                  <td className="num-col">{formatSec(c.p50WaitSeconds)}</td>
                  <td className="num-col">{formatSec(c.p95WaitSeconds)}</td>
                  <td className="num-col">{formatSec(c.lastWaitSeconds)}</td>
                  <td className="num-col">{formatSec(c.cpuSeconds)}</td>
                  <td className="num-col">{formatBytes(c.peakMemoryBytes)}</td>
                  <td className="num-col">
                    <span
                      className={
                        c.lastExitCode !== 0 ? "exit-code-err" : "exit-code-ok"
                      }
                    >
                      {c.lastExitCode}
                    </span>
                  </td>
                  <td className="sparkline-col">
                    <Sparkline values={c.recentHeldSeconds} />
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </div>
  );
});

// Main ConsumptionPanel Component
export function ConsumptionPanel({
  initialMinutes = 30,
  initialFolded = false,
  initialData = null,
  initialCommands = [],
}: {
  initialMinutes?: number;
  initialFolded?: boolean;
  initialData?: MachineServiceSeriesResponse | null;
  initialCommands?: CommandCost[];
} = {}) {
  const djinn = useDjinn();
  const [minutes, setMinutes] = useState<number>(initialMinutes);
  const [folded, setFolded] = useState<boolean>(initialFolded);
  const [data, setData] = useState<MachineServiceSeriesResponse | null>(
    initialData,
  );
  const [commands, setCommands] = useState<CommandCost[]>(initialCommands);

  // Polling effect: only poll when djinn is available and panel is visible (!folded)
  useEffect(() => {
    if (!djinn || folded) return;

    let active = true;
    const fetchSeries = async () => {
      try {
        const [seriesRes, cmdRes] = await Promise.all([
          djinn.clients.machine.series({ minutes }).catch(() => null),
          djinn.clients.commands.list({}).catch(() => null),
        ]);
        if (!active) return;
        if (seriesRes) setData(seriesRes);
        if (cmdRes?.costs) setCommands(cmdRes.costs);
      } catch {
        // Silently keep current data
      }
    };

    // A window at rest (stillness.ts) draws no new graph: it reads them again as it wakes.
    const poll = () => {
      if (!still()) void fetchSeries();
    };
    void fetchSeries();
    const interval = setInterval(poll, 5000);
    const stop = onStillness(poll);

    return () => {
      active = false;
      clearInterval(interval);
      stop();
    };
  }, [djinn, folded, minutes]);

  // Compute time bounds [tMin, tMax]
  const { tMin, tMax } = useMemo(() => {
    const allTimes: number[] = [];
    if (data?.machine?.points) {
      for (const p of data.machine.points) {
        if (p.time) allTimes.push(timestampDate(p.time).getTime());
      }
    }
    if (data?.workers) {
      for (const w of data.workers) {
        for (const p of w.points) {
          if (p.time) allTimes.push(timestampDate(p.time).getTime());
        }
        for (const g of w.gates) {
          if (g.grantTime) allTimes.push(timestampDate(g.grantTime).getTime());
          if (g.releaseTime)
            allTimes.push(timestampDate(g.releaseTime).getTime());
        }
      }
    }
    if (data?.gates) {
      for (const g of data.gates) {
        if (g.grantTime) allTimes.push(timestampDate(g.grantTime).getTime());
        if (g.releaseTime)
          allTimes.push(timestampDate(g.releaseTime).getTime());
      }
    }

    const maxT = allTimes.length > 0 ? Math.max(...allTimes) : Date.now();
    // If fake clock was used (e.g. in tests) or time is in future/past:
    const now =
      Math.abs(Date.now() - maxT) > 24 * 3600 * 1000
        ? maxT
        : Math.max(Date.now(), maxT);
    const minT = now - minutes * 60 * 1000;
    return { tMin: minT, tMax: now };
  }, [data, minutes]);

  const workers = data?.workers ?? [];
  const workerMeasure = data?.workerMeasure;

  return (
    <section className="consumption-panel" aria-label={t("consumption.title")}>
      <header className="consumption-header">
        <div className="consumption-title-wrap">
          <button
            type="button"
            className="consumption-fold-btn"
            onClick={() => setFolded((f) => !f)}
            title={folded ? t("consumption.unfold") : t("consumption.fold")}
            aria-expanded={!folded}
          >
            {folded ? <ChevronRight size={15} /> : <ChevronDown size={15} />}
            <span className="consumption-panel-heading">
              {t("consumption.title")}
            </span>
          </button>
        </div>

        {!folded && (
          <div className="consumption-controls">
            <div className="consumption-range-selector" role="group">
              <button
                type="button"
                className={`range-pill ${minutes === 10 ? "active" : ""}`}
                onClick={() => setMinutes(10)}
              >
                {t("consumption.range_10m")}
              </button>
              <button
                type="button"
                className={`range-pill ${minutes === 30 ? "active" : ""}`}
                onClick={() => setMinutes(30)}
              >
                {t("consumption.range_30m")}
              </button>
              <button
                type="button"
                className={`range-pill ${minutes === 120 ? "active" : ""}`}
                onClick={() => setMinutes(120)}
              >
                {t("consumption.range_120m")}
              </button>
            </div>
          </div>
        )}
      </header>

      {!folded && (
        <div className="consumption-body">
          {/* Unmeasured notice if workers are not measured (e.g. Windows) */}
          {workerMeasure && (
            <div className="consumption-notice">
              <p className="muted-text">
                {t("resources.not_measured")}
                {workerMeasure !== "windows" ? ` (${workerMeasure})` : ""}
              </p>
            </div>
          )}

          {/* Machine Total Band */}
          {data?.machine && (
            <MachineBand
              series={data.machine}
              gates={data.gates}
              tMin={tMin}
              tMax={tMax}
            />
          )}

          {/* Running Workers Section */}
          <div className="consumption-workers-section">
            {workers.length === 0 ? (
              <div className="consumption-no-workers">
                <p className="muted-text">{t("consumption.no_workers")}</p>
              </div>
            ) : (
              <div className="consumption-workers-list">
                {workers.map((w) => (
                  <WorkerRow
                    key={w.taskId || w.code}
                    worker={w}
                    machineCores={data?.machine?.cores ?? 1}
                    tMin={tMin}
                    tMax={tMax}
                  />
                ))}
              </div>
            )}
          </div>

          {/* Commands Cost & Gates Table */}
          <CommandsTable commands={commands} />
        </div>
      )}
    </section>
  );
}

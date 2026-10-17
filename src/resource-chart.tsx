// A line chart of the resources panel, drawn in the window's own SVG: CPU in % or memory over time, with a few
// labelled ticks on both axes, labelled reference lines (the machine's memory, the load limit), the gates a worker
// held as bands behind the line, and the value under the pointer: a crosshair snaps to the nearest sample and says
// its time and value. The arrow keys walk the samples, Home and End jump to the ends, Escape lets go. New samples
// redraw the chart alone: the panel appends them (src/data/series.ts), and the point shown stays the one chosen.
import {
  type KeyboardEvent,
  type PointerEvent,
  memo,
  useEffect,
  useId,
  useRef,
  useState,
} from "react";

import type { GateInterval } from "../gen/ts/machine/v1/machine_pb";
import { timeOf } from "./data/series";
import { language, t } from "./i18n";
import { useTextScale } from "./text-size";
import { memory } from "./usage";

export interface ChartPoint {
  t: number;
  v: number;
}

// ChartLine is a reference line across the chart, labelled at its right end.
export interface ChartLine {
  value: number;
  label: string;
}

export type ChartUnit = "cpu" | "memory";

// ChartExtra is a series drawn in neutral ink beside the main one, dashed or dotted, and named in the readout.
export interface ChartExtra {
  label: string;
  points: readonly ChartPoint[];
  style: "dash" | "dot";
}

const PAD = { top: 12, right: 14, bottom: 24, left: 56 };
// The width drawn before the chart is measured, and in a test.
const DEFAULT_WIDTH = 480;
const MIB = 1024 * 1024;

const percent = new Intl.NumberFormat(language, {
  style: "percent",
  maximumFractionDigits: 1,
});
const percentShort = new Intl.NumberFormat(language, {
  style: "percent",
  maximumFractionDigits: 0,
});

// formatValue says a value of the chart: CPU in %, memory in GB with its share of the machine's when known.
export function formatValue(
  unit: ChartUnit,
  v: number,
  memoryTotal = 0,
): string {
  if (unit === "cpu") return percent.format(v / 100);
  const bytes = memory(BigInt(Math.max(0, Math.round(v))));
  return memoryTotal > 0
    ? t("chart.of_max", {
        value: bytes,
        share: percent.format(v / memoryTotal),
      })
    : bytes;
}

function tickLabel(unit: ChartUnit, v: number): string {
  if (unit === "cpu") return percentShort.format(v / 100);
  return v === 0 ? "0" : memory(BigInt(Math.round(v)));
}

// scale is the top of the y axis and its ticks: 0, 50 and 100% for the CPU of a machine or a worker on one core,
// halves of a rounder top beyond; memory in steps of 64 MB to 128 GB, four at most.
export function scale(
  unit: ChartUnit,
  top: number,
): { yMax: number; ticks: number[] } {
  if (unit === "cpu") {
    const yMax = Math.max(100, Math.ceil(top / 50) * 50);
    return { yMax, ticks: [0, yMax / 2, yMax] };
  }
  const want = Math.max(top, 64 * MIB);
  let step = 64 * MIB;
  while (Math.ceil(want / step) > 4) step *= 2;
  const n = Math.ceil(want / step);
  return {
    yMax: n * step,
    ticks: Array.from({ length: n + 1 }, (_, i) => i * step),
  };
}

const MINUTE = 60_000;
const DAY = 24 * 60 * MINUTE;

// spansDays tells whether a range covers more than one calendar day: its times then say their date.
export function spansDays(tMin: number, tMax: number): boolean {
  return new Date(tMin).toDateString() !== new Date(tMax).toDateString();
}

// timeTicks are the times labelled under the chart, five at most: whole minutes, hours or days of the local clock.
export function timeTicks(tMin: number, tMax: number): number[] {
  const span = Math.max(1, tMax - tMin);
  const step =
    [1, 2, 5, 10, 15, 30, 60, 120, 180, 360, 720, 1440, 2880].find(
      (m) => span / (m * MINUTE) <= 5,
    ) ?? 2880;
  const at = new Date(tMin);
  if (step < 60) {
    at.setSeconds(0, 0);
    at.setMinutes(Math.floor(at.getMinutes() / step) * step);
  } else if (step < 1440) {
    at.setMinutes(0, 0, 0);
    at.setHours(Math.floor(at.getHours() / (step / 60)) * (step / 60));
  } else at.setHours(0, 0, 0, 0);
  const next = () =>
    step < 1440
      ? at.setTime(at.getTime() + step * MINUTE)
      : at.setDate(at.getDate() + step / 1440);
  while (at.getTime() < tMin) next();
  const out: number[] = [];
  for (; at.getTime() <= tMax; next()) out.push(at.getTime());
  return out;
}

const dayFormat = { day: "numeric", month: "short" } as const;

// tickLabels label the time ticks: the time, its date too on the first tick of each day when the range spans days,
// the date alone for ticks a day apart.
export function tickLabels(
  ticks: readonly number[],
  tMin: number,
  tMax: number,
): string[] {
  const days = spansDays(tMin, tMax);
  const daily = ticks.length > 1 && ticks[1] - ticks[0] >= DAY;
  return ticks.map((at, i) => {
    const d = new Date(at);
    if (daily) return d.toLocaleDateString(language, dayFormat);
    const time = clock(at);
    const newDay =
      days &&
      (i === 0 || new Date(ticks[i - 1]).toDateString() !== d.toDateString());
    return newDay
      ? `${d.toLocaleDateString(language, dayFormat)} ${time}`
      : time;
  });
}

function clock(ms: number, seconds = false, date = false): string {
  return new Date(ms).toLocaleString(language, {
    ...(date ? dayFormat : {}),
    hour: "2-digit",
    minute: "2-digit",
    ...(seconds ? { second: "2-digit" } : {}),
  });
}

// nearest is the index of the point closest in time to at; -1 when there is none.
export function nearest(points: readonly ChartPoint[], at: number): number {
  if (points.length === 0) return -1;
  let lo = 0;
  let hi = points.length - 1;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (points[mid].t < at) lo = mid + 1;
    else hi = mid;
  }
  if (lo > 0 && at - points[lo - 1].t <= points[lo].t - at) return lo - 1;
  return lo;
}

const clamp01 = (v: number) => Math.max(0, Math.min(1, v));

export const ResourceChart = memo(function ResourceChart({
  title,
  unit,
  summary,
  points,
  extras = [],
  tMin,
  tMax,
  top,
  memoryTotal = 0,
  lines = [],
  gates = [],
  height: tall = 190,
}: {
  title: string;
  unit: ChartUnit;
  // What the chart says at a glance, beside its title: the last value, the peak.
  summary: string;
  points: readonly ChartPoint[];
  // Series drawn beside the main one: Djinn with what it started, Djinn itself.
  extras?: readonly ChartExtra[];
  tMin: number;
  tMax: number;
  // The highest value the y axis must hold.
  top: number;
  // The machine's memory: memory values say their share of it.
  memoryTotal?: number;
  lines?: readonly ChartLine[];
  gates?: readonly GateInterval[];
  // In pixels at the default text size: the chart grows and shrinks with it (text-size.ts).
  height?: number;
}) {
  const height = Math.round(tall * useTextScale());
  const plot = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(DEFAULT_WIDTH);
  // The time of the point shown, kept as new samples come; null when none is.
  const [cursor, setCursor] = useState<number | null>(null);
  const [focused, setFocused] = useState(false);
  const id = useId();
  useEffect(() => {
    const element = plot.current;
    if (!element || typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(([entry]) => {
      const w = Math.round(entry.contentRect.width);
      if (w > 0) setWidth(w);
    });
    observer.observe(element);
    return () => observer.disconnect();
  }, []);

  const { yMax, ticks } = scale(unit, top);
  const plotW = Math.max(1, width - PAD.left - PAD.right);
  const plotH = Math.max(1, height - PAD.top - PAD.bottom);
  const span = Math.max(1, tMax - tMin);
  const x = (at: number) => PAD.left + clamp01((at - tMin) / span) * plotW;
  const y = (v: number) => PAD.top + (1 - clamp01(v / yMax)) * plotH;
  const base = PAD.top + plotH;
  const path = (pts: readonly ChartPoint[]) =>
    pts
      .map(
        (p, i) => `${i ? "L" : "M"}${x(p.t).toFixed(1)} ${y(p.v).toFixed(1)}`,
      )
      .join(" ");
  const line = points.length > 1 ? path(points) : "";
  const area = line
    ? `M${x(points[0].t).toFixed(1)} ${base} ${line.replace(/^M/, "L")} L${x(points[points.length - 1].t).toFixed(1)} ${base} Z`
    : "";
  const last = points[points.length - 1];
  const index = cursor === null ? -1 : nearest(points, cursor);
  const at = index >= 0 ? points[index] : undefined;
  const readout = at
    ? [
        clock(at.t, true, spansDays(tMin, tMax)),
        `${title} ${formatValue(unit, at.v, memoryTotal)}`,
        ...extras
          .filter((e) => e.points.length > 0)
          .map(
            (e) =>
              `${e.label} ${formatValue(unit, e.points[nearest(e.points, at.t)].v, memoryTotal)}`,
          ),
      ]
    : [];

  const toTime = (clientX: number, left: number) =>
    tMin + ((clientX - left - PAD.left) / plotW) * span;
  const onPointerMove = (event: PointerEvent<HTMLDivElement>) => {
    if (points.length === 0) return;
    const box = event.currentTarget.getBoundingClientRect();
    const i = nearest(points, toTime(event.clientX, box.left));
    setCursor(points[i].t);
  };
  const onPointerLeave = () => {
    if (!focused) setCursor(null);
  };
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (points.length === 0) return;
    const from = index >= 0 ? index : points.length - 1;
    const to = {
      ArrowLeft: from - 1,
      ArrowRight: from + 1,
      Home: 0,
      End: points.length - 1,
    }[event.key];
    if (event.key === "Escape") {
      setCursor(null);
      return;
    }
    if (to === undefined) return;
    event.preventDefault();
    setCursor(points[Math.max(0, Math.min(points.length - 1, to))].t);
  };

  const times = timeTicks(tMin, tMax);
  const timeLabels = tickLabels(times, tMin, tMax);
  // A label that would touch the one before it, or leave the chart, is left out: its tick mark stays.
  let room = 0;
  const shownTimes = times.map((at, i) => {
    const half = (timeLabels[i].length * 7.5) / 2;
    const fits = x(at) - half >= room && x(at) + half <= width;
    if (fits) room = x(at) + half + 8;
    return fits;
  });
  const hatch = `${id}-hatch`;
  const cursorX = at ? x(at.t) : 0;
  return (
    <figure className={`resource-chart ${unit}`}>
      <figcaption className="resource-chart-head">
        <span className="resource-chart-title">
          <span className="resource-chart-key" aria-hidden="true" />
          {title}
        </span>
        <span className="resource-chart-now">{summary}</span>
      </figcaption>
      <div
        ref={plot}
        className="resource-chart-plot"
        tabIndex={0}
        role="group"
        aria-roledescription={t("chart.role")}
        aria-label={`${title}: ${summary}`}
        aria-describedby={`${id}-help`}
        onPointerMove={onPointerMove}
        onPointerLeave={onPointerLeave}
        onKeyDown={onKeyDown}
        onFocus={() => {
          setFocused(true);
          if (cursor === null && last) setCursor(last.t);
        }}
        onBlur={() => {
          setFocused(false);
          setCursor(null);
        }}
      >
        <svg
          className="resource-chart-svg"
          width={width}
          height={height}
          viewBox={`0 0 ${width} ${height}`}
          aria-hidden="true"
        >
          <defs>
            <pattern
              id={hatch}
              width="6"
              height="6"
              patternTransform="rotate(45)"
              patternUnits="userSpaceOnUse"
            >
              <line
                className="resource-chart-hatch"
                x1="0"
                y1="0"
                x2="0"
                y2="6"
              />
            </pattern>
          </defs>
          {ticks.map((v) => (
            <g key={v}>
              <line
                className="resource-chart-grid"
                x1={PAD.left}
                x2={PAD.left + plotW}
                y1={y(v)}
                y2={y(v)}
              />
              <text
                className="resource-chart-tick"
                x={PAD.left - 6}
                y={y(v) + 4}
                textAnchor="end"
              >
                {tickLabel(unit, v)}
              </text>
            </g>
          ))}
          {times.map((at, i) => (
            <g key={at}>
              <line
                className="resource-chart-axis"
                x1={x(at)}
                x2={x(at)}
                y1={base}
                y2={base + 4}
              />
              {shownTimes[i] && (
                <text
                  className="resource-chart-tick"
                  x={x(at)}
                  y={height - 6}
                  textAnchor="middle"
                >
                  {timeLabels[i]}
                </text>
              )}
            </g>
          ))}
          <line
            className="resource-chart-axis"
            x1={PAD.left}
            x2={PAD.left + plotW}
            y1={base}
            y2={base}
          />
          {gates.map((g, i) => {
            const grant = timeOf(g.grantTime);
            const release = g.releaseTime ? timeOf(g.releaseTime) : tMax;
            const asked = g.askedTime ? timeOf(g.askedTime) : grant;
            if (!grant || release < tMin || asked > tMax) return null;
            const held = Math.max(2, x(release) - x(grant));
            return (
              <g key={i}>
                {x(grant) - x(asked) > 1 && (
                  <rect
                    x={x(asked)}
                    y={PAD.top}
                    width={x(grant) - x(asked)}
                    height={plotH}
                    fill={`url(#${hatch})`}
                  />
                )}
                <rect
                  className="resource-chart-gate"
                  x={x(grant)}
                  y={PAD.top}
                  width={held}
                  height={plotH}
                />
                {held >= 40 && (
                  <text
                    className="resource-chart-gate-label"
                    x={x(grant) + 4}
                    y={PAD.top + 12}
                  >
                    {t("chart.gate", { gate: g.gate })}
                  </text>
                )}
              </g>
            );
          })}
          {lines.map((l) => (
            <g key={l.label}>
              <line
                className="resource-chart-ref"
                x1={PAD.left}
                x2={PAD.left + plotW}
                y1={y(l.value)}
                y2={y(l.value)}
              />
              <text
                className="resource-chart-ref-label"
                x={PAD.left + plotW - 4}
                y={Math.max(PAD.top + 10, y(l.value) - 4)}
                textAnchor="end"
              >
                {l.label}
              </text>
            </g>
          ))}
          {area && <path className="resource-chart-area" d={area} />}
          {line && <path className="resource-chart-line" d={line} />}
          {extras.map(
            (e) =>
              e.points.length > 1 && (
                <path
                  key={e.label}
                  className={`resource-chart-extra ${e.style}`}
                  d={path(e.points)}
                />
              ),
          )}
          {last && !at && (
            <circle
              className="resource-chart-dot"
              cx={x(last.t)}
              cy={y(last.v)}
              r="4"
            />
          )}
          {at && (
            <g className="resource-chart-cursor">
              <line x1={cursorX} x2={cursorX} y1={PAD.top} y2={base} />
              <circle
                className="resource-chart-dot"
                cx={cursorX}
                cy={y(at.v)}
                r="4.5"
              />
            </g>
          )}
        </svg>
        {at && (
          <div
            className="resource-chart-readout"
            aria-hidden="true"
            style={
              cursorX > width / 2
                ? { right: width - cursorX + 8 }
                : { left: cursorX + 8 }
            }
          >
            <span>{readout[0]}</span>
            <strong>{readout[1]}</strong>
            {readout.slice(2).map((line) => (
              <span key={line}>{line}</span>
            ))}
          </div>
        )}
        <span id={`${id}-help`} className="resource-chart-sr">
          {t("chart.help")}
        </span>
        <span className="resource-chart-sr" aria-live="polite">
          {readout.join(", ")}
        </span>
      </div>
    </figure>
  );
});

// The ECharts options of the window's charts, built from a frame by code, never written by hand: what Will's lint
// checked on a chart (dolmen dataviz-v2, L53, L59) holds here by construction, and tests/charts.test.mjs checks it.
// - Zero at the base of a value axis, unless the scale is logarithmic, which the axis then names.
// - Six series at most: past six, the smallest fold into "Others", in a neutral colour.
// - An absent value stays null, never 0: no bar, a gap in a line; "Others" is null where all it folds are.
// - Every value says its unit (a duration as a counter, a cost in dollars) on the axis and in the tooltip.
// - A column's type comes from the frame's schema, never from its first row.
// - No animation: the window at rest draws no frame (html[data-still], src/stillness.ts).
import type {
  BarSeriesOption,
  LineSeriesOption,
  ScatterSeriesOption,
} from "echarts/charts";
import type {
  GridComponentOption,
  LegendComponentOption,
  TooltipComponentOption,
} from "echarts/components";
import type { ComposeOption } from "echarts/core";

import { dayText } from "../data/format";
import { language, t } from "../i18n";
import {
  type Cell,
  ChartError,
  type Column,
  type Frame,
  column,
  measure,
  numberOf,
  valueText,
} from "./frame";
import type { ChartTheme } from "./theme";

export type ChartOption = ComposeOption<
  | BarSeriesOption
  | LineSeriesOption
  | ScatterSeriesOption
  | GridComponentOption
  | LegendComponentOption
  | TooltipComponentOption
>;

// The most series a chart shows, "Others" included.
export const MAX_SERIES = 6;

// The buckets of a time axis, as the query groups them (Aggregation.Granularity, local time).
export type Grain = "day" | "week" | "month";

// BarSpec draws y by x, split into series by a column if any: side by side, stacked, or stacked to 100 %.
export interface BarSpec {
  kind: "bar";
  x: string;
  y: string;
  series?: string;
  // The series' values in their fixed order: a series keeps its colour whatever the others do. The values not
  // listed follow, largest first.
  order?: readonly string[];
  // A time x: the buckets with no row are drawn empty, the axis keeps its days.
  grain?: Grain;
  stack?: "stack" | "share";
  horizontal?: boolean;
  log?: boolean;
}

// LineSpec draws y over x, one line per series.
export interface LineSpec {
  kind: "line";
  x: string;
  y: string;
  series?: string;
  order?: readonly string[];
  grain?: Grain;
}

// SpreadSpec draws, for each category, its median as a bar and its p90 as a mark: the typical value and the slow
// tail, never a sum nor a mean (L53).
export interface SpreadSpec {
  kind: "spread";
  category: string;
  median: string;
  p90: string;
  log?: boolean;
}

export type ChartSpec = BarSpec | LineSpec | SpreadSpec;

// Look is what a chart takes from the page: the theme's colours, the text size's scale and the font.
export interface Look {
  theme: ChartTheme;
  scale: number;
  font?: string;
}

// chartOption builds the option of a spec over a frame; it throws a ChartError when the frame lacks what it asks.
export function chartOption(
  frame: Frame,
  spec: ChartSpec,
  look: Look,
): ChartOption {
  switch (spec.kind) {
    case "bar":
      return barOption(frame, spec, look);
    case "line":
      return lineOption(frame, spec, look);
    case "spread":
      return spreadOption(frame, spec, look);
  }
}

interface Series {
  name: string;
  values: (number | null)[];
  other?: boolean;
}

interface Pivot {
  labels: string[];
  series: Series[];
}

// pivot lays the rows out by x and by series. A time x is in time order, its gaps filled when the grain is known;
// another x keeps the rows' order, the query's sort.
export function pivot(
  frame: Frame,
  spec: {
    x: string;
    y: string;
    series?: string;
    order?: readonly string[];
    grain?: Grain;
  },
): Pivot {
  const x = column(frame, spec.x);
  measure(frame, spec.y);
  const split = spec.series ? column(frame, spec.series) : undefined;
  const time = x.type === "time";
  const keys: (string | number)[] = [];
  const seen = new Set<string | number>();
  const cells = new Map<string, Map<string | number, number | null>>();
  for (const row of frame.rows) {
    const key = xKey(row[spec.x], time);
    if (key === undefined) continue;
    if (!seen.has(key)) {
      seen.add(key);
      keys.push(key);
    }
    const name = split ? nameOf(row[split.ref]) : column(frame, spec.y).label;
    let byX = cells.get(name);
    if (!byX) cells.set(name, (byX = new Map()));
    byX.set(key, add(byX.get(key) ?? null, numberOf(row[spec.y])));
  }
  if (time) {
    (keys as number[]).sort((a, b) => a - b);
    if (spec.grain)
      keys.splice(0, keys.length, ...fill(keys as number[], spec.grain));
  }
  const all = [...cells].map(([name, byX]) => ({
    name,
    values: keys.map((k) => byX.get(k) ?? null),
  }));
  return {
    labels: keys.map((k) =>
      time ? timeText(new Date(k), spec.grain) : String(k),
    ),
    series: fold(all, spec.order ?? []),
  };
}

function xKey(cell: Cell | undefined, time: boolean) {
  if (time) return cell instanceof Date ? cell.getTime() : undefined;
  return nameOf(cell);
}

function nameOf(cell: Cell | undefined): string {
  if (cell === null || cell === undefined) return "–";
  if (cell instanceof Date) return dayText(cell);
  return String(cell);
}

// add sums two values; null with null stays null.
function add(a: number | null, b: number | null): number | null {
  if (a === null) return b;
  if (b === null) return a;
  return a + b;
}

// fill gives every bucket from the first to the last, a thousand at most.
function fill(keys: number[], grain: Grain): number[] {
  if (!keys.length) return keys;
  const out: number[] = [];
  const last = keys[keys.length - 1];
  const d = new Date(keys[0]);
  while (d.getTime() <= last && out.length < 1000) {
    out.push(d.getTime());
    if (grain === "day") d.setDate(d.getDate() + 1);
    else if (grain === "week") d.setDate(d.getDate() + 7);
    else d.setMonth(d.getMonth() + 1);
  }
  // A bucket off the steps (a change of hour, a query in another grain) keeps its place.
  return [...new Set([...out, ...keys])].sort((a, b) => a - b);
}

function timeText(d: Date, grain?: Grain): string {
  if (grain === "month")
    return d.toLocaleDateString(language, { month: "short", year: "2-digit" });
  return dayText(d);
}

// fold orders the series, the listed ones first, then the largest; past MAX_SERIES, the smallest fold into one.
export function fold(all: Series[], order: readonly string[]): Series[] {
  const total = (s: Series) =>
    s.values.reduce<number>((sum, v) => sum + Math.abs(v ?? 0), 0);
  const rank = (s: Series) => {
    const i = order.indexOf(s.name);
    return i < 0 ? order.length : i;
  };
  const sorted = [...all].sort(
    (a, b) => rank(a) - rank(b) || total(b) - total(a),
  );
  if (sorted.length <= MAX_SERIES) return sorted;
  const kept = sorted.slice(0, MAX_SERIES - 1);
  const rest = sorted.slice(MAX_SERIES - 1);
  const values = rest[0].values.map((_, i) =>
    rest.reduce<number | null>((sum, s) => add(sum, s.values[i]), null),
  );
  return [...kept, { name: t("charts.others"), values, other: true }];
}

// colours gives each series its hue in order, "Others" its neutral one.
function colours(series: Series[], theme: ChartTheme): string[] {
  let next = 0;
  return series.map((s) =>
    s.other ? theme.other : theme.palette[next++ % theme.palette.length],
  );
}

// base is what every chart shares: no animation, the page's font and colours, a tooltip that says units.
function base(
  look: Look,
  unit: Column,
  legend: boolean,
  named = false,
): ChartOption {
  const { theme, scale } = look;
  const size = Math.round(12 * scale);
  return {
    animation: false,
    backgroundColor: "transparent",
    textStyle: { fontFamily: look.font, fontSize: size, color: theme.ink },
    grid: {
      left: 8 * scale,
      right: 16 * scale,
      // The legend's line, then a log axis's name above the plot.
      top: ((legend ? 36 : 12) + (named ? 20 : 0)) * scale,
      bottom: 8 * scale,
    },
    legend: legend
      ? {
          top: 0,
          left: 0,
          icon: "roundRect",
          itemWidth: size,
          itemHeight: size,
          textStyle: { color: theme.inkSoft, fontSize: size },
        }
      : { show: false },
    tooltip: {
      trigger: "axis",
      axisPointer: { type: "shadow" },
      transitionDuration: 0,
      confine: true,
      backgroundColor: theme.surface,
      borderColor: theme.grid,
      textStyle: { color: theme.ink, fontSize: size },
      // A mark placed on a category axis by its pair [value, index] says its value.
      valueFormatter: (v) => {
        const n = Array.isArray(v) ? v[0] : v;
        return valueText(unit, typeof n === "number" ? n : null);
      },
    },
  };
}

// valueAxis is the axis of a measure: from zero, or logarithmic and named so, from the power of ten under its least
// value (a log axis of milliseconds would start at 1 ms); 0 to 100 % for a share. Only a log axis says its name: the
// card's title names the measure, the labels its unit, and a name at the top would sit on the legend.
function valueAxis(
  col: Column,
  look: Look,
  values: (number | null)[],
  opts: { log?: boolean; share?: boolean },
) {
  const { theme, scale } = look;
  const negative = values.some((v) => v !== null && v < 0);
  const least = Math.min(
    ...values.filter((v): v is number => v !== null && v > 0),
  );
  const floor = Number.isFinite(least)
    ? 10 ** Math.floor(Math.log10(least))
    : undefined;
  return {
    type: opts.log ? ("log" as const) : ("value" as const),
    name: opts.log ? t("charts.log_scale", { label: col.label }) : undefined,
    nameLocation: "end" as const,
    nameTextStyle: { color: theme.inkSoft, fontSize: Math.round(12 * scale) },
    scale: false,
    min: opts.log ? floor : negative ? undefined : 0,
    max: opts.share ? 100 : undefined,
    axisLabel: {
      color: theme.inkSoft,
      formatter: (v: number) => valueText(col, v),
    },
    axisLine: { show: false },
    splitLine: { lineStyle: { color: theme.grid } },
  };
}

function categoryAxis(labels: string[], look: Look, inverse = false) {
  return {
    type: "category" as const,
    data: labels,
    inverse,
    axisTick: { show: false },
    axisLine: { lineStyle: { color: look.theme.grid } },
    axisLabel: { color: look.theme.inkSoft, hideOverlap: true },
  };
}

// shares turns each x's values into its percents of the x's total; null stays null, an x of total 0 is all null.
function shares(series: Series[]): Series[] {
  const totals = (series[0]?.values ?? []).map((_, i) =>
    series.reduce((sum, s) => sum + (s.values[i] ?? 0), 0),
  );
  return series.map((s) => ({
    ...s,
    values: s.values.map((v, i) =>
      v === null || totals[i] === 0 ? null : (v / totals[i]) * 100,
    ),
  }));
}

// logged drops the values a log scale cannot place: 0 and below are absent there, never drawn as 1.
const logged = (series: Series[]): Series[] =>
  series.map((s) => ({
    ...s,
    values: s.values.map((v) => (v !== null && v > 0 ? v : null)),
  }));

function barOption(frame: Frame, spec: BarSpec, look: Look): ChartOption {
  const y = measure(frame, spec.y);
  const { labels, series: raw } = pivot(frame, spec);
  const share = spec.stack === "share";
  const series = share ? shares(raw) : spec.log ? logged(raw) : raw;
  const unit: Column = share
    ? { ref: y.ref, label: y.label, type: "number", unit: "percent" }
    : y;
  const stacked = spec.stack !== undefined;
  const colour = colours(series, look.theme);
  const value = valueAxis(
    unit,
    look,
    series.flatMap((s) => s.values),
    { log: spec.log && !share, share },
  );
  const category = categoryAxis(labels, look, spec.horizontal);
  const end = spec.horizontal ? [0, 4, 4, 0] : [4, 4, 0, 0];
  return {
    ...base(
      look,
      unit,
      series.length > 1,
      Boolean(spec.log && !share && !spec.horizontal),
    ),
    xAxis: spec.horizontal ? value : category,
    yAxis: spec.horizontal ? category : value,
    series: series.map((s, i): BarSeriesOption => ({
      type: "bar",
      name: s.name,
      data: s.values,
      stack: stacked ? "total" : undefined,
      barMaxWidth: 24 * look.scale,
      color: colour[i],
      itemStyle: stacked
        ? { borderColor: look.theme.surface, borderWidth: 1 }
        : { borderRadius: end },
    })),
  };
}

function lineOption(frame: Frame, spec: LineSpec, look: Look): ChartOption {
  const y = measure(frame, spec.y);
  const { labels, series } = pivot(frame, spec);
  const colour = colours(series, look.theme);
  return {
    ...base(look, y, series.length > 1),
    tooltip: {
      ...(base(look, y, false).tooltip as TooltipComponentOption),
      axisPointer: { type: "line", lineStyle: { color: look.theme.inkSoft } },
    },
    xAxis: { ...categoryAxis(labels, look), boundaryGap: false },
    yAxis: valueAxis(
      y,
      look,
      series.flatMap((s) => s.values),
      {},
    ),
    series: series.map((s, i): LineSeriesOption => ({
      type: "line",
      name: s.name,
      data: s.values,
      connectNulls: false,
      showSymbol: false,
      symbolSize: 8 * look.scale,
      color: colour[i],
      lineStyle: { width: 2 },
    })),
  };
}

function spreadOption(frame: Frame, spec: SpreadSpec, look: Look): ChartOption {
  const category = column(frame, spec.category);
  const median = measure(frame, spec.median);
  const p90 = measure(frame, spec.p90);
  if (median.type !== p90.type || median.unit !== p90.unit)
    throw new ChartError(
      `${spec.median} and ${spec.p90} are not in one unit: one axis cannot hold both`,
    );
  const labels = frame.rows.map((r) => nameOf(r[category.ref]));
  let medians = frame.rows.map((r) => numberOf(r[median.ref]));
  let tails = frame.rows.map((r) => numberOf(r[p90.ref]));
  if (spec.log) {
    const kept = (v: number | null) => (v !== null && v > 0 ? v : null);
    medians = medians.map(kept);
    tails = tails.map(kept);
  }
  const size = look.scale;
  return {
    ...base(look, median, true),
    xAxis: valueAxis(median, look, [...medians, ...tails], { log: spec.log }),
    yAxis: categoryAxis(labels, look, true),
    series: [
      {
        type: "bar",
        name: median.label,
        data: medians,
        barMaxWidth: 16 * size,
        color: look.theme.palette[0],
        itemStyle: { borderRadius: [0, 4, 4, 0] },
      },
      {
        type: "scatter",
        name: p90.label,
        // A scatter on a category axis takes its points as [value, category]; an absent p90 draws no mark.
        data: tails.map((v, i) => (v === null ? [null, i] : [v, i])),
        symbol: "rect",
        symbolSize: [3 * size, 18 * size],
        color: look.theme.ink,
      },
    ],
  };
}

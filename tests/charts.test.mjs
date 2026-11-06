// The window's charts (src/charts): the rules of Will's chart lint (dolmen dataviz-v2, L53, L59) as tests on the
// options the builders make, the theme read from the CSS tokens, and a real render by ECharts' SVG renderer.
// Run with `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";

import { bundle, root } from "./bundle.mjs";

const c = await bundle(
  "charts",
  `export { ChartError, column, measure, valueText } from "@/src/charts/frame.ts";
export { MAX_SERIES, chartOption } from "@/src/charts/option.ts";
export { SERIES_TOKENS, chartTheme } from "@/src/charts/theme.ts";
export { loadEcharts } from "@/src/charts/engine.ts";
export { t } from "@/src/i18n.ts";`,
);

const theme = {
  palette: ["#000001", "#000002", "#000003", "#000004", "#000005"],
  other: "#00000f",
  ink: "#eeeeee",
  inkSoft: "#aaaaaa",
  grid: "#333333",
  surface: "#151517",
};
const look = { theme, scale: 1 };

const day = (d) => new Date(2026, 10, d);
const HOUR = 3_600_000;

// Tasks counted by day and kind, as an aggregate of TASKS answers.
const byDay = {
  columns: [
    { ref: "create_time::day", label: "Day", type: "time" },
    { ref: "kind", label: "Kind", type: "enum" },
    { ref: "count", label: "Tasks", type: "number" },
  ],
  rows: [
    { "create_time::day": day(3), kind: "work", count: 4 },
    { "create_time::day": day(1), kind: "work", count: 2 },
    { "create_time::day": day(1), kind: "mock", count: 1 },
    { "create_time::day": day(3), kind: "mock", count: null },
  ],
};

const bar = (spec, frame = byDay) =>
  c.chartOption(
    frame,
    { kind: "bar", x: "create_time::day", y: "count", ...spec },
    look,
  );

const named = (option, name) => option.series.find((s) => s.name === name);

test("a value axis starts at zero, unless it is a log scale, which it names", () => {
  for (const option of [
    bar({}),
    bar({ stack: "stack" }),
    c.chartOption(
      byDay,
      { kind: "line", x: "create_time::day", y: "count" },
      look,
    ),
  ]) {
    assert.equal(option.yAxis.type, "value");
    assert.equal(option.yAxis.min, 0);
    assert.equal(option.yAxis.scale, false);
  }
  const log = bar({ log: true });
  assert.equal(log.yAxis.type, "log");
  // From the power of ten under the least value (the counts are 1 to 4): never 0, nor 1 ms on a duration.
  assert.equal(log.yAxis.min, 1);
  assert.equal(
    bar({}).yAxis.name,
    undefined,
    "a linear axis leaves its name to the card",
  );
  assert.equal(log.yAxis.name, c.t("charts.log_scale", { label: "Tasks" }));
  assert.match(log.yAxis.name, /log/);
  // A horizontal bar lies on its value axis the same way.
  const lying = bar({ horizontal: true });
  assert.equal(lying.xAxis.min, 0);
  assert.equal(lying.yAxis.type, "category");
});

test("a negative value leaves the axis free to go below zero, from zero still", () => {
  const frame = {
    columns: byDay.columns,
    rows: [{ "create_time::day": day(1), kind: "work", count: -3 }],
  };
  const option = bar({}, frame);
  assert.equal(option.yAxis.min, undefined);
  assert.equal(option.yAxis.scale, false);
});

test("past six series, the smallest fold into Others, in its neutral colour", () => {
  const kinds = ["a", "b", "c", "d", "e", "f", "g", "h", "i"];
  const frame = {
    columns: byDay.columns,
    rows: kinds.flatMap((kind, i) => [
      { "create_time::day": day(1), kind, count: 100 - i },
      // Only the smallest has nothing on the 2nd: the rest still sums there.
      { "create_time::day": day(2), kind, count: kind === "i" ? null : 1 },
    ]),
  };
  const option = bar({ series: "kind", stack: "stack" }, frame);
  assert.equal(option.series.length, c.MAX_SERIES);
  assert.deepEqual(
    option.series.map((s) => s.name),
    ["a", "b", "c", "d", "e", c.t("charts.others")],
  );
  const others = option.series.at(-1);
  assert.deepEqual(others.data, [95 + 94 + 93 + 92, 3]);
  assert.equal(others.color, theme.other);
  assert.deepEqual(
    option.series.slice(0, 5).map((s) => s.color),
    theme.palette,
  );
  assert.equal(option.legend.show, undefined, "several series have a legend");
  // Six exactly stay six: nothing folds.
  const six = {
    columns: byDay.columns,
    rows: kinds
      .slice(0, 6)
      .map((kind) => ({ "create_time::day": day(1), kind, count: 1 })),
  };
  assert.ok(
    !bar({ series: "kind" }, six).series.some(
      (s) => s.name === c.t("charts.others"),
    ),
  );
  // One series needs no legend: the card's title names it.
  assert.equal(bar({}).legend.show, false);
});

test("Others is absent where everything it folds is absent", () => {
  const kinds = ["a", "b", "c", "d", "e", "f", "g"];
  const frame = {
    columns: byDay.columns,
    rows: kinds.map((kind, i) => ({
      "create_time::day": day(1),
      kind,
      count: i < 5 ? 10 : null,
    })),
  };
  assert.deepEqual(
    named(bar({ series: "kind" }, frame), c.t("charts.others")).data,
    [null],
  );
});

test("a series keeps its colour when another is absent: colour follows the entity", () => {
  const order = ["work", "mock"];
  const both = bar({ series: "kind", order });
  const onlyMock = bar(
    { series: "kind", order: ["mock"] },
    {
      columns: byDay.columns,
      rows: byDay.rows.filter((r) => r.kind === "mock"),
    },
  );
  assert.equal(named(both, "work").color, theme.palette[0]);
  assert.equal(named(both, "mock").color, theme.palette[1]);
  assert.deepEqual(
    both.series.map((s) => s.name),
    order,
    "listed series come in their order, whatever their size",
  );
  assert.equal(named(onlyMock, "mock").color, theme.palette[0]);
});

test("an absent value stays null, never 0: no bar, a gap in a line, an empty day kept", () => {
  const frame = {
    columns: byDay.columns,
    rows: [
      ...byDay.rows,
      { "create_time::day": day(6), kind: "work", count: 1 },
    ],
  };
  const option = bar({ series: "kind", grain: "day" }, frame);
  // The 1st to the 6th: the days without a row are on the axis, empty.
  assert.equal(option.xAxis.data.length, 6);
  assert.deepEqual(named(option, "work").data, [2, null, 4, null, null, 1]);
  assert.deepEqual(named(option, "mock").data, [
    1,
    null,
    null,
    null,
    null,
    null,
  ]);
  const line = c.chartOption(
    frame,
    {
      kind: "line",
      x: "create_time::day",
      y: "count",
      series: "kind",
      grain: "day",
    },
    look,
  );
  assert.ok(line.series.every((s) => s.connectNulls === false));
  assert.deepEqual(named(line, "mock").data, [1, null, null, null, null, null]);
  // A share of a day with nothing is absent, not 0 %; a log scale cannot place 0.
  const share = bar({ series: "kind", stack: "share", grain: "day" }, frame);
  assert.deepEqual(named(share, "work").data, [
    (2 / 3) * 100,
    null,
    100,
    null,
    null,
    100,
  ]);
  const zero = {
    columns: byDay.columns,
    rows: [{ "create_time::day": day(1), kind: "work", count: 0 }],
  };
  assert.deepEqual(bar({ log: true }, zero).series[0].data, [null]);
});

test("a share stacks to 100 % and says percents", () => {
  const option = bar({ series: "kind", stack: "share" });
  assert.equal(option.yAxis.max, 100);
  assert.ok(option.series.every((s) => s.stack === "total"));
  assert.equal(
    option.tooltip.valueFormatter(50),
    c.valueText({ ref: "", label: "", type: "number", unit: "percent" }, 50),
  );
  assert.match(option.tooltip.valueFormatter(50), /50\s?%/);
});

test("every value says its unit, on the axis and in the tooltip", () => {
  const cost = {
    columns: [
      { ref: "wish_id->title", label: "Wish", type: "text" },
      {
        ref: "usage.cost_usd::sum",
        label: "Cost",
        type: "number",
        unit: "usd",
      },
    ],
    rows: [{ "wish_id->title": "Stats", "usage.cost_usd::sum": 12.5 }],
  };
  const option = c.chartOption(
    cost,
    { kind: "bar", x: "wish_id->title", y: "usage.cost_usd::sum" },
    look,
  );
  assert.match(option.yAxis.axisLabel.formatter(12.5), /\$12\.50|12,50\s?\$/);
  assert.equal(
    option.tooltip.valueFormatter(12.5),
    option.yAxis.axisLabel.formatter(12.5),
  );
  assert.equal(
    option.tooltip.valueFormatter(null),
    "–",
    "absent says a dash, not 0",
  );
  const tokens = { ref: "t", label: "Tokens", type: "number", unit: "tokens" };
  assert.match(c.valueText(tokens, 1200), /tokens$/);
});

test("a column's type comes from the schema, never from its first row", () => {
  // The first row has no duration: the column is still a duration, said as one.
  const waits = {
    columns: [
      { ref: "kind", label: "Kind", type: "enum" },
      { ref: "wait::median", label: "Median wait", type: "duration" },
      { ref: "wait::p90", label: "p90 wait", type: "duration" },
    ],
    rows: [
      { kind: "work", "wait::median": null, "wait::p90": null },
      { kind: "mock", "wait::median": 2 * HOUR, "wait::p90": 5 * HOUR },
    ],
  };
  const option = c.chartOption(
    waits,
    {
      kind: "spread",
      category: "kind",
      median: "wait::median",
      p90: "wait::p90",
    },
    look,
  );
  assert.equal(
    option.xAxis.axisLabel.formatter(2 * HOUR),
    c.valueText(waits.columns[1], 2 * HOUR),
  );
  assert.doesNotMatch(
    option.xAxis.axisLabel.formatter(2 * HOUR),
    /7200000|7,200,000/,
  );
  // A time x is in time order and keeps its days, whatever the rows' order and their first cell.
  const late = {
    columns: byDay.columns,
    rows: [{ "create_time::day": null, kind: "work", count: 9 }, ...byDay.rows],
  };
  assert.deepEqual(bar({}, late).xAxis.data, bar({}).xAxis.data);
  assert.equal(bar({}).xAxis.data.length, 2);
  // A text that reads as a number is not a measure: the schema says text.
  const texts = {
    columns: [
      { ref: "code", label: "Code", type: "text" },
      { ref: "size", label: "Size", type: "text" },
      { ref: "n", label: "Tasks", type: "number" },
    ],
    rows: [{ code: "W1", size: "12", n: 1 }],
  };
  assert.throws(
    () => c.chartOption(texts, { kind: "bar", x: "code", y: "size" }, look),
    c.ChartError,
  );
  assert.throws(
    () => c.chartOption(texts, { kind: "bar", x: "nothing", y: "n" }, look),
    /no column nothing/,
  );
});

test("a spread shows the median and the p90, on one axis and in one unit", () => {
  const frame = {
    columns: [
      { ref: "kind", label: "Kind", type: "enum" },
      { ref: "run::median", label: "Median", type: "duration" },
      { ref: "run::p90", label: "p90", type: "duration" },
      { ref: "cost::p90", label: "p90 cost", type: "number", unit: "usd" },
    ],
    rows: [
      {
        kind: "work",
        "run::median": HOUR,
        "run::p90": 3 * HOUR,
        "cost::p90": 2,
      },
      { kind: "mock", "run::median": 0, "run::p90": null, "cost::p90": 1 },
    ],
  };
  const option = c.chartOption(
    frame,
    {
      kind: "spread",
      category: "kind",
      median: "run::median",
      p90: "run::p90",
      log: true,
    },
    look,
  );
  assert.deepEqual(
    option.series.map((s) => [s.type, s.name]),
    [
      ["bar", "Median"],
      ["scatter", "p90"],
    ],
  );
  assert.deepEqual(
    option.series[0].data,
    [HOUR, null],
    "0 has no place on a log scale",
  );
  assert.deepEqual(option.series[1].data, [
    [3 * HOUR, 0],
    [null, 1],
  ]);
  assert.equal(option.xAxis.type, "log");
  assert.equal(
    option.xAxis.min,
    1_000_000,
    "an hour's axis starts at the power of ten under it, not at 1 ms",
  );
  assert.deepEqual(option.yAxis.data, ["work", "mock"]);
  assert.equal(option.yAxis.inverse, true, "the first row reads at the top");
  assert.throws(
    () =>
      c.chartOption(
        frame,
        {
          kind: "spread",
          category: "kind",
          median: "run::median",
          p90: "cost::p90",
        },
        look,
      ),
    c.ChartError,
  );
});

test("no chart animates: the window at rest draws no frame", () => {
  const options = [
    bar({}),
    bar({ series: "kind", stack: "share" }),
    c.chartOption(
      byDay,
      { kind: "line", x: "create_time::day", y: "count" },
      look,
    ),
  ];
  for (const option of options) {
    assert.equal(option.animation, false);
    assert.equal(option.tooltip.transitionDuration, 0);
  }
});

test("the text size scales the chart's text and marks", () => {
  const big = c.chartOption(
    byDay,
    { kind: "bar", x: "create_time::day", y: "count" },
    { theme, scale: 1.5 },
  );
  assert.equal(big.textStyle.fontSize, 18);
  assert.equal(big.series[0].barMaxWidth, 36);
});

test("the chart's colours are the theme's tokens, stepped apart in the dark and the light theme", () => {
  const css = fs.readFileSync(path.join(root, "src/theme.css"), "utf8");
  const block = (selector) => {
    const start = css.indexOf(`${selector} {`);
    assert.ok(start >= 0, selector);
    return css.slice(start, css.indexOf("\n}", start));
  };
  const tokens = (text) =>
    Object.fromEntries(
      [...text.matchAll(/--(chart-[\w-]+):\s*(#[0-9a-f]{6});/g)].map((m) => [
        m[1],
        m[2],
      ]),
    );
  const dark = tokens(block(":root"));
  const light = tokens(block('html[data-theme="light"]'));
  const names = [...c.SERIES_TOKENS, "chart-other"];
  assert.deepEqual(Object.keys(dark).sort(), [...names].sort());
  assert.deepEqual(Object.keys(light).sort(), [...names].sort());
  assert.ok(
    names.every((n) => dark[n] !== light[n]),
    "each theme has its own steps",
  );
  assert.equal(
    new Set(Object.values(dark)).size,
    names.length,
    "no two series share a hue",
  );

  // chartTheme reads them as the page has them now.
  const page = {
    "chart-1": " #3987e5",
    text: "#ececef",
    muted: "#9d9da5",
    line: "#2a2a2e",
    surface: "#151517",
  };
  globalThis.document = { documentElement: {} };
  globalThis.getComputedStyle = () => ({
    getPropertyValue: (name) => page[name.slice(2)] ?? "",
  });
  try {
    const read = c.chartTheme();
    assert.equal(read.palette[0], "#3987e5");
    assert.equal(read.palette.length, 5);
    assert.equal(read.ink, "#ececef");
    assert.equal(read.surface, "#151517");
  } finally {
    delete globalThis.document;
    delete globalThis.getComputedStyle;
  }
});

test("ECharts draws every kind with the modules it loads, without an animation", async () => {
  const core = await c.loadEcharts();
  assert.equal(await c.loadEcharts(), core, "loaded once");
  const waits = {
    columns: [
      { ref: "kind", label: "Kind", type: "enum" },
      { ref: "m", label: "Median", type: "duration" },
      { ref: "p", label: "p90", type: "duration" },
    ],
    rows: [{ kind: "work", m: HOUR, p: 3 * HOUR }],
  };
  const options = [
    bar({ series: "kind", stack: "stack", grain: "day" }),
    bar({ series: "kind", stack: "share", horizontal: true }),
    bar({ log: true }),
    c.chartOption(
      byDay,
      { kind: "line", x: "create_time::day", y: "count", series: "kind" },
      look,
    ),
    c.chartOption(
      waits,
      { kind: "spread", category: "kind", median: "m", p90: "p" },
      look,
    ),
  ];
  for (const option of options) {
    const chart = core.init(null, null, {
      renderer: "svg",
      ssr: true,
      width: 480,
      height: 220,
    });
    try {
      chart.setOption(option);
      const svg = chart.renderToSVGString();
      assert.match(svg, /^<svg/);
      assert.doesNotMatch(
        svg,
        /<animate|@keyframes/,
        "no animation in the drawing",
      );
      for (const s of option.series.length > 1 ? option.series : [])
        assert.ok(svg.includes(s.name), `the legend names ${s.name}`);
    } finally {
      chart.dispose();
    }
  }
});

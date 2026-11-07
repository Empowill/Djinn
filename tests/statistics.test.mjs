// The Statistics tab of the Hourglass (src/statistics.tsx, L53 A): one query per card, the filters each takes, the
// names a card gives the server's values without changing them, the six figures read from the server's tables, and
// the Hourglass's tabs. The rules of the charts themselves are tests/charts.test.mjs's. Run with `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { bundle } from "./bundle.mjs";
import { setupMockDom } from "./screens.mjs";

const s = await bundle(
  "statistics",
  `export { createElement, act } from "react";
export { createRoot } from "react-dom/client";
export { renderToStaticMarkup } from "react-dom/server";
export { createRouterTransport } from "@connectrpc/connect";
export { createDjinn, DjinnProvider } from "@/src/data/djinn.tsx";
export { readTable } from "@/src/data/query.ts";
export {
  Collection,
  CompareOp,
  FilterGroupOp,
  MeasureFn,
  Period,
  QueryService,
} from "@/gen/ts/query/v1/query_pb.ts";
export { MachineService, GateService, CommandService } from "@/gen/ts/machine/v1/machine_pb.ts";
export {
  Statistics,
  StatisticsFigures,
  binText,
  cardQueries,
  firstDay,
  frameOf,
  typeText,
  useQuery,
} from "@/src/statistics.tsx";
export { Hourglass } from "@/src/hourglass.tsx";
export * as A from "apache-arrow";`,
);
const { A, createElement: h } = s;

const all = { project: "", period: "all", grain: "day" };
const P = "01a118fa-4308-736a-9ff9-5fc373a4294f";

// field is a nullable Arrow field with the metadata internal/query gives it.
function field(name, type, meta) {
  const metadata = new Map();
  metadata.set("djinn", JSON.stringify({ ref: name, ...meta }));
  return new A.Field(name, type, true, metadata);
}

// ipc writes one record batch of columns, each [field, values], as an Arrow IPC stream.
function ipc(columns) {
  const fields = columns.map(([f]) => f);
  const length = columns[0][1].length;
  const children = columns.map(
    ([f, values]) => A.vectorFromArray(values, f.type).data[0],
  );
  const data = A.makeData({
    type: new A.Struct(fields),
    length,
    nullCount: 0,
    children,
  });
  return A.tableToIPC(
    new A.Table([new A.RecordBatch(new A.Schema(fields), data)]),
    "stream",
  );
}

const enumType = () => new A.Dictionary(new A.Utf8(), new A.Int32());
const durationType = () => new A.Duration(A.TimeUnit.MILLISECOND);
const count = (values) => [
  field("count", new A.Int64(), { label: "Count", type: "int", fn: "count" }),
  values.map(BigInt),
];
const SECOND = 1000;
const MINUTE = 60 * SECOND;

// The typed messages of a card's query, written back in the texts the command line types (status=done,
// create_time::day, running::median, -count): what each test reads, beside what the window sends.
const pad = (n) => String(n).padStart(2, "0");
const dayText = (d) =>
  `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
const ms = (d) => Number(d.seconds) * 1000 + Math.round(d.nanos / 1e6);
function durationText(n) {
  for (const [unit, size] of [
    ["h", 60 * MINUTE],
    ["m", MINUTE],
    ["s", SECOND],
  ])
    if (n >= size && n % size === 0) return `${n / size}${unit}`;
  return `${n / SECOND}s`;
}
function filterText(f) {
  const { case: kind, value: v } = f.where;
  if (kind === "group" && v.op === s.FilterGroupOp.NOT)
    return filterText(v.filters[0]).replace("=", "!=");
  if (kind === "stringIn") return `${v.ref}=${v.values.join("|")}`;
  if (kind === "null") return `${v.ref}=null`;
  if (kind === "time" && v.op === s.CompareOp.GREATER_THAN_EQUAL)
    return `${v.ref}>=${dayText(new Date(ms(v.value)))}`;
  throw new Error(`no text for ${JSON.stringify(f)}`);
}
function groupText(g) {
  const { case: kind, value: v } = g.bucket ?? {};
  if (!kind) return g.ref;
  if (kind === "period")
    return `${g.ref}::${v === s.Period.WEEK ? "week" : "day"}`;
  if (kind === "durationBins")
    return `${g.ref}::bins(${v.bounds.map((d) => durationText(ms(d))).join(",")})`;
  return `${g.ref}::bins(${v.bounds.join(",")})`;
}
function measureText(m) {
  const fn = s.MeasureFn[m.fn ?? s.MeasureFn.COUNT].toLowerCase();
  const body = m.ref ? `${m.ref}::${fn}` : fn;
  return m.name ? `${m.name}=${body}` : body;
}
const orderText = (o) => (o.desc ? `-${o.ref}` : o.ref);
// texts is a card's query in texts.
function texts(request) {
  return {
    ...request,
    filter: (request.filter ?? []).map(filterText),
    groupBy: (request.groupBy ?? []).map(groupText),
    measure: (request.measure ?? []).map(measureText),
    sort: (request.sort ?? []).map(orderText),
  };
}
// cardTexts are every card's query in texts.
const cardTexts = (f) =>
  Object.fromEntries(
    Object.entries(s.cardQueries(f)).map(([k, v]) => [k, texts(v)]),
  );

test("each card asks the server for its own aggregate: the window computes nothing", () => {
  const q = cardTexts(all);
  assert.deepEqual(Object.keys(q), [
    "done",
    "azimas",
    "work",
    "delivery",
    "answer",
    "cost",
    "created",
    "merged",
    "times",
    "deliveries",
    "answers",
    "prompts",
    "projects",
  ]);
  for (const [name, request] of Object.entries(q)) {
    assert.ok(request.measure?.length, `${name} measures`);
    assert.equal(request.select, undefined, `${name} lists no rows`);
  }
  assert.equal(q.answer.collection, s.Collection.QUESTIONS);
  assert.equal(q.answers.collection, s.Collection.QUESTIONS);
  assert.equal(q.done.collection, s.Collection.TASKS);
  // Times are medians and p90s, never sums nor means; an azima sums its parts', a watcher sleeps: both stay out.
  assert.deepEqual(q.times.filter, ["type!=azima|watcher", "status=done"]);
  assert.ok(
    q.times.measure.every((m) => /::(median|p90)$/.test(m) || m === "count"),
  );
  assert.deepEqual(q.work.filter, ["type=work", "status=done"]);
  assert.deepEqual(q.work.measure, ["running::median", "running::p90"]);
  assert.deepEqual(q.created.groupBy, ["create_time::day", "type"]);
  assert.deepEqual(q.created.filter, ["type!=azima"]);
  assert.deepEqual(cardTexts({ ...all, grain: "week" }).created.groupBy, [
    "create_time::week",
    "type",
  ]);
  // The histograms ask for their bins; the server gives each, empty or not.
  assert.deepEqual(q.deliveries.groupBy, [
    "integration::delay::bins(0s,10s,1m,10m,1h,24h)",
  ]);
  assert.deepEqual(q.prompts.groupBy, [
    "prompt::size::bins(0,100,300,1000,3000,10000)",
  ]);
});

test("the filters: a period from its first day, a project, the questions by their wish's projects", () => {
  const now = new Date(2026, 10, 7, 1, 30);
  assert.equal(dayText(s.firstDay("7d", now)), "2026-11-01");
  assert.equal(dayText(s.firstDay("30d", now)), "2026-10-09");
  assert.equal(s.firstDay("30d", now).getHours(), 0);
  assert.equal(s.firstDay("all", now), undefined);
  const q = cardTexts({ project: P, period: "7d", grain: "day" });
  const day = dayText(s.firstDay("7d"));
  assert.deepEqual(q.cost.filter, [`create_time>=${day}`, `project_id=${P}`]);
  assert.deepEqual(q.answer.filter, [
    `create_time>=${day}`,
    `wish_id->project_ids=${P}`,
  ]);
  // The azimas merged count by the time of their merge.
  assert.deepEqual(q.merged.filter, [
    "type=azima",
    "azima.merged.time!=null",
    `azima.merged.time>=${day}`,
    `project_id=${P}`,
  ]);
  // No filter at all, all projects and all time.
  assert.deepEqual(cardTexts(all).cost.filter, []);
});

test("a bin is named by its range, a type by its name; every value stays the server's", async () => {
  const bounds = [0, 10 * SECOND, MINUTE, 24 * 60 * MINUTE];
  const table = await s.readTable(
    ipc([
      [
        field("integration::delay::bins", durationType(), {
          label: "Delay",
          type: "duration",
          bounds,
        }),
        bounds.map(BigInt),
      ],
      count([3, 0, 2, 1]),
    ]),
  );
  const col = table.columns[0];
  assert.deepEqual(
    table.rows.map((r) => s.binText(col, r["integration::delay::bins"])),
    ["< 10s", "10s – 1m", "1m – 1d", "≥ 1d"],
  );
  const frame = s.frameOf(table, { count: "Tasks" });
  assert.deepEqual(
    frame.columns.map((c) => [c.ref, c.label, c.type]),
    [
      ["integration::delay::bins", "Delay", "text"],
      ["count", "Tasks", "number"],
    ],
  );
  assert.deepEqual(
    frame.rows.map((r) => r.count),
    [3, 0, 2, 1],
  );

  const types = await s.readTable(
    ipc([
      [
        field("type", enumType(), { label: "Type", type: "enum" }),
        ["TASK_KIND_WORK", "TASK_KIND_MOCKUP"],
      ],
      [
        field("running::median", durationType(), {
          label: "Running median",
          type: "duration",
          fn: "median",
        }),
        [90_000n, null],
      ],
    ]),
  );
  const named = s.frameOf(types, {});
  assert.deepEqual(
    named.rows.map((r) => [r.type, r["running::median"]]),
    [
      ["Work", 90_000],
      // Absent, never 0.
      ["Mockup", null],
    ],
  );
  assert.equal(s.typeText("TASK_KIND_WATCHER"), "Watcher");
  assert.equal(s.typeText("TASK_KIND_NEW"), "TASK_KIND_NEW");
});

// answers is what a QueryService answers each card's query in the figures' test.
function answers(typed) {
  const request = texts(typed);
  const measures = request.measure.join(" ");
  if (request.groupBy[0] === "status")
    return ipc([
      [
        field("status", enumType(), { label: "Status", type: "enum" }),
        ["TASK_STATUS_DONE", "TASK_STATUS_CANCELLED"],
      ],
      count([12, 3]),
    ]);
  if (measures.includes("merged="))
    return ipc([
      count([4]),
      [field("merged", new A.Int64(), { label: "Merged", type: "int" }), [1n]],
    ]);
  if (measures.includes("usage.cost_usd::sum"))
    return ipc([
      [
        field("usage.cost_usd::sum", new A.Float64(), {
          label: "Cost",
          type: "float",
          unit: "usd",
        }),
        [12.5],
      ],
    ]);
  // A median and a p90, the p90 unknown: no row had one.
  const [median, p90] = request.measure;
  return ipc([
    [
      field(median, durationType(), { label: median, type: "duration" }),
      [12n * BigInt(MINUTE)],
    ],
    [field(p90, durationType(), { label: p90, type: "duration" }), [null]],
  ]);
}

function statisticsDjinn() {
  const asked = [];
  const transport = s.createRouterTransport(({ service }) => {
    service(s.QueryService, {
      query(request) {
        asked.push(request);
        return { table: answers(request), total: 1n };
      },
    });
  });
  return { asked, djinn: s.createDjinn(transport, 10) };
}

// text is an element's text, its children's text nodes joined; React sets a lone text child as textContent.
function text(el) {
  if (el.nodeType === 3) return el.nodeValue ?? "";
  if (!el.childNodes?.length && typeof el.textContent === "string")
    return el.textContent;
  return (el.childNodes ?? []).map(text).join("");
}

function find(el, test) {
  if (test(el)) return el;
  for (const child of el.childNodes ?? []) {
    const found = find(child, test);
    if (found) return found;
  }
  return undefined;
}

test("the six figures say what the server read: a median, its p90, a dash for none", async () => {
  const container = setupMockDom();
  const { asked, djinn } = statisticsDjinn();
  const root = s.createRoot(container);
  try {
    await s.act(async () => {
      root.render(
        h(
          s.DjinnProvider,
          { djinn },
          h(s.StatisticsFigures, { q: s.cardQueries(all) }),
        ),
      );
    });
    for (let i = 0; i < 50 && !/\$12\.50/.test(text(container)); i++)
      await s.act(() => new Promise((resolve) => setTimeout(resolve, 10)));
    // One query per figure, each in Arrow.
    assert.equal(asked.length, 6);
    const figure = (id) =>
      text(find(container, (el) => el.attributes?.["data-card"] === id));
    assert.equal(figure("done"), "Tasks done123 cancelled · 0 failed");
    assert.equal(figure("azimas"), "Azimas merged1of 4 azimas");
    assert.equal(figure("work"), "Run of a work12mmedian · p90 –");
    assert.equal(
      figure("cost"),
      "Estimated cost$12.50tokens × prices of the providers",
    );
  } finally {
    s.act(() => root.unmount());
    await new Promise((resolve) => setTimeout(resolve, 30));
    for (const handle of process._getActiveHandles())
      if (handle?.constructor?.name === "MessagePort") handle.unref();
  }
});

test("a card's read changes with its request: never the old table, whose columns the new chart does not name", async () => {
  // Grouped by week, the chart asks for create_time::week: a render with the day's table threw ChartError from an
  // effect, and the window unmounted down to its splash.
  const container = setupMockDom();
  const transport = s.createRouterTransport(({ service }) => {
    service(s.QueryService, {
      query(request) {
        const by = texts(request).groupBy[0];
        return {
          table: ipc([
            [field(by, new A.Utf8(), { label: by, type: "text" }), ["a"]],
            count([1]),
          ]),
          total: 1n,
        };
      },
    });
  });
  const djinn = s.createDjinn(transport, 10);
  const seen = [];
  const Probe = ({ request }) => {
    const read = s.useQuery(request);
    seen.push(read.state === "done" ? read.table.columns[0].ref : read.state);
    return null;
  };
  const root = s.createRoot(container);
  const show = (grain) =>
    s.act(async () => {
      root.render(
        h(
          s.DjinnProvider,
          { djinn },
          h(Probe, { request: s.cardQueries({ ...all, grain }).created }),
        ),
      );
    });
  const settle = async (ref) => {
    for (let i = 0; i < 50 && seen.at(-1) !== ref; i++)
      await s.act(() => new Promise((resolve) => setTimeout(resolve, 10)));
    assert.equal(seen.at(-1), ref);
  };
  try {
    await show("day");
    await settle("create_time::day");
    const from = seen.length;
    await show("week");
    await settle("create_time::week");
    assert.ok(
      !seen.slice(from).includes("create_time::day"),
      `the week's renders read ${seen.slice(from).join(", ")}`,
    );
  } finally {
    s.act(() => root.unmount());
    await new Promise((resolve) => setTimeout(resolve, 30));
    for (const handle of process._getActiveHandles())
      if (handle?.constructor?.name === "MessagePort") handle.unref();
  }
});

test("the Hourglass has three tabs, the machine first, each with its icon of the wishes' bank; Statistics shows its filters and its cards as it reads", () => {
  const { djinn } = statisticsDjinn();
  const html = s.renderToStaticMarkup(
    h(s.DjinnProvider, { djinn }, h(s.Hourglass, { wishes: [] })),
  );
  const tabs = html.slice(
    html.indexOf('<div class="view-tabs hourglass-tabs"'),
    html.indexOf('<div role="tabpanel"'),
  );
  assert.deepEqual(
    [
      ...tabs.matchAll(
        /<button type="button" role="tab" id="hourglass-tab-(\w+)" aria-selected="(\w+)"[^>]*><svg class="lucide wish-icon wish-duo-\w+"[^>]*>[\s\S]*?<\/svg>(\w+)<\/button>/g,
      ),
    ].map(([, id, selected, label]) => `${id} ${selected} ${label}`),
    [
      "machine true Machine",
      "gates false Gates",
      "statistics false Statistics",
    ],
  );
  assert.match(html, /class="consumption-panel"/);

  const tab = s.renderToStaticMarkup(
    h(s.DjinnProvider, { djinn }, h(s.Statistics)),
  );
  assert.match(tab, /<option value="" selected="">All<\/option>/);
  assert.match(tab, /aria-pressed="true">30 d<\/button>/);
  assert.match(tab, /aria-pressed="true">Day<\/button>/);
  // Six figures, seven cards, each reading.
  assert.equal([...tab.matchAll(/class="statistics-figure"/g)].length, 6);
  assert.equal([...tab.matchAll(/class="statistics-card( wide)?"/g)].length, 7);
  assert.equal([...tab.matchAll(/>Reading…</g)].length, 7);
});

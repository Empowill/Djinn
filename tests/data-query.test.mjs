// The reader of QueryService (src/data/query.ts) on Arrow tables shaped as internal/query writes them: Int64, Float64,
// Bool, Utf8, Dictionary(Int32, Utf8), Timestamp(ms, UTC), Duration(ms) and List, each field with its "djinn"
// metadata. Run with `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { bundle } from "./bundle.mjs";

const q = await bundle(
  "query",
  `export { frameOf, query, readTable } from "@/src/data/query.ts";
export { createRouterTransport, createClient } from "@connectrpc/connect";
export { Collection, Format, QueryService } from "@/gen/ts/query/v1/query_pb.ts";
export * as A from "apache-arrow";`,
);
const { A } = q;

// field is a nullable Arrow field with the metadata internal/query gives it.
function field(name, type, meta) {
  const metadata = new Map();
  if (meta) metadata.set("djinn", JSON.stringify({ ref: name, ...meta }));
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
  const batch = new A.RecordBatch(new A.Schema(fields), data);
  return A.tableToIPC(new A.Table([batch]), "stream");
}

const enumType = () => new A.Dictionary(new A.Utf8(), new A.Int32());
const timeType = () => new A.TimestampMillisecond("UTC");
const durationType = () => new A.Duration(A.TimeUnit.MILLISECOND);
const listType = () => new A.List(new A.Field("item", new A.Utf8(), true));

const t1 = Date.UTC(2026, 10, 1, 9, 30);
const t2 = Date.UTC(2026, 10, 6, 23, 59, 59, 250);

// Two tasks as a list of TASKS answers: the second has no cost, no start, no running time and no scopes.
const tasks = () =>
  ipc([
    [
      field("code", new A.Utf8(), { label: "Code", type: "string" }),
      ["W1", "W2"],
    ],
    [
      field("status", enumType(), { label: "Status", type: "enum" }),
      ["TASK_STATUS_DONE", null],
    ],
    [
      field("turns.total", new A.Int64(), { label: "Turns", type: "int" }),
      [12n, 0n],
    ],
    [
      field("usage.cost_usd", new A.Float64(), {
        label: "Cost",
        type: "float",
        unit: "usd",
      }),
      [1.25, null],
    ],
    [
      field("scheduled", new A.Bool(), { label: "Scheduled", type: "bool" }),
      [true, false],
    ],
    [
      field("start_time", timeType(), { label: "Started", type: "time" }),
      [t1, null],
    ],
    [
      field("running", durationType(), { label: "Running", type: "duration" }),
      [90_000n, null],
    ],
    [
      field("write_scopes", listType(), {
        label: "Write scopes",
        type: "string",
        list: true,
      }),
      [["src/data", "package.json"], null],
    ],
  ]);

test("a list comes back as typed rows, its columns described by the schema", async () => {
  const table = await q.readTable(tasks());
  assert.deepEqual(
    table.columns.map((c) => [c.ref, c.label, c.type, c.list, c.unit]),
    [
      ["code", "Code", "text", false, undefined],
      ["status", "Status", "enum", false, undefined],
      ["turns.total", "Turns", "number", false, undefined],
      ["usage.cost_usd", "Cost", "number", false, "usd"],
      ["scheduled", "Scheduled", "bool", false, undefined],
      ["start_time", "Started", "time", false, undefined],
      ["running", "Running", "duration", false, undefined],
      ["write_scopes", "Write scopes", "text", true, undefined],
    ],
  );
  assert.equal(table.total, 2);
  const [w1, w2] = table.rows;
  assert.equal(w1.code, "W1");
  assert.equal(w1.status, "TASK_STATUS_DONE");
  // An Int64 is a number, not a bigint; a duration its milliseconds.
  assert.equal(w1["turns.total"], 12);
  assert.equal(typeof w1["turns.total"], "number");
  assert.equal(w1.running, 90_000);
  assert.equal(typeof w1.running, "number");
  assert.equal(w1["usage.cost_usd"], 1.25);
  assert.equal(w1.scheduled, true);
  assert.deepEqual(w1.write_scopes, ["src/data", "package.json"]);
  // A zero stays a zero, false stays false.
  assert.equal(w2["turns.total"], 0);
  assert.equal(w2.scheduled, false);
});

test("an absent value is null, never 0 nor an empty text", async () => {
  const [, w2] = (await q.readTable(tasks())).rows;
  for (const ref of [
    "status",
    "usage.cost_usd",
    "start_time",
    "running",
    "write_scopes",
  ])
    assert.equal(w2[ref], null, ref);
});

test("an instant is a Date at the same millisecond, in UTC", async () => {
  const table = await q.readTable(
    ipc([
      [
        field("create_time", timeType(), { label: "Created", type: "time" }),
        [t1, t2, null],
      ],
    ]),
  );
  const [a, b, c] = table.rows.map((r) => r.create_time);
  assert.ok(a instanceof Date);
  assert.equal(a.getTime(), t1);
  assert.equal(b.toISOString(), "2026-11-06T23:59:59.250Z");
  assert.equal(c, null);
});

test("an aggregate keeps its groups, its measures and what each reads", async () => {
  const week = (d) => Date.UTC(2026, 10, d);
  const bytes = ipc([
    [
      field("create_time::week", timeType(), {
        label: "Created (week)",
        type: "time",
        of: "create_time",
        granularity: "week",
      }),
      [week(2), week(2), week(9)],
    ],
    [
      field("running::bins", durationType(), {
        label: "Running",
        type: "duration",
        of: "running",
        bounds: [0, 10_000, 60_000],
      }),
      [0n, 10_000n, 60_000n],
    ],
    [
      field("count", new A.Int64(), {
        label: "Count",
        type: "int",
        fn: "count",
      }),
      [4n, 0n, 2n],
    ],
    [
      field("cost", new A.Float64(), {
        label: "Cost",
        type: "float",
        unit: "usd",
        of: "usage.cost_usd",
        fn: "sum",
      }),
      [3.5, null, 0.5],
    ],
  ]);
  const table = await q.readTable(bytes, 7);
  // total is what the server counted before its limit, not the rows read.
  assert.equal(table.total, 7);
  const [period, bins, count, cost] = table.columns;
  assert.equal(period.granularity, "week");
  assert.equal(period.of, "create_time");
  assert.equal(period.type, "time");
  assert.deepEqual(bins.bounds, [0, 10_000, 60_000]);
  assert.equal(bins.type, "duration");
  assert.equal(count.fn, "count");
  assert.equal(cost.fn, "sum");
  assert.equal(cost.of, "usage.cost_usd");
  assert.equal(cost.unit, "usd");
  assert.deepEqual(
    table.rows.map((r) => [
      r["create_time::week"].getTime(),
      r["running::bins"],
      r.count,
      r.cost,
    ]),
    [
      [week(2), 0, 4, 3.5],
      [week(2), 10_000, 0, null],
      [week(9), 60_000, 2, 0.5],
    ],
  );
});

test("a field without metadata is named by its ref", async () => {
  const table = await q.readTable(ipc([[field("code", new A.Utf8()), ["W1"]]]));
  assert.deepEqual(table.columns, [
    { ref: "code", label: "code", type: "text", list: false },
  ]);
});

test("query asks QueryService for Arrow and reads its table", async () => {
  const asked = [];
  const transport = q.createRouterTransport(({ service }) => {
    service(q.QueryService, {
      query(request) {
        asked.push(request);
        return { arrow: tasks(), total: 40n };
      },
    });
  });
  const client = q.createClient(q.QueryService, transport);
  const table = await q.query(client, {
    collection: q.Collection.TASKS,
    filter: ["status=done|null"],
    sort: ["-create_time"],
    select: ["code", "status"],
    limit: 2,
  });
  assert.equal(asked.length, 1);
  assert.equal(asked[0].format, q.Format.ARROW);
  assert.equal(asked[0].collection, q.Collection.TASKS);
  assert.deepEqual(asked[0].filter, ["status=done|null"]);
  assert.deepEqual(asked[0].sort, ["-create_time"]);
  assert.deepEqual(asked[0].select, ["code", "status"]);
  assert.equal(asked[0].limit, 2);
  assert.equal(table.total, 40);
  assert.equal(table.rows.length, 2);
  assert.equal(table.rows[0].code, "W1");
});

test("frameOf leaves out the list columns a chart never draws", async () => {
  const table = await q.readTable(tasks());
  const frame = q.frameOf(table);
  assert.ok(!frame.columns.some((c) => c.ref === "write_scopes"));
  assert.equal(frame.columns.length, table.columns.length - 1);
  assert.ok(!("write_scopes" in frame.rows[0]));
  assert.equal(frame.rows[0].code, "W1");
  // A table without lists is the frame as it is.
  const flat = await q.readTable(ipc([[field("code", new A.Utf8()), ["W1"]]]));
  assert.equal(q.frameOf(flat), flat);
});

// The reader of QueryService (L59): it asks a collection for a table in Arrow and gives it back as typed rows. A
// column's type comes from the Arrow schema, never from its values: a whole number or a duration is a number (Arrow's
// bigint read once), an instant a Date, an absent value null, never 0 (Q111 A). Each field's "djinn" metadata gives the
// column its ref, label and unit, and an aggregated column what it reads. apache-arrow is loaded on demand.
import type { MessageInitShape } from "@bufbuild/protobuf";
import { durationFromMs, timestampFromDate } from "@bufbuild/protobuf/wkt";
import type { Client } from "@connectrpc/connect";
import type { Data, DataType, Field, Vector } from "apache-arrow";

import {
  CompareOp,
  type FilterSchema,
  FilterGroupOp,
  Format,
  type GroupBySchema,
  type MeasureFn,
  type MeasureSchema,
  type OrderSchema,
  type Period,
  type QueryService,
  type QueryServiceQueryRequestSchema,
} from "../../gen/ts/query/v1/query_pb";
import type { Cell, Column, ColumnType, Frame } from "../charts/frame";

// QueryColumn is a column of a query's table, as its schema says it.
export interface QueryColumn extends Column {
  // A list of values of its type in each row.
  list: boolean;
  // An aggregated column's: the ref it reads and its function (count, sum, avg, min, max, median, p90).
  of?: string;
  fn?: string;
  // A group of instants: its period, day, week or month, in the machine's time zone.
  granularity?: string;
  // A group by bins: the lower bounds of its bins, a duration's in milliseconds.
  bounds?: readonly number[];
}

// QueryCell is a cell of a query's row: a list column's holds an array, or null.
export type QueryCell = Cell | readonly Cell[];
export type QueryRow = Readonly<Record<string, QueryCell>>;

export interface QueryTable {
  columns: readonly QueryColumn[];
  rows: readonly QueryRow[];
  // The rows before the limit: the groups when aggregated.
  total: number;
}

// QueryRequest is what a query asks, in QueryService's typed messages: the builders below write them. The table
// always comes in Arrow, QueryService's standard.
export type QueryRequest = Omit<
  MessageInitShape<typeof QueryServiceQueryRequestSchema>,
  "$typeName" | "format"
>;

export type FilterInit = MessageInitShape<typeof FilterSchema>;

// not holds when filter does not: for In, an absent value included.
export function not(filter: FilterInit): FilterInit {
  return {
    where: {
      case: "group",
      value: { op: FilterGroupOp.NOT, filters: [filter] },
    },
  };
}

// oneOf holds when ref, a text or an enum, is one of values, ignoring case: an enum by its value's name, prefix or
// not (oneOf("type", "work")).
export function oneOf(ref: string, ...values: string[]): FilterInit {
  return { where: { case: "stringIn", value: { ref, values } } };
}

// present holds when ref has a value.
export function present(ref: string): FilterInit {
  return not({ where: { case: "null", value: { ref } } });
}

// since holds when the time ref is at or after at.
export function since(ref: string, at: Date): FilterInit {
  return {
    where: {
      case: "time",
      value: {
        ref,
        op: CompareOp.GREATER_THAN_EQUAL,
        value: timestampFromDate(at),
      },
    },
  };
}

// by groups by each value of ref; byPeriod a time by period, in the machine's time zone.
export function by(ref: string): MessageInitShape<typeof GroupBySchema> {
  return { ref };
}

export function byPeriod(
  ref: string,
  period: Period,
): MessageInitShape<typeof GroupBySchema> {
  return { ref, bucket: { case: "period", value: period } };
}

// byDurationBins groups a duration by bins, their bounds in milliseconds; byNumberBins a number.
export function byDurationBins(
  ref: string,
  boundsMs: readonly number[],
): MessageInitShape<typeof GroupBySchema> {
  return {
    ref,
    bucket: {
      case: "durationBins",
      value: { bounds: boundsMs.map((ms) => durationFromMs(ms)) },
    },
  };
}

export function byNumberBins(
  ref: string,
  bounds: readonly number[],
): MessageInitShape<typeof GroupBySchema> {
  return {
    ref,
    bucket: { case: "numberBins", value: { bounds: [...bounds] } },
  };
}

// measure computes fn over ref in each group, its column named name, or ref::fn; without ref, it counts the rows.
export function measure(
  fn: MeasureFn,
  ref = "",
  name = "",
): MessageInitShape<typeof MeasureSchema> {
  return { ref, fn, name };
}

// order sorts by ref, or by a column of an aggregate.
export function order(
  ref: string,
  desc = false,
): MessageInitShape<typeof OrderSchema> {
  return { ref, desc };
}

type Arrow = typeof import("./arrow");

let arrow: Promise<Arrow> | undefined;

// loadArrow loads apache-arrow once; a failed load is tried again at the next call.
export function loadArrow(): Promise<Arrow> {
  arrow ??= import("./arrow").catch((err: unknown) => {
    arrow = undefined;
    throw err;
  });
  return arrow;
}

// query reads a table from QueryService. apache-arrow loads while the server answers.
export async function query(
  client: Client<typeof QueryService>,
  request: QueryRequest,
  signal?: AbortSignal,
): Promise<QueryTable> {
  const [response] = await Promise.all([
    client.query({ ...request, format: Format.ARROW }, { signal }),
    loadArrow(),
  ]);
  return readTable(response.table, Number(response.total));
}

// readTable reads an Arrow IPC stream of QueryService into typed rows. total defaults to the rows read.
export async function readTable(
  bytes: Uint8Array,
  total?: number,
): Promise<QueryTable> {
  const { DataType: Types, tableFromIPC } = await loadArrow();
  const table = tableFromIPC(bytes);
  const columns: QueryColumn[] = [];
  const cells: QueryCell[][] = [];
  table.schema.fields.forEach((field: Field, i: number) => {
    const list = Types.isList(field.type);
    const type: DataType = list ? field.type.children[0].type : field.type;
    const col = describe(field, kindOf(Types, type), list);
    const vector = table.getChildAt(i);
    columns.push(col);
    cells.push(vector ? readColumn(Types, vector, col) : []);
  });
  const rows: QueryRow[] = new Array<QueryRow>(table.numRows);
  for (let r = 0; r < table.numRows; r++) {
    const row: Record<string, QueryCell> = {};
    columns.forEach((col, c) => (row[col.ref] = cells[c][r]));
    rows[r] = row;
  }
  return { columns, rows, total: total ?? table.numRows };
}

// frameOf is the table a chart draws: its columns but lists, which no chart draws.
export function frameOf(table: QueryTable): Frame {
  const columns = table.columns.filter((c) => !c.list);
  if (columns.length === table.columns.length) return table as unknown as Frame;
  const rows = table.rows.map((row) => {
    const scalar: Record<string, Cell> = {};
    for (const c of columns) scalar[c.ref] = row[c.ref] as Cell;
    return scalar;
  });
  return { columns, rows };
}

// readColumn reads a column's cells. A plain column (text, enum, number, instant, duration) is read from its buffers:
// an enum's names decoded once, not once per row; apache-arrow's get, per cell, is ~3x slower on a list of tasks. A
// list, a bool, or a slice of another table goes through get.
function readColumn(
  Types: Arrow["DataType"],
  vector: Vector,
  col: QueryColumn,
): QueryCell[] {
  const out = new Array<QueryCell>(vector.length);
  const read = reader(col.type);
  let at = 0;
  for (const data of vector.data) {
    const cell = col.list ? undefined : plain(Types, data, read);
    const nulls = data.nullCount > 0;
    for (let r = 0; r < data.length; r++, at++) {
      if (nulls && !data.getValid(r)) out[at] = null;
      else if (cell) out[at] = cell(r);
      else {
        const v: unknown = vector.get(at);
        if (v == null) out[at] = null;
        else if (col.list)
          out[at] = Array.from(v as Vector, (x: unknown) =>
            x == null ? null : read(x),
          );
        else out[at] = read(v);
      }
    }
  }
  return out;
}

const utf8 = new TextDecoder();

// plain reads the valid cells of a chunk from its buffers, when its type has a plain layout; undefined otherwise.
function plain(
  Types: Arrow["DataType"],
  data: Data,
  read: (v: unknown) => Cell,
): ((r: number) => Cell) | undefined {
  if (data.offset !== 0) return undefined;
  const type = data.type;
  if (Types.isDictionary(type) && data.dictionary) {
    const names = Array.from(data.dictionary, (x: unknown) =>
      x == null ? null : String(x),
    );
    const index = data.values as ArrayLike<number>;
    return (r) => names[index[r]] ?? null;
  }
  if (Types.isUtf8(type)) {
    const bytes = data.values as Uint8Array;
    const offsets = data.valueOffsets as Int32Array;
    return (r) => utf8.decode(bytes.subarray(offsets[r], offsets[r + 1]));
  }
  const values: unknown = data.values;
  const numbers =
    values instanceof BigInt64Array ||
    values instanceof Float64Array ||
    values instanceof Int32Array;
  if (
    numbers &&
    (Types.isInt(type) ||
      Types.isFloat(type) ||
      Types.isTimestamp(type) ||
      Types.isDuration(type))
  )
    return (r) => read(values[r]);
  return undefined;
}

// kindOf is a column's type by its Arrow type.
function kindOf(Types: Arrow["DataType"], type: DataType): ColumnType {
  if (Types.isDictionary(type)) return "enum";
  if (Types.isInt(type) || Types.isFloat(type)) return "number";
  if (Types.isBool(type)) return "bool";
  if (Types.isTimestamp(type) || Types.isDate(type)) return "time";
  if (Types.isDuration(type)) return "duration";
  return "text";
}

// reader turns a value apache-arrow gives, never null, into a cell of the column's type.
function reader(type: ColumnType): (v: unknown) => Cell {
  switch (type) {
    case "number":
    case "duration":
      return (v) => Number(v as number | bigint);
    case "time":
      return (v) => new Date(Number(v as number | bigint | Date));
    case "bool":
      return (v) => Boolean(v);
    default:
      return (v) => String(v);
  }
}

// Meta is the "djinn" metadata of a field, as internal/query writes it.
interface Meta {
  ref?: string;
  label?: string;
  unit?: string;
  of?: string;
  fn?: string;
  granularity?: string;
  bounds?: unknown[];
}

// describe makes a field's column from its metadata; a field without one is named by its ref.
function describe(field: Field, type: ColumnType, list: boolean): QueryColumn {
  let meta: Meta = {};
  const text = field.metadata.get("djinn");
  if (text) {
    try {
      meta = JSON.parse(text) as Meta;
    } catch {
      meta = {};
    }
  }
  const col: QueryColumn = {
    ref: meta.ref || field.name,
    label: meta.label || meta.ref || field.name,
    type,
    list,
  };
  if (meta.unit) col.unit = meta.unit;
  if (meta.of) col.of = meta.of;
  if (meta.fn) col.fn = meta.fn;
  if (meta.granularity) col.granularity = meta.granularity;
  if (meta.bounds?.length) col.bounds = meta.bounds.map(Number);
  return col;
}

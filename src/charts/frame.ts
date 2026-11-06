// The table a chart draws: columns described by the schema, rows of typed values. The reader of QueryService
// (src/data/query.ts, L59) makes one from an Arrow table: a column's type comes from the schema, never from its first
// row, and an absent value is null, never 0 (Q111 A).
import { span } from "../data/format";
import { language } from "../i18n";

// ColumnType is what a column holds, as the query's schema says it: a time is a Date, a duration milliseconds.
export type ColumnType =
  "text" | "enum" | "bool" | "number" | "time" | "duration";

export interface Column {
  // The query's reference: "status", "usage.cost_usd", "wish_id->title", "create_time::day".
  ref: string;
  // The name shown in the legend, the tooltip and the axis.
  label: string;
  type: ColumnType;
  // The unit of a number: "usd", "percent", "tokens"…; a duration has its own.
  unit?: string;
}

export type Cell = string | number | boolean | Date | null;
export type Row = Readonly<Record<string, Cell | undefined>>;

export interface Frame {
  columns: readonly Column[];
  rows: readonly Row[];
}

// ChartError says a chart asks the frame for what it does not hold: a missing column, a measure that is not one.
export class ChartError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "ChartError";
  }
}

// column finds a column by its reference.
export function column(frame: Frame, ref: string): Column {
  const found = frame.columns.find((c) => c.ref === ref);
  if (!found) throw new ChartError(`no column ${ref} in the frame`);
  return found;
}

// measure finds a column a chart measures: a number or a duration, by the schema.
export function measure(frame: Frame, ref: string): Column {
  const found = column(frame, ref);
  if (found.type !== "number" && found.type !== "duration")
    throw new ChartError(`${ref} is a ${found.type}, not a measure`);
  return found;
}

// numberOf reads a measure's cell: a number, or null when absent or not finite.
export function numberOf(cell: Cell | undefined): number | null {
  return typeof cell === "number" && Number.isFinite(cell) ? cell : null;
}

const plain = new Intl.NumberFormat(language, { maximumFractionDigits: 1 });
const usd = new Intl.NumberFormat(language, {
  style: "currency",
  currency: "USD",
  maximumFractionDigits: 2,
});
const percent = new Intl.NumberFormat(language, {
  style: "percent",
  maximumFractionDigits: 1,
});

// valueText says a measure's value with its unit; an absent value is a dash, never 0.
export function valueText(col: Column, value: number | null): string {
  if (value === null) return "–";
  if (col.type === "duration") return span(value);
  switch (col.unit) {
    case "usd":
      return usd.format(value);
    case "percent":
      return percent.format(value / 100);
    case undefined:
    case "":
      return plain.format(value);
    default:
      return `${plain.format(value)} ${col.unit}`;
  }
}

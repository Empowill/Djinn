// The Statistics tab of the Hourglass (L53 variant A, L59): filters in one line, six figures, then cards. Each card asks
// QueryService for its own aggregate, read once as the tab opens and again when a filter changes, never polled: the
// window draws what the server computed, it computes nothing itself. A time is a median and a p90, never a sum nor a
// mean; an absent value is a dash, never 0.
import { type ReactNode, useEffect, useMemo, useState } from "react";

import { create, fromJsonString, toJsonString } from "@bufbuild/protobuf";

import {
  Collection,
  MeasureFn,
  Period as QueryPeriod,
  QueryServiceQueryRequestSchema,
} from "../gen/ts/query/v1/query_pb";
import { Chart } from "./charts/chart";
import {
  type Cell,
  type Column,
  type Frame,
  numberOf,
  valueText,
} from "./charts/frame";
import type { ChartSpec } from "./charts/option";
import { useClients, useData } from "./data/djinn";
import { secondText } from "./data/format";
import {
  by,
  byDurationBins,
  byNumberBins,
  byPeriod,
  type FilterInit,
  measure,
  not,
  oneOf,
  order,
  present,
  type QueryColumn,
  type QueryRequest,
  type QueryTable,
  query,
  since,
} from "./data/query";
import { t } from "./i18n";
import "./statistics.css";

export type Period = "7d" | "30d" | "all";
export type Grain = "day" | "week";

// Filters are what every card reads: a project or all, a period back from today, the buckets of a time axis.
export interface Filters {
  project: string;
  period: Period;
  grain: Grain;
}

// The types of a task as the ref type gives them, in the order a chart gives them their colours; azimas and watchers
// come last: an azima sums its parts' times, a watcher runs a command and sleeps.
const TYPES = [
  "TASK_KIND_WORK",
  "TASK_KIND_CORRECTION",
  "TASK_KIND_REVIEW",
  "TASK_KIND_INVESTIGATION",
  "TASK_KIND_MOCKUP",
  "TASK_KIND_IFRIT",
  "TASK_KIND_WATCHER",
  "TASK_KIND_AZIMA",
] as const;

const typeKeys = {
  TASK_KIND_WORK: "statistics.type_work",
  TASK_KIND_CORRECTION: "statistics.type_correction",
  TASK_KIND_REVIEW: "statistics.type_review",
  TASK_KIND_INVESTIGATION: "statistics.type_investigation",
  TASK_KIND_MOCKUP: "statistics.type_mockup",
  TASK_KIND_IFRIT: "statistics.type_ifrit",
  TASK_KIND_WATCHER: "statistics.type_watcher",
  TASK_KIND_AZIMA: "statistics.type_azima",
} as const;

// typeText names a task's type; an unknown one keeps its value.
export function typeText(value: Cell | undefined): string {
  const key = typeKeys[value as keyof typeof typeKeys];
  return key ? t(key) : String(value ?? "–");
}

const TYPE_ORDER = TYPES.map((v) => t(typeKeys[v]));

// The bins of the histograms: durations in milliseconds, sizes in tokens.
const S = 1000;
const M = 60 * S;
const H = 60 * M;
const DELIVERY_BINS = [0, 10 * S, M, 10 * M, H, 24 * H];
const ANSWER_BINS = [0, M, 10 * M, H, 4 * H, 24 * H];
const PROMPT_BINS = [0, 100, 300, 1000, 3000, 10000];

// firstDay is the start of the period's first day, in the machine's time zone; none for all.
export function firstDay(period: Period, now = new Date()): Date | undefined {
  if (period === "all") return undefined;
  const d = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  d.setDate(d.getDate() - (period === "7d" ? 6 : 29));
  return d;
}

// taskFilters are the filters every query on the tasks takes: created in the period, in the project.
function taskFilters(f: Filters, time = "create_time"): FilterInit[] {
  const out: FilterInit[] = [];
  const day = firstDay(f.period);
  if (day) out.push(since(time, day));
  if (f.project) out.push(oneOf("project_id", f.project));
  return out;
}

// questionFilters: a question belongs to a wish, the wish to its projects.
function questionFilters(f: Filters): FilterInit[] {
  const out: FilterInit[] = [];
  const day = firstDay(f.period);
  if (day) out.push(since("create_time", day));
  if (f.project) out.push(oneOf("wish_id->project_ids", f.project));
  return out;
}

const count = measure(MeasureFn.COUNT);
const median = (ref: string) => measure(MeasureFn.MEDIAN, ref);
const p90 = (ref: string) => measure(MeasureFn.P90, ref);

// The queries of the cards, one each: what the tab asks the server.
export function cardQueries(f: Filters): Record<string, QueryRequest> {
  const tasks = Collection.TASKS;
  const questions = Collection.QUESTIONS;
  const period = f.grain === "week" ? QueryPeriod.WEEK : QueryPeriod.DAY;
  // Agents' work: neither an azima, which sums its parts' times, nor a watcher, which runs a command and sleeps.
  const agents = not(oneOf("type", "azima", "watcher"));
  const done = oneOf("status", "done");
  return {
    done: {
      collection: tasks,
      filter: [not(oneOf("type", "azima")), ...taskFilters(f)],
      groupBy: [by("status")],
      measure: [count],
    },
    azimas: {
      collection: tasks,
      filter: [oneOf("type", "azima"), ...taskFilters(f)],
      measure: [count, measure(MeasureFn.COUNT, "azima.merged.time", "merged")],
    },
    work: {
      collection: tasks,
      filter: [oneOf("type", "work"), done, ...taskFilters(f)],
      measure: [median("running"), p90("running")],
    },
    delivery: {
      collection: tasks,
      filter: taskFilters(f),
      measure: [median("integration::delay"), p90("integration::delay")],
    },
    answer: {
      collection: questions,
      filter: questionFilters(f),
      measure: [median("answer::delay"), p90("answer::delay")],
    },
    cost: {
      collection: tasks,
      filter: taskFilters(f),
      measure: [measure(MeasureFn.SUM, "usage.cost_usd")],
    },
    created: {
      collection: tasks,
      filter: [not(oneOf("type", "azima")), ...taskFilters(f)],
      groupBy: [byPeriod("create_time", period), by("type")],
      measure: [count],
    },
    merged: {
      collection: tasks,
      filter: [
        oneOf("type", "azima"),
        present("azima.merged.time"),
        ...taskFilters(f, "azima.merged.time"),
      ],
      groupBy: [byPeriod("azima.merged.time", period)],
      measure: [count],
    },
    times: {
      collection: tasks,
      filter: [agents, done, ...taskFilters(f)],
      groupBy: [by("type")],
      measure: [
        count,
        median("waiting_before_start"),
        p90("waiting_before_start"),
        median("running"),
        p90("running"),
        median("waiting_gate"),
        p90("waiting_gate"),
        median("paused"),
        p90("paused"),
      ],
    },
    deliveries: {
      collection: tasks,
      filter: [present("integration::delay"), ...taskFilters(f)],
      groupBy: [byDurationBins("integration::delay", DELIVERY_BINS)],
      measure: [count],
    },
    answers: {
      collection: questions,
      filter: [present("answer::delay"), ...questionFilters(f)],
      groupBy: [byDurationBins("answer::delay", ANSWER_BINS)],
      measure: [count],
    },
    prompts: {
      collection: tasks,
      filter: [agents, present("prompt::size"), ...taskFilters(f)],
      groupBy: [byNumberBins("prompt::size", PROMPT_BINS)],
      measure: [count],
    },
    projects: {
      collection: tasks,
      filter: [agents, ...taskFilters(f)],
      groupBy: [by("project_id->name"), by("project_id")],
      measure: [
        count,
        measure(MeasureFn.SUM, "usage.cost_usd"),
        median("running"),
      ],
      sort: [order("count", true)],
    },
  };
}

type Read =
  | { state: "loading" }
  | { state: "done"; table: QueryTable }
  | { state: "error"; message: string };

// useQuery reads a card's table once, and again when its request changes; a request left behind is aborted.
export function useQuery(request: QueryRequest): Read {
  const { query: client } = useClients();
  // The request's canonical JSON: a key that changes only with what it asks, its times included.
  const key = toJsonString(
    QueryServiceQueryRequestSchema,
    create(QueryServiceQueryRequestSchema, request),
  );
  // The read keeps the key it answers: until the new request's read, the render after a change reads loading, never
  // the old table, whose columns the new spec does not name (create_time::day under a week).
  const [read, setRead] = useState<{ key: string; read: Read }>({
    key,
    read: { state: "loading" },
  });
  useEffect(() => {
    const abort = new AbortController();
    setRead({ key, read: { state: "loading" } });
    query(
      client,
      fromJsonString(QueryServiceQueryRequestSchema, key),
      abort.signal,
    ).then(
      (table) => {
        if (!abort.signal.aborted)
          setRead({ key, read: { state: "done", table } });
      },
      (err: unknown) => {
        if (abort.signal.aborted) return;
        console.error("statistics: a query failed", err);
        setRead({
          key,
          read: {
            state: "error",
            message: err instanceof Error ? err.message : String(err),
          },
        });
      },
    );
    return () => abort.abort();
  }, [client, key]);
  return read.key === key ? read.read : LOADING;
}

const LOADING: Read = { state: "loading" };

// cell reads a table's cell in its first row, or in the row whose ref holds value.
function cellOf(
  table: QueryTable,
  ref: string,
  where?: { ref: string; value: string },
): number | null {
  const row = where
    ? table.rows.find((r) => r[where.ref] === where.value)
    : table.rows[0];
  return row ? numberOf(row[ref] as Cell) : null;
}

function columnOf(table: QueryTable, ref: string): Column {
  return (
    table.columns.find((c) => c.ref === ref) ?? {
      ref,
      label: ref,
      type: "number",
    }
  );
}

// binText names the bin whose lower bound is value: below the second bound for the first, from the last bound up
// for the last. A bound reads in its column's type, without a unit for a number: the card says it.
export function binText(col: QueryColumn, value: Cell | undefined): string {
  const bounds = col.bounds ?? [];
  const text = (v: number) =>
    valueText({ ref: col.ref, label: col.label, type: col.type }, v);
  const i = bounds.findIndex((b) => b === value);
  if (i < 0 || bounds.length < 2) return String(value ?? "–");
  if (i === 0) return `< ${text(bounds[1])}`;
  if (i === bounds.length - 1) return `≥ ${text(bounds[i])}`;
  return `${text(bounds[i])} – ${text(bounds[i + 1])}`;
}

// frameOf is the frame a card draws: the table's columns with the labels the card gives, a type's value named, a bin
// named by its range. It only names: every value stays as the server gave it.
export function frameOf(
  table: QueryTable,
  labels: Record<string, string>,
): Frame {
  const columns = table.columns
    .filter((c) => !c.list)
    .map((c): Column => {
      const named = c.ref === "type" || Boolean(c.bounds);
      return {
        ref: c.ref,
        label: labels[c.ref] ?? c.label,
        type: named ? "text" : c.type,
        unit: c.unit,
      };
    });
  const rows = table.rows.map((row) => {
    const out: Record<string, Cell> = {};
    for (const c of table.columns) {
      if (c.list) continue;
      const v = row[c.ref] as Cell;
      out[c.ref] =
        c.ref === "type" ? typeText(v) : c.bounds ? binText(c, v) : v;
    }
    return out;
  });
  return { columns, rows };
}

function Card({
  title,
  detail,
  wide,
  read,
  children,
  id,
}: {
  title: string;
  detail?: string;
  wide?: boolean;
  read: Read;
  id: string;
  children: (table: QueryTable) => ReactNode;
}) {
  return (
    <section
      className={`statistics-card${wide ? " wide" : ""}`}
      aria-label={title}
      data-card={id}
      aria-busy={read.state === "loading"}
    >
      <header>
        <h3>{title}</h3>
      </header>
      {detail && <p className="statistics-detail muted-text">{detail}</p>}
      {read.state === "loading" && (
        <p className="statistics-empty muted-text">{t("statistics.loading")}</p>
      )}
      {read.state === "error" && (
        <p className="statistics-empty statistics-error">
          {t("statistics.failed", { error: read.message })}
        </p>
      )}
      {read.state === "done" && children(read.table)}
    </section>
  );
}

function Empty({ text = t("statistics.none") }: { text?: string }) {
  return <p className="statistics-empty muted-text">{text}</p>;
}

// Figure is one of the six figures: a value, its label, and a line under it.
function Figure({
  id,
  label,
  read,
  value,
  sub,
}: {
  id: string;
  label: string;
  read: Read;
  value: (table: QueryTable) => string;
  sub: (table: QueryTable) => string;
}) {
  const table = read.state === "done" ? read.table : undefined;
  return (
    <div
      className="statistics-figure"
      data-card={id}
      aria-busy={read.state === "loading"}
    >
      <span className="statistics-figure-label">{label}</span>
      <strong className="statistics-figure-value">
        {table ? value(table) : read.state === "error" ? "!" : "…"}
      </strong>
      <span className="statistics-figure-sub">
        {table
          ? sub(table)
          : read.state === "error"
            ? t("statistics.failed", { error: read.message })
            : " "}
      </span>
    </div>
  );
}

// spanOf says a duration the server measured, a dash when absent.
function spanOf(table: QueryTable, ref: string): string {
  return valueText(columnOf(table, ref), cellOf(table, ref));
}

// StatisticsFigures are the six figures of the tab, each reading its query.
export function StatisticsFigures({ q }: { q: Record<string, QueryRequest> }) {
  const done = useQuery(q.done);
  const azimas = useQuery(q.azimas);
  const work = useQuery(q.work);
  const delivery = useQuery(q.delivery);
  const answer = useQuery(q.answer);
  const cost = useQuery(q.cost);
  const count = (table: QueryTable, status: string) =>
    cellOf(table, "count", { ref: "status", value: status }) ?? 0;
  const plain = (n: number | null) =>
    valueText({ ref: "n", label: "", type: "number" }, n);
  return (
    <div className="statistics-figures">
      <Figure
        id="done"
        label={t("statistics.done")}
        read={done}
        value={(tb) => plain(count(tb, "TASK_STATUS_DONE"))}
        sub={(tb) =>
          t("statistics.done_sub", {
            cancelled: plain(count(tb, "TASK_STATUS_CANCELLED")),
            failed: plain(count(tb, "TASK_STATUS_FAILED")),
          })
        }
      />
      <Figure
        id="azimas"
        label={t("statistics.azimas")}
        read={azimas}
        value={(tb) => plain(cellOf(tb, "merged") ?? 0)}
        sub={(tb) =>
          t("statistics.azimas_sub", { count: cellOf(tb, "count") ?? 0 })
        }
      />
      <Figure
        id="work"
        label={t("statistics.work")}
        read={work}
        value={(tb) => spanOf(tb, "running::median")}
        sub={(tb) =>
          t("statistics.median_p90", { p90: spanOf(tb, "running::p90") })
        }
      />
      <Figure
        id="delivery"
        label={t("statistics.delivery")}
        read={delivery}
        value={(tb) => spanOf(tb, "integration::delay::median")}
        sub={(tb) =>
          t("statistics.median_p90", {
            p90: spanOf(tb, "integration::delay::p90"),
          })
        }
      />
      <Figure
        id="answer"
        label={t("statistics.answer")}
        read={answer}
        value={(tb) => spanOf(tb, "answer::delay::median")}
        sub={(tb) =>
          t("statistics.median_p90", {
            p90: spanOf(tb, "answer::delay::p90"),
          })
        }
      />
      <Figure
        id="cost"
        label={t("statistics.cost")}
        read={cost}
        value={(tb) =>
          valueText(
            columnOf(tb, "usage.cost_usd::sum"),
            cellOf(tb, "usage.cost_usd::sum"),
          )
        }
        sub={() => t("statistics.cost_sub")}
      />
    </div>
  );
}

// ChartCard draws a card's table, or says it holds nothing.
function ChartCard({
  id,
  title,
  detail,
  wide,
  request,
  labels,
  spec,
  height,
  none,
}: {
  id: string;
  title: string;
  detail?: string;
  wide?: boolean;
  request: QueryRequest;
  labels: Record<string, string>;
  spec: ChartSpec;
  height?: number;
  // What the card says when it holds nothing.
  none?: string;
}) {
  const read = useQuery(request);
  const frame = useMemo(
    () =>
      read.state === "done"
        ? frameOf(read.table, labels)
        : { columns: [], rows: [] },
    [read, labels],
  );
  // A histogram keeps its empty bins: it holds nothing when every count is 0.
  const empty = !frame.rows.some((r) => (numberOf(r.count ?? null) ?? 0) > 0);
  return (
    <Card id={id} title={title} detail={detail} wide={wide} read={read}>
      {() =>
        empty ? (
          <Empty text={none} />
        ) : (
          <Chart frame={frame} spec={spec} label={title} height={height} />
        )
      }
    </Card>
  );
}

const TIME_REFS = [
  ["waiting_before_start", "statistics.time_wait"],
  ["running", "statistics.time_running"],
  ["waiting_gate", "statistics.time_gates"],
  ["paused", "statistics.time_paused"],
] as const;

// TimesCard: the time of a task by its type, a median and its p90; the running time drawn, every time in a table.
function TimesCard({ request }: { request: QueryRequest }) {
  const read = useQuery(request);
  const labels = useMemo(
    () => ({
      type: t("statistics.type"),
      "running::median": t("statistics.median"),
      "running::p90": t("statistics.p90"),
    }),
    [],
  );
  const frame = useMemo(
    () => (read.state === "done" ? frameOf(read.table, labels) : undefined),
    [read, labels],
  );
  return (
    <Card
      id="times"
      wide
      title={t("statistics.times")}
      detail={t("statistics.times_detail")}
      read={read}
    >
      {(table) =>
        !table.rows.length || !frame ? (
          <Empty />
        ) : (
          <>
            <Chart
              frame={frame}
              spec={{
                kind: "spread",
                category: "type",
                median: "running::median",
                p90: "running::p90",
                log: true,
              }}
              label={t("statistics.times")}
              height={40 + 28 * table.rows.length}
            />
            <table className="statistics-table">
              <thead>
                <tr>
                  <th>{t("statistics.type_count")}</th>
                  {TIME_REFS.map(([ref, key]) => (
                    <th key={ref}>{t(key)}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {table.rows.map((row) => (
                  <tr key={String(row.type)}>
                    <th>
                      {typeText(row.type as Cell)}{" "}
                      <span className="muted-text">
                        {numberOf(row.count as Cell) ?? 0}
                      </span>
                    </th>
                    {TIME_REFS.map(([ref]) => (
                      <td key={ref}>
                        {t("statistics.median_and_p90", {
                          median: valueText(
                            columnOf(table, `${ref}::median`),
                            numberOf(row[`${ref}::median`] as Cell),
                          ),
                          p90: valueText(
                            columnOf(table, `${ref}::p90`),
                            numberOf(row[`${ref}::p90`] as Cell),
                          ),
                        })}
                      </td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </>
        )
      }
    </Card>
  );
}

// ProjectsCard: a row per project, its tasks, their cost and the median time of their run; a click filters the tab.
function ProjectsCard({
  request,
  onProject,
}: {
  request: QueryRequest;
  onProject: (id: string) => void;
}) {
  const read = useQuery(request);
  return (
    <Card
      id="projects"
      wide
      title={t("statistics.projects")}
      detail={t("statistics.projects_detail")}
      read={read}
    >
      {(table) =>
        !table.rows.length ? (
          <Empty />
        ) : (
          <table className="statistics-table">
            <thead>
              <tr>
                <th>{t("statistics.project")}</th>
                <th>{t("statistics.tasks")}</th>
                <th>{t("statistics.cost")}</th>
                <th>{t("statistics.running_median")}</th>
              </tr>
            </thead>
            <tbody>
              {table.rows.map((row) => {
                const id = String(row.project_id ?? "");
                const name =
                  (row["project_id->name"] as string | null) ||
                  t("statistics.no_project");
                return (
                  <tr key={id || "none"}>
                    <th>
                      {id ? (
                        <button
                          type="button"
                          className="text-link statistics-project"
                          onClick={() => onProject(id)}
                        >
                          {name}
                        </button>
                      ) : (
                        name
                      )}
                    </th>
                    <td>{numberOf(row.count as Cell) ?? 0}</td>
                    <td>
                      {valueText(
                        columnOf(table, "usage.cost_usd::sum"),
                        numberOf(row["usage.cost_usd::sum"] as Cell),
                      )}
                    </td>
                    <td>
                      {valueText(
                        columnOf(table, "running::median"),
                        numberOf(row["running::median"] as Cell),
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )
      }
    </Card>
  );
}

// The filters last while the window lives: back on the tab, it reads the same.
let kept: Filters = { project: "", period: "30d", grain: "day" };

// Statistics is the tab: its filters, then its figures and cards, each reading its own query.
export function Statistics() {
  const projects = useData((s) => s.projects);
  const [filters, setFilters] = useState<Filters>(kept);
  const [readAt, setReadAt] = useState(() => new Date());
  const set = (patch: Partial<Filters>) => {
    kept = { ...filters, ...patch };
    setFilters(kept);
    setReadAt(new Date());
  };
  const q = useMemo(() => cardQueries(filters), [filters]);
  const counts = useMemo(() => ({ count: t("statistics.tasks") }), []);
  const binCounts = useMemo(() => ({ count: t("statistics.count") }), []);
  const merged = useMemo(() => ({ count: t("statistics.azimas_merged") }), []);
  const periods: [Period, string][] = [
    ["7d", t("statistics.period_7d")],
    ["30d", t("statistics.period_30d")],
    ["all", t("statistics.period_all")],
  ];
  const grains: [Grain, string][] = [
    ["day", t("statistics.grain_day")],
    ["week", t("statistics.grain_week")],
  ];
  return (
    <div className="statistics">
      <div className="statistics-filters">
        <label className="statistics-select">
          <span className="muted-text">{t("statistics.project")}</span>
          <select
            value={filters.project}
            onChange={(e) => set({ project: e.target.value })}
          >
            <option value="">{t("statistics.all_projects")}</option>
            {projects.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>
        </label>
        <div
          className="segmented-switch"
          role="group"
          aria-label={t("statistics.period")}
        >
          {periods.map(([id, label]) => (
            <button
              key={id}
              type="button"
              className={filters.period === id ? "active" : ""}
              aria-pressed={filters.period === id}
              onClick={() => set({ period: id })}
            >
              {label}
            </button>
          ))}
        </div>
        <div
          className="segmented-switch"
          role="group"
          aria-label={t("statistics.grain")}
        >
          {grains.map(([id, label]) => (
            <button
              key={id}
              type="button"
              className={filters.grain === id ? "active" : ""}
              aria-pressed={filters.grain === id}
              onClick={() => set({ grain: id })}
            >
              {label}
            </button>
          ))}
        </div>
        <span className="statistics-read muted-text">
          {t("statistics.read_at", { time: secondText(readAt) })}
        </span>
      </div>

      <StatisticsFigures q={q} />

      <div className="statistics-cards">
        <ChartCard
          id="created"
          wide
          title={t("statistics.created")}
          detail={t("statistics.created_detail")}
          request={q.created}
          labels={counts}
          spec={{
            kind: "bar",
            x: `create_time::${filters.grain}`,
            y: "count",
            series: "type",
            order: TYPE_ORDER,
            grain: filters.grain,
            stack: "stack",
          }}
        />
        <ChartCard
          id="merged"
          title={t("statistics.merged")}
          detail={t("statistics.merged_detail")}
          request={q.merged}
          labels={merged}
          spec={{
            kind: "bar",
            x: `azima.merged.time::${filters.grain}`,
            y: "count",
            grain: filters.grain,
          }}
        />
        <ChartCard
          id="deliveries"
          title={t("statistics.deliveries")}
          detail={t("statistics.deliveries_detail")}
          request={q.deliveries}
          labels={binCounts}
          spec={{
            kind: "bar",
            x: "integration::delay::bins",
            y: "count",
          }}
        />
        <TimesCard request={q.times} />
        <ChartCard
          id="answers"
          title={t("statistics.answers")}
          detail={t("statistics.answers_detail")}
          request={q.answers}
          labels={binCounts}
          spec={{
            kind: "bar",
            x: "answer::delay::bins",
            y: "count",
          }}
        />
        <ChartCard
          id="prompts"
          title={t("statistics.prompts")}
          detail={t("statistics.prompts_detail")}
          request={q.prompts}
          none={t("statistics.prompts_none")}
          labels={binCounts}
          spec={{
            kind: "bar",
            x: "prompt::size::bins",
            y: "count",
          }}
        />
        <ProjectsCard
          request={q.projects}
          onProject={(id) => set({ project: id })}
        />
      </div>
    </div>
  );
}

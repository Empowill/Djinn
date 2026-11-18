// The cascade band of a granted wish (variant A of L28, Q80 A): under the wish's header, one segment per azima of the
// freeing pass the grant starts (internal/harness/free.go), green when the azima is freed, hatched amber when the pass
// kept something of it, with what each freed or kept and why below; at the end it folds into a one-line summary, which
// opens the detail again. Only the display is held back, to a minimum pace (350 ms an azima, 2.5 s a cascade): the pass
// itself never waits for it.
import { useEffect, useState } from "react";
import {
  Check,
  ChevronDown,
  ChevronRight,
  Circle,
  CircleDot,
  Lock,
} from "lucide-react";

import {
  type DiskFreeing,
  type DiskFreeingKept,
  DiskFreeingPart,
  DiskFreeingReason,
} from "../gen/ts/machine/v1/machine_pb";
import { type Task, type Wish, WishState } from "../gen/ts/plan/v1/plan_pb";
import { useData, useStore } from "./data/djinn";
import { t } from "./i18n";
import { formatValue } from "./resource-chart";
import "./freeing-band.css";

// MIN_STEP is the least time an azima shows as the one being freed; MIN_TOTAL the least a whole cascade takes (L28).
export const MIN_STEP = 350;
export const MIN_TOTAL = 2500;
// SUMMARY_MS is how long the whole detail stays once the cascade is shown done, before it folds into its summary.
export const SUMMARY_MS = 8000;
// STALE_MS is how long after a pass ended the window may first see it and still play it: past it, the band opens
// folded (a wish opened long after its grant).
export const STALE_MS = 10_000;
// FAST_EVERY is how often the band reads the machine while a cascade of its wish runs, or a grant waits for one.
const FAST_EVERY = 500;
// GRANT_WAIT is how long after a grant the band waits for its cascade to show up.
const GRANT_WAIT = 20_000;

// Segment is an azima of a cascade, as the band shows it.
export type Segment = {
  key: string;
  code: string;
  title: string;
  total: number;
  done: number;
  freedBytes: number;
  kept: DiskFreeingKept[];
};

// cascadeOf is the cascade of wish in the pass freeing, null when the pass frees nothing of it: its azimas in the
// pass's order, named after tasks (the tasks of no azima as one more).
export function cascadeOf(
  freeing: DiskFreeing | undefined,
  wishId: string,
  tasks: readonly Task[],
): Segment[] | null {
  const azimas = (freeing?.azimas ?? []).filter((a) => a.wishId === wishId);
  if (!azimas.length) return null;
  const byId = new Map(tasks.map((task) => [task.id, task]));
  return azimas.map((a) => {
    const azima = a.azimaId ? byId.get(a.azimaId) : undefined;
    return {
      key: a.azimaId || "none",
      code: azima?.code ?? "",
      title: a.azimaId ? (azima?.title ?? "") : t("freeing.no_azima"),
      total: a.total,
      done: a.done,
      freedBytes: Number(a.freedBytes),
      kept: a.keptItems,
    };
  });
}

// stepOf is how long each of n azimas shows at least: 350 ms, stretched so that the cascade takes 2.5 s.
export function stepOf(n: number): number {
  return Math.max(MIN_STEP, MIN_TOTAL / Math.max(1, n));
}

// shownTimes is when each azima shows done, from when the band started and when it saw each done (undefined: not
// yet): never before it is done, nor before the one before it plus a step. Infinity for not yet known.
export function shownTimes(
  seen: readonly (number | undefined)[],
  start: number,
): number[] {
  const step = stepOf(seen.length);
  const out: number[] = [];
  let last = start;
  for (const at of seen) {
    last = Math.max(at ?? Infinity, last + step);
    out.push(last);
  }
  return out;
}

// Shown is what the band shows at a time: how many azimas done, which one is being freed (-1: none), whether it plays,
// shows its whole summary or is folded, and when that changes next (Infinity: not by time alone).
export type Shown = {
  done: number;
  now: number;
  phase: "playing" | "summary" | "folded";
  next: number;
};

export function shownAt(times: readonly number[], at: number): Shown {
  const done = times.filter((x) => x <= at).length;
  if (done < times.length)
    return { done, now: done, phase: "playing", next: times[done] };
  const end = (times.at(-1) ?? at) + SUMMARY_MS;
  if (at < end) return { done, now: -1, phase: "summary", next: end };
  return { done, now: -1, phase: "folded", next: Infinity };
}

// keptText says what the pass kept of a task and why, for one kept item.
export function keptText(k: DiskFreeingKept, code: string): string {
  const part = {
    [DiskFreeingPart.WORKTREE]: t("freeing.part_worktree"),
    [DiskFreeingPart.SCRATCH]: t("freeing.part_scratch"),
    [DiskFreeingPart.BRANCH]: t("freeing.part_branch"),
    [DiskFreeingPart.TASK]: t("freeing.part_task"),
    [DiskFreeingPart.UNSPECIFIED]: t("freeing.part_task"),
  }[k.part];
  let why: string;
  switch (k.reason) {
    case DiskFreeingReason.UNCOMMITTED:
      why = t("freeing.why_uncommitted");
      break;
    case DiskFreeingReason.UNPUSHED:
      why = t("freeing.why_unpushed", { count: k.commits });
      break;
    case DiskFreeingReason.RUNNING:
      why = t("freeing.why_running");
      break;
    case DiskFreeingReason.NO_PROJECT:
      why = t("freeing.why_no_project");
      break;
    default:
      why = k.detail;
  }
  return t("freeing.kept_line", { code: code || "—", part, why });
}

const size = (bytes: number) => formatValue("memory", bytes);

// summaryText is the cascade's one-line summary: what it freed, how many azimas it cleared, how many it kept of.
export function summaryText(segments: readonly Segment[]): string {
  const freed = segments.reduce((sum, s) => sum + s.freedBytes, 0);
  const kept = segments.filter((s) => s.kept.length > 0).length;
  const cleared = segments.length - kept;
  const parts = [
    t("freeing.summary_freed", { size: size(freed) }),
    t("freeing.summary_cleared", { count: cleared }),
  ];
  if (kept) parts.push(t("freeing.summary_kept", { count: kept }));
  return parts.join(" · ");
}

// FreeingBandView draws a cascade as shown: segments, the azimas' lines with what each freed or kept and why, and the
// summary; folded, one line that opens the detail.
export function FreeingBandView({
  segments,
  shown,
  open,
  codes,
  onToggle,
}: {
  segments: readonly Segment[];
  shown: Shown;
  open: boolean;
  codes: ReadonlyMap<string, string>;
  onToggle: () => void;
}) {
  const done = segments.slice(0, shown.done);
  const summary = summaryText(segments);
  if (shown.phase === "folded" && !open)
    return (
      <div className="freeing-band folded" data-phase="folded">
        <button className="freeing-fold" onClick={onToggle}>
          <ChevronRight size={14} aria-hidden="true" />
          <span className="freeing-label">{t("freeing.title")}</span>
          <span className="freeing-sum">{summary}</span>
        </button>
      </div>
    );
  const freed = done.reduce((sum, s) => sum + s.freedBytes, 0);
  return (
    <div className="freeing-band" data-phase={shown.phase}>
      <div className="freeing-top">
        {shown.phase === "folded" ? (
          <button className="freeing-fold" onClick={onToggle}>
            <ChevronDown size={14} aria-hidden="true" />
            <span className="freeing-label">{t("freeing.title")}</span>
          </button>
        ) : (
          <span className="freeing-label">{t("freeing.title")}</span>
        )}
        {shown.phase === "playing" && (
          <span className="freeing-count">
            {shown.done + 1} / {segments.length}
          </span>
        )}
        <span className="freeing-bytes">{size(freed)}</span>
      </div>
      <div className="freeing-segs" aria-hidden="true">
        {segments.map((s, i) => (
          <i
            key={s.key}
            className={[
              "freeing-seg",
              i < shown.done && "on",
              i === shown.now && "now",
              i < shown.done && s.kept.length > 0 && "kept",
            ]
              .filter(Boolean)
              .join(" ")}
          >
            <b />
          </i>
        ))}
      </div>
      <div className="freeing-rows">
        {segments.map((s, i) => {
          const isDone = i < shown.done;
          const kept = isDone && s.kept.length > 0;
          const state = isDone
            ? kept
              ? "kept"
              : "freed"
            : i === shown.now
              ? "now"
              : "wait";
          const Icon = {
            kept: Lock,
            freed: Check,
            now: CircleDot,
            wait: Circle,
          }[state];
          return (
            <div key={s.key} className={`freeing-row ${state}`}>
              <div className="freeing-line">
                <Icon size={14} aria-hidden="true" />
                <span className="freeing-code">{s.code}</span>
                <span className="freeing-what">{s.title}</span>
                <span className="freeing-amount">
                  {isDone
                    ? s.freedBytes > 0
                      ? size(s.freedBytes)
                      : t("freeing.nothing_freed")
                    : i === shown.now
                      ? "…"
                      : ""}
                </span>
              </div>
              {kept &&
                s.kept.map((k, j) => (
                  <div className="freeing-why" key={j}>
                    {keptText(k, codes.get(k.taskId) ?? "")}
                  </div>
                ))}
            </div>
          );
        })}
      </div>
      {shown.phase !== "playing" && (
        <div className="freeing-sum">{summary}</div>
      )}
    </div>
  );
}

// CascadePace is what the band remembers of the pass it plays: when it first saw it, and when it saw each azima done;
// it tells what to show at a time.
export class CascadePace {
  private pass = "";
  private start = 0;
  private seen = new Map<string, number>();
  private old = false;

  // observe takes the cascade of pass as read at at, ended at ended (undefined: still running), and says what to show.
  observe(
    pass: string,
    segments: readonly Segment[],
    ended: number | undefined,
    at: number = Date.now(),
  ): Shown {
    if (pass !== this.pass) {
      this.pass = pass;
      this.start = at;
      this.seen = new Map();
      this.old = ended !== undefined && at - ended > STALE_MS;
    }
    if (this.old)
      return {
        done: segments.length,
        now: -1,
        phase: "folded",
        next: Infinity,
      };
    for (const s of segments)
      if (s.done >= s.total && !this.seen.has(s.key)) this.seen.set(s.key, at);
    return shownAt(
      shownTimes(
        segments.map((s) => this.seen.get(s.key)),
        this.start,
      ),
      at,
    );
  }
}

// timeOf is a timestamp in milliseconds, undefined when unset.
function timeOf(ts?: { seconds: bigint; nanos: number }): number | undefined {
  return ts ? Number(ts.seconds) * 1000 + ts.nanos / 1e6 : undefined;
}

// FreeingBand follows the freeing pass of a granted wish, reading the machine twice a second while it runs, and plays
// it at the band's pace.
export function FreeingBand({
  wish,
  tasks,
}: {
  wish: Wish;
  tasks: readonly Task[];
}) {
  const store = useStore();
  const freeing = useData((s) => s.machine?.disk?.freeing);
  const segments = cascadeOf(freeing, wish.id, tasks);
  const granted = wish.state === WishState.GRANTED;
  const ended = timeOf(freeing?.endTime);
  const grantAt = timeOf(wish.grantTime);
  const [pace] = useState(() => new CascadePace());
  const [tick, setTick] = useState(0);
  const [open, setOpen] = useState(false);

  const shown =
    segments && granted
      ? pace.observe(
          `${wish.id}@${timeOf(freeing?.startTime) ?? 0}`,
          segments,
          ended,
        )
      : null;

  // While the cascade runs, or a grant waits for its pass (GRANT_WAIT at most), read the machine fast.
  const running = !!segments && ended === undefined;
  const [expired, setExpired] = useState<number | undefined>();
  useEffect(() => {
    if (grantAt === undefined) return;
    const id = setTimeout(
      () => setExpired(grantAt),
      Math.max(0, grantAt + GRANT_WAIT - Date.now()),
    );
    return () => clearTimeout(id);
  }, [grantAt]);
  const waiting =
    granted &&
    (running || (!segments && grantAt !== undefined && expired !== grantAt));
  useEffect(() => {
    if (!waiting) return;
    const id = setInterval(() => void store.readMachine(), FAST_EVERY);
    return () => clearInterval(id);
  }, [waiting, store]);

  // Until it folds, render again when what is shown changes by time.
  const next = shown?.next ?? Infinity;
  useEffect(() => {
    if (!Number.isFinite(next)) return;
    const id = setTimeout(
      () => setTick((n) => n + 1),
      Math.max(0, next - Date.now()),
    );
    return () => clearTimeout(id);
  }, [next, tick]);

  if (!segments || !shown) return null;
  const codes = new Map(tasks.map((task) => [task.id, task.code]));
  return (
    <FreeingBandView
      segments={segments}
      shown={shown}
      open={open}
      codes={codes}
      onToggle={() => setOpen((o) => !o)}
    />
  );
}

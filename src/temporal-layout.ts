import type { Agent, FlightEvent, Task } from "./types";
import { language, t } from "./i18n";

// Journal titles are written in the page's language; older journals hold French titles. These
// matches read both, plus the English source.
const isYou = (title: string) =>
  title === "you" || title === "vous" || title === t("chat.you").toLowerCase();
const mentions = (title: string, ...words: string[]) =>
  words.some((word) => title.includes(word));

/**
 * The timeline intentionally works from recorded timestamps only.  A missing
 * timestamp is dropped instead of being replaced with the current time.
 */
export function timestampMs(value: unknown): number | undefined {
  if (typeof value !== "string" || !value.trim()) return undefined;
  const parsed = Date.parse(value);
  return Number.isFinite(parsed) ? parsed : undefined;
}

export type IntervalEnd = "completed" | "blocked" | "running" | "open";

export interface TemporalInterval {
  id: string;
  agentId: string;
  runId?: string;
  start: string;
  startMs: number;
  end?: string;
  endMs?: number;
  endKind: IntervalEnd;
  lifecycle?: FlightEvent["lifecycle"];
  title: string;
  detail: string;
  worktree?: string;
  branch?: string;
  sourceEventIds: string[];
}

export interface TemporalActivityPoint {
  id: string;
  agentId?: string;
  runId?: string;
  time: string;
  timeMs: number;
  type: FlightEvent["type"];
  lifecycle?: FlightEvent["lifecycle"];
  title: string;
  detail: string;
  worktree?: string;
  branch?: string;
}

export type HumanInterventionKind = "instruction" | "decision" | "feedback";

export interface HumanIntervention {
  id: string;
  kind: HumanInterventionKind;
  time: string;
  timeMs: number;
  title: string;
  detail: string;
  agentId?: string;
  resolved?: boolean;
}

export interface TemporalLane {
  agentId: string;
  intervals: TemporalInterval[];
  activities: TemporalActivityPoint[];
}

export interface TemporalTick {
  timeMs: number;
  elapsedMs: number;
  ratio: number;
}

export interface TemporalAxis {
  startMs?: number;
  endMs?: number;
  durationMs: number;
  ticks: TemporalTick[];
}

export interface TemporalTimelineModel {
  axis: TemporalAxis;
  lanes: TemporalLane[];
  intervals: TemporalInterval[];
  activities: TemporalActivityPoint[];
  humanInterventions: HumanIntervention[];
  hasRecordedTimes: boolean;
  currentTimeMs?: number;
}

export interface TemporalLayoutOptions {
  /** Supply a stable clock in tests or a frame clock in the running view. */
  currentTime?: string | number | Date;
  /** Override the number of axis labels without changing the time scale. */
  tickCount?: number;
}

type EventWithIndex = { event: FlightEvent; index: number; timeMs: number };

type TemporalInstruction = {
  id: string;
  text: string;
  time?: string;
  agentId?: string;
};

type TemporalQuestion = {
  id: string;
  title?: string;
  answer?: string;
  answeredAt?: string;
  agentId?: string;
};

type TemporalFeedback = {
  id: string;
  text: string;
  createdAt?: string;
  /** Kept for imported sessions written before createdAt was standardized. */
  time?: string;
  resolved?: boolean;
};

type TemporalTask = Pick<
  Task,
  "status" | "agents" | "events" | "questions" | "feedback"
> & {
  instructions?: TemporalInstruction[];
  runId?: string;
};

const lifecycleValues = new Set<FlightEvent["lifecycle"]>([
  "started",
  "completed",
  "blocked",
]);

function eventSignature(entry: FlightEvent, timeMs: number): string {
  return [
    entry.id,
    entry.agentId || "",
    entry.runId || "",
    entry.lifecycle || "",
    timeMs,
  ].join("\u0000");
}

function recordedCurrentTime(
  value: TemporalLayoutOptions["currentTime"],
): number | undefined {
  if (value instanceof Date) {
    return Number.isFinite(value.getTime()) ? value.getTime() : undefined;
  }
  if (typeof value === "number")
    return Number.isFinite(value) ? value : undefined;
  return timestampMs(value);
}

function elapsedStep(durationMs: number, target: number): number {
  if (!durationMs) return 0;
  const rough = durationMs / Math.max(1, target - 1);
  const magnitude = 10 ** Math.floor(Math.log10(rough));
  const normalized = rough / magnitude;
  const factor =
    normalized <= 1 ? 1 : normalized <= 2 ? 2 : normalized <= 5 ? 5 : 10;
  return factor * magnitude;
}

export function formatElapsed(ms: number): string {
  const safe = Math.max(0, Math.round(ms));
  const totalSeconds = Math.floor(safe / 1000);
  const seconds = totalSeconds % 60;
  const totalMinutes = Math.floor(totalSeconds / 60);
  const minutes = totalMinutes % 60;
  const hours = Math.floor(totalMinutes / 60);
  if (hours) return `+${hours}h ${String(minutes).padStart(2, "0")}m`;
  if (minutes) return `+${minutes}m ${String(seconds).padStart(2, "0")}s`;
  return `+${seconds}s`;
}

export function formatClock(value: string | number | Date): string {
  const date =
    value instanceof Date
      ? value
      : new Date(typeof value === "number" ? value : value);
  if (!Number.isFinite(date.getTime())) return t("timeline.time_unavailable");
  return date.toLocaleTimeString(language, {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

export function ratioAt(
  valueMs: number,
  startMs?: number,
  endMs?: number,
): number {
  if (startMs === undefined || endMs === undefined) return 0;
  if (endMs <= startMs) return 0.5;
  return Math.max(0, Math.min(1, (valueMs - startMs) / (endMs - startMs)));
}

export function buildTicks(
  startMs: number | undefined,
  endMs: number | undefined,
  target = 6,
): TemporalTick[] {
  if (startMs === undefined || endMs === undefined) return [];
  const durationMs = Math.max(0, endMs - startMs);
  if (!durationMs) {
    return [{ timeMs: startMs, elapsedMs: 0, ratio: 0.5 }];
  }
  const step = elapsedStep(durationMs, Math.max(2, Math.round(target)));
  const ticks: TemporalTick[] = [];
  for (let elapsed = 0; elapsed <= durationMs + step * 0.001; elapsed += step) {
    ticks.push({
      timeMs: Math.min(endMs, startMs + elapsed),
      elapsedMs: Math.min(durationMs, elapsed),
      ratio: ratioAt(startMs + elapsed, startMs, endMs),
    });
    if (ticks.length > 30) break;
  }
  if (ticks[ticks.length - 1]?.timeMs !== endMs) {
    ticks.push({ timeMs: endMs, elapsedMs: durationMs, ratio: 1 });
  }
  return ticks;
}

function sortByTime<T extends { timeMs: number }>(items: T[]): T[] {
  return [...items].sort((a, b) => a.timeMs - b.timeMs);
}

function sortIntervals(items: TemporalInterval[]): TemporalInterval[] {
  return [...items].sort(
    (a, b) =>
      a.startMs - b.startMs || (a.endMs ?? a.startMs) - (b.endMs ?? b.startMs),
  );
}

function eventActivity(
  entry: FlightEvent,
  timeMs: number,
): TemporalActivityPoint {
  return {
    id: `activity:${entry.id}`,
    agentId: entry.agentId,
    runId: entry.runId,
    time: entry.time,
    timeMs,
    type: entry.type,
    lifecycle: entry.lifecycle,
    title: entry.title,
    detail: entry.detail,
    worktree: entry.worktree || undefined,
    branch: entry.branch || undefined,
  };
}

function isHumanJournalEvent(entry: FlightEvent): boolean {
  if (entry.actor === "human") return true;
  const title = entry.title.toLocaleLowerCase("fr-FR");
  return (
    isYou(title) ||
    title.startsWith("vous ") ||
    title.startsWith("you ") ||
    mentions(title, "comment", "commentaire") ||
    (entry.type === "review" && !entry.agentId)
  );
}

function interventionFingerprint(
  kind: HumanInterventionKind,
  timeMs: number,
  detail: string,
  agentId?: string,
): string {
  return [kind, timeMs, detail.trim(), agentId || ""].join("\u0000");
}

function isMirroredHumanEvent(
  entry: FlightEvent,
  intervention: HumanIntervention,
): boolean {
  const title = entry.title.toLocaleLowerCase("fr-FR");
  const detail = entry.detail.trim();
  if (intervention.kind === "instruction") {
    return (
      isYou(title) ||
      mentions(
        title,
        "instruction taken into account",
        "indication prise en compte",
        t("chat.event_instruction_applied").toLowerCase(),
      ) ||
      detail === intervention.detail.trim()
    );
  }
  if (intervention.kind === "decision") {
    return (
      title.startsWith(`${intervention.id.replace(/^question:/, "")} ·`) ||
      detail === intervention.detail.trim() ||
      (entry.type === "decision" && mentions(title, "decision", "décision"))
    );
  }
  return (
    mentions(title, "comment", "commentaire") ||
    title.includes("feedback") ||
    detail === intervention.detail.trim()
  );
}

function matchesInterventionReference(
  entry: FlightEvent,
  intervention: HumanIntervention,
): boolean {
  const reference = entry.interventionId;
  if (!reference) return false;
  if (reference === intervention.id) return true;
  if (intervention.kind === "instruction")
    return reference === intervention.id.replace(/^instruction:/, "");
  if (intervention.kind === "feedback")
    return reference === intervention.id.replace(/^feedback:/, "");
  return reference.startsWith(
    `${intervention.id.replace(/^question:/, "answer:")}:`,
  );
}

/**
 * Collects explicit human actions, then legacy journal actions only when they
 * are recognizable as human authored. Journal mirrors are removed by time and
 * content so one action has one marker.
 */
export function buildHumanInterventions(
  task: TemporalTask,
): HumanIntervention[] {
  const explicit: HumanIntervention[] = [];
  for (const instruction of task.instructions || []) {
    const timeMs = timestampMs(instruction.time);
    if (timeMs === undefined || !instruction.text.trim()) continue;
    explicit.push({
      id: `instruction:${instruction.id}`,
      kind: "instruction",
      time: instruction.time as string,
      timeMs,
      title: t("timeline.instruction"),
      detail: instruction.text,
      agentId: instruction.agentId,
    });
  }
  for (const question of task.questions || []) {
    const timeMs = timestampMs(question.answeredAt);
    if (timeMs === undefined || !question.answer?.trim()) continue;
    explicit.push({
      id: `question:${question.id}`,
      kind: "decision",
      time: question.answeredAt as string,
      timeMs,
      title: question.title || question.id,
      detail: question.answer,
      agentId: question.agentId,
    });
  }
  for (const feedback of (task.feedback || []) as TemporalFeedback[]) {
    const rawTime = feedback.createdAt || feedback.time;
    const timeMs = timestampMs(rawTime);
    if (timeMs === undefined || !feedback.text?.trim()) continue;
    explicit.push({
      id: `feedback:${feedback.id}`,
      kind: "feedback",
      time: rawTime as string,
      timeMs,
      title: t("timeline.review_feedback"),
      detail: feedback.text,
      resolved: feedback.resolved,
    });
  }

  const markers = [...explicit];
  const seen = new Set(
    explicit.map((item) =>
      interventionFingerprint(
        item.kind,
        item.timeMs,
        item.detail,
        item.agentId,
      ),
    ),
  );
  for (const entry of task.events || []) {
    const timeMs = timestampMs(entry.time);
    if (timeMs === undefined) continue;
    const title = entry.title.toLocaleLowerCase("fr-FR");
    const humanLike =
      entry.actor === "human" ||
      isYou(title) ||
      title.startsWith("vous ") ||
      title.startsWith("you ") ||
      mentions(title, "comment", "commentaire") ||
      (entry.type === "review" && !entry.agentId) ||
      entry.agentId === "human";
    if (!humanLike) continue;
    const kind: HumanInterventionKind =
      mentions(title, "comment", "commentaire") ||
      title.includes("feedback") ||
      entry.type === "review"
        ? "feedback"
        : mentions(title, "decision", "décision") || entry.type === "decision"
          ? "decision"
          : "instruction";
    const detail = entry.detail || entry.title;
    if (
      markers.some(
        (item) =>
          matchesInterventionReference(entry, item) ||
          isMirroredHumanEvent(entry, item),
      )
    )
      continue;
    const fingerprint = interventionFingerprint(
      kind,
      timeMs,
      detail,
      entry.agentId,
    );
    if (seen.has(fingerprint)) continue;
    seen.add(fingerprint);
    markers.push({
      id: `event:${entry.id}`,
      kind,
      time: entry.time,
      timeMs,
      title: entry.title,
      detail,
      agentId: entry.agentId,
    });
  }
  return sortByTime(markers);
}

function closeInterval(
  interval: TemporalInterval,
  terminal: EventWithIndex,
): boolean {
  if (interval.endMs !== undefined || terminal.timeMs < interval.startMs)
    return false;
  interval.end = terminal.event.time;
  interval.endMs = terminal.timeMs;
  interval.endKind =
    terminal.event.lifecycle === "blocked" ? "blocked" : "completed";
  interval.lifecycle = terminal.event.lifecycle;
  interval.sourceEventIds.push(terminal.event.id);
  interval.worktree ||= terminal.event.worktree || undefined;
  interval.branch ||= terminal.event.branch || undefined;
  return true;
}

function laneIds(
  task: TemporalTask,
  intervals: TemporalInterval[],
  activities: TemporalActivityPoint[],
): string[] {
  const ids = (task.agents || []).map((agent) => agent.id);
  for (const item of [...intervals, ...activities]) {
    const id = item.agentId || "__mission__";
    if (!ids.includes(id)) ids.push(id);
  }
  return ids;
}

function currentTimeForTask(
  task: TemporalTask,
  options: TemporalLayoutOptions,
): number | undefined {
  if (task.status !== "running") return undefined;
  return recordedCurrentTime(options.currentTime) ?? Date.now();
}

/**
 * Converts the task journal into one common elapsed-time coordinate system.
 * Started/completed/blocked lifecycle pairs are intervals. Everything else is
 * retained as an activity point at its recorded timestamp.
 */
export function buildTemporalLayout(
  task: TemporalTask,
  options: TemporalLayoutOptions = {},
): TemporalTimelineModel {
  const uniqueEvents = new Set<string>();
  const recordedEvents: EventWithIndex[] = [];
  for (const [index, entry] of (task.events || []).entries()) {
    const timeMs = timestampMs(entry.time);
    if (timeMs === undefined) continue;
    const signature = eventSignature(entry, timeMs);
    if (uniqueEvents.has(signature)) continue;
    uniqueEvents.add(signature);
    recordedEvents.push({ event: entry, index, timeMs });
  }
  recordedEvents.sort((a, b) => a.timeMs - b.timeMs || a.index - b.index);

  const intervals: TemporalInterval[] = [];
  const activities: TemporalActivityPoint[] = [];
  const openByKey = new Map<string, TemporalInterval[]>();
  const lifecycleEventIds = new Set<string>();

  const keyFor = (entry: FlightEvent) =>
    `${entry.runId || "legacy"}\u0000${entry.agentId || "mission"}`;
  const addActivity = (item: EventWithIndex) =>
    activities.push(eventActivity(item.event, item.timeMs));

  for (const item of recordedEvents) {
    const entry = item.event;
    // Human journal entries have their own row. Keeping them in an agent lane
    // would show the same intervention twice beside its deduplicated marker.
    if (isHumanJournalEvent(entry)) continue;
    if (
      !entry.lifecycle ||
      !lifecycleValues.has(entry.lifecycle) ||
      !entry.agentId
    ) {
      addActivity(item);
      continue;
    }
    const key = keyFor(entry);
    const stack = openByKey.get(key) || [];
    if (entry.lifecycle === "started") {
      const duplicate = stack.find(
        (interval) => interval.startMs === item.timeMs,
      );
      if (duplicate) {
        duplicate.sourceEventIds.push(entry.id);
        lifecycleEventIds.add(entry.id);
        continue;
      }
      const interval: TemporalInterval = {
        id: `interval:${entry.id}`,
        agentId: entry.agentId,
        runId: entry.runId,
        start: entry.time,
        startMs: item.timeMs,
        endKind: "open",
        lifecycle: entry.lifecycle,
        title: entry.title,
        detail: entry.detail,
        worktree: entry.worktree || undefined,
        branch: entry.branch || undefined,
        sourceEventIds: [entry.id],
      };
      intervals.push(interval);
      stack.push(interval);
      openByKey.set(key, stack);
      lifecycleEventIds.add(entry.id);
      continue;
    }
    const open = [...stack]
      .reverse()
      .find((interval) => interval.endMs === undefined);
    if (!open) {
      const duplicateTerminal = intervals.some(
        (interval) =>
          interval.agentId === entry.agentId &&
          interval.runId === entry.runId &&
          interval.endMs === item.timeMs &&
          interval.lifecycle === entry.lifecycle,
      );
      if (duplicateTerminal) continue;
      addActivity(item);
      continue;
    }
    if (closeInterval(open, item)) {
      lifecycleEventIds.add(entry.id);
      openByKey.set(
        key,
        stack.filter((interval) => interval.endMs === undefined),
      );
    } else {
      // Preserve an out-of-order terminal event as a point; never stretch a
      // bar backwards to make malformed input look like a valid run.
      addActivity(item);
    }
  }

  const currentTimeMs = currentTimeForTask(task, options);
  if (currentTimeMs !== undefined) {
    for (const interval of intervals) {
      if (interval.endMs !== undefined || interval.startMs > currentTimeMs)
        continue;
      if (interval.endKind !== "open") continue;
      interval.endMs = currentTimeMs;
      interval.endKind = "running";
    }
  }

  // Lifecycle events that had no matching start are already activity points.
  // Keep the variable as a deliberate reminder that unmatched terminal events
  // must never be turned into fabricated intervals.
  void lifecycleEventIds;

  const laneById = new Map<string, TemporalLane>();
  for (const agentId of laneIds(task, intervals, activities))
    laneById.set(agentId, { agentId, intervals: [], activities: [] });
  for (const interval of intervals)
    laneById.get(interval.agentId)?.intervals.push(interval);
  for (const activity of activities) {
    laneById.get(activity.agentId || "__mission__")?.activities.push(activity);
  }
  const lanes = [...laneById.values()].map((lane) => ({
    ...lane,
    intervals: sortIntervals(lane.intervals),
    activities: sortByTime(lane.activities),
  }));

  const humanInterventions = buildHumanInterventions(task);
  const allTimes = [
    ...intervals.flatMap((interval) => [interval.startMs, interval.endMs]),
    ...activities.map((activity) => activity.timeMs),
    ...humanInterventions.map((item) => item.timeMs),
  ].filter(
    (time): time is number => typeof time === "number" && Number.isFinite(time),
  );
  const startMs = allTimes.length ? Math.min(...allTimes) : undefined;
  const endMs = allTimes.length ? Math.max(...allTimes) : undefined;
  const durationMs =
    startMs === undefined || endMs === undefined
      ? 0
      : Math.max(0, endMs - startMs);

  return {
    axis: {
      startMs,
      endMs,
      durationMs,
      ticks: buildTicks(startMs, endMs, options.tickCount),
    },
    lanes,
    intervals: sortIntervals(intervals),
    activities: sortByTime(activities),
    humanInterventions,
    hasRecordedTimes: allTimes.length > 0,
    currentTimeMs,
  };
}

export const buildTimelineLayout = buildTemporalLayout;
export const deriveTemporalLayout = buildTemporalLayout;

export function intervalGeometry(
  interval: TemporalInterval,
  axis: TemporalAxis,
): { left: number; width: number } {
  const left = ratioAt(interval.startMs, axis.startMs, axis.endMs) * 100;
  const end = interval.endMs ?? interval.startMs;
  const right = ratioAt(end, axis.startMs, axis.endMs) * 100;
  return { left: Math.min(left, right), width: Math.max(0, right - left) };
}

export function pointPosition(timeMs: number, axis: TemporalAxis): number {
  return ratioAt(timeMs, axis.startMs, axis.endMs) * 100;
}

export function temporalCanvasWidth(
  durationMs: number,
  scale = 1,
  minimum = 680,
  pixelsPerMinute = 7,
): number {
  return Math.max(
    minimum,
    (Math.max(0, durationMs) / 60000) * pixelsPerMinute * scale,
  );
}

/** Greedy row allocation keeps concurrent intervals legible within an agent lane. */
export function allocateIntervalRows(
  intervals: TemporalInterval[],
): Map<string, number> {
  const rows: number[] = [];
  const result = new Map<string, number>();
  for (const interval of sortIntervals(intervals)) {
    const start = interval.startMs;
    // An unclosed static interval has no known end. Keep its lane occupied so
    // another recorded start cannot visually overwrite it.
    const end = interval.endMs ?? Number.POSITIVE_INFINITY;
    let row = rows.findIndex((lastEnd) => lastEnd <= start);
    if (row === -1) {
      row = rows.length;
      rows.push(end);
    } else rows[row] = end;
    result.set(interval.id, row);
  }
  return result;
}

export function agentById(
  task: Pick<Task, "agents">,
  id: string,
): Agent | undefined {
  return task.agents.find((agent) => agent.id === id);
}

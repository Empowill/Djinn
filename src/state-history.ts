import type { AppState, FlightEvent, Task } from "./types";
import { t } from "./i18n";

export const MAX_TOOL_EVENT_DETAIL_LENGTH = 4_000;
export const MAX_TOOL_DETAIL_BUDGET = 1_000_000;
export const ARCHIVE_MARKER = t("history.archive_marker");

type StateHistoryState = {
  tasks: Array<Pick<Task, "events">>;
};

export class StateHistoryError extends Error {
  code = "invalid_state";

  constructor(message: string) {
    super(message);
    this.name = "StateHistoryError";
  }
}

function record(value: unknown, label: string): Record<string, any> {
  if (!value || typeof value !== "object" || Array.isArray(value))
    throw new StateHistoryError(`${label} must be an object`);
  return value as Record<string, any>;
}

function eventTime(event: Record<string, any>): number {
  const parsed = typeof event.time === "string" ? Date.parse(event.time) : NaN;
  return Number.isFinite(parsed) ? parsed : Number.NEGATIVE_INFINITY;
}

/** Keep the marker whenever the available budget can contain it. */
export function archiveDetail(
  detail: string,
  budget: number,
  maxDetailLength = MAX_TOOL_EVENT_DETAIL_LENGTH,
): string {
  const available = Math.min(Math.max(0, budget), Math.max(0, maxDetailLength));
  if (available === 0) return "";
  const markerLength = ARCHIVE_MARKER.length + 1;
  if (available < markerLength) return ARCHIVE_MARKER.slice(0, available);
  const excerptLength = Math.min(detail.length, available - markerLength);
  return `${detail.slice(0, excerptLength)} ${ARCHIVE_MARKER}`;
}

/**
 * Compact only non-human provider tool details. The mission journal retains
 * complete output, so history compaction may replace old output with a clear
 * French archive marker. The result shares all unchanged branches with the
 * input and is the input itself when no detail needs changing.
 */
export function compactStateHistory<T extends StateHistoryState | AppState>(
  value: T,
  options: {
    maxDetailLength?: number;
    detailBudget?: number;
  } = {},
): T {
  const root = record(value, "state");
  if (!Array.isArray(root.tasks))
    throw new StateHistoryError("state.tasks must be an array");
  const maxDetailLength =
    options.maxDetailLength ?? MAX_TOOL_EVENT_DETAIL_LENGTH;
  const detailBudget = options.detailBudget ?? MAX_TOOL_DETAIL_BUDGET;
  if (
    !Number.isInteger(maxDetailLength) ||
    maxDetailLength < 0 ||
    !Number.isInteger(detailBudget) ||
    detailBudget < 0
  )
    throw new StateHistoryError("Invalid history compaction limits");

  const candidates: Array<{
    taskIndex: number;
    eventIndex: number;
    event: Record<string, any>;
    index: number;
  }> = [];
  for (let taskIndex = 0; taskIndex < root.tasks.length; taskIndex += 1) {
    const task = record(root.tasks[taskIndex], `state.tasks.${taskIndex}`);
    if (!Array.isArray(task.events))
      throw new StateHistoryError(
        `state.tasks.${taskIndex}.events must be an array`,
      );
    for (let eventIndex = 0; eventIndex < task.events.length; eventIndex += 1) {
      const event = record(
        task.events[eventIndex],
        `state.tasks.${taskIndex}.events.${eventIndex}`,
      );
      if (event.type !== "tool" || event.actor === "human") continue;
      if (typeof event.detail !== "string")
        throw new StateHistoryError(
          `state.tasks.${taskIndex}.events.${eventIndex}.detail must be a string`,
        );
      candidates.push({
        taskIndex,
        eventIndex,
        event,
        index: candidates.length,
      });
    }
  }

  candidates.sort(
    (left, right) =>
      eventTime(right.event) - eventTime(left.event) ||
      left.index - right.index,
  );
  let remaining = detailBudget;
  const replacements = new Map<number, Map<number, string>>();
  for (const candidate of candidates) {
    const detail = candidate.event.detail as string;
    const compacted =
      detail.length <= maxDetailLength && detail.length <= remaining
        ? detail
        : archiveDetail(detail, remaining, maxDetailLength);
    if (compacted !== detail) {
      let taskReplacements = replacements.get(candidate.taskIndex);
      if (!taskReplacements) {
        taskReplacements = new Map();
        replacements.set(candidate.taskIndex, taskReplacements);
      }
      taskReplacements.set(candidate.eventIndex, compacted);
    }
    remaining -= compacted.length;
  }
  if (replacements.size === 0) return value;

  const tasks = [...root.tasks];
  for (const [taskIndex, taskReplacements] of replacements) {
    const task = root.tasks[taskIndex] as Record<string, any>;
    const events = [...task.events];
    for (const [eventIndex, detail] of taskReplacements)
      events[eventIndex] = { ...events[eventIndex], detail };
    tasks[taskIndex] = { ...task, events };
  }
  return { ...root, tasks } as T;
}

// Keep the AppState import part of the public type contract without forcing
// runtime code to load the renderer's type-only module.
export type CompactibleAppState = AppState;
export type CompactibleFlightEvent = FlightEvent;

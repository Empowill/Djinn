// Generated from src/state-history.ts. Keep both in sync.
"use strict";

const MAX_TOOL_EVENT_DETAIL_LENGTH = 4_000;
const MAX_TOOL_DETAIL_BUDGET = 1_000_000;
const ARCHIVE_MARKER =
  "[Détail complet archivé dans le journal de mission.]";

class StateHistoryError extends Error {
  constructor(message) {
    super(message);
    this.name = "StateHistoryError";
    this.code = "invalid_state";
  }
}

function record(value, label) {
  if (!value || typeof value !== "object" || Array.isArray(value))
    throw new StateHistoryError(`${label} must be an object`);
  return value;
}

function eventTime(event) {
  const parsed = typeof event.time === "string" ? Date.parse(event.time) : NaN;
  return Number.isFinite(parsed) ? parsed : Number.NEGATIVE_INFINITY;
}

function archiveDetail(
  detail,
  budget,
  maxDetailLength = MAX_TOOL_EVENT_DETAIL_LENGTH,
) {
  const available = Math.min(Math.max(0, budget), Math.max(0, maxDetailLength));
  if (available === 0) return "";
  const markerLength = ARCHIVE_MARKER.length + 1;
  if (available < markerLength)
    return ARCHIVE_MARKER.slice(0, available);
  const excerptLength = Math.min(
    detail.length,
    available - markerLength,
  );
  return `${detail.slice(0, excerptLength)} ${ARCHIVE_MARKER}`;
}

function compactStateHistory(value, options = {}) {
  const root = record(value, "state");
  if (!Array.isArray(root.tasks))
    throw new StateHistoryError("state.tasks must be an array");
  const maxDetailLength =
    options.maxDetailLength === undefined
      ? MAX_TOOL_EVENT_DETAIL_LENGTH
      : options.maxDetailLength;
  const detailBudget =
    options.detailBudget === undefined
      ? MAX_TOOL_DETAIL_BUDGET
      : options.detailBudget;
  if (
    !Number.isInteger(maxDetailLength) ||
    maxDetailLength < 0 ||
    !Number.isInteger(detailBudget) ||
    detailBudget < 0
  )
    throw new StateHistoryError("Invalid history compaction limits");

  const candidates = [];
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
      candidates.push({ taskIndex, eventIndex, event, index: candidates.length });
    }
  }
  candidates.sort(
    (left, right) =>
      eventTime(right.event) - eventTime(left.event) || left.index - right.index,
  );
  let remaining = detailBudget;
  const replacements = new Map();
  for (const candidate of candidates) {
    const detail = candidate.event.detail;
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
    const task = root.tasks[taskIndex];
    const events = [...task.events];
    for (const [eventIndex, detail] of taskReplacements)
      events[eventIndex] = { ...events[eventIndex], detail };
    tasks[taskIndex] = { ...task, events };
  }
  return { ...root, tasks };
}

module.exports = {
  ARCHIVE_MARKER,
  MAX_TOOL_DETAIL_BUDGET,
  MAX_TOOL_EVENT_DETAIL_LENGTH,
  StateHistoryError,
  archiveDetail,
  compactStateHistory,
};

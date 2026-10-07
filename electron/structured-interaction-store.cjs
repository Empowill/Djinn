"use strict";

const crypto = require("node:crypto");
const fsp = require("node:fs").promises;
const path = require("node:path");

const MAX_TASK_ID_LENGTH = 256;
const MAX_EVENTS = 256;
const MAX_BYTES = 12_000_000;
const MAX_STRING = 16_000;

function storeError(code, message, cause) {
  const error = new Error(message, cause === undefined ? undefined : { cause });
  error.code = code;
  error.isStructuredInteractionStoreError = true;
  if (cause !== undefined) error.cause = cause;
  return error;
}

function validateTaskId(value) {
  if (typeof value !== "string")
    throw storeError("invalid_task_id", "taskId must be a string");
  const result = value.trim();
  if (!result || result.length > MAX_TASK_ID_LENGTH || result.includes("\u0000"))
    throw storeError("invalid_task_id", "taskId is invalid");
  return result;
}

function compact(value, depth = 0) {
  if (typeof value === "string")
    return value.length > MAX_STRING ? `${value.slice(0, MAX_STRING)}…` : value;
  if (Array.isArray(value))
    return value.slice(0, 100).map((entry) => compact(entry, depth + 1));
  if (!value || typeof value !== "object" || depth > 6) return value;
  return Object.fromEntries(
    Object.entries(value).map(([key, entry]) => [key, compact(entry, depth + 1)]),
  );
}

function keyFor(envelope) {
  const data = envelope?.data && typeof envelope.data === "object" ? envelope.data : {};
  if (envelope?.type === "step_result")
    return String(data.stepId || envelope.stepId || data.runId || envelope.runId || envelope.eventId);
  return String(data.id || data.workItemId || data.runId || envelope.runId || envelope.eventId);
}

function isSupportedEnvelope(envelope) {
  return (
    envelope &&
    typeof envelope === "object" &&
    !Array.isArray(envelope) &&
    ["question", "artifact", "action", "work_item", "report", "step_result"].includes(
      envelope.type,
    )
  );
}

function normalizeEnvelope(envelope) {
  if (!isSupportedEnvelope(envelope)) return null;
  const data =
    envelope.data && typeof envelope.data === "object"
      ? compact(envelope.data)
      : envelope.data;
  if (
    envelope.type === "artifact" &&
    envelope.data &&
    typeof envelope.data === "object" &&
    typeof envelope.data.content === "string"
  )
    data.content = envelope.data.content;
  return { ...envelope, data };
}

function trimEvents(events) {
  const map = new Map();
  for (const event of Array.isArray(events) ? events : []) {
    const normalized = normalizeEnvelope(event);
    if (!normalized) continue;
    const key = `${normalized.type}:${keyFor(normalized)}`;
    map.delete(key);
    map.set(key, normalized);
  }
  const size = () =>
    [...map.values()].reduce((total, event) => total + JSON.stringify(event).length, 0);
  while (map.size > MAX_EVENTS || size() > MAX_BYTES)
    map.delete(map.keys().next().value);
  return [...map.values()];
}

class StructuredInteractionStore {
  constructor(directory) {
    if (typeof directory !== "string" || !path.isAbsolute(directory))
      throw storeError("invalid_directory", "Structured interaction directory must be absolute");
    this.directory = path.resolve(directory);
    this.queues = new Map();
  }

  _file(taskId) {
    const id = validateTaskId(taskId);
    const name = `${crypto.createHash("sha256").update(id, "utf8").digest("hex")}.json`;
    const file = path.join(this.directory, name);
    if (path.dirname(file) !== this.directory)
      throw storeError("invalid_path", "Structured interaction path escaped its directory");
    return { id, file };
  }

  _enqueue(file, operation) {
    const previous = this.queues.get(file) || Promise.resolve();
    const current = previous.catch(() => undefined).then(operation);
    let tracked;
    tracked = current.finally(() => {
      if (this.queues.get(file) === tracked) this.queues.delete(file);
    });
    this.queues.set(file, tracked);
    return tracked;
  }

  async _read(file) {
    let text;
    try {
      text = await fsp.readFile(file, "utf8");
    } catch (error) {
      if (error?.code === "ENOENT") return [];
      throw storeError("read_failed", `Unable to read structured interactions: ${error.message}`, error);
    }
    try {
      const value = JSON.parse(text);
      return trimEvents(value?.version === 1 ? value.events : value);
    } catch (error) {
      throw storeError("corrupt_store", `Structured interactions are invalid: ${error.message}`, error);
    }
  }

  async upsert(taskId, envelope) {
    const { id, file } = this._file(taskId);
    const normalized = normalizeEnvelope(envelope);
    if (!normalized || normalized.taskId !== undefined && normalized.taskId !== id)
      throw storeError("invalid_envelope", "Structured interaction envelope is invalid");
    return this._enqueue(file, async () => {
      const events = trimEvents([...(await this._read(file)), normalized]);
      await fsp.mkdir(this.directory, { recursive: true, mode: 0o700 });
      const temporary = `${file}.${process.pid}.${crypto.randomUUID()}.tmp`;
      try {
        await fsp.writeFile(
          temporary,
          `${JSON.stringify({ version: 1, taskId: id, events })}\n`,
          { encoding: "utf8", mode: 0o600, flag: "wx" },
        );
        await fsp.rename(temporary, file);
      } catch (error) {
        try {
          await fsp.unlink(temporary);
        } catch {
          // The temporary file may not have been created.
        }
        throw storeError("write_failed", `Unable to save structured interactions: ${error.message}`, error);
      }
    });
  }

  async read(taskId) {
    const { id, file } = this._file(taskId);
    await this.flush(id);
    return { taskId: id, events: await this._read(file) };
  }

  async flush(taskId) {
    if (taskId !== undefined) {
      const { file } = this._file(taskId);
      while (this.queues.has(file)) await this.queues.get(file);
      return;
    }
    while (this.queues.size) await Promise.all([...this.queues.values()]);
  }
}

module.exports = {
  StructuredInteractionStore,
  MAX_EVENTS,
  MAX_BYTES,
  MAX_STRING,
  trimEvents,
  normalizeEnvelope,
};

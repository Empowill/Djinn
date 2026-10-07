"use strict";

const fs = require("node:fs");
const fsp = fs.promises;
const path = require("node:path");
const crypto = require("node:crypto");
const readline = require("node:readline");

const MAX_PAGE_SIZE = 200;
const DEFAULT_PAGE_SIZE = MAX_PAGE_SIZE;
const MAX_TASK_ID_LENGTH = 256;
const JOURNAL_DIRECTORY = "mission-journal";

function journalError(code, message, cause) {
  const error = new Error(message, cause === undefined ? undefined : { cause });
  error.code = code;
  error.isMissionJournalError = true;
  if (cause !== undefined) error.cause = cause;
  return error;
}

function isJournalError(error) {
  return error?.isMissionJournalError === true;
}

function validateTaskId(value) {
  if (typeof value !== "string")
    throw journalError("invalid_task_id", "taskId must be a string");
  const result = value.trim();
  if (!result)
    throw journalError("invalid_task_id", "taskId must not be empty");
  if (result.length > MAX_TASK_ID_LENGTH)
    throw journalError("invalid_task_id", "taskId exceeds the 256 character limit");
  if (result.includes("\u0000"))
    throw journalError("invalid_task_id", "taskId contains a NUL character");
  return result;
}

function validateLimit(value) {
  if (value === undefined) return DEFAULT_PAGE_SIZE;
  if (!Number.isInteger(value) || value < 1 || value > MAX_PAGE_SIZE)
    throw journalError("invalid_limit", `limit must be an integer between 1 and ${MAX_PAGE_SIZE}`);
  return value;
}

function validateCursor(value) {
  if (value === undefined) return 0;
  if (!Number.isSafeInteger(value) || value < 0)
    throw journalError("invalid_cursor", "cursor must be a non-negative line number");
  return value;
}

function assertDirectory(directory) {
  if (typeof directory !== "string" || !path.isAbsolute(directory))
    throw journalError("invalid_journal_directory", "Journal directory must be absolute");
  return path.resolve(directory);
}

function hashTaskId(taskId) {
  return crypto.createHash("sha256").update(taskId, "utf8").digest("hex");
}

function isMissing(error) {
  return error && error.code === "ENOENT";
}

class MissionJournal {
  constructor(directory, options = {}) {
    const base = assertDirectory(directory);
    this.directory =
      options.userData === true ? path.join(base, JOURNAL_DIRECTORY) : base;
    this._queues = new Map();
    this._pending = new Set();
  }

  static forUserData(userDataDirectory) {
    return new MissionJournal(userDataDirectory, { userData: true });
  }

  _filePath(taskId) {
    const id = validateTaskId(taskId);
    const file = path.join(this.directory, `${hashTaskId(id)}.jsonl`);
    if (path.dirname(file) !== this.directory)
      throw journalError("invalid_journal_path", "Journal path escaped its directory");
    return { id, file };
  }

  async _lstat(file) {
    try {
      const info = await fsp.lstat(file);
      if (!info.isFile() || info.isSymbolicLink())
        throw journalError("invalid_journal_path", "Journal path is not a regular file");
      return info;
    } catch (error) {
      if (isMissing(error)) return null;
      if (isJournalError(error)) throw error;
      throw journalError("journal_stat_failed", `Unable to inspect mission journal: ${error.message}`, error);
    }
  }

  _serialize(taskId, envelope) {
    if (!envelope || typeof envelope !== "object" || Array.isArray(envelope))
      throw journalError("invalid_envelope", "Journal envelope must be an object");
    if (
      envelope.taskId !== undefined &&
      envelope.taskId !== null &&
      envelope.taskId !== taskId
    )
      throw journalError("invalid_envelope", "Envelope taskId does not match journal taskId");
    let serialized;
    try {
      serialized = JSON.stringify(envelope);
    } catch (error) {
      throw journalError("invalid_envelope", `Journal envelope is not JSON serializable: ${error.message}`, error);
    }
    if (!serialized || serialized[0] !== "{")
      throw journalError("invalid_envelope", "Journal envelope must serialize to a JSON object");
    return `${serialized}\n`;
  }

  async _write(file, line) {
    try {
      await fsp.mkdir(this.directory, { recursive: true, mode: 0o700 });
      await this._lstat(file);
      const noFollow = Number.isInteger(fs.constants.O_NOFOLLOW)
        ? fs.constants.O_NOFOLLOW
        : 0;
      const flags =
        fs.constants.O_WRONLY |
        fs.constants.O_APPEND |
        fs.constants.O_CREAT |
        noFollow;
      const handle = await fsp.open(file, flags, 0o600);
      let failure;
      try {
        const info = await handle.stat();
        if (!info.isFile())
          throw journalError("invalid_journal_path", "Journal path is not a regular file");
        await handle.writeFile(line, "utf8");
        await handle.sync();
      } catch (error) {
        failure = error;
      }
      try {
        await handle.close();
      } catch (error) {
        failure ||= error;
      }
      if (failure) throw failure;
    } catch (error) {
      if (isJournalError(error)) throw error;
      throw journalError("journal_append_failed", `Unable to append mission journal: ${error.message}`, error);
    }
  }

  _enqueue(file, operation) {
    const previous = this._queues.get(file) || Promise.resolve();
    const run = previous.catch(() => undefined).then(operation);
    let tracked;
    tracked = run.finally(() => {
      this._pending.delete(tracked);
      if (this._queues.get(file) === tracked) this._queues.delete(file);
    });
    this._queues.set(file, tracked);
    this._pending.add(tracked);
    return tracked;
  }

  async append(taskId, envelope) {
    const { id, file } = this._filePath(taskId);
    const line = this._serialize(id, envelope);
    await this._enqueue(file, () => this._write(file, line));
  }

  async _flushFile(file) {
    while (this._queues.has(file)) await this._queues.get(file);
  }

  async flush(taskId) {
    if (taskId !== undefined) {
      const { file } = this._filePath(taskId);
      await this._flushFile(file);
      return;
    }
    while (this._pending.size) await Promise.all([...this._pending]);
  }

  async close() {
    await this.flush();
  }

  async readPage(taskId, options = {}) {
    const { id, file } = this._filePath(taskId);
    if (!options || typeof options !== "object" || Array.isArray(options))
      throw journalError("invalid_page", "Page options must be an object");
    const cursor = validateCursor(options.cursor);
    const limit = validateLimit(options.limit);
    await this._flushFile(file);
    if (!(await this._lstat(file)))
      return { taskId: id, cursor, events: [], nextCursor: null, hasMore: false };

    const events = [];
    let lineNumber = 0;
    let hasMore = false;
    let stream;
    let input;
    try {
      stream = fs.createReadStream(file, { encoding: "utf8" });
      input = readline.createInterface({ input: stream, crlfDelay: Infinity });
      for await (const line of input) {
        const current = lineNumber++;
        if (current < cursor) continue;
        if (events.length >= limit) {
          hasMore = true;
          break;
        }
        if (!line)
          throw journalError("journal_corrupt", `Empty journal line at ${current}`);
        try {
          events.push(JSON.parse(line));
        } catch (error) {
          throw journalError("journal_corrupt", `Invalid journal JSON at line ${current}`, error);
        }
      }
    } catch (error) {
      if (isJournalError(error)) throw error;
      throw journalError("journal_read_failed", `Unable to read mission journal: ${error.message}`, error);
    } finally {
      input?.close();
      stream?.destroy();
    }
    return {
      taskId: id,
      cursor,
      events,
      nextCursor: hasMore ? cursor + events.length : null,
      hasMore,
    };
  }

  async stat(taskId) {
    const { id, file } = this._filePath(taskId);
    await this._flushFile(file);
    const info = await this._lstat(file);
    if (!info)
      return { taskId: id, exists: false, bytes: 0, modifiedAt: null };
    return {
      taskId: id,
      exists: true,
      bytes: info.size,
      modifiedAt: info.mtime.toISOString(),
    };
  }
}

module.exports = {
  MissionJournal,
  MAX_PAGE_SIZE,
  DEFAULT_PAGE_SIZE,
  MAX_TASK_ID_LENGTH,
  validateTaskId,
  hashTaskId,
};

"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const fsp = fs.promises;
const os = require("node:os");
const path = require("node:path");
const crypto = require("node:crypto");
const {
  MissionJournal,
  MAX_PAGE_SIZE,
} = require("../electron/mission-journal.cjs");

function temporaryDirectory(prefix = "djinn-journal-") {
  return fs.mkdtempSync(path.join(os.tmpdir(), prefix));
}

test("terminal envelopes survive flush, store close, and store recreation", async () => {
  const directory = temporaryDirectory();
  try {
    const first = new MissionJournal(directory);
    await first.append("mission-final", {
      eventId: "event-delta",
      taskId: "mission-final",
      type: "text",
      data: { streaming: true, text: "partial" },
    });
    await first.append("mission-final", {
      eventId: "event-terminal",
      taskId: "mission-final",
      type: "status",
      data: { status: "completed" },
    });
    await first.close();

    const second = new MissionJournal(directory);
    const page = await second.readPage("mission-final");
    assert.deepEqual(
      page.events.map((event) => event.eventId),
      ["event-delta", "event-terminal"],
    );
    assert.equal(page.nextCursor, null);
    const info = await second.stat("mission-final");
    assert.equal(info.exists, true);
    assert.ok(info.bytes > 0);
  } finally {
    fs.rmSync(directory, { recursive: true, force: true });
  }
});

test("readPage paginates by JSONL line cursor and never truncates history", async () => {
  const directory = temporaryDirectory();
  try {
    const journal = new MissionJournal(directory);
    for (let index = 0; index < 5; index += 1)
      await journal.append("mission-pages", { eventId: `event-${index}`, index });
    const first = await journal.readPage("mission-pages", { limit: 2 });
    assert.deepEqual(
      first.events.map((event) => event.index),
      [0, 1],
    );
    assert.equal(first.nextCursor, 2);
    assert.equal(first.hasMore, true);
    const second = await journal.readPage("mission-pages", {
      cursor: first.nextCursor,
      limit: 2,
    });
    assert.deepEqual(
      second.events.map((event) => event.index),
      [2, 3],
    );
    assert.equal(second.nextCursor, 4);
    const last = await journal.readPage("mission-pages", {
      cursor: second.nextCursor,
      limit: 2,
    });
    assert.deepEqual(last.events.map((event) => event.index), [4]);
    assert.equal(last.nextCursor, null);
  } finally {
    fs.rmSync(directory, { recursive: true, force: true });
  }
});

test("concurrent appends are flushed in invocation order", async () => {
  const directory = temporaryDirectory();
  try {
    const journal = new MissionJournal(directory);
    await Promise.all(
      Array.from({ length: 64 }, (_, index) =>
        journal.append("mission-order", { index }),
      ),
    );
    await journal.flush();
    const page = await journal.readPage("mission-order", { limit: MAX_PAGE_SIZE });
    assert.deepEqual(
      page.events.map((event) => event.index),
      Array.from({ length: 64 }, (_, index) => index),
    );
  } finally {
    fs.rmSync(directory, { recursive: true, force: true });
  }
});

test("task IDs are hashed for paths and invalid page bounds fail visibly", async () => {
  const directory = temporaryDirectory();
  try {
    const journal = new MissionJournal(directory);
    const escapedId = "../../mission-journal-outside";
    await journal.append(escapedId, { type: "note" });
    const entries = await fsp.readdir(directory);
    assert.deepEqual(entries, [
      `${crypto.createHash("sha256").update(escapedId).digest("hex")}.jsonl`,
    ]);
    await assert.rejects(
      journal.append("\u0000", { type: "note" }),
      (error) => error.code === "invalid_task_id",
    );
    await assert.rejects(
      journal.readPage("mission", { limit: MAX_PAGE_SIZE + 1 }),
      (error) => error.code === "invalid_limit",
    );
    await assert.rejects(
      journal.readPage("mission", { cursor: -1 }),
      (error) => error.code === "invalid_cursor",
    );
    await assert.rejects(
      journal.append("mission", { taskId: "other", type: "note" }),
      (error) => error.code === "invalid_envelope",
    );
  } finally {
    fs.rmSync(directory, { recursive: true, force: true });
  }
});

test("journal filesystem failures are returned to the caller", async () => {
  const directory = temporaryDirectory();
  const filePath = path.join(
    directory,
    `${crypto.createHash("sha256").update("mission-link").digest("hex")}.jsonl`,
  );
  const outside = path.join(directory, "outside.jsonl");
  try {
    await fsp.writeFile(outside, "outside\n");
    await fsp.symlink(outside, filePath);
    const journal = new MissionJournal(directory);
    await assert.rejects(
      journal.append("mission-link", { type: "note" }),
      (error) => error.code === "invalid_journal_path",
    );
    await assert.rejects(
      journal.stat("mission-link"),
      (error) => error.code === "invalid_journal_path",
    );
  } finally {
    fs.rmSync(directory, { recursive: true, force: true });
  }
});

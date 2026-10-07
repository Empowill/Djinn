"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const Module = require("node:module");
const ts = require("typescript");

function loadTypeScript(filename) {
  const source = fs.readFileSync(filename, "utf8");
  const compiled = ts.transpileModule(source, {
    fileName: filename,
    compilerOptions: {
      module: ts.ModuleKind.CommonJS,
      target: ts.ScriptTarget.ES2022,
      esModuleInterop: true,
    },
  });
  const loaded = new Module(filename, module);
  loaded.filename = filename;
  loaded.paths = Module._nodeModulePaths(path.dirname(filename));
  loaded._compile(compiled.outputText, filename);
  return loaded.exports;
}

const {
  collectMissionJournal,
  journalEventIdentity,
  mergeMissionJournalEvents,
  readAllMissionJournalPages,
  serializeMissionJournal,
} = loadTypeScript(path.resolve(__dirname, "../src/mission-journal-export.ts"));

const time = (seconds) => `2026-10-06T10:00:${String(seconds).padStart(2, "0")}.000Z`;

test("journal export combines native and task events with stable identity and human precedence", async () => {
  const native = [
    {
      eventId: "native-note",
      runId: "run-1",
      type: "note",
      timestamp: time(2),
      data: { title: "Agent", detail: "same event" },
    },
    {
      eventId: "stream-partial",
      runId: "run-1",
      type: "text",
      timestamp: time(3),
      data: { streaming: true, messageId: "m-1", text: "final" },
    },
    {
      eventId: "stream-first",
      runId: "run-1",
      type: "text",
      timestamp: time(1),
      data: { streaming: true, messageId: "m-1", text: "first" },
    },
  ];
  const taskEvents = [
    {
      id: "native-note",
      time: time(4),
      type: "decision",
      title: "Validation humaine",
      detail: "Conservée",
      actor: "human",
    },
    {
      id: "run-1:message:m-1",
      time: time(1),
      type: "note",
      title: "Compte rendu",
      detail: "renderer mirror",
      actor: "agent",
    },
    {
      id: "human-only",
      time: time(2),
      type: "phase",
      title: "Validé",
      detail: "Par vous",
      actor: "human",
    },
  ];
  assert.equal(journalEventIdentity(native[1]), "run-1:message:m-1");
  const events = await collectMissionJournal(
    { id: "task-1", events: taskEvents },
    async (_taskId, cursor) =>
      cursor === 0
        ? { events: native, nextCursor: null, hasMore: false }
        : { events: [], nextCursor: null, hasMore: false },
  );
  assert.deepEqual(
    events.map((event) => event.id || event.eventId),
    ["human-only", "stream-partial", "native-note"],
  );
  assert.equal(events[2].actor, "human");
  assert.equal(events[1].data.text, "final");
  assert.match(serializeMissionJournal(events), /\n$/);
  assert.equal(serializeMissionJournal(events).trim().split("\n").length, 3);
});

test("journal pagination rejects invalid and non-progressive pages before completion", async () => {
  await assert.rejects(
    readAllMissionJournalPages("task", async () => ({
      events: [{ eventId: "one" }],
      nextCursor: 0,
      hasMore: true,
    })),
    /progresser/,
  );
  await assert.rejects(
    readAllMissionJournalPages("task", async () => ({
      events: [],
      nextCursor: 1,
      hasMore: false,
    })),
    /dernière page/,
  );
  await assert.rejects(
    readAllMissionJournalPages("task", async () => ({
      events: [],
      nextCursor: null,
      hasMore: "yes",
    })),
    /hasMore/,
  );
});

test("journal pagination follows valid cursors", async () => {
  const calls = [];
  const events = await readAllMissionJournalPages("task", async (_id, cursor) => {
    calls.push(cursor);
    return cursor === 0
      ? { events: [{ eventId: "one" }], nextCursor: 1, hasMore: true }
      : { events: [{ eventId: "two" }], nextCursor: null, hasMore: false };
  });
  assert.deepEqual(calls, [0, 1]);
  assert.deepEqual(events.map((event) => event.eventId), ["one", "two"]);
  assert.deepEqual(mergeMissionJournalEvents([], []), []);
});

test("journal sorting keeps insertion order for equal timestamps", () => {
  const events = mergeMissionJournalEvents(
    [
      { eventId: "first", timestamp: time(5), data: {} },
      { eventId: "second", timestamp: time(5), data: {} },
    ],
    [
      {
        id: "first",
        time: time(5),
        type: "decision",
        title: "Décision humaine",
        detail: "Garder",
        actor: "human",
      },
    ],
  );
  assert.deepEqual(events.map((event) => event.eventId || event.id), ["first", "second"]);
});

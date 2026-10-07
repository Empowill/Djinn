"use strict";
const test = require("node:test");
const assert = require("node:assert/strict");
const {
  claudeModels,
  readCodexModels,
  discoverCodexModels,
} = require("../electron/provider-models.cjs");
const { spawn } = require("node:child_process");
const path = require("node:path");

test("Codex catalog follows pagination, filters hidden models and preserves dispatch IDs", async () => {
  const requests = [];
  const pages = [
    {
      data: [
        {
          id: "display-only",
          model: "actual-id",
          displayName: "Readable name",
          isDefault: true,
        },
        { id: "secret", hidden: true },
      ],
      nextCursor: "page2",
    },
    {
      data: [{ model: "second-id", displayName: "Second model" }],
      nextCursor: null,
    },
  ];
  const models = await readCodexModels({
    ready: Promise.resolve(),
    request: async (method, params) => {
      requests.push({ method, params });
      return pages.shift();
    },
  });
  assert.deepEqual(
    models.map((x) => x.id),
    ["actual-id", "second-id"],
  );
  assert.equal(models[0].name, "Readable name");
  assert.equal(models[0].isDefault, true);
  assert.equal(requests[1].params.cursor, "page2");
  assert.ok(
    requests.every(
      (x) => x.method === "model/list" && x.params.includeHidden === false,
    ),
  );
});

test("Codex rejects broken catalogs and pagination cycles", async () => {
  await assert.rejects(
    readCodexModels({ ready: Promise.resolve(), request: async () => ({}) }),
    /invalide/,
  );
  await assert.rejects(
    readCodexModels({
      ready: Promise.resolve(),
      request: async () => ({ data: [], nextCursor: "repeat" }),
    }),
    /Pagination/,
  );
});

test("Claude catalog uses T3 version gates, readable names, IDs and legacy status", () => {
  assert.equal(
    claudeModels("Claude Code 2.1.100").some((x) => x.id === "claude-opus-5"),
    false,
  );
  assert.equal(
    claudeModels("Claude Code 2.1.219").some((x) => x.id === "claude-opus-5"),
    true,
  );
  const models = claudeModels("2.1.284");
  assert.equal(models.find((x) => x.id === "claude-fable-5-1").isDefault, true);
  assert.equal(
    models.find((x) => x.id === "claude-opus-5-5").name,
    "Claude Opus 5.5",
  );
  assert.ok(models.find((x) => x.id === "claude-opus-4-6").isLegacy);
  assert.equal(
    claudeModels(null).some((x) => x.id === "claude-opus-5"),
    false,
  );
});

test("Metadata discovery uses the native protocol and closes its process without creating a turn", async () => {
  let child;
  const models = await discoverCodexModels(
    "unused",
    process.env,
    (_command, args, options) => {
      assert.deepEqual(args, ["app-server", "--listen", "stdio://"]);
      child = spawn(
        process.execPath,
        [path.join(__dirname, "fixtures/fake-codex.cjs"), ...args],
        options,
      );
      return child;
    },
  );
  assert.equal(models[0].id, "fixture-model");
  assert.notEqual(child.exitCode === null && child.signalCode === null, true);
});

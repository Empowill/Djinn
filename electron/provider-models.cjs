"use strict";
const { spawn } = require("node:child_process");
const { CodexAppServer } = require("./codex-app-server.cjs");
const claudeManifest = require("./claude-models.json");

function compareVersions(left, right) {
  const parse = (value) =>
    String(value || "")
      .match(/\d+\.\d+\.\d+/)?.[0]
      .split(".")
      .map(Number);
  const a = parse(left),
    b = parse(right);
  if (!a || !b) return null;
  for (let i = 0; i < 3; i++) if (a[i] !== b[i]) return a[i] - b[i];
  return 0;
}

// T3 Code's version-gated Claude manifest: canonical IDs are dispatched intact.
function claudeModels(version) {
  return claudeManifest.models
    .filter((model) => {
      const compatibility = model.adapter?.claudeCode || {};
      if (compatibility.minVersion) {
        const order = compareVersions(version, compatibility.minVersion);
        if (order === null || order < 0) return false;
      }
      if (compatibility.maxVersionExclusive) {
        const order = compareVersions(
          version,
          compatibility.maxVersionExclusive,
        );
        if (order === null || order >= 0) return false;
      }
      return true;
    })
    .map((model) => ({
      id: model.slug,
      name: model.name,
      aliases: model.aliases || [],
      isDefault: model.slug === claudeManifest.defaultModel,
      isLegacy: model.status === "legacy",
    }))
    .sort((a, b) => Number(a.isLegacy) - Number(b.isLegacy));
}

async function readCodexModels(server) {
  await server.ready;
  let cursor;
  const models = new Map(),
    cursors = new Set();
  do {
    const response = await server.request("model/list", {
      limit: 100,
      includeHidden: false,
      ...(cursor ? { cursor } : {}),
    });
    if (!response || !Array.isArray(response.data))
      throw new Error("Catalogue Codex invalide");
    for (const model of response.data) {
      const id = model.model || model.id;
      if (model.hidden || typeof id !== "string" || !id.trim()) continue;
      models.set(id, {
        id,
        name: model.displayName || id,
        description:
          typeof model.description === "string" ? model.description : "",
        isDefault: model.isDefault === true,
      });
    }
    cursor = response.nextCursor;
    if (cursor && (cursors.has(cursor) || cursors.size >= 20))
      throw new Error("Pagination du catalogue Codex invalide");
    cursors.add(cursor);
  } while (cursor);
  return [...models.values()];
}

// Dedicated metadata-only transport: no thread or model turn is created.
async function discoverCodexModels(command, env, spawnProcess = spawn) {
  const child = spawnProcess(command, ["app-server", "--listen", "stdio://"], {
    shell: false,
    windowsHide: true,
    stdio: ["pipe", "pipe", "pipe"],
    env,
  });
  const server = new CodexAppServer(child, { requestTimeout: 8000 });
  try {
    return await readCodexModels(server);
  } finally {
    await server.abortTransport(new Error("Catalogue chargé"));
  }
}

module.exports = {
  compareVersions,
  claudeModels,
  readCodexModels,
  discoverCodexModels,
};

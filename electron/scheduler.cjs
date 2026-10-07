"use strict";
const path = require("node:path");
const fs = require("node:fs");
const os = require("node:os");

const ISOLATION_MODES = Object.freeze(["shared", "worktree"]);
const MAX_RESOURCE_LABELS = 64;
const MAX_CPU = 1024;
const MAX_MEMORY_MB = Number.MAX_SAFE_INTEGER;

function list(value, field) {
  if (value === undefined) return undefined;
  if (!Array.isArray(value) || value.length > MAX_RESOURCE_LABELS)
    throw new Error(`${field} must contain at most ${MAX_RESOURCE_LABELS} labels`);
  const result = value.map((entry) => {
    if (
      typeof entry !== "string" ||
      !entry.trim() ||
      entry.length > 128 ||
      /[\0\r\n]/.test(entry)
    )
      throw new Error(`${field} contains an invalid label`);
    return entry.trim();
  });
  return [...new Set(result)];
}

function validateIsolation(value) {
  if (value === undefined || value === null) return "shared";
  if (!ISOLATION_MODES.includes(value))
    throw new Error("isolation must be shared or worktree");
  return value;
}

/**
 * A worker can describe the resources it consumes and the capabilities it
 * needs. The scheduler treats omitted values as the smallest useful request,
 * which keeps existing missions compatible while allowing projects to opt in
 * to adaptive scheduling.
 */
function validateResourceProfile(value) {
  if (value === undefined || value === null) return undefined;
  if (!value || typeof value !== "object" || Array.isArray(value))
    throw new Error("resources must be an object");
  const positive = (raw, field, fallback, maximum) => {
    if (raw === undefined) return fallback;
    if (
      typeof raw !== "number" ||
      !Number.isFinite(raw) ||
      raw <= 0 ||
      raw > maximum
    )
      throw new Error(`${field} must be a positive number at most ${maximum}`);
    return raw;
  };
  return {
    cpu: positive(value.cpu ?? value.cpuCores, "resources.cpu", 1, MAX_CPU),
    memoryMb: positive(
      value.memoryMb ?? value.memory,
      "resources.memoryMb",
      undefined,
      MAX_MEMORY_MB,
    ),
    labels: list(value.labels, "resources.labels") || [],
    requires: list(value.requires, "resources.requires") || [],
    excludes: list(value.excludes, "resources.excludes") || [],
    exclusive: list(value.exclusive, "resources.exclusive") || [],
  };
}

function systemCapacity(overrides = {}) {
  const cpu = Number(
    overrides.cpu ??
      overrides.cpuCores ??
      os.availableParallelism?.() ??
      os.cpus?.().length ??
      1,
  );
  const memoryMb = Number(
    overrides.memoryMb ??
      Math.floor((os.freemem?.() || os.totalmem?.() || 1024 * 1024) / 1024 / 1024),
  );
  return {
    cpu: Number.isFinite(cpu) && cpu > 0 ? cpu : 1,
    memoryMb:
      Number.isFinite(memoryMb) && memoryMb > 0 ? memoryMb : Number.POSITIVE_INFINITY,
    labels: list(overrides.labels, "capacity.labels") || [],
  };
}

function validateWriteScope(value) {
  if (value === undefined) return undefined;
  if (!Array.isArray(value) || value.length > 200)
    throw new Error("writeScope must contain at most 200 relative paths");
  return [
    ...new Set(
      value.map((raw) => {
        if (
          typeof raw !== "string" ||
          !raw.trim() ||
          raw.length > 4096 ||
          /[\0\r\n]/.test(raw)
        )
          throw new Error("writeScope contains an invalid path");
        const value = raw.trim().replaceAll("\\", "/").replace(/\/+$/, "");
        if (value === "*") return value;
        if (
          value.startsWith("/") ||
          /^[A-Za-z]:/.test(value) ||
          /[*?\[\]{}]/.test(value) ||
          value.split("/").some((p) => !p || p === ".." || p === ".")
        )
          throw new Error(
            "writeScope must contain literal relative files or directories",
          );
        return value;
      }),
    ),
  ];
}
function contains(parent, child) {
  return child === parent || child.startsWith(parent + path.sep);
}
function canonicalPath(value) {
  let existing = value,
    suffix = [];
  while (!fs.existsSync(existing)) {
    try {
      fs.lstatSync(existing);
      throw new Error("Ownership contains a dangling symlink");
    } catch (error) {
      if (error.code !== "ENOENT") throw error;
    }
    const parent = path.dirname(existing);
    if (parent === existing) throw new Error("Ownership root does not exist");
    suffix.unshift(path.basename(existing));
    existing = parent;
  }
  return path.join(fs.realpathSync(existing), ...suffix);
}
function prepareAgents(cwd, agents) {
  const root = fs.realpathSync(cwd);
  return agents.map((agent) => {
    const scope = validateWriteScope(agent.writeScope);
    const isolation = validateIsolation(agent.isolation);
    const resources = validateResourceProfile(agent.resources);
    let workspaceRoot = root;
    if (isolation === "worktree") {
      if (typeof agent.worktree !== "string" || !path.isAbsolute(agent.worktree))
        throw new Error(
          `Agent ${agent.id} must provide an existing absolute worktree when isolation is worktree`,
        );
      workspaceRoot = fs.realpathSync(agent.worktree);
      if (!fs.statSync(workspaceRoot).isDirectory())
        throw new Error(`Worktree for ${agent.id} is not a directory`);
    }
    const writePaths = agent.readOnly
      ? []
      : !scope?.length || scope.includes("*")
        ? [workspaceRoot]
        : scope.map((p) => {
            const resolved = canonicalPath(path.resolve(workspaceRoot, p));
            if (!contains(workspaceRoot, resolved))
              throw new Error(
                `Ownership for ${agent.id} leaves the project root`,
              );
            return resolved;
          });
    return {
      ...agent,
      isolation,
      resources,
      workspaceRoot,
      writeScope: scope,
      writePaths,
    };
  });
}
function resourceConflict(a, b) {
  const profile = (agent) => {
    const resources = agent?.resources || {};
    return {
      labels: resources.labels || [],
      requires: resources.requires || [],
      excludes: resources.excludes || [],
      exclusive: resources.exclusive || [],
    };
  };
  const left = profile(a);
  const right = profile(b);
  const intersects = (one = [], two = []) =>
    one.some((value) => two.includes(value));
  if (intersects(left.exclusive, right.exclusive)) return true;
  if (intersects(left.exclusive, right.labels.concat(right.requires)))
    return true;
  if (intersects(right.exclusive, left.labels.concat(left.requires)))
    return true;
  if (intersects(left.excludes, right.labels.concat(right.requires || [])))
    return true;
  if (intersects(right.excludes, left.labels.concat(left.requires || [])))
    return true;
  return false;
}

function conflicts(a, b, mode = "execute") {
  if (resourceConflict(a, b)) return true;
  if (mode !== "execute" || a.readOnly || b.readOnly) return false;
  const left = a.writePaths || a.writeScope || ["*"],
    right = b.writePaths || b.writeScope || ["*"];
  const ownershipConflict =
    !left.length ||
    !right.length ||
    left.includes("*") ||
    right.includes("*") ||
    left.some((p) => right.some((q) => contains(p, q) || contains(q, p)));
  return ownershipConflict;
}
function validateDependencies(agents) {
  const byId = new Map(agents.map((a) => [a.id, a]));
  const visiting = new Set(),
    visited = new Set();
  function visit(id) {
    if (visiting.has(id)) throw new Error("Agent dependencies contain a cycle");
    if (visited.has(id)) return;
    visiting.add(id);
    for (const dependency of byId.get(id).dependsOn || []) {
      if (!byId.has(dependency) || dependency === id)
        throw new Error(`Unknown or self dependency for ${id}: ${dependency}`);
      visit(dependency);
    }
    visiting.delete(id);
    visited.add(id);
  }
  agents.forEach((a) => visit(a.id));
}
function resourceReason(agent, active, capacity) {
  if (!capacity) return null;
  const profile = agent.resources || {};
  const labels = capacity.labels || [];
  const missing = (profile.requires || []).filter(
    (required) => !labels.includes(required),
  );
  const excluded = (profile.excludes || []).filter((value) => labels.includes(value));
  if (missing.length || excluded.length)
    return {
      kind: "resource_unavailable",
      resources: [...missing, ...excluded],
      agents: [],
    };
  const cpu = active.reduce((sum, item) => sum + Number(item.resources?.cpu || 1), 0);
  const memoryMb = active.reduce(
    (sum, item) => sum + Number(item.resources?.memoryMb || 0),
    0,
  );
  if (cpu + Number(profile.cpu || 1) > capacity.cpu)
    return {
      kind: "resource_capacity",
      resource: "cpu",
      agents: active.map((item) => item.id),
    };
  if (
    Number.isFinite(capacity.memoryMb) &&
    memoryMb + Number(profile.memoryMb || 0) > capacity.memoryMb
  )
    return {
      kind: "resource_capacity",
      resource: "memoryMb",
      agents: active.map((item) => item.id),
    };
  return null;
}

function reasonFor(agent, active, completed, limit, mode, capacity) {
  const unmet = (agent.dependsOn || []).filter((id) => !completed.has(id));
  if (unmet.length) return { kind: "dependency", agents: unmet };
  const failed = (agent.dependsOn || []).filter(
    (id) => !["completed", "done"].includes(completed.get(id)?.status),
  );
  if (failed.length) return { kind: "dependency_failed", agents: failed };
  const overlap = active
    .filter((other) => conflicts(agent, other, mode))
    .map((other) => other.id);
  if (overlap.length) return { kind: "ownership_conflict", agents: overlap };
  const resource = resourceReason(agent, active, capacity);
  if (resource) return resource;
  if (active.length >= limit)
    return { kind: "concurrency_limit", agents: active.map((a) => a.id) };
  return null;
}
function buildWaves(agents, limit, mode, options = {}) {
  const remaining = agents.slice(),
    completed = new Map(),
    waves = [];
  const capacity = options?.capacity ? systemCapacity(options.capacity) : undefined;
  while (remaining.length) {
    const batch = [];
    for (const agent of remaining)
      if (!reasonFor(agent, batch, completed, limit, mode, capacity))
        batch.push(agent);
    if (!batch.length) {
      const impossible = remaining[0];
      const reason = resourceReason(impossible, [], capacity);
      if (reason?.kind === "resource_capacity")
        throw new Error(
          `Agent ${impossible.id} exceeds the available ${reason.resource} capacity`,
        );
      if (reason?.kind === "resource_unavailable")
        throw new Error(
          `Agent ${impossible.id} requires unavailable resources: ${reason.resources.join(", ")}`,
        );
      throw new Error("Agent scheduling made no progress");
    }
    waves.push(batch);
    for (const a of batch) {
      remaining.splice(remaining.indexOf(a), 1);
      completed.set(a.id, { status: "completed" });
    }
  }
  return waves;
}
module.exports = {
  pathsOverlap: (a, b) => contains(a, b) || contains(b, a),
  validateWriteScope,
  validateIsolation,
  validateResourceProfile,
  systemCapacity,
  prepareAgents,
  resourceConflict,
  conflicts,
  validateDependencies,
  reasonFor,
  buildWaves,
};

"use strict";
const test = require("node:test"),
  assert = require("node:assert/strict"),
  fs = require("node:fs"),
  path = require("node:path"),
  os = require("node:os");
const scheduler = require("../electron/scheduler.cjs");
const worker = (id, scope, dependsOn) => ({ id, writeScope: scope, dependsOn });
test("disjoint writers share a wave while bounded and intentional root ownership serialize", () => {
  const agents = [
    worker("a", ["src/a.ts"]),
    worker("b", ["src/a.ts"]),
    worker("c", ["src/c.ts"]),
    worker("d", ["src/d.ts"]),
  ];
  assert.deepEqual(
    scheduler
      .buildWaves(agents, 3, "execute")
      .map((batch) => batch.map((a) => a.id)),
    [["a", "c", "d"], ["b"]],
  );
  assert.equal(
    scheduler.conflicts(worker("dir", ["src"]), worker("file", ["src/a.ts"])),
    true,
  );
  assert.equal(
    scheduler.conflicts({ id: "inspector" }, worker("file", ["src/a.ts"])),
    false,
  );
  assert.throws(
    () => scheduler.conflicts({ id: "writer", readOnly: false }, worker("file", ["src/a.ts"])),
    /writeScope/,
  );
  assert.equal(
    scheduler.conflicts(
      { ...worker("read"), readOnly: true },
      worker("writer", ["src"]),
    ),
    false,
  );
});

test("omitted ownership is a concurrent inspector and writers must declare a scope", () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-scheduler-defaults-"));
  try {
    const [inspector] = scheduler.prepareAgents(root, [{ id: "inspector" }]);
    assert.equal(inspector.readOnly, true);
    assert.deepEqual(inspector.writePaths, []);
    assert.throws(
      () => scheduler.prepareAgents(root, [{ id: "writer", readOnly: false }]),
      /writeScope/,
    );
    const [rootWriter] = scheduler.prepareAgents(root, [{
      id: "root-writer",
      readOnly: false,
      writeScope: ["*"],
    }]);
    assert.equal(rootWriter.readOnly, false);
    assert.deepEqual(rootWriter.writePaths, [fs.realpathSync(root)]);
    assert.deepEqual(
      scheduler.buildWaves([
        { id: "left", writeScope: ["left.ts"] },
        { id: "right", writeScope: ["right.ts"] },
        { id: "inspect" },
      ], 3, "execute").map((wave) => wave.map((agent) => agent.id)),
      [["left", "right", "inspect"]],
    );
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
});
test("resource conflicts tolerate omitted profiles while preserving declared exclusions", () => {
  assert.equal(
    scheduler.resourceConflict(worker("plain-a", ["a.ts"]), worker("plain-b", ["b.ts"])),
    false,
  );
  assert.equal(
    scheduler.resourceConflict(
      { resources: { excludes: ["gpu"] } },
      { resources: { labels: ["gpu"] } },
    ),
    true,
  );
  assert.equal(
    scheduler.resourceConflict(
      { resources: { excludes: ["gpu"] } },
      { resources: { requires: ["gpu"] } },
    ),
    true,
  );
});
test("resource conflicts serialize exclusive readers in every mode while compatible readers share", () => {
  const modes = ["execute", "plan", "review"];
  for (const mode of modes) {
    const left = {
      id: `${mode}-gpu-a`,
      readOnly: true,
      writeScope: ["a.ts"],
      resources: { exclusive: ["gpu"] },
    };
    const right = {
      id: `${mode}-gpu-b`,
      readOnly: true,
      writeScope: ["b.ts"],
      resources: { exclusive: ["gpu"] },
    };
    assert.equal(scheduler.conflicts(left, right, mode), true);
    assert.deepEqual(
      scheduler
        .buildWaves([left, right], 2, mode)
        .map((batch) => batch.map((agent) => agent.id)),
      [[left.id], [right.id]],
    );

    const compatibleLeft = {
      id: `${mode}-shared-a`,
      readOnly: true,
      writeScope: ["a.ts"],
      resources: { labels: ["gpu"] },
    };
    const compatibleRight = {
      id: `${mode}-shared-b`,
      readOnly: true,
      writeScope: ["b.ts"],
      resources: { labels: ["gpu"] },
    };
    assert.equal(
      scheduler.conflicts(compatibleLeft, compatibleRight, mode),
      false,
    );
    assert.deepEqual(
      scheduler
        .buildWaves([compatibleLeft, compatibleRight], 2, mode)
        .map((batch) => batch.map((agent) => agent.id)),
      [[compatibleLeft.id, compatibleRight.id]],
    );
  }
  assert.equal(
    scheduler.resourceConflict(
      { resources: { exclusive: ["gpu"] } },
      { resources: { requires: ["gpu"] } },
    ),
    true,
  );
});
test("keeps CPU bounded separately while allowing multi-gigabyte memory requests", () => {
  assert.equal(
    scheduler.validateResourceProfile({ cpu: 1024, memoryMb: 4096 }).memoryMb,
    4096,
  );
  assert.throws(
    () => scheduler.validateResourceProfile({ memoryMb: Number.MAX_SAFE_INTEGER + 1 }),
    /memoryMb/,
  );
  assert.throws(
    () => scheduler.validateResourceProfile({ cpu: 1025 }),
    /cpu/,
  );

  const agents = ["a", "b", "c", "d"].map((id) => ({
    id,
    readOnly: true,
    resources: { memoryMb: 4096 },
  }));
  assert.deepEqual(
    scheduler
      .buildWaves(agents, 4, "review", {
        capacity: { cpu: 4, memoryMb: 16384 },
      })
      .map((batch) => batch.map((agent) => agent.id)),
    [["a", "b", "c", "d"]],
  );
  assert.throws(
    () =>
      scheduler.buildWaves(
        [{ id: "too-large", resources: { memoryMb: 16385 } }],
        1,
        "review",
        { capacity: { cpu: 1, memoryMb: 16384 } },
      ),
    /memoryMb/,
  );
});
test("dependencies defer only the dependent work and reject unknown or cyclic contracts", () => {
  const agents = [
    worker("contract", ["types.ts"]),
    worker("dependent", ["consumer.ts"], ["contract"]),
    worker("independent", ["ui"]),
  ];
  scheduler.validateDependencies(agents);
  assert.deepEqual(
    scheduler
      .buildWaves(agents, 3, "execute")
      .map((batch) => batch.map((a) => a.id)),
    [["contract", "independent"], ["dependent"]],
  );
  assert.throws(
    () => scheduler.validateDependencies([worker("a", [], ["missing"])]),
    /Unknown/,
  );
  assert.throws(
    () =>
      scheduler.validateDependencies([
        worker("a", [], ["b"]),
        worker("b", [], ["a"]),
      ]),
    /cycle/,
  );
  assert.equal(
    scheduler.reasonFor(
      agents[1],
      [],
      new Map([["contract", { status: "error" }]]),
      3,
      "execute",
    ).kind,
    "dependency_failed",
  );
});
test("canonical ownership detects symlink aliases and rejects escapes or dangling links", () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-scopes-")),
    outside = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-outside-"));
  try {
    fs.mkdirSync(path.join(root, "src"));
    fs.symlinkSync(path.join(root, "src"), path.join(root, "alias"));
    fs.symlinkSync(outside, path.join(root, "outside"));
    fs.symlinkSync(path.join(root, "missing"), path.join(root, "dangling"));
    const [a, b] = scheduler.prepareAgents(root, [
      worker("a", ["src/new.ts"]),
      worker("b", ["alias/new.ts"]),
    ]);
    assert.equal(scheduler.conflicts(a, b), true);
    assert.throws(
      () => scheduler.prepareAgents(root, [worker("a", ["outside/new.ts"])]),
      /leaves/,
    );
    assert.throws(
      () => scheduler.prepareAgents(root, [worker("a", ["dangling/new.ts"])]),
      /dangling/,
    );
    for (const scope of [
      ["../escape"],
      ["/tmp"],
      ["src/*"],
      ["src/./a"],
      ["C:\\private"],
    ])
      assert.throws(() => scheduler.validateWriteScope(scope));
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
    fs.rmSync(outside, { recursive: true, force: true });
  }
});

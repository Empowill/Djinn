"use strict";
const assert = require("node:assert/strict");
const test = require("node:test");
const fs = require("node:fs/promises");
const os = require("node:os");
const path = require("node:path");
const { writeMissionContext } = require("../electron/mission-context.cjs");

test("full canonical evidence remains readable on demand without replaying images or trusting IDs as paths", async () => {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), "djinn-supports-"));
  try {
    const content =
      "# Canonical\n" +
      "Detailed contract\n".repeat(2000) +
      "Acceptance criteria at the end";
    const task = {
      id: "../../mission",
      artifacts: [
        {
          id: "../../outside",
          title: "Contract",
          type: "document",
          content,
          sourceOfTruth: true,
        },
        {
          id: "image",
          title: "Screenshot",
          type: "screenshot",
          content: "private image bytes",
        },
      ],
    };
    const indexPath = await writeMissionContext(task, root);
    const index = JSON.parse(await fs.readFile(indexPath, "utf8"));
    assert.equal(index.supports.length, 1);
    assert.equal(index.supports[0].sourceOfTruth, true);
    assert.equal(await fs.readFile(index.supports[0].path, "utf8"), content);
    assert.ok(
      path
        .relative(await fs.realpath(root), index.supports[0].path)
        .startsWith(`mission-supports${path.sep}`),
    );
    assert.ok((await fs.stat(index.supports[0].path)).isFile());
    if (process.platform !== "win32")
      assert.equal((await fs.stat(index.supports[0].path)).mode & 0o777, 0o600);
  } finally {
    await fs.rm(root, { recursive: true, force: true });
  }
});

test("an existing support symlink cannot overwrite a file outside the archive", async () => {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), "djinn-supports-"));
  try {
    const task = {
      id: "task",
      artifacts: [
        {
          id: "support",
          title: "Contract",
          type: "document",
          content: "original",
        },
      ],
    };
    const index = JSON.parse(
      await fs.readFile(await writeMissionContext(task, root), "utf8"),
    );
    const outside = path.join(root, "keep.txt");
    await fs.writeFile(outside, "keep");
    await fs.unlink(index.supports[0].path);
    await fs.symlink(outside, index.supports[0].path);
    await assert.rejects(writeMissionContext(task, root));
    assert.equal(await fs.readFile(outside, "utf8"), "keep");
  } finally {
    await fs.rm(root, { recursive: true, force: true });
  }
});

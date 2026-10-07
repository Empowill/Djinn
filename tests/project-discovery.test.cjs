"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const fsp = fs.promises;
const os = require("node:os");
const path = require("node:path");
const crypto = require("node:crypto");

const { scanProject } = require("../electron/project-discovery.cjs");

async function fixture() {
  const directory = await fsp.mkdtemp(path.join(os.tmpdir(), "djinn-discovery-"));
  await fsp.mkdir(path.join(directory, ".agents", "skills", "review"), {
    recursive: true,
  });
  await fsp.mkdir(path.join(directory, "app", "apps", "prototypes"), {
    recursive: true,
  });
  await fsp.mkdir(path.join(directory, "tests"), { recursive: true });
  await fsp.mkdir(path.join(directory, "node_modules", "ignored"), {
    recursive: true,
  });
  await fsp.mkdir(path.join(directory, "build"), { recursive: true });
  await fsp.writeFile(
    path.join(directory, "AGENTS.md"),
    "# Repository instructions\nRun the checks recorded in package.json.\n",
  );
  await fsp.writeFile(
    path.join(directory, "CLAUDE.md"),
    "Read the project documentation before proposing changes.\n",
  );
  await fsp.writeFile(
    path.join(directory, ".agents", "skills", "review", "SKILL.md"),
    "---\nname: review\n---\nReview evidence only.\n",
  );
  await fsp.writeFile(
    path.join(directory, "package.json"),
    JSON.stringify({
      name: "fixture-project",
      scripts: { dev: "vite", test: "node --test", build: "tsc" },
      workspaces: ["app/*"],
    }),
  );
  await fsp.writeFile(
    path.join(directory, "app", "apps", "prototypes", "package.json"),
    JSON.stringify({ name: "fixture-prototypes", scripts: { dev: "vite" } }),
  );
  await fsp.writeFile(
    path.join(directory, "tests", "verification.test.cjs"),
    "test('fixture', () => {});\n",
  );
  await fsp.writeFile(path.join(directory, ".env.local"), "TOKEN=private\n");
  await fsp.writeFile(path.join(directory, "credentials.json"), '{"token":"private"}\n');
  await fsp.writeFile(path.join(directory, "node_modules", "ignored", "x.js"), "private\n");
  await fsp.writeFile(path.join(directory, "build", "ignored.js"), "private\n");
  try {
    await fsp.symlink(path.join(directory, "node_modules"), path.join(directory, "linked-deps"));
  } catch {
    // Symlink creation may be unavailable on a restricted Windows runner.
  }
  return directory;
}

test("scanProject returns bounded, relative, local evidence and pending workflows", async () => {
  const directory = await fixture();
  const report = await scanProject({
    directory,
    history: [
      {
        title: "Previous mission",
        brief: "A local summary",
        steps: [{ type: "review", title: "Review", status: "completed" }],
        instructions: [{ text: "Keep the existing contract." }],
      },
    ],
  });

  assert.deepEqual(Object.keys(report), [
    "directory",
    "name",
    "scannedAt",
    "filesScanned",
    "historyCount",
    "summary",
    "evidence",
    "workflows",
    "sourcesOfTruth",
    "locations",
    "conventions",
    "notes",
    "analysis",
  ]);
  assert.equal(report.directory, directory);
  assert.equal(report.name, path.basename(directory));
  assert.equal(report.historyCount, 1);
  assert.equal(report.analysis.status, "local");
  assert.ok(report.filesScanned > 0);
  assert.ok(report.evidence.some((entry) => entry.path === "AGENTS.md" && entry.kind === "instructions"));
  assert.ok(report.evidence.some((entry) => entry.path.endsWith("/SKILL.md") && entry.kind === "skill"));
  assert.ok(report.evidence.some((entry) => entry.path === "package.json" && entry.kind === "automation"));
  assert.ok(report.evidence.some((entry) => entry.path.includes("prototypes/package.json") && entry.kind === "prototype"));
  assert.equal(report.evidence.some((entry) => entry.path.includes(".env")), false);
  assert.equal(report.evidence.some((entry) => entry.path.includes("credentials")), false);
  assert.equal(report.evidence.some((entry) => entry.path.includes("node_modules")), false);
  assert.equal(report.evidence.some((entry) => entry.path.startsWith("build/")), false);
  for (const entry of report.evidence) {
    assert.equal(path.isAbsolute(entry.path), false);
    assert.ok(entry.excerpt.length <= 1_600);
  }
  for (const workflow of report.workflows) {
    assert.match(workflow.id, /^[0-9a-f-]{36}$/i);
    assert.ok(workflow.steps.length <= 8);
    for (const step of workflow.steps) {
      assert.match(step.id, /^[0-9a-f-]{36}$/i);
      assert.notEqual(step.type, "discussion");
      assert.equal(step.status, "pending");
    }
  }
  assert.ok(report.workflows.some((workflow) => workflow.steps.some((step) => step.type === "prototype")));
  assert.ok(report.workflows.some((workflow) => workflow.steps.some((step) => step.type === "implementation")));
  assert.equal(report.locations.skills, ".agents/skills");
  assert.ok(report.workflows.every(workflow => workflow.steps.every(step => step.skills.includes("review"))));
  assert.ok(report.summary.includes("AGENTS.md") || report.summary.includes("package.json"));
  assert.ok(report.notes.some((note) => note.includes("AGENTS.md")));
});

test("scanProject does not invoke a provider and rejects a symlink root", async () => {
  const directory = await fixture();
  const symlink = path.join(os.tmpdir(), `djinn-discovery-link-${crypto.randomUUID()}`);
  try {
    await fsp.symlink(directory, symlink);
  } catch {
    return;
  }
  await assert.rejects(
    scanProject({ directory: symlink }),
    (error) => error.code === "invalid_directory",
  );
});

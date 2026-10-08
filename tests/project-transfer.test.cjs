"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const Module = require("node:module");
const ts = require("typescript");

function loadTypeScript(relativePath) {
  const filename = path.resolve(__dirname, "..", relativePath);
  const { outputText } = ts.transpileModule(
    fs.readFileSync(filename, "utf8"),
    {
      fileName: filename,
      compilerOptions: {
        module: ts.ModuleKind.CommonJS,
        target: ts.ScriptTarget.ES2022,
      },
    },
  );
  const loaded = new Module(filename, module);
  loaded.filename = filename;
  loaded.paths = Module._nodeModulePaths(path.dirname(filename));
  loaded.require = (request) =>
    request.startsWith("./")
      ? loadTypeScript(`src/${request.slice(2)}.ts`)
      : require(request);
  loaded._compile(outputText, filename);
  return loaded.exports;
}

const { exportWorkflow, importWorkflow } = loadTypeScript(
  "src/project-transfer.ts",
);

const step = (type, id = `${type}-source`) => ({
  id,
  type,
  title: type,
  objective: `Objectif ${type}`,
  status: "pending",
  exitCriteria: ["Décision enregistrée"],
  expectedArtifacts: ["document"],
  skills: [],
});

test("portable workflow round trip keeps pending stages and regenerates identities", () => {
  const source = {
    id: "source-workflow",
    title: "Parcours classique",
    steps: [step("specification"), step("implementation"), step("review"), step("delivery")],
  };
  const text = exportWorkflow(source);
  const document = JSON.parse(text);
  assert.deepEqual(Object.keys(document), ["format", "version", "workflow"]);
  assert.equal(document.format, "djinn-workflow");
  assert.equal(document.version, 1);
  const imported = importWorkflow(text);
  assert.equal(imported.title, source.title);
  assert.equal(imported.steps.length, source.steps.length);
  assert.notEqual(imported.id, source.id);
  assert.ok(imported.steps.every((item, index) => item.id !== source.steps[index].id));
  assert.deepEqual(
    imported.steps.map(({ id, ...item }) => item),
    source.steps.map(({ id, ...item }) => item),
  );
});

test("portable import rejects executable state, placeholder discussions, and project leakage", () => {
  const source = {
    id: "source-workflow",
    title: "Parcours",
    steps: [step("specification")],
  };
  const valid = JSON.parse(exportWorkflow(source));
  for (const mutate of [
    (document) => (document.workflow.steps[0].status = "running"),
    (document) => (document.workflow.steps[0].startedAt = "2026-10-06T08:00:00.000Z"),
    (document) => (document.workflow.steps[0].command = "rm -rf"),
    (document) => (document.workflow.directory = "/tmp/project"),
    (document) => (document.workflow.conventions = "secret"),
    (document) => (document.workflow.steps[0].type = "discussion"),
  ]) {
    const document = structuredClone(valid);
    mutate(document);
    assert.throws(() => importWorkflow(document), /workflow portable/i);
  }
  assert.throws(() => importWorkflow("not json"), /workflow portable/i);
});


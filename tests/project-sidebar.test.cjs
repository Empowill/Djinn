"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const Module = require("node:module");
const test = require("node:test");
const React = require("react");
const { renderToStaticMarkup } = require("react-dom/server");
const ts = require("typescript");

const root = path.resolve(__dirname, "../src");
const sidebarFile = path.join(root, "project-sidebar.tsx");
const originals = { ...Module._extensions };

const compile = (module, filename) => {
  const originalRequire = module.require.bind(module);
  module.require = (request) =>
    request === "lucide-react"
      ? new Proxy({}, { get: () => () => null })
      : originalRequire(request);
  module._compile(
    ts.transpileModule(fs.readFileSync(filename, "utf8"), {
      fileName: filename,
      compilerOptions: {
        module: ts.ModuleKind.CommonJS,
        target: ts.ScriptTarget.ES2022,
        jsx: ts.JsxEmit.ReactJSX,
        esModuleInterop: true,
      },
    }).outputText,
    filename,
  );
};

Module._extensions[".tsx"] = compile;
Module._extensions[".ts"] = compile;
Module._extensions[".css"] = () => {};

function project(id, name, updatedAt, directory = `/tmp/${id}`) {
  return { id, name, directory, updatedAt };
}

function task(id, title, projectRecord, createdAt) {
  return {
    id,
    title,
    project: projectRecord?.directory || "/tmp/orphan",
    projectId: projectRecord?.id,
    createdAt,
    status: "idle",
    demo: false,
  };
}

test("ProjectSidebar renders projects, missions, and orphans newest first stably", () => {
  const { ProjectSidebar } = require(sidebarFile);
  const oldProject = project("old", "Project old", "2025-01-01T00:00:00Z");
  const newProject = project("new", "Project new", "2026-02-01T00:00:00Z");
  const tiedProjectA = project(
    "tie-a",
    "Project tie A",
    "2026-01-01T00:00:00Z",
  );
  const tiedProjectB = project(
    "tie-b",
    "Project tie B",
    "2026-01-01T00:00:00Z",
  );
  const invalidProjectA = project("bad-a", "Project invalid A", "not-a-date");
  const invalidProjectB = project(
    "bad-b",
    "Project invalid B",
    "also-not-a-date",
  );

  const missions = [
    task("old-mission", "Mission old", newProject, "2025-01-01T00:00:00Z"),
    task("new-mission", "Mission new", newProject, "2026-02-01T00:00:00Z"),
    task("tie-mission-a", "Mission tie A", newProject, "2026-01-01T00:00:00Z"),
    task("invalid-mission-a", "Mission invalid A", newProject, "not-a-date"),
    task("tie-mission-b", "Mission tie B", newProject, "2026-01-01T00:00:00Z"),
    task(
      "invalid-mission-b",
      "Mission invalid B",
      newProject,
      "also-not-a-date",
    ),
    task("old-orphan", "Orphan old", undefined, "2025-01-01T00:00:00Z"),
    task("new-orphan", "Orphan new", undefined, "2026-02-01T00:00:00Z"),
    task("tie-orphan-a", "Orphan tie A", undefined, "2026-01-01T00:00:00Z"),
    task("invalid-orphan-a", "Orphan invalid A", undefined, "not-a-date"),
    task("tie-orphan-b", "Orphan tie B", undefined, "2026-01-01T00:00:00Z"),
    task("invalid-orphan-b", "Orphan invalid B", undefined, "also-not-a-date"),
  ];
  const markup = renderToStaticMarkup(
    React.createElement(ProjectSidebar, {
      projects: [
        oldProject,
        tiedProjectA,
        newProject,
        invalidProjectA,
        tiedProjectB,
        invalidProjectB,
      ],
      tasks: missions,
      selectedTaskId: "",
      selectedProjectId: "",
      collapsed: false,
      onSelectTask() {},
      onSelectProject() {},
      onNewProject() {},
      onEditProject() {},
      onNewMission() {},
    }),
  );
  const assertOrder = (orderedLabels) => {
    let previous = -1;
    for (const label of orderedLabels) {
      const position = markup.indexOf(label);
      assert.ok(
        position > previous,
        `${label} should follow the previous item`,
      );
      previous = position;
    }
  };
  assertOrder([
    "Project new",
    "Project tie A",
    "Project tie B",
    "Project old",
    "Project invalid A",
    "Project invalid B",
  ]);
  assertOrder([
    "Mission new",
    "Mission tie A",
    "Mission tie B",
    "Mission old",
    "Mission invalid A",
    "Mission invalid B",
  ]);
  assertOrder([
    "Orphan new",
    "Orphan tie A",
    "Orphan tie B",
    "Orphan old",
    "Orphan invalid A",
    "Orphan invalid B",
  ]);
});

test.after(() => {
  Object.assign(Module._extensions, originals);
  delete require.cache[sidebarFile];
});

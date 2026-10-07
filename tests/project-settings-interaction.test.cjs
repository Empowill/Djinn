"use strict";
const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const Module = require("node:module");
const ts = require("typescript");
const workflow = require("../electron/workflow.cjs");

test("project settings preserve historical workflows and wait for persistence errors", async () => {
  const original = { ...Module._extensions },
    slots = [];
  let index = 0;
  const filename = path.resolve(__dirname, "../src/project-setup.tsx");
  const hooks = {
    useState(initial) {
      const i = index++;
      if (!(i in slots))
        slots[i] = typeof initial === "function" ? initial() : initial;
      return [
        slots[i],
        (value) => {
          slots[i] = typeof value === "function" ? value(slots[i]) : value;
        },
      ];
    },
    useRef: (value) => ({ current: value }),
    useEffect() {},
  };
  const compile = (module, source) => {
    const requireOriginal = module.require.bind(module);
    module.require = (request) =>
      request === "react" && source === filename
        ? hooks
        : requireOriginal(request);
    module._compile(
      ts.transpileModule(fs.readFileSync(source, "utf8"), {
        compilerOptions: {
          module: ts.ModuleKind.CommonJS,
          target: ts.ScriptTarget.ES2022,
          jsx: ts.JsxEmit.ReactJSX,
        },
      }).outputText,
      source,
    );
  };
  Module._extensions[".tsx"] = Module._extensions[".ts"] = compile;
  Module._extensions[".css"] = () => {};
  global.window = { djinn: { validateProject: async (project) => project } };
  try {
    const { ProjectSetup } = require(filename);
    const project = {
      id: "p",
      name: "Produit",
      directory: "/tmp/project",
      conventions: "",
      locations: {},
      workflowPolicy: "enforced",
      preferences: { provider: "claude", model: "", concurrency: 5 },
      workflows: [
        {
          id: "first",
          title: "Parcours imposé",
          steps: workflow.createDefaultSteps("one"),
        },
        {
          id: "second",
          title: "Autre parcours",
          steps: workflow.createDefaultSteps("two"),
        },
      ],
      updatedAt: "2026-10-06T08:00:00.000Z",
    };
    const saved = [],
      messages = [];
    const props = {
      project,
      defaultProvider: "codex",
      defaultModel: "codex-only-model",
      onClose() {},
      onToast: (message) => messages.push(message),
      onSave: async (value) => {
        saved.push(value);
      },
    };
    const render = () => {
      index = 0;
      return ProjectSetup(props);
    };
    await render().props.onSubmit({ preventDefault() {} });
    assert.deepEqual(
      saved[0].workflows.map((workflow) => workflow.id),
      ["first", "second"],
    );
    assert.deepEqual(saved[0].preferences, {
      provider: "claude",
      model: "",
      concurrency: 5,
    });
    props.onSave = async () => {
      throw new Error("Disque indisponible");
    };
    await render().props.onSubmit({ preventDefault() {} });
    assert.equal(messages.at(-1), "Disque indisponible");
  } finally {
    Object.assign(Module._extensions, original);
    delete global.window;
  }
});

"use strict";
const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const Module = require("node:module");
const ts = require("typescript");

test("fullscreen prompt starts agent preparation even with a legacy enforced project workflow", () => {
  const original = { ...Module._extensions };
  const slots = [];
  let index = 0;
  const filename = path.resolve(__dirname, "../src/app.tsx");
  const hooks = {
    ...require("react"),
    useState(initial) {
      const slot = index++;
      if (!(slot in slots))
        slots[slot] = typeof initial === "function" ? initial() : initial;
      return [
        slots[slot],
        (value) => {
          slots[slot] =
            typeof value === "function" ? value(slots[slot]) : value;
        },
      ];
    },
  };
  const compile = (module, source) => {
    const originalRequire = module.require.bind(module);
    module.require = (request) =>
      request === "react" && source === filename
        ? hooks
        : request === "./visuals"
          ? { Orb: () => null, Machine: () => null }
          : originalRequire(request);
    module._compile(
      ts.transpileModule(fs.readFileSync(source, "utf8"), {
        fileName: source,
        compilerOptions: {
          module: ts.ModuleKind.CommonJS,
          target: ts.ScriptTarget.ES2022,
          jsx: ts.JsxEmit.ReactJSX,
          esModuleInterop: true,
        },
      }).outputText,
      source,
    );
  };
  Module._extensions[".tsx"] = Module._extensions[".ts"] = compile;
  Module._extensions[".css"] = () => {};
  const find = (node, predicate) => {
    if (!node || typeof node !== "object") return;
    if (predicate(node)) return node;
    for (const child of [node.props?.children].flat(Infinity)) {
      const found = find(child, predicate);
      if (found) return found;
    }
  };
  try {
    const { NewMission } = require(filename);
    const project = {
      id: "project",
      name: "Produit",
      directory: "/tmp/product",
      conventions: "Existing knowledge",
      locations: {},
      workflowPolicy: "enforced",
      workflows: [{ id: "legacy", title: "Old workflow", steps: [] }],
      preferences: { provider: "claude", model: "", concurrency: 4 },
    };
    const calls = [];
    const props = {
      projects: [project],
      selectedProjectId: project.id,
      provider: "codex",
      model: "codex-model",
      onClose() {},
      onToast() {},
      onCreate: (...args) => calls.push(args),
    };
    const render = () => {
      index = 0;
      return NewMission(props);
    };
    let tree = render();
    assert.equal(
      find(tree, (node) => node.props?.type === "submit").props.disabled,
      true,
    );
    const providerPicker = find(
      tree,
      (node) => node.props?.id === "mission-provider",
    );
    assert.equal(providerPicker.props.value, "claude");
    assert.equal(
      find(
        tree,
        (node) => node.props?.["aria-label"] === "Indications complémentaires",
      ),
      undefined,
    );
    find(tree, (node) => node.props?.id === "mission-prompt").props.onChange({
      target: { value: "  Je veux uniquement une spécification  " },
    });
    providerPicker.props.onChange({ target: { value: "codex" } });
    const modelPicker = find(
      render(),
      (node) => node.type?.name === "ModelPicker",
    );
    assert.equal(modelPicker.props.provider, "codex");
    assert.equal(modelPicker.props.value, "");
    modelPicker.props.onChange("selected-model");
    find(render(), (node) => node.type === "form").props.onSubmit({
      preventDefault() {},
    });
    assert.equal(calls.length, 1);
    const [title, brief, directory, provider, model, options] = calls[0];
    assert.equal(title, "");
    assert.equal(brief, "Je veux uniquement une spécification");
    assert.equal(directory, project.directory);
    assert.equal(provider, "codex");
    assert.equal(model, "selected-model");
    assert.equal(options.projectRecord, project);
    assert.equal(options.workflowMode, "flexible");
    assert.equal(options.autoWorkflow, true);
    assert.deepEqual(
      options.steps.map((step) => step.type),
      ["discussion"],
    );
    assert.equal(options.indication, undefined);
    assert.equal(options.concurrency, 4);
    assert.equal(project.workflowPolicy, "enforced");
  } finally {
    Object.assign(Module._extensions, original);
  }
});

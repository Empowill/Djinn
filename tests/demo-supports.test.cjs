"use strict";
const test = require("node:test"), assert = require("node:assert/strict"), fs = require("node:fs"), ts = require("typescript"), Module = require("node:module"), path = require("node:path"), vm = require("node:vm");
function load(relative) {
  const filename = path.resolve(relative), m = new Module(filename, module);
  m.filename = filename; m.paths = Module._nodeModulePaths(path.dirname(filename));
  m._compile(ts.transpileModule(fs.readFileSync(filename, "utf8"), { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText, filename);
  return m.exports;
}
const { demoArtifacts, demoVisualization, enrichDemoState } = load("src/demo-supports.ts");
test("saved demo migration adds every missing capability and preserves human revisions idempotently", () => {
  const human = { id: "demo-visualization", content: "Human edit", revision: 4, editedBy: "human", sourceOfTruth: true };
  const t = { id: "demo", demo: true, createdAt: "2026-10-06T08:00:00.000Z", activeStepId: "s1", steps: [{ id: "s1" }], artifacts: [human], events: [] };
  const live = { ...t, id: "real", demo: false };
  const state = { tasks: [t, live], selectedId: "real", version: 2, settings: {} };
  const first = enrichDemoState(state), second = enrichDemoState(first);
  assert.equal(second.tasks[0].artifacts[0], human);
  assert.equal(second.tasks[1], live);
  assert.equal(second.selectedId, "real");
  assert.equal(second.tasks[0].artifacts.length, 5);
  assert.equal(second.tasks[0].events.length, 4);
  assert.equal(second.tasks[0], first.tasks[0]);
  assert.equal(new Set(second.tasks[0].artifacts.map(a => a.id)).size, 5);
  assert.ok(second.tasks[0].artifacts.slice(1).every(a => a.stepId === "s1"));
  assert.ok(second.tasks[0].events.every(e => e.title.startsWith("Support disponible :") && e.stepId === "s1"));
});
test("demo scripts update linked chart/table, filtering, cards and reset with simulated data", () => {
  const nodes = new Map();
  const element = (id) => {
    if (!nodes.has(id)) nodes.set(id, { id, value: id === "volume" ? "100" : id === "status" ? "all" : "", textContent: "", innerHTML: "", hidden: false, events: {}, attributes: {}, addEventListener(name, fn) { this.events[name] = fn; }, setAttribute(name, v) { this.attributes[name] = v; }, toggleAttribute(name, force) { if(force) this.attributes[name] = ""; else delete this.attributes[name]; } });
    return nodes.get(id);
  };
  const advance = ["nav", "ui", "check"].map(k => ({ ...element(k), dataset: { advance: k } }));
  const graph = ["mission", "design", "build", "review"].map(k => ({ ...element("node-" + k), dataset: { node: k } }));
  const context = { document: { getElementById: element, querySelectorAll: q => q === "[data-advance]" ? advance : graph } };
  const script = demoVisualization.match(/<script>([\s\S]*?)<\/script>/)[1];
  vm.runInNewContext(script, context, { timeout: 1000 });
  assert.equal(element("total").textContent, "Total simulé : 129 tâches.");
  element("volume").value = "150"; element("volume").events.input();
  assert.equal(element("total").textContent, "Total simulé : 195 tâches.");
  assert.match(element("chartRows").innerHTML, /<td>47<\/td>/);
  element("curve").attributes.hidden = "";
  element("lineMode").onclick(); assert.equal(element("bars").hidden, true); assert.equal("hidden" in element("curve").attributes, false);
  element("search").value = "Nova"; element("search").events.input(); assert.equal(element("count").textContent, "1 projet(s) fictif(s).");
  element("status").value = "done"; element("status").events.change(); assert.equal(element("count").textContent, "Aucun projet correspondant.");
  advance[0].onclick(); advance[0].onclick(); assert.equal(element("progress").textContent, "1 tâche(s) terminée(s) sur 3.");
  graph[3].onclick(); assert.match(element("role").textContent, /attend le design et la réalisation/);
  element("reset").onclick(); assert.equal(element("total").textContent, "Total simulé : 129 tâches."); assert.equal(element("count").textContent, "4 projet(s) fictif(s)."); assert.equal(element("progress").textContent, "0 tâche(s) terminée(s) sur 3.");
  assert.equal(element("bars").hidden, false);
});
test("demo source and engine theme fit the isolated support contract", () => {
  const examples = demoArtifacts("2026-10-06T08:00:00.000Z");
  assert.deepEqual(new Set(examples.map(a => a.type)), new Set(["visualization", "document", "code", "screenshot"]));
  assert.ok(demoVisualization.length < 500000);
  assert.doesNotMatch(demoVisualization, /(?:fetch\(|XMLHttpRequest|https?:\/\/|window\.djinn|require\()/);
  const { visualizationDocument } = load("src/visualization-document.ts");
  const { visualizationHtml } = require("../electron/visualization.cjs");
  for (const html of [visualizationDocument("<p>Example</p>", "token"), visualizationHtml("<p>Example</p>", "token")]) {
    assert.match(html, /--font-size-body:12px/);
    assert.match(html, /textarea\{font:inherit/);
    assert.ok(html.indexOf("Content-Security-Policy") < html.indexOf("<p>Example</p>"));
  }
});

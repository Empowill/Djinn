"use strict";
const test = require("node:test"),
  assert = require("node:assert/strict");
const {
  VisualizationRegistry,
  visualizationHtml,
  policy,
} = require("../electron/visualization.cjs");
test("visualization runs in its own document with no network, nested frames or native permissions", () => {
  const registry = new VisualizationRegistry();
  const result = registry.create(
    '<h1>Simulation</h1><script>document.body.dataset.ready="yes"</script>',
  );
  const html = registry.get(result.url);
  assert.ok(html.indexOf("Content-Security-Policy") < html.indexOf("<h1>"));
  assert.match(policy, /connect-src 'none'/);
  assert.match(policy, /frame-src 'none'/);
  assert.match(policy, /form-action 'none'/);
  assert.match(html, /djinn:visualization-height/);
  assert.ok(html.includes(result.token));
  assert.equal(registry.get("djinn-visualization://missing"), undefined);
  for (const url of [
    `https://${result.token}/`,
    `djinn-visualization://${result.token}/other`,
    `djinn-visualization://user@${result.token}/`,
    `djinn-visualization://${result.token}/?other`,
  ])
    assert.equal(registry.get(url), undefined);
  assert.throws(
    () => visualizationHtml("x".repeat(500001), "token"),
    /exceeds/,
  );
});

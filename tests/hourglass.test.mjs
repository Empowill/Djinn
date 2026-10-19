// The Hourglass (src/hourglass.tsx): its sections, its line on the flying carpet, its reads. Run with `go tool task
// test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { resources, screens, setupMockDom } from "./screens.mjs";

const { s, h } = await screens();
const { hourglassDjinn } = resources(s);

test("the Hourglass: its sections, the machine first, and the flying carpet's line to it", async () => {
  const { djinn } = hourglassDjinn();
  const html = s.renderToStaticMarkup(
    h(s.DjinnProvider, { djinn }, h(s.Hourglass, { wishes: [] })),
  );
  assert.match(
    html,
    /<div class="breadcrumbs"><strong>Hourglass<\/strong><\/div>/,
  );
  assert.match(html, /<h1>Hourglass<\/h1>/);
  // One section per live metric, the machine first: the whole resources panel, with its range.
  const sections = [...html.matchAll(/class="hourglass-section"/g)];
  assert.equal(sections.length, 1);
  assert.match(
    html,
    /<div id="hourglass-machine" class="hourglass-section"><section class="consumption-panel" aria-label="Resource consumption">/,
  );
  assert.match(html, /aria-pressed="true">30 min<\/button>/);

  // The flying carpet keeps one line of the machine's load, read for the status bar, with a link to the Hourglass.
  await djinn.store.readMachine();
  const line = s.renderToStaticMarkup(
    h(s.DjinnProvider, { djinn }, h(s.HourglassLine)),
  );
  assert.equal(
    line.replace(/<[^>]+>/g, ""),
    "1 of 4 workers running, memory 4 GB · 25% of the machine. See it live in the Hourglass",
  );
  assert.match(
    line,
    /<button type="button" class="text-link hourglass-line-open">See it live in the Hourglass<\/button>/,
  );
});

test("leaving the Hourglass stops its reads and its timers", async () => {
  const container = setupMockDom();
  const { reads, djinn } = hourglassDjinn();
  const timers = () =>
    process.getActiveResourcesInfo().filter((r) => r === "Timeout").length;
  const before = timers();
  const root = s.createRoot(container);
  await s.act(async () => {
    root.render(
      h(s.DjinnProvider, { djinn }, h(s.HourglassSections, { wishes: [] })),
    );
  });
  await s.act(() => new Promise((resolve) => setImmediate(resolve)));
  // Shown: the machine's series and the commands are read, and read again by timers.
  const shown = timers();
  s.act(() => root.unmount());
  // The store's watch, stopped with the provider, ends its last wait of 10 ms.
  await new Promise((resolve) => setTimeout(resolve, 50));
  const left = timers();
  // React's scheduler keeps a port open: it holds the test's process no more.
  for (const handle of process._getActiveHandles())
    if (handle?.constructor?.name === "MessagePort") handle.unref();
  assert.ok(reads.series >= 1 && reads.commands >= 1, JSON.stringify(reads));
  assert.ok(shown > before, `timers while shown: ${shown}, before: ${before}`);
  // Left: no timer stays.
  assert.equal(left, before);
});

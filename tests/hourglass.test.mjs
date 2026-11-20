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

// leaves mounts what renders, lets its first reads answer, unmounts it, and says the reads, the timers while shown and
// the timers left.
async function leaves(render) {
  const container = setupMockDom();
  const { reads, djinn } = hourglassDjinn();
  const timers = () =>
    process.getActiveResourcesInfo().filter((r) => r === "Timeout").length;
  const before = timers();
  const root = s.createRoot(container);
  await s.act(async () => {
    root.render(h(s.DjinnProvider, { djinn }, render()));
  });
  await s.act(() => new Promise((resolve) => setImmediate(resolve)));
  const shown = timers();
  s.act(() => root.unmount());
  // The store's watch, stopped with the provider, ends its last wait of 10 ms.
  await new Promise((resolve) => setTimeout(resolve, 50));
  const left = timers();
  // React's scheduler keeps a port open: it holds the test's process no more.
  for (const handle of process._getActiveHandles())
    if (handle?.constructor?.name === "MessagePort") handle.unref();
  return { reads, before, shown, left };
}

test("leaving the Hourglass stops its reads and its timers", async () => {
  // The machine: its series, read again by a timer; the commands are the Gates tab's.
  const machine = await leaves(() => h(s.HourglassSections, { wishes: [] }));
  assert.ok(machine.reads.series >= 1, JSON.stringify(machine.reads));
  assert.equal(machine.reads.commands, 0);
  assert.ok(
    machine.shown > machine.before,
    `timers while shown: ${machine.shown}, before: ${machine.before}`,
  );
  // Left: no timer stays.
  assert.equal(machine.left, machine.before);

  // The Gates tab: the gates live, the history of the gates over the last hour and the commands, each read again by a
  // timer while it shows; the series are the Machine tab's.
  const gates = await leaves(() => h(s.GatesTab, {}));
  assert.ok(
    gates.reads.gates >= 1 &&
      gates.reads.history.length >= 1 &&
      gates.reads.history.every((m) => m === 60) &&
      gates.reads.commands >= 1,
    JSON.stringify(gates.reads),
  );
  assert.equal(gates.reads.series, 0);
  // The history whole at first; the commands a page, brief, with the summary of them all.
  assert.equal(gates.reads.historyAfter[0], false);
  assert.deepEqual(gates.reads.commandAsks[0], {
    byTotal: true,
    brief: true,
    summary: true,
    pageSize: s.PAGE,
    pageToken: "",
  });
  // Over a day or 6 hours, the history is read over the window chosen, and less often: its timer stops all the same.
  for (const minutes of [360, 1440]) {
    const window = await leaves(() =>
      h(s.GatesTab, { initialPeriod: minutes }),
    );
    assert.deepEqual(window.reads.history, [minutes]);
    assert.equal(window.left, window.before);
  }
  assert.ok(gates.shown > gates.before);
  assert.equal(gates.left, gates.before);
});

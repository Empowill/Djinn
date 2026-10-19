// The resources panel (src/consumption-panel.tsx, src/resource-chart.tsx): the machine's charts, each worker. Run with
// `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { resources, screens } from "./screens.mjs";

const { s, h, live } = await screens();
const { t0, at, longTitle, resourceData, resourceCommands } = resources(s);

const resourceTask = {
  id: "task-w1",
  wishId: "wish-1",
  projectId: "p1",
  code: "W1",
  title: longTitle,
  status: s.TaskStatus.RUNNING,
  provider: s.Provider.CLAUDE,
  model: "claude-opus-5-5",
  startTime: at(t0),
  firstStartTime: at(t0),
  runs: [{ startTime: at(t0) }],
  turns: { run: 37, limit: 500, total: 412 },
  usage: { inputTokens: 10n, outputTokens: 2000n, costUsd: 1.25 },
};

test("the resources panel: the machine's charts, each worker's title line in full with its facts and charts, the commands of the active wishes' projects grouped under their gates", () => {
  const props = {
    initialData: resourceData(),
    initialCommands: resourceCommands,
    tasks: new Map([["task-w1", resourceTask]]),
    projects: [
      { id: "p1", name: "djinn" },
      { id: "p2", name: "old-project" },
    ],
    projectIds: new Set(["p1"]),
  };
  const resourcesHtml = s.renderToStaticMarkup(h(s.ConsumptionPanel, props));
  const gatesHtml = s.renderToStaticMarkup(
    h(s.ConsumptionPanel, { ...props, initialTab: "gates" }),
  );
  const html = resourcesHtml + gatesHtml;

  // The header: the ranges, up to a week, the one shown pressed.
  assert.match(html, /class="consumption-panel-heading">Resource consumption</);
  assert.match(html, /aria-pressed="true">30 min<\/button>/);
  for (const range of ["10 min", "2 h", "24 h", "7 days"])
    assert.match(html, new RegExp(`aria-pressed="false">${range}</button>`));

  // The machine: shown in the workers view (Resources) as well as Gates — CPU out of 100% and memory against the
  // machine's, both labelled, the load limit too.
  for (const tabHtml of [resourcesHtml, gatesHtml]) {
    assert.match(
      tabHtml,
      /<section class="resource-machine" aria-label="Machine">/,
    );
    assert.match(tabHtml, /8 cores · 16 GB/);
    assert.match(tabHtml, /now 62% · peak 62%/);
    assert.match(tabHtml, /5 GB of 16 GB/);
    assert.match(tabHtml, /class="resource-chart-ref-label"[^>]*>max 16 GB</);
    assert.match(
      tabHtml,
      /class="resource-chart-ref-label"[^>]*>load limit 80%</,
    );
    assert.match(
      tabHtml,
      /class="resource-chart-ref-label"[^>]*>load limit 12 GB</,
    );
    assert.match(tabHtml, /class="resource-chart-tick"[^>]*>100%</);
  }
  // Djinn apart: with everything it started, dashed, and its own process alone, dotted, both named.
  assert.match(
    resourcesHtml,
    /Dashed: Djinn with everything it started \(window, the Djinn&#x27;s agent, workers, gates\)/,
  );
  assert.match(
    resourcesHtml,
    /Dotted: Djinn&#x27;s own process alone \(djinn up\)/,
  );
  assert.match(resourcesHtml, /<path class="resource-chart-extra dash" d="M/);
  assert.match(resourcesHtml, /<path class="resource-chart-extra dot" d="M/);
  // Times under each chart: a few ticks of the clock.
  const ticks = [
    ...resourcesHtml.matchAll(
      /class="resource-chart-tick"[^>]*>(\d\d?:\d\d( [AP]M)?)</g,
    ),
  ];
  assert.ok(
    ticks.length >= 3 * 6,
    `time ticks under the six charts: ${ticks.length}`,
  );
  // The gates held, the project of no active wish left out.
  assert.match(
    resourcesHtml,
    /aria-label="Gate lint \(djinn\): go tool task lint\. Waited 6m40s, held 13m20s\."/,
  );
  assert.doesNotMatch(html, /Gate test \(old\)/);

  // Worker heatmaps: CPU and memory side by side, no switch.
  assert.doesNotMatch(resourcesHtml, /resource-heatmap-switch/);
  assert.match(
    resourcesHtml,
    /<div class="resource-heatmap-group cpu" role="group" aria-label="CPU"><span class="resource-heatmap-label">CPU<\/span>/,
  );
  assert.match(
    resourcesHtml,
    /<div class="resource-heatmap-group memory" role="group" aria-label="Memory"><span class="resource-heatmap-label">Memory<\/span>/,
  );
  assert.match(
    resourcesHtml,
    /class="resource-heatmap-cell cpu "[^>]*aria-label="W1: 75\.4%"/,
  );
  assert.match(
    resourcesHtml,
    /class="resource-heatmap-cell memory "[^>]*aria-label="W1: 60 MB"/,
  );

  // A worker: the title line holds its code, its agent and the whole title, never cut.
  const w1 = html.slice(
    html.indexOf('aria-label="W1 '),
    html.indexOf('aria-label="W2 '),
  );
  assert.match(
    w1,
    /<h4 class="resource-worker-title"><span class="agent-code">W1<\/span><span class="provider-badge provider-claude">claude<\/span><span class="resource-worker-name">Make the flying carpet&#x27;s resources read at a glance: [^<]*larger charts<\/span>/,
  );
  assert.doesNotMatch(w1, /…<\/span>/);
  // Its link to the task in its wish: a small eye on the title line.
  assert.match(
    w1,
    /<button class="icon-button resource-worker-open" type="button" aria-label="Open W1 in its wish"><svg/,
  );
  // The facts of its job, from its task.
  assert.match(w1, /<dt>Project<\/dt><dd>djinn<\/dd>/);
  assert.match(w1, /<dt>Turns<\/dt><dd><span class="task-turns"/);
  // Its turns over its whole life first, its run against its limit beside them.
  assert.match(w1, /<\/svg>412<span class="task-turns-run">37\/500<\/span>/);
  assert.match(w1, /<dt>Spent<\/dt>/);
  assert.match(w1, /<dt>Gate<\/dt><dd>held 13m20s, once<\/dd>/);
  assert.match(w1, /<dt>CPU<\/dt><dd>now 75\.4% · peak 85%<\/dd>/);
  // Its CPU and memory over time, larger, with the gate it held behind.
  assert.equal(
    (w1.match(/<figure class="resource-chart cpu">/g) ?? []).length,
    1,
  );
  assert.equal(
    (w1.match(/<figure class="resource-chart memory">/g) ?? []).length,
    1,
  );
  assert.match(w1, /<svg class="resource-chart-svg" width="480" height="190"/);
  assert.match(w1, /class="resource-chart-gate"/);
  // One scale for every worker: W1's CPU axis reaches W2's 140% peak, its memory axis W2's 128 MB.
  assert.match(html, /Every worker&#x27;s charts share one scale/);
  const w1Cpu = w1.slice(w1.indexOf('<figure class="resource-chart cpu">'));
  assert.match(w1Cpu, /class="resource-chart-tick"[^>]*>150%</);
  const w1Memory = w1.slice(
    w1.indexOf('<figure class="resource-chart memory">'),
  );
  assert.match(w1Memory, /class="resource-chart-tick"[^>]*>192 MB</);
  assert.match(
    w1,
    /role="group" aria-roledescription="chart" aria-label="CPU \(100% is one core\): now 75\.4% · peak 85%"/,
  );
  // A paused worker says so; without its task, the facts the series give.
  const w2 = html.slice(html.indexOf('aria-label="W2 '));
  assert.match(
    w2,
    /class="resource-worker is-paused"|resource-worker-paused">paused</,
  );
  assert.match(w2, /<dt>CPU<\/dt><dd>now 135\.2% · peak 140%<\/dd>/);

  // Where the time goes: djinn's commands, grouped under their gates, the most time first; the old project's not.
  assert.match(html, /Where the time goes: commands and their gates/);
  assert.match(html, /A gate is a named lock/);
  // One section per project of an active wish, saying first where most of its time goes.
  assert.match(
    html,
    /<section class="resource-project" aria-label="Project djinn"><h5 class="resource-project-name">Project djinn<span class="resource-section-detail">45m30s of commands<\/span><\/h5><p class="resource-project-top">Most of its time: go tool task e2e \(gate e2e\), 66% of it\.<\/p>/,
  );
  assert.doesNotMatch(html, /old-project|test-old-suite/);
  const table = html.slice(html.indexOf('class="resource-commands-table"'));
  const rows = [
    ...table.matchAll(
      /<tr class="resource-(gate|command)-row">([\s\S]*?)<\/tr>/g,
    ),
  ].map(
    ([, kind, row]) =>
      `${kind}: ${row
        .replace(/<[^>]+>/g, " ")
        .replace(/\s+/g, " ")
        .trim()}`,
  );
  assert.deepEqual(rows, [
    // 2 runs of the whole suite and 4 of some specs, 7 asked: 1800 s + 330 s.
    "gate: gate e2e 7 6 35m30s 5m55s 3m 30s 35m36s 5m56s",
    "command: go tool task e2e 2 2 30m 66% 15m 2m 1m 30m2s 15m1s",
    "command: go tool task e2e … 2 sets of arguments: -- e2e/wish-render.spec.ts, -- e2e/flight-plan.spec.ts 5 4 5m30s 12% 1m22s 1m 15s 5m34s 1m23s",
    "gate: gate lint 15 15 10m 40s 30s 2s 10m15s 41s",
    "command: go tool task lint 15 15 10m 22% 40s 30s 2s 10m15s 41s",
  ]);
  assert.match(
    html,
    /<th scope="col" rowSpan="2" class="num-col">Asked<\/th><th scope="col" rowSpan="2" class="num-col">Ran<\/th><th scope="colgroup" colSpan="2" class="resource-col-group">Time<\/th><th scope="colgroup" colSpan="2" class="resource-col-group">Waiting for the gate<\/th><th scope="colgroup" colSpan="2" class="resource-col-group">Holding the gate<\/th>/,
  );
  assert.equal(
    (html.match(/>total<\/th><th scope="col" class="num-col">mean</g) ?? [])
      .length,
    3,
  );
});

test("a chart says the value under the pointer and at the arrow keys, and keeps it as new samples come", () => {
  const points = [
    { t: 0, v: 10 },
    { t: 60_000, v: 95 },
    { t: 120_000, v: 20 },
  ];
  const chart = live(s.ResourceChart, {
    title: "CPU",
    unit: "cpu",
    summary: "now 20%",
    points,
    tMin: 0,
    tMax: 120_000,
    top: 100,
  });
  const plot = () => chart.one("resource-chart-plot");
  assert.equal(plot().tabIndex, 0);
  assert.equal(plot().role, "group");
  assert.doesNotMatch(chart.html(), /resource-chart-readout/);
  // The pointer at the middle of the plot (56 px of axis, 410 px of plot): the peak at one minute.
  const box = { getBoundingClientRect: () => ({ left: 0 }) };
  plot().onPointerMove({ clientX: 56 + 205, currentTarget: box });
  let html = chart.html();
  assert.match(html, /class="resource-chart-cursor"/);
  assert.match(html, /<strong>CPU 95%<\/strong>/);
  assert.match(html, /aria-live="polite">[^<]*CPU 95%</);
  // The keys walk the samples.
  const key = (k) => plot().onKeyDown({ key: k, preventDefault() {} });
  key("ArrowRight");
  assert.match(chart.html(), /<strong>CPU 20%<\/strong>/);
  key("Home");
  assert.match(chart.html(), /<strong>CPU 10%<\/strong>/);
  key("ArrowRight");
  assert.match(chart.html(), /<strong>CPU 95%<\/strong>/);
  // A new sample: the chart grows, the point shown stays the one chosen.
  chart.update({
    points: [...points, { t: 180_000, v: 55 }],
    tMax: 180_000,
    summary: "now 55%",
  });
  html = chart.html();
  assert.match(html, /<strong>CPU 95%<\/strong>/);
  assert.match(html, /aria-label="CPU: now 55%"/);
  key("End");
  assert.match(chart.html(), /<strong>CPU 55%<\/strong>/);
  // The pointer leaves: the readout goes, the last sample marked.
  plot().onPointerLeave();
  html = chart.html();
  assert.doesNotMatch(html, /resource-chart-readout/);
  assert.match(html, /<circle class="resource-chart-dot"/);

  // Memory says GB and its share of the machine.
  const memory = live(s.ResourceChart, {
    title: "Memory",
    unit: "memory",
    summary: "",
    points: [{ t: 0, v: 4 * 1024 ** 3 }],
    tMin: 0,
    tMax: 60_000,
    top: 16 * 1024 ** 3,
    memoryTotal: 16 * 1024 ** 3,
  });
  memory
    .one("resource-chart-plot")
    .onKeyDown({ key: "End", preventDefault() {} });
  assert.match(
    memory.html(),
    /<strong>Memory 4 GB · 25% of the machine<\/strong>/,
  );
});

test("a worker's eye opens its task in its wish, through the window's focus", () => {
  const opened = [];
  const row = live(s.WorkerRow, {
    worker: resourceData().workers[0],
    memoryTotal: 0,
    tMin: 0,
    tMax: 1,
    onOpen: (wishId, taskId) => opened.push([wishId, taskId]),
  });
  const eye = row.one("resource-worker-open");
  assert.equal(eye.label, "Open W1 in its wish");
  eye.onClick();
  assert.deepEqual(opened, [["wish-1", "task-w1"]]);
  // The window's focus takes it to the Tasks tab, where the task's card shows.
  assert.equal(s.tabOf("task-task-w1"), "tasks");
});

test("the disk tab: whole-machine bar with guard marks, Machine items grouped by risk with cleanup commands and all-safe row, and Djinn folders with worker isolated Go caches", () => {
  const gib = 1024n ** 3n;
  const disk = {
    availableBytes: 394n * gib,
    totalBytes: 993n * gib,
    folders: [
      { path: "/home/dev/.cache/go-build", sizeBytes: 80n * gib },
      {
        path: "/home/dev/.config/djinn/projects/p1/worktrees",
        sizeBytes: 15n * gib,
      },
      { path: "/home/dev/.npm", sizeBytes: 2n * gib },
      { path: "/home/dev/.cache/ms-playwright", sizeBytes: 1n * gib },
      { path: "/home/dev/.config/djinn/logs", sizeBytes: 1n * gib },
      { path: "/home/dev/.config/djinn", sizeBytes: 16n * gib },
    ],
    items: [
      {
        kind: "go-build",
        path: "~/.cache/go-build",
        sizeBytes: 80n * gib,
        reclaimableBytes: 63n * gib,
        risk: s.DiskRisk.REBUILD,
        command: "find ~/.cache/go-build -type f -mmin +360 -delete",
      },
      {
        kind: "go-isolated",
        path: "~/.cache/*-go-build",
        sizeBytes: 33n * gib,
        reclaimableBytes: 33n * gib,
        risk: s.DiskRisk.REBUILD,
        command: "rm -rf ~/.cache/*-go-build",
        count: 9,
        djinn: true,
      },
      {
        kind: "go-mod",
        path: "~/go/pkg/mod",
        sizeBytes: 31n * gib,
        reclaimableBytes: 31n * gib,
        risk: s.DiskRisk.REBUILD,
        command: "go clean -modcache",
      },
      {
        kind: "docker-builder",
        path: "docker builder",
        sizeBytes: 19n * gib,
        reclaimableBytes: 13n * gib,
        risk: s.DiskRisk.REBUILD,
        command: "docker builder prune -f",
      },
      {
        kind: "docker-images",
        path: "docker images",
        sizeBytes: 16n * gib,
        reclaimableBytes: 7n * gib,
        risk: s.DiskRisk.REBUILD,
        command: "docker image prune -a -f",
      },
      {
        kind: "jetbrains",
        path: "~/.cache/JetBrains",
        sizeBytes: 6n * gib,
        reclaimableBytes: 6n * gib,
        risk: s.DiskRisk.LOSS,
        command: "rm -rf ~/.cache/JetBrains",
      },
      {
        kind: "pnpm",
        path: "~/.local/share/pnpm + ~/.cache/pnpm",
        sizeBytes: 3n * gib,
        reclaimableUnknown: true,
        risk: s.DiskRisk.SAFE,
        command: "pnpm store prune",
      },
      {
        kind: "docker-volumes",
        path: "docker volume",
        sizeBytes: 3n * gib,
        reclaimableBytes: 1n * gib,
        risk: s.DiskRisk.LOSS,
        command: "docker volume prune -f",
      },
      {
        kind: "pip",
        path: "~/.cache/pip",
        sizeBytes: 3n * gib,
        reclaimableBytes: 3n * gib,
        risk: s.DiskRisk.REBUILD,
        command: "pip cache purge",
      },
      {
        kind: "uv",
        path: "~/.cache/uv",
        sizeBytes: 2n * gib,
        reclaimableUnknown: true,
        risk: s.DiskRisk.SAFE,
        command: "uv cache prune",
      },
      {
        kind: "npm",
        path: "~/.npm",
        sizeBytes: 2n * gib,
        reclaimableBytes: 2n * gib,
        risk: s.DiskRisk.REBUILD,
        command: "npm cache clean --force",
      },
    ],
  };

  const sec = live(s.DiskSection, { disk });
  let html = sec.html();

  // Whole-disk bar, guard marks, and legend.
  assert.match(html, /394 GB free of 993 GB/);
  assert.match(html, /aria-label="599 GB used of 993 GB"/);
  assert.match(html, /title="30 GB free: Go build cache trimmed"/);
  assert.match(html, /title="10 GB free: push checks held"/);
  assert.match(html, /Go 144 GB/);
  assert.match(html, /Docker 38 GB/);
  assert.match(html, /Djinn 16 GB/);
  assert.match(html, /Other caches 17 GB/);
  assert.match(html, /Unmeasured 384 GB/);
  assert.match(html, /Free 394 GB/);

  // Sub-tabs with their totals (Djinn 17 GB folders + 33 GB isolated Go caches = 50 GB; Machine = 165 GB).
  assert.match(html, /Djinn and its workers\s*<b>50 GB<\/b>/);
  assert.match(html, /Machine\s*<b>165 GB<\/b>/);

  // Machine tab: three risk groups (Safe, Rebuilds, Data loss) with totals and cleanup commands, plus all-safe row.
  assert.match(
    html,
    /Safe[\s\S]*?nothing is lost, nothing to redo[\s\S]*?frees \?/,
  );
  assert.match(
    html,
    /Rebuilds[\s\S]*?time or network on next use[\s\S]*?frees 119 GB/,
  );
  assert.match(
    html,
    /Data loss[\s\S]*?what is removed does not come back[\s\S]*?frees 7 GB/,
  );
  assert.match(
    html,
    /<code>find ~\/\.cache\/go-build -type f -mmin \+360 -delete<\/code>/,
  );
  assert.match(html, /<code>go clean -modcache<\/code>/);
  assert.match(html, /<code>docker builder prune -f<\/code>/);
  assert.match(html, /<code>docker volume prune -f<\/code>/);
  assert.match(
    html,
    /All “safe” at once<\/span><code>pnpm store prune &amp;&amp; uv cache prune<\/code>/,
  );

  // Switch to the Djinn tab: Djinn folders + isolated Go caches (9 folders) on the Djinn side, without duplicating go-build or npm.
  sec.all("resource-disk-subtab")[0].props.onClick();
  html = sec.html();
  assert.match(html, /\/home\/dev\/\.config\/djinn\/projects\/p1\/worktrees/);
  assert.match(html, /Isolated Go caches \(9 folders\)/);
  assert.match(html, /<code>rm -rf ~\/\.cache\/\*-go-build<\/code>/);
  assert.doesNotMatch(html, /\/home\/dev\/\.cache\/go-build/);
  assert.doesNotMatch(html, /\/home\/dev\/\.npm/);
  assert.match(
    html,
    /Djinn removes a worktree when its task ends; isolated Go caches come from worker builds that set their own GOCACHE\./,
  );
});

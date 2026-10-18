// The glance (src/glance.tsx): every worker in one order, the merges, the queue and the repairs. Run with `go tool task
// test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { screens, wish } from "./screens.mjs";

const { s, h } = await screens();

// The glance (Decision Q08, mockup L04 v2, W219): beside the questions and on a wish's page, each wish's workers in
// one order whatever their kind: what is in progress on top, the one begun first above (work, investigation,
// investigator, lit ifrit), then the paused ones, a circled pause with why on hover, then what waits (under the task it
// waits on when that one waits too, else naming it and its wish), then what is planned; an urgent task carries its
// label. Below them, the watchers, a line each, with what they said last, since when, and the request they watch.
test("the glance lists every worker in one order by state: in progress, paused, waiting, planned; then the watchers", async () => {
  const at = (seconds) => ({ seconds: BigInt(seconds), nanos: 0 });
  const nowS = Math.floor(Date.now() / 1000);
  const lamp = wish(
    "01a1440a-0000-7000-8000-000000000001",
    "Ship the lamp",
    s.WishState.ACTIVE,
    1,
  );
  const qol = wish(
    "01a1440a-0000-7000-8000-000000000002",
    "Djinn QoL",
    s.WishState.ACTIVE,
    2,
  );
  const task = (wishId, code, status, extra = {}) => ({
    id: `${wishId.slice(-1)}-${code}`,
    wishId,
    code,
    title: `Task ${code}`,
    status,
    dependsOn: [],
    createTime: at(1),
    ...extra,
  });
  const L = (code, status, extra) => task(lamp.id, code, status, extra);
  const id = (wishId, code) => `${wishId.slice(-1)}-${code}`;
  const begun = (seconds) => ({ firstStartTime: at(seconds) });
  const tasks = {
    [lamp.id]: [
      L("T1", s.TaskStatus.PENDING, {
        kind: s.TaskKind.AZIMA,
        azima: {
          state: s.AzimaState.IN_PROGRESS,
          pr: { url: "https://github.com/acme/lamp/pull/40" },
        },
      }),
      // Running, begun at 100; it forks W17, cut short.
      L("W1", s.TaskStatus.RUNNING, {
        partOf: id(lamp.id, "T1"),
        forkOf: "W17",
        ...begun(100),
      }),
      // W3 waits on W2, which waits on W1, running: W2 names W1 in its own place, W3 sits under W2.
      L("W3", s.TaskStatus.PENDING, { dependsOn: [id(lamp.id, "W2")] }),
      L("W2", s.TaskStatus.PENDING, { dependsOn: [id(lamp.id, "W1")] }),
      // W4's dependency is done: planned.
      L("W4", s.TaskStatus.PENDING, { dependsOn: [id(lamp.id, "W10")] }),
      L("W5", s.TaskStatus.PENDING, { dependsOn: [id(qol.id, "W26")] }),
      // The investigator began before W1: above it.
      L("W6", s.TaskStatus.RUNNING, {
        role: s.TaskRole.INVESTIGATOR,
        question: "Q4",
        ...begun(50),
      }),
      L("W7", s.TaskStatus.RUNNING, {
        provider: s.Provider.WATCH,
        babysit: {
          url: "https://gitlab.com/acme/lamp/-/merge_requests/3",
          number: 3,
        },
        lastLine: "GREEN",
        lastLineTime: at(nowS - 720),
        ...begun(10),
      }),
      // Its request is its azima's.
      L("W8", s.TaskStatus.RUNNING, {
        provider: s.Provider.WATCH,
        partOf: id(lamp.id, "T1"),
        lastLine: "CI_FAILED",
        ...begun(20),
      }),
      L("W9", s.TaskStatus.STOPPED, {
        provider: s.Provider.WATCH,
        error: "stopped on request",
        ...begun(5),
      }),
      L("W10", s.TaskStatus.DONE, { endTime: at(5) }),
      L("W11", s.TaskStatus.FAILED, { endTime: at(5) }),
      L("W12", s.TaskStatus.WAITING),
      // A lit ifrit, listening; an urgent task; an investigation: all in progress, by when they began.
      L("W13", s.TaskStatus.RUNNING, {
        kind: s.TaskKind.IFRIT,
        ifrit: { state: s.IfritState.LISTENING },
        ...begun(200),
      }),
      L("W20", s.TaskStatus.RUNNING, { urgent: true, ...begun(300) }),
      L("W21", s.TaskStatus.RUNNING, {
        kind: s.TaskKind.INVESTIGATION,
        ...begun(250),
      }),
      // Paused: held still, stopped on request, its provider's limit to wait for; cut short and forked: gone.
      L("W14", s.TaskStatus.PAUSED, {
        runs: [{ reason: "pause" }],
        ...begun(20),
      }),
      L("W15", s.TaskStatus.STOPPED, {
        error: "stopped on request",
        ...begun(30),
      }),
      L("W16", s.TaskStatus.RESUMING, {
        resumeAfter: at(nowS + 3600),
        ...begun(10),
      }),
      L("W17", s.TaskStatus.INTERRUPTED, begun(5)),
      L("W18", s.TaskStatus.CANCELLED, begun(5)),
    ],
    [qol.id]: [task(qol.id, "W26", s.TaskStatus.RUNNING)],
  };
  const all = [...tasks[lamp.id], ...tasks[qol.id]];
  const titles = new Map([
    [lamp.id, lamp.title],
    [qol.id, qol.title],
  ]);
  const glance = s.glanceOf(
    tasks[lamp.id],
    (x) => all.find((t) => t.id === x),
    (x) => titles.get(x),
  );
  assert.deepEqual(
    glance.rows.map((r) => [
      r.task.code,
      r.state,
      r.depth,
      r.after ?? "",
      r.afterWish ?? "",
    ]),
    [
      ["W6", "investigate", 0, "", ""],
      ["W1", "run", 0, "", ""],
      ["W13", "run", 0, "", ""],
      ["W21", "run", 0, "", ""],
      ["W20", "run", 0, "", ""],
      ["W16", "paused", 0, "", ""],
      ["W14", "paused", 0, "", ""],
      ["W15", "paused", 0, "", ""],
      ["W2", "wait", 0, "W1", ""],
      ["W3", "wait", 1, "W2", ""],
      ["W5", "wait", 0, "W26", "Djinn QoL"],
      ["W4", "plan", 0, "", ""],
    ],
  );
  assert.deepEqual(
    glance.watchers.map((x) => [x.task.code, x.state, x.url]),
    [
      ["W7", "watch", "https://gitlab.com/acme/lamp/-/merge_requests/3"],
      ["W8", "watch", "https://github.com/acme/lamp/pull/40"],
      ["W9", "paused", ""],
    ],
  );
  // A dependency no wish read holds nothing back: planned.
  const lone = s.glanceOf(
    [L("W5", s.TaskStatus.PENDING, { dependsOn: ["gone"] })],
    () => undefined,
    () => undefined,
  );
  assert.deepEqual(
    lone.rows.map((r) => r.state),
    ["plan"],
  );
  // The side panel counts the same: 4 paused (3 workers and a watcher), W17 forked into W1 none.
  const counted = s.runningOf(tasks[lamp.id]);
  assert.deepEqual(
    [counted.running, counted.investigating, counted.ifrits, counted.paused],
    [3, 1, 1, 4],
  );

  const transport = s.createRouterTransport(({ service }) => {
    service(s.WishService, { list: () => ({ wishes: [lamp, qol] }) });
    service(s.TaskService, {
      list: (req) => ({ tasks: tasks[req.wishId] ?? [] }),
    });
    service(s.QuestionService, { list: () => ({ questions: [] }) });
    service(s.BlockService, { list: () => ({ blocks: [] }) });
  });
  const djinn = s.createDjinn(transport, 10);
  const closes = [lamp, qol].map((w) => djinn.store.open(w.id));
  await djinn.store.changed(lamp.id, [s.Change.WISH]);
  await djinn.store.changed(qol.id, [s.Change.WISH]);
  closes.forEach((close) => close());
  const render = (node) =>
    s.renderToStaticMarkup(h(s.DjinnProvider, { djinn }, node));

  // The flying carpet: each active wish under its title, which opens it, with its counts, each worker once.
  const plan = render(
    h(s.FlightPlan, { wishes: [lamp, qol], onOpen() {}, onToast() {} }),
  );
  assert.doesNotMatch(plan, /plan-wish/);
  assert.match(plan, /<section class="glance" aria-label="What runs">/);
  assert.match(
    plan,
    /<button type="button" title="Open this wish">Ship the lamp<\/button><span class="glance-count">1 ifrit lit · 3 running · 1 investigating · 4 paused · 4 to come · 2 watching<\/span>/,
  );
  assert.match(
    plan,
    /<button type="button" title="Open this wish">Djinn QoL<\/button><span class="glance-count">1 running<\/span>/,
  );
  // One list, in the glance's order: no list of investigators apart.
  const wide = plan.slice(
    plan.indexOf('class="glance-wide"'),
    plan.indexOf('class="glance-narrow"'),
  );
  const lampPart = wide.slice(0, wide.indexOf("Djinn QoL</button>"));
  assert.doesNotMatch(plan, /glance-investigators/);
  assert.deepEqual(
    [
      ...lampPart.matchAll(
        /class="glance-row ([a-z ]+)" title="[^"·]+· (W\d+) /g,
      ),
    ].map((m) => `${m[2]} ${m[1]}`),
    [
      "W6 investigate",
      "W1 run",
      "W13 run",
      "W21 run",
      "W20 run",
      "W16 paused",
      "W14 paused",
      "W15 paused",
      "W2 wait",
      "W3 wait child",
      "W5 wait",
      "W4 plan",
      "W7 watch watch",
      "W8 watch watch",
      "W9 watch paused",
    ],
  );
  // Running blue; the investigator violet, naming its question; the lit ifrit's flame.
  assert.match(
    plan,
    /<li class="glance-item"><button type="button" class="glance-row run" title="Running · W1 Task W1"><span class="glance-dot run" role="img" aria-label="Running"><\/span><span class="glance-code">W1<\/span>/,
  );
  assert.match(
    plan,
    /class="glance-row investigate" title="Investigating · W6 Task W6"><span class="glance-dot investigate"[\s\S]*?<span class="glance-after">on Q4<\/span>/,
  );
  assert.match(
    plan,
    /class="glance-row run" title="[^"]+ · W13 Task W13"><span class="glance-flame tone-ifrit"/,
  );
  // An urgent task and an investigation carry the tasks view's labels.
  assert.match(
    plan,
    /title="Running · W20 Task W20">[\s\S]*?<span class="glance-title"><span class="urgent-label" title="[^"]+">Urgent<\/span>Task W20/,
  );
  assert.match(
    plan,
    /title="Running · W21 Task W21">[\s\S]*?<span class="glance-title"><span class="investigation-label" title="[^"]+">Investigation<\/span>Task W21/,
  );
  // Paused: a circled pause, its word, and why on hover.
  assert.match(
    plan,
    /<button type="button" class="glance-row paused" title="Stopped · W15 Task W15\nstopped on request"><span class="glance-pause tone-paused" role="img" aria-label="Stopped"><svg[^>]*lucide-circle-pause[\s\S]*?<span class="glance-after">Stopped<\/span>/,
  );
  assert.match(plan, /title="Paused · W14 Task W14\npause"/);
  assert.match(
    plan,
    /title="[^"]+ · W16 Task W16\nresumes after \d\d:\d\d[^"]*, once its usage limit resets"/,
  );
  assert.doesNotMatch(plan, /title="[^"]*· (W17|W18) /);
  // Waiting: W2 names W1, at work, in its own place; W3 under W2.
  assert.match(
    plan,
    /<button type="button" class="glance-row wait" title="Waits · W2 Task W2">[\s\S]*?<span class="glance-after">waits on W1<\/span>/,
  );
  assert.match(
    plan,
    /<button type="button" class="glance-row wait child" title="Waits · W3 Task W3" style="--depth:1">/,
  );
  assert.match(
    plan,
    /<li class="glance-item"><button type="button" class="glance-row wait" title="Waits · W5 Task W5">[\s\S]*?<span class="glance-after">waits on W26 · Djinn QoL<\/span>/,
  );
  assert.match(
    plan,
    /<li class="glance-item"><button type="button" class="glance-row plan" title="Planned · W4 Task W4"><span class="glance-dot plan"/,
  );
  // The watchers, unfolded: what they said last, since when (12 minutes ago when the test started, a second more once
  // it crossed one), and their request, a link out.
  assert.match(
    plan,
    /<li class="glance-item glance-watcher watch"><button type="button" class="glance-row watch watch" title="Watching · W7 Task W7"><span class="glance-eye tone-watching" role="img" aria-label="Watching"><svg[^>]*lucide-eye[\s\S]*?<span class="glance-code">W7<\/span><span class="glance-title"><span class="glance-watch-line">GREEN<\/span><span class="glance-since">12m(?:1s)?<\/span><\/span><\/button><a href="https:\/\/gitlab.com\/acme\/lamp\/-\/merge_requests\/3" class="glance-link" target="_blank" rel="noopener noreferrer">!3<svg[^>]*lucide-external-link/,
  );
  assert.match(
    plan,
    /<span class="glance-watch-line">CI_FAILED<\/span>[\s\S]*?<a href="https:\/\/github.com\/acme\/lamp\/pull\/40" class="glance-link"[^>]*>PR #40<svg/,
  );
  assert.match(
    plan,
    /class="glance-row watch paused" title="Stopped · W9 Task W9\nstopped on request"><span class="glance-pause tone-paused"/,
  );
  assert.doesNotMatch(
    plan,
    /glance-row[^"]*" title="[^"]*· W9 [^"]*"[^>]*>[\s\S]{0,400}glance-link/,
  );
  // Done, failed, waiting for your answer, azimas: not in the glance.
  assert.doesNotMatch(plan, /title="[^"]*· (W10|W11|W12|T1) /);
  // Gold is for what waits on you: never in the glance.
  assert.doesNotMatch(plan, /glance[^"]*waiting/);
  // The legend names the states shown, the pause among them.
  assert.match(
    plan,
    /<p class="glance-legend">.*running.*investigating.*<span class="glance-pause tone-paused" aria-hidden="true">.*paused.*waits on a task.*planned.*watches<\/span><\/p>/,
  );
  // Narrow: one line per wish, a mark per worker in the same order, the watchers counted, the paused one marked.
  assert.match(
    plan,
    /<button type="button" class="glance-line" title="Open this wish"><span class="name">Ship the lamp<\/span><span class="glance-line-marks">(<span class="glance-(dot (run|wait|plan|investigate)|flame tone-ifrit|pause tone-paused)" role="img" aria-label="[^"]+" title="[^"]+">(<svg[\s\S]*?<\/svg>)?<\/span>){12}<span class="glance-line-watch" title="2 watching">/,
  );

  // A wish's page: the wish alone, without its title; the task across wishes still names its wish.
  const view = render(h(s.WishView, { wish: lamp, onToast() {} }));
  assert.match(view, /<section class="glance" aria-label="What runs">/);
  assert.doesNotMatch(view, /glance-head|Djinn QoL<\/button>/);
  assert.match(view, /waits on W26 · Djinn QoL/);
  assert.match(view, /class="glance-row paused" title="Stopped · W15 /);
  assert.match(
    view,
    /<div class="glance-line"><span class="name">Ship the lamp<\/span>/,
  );
  // Its journal and rights below both columns.
  assert.match(view, /<div class="main-after">/);

  // The side panel counts the paused workers, beside the others, and on the folded rail's corner.
  const side = (collapsed) =>
    s.renderToStaticMarkup(
      h(s.WishSidebar, {
        wishes: [lamp],
        projects: [],
        counts: { [lamp.id]: { questions: 0, merging: 0, ...counted } },
        selectedWishId: "",
        selectedProjectId: "",
        collapsed,
        onSelectWish() {},
        onSelectProject() {},
        onMove() {},
        onNewProject() {},
      }),
    );
  assert.match(
    side(false),
    /<span class="wish-sign tone-paused" title="4 workers paused: stopped, their work kept until they go on"[^>]*><svg[^>]*lucide-circle-pause[\s\S]*?<b>4<\/b>/,
  );
  assert.match(
    side(true),
    /<span class="wish-paused-corner" title="4 workers paused: [^"]+" aria-label="[^"]+" role="img"><svg[^>]*lucide-circle-pause[\s\S]*?<b>4<\/b><\/span>/,
  );

  // A wish where nothing runs or is planned says so.
  const quiet = s.glanceOf(
    [],
    () => undefined,
    () => undefined,
  );
  assert.deepEqual([quiet.rows, quiet.watchers], [[], []]);
  const calm = wish(
    "01a1440a-0000-7000-8000-000000000003",
    "Calm",
    s.WishState.ACTIVE,
    3,
  );
  const calmPlan = render(
    h(s.FlightPlan, { wishes: [calm], onOpen() {}, onToast() {} }),
  );
  assert.match(
    calmPlan,
    /<li class="glance-quiet">Nothing runs, nothing planned\.<\/li>/,
  );
  assert.match(
    calmPlan,
    /<span class="glance-line-marks zero">nothing<\/span>/,
  );
  assert.doesNotMatch(calmPlan, /glance-legend/);
});

// Beside the workers, the work on its way into each wish's branch and the repair workers (W137): the batch merging with
// the check it runs and for how long, a push the checks hold with the tests that failed, the merges waiting in line
// in the order Djinn merges them, and the repair workers apart, a wrench in place of their dot.
test("the glance shows the merges in progress, the queue, a held push and the repair workers", async () => {
  const at = (seconds) => ({ seconds: BigInt(seconds), nanos: 0 });
  const nowS = Math.floor(Date.now() / 1000);
  const qolId = "01a1440a-0000-7000-8000-000000000011";
  const lampId = "01a1440a-0000-7000-8000-000000000012";
  const id = (code) => `q-${code}`;
  const task = (code, status, extra = {}) => ({
    id: id(code),
    wishId: qolId,
    projectId: "p1",
    code,
    title: `Task ${code}`,
    status,
    dependsOn: [],
    createTime: at(1),
    ...extra,
  });
  const done = (code, integration, extra = {}) =>
    task(code, s.TaskStatus.DONE, {
      endTime: at(10),
      integration: { branch: "feat/qol", ...integration },
      ...extra,
    });
  const tasks = [
    done("W1", { state: s.IntegrationState.INTEGRATING }),
    done("W2", {
      state: s.IntegrationState.INTEGRATING,
      check: "lint",
      updateTime: at(nowS - 80),
    }),
    done(
      "W3",
      {
        state: s.IntegrationState.PENDING,
        reason: "waits for W9, which repairs feat/qol",
      },
      { endTime: at(20) },
    ),
    done("W4", { state: s.IntegrationState.PENDING, alone: true }),
    done("W5", { state: s.IntegrationState.COMMITTED, sha: "abc" }),
    done("W6", { state: s.IntegrationState.RED, correctedBy: id("W9") }),
    done("W7", {
      state: s.IntegrationState.UNCOMMITTED,
      reviewedBy: id("W10"),
    }),
    task("W9", s.TaskStatus.RUNNING, {
      correction: {
        attempt: 2,
        failure: { state: s.IntegrationState.RED, taskIds: [id("W6")] },
      },
    }),
    task("W10", s.TaskStatus.PENDING, {
      review: { attempt: 1, taskIds: [id("W7")] },
    }),
    task("W11", s.TaskStatus.RUNNING, {
      correction: { attempt: 1, failure: { mainSha: "f00d" } },
    }),
    task("W12", s.TaskStatus.RUNNING, {
      correction: {
        attempt: 1,
        failure: { state: s.IntegrationState.RED },
      },
    }),
    task("W13", s.TaskStatus.RUNNING),
  ];
  const qol = wish(qolId, "Djinn QoL", s.WishState.ACTIVE, 1, {
    pushes: [
      {
        projectId: "p1",
        held: "test: go tool task test exited 1",
        correctedBy: id("W12"),
        failure: {
          command: "go tool task test",
          output: "ok  pkg/a\n--- FAIL: TestLogin\n--- FAIL: TestLogout\nFAIL",
        },
        run: { branch: "feat/qol", stage: s.PushStage.HELD, note: "test" },
      },
    ],
  });
  const lamp = wish(lampId, "Ship the lamp", s.WishState.ACTIVE, 2, {
    pushes: [
      {
        projectId: "p2",
        run: {
          branch: "feat/lamp",
          stage: s.PushStage.CHECKING,
          check: "test",
          startTime: at(nowS - 65),
        },
      },
    ],
  });

  const glance = s.glanceOf(
    tasks,
    () => undefined,
    () => undefined,
    qol,
  );
  // The workers alone in the rows: the repair workers are apart.
  assert.deepEqual(
    glance.rows.map((r) => r.task.code),
    ["W13"],
  );
  assert.deepEqual(
    glance.merges.map((m) =>
      m.kind === "merging"
        ? [m.kind, m.branch, m.tasks.map((x) => x.code).join(","), m.check]
        : m.kind === "held"
          ? [m.kind, m.branch, m.tests.join(","), m.by?.code]
          : m.kind === "queued"
            ? [m.kind, m.task.code, m.reason]
            : [m.kind],
    ),
    [
      ["merging", "feat/qol", "W1,W2", "lint"],
      ["held", "feat/qol", "TestLogin,TestLogout", "W12"],
      // In the order Djinn merges them: the first ended first.
      ["queued", "W4", "merges alone"],
      ["queued", "W3", "waits for W9, which repairs feat/qol"],
    ],
  );
  assert.deepEqual(
    glance.repairs.map((r) => [r.task.code, r.kind, r.state, r.of.join(",")]),
    [
      ["W9", "red", "run", "W6"],
      ["W11", "main", "run", ""],
      ["W12", "push", "run", ""],
      ["W10", "review", "plan", "W7"],
    ],
  );
  // The tests a check's output names as failed.
  assert.deepEqual(
    s.failingTests("ok\n--- FAIL: TestA\n    --- FAIL: TestA/sub\nFAIL"),
    ["TestA", "TestA/sub"],
  );
  assert.deepEqual(
    s.failingTests(
      "--- FAIL: TestA\nFailed: internal/harness TestA, internal/ui TestB\n",
    ),
    ["internal/harness TestA", "internal/ui TestB"],
  );
  assert.deepEqual(
    s.failingTests(
      "  1) [chromium] › e2e/glance.spec.ts:12:3 › the glance\n  ✘  2 [chromium] › e2e/wish-render.spec.ts:40:1 › opens (1.2s)",
    ),
    ["e2e/glance.spec.ts:12", "e2e/wish-render.spec.ts:40"],
  );

  const details = { [qolId]: tasks, [lampId]: [] };
  const transport = s.createRouterTransport(({ service }) => {
    service(s.WishService, { list: () => ({ wishes: [qol, lamp] }) });
    service(s.TaskService, {
      list: (req) => ({ tasks: details[req.wishId] ?? [] }),
    });
    service(s.QuestionService, { list: () => ({ questions: [] }) });
    service(s.BlockService, { list: () => ({ blocks: [] }) });
  });
  const djinn = s.createDjinn(transport, 10);
  const closes = [qol, lamp].map((w) => djinn.store.open(w.id));
  await djinn.store.changed(qolId, [s.Change.WISH]);
  await djinn.store.changed(lampId, [s.Change.WISH]);
  closes.forEach((close) => close());
  const plan = s.renderToStaticMarkup(
    h(
      s.DjinnProvider,
      { djinn },
      h(s.FlightPlan, { wishes: [qol, lamp], onOpen() {}, onToast() {} }),
    ),
  );
  assert.match(
    plan,
    /Djinn QoL<\/button><span class="glance-count">1 running · 2 merging · 2 to merge · push held · 4 repairs<\/span>/,
  );
  assert.match(
    plan,
    /<ul class="glance-list glance-flow" aria-label="Merges and repairs">/,
  );
  // The batch merging, its check and for how long; a click opens its first task.
  assert.match(
    plan,
    /<button type="button" class="glance-row glance-merge merging" title="W1, W2 → feat\/qol\nW1 Task W1\nW2 Task W2"><span class="glance-mark run" role="img" aria-label="merging"><svg[^>]*lucide-git-merge[\s\S]*?<span class="glance-title glance-span">W1, W2 → feat\/qol<span class="glance-after" title="through the gate lint">checks lint<span class="glance-since">1m2\ds<\/span>/,
  );
  // The held push, salmon, the tests that failed.
  assert.match(
    plan,
    /<button type="button" class="glance-row glance-merge held" title="Push of feat\/qol held\ntest: go tool task test exited 1"><span class="glance-mark held" role="img" aria-label="push held"><svg[^>]*lucide-cloud-off[\s\S]*?Push of feat\/qol held<span class="glance-after">test red: TestLogin, TestLogout<\/span>/,
  );
  // The queue, its reasons.
  assert.match(
    plan,
    /class="glance-row glance-merge queued" title="W4 Task W4\nmerges alone"><span class="glance-mark wait"[\s\S]*?W4 → feat\/qol<span class="glance-after">merges alone<\/span>[\s\S]*?W3 → feat\/qol<span class="glance-after">waits for W9, which repairs feat\/qol<\/span>/,
  );
  // The repair workers: a wrench, blue at work, grey to start, saying what each repairs.
  assert.match(
    plan,
    /class="glance-row glance-repair run" title="Running · W9 Task W9"><span class="glance-mark run" role="img" aria-label="repair worker · Running"><svg[^>]*lucide-wrench[\s\S]*?<span class="glance-code">W9<\/span><span class="glance-title">makes the checks of W6 pass<span class="glance-after">Running · attempt 2<\/span>/,
  );
  assert.match(
    plan,
    /W11<\/span><span class="glance-title">settles the merge of main</,
  );
  assert.match(
    plan,
    /W12<\/span><span class="glance-title">makes the push checks pass</,
  );
  assert.match(
    plan,
    /class="glance-row glance-repair plan" title="Planned · W10 Task W10"><span class="glance-mark wait"[\s\S]*?commits the work W7 left not committed/,
  );
  // The push checks running on the other wish.
  assert.match(
    plan,
    /class="glance-row glance-merge pushing" title="Push of feat\/lamp">[\s\S]*?Push of feat\/lamp<span class="glance-after">checks test<span class="glance-since">1m\ds<\/span>/,
  );
  // Gold is for what waits on you: never in the glance.
  assert.doesNotMatch(plan, /glance[^"]*waiting/);
  // The legend names them.
  assert.match(
    plan,
    /<p class="glance-legend">.*running.*merging.*waits to merge.*push held.*repair worker<\/span><\/p>/,
  );
  // Narrow: a wrench per repair worker, one mark for the merges, one for the held push.
  assert.match(
    plan,
    /<span class="name">Djinn QoL<\/span><span class="glance-line-marks"><span class="glance-dot run"[^>]*><\/span>(<span class="glance-mark (run|wait)" role="img" aria-label="repair worker" title="[^"]+"><svg[^>]*lucide-wrench[\s\S]*?<\/svg><\/span>){4}<span class="glance-mark run" role="img" aria-label="merging" title="2 merging · 2 to merge · push held">[\s\S]*?<span class="glance-mark held" role="img" aria-label="push held">/,
  );
});

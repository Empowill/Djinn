// The Tasks tab (src/task-tabs.tsx): what moves or waits, azima groups, merges under the task they serve. Run with `go
// tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { screens, wish } from "./screens.mjs";

const { s, h } = await screens();

test("the Tasks tab lists what moves or waits by status", () => {
  const at = (seconds) => ({ seconds: BigInt(seconds), nanos: 0 });
  const tasks = [
    ["W1", s.TaskStatus.PENDING],
    ["W2", s.TaskStatus.DONE, 10],
    ["W3", s.TaskStatus.WAITING],
    ["W4", s.TaskStatus.STOPPED, 30],
    ["W5", s.TaskStatus.FAILED],
    ["W6", s.TaskStatus.PAUSED],
    ["W7", s.TaskStatus.INTERRUPTED],
    ["W8", s.TaskStatus.RUNNING],
    ["W9", s.TaskStatus.DONE, 20],
  ].map(([code, status, end]) => ({
    id: code,
    code,
    title: `Task ${code}`,
    status,
    createTime: at(1),
    endTime: end ? at(end) : undefined,
    closed:
      code === "W9"
        ? { actor: s.Closer.DEVELOPER, createTime: at(20), note: "merged" }
        : undefined,
  }));
  const html = s.renderToStaticMarkup(
    h(s.TaskSections, {
      moving: s.movingTasks(tasks),
      render: (task) =>
        h(s.WishTask, {
          key: task.id,
          task,
          onStop() {},
          async onSend() {},
          async onDone() {},
        }),
    }),
  );
  const order = [...html.matchAll(/<span class="agent-code">(W\d)</g)].map(
    (m) => m[1],
  );
  assert.deepEqual(order, ["W8", "W5", "W3", "W6", "W1"]);
  assert.ok(html.indexOf("Moving or waiting") < html.indexOf("Task W8"));
  assert.match(html, /Moving or waiting<span class="count">5<\/span>/);
  assert.doesNotMatch(html, /tasks-finished/);
  assert.doesNotMatch(html, /Finished/);
});

test("the Tasks tab groups work under its azima, which says what it waits for and its progress, and never waits", () => {
  const at = (seconds) => ({ seconds: BigInt(seconds), nanos: 0 });
  const azima = (code, title, extra) => ({
    id: code,
    code,
    title,
    kind: s.TaskKind.AZIMA,
    status: s.TaskStatus.PENDING,
    dependsOn: [],
    createTime: at(1),
    ...extra,
  });
  const work = (code, status, partOf, end) => ({
    id: code,
    code,
    title: `Task ${code}`,
    status,
    partOf,
    dependsOn: [],
    createTime: at(1),
    endTime: end ? at(end) : undefined,
  });
  const tasks = [
    azima("T1", "Lay the ground", {
      status: s.TaskStatus.DONE,
      azima: { state: s.AzimaState.DONE, ready: true },
    }),
    azima("T2", "The orchestrator", {
      dependsOn: ["T1"],
      azima: {
        state: s.AzimaState.IN_PROGRESS,
        ready: true,
        parts: 3,
        partsDone: 1,
        partsRunning: 1,
      },
    }),
    azima("T10", "Spread the work", {
      dependsOn: ["T1", "T2"],
      azima: { state: s.AzimaState.OPEN, ready: false },
    }),
    work("W1", s.TaskStatus.DONE, "T2", 10),
    work("W2", s.TaskStatus.RUNNING, "T2"),
    work("W3", s.TaskStatus.PENDING, "T2"),
    work("W4", s.TaskStatus.PENDING, ""),
    work("W5", s.TaskStatus.DONE, "", 20),
  ];
  const byId = new Map(tasks.map((task) => [task.id, task]));
  const card = (task) =>
    h(s.WishTask, {
      key: task.id,
      task,
      onStop() {},
      async onSend() {},
      async onDone() {},
    });
  const groups = s.azimaGroups(tasks);
  const sections = (focus = "") =>
    s.renderToStaticMarkup(
      h(s.TaskSections, {
        moving: s.movingTasks(tasks),
        azimas: groups.filter((x) => !s.azimaFinished(x.azima)),
        doneAzimas: groups.filter((x) => s.azimaFinished(x.azima)),
        fold: `test-${focus}`,
        showDone: focus === "T1",
        renderAzima: ({ azima, parts }) =>
          h(s.AzimaCard, {
            key: azima.id,
            azima,
            parts,
            tasks: byId,
            render: card,
            focus,
          }),
        render: card,
      }),
    );
  const codes = (html) =>
    [...html.matchAll(/<span class="agent-code">([TW]\d+)</g)].map((m) => m[1]);
  const html = sections();
  // Work of no azima moves or waits on its own; T2, under way, opened on its parts (running, planned), its finished
  // one folded; T10 waits; T1, done, folded and not rendered.
  assert.deepEqual(codes(html), ["W4", "T2", "W2", "W3", "T10"]);
  const folds = [
    ...html.matchAll(
      /<button type="button" class="fold-line" aria-expanded="false">.*?<\/button>/g,
    ),
  ].map((m) => m[0].replace(/<[^>]+>/g, ""));
  assert.deepEqual(folds, ["Show 1 finished", "Show 1 finished"]);
  // A link to a finished part unfolds its azima's; a link to a done azima, the done ones.
  assert.deepEqual(codes(sections("W1")), [
    "W4",
    "T2",
    "W2",
    "W3",
    "W1",
    "T10",
  ]);
  assert.match(
    sections("W1"),
    /aria-expanded="true">.*?Hide the finished ones</,
  );
  assert.deepEqual(codes(sections("T1")), [
    "W4",
    "T2",
    "W2",
    "W3",
    "T10",
    "T1",
  ]);
  assert.match(html, /Moving or waiting<span class="count">1<\/span>/);
  assert.match(html, /Azimas<span class="count">3<\/span>/);
  assert.doesNotMatch(html, /tasks-finished/);
  assert.doesNotMatch(html, /Finished/);
  assert.match(html, /Waits for T2</);
  // What comes before an azima that waits for nothing is on its title's hover, not in its heading.
  assert.match(html, /<strong title="[^"]*\nafter T1">/);
  assert.doesNotMatch(html, />after T1</);
  assert.match(html, /1\/3/);
  assert.match(html, /In progress/);
  // No azima is ever said to wait for you.
  assert.doesNotMatch(html, /Waits for your answer/);
  assert.doesNotMatch(html, /tone-waiting/);

  // Nor does the flying carpet: an azima neither waits for you nor moves.
  const wish = { id: "w1", title: "Lamp", state: s.WishState.ACTIVE, rank: 1 };
  const plan = s.flightPlan([wish], {
    w1: { tasks, questions: [], blocks: [], loaded: true },
  });
  assert.equal(plan.waiting.length, 0);
  assert.deepEqual(
    plan.moving.map((x) => x.item.code),
    ["W4"],
  );
  assert.deepEqual(
    plan.azimas.map((x) => x.item.azima.code),
    ["T2", "T10", "T1"],
  );
});

test("a merge sits under the original task it serves, folded and marked Merge; the others it serves link to it", () => {
  const at = (seconds) => ({ seconds: BigInt(seconds), nanos: 0 });
  const work = (code, status, extra) => ({
    id: code,
    code,
    title: `Task ${code}`,
    status,
    partOf: "T1",
    dependsOn: [],
    createTime: at(1),
    endTime: status === s.TaskStatus.DONE ? at(10) : undefined,
    ...extra,
  });
  const correction = (code, status, taskIds, extra) =>
    work(code, status, {
      title: `Merge: Task W401 (W401) — conflict in docs/agent-protocol.md`,
      correction: { failure: { taskIds }, attempt: 1 },
      ...extra,
    });
  const tasks = [
    {
      id: "T1",
      code: "T1",
      title: "The plan",
      kind: s.TaskKind.AZIMA,
      status: s.TaskStatus.PENDING,
      dependsOn: [],
      createTime: at(1),
      azima: { state: s.AzimaState.IN_PROGRESS, ready: true },
    },
    work("W401", s.TaskStatus.DONE, {
      integration: { state: s.IntegrationState.CONFLICT },
    }),
    work("W403", s.TaskStatus.DONE),
    work("W405", s.TaskStatus.PENDING),
    // W419 corrected W401; W422 corrects W419 and W403: both serve W401 first.
    correction("W419", s.TaskStatus.DONE, ["W401"]),
    correction("W422", s.TaskStatus.WAITING, ["W419", "W403"]),
    // A push correction serves no task: it stays a peer, marked.
    correction("W430", s.TaskStatus.RUNNING, [], { partOf: "" }),
  ];
  const byId = new Map(tasks.map((task) => [task.id, task]));
  const nest = s.mergeNest(tasks);
  assert.deepEqual(
    [...nest.host],
    [
      ["W419", "W401"],
      ["W422", "W401"],
    ],
  );
  const sections = (focus = "") => {
    const card = (task) => {
      const merges = nest.under.get(task.id);
      return h(s.WishTask, {
        key: task.id,
        task,
        focused: focus === task.id,
        merges,
        nested: merges?.map(card),
        mergeOpen: nest.host.get(focus) === task.id,
        alsoMerged: nest.links.get(task.id),
        onStop() {},
        async onSend() {},
        async onDone() {},
      });
    };
    const groups = s.azimaGroups(tasks);
    return s.renderToStaticMarkup(
      h(s.TaskSections, {
        moving: s.movingTasks(tasks),
        azimas: groups,
        fold: `merge-${focus}`,
        renderAzima: ({ azima, parts }) =>
          h(s.AzimaCard, {
            key: azima.id,
            azima,
            parts,
            tasks: byId,
            render: card,
            focus,
            nest,
          }),
        render: card,
      }),
    );
  };
  const codes = (html) =>
    [...html.matchAll(/<span class="agent-code">([TW]\d+)</g)].map((m) => m[1]);
  const html = sections();
  // The merges are no peers of the azima's work: W401, finished, moves while its merge waits; W403, finished, folds.
  assert.deepEqual(codes(html), ["W430", "T1", "W401", "W405"]);
  assert.match(
    html,
    /<button type="button" class="wish-task-merges-toggle" aria-expanded="false" title="W419 Merge: Task W401[^"]*\nW422 Merge: [^"]*">.*?<span class="merge-label">Merge<\/span><span class="agent-code">W419, W422<\/span>/,
  );
  // The push correction is marked where it stands.
  assert.match(
    html,
    /<span class="merge-label">Merge<\/span><span class="agent-code">W430<\/span>/,
  );
  // Brought into sight, W422 unfolds under W401, marked.
  const shown = sections("W422");
  assert.deepEqual(codes(shown), [
    "W430",
    "T1",
    "W401",
    "W419",
    "W422",
    "W405",
  ]);
  assert.match(
    shown,
    /<span class="merge-label">Merge<\/span><span class="agent-code">W422<\/span><span class="task-title-wrap">/,
  );
  // W403, which it serves too, links to W401.
  assert.match(
    sections("W403"),
    /wish-task-merged-in"><button type="button" class="text-button" title="W422 also brings this work in; it shows under W401"><span class="merge-label">Merge<\/span><span class="agent-code">W422<\/span>/,
  );
  // Counted with their original: four tasks of work, the push correction one of them.
  assert.equal(s.workCount(tasks), 4);
  // The attention bar puts W422's line on W401.
  const wish = { id: "w", code: "D1", title: "Wish" };
  const waiting = s.waitingTasks(wish, { tasks, questions: [] });
  const [line] = s.attentionOf([], waiting, []);
  assert.equal(line.code, "W401");
  assert.match(line.text, /^Merge W422 · /);
  assert.equal(line.target, "waiting-W422");
});

// W507: the lead made a wish with no project and one research task; its worker ended done while its permissions
// refused 6 calls. The window showed no task, read the wish's rank as 3 questions, and proposed the grant.
test("regression W507: a wish without a project shows its task, its rank as a rank, and no grant", async () => {
  const wishId = "01a1317d-4d8c-7de3-b351-e54f2a70840f";
  // The lamp proposes no grant while the lead has not decided about a task's refused calls (internal/plan.Ready).
  const wispr = wish(
    wishId,
    "Install Wispr Flow on Linux",
    s.WishState.ACTIVE,
    3,
  );
  const at = (seconds) => ({ seconds: BigInt(seconds), nanos: 0 });
  const research = {
    id: "01a1317d-4e22-78bc-9bfa-daef6104ed3a",
    wishId,
    code: "W1",
    title: "Find a safe, real way to run Wispr Flow on this Linux machine",
    status: s.TaskStatus.DONE,
    createTime: at(1),
    endTime: at(30),
    refused: { count: 6, names: ["Read", "Glob"] },
  };
  const transport = s.createRouterTransport(({ service }) => {
    service(s.WishService, {
      list: () => ({ wishes: [wispr] }),
      watch: () => new ReadableStream(),
    });
    service(s.ProjectService, { list: () => ({ projects: [] }) });
    service(s.TaskService, {
      list: () => ({ tasks: [research], total: 1 }),
    });
    service(s.QuestionService, {
      list: () => ({ questions: [], total: 0 }),
    });
    service(s.BlockService, { list: () => ({ blocks: [], total: 0 }) });
  });
  const djinn = s.createDjinn(transport, 10);
  const close = djinn.store.open(wishId);
  await djinn.store.changed(wishId, [s.Change.WISH, s.Change.TASK]);
  close();
  const html = s.renderToStaticMarkup(
    h(s.DjinnProvider, { djinn }, h(s.WishView, { wish: wispr, onToast() {} })),
  );
  assert.match(html, /<span class="eyebrow">No project<\/span>/);
  // (b) The rank reads as a rank, and nothing counts questions: there are none.
  assert.match(html, /<span>Active · rank 3<\/span>/);
  assert.doesNotMatch(html, /questions? wait|to decide/);
  // (c) No grant until the lead decides about the refused calls, and nothing waits for you: they are the lead's
  // (Q273), never a warning in the window.
  assert.doesNotMatch(html, /My wish is granted|id="grant-/);
  assert.doesNotMatch(
    html,
    /(?<!othing )waits? for you|with problems|refused|id="waiting-|tone-interrupted/,
  );
  // (a) The Tasks tab counts it.
  assert.match(
    html,
    /id="view-tab-tasks" aria-selected="false" class=""><span class="djinn-eye-perch" data-perch="tab:tasks" aria-hidden="true"><\/span>Tasks<span class="count">1<\/span>/,
  );

  // (a) In the Tasks tab, a task part of no azima shows, folded under Finished once done, a clean Done whether its
  // calls were refused or not.
  const clean = {
    ...research,
    id: "t2",
    code: "W2",
    title: "Read the vendor's notes",
    refused: undefined,
  };
  const tasks = [research, clean];
  const card = (task) =>
    h(s.WishTask, {
      key: task.id,
      task,
      onStop() {},
      async onSend() {},
      async onDone() {},
    });
  const tab = s.renderToStaticMarkup(
    h(s.TaskSections, {
      moving: s.movingTasks(tasks),
      finished: s.finishedTasks(tasks),
      render: card,
    }),
  );
  assert.match(tab, /Moving or waiting<span class="count">0<\/span>/);
  assert.match(
    tab,
    /<section class="wish-section tasks-finished" aria-label="Finished"><div class="section-title"><h2>Finished<span class="count">2<\/span><\/h2><\/div><button type="button" class="fold-line" aria-expanded="false">.*Show the 2 finished<\/button><\/section>/,
  );
  // A link brings them into sight: unfolded, both a clean Done, neither to mark done.
  const shown = s.renderToStaticMarkup(
    h(s.TaskSections, {
      moving: s.movingTasks(tasks),
      finished: s.finishedTasks(tasks),
      showFinished: true,
      render: card,
    }),
  );
  assert.match(
    shown,
    /tasks-finished.*class="wish-task tone-done[^"]*".*<span>Done<\/span>.*<span class="agent-code">W1<.*class="wish-task tone-done[^"]*".*<span>Done<\/span>.*<span class="agent-code">W2</,
  );
  assert.doesNotMatch(shown, /Done with problems|aria-label="Mark done"/);

  // (b) The side panel shows the rank as #3, beside no pill.
  const side = s.renderToStaticMarkup(
    h(s.WishSidebar, {
      wishes: [wispr],
      projects: [],
      counts: { [wishId]: { questions: 0, running: 0, watching: 0 } },
      selectedWishId: wishId,
      selectedProjectId: "",
      collapsed: false,
      onSelectWish() {},
      onSelectProject() {},
      onMove() {},
      onNewProject() {},
    }),
  );
  assert.match(side, /<small class="wish-rank">#3<\/small>/);
  assert.doesNotMatch(side, /count-pill/);
});

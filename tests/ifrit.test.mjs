// The ifrits (src/ifrit.tsx): their bands, their cards, their marks on a wish, the gates they hold. Run with `go tool
// task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { screens, wish } from "./screens.mjs";

const { s, h } = await screens();

// An ifrit (T51) lit on a wish, as tilasm L06 (section 6) draws it: a lit ifrit fixture, its questions, those of the
// lead about it, and the other questions of the wish.
const ifritAt = (seconds) => ({
  seconds: BigInt(Math.floor(seconds)),
  nanos: 0,
});

function litIfrit(id, code, activity, extra = {}) {
  return {
    id,
    wishId: "01a14438-8011-7520-a2c2-96da9e2791e6",
    code,
    title: `Front of the lamp ${code}`,
    kind: s.TaskKind.IFRIT,
    status: s.TaskStatus.RUNNING,
    dependsOn: [],
    createTime: ifritAt(1),
    startTime: ifritAt(2),
    lastActivityTime: ifritAt(activity),
    ifrit: {
      state: s.IfritState.LISTENING,
      serves: [],
      reply: "",
      ...extra,
    },
  };
}

test("each lit ifrit has a band after the lead's choice, the latest active first, holding its questions and the lead's about it; without one, today's order", () => {
  const w = wish("w1", "Ship the lamp", s.WishState.ACTIVE, 1);
  const front = litIfrit("i1", "W12", 10, {
    reply: "Button made primary, 8 px margin.\nReload /reviews.",
    serves: [
      {
        name: "front",
        state: s.ServeState.RUNNING,
        gate: "",
        ports: [{ number: 5173, label: "", open: true }],
      },
      {
        name: "back",
        state: s.ServeState.RUNNING,
        gate: "stack",
        ports: [{ number: 8080, label: "api", open: false }],
      },
    ],
  });
  const back = litIfrit("i2", "W13", 20);
  const q = (id, extra = {}) => ({
    id,
    code: id.toUpperCase(),
    text: `text of ${id}`,
    before: "",
    move: false,
    taskId: "",
    aboutTaskId: "",
    createTime: ifritAt(Number(id.slice(1))),
    ...extra,
  });
  const questions = [
    { wish: w, item: q("q1"), blocking: ["W1"] },
    {
      wish: w,
      item: q("q2", { aboutTaskId: "i1", before: "before the demo" }),
      blocking: [],
    },
    { wish: w, item: q("q3", { taskId: "i1" }), blocking: ["W14"] },
    { wish: w, item: q("q4", { taskId: "i1" }), blocking: [] },
    { wish: w, item: q("q5"), blocking: [] },
    {
      wish: w,
      item: q("q6", { aboutTaskId: "i2", before: "before the merge" }),
      blocking: [],
    },
  ];
  const prompt = {
    title: "Would you like to run the following command?",
    lines: [],
    options: ["Yes", "No"],
  };
  const today = s.attentionOf(questions, [], [w], undefined, prompt);
  assert.deepEqual(
    today.map((i) => i.key),
    ["lead-prompt", "q1", "q3", "q2", "q6", "ready-w1"],
  );
  // Without a lit ifrit, the order is exactly today's.
  assert.deepEqual(
    s.attentionOf(questions, [], [w], undefined, prompt, []),
    today,
  );
  // With two lit ifrits: the lead's choice, then a band each, the latest activity first, then the rest as today.
  const ifrits = [
    { wish: w, item: back },
    { wish: w, item: front },
  ];
  const lit = s.attentionOf(questions, [], [w], undefined, prompt, ifrits);
  assert.deepEqual(
    lit.map((i) => [i.key, i.level, i.target]),
    [
      ["lead-prompt", "blocking", "lead-prompt"],
      ["ifrit-i2", "ifrit", "task-i2"],
      ["ifrit-i1", "ifrit", "task-i1"],
      ["q1", "blocking", "question-q1"],
      ["ready-w1", "ready", "grant-w1"],
    ],
  );
  // A band holds the ifrit's own questions, then the lead's about it, each the most urgent first; one that can wait
  // (q4) is in the questions column alone, as below (q5).
  assert.deepEqual(
    lit[2].band.items.map((i) => [i.key, i.level, i.asker]),
    [
      ["q3", "blocking", "asked by W12"],
      ["q2", "question", "the Djinn, about W12"],
    ],
  );
  assert.deepEqual(
    lit[1].band.items.map((i) => [i.key, i.asker]),
    [["q6", "the Djinn, about W13"]],
  );
  // Never shown twice: each question that holds up its wish once, in a band or below.
  const shown = lit.flatMap((i) => [
    i.key,
    ...(i.band?.items.map((x) => x.key) ?? []),
  ]);
  assert.equal(new Set(shown).size, shown.length);
  for (const id of ["q1", "q2", "q3", "q6"]) assert.ok(shown.includes(id), id);
  for (const id of ["q4", "q5"]) assert.ok(!shown.includes(id), id);
  // No lead's choice: the bands come first.
  assert.deepEqual(
    s
      .attentionOf(questions, [], [], undefined, undefined, ifrits)
      .slice(0, 2)
      .map((i) => i.key),
    ["ifrit-i2", "ifrit-i1"],
  );

  // The band: its flame, code, title and word, a button to its card; its links; the first line of its last reply;
  // then its lines with who asked them. The summary counts every line, a band's included.
  const djinn = s.createDjinn(
    s.createRouterTransport(() => {}),
    10,
  );
  const render = (items) =>
    s.renderToStaticMarkup(
      h(s.DjinnProvider, { djinn }, h(s.AttentionBar, { items })),
    );
  const markup = render(lit);
  assert.match(markup, /<nav class="attention-bar with-ifrit"/);
  assert.match(markup, /8 things wait for you/);
  assert.match(
    markup,
    /<section class="attention-ifrit tone-ifrit" aria-label="Ifrit W12: Front of the lamp W12"><div class="attention-ifrit-head"><button class="attention-ifrit-title"><span class="attention-level"><svg[^>]*lucide-flame[^>]*>.*?<\/svg>Ifrit<\/span><b class="attention-code">W12<\/b><span class="attention-text">Front of the lamp W12<\/span><span class="attention-ifrit-state">Lit<\/span>/,
  );
  assert.match(
    markup,
    /<span class="ifrit-link " title="http:\/\/localhost:5173\/"><a href="http:\/\/localhost:5173\/"[^>]*>localhost:5173<\/a><span class="ifrit-link-label">front<\/span><\/span>/,
  );
  assert.match(
    markup,
    /<span class="ifrit-link closed" title="localhost:8080 no longer answers">/,
  );
  assert.match(
    markup,
    /<p class="attention-ifrit-reply" title="The first line of its last reply"><q>Button made primary, 8 px margin.<\/q><\/p>/,
  );
  assert.match(
    markup,
    /<button class="attention-item level-blocking"><span class="attention-level">Blocking<\/span><b class="attention-code">Q3<\/b><span class="attention-asker">asked by W12<\/span>/,
  );
  // Not warned yet: no countdown, no Keep lit.
  assert.doesNotMatch(markup, /ifrit-countdown|Keep lit/);
  // Warned: the countdown, gold and dotted, and Keep lit.
  const now = Date.now() / 1000;
  const warned = {
    ...front,
    ifrit: {
      ...front.ifrit,
      warnTime: ifritAt(now - 60),
      outTime: ifritAt(now + 210),
    },
  };
  const hot = render(
    s.attentionOf([], [], [], undefined, undefined, [
      { wish: w, item: warned },
    ]),
  );
  assert.match(
    hot,
    /<span class="attention-ifrit-state">Goes out in 4 min<\/span>/,
  );
  assert.match(
    hot,
    /<span class="ifrit-countdown" role="timer"><svg[^>]*>.*?<\/svg>Goes out in 4 min<\/span><button type="button" class="button secondary small ifrit-keep" title="You are here: Djinn keeps its ifrits lit">Keep lit<\/button>/,
  );
});

test("a lit ifrit shows plainly on its wish: a flame and its count on the bar's second line, the wish's tone, its badge in the wish's head, its band first", async () => {
  const wishId = "01a14438-8011-7520-a2c2-96da9e2791e6";
  const lamp = wish(wishId, "Ship the lamp", s.WishState.ACTIVE, 1);
  const front = litIfrit("i1", "W12", 10);
  const worker = {
    id: "w1",
    wishId,
    code: "W1",
    title: "Pour",
    status: s.TaskStatus.RUNNING,
    dependsOn: [],
    createTime: ifritAt(1),
    startTime: ifritAt(2),
  };
  const out = {
    ...front,
    status: s.TaskStatus.STOPPED,
    ifrit: { ...front.ifrit, state: s.IfritState.OUT },
  };
  const sidebar = (list, questions = 0) =>
    s.renderToStaticMarkup(
      h(s.WishSidebar, {
        wishes: [lamp],
        projects: [],
        counts: { [wishId]: { questions, ...s.runningOf(list) } },
        selectedWishId: "",
        selectedProjectId: "",
        collapsed: false,
        onSelectWish() {},
        onSelectProject() {},
        onMove() {},
        onNewProject() {},
      }),
    );
  // The logo's flame comes first on the second line, without a disc, and with no number for one ifrit alone; the
  // worker is counted alone, then the questions' hand.
  const side = sidebar([front, worker], 1);
  assert.match(
    side,
    /<span class="wish-counts"><span class="wish-sign tone-ifrit" title="1 ifrit lit" aria-label="1 ifrit lit" role="img"><svg class="lucide ifrit-flame"[^>]*>(?:(?!<\/svg>).)*<\/svg><\/span>/,
  );
  assert.match(side, /wish-sign tone-running" title="1 running"/);
  assert.ok(
    side.indexOf("wish-sign tone-ifrit") <
      side.indexOf("wish-sign tone-waiting"),
  );
  // Where the bar folds to its icons, the logo's flame on the icon's corner, without a disc.
  assert.match(
    side,
    /<span class="wish-flame-corner" title="1 ifrit lit" aria-label="1 ifrit lit" role="img"><svg class="lucide ifrit-flame"/,
  );
  // Put out: no flame on the bar, the wish's tone is today's.
  const outSide = sidebar([out]);
  assert.doesNotMatch(outSide, /tone-ifrit|lucide-flame/);

  const page = async (list) => {
    const transport = s.createRouterTransport(({ service }) => {
      service(s.WishService, { list: () => ({ wishes: [lamp] }) });
      service(s.TaskService, { list: () => ({ tasks: list }) });
      service(s.QuestionService, { list: () => ({ questions: [] }) });
      service(s.BlockService, { list: () => ({ blocks: [] }) });
    });
    const djinn = s.createDjinn(transport, 10);
    const close = djinn.store.open(wishId);
    await djinn.store.changed(wishId, [s.Change.WISH]);
    close();
    const view = s.renderToStaticMarkup(
      h(
        s.DjinnProvider,
        { djinn },
        h(s.WishView, { wish: lamp, onToast() {} }),
      ),
    );
    const plan = s.renderToStaticMarkup(
      h(
        s.DjinnProvider,
        { djinn },
        h(s.FlightPlan, { wishes: [lamp], onOpen() {}, onToast() {} }),
      ),
    );
    return { view, plan };
  };
  // The wish's head: its state in the ifrit's tone, with the flame, and the ifrit's badge and word beside it.
  const now = await page([front, worker]);
  assert.match(
    now.view,
    /<div class="review-pills"><span class="status-badge tone-ifrit"><svg[^>]*lucide-flame[^>]*>.*?<\/svg><span>Active · rank 1<\/span><\/span><span class="status-badge tone-ifrit" title="Front of the lamp W12"><svg[^>]*lucide-flame[^>]*>.*?<\/svg><span>Ifrit W12 · Lit<\/span><\/span>/,
  );
  // Its band heads what waits for you, in the wish and on the flying carpet.
  for (const html of [now.view, now.plan])
    assert.match(
      html,
      /<nav class="attention-bar with-ifrit" aria-label="What waits for you">.*?<ul><li><section class="attention-ifrit tone-ifrit" aria-label="Ifrit W12: Front of the lamp W12">/,
    );
  // The glance of what runs: its flame and word in place of the blue dot, counted apart from the worker.
  assert.match(now.plan, /glance-count">1 ifrit lit · 1 running</);
  assert.match(
    now.plan,
    /<span class="glance-flame tone-ifrit" role="img" aria-label="Lit"><svg[^>]*lucide-flame/,
  );
  // Put out: grey, its flame outlined, and no band.
  const gone = await page([out]);
  assert.match(
    gone.view,
    /<span class="status-badge tone-out" title="Front of the lamp W12"><svg[^>]*lucide-flame[^>]*>.*?<\/svg><span>Ifrit W12 · Out<\/span><\/span>/,
  );
  assert.doesNotMatch(
    gone.view,
    /fill="currentColor"[^>]*lucide-flame|attention-ifrit/,
  );
});

test("an ifrit's card: its word, its links and gates, Finish; out, Relight; warned, the countdown and Keep lit; a task waiting for its gate says when it comes back", async () => {
  const front = litIfrit("i1", "W12", 10, {
    reply: "Button made primary.\nReload /reviews.",
    serves: [
      {
        name: "back",
        state: s.ServeState.RUNNING,
        gate: "stack",
        ports: [{ number: 8080, label: "api", open: true }],
      },
    ],
  });
  const waiter = {
    id: "t15",
    wishId: front.wishId,
    code: "W15",
    title: "Tests",
    status: s.TaskStatus.RUNNING,
    dependsOn: [],
    createTime: ifritAt(1),
    startTime: ifritAt(2),
  };
  const release = Date.UTC(2026, 9, 16, 15, 42);
  const transport = s.createRouterTransport(({ service }) => {
    service(s.MachineService, { show: () => ({ machine: { workers: 4 } }) });
    service(s.GateService, {
      list: () => ({
        gates: [
          {
            name: "stack",
            holder: "W12 (Front of the lamp W12): task run-back",
            holderTaskId: "i1",
            ifrit: true,
            waiting: ["W15 (Tests): go tool task test"],
            expireTime: ifritAt(release / 1000),
          },
          { name: "lint", holder: "W3 (Lint)", holderTaskId: "t3" },
        ],
      }),
    });
  });
  const djinn = s.createDjinn(transport, 10);
  await djinn.store.readMachine();
  const card = (task, extra = {}) =>
    s.renderToStaticMarkup(
      h(
        s.DjinnProvider,
        { djinn },
        h(s.WishTask, {
          task,
          codes: new Map(),
          onStop() {},
          onSend: async () => {},
          onDone: async () => {},
          onRelight: async () => {},
          ...extra,
        }),
      ),
    );
  const lit = card(front);
  assert.match(lit, /<article class="wish-task tone-ifrit /);
  assert.match(
    lit,
    /<span class="status-badge tone-ifrit" title="active [^"]* ago"><svg[^>]*fill="currentColor"[^>]*lucide-flame[^>]*>.*?<\/svg><span>Lit<\/span>/,
  );
  assert.match(
    lit,
    /<div class="wish-task-note ifrit-line"><span class="ifrit-links"><span class="ifrit-link " title="http:\/\/localhost:8080\/"><a href="http:\/\/localhost:8080\/"[^>]*>localhost:8080<\/a><span class="ifrit-link-label">api<\/span><\/span><\/span><span class="ifrit-gates" title="Held while it is lit: others wait until it goes out or ends"><span class="ifrit-gate"><svg[^>]*>.*?<\/svg>gate stack<\/span><\/span><span class="ifrit-actions"><button type="button" class="button secondary small" title="Finish the ifrit: its input closes, its serves stop, and its work integrates like any task&#x27;s"><svg[^>]*>.*?<\/svg><span>Finish<\/span><\/button><\/span><\/div>/,
  );
  assert.match(
    lit,
    /<p class="wish-task-note ifrit-reply" title="The first line of its last reply"><q>Button made primary.<\/q><\/p>/,
  );
  // Finish takes the place of Mark done; no Relight while lit.
  assert.doesNotMatch(lit, /aria-label="Mark done"|Relight/);
  // Put out: grey, Relight beside Finish, and no gate held.
  const out = card({
    ...front,
    status: s.TaskStatus.STOPPED,
    ifrit: {
      ...front.ifrit,
      state: s.IfritState.OUT,
      serves: front.ifrit.serves.map((x) => ({
        ...x,
        state: s.ServeState.STOPPED,
      })),
    },
  });
  assert.match(out, /<article class="wish-task tone-out /);
  assert.match(out, /<span>Out<\/span>/);
  assert.match(
    out,
    /<button type="button" class="button secondary small" title="A message lights it again, in its own session and worktree" aria-expanded="false"><svg[^>]*lucide-flame[^>]*>.*?<\/svg><span>Relight<\/span><\/button>/,
  );
  assert.doesNotMatch(
    out,
    /ifrit-gate"|localhost:8080<\/a>|aria-label="Mark done"/,
  );
  // Warned: the countdown and Keep lit.
  const now = Date.now() / 1000;
  const warned = card({
    ...front,
    ifrit: {
      ...front.ifrit,
      warnTime: ifritAt(now - 60),
      outTime: ifritAt(now + 210),
    },
  });
  assert.match(warned, /<span>Goes out in 4 min<\/span>/);
  assert.match(warned, /class="ifrit-countdown" role="timer"/);
  assert.match(warned, />Keep lit<\/button>/);
  // A task waiting for the gate the ifrit holds: the flame, the holder, and the hour it comes back at the latest.
  const time = new Date(release).toLocaleTimeString("en", {
    hour: "2-digit",
    minute: "2-digit",
  });
  assert.match(
    card(waiter),
    new RegExp(
      `<p class="wish-task-note ifrit-wait tone-ifrit"><svg[^>]*lucide-flame[^>]*>.*?</svg><span>Waits for the gate stack, held by the ifrit W12 \\(Front of the lamp W12\\): task run-back; given back when you leave, at ${time} at the latest</span></p>`,
    ),
  );
  // A task no ifrit holds up: no such line.
  assert.doesNotMatch(card({ ...waiter, code: "W16" }), /ifrit-wait/);
});

test("the status bar says how many gates an ifrit holds, apart, with a vermilion dot", () => {
  const gates = [
    { name: "stack", holder: "W12 (Front)", ifrit: true, waiting: [] },
    { name: "lint", holder: "W3 (Lint)", ifrit: false, waiting: [] },
    { name: "test", holder: "", ifrit: false, waiting: ["W4 (Test)"] },
  ];
  assert.equal(
    s.renderToStaticMarkup(h(s.HeldGates, { gates })),
    '2 gates held<span class="status-ifrit"><span class="status-dot ifrit"></span>1 gate held by an ifrit</span>',
  );
  assert.equal(
    s.renderToStaticMarkup(h(s.HeldGates, { gates: gates.slice(1) })),
    "1 gate held",
  );
});

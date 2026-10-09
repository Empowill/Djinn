// The screens on the services, rendered to text from a store filled by in-memory services. Run with
// `go tool task test-ui`. Node reports English: the texts asserted are the English ones.
import test from "node:test";
import assert from "node:assert/strict";

import { bundle } from "./bundle.mjs";

const s = await bundle(
  "screens",
  `export { createElement } from "react";
export { renderToStaticMarkup } from "react-dom/server";
export { createRouterTransport } from "@connectrpc/connect";
export { createDjinn, DjinnProvider } from "@/src/data/djinn.tsx";
export { WishSidebar } from "@/src/wish-sidebar.tsx";
export { WishQuestion } from "@/src/wish-question.tsx";
export { WishView } from "@/src/wish-view.tsx";
export { WishTask } from "@/src/wish-task.tsx";
export { FlightPlan } from "@/src/flight-plan.tsx";
export { TaskSections } from "@/src/task-tabs.tsx";
export {
  azimaGroups,
  finishedTasks,
  flightPlan,
  movingTasks,
} from "@/src/data/flight.ts";
export { AzimaCard } from "@/src/azima.tsx";
export { FolderField, ShortcutField } from "@/src/wish-dialogs.tsx";
export {
  LeadButton,
  LeadMenu,
  WishDescription,
  recordedAgent,
} from "@/src/wish-head.tsx";
export { UpdateBannerView } from "@/src/update-banner.tsx";
export { memory, resourcesDetail } from "@/src/usage.tsx";
export * from "@/gen/ts/plan/v1/plan_pb.ts";`,
);
const h = s.createElement;

const wish = (id, title, state, rank, extra = {}) => ({
  id,
  title,
  state,
  rank,
  projectIds: [],
  allowances: [],
  ...extra,
});

test("the side panel ranks the active wishes, counts the three places, and folds the granted ones", () => {
  const html = s.renderToStaticMarkup(
    h(s.WishSidebar, {
      wishes: [
        wish("w1", "Ship the lamp", s.WishState.ACTIVE, 1),
        wish("w2", "Polish the brass", s.WishState.ACTIVE, 2, { ready: true }),
        wish("w3", "Trim the wick", s.WishState.PAUSED, 0),
        wish("w4", "Light it", s.WishState.GRANTED, 0),
      ],
      projects: [{ id: "p1", name: "lamp", directory: "/tmp/lamp" }],
      selectedWishId: "w2",
      selectedProjectId: "",
      collapsed: false,
      onSelectWish() {},
      onSelectProject() {},
      onMove() {},
      onNewProject() {},
    }),
  );
  assert.match(html, />2\/3</);
  assert.ok(html.indexOf("Ship the lamp") < html.indexOf("Polish the brass"));
  assert.match(html, /Paused/);
  assert.match(html, /1 granted/);
  // Granted wishes stay folded until asked.
  assert.doesNotMatch(html, /Light it/);
  // The active ones are dragged to a new rank, and a paused one among them; a granted one is not.
  assert.equal(html.match(/draggable="true"/g).length, 3);
  // A paused wish goes first with its button.
  assert.equal(html.match(/Make it the first active wish/g).length / 2, 1);
  assert.match(html, /mission-nav wish-nav selected/);
  // Each wish says at a glance what waits and what runs: an icon and a count, with their words.
  const counted = s.renderToStaticMarkup(
    h(s.WishSidebar, {
      wishes: [wish("w1", "Ship the lamp", s.WishState.ACTIVE, 1)],
      projects: [],
      counts: { w1: { questions: 2, running: 1 } },
      onSelectPlan() {},
      selectedWishId: "",
      selectedProjectId: "",
      collapsed: false,
      onSelectWish() {},
      onSelectProject() {},
      onMove() {},
      onNewProject() {},
    }),
  );
  assert.match(counted, /wish-tone tone-waiting/);
  assert.match(counted, /aria-label="2 questions wait for your answer."/);
  assert.match(counted, /aria-label="1 running"[^>]*><span class="live-dot"/);
  assert.match(html, />lamp</);
});

test("the side panel counts the new inbox items on the flight plan's entry, even with no active wish", () => {
  const sidebar = (wishes, inbox) =>
    s.renderToStaticMarkup(
      h(s.WishSidebar, {
        wishes,
        projects: [],
        inbox,
        onSelectPlan() {},
        selectedWishId: "",
        selectedProjectId: "",
        collapsed: false,
        onSelectWish() {},
        onSelectProject() {},
        onMove() {},
        onNewProject() {},
      }),
    );
  const paused = wish("w1", "Trim the wick", s.WishState.PAUSED, 0);
  // Nothing active, nothing new: no flight plan.
  assert.doesNotMatch(sidebar([paused], 0), /plan-nav/);
  // Two new items: the flight plan shows them, its entry counts them.
  const html = sidebar([paused], 2);
  assert.match(html, /plan-nav/);
  assert.match(
    html,
    /aria-label="2 new items in the inbox"[^>]*><svg[^>]*lucide-inbox[^>]*>.*?<b>2<\/b>/,
  );
  assert.match(
    sidebar([wish("w2", "Ship the lamp", s.WishState.ACTIVE, 1)], 1),
    /aria-label="1 new item in the inbox"/,
  );
});

test("a question shows its options by letter and its recommendation; answered, what was chosen", () => {
  const open = s.renderToStaticMarkup(
    h(s.WishQuestion, {
      question: {
        id: "q1",
        code: "Q01",
        text: "Which oil?",
        options: ["Olive", "Paraffin"],
        recommendation: "**A**, for the smell.",
        context: "",
      },
      expanded: true,
      onAnswer: async () => {},
    }),
  );
  assert.match(open, /Which oil\?/);
  assert.match(open, /<strong class="option-letter">A<\/strong><p>Olive<\/p>/);
  assert.match(
    open,
    /<strong class="option-letter">B<\/strong><p>Paraffin<\/p>/,
  );
  assert.match(open, /<strong>A<\/strong>, for the smell\./);
  assert.match(open, /Confirm this choice/);
  // The recommendation is boxed first, its option marked; nothing to rub without onMark.
  assert.ok(open.indexOf("Recommendation · A") < open.indexOf("Olive"));
  assert.match(open, /Olive<\/p><span class="option-recommended">Recommended/);
  assert.match(open, /Waiting for you/);
  assert.doesNotMatch(open, /Rub the lamp/);

  // With the lamp's writes: rub, enlighten, and a read mark already put.
  const lamp = s.renderToStaticMarkup(
    h(s.WishQuestion, {
      question: {
        id: "q1",
        code: "Q01",
        text: "Which oil?",
        options: ["Olive", "Paraffin"],
        recommendation: "B: brighter.",
        context: "## Cost\n\nOlive is dearer.",
        marks: [{ kind: s.MarkKind.READ }],
        rounds: [],
        revision: 0,
      },
      blocking: ["W2"],
      onAnswer: async () => {},
      onMark: async () => {},
      onEnlighten: async () => {},
    }),
  );
  assert.match(lamp, /title="Apply the recommendation"[^>]*>.*Rub the lamp/);
  assert.match(lamp, /Enlighten me/);
  assert.match(lamp, /aria-pressed="true"[^>]*>.*Read<\/button>/);
  assert.match(lamp, /Blocks W2/);
  assert.match(lamp, /What is at stake<\/h4>.*<h2>Cost<\/h2>/);
  assert.match(lamp, /question-card open is-blocking/);
  // No option named: no rub, the choice is yours.
  const vague = s.renderToStaticMarkup(
    h(s.WishQuestion, {
      question: {
        id: "q1",
        code: "Q01",
        text: "Which oil?",
        options: ["Olive", "Paraffin"],
        recommendation: "Both burn.",
        rounds: [],
      },
      onAnswer: async () => {},
      onMark: async () => {},
    }),
  );
  assert.doesNotMatch(vague, /Rub the lamp/);

  // Being investigated, then revised: its own state, the note, the badge, the history folded.
  const digging = s.renderToStaticMarkup(
    h(s.WishQuestion, {
      question: {
        id: "q1",
        code: "Q01",
        text: "Which oil?",
        options: ["Olive", "Paraffin"],
        recommendation: "A: it smells good.",
        revision: 1,
        rounds: [
          { kind: s.RoundKind.ENLIGHTEN, note: "Burn time?" },
          { kind: s.RoundKind.REVISE, recommendation: "B: brighter." },
          { kind: s.RoundKind.ENLIGHTEN, note: "And the price?" },
        ],
      },
      onAnswer: async () => {},
      onEnlighten: async () => {},
    }),
  );
  assert.match(digging, /question-card investigating/);
  assert.match(digging, /Being investigated/);
  assert.match(digging, /You asked to find out more: And the price\?/);
  assert.match(digging, /Revised/);
  assert.match(
    digging,
    /<details class="question-rounds"><summary>.*History: 3 rounds/,
  );
  assert.match(digging, /Recommended before: B: brighter\./);
  // Already asked: no second request.
  assert.doesNotMatch(digging, /Enlighten me/);

  const answered = s.renderToStaticMarkup(
    h(s.WishQuestion, {
      question: {
        id: "q1",
        code: "Q01",
        text: "Which oil?",
        options: ["Olive", "Paraffin"],
        answer: { choice: s.Choice.B, note: "" },
      },
      onAnswer: async () => {},
    }),
  );
  assert.match(answered, /B · Paraffin/);
  assert.match(answered, /Decision recorded/);
});

test("a wish's screen puts its questions first, proposes to grant it when ready, and shows tasks and blocks", async () => {
  const wishId = "01a11833-a440-7479-a067-52615c91da71";
  const ready = wish(wishId, "Ship the lamp", s.WishState.ACTIVE, 1, {
    ready: true,
  });
  const transport = s.createRouterTransport(({ service }) => {
    service(s.WishService, { list: () => ({ wishes: [ready] }) });
    service(s.TaskService, {
      list: () => ({
        tasks: [
          {
            id: "t1",
            wishId,
            code: "W1",
            title: "Trim the wick",
            status: s.TaskStatus.DONE,
          },
          {
            id: "t2",
            wishId,
            code: "W2",
            title: "Light the wick",
            status: s.TaskStatus.FAILED,
            error: "exit code 1",
          },
        ],
      }),
    });
    service(s.QuestionService, {
      list: () => ({
        questions: [
          {
            id: "q1",
            wishId,
            code: "Q01",
            text: "Light it tonight?",
            options: [],
            answer: { choice: s.Choice.YES, note: "" },
          },
          {
            id: "q2",
            wishId,
            code: "Q02",
            text: "Which oil?",
            options: ["Olive", "Paraffin"],
            rounds: [{ kind: s.RoundKind.ENLIGHTEN, note: "Burn time?" }],
          },
        ],
      }),
    });
    service(s.BlockService, {
      list: () => ({
        blocks: [
          {
            id: "b1",
            wishId,
            kind: "section",
            title: "Lexicon",
            content: "A **wick** carries the oil.",
          },
        ],
      }),
    });
  });
  const djinn = s.createDjinn(transport, 10);
  const close = djinn.store.open(wishId);
  await djinn.store.changed(wishId, [s.Change.WISH]);
  close();
  const html = s.renderToStaticMarkup(
    h(s.DjinnProvider, { djinn }, h(s.WishView, { wish: ready, onToast() {} })),
  );
  assert.match(html, /My wish is granted/);
  // The tasks have a tab of their own, with their count: the wish's tab does not show them.
  assert.match(
    html,
    /role="tab" id="view-tab-main" aria-selected="true" class="active">Wish<\/button>/,
  );
  assert.match(
    html,
    /role="tab" id="view-tab-tasks" aria-selected="false" class="">Tasks<span class="count">2<\/span>/,
  );
  assert.doesNotMatch(html, /Trim the wick|Light the wick/);
  assert.match(html, /<strong>wick<\/strong> carries the oil\./);
  // The answered question is in the Decisions tab, apart, with no mark.
  assert.match(
    html,
    /role="tab" id="view-tab-decisions" aria-selected="false" class="">Decisions<span class="count">1<\/span>/,
  );
  assert.doesNotMatch(html, /Light it tonight\?/);
  // Nothing waits for an answer: no yes/no card waiting.
  assert.doesNotMatch(html, /Confirm the answer/);
  // The bar at the top lists what waits for you: here, the grant, which it links to.
  assert.match(
    html,
    /<nav class="attention-bar" aria-label="What waits for you">/,
  );
  assert.match(html, /1 thing waits for you/);
  assert.match(html, /id="grant-01a11833/);
  // The status language, in words: the wish waits, one task of two is done, a question is being investigated, apart.
  assert.match(html, /count-pill tone-done" title="1 of 2 tasks done"/);
  assert.match(
    html,
    /<h2>Being investigated<span class="count">1<\/span><\/h2>/,
  );
  assert.match(html, /1 question being investigated by the lead/);
  assert.match(html, /You asked to find out more: Burn time\?/);
  // A block has its read and approve marks; the journal is folded.
  assert.match(html, /id="block-b1".*Mark read.*Approve as it is/);
  assert.match(html, /class="fold-heading" aria-expanded="false"/);
});

const usage = (input, output, read, write, costUsd) => ({
  inputTokens: BigInt(input),
  outputTokens: BigInt(output),
  cacheReadTokens: BigInt(read),
  cacheWriteTokens: BigInt(write),
  costUsd,
});

test("a task shows its tokens and its cost; a Codex task, its tokens only", () => {
  const claude = s.renderToStaticMarkup(
    h(s.WishTask, {
      task: {
        id: "t1",
        code: "W1",
        title: "Trim the wick",
        status: s.TaskStatus.DONE,
        usage: usage(4, 409, 38153, 12845, 0.0032),
      },
      onStop() {},
    }),
  );
  assert.match(claude, /51\.4K tokens · \$0\.003/);
  assert.match(
    claude,
    /input 4 · output 409 · cache read 38\.2K · cache written 12\.8K · \$0\.003/,
  );
  const codex = s.renderToStaticMarkup(
    h(s.WishTask, {
      task: {
        id: "t2",
        code: "W2",
        title: "Read the map",
        status: s.TaskStatus.DONE,
        provider: s.Provider.CODEX,
        usage: usage(1200, 300, 5000, 0, 0),
      },
      onStop() {},
    }),
  );
  assert.match(codex, /6\.5K tokens</);
  assert.doesNotMatch(codex, /\$/);
});

test("a running task shows what its worker uses now; its facts, the peaks too", () => {
  const resources = {
    cpuPercent: 34.4,
    memoryBytes: 512n << 20n,
    processes: 3,
    peakCpuPercent: 180,
    peakMemoryBytes: 1288490189n,
    readTime: { seconds: 1760000000n, nanos: 0 },
  };
  const card = (status) =>
    s.renderToStaticMarkup(
      h(s.WishTask, {
        task: {
          id: "t1",
          code: "W1",
          title: "Trim the wick",
          status,
          resources,
        },
        onStop() {},
      }),
    );
  assert.match(
    card(s.TaskStatus.RUNNING),
    /title="now CPU 34% · 512 MB · 3 processes · peak CPU 180% · 1\.2 GB">CPU 34% · 512 MB</,
  );
  assert.doesNotMatch(card(s.TaskStatus.DONE), /CPU/);
  assert.equal(s.resourcesDetail(resources, false), "peak CPU 180% · 1.2 GB");
  assert.equal(s.memory(1536n), "1.5 kB");
});

test("a task no worker runs can be marked done; a task closed by hand says who closed it, and why", () => {
  const card = (status, extra = {}) =>
    s.renderToStaticMarkup(
      h(s.WishTask, {
        task: {
          id: "t1",
          code: "W1",
          title: "Trim the wick",
          status,
          ...extra,
        },
        onStop() {},
        async onSend() {},
        async onDone() {},
      }),
    );
  for (const status of [
    s.TaskStatus.PENDING,
    s.TaskStatus.WAITING,
    s.TaskStatus.INTERRUPTED,
    s.TaskStatus.FAILED,
    s.TaskStatus.STOPPED,
  ])
    assert.match(card(status), /aria-label="Mark done"/, s.TaskStatus[status]);
  for (const status of [
    s.TaskStatus.RUNNING,
    s.TaskStatus.PAUSED,
    s.TaskStatus.DONE,
  ])
    assert.doesNotMatch(card(status), /Mark done/, s.TaskStatus[status]);
  const closed = card(s.TaskStatus.DONE, {
    endTime: { seconds: 1760000000n, nanos: 0 },
    closed: {
      actor: s.Closer.DEVELOPER,
      createTime: { seconds: 1760000000n, nanos: 0 },
      note: "merged in Git",
    },
  });
  assert.match(
    closed,
    /<p class="wish-task-note wish-task-closed">Closed by you, [^<]+: merged in Git<\/p>/,
  );
  const byLead = card(s.TaskStatus.DONE, {
    closed: {
      actor: s.Closer.LEAD,
      createTime: { seconds: 1760000000n, nanos: 0 },
      note: "",
    },
  });
  assert.match(byLead, /wish-task-closed">Closed by the lead, [^<]+<\/p>/);
});

test("a task a fork continues links to its fork; a code no card holds stays text", () => {
  const card = (codes) =>
    s.renderToStaticMarkup(
      h(s.WishTask, {
        task: {
          id: "t1",
          code: "W1",
          title: "Trim the wick",
          status: s.TaskStatus.DONE,
          closed: {
            actor: s.Closer.LEAD,
            createTime: { seconds: 1760000000n, nanos: 0 },
            note: "continued in W2",
            continuedIn: "W2",
          },
        },
        codes,
        onStop() {},
        async onSend() {},
      }),
    );
  assert.match(
    card(new Map([["t2", "W2"]])),
    /Closed by the lead, [^<]+: continued in <button type="button" class="text-button agent-code task-link">W2<\/button><\/p>/,
  );
  assert.match(
    card(new Map()),
    /: continued in <span class="agent-code">W2<\/span><\/p>/,
  );
});

test("the Tasks tab lists what moves or waits by status, then the finished tasks, the latest first", () => {
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
      finished: s.finishedTasks(tasks),
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
  // W7, cut short and not resumed by Djinn, is history: among the finished ones.
  assert.deepEqual(order, [
    "W8",
    "W5",
    "W3",
    "W6",
    "W1",
    "W4",
    "W9",
    "W2",
    "W7",
  ]);
  assert.ok(
    html.indexOf("Moving or waiting") < html.indexOf("Task W8") &&
      html.indexOf("Finished") > html.indexOf("Task W1") &&
      html.indexOf("Finished") < html.indexOf("Task W4"),
  );
  assert.match(html, /Moving or waiting<span class="count">5<\/span>/);
  assert.match(html, /Finished<span class="count">4<\/span>/);
  assert.match(html, /Closed by you, [^<]+: merged/);
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
  const html = s.renderToStaticMarkup(
    h(s.TaskSections, {
      moving: s.movingTasks(tasks),
      finished: s.finishedTasks(tasks),
      azimas: s.azimaGroups(tasks),
      renderAzima: ({ azima, parts }) =>
        h(s.AzimaCard, {
          key: azima.id,
          azima,
          parts,
          tasks: byId,
          render: card,
        }),
      render: card,
    }),
  );
  const order = [...html.matchAll(/<span class="agent-code">([TW]\d+)</g)].map(
    (m) => m[1],
  );
  // Work of no azima moves or waits on its own; T2, under way, opened on its parts (running, planned, then done);
  // T10 waits; T1, done, folded; the finished work of no azima last.
  assert.deepEqual(order, ["W4", "T2", "W2", "W3", "W1", "T10", "T1", "W5"]);
  assert.match(html, /Moving or waiting<span class="count">1<\/span>/);
  assert.match(html, /Azimas<span class="count">3<\/span>/);
  assert.match(html, /Finished<span class="count">1<\/span>/);
  assert.match(html, /Waits for T2</);
  assert.match(html, /after T1</);
  assert.match(html, /1\/3/);
  assert.match(html, /In progress/);
  // No azima is ever said to wait for you.
  assert.doesNotMatch(html, /Waits for your answer/);
  assert.doesNotMatch(html, /tone-waiting/);

  // Nor does the flight plan: an azima neither waits for you nor moves.
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

// two wishes, a question each, a worker running in the second, read from in-memory services.
function twoWishes() {
  const lamp = wish(
    "01a11833-a440-7479-a067-52615c91da71",
    "Ship the lamp",
    s.WishState.ACTIVE,
    1,
  );
  const oil = wish(
    "01a11833-a440-7479-a067-52615c91da72",
    "Find the oil",
    s.WishState.ACTIVE,
    2,
  );
  const tasks = {
    [lamp.id]: [
      {
        id: "t1",
        wishId: lamp.id,
        code: "W1",
        title: "Trim the wick",
        status: s.TaskStatus.DONE,
        usage: usage(10, 400, 38000, 12000, 0.42),
      },
    ],
    [oil.id]: [
      {
        id: "t2",
        wishId: oil.id,
        code: "W1",
        title: "Taste the oils",
        status: s.TaskStatus.RUNNING,
        provider: s.Provider.CODEX,
        usage: usage(1200, 300, 5000, 0, 0),
      },
      {
        id: "t3",
        wishId: oil.id,
        code: "W2",
        title: "Pour it",
        status: s.TaskStatus.WAITING,
        editQuestionId: "q2",
      },
    ],
  };
  const questions = {
    [lamp.id]: [
      {
        id: "q1",
        wishId: lamp.id,
        code: "Q01",
        text: "Brass or glass?",
        options: ["Brass", "Glass"],
      },
      {
        id: "q3",
        wishId: lamp.id,
        code: "Q02",
        text: "Tonight?",
        options: [],
        answer: { choice: s.Choice.YES, note: "" },
      },
    ],
    [oil.id]: [
      {
        id: "q2",
        wishId: oil.id,
        code: "Q01",
        text: "May W2 pour?",
        options: [],
      },
    ],
  };
  const transport = s.createRouterTransport(({ service }) => {
    service(s.WishService, { list: () => ({ wishes: [lamp, oil] }) });
    service(s.TaskService, {
      list: (req) => ({ tasks: tasks[req.wishId] ?? [] }),
    });
    service(s.QuestionService, {
      list: (req) => ({ questions: questions[req.wishId] ?? [] }),
    });
    service(s.BlockService, { list: () => ({ blocks: [] }) });
  });
  return { lamp, oil, djinn: s.createDjinn(transport, 10) };
}

test("the flight plan merges the active wishes: their questions, the blocking one first, each with its wish", async () => {
  const { lamp, oil, djinn } = twoWishes();
  const closes = [djinn.store.open(lamp.id), djinn.store.open(oil.id)];
  await djinn.store.changed(lamp.id, [s.Change.WISH]);
  closes.forEach((close) => close());
  const html = s.renderToStaticMarkup(
    h(
      s.DjinnProvider,
      { djinn },
      h(s.FlightPlan, { wishes: [lamp, oil], onOpen() {}, onToast() {} }),
    ),
  );
  assert.match(html, /<h1>Flight plan<\/h1>/);
  // The blocking question of the second wish comes before the free one of the first, each marked with its wish.
  const blocking = html.indexOf("May W2 pour?");
  const free = html.indexOf("Brass or glass?");
  assert.ok(blocking > 0 && free > blocking);
  assert.match(html, /Blocks W2/);
  assert.match(
    html,
    /class="wish-origin" title="Find the oil"><b>2<\/b>Find the oil/,
  );
  // What waits; the tasks and the decisions of both wishes in their own tabs.
  assert.match(html, /W2 waits for your answer to Q01 before it may edit\./);
  assert.match(
    html,
    /id="view-tab-tasks"[^>]*>Tasks<span class="count">3<\/span>/,
  );
  assert.doesNotMatch(html, /Taste the oils/);
  assert.match(
    html,
    /id="view-tab-decisions"[^>]*>Decisions<span class="count">1<\/span>/,
  );
  assert.doesNotMatch(html, /Tonight\?/);
  // What each wish spent: the first with its cost, the second in tokens only.
  assert.match(
    html,
    /input 10 · output 400 · cache read 38K · cache written 12K · \$0\.42/,
  );
  assert.match(html, /input 1\.2K · output 300 · cache read 5K</);
  assert.match(html, /1 task without a cost: its agent gives tokens only/);
});

test("the flight plan hides its empty sections", async () => {
  const lamp = wish(
    "01a11833-a440-7479-a067-52615c91da71",
    "Ship the lamp",
    s.WishState.ACTIVE,
    1,
  );
  const transport = s.createRouterTransport(({ service }) => {
    service(s.WishService, { list: () => ({ wishes: [lamp] }) });
    service(s.TaskService, { list: () => ({ tasks: [] }) });
    service(s.QuestionService, { list: () => ({ questions: [] }) });
    service(s.BlockService, { list: () => ({ blocks: [] }) });
  });
  const djinn = s.createDjinn(transport, 10);
  const close = djinn.store.open(lamp.id);
  await djinn.store.changed(lamp.id, [s.Change.WISH]);
  close();
  const html = s.renderToStaticMarkup(
    h(
      s.DjinnProvider,
      { djinn },
      h(s.FlightPlan, { wishes: [lamp], onOpen() {}, onToast() {} }),
    ),
  );
  assert.doesNotMatch(html, /Your move|Who runs now|Spent/);
  assert.match(html, /Nothing waits for you, and nothing runs\./);
});

test("the inbox shows each item with its proposed route, the recommended destination first", async () => {
  const lamp = wish(
    "01a11833-a440-7479-a067-52615c91da71",
    "Ship the lamp",
    s.WishState.ACTIVE,
    1,
  );
  const item = {
    id: "01a12079-0000-7000-8000-000000000001",
    source: "babysit-mr",
    projectId: "p1",
    key: "https://gitlab.example.com/acme/gong/-/merge_requests/12",
    text: "Babysit !12 · Fix the wick\nhttps://gitlab.example.com/acme/gong/-/merge_requests/12",
    state: s.InboxState.NEW,
    route: {
      request: "Babysit !12",
      options: [
        {
          kind: s.RouteKind.NEW,
          title: "Babysit !12",
          projectIds: ["p1"],
          reason: "the skill babysit-mr makes this wish",
          template: { skill: "babysit-mr", projectId: "p1" },
        },
        { kind: s.RouteKind.FILE, wishId: lamp.id, title: lamp.title },
      ],
    },
  };
  const transport = s.createRouterTransport(({ service }) => {
    service(s.WishService, { list: () => ({ wishes: [lamp] }) });
    service(s.ProjectService, {
      list: () => ({ projects: [{ id: "p1", name: "gong" }] }),
    });
    service(s.InboxService, { list: () => ({ items: [item] }) });
    service(s.TaskService, { list: () => ({ tasks: [] }) });
    service(s.QuestionService, { list: () => ({ questions: [] }) });
    service(s.BlockService, { list: () => ({ blocks: [] }) });
  });
  const djinn = s.createDjinn(transport, 10);
  await djinn.store.changed("", [
    s.Change.WISH,
    s.Change.PROJECT,
    s.Change.INBOX,
  ]);
  const html = s.renderToStaticMarkup(
    h(
      s.DjinnProvider,
      { djinn },
      h(s.FlightPlan, { wishes: [lamp], onOpen() {}, onToast() {} }),
    ),
  );
  assert.match(html, /Inbox/);
  assert.match(html, /From babysit-mr/);
  assert.match(html, /<h3>Babysit !12 · Fix the wick<\/h3>/);
  const made = html.indexOf(
    "New wish “Babysit !12”, in gong, from the skill babysit-mr",
  );
  const filed = html.indexOf("File it in “Ship the lamp”");
  assert.ok(made > 0 && filed > made);
  assert.match(html, /Rub the lamp/);
  assert.match(html, /Dismiss/);
});

test("the folder of a new project has a folder dialog's button only when the window has one", () => {
  const typed = s.renderToStaticMarkup(
    h(s.FolderField, { value: "/tmp/lamp", onChange() {} }),
  );
  assert.match(typed, /value="\/tmp\/lamp"/);
  assert.doesNotMatch(typed, /<button/);
  assert.doesNotMatch(typed, /Choose a folder…/);

  const native = s.renderToStaticMarkup(
    h(s.FolderField, { value: "", onChange() {}, onChoose() {} }),
  );
  assert.match(native, /<input/);
  assert.match(
    native,
    /<button type="button"[^>]*>.*Choose a folder…<\/button>/,
  );
});

test("the global shortcut is a field of the settings, disabled where djinn cannot take one", () => {
  const set = s.renderToStaticMarkup(
    h(s.ShortcutField, {
      shortcut: {
        chord: "Ctrl+Alt+Space",
        defaultChord: "Ctrl+Alt+Space",
        available: true,
        problem: "",
      },
      onSave: async () => {},
    }),
  );
  assert.match(set, /Global shortcut/);
  assert.match(set, /value="Ctrl\+Alt\+Space"/);
  assert.match(set, /Default: Ctrl\+Alt\+Space/);
  assert.doesNotMatch(set, /disabled/);
  assert.doesNotMatch(set, /Not working/);

  const refused = s.renderToStaticMarkup(
    h(s.ShortcutField, {
      shortcut: {
        chord: "Ctrl+Alt+J",
        defaultChord: "Ctrl+Alt+Space",
        available: true,
        problem: "another application holds it",
      },
      onSave: async () => {},
    }),
  );
  assert.match(refused, /Not working: another application holds it/);

  const browser = s.renderToStaticMarkup(
    h(s.ShortcutField, { shortcut: undefined, onSave: async () => {} }),
  );
  assert.match(browser, /<input[^>]*disabled=""/);
  assert.match(browser, /Only in Djinn&#x27;s window/);
});

test("the update banner links the release notes of a newer release, and only a web link", () => {
  const banner = (state, phase = { kind: "idle" }) =>
    s.renderToStaticMarkup(
      h(s.UpdateBannerView, {
        state: { current: "v1.0.0", notResumed: [], ...state },
        phase,
        dismissed: false,
        onInstall() {},
        onDismiss() {},
        onNotes() {},
      }),
    );
  const release = banner({
    ready: "v2.0.0",
    notesUrl: "https://github.com/Empowill/Djinn/releases/tag/v2.0.0",
  });
  assert.match(release, /A new version of Djinn is ready/);
  assert.match(
    release,
    /<a href="https:\/\/github.com\/Empowill\/Djinn\/releases\/tag\/v2.0.0" target="_blank" rel="noopener noreferrer">Release notes<\/a>/,
  );
  assert.match(release, /<button type="button">Update<\/button>/);
  // While it restarts, the notes stay; the button goes.
  const restarting = banner(
    { ready: "v2.0.0", notesUrl: "https://example.com/v2.0.0" },
    { kind: "restarting" },
  );
  assert.match(restarting, /Restarting…/);
  assert.match(restarting, /Release notes/);
  assert.doesNotMatch(restarting, /<button/);
  // A local install has no notes; a link that is not a web one is not shown.
  assert.doesNotMatch(banner({ ready: "local-abc", notesUrl: "" }), /<a /);
  assert.doesNotMatch(
    banner({ ready: "v2.0.0", notesUrl: "javascript:alert(1)" }),
    /<a /,
  );
  // Nothing waits: no banner, whatever the notes.
  assert.equal(banner({ ready: "", notesUrl: "https://example.com" }), "");
});

test("Lead is split: the button resumes the recorded lead, the arrow lists this machine's agents", () => {
  const agents = [
    { id: "codex", name: "Codex", available: false, command: "codex" },
    { id: "claude", name: "Claude", available: true, command: "/bin/claude" },
    {
      id: "antigravity",
      name: "Antigravity",
      available: true,
      command: "/bin/agy",
    },
  ];
  const recorded = s.recordedAgent({
    provider: s.Provider.UNSPECIFIED,
    sessionId: "s1",
    directory: "/tmp/lamp",
  });
  assert.equal(recorded, s.Provider.CLAUDE);
  assert.equal(
    s.recordedAgent({ provider: s.Provider.CODEX, sessionId: "" }),
    undefined,
  );
  const props = {
    recorded,
    agents,
    loadAgents: async () => agents,
    onLead() {},
    onPick() {},
  };

  // Closed: Lead, then the arrow, which says it opens a menu.
  const closed = s.renderToStaticMarkup(h(s.LeadButton, props));
  assert.match(closed, /<span>Lead<\/span><\/button>/);
  assert.match(
    closed,
    /aria-label="Choose the agent"[^>]*aria-haspopup="menu" aria-expanded="false"/,
  );
  assert.doesNotMatch(closed, /role="menu"/);

  // Open: claude, codex, antigravity in that order; the recorded one marked, the missing one disabled with why.
  const open = s.renderToStaticMarkup(
    h(s.LeadButton, { ...props, open: true }),
  );
  assert.match(open, /aria-expanded="true"/);
  assert.match(
    open,
    /<div class="lead-menu" role="menu" aria-label="Choose the agent">/,
  );
  const order = ["Claude", "Codex", "Antigravity"].map((name) =>
    open.indexOf(`<span class="lead-agent-name">${name}`),
  );
  assert.ok(
    order[0] > 0 && order[0] < order[1] && order[1] < order[2],
    order.join(),
  );
  assert.match(
    open,
    /<button role="menuitem" class="lead-agent current" aria-current="true"><span class="lead-agent-name">Claude<svg[^]*?The wish&#x27;s lead: resumes its session/,
  );
  assert.match(
    open,
    /<button role="menuitem" class="lead-agent" disabled=""><span class="lead-agent-name">Codex<\/span><span class="lead-agent-detail">codex is not installed on this machine<\/span>/,
  );
  assert.match(
    open,
    /<button role="menuitem" class="lead-agent"><span class="lead-agent-name">Antigravity<\/span><span class="lead-agent-detail">Starts a new lead from the brief<\/span>/,
  );
  // The agents not yet known: the menu says it loads.
  const loading = s.renderToStaticMarkup(
    h(s.LeadMenu, { recorded, onPick() {} }),
  );
  assert.match(loading, /Loading…/);
});

test("the wish's description shows under its title, the title until one is written, and edits in place", () => {
  const titled = s.renderToStaticMarkup(
    h(s.WishDescription, {
      title: "Ship the lamp",
      description: "",
      onSave() {},
    }),
  );
  assert.match(
    titled,
    /<p class="wish-description" role="button" tabindex="0" title="Click to describe the wish[^"]*">Ship the lamp<\/p>/,
  );
  const described = s.renderToStaticMarkup(
    h(s.WishDescription, {
      title: "Ship the lamp",
      description: "Light the house.\nNot the street.",
      onSave() {},
    }),
  );
  assert.match(described, />Light the house.\nNot the street.<\/p>/);
  const editing = s.renderToStaticMarkup(
    h(s.WishDescription, {
      title: "Ship the lamp",
      description: "Light the house.\nNot the street.",
      editing: true,
      onSave() {},
    }),
  );
  assert.match(
    editing,
    /<textarea class="wish-description-edit" aria-label="Description" rows="2" autofocus="">Light the house.\nNot the street.<\/textarea>/,
  );
});

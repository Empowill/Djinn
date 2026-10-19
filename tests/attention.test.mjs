// The attention bar (src/attention.tsx): what holds up a wish, and where each line takes you. Run with `go tool task
// test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { screens, wish } from "./screens.mjs";

const { s, h } = await screens();

test("the attention bar counts what holds up a wish: blocking, your move, before X; never a question that can wait", () => {
  const w = wish("w1", "Ship the lamp", s.WishState.ACTIVE, 1);
  const q = (id, before = "", move = false) => ({
    id,
    code: id.toUpperCase(),
    text: id,
    before,
    move,
  });
  const items = s.attentionOf(
    [
      { wish: w, item: q("q1", "before the merge"), blocking: ["W1"] },
      { wish: w, item: q("q2", "before the demo"), blocking: [] },
      { wish: w, item: q("q3"), blocking: [] },
      { wish: w, item: q("q4", "before the release", true), blocking: [] },
      { wish: w, item: { ...q("q5"), waitingLead: true }, blocking: [] },
      { wish: w, item: q("q6", "", true), blocking: [] },
    ],
    [
      {
        wish: w,
        item: { id: "t9", code: "W9", status: s.TaskStatus.WAITING },
        question: "",
      },
    ],
    [w],
  );
  assert.deepEqual(
    items.map((i) => [i.key, i.level, i.label]),
    [
      ["q1", "blocking", undefined],
      ["q4", "move", undefined],
      ["q2", "question", "before the demo"],
      ["q5", "question", "Waiting for the Djinn"],
      ["t9", "action", undefined],
      ["ready-w1", "ready", undefined],
    ],
  );
  // q3 can wait, and so does q6, a move needed before nothing: their cards say "Can wait", the bar leaves them out.
  const markup = s.renderToStaticMarkup(h(s.AttentionBar, { items }));
  assert.match(markup, /6 things wait for you/);
  assert.doesNotMatch(markup, /Can wait|>Q3<|>Q6</);
  assert.match(markup, /<button class="attention-item level-move"/);
  // A move with no link has no button beside its line.
  assert.doesNotMatch(markup, /move-link/);
  assert.match(
    markup,
    /<span class="attention-level"><svg[^>]*class="lucide lucide-circle-user-round[^>]*>.*<\/svg>Your move<\/span>/,
  );
});

test("the attention bar counts a blocking question and a move, not a question that can wait; nothing but those, no bar", () => {
  const w = wish("w1", "Ship the lamp", s.WishState.ACTIVE, 1);
  const q = (id, extra = {}) => ({
    id,
    code: id.toUpperCase(),
    text: id,
    ...extra,
  });
  const blocking = { wish: w, item: q("q1"), blocking: ["W1"] };
  const move = {
    wish: w,
    item: q("q2", { move: true, before: "before the release" }),
    blocking: [],
  };
  const canWait = { wish: w, item: q("q3"), blocking: [] };
  const items = s.attentionOf([blocking, canWait, move], [], []);
  assert.deepEqual(
    items.map((i) => [i.key, i.level]),
    [
      ["q1", "blocking"],
      ["q2", "move"],
    ],
  );
  assert.match(
    s.renderToStaticMarkup(h(s.AttentionBar, { items })),
    /2 things wait for you/,
  );
  // One definition: the bar keeps what holdsUp keeps.
  assert.deepEqual([blocking, move, canWait].map(s.holdsUp), [
    true,
    true,
    false,
  ]);
  const alone = s.attentionOf([canWait], [], []);
  assert.equal(alone.length, 0);
  assert.equal(s.renderToStaticMarkup(h(s.AttentionBar, { items: alone })), "");
});

test("a move's line in the attention bar has its main link beside it, a button", () => {
  const w = wish("w1", "Ship the lamp", s.WishState.ACTIVE, 1);
  const pr = "https://github.com/empowill/djinn/pull/2";
  const items = s.attentionOf(
    [
      {
        wish: w,
        item: {
          id: "q1",
          code: "Q1",
          text: "Review PR #2 and take it out of draft",
          context: `The task opened it as a draft: ${pr}`,
          before: "before the release",
          move: true,
          options: [],
        },
        blocking: [],
      },
      {
        wish: w,
        item: {
          id: "q2",
          code: "Q2",
          text: `Which colour? ${pr}`,
          before: "before the demo",
          options: [],
        },
        blocking: [],
      },
    ],
    [],
    [],
  );
  assert.deepEqual(
    items.map((i) => [i.key, i.level, i.link]),
    [
      ["q1", "move", pr],
      ["q2", "question", undefined],
    ],
  );
  const markup = s.renderToStaticMarkup(h(s.AttentionBar, { items }));
  assert.match(
    markup,
    new RegExp(
      `<li class="attention-with-link"><button class="attention-item level-move">.*?</button><a href="${pr}" class="button secondary small move-link" target="_blank" rel="noopener noreferrer"><svg[^>]*>.*?</svg><span>Open PR #2</span></a></li>`,
    ),
  );
  assert.equal(markup.match(/move-link/g).length, 1);
});

test("the choice the lead's agent shows is the first, blocking line of the attention bar, its options as buttons", () => {
  const w = wish("w1", "Ship the lamp", s.WishState.ACTIVE, 1);
  const prompt = {
    title: "Would you like to run the following command?",
    lines: ["Reason: read the wish", "$ djinn wish brief"],
    options: ["Yes, proceed (y)", "No, cancel"],
  };
  const items = s.attentionOf(
    [{ wish: w, item: { id: "q1", code: "Q1", text: "q1" }, blocking: ["W1"] }],
    [],
    [],
    undefined,
    prompt,
  );
  assert.deepEqual(
    items.map((i) => [i.key, i.level, i.label]),
    [
      ["lead-prompt", "blocking", "Approval for the Djinn"],
      ["q1", "blocking", undefined],
    ],
  );
  const djinn = s.createDjinn(
    s.createRouterTransport(() => {}),
    10,
  );
  const markup = s.renderToStaticMarkup(
    h(s.DjinnProvider, { djinn }, h(s.AttentionBar, { items })),
  );
  assert.match(markup, /2 things wait for you/);
  assert.match(
    markup,
    /<li><div id="lead-prompt" class="attention-item attention-prompt level-blocking"><div class="attention-prompt-head"><span class="attention-level"><svg[^>]*>.*?<\/svg>Approval for the Djinn<\/span><span class="attention-text">Would you like to run the following command\?<\/span><\/div><pre class="attention-prompt-lines">Reason: read the wish\n\$ djinn wish brief<\/pre>/,
  );
  assert.match(
    markup,
    /<button class="button secondary small">Yes, proceed \(y\)<\/button><button class="button secondary small">No, cancel<\/button>/,
  );
  // Without options, no choice shows: no line.
  assert.equal(
    s.attentionOf([], [], [], undefined, { title: "", lines: [], options: [] })
      .length,
    0,
  );
});

test("a line of the attention bar takes you to its target in another tab, a folded part or another wish; a line gone is plain text", () => {
  const w1 = wish("w1", "Ship the lamp", s.WishState.ACTIVE, 1);
  const w2 = wish("w2", "Light the lamp", s.WishState.ACTIVE, 2);
  const q = (id, extra = {}) => ({
    id,
    code: id.toUpperCase(),
    text: id,
    ...extra,
  });
  const before = { before: "before the merge" };
  const items = s.attentionOf(
    [
      { wish: w1, item: q("q1", before), blocking: [] },
      { wish: w1, item: q("q2", { waitingLead: true }), blocking: [] },
      { wish: w2, item: q("q3", before), blocking: [] },
      { wish: w1, item: q("q4", before), blocking: [] },
    ],
    [
      {
        wish: w1,
        item: {
          id: "t9",
          code: "W9",
          status: s.TaskStatus.WAITING,
        },
        question: "",
      },
    ],
    [w1],
  );
  const line = (key) => items.find((i) => i.key === key);
  // What the wish w1 shows: its open question q1 and its worker in the first tab, q2 answered in the decisions, its
  // grant; q4 withdrawn meanwhile is not there any more.
  const shown = new Set([
    "question-q1",
    "decision-q2",
    "waiting-t9",
    "grant-w1",
  ]);

  // In another tab: an open question in the first tab, one answered and waiting for a lead in the decisions.
  assert.equal(line("q2").target, "decision-q2");
  assert.equal(s.tabOf("decision-q2"), "decisions");
  assert.deepEqual(s.wayTo(line("q1"), shown, "w1"), { tab: "main" });
  assert.deepEqual(s.wayTo(line("q2"), shown, "w1"), { tab: "decisions" });
  assert.deepEqual(s.wayTo(line("t9"), shown, "w1"), { tab: "main" });
  assert.deepEqual(s.wayTo(line("ready-w1"), shown, "w1"), { tab: "main" });
  // In another wish: its wish opens, whatever this one shows; in the flying carpet, every wish is here.
  assert.deepEqual(s.wayTo(line("q3"), shown, "w1"), { wishId: "w2" });
  assert.equal(s.wayTo(line("q3"), shown), undefined);
  assert.deepEqual(s.wayTo(line("q3"), new Set(["question-q3"])), {
    tab: "main",
  });
  // Gone: no way, and the bar does not offer it as a button.
  assert.equal(s.wayTo(line("q4"), shown, "w1"), undefined);
  const markup = s.renderToStaticMarkup(
    h(s.AttentionBar, {
      items: items.filter((i) => i.wishId === "w1"),
      reach: (item) => (s.wayTo(item, shown, "w1") ? () => {} : undefined),
    }),
  );
  assert.equal(markup.match(/<button class="attention-item/g).length, 4);
  assert.match(
    markup,
    /<span class="attention-item level-question"><span class="attention-level">before the merge<\/span><b class="attention-code">Q4<\/b><span class="attention-text">q4<\/span><\/span>/,
  );
});

test("a failed worker never waits for you: refused calls are the Djinn's (W96)", () => {
  // W19 of wish Will retour beta (16/10/2026): an investigator failed on refused djinn commands, Djinn started W20 in
  // its place, and W19 still showed at the top as waiting for you, with nothing you could do.
  const w = wish("w1", "Will retour beta", s.WishState.ACTIVE, 1);
  const w19 = {
    id: "t19",
    wishId: "w1",
    code: "W19",
    title: "Q17: enlighten",
    status: s.TaskStatus.FAILED,
    role: s.TaskRole.INVESTIGATOR,
    question: "Q17",
    error: "its command was refused: djinn task list --wish-id w1 | grep Q17",
    refused: { count: 2, names: ["djinn"] },
  };
  const w20 = {
    ...w19,
    id: "t20",
    code: "W20",
    status: s.TaskStatus.RUNNING,
    error: "",
    refused: undefined,
  };
  const failed = {
    ...w19,
    id: "t3",
    code: "W3",
    title: "Light the wick",
    role: s.TaskRole.UNSPECIFIED,
    question: "",
    error: "exit code 1: fuel line broken",
    refused: undefined,
  };
  const detail = { tasks: [w19, w20, failed], questions: [] };
  assert.deepEqual(s.waitingTasks(w, detail), []);
  assert.deepEqual(s.attentionOf([], s.waitingTasks(w, detail), []), []);
  const plan = s.flightPlan([w], {
    w1: { ...detail, blocks: [], loaded: true },
  });
  assert.deepEqual(plan.waiting, []);
  // It stays in sight among the tasks that move, failed, for the Djinn to take up.
  assert.deepEqual(
    plan.moving.map(({ item }) => item.code),
    ["W20", "W19", "W3"],
  );
  // A worker that waits for its edit question still waits for you.
  const asking = {
    id: "t4",
    wishId: "w1",
    code: "W4",
    title: "Edit",
    status: s.TaskStatus.WAITING,
  };
  const waiting = s.waitingTasks(w, { tasks: [w19, asking], questions: [] });
  assert.deepEqual(
    waiting.map(({ item }) => item.code),
    ["W4"],
  );
  const [line] = s.attentionOf([], waiting, []);
  assert.equal(line.target, "waiting-t4");
  assert.equal(line.text, "waits for your answer before it may edit");
  // The flying carpet's list of what waits for you names it alone.
  const html = s.renderToStaticMarkup(h(s.WaitingTasks, { waiting }));
  assert.match(html, /waiting-t4/);
  assert.doesNotMatch(html, /W19|refused/);
});

test("a limit the driven lead hit comes first in the attention bar, a button per move; none says the events wait", () => {
  const w = wish("w1", "Ship the lamp", s.WishState.ACTIVE, 1);
  const accounts = [
    {
      name: "work",
      provider: s.Provider.CLAUDE,
      label: "Work",
      configDirectory: "/d",
    },
  ];
  const limit = {
    text: "the account's session limit, resets at 07:20",
    seat: { provider: s.Provider.CLAUDE, model: "", account: "" },
    moves: [
      { provider: s.Provider.CLAUDE, model: "", account: "work" },
      { provider: s.Provider.CODEX, model: "", account: "" },
    ],
  };
  const items = s.attentionOf(
    [{ wish: w, item: { id: "q1", code: "Q1", text: "q1" }, blocking: ["W1"] }],
    [],
    [],
    undefined,
    undefined,
    [],
    { limit, accounts },
  );
  assert.deepEqual(
    items.map((i) => [i.key, i.level, i.label]),
    [
      ["lead-limit", "blocking", "The Djinn hit a limit"],
      ["q1", "blocking", undefined],
    ],
  );
  assert.equal(
    items[0].text,
    "Claude Code hit the account's session limit, resets at 07:20: its events wait",
  );
  const djinn = s.createDjinn(
    s.createRouterTransport(() => {}),
    10,
  );
  const markup = s.renderToStaticMarkup(
    h(s.DjinnProvider, { djinn }, h(s.AttentionBar, { items })),
  );
  assert.match(
    markup,
    /<div id="lead-limit" class="attention-item attention-prompt level-blocking">[^]*?<button class="button secondary small">Move the Djinn to Claude Code · Work<\/button><button class="button secondary small">Move the Djinn to Codex<\/button>/,
  );
  const alone = s.attentionOf([], [], [], undefined, undefined, [], {
    limit: { ...limit, moves: [] },
    accounts,
  });
  const none = s.renderToStaticMarkup(
    h(s.DjinnProvider, { djinn }, h(s.AttentionBar, { items: alone })),
  );
  assert.match(
    none,
    /No other agent or account is signed in here: its events wait for the reset/,
  );
});

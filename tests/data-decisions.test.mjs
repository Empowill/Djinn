// The decision log of a wish, read from the protos, and its tab: the decisions the latest first, who took them, the
// tasks they led to, no button. Run with `go tool task test-ui`. Node reports English.
import test from "node:test";
import assert from "node:assert/strict";

import { bundle } from "./bundle.mjs";

const d = await bundle(
  "decisions",
  `export * from "@/src/data/decisions.ts";
export { createElement } from "react";
export { renderToStaticMarkup } from "react-dom/server";
export { DecisionLog } from "@/src/decision-log.tsx";
export { WishTask } from "@/src/wish-task.tsx";
export * from "@/gen/ts/plan/v1/plan_pb.ts";`,
);
const h = d.createElement;

const at = (seconds) => ({ seconds: BigInt(seconds), nanos: 0 });
const approved = [{ kind: d.MarkKind.APPROVED, actor: "local" }];

// A wish: two questions answered, one open, three decision blocks, a section; three tasks, two from decisions.
const questions = [
  {
    id: "q1",
    code: "Q01",
    text: "Brass or copper?",
    options: ["Brass", "Copper"],
    answer: { choice: d.Choice.A, note: "Brass shines.", createTime: at(10) },
    icon: "🔒",
    marks: [],
  },
  {
    id: "q2",
    code: "Q02",
    text: "Light it tonight?",
    options: [],
    answer: { choice: d.Choice.YES, note: "", createTime: at(40) },
    marks: approved,
  },
  { id: "q3", code: "Q03", text: "Which oil?", options: [] },
];
const blocks = [
  {
    id: "b1",
    kind: "decision",
    title: "No wick",
    content: "Later.",
    taskId: "t1",
    createTime: at(20),
    icon: "🧱",
  },
  {
    id: "b2",
    kind: "Decision",
    title: "Postpone the glass",
    content: "Once the brass holds.",
    createTime: at(30),
  },
  {
    id: "b3",
    kind: "decision",
    title: "One lamp",
    content: "",
    createTime: at(50),
    marks: approved,
  },
  {
    id: "b4",
    kind: "section",
    title: "Lexicon",
    content: "",
    createTime: at(60),
  },
];
const tasks = [
  {
    id: "t1",
    code: "W1",
    title: "Polish",
    status: d.TaskStatus.RUNNING,
    decision: "q01",
  },
  {
    id: "t2",
    code: "W2",
    title: "Glass",
    status: d.TaskStatus.PENDING,
    decision: "b2",
  },
  { id: "t3", code: "W3", title: "Oil", status: d.TaskStatus.DONE },
];

test("the decisions are the answered questions and the decision blocks, the latest first, with who took them", () => {
  const log = d.decisionsOf(questions, blocks, tasks);
  assert.deepEqual(
    log.map((x) => [
      x.ref,
      x.icon,
      x.human,
      x.approved,
      x.by,
      x.tasks.map((t) => t.code).join(),
    ]),
    [
      ["b3", "📌", true, true, "", ""],
      ["Q02", "💬", true, true, "", ""],
      ["b2", "📌", false, false, "lead", "W2"],
      ["b1", "🧱", false, false, "W1", ""],
      ["Q01", "🔒", true, false, "", "W1"],
    ],
  );
  assert.equal(d.decisionOf(tasks[0], log)?.id, "q1");
  assert.equal(d.decisionOf(tasks[1], log)?.id, "b2");
  assert.equal(d.decisionOf(tasks[2], log), undefined);
});

test("the Decisions tab lists them without a button: the human label and colour, an agent in words, links to tasks", () => {
  const items = d
    .decisionsOf(questions, blocks, tasks)
    .map((item) => ({ item }));
  const html = d.renderToStaticMarkup(h(d.DecisionLog, { items, onTask() {} }));
  assert.doesNotMatch(html, /<button/);
  assert.doesNotMatch(html, /Mark read|Approve as it is/);
  // The latest first.
  const order = [
    "One lamp",
    "Light it tonight?",
    "Postpone the glass",
    "No wick",
    "Brass or copper?",
  ];
  const at = order.map((s) => html.indexOf(s));
  assert.ok(
    at.every((i, n) => i > 0 && (n === 0 || i > at[n - 1])),
    `order ${at}`,
  );
  // The developer's in the human tone, with an icon and a word; an agent's in words.
  assert.match(
    html,
    /decision-row tone-human[^>]*id="decision-q1".*status-badge tone-human.*<svg[^>]*>.*<span>Decided by you<\/span>/,
  );
  assert.match(html, /id="decision-b3".*<span>Approved by you<\/span>/);
  assert.match(html, /decision-row agent[^>]*id="decision-b2".*By the lead/);
  assert.match(html, /id="decision-b1".*By W1/);
  // One emoji at the start of each row, the choice in words, the note.
  assert.match(
    html,
    /<span class="decision-icon" aria-hidden="true">🔒<\/span>/,
  );
  assert.match(html, /→ A · Brass/);
  assert.match(html, /Brass shines\./);
  // A decision links to the tasks it led to.
  assert.match(
    html,
    /id="decision-q1".*Led to<a class="agent-code" href="#task-t1" title="Open W1 in the Tasks tab">W1<\/a>/,
  );
  assert.match(html, /id="decision-b2".*href="#task-t2"[^>]*>W2<\/a>/);
  // Nothing decided yet: it says so.
  const empty = d.renderToStaticMarkup(
    h(d.DecisionLog, { items: [], onTask() {} }),
  );
  assert.match(empty, /No decision yet/);
});

test("an answer in a wish without a lead session says no lead was told; a decision block, nothing", () => {
  const items = d
    .decisionsOf(questions, blocks, tasks)
    .map((item) => ({ item }));
  const told = d.renderToStaticMarkup(
    h(d.DecisionLog, { items, noLead: () => false, onTask() {} }),
  );
  assert.doesNotMatch(told, /No lead to tell/);
  const html = d.renderToStaticMarkup(
    h(d.DecisionLog, { items, noLead: () => true, onTask() {} }),
  );
  // Q01 and Q02, the two answered questions; no block.
  assert.equal(
    html.match(
      /<p class="no-lead">No lead to tell: the answer waits in the wish&#x27;s brief\.<\/p>/g,
    )?.length,
    2,
  );
  assert.match(html, /→ A · Brass<\/p><p class="no-lead">/);
  assert.doesNotMatch(html, /id="decision-b2".*?no-lead.*?id="decision-b1"/);
});

test("a task links back to the decision it comes from", () => {
  const log = d.decisionsOf(questions, blocks, tasks);
  const html = d.renderToStaticMarkup(
    h(d.WishTask, {
      task: { ...tasks[0], usage: undefined, dependsOn: [] },
      decision: d.decisionOf(tasks[0], log),
      onDecision() {},
      onStop() {},
      onSend: async () => {},
    }),
  );
  assert.match(
    html,
    /<a class="decision-link" href="#decision-q1" title="Show the decision Q01 in the Decisions tab"><span aria-hidden="true">🔒<\/span>From Q01<\/a>/,
  );
});

// A wish of many answered questions: Q001 the oldest.
const many = (n) =>
  Array.from({ length: n }, (_, i) => ({
    id: `m${i + 1}`,
    code: `Q${String(i + 1).padStart(3, "0")}`,
    text: `Decision number ${i + 1}?`,
    options: [],
    answer: { choice: d.Choice.YES, note: "", createTime: at(i + 1) },
    marks: [],
  }));
const rows = (html) => html.match(/<article class="decision-row/g)?.length ?? 0;

test("the Decisions tab shows the latest 30, the older ones folded out of the page behind a line", () => {
  const log = (n, focus) =>
    d.renderToStaticMarkup(
      h(d.DecisionLog, {
        items: d.decisionsOf(many(n), [], []).map((item) => ({ item })),
        focus,
        onTask() {},
      }),
    );
  // 45: the 30 latest, the 15 others behind the line, not rendered; the count says 45.
  const some = log(45);
  assert.equal(rows(some), 30);
  assert.match(some, /<span class="count">45<\/span>/);
  assert.match(some, /id="decision-m45"/);
  assert.match(some, /id="decision-m16"/);
  assert.doesNotMatch(some, /id="decision-m15"/);
  assert.match(
    some,
    /<\/div><button class="text-button older-line">.*Show the 15 older decisions<\/button><\/section>$/,
  );
  // 100: the line unfolds a batch of 30 of the 70.
  assert.match(log(100), /Show 30 of the 70 older decisions/);
  // A decision a task leads back to is unfolded, by whole batches: the 50th latest shows 60.
  const focused = log(100, "m51");
  assert.equal(rows(focused), 60);
  assert.match(focused, /decision-row[^"]*focused" id="decision-m51"/);
  assert.match(focused, /Show 30 of the 40 older decisions/);
  // 30 or fewer: no line.
  assert.equal(rows(log(30)), 30);
  assert.doesNotMatch(log(30), /older-line/);
});

test("a long note folds under its first line, its body rendered once opened", () => {
  const note = `Brass, for three reasons.\n\n${"- one more reason\n".repeat(8)}`;
  const items = d
    .decisionsOf(
      [
        {
          ...many(1)[0],
          answer: { choice: d.Choice.YES, note, createTime: at(1) },
        },
      ],
      [],
      [],
    )
    .map((item) => ({ item }));
  const html = d.renderToStaticMarkup(h(d.DecisionLog, { items, onTask() {} }));
  assert.match(
    html,
    /<details class="decision-note"><summary>Brass, for three reasons\.<\/summary><\/details>/,
  );
  assert.doesNotMatch(html, /one more reason/);
});

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

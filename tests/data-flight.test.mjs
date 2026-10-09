// The flight plan of several wishes, what tasks spent, and the journal of a wish, read from the protos. Run with
// `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { bundle } from "./bundle.mjs";

const f = await bundle(
  "flight",
  `export * from "@/src/data/flight.ts";
export * from "@/src/data/journal.ts";
export { taskStatusText, taskTone } from "@/src/data/format.ts";
export { create } from "@bufbuild/protobuf";
export { anyPack } from "@bufbuild/protobuf/wkt";
export * from "@/gen/ts/plan/v1/plan_pb.ts";`,
);

const at = (seconds) => ({ seconds: BigInt(seconds), nanos: 0 });
const wish = (id, title, rank, extra = {}) => ({
  id,
  title,
  rank,
  state: f.WishState.ACTIVE,
  ...extra,
});
const detail = (tasks, questions) => ({
  tasks,
  questions,
  blocks: [],
  loaded: true,
});

test("the flight plan merges the wishes: blocking questions first, every item keeps its wish", () => {
  const lamp = wish("w1", "Ship the lamp", 1);
  const oil = wish("w2", "Find the oil", 2, { ready: true });
  const plan = f.flightPlan([lamp, oil], {
    w1: detail(
      [
        { id: "t1", code: "W1", status: f.TaskStatus.RUNNING },
        { id: "t3", code: "W3", status: f.TaskStatus.INTERRUPTED },
      ],
      [
        { id: "q1", code: "Q01", text: "Brass?" },
        {
          id: "q2",
          code: "Q02",
          text: "Glass?",
          answer: { choice: f.Choice.YES, createTime: at(10) },
        },
      ],
    ),
    w2: detail(
      [
        {
          id: "t2",
          code: "W2",
          status: f.TaskStatus.WAITING,
          editQuestionId: "q3",
        },
      ],
      [
        { id: "q3", code: "Q01", text: "May W2 edit?" },
        {
          id: "q4",
          code: "Q02",
          text: "Olive?",
          answer: { choice: f.Choice.A, createTime: at(20) },
        },
      ],
    ),
  });
  // The second wish's blocking question comes before the first wish's free one.
  assert.deepEqual(
    plan.questions.map((q) => [q.wish.id, q.item.id, q.blocking]),
    [
      ["w2", "q3", ["W2"]],
      ["w1", "q1", []],
    ],
  );
  assert.deepEqual(
    plan.waiting.map((w) => [w.wish.id, w.item.code, w.question]),
    [
      ["w1", "W3", ""],
      ["w2", "W2", "Q01"],
    ],
  );
  assert.deepEqual(
    plan.running.map((r) => [r.wish.id, r.item.code]),
    [["w1", "W1"]],
  );
  // The latest decision first, whatever its wish.
  assert.deepEqual(
    plan.decisions.map((d) => [d.wish.id, d.item.id]),
    [
      ["w2", "q4"],
      ["w1", "q2"],
    ],
  );
  assert.deepEqual(
    plan.ready.map((w) => w.id),
    ["w2"],
  );
  assert.equal(plan.loaded, true);
  // A wish not read yet: the plan says so.
  assert.equal(f.flightPlan([lamp], {}).loaded, false);
});

test("the Tasks tab: what moves or waits by status, then the finished tasks, the latest ended first", () => {
  const tasks = [
    {
      id: "t1",
      code: "W1",
      status: f.TaskStatus.DONE,
      createTime: at(1),
      endTime: at(10),
    },
    {
      id: "t2",
      code: "W2",
      status: f.TaskStatus.INTERRUPTED,
      createTime: at(2),
    },
    {
      id: "t3",
      code: "W3",
      status: f.TaskStatus.STOPPED,
      createTime: at(3),
      endTime: at(30),
    },
    {
      id: "t4",
      code: "W4",
      status: f.TaskStatus.DONE,
      createTime: at(4),
      endTime: at(10),
    },
    {
      id: "t5",
      code: "W5",
      status: f.TaskStatus.FAILED,
      createTime: at(5),
      endTime: at(50),
    },
    { id: "t6", code: "W6", status: f.TaskStatus.DONE, createTime: at(6) },
  ];
  assert.deepEqual(
    f.finishedTasks(tasks).map((t) => t.code),
    ["W3", "W4", "W1", "W6"],
  );
  // Running, cut short and failed first, then waiting and paused, the planned ones last.
  assert.deepEqual(
    f.movingTasks(tasks).map((t) => t.code),
    ["W2", "W5"],
  );
  const moving = [
    { code: "P", status: f.TaskStatus.PENDING },
    { code: "A", status: f.TaskStatus.PAUSED },
    { code: "W", status: f.TaskStatus.WAITING },
    { code: "F", status: f.TaskStatus.FAILED },
    { code: "I", status: f.TaskStatus.INTERRUPTED },
    { code: "R", status: f.TaskStatus.RUNNING },
    { code: "R2", status: f.TaskStatus.RUNNING },
    { code: "S", status: f.TaskStatus.RESUMING },
    {
      code: "WA",
      status: f.TaskStatus.RUNNING,
      provider: f.Provider.WATCH,
    },
  ];
  // A running watcher watches: it waits after the paused ones.
  assert.deepEqual(
    f.movingTasks(moving).map((t) => t.code),
    ["R", "R2", "I", "F", "S", "W", "A", "WA", "P"],
  );
  // A task cut short and taken over by a fork is finished, as on the page.
  const forked = [
    { id: "a", wishId: "w", code: "W1", status: f.TaskStatus.INTERRUPTED },
    {
      id: "b",
      wishId: "w",
      code: "W2",
      status: f.TaskStatus.RUNNING,
      forkOf: "W1",
    },
  ];
  assert.deepEqual(
    f.finishedTasks(forked).map((t) => t.code),
    ["W1"],
  );
  // The flight plan merges the Tasks tabs of its wishes, each task with its wish.
  const lamp = wish("w1", "Ship the lamp", 1);
  const oil = wish("w2", "Find the oil", 2);
  const plan = f.flightPlan([lamp, oil], {
    w1: detail(tasks.slice(0, 3), []),
    w2: detail(tasks.slice(3), []),
  });
  assert.deepEqual(
    plan.finished.map(({ wish, item }) => `${wish.id}:${item.code}`),
    ["w1:W3", "w2:W4", "w1:W1", "w2:W6"],
  );
  assert.deepEqual(
    plan.moving.map(({ wish, item }) => `${wish.id}:${item.code}`),
    ["w1:W2", "w2:W5"],
  );
});

test("what tasks spent: tokens always, the cost only where the agent gives one", () => {
  const usage = (input, output, read, write, cost) => ({
    inputTokens: BigInt(input),
    outputTokens: BigInt(output),
    cacheReadTokens: BigInt(read),
    cacheWriteTokens: BigInt(write),
    costUsd: cost,
  });
  const spent = f.spent([
    { usage: usage(10, 400, 38000, 12000, 0.0032) }, // claude
    { usage: usage(1200, 300, 5000, 0, 0) }, // codex: tokens only
    { usage: usage(0, 0, 0, 0, 0) }, // planned
    {},
  ]);
  assert.equal(spent.tasks, 2);
  assert.equal(spent.inputTokens, 1210n);
  assert.equal(spent.outputTokens, 700n);
  assert.equal(spent.cacheReadTokens, 43000n);
  assert.equal(spent.cacheWriteTokens, 12000n);
  assert.equal(spent.costUsd, 0.0032);
  assert.equal(spent.withoutCost, 1);
  assert.equal(f.tokensOf(usage(1, 2, 3, 4, 0)), 10n);
});

test("the journal reads its commands from their requests, with the log blocks, the latest first", () => {
  const command = (id, seconds, method, schema, init) => ({
    id,
    at: at(seconds),
    method,
    request: f.anyPack(schema, f.create(schema, init)),
  });
  const exp = {
    tasks: [{ id: "t1", code: "W1" }],
    questions: [{ id: "q1", code: "Q01" }],
    projects: [{ id: "p1", name: "lamp" }],
    commands: [
      command(
        "c1",
        1,
        "/plan.v1.WishService/Make",
        f.WishServiceMakeRequestSchema,
        { title: "Ship the lamp" },
      ),
      command(
        "c2",
        3,
        "/plan.v1.QuestionService/Answer",
        f.QuestionServiceAnswerRequestSchema,
        {
          question: { ref: { case: "id", value: "q1" } },
          choice: f.Choice.B,
          note: "Glass.",
        },
      ),
      command(
        "c3",
        4,
        "/plan.v1.TaskService/Stop",
        f.TaskServiceStopRequestSchema,
        { taskId: "t1" },
      ),
      command(
        "c4",
        5,
        "/plan.v1.WishService/Allow",
        f.WishServiceAllowRequestSchema,
        { projectId: "p1", mode: f.Allowance.EDIT },
      ),
    ],
  };
  const blocks = [
    {
      id: "b1",
      kind: "Log",
      title: "Kick-off",
      content: "Started **W1**.",
      createTime: at(2),
    },
    {
      id: "b2",
      kind: "section",
      title: "Lexicon",
      content: "",
      createTime: at(6),
    },
  ];
  assert.deepEqual(
    f.journal(exp, blocks).map((e) => [e.id, e.command, e.summary, e.note]),
    [
      ["c4", "wish allow", "edit lamp", ""],
      ["c3", "task stop", "W1", ""],
      ["c2", "question answer", "Q01 b Glass.", ""],
      ["b1", "", "Kick-off", "Started **W1**."],
      ["c1", "wish make", "Ship the lamp", ""],
    ],
  );
  // Without the commands, the log blocks alone.
  assert.deepEqual(
    f.journal(undefined, blocks).map((e) => e.id),
    ["b1"],
  );
  assert.equal(
    f.commandName("/plan.v1.SkillService/Unsummon"),
    "skill unsummon",
  );
});

test("a task Djinn resumes, or one resumed as another task, is not the user's move", () => {
  const lamp = wish("w1", "Ship the lamp", 1);
  const tasks = [
    { id: "t1", wishId: "w1", code: "W1", status: f.TaskStatus.RESUMING },
    {
      id: "t2",
      wishId: "w1",
      code: "W2",
      status: f.TaskStatus.RESUMING,
      resumeAfter: at(100),
    },
    { id: "t3", wishId: "w1", code: "W3", status: f.TaskStatus.INTERRUPTED },
    { id: "t4", wishId: "w1", code: "W4", status: f.TaskStatus.INTERRUPTED },
    {
      id: "t5",
      wishId: "w1",
      code: "W5",
      status: f.TaskStatus.RUNNING,
      forkOf: "W3",
    },
  ];
  const plan = f.flightPlan([lamp], { w1: detail(tasks, []) });
  // Only W4 waits for the user: cut short, and nothing took it over.
  assert.deepEqual(
    plan.waiting.map((w) => w.item.code),
    ["W4"],
  );
  assert.equal(f.forkedAs(tasks[2], tasks), "W5");
  assert.equal(f.forkedAs(tasks[3], tasks), "");
  // The ones Djinn resumes show among the running ones.
  assert.deepEqual(
    plan.running.map((r) => r.item.code),
    ["W1", "W2", "W5"],
  );
});

test("the states Djinn handles by itself speak the window's status language", () => {
  const S = f.TaskStatus;
  assert.equal(f.taskTone({ status: S.RESUMING }), "running");
  assert.equal(
    f.taskTone({ status: S.RESUMING, resumeAfter: at(100) }),
    "paused",
  );
  assert.equal(f.taskTone({ status: S.INTERRUPTED }, "W5"), "stopped");
  assert.equal(f.taskTone({ status: S.INTERRUPTED }), "interrupted");
});

test("a watcher has its own tone while its command runs", () => {
  const S = f.TaskStatus;
  const watch = (status) => ({ status, provider: f.Provider.WATCH });
  assert.equal(f.taskTone(watch(S.RUNNING)), "watching");
  assert.equal(f.taskStatusText(watch(S.RUNNING)), "Watching");
  assert.equal(f.taskTone(watch(S.PAUSED)), "paused");
  assert.equal(f.taskTone(watch(S.DONE)), "done");
  assert.equal(
    f.taskTone({ status: S.RUNNING, provider: f.Provider.CLAUDE }),
    "running",
  );
});

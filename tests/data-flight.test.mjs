// The flying carpet of several wishes, what tasks spent, and the journal of a wish, read from the protos. Run with
// `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { bundle } from "./bundle.mjs";

const f = await bundle(
  "flight",
  `export * from "@/src/data/flight.ts";
export * from "@/src/data/journal.ts";
export {
  cancelledText,
  deletedText,
  fixerOf,
  isTaskMerging,
  runningOf,
  span,
  taskFinished,
  taskStatusText,
  taskTime,
  taskTone,
  wishTone,
} from "@/src/data/format.ts";
export * from "@/src/data/ifrit.ts";
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

test("the flying carpet merges the wishes: questions by wish, as they were asked, every item keeps its wish", () => {
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
  // The wishes' rank first: the second wish's blocking question stays after the first wish's free one.
  assert.deepEqual(
    plan.questions.map((q) => [q.wish.id, q.item.id, q.blocking]),
    [
      ["w1", "q1", []],
      ["w2", "q3", ["W2"]],
    ],
  );
  // Only a worker that asks waits for the user: W3, cut short, is Djinn's to resume or history.
  assert.deepEqual(
    plan.waiting.map((w) => [w.wish.id, w.item.code, w.question]),
    [["w2", "W2", "Q01"]],
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
    ["W3", "W4", "W1", "W6", "W2"],
  );
  // Running and failed first, then waiting and paused, the planned ones last; a task cut short is finished.
  assert.deepEqual(
    f.movingTasks(tasks).map((t) => t.code),
    ["W5"],
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
    ["R", "R2", "F", "S", "W", "A", "WA", "P"],
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
  // The flying carpet merges the Tasks tabs of its wishes, each task with its wish.
  const lamp = wish("w1", "Ship the lamp", 1);
  const oil = wish("w2", "Find the oil", 2);
  const plan = f.flightPlan([lamp, oil], {
    w1: detail(tasks.slice(0, 3), []),
    w2: detail(tasks.slice(3), []),
  });
  assert.deepEqual(
    plan.finished.map(({ wish, item }) => `${wish.id}:${item.code}`),
    ["w1:W3", "w2:W4", "w1:W1", "w2:W6", "w1:W2"],
  );
  assert.deepEqual(
    plan.moving.map(({ wish, item }) => `${wish.id}:${item.code}`),
    ["w2:W5"],
  );
});

test("a done task is settled once its work is committed, has none to merge, or its integration is cleared", () => {
  const I = f.IntegrationState;
  const done = (state, extra = {}) => ({
    id: `t${state}`,
    code: `W${state}`,
    status: f.TaskStatus.DONE,
    integration: state === undefined ? undefined : { state },
    ...extra,
  });
  const nest = f.mergeNest([]);
  const settled = (task) => f.settledTask(task, nest);
  // Work committed, no work to merge (no integration: no branch, or cleared): folded with the finished.
  assert.equal(settled(done(I.COMMITTED)), true);
  assert.equal(settled(done(undefined)), true);
  assert.equal(settled(done(I.UNSPECIFIED)), true);
  // Work on its way: waiting for its batch, merging, in conflict, red, or left not committed. It stays in sight.
  for (const state of [
    I.PENDING,
    I.INTEGRATING,
    I.CONFLICT,
    I.RED,
    I.UNCOMMITTED,
  ])
    assert.equal(settled(done(state)), false, `state ${state}`);
  // A cancelled or stopped task has no work to merge: whatever its integration says, it is settled.
  assert.equal(
    settled(done(I.PENDING, { status: f.TaskStatus.CANCELLED })),
    true,
  );
  assert.equal(
    settled(done(I.CONFLICT, { status: f.TaskStatus.STOPPED })),
    true,
  );
  // A task whose correction is done but merging moves with it, until that correction's work is committed.
  const conflicted = done(I.COMMITTED, { id: "w1", code: "W1" });
  const correction = (state) => ({
    id: "w2",
    code: "W2",
    status: f.TaskStatus.DONE,
    integration: { state },
    correction: { failure: { taskIds: ["w1"] } },
  });
  const merging = [conflicted, correction(I.INTEGRATING)];
  assert.equal(f.settledTask(conflicted, f.mergeNest(merging)), false);
  const committed = [conflicted, correction(I.COMMITTED)];
  assert.equal(f.settledTask(conflicted, f.mergeNest(committed)), true);
});

test("work that merges stays among the moving tasks, then folds with the finished once committed", () => {
  const I = f.IntegrationState;
  const tasks = (state) => [
    {
      id: "a1",
      code: "T1",
      kind: f.TaskKind.AZIMA,
      azima: { state: f.AzimaState.IN_PROGRESS },
    },
    {
      id: "w8",
      code: "W8",
      status: f.TaskStatus.DONE,
      partOf: "a1",
      endTime: at(20),
      integration: { state },
    },
    {
      id: "w1",
      code: "W1",
      status: f.TaskStatus.DONE,
      partOf: "a1",
      endTime: at(10),
      integration: { state: I.COMMITTED },
    },
    {
      id: "w5",
      code: "W5",
      status: f.TaskStatus.DONE,
      endTime: at(30),
      integration: { state },
    },
    {
      id: "w9",
      code: "W9",
      status: f.TaskStatus.PENDING,
      waitReason: "waits for W8 to be committed",
    },
  ];
  const merging = tasks(I.PENDING);
  assert.deepEqual(
    f.movingTasks(merging).map((x) => x.code),
    ["W5", "W9"],
  );
  assert.deepEqual(f.finishedTasks(merging), []);
  // Within its azima, W8 comes first among the moving parts, never behind "show the N finished".
  const [group] = f.azimaGroups(merging);
  assert.deepEqual(
    group.parts.map((x) => x.code),
    ["W8", "W1"],
  );
  const nest = f.mergeNest(merging);
  assert.deepEqual(
    group.parts.filter((x) => !f.settledTask(x, nest)).map((x) => x.code),
    ["W8"],
  );
  // The flying carpet does the same, and counts the work that merges as running.
  const lamp = wish("w1", "Ship the lamp", 1);
  const plan = f.flightPlan([lamp], { w1: detail(merging, []) });
  assert.deepEqual(
    plan.moving.map(({ item }) => item.code),
    ["W5", "W9"],
  );
  assert.deepEqual(
    plan.running.map(({ item }) => item.code),
    ["W8", "W5"],
  );
  // Committed, it folds with the finished, and runs no more.
  const committed = tasks(I.COMMITTED);
  assert.deepEqual(
    f.movingTasks(committed).map((x) => x.code),
    ["W9"],
  );
  assert.deepEqual(
    f.finishedTasks(committed).map((x) => x.code),
    ["W5"],
  );
  const after = f.flightPlan([lamp], { w1: detail(committed, []) });
  assert.deepEqual(after.running, []);
  assert.deepEqual(
    after.finished.map(({ item }) => item.code),
    ["W5"],
  );
});

test("work that merges counts as running: a worker at work, not a watcher", () => {
  const I = f.IntegrationState;
  const S = f.TaskStatus;
  const done = (state) => ({ status: S.DONE, integration: { state } });
  assert.equal(f.isTaskMerging(done(I.PENDING)), true);
  assert.equal(f.isTaskMerging(done(I.INTEGRATING)), true);
  for (const state of [I.COMMITTED, I.CONFLICT, I.RED, I.UNCOMMITTED])
    assert.equal(f.isTaskMerging(done(state)), false, `state ${state}`);
  // Only a done task merges: a running one runs already.
  assert.equal(
    f.isTaskMerging({ status: S.RUNNING, integration: { state: I.PENDING } }),
    false,
  );
  assert.deepEqual(
    f.runningOf([
      { status: S.RUNNING },
      { status: S.RUNNING, provider: f.Provider.WATCH },
      done(I.PENDING),
      done(I.INTEGRATING),
      done(I.COMMITTED),
      done(I.CONFLICT),
      { status: S.DONE },
    ]),
    {
      running: 1,
      investigating: 0,
      watching: 1,
      merging: 2,
      fixing: 0,
      ifrits: 0,
    },
  );
});

test("a failed merge is being fixed while its correction works, failed when none does", () => {
  const I = f.IntegrationState;
  const S = f.TaskStatus;
  // W1's merge failed; its correction is task c, in the state given.
  const failed = (by, extra = {}) => ({
    id: "t1",
    code: "W1",
    status: S.DONE,
    integration: { state: I.CONFLICT, correctedBy: by, ...extra },
  });
  const correction = (status, integration) => ({
    id: "c",
    code: "W9",
    status,
    integration,
  });
  const fixer = (task, c) => f.fixerOf(task, [task, c])?.code ?? "";
  for (const status of [S.PENDING, S.RUNNING, S.WAITING, S.PAUSED, S.RESUMING])
    assert.equal(
      fixer(failed("c"), correction(status)),
      "W9",
      `status ${status}`,
    );
  for (const status of [S.FAILED, S.STOPPED, S.CANCELLED, S.INTERRUPTED])
    assert.equal(
      fixer(failed("c"), correction(status)),
      "",
      `status ${status}`,
    );
  // Red checks too; not a merge that merges, nor one committed.
  assert.equal(
    fixer(
      { ...failed("c"), integration: { state: I.RED, correctedBy: "c" } },
      correction(S.RUNNING),
    ),
    "W9",
  );
  for (const state of [I.PENDING, I.INTEGRATING, I.COMMITTED, I.UNCOMMITTED])
    assert.equal(
      fixer(
        { ...failed("c"), integration: { state, correctedBy: "c" } },
        correction(S.RUNNING),
      ),
      "",
      `state ${state}`,
    );
  // No correction, one not among the tasks, attempts spent (Djinn asked the person).
  assert.equal(fixer(failed(""), correction(S.RUNNING)), "");
  assert.equal(fixer(failed("gone"), correction(S.RUNNING)), "");
  assert.equal(
    fixer(failed("c", { questionId: "q1" }), correction(S.RUNNING)),
    "",
  );
  // A correction done: its work merges, it fixes until it is in; its own merge failed, it fixes while its own
  // correction does; with no work recorded, Djinn drops it.
  assert.equal(
    fixer(failed("c"), correction(S.DONE, { state: I.PENDING })),
    "W9",
  );
  assert.equal(fixer(failed("c"), correction(S.DONE, { state: I.RED })), "");
  assert.equal(fixer(failed("c"), correction(S.DONE)), "");
  const own = { id: "c2", code: "W10", status: S.RUNNING };
  const red = correction(S.DONE, { state: I.RED, correctedBy: "c2" });
  assert.equal(f.fixerOf(failed("c"), [failed("c"), red, own])?.code, "W9");
  // Corrections that point at each other fix nothing.
  const loop = correction(S.DONE, { state: I.RED, correctedBy: "c" });
  assert.equal(fixer(failed("c"), loop), "");

  // The badge: orange, its words name the correction; red without one.
  assert.equal(f.taskTone(failed("c"), "", "W9"), "fixing");
  assert.equal(
    f.taskStatusText(failed("c"), "", "W9"),
    "Merge being fixed by W9",
  );
  assert.equal(f.taskTone(failed("c")), "failed");
  assert.equal(f.taskStatusText(failed("c")), "Merge failed");
  // A task that is not done keeps its state whatever its integration says.
  assert.equal(
    f.taskTone(
      { status: S.FAILED, integration: { state: I.CONFLICT } },
      "",
      "W9",
    ),
    "failed",
  );

  // The counts: fixing apart from merging, the correction running with the running.
  assert.deepEqual(
    f.runningOf([
      failed("c"),
      correction(S.RUNNING),
      { id: "t2", status: S.DONE, integration: { state: I.RED } },
    ]),
    {
      running: 1,
      investigating: 0,
      watching: 0,
      merging: 0,
      fixing: 1,
      ifrits: 0,
    },
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
  // Nothing waits for the user: Djinn resumes what it can, and W4, cut short for good, is history.
  assert.deepEqual(
    plan.waiting.map((w) => w.item.code),
    [],
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

test("a cancelled task has its own muted tone, never done's, and a compact note on hover", () => {
  const S = f.TaskStatus;
  const cancelled = (actor, note) => ({
    status: S.CANCELLED,
    cancelled: { actor, note, createTime: at(100) },
  });
  const task = cancelled(f.Closer.LEAD, " superseded by W7 ");
  assert.equal(f.taskTone(task), "cancelled");
  assert.notEqual(f.taskTone(task), f.taskTone({ status: S.DONE }));
  assert.equal(f.taskStatusText(task), "Cancelled");
  assert.equal(f.cancelledText(task), "By the Djinn: superseded by W7");
  assert.equal(f.cancelledText(cancelled(f.Closer.DEVELOPER, "")), "By you");
  assert.equal(f.cancelledText({ status: S.DONE }), undefined);
  // It is history: finished, dealt with, never the person's move.
  assert.ok(f.taskFinished(S.CANCELLED));
  assert.ok(f.finishedTask(task));
  assert.deepEqual(
    f.waitingTasks({ id: "w" }, { tasks: [task], questions: [] }),
    [],
  );
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

// An investigator, the worker Enlighten me starts on a question (TaskRole.INVESTIGATOR), is violet as the button that
// started it while it works, and counted apart from the workers that run, blue; ended, it is a task like any other.
test("an investigator at work has its own tone and count, apart from the running workers", () => {
  const S = f.TaskStatus;
  const R = f.TaskRole;
  const investigator = (status) => ({
    status,
    role: R.INVESTIGATOR,
    question: "Q3",
  });
  assert.equal(f.taskTone(investigator(S.RUNNING)), "investigating");
  assert.equal(f.taskStatusText(investigator(S.RUNNING)), "Investigating");
  assert.equal(f.taskTone(investigator(S.RESUMING)), "investigating");
  assert.equal(
    f.taskTone({ ...investigator(S.RESUMING), resumeAfter: at(100) }),
    "paused",
  );
  assert.equal(f.taskTone(investigator(S.DONE)), "done");
  assert.equal(f.taskStatusText(investigator(S.DONE)), "Done");
  assert.equal(
    f.taskTone({ status: S.RUNNING, role: R.UNSPECIFIED }),
    "running",
  );
  assert.deepEqual(
    f.runningOf([
      { status: S.RUNNING },
      investigator(S.RUNNING),
      investigator(S.RUNNING),
      investigator(S.DONE),
      { status: S.RUNNING, provider: f.Provider.WATCH },
    ]),
    {
      running: 1,
      investigating: 2,
      watching: 1,
      merging: 0,
      fixing: 0,
      ifrits: 0,
    },
  );
});

test("a done task derives its badge text and tone from its integration state", () => {
  const S = f.TaskStatus;
  const I = f.IntegrationState;
  const taskWith = (integration) => ({
    status: S.DONE,
    integration: integration ? { state: integration } : undefined,
  });

  // PENDING or INTEGRATING -> Merging, running tone
  assert.equal(f.taskStatusText(taskWith(I.PENDING)), "Merging");
  assert.equal(f.taskTone(taskWith(I.PENDING)), "running");
  assert.equal(f.taskStatusText(taskWith(I.INTEGRATING)), "Merging");
  assert.equal(f.taskTone(taskWith(I.INTEGRATING)), "running");

  // CONFLICT or RED -> Merge failed, failed tone
  assert.equal(f.taskStatusText(taskWith(I.CONFLICT)), "Merge failed");
  assert.equal(f.taskTone(taskWith(I.CONFLICT)), "failed");
  assert.equal(f.taskStatusText(taskWith(I.RED)), "Merge failed");
  assert.equal(f.taskTone(taskWith(I.RED)), "failed");

  // UNCOMMITTED -> To review, waiting tone
  assert.equal(f.taskStatusText(taskWith(I.UNCOMMITTED)), "To review");
  assert.equal(f.taskTone(taskWith(I.UNCOMMITTED)), "waiting");

  // COMMITTED or no integration -> Done, done tone
  assert.equal(f.taskStatusText(taskWith(I.COMMITTED)), "Done");
  assert.equal(f.taskTone(taskWith(I.COMMITTED)), "done");
  assert.equal(f.taskStatusText(taskWith(undefined)), "Done");
  assert.equal(f.taskTone(taskWith(undefined)), "done");

  // Non-done tasks keep their status text/tone regardless of integration
  assert.equal(
    f.taskStatusText({ status: S.RUNNING, integration: { state: I.PENDING } }),
    "Running",
  );
  assert.equal(
    f.taskTone({ status: S.RUNNING, integration: { state: I.PENDING } }),
    "running",
  );
  assert.equal(
    f.taskStatusText({ status: S.FAILED, integration: { state: I.CONFLICT } }),
    "Failed",
  );
  assert.equal(
    f.taskTone({ status: S.FAILED, integration: { state: I.CONFLICT } }),
    "failed",
  );
});

test("a wish deleted says which worktrees stay, and why", () => {
  assert.equal(f.deletedText("Oil", []), "“Oil” deleted.");
  assert.equal(
    f.deletedText("Oil", [
      {
        taskCode: "W2",
        branch: "w2-work",
        base: "main",
        commits: 2,
        changed: true,
        error: "",
      },
      {
        taskCode: "W3",
        branch: "w3-work",
        base: "main",
        commits: 0,
        changed: false,
        error: "git worktree: locked",
      },
    ]),
    "“Oil” deleted. W2 keeps its worktree, on w2-work: 2 commits beyond main, changes not committed. " +
      "W3 keeps its worktree, on w3-work: git worktree: locked.",
  );
});

test("a task says how long it runs as a counter, two units at most", () => {
  assert.equal(f.span(45_000), "45s");
  assert.equal(f.span(61_000), "1m1s");
  assert.equal(f.span(3_660_000), "1h1m");
  assert.equal(f.span((27 * 3600 + 5 * 60) * 1000), "1d3h");
  const S = f.TaskStatus;
  // Running: from its start until now; ended: how long it ran; not started: nothing.
  const now = 1_000_000 * 1000;
  assert.equal(
    f.taskTime({ status: S.RUNNING, startTime: at(1_000_000 - 3_661) }, now)
      .text,
    "1 run · 1h1m running",
  );
  assert.equal(
    f.taskTime({ status: S.DONE, startTime: at(100), endTime: at(161) }, now)
      .text,
    "1 run · 1m1s running",
  );
  assert.equal(f.taskTime({ status: S.PENDING }, now).text, "");

  // Multiple runs, pause, and wait before start in tooltip and card line
  const multiRunTask = {
    status: S.DONE,
    firstStartTime: at(1000),
    startTime: at(2440),
    endTime: at(3340),
    runs: [
      { startTime: at(1000), endTime: at(1720), reason: "pause" },
      { startTime: at(1960), endTime: at(2440), reason: "pause" },
      { startTime: at(2440), endTime: at(3340), reason: "done" },
    ],
    waitingBeforeStart: { seconds: 540n, nanos: 0 },
  };
  const multiTime = f.taskTime(multiRunTask, now);
  assert.equal(multiTime.text, "3 runs · 35m running · 4m paused");
  assert.equal(
    multiTime.title,
    "3 runs: 12m, 8m, 15m; paused 4m; waited 9m for a slot",
  );

  // Counter frozen when paused
  const pausedTask = {
    status: S.PAUSED,
    firstStartTime: at(1000),
    startTime: at(1000),
    endTime: at(1720),
    runs: [{ startTime: at(1000), endTime: at(1720), reason: "pause" }],
    paused: { seconds: 240n, nanos: 0 },
  };
  const pausedAtT1 = f.taskTime(pausedTask, 2000 * 1000);
  const pausedAtT2 = f.taskTime(pausedTask, 5000 * 1000);
  assert.equal(pausedAtT1.text, "1 run · 12m running · 4m paused");
  assert.equal(pausedAtT2.text, pausedAtT1.text);

  // Waiting gate is included on card when non-zero
  const gatedTask = {
    status: S.DONE,
    firstStartTime: at(1000),
    startTime: at(1000),
    endTime: at(2000),
    runs: [{ startTime: at(1000), endTime: at(2000), reason: "done" }],
    waitingGate: { seconds: 120n, nanos: 0 },
  };
  const gatedTime = f.taskTime(gatedTask, now);
  assert.equal(gatedTime.text, "1 run · 14m40s running · 2m waiting gate");
});

test("the azimas' progress counts the azimas done, those awaiting their proof apart, and not the work", () => {
  const S = f.TaskStatus;
  const tasks = [
    { id: "a", kind: f.TaskKind.AZIMA, azima: { state: f.AzimaState.DONE } },
    {
      id: "b",
      kind: f.TaskKind.AZIMA,
      azima: { state: f.AzimaState.IN_PROGRESS },
    },
    {
      id: "c",
      kind: f.TaskKind.AZIMA,
      status: S.DONE,
      azima: { state: f.AzimaState.DONE },
    },
    {
      id: "d",
      kind: f.TaskKind.AZIMA,
      azima: { state: f.AzimaState.AWAITING_PROOF },
    },
    { id: "w1", status: S.DONE, partOf: "b" },
    { id: "w2", status: S.RUNNING, partOf: "b" },
  ];
  assert.deepEqual(f.azimasDone(tasks), { done: 2, proof: 1, count: 4 });
  assert.equal(f.workCount(tasks), 2);

  // Status DONE never overrides computed state IN_PROGRESS.
  const runningAzima = [
    {
      id: "e",
      kind: f.TaskKind.AZIMA,
      status: S.DONE,
      azima: { state: f.AzimaState.IN_PROGRESS },
    },
  ];
  assert.deepEqual(f.azimasDone(runningAzima), { done: 0, proof: 0, count: 1 });

  // An azima whose task merges or whose merge failed is in progress (computed by the server).
  const mergingAzimas = [
    {
      id: "a1",
      kind: f.TaskKind.AZIMA,
      azima: { state: f.AzimaState.IN_PROGRESS },
    },
    {
      id: "w3",
      status: S.DONE,
      partOf: "a1",
      integration: { state: f.IntegrationState.PENDING },
    },
    {
      id: "a2",
      kind: f.TaskKind.AZIMA,
      azima: { state: f.AzimaState.IN_PROGRESS },
    },
    {
      id: "w4",
      status: S.DONE,
      partOf: "a2",
      integration: { state: f.IntegrationState.CONFLICT },
    },
    {
      id: "a3",
      kind: f.TaskKind.AZIMA,
      azima: { state: f.AzimaState.DONE },
    },
    {
      id: "w5",
      status: S.DONE,
      partOf: "a3",
      integration: { state: f.IntegrationState.COMMITTED },
    },
    {
      id: "a4",
      kind: f.TaskKind.AZIMA,
      azima: { state: f.AzimaState.IN_PROGRESS },
    },
    {
      id: "w6",
      status: S.DONE,
      partOf: "a4",
      integration: { state: f.IntegrationState.INTEGRATING },
    },
  ];
  assert.deepEqual(f.azimasDone(mergingAzimas), {
    done: 1,
    proof: 0,
    count: 4,
  });

  // Azima counters use server Task.azima fields, right before any azima is opened (e.g. paginated wish >50 tasks).
  const paginatedWishTasks = [
    {
      id: "az1",
      kind: f.TaskKind.AZIMA,
      azima: {
        state: f.AzimaState.IN_PROGRESS,
        parts: 200,
        partsDone: 150,
        partsRunning: 2,
      },
    },
    {
      id: "az2",
      kind: f.TaskKind.AZIMA,
      azima: {
        state: f.AzimaState.DONE,
        parts: 100,
        partsDone: 100,
        partsRunning: 0,
      },
    },
    {
      id: "az3",
      kind: f.TaskKind.AZIMA,
      azima: {
        state: f.AzimaState.AWAITING_PROOF,
        parts: 130,
        partsDone: 130,
        partsRunning: 0,
      },
    },
    // Only a small page of parts is loaded into the store (e.g. page 1 of 50 tasks)
    { id: "p1", status: S.DONE, partOf: "az1" },
    { id: "p2", status: S.RUNNING, partOf: "az1" },
  ];
  assert.deepEqual(f.azimasDone(paginatedWishTasks), {
    done: 1,
    proof: 1,
    count: 3,
  });
  assert.equal(f.workCount(paginatedWishTasks), 430);
  assert.equal(f.workDoneCount(paginatedWishTasks), 380);

  // A cancelled task is no work left, part of an azima or not: out of the count, never done.
  const cancelled = [
    {
      id: "c1",
      kind: f.TaskKind.AZIMA,
      azima: { state: f.AzimaState.AWAITING_PROOF, parts: 1, partsDone: 1 },
    },
    { id: "x1", status: S.DONE, partOf: "c1" },
    { id: "x2", status: S.CANCELLED, partOf: "c1" },
    { id: "x3", status: S.DONE },
    { id: "x4", status: S.CANCELLED },
  ];
  assert.equal(f.workCount(cancelled), 2);
  assert.equal(f.workDoneCount(cancelled), 2);
  // Without the server's counts, the same.
  const local = cancelled.map((x) => ({ ...x, azima: undefined }));
  assert.equal(f.workCount(local), 2);
  assert.equal(f.workDoneCount(local), 2);
});

// Nothing moves under the reader (AGENTS.md, Interface): a question asked later comes below, whatever it holds up.
test("open questions come as they were asked, by wish, whatever they hold up; each keeps its urgency", () => {
  const lamp = wish("w1", "Ship the lamp", 1);
  const oil = wish("w2", "Find the oil", 2);
  const plan = f.flightPlan([lamp, oil], {
    w1: detail(
      [],
      [
        { id: "q1", code: "Q01", text: "Brass?", createTime: at(10) },
        {
          id: "q2",
          code: "Q02",
          text: "Wick?",
          before: "before the merge",
          createTime: at(20),
        },
        {
          id: "q6",
          code: "Q03",
          text: "Push tag?",
          move: true,
          before: "before the release",
          createTime: at(30),
        },
        { id: "q7", code: "Q04", text: "Install build?", move: true },
      ],
    ),
    w2: detail(
      [
        {
          id: "t1",
          code: "W1",
          status: f.TaskStatus.WAITING,
          editQuestionId: "q5",
        },
      ],
      [
        { id: "q3", code: "Q01", text: "Olive?", createTime: at(5) },
        {
          id: "q4",
          code: "Q02",
          text: "Glass?",
          before: "before the demo",
          createTime: at(15),
        },
        {
          id: "q5",
          code: "Q03",
          text: "May W1 edit?",
          before: "before the demo",
          createTime: at(25),
        },
      ],
    ),
  });
  // The first wish's, then the second's, each the oldest first: one with no time comes first, by its id.
  assert.deepEqual(
    plan.questions.map((q) => [q.item.id, f.urgency(q)]),
    [
      ["q7", 3],
      ["q1", 3],
      ["q2", 2],
      ["q6", 1],
      ["q3", 3],
      ["q4", 2],
      ["q5", 0],
    ],
  );
  // A revision keeps the question's creation time: it stays where it was, even listed after a newer one (the store
  // put it back in place of its old copy).
  const revised = {
    id: "q1",
    code: "Q01",
    text: "Brass or copper?",
    revision: 1,
    createTime: at(10),
    rounds: [{ kind: f.RoundKind.REVISE, createTime: at(40) }],
  };
  assert.deepEqual(
    f
      .openQuestions(
        lamp,
        detail(
          [],
          [
            { id: "q2", code: "Q02", text: "Wick?", createTime: at(20) },
            revised,
            { id: "q8", code: "Q05", text: "Oil?", createTime: at(50) },
          ],
        ),
      )
      .map((q) => q.item.code),
    ["Q01", "Q02", "Q05"],
  );
});

// An ifrit (T51): a worker that stays lit while the developer is there. Its tone and word never rest on the colour
// alone: lit, at work, going out in 4 min once warned, out; it counts apart from the workers, and turns its wish's tone
// before what waits.
test("an ifrit lit, at work, warned or out: its tone, its word, and its count apart from the workers", () => {
  const S = f.TaskStatus;
  const I = f.IfritState;
  const now = Date.UTC(2026, 9, 16, 15, 0, 0);
  const ms = (t) => ({ seconds: BigInt(Math.floor(t / 1000)), nanos: 0 });
  const ifrit = (status, state, extra = {}) => ({
    id: "i1",
    code: "W12",
    title: "Front of the lamp",
    kind: f.TaskKind.IFRIT,
    status,
    ifrit: { state, serves: [], reply: "", ...extra },
  });
  const working = ifrit(S.RUNNING, I.WORKING);
  const lit = ifrit(S.RUNNING, I.LISTENING, { outTime: ms(now + 30 * 60e3) });
  const warned = ifrit(S.RUNNING, I.LISTENING, {
    warnTime: ms(now - 60e3),
    outTime: ms(now + 3.5 * 60e3),
  });
  const out = ifrit(S.STOPPED, I.OUT);
  const done = ifrit(S.DONE, I.UNSPECIFIED);
  assert.deepEqual(
    [working, lit, warned, out, done].map((t) => f.taskTone(t)),
    ["ifrit", "ifrit", "ifrit", "out", "done"],
  );
  assert.deepEqual(
    [working, lit, warned, out, done].map((t) => f.ifritPhase(t, now)),
    ["working", "listening", "warned", "out", undefined],
  );
  assert.deepEqual(
    [working, lit, warned, out].map((t) => f.ifritWord(t, now)),
    ["At work", "Lit", "Goes out in 4 min", "Out"],
  );
  // Before its warning time, no countdown; a row that does not tick says where it stands, never a countdown.
  assert.equal(f.ifritWord(warned, now - 2 * 60e3), "Lit", "not warned yet");
  assert.equal(f.taskStatusText(warned), "Lit");
  assert.equal(f.taskStatusText(out), "Out");
  assert.equal(f.taskStatusText(done), "Done");
  // A work task is no ifrit, whatever its fields.
  assert.equal(f.taskTone({ status: S.RUNNING, ifrit: lit.ifrit }), "running");

  // Counted apart: the workers running are the work alone; one put out counts nowhere.
  assert.deepEqual(f.runningOf([working, lit, out, { status: S.RUNNING }]), {
    running: 1,
    investigating: 0,
    watching: 0,
    merging: 0,
    fixing: 0,
    ifrits: 2,
  });
  // Its wish takes its tone before what waits, after a paused or granted wish's.
  const w = { state: f.WishState.ACTIVE, ready: true };
  assert.equal(f.wishTone(w, 2, 1, 0, 0, 1), "ifrit");
  assert.equal(f.wishTone(w, 2, 1, 0, 0, 0), "waiting");
  assert.equal(
    f.wishTone({ ...w, state: f.WishState.PAUSED }, 0, 0, 0, 0, 1),
    "paused",
  );
});

test("an ifrit's ports are links, its gates those it holds, and a task waiting for one is told when it comes back", () => {
  const R = f.ServeState;
  const ms = (t) => ({ seconds: BigInt(Math.floor(t / 1000)), nanos: 0 });
  const ifrit = {
    id: "i1",
    code: "W12",
    title: "Front of the lamp",
    kind: f.TaskKind.IFRIT,
    status: f.TaskStatus.RUNNING,
    ifrit: {
      state: f.IfritState.LISTENING,
      serves: [
        {
          name: "front",
          state: R.RUNNING,
          gate: "",
          ports: [{ number: 5173, label: "", open: true }],
        },
        {
          name: "back",
          state: R.STARTING,
          gate: "Stack",
          ports: [{ number: 8080, label: "api", open: false }],
        },
        {
          name: "old",
          state: R.STOPPED,
          gate: "db",
          ports: [{ number: 9000, label: "", open: true }],
        },
      ],
    },
  };
  // The ports of the serves that run, named as declared, else by their serve; one that does not answer says so.
  assert.deepEqual(f.ifritLinks(ifrit), [
    {
      url: "http://localhost:5173/",
      host: "localhost:5173",
      label: "front",
      open: true,
    },
    {
      url: "http://localhost:8080/",
      host: "localhost:8080",
      label: "api",
      open: false,
    },
  ]);
  const release = Date.UTC(2026, 9, 16, 15, 42);
  const gates = [
    {
      name: "stack",
      holder: "W12 (Front of the lamp): task run-back",
      holderTaskId: "i1",
      ifrit: true,
      waiting: ["W15 (Tests): go tool task test"],
      expireTime: ms(release),
    },
    {
      name: "lint",
      holder: "W3 (Lint)",
      holderTaskId: "t3",
      ifrit: false,
      waiting: ["W16 (Other): go tool task lint"],
    },
    {
      name: "e2e",
      holder: "W12 (Front of the lamp): npx playwright test",
      holderTaskId: "i1",
      ifrit: true,
      waiting: [],
    },
  ];
  // The gates Djinn lists as its own, and those its running serves hold, each once.
  assert.deepEqual(f.ifritGates(ifrit, gates), ["e2e", "stack"]);
  const wait = f.ifritWaitOf({ code: "W15", title: "Tests" }, gates);
  assert.equal(wait.gate, "stack");
  assert.equal(wait.holder, "W12 (Front of the lamp): task run-back");
  assert.equal(
    wait.release,
    new Date(release).toLocaleTimeString("en", {
      hour: "2-digit",
      minute: "2-digit",
    }),
  );
  // A gate held by a worker that is no ifrit, or a task that waits for nothing: no line.
  assert.equal(
    f.ifritWaitOf({ code: "W16", title: "Other" }, gates),
    undefined,
  );
  assert.equal(f.ifritWaitOf({ code: "W1", title: "Tests" }, gates), undefined);

  // The lit ifrits, the latest activity first; one put out is not lit.
  const at = (id, seconds, extra = {}) => ({
    item: {
      ...ifrit,
      id,
      lastActivityTime: { seconds: BigInt(seconds), nanos: 0 },
      ...extra,
    },
  });
  assert.deepEqual(
    f
      .litIfrits([
        at("a", 10),
        at("b", 20),
        at("c", 30, {
          status: f.TaskStatus.STOPPED,
          ifrit: { state: f.IfritState.OUT, serves: [] },
        }),
        at("d", 40, { kind: f.TaskKind.WORK }),
      ])
      .map(({ item }) => item.id),
    ["b", "a"],
  );
});

test("a question put off for later goes below those asked before, above one asked after; cleared, back in place", () => {
  const lamp = wish("w1", "Ship the lamp", 1);
  const asked = [
    { id: "q1", code: "Q01", text: "Brass?", createTime: at(10) },
    { id: "q2", code: "Q02", text: "Wick?", createTime: at(20) },
    { id: "q3", code: "Q03", text: "Glass?", createTime: at(30) },
    { id: "q4", code: "Q04", text: "Oil?", createTime: at(50) },
  ];
  const codes = (questions) =>
    f.openQuestions(lamp, detail([], questions)).map((q) => q.item.code);
  assert.deepEqual(codes(asked), ["Q01", "Q02", "Q03", "Q04"]);
  // Q01 put off at 40: below Q02 and Q03, asked before, above Q04, asked after.
  const putOff = [{ ...asked[0], laterTime: at(40) }, ...asked.slice(1)];
  assert.deepEqual(codes(putOff), ["Q02", "Q03", "Q01", "Q04"]);
  assert.equal(f.placeOf(putOff[0]), 40_000);
  // Its date taken off, it is back where it was asked; a date before that changes nothing.
  assert.deepEqual(
    codes([{ ...asked[0], laterTime: undefined }, ...asked.slice(1)]),
    ["Q01", "Q02", "Q03", "Q04"],
  );
  assert.equal(f.placeOf({ ...asked[2], laterTime: at(1) }), 30_000);
});

test("an urgent task shows so while it is to run or runs, never a watcher nor an azima (W133)", () => {
  const task = (status, extra = {}) =>
    f.create(f.TaskSchema, { code: "W2", status, urgent: true, ...extra });
  assert.equal(f.isUrgent(task(f.TaskStatus.PENDING)), true);
  assert.equal(f.isUrgent(task(f.TaskStatus.RUNNING)), true);
  assert.equal(f.isUrgent(task(f.TaskStatus.DONE)), false);
  assert.equal(
    f.isUrgent(task(f.TaskStatus.PENDING, { urgent: false })),
    false,
  );
  assert.equal(
    f.isUrgent(task(f.TaskStatus.PENDING, { provider: f.Provider.WATCH })),
    false,
  );
  assert.equal(
    f.isUrgent(task(f.TaskStatus.PENDING, { kind: f.TaskKind.AZIMA })),
    false,
  );
});

test("a question asleep waits for its conditions, out of the open ones; woken, it comes first, the first woken first", () => {
  const lamp = wish("w1", "Ship the lamp", 1);
  const sleep = (wakeTime) => ({
    conditions: [{ kind: f.SleepKind.TASK, code: "W1", taskId: "t1", url: "" }],
    createTime: at(5),
    ...(wakeTime && { wakeTime }),
  });
  const asked = [
    { id: "q1", code: "Q01", text: "Brass?", createTime: at(10) },
    {
      id: "q2",
      code: "Q02",
      text: "Wick?",
      createTime: at(20),
      sleep: sleep(),
    },
    {
      id: "q3",
      code: "Q03",
      text: "Glass?",
      createTime: at(30),
      sleep: sleep(at(60)),
    },
    {
      id: "q4",
      code: "Q04",
      text: "Oil?",
      createTime: at(40),
      sleep: sleep(at(50)),
    },
    // Put off after it woke: back among the others, put off.
    {
      id: "q5",
      code: "Q05",
      text: "Lid?",
      createTime: at(1),
      sleep: sleep(at(50)),
      laterTime: at(70),
    },
  ];
  const d = detail([], asked);
  assert.deepEqual(
    f.openQuestions(lamp, d).map((q) => q.item.code),
    ["Q04", "Q03", "Q01", "Q05"],
  );
  assert.deepEqual(
    f.asleepQuestions(lamp, d).map((q) => q.item.code),
    ["Q02"],
  );
  const plan = f.flightPlan([lamp], { w1: d });
  assert.deepEqual(
    plan.asleep.map((q) => q.item.code),
    ["Q02"],
  );
  assert.ok(!plan.questions.some((q) => q.item.code === "Q02"));
  // Answered, it waits for nothing.
  assert.deepEqual(
    f.asleepQuestions(
      lamp,
      detail([], [{ ...asked[1], answer: { choice: f.Choice.YES } }]),
    ),
    [],
  );
});

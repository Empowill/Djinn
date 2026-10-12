// The store of the page against in-memory plan services. Run with `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { bundle } from "./bundle.mjs";

const {
  createStore,
  createClients,
  createRouterTransport,
  Change,
  WishService,
  ProjectService,
  TaskService,
  QuestionService,
  BlockService,
  InboxService,
  LeadService,
  TaskKind,
  TaskStatus,
  AzimaState,
  azimaGroups,
  loose,
  azimasDone,
  workCount,
  workDoneCount,
} = await bundle(
  "store",
  `export { createStore } from "@/src/data/store.ts";
export { createClients } from "@/src/data/client.ts";
export { createRouterTransport } from "@connectrpc/connect";
export { azimaGroups, loose, azimasDone, workCount, workDoneCount } from "@/src/data/flight.ts";
export * from "@/gen/ts/plan/v1/plan_pb.ts";`,
);

const wishId = "01a11833-a440-7479-a067-52615c91da71";
const taskId = "01a11876-6480-7ec2-9833-abd058ae4a59";

// A channel the test pushes watch messages into; ends a stream with an error when told.
function channel() {
  const queue = [];
  let wake;
  return {
    push(msg) {
      queue.push(msg);
      wake?.();
    },
    async *stream() {
      for (;;) {
        while (queue.length) {
          const msg = queue.shift();
          if (msg instanceof Error) throw msg;
          yield msg;
        }
        await new Promise((r) => (wake = r));
      }
    },
  };
}

// server answers from data, counts every read by method, and streams watch messages from a channel.
function server(data) {
  const reads = {};
  const count = (name) => (reads[name] = (reads[name] ?? 0) + 1);
  const watch = channel();
  const transport = createRouterTransport(({ service }) => {
    service(WishService, {
      list: () => (count("wishes"), { wishes: data.wishes }),
      watch: () => (count("watch"), watch.stream()),
    });
    service(ProjectService, {
      list: () => (count("projects"), { projects: data.projects }),
    });
    service(LeadService, {
      get: () => ({ lead: data.lead }),
    });
    service(TaskService, {
      list: (req) => {
        count("tasks");
        if (data.listTasks) return data.listTasks(req);
        return {
          tasks: data.tasks,
          total: data.tasksTotal ?? data.tasks.length,
          nextPageToken: data.tasksNextToken ?? "",
        };
      },
      get: (req) => {
        count("taskGet");
        if (data.getTask) return data.getTask(req);
        const task = data.tasks?.find((t) => t.id === req.taskId);
        return { task };
      },
      watch: async function* (req) {
        count("events");
        for (const event of data.events.filter((e) => e.seq > req.afterSeq))
          yield { event };
        // A running task's watch stays open.
        if (data.running) await new Promise(() => {});
      },
    });
    service(QuestionService, {
      list: (req) => {
        count("questions");
        if (data.listQuestions) return data.listQuestions(req);
        return {
          questions: data.questions,
          total: data.questionsTotal ?? data.questions.length,
          nextPageToken: data.questionsNextToken ?? "",
        };
      },
    });
    service(BlockService, {
      list: (req) => {
        count("blocks");
        if (data.listBlocks) return data.listBlocks(req);
        return {
          blocks: data.blocks,
          total: data.blocksTotal ?? data.blocks.length,
          nextPageToken: data.blocksNextToken ?? "",
        };
      },
    });
    service(InboxService, {
      list: () => (count("inbox"), { items: data.inbox }),
      sources: () => (count("sources"), { sources: data.sources }),
    });
  });
  return { clients: createClients(transport), reads, watch };
}

function sample() {
  return {
    wishes: [{ id: wishId, title: "Ship the lamp", rank: 1, state: 1 }],
    projects: [{ id: "p1", name: "lamp" }],
    tasks: [
      {
        id: taskId,
        wishId,
        code: "T01",
        title: "Polish",
        status: 2,
        prompt: "Polish the brass with care",
      },
    ],
    questions: [{ id: "q1", wishId, code: "Q01", text: "Oil?" }],
    blocks: [{ id: "b1", wishId, title: "Lexicon", content: "A wick." }],
    inbox: [{ id: "i1", source: "babysit-mr", text: "Babysit !12" }],
    sources: [{ name: "lamp/babysit-mr", skill: "babysit-mr", plugged: true }],
    events: [
      { id: "e1", taskId, seq: 1n, kind: 1, text: "Polish the brass" },
      { id: "e2", taskId, seq: 2n, kind: 2, text: "Done." },
    ],
  };
}

// until waits for the store's state to pass check.
function until(store, check) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(
      () => reject(new Error("the state never came")),
      2000,
    );
    const look = () => {
      if (!check(store.getState())) return;
      clearTimeout(timer);
      stop();
      resolve(store.getState());
    };
    const stop = store.subscribe(look);
    look();
  });
}

const everything = [
  Change.WISH,
  Change.TASK,
  Change.QUESTION,
  Change.BLOCK,
  Change.PROJECT,
];

test("the first message of the watch reads the wishes and the projects", async () => {
  const { clients, reads, watch } = server(sample());
  const store = createStore(clients, 10);
  const stop = store.start();
  watch.push({ wishId: "", changes: everything });
  const state = await until(store, (s) => s.loaded);
  stop();
  assert.equal(state.live, true);
  assert.equal(state.wishes[0].title, "Ship the lamp");
  assert.equal(state.projects[0].name, "lamp");
  // No wish is shown: none of their tasks is read.
  assert.equal(reads.tasks, undefined);
});

test("a wish shown is read, and only what changed is read again", async () => {
  const data = sample();
  const { clients, reads, watch } = server(data);
  const store = createStore(clients, 10);
  const stop = store.start();
  watch.push({ wishId: "", changes: everything });
  await until(store, (s) => s.loaded);
  const close = store.open(wishId);
  const state = await until(store, (s) => s.details[wishId]?.loaded);
  assert.equal(state.details[wishId].questions[0].text, "Oil?");
  assert.equal(state.details[wishId].blocks[0].content, "A wick.");
  const before = { ...reads };

  data.questions = [{ ...data.questions[0], answer: { choice: 1, note: "" } }];
  watch.push({ wishId, changes: [Change.WISH, Change.QUESTION] });
  await until(
    store,
    (s) => s.details[wishId].questions[0].answer !== undefined,
  );
  assert.equal(reads.questions, before.questions + 1);
  assert.equal(reads.wishes, before.wishes + 1);
  assert.equal(reads.tasks, before.tasks, "tasks did not change");
  assert.equal(reads.blocks, before.blocks, "blocks did not change");

  // A wish no longer shown is not read again.
  close();
  watch.push({ wishId, changes: [Change.BLOCK] });
  watch.push({ wishId: "", changes: [Change.PROJECT] });
  await until(store, () => reads.projects === before.projects + 1);
  assert.equal(reads.blocks, before.blocks);
  stop();
});

test("a broken watch reconnects and reads everything again", async () => {
  const { clients, reads, watch } = server(sample());
  const store = createStore(clients, 10);
  const stop = store.start();
  watch.push({ wishId: "", changes: everything });
  await until(store, (s) => s.loaded);
  watch.push(new Error("djinn restarted"));
  await until(store, (s) => !s.live);
  watch.push({ wishId: "", changes: everything });
  await until(store, (s) => s.live && reads.wishes === 2);
  assert.equal(reads.watch, 2);
  stop();
});

test("a task's events come in order, and a second follow reads only what is new", async () => {
  const data = sample();
  const { clients, reads } = server(data);
  const store = createStore(clients, 10);
  const release = store.follow(taskId);
  let state = await until(store, (s) => s.events[taskId]?.length === 2);
  assert.deepEqual(
    state.events[taskId].map((e) => e.text),
    ["Polish the brass", "Done."],
  );
  release();
  data.events.push({ id: "e3", taskId, seq: 3n, kind: 6, text: "ended" });
  store.follow(taskId);
  state = await until(store, (s) => s.events[taskId]?.length === 3);
  assert.equal(state.events[taskId][2].text, "ended");
  assert.equal(reads.events, 2);
});

// task-pause.spec.ts saw "resumed" twice: the new status followed again while the old follow still held the event.
test("a task followed again as its status changes keeps each event once", async () => {
  const data = { ...sample(), running: true };
  const { clients, reads } = server(data);
  const store = createStore(clients, 10);
  const release = store.follow(taskId);
  // Less than the 50 ms the events wait for the screen.
  await new Promise((r) => setTimeout(r, 10));
  release();
  const again = store.follow(taskId);
  await until(store, (s) => s.events[taskId]?.length >= 2);
  await new Promise((r) => setTimeout(r, 100));
  assert.deepEqual(
    store.getState().events[taskId].map((e) => e.seq),
    [1n, 2n],
  );
  assert.equal(reads.events, 2);
  again();
});

test("reading a task's prompt gets the task alone without following its events", async () => {
  const data = sample();
  const { clients, reads } = server(data);
  const store = createStore(clients, 10);
  const prompt = await store.readPrompt(taskId);
  assert.equal(prompt, "Polish the brass with care");
  assert.equal(reads.taskGet, 1);
  assert.equal(reads.events, undefined);
  // A second read takes the prompt from the store's cache.
  const cached = await store.readPrompt(taskId);
  assert.equal(cached, "Polish the brass with care");
  assert.equal(reads.taskGet, 1);
});

test("opening a wish does not follow its tasks' events until follow is requested", async () => {
  const data = sample();
  const { clients, reads } = server(data);
  const store = createStore(clients, 10);
  store.open(wishId);
  await until(store, (s) => s.details[wishId]?.tasks.length === 1);
  assert.equal(reads.events, undefined);

  // Following the task starts reading its events.
  const release = store.follow(taskId);
  await until(store, (s) => s.events[taskId]?.length === 2);
  assert.equal(reads.events, 1);
  assert.equal(store.getState().prompts[taskId], "Polish the brass");
  release();
});

test("a write the page made is read at once", async () => {
  const data = sample();
  const { clients, reads } = server(data);
  const store = createStore(clients, 10);
  store.open(wishId);
  await until(store, (s) => s.details[wishId]?.loaded);
  data.blocks = [];
  await store.changed(wishId, [Change.BLOCK]);
  assert.deepEqual(store.getState().details[wishId].blocks, []);
  assert.equal(reads.blocks, 2);
});

test("a change of the inbox reads it and its sources again, and nothing else", async () => {
  const data = sample();
  const { clients, reads, watch } = server(data);
  const store = createStore(clients, 10);
  const stop = store.start();
  watch.push({ wishId: "", changes: [...everything, Change.INBOX] });
  const state = await until(store, (s) => s.loaded && s.inbox.length === 1);
  assert.equal(state.inbox[0].source, "babysit-mr");
  assert.equal(state.sources[0].name, "lamp/babysit-mr");
  assert.equal(state.error, "");
  const before = { ...reads };
  data.inbox = [];
  watch.push({ wishId: "", changes: [Change.INBOX] });
  await until(store, (s) => s.inbox.length === 0);
  stop();
  assert.equal(reads.inbox, before.inbox + 1);
  assert.equal(reads.sources, before.sources + 1);
  assert.equal(reads.wishes, before.wishes);
  assert.equal(reads.projects, before.projects);
});

// opened starts a store on sample data, with the wish shown and read.
async function opened() {
  const data = sample();
  data.tasks.push({ id: `${taskId}9`, wishId, code: "T02", title: "Wick" });
  const { clients, reads, watch } = server(data);
  const store = createStore(clients, 10);
  const stop = store.start();
  watch.push({ wishId: "", changes: everything });
  await until(store, (s) => s.loaded);
  store.open(wishId);
  await until(store, (s) => s.details[wishId]?.loaded);
  return { store, stop, watch, before: { ...reads }, reads };
}

const carried = (changed) => ({
  tasks: [],
  questions: [],
  blocks: [],
  deleted: [],
  ...changed,
});

test("what the watch brings is put in place, without reading, and the rest keeps its reference", async () => {
  const { store, stop, watch, before, reads } = await opened();
  const shown = store.getState().details[wishId];
  const [first, second] = shown.tasks;
  watch.push({
    wishId,
    changes: [Change.TASK],
    changed: carried({ tasks: [{ ...second, status: 4, lastLine: "Done" }] }),
  });
  const state = await until(
    store,
    (s) => s.details[wishId].tasks[1].lastLine === "Done",
  );
  const detail = state.details[wishId];
  assert.equal(detail.tasks.length, 2);
  assert.equal(detail.tasks[0], first, "the task that did not change");
  assert.equal(detail.questions, shown.questions);
  assert.equal(detail.blocks, shown.blocks);
  assert.equal(reads.tasks, before.tasks);
  assert.equal(reads.wishes, before.wishes);

  // One that comes back equal keeps its reference: nothing is drawn again.
  const now = detail.tasks;
  watch.push({
    wishId,
    changes: [Change.TASK],
    changed: carried({ tasks: [{ ...first }] }),
  });
  watch.push({ wishId: "", changes: [Change.PROJECT] });
  await until(store, () => reads.projects === before.projects + 1);
  assert.equal(store.getState().details[wishId].tasks, now);
  stop();
});

test("a new one goes where the service lists it, and one deleted goes", async () => {
  const { store, stop, watch, before, reads } = await opened();
  const shown = store.getState().details[wishId];
  watch.push({
    wishId,
    changes: [Change.TASK, Change.BLOCK, Change.QUESTION],
    changed: carried({
      // Between the two tasks, by id.
      tasks: [{ id: `${taskId}5`, wishId, code: "T03", title: "Oil" }],
      blocks: [
        { id: "b0", wishId, title: "First", position: 0n },
        { id: "b2", wishId, title: "Last", position: 5n },
      ],
      deleted: ["q1"],
    }),
  });
  const state = await until(
    store,
    (s) => s.details[wishId].questions.length === 0,
  );
  const detail = state.details[wishId];
  assert.deepEqual(
    detail.tasks.map((t) => t.code),
    ["T01", "T03", "T02"],
  );
  assert.deepEqual(
    detail.blocks.map((b) => b.id),
    ["b0", "b1", "b2"],
  );
  assert.equal(detail.blocks[1], shown.blocks[0]);
  assert.equal(reads.tasks, before.tasks);
  assert.equal(reads.blocks, before.blocks);
  assert.equal(reads.questions, before.questions);
  stop();
});

test("a change the page does not know reads everything again", async () => {
  const { store, stop, watch, before, reads } = await opened();
  watch.push({ wishId, changes: [99] });
  await until(
    store,
    () =>
      reads.tasks === before.tasks + 1 && reads.wishes === before.wishes + 1,
  );
  assert.equal(reads.blocks, before.blocks + 1);
  assert.equal(reads.questions, before.questions + 1);
  stop();
});

test("opening with specific kinds only reads those kinds", async () => {
  const data = sample();
  const { clients, reads, watch } = server(data);
  const store = createStore(clients, 10);
  const stop = store.start();
  watch.push({ wishId: "", changes: everything });
  await until(store, (s) => s.loaded);

  store.open(wishId, [Change.TASK]);
  const state = await until(store, (s) => s.details[wishId]?.loaded);
  assert.equal(reads.tasks, 1);
  assert.equal(reads.questions, undefined);
  assert.equal(reads.blocks, undefined);
  assert.equal(state.details[wishId].tasks.length, 1);
  assert.deepEqual(state.details[wishId].questions, []);
  assert.deepEqual(state.details[wishId].blocks, []);
  assert.equal(state.details[wishId].loadedKinds.has(Change.TASK), true);
  assert.equal(state.details[wishId].loadedKinds.has(Change.QUESTION), false);

  // Load questions on demand
  await store.loadKinds(wishId, [Change.QUESTION]);
  const state2 = store.getState();
  assert.equal(reads.tasks, 1, "tasks was not re-read");
  assert.equal(reads.questions, 1, "questions was read");
  assert.equal(reads.blocks, undefined);
  assert.equal(state2.details[wishId].questions.length, 1);
  assert.equal(state2.details[wishId].loadedKinds.has(Change.QUESTION), true);

  // Calling loadKinds for already loaded kind does not trigger re-read
  await store.loadKinds(wishId, [Change.TASK]);
  assert.equal(reads.tasks, 1);

  stop();
});

test("watch updates ignore unrequested kinds and only update loaded kinds", async () => {
  const data = sample();
  const { clients, reads, watch } = server(data);
  const store = createStore(clients, 10);
  const stop = store.start();
  watch.push({ wishId: "", changes: everything });
  await until(store, (s) => s.loaded);

  store.open(wishId, [Change.TASK]);
  await until(store, (s) => s.details[wishId]?.loadedKinds.has(Change.TASK));
  const before = { ...reads };

  // Watch event arrives with changes for TASK and QUESTION
  watch.push({
    wishId,
    changes: [Change.TASK, Change.QUESTION],
    changed: carried({
      tasks: [{ ...data.tasks[0], status: 4 }],
      questions: [{ id: "q1", wishId, code: "Q01", text: "New text?" }],
    }),
  });

  const state = await until(
    store,
    (s) => s.details[wishId]?.tasks[0]?.status === 4,
  );
  assert.equal(state.details[wishId].tasks[0].status, 4);
  assert.deepEqual(state.details[wishId].questions, []);
  assert.equal(reads.tasks, before.tasks);
  assert.equal(
    reads.questions,
    undefined,
    "questions was never read from server",
  );

  stop();
});

test("loadMore fetches the next page and merges items", async () => {
  const data = sample();
  const task1 = { id: `${taskId}1`, wishId, code: "T01", title: "Task 1" };
  const task2 = { id: `${taskId}2`, wishId, code: "T02", title: "Task 2" };
  let asked = 0;
  data.listTasks = (req) => {
    if (req.pageToken === "token-p2") {
      asked = req.pageSize;
      return {
        tasks: [task2],
        total: 2,
        nextPageToken: "",
      };
    }
    return {
      tasks: [task1],
      total: 2,
      nextPageToken: "token-p2",
    };
  };

  const { clients, reads, watch } = server(data);
  const store = createStore(clients, 10);
  const stop = store.start();
  watch.push({ wishId: "", changes: everything });
  await until(store, (s) => s.loaded);

  store.open(wishId, [Change.TASK]);
  await until(store, (s) => s.details[wishId]?.loadedKinds.has(Change.TASK));

  const state1 = store.getState().details[wishId];
  assert.equal(state1.tasks.length, 1);
  assert.equal(state1.tasks[0].id, task1.id);
  assert.equal(state1.tasksTotal, 2);
  assert.equal(state1.tasksNextToken, "token-p2");
  assert.equal(reads.tasks, 1);

  // The tree of the Tasks tab reads the rest by pages of the most tasks a page holds.
  await store.loadMore(wishId, Change.TASK, 200);
  const state2 = store.getState().details[wishId];
  assert.equal(reads.tasks, 2);
  assert.equal(asked, 200);
  assert.equal(state2.tasks.length, 2);
  assert.deepEqual(
    state2.tasks.map((t) => t.code),
    ["T01", "T02"],
  );
  assert.equal(state2.tasksTotal, 2);
  assert.equal(state2.tasksNextToken, "");

  // Calling loadMore when nextToken is empty does nothing
  await store.loadMore(wishId, Change.TASK);
  assert.equal(reads.tasks, 2);

  stop();
});

test("incremental changes adjust server totals", async () => {
  const data = sample();
  data.tasksTotal = 10;
  const { clients, watch } = server(data);
  const store = createStore(clients, 10);
  const stop = store.start();
  watch.push({ wishId: "", changes: everything });
  await until(store, (s) => s.loaded);

  store.open(wishId, [Change.TASK]);
  await until(store, (s) => s.details[wishId]?.loadedKinds.has(Change.TASK));
  assert.equal(store.getState().details[wishId].tasksTotal, 10);

  const newTask = { id: `${taskId}8`, wishId, code: "T02", title: "New" };
  watch.push({
    wishId,
    changes: [Change.TASK],
    changed: carried({ tasks: [newTask] }),
  });
  await until(store, (s) => s.details[wishId]?.tasks.length === 2);
  assert.equal(store.getState().details[wishId].tasksTotal, 11);

  watch.push({
    wishId,
    changes: [Change.TASK],
    changed: carried({ deleted: [newTask.id] }),
  });
  await until(store, (s) => s.details[wishId]?.tasks.length === 1);
  assert.equal(store.getState().details[wishId].tasksTotal, 10);

  stop();
});

test("a move removes the azima from the first wish's detail and adds it to the second's", async () => {
  const data = sample();
  const wish1 = wishId;
  const wish2 = "01a11833-a440-7479-a067-52615c91da72";
  data.wishes.push({
    id: wish2,
    title: "Collaborative Djinn",
    rank: 2,
    state: 1,
  });

  const azima = {
    id: `${taskId}1`,
    wishId: wish1,
    code: "T01",
    title: "Azima",
    kind: 2,
  };
  const part = {
    id: `${taskId}2`,
    wishId: wish1,
    code: "W01",
    title: "Part",
    partOf: azima.id,
  };
  const allTasks = [azima, part];
  data.listTasks = (req) => {
    const tasks = allTasks.filter((t) => t.wishId === req.wishId);
    return { tasks, total: tasks.length, nextPageToken: "" };
  };

  const { clients, reads, watch } = server(data);
  const store = createStore(clients, 10);
  const stop = store.start();
  watch.push({ wishId: "", changes: everything });
  await until(store, (s) => s.loaded);

  store.open(wish1, [Change.TASK]);
  store.open(wish2, [Change.TASK]);
  await until(
    store,
    (s) =>
      s.details[wish1]?.loadedKinds.has(Change.TASK) &&
      s.details[wish2]?.loadedKinds.has(Change.TASK),
  );

  assert.equal(store.getState().details[wish1].tasks.length, 2);
  assert.equal(store.getState().details[wish1].tasksTotal, 2);
  assert.equal(store.getState().details[wish2].tasks.length, 0);
  assert.equal(store.getState().details[wish2].tasksTotal, 0);
  const before = { ...reads };

  // Move azima and part from wish1 to wish2.
  const movedAzima = { ...azima, wishId: wish2, code: "T02" };
  const movedPart = { ...part, wishId: wish2, partOf: movedAzima.id };
  watch.push({
    wishId: wish1,
    changes: [Change.TASK],
    changed: carried({ deleted: [azima.id, part.id] }),
  });
  watch.push({
    wishId: wish2,
    changes: [Change.TASK],
    changed: carried({ tasks: [movedAzima, movedPart] }),
  });

  const state = await until(
    store,
    (s) =>
      s.details[wish1]?.tasks.length === 0 &&
      s.details[wish2]?.tasks.length === 2,
  );

  assert.equal(state.details[wish1].tasks.length, 0);
  assert.equal(state.details[wish1].tasksTotal, 0);
  assert.equal(state.details[wish2].tasks.length, 2);
  assert.equal(state.details[wish2].tasksTotal, 2);
  assert.deepEqual(
    state.details[wish2].tasks.map((t) => t.id),
    [movedAzima.id, movedPart.id],
  );
  assert.equal(state.details[wish2].tasks[0].code, "T02");
  assert.equal(state.details[wish2].tasks[1].partOf, movedAzima.id);
  assert.equal(reads.tasks, before.tasks, "tasks was not re-read from server");

  stop();
});

test("open questions beyond page 1 are loaded into store", async () => {
  const data = sample();
  const answered = Array.from({ length: 50 }, (_, i) => ({
    id: `01a11800-0000-0000-0000-${String(i).padStart(12, "0")}`,
    wishId,
    code: `Q${String(i + 1).padStart(2, "0")}`,
    text: `Answered question ${i + 1}`,
    answer: { choice: 1, note: "ok" },
  }));
  const open51 = {
    id: "01a11800-0000-0000-0000-000000000051",
    wishId,
    code: "Q51",
    text: "Can wait question 51?",
  };
  const open52 = {
    id: "01a11800-0000-0000-0000-000000000052",
    wishId,
    code: "Q52",
    text: "Blocking question 52?",
  };

  data.listQuestions = (req) => {
    if (req.open) {
      return {
        questions: [open51, open52],
        total: 2,
        nextPageToken: "",
      };
    }
    if (!req.pageToken) {
      return {
        questions: answered,
        total: 52,
        nextPageToken: "token-p2",
      };
    }
    return {
      questions: [open51, open52],
      total: 52,
      nextPageToken: "",
    };
  };

  const { clients, watch } = server(data);
  const store = createStore(clients, 10);
  const stop = store.start();
  watch.push({ wishId: "", changes: everything });
  await until(store, (s) => s.loaded);

  store.open(wishId, [Change.QUESTION]);
  await until(store, (s) =>
    s.details[wishId]?.loadedKinds.has(Change.QUESTION),
  );

  const detail = store.getState().details[wishId];
  assert.equal(detail.questions.length, 52);
  assert.equal(detail.questionsTotal, 52);
  assert.equal(detail.questionsNextToken, "token-p2");
  assert.ok(detail.questions.some((q) => q.code === "Q51" && !q.answer));
  assert.ok(detail.questions.some((q) => q.code === "Q52" && !q.answer));

  stop();
});

test("incremental updates refresh derived views: azima groups, loose tasks, dependencies, description", async () => {
  const wish1 = "01a11833-a440-7479-a067-52615c91da71";
  const w1 = {
    id: "01a11876-6480-7ec2-9833-abd058ae4a01",
    wishId: wish1,
    code: "W1",
    title: "Task 1",
    kind: TaskKind.WORK,
    status: TaskStatus.PLAN,
    dependsOn: [],
    partOf: "",
  };
  const w2 = {
    id: "01a11876-6480-7ec2-9833-abd058ae4a02",
    wishId: wish1,
    code: "W2",
    title: "Task 2",
    kind: TaskKind.WORK,
    status: TaskStatus.PLAN,
    dependsOn: [],
    partOf: "",
  };
  const data = {
    wishes: [{ id: wish1, title: "Test Wish" }],
    tasks: [w1, w2],
  };

  const { clients, watch } = server(data);
  const store = createStore(clients, 10);
  const stop = store.start();
  watch.push({ wishId: "", changes: everything });
  await until(store, (s) => s.loaded);

  store.open(wish1, [Change.TASK]);
  await until(store, (s) => s.details[wish1]?.loadedKinds.has(Change.TASK));

  let tasks = store.getState().details[wish1].tasks;
  assert.equal(tasks.length, 2);
  assert.deepEqual(
    loose(tasks).map((t) => t.id),
    [w1.id, w2.id],
  );
  assert.equal(azimaGroups(tasks).length, 0);

  // 1. New azima appears
  const t1 = {
    id: "01a11876-6480-7ec2-9833-abd058ae4a03",
    wishId: wish1,
    code: "T1",
    title: "Azima 1",
    kind: TaskKind.AZIMA,
    status: TaskStatus.PLAN,
    azima: { state: AzimaState.OPEN },
    dependsOn: [],
    partOf: "",
    description: "",
  };
  watch.push({
    wishId: wish1,
    changes: [Change.TASK],
    changed: carried({ tasks: [t1] }),
  });

  await until(store, (s) => s.details[wish1]?.tasks.length === 3);
  tasks = store.getState().details[wish1].tasks;
  assert.equal(azimaGroups(tasks).length, 1);
  assert.equal(azimaGroups(tasks)[0].azima.id, t1.id);
  assert.equal(azimaGroups(tasks)[0].parts.length, 0);
  assert.deepEqual(
    loose(tasks).map((t) => t.id),
    [w1.id, w2.id],
  );

  // 2. Task partOf change moves it under its azima
  const w1Grouped = { ...w1, partOf: t1.id };
  watch.push({
    wishId: wish1,
    changes: [Change.TASK],
    changed: carried({ tasks: [w1Grouped] }),
  });

  await until(
    store,
    (s) =>
      s.details[wish1]?.tasks.find((t) => t.id === w1.id)?.partOf === t1.id,
  );
  tasks = store.getState().details[wish1].tasks;
  assert.deepEqual(
    loose(tasks).map((t) => t.id),
    [w2.id],
  );
  assert.equal(azimaGroups(tasks).length, 1);
  assert.equal(azimaGroups(tasks)[0].parts.length, 1);
  assert.equal(azimaGroups(tasks)[0].parts[0].id, w1.id);

  // 3. Dependency change updates its dependsOn (after line)
  const w2Dep = { ...w2, dependsOn: [w1.id] };
  watch.push({
    wishId: wish1,
    changes: [Change.TASK],
    changed: carried({ tasks: [w2Dep] }),
  });

  await until(
    store,
    (s) =>
      s.details[wish1]?.tasks.find((t) => t.id === w2.id)?.dependsOn?.length ===
      1,
  );
  tasks = store.getState().details[wish1].tasks;
  const foundW2 = tasks.find((t) => t.id === w2.id);
  assert.deepEqual(foundW2.dependsOn, [w1.id]);

  // 4. Description appears on the azima
  const t1Described = { ...t1, description: "A detailed azima description" };
  watch.push({
    wishId: wish1,
    changes: [Change.TASK],
    changed: carried({ tasks: [t1Described] }),
  });

  await until(
    store,
    (s) =>
      s.details[wish1]?.tasks.find((t) => t.id === t1.id)?.description ===
      "A detailed azima description",
  );
  tasks = store.getState().details[wish1].tasks;
  assert.equal(
    azimaGroups(tasks)[0].azima.description,
    "A detailed azima description",
  );

  stop();
});

test("worker measurements over a minute update tasks in place without task-list re-reads", async () => {
  const data = sample();
  const workerCount = 5;
  const workerTasks = [];
  for (let i = 1; i <= workerCount; i++) {
    workerTasks.push({
      id: `worker-task-${i}`,
      wishId,
      code: `T0${i}`,
      title: `Worker ${i}`,
      status: TaskStatus.IN_PROGRESS,
      resources: { cpuPercent: 10, memoryBytes: 100_000_000n },
    });
  }
  data.tasks = workerTasks;
  const { clients, reads, watch } = server(data);
  const store = createStore(clients, 10);
  const stop = store.start();
  watch.push({ wishId: "", changes: everything });
  await until(store, (s) => s.loaded);
  store.open(wishId);
  await until(store, (s) => s.details[wishId]?.loaded);

  // Initial read when wish was opened
  assert.equal(reads.tasks, 1, "initial task list read");

  // Simulate 1 minute of worker measurements (every 5 seconds = 12 ticks) for N=5 workers
  for (let tick = 1; tick <= 12; tick++) {
    for (let i = 1; i <= workerCount; i++) {
      const updatedTask = {
        id: `worker-task-${i}`,
        wishId,
        code: `T0${i}`,
        title: `Worker ${i}`,
        status: TaskStatus.IN_PROGRESS,
        resources: {
          cpuPercent: 10 + tick * 5 + i,
          memoryBytes: BigInt(100_000_000 + tick * 1_000_000),
        },
      };
      watch.push({
        wishId,
        changes: [Change.TASK],
        changed: carried({ tasks: [updatedTask] }),
      });
    }
  }

  // Wait until the final measurement arrives and updates the store
  await until(
    store,
    (s) =>
      s.details[wishId]?.tasks.find(
        (t) => t.id === `worker-task-${workerCount}`,
      )?.resources?.cpuPercent ===
      10 + 12 * 5 + workerCount,
  );

  // Bound: exactly 0 task-list re-reads during worker measurements (total reads.tasks remains 1)
  assert.equal(
    reads.tasks,
    1,
    "bound: task-list re-reads during 60s of measurements is 0 (total reads.tasks = 1)",
  );

  // Verify all workers received the updated in-place metrics
  const currentTasks = store.getState().details[wishId].tasks;
  for (let i = 1; i <= workerCount; i++) {
    const t = currentTasks.find((item) => item.id === `worker-task-${i}`);
    assert.equal(t.resources.cpuPercent, 10 + 12 * 5 + i);
  }

  stop();
});

// The throttle's rule, not a count over wall time: a slower runner (Windows' timers tick every ~16 ms) pushes the same
// burst over a longer time, and more reads are right then. Each read of a wish's tasks comes at least throttleMs after
// the one before it, so n reads need (n - 1) * throttleMs: the bound is measured on the clock, at any speed.
for (const paceMs of [2, 16]) {
  test(`window throttles task re-reads during rapid changes and never enters a tight loop (a change every ${paceMs} ms)`, async () => {
    const data = sample();
    const { clients, reads, watch } = server(data);
    const throttleMs = 50;
    const store = createStore(clients, 10, throttleMs);
    const stop = store.start();
    watch.push({ wishId: "", changes: everything });
    await until(store, (s) => s.loaded);
    const start = Date.now();
    store.open(wishId);
    await until(store, (s) => s.details[wishId]?.loaded);
    assert.equal(reads.tasks, 1);

    const pushes = 25;
    for (let i = 0; i < pushes; i++) {
      watch.push({ wishId, changes: [Change.TASK] });
      await new Promise((r) => setTimeout(r, paceMs));
    }

    // No tight loop: the reads stop once the burst is read, however slow the machine is.
    let seen = -1;
    for (let window = 0; reads.tasks !== seen; window++) {
      assert.ok(window < 40, `task reads never stop: ${reads.tasks}`);
      seen = reads.tasks;
      await new Promise((r) => setTimeout(r, 3 * throttleMs));
    }
    const elapsed = Date.now() - start;

    const bound = 1 + Math.floor(elapsed / throttleMs);
    assert.ok(
      reads.tasks <= bound,
      `expected at most ${bound} task reads in ${elapsed} ms (one per ${throttleMs} ms), got ${reads.tasks}`,
    );
    assert.ok(reads.tasks >= 2, "the burst is read after the first read");

    stop();
  });
}

test("a wish with more tasks than one page has right azima counters before any azima is opened", async () => {
  const data = sample();
  // 430 tasks total: 2 azimas with server counts, only page 1 (50 tasks) loaded first
  const azima1 = {
    id: "az-1",
    wishId,
    code: "T1",
    title: "Orchestrator",
    kind: TaskKind.AZIMA,
    status: TaskStatus.RUNNING,
    azima: {
      state: AzimaState.IN_PROGRESS,
      ready: true,
      parts: 250,
      partsDone: 200,
      partsRunning: 2,
    },
  };
  const azima2 = {
    id: "az-2",
    wishId,
    code: "T2",
    title: "Groundwork",
    kind: TaskKind.AZIMA,
    status: TaskStatus.DONE,
    azima: {
      state: AzimaState.DONE,
      ready: true,
      parts: 180,
      partsDone: 180,
      partsRunning: 0,
    },
  };
  // Page 1 tasks: azima1, azima2, and 48 parts of azima1
  const page1Tasks = [azima1, azima2];
  for (let i = 1; i <= 48; i++) {
    page1Tasks.push({
      id: `task-part-${i}`,
      wishId,
      code: `T${i + 2}`,
      title: `Part ${i}`,
      kind: TaskKind.WORKER,
      partOf: "az-1",
      status: i <= 20 ? TaskStatus.DONE : TaskStatus.PENDING,
    });
  }

  data.tasks = page1Tasks;
  data.tasksTotal = 430;
  data.tasksNextToken = "token-page-2";

  const { clients, watch } = server(data);
  const store = createStore(clients, 10);
  const stop = store.start();
  watch.push({ wishId: "", changes: everything });
  await until(store, (s) => s.loaded);

  store.open(wishId);
  await until(store, (s) => s.details[wishId]?.loaded);

  const detail = store.getState().details[wishId];
  assert.equal(detail.tasks.length, 50);
  assert.equal(detail.tasksNextToken, "token-page-2");

  // Before any azima is opened, counters use server Task.azima fields:
  assert.deepEqual(azimasDone(detail.tasks), { done: 1, proof: 0, count: 2 });
  assert.equal(workCount(detail.tasks), 430);
  assert.equal(workDoneCount(detail.tasks), 380);

  stop();
});

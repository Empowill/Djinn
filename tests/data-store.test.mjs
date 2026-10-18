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
  RoundKind,
  TaskStatus,
  AzimaState,
  azimaGroups,
  loose,
  movingTasks,
  azimasDone,
  workCount,
  workDoneCount,
} = await bundle(
  "store",
  `export { createStore } from "@/src/data/store.ts";
export { createClients } from "@/src/data/client.ts";
export { createRouterTransport } from "@connectrpc/connect";
export { azimaGroups, loose, movingTasks, azimasDone, workCount, workDoneCount } from "@/src/data/flight.ts";
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
        if (req.live) {
          const live = data.tasks.filter(
            (t) =>
              t.status !== TaskStatus.DONE && t.status !== TaskStatus.CANCELLED,
          );
          return { tasks: live, total: live.length, nextPageToken: "" };
        }
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
  const store = createStore(clients, 10, 10, 1000);
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
  const store = createStore(clients, 10, 10, 1000);
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

// W503 saw the window's memory climb to 1.1 GB: the events of every task card ever opened stayed in the store.
test("a task card closed frees its events, and opened again reads them again", async () => {
  const data = sample();
  const { clients, reads } = server(data);
  const store = createStore(clients, 10, 10, 30);
  const first = store.follow(taskId);
  // Two screens show the task: the events stay while one of them does.
  const second = store.follow(taskId);
  await until(store, (s) => s.events[taskId]?.length === 2);
  first();
  first();
  await new Promise((r) => setTimeout(r, 60));
  assert.equal(store.getState().events[taskId]?.length, 2);
  second();
  const freed = await until(store, (s) => !(taskId in s.events));
  assert.equal(Object.keys(freed.events).length, 0);
  const again = store.follow(taskId);
  await until(store, (s) => s.events[taskId]?.length === 2);
  assert.equal(reads.events, 2);
  again();
});

// W7 saw "[unknown] Failed to fetch" thrown at the page: Chromium cuts every request in flight when Docker adds a
// network interface, and the prompt's read was left to fail (W249).
test("reading a task's prompt gets the task alone, read again when the network cut it, then kept", async () => {
  const data = sample();
  let fail = 1;
  data.getTask = (req) => {
    if (fail-- > 0) throw new Error("Failed to fetch");
    return { task: data.tasks.find((t) => t.id === req.taskId) };
  };
  const { clients, reads } = server(data);
  const store = createStore(clients, 10);
  // The cut read throws nothing at the page: it gives no prompt, and is made again.
  assert.equal(await store.readPrompt(taskId), "");
  const state = await until(store, (s) => !!s.prompts[taskId]);
  assert.equal(state.prompts[taskId], "Polish the brass with care");
  assert.equal(reads.taskGet, 2);
  assert.equal(reads.events, undefined);
  // A second read takes the prompt from the store's cache.
  assert.equal(await store.readPrompt(taskId), "Polish the brass with care");
  assert.equal(reads.taskGet, 2);
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
    if (req.live) return { tasks: [], total: 0, nextPageToken: "" };
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
  // The first page, and the live tasks past it (W219): none here.
  assert.equal(reads.tasks, 2);

  // The tree of the Tasks tab reads the rest by pages of the most tasks a page holds.
  await store.loadMore(wishId, Change.TASK, 200);
  const state2 = store.getState().details[wishId];
  assert.equal(reads.tasks, 3);
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
  assert.equal(reads.tasks, 3);

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

  const { clients, reads, watch } = server(data);
  const store = createStore(clients, 10);
  const stop = store.start();
  watch.push({ wishId: "", changes: everything });
  await until(store, (s) => s.loaded);

  store.open(wish1, [Change.TASK]);
  await until(store, (s) => s.details[wish1]?.loadedKinds.has(Change.TASK));
  const initialTaskReads = reads.tasks;

  let tasks = store.getState().details[wish1].tasks;
  assert.equal(tasks.length, 2);
  assert.deepEqual(
    loose(tasks).map((t) => t.id),
    [w1.id, w2.id],
  );
  assert.deepEqual(
    movingTasks(tasks).map((t) => t.id),
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
  assert.deepEqual(
    movingTasks(tasks).map((t) => t.id),
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
  assert.equal(reads.tasks, initialTaskReads);

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

// W486: the wish Dew, made from the command line, held an azima T1 the lead spawned with no plan file and W1, a
// worker part of it, running. The window showed neither W1 nor a running worker in its counters until W1 ended. The
// store kept a wish's tasks as a failed read, or a change taken while the wish was not shown, left them, until the
// same task changed again: both are read again now.
function dew() {
  const t1 = {
    id: "01a130b1-4a8a-7761-9222-553b369b85cc",
    wishId,
    code: "T1",
    title: "Silae",
    kind: TaskKind.AZIMA,
    status: TaskStatus.PENDING,
    description: "Can it be done?",
    azima: { state: AzimaState.OPEN, ready: true },
  };
  const w1 = {
    id: "01a130b1-d124-77f1-8ce9-f40d4530bcbe",
    wishId,
    code: "W1",
    title: "Silae feasibility study",
    kind: TaskKind.WORK,
    status: TaskStatus.RUNNING,
    partOf: t1.id,
  };
  return { t1, w1 };
}

// running are the tasks of a wish whose worker runs, as the window's counters count them.
const runningIn = (s) =>
  (s.details[wishId]?.tasks ?? []).filter(
    (x) => x.status === TaskStatus.RUNNING,
  );

test("a task list whose read failed is read again: the worker spawned meanwhile shows in its azima and counts", async () => {
  const { t1, w1 } = dew();
  const data = { ...sample(), tasks: [t1] };
  let fail = 0;
  data.listTasks = () => {
    if (fail-- > 0) throw new Error("database is locked");
    return { tasks: data.tasks, total: data.tasks.length, nextPageToken: "" };
  };
  const { clients, reads, watch } = server(data);
  const store = createStore(clients, 10);
  const stop = store.start();
  watch.push({ wishId: "", changes: everything });
  await until(store, (s) => s.loaded);
  store.open(wishId);
  await until(store, (s) => s.details[wishId]?.tasks.length === 1);

  // The server could not carry W1 (no entities): the window reads the list, and that read fails once.
  data.tasks = [t1, w1];
  fail = 1;
  const before = reads.tasks;
  watch.push({ wishId, changes: [Change.TASK, Change.WISH] });
  const state = await until(store, (s) => runningIn(s).length === 1);
  assert.ok(reads.tasks >= before + 2, "read again after it failed");
  const [group] = azimaGroups(state.details[wishId].tasks);
  assert.equal(group.azima.code, "T1");
  assert.deepEqual(
    group.parts.map((x) => x.code),
    ["W1"],
  );
  assert.deepEqual(loose(state.details[wishId].tasks), []);
  stop();
});

test("a wish's first task list that failed is not taken as read: it is read again", async () => {
  const { t1, w1 } = dew();
  const data = { ...sample(), tasks: [t1, w1] };
  let fail = 1;
  data.listTasks = () => {
    if (fail-- > 0) throw new Error("unavailable");
    return { tasks: data.tasks, total: data.tasks.length, nextPageToken: "" };
  };
  const { clients, watch } = server(data);
  const store = createStore(clients, 10);
  const stop = store.start();
  watch.push({ wishId: "", changes: everything });
  await until(store, (s) => s.loaded);
  store.open(wishId);
  const state = await until(store, (s) => runningIn(s).length === 1);
  assert.ok(state.details[wishId].loadedKinds.has(Change.TASK));
  stop();
});

test("a wish shown again reads again what changed while it was not shown", async () => {
  const { t1, w1 } = dew();
  const data = { ...sample(), tasks: [t1] };
  const { clients, reads, watch } = server(data);
  const store = createStore(clients, 10);
  const stop = store.start();
  watch.push({ wishId: "", changes: everything });
  await until(store, (s) => s.loaded);
  const close = store.open(wishId);
  await until(store, (s) => s.details[wishId]?.tasks.length === 1);
  close();

  // W1 is spawned and starts while no screen shows the wish: the window does not take it.
  data.tasks = [t1, w1];
  watch.push({
    wishId,
    changes: [Change.TASK],
    changed: carried({ tasks: [w1] }),
  });
  watch.push({ wishId: "", changes: [Change.PROJECT] });
  const projects = reads.projects;
  await until(store, () => reads.projects === projects + 1);
  assert.equal(runningIn(store.getState()).length, 0);

  // Shown again, its tasks are read again, not kept as they were.
  store.open(wishId);
  const state = await until(store, (s) => runningIn(s).length === 1);
  assert.equal(azimaGroups(state.details[wishId].tasks)[0].parts[0].code, "W1");
  stop();
});

// The questions of incident Q253's wish: fifty answered on the first page, then one open past it.
function pagedQuestions(data) {
  const answered = Array.from({ length: 50 }, (_, i) => ({
    id: `01a11800-0000-0000-0000-${String(i).padStart(12, "0")}`,
    wishId,
    code: `Q${String(i + 1).padStart(2, "0")}`,
    text: `Answered question ${i + 1}`,
    answer: { choice: 1, note: "ok" },
  }));
  const open = {
    id: "01a11800-0000-0000-0000-000000000253",
    wishId,
    code: "Q253",
    text: "Which name for the page?",
  };
  data.listQuestions = (req) => {
    const all = [...answered, open];
    if (req.open) {
      const list = all.filter((q) => !q.answer);
      return { questions: list, total: list.length, nextPageToken: "" };
    }
    if (!req.pageToken)
      return { questions: answered, total: 51, nextPageToken: "token-p2" };
    return { questions: [open], total: 51, nextPageToken: "" };
  };
  return {
    answer(note) {
      open.answer = { choice: 3, note };
      return { ...open };
    },
  };
}

test("an answered question past the first page stays, answered, when the questions are read again", async () => {
  const data = sample();
  const q253 = pagedQuestions(data);
  const { clients, watch } = server(data);
  const store = createStore(clients, 10);
  const stop = store.start();
  watch.push({ wishId: "", changes: everything });
  await until(store, (s) => s.loaded);
  store.open(wishId, [Change.QUESTION]);
  await until(store, (s) =>
    s.details[wishId]?.questions.some((q) => q.code === "Q253"),
  );

  // The window answers it, then reads the questions again before the watch brings it.
  q253.answer("The hourglass.");
  await store.changed(wishId, [Change.QUESTION]);
  const shown = store
    .getState()
    .details[wishId].questions.find((q) => q.code === "Q253");
  stop();
  assert.ok(shown, "the answered question left the screens");
  assert.equal(shown.answer?.note, "The hourglass.");
});

test("a read that never answers fails after its timeout: the store reads again, the answer shows", async () => {
  const data = sample();
  const q253 = pagedQuestions(data);
  let hang = false;
  const list = data.listQuestions;
  data.listQuestions = (req) => (hang ? new Promise(() => {}) : list(req));
  const { clients, reads, watch } = server(data);
  const store = createStore(clients, 10, 10, 5_000, 100);
  const stop = store.start();
  watch.push({ wishId: "", changes: everything });
  await until(store, (s) => s.loaded);
  store.open(wishId, [Change.QUESTION]);
  await until(store, (s) =>
    s.details[wishId]?.questions.some((q) => q.code === "Q253"),
  );

  // A read hangs: the next ones must not wait for it for ever.
  hang = true;
  void store.changed(wishId, [Change.QUESTION]);
  await new Promise((r) => setTimeout(r, 20));
  hang = false;
  q253.answer("The hourglass.");
  const wishes = reads.wishes;
  void store.changed(wishId, [Change.QUESTION, Change.WISH]);
  const state = await until(
    store,
    (s) =>
      !!s.details[wishId]?.questions.find((q) => q.code === "Q253")?.answer,
  );
  stop();
  assert.ok(reads.wishes > wishes, "the wishes were never read again");
  assert.equal(
    state.details[wishId].questions.find((q) => q.code === "Q253").answer.note,
    "The hourglass.",
  );
});

test("a question an answer returned shows answered at once, without a read", async () => {
  const data = sample();
  const q253 = pagedQuestions(data);
  const { clients, reads, watch } = server(data);
  const store = createStore(clients, 10);
  const stop = store.start();
  watch.push({ wishId: "", changes: everything });
  await until(store, (s) => s.loaded);
  store.open(wishId, [Change.QUESTION]);
  await until(store, (s) =>
    s.details[wishId]?.questions.some((q) => q.code === "Q253"),
  );
  const before = reads.questions;
  store.putQuestion(q253.answer("The hourglass."));
  const shown = store
    .getState()
    .details[wishId].questions.find((q) => q.code === "Q253");
  stop();
  assert.equal(shown.answer?.note, "The hourglass.");
  assert.equal(reads.questions, before);
});

// Incident Q274: a question showed "Being investigated" after its investigator had revised it, until a reload.
const enlightened = {
  id: "q1",
  wishId,
  code: "Q01",
  text: "Which wick?",
  options: ["Cotton", "Linen"],
  rounds: [{ kind: RoundKind.ENLIGHTEN, note: "Which lasts?" }],
};
const revisedQ01 = {
  ...enlightened,
  context: "Linen burns twice as long.",
  recommendation: "B: it lasts.",
  revision: 1,
  rounds: [...enlightened.rounds, { kind: RoundKind.REVISE }],
};
const lastRound = (s) =>
  s.details[wishId]?.questions.find((q) => q.id === "q1")?.rounds.at(-1)?.kind;

// investigated starts a store on a wish whose question Q01 is being investigated, its questions read.
async function investigated(data = sample()) {
  data.questions = [enlightened];
  const { clients, reads, watch } = server(data);
  const store = createStore(clients, 10);
  const stop = store.start();
  watch.push({ wishId: "", changes: everything });
  await until(store, (s) => s.loaded);
  store.open(wishId);
  await until(store, (s) => lastRound(s) === RoundKind.ENLIGHTEN);
  return { data, store, stop, watch, reads };
}

test("a revision the watch brings takes the question out of investigation, without a read", async () => {
  const { store, stop, watch, reads } = await investigated();
  const before = reads.questions;
  watch.push({
    wishId,
    changes: [Change.QUESTION, Change.WISH],
    changed: carried({ questions: [revisedQ01] }),
  });
  const state = await until(store, (s) => lastRound(s) === RoundKind.REVISE);
  stop();
  const q = state.details[wishId].questions[0];
  assert.equal(q.context, "Linen burns twice as long.");
  assert.equal(q.recommendation, "B: it lasts.");
  assert.equal(reads.questions, before);
});

test("a read that left before a revision does not take the question back under investigation", async () => {
  const { data, store, stop } = await investigated();
  // A read leaves while the question is still under investigation, and answers late.
  let answer;
  data.listQuestions = () =>
    new Promise((r) => (answer = r)).then(() => ({
      questions: [enlightened],
      total: 1,
    }));
  const reading = store.changed(wishId, [Change.QUESTION]);
  await new Promise((r) => setTimeout(r, 20));
  // Meanwhile the revision shows.
  store.putQuestion(revisedQ01);
  assert.equal(lastRound(store.getState()), RoundKind.REVISE);
  answer();
  await reading;
  stop();
  assert.equal(lastRound(store.getState()), RoundKind.REVISE);
});

test("a page loaded across a revision does not take the question back under investigation", async () => {
  const data = sample();
  // A first page, and a page after it.
  data.listQuestions = (req) => ({
    questions: [enlightened],
    total: 2,
    nextPageToken: req.open || req.pageToken ? "" : "page2",
  });
  const { store, stop, watch } = await investigated(data);
  let answer;
  data.listQuestions = () =>
    new Promise((r) => (answer = r)).then(() => ({
      questions: [enlightened],
      total: 1,
      nextPageToken: "",
    }));
  const loading = store.loadMore(wishId, Change.QUESTION);
  await new Promise((r) => setTimeout(r, 20));
  watch.push({
    wishId,
    changes: [Change.QUESTION],
    changed: carried({ questions: [revisedQ01] }),
  });
  await until(store, (s) => lastRound(s) === RoundKind.REVISE);
  answer();
  await loading;
  stop();
  assert.equal(lastRound(store.getState()), RoundKind.REVISE);
});

test("a watch back after djinn restarted reads the open questions again: one revised meanwhile shows revised", async () => {
  const { data, store, stop, watch, reads } = await investigated();
  watch.push(new Error("djinn restarted"));
  await until(store, (s) => !s.live);
  // Revised while the window was cut from djinn: no message brings it.
  data.questions = [revisedQ01];
  const before = reads.questions;
  watch.push({ wishId: "", changes: everything });
  await until(store, (s) => s.live && lastRound(s) === RoundKind.REVISE);
  stop();
  assert.ok(reads.questions > before);
});

// W219: on arriving on a wish, its glance showed the running workers (the watch brings them as they change) but not the
// waiting ones past the first page of its tasks, until the Tasks tab read further. The first read brings the live
// tasks too, whatever page they sit on, and a read again goes no further than the pages read.
test("the first read of a wish's tasks brings its waiting work past the first page", async () => {
  const data = sample();
  const old = [
    {
      id: "t1",
      wishId,
      code: "W1",
      title: "Old work",
      status: TaskStatus.DONE,
    },
    {
      id: "t2",
      wishId,
      code: "W2",
      title: "Old work",
      status: TaskStatus.DONE,
    },
  ];
  const later = {
    id: "t3",
    wishId,
    code: "W3",
    title: "Merged",
    status: TaskStatus.DONE,
  };
  const waiting = {
    id: "t4",
    wishId,
    code: "W4",
    title: "Waits on W5",
    status: TaskStatus.PENDING,
    dependsOn: ["t5"],
  };
  const running = {
    id: "t5",
    wishId,
    code: "W5",
    title: "Runs",
    status: TaskStatus.RUNNING,
  };
  const asked = [];
  data.listTasks = (req) => {
    asked.push(req.live ? "live" : req.pageToken || "first");
    if (req.live)
      return { tasks: [waiting, running], total: 2, nextPageToken: "" };
    return req.pageToken
      ? { tasks: [later, waiting, running], total: 5, nextPageToken: "" }
      : { tasks: old, total: 5, nextPageToken: "p2" };
  };
  const { clients, watch } = server(data);
  const store = createStore(clients, 10);
  const stop = store.start();
  watch.push({ wishId: "", changes: everything });
  await until(store, (s) => s.loaded);
  store.open(wishId, [Change.TASK]);
  const state = await until(store, (s) =>
    s.details[wishId]?.loadedKinds.has(Change.TASK),
  );
  assert.deepEqual(
    state.details[wishId].tasks.map((t) => t.code),
    ["W1", "W2", "W4", "W5"],
  );
  assert.equal(state.details[wishId].tasksNextToken, "p2");
  // Read again: the live ones are not a reason to read every page.
  await store.changed(wishId, [Change.TASK]);
  stop();
  assert.deepEqual(asked, ["first", "live", "first", "live"]);
  // The Tasks tab reads further, and the tasks keep their order.
  await store.loadMore(wishId, Change.TASK);
  assert.deepEqual(
    store.getState().details[wishId].tasks.map((t) => t.code),
    ["W1", "W2", "W3", "W4", "W5"],
  );
});

// Incident Q274, second case: after a restart the window kept W544, the enlighten task of Q254, running, and Q254
// under investigation; W544 had ended, and Q254 was answered. W544 sat past the first page of the wish's tasks: the
// read after the watch came back went no further than that page.
test("an enlighten task that ended while djinn was away shows ended after the watch comes back, its question answered", async () => {
  const data = sample();
  const old = [
    { id: "t1", wishId, code: "W1", title: "Old work", status: 4 },
    { id: "t2", wishId, code: "W2", title: "Old work", status: 4 },
  ];
  const w544 = {
    id: "t3",
    wishId,
    code: "W544",
    title: "Q254: enlighten",
    status: TaskStatus.RUNNING,
  };
  data.listTasks = (req) =>
    req.live
      ? {
          tasks: w544.status === TaskStatus.RUNNING ? [w544] : [],
          total: 1,
          nextPageToken: "",
        }
      : req.pageToken
        ? { tasks: [w544], total: 3, nextPageToken: "" }
        : { tasks: old, total: 3, nextPageToken: "p2" };
  const { store, stop, watch, reads } = await investigated(data);
  await store.loadMore(wishId, Change.TASK);
  const status = (s) =>
    s.details[wishId]?.tasks.find((t) => t.code === "W544")?.status;
  assert.equal(status(store.getState()), TaskStatus.RUNNING);

  watch.push(new Error("djinn restarted"));
  await until(store, (s) => !s.live);
  // Meanwhile W544 ends, and the developer answers Q01 elsewhere.
  w544.status = TaskStatus.DONE;
  data.questions = [{ ...enlightened, answer: { choice: 1 } }];
  const before = reads.tasks;
  watch.push({ wishId: "", changes: everything });
  const state = await until(
    store,
    (s) =>
      s.live &&
      status(s) === TaskStatus.DONE &&
      !!s.details[wishId].questions[0].answer,
  );
  stop();
  assert.ok(reads.tasks > before);
  assert.equal(state.details[wishId].tasks.length, 3);
});

test("updating a planned task's prompt, title, provider, model and dependencies shows in the open wish at once without reload", async () => {
  const data = sample();
  const { clients, watch } = server(data);
  const store = createStore(clients, 10);
  const stop = store.start();
  watch.push({ wishId: "", changes: everything });
  store.open(wishId);
  await until(store, (s) => s.details[wishId]?.loaded);

  // Even when readPrompt cached the initial prompt, subsequent updates replace it at once.
  assert.equal(await store.readPrompt(taskId), "Polish the brass with care");
  assert.equal(store.getState().prompts[taskId], "Polish the brass with care");

  // 1. Prompt-only update streamed in changed.tasks.
  const promptOnly = {
    ...data.tasks[0],
    prompt: "Polish only the spout first",
  };
  data.tasks = [promptOnly];
  watch.push({
    wishId,
    changes: [Change.TASK],
    changed: { tasks: [promptOnly] },
  });
  let state = await until(
    store,
    (s) =>
      s.details[wishId]?.tasks[0]?.prompt === "Polish only the spout first",
  );
  assert.equal(state.prompts[taskId], "Polish only the spout first");

  // 2. Single-task update of all fields streamed directly in changed.tasks by WishService.Watch.
  const streamed = {
    ...promptOnly,
    title: "Burnish the lamp",
    prompt: "Burnish the brass until it shines",
    provider: 4,
    model: "gemini-3.8-flash-high",
    dependsOn: ["t0"],
  };
  data.tasks = [streamed];
  watch.push({
    wishId,
    changes: [Change.TASK],
    changed: { tasks: [streamed] },
  });
  state = await until(
    store,
    (s) =>
      s.details[wishId]?.tasks[0]?.prompt ===
      "Burnish the brass until it shines",
  );
  assert.equal(state.details[wishId].tasks[0].title, "Burnish the lamp");
  assert.equal(state.details[wishId].tasks[0].provider, 4);
  assert.equal(state.details[wishId].tasks[0].model, "gemini-3.8-flash-high");
  assert.deepEqual(state.details[wishId].tasks[0].dependsOn, ["t0"]);
  assert.equal(state.prompts[taskId], "Burnish the brass until it shines");

  // 3. Bulk update (> 20 tasks, like 26 prompts rewritten at once) where WishService.Watch sends Change.TASK
  // without changed.tasks and the store re-lists the wish's tasks.
  const relisted = {
    ...streamed,
    title: "Rewrite all twenty-six prompts",
    prompt: "Twenty-six prompts rewritten at once",
    provider: 1,
    model: "claude-opus-4-7",
    dependsOn: [],
  };
  data.tasks = [relisted];
  watch.push({ wishId, changes: [Change.TASK] });
  state = await until(
    store,
    (s) =>
      s.details[wishId]?.tasks[0]?.prompt ===
      "Twenty-six prompts rewritten at once",
  );
  assert.equal(
    state.details[wishId].tasks[0].title,
    "Rewrite all twenty-six prompts",
  );
  assert.equal(state.details[wishId].tasks[0].provider, 1);
  assert.equal(state.details[wishId].tasks[0].model, "claude-opus-4-7");
  assert.deepEqual(state.details[wishId].tasks[0].dependsOn, []);
  assert.equal(state.prompts[taskId], "Twenty-six prompts rewritten at once");

  // 4. Following the task's event stream (TaskService.Watch) also updates the cached prompt to the latest PROMPT event.
  data.events = [
    { seq: 1n, kind: 1, text: "Old event prompt" },
    { seq: 2n, kind: 1, text: "Latest event prompt" },
  ];
  const release = store.follow(taskId);
  state = await until(
    store,
    (s) => s.prompts[taskId] === "Latest event prompt",
  );
  assert.equal(state.prompts[taskId], "Latest event prompt");
  release();
  stop();
});

test("answering open questions past two full pages of decided questions keeps each answer and updates the remaining open questions", async () => {
  const data = sample();
  const decided = Array.from({ length: 100 }, (_, i) => ({
    id: `01a11800-0000-0000-0000-${String(i + 1).padStart(12, "0")}`,
    wishId,
    code: `Q${String(i + 1).padStart(2, "0")}`,
    text: `Decided question ${i + 1}?`,
    options: ["Yes", "No"],
    answer: { choice: 1, note: "yes" },
  }));
  const q101 = {
    id: "01a11800-0000-0000-0000-000000000101",
    wishId,
    code: "Q101",
    text: "Can wait question 101?",
    options: ["Option A", "Option B"],
  };
  const q102 = {
    id: "01a11800-0000-0000-0000-000000000102",
    wishId,
    code: "Q102",
    text: "Blocking question 102?",
    options: ["Proceed", "Stop"],
  };
  data.listQuestions = (req) => {
    const all = [...decided, q101, q102];
    if (req.open) {
      const openList = all.filter((q) => !q.answer);
      return { questions: openList, total: openList.length, nextPageToken: "" };
    }
    if (!req.pageToken) {
      return {
        questions: decided.slice(0, 50),
        total: 102,
        nextPageToken: "token-p2",
      };
    }
    if (req.pageToken === "token-p2") {
      return {
        questions: decided.slice(50, 100),
        total: 102,
        nextPageToken: "token-p3",
      };
    }
    return {
      questions: [q101, q102],
      total: 102,
      nextPageToken: "",
    };
  };

  const { clients, watch } = server(data);
  const store = createStore(clients, 10);
  const stop = store.start();
  watch.push({ wishId: "", changes: everything });
  await until(store, (s) => s.loaded);

  store.open(wishId, [Change.QUESTION]);
  await until(
    store,
    (s) =>
      s.details[wishId]?.questions.some((q) => q.code === "Q101") &&
      s.details[wishId]?.questions.some((q) => q.code === "Q102"),
  );

  // Answer Q101 on page 3: putQuestion + changed(QUESTION, WISH) reads up to the furthest known question (page 3),
  // keeping Q101 decided with its note and Q102 open.
  q101.answer = { choice: 2, note: "Option B chosen" };
  store.putQuestion({ ...q101 });
  await store.changed(wishId, [Change.QUESTION, Change.WISH]);

  let questions = store.getState().details[wishId].questions;
  const shown101 = questions.find((q) => q.code === "Q101");
  const shown102 = questions.find((q) => q.code === "Q102");
  assert.equal(shown101?.answer?.choice, 2);
  assert.equal(shown101?.answer?.note, "Option B chosen");
  assert.equal(shown102?.answer, undefined);
  assert.equal(questions.filter((q) => !q.answer).length, 1);

  // Answer Q102 on page 3: both Q101 and Q102 remain in the store as decided, with 0 open questions left.
  q102.answer = { choice: 1, note: "Proceed now" };
  store.putQuestion({ ...q102 });
  await store.changed(wishId, [Change.QUESTION, Change.WISH]);

  questions = store.getState().details[wishId].questions;
  stop();
  assert.equal(
    questions.find((q) => q.code === "Q101")?.answer?.note,
    "Option B chosen",
  );
  assert.equal(
    questions.find((q) => q.code === "Q102")?.answer?.note,
    "Proceed now",
  );
  assert.equal(questions.filter((q) => !q.answer).length, 0);
});

test("a wish view with a followed running task keeps at most 2 concurrent long-lived streams", async () => {
  const data = sample();
  data.running = true;
  const watch = channel();
  let activeStreams = 0;
  let peakStreams = 0;
  const activeByPath = new Map();

  const trackStream = async function* (path, inner) {
    activeStreams++;
    activeByPath.set(path, (activeByPath.get(path) ?? 0) + 1);
    if (activeStreams > peakStreams) peakStreams = activeStreams;
    try {
      yield* inner;
    } finally {
      activeStreams--;
      const next = (activeByPath.get(path) ?? 1) - 1;
      if (next <= 0) activeByPath.delete(path);
      else activeByPath.set(path, next);
    }
  };

  const transport = createRouterTransport(({ service }) => {
    service(WishService, {
      list: () => ({ wishes: data.wishes }),
      watch: () =>
        trackStream("/djinn.plan.v1.WishService/Watch", watch.stream()),
    });
    service(ProjectService, {
      list: () => ({ projects: data.projects }),
    });
    service(LeadService, {
      get: () => ({ lead: data.lead }),
    });
    service(TaskService, {
      list: () => ({
        tasks: data.tasks,
        total: data.tasks.length,
        nextPageToken: "",
      }),
      get: (req) => ({ task: data.tasks.find((t) => t.id === req.taskId) }),
      watch: (req) =>
        trackStream(
          "/djinn.plan.v1.TaskService/Watch",
          (async function* () {
            for (const event of data.events.filter((e) => e.seq > req.afterSeq))
              yield { event };
            await new Promise(() => {});
          })(),
        ),
    });
    service(QuestionService, {
      list: () => ({
        questions: data.questions,
        total: data.questions.length,
        nextPageToken: "",
      }),
    });
    service(BlockService, {
      list: () => ({
        blocks: data.blocks,
        total: data.blocks.length,
        nextPageToken: "",
      }),
    });
    service(InboxService, {
      list: () => ({ items: data.inbox }),
      sources: () => ({ sources: data.sources }),
    });
  });

  const store = createStore(createClients(transport), 10);
  const stop = store.start();
  watch.push({ wishId: "", changes: everything });
  await until(store, (s) => s.loaded);

  store.open(wishId);
  await until(store, (s) => s.details[wishId]?.loaded);

  const release = store.follow(taskId);
  await until(store, (s) => (s.events[taskId]?.length ?? 0) === 2);

  assert.equal(activeStreams, 2);
  assert.ok(
    peakStreams <= 4,
    `peak concurrent streams ${peakStreams} exceeded 4`,
  );
  assert.equal(activeByPath.get("/djinn.plan.v1.WishService/Watch"), 1);
  assert.equal(activeByPath.get("/djinn.plan.v1.TaskService/Watch"), 1);
  assert.equal(activeByPath.get("/djinn.ui.v1.UiService/WatchShow") ?? 0, 0);
  assert.equal(activeByPath.get("/djinn.ui.v1.UiService/WatchUpdate") ?? 0, 0);
  assert.equal(activeByPath.get("/djinn.load.v1.LoadService/Watch") ?? 0, 0);

  release();
  stop();
});

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
} = await bundle(
  "store",
  `export { createStore } from "@/src/data/store.ts";
export { createClients } from "@/src/data/client.ts";
export { createRouterTransport } from "@connectrpc/connect";
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
    service(TaskService, {
      list: () => (count("tasks"), { tasks: data.tasks }),
      watch: async function* (req) {
        count("events");
        for (const event of data.events.filter((e) => e.seq > req.afterSeq))
          yield { event };
      },
    });
    service(QuestionService, {
      list: () => (count("questions"), { questions: data.questions }),
    });
    service(BlockService, {
      list: () => (count("blocks"), { blocks: data.blocks }),
    });
    service(InboxService, {
      list: () => (count("inbox"), { items: data.inbox }),
    });
  });
  return { clients: createClients(transport), reads, watch };
}

function sample() {
  return {
    wishes: [{ id: wishId, title: "Ship the lamp", rank: 1, state: 1 }],
    projects: [{ id: "p1", name: "lamp" }],
    tasks: [{ id: taskId, wishId, code: "T01", title: "Polish", status: 2 }],
    questions: [{ id: "q1", wishId, code: "Q01", text: "Oil?" }],
    blocks: [{ id: "b1", wishId, title: "Lexicon", content: "A wick." }],
    inbox: [{ id: "i1", source: "babysit-mr", text: "Babysit !12" }],
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

test("a change of the inbox reads it again, and nothing else", async () => {
  const data = sample();
  const { clients, reads, watch } = server(data);
  const store = createStore(clients, 10);
  const stop = store.start();
  watch.push({ wishId: "", changes: [...everything, Change.INBOX] });
  const state = await until(store, (s) => s.loaded && s.inbox.length === 1);
  assert.equal(state.inbox[0].source, "babysit-mr");
  const before = { ...reads };
  data.inbox = [];
  watch.push({ wishId: "", changes: [Change.INBOX] });
  await until(store, (s) => s.inbox.length === 0);
  stop();
  assert.equal(reads.inbox, before.inbox + 1);
  assert.equal(reads.wishes, before.wishes);
  assert.equal(reads.projects, before.projects);
});

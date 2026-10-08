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
  // Only the active ones are dragged to a new rank.
  assert.equal(html.match(/draggable="true"/g).length, 2);
  assert.match(html, /mission-nav wish-nav selected/);
  assert.match(html, />lamp</);
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
  assert.match(open, /<strong>A<\/strong><p>Olive<\/p>/);
  assert.match(open, /<strong>B<\/strong><p>Paraffin<\/p>/);
  assert.match(open, /<strong>A<\/strong>, for the smell\./);
  assert.match(open, /Confirm this choice/);

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
  assert.match(html, /Trim the wick/);
  assert.match(html, /<strong>wick<\/strong> carries the oil\./);
  assert.match(html, /1 decision recorded/);
  // Nothing waits for an answer: no open question card.
  assert.doesNotMatch(html, /Confirm the answer/);
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
  // What waits, what runs, the latest decisions.
  assert.match(html, /W2 waits for your answer to Q01 before it may edit\./);
  assert.match(html, /Who runs now/);
  assert.match(html, /Taste the oils/);
  assert.match(html, /Latest decisions/);
  assert.match(html, /Tonight\?/);
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
  assert.doesNotMatch(html, /Your move|Who runs now|Latest decisions|Spent/);
  assert.match(html, /Nothing waits for you, and nothing runs\./);
});

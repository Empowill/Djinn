// The screens on the services, rendered to text from a store filled by in-memory services. Run with
// `go tool task test-ui`. Node reports English: the texts asserted are the English ones.
import test from "node:test";
import assert from "node:assert/strict";

import { bundle } from "./bundle.mjs";

const s = await bundle(
  "screens",
  `export {
  createElement,
  act,
  __CLIENT_INTERNALS_DO_NOT_USE_OR_WARN_USERS_THEY_CANNOT_UPGRADE as reactInternals,
} from "react";
export { createRoot } from "react-dom/client";
export { renderToStaticMarkup } from "react-dom/server";
export { createRouterTransport } from "@connectrpc/connect";
export { createDjinn, DjinnProvider } from "@/src/data/djinn.tsx";
export { WishSidebar } from "@/src/wish-sidebar.tsx";
export { WishQuestion } from "@/src/wish-question.tsx";
export { WishView } from "@/src/wish-view.tsx";
export { WishTask, promptPreview } from "@/src/wish-task.tsx";
export {
  azimaTime,
  shortModel,
  effectivePushStrategy,
  azimaBranchName,
} from "@/src/data/format.ts";
export { FlightPlan } from "@/src/flight-plan.tsx";
export { attentionOf, AttentionBar } from "@/src/attention.tsx";
export { TaskSections } from "@/src/task-tabs.tsx";
export {
  azimaGroups,
  azimaRank,
  draftAzimas,
  flightPlan,
  movingTasks,
  taskDealtWith,
  waitingTasks,
} from "@/src/data/flight.ts";
export { AzimaCard, azimaFinished, DraftAzimaCard } from "@/src/azima.tsx";
export {
  FolderField,
  ProjectChecks,
  ProjectPanel,
  ShortcutField,
} from "@/src/wish-dialogs.tsx";
export {
  LastPushes,
  LeadButton,
  LeadMenu,
  MainMerges,
  PushStrategySelector,
  WishDescription,
  recordedAgent,
} from "@/src/wish-head.tsx";
export { UpdateBannerView } from "@/src/update-banner.tsx";
export { InstallStep } from "@/gen/ts/ui/v1/ui_pb.ts";
export { memory, resourcesDetail } from "@/src/usage.tsx";
export { TilasmList } from "@/src/tilasms.tsx";
export { AgentBlocks } from "@/src/agent-blocks.tsx";
export { MarkdownBody } from "@/src/markdown-body.tsx";
export { openDjinnLink, parseDjinnLink } from "@/src/data/links.ts";
export { ConnectError, Code } from "@connectrpc/connect";
export { readDrop } from "@/src/data/tilasms.ts";
export { TilasmService } from "@/gen/ts/plan/v1/tilasm_pb.ts";
export * from "@/gen/ts/plan/v1/plan_pb.ts";`,
);
const h = s.createElement;

// live renders a component whose state lives between renders, so that a test types and clicks with no DOM: it calls
// the component with a dispatcher that keeps its useState, as React does, and walks the elements it returns. The
// components it reaches use no other hook.
function live(component, props) {
  const states = [];
  const render = () => {
    const internals = s.reactInternals;
    const before = internals.H;
    let at = 0;
    internals.H = {
      useState(initial) {
        const i = at++;
        if (!(i in states))
          states[i] = typeof initial === "function" ? initial() : initial;
        const set = (value) => {
          states[i] = typeof value === "function" ? value(states[i]) : value;
        };
        return [states[i], set];
      },
      useEffect() {},
    };
    try {
      const fn = typeof component === "function" ? component : component.type;
      return fn(props);
    } finally {
      internals.H = before;
    }
  };
  // The elements of the tree of tag name, or whose class has name, in order.
  const all = (name) => {
    const found = [];
    const walk = (node) => {
      if (Array.isArray(node)) node.forEach(walk);
      else if (node && typeof node === "object" && node.props) {
        const classes = String(node.props.className ?? "").split(" ");
        if (node.type === name || classes.includes(name)) found.push(node);
        walk(node.props.children);
      }
    };
    walk(render());
    return found;
  };
  const one = (name) => {
    const found = all(name);
    assert.equal(found.length, 1, `one ${name}`);
    return found[0].props;
  };
  return {
    html: () => s.renderToStaticMarkup(render()),
    all,
    one,
    // Its props change, as when djinn sends the question again.
    update(next) {
      props = { ...props, ...next };
    },
    // The handlers' promises settle.
    settle: () => new Promise((resolve) => setImmediate(resolve)),
  };
}

const wish = (id, title, state, rank, extra = {}) => ({
  id,
  title,
  state,
  rank,
  projectIds: [],
  allowances: [],
  pushes: [],
  mains: [],
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
      counts: { w1: { questions: 2, running: 1, watching: 1 } },
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
  // A watcher is counted apart: it waits for its command to print, it does not work.
  assert.match(counted, /count-pill tone-watching" title="1 watching"/);
  // A wish whose only process is a watcher is watching, not running.
  const watched = s.renderToStaticMarkup(
    h(s.WishSidebar, {
      wishes: [wish("w1", "Ship the lamp", s.WishState.ACTIVE, 1)],
      projects: [],
      counts: { w1: { questions: 0, running: 0, watching: 1 } },
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
  assert.match(watched, /wish-tone tone-watching/);
  assert.doesNotMatch(watched, /running/);
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
  // Two gestures, no third: the lamp is rubbed on the recommended option, preselected.
  assert.doesNotMatch(open, /Confirm/);
  assert.match(
    open,
    /<button class="option selected" aria-pressed="true"><strong class="option-letter">A</,
  );
  assert.match(
    open,
    /title="Answer with the selected option and your note"><svg[^]*?Rub the lamp<\/button>/,
  );
  // The recommendation is boxed first, its option marked.
  assert.ok(open.indexOf("Recommendation · A") < open.indexOf("Olive"));
  assert.match(open, /Olive<\/p><span class="option-recommended">Recommended/);
  // Nothing waits for it, nothing said before what: it can wait, grey.
  assert.match(open, /question-card open can-wait/);
  assert.match(open, /status-badge tone-later[^>]*>.*<span>Can wait<\/span>/);
  // Needed before the merge: orange, under those words.
  const before = s.renderToStaticMarkup(
    h(s.WishQuestion, {
      question: {
        id: "q1",
        code: "Q01",
        text: "Which oil?",
        options: ["Olive", "Paraffin"],
        recommendation: "",
        context: "",
        before: "before the merge",
      },
      onAnswer: async () => {},
    }),
  );
  assert.match(
    before,
    /status-badge tone-waiting[^>]*>.*<span>before the merge<\/span>/,
  );
  assert.doesNotMatch(before, /can-wait|is-blocking/);

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
  // Enlighten on the left, then the lamp; B, recommended, is the one rubbed.
  assert.ok(lamp.indexOf("Enlighten me") < lamp.indexOf("Rub the lamp"));
  assert.match(
    lamp,
    /<button class="option selected" aria-pressed="true"><strong class="option-letter">B</,
  );
  assert.match(lamp, /aria-pressed="true"[^>]*>.*Read<\/button>/);
  assert.match(lamp, /Blocks W2/);
  assert.match(lamp, /What is at stake<\/h4>.*<h2>Cost<\/h2>/);
  assert.match(lamp, /question-card open is-blocking/);
  // No option named: nothing selected, the lamp waits for your pick.
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
  assert.doesNotMatch(vague, /option selected/);
  assert.match(
    vague,
    /title="Pick an option first" disabled=""><svg[^]*?Rub the lamp<\/button>/,
  );

  // Being investigated, then revised: one line out of the way, which opens on its state, the note, the badge, the
  // history folded.
  const card = live(s.WishQuestion, {
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
  });
  const folded = card.html();
  assert.match(folded, /question-card investigating {2}folded/);
  assert.match(
    folded,
    /<button class="question-heading question-fold" aria-expanded="false"><svg[^>]*lucide-lightbulb[^]*?<span class="question-id">Q01<\/span><span class="question-fold-text">Which oil\?<\/span>.*Being investigated.*<span class="question-fold-note">You asked: And the price\?<\/span>/,
  );
  assert.doesNotMatch(folded, /question-inner|Olive|<textarea/);
  card.one("question-fold").onClick();
  const digging = card.html();
  assert.doesNotMatch(digging, /folded/);
  assert.match(
    digging,
    /<button class="question-heading" aria-expanded="true">/,
  );
  assert.match(digging, /You asked to find out more: And the price\?/);
  assert.match(digging, /Revised/);
  assert.match(
    digging,
    /<details class="question-rounds"><summary>.*History: 3 rounds/,
  );
  assert.match(digging, /Recommended before: B: brighter\./);
  // Already asked: no second request.
  assert.doesNotMatch(digging, /lamp-enlighten/);
  // A click on its heading folds it again.
  card.one("question-heading").onClick();
  assert.match(card.html(), /question-fold/);
  // The investigator revises it: it waits for an answer again, open by itself.
  card.update({
    question: {
      id: "q1",
      code: "Q01",
      text: "Which oil?",
      options: ["Olive", "Paraffin"],
      recommendation: "B: it lasts longer.",
      revision: 2,
      rounds: [
        { kind: s.RoundKind.ENLIGHTEN, note: "Burn time?" },
        { kind: s.RoundKind.REVISE, recommendation: "B: brighter." },
        { kind: s.RoundKind.ENLIGHTEN, note: "And the price?" },
        { kind: s.RoundKind.REVISE, recommendation: "A: it smells good." },
      ],
    },
  });
  const revised = card.html();
  assert.match(revised, /question-card open can-wait"/);
  assert.doesNotMatch(revised, /question-fold|Being investigated/);
  assert.match(revised, /<div class="question-heading">/);
  assert.match(revised, /Paraffin.*<textarea/);
  assert.match(revised, /lamp-enlighten/);

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

test("Enlighten me sends the answer's note in one click: no second field", async () => {
  const sent = [];
  const card = live(s.WishQuestion, {
    question: {
      id: "q1",
      code: "Q01",
      text: "Which oil?",
      options: ["Olive", "Paraffin"],
      recommendation: "A: it smells good.",
      rounds: [],
    },
    onAnswer: async () => assert.fail("Enlighten me answers nothing"),
    onEnlighten: async (note) => {
      sent.push(note);
    },
  });
  // One field, for both gestures; Enlighten me opens nothing.
  const open = card.html();
  assert.equal(open.match(/<textarea/g).length, 1);
  assert.match(
    open,
    /placeholder="A note with your choice, or what to look into for Enlighten me \(optional\)"/,
  );
  assert.match(
    open,
    /<button class="button secondary lamp-enlighten" title="Find out more before deciding: the lead looks into what your note asks, then revises the question">/,
  );
  assert.doesNotMatch(open, /aria-expanded|Ask the lead to investigate/);

  // Typed in the note, sent at the first click, as typed; the note is cleared.
  card.one("textarea").onChange({
    target: { value: "  How long does each burn?\nAnd the price?  " },
  });
  assert.match(card.html(), /<textarea[^>]*> {2}How long does each burn\?/);
  card.one("lamp-enlighten").onClick();
  await card.settle();
  assert.deepEqual(sent, ["How long does each burn?\nAnd the price?"]);
  assert.match(card.html(), /<textarea[^>]*><\/textarea>/);
  assert.equal(card.all("textarea").length, 1);

  // Nothing typed: it asks to investigate in general.
  card.one("lamp-enlighten").onClick();
  await card.settle();
  assert.deepEqual(sent.at(-1), "");

  // Refused: the note stays, to try again.
  const kept = live(s.WishQuestion, {
    question: { id: "q2", code: "Q02", text: "Which wick?", options: [] },
    onAnswer: async () => {},
    onEnlighten: async () => {
      throw new Error("refused");
    },
  });
  assert.match(
    kept.html(),
    /placeholder="Your answer, or what to look into for Enlighten me"/,
  );
  kept.one("textarea").onChange({ target: { value: "Cotton?" } });
  kept.one("lamp-enlighten").onClick();
  await kept.settle();
  assert.match(kept.html(), /<textarea[^>]*>Cotton\?<\/textarea>/);
});

test("a question of a wish without a lead session says no lead is told: open, then answered", () => {
  const props = (noLead, extra) =>
    s.renderToStaticMarkup(
      h(s.WishQuestion, {
        question: {
          id: "q1",
          code: "Q01",
          text: "Which oil?",
          options: ["Olive", "Paraffin"],
          recommendation: "A: it smells good.",
          ...extra,
        },
        expanded: true,
        noLead,
        onAnswer: async () => {},
      }),
    );
  const told = props(false, {});
  assert.match(told, /Your answer goes to the lead and the workers\./);
  assert.doesNotMatch(told, /No lead to tell/);
  const open = props(true, {});
  assert.match(
    open,
    /No lead to tell: your answer goes to the workers and waits in the wish&#x27;s brief\./,
  );
  assert.doesNotMatch(open, /goes to the lead/);
  const answer = { answer: { choice: s.Choice.A, note: "" } };
  assert.doesNotMatch(props(false, answer), /No lead to tell/);
  assert.match(
    props(true, answer),
    /<p class="no-lead">No lead to tell: the answer waits in the wish&#x27;s brief\.<\/p>/,
  );
});

test("a running worker pauses from its card, a paused one resumes; none where djinn cannot pause", () => {
  const card = (status, provider, onHold) =>
    s.renderToStaticMarkup(
      h(s.WishTask, {
        task: {
          id: "t1",
          code: "W1",
          title: "Trim the wick",
          status,
          provider,
        },
        onStop() {},
        async onSend() {},
        onHold,
      }),
    );
  const hold = () => {};
  for (const provider of [
    s.Provider.CLAUDE,
    s.Provider.FAKE,
    s.Provider.WATCH,
  ]) {
    const running = card(s.TaskStatus.RUNNING, provider, hold);
    assert.match(
      running,
      /title="Pause the worker: it holds where it is and frees its slot" aria-label="Pause the worker"/,
      s.Provider[provider],
    );
    assert.doesNotMatch(running, /Resume the worker/);
    const paused = card(s.TaskStatus.PAUSED, provider, hold);
    assert.match(
      paused,
      /title="Resume the worker where it was" aria-label="Resume the worker"/,
    );
    assert.doesNotMatch(paused, /aria-label="Pause the worker"/);
  }
  // Nothing to pause in a task no worker runs.
  for (const status of [
    s.TaskStatus.PENDING,
    s.TaskStatus.WAITING,
    s.TaskStatus.RESUMING,
    s.TaskStatus.DONE,
    s.TaskStatus.FAILED,
  ])
    assert.doesNotMatch(
      card(status, s.Provider.CLAUDE, hold),
      /Pause the worker|Resume the worker/,
      s.TaskStatus[status],
    );
  // Without onHold (Windows): no button, stop stays.
  const windows = card(s.TaskStatus.RUNNING, s.Provider.CLAUDE, undefined);
  assert.doesNotMatch(windows, /Pause the worker|Resume the worker/);
  assert.match(windows, /aria-label="Stop the worker"/);
});

test("a wish's screen puts its questions first, proposes to grant it when ready, and leaves its blocks to the agents' tab", async () => {
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
            continuing: true,
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
          {
            id: "b2",
            wishId,
            kind: "decision",
            title: "Olive oil only",
            content: "It smells good.",
          },
          { id: "b3", wishId, kind: "log", title: "", content: "W1 started." },
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
  // No notes: the blocks for agents have the last tab, discreet, with their count: neither the decision nor the log.
  assert.doesNotMatch(
    html,
    /Lexicon|wick<\/strong> carries|id="block-b1"|>Notes</,
  );
  assert.match(
    html,
    /id="view-tab-tilasms".*role="tab" id="view-tab-agents" aria-selected="false" class="discreet">For agents<span class="count">1<\/span><\/button><\/div>/,
  );
  // The answered question and the decision block are in the Decisions tab, apart, with no mark.
  assert.match(
    html,
    /role="tab" id="view-tab-decisions" aria-selected="false" class="">Decisions<span class="count">2<\/span>/,
  );
  assert.doesNotMatch(html, /Light it tonight\?/);
  // Nothing waits for an answer: no yes/no card waiting.
  assert.doesNotMatch(html, /placeholder="Your answer"/);
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
  // Folded to one line, what you asked with it.
  assert.match(html, /question-card investigating {2}folded/);
  assert.match(html, /You asked: Burn time\?/);
  // No mark on what the wish shows, nor on the folded question; the journal is folded.
  assert.doesNotMatch(html, /Approve as it is|Mark read/);
  assert.match(html, /class="fold-heading" aria-expanded="false"/);
});

test("the For agents tab folds each block under its title and kind, with no mark", () => {
  const html = s.renderToStaticMarkup(
    h(s.AgentBlocks, {
      blocks: [
        {
          id: "b1",
          wishId: "w",
          taskId: "t1",
          kind: "report",
          title: "Where the oil is",
          content: "In **the cellar**.",
        },
        { id: "b2", wishId: "w", kind: "hand-off", title: "", content: "x" },
      ],
      codes: new Map([["t1", "W1"]]),
    }),
  );
  assert.match(html, /role="tabpanel" aria-labelledby="view-tab-agents"/);
  assert.match(html, /Nothing here waits for you: what does is a question\./);
  assert.match(
    html,
    /id="block-b1"><button class="fold-heading" aria-expanded="false">.*<h3>Where the oil is<\/h3><span class="eyebrow">report · About W1<\/span><\/button><\/article>/,
  );
  assert.match(
    html,
    /<h3>\(untitled\)<\/h3><span class="eyebrow">hand-off<\/span>/,
  );
  // Folded: the body is not rendered until opened (the e2e opens one).
  assert.doesNotMatch(html, /the cellar/);
  assert.doesNotMatch(html, /Mark read|Approve as it is|mark-toggle/);
  const none = s.renderToStaticMarkup(
    h(s.AgentBlocks, { blocks: [], codes: new Map() }),
  );
  assert.match(none, /No block for agents yet\./);
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

test("shortModel formats model identifiers cleanly", () => {
  assert.equal(s.shortModel("claude-sonnet-5-5-20250929"), "sonnet 5.5");
  assert.equal(s.shortModel("gemini-3.8-flash-high"), "gemini 3.8 flash");
  assert.equal(s.shortModel("gpt-5.5"), "gpt-5.5");
  assert.equal(s.shortModel("claude-3-7-sonnet"), "sonnet 3.7");
  assert.equal(s.shortModel("claude-opus-4-0"), "opus 4.0");
  assert.equal(s.shortModel("gemini-pro"), "gemini pro");
  assert.equal(s.shortModel("anthropic/claude-3.5-sonnet"), "sonnet 3.5");
});

test("a task card shows the model chosen for the worker; watchers do not show it", () => {
  const card = (task) =>
    s.renderToStaticMarkup(h(s.WishTask, { task, onStop() {} }));

  // Regular task with model
  const claude = card({
    id: "t1",
    code: "W1",
    title: "Trim the wick",
    status: s.TaskStatus.RUNNING,
    model: "claude-sonnet-5-5-20250929",
  });
  assert.match(
    claude,
    /<span class="task-model" title="claude-sonnet-5-5-20250929">sonnet 5\.5<\/span>/,
  );

  // Question worker task with model
  const questionWorker = card({
    id: "t2",
    code: "W2",
    title: "Investigate Q01",
    status: s.TaskStatus.RUNNING,
    provider: s.Provider.ANTIGRAVITY,
    model: "gemini-3.8-flash-high",
    question: "q1",
  });
  assert.match(
    questionWorker,
    /<span class="task-model" title="gemini-3.8-flash-high">gemini 3\.8 flash<\/span>/,
  );

  // Correction worker task with model
  const correctionWorker = card({
    id: "t3",
    code: "W3",
    title: "Correct integration failure",
    status: s.TaskStatus.RUNNING,
    provider: s.Provider.CODEX,
    model: "gpt-5.5",
    correction: { attempts: 1 },
  });
  assert.match(
    correctionWorker,
    /<span class="task-model" title="gpt-5.5">gpt-5\.5<\/span>/,
  );

  // Watcher task does not show model even if set
  const watcher = card({
    id: "t4",
    code: "W4",
    title: "Watch checks",
    status: s.TaskStatus.RUNNING,
    provider: s.Provider.WATCH,
    model: "claude-sonnet-5-5",
  });
  assert.doesNotMatch(watcher, /task-model/);

  // Task with empty model does not show badge
  const noModel = card({
    id: "t5",
    code: "W5",
    title: "No model",
    status: s.TaskStatus.RUNNING,
    model: "",
  });
  assert.doesNotMatch(noModel, /task-model/);
});

test("a task card shows its prompt on hover, and opened shows prompt and no logs until the button", () => {
  const task = {
    id: "t1",
    code: "W1",
    title: "Trim the wick of the brass lamp",
    prompt:
      "First instruction line\nSecond instruction line\nThird line\nFourth line",
    status: s.TaskStatus.DONE,
  };

  // Unopened card: title has no native title attribute; tooltip contains full title and prompt preview.
  const unopened = s.renderToStaticMarkup(h(s.WishTask, { task, onStop() {} }));
  assert.match(
    unopened,
    /<strong class="task-title">Trim the wick of the brass lamp<\/strong>/,
  );
  assert.doesNotMatch(unopened, /<strong[^>]*title=/);
  assert.match(unopened, /<span class="task-tooltip" role="tooltip">/);
  assert.match(
    unopened,
    /<span class="task-tooltip-title">Trim the wick of the brass lamp<\/span>/,
  );
  assert.match(
    unopened,
    /<span class="task-tooltip-prompt">First instruction line\nSecond instruction line\nThird line…<\/span>/,
  );
  assert.doesNotMatch(unopened, /wish-task-body/);

  // Opened card: shows full title, prompt in Markdown with fold, and See logs button without logs.
  const opened = s.renderToStaticMarkup(
    h(s.WishTask, { task, focused: true, onStop() {} }),
  );
  assert.match(opened, /wish-task-prompt-section/);
  assert.match(opened, /wish-task-prompt-body/);
  assert.match(opened, /First instruction line/);
  assert.match(opened, /wish-task-logs-button/);
  assert.match(opened, /See logs/);
  // Logs are deferred: event container is not rendered until logs are toggled.
  assert.doesNotMatch(opened, /class="wish-task-events"/);
});

test("promptPreview truncates long prompts to a few lines with an ellipsis", () => {
  assert.equal(s.promptPreview(""), "");
  assert.equal(s.promptPreview("Short prompt"), "Short prompt");
  assert.equal(
    s.promptPreview("Line 1\nLine 2\nLine 3\nLine 4"),
    "Line 1\nLine 2\nLine 3…",
  );
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

test("the Tasks tab lists what moves or waits by status", () => {
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
  assert.deepEqual(order, ["W8", "W5", "W3", "W6", "W1"]);
  assert.ok(html.indexOf("Moving or waiting") < html.indexOf("Task W8"));
  assert.match(html, /Moving or waiting<span class="count">5<\/span>/);
  assert.doesNotMatch(html, /tasks-finished/);
  assert.doesNotMatch(html, /Finished/);
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
  const groups = s.azimaGroups(tasks);
  const sections = (focus = "") =>
    s.renderToStaticMarkup(
      h(s.TaskSections, {
        moving: s.movingTasks(tasks),
        azimas: groups.filter((x) => !s.azimaFinished(x.azima)),
        doneAzimas: groups.filter((x) => s.azimaFinished(x.azima)),
        fold: `test-${focus}`,
        showDone: focus === "T1",
        renderAzima: ({ azima, parts }) =>
          h(s.AzimaCard, {
            key: azima.id,
            azima,
            parts,
            tasks: byId,
            render: card,
            focus,
          }),
        render: card,
      }),
    );
  const codes = (html) =>
    [...html.matchAll(/<span class="agent-code">([TW]\d+)</g)].map((m) => m[1]);
  const html = sections();
  // Work of no azima moves or waits on its own; T2, under way, opened on its parts (running, planned), its finished
  // one folded; T10 waits; T1, done, folded and not rendered.
  assert.deepEqual(codes(html), ["W4", "T2", "W2", "W3", "T10"]);
  const folds = [
    ...html.matchAll(
      /<button type="button" class="fold-line" aria-expanded="false">.*?<\/button>/g,
    ),
  ].map((m) => m[0].replace(/<[^>]+>/g, ""));
  assert.deepEqual(folds, ["Show 1 finished", "Show 1 finished"]);
  // A link to a finished part unfolds its azima's; a link to a done azima, the done ones.
  assert.deepEqual(codes(sections("W1")), [
    "W4",
    "T2",
    "W2",
    "W3",
    "W1",
    "T10",
  ]);
  assert.match(
    sections("W1"),
    /aria-expanded="true">.*?Hide the finished ones</,
  );
  assert.deepEqual(codes(sections("T1")), [
    "W4",
    "T2",
    "W2",
    "W3",
    "T10",
    "T1",
  ]);
  assert.match(html, /Moving or waiting<span class="count">1<\/span>/);
  assert.match(html, /Azimas<span class="count">3<\/span>/);
  assert.doesNotMatch(html, /tasks-finished/);
  assert.doesNotMatch(html, /Finished/);
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
    /id="view-tab-tasks"[^>]*>Tasks<span class="count">2<\/span>/,
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
    service(s.InboxService, {
      list: () => ({ items: [item] }),
      sources: () => ({
        sources: [{ name: "gong/babysit-mr", skill: "babysit-mr" }],
      }),
    });
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
  // The sources fold under the items.
  assert.match(
    html,
    /<details class="inbox-sources-fold"><summary>Sources \(1\)<\/summary>/,
  );
});

test("the empty inbox lists the sources the skills declare, each with Plug in or Unplug; without any, it is hidden", async () => {
  const lamp = wish(
    "01a11833-a440-7479-a067-52615c91da71",
    "Ship the lamp",
    s.WishState.ACTIVE,
    1,
  );
  const render = async (sources) => {
    const transport = s.createRouterTransport(({ service }) => {
      service(s.WishService, { list: () => ({ wishes: [lamp] }) });
      service(s.ProjectService, { list: () => ({ projects: [] }) });
      service(s.InboxService, {
        list: () => ({ items: [] }),
        sources: () => ({ sources }),
      });
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
    return s.renderToStaticMarkup(
      h(
        s.DjinnProvider,
        { djinn },
        h(s.FlightPlan, { wishes: [lamp], onOpen() {}, onToast() {} }),
      ),
    );
  };

  assert.doesNotMatch(await render([]), /Inbox/);

  const html = await render([
    {
      name: "djinn/babysit-pr",
      skill: "babysit-pr",
      project: "djinn",
      watch: "sh .agents/skills/babysit-pr/inbox.sh",
      every: "5m0s",
    },
    {
      name: "gong/mentions",
      skill: "mentions",
      project: "gong",
      watch: "mentions --new",
      every: "1h0m0s",
      plugged: true,
    },
    {
      name: "gong/noisy",
      skill: "noisy",
      project: "gong",
      error: "metadata.djinn.source.every: 10s is too often",
    },
  ]);
  assert.match(html, /Inbox/);
  assert.match(
    html,
    /Djinn reads only the sources you plug in, on this machine/,
  );
  const rows = html
    .split('<li class="inbox-source-row">')
    .slice(1)
    .map((row) => row.slice(0, row.indexOf("</li>")));
  assert.equal(rows.length, 3);
  assert.match(rows[0], /djinn\/babysit-pr/);
  assert.match(
    rows[0],
    /<code>sh .agents\/skills\/babysit-pr\/inbox.sh<\/code>/,
  );
  assert.match(rows[0], /Unplugged: runs nothing/);
  assert.match(rows[0], />Plug in<\/button>/);
  assert.match(rows[1], /Plugged in, every 1h/);
  assert.match(rows[1], />Unplug<\/button>/);
  // A source that cannot run says why, and offers nothing to plug in.
  assert.match(rows[2], /10s is too often/);
  assert.doesNotMatch(rows[2], /<button/);
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
  // The djinn's mark heads it; the brass Update sits beside the title, the notes a quiet link after it.
  assert.match(
    release,
    /^<div class="update-banner" role="status"><section class="update-banner-part"><div class="update-banner-head"><div class="update-banner-title"><span class="brand small"><span class="brand-mark"><img/,
  );
  assert.match(
    release,
    /A new version of Djinn is ready<\/span><\/span><\/div><div class="update-banner-actions"><button type="button" class="update-banner-primary">Update<\/button><a /,
  );
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
  // After a restart, the terminals that did not start again: the mark heads them, Dismiss in violet beside the title.
  const lost = banner({ ready: "", notesUrl: "", notResumed: ["npm run dev"] });
  assert.match(
    lost,
    /<span class="brand small">.*<div class="update-banner-actions"><button type="button" class="update-banner-dismiss">Dismiss<\/button><\/div><\/div><div class="update-banner-body"><ul class="update-banner-terminals"><li>npm run dev<\/li>/,
  );
  // Nothing waits: no banner, whatever the notes.
  assert.equal(banner({ ready: "", notesUrl: "https://example.com" }), "");
});

const stamp = (iso) => ({
  seconds: BigInt(Date.parse(iso) / 1000),
  nanos: 0,
});

const tilasm = (id, code, title, extra = {}) => ({
  id,
  wishId: "w1",
  code,
  title,
  author: "lead",
  cites: [],
  versions: [
    {
      number: 1,
      author: "lead",
      files: 3,
      size: 2048n,
      restoredFrom: 0,
      createTime: stamp("2026-10-09T10:00:00Z"),
    },
  ],
  updateTime: stamp("2026-10-09T10:00:00Z"),
  ...extra,
});

test("the Tilasms tab lists each tilasm with its code, title, author, date and what it cites, and opens one in a sandboxed frame", () => {
  const model = tilasm(
    "01a1223a-ae45-728f-8c37-c005eee91edb",
    "L01",
    "The objects in the database",
    {
      cites: ["t29", "gone-1234-5678"],
      author: "W12",
      versions: [
        {
          number: 1,
          author: "W12",
          files: 3,
          size: 2048n,
          restoredFrom: 0,
          createTime: stamp("2026-10-09T10:00:00Z"),
        },
        {
          number: 2,
          author: "lead",
          files: 1,
          size: 512n,
          restoredFrom: 0,
          createTime: stamp("2026-10-09T11:00:00Z"),
        },
        {
          number: 3,
          author: "developer",
          files: 3,
          size: 2048n,
          restoredFrom: 1,
          createTime: stamp("2026-10-09T12:00:00Z"),
        },
      ],
    },
  );
  const flows = tilasm("01a1223a-ae45-728f-8c37-c005eee91edc", "L02", "Flows");
  const props = {
    tilasms: [model, flows],
    count: 2,
    codes: new Map([["t29", "T29"]]),
    search: "",
    onSearch() {},
    onOpen() {},
    history: "",
    onHistory() {},
    onRestore() {},
    onExport() {},
    onDrop() {},
  };
  const html = s.renderToStaticMarkup(h(s.TilasmList, props));
  assert.match(html, /role="tabpanel" aria-labelledby="view-tab-tilasms"/);
  assert.match(
    html,
    /placeholder="Search the tilasms \(talismans\) of this wish"/,
  );
  assert.match(
    html,
    /Drop a folder with an index.html, or a .zip, to add a tilasm/,
  );
  // A row per tilasm, by code: its title opens it; who made it and when; what it cites, by code.
  assert.ok(html.indexOf("L01") < html.indexOf("L02"));
  assert.match(
    html,
    /tilasm-title"[^>]*>The objects in the database<\/button>/,
  );
  assert.match(html, /W12 · /);
  assert.match(
    html,
    /Explains <span class="tilasm-code">T29<\/span><span class="tilasm-code">gone-123<\/span>/,
  );
  // Nothing open: no frame; the history folded.
  assert.doesNotMatch(html, /<iframe/);
  assert.doesNotMatch(html, /Restore this version/);

  // Open: a frame served by Djinn at the tilasm's address, scripts only, never Djinn's origin.
  const open = s.renderToStaticMarkup(
    h(s.TilasmList, { ...props, open: model, history: model.id }),
  );
  const frame = open.match(/<iframe[^>]*>/)[0];
  assert.match(frame, /src="\/tilasm\/01a1223a-ae45-728f-8c37-c005eee91edb\/"/);
  assert.match(frame, /sandbox="allow-scripts"/);
  assert.doesNotMatch(frame, /allow-same-origin/);
  assert.match(frame, /title="L01 · The objects in the database"/);
  assert.match(
    open,
    /<section class="tilasm-view" aria-label="The objects in the database">.*v3/,
  );
  assert.match(open, /tilasm-row open/);
  // The history: the latest first, each with who and when, its files and size; an earlier one restores.
  const versions = open.match(/<ol class="tilasm-history"[\s\S]*?<\/ol>/)[0];
  assert.ok(versions.indexOf("v3") < versions.indexOf("v2"));
  assert.match(versions, /developer · .* · 3 files · 2 kB · restores v1/);
  assert.match(versions, /1 file · 512 byte/);
  assert.equal(versions.match(/Restore this version/g).length, 2);

  // A search that finds nothing says so; a wish without tilasms says how to make one.
  assert.match(
    s.renderToStaticMarkup(
      h(s.TilasmList, { ...props, tilasms: [], search: "lamp" }),
    ),
    /No tilasm holds “lamp”/,
  );
  assert.match(
    s.renderToStaticMarkup(
      h(s.TilasmList, { ...props, tilasms: [], count: 0 }),
    ),
    /No tilasm yet: .*djinn tilasm put/,
  );
});

test("a wish's view has a Tilasms tab beside Tasks and Decisions, with its count", async () => {
  const wishId = "01a11833-a440-7479-a067-52615c91da71";
  const lamp = wish(wishId, "Ship the lamp", s.WishState.ACTIVE, 1);
  const transport = s.createRouterTransport(({ service }) => {
    service(s.WishService, { list: () => ({ wishes: [lamp] }) });
    service(s.TaskService, { list: () => ({ tasks: [] }) });
    service(s.QuestionService, { list: () => ({ questions: [] }) });
    service(s.BlockService, { list: () => ({ blocks: [] }) });
    service(s.TilasmService, {
      list: (req) => ({
        tilasms: req.wish === wishId ? [tilasm("x1", "L01", "Model")] : [],
      }),
    });
  });
  const djinn = s.createDjinn(transport, 10);
  const close = djinn.store.open(wishId);
  await djinn.store.changed(wishId, [s.Change.TILASM]);
  close();
  assert.equal(djinn.store.getState().error, "");
  const html = s.renderToStaticMarkup(
    h(s.DjinnProvider, { djinn }, h(s.WishView, { wish: lamp, onToast() {} })),
  );
  assert.match(
    html,
    /id="view-tab-decisions".*id="view-tab-tilasms" aria-selected="false" class="">Tilasms<span class="count">1<\/span>/,
  );
});

test("a folder dropped on the Tilasms tab is read with its files by their paths in it, hidden files left out", async () => {
  const file = (name, text) => ({
    name,
    isFile: true,
    isDirectory: false,
    file: (ok) => ok(new Blob([text])),
  });
  const folder = (name, children) => {
    let read = false;
    return {
      name,
      isFile: false,
      isDirectory: true,
      // readEntries gives a batch, then an empty one.
      createReader: () => ({
        readEntries: (ok) => ok(read ? [] : ((read = true), children)),
      }),
    };
  };
  const dropped = await s.readDrop([
    folder("Data model", [
      file("index.html", "<p>model</p>"),
      file(".DS_Store", "x"),
      folder("css", [file("a.css", "p{}")]),
    ]),
  ]);
  assert.equal(dropped.name, "Data model");
  assert.deepEqual(
    dropped.files.map((f) => [f.path, new TextDecoder().decode(f.content)]),
    [
      ["index.html", "<p>model</p>"],
      ["css/a.css", "p{}"],
    ],
  );
  const zip = await s.readDrop([file("model.zip", "PK")]);
  assert.equal(zip.name, "model.zip");
  assert.deepEqual(
    zip.files.map((f) => f.path),
    ["model.zip"],
  );
});

test("a djinn:// link in Markdown stays a link that opens what it names in place: a tilasm in its wish's Tilasms tab", async () => {
  const wishId = "01a11833-a440-7479-a067-52615c91da71";
  const id = "01a1223a-ae45-728f-8c37-c005eee91edb";
  // In a block, a question, a decision or the brief: Markdown keeps the link, which does not leave the page.
  const html = s.renderToStaticMarkup(
    h(s.MarkdownBody, {
      text: `See [L01](djinn://tilasm/${id}), [the wish](djinn://wish/${wishId}) and [the docs](https://example.com).`,
    }),
  );
  assert.match(
    html,
    new RegExp(`<a href="djinn://tilasm/${id}" class="djinn-link">L01</a>`),
  );
  assert.match(
    html,
    new RegExp(
      `<a href="djinn://wish/${wishId}" class="djinn-link">the wish</a>`,
    ),
  );
  assert.match(html, /<a href="https:\/\/example.com" target="_blank"/);

  // Read as djinn open reads them.
  assert.deepEqual(
    s.parseDjinnLink(`DJINN://Talisman/${id.toUpperCase()}/?x#y`),
    {
      kind: "tilasm",
      id,
    },
  );
  for (const unknown of [
    "djinn://moon/" + id,
    "djinn://tilasm/L01",
    `djinn://tilasm/${id}/more`,
    `https://tilasm/${id}`,
  ])
    assert.equal(s.parseDjinnLink(unknown), undefined, unknown);

  // A click: the tilasm's wish on its Tilasms tab, the wish, or the window says it does not know the link.
  const transport = s.createRouterTransport(({ service }) => {
    service(s.TilasmService, {
      get: (req) => {
        if (req.tilasm?.ref.value !== id)
          throw new s.ConnectError("no tilasm", s.Code.NotFound);
        return { tilasm: tilasm(id, "L01", "Model", { wishId }) };
      },
    });
  });
  const shown = [];
  const djinn = {
    clients: s.createDjinn(transport, 10).clients,
    focus: { show: (focus) => shown.push(focus) },
  };
  const gone = "djinn://tilasm/01a1223a-ae45-728f-8c37-000000000000";
  for (const link of [
    `djinn://tilasm/${id}`,
    `djinn://wish/${wishId}`,
    gone,
    "djinn://moon/x",
  ])
    await s.openDjinnLink(djinn, link);
  assert.deepEqual(shown, [
    { wishId, tilasmId: id },
    { wishId },
    { unknownLink: gone },
    { unknownLink: "djinn://moon/x" },
  ]);

  // The wish's view, asked to open the tilasm: its Tilasms tab, the tilasm in the frame.
  const lamp = wish(wishId, "Ship the lamp", s.WishState.ACTIVE, 1);
  const store = s.createDjinn(
    s.createRouterTransport(({ service }) => {
      service(s.WishService, { list: () => ({ wishes: [lamp] }) });
      service(s.TaskService, { list: () => ({ tasks: [] }) });
      service(s.QuestionService, { list: () => ({ questions: [] }) });
      service(s.BlockService, { list: () => ({ blocks: [] }) });
      service(s.TilasmService, {
        list: () => ({
          tilasms: [
            tilasm("01a1223a-ae45-728f-8c37-c005eee91edc", "L02", "Flows", {
              wishId,
            }),
            tilasm(id, "L01", "Model", { wishId }),
          ],
        }),
      });
    }),
    10,
  );
  const close = store.store.open(wishId);
  await store.store.changed(wishId, [s.Change.TILASM]);
  close();
  const view = s.renderToStaticMarkup(
    h(
      s.DjinnProvider,
      { djinn: store },
      h(s.WishView, {
        wish: lamp,
        opening: { wishId, tilasmId: id },
        onToast() {},
      }),
    ),
  );
  assert.match(view, /id="view-tab-tilasms" aria-selected="true"/);
  assert.match(view, new RegExp(`<iframe[^>]*src="/tilasm/${id}/"`));
  assert.match(view, /<section class="tilasm-view" aria-label="Model">/);
});

test("the Tasks tab says where each task's work stands on its way into the wish's branch", () => {
  const at = (seconds) => ({ seconds: BigInt(seconds), nanos: 0 });
  const work = (state, extra = {}) => ({
    state,
    branch: "feat/x",
    sha: "1a2b3c4d5e6f",
    reason: "",
    correctedBy: "",
    reviewedBy: "",
    ...extra,
  });
  const tasks = [
    ["W1", undefined],
    ["W2", work(s.IntegrationState.PENDING)],
    ["W3", work(s.IntegrationState.COMMITTED)],
    [
      "W4",
      work(s.IntegrationState.CONFLICT, {
        reason: "W4 conflicts with feat/x in a.go",
        correctedBy: "W9",
      }),
    ],
    ["W5", work(s.IntegrationState.RED, { reason: "test exited 1" })],
    ["W6", work(s.IntegrationState.INTEGRATING)],
    ["W8", work(s.IntegrationState.UNCOMMITTED, { reviewedBy: "W9" })],
    ["W10", work(s.IntegrationState.COMMITTED, { reviewedBy: "W9" })],
  ].map(([code, integration], i) => ({
    id: code,
    code,
    title: `Task ${code}`,
    status: s.TaskStatus.DONE,
    createTime: at(1),
    endTime: at(10 - i),
    integration,
  }));
  const card = (task) =>
    s.renderToStaticMarkup(
      h(s.WishTask, { task, onStop() {}, async onSend() {} }),
    );
  const notes = tasks.map((task) => {
    const m = card(task).match(
      /<p class="wish-task-note wish-task-work work-([a-z]+)">([^<]*)<\/p>/,
    );
    return m ? `${task.code} ${m[1]}: ${m[2]}` : `${task.code} done`;
  });
  assert.deepEqual(notes, [
    "W1 done",
    "W2 waiting: Done, waiting to be committed",
    "W3 done: Committed into feat/x as 1a2b3c4d",
    "W4 failed: Conflict, not committed: W4 conflicts with feat/x in a.go, corrected by W9",
    "W5 failed: Red tests, not committed: test exited 1",
    "W6 running: Being committed into feat/x",
    "W8 waiting: Changes not committed, not merged, reviewed by W9",
    "W10 done: Committed into feat/x as 1a2b3c4d, reviewed by W9",
  ]);
  // A task that waits for another's work to be committed says so.
  const waits = card({
    id: "W7",
    code: "W7",
    title: "Next",
    status: s.TaskStatus.PENDING,
    createTime: at(1),
    waitReason: "waits for W2 to be committed",
  });
  assert.match(waits, /waits for W2 to be committed/);
});

test("the update banner proposes to install a build committed, with what changed", () => {
  const build = {
    wishTitle: "Run Djinn on itself",
    project: "djinn",
    branch: "feat/wails-go",
    sha: "1a2b3c4d5e6f",
    tasks: ["W5", "W6"],
    changes: ["Work of W6", "Work of W5"],
    summaries: [
      {
        code: "W5",
        title: "Work of W5",
        summary: "To check: the banner shows the build.",
      },
      {
        code: "W6",
        title: "Work of W6",
        summary: "Done: W6 finished.",
      },
    ],
  };
  const banner = (phase = { kind: "idle" }, dismissedBuild = "", installing) =>
    s.renderToStaticMarkup(
      h(s.UpdateBannerView, {
        state: {
          current: "v1",
          ready: "",
          notResumed: [],
          notesUrl: "",
          build,
          installing,
        },
        phase,
        dismissed: false,
        dismissedBuild,
        onInstall() {},
        onDismiss() {},
        onNotes() {},
      }),
    );
  const html = banner();
  assert.match(
    html,
    /W5, W6 pushed with feat\/wails-go of djinn <code>1a2b3c4d<\/code>/,
  );
  assert.doesNotMatch(html, /What to check/);
  assert.match(
    html,
    /<ul class="update-banner-tasks"><li><details class="update-banner-task"><summary><code>W5<\/code> Work of W5<\/summary><div class="update-banner-summary">To check: the banner shows the build\.<\/div><\/details><\/li><li><details class="update-banner-task"><summary><code>W6<\/code> Work of W6<\/summary><div class="update-banner-summary">Done: W6 finished\.<\/div><\/details><\/li><\/ul>/,
  );
  assert.match(
    html,
    /<details class="update-banner-commits"><summary>commits<\/summary><ul><li>Work of W6<\/li><li>Work of W5<\/li><\/ul><\/details>/,
  );
  // The mark and the actions head the banner, beside the title: Install and restart in brass, Dismiss in violet; what
  // changed comes below, at its full width.
  assert.match(html, /<span class="brand small"><span class="brand-mark"><img/);
  assert.match(
    html,
    /<\/code><\/span><\/div><div class="update-banner-actions"><button type="button" class="update-banner-primary">Install and restart<\/button><button type="button" class="update-banner-dismiss">Dismiss<\/button><\/div><\/div><div class="update-banner-body"><strong>What changed/,
  );
  // While it installs, the buttons go.
  const installing = banner({ kind: "installing" });
  assert.match(installing, /Installing…/);
  assert.doesNotMatch(installing, /<button/);
  // Then it says where the install stands, as Djinn tells it, in this window or another: what it waits for, that it
  // builds, that Djinn restarts.
  const step = (step, waiting = "") =>
    banner({ kind: "idle" }, "", { sha: build.sha, step, waiting });
  const waits = step(s.InstallStep.WAITING, "gate install: held by W9 (Other)");
  assert.match(waits, /Waiting — gate install: held by W9 \(Other\)/);
  assert.doesNotMatch(waits, /<button/);
  assert.match(step(s.InstallStep.PREPARING), /Preparing the build…/);
  assert.match(step(s.InstallStep.BUILDING), /Building…/);
  assert.match(step(s.InstallStep.RESTARTING), /Restarting…/);
  // Dismissed, that build no longer shows.
  assert.equal(banner({ kind: "idle" }, build.sha), "");
  // A build that installed no newer Djinn says it is installed, and restarts nothing.
  assert.match(
    s.renderToStaticMarkup(
      h(s.UpdateBannerView, {
        state: { current: "v1", ready: "", notResumed: [], notesUrl: "" },
        phase: { kind: "installed", sha: build.sha },
        dismissed: false,
        onInstall() {},
        onDismiss() {},
        onNotes() {},
      }),
    ),
    /Installed 1a2b3c4d/,
  );
});

test("an azima whose work is done awaits its proof: its own label and tone, what it needs, the pill apart, and the flight plan lists what a person can give", async () => {
  const wishId = "01a11833-a440-7479-a067-52615c91da71";
  const lamp = wish(wishId, "Ship the lamp", s.WishState.ACTIVE, 1);
  const need = (box, needs, provers, reviewer = "") => ({
    box,
    needs,
    provers,
    reviewer,
  });
  const azima = (code, title, state, extra = {}) => ({
    id: code,
    wishId,
    code,
    title,
    kind: s.TaskKind.AZIMA,
    status: s.TaskStatus.PENDING,
    dependsOn: [],
    proofNeeds: [],
    azima: { state, ready: true, parts: 1, partsDone: 1 },
    ...extra,
  });
  const tasks = [
    azima("T1", "Lay the ground", s.AzimaState.DONE, {
      status: s.TaskStatus.DONE,
    }),
    azima("T3", "The interface", s.AzimaState.AWAITING_PROOF, {
      proofNeeds: [
        need(
          "Clément has reviewed the switch.",
          "Clément's review",
          [s.Prover.REVIEW],
          "Clément",
        ),
      ],
    }),
    azima("T6", "Native e2e", s.AzimaState.AWAITING_PROOF, {
      proofNeeds: [
        need("The same scenario runs on macOS.", "a Mac", [s.Prover.MAC]),
      ],
    }),
    azima("T7", "The orchestrator", s.AzimaState.IN_PROGRESS),
    {
      id: "w1",
      wishId,
      code: "W1",
      title: "Switch",
      status: s.TaskStatus.DONE,
      partOf: "T3",
    },
    {
      id: "w2",
      wishId,
      code: "W2",
      title: "Drive",
      status: s.TaskStatus.DONE,
      partOf: "T6",
    },
  ];
  const byId = new Map(tasks.map((task) => [task.id, task]));
  const card = s.renderToStaticMarkup(
    h(s.AzimaCard, {
      azima: tasks[2],
      parts: [tasks[5]],
      tasks: byId,
      render: () => null,
    }),
  );
  assert.match(card, /azima-card tone-proof/);
  assert.match(
    card,
    /<span class="status-badge tone-proof" title="Its work is done: to validate\n• The same scenario runs on macOS\. Needs a Mac\n[^"]*then validate it: it is done\.">.*<span>To validate<\/span>/,
  );
  assert.match(
    card,
    /<span class="azima-needs" title="Its work is done: to validate\n• The same scenario runs on macOS\. Needs a Mac\n[^"]*">a Mac<\/span>/,
  );
  assert.doesNotMatch(card, /tone-waiting/);

  const transport = s.createRouterTransport(({ service }) => {
    service(s.WishService, { list: () => ({ wishes: [lamp] }) });
    service(s.TaskService, { list: () => ({ tasks }) });
    service(s.QuestionService, { list: () => ({ questions: [] }) });
    service(s.BlockService, { list: () => ({ blocks: [] }) });
  });
  const djinn = s.createDjinn(transport, 10);
  const close = djinn.store.open(wishId);
  await djinn.store.changed(wishId, [s.Change.WISH]);
  close();
  // The wish's head counts the azimas awaiting their proof apart from the done ones.
  const page = s.renderToStaticMarkup(
    h(s.DjinnProvider, { djinn }, h(s.WishView, { wish: lamp, onToast() {} })),
  );
  assert.match(
    page,
    /title="1 of 4 azimas done, 2 to validate"[^>]*>.*?<b>1<\/b>done · <b class="tone-proof">2<\/b> to validate \/ 4 azimas<\/span>/,
  );
  // The flight plan lists the proof a person can give among what waits for them, never as work; a Mac's is not.
  const plan = s.renderToStaticMarkup(
    h(
      s.DjinnProvider,
      { djinn },
      h(s.FlightPlan, { wishes: [lamp], onOpen() {}, onToast() {} }),
    ),
  );
  const yourMove = plan.slice(plan.indexOf('id="action-center"'));
  assert.match(
    yourMove,
    /<h3>Proofs you can give<span class="count">1<\/span><\/h3>/,
  );
  assert.match(
    yourMove,
    /status-badge tone-proof" title="Needs Clément&#x27;s review"><svg[^]*?<span>Clément&#x27;s review<\/span>/,
  );
  assert.match(yourMove, /T3: Clément has reviewed the switch\./);
  assert.doesNotMatch(plan, /The same scenario runs on macOS/);
  assert.match(
    plan,
    /id="view-tab-tasks"[^>]*>Tasks<span class="count">2<\/span>/,
  );
  const fp = s.flightPlan([lamp], {
    [wishId]: { tasks, questions: [], blocks: [], loaded: true },
  });
  assert.deepEqual(
    fp.azimas.map((x) => x.item.azima.code),
    ["T7", "T3", "T6", "T1"],
  );
  assert.equal(fp.moving.length + fp.waiting.length, 0);
});

test("an azima whose status is done while its state is in progress shows in progress: the card and the wish pill", async () => {
  const wishId = "01a11833-a440-7479-a067-52615c91da71";
  const lamp = wish(wishId, "Ship the lamp", s.WishState.ACTIVE, 1);
  const azimaTask = {
    id: "t1",
    wishId,
    code: "T1",
    title: "Lay the ground",
    kind: s.TaskKind.AZIMA,
    status: s.TaskStatus.DONE,
    dependsOn: [],
    proofNeeds: [],
    azima: {
      state: s.AzimaState.IN_PROGRESS,
      partsRunning: 1,
      parts: 2,
      partsDone: 1,
    },
  };
  const part1 = {
    id: "w1",
    wishId,
    code: "W1",
    title: "Foundations",
    status: s.TaskStatus.DONE,
    partOf: "t1",
  };
  const part2 = {
    id: "w2",
    wishId,
    code: "W2",
    title: "Plumbing",
    status: s.TaskStatus.RUNNING,
    partOf: "t1",
  };
  const tasks = [azimaTask, part1, part2];
  const byId = new Map(tasks.map((task) => [task.id, task]));

  // The card shows in progress with its tone and badge, never done.
  const card = s.renderToStaticMarkup(
    h(s.AzimaCard, {
      azima: azimaTask,
      parts: [part1, part2],
      tasks: byId,
      render: () => null,
    }),
  );
  assert.match(card, /azima-card tone-running/);
  assert.match(
    card,
    /<span class="status-badge tone-running">.*?<span>In progress<\/span><\/span>/,
  );
  assert.doesNotMatch(card, /tone-done/);
  assert.doesNotMatch(card, />Done<\/span>/);

  const transport = s.createRouterTransport(({ service }) => {
    service(s.WishService, { list: () => ({ wishes: [lamp] }) });
    service(s.TaskService, { list: () => ({ tasks }) });
    service(s.QuestionService, { list: () => ({ questions: [] }) });
    service(s.BlockService, { list: () => ({ blocks: [] }) });
  });
  const djinn = s.createDjinn(transport, 10);
  const close = djinn.store.open(wishId);
  await djinn.store.changed(wishId, [s.Change.WISH]);
  close();

  // The wish's head pill counts 0 done of 1 azimas, not 1 done.
  const page = s.renderToStaticMarkup(
    h(s.DjinnProvider, { djinn }, h(s.WishView, { wish: lamp, onToast() {} })),
  );
  assert.match(page, /title="0 of 1 azimas done"/);
  assert.match(page, /<b>0<\/b>\/ 1 azimas<\/span>/);
  assert.doesNotMatch(page, /title="1 of 1 azimas done"/);
  assert.doesNotMatch(page, /<b>1<\/b>\/ 1 azimas<\/span>/);
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
  assert.equal(
    s.recordedAgent({ provider: s.Provider.ANTIGRAVITY, sessionId: "" }),
    s.Provider.ANTIGRAVITY,
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

test("the attention bar says how much each question holds up: blocking, your move, before X, can wait", () => {
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
      { wish: w, item: q("q4", "", true), blocking: [] },
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
      ["t9", "action", undefined],
      ["q3", "later", undefined],
      ["ready-w1", "ready", undefined],
    ],
  );
  const markup = s.renderToStaticMarkup(h(s.AttentionBar, { items }));
  assert.match(markup, /<button class="attention-item level-move"/);
  assert.match(
    markup,
    /<span class="attention-level"><svg[^>]*class="lucide lucide-circle-user-round[^>]*>.*<\/svg>Your move<\/span>/,
  );
});

test("the attention bar shows a failed worker until it is dealt with (continued, done, or a new task names it)", () => {
  const w = wish("w1", "Ship the lamp", s.WishState.ACTIVE, 1);
  const failedTask = {
    id: "t1",
    wishId: "w1",
    code: "W1",
    title: "Light the wick",
    status: s.TaskStatus.FAILED,
    error: "exit code 1: fuel line broken\nstack trace follows",
  };

  // 1. Initially, undealt-with failed task is in waitingTasks and attentionOf.
  let tasks = [failedTask];
  let waiting = s.waitingTasks(w, { tasks, questions: [] });
  assert.equal(waiting.length, 1);
  assert.equal(waiting[0].item.id, "t1");

  let items = s.attentionOf([], waiting, []);
  assert.equal(items.length, 1);
  assert.equal(items[0].key, "t1");
  assert.equal(items[0].level, "action");
  assert.equal(items[0].target, "waiting-t1");
  assert.equal(items[0].code, "W1");
  assert.equal(items[0].text, "exit code 1: fuel line broken");

  // 2. When marked done: dealt with, disappears from attention bar.
  tasks = [{ ...failedTask, status: s.TaskStatus.DONE }];
  waiting = s.waitingTasks(w, { tasks, questions: [] });
  assert.equal(waiting.length, 0);

  // 3. When continuing: true: dealt with, disappears.
  tasks = [{ ...failedTask, continuing: true }];
  waiting = s.waitingTasks(w, { tasks, questions: [] });
  assert.equal(waiting.length, 0);

  // 4. When closed.continuedIn is set: dealt with, disappears.
  tasks = [{ ...failedTask, closed: { continuedIn: "t2" } }];
  waiting = s.waitingTasks(w, { tasks, questions: [] });
  assert.equal(waiting.length, 0);

  // 5. When forked by another task (forkOf === "W1"): dealt with, disappears.
  tasks = [
    failedTask,
    {
      id: "t2",
      wishId: "w1",
      code: "W2",
      title: "Forked",
      status: s.TaskStatus.RUNNING,
      forkOf: "W1",
    },
  ];
  waiting = s.waitingTasks(w, { tasks, questions: [] });
  assert.equal(waiting.length, 0);

  // 6. When another task has decision === "W1": dealt with, disappears.
  tasks = [
    failedTask,
    {
      id: "t2",
      wishId: "w1",
      code: "W2",
      title: "Follow up",
      status: s.TaskStatus.RUNNING,
      decision: "W1",
    },
  ];
  waiting = s.waitingTasks(w, { tasks, questions: [] });
  assert.equal(waiting.length, 0);

  // 7. When another task's title names W1: dealt with, disappears.
  tasks = [
    failedTask,
    {
      id: "t2",
      wishId: "w1",
      code: "W2",
      title: "Fix W1 build error",
      status: s.TaskStatus.RUNNING,
    },
  ];
  waiting = s.waitingTasks(w, { tasks, questions: [] });
  assert.equal(waiting.length, 0);

  // 8. When another task's correction failure taskIds includes t1.id: dealt with, disappears.
  tasks = [
    failedTask,
    {
      id: "t2",
      wishId: "w1",
      code: "W2",
      title: "Correct W1",
      status: s.TaskStatus.RUNNING,
      correction: { failure: { taskIds: ["t1"] } },
    },
  ];
  waiting = s.waitingTasks(w, { tasks, questions: [] });
  assert.equal(waiting.length, 0);

  // 9. When another task's review taskIds includes t1.id: dealt with, disappears.
  tasks = [
    failedTask,
    {
      id: "t2",
      wishId: "w1",
      code: "W2",
      title: "Review W1",
      status: s.TaskStatus.RUNNING,
      review: { taskIds: ["t1"] },
    },
  ];
  waiting = s.waitingTasks(w, { tasks, questions: [] });
  assert.equal(waiting.length, 0);
});

test("the wish's head says where Djinn last pushed its integration branch, and a push refused", () => {
  const last = {
    branch: "feat/x",
    remote: "origin",
    count: 3,
    commits: ["Work of W3", "Work of W2", "Work of W1"],
    pushTime: { seconds: 1791640800n, nanos: 0 },
  };
  const one = s.renderToStaticMarkup(
    h(s.LastPushes, {
      pushes: [{ projectId: "p1", last, refused: "" }],
      projects: [{ id: "p1", name: "app" }],
    }),
  );
  assert.match(
    one,
    /<span class="wish-push"><svg[^]*?<\/svg><span title="Work of W3\nWork of W2\nWork of W1">Pushed feat\/x to origin, 3 commits, [^<]+<\/span><\/span>/,
  );
  assert.doesNotMatch(one, /<b>app<\/b>/);
  // Several projects: each push names its project; one the remote refused says so; none yet, nothing.
  const two = s.renderToStaticMarkup(
    h(s.LastPushes, {
      pushes: [
        { projectId: "p1", last: { ...last, count: 1 }, refused: "" },
        {
          projectId: "p2",
          refused: "its branch has commits that this one does not",
        },
        { projectId: "p3", refused: "" },
      ],
      projects: [
        { id: "p1", name: "app" },
        { id: "p2", name: "api" },
        { id: "p3", name: "web" },
      ],
    }),
  );
  assert.match(
    two,
    /<b>app<\/b><span title="[^"]*">Pushed feat\/x to origin, 1 commit, /,
  );
  assert.match(
    two,
    /<b>api<\/b><span class="wish-push-refused" title="its branch has commits that this one does not">The remote refused the last push: Djinn asks you<\/span>/,
  );
  assert.doesNotMatch(two, /web/);
});

test("the wish's head says a push its checks hold, why on hover", () => {
  const html = s.renderToStaticMarkup(
    h(s.LastPushes, {
      pushes: [
        {
          projectId: "p1",
          refused: "",
          held: "at 1a2b3c4d, test: go tool task test exited 1",
        },
      ],
      projects: [{ id: "p1", name: "app" }],
    }),
  );
  assert.match(
    html,
    /<span class="wish-push-refused" title="at 1a2b3c4d, test: go tool task test exited 1">The push is held: its checks are red<\/span>/,
  );
});

test("the wish's head says the merge of main waits, why visible and on hover", () => {
  const html = s.renderToStaticMarkup(
    h(s.MainMerges, {
      mains: [
        {
          projectId: "p1",
          held: "fetch main from origin: fatal: repository 'foo' does not exist\nfatal: Could not read from remote repository.",
        },
      ],
      projects: [{ id: "p1", name: "app" }],
    }),
  );
  assert.match(
    html,
    /<span class="wish-push-refused" title="fetch main from origin: fatal: repository &#x27;foo&#x27; does not exist\nfatal: Could not read from remote repository\.">The merge of main waits: fetch main from origin: fatal: repository &#x27;foo&#x27; does not exist<\/span>/,
  );
});

test("the project view lists the setup and the checks, when each runs, and how each last ran", () => {
  const none = s.renderToStaticMarkup(
    h(s.ProjectChecks, { setup: "", checks: [], runs: [] }),
  );
  assert.match(none, /No check: Djinn does not integrate this project/);
  const html = s.renderToStaticMarkup(
    h(s.ProjectChecks, {
      setup: "npm ci",
      checks: [
        {
          name: "lint",
          command: "go tool task lint",
          when: [s.CheckWhen.COMMIT, s.CheckWhen.PUSH],
        },
        {
          name: "test",
          command: "go tool task test",
          when: [s.CheckWhen.PUSH],
        },
      ],
      runs: [
        {
          name: "setup",
          setup: true,
          passed: true,
          sha: "0123456789abcdef",
          durationMs: 61_000n,
          reason: "",
          output: "",
          endTime: { seconds: 1791640800n, nanos: 0 },
        },
        {
          name: "LINT",
          setup: false,
          passed: false,
          sha: "fedcba9876543210",
          durationMs: 2_000n,
          reason: "go tool task lint exited 1:\nmain.go:1: unused",
          output: "main.go:1: unused",
          endTime: { seconds: 1791640800n, nanos: 0 },
        },
      ],
    }),
  );
  assert.ok(
    html.indexOf("npm ci") < html.indexOf("go tool task lint") &&
      html.indexOf("go tool task lint") < html.indexOf("go tool task test"),
  );
  assert.match(
    html,
    /<strong>setup<\/strong><p><code>npm ci<\/code><\/p><p class="muted-text">Passed on 01234567, [^<]+, in 1m1s\.<\/p><\/div><span><span class="badge muted">makes a worktree ready<\/span>/,
  );
  assert.match(
    html,
    /<p class="login-message" title="main.go:1: unused">Failed on fedcba98, [^<]+: go tool task lint exited 1<\/p><\/div><span><span class="badge muted">before a commit<\/span><span class="badge muted">before a push<\/span>/,
  );
  assert.match(
    html,
    /<strong>test<\/strong><p><code>go tool task test<\/code><\/p><p class="muted-text">Not run yet\.<\/p>/,
  );
});

function setupMockDom() {
  globalThis.IS_REACT_ACT_ENVIRONMENT = true;

  class MockElement {
    constructor(nodeType, nodeName) {
      this.nodeType = nodeType;
      this.nodeName = nodeName;
      this.tagName = nodeName;
      this.childNodes = [];
      this.parentNode = null;
      this.attributes = {};
      this.style = {};
      this.ownerDocument = globalThis.document;
      this.namespaceURI = "http://www.w3.org/1999/xhtml";
    }
    get children() {
      return this.childNodes.filter((c) => c.nodeType === 1);
    }
    appendChild(child) {
      child.parentNode = this;
      this.childNodes.push(child);
      return child;
    }
    removeChild(child) {
      const idx = this.childNodes.indexOf(child);
      if (idx !== -1) {
        this.childNodes.splice(idx, 1);
        child.parentNode = null;
      }
      return child;
    }
    insertBefore(child, before) {
      child.parentNode = this;
      const idx = this.childNodes.indexOf(before);
      if (idx !== -1) this.childNodes.splice(idx, 0, child);
      else this.childNodes.push(child);
      return child;
    }
    setAttribute(k, v) {
      this.attributes[k] = String(v);
    }
    removeAttribute(k) {
      delete this.attributes[k];
    }
    addEventListener() {}
    removeEventListener() {}
  }

  globalThis.HTMLIFrameElement = class HTMLIFrameElement {};
  globalThis.HTMLElement = MockElement;
  globalThis.Element = MockElement;
  globalThis.Node = MockElement;

  globalThis.addEventListener = () => {};
  globalThis.removeEventListener = () => {};
  globalThis.CSS = { escape: (str) => str, supports: () => false };
  globalThis.MutationObserver = class MutationObserver {
    observe() {}
    disconnect() {}
    takeRecords() {
      return [];
    }
  };
  globalThis.ResizeObserver = class ResizeObserver {
    observe() {}
    unobserve() {}
    disconnect() {}
  };

  globalThis.requestAnimationFrame = (cb) => setTimeout(cb, 0);
  globalThis.cancelAnimationFrame = (id) => clearTimeout(id);

  globalThis.document = {
    nodeType: 9,
    createElement(tag) {
      return new MockElement(1, tag.toUpperCase());
    },
    createElementNS(ns, tag) {
      return new MockElement(1, tag.toUpperCase());
    },
    createTextNode(text) {
      const el = new MockElement(3, "#text");
      el.nodeValue = text;
      return el;
    },
    createComment(data) {
      const el = new MockElement(8, "#comment");
      el.data = data;
      return el;
    },
    documentElement: new MockElement(1, "HTML"),
    body: new MockElement(1, "BODY"),
    activeElement: null,
    addEventListener() {},
    removeEventListener() {},
    defaultView: globalThis,
  };
  globalThis.window = globalThis;
  return new MockElement(1, "DIV");
}

test("a card does not re-render when another card changes in a list", async () => {
  const container = setupMockDom();
  const wishId = "01a118fa-4308-736a-9ff9-5fc373a4294f";
  const activeWish = wish(wishId, "Ship the lamp", s.WishState.ACTIVE, 1);

  // 1. In WishView, when question Q02 changes, Q01 does not re-render.
  let questions = [
    { id: "q1", wishId, code: "Q01", text: "Question 1", options: ["A", "B"] },
    { id: "q2", wishId, code: "Q02", text: "Question 2", options: ["X", "Y"] },
  ];
  const transport = s.createRouterTransport(({ service }) => {
    service(s.WishService, {
      list: () => ({ wishes: [activeWish] }),
      describe: () => ({ wish: activeWish }),
      watch: async function* () {},
    });
    service(s.TaskService, { list: () => ({ tasks: [] }) });
    service(s.QuestionService, { list: () => ({ questions }) });
    service(s.BlockService, { list: () => ({ blocks: [] }) });
  });

  const djinn = s.createDjinn(transport, 10);
  const close = djinn.store.open(wishId);
  await djinn.store.changed(wishId, [s.Change.WISH]);

  const questionRenders = {};
  const origQuestionType = s.WishQuestion.type;
  s.WishQuestion.type = function SpiedWishQuestion(props) {
    questionRenders[props.question.id] =
      (questionRenders[props.question.id] || 0) + 1;
    return origQuestionType(props);
  };

  const root = s.createRoot(container);
  s.act(() => {
    root.render(
      h(
        s.DjinnProvider,
        { djinn },
        h(s.WishView, { wish: activeWish, onToast() {} }),
      ),
    );
  });

  assert.equal(questionRenders.q1, 1, "Q1 renders once initially");
  assert.equal(questionRenders.q2, 1, "Q2 renders once initially");

  // Modify question 2 and notify the store.
  questions = [
    { id: "q1", wishId, code: "Q01", text: "Question 1", options: ["A", "B"] },
    {
      id: "q2",
      wishId,
      code: "Q02",
      text: "Question 2 modified",
      options: ["X", "Y"],
    },
  ];
  await s.act(async () => {
    await djinn.store.changed(wishId, [s.Change.QUESTION]);
  });

  assert.equal(questionRenders.q1, 1, "Q1 does not re-render when Q2 changes");
  assert.equal(questionRenders.q2, 2, "Q2 re-renders when Q2 changes");
  s.WishQuestion.type = origQuestionType;
  close();

  // 2. In TaskSections, when task T02 changes, T01 does not re-render.
  const taskRenders = {};
  const origTaskType = s.WishTask.type;
  s.WishTask.type = function SpiedWishTask(props) {
    taskRenders[props.task.id] = (taskRenders[props.task.id] || 0) + 1;
    return origTaskType(props);
  };

  const task1 = {
    id: "t1",
    wishId,
    code: "W1",
    title: "Task 1",
    status: s.TaskStatus.RUNNING,
  };
  const task2 = {
    id: "t2",
    wishId,
    code: "W2",
    title: "Task 2",
    status: s.TaskStatus.RUNNING,
  };

  const onStop = () => {};
  const onSend = async () => {};
  const renderTask = (t) =>
    h(s.WishTask, { key: t.id, task: t, onStop, onSend });

  const taskRoot = s.createRoot(setupMockDom());
  s.act(() => {
    taskRoot.render(
      h(s.TaskSections, {
        moving: [task1, task2],
        azimas: [],
        doneAzimas: [],
        render: renderTask,
      }),
    );
  });

  assert.equal(taskRenders.t1, 1, "T1 renders once initially");
  assert.equal(taskRenders.t2, 1, "T2 renders once initially");

  const task2Updated = { ...task2, status: s.TaskStatus.DONE };
  s.act(() => {
    taskRoot.render(
      h(s.TaskSections, {
        moving: [task1, task2Updated],
        azimas: [],
        doneAzimas: [],
        render: renderTask,
      }),
    );
  });

  assert.equal(taskRenders.t1, 1, "T1 does not re-render when T2 changes");
  assert.equal(taskRenders.t2, 2, "T2 re-renders when T2 changes");
  s.WishTask.type = origTaskType;
  s.act(() => {
    root.unmount();
    taskRoot.unmount();
  });

  for (const h of process._getActiveHandles()) {
    if (
      h &&
      typeof h.unref === "function" &&
      h.constructor?.name === "MessagePort"
    ) {
      h.unref();
    }
  }
});

test("the wish's head says when pushes are on demand", () => {
  const onDemandOnly = s.renderToStaticMarkup(
    h(s.LastPushes, {
      pushes: [],
      projects: [{ id: "p1", name: "app", push: s.ProjectPush.ON_DEMAND }],
    }),
  );
  assert.match(
    onDemandOnly,
    /<span class="wish-push-on-demand">pushes on demand<\/span>/,
  );
  assert.doesNotMatch(onDemandOnly, /Pushed/);

  const last = {
    branch: "feat/x",
    remote: "origin",
    count: 2,
    commits: ["Work of W2", "Work of W1"],
    pushTime: { seconds: 1791640800n, nanos: 0 },
  };
  const both = s.renderToStaticMarkup(
    h(s.LastPushes, {
      pushes: [{ projectId: "p1", last, refused: "" }],
      projects: [{ id: "p1", name: "app", push: s.ProjectPush.ON_DEMAND }],
    }),
  );
  assert.match(both, /Pushed feat\/x to origin, 2 commits/);
  assert.match(
    both,
    /<span class="wish-push-on-demand">pushes on demand<\/span>/,
  );
});

test("the side panel shows when a project's integration branch is out of sync and offers a push button", () => {
  const project = {
    id: "p1",
    name: "lamp",
    directory: "/tmp/lamp",
    git: true,
    sync: {
      ahead: 3,
      behind: 1,
      remote: "origin",
      branch: "feat/x",
      target: "feat/x",
    },
  };
  const html = s.renderToStaticMarkup(
    h(s.WishSidebar, {
      wishes: [],
      projects: [project],
      selectedWishId: "",
      selectedProjectId: "",
      collapsed: false,
      onSelectWish() {},
      onSelectProject() {},
      onMove() {},
      onNewProject() {},
      onPush() {},
    }),
  );
  assert.match(html, /class="project-sync-row"/);
  assert.match(html, /3 commits ahead of origin\/feat\/x, 1 behind/);
  assert.match(
    html,
    /<button type="button" class="button accent small"[^>]*>Push<\/button>/,
  );

  const collapsedHtml = s.renderToStaticMarkup(
    h(s.WishSidebar, {
      wishes: [],
      projects: [project],
      selectedWishId: "",
      selectedProjectId: "",
      collapsed: true,
      onSelectWish() {},
      onSelectProject() {},
      onMove() {},
      onNewProject() {},
      onPush() {},
    }),
  );
  assert.doesNotMatch(collapsedHtml, /class="project-sync-row"/);
});

test("the project panel displays the push cadence setting, out-of-sync status, and push button", () => {
  const p1 = {
    id: "p1",
    name: "app",
    git: true,
    directory: "/tmp/app",
    remote: "git@github.com:org/app.git",
    push: s.ProjectPush.STANDARD,
    sync: {
      ahead: 3,
      behind: 0,
      remote: "origin",
      branch: "feat/x",
      target: "feat/x",
    },
  };
  const transport = s.createRouterTransport(({ service }) => {
    service(s.ProjectService, {
      show: () => ({ project: p1 }),
      push: () => ({}),
      setPush: () => ({ project: p1 }),
    });
    service(s.SkillService, {
      list: () => ({ skills: [] }),
    });
  });
  const djinn = s.createDjinn(transport, 10);
  const html = s.renderToStaticMarkup(
    h(
      s.DjinnProvider,
      { djinn },
      h(s.ProjectPanel, { project: p1, onClose() {} }),
    ),
  );
  assert.match(html, /3 commits ahead of origin\/feat\/x/);
  assert.match(
    html,
    /<button type="button" class="button accent small"[^>]*>Push<\/button>/,
  );
  assert.match(
    html,
    /Pushes at an azima(?:'|&#x27;)s end or after 3 tasks and an hour\./,
  );
  assert.match(
    html,
    /<button type="button" class="active">Standard<\/button><button type="button" class="">On demand<\/button>/,
  );

  const p2 = {
    ...p1,
    push: s.ProjectPush.ON_DEMAND,
    sync: undefined,
  };
  const htmlOnDemand = s.renderToStaticMarkup(
    h(
      s.DjinnProvider,
      { djinn },
      h(s.ProjectPanel, { project: p2, onClose() {} }),
    ),
  );
  assert.match(htmlOnDemand, /Pushes only when you ask\./);
  assert.doesNotMatch(
    htmlOnDemand,
    /<button type="button" class="button accent small"[^>]*>Push<\/button>/,
  );
  assert.match(
    htmlOnDemand,
    /<button type="button" class="">Standard<\/button><button type="button" class="active">On demand<\/button>/,
  );
});

test("draft azimas: fold, card with description, actions, and flight plan", () => {
  const at = (seconds) => ({ seconds: BigInt(seconds), nanos: 0 });
  const draft = (code, title, extra = {}) => ({
    id: code,
    code,
    title,
    kind: s.TaskKind.AZIMA,
    status: s.TaskStatus.PENDING,
    draft: true,
    description: "Explore multi-machine distribution.",
    dependsOn: [],
    createTime: at(1),
    azima: { state: s.AzimaState.DRAFT },
    ...extra,
  });
  const regular = (code, title, extra = {}) => ({
    id: code,
    code,
    title,
    kind: s.TaskKind.AZIMA,
    status: s.TaskStatus.PENDING,
    draft: false,
    dependsOn: [],
    createTime: at(1),
    azima: { state: s.AzimaState.OPEN, ready: true },
    ...extra,
  });
  const work = (code, status) => ({
    id: code,
    code,
    title: `Task ${code}`,
    status,
    dependsOn: [],
    createTime: at(1),
  });

  const d1 = draft("T31", "Spread work over other machines", {
    description: "Anticipate spreading work across nodes before launch.",
  });
  const d2 = draft("T32", "Cold storage backup", { description: "" });
  const openAzima = regular("T01", "Core pipeline");
  const runningWork = work("W01", s.TaskStatus.RUNNING);

  const allTasks = [d1, d2, openAzima, runningWork];
  const byId = new Map(allTasks.map((t) => [t.id, t]));

  // 1. Flight plan: drafts are collected in drafts, NOT in moving, waiting, or azimas.
  const myWish = wish("w1", "Multi-machine", s.WishState.ACTIVE, 1);
  const plan = s.flightPlan([myWish], {
    [myWish.id]: {
      wish: myWish,
      tasks: allTasks,
      questions: [],
      blocks: [],
      allowances: [],
      runs: [],
      loaded: true,
    },
  });
  assert.equal(plan.drafts.length, 2);
  assert.deepEqual(
    plan.drafts.map((x) => x.item.code),
    ["T31", "T32"],
  );
  // Drafts are not moving tasks:
  assert.deepEqual(
    plan.moving.map((x) => x.item.code),
    ["W01"],
  );
  // Drafts are not in active azimas list:
  assert.deepEqual(
    plan.azimas.map((x) => x.item.azima.code),
    ["T01"],
  );

  // 2. DraftAzimaCard rendered directly: shows draft badge, code, title.
  let openedCode = "";
  let movedCode = "";
  const cardComponent = (az) =>
    h(s.DraftAzimaCard, {
      key: az.id,
      azima: az,
      tasks: byId,
      onOpen: () => {
        openedCode = az.code;
      },
      onMove: () => {
        movedCode = az.code;
      },
      focus: az.id,
    });

  const cardHtml = s.renderToStaticMarkup(cardComponent(d1));
  assert.match(cardHtml, /azima-card tone-later open/);
  assert.match(cardHtml, /<span class="agent-code">T31<\/span>/);
  assert.match(cardHtml, /<strong title="Spread work over other machines">/);
  assert.match(
    cardHtml,
    /Anticipate spreading work across nodes before launch\./,
  );
  assert.match(cardHtml, />Open this azima<\/button>/);
  assert.match(cardHtml, />Move<\/button>/);

  // When card has no description: shows muted "No description yet."
  const emptyCardHtml = s.renderToStaticMarkup(cardComponent(d2));
  assert.match(emptyCardHtml, /No description yet\./);

  // Clicking open and move triggers callbacks:
  const liveCard = live(s.DraftAzimaCard, {
    azima: d1,
    tasks: byId,
    onOpen: () => {
      openedCode = d1.code;
    },
    onMove: () => {
      movedCode = d1.code;
    },
    focus: d1.id,
  });
  const buttons = liveCard.all("button");
  const openBtn = buttons.find((b) => b.props.children === "Open this azima");
  const moveBtn = buttons.find((b) => b.props.children === "Move");
  assert.ok(openBtn && moveBtn);
  openBtn.props.onClick();
  assert.equal(openedCode, "T31");
  moveBtn.props.onClick();
  assert.equal(movedCode, "T31");

  // 3. TaskSections fold: "Later: 2 drafts"
  const sectionsHtml = s.renderToStaticMarkup(
    h(s.TaskSections, {
      moving: s.movingTasks(allTasks),
      azimas: s.azimaGroups(allTasks),
      doneAzimas: [],
      drafts: s.draftAzimas(allTasks),
      showDrafts: false,
      renderDraft: (draft) => cardComponent(draft),
      render: () => null,
    }),
  );
  assert.match(sectionsHtml, /Later: 2 drafts/);
  // When fold is closed, cards are not rendered
  assert.doesNotMatch(sectionsHtml, /Anticipate spreading work/);

  // When fold is open: cards are rendered
  const openSectionsHtml = s.renderToStaticMarkup(
    h(s.TaskSections, {
      moving: s.movingTasks(allTasks),
      azimas: s.azimaGroups(allTasks),
      doneAzimas: [],
      drafts: s.draftAzimas(allTasks),
      showDrafts: true,
      renderDraft: (draft) => cardComponent(draft),
      render: () => null,
    }),
  );
  assert.match(openSectionsHtml, /Hide drafts/);
  assert.match(
    openSectionsHtml,
    /Anticipate spreading work across nodes before launch\./,
  );
});

test("azimas are ordered in a stable 6-tier order and laid out in a single column", () => {
  const at = (seconds) => ({ seconds: BigInt(seconds), nanos: 0 });
  const azima = (code, title, extra) => ({
    id: code,
    code,
    title,
    kind: s.TaskKind.AZIMA,
    status: s.TaskStatus.PENDING,
    dependsOn: [],
    proofNeeds: [],
    createTime: at(100),
    ...extra,
  });
  const work = (code, status, partOf, start, end) => ({
    id: code,
    code,
    title: `Task ${code}`,
    status,
    partOf,
    dependsOn: [],
    createTime: at(100),
    startTime: start ? at(start) : undefined,
    endTime: end ? at(end) : undefined,
  });

  // Six tiers of azimas:
  // Tier 0: Moving (parts running or state in progress)
  const aMoving1 = azima("T02", "Moving orchestrator", {
    startTime: at(150),
    azima: {
      state: s.AzimaState.IN_PROGRESS,
      ready: true,
      parts: 3,
      partsDone: 1,
      partsRunning: 1,
    },
  });
  const aMoving2 = azima("T01", "Moving builder", {
    azima: {
      state: s.AzimaState.OPEN,
      ready: true,
      parts: 2,
      partsDone: 0,
      partsRunning: 1,
    },
  });
  // Tier 1: Awaiting proof
  const aProof1 = azima("T04", "Validate release", {
    azima: {
      state: s.AzimaState.AWAITING_PROOF,
      ready: true,
      parts: 2,
      partsDone: 2,
    },
  });
  const aProof2 = azima("T03", "Review design", {
    azima: {
      state: s.AzimaState.AWAITING_PROOF,
      ready: true,
      parts: 1,
      partsDone: 1,
    },
  });
  // Tier 2: Ready not started
  const aReady1 = azima("T06", "Ready pipeline B", {
    azima: { state: s.AzimaState.OPEN, ready: true },
  });
  const aReady2 = azima("T05", "Ready pipeline A", {
    azima: { state: s.AzimaState.OPEN, ready: true },
  });
  // Tier 3: Blocked
  const aBlocked1 = azima("T08", "Blocked consumer", {
    azima: { state: s.AzimaState.OPEN, ready: false },
  });
  const aBlocked2 = azima("T07", "Blocked worker", {
    azima: { state: s.AzimaState.OPEN, ready: false },
  });
  // Tier 4: Draft
  const aDraft1 = azima("T31", "Draft expansion", {
    draft: true,
    azima: { state: s.AzimaState.DRAFT },
  });
  const aDraft2 = azima("T30", "Draft caching", {
    draft: true,
  });
  // Tier 5: Done
  const aDone1 = azima("T10", "Done setup", {
    status: s.TaskStatus.DONE,
    startTime: at(110),
    endTime: at(140),
    azima: {
      state: s.AzimaState.DONE,
      ready: true,
      parts: 1,
      partsDone: 1,
    },
  });
  const aDone2 = azima("T09", "Done foundation", {
    status: s.TaskStatus.DONE,
    startTime: at(105),
    endTime: at(125),
    azima: {
      state: s.AzimaState.DONE,
      ready: true,
      parts: 1,
      partsDone: 1,
    },
  });

  // Parts for T02 (1 done, 1 running, 1 waiting)
  const w1 = work("W01", s.TaskStatus.DONE, "T02", 150, 180);
  const w2 = work("W02", s.TaskStatus.RUNNING, "T02", 185);
  const w3 = work("W03", s.TaskStatus.WAITING, "T02");
  // Part for T01 (1 running)
  const w4 = work("W04", s.TaskStatus.RUNNING, "T01", 160);

  // 1. Verify azimaRank for each tier
  assert.equal(s.azimaRank(aMoving1), 0);
  assert.equal(s.azimaRank(aMoving2), 0);
  assert.equal(s.azimaRank(aProof1), 1);
  assert.equal(s.azimaRank(aProof2), 1);
  assert.equal(s.azimaRank(aReady1), 2);
  assert.equal(s.azimaRank(aReady2), 2);
  assert.equal(s.azimaRank(aBlocked1), 3);
  assert.equal(s.azimaRank(aBlocked2), 3);
  assert.equal(s.azimaRank(aDraft1), 4);
  assert.equal(s.azimaRank(aDraft2), 4);
  assert.equal(s.azimaRank(aDone1), 5);
  assert.equal(s.azimaRank(aDone2), 5);

  // Azimas in shuffled input order
  const allTasks = [
    aDone1,
    aBlocked1,
    aReady1,
    aProof1,
    aMoving1,
    aDraft1,
    w1,
    w2,
    w3,
    w4,
    aDraft2,
    aMoving2,
    aProof2,
    aReady2,
    aBlocked2,
    aDone2,
  ];

  // 2. azimaGroups orders non-drafts strictly by azimaRank, then compareCodes
  const groups = s.azimaGroups(allTasks);
  const groupCodes = groups.map((g) => g.azima.code);
  assert.deepEqual(groupCodes, [
    // Tier 0 (moving, ordered by code)
    "T01",
    "T02",
    // Tier 1 (awaiting proof, ordered by code)
    "T03",
    "T04",
    // Tier 2 (ready, ordered by code)
    "T05",
    "T06",
    // Tier 3 (blocked, ordered by code)
    "T07",
    "T08",
    // Tier 5 (done, ordered by code)
    "T09",
    "T10",
  ]);

  // Drafts are collected separately, ordered by code
  const drafts = s.draftAzimas(allTasks);
  assert.deepEqual(
    drafts.map((d) => d.code),
    ["T30", "T31"],
  );

  // 3. Flight plan azimas are sorted by azimaRank, then compareCodes
  const testWish = wish("w_test", "Azima layout wish", s.WishState.ACTIVE, 1);
  const plan = s.flightPlan([testWish], {
    [testWish.id]: {
      wish: testWish,
      tasks: allTasks,
      questions: [],
      blocks: [],
      allowances: [],
      runs: [],
      loaded: true,
    },
  });
  assert.deepEqual(
    plan.azimas.map((x) => x.item.azima.code),
    ["T01", "T02", "T03", "T04", "T05", "T06", "T07", "T08", "T09", "T10"],
  );
  assert.deepEqual(
    plan.drafts.map((x) => x.item.code),
    ["T30", "T31"],
  );

  // 4. Layout: TaskSections renders in a single column (.azima-list), never .azima-grid
  const byId = new Map(allTasks.map((t) => [t.id, t]));
  const card = (task) =>
    h(s.WishTask, {
      key: task.id,
      task,
      onStop() {},
      async onSend() {},
      async onDone() {},
    });
  const renderAzima = ({ azima, parts }) =>
    h(s.AzimaCard, {
      key: azima.id,
      azima,
      parts,
      tasks: byId,
      render: card,
    });
  const renderDraft = (draft) =>
    h(s.DraftAzimaCard, {
      key: draft.id,
      azima: draft,
      tasks: byId,
      onOpen() {},
      onMove() {},
    });

  const activeAzimas = groups.filter((g) => !s.azimaFinished(g.azima));
  const doneAzimas = groups.filter((g) => s.azimaFinished(g.azima));

  const sectionsHtml = s.renderToStaticMarkup(
    h(s.TaskSections, {
      moving: s.movingTasks(allTasks),
      azimas: activeAzimas,
      doneAzimas,
      drafts,
      showDone: true,
      showDrafts: true,
      renderAzima,
      renderDraft,
      render: card,
    }),
  );

  // Must have .azima-list and never .azima-grid
  assert.match(sectionsHtml, /<div class="azima-list">/);
  assert.doesNotMatch(sectionsHtml, /azima-grid/);

  // The active azimas appear in single column in exact stable order
  const activeOrder = [
    ...sectionsHtml.matchAll(/<span class="agent-code">(T\d+)<\/span>/g),
  ].map((m) => m[1]);
  assert.deepEqual(activeOrder, [
    "T01",
    "T02",
    "T03",
    "T04",
    "T05",
    "T06",
    "T07",
    "T08",
    "T30",
    "T31",
    "T09",
    "T10",
  ]);

  // Fold order: drafts fold appears before done fold
  const closedFoldsHtml = s.renderToStaticMarkup(
    h(s.TaskSections, {
      moving: s.movingTasks(allTasks),
      azimas: activeAzimas,
      doneAzimas,
      drafts,
      showDone: false,
      showDrafts: false,
      renderAzima,
      renderDraft,
      render: card,
    }),
  );
  const draftsFoldIndex = closedFoldsHtml.indexOf("Later: 2 drafts");
  const doneFoldIndex = closedFoldsHtml.indexOf("Show the 2 finished");
  assert.ok(
    draftsFoldIndex > 0 && doneFoldIndex > draftsFoldIndex,
    "drafts fold must appear before done fold",
  );

  // 5. Azima row details: code, goal, state, progress bar, counts, parts text, time
  const t2CardHtml = s.renderToStaticMarkup(
    h(s.AzimaCard, {
      azima: aMoving1,
      parts: [w1, w2, w3],
      tasks: byId,
      render: card,
      focus: aMoving1.id,
    }),
  );

  // Code, goal (title), state badge
  assert.match(t2CardHtml, /<span class="agent-code">T02<\/span>/);
  assert.match(t2CardHtml, /Moving orchestrator/);
  assert.match(
    t2CardHtml,
    /<span class="status-badge tone-running">[\s\S]*?<span>In progress<\/span><\/span>/,
  );

  // Progress: segmented bar, counts (1/3), parts detail (1 done, 1 running, 1 waiting)
  assert.match(t2CardHtml, /<span class="azima-bar-done" style="width:33\.33/);
  assert.match(
    t2CardHtml,
    /<span class="azima-bar-running" style="width:33\.33/,
  );
  assert.match(t2CardHtml, /<span class="azima-progress-counts">1\/3<\/span>/);
  assert.match(
    t2CardHtml,
    /<span class="azima-progress-parts"[^>]*>1 done, 1 running, 1 waiting<\/span>/,
  );

  // Time: azimaTime formats running duration
  assert.match(t2CardHtml, /<span class="task-time"/);

  // Parts wrapped in .card-grid.task-grid inside .azima-parts (flowing in columns on wide screens)
  assert.match(
    t2CardHtml,
    /<div class="azima-parts"><div class="card-grid task-grid">/,
  );

  // 6. Test azimaTime function directly
  const nowMs = 200_000;
  const runningTime = s.azimaTime(aMoving1, [w1, w2, w3], nowMs);
  assert.ok(runningTime.text.length > 0);
  assert.match(runningTime.title, /running since/i);

  const doneTime = s.azimaTime(aDone1, [], nowMs);
  assert.equal(doneTime.text, "30s");
  assert.match(doneTime.title, /ran from .* to/i);
});

test("the wish shows its push strategy and azima cards show branch, sync and PR in per-azima mode", () => {
  // 1. Effective strategy resolution
  const wDefault = { id: "w1", pushStrategy: s.PushStrategy.UNSPECIFIED };
  const pDefault = [{ id: "p1", pushStrategy: s.PushStrategy.UNSPECIFIED }];
  const pAzima = [{ id: "p1", pushStrategy: s.PushStrategy.AZIMA }];
  const wWish = { id: "w1", pushStrategy: s.PushStrategy.WISH };
  const wAzima = { id: "w1", pushStrategy: s.PushStrategy.AZIMA };

  assert.equal(
    s.effectivePushStrategy(wDefault, pDefault),
    s.PushStrategy.WISH,
  );
  assert.equal(s.effectivePushStrategy(wDefault, pAzima), s.PushStrategy.AZIMA);
  assert.equal(s.effectivePushStrategy(wWish, pAzima), s.PushStrategy.WISH);
  assert.equal(s.effectivePushStrategy(wAzima, pDefault), s.PushStrategy.AZIMA);

  // 2. PushStrategySelector component
  let chosenStrategy = null;
  const selectorHtml = s.renderToStaticMarkup(
    h(s.PushStrategySelector, {
      wish: wDefault,
      projects: pAzima,
      onChange: (strat) => {
        chosenStrategy = strat;
      },
    }),
  );
  assert.match(selectorHtml, /class="wish-push-strategy"/);
  assert.match(selectorHtml, /aria-label="Push strategy"/);
  assert.match(selectorHtml, /Per wish<\/button>/);
  assert.match(
    selectorHtml,
    /<button[^>]*class="active"[^>]*>Per azima<\/button>/,
  );

  // Test live clicking
  const liveSelector = live(s.PushStrategySelector, {
    wish: wDefault,
    projects: pAzima,
    onChange: (strat) => {
      chosenStrategy = strat;
    },
  });
  const buttons = liveSelector.all("button");
  const wishBtn = buttons.find((b) => b.props.children === "Per wish");
  assert.ok(wishBtn);
  wishBtn.props.onClick();
  assert.equal(chosenStrategy, s.PushStrategy.WISH);

  // 3. Azima branch naming helper
  const taskT27 = {
    id: "a27",
    code: "T27",
    title: "Djinn stays fast",
    azima: {},
  };
  assert.equal(s.azimaBranchName(taskT27), "djinn/T27-djinn-stays-fast");

  // 4. AzimaCard in default (per-wish) mode does not show branch, sync or PR
  const defaultCardHtml = s.renderToStaticMarkup(
    h(s.AzimaCard, {
      azima: {
        ...taskT27,
        azima: {
          branch: "djinn/T27-djinn-stays-fast",
          sync: { ahead: 1, behind: 0 },
        },
      },
      parts: [],
      tasks: new Map(),
      render: () => null,
      isPerAzima: false,
    }),
  );
  assert.doesNotMatch(defaultCardHtml, /azima-branch/);
  assert.doesNotMatch(defaultCardHtml, /azima-actions/);

  // 5. AzimaCard in per-azima mode with branch, base branch, sync, push button, and PR
  let pushedAzima = null;
  let openedPrUrl = null;
  const azimaPerAzima = {
    ...taskT27,
    azima: {
      branch: "djinn/T27-djinn-stays-fast",
      baseBranch: "djinn/T26-split-the-branch",
      sync: { ahead: 2, behind: 0 },
      pr: {
        state: s.AzimaPrState.OPEN,
        url: "https://github.com/Empowill/Djinn/pull/42",
      },
    },
  };

  const perAzimaHtml = s.renderToStaticMarkup(
    h(s.AzimaCard, {
      azima: azimaPerAzima,
      parts: [],
      tasks: new Map(),
      render: () => null,
      isPerAzima: true,
      onPush: (a) => {
        pushedAzima = a;
      },
      onOpenPr: (url) => {
        openedPrUrl = url;
      },
    }),
  );

  // Branch and base branch
  assert.match(
    perAzimaHtml,
    /<span class="azima-branch"[^>]*>djinn\/T27-djinn-stays-fast<\/span>/,
  );
  assert.match(
    perAzimaHtml,
    /<span class="azima-base-branch"[^>]*>based on djinn\/T26-split-the-branch<\/span>/,
  );

  // Sync state and push button
  assert.match(
    perAzimaHtml,
    /<span class="azima-sync-status"[^>]*>2 commits ahead of origin\/<\/span>/,
  );
  assert.match(
    perAzimaHtml,
    /<button[^>]*class="button accent small"[^>]*>Push<\/button>/,
  );

  // PR link with state "PR open"
  assert.match(
    perAzimaHtml,
    /<a href="https:\/\/github\.com\/Empowill\/Djinn\/pull\/42" class="azima-pr-link"/,
  );
  assert.match(perAzimaHtml, /PR open/);

  // Test live click on push and PR
  const liveAzimaCard = live(s.AzimaCard, {
    azima: azimaPerAzima,
    parts: [],
    tasks: new Map(),
    render: () => null,
    isPerAzima: true,
    onPush: (a) => {
      pushedAzima = a;
    },
    onOpenPr: (url) => {
      openedPrUrl = url;
    },
  });
  const cardButtons = liveAzimaCard.all("button");
  const pushBtn = cardButtons.find((b) => b.props.children === "Push");
  assert.ok(pushBtn);
  const fakeEvent = { stopPropagation() {}, preventDefault() {} };
  pushBtn.props.onClick(fakeEvent);
  assert.equal(pushedAzima?.id, "a27");

  const prLink = liveAzimaCard
    .all("a")
    .find((a) => a.props.className === "azima-pr-link");
  assert.ok(prLink);
  prLink.props.onClick(fakeEvent);
  assert.equal(openedPrUrl, "https://github.com/Empowill/Djinn/pull/42");

  // 6. Test pushed state (clean sync) and other PR states (PROPOSED, MERGED, and badge without URL)
  const cleanAndProposedHtml = s.renderToStaticMarkup(
    h(s.AzimaCard, {
      azima: {
        ...taskT27,
        azima: {
          branch: "djinn/T27-djinn-stays-fast",
          sync: { ahead: 0, behind: 0, target: "origin" },
          pr: { state: s.AzimaPrState.PROPOSED },
        },
      },
      parts: [],
      tasks: new Map(),
      render: () => null,
      isPerAzima: true,
    }),
  );
  assert.match(
    cleanAndProposedHtml,
    /<span class="azima-pushed-status">Pushed<\/span>/,
  );
  assert.match(
    cleanAndProposedHtml,
    /<span class="azima-pr-badge">PR proposed<\/span>/,
  );
  assert.doesNotMatch(cleanAndProposedHtml, /<button[^>]*>Push<\/button>/);

  const mergedHtml = s.renderToStaticMarkup(
    h(s.AzimaCard, {
      azima: {
        ...taskT27,
        azima: {
          sync: { ahead: 0, behind: 0, branch: "main" },
          pr: { state: s.AzimaPrState.MERGED },
        },
      },
      parts: [],
      tasks: new Map(),
      render: () => null,
      isPerAzima: true,
    }),
  );
  assert.match(mergedHtml, /<span class="azima-pushed-status">Pushed<\/span>/);
  assert.match(mergedHtml, /<span class="azima-pr-badge">PR merged<\/span>/);

  // 7. Verify T27: AzimaCard is memoized (React.memo)
  assert.equal(typeof s.AzimaCard, "object");
  assert.equal(s.AzimaCard.$$typeof, Symbol.for("react.memo"));
});

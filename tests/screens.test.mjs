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
export { SyncLine, SyncLines } from "@/src/project-sync.tsx";
export { WishQuestion } from "@/src/wish-question.tsx";
export { WishView } from "@/src/wish-view.tsx";
export {
  WishTask,
  TurnCommentsButton,
  promptPreview,
  isGreenWait,
  compactGreenWait,
} from "@/src/wish-task.tsx";
export {
  azimaTime,
  shortModel,
  effectivePushStrategy,
  azimaBranchName,
  recordModelAlias,
  clearModelAliases,
  taskModelDisplay,
  isPrincipal,
  principalWish,
  runningOf,
  fixerOf,
} from "@/src/data/format.ts";
export { FlightPlan } from "@/src/flight-plan.tsx";
export {
  attentionOf,
  AttentionBar,
  tabOf,
  wayTo,
} from "@/src/attention.tsx";
export { DecisionLog } from "@/src/decision-log.tsx";
export { TaskSections, ViewTabs } from "@/src/task-tabs.tsx";
export { ReviewTab, ReviewLine } from "@/src/review-tab.tsx";
export { reviewRows, reviewLeft, toReview } from "@/src/data/review.ts";
export {
  azimaGroups,
  azimaRank,
  draftAzimas,
  finishedTasks,
  flightPlan,
  mergeNest,
  movingTasks,
  taskDealtWith,
  waitingLeadQuestions,
  waitingTasks,
  workCount,
  spent,
} from "@/src/data/flight.ts";
export { AzimaCard, azimaFinished, DraftAzimaCard } from "@/src/azima.tsx";
export {
  FolderField,
  MakeWish,
  ProjectChecks,
  ProjectPanel,
  ShortcutField,
  WatchCommands,
} from "@/src/wish-dialogs.tsx";
export {
  LastPushes,
  LeadButton,
  LeadMenu,
  MainMerges,
  PushStrategySelector,
  WhereWorkLands,
  BranchPicker,
  WishDescription,
  WishTitle,
  leadTrouble,
  recordedAgent,
} from "@/src/wish-head.tsx";
export { UpdateBannerView, readyKey } from "@/src/update-banner.tsx";
export { platformShortcut } from "@/src/tooltip.tsx";
export {
  digitKey,
  learnDigitKey,
  pageDigit,
  pageShortcut,
} from "@/src/page-keys.ts";
export { PageHeaderAction } from "@/src/page-header.tsx";
export { InstallStep } from "@/gen/ts/ui/v1/ui_pb.ts";
export {
  memory,
  resourcesDetail,
  setUsageUnit,
  getUsageUnit,
  SpentLine,
} from "@/src/usage.tsx";
export { TilasmList, newestTilasm } from "@/src/tilasms.tsx";
export { AgentBlocks } from "@/src/agent-blocks.tsx";
export { MarkdownBody } from "@/src/markdown-body.tsx";
export { CopyButton } from "@/src/copy-command.tsx";
export { Link, MoveLink } from "@/src/link.tsx";
export * as dew from "@/e2e/report-question.ts";
export { SafeSvg, sanitizeSvg } from "@/src/safe-svg.tsx";
export { openDjinnLink, parseDjinnLink } from "@/src/data/links.ts";
export { ConnectError, Code } from "@connectrpc/connect";
export {
  entriesFromFiles,
  entriesOf,
  readDrop,
} from "@/src/data/tilasms.ts";
export { TilasmService } from "@/gen/ts/plan/v1/tilasm_pb.ts";
export { ConsumptionPanel, WorkerRow } from "@/src/consumption-panel.tsx";
export { ResourceChart } from "@/src/resource-chart.tsx";
export {
  MachineService,
  CommandService,
  GateService,
} from "@/gen/ts/machine/v1/machine_pb.ts";
export {
  HOURGLASS,
  Hourglass,
  HourglassLine,
  HourglassSections,
} from "@/src/hourglass.tsx";
export {
  TaskDiffView,
  UnifiedView,
  SplitView,
  CommentBox,
  appendChunk,
} from "@/src/diff.tsx";
export { setPriceTable } from "@/src/data/prices.ts";
export {
  OperatingLoad,
  OperatingLoadSlider,
  OperatingLoadForecast,
  loadManager,
  NOTCHES,
  notchLabel,
  notchSlug,
} from "@/src/operating-load.tsx";
export { LoadNotch } from "@/gen/ts/djinn/v1/load_pb.ts";
export * from "@/gen/ts/plan/v1/plan_pb.ts";`,
);
const h = s.createElement;

// A fixture price table keeps screen tests independent of changes to internal/usage/prices.yaml.
s.setPriceTable({
  date: "2026-01-01",
  currency: "USD",
  models: {
    "claude-sonnet-5-5": {
      input: 2.0,
      output: 10.0,
      cacheRead: 0.1,
      cacheWrite: 2.5,
      thinking: 10.0,
      source: "https://example.com/pricing",
      date: "2026-01-01",
    },
    "gpt-5.5": {
      input: 5.0,
      output: 30.0,
      cacheRead: 0.5,
      cacheWrite: 0.0,
      thinking: 30.0,
      source: "https://example.com/pricing",
      date: "2026-01-01",
    },
    "gemini-3.8-flash-high": {
      input: 0.75,
      output: 3.75,
      cacheRead: 0.075,
      cacheWrite: 0.0,
      thinking: 3.75,
      source: "https://example.com/pricing",
      date: "2026-01-01",
    },
  },
});

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
      useRef(initial) {
        const i = at++;
        if (!(i in states)) states[i] = { current: initial };
        return states[i];
      },
      useEffect() {},
      useLayoutEffect() {},
      useMemo(make) {
        return make();
      },
      useCallback(fn) {
        return fn;
      },
      useId() {
        return ":live:";
      },
      useSyncExternalStore(subscribe, getSnapshot) {
        return getSnapshot();
      },
      // A context reads its default: no provider above.
      use(context) {
        return context._currentValue;
      },
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
  assert.match(
    html,
    /<div class="sidebar-section-label"><svg[^>]*lucide-sparkles[^>]*>[^]*?<\/svg><span>Wishes<\/span><span class="nav-counter "/,
  );
  assert.match(
    html,
    /<div class="sidebar-section-label project-section-label"><svg[^>]*lucide-folder[^>]*>[^]*?<\/svg><span>Projects<\/span><button title="Create a project"/,
  );
  assert.match(html, />2\/3</);
  assert.ok(html.indexOf("Ship the lamp") < html.indexOf("Polish the brass"));
  assert.match(html, /Paused/);
  assert.match(html, /1 granted/);
  // Granted wishes stay folded until asked.
  assert.doesNotMatch(html, /Light it/);
  // Every wish shown is dragged by its grip, on the left of its row: the active ones to a new rank, a paused one among
  // them or among the paused ones (Q01).
  assert.equal(html.match(/draggable="true"/g).length, 3);
  assert.equal(
    html.match(
      /draggable="true"><span class="wish-grip" aria-hidden="true"><svg[^>]*lucide-grip-vertical/g,
    ).length,
    3,
  );
  assert.match(
    html,
    /title="Trim the wick \(drag, or Alt\+Arrow, to change its place/,
  );
  // A paused wish goes first with its button.
  assert.equal(html.match(/Make it the first active wish/g).length / 2, 1);
  assert.match(html, /mission-nav wish-nav selected/);
  // An active wish takes a second line under its title, its rank at the end; a paused one keeps a single line.
  assert.equal(html.match(/class="wish-counts"/g).length, 2);
  assert.match(
    html,
    /<span class="wish-title">Polish the brass<\/span><span class="wish-counts">/,
  );
  assert.match(
    html,
    /<span class="wish-title">Trim the wick<\/span><\/button>/,
  );
  // A wish ready to grant says it there, its icon the clipboard to check, not a raised hand.
  assert.match(html, /wish-tone tone-proof/);
  assert.match(html, />ready to grant<\/span><small class="wish-rank">/);
  // Each wish says at a glance what waits and what runs: an icon and a count, with their words, on its second line.
  const sidebar = (
    collapsed,
    count = { questions: 2, running: 1, watching: 1, merging: 0 },
  ) =>
    s.renderToStaticMarkup(
      h(s.WishSidebar, {
        wishes: [wish("w1", "Ship the lamp", s.WishState.ACTIVE, 1)],
        projects: [],
        counts: { w1: count },
        onSelectPlan() {},
        selectedWishId: "",
        selectedProjectId: "",
        collapsed,
        onSelectWish() {},
        onSelectProject() {},
        onMove() {},
        onNewProject() {},
      }),
    );
  const counted = sidebar(false);
  const row = counted.slice(counted.indexOf("wish-nav"));
  assert.match(row, /aria-label="2 questions wait for your answer."/);
  assert.match(row, /aria-label="1 running"[^>]*><span class="live-dot"/);
  // A watcher is counted apart: it waits for its command to print, it does not work.
  assert.match(row, /count-pill tone-watching" title="1 watching"/);
  // What waits for you first, then what works, then what watches, then the rank.
  const at = (text) => row.indexOf(text);
  assert.ok(at('title="2 questions') < at('title="1 running"'));
  assert.ok(at('title="1 running"') < at('title="1 watching"'));
  assert.ok(at('title="1 watching"') < at("wish-rank"));
  // The questions have their count: no raised hand repeats it beside the title, nor on the count (Q01). The icon
  // says what the wish does.
  assert.match(row, /wish-tone tone-running asks/);
  assert.match(row, /lucide-message-circle-question/);
  // The hand only waits for a window under 820px wide, whose CSS hides the second line: it takes the icon's place.
  assert.equal(row.match(/lucide-hand/g).length, 1);
  assert.match(
    row,
    /<span class="wish-hand tone-waiting"><svg[^>]*lucide-hand[^>]*>.*?<\/svg><\/span><\/span>/,
  );
  // A wish with no question carries no hand.
  assert.doesNotMatch(
    sidebar(false, { questions: 0, running: 1, watching: 0, merging: 0 }),
    /wish-hand|lucide-hand/,
  );
  // Folded to the rail, the icon is all that shows: it keeps the hand, and no count.
  const rail = sidebar(true);
  assert.match(rail, /wish-tone tone-waiting /);
  assert.doesNotMatch(rail, /wish-hand/);
  assert.doesNotMatch(rail, /wish-counts/);
  assert.doesNotMatch(rail, /wish-grip/);
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

test("the side panel counts the new inbox items on the flying carpet's entry, even with no active wish", () => {
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
  // Nothing active, nothing new: no flying carpet.
  assert.doesNotMatch(sidebar([paused], 0), /plan-nav/);
  // Two new items: the flying carpet shows them, its entry counts them.
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
    /<div class="option selected" role="button" tabindex="0" aria-pressed="true"><strong class="option-letter">A</,
  );
  assert.match(
    open,
    /title="Answer with the selected option and your note \(↵\)"><svg[^]*?Rub the lamp<kbd class="key-hint" aria-hidden="true">↵<\/kbd><\/button>/,
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

  // With the lamp's writes: Move at the top, then Withdraw on the left, Enlighten in the middle, and the lamp on the right.
  const lamp = s.renderToStaticMarkup(
    h(s.WishQuestion, {
      question: {
        id: "q1",
        code: "Q01",
        text: "Which oil?",
        options: ["Olive", "Paraffin"],
        recommendation: "B: brighter.",
        context: "## Cost\n\nOlive is dearer.",
        rounds: [],
        revision: 0,
      },
      blocking: ["W2"],
      onAnswer: async () => {},
      onEnlighten: async () => {},
      onMove: () => {},
      onWithdraw: () => {},
    }),
  );
  // Move at the top; Withdraw on the left, Enlighten in the middle, then the lamp; B, recommended, is the one rubbed.
  assert.match(
    lamp,
    /<button type="button" class="button secondary small question-move"><svg[^>]*>.*?<\/svg>Move to another wish…<\/button>/,
  );
  assert.ok(
    lamp.indexOf("Withdraw question…") < lamp.indexOf(">Enlighten me<kbd"),
  );
  assert.ok(lamp.indexOf(">Enlighten me<kbd") < lamp.indexOf("Rub the lamp"));
  assert.match(
    lamp,
    /<div class="option selected" role="button" tabindex="0" aria-pressed="true"><strong class="option-letter">B</,
  );
  assert.doesNotMatch(
    lamp,
    /mark-toggle|question-menu|Your answer goes to the Djinn/,
  );
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
    }),
  );
  assert.doesNotMatch(vague, /option selected/);
  assert.match(
    vague,
    /title="Pick an option first" disabled=""><svg[^]*?Rub the lamp<kbd class="key-hint" aria-hidden="true">↵<\/kbd><\/button>/,
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
  // A request no one took on shows its age, to the minute (W557).
  const old = live(s.WishQuestion, {
    question: {
      id: "q2",
      code: "Q02",
      text: "Which wick?",
      options: ["Cotton", "Hemp"],
      recommendation: "A: it burns even.",
      rounds: [
        {
          kind: s.RoundKind.ENLIGHTEN,
          note: "Burn time?",
          createTime: {
            seconds: BigInt(
              Math.floor(Date.now() / 1000) - (4 * 3600 + 12 * 60 + 20),
            ),
            nanos: 0,
          },
        },
      ],
    },
    onAnswer: async () => {},
    onEnlighten: async () => {},
  });
  assert.match(old.html(), /Being investigated for 4h12m/);
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
    /placeholder="A note with your choice, or what to look into for Enlighten me \(optional · ⇧↵ new line\)"/,
  );
  assert.match(
    open,
    /<button class="button secondary lamp-enlighten" title="Find out more before deciding: the Djinn looks into what your note asks, then revises the question \(⌃⇧↵\)">/,
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
    /placeholder="Your answer, or what to look into for Enlighten me \(⇧↵ new line\)"/,
  );
  kept.one("textarea").onChange({ target: { value: "Cotton?" } });
  kept.one("lamp-enlighten").onClick();
  await kept.settle();
  assert.match(kept.html(), /<textarea[^>]*>Cotton\?<\/textarea>/);
});

// A gesture shows on its card, by key as by click: its button is down while the answer waits, then the answer leaves
// as it comes back up; the card keeps the gesture's colour, and a wisp rises from the chosen button only.
test("a gesture presses its button and the card answers back in its colour", async () => {
  const answered = [];
  const enlightened = [];
  const card = live(s.WishQuestion, {
    question: {
      id: "q1",
      code: "Q01",
      text: "Which oil?",
      options: ["Olive", "Paraffin"],
      recommendation: "A: it smells good.",
    },
    onAnswer: async (choice) => {
      answered.push(choice);
    },
    onEnlighten: async (note) => {
      enlightened.push(note);
    },
  });
  assert.doesNotMatch(card.html(), /gesture-|is-pressed|question-wisp/);

  // Ctrl+Enter on the card: the lamp is down, the card plays the rub, nothing has left yet.
  card.one("question-card").onKeyDown({
    key: "Enter",
    ctrlKey: true,
    preventDefault() {},
  });
  let html = card.html();
  assert.match(html, /class="question-card open can-wait gesture-rub"/);
  assert.match(html, /class="button accent lamp-rub is-pressed"/);
  assert.match(
    html,
    /<span class="question-wisp" aria-hidden="true"><\/span><\/button>/,
  );
  assert.equal(html.match(/question-wisp/g).length, 1);
  assert.match(html, /class="button secondary lamp-enlighten"/);
  assert.equal(answered.length, 0);

  // Back up: the answer left, the card still glows in the lamp's colour.
  await card.settle();
  assert.deepEqual(answered, [s.Choice.A]);
  html = card.html();
  assert.match(html, /class="button accent lamp-rub"/);
  assert.match(html, /gesture-rub/);

  // A click plays the same; Enlighten me plays its own, the wisp on its button.
  const other = live(s.WishQuestion, {
    question: { id: "q2", code: "Q02", text: "Which wick?", options: [] },
    onAnswer: async () => {},
    onEnlighten: async (note) => {
      enlightened.push(note);
    },
  });
  other.one("lamp-enlighten").onClick();
  html = other.html();
  assert.match(html, /class="question-card open can-wait gesture-enlighten"/);
  assert.match(html, /class="button secondary lamp-enlighten is-pressed"/);
  assert.match(
    html,
    /⇧↵<\/kbd><span class="question-wisp" aria-hidden="true">/,
  );
  await other.settle();
  assert.deepEqual(enlightened, [""]);

  // Refused: the card stops playing, as it was.
  const refused = live(s.WishQuestion, {
    question: { id: "q3", code: "Q03", text: "Which lamp?", options: [] },
    onAnswer: async () => {
      throw new Error("refused");
    },
  });
  refused.one("lamp-rub").onClick();
  assert.match(refused.html(), /gesture-rub/);
  await refused.settle();
  assert.doesNotMatch(refused.html(), /gesture-|is-pressed|question-wisp/);
});

test("keyboard shortcuts on a question card: Enter, Shift+Enter, Ctrl+Enter, Enlighten, Withdraw, Move, composing, arrows, 1-4, and macOS hints", async () => {
  const answered = [];
  const enlightened = [];
  const moved = [];
  const withdrawn = [];
  const card = live(s.WishQuestion, {
    question: {
      id: "q1",
      code: "Q01",
      text: "Which oil?",
      options: ["Olive", "Paraffin", "Canola", "Sesame"],
      recommendation: "A: it smells good.",
    },
    onAnswer: async (choice, note) => {
      answered.push({ choice, note });
    },
    onEnlighten: async (note) => {
      enlightened.push(note);
    },
    onMove: (q) => {
      moved.push(q.code);
    },
    onWithdraw: (q) => {
      withdrawn.push(q.code);
    },
  });

  // 1. Initial state: option A is recommended and preselected.
  assert.match(
    card.html(),
    /<div class="option selected" role="button" tabindex="0" aria-pressed="true"><strong class="option-letter">A</,
  );
  assert.doesNotMatch(card.html(), /question-hint unpicked/);

  // Arrow navigation: Down/Right moves next (wraps), Up/Left moves previous.
  let prevented = false;
  card.one("question-card").onKeyDown({
    key: "ArrowDown",
    preventDefault: () => {
      prevented = true;
    },
  });
  assert.ok(prevented);
  assert.match(
    card.html(),
    /<div class="option selected" role="button" tabindex="0" aria-pressed="true"><strong class="option-letter">B</,
  );

  card.one("question-card").onKeyDown({
    key: "ArrowRight",
    preventDefault: () => {},
  });
  assert.match(
    card.html(),
    /<div class="option selected" role="button" tabindex="0" aria-pressed="true"><strong class="option-letter">C</,
  );

  card.one("question-card").onKeyDown({
    key: "ArrowLeft",
    preventDefault: () => {},
  });
  assert.match(
    card.html(),
    /<div class="option selected" role="button" tabindex="0" aria-pressed="true"><strong class="option-letter">B</,
  );

  card.one("question-card").onKeyDown({
    key: "ArrowUp",
    preventDefault: () => {},
  });
  assert.match(
    card.html(),
    /<div class="option selected" role="button" tabindex="0" aria-pressed="true"><strong class="option-letter">A</,
  );

  // Number keys 1-4 pick options
  card.one("question-card").onKeyDown({
    key: "4",
    preventDefault: () => {},
  });
  assert.match(
    card.html(),
    /<div class="option selected" role="button" tabindex="0" aria-pressed="true"><strong class="option-letter">D</,
  );

  card.one("question-card").onKeyDown({
    key: "2",
    preventDefault: () => {},
  });
  assert.match(
    card.html(),
    /<div class="option selected" role="button" tabindex="0" aria-pressed="true"><strong class="option-letter">B</,
  );

  // Typing in the textarea
  card.one("textarea").onChange({ target: { value: "Pure and filtered" } });

  // IME composition: isComposing ignores Enter
  card.one("textarea").onKeyDown({
    key: "Enter",
    isComposing: true,
    preventDefault: () => {},
  });
  await card.settle();
  assert.equal(answered.length, 0);

  // Shift+Enter in textarea does not answer (allows native newline)
  let shiftPrevented = false;
  card.one("textarea").onKeyDown({
    key: "Enter",
    shiftKey: true,
    preventDefault: () => {
      shiftPrevented = true;
    },
  });
  await card.settle();
  assert.equal(shiftPrevented, false);
  assert.equal(answered.length, 0);

  // Ctrl+Shift+Enter in textarea triggers Enlighten me, sending note text and clearing note
  let enlightenPrevented = false;
  card.one("textarea").onKeyDown({
    key: "Enter",
    ctrlKey: true,
    shiftKey: true,
    preventDefault: () => {
      enlightenPrevented = true;
    },
  });
  await card.settle();
  assert.ok(enlightenPrevented);
  assert.deepEqual(enlightened, ["Pure and filtered"]);
  assert.match(card.html(), /<textarea[^>]*><\/textarea>/);

  // Type new note, then Enter in textarea rubs the lamp
  card.one("textarea").onChange({ target: { value: "Pressed today" } });
  let enterPrevented = false;
  card.one("textarea").onKeyDown({
    key: "Enter",
    preventDefault: () => {
      enterPrevented = true;
    },
  });
  await card.settle();
  assert.ok(enterPrevented);
  assert.deepEqual(answered, [{ choice: s.Choice.B, note: "Pressed today" }]);

  // Ctrl+Enter on card answers as well
  card.one("question-card").onKeyDown({
    key: "Enter",
    ctrlKey: true,
    preventDefault: () => {},
  });
  await card.settle();
  assert.equal(answered.length, 2);

  // Cmd+Shift+Enter on card triggers enlighten
  card.one("textarea").onChange({ target: { value: "From card" } });
  card.one("question-card").onKeyDown({
    key: "Enter",
    metaKey: true,
    shiftKey: true,
    preventDefault: () => {},
  });
  await card.settle();
  assert.deepEqual(enlightened, ["Pure and filtered", "From card"]);

  // Move button at the top, Withdraw button at the bottom, and Mod+Shift+Backspace on the card and in the textarea
  card.one("question-move").onClick();
  assert.deepEqual(moved, ["Q01"]);
  card.one("lamp-withdraw").onClick();
  assert.deepEqual(withdrawn, ["Q01"]);
  let withdrawPrevented = false;
  card.one("question-card").onKeyDown({
    key: "Backspace",
    ctrlKey: true,
    shiftKey: true,
    preventDefault: () => {
      withdrawPrevented = true;
    },
  });
  assert.ok(withdrawPrevented);
  assert.deepEqual(withdrawn, ["Q01", "Q01"]);
  let textareaWithdrawPrevented = false;
  card.one("textarea").onKeyDown({
    key: "Backspace",
    metaKey: true,
    shiftKey: true,
    preventDefault: () => {
      textareaWithdrawPrevented = true;
    },
  });
  assert.ok(textareaWithdrawPrevented);
  assert.deepEqual(withdrawn, ["Q01", "Q01", "Q01"]);
  // Plain Backspace or Ctrl+Alt+Backspace does not withdraw
  card.one("question-card").onKeyDown({
    key: "Backspace",
    preventDefault: () => {},
  });
  card.one("question-card").onKeyDown({
    key: "Backspace",
    ctrlKey: true,
    shiftKey: true,
    altKey: true,
    preventDefault: () => {},
  });
  assert.equal(withdrawn.length, 3);

  // Question with no recommendation: Enter without option selected does not answer and shows alert
  const unselectedAnswered = [];
  const cardNoRec = live(s.WishQuestion, {
    question: {
      id: "q2",
      code: "Q02",
      text: "Which wick?",
      options: ["Cotton", "Linen"],
      recommendation: "",
    },
    onAnswer: async (choice, note) => {
      unselectedAnswered.push({ choice, note });
    },
  });
  assert.doesNotMatch(cardNoRec.html(), /option selected/);
  assert.doesNotMatch(cardNoRec.html(), /question-hint unpicked/);

  // Ctrl+Enter on card warns
  cardNoRec.one("question-card").onKeyDown({
    key: "Enter",
    ctrlKey: true,
    preventDefault: () => {},
  });
  await cardNoRec.settle();
  assert.equal(unselectedAnswered.length, 0);
  assert.match(
    cardNoRec.html(),
    /<p class="question-hint unpicked" role="alert">.*Pick an option first<\/p>/,
  );

  // Enter on a focused option picks it and answers nothing (W574), as a click does.
  const optionB = {};
  cardNoRec.all("option")[1].props.onKeyDown({
    key: "Enter",
    target: optionB,
    currentTarget: optionB,
    preventDefault: () => {},
  });
  await cardNoRec.settle();
  assert.equal(unselectedAnswered.length, 0);
  assert.match(
    cardNoRec.html(),
    /<div class="option selected" role="button" tabindex="0" aria-pressed="true"><strong class="option-letter">B</,
  );
  cardNoRec.all("option")[0].props.onClick();
  await cardNoRec.settle();
  assert.equal(unselectedAnswered.length, 0);

  // Pressing 1 picks first option and clears unpicked warning
  cardNoRec.one("question-card").onKeyDown({
    key: "1",
    preventDefault: () => {},
  });
  assert.match(
    cardNoRec.html(),
    /<div class="option selected" role="button" tabindex="0" aria-pressed="true"><strong class="option-letter">A</,
  );
  assert.doesNotMatch(cardNoRec.html(), /question-hint unpicked/);

  // Enter alone on the card answers nothing (W574): a click on its text leaves the focus there.
  cardNoRec.one("question-card").onKeyDown({
    key: "Enter",
    preventDefault: () => {},
  });
  await cardNoRec.settle();
  assert.equal(unselectedAnswered.length, 0);
  assert.doesNotMatch(cardNoRec.html(), /gesture-/);

  // Now Ctrl+Enter answers
  cardNoRec.one("question-card").onKeyDown({
    key: "Enter",
    ctrlKey: true,
    preventDefault: () => {},
  });
  await cardNoRec.settle();
  assert.deepEqual(unselectedAnswered, [{ choice: s.Choice.A, note: "" }]);

  // macOS symbols on macOS
  globalThis.__DJINN_MAC__ = true;
  try {
    const macMarkup = s.renderToStaticMarkup(
      h(s.WishQuestion, {
        question: {
          id: "q3",
          code: "Q03",
          text: "Mac test?",
          options: ["Yes", "No"],
          recommendation: "",
        },
        onAnswer: async () => {},
        onEnlighten: async () => {},
        onWithdraw: () => {},
      }),
    );
    assert.match(
      macMarkup,
      /<kbd class="key-hint" aria-hidden="true">⌘⇧⌫<\/kbd>/,
    );
    assert.match(
      macMarkup,
      /title="Close this question without an answer \(⌘⇧⌫\)"/,
    );
    assert.match(
      macMarkup,
      /<kbd class="key-hint" aria-hidden="true">⌘⇧↵<\/kbd>/,
    );
    assert.match(
      macMarkup,
      /title="Find out more before deciding: the Djinn looks into what your note asks, then revises the question \(⌘⇧↵\)"/,
    );
  } finally {
    delete globalThis.__DJINN_MAC__;
  }
});

// A shortcut reads ⌘ on a Mac and Ctrl elsewhere, in the tooltip and in aria-keyshortcuts; an icon action of a page
// header names itself by its label and keeps its explanation for the tooltip, not a native title.
test("shortcuts follow the platform, and header actions carry tooltips, not titles", () => {
  globalThis.__DJINN_MAC__ = true;
  try {
    assert.deepEqual(s.platformShortcut("N"), {
      label: "⌘ N",
      aria: "Meta+N",
    });
  } finally {
    delete globalThis.__DJINN_MAC__;
  }
  globalThis.__DJINN_MAC__ = false;
  try {
    assert.deepEqual(s.platformShortcut("\\"), {
      label: "Ctrl \\",
      aria: "Control+\\",
    });
  } finally {
    delete globalThis.__DJINN_MAC__;
  }
  const action = s.renderToStaticMarkup(
    h(s.PageHeaderAction, { label: "Share", title: "Export the wish" }),
  );
  assert.match(action, /aria-label="Share"/);
  assert.doesNotMatch(action, /title=/);
  // Closed, the tooltip is not in the page.
  assert.doesNotMatch(action, /role="tooltip"/);
});

test("a question reads top-down: its title, what is at stake folded, its report, the recommendation, the options, the note, then the gestures", () => {
  const wishId = "01a130b1-398a-7072-be60-ce928cb7241e";
  const report = {
    id: "01a130d9-0000-7000-8000-000000000001",
    wishId,
    kind: "report",
    title: s.dew.REPORT_TITLE,
    content: s.dew.REPORT,
  };
  const html = s.renderToStaticMarkup(
    h(s.WishQuestion, {
      question: {
        id: "q4",
        code: "Q04",
        wishId,
        text: s.dew.QUESTION,
        options: s.dew.options(wishId),
        context: s.dew.CONTEXT,
        recommendation: s.dew.RECOMMENDATION,
        before: "before the implementation starts",
        move: true,
        rounds: [],
      },
      report,
      onAnswer: async () => {},
      onEnlighten: async () => {},
      onMove: () => {},
      onWithdraw: () => {},
    }),
  );
  // The explanation comes before the call to act, the note above the gestures at the bottom.
  const at = (text) => {
    const i = html.indexOf(text);
    assert.ok(i >= 0, `the card shows ${text}`);
    return i;
  };
  const order = [
    "Will you run the command?",
    "Move to another wish…",
    "What is at stake</h4>",
    s.dew.CONTEXT_FIRST,
    "Read more",
    `Report: ${s.dew.REPORT_TITLE}`,
    "Recommendation · A",
    '<strong class="option-letter">A</strong>',
    '<strong class="option-letter">B</strong>',
    "<textarea",
    "Withdraw question…",
    ">Enlighten me<kbd",
    "Rub the lamp",
  ].map(at);
  assert.deepEqual(
    order,
    [...order].sort((a, b) => a - b),
  );
  assert.doesNotMatch(html, /Your answer goes to the Djinn/);
  // Its before words stay on its badge.
  assert.match(
    html,
    /status-badge tone-waiting[^>]*>.*before the implementation starts/,
  );
  // The long context shows its first section; Read more unfolds the rest in place.
  assert.match(
    html,
    /<button type="button" class="read-more" aria-expanded="false"><svg[^>]*>.*?<\/svg>Read more<\/button>/,
  );
  assert.doesNotMatch(html, new RegExp(s.dew.CONTEXT_LATER));
  // The report it names is one click away, folded.
  assert.match(
    html,
    /<button type="button" class="question-report-link" aria-expanded="false">.*Report: Payroll for acme: feasibility/,
  );
  assert.doesNotMatch(html, /What the fix costs/);
  // The option's command is selectable text with its copy button, inside an option that is no button.
  assert.match(
    html,
    new RegExp(
      `<div class="option selected" role="button" tabindex="0" aria-pressed="true"><strong class="option-letter">A</strong><p>Done: I ran <span class="command"><code>djinn wish allow ${wishId} --mode auto</code><button type="button" class="copy-command" title="Copy the command" aria-label="Copy the command">`,
    ),
  );
  assert.doesNotMatch(html, /<button class="option/);
});

test("a move's links are clickable in its title, context, recommendation and options; its main link is a button", () => {
  const mr = "https://gitlab.com/empowill/app/-/merge_requests/123";
  const ci = "https://gitlab.com/empowill/app/-/pipelines/9";
  const tilasm = "djinn://tilasm/01a130b1-398a-7072-be60-ce928cb7241e";
  const question = {
    id: "q7",
    code: "Q07",
    text: `Review MR !123 and take it out of draft: ${mr}`,
    context: `W12 opened it as a draft; its CI: ${ci}. The diagram: ${tilasm}\n\n\`\`\`sh\nglab mr update 123 --ready\n\`\`\``,
    recommendation: "A: read the diff, then run `glab mr update 123 --ready`",
    options: [`Done: it is ready, ${mr}`, "Not yet"],
    move: true,
    rounds: [],
  };
  const html = s.renderToStaticMarkup(
    h(s.WishQuestion, { question, onAnswer: async () => {} }),
  );
  const a = (href, cls) =>
    cls === "djinn-link"
      ? `<a href="${href}" class="djinn-link">`
      : `<a href="${href}"${cls ? ` class="${cls}"` : ""} target="_blank" rel="noopener noreferrer">`;
  // The title: its link, in the text.
  assert.match(
    html,
    new RegExp(
      `<h3>Review MR !123 and take it out of draft: ${a(mr, "text-link")}${mr}</a></h3>`.replace(
        /[?]/g,
        "\\?",
      ),
    ),
  );
  // The move's main link, the first of the card, a button before what is at stake.
  assert.ok(
    html.includes(
      `<p class="question-move-link">${a(mr, "button secondary small move-link")}<svg`,
    ),
  );
  assert.match(html, /<span>Open MR !123<\/span><\/a><\/p>/);
  assert.ok(
    html.indexOf("question-move-link") < html.indexOf("question-context"),
  );
  // The context: a web address and a bare djinn:// link are links; its command block has a copy button.
  assert.ok(html.includes(`${a(ci)}${ci}</a>`));
  assert.ok(html.includes(`${a(tilasm, "djinn-link")}${tilasm}</a>`));
  assert.match(
    html,
    /<button type="button" aria-label="Copy the code" class="ac-copy">Copy<\/button><\/div><pre><code class="language-sh">glab mr update 123 --ready\n<\/code><\/pre>/,
  );
  // The recommendation's command is copyable.
  assert.match(
    html,
    /Recommendation · A<\/span><div class="ac-markdown"><p>A: read the diff, then run <span class="command"><code>glab mr update 123 --ready<\/code><button type="button" class="copy-command"/,
  );
  // An option's link.
  assert.ok(
    html.includes(`<p>Done: it is ready, ${a(mr, "text-link")}${mr}</a></p>`),
  );

  // A question that is no move has no button; an answered move neither, and its title, in the heading's button, is
  // text.
  const plain = s.renderToStaticMarkup(
    h(s.WishQuestion, {
      question: { ...question, move: false },
      onAnswer: async () => {},
    }),
  );
  assert.doesNotMatch(plain, /move-link/);
  const answered = s.renderToStaticMarkup(
    h(s.WishQuestion, {
      question: {
        ...question,
        answer: { choice: s.Choice.A, note: "" },
      },
      expanded: true,
      onAnswer: async () => {},
    }),
  );
  assert.doesNotMatch(answered, /move-link/);
  assert.ok(
    answered.includes(
      `<h3>Review MR !123 and take it out of draft: ${mr}</h3>`,
    ),
  );
});

test("a link's click opens it and stops there: the option around it is not picked", () => {
  for (const href of [
    "https://gitlab.com/empowill/app/-/merge_requests/123",
    "djinn://tilasm/01a130b1-398a-7072-be60-ce928cb7241e",
  ]) {
    const link = live(s.Link, { href, className: "text-link", children: href });
    let stopped = 0;
    link.one("a").onClick({
      stopPropagation: () => stopped++,
      preventDefault: () => {},
    });
    assert.equal(stopped, 1, href);
  }
  // A link of another scheme is text.
  assert.equal(
    s.renderToStaticMarkup(h(s.Link, { href: "file:///etc/passwd" }, "x")),
    "<span>x</span>",
  );
});

test("a copy button copies the exact command and says so, and never answers the question around it", async () => {
  const copied = [];
  const clipboard = Object.getOwnPropertyDescriptor(navigator, "clipboard");
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: { writeText: async (text) => copied.push(text) },
  });
  try {
    const command =
      "djinn wish allow 01a130b1-398a-7072-be60-ce928cb7241e --mode auto";
    const button = live(s.CopyButton, { text: command });
    let stopped = 0;
    button.one("copy-command").onClick({
      stopPropagation: () => stopped++,
      preventDefault: () => {},
    });
    await button.settle();
    assert.deepEqual(copied, [command]);
    // Its click stops there: the option around it is not picked.
    assert.equal(stopped, 1);
    assert.match(
      button.html(),
      /class="copy-command copied" title="Copied" aria-label="Copied">.*<span class="copy-feedback" role="status">Copied<\/span>/,
    );
  } finally {
    if (clipboard) Object.defineProperty(navigator, "clipboard", clipboard);
    else delete navigator.clipboard;
  }

  // Enter on a copy button presses it: the card does not rub the lamp.
  const answered = [];
  const card = live(s.WishQuestion, {
    question: {
      id: "q4",
      code: "Q04",
      text: s.dew.QUESTION,
      options: s.dew.options("w"),
      recommendation: s.dew.RECOMMENDATION,
    },
    onAnswer: async (choice) => {
      answered.push(choice);
    },
  });
  const onCopy = {
    tagName: "BUTTON",
    closest: (selector) => (selector.startsWith("button") ? {} : null),
  };
  let prevented = false;
  card.one("question-card").onKeyDown({
    key: "Enter",
    target: onCopy,
    preventDefault: () => {
      prevented = true;
    },
  });
  await card.settle();
  assert.deepEqual(answered, []);
  assert.equal(prevented, false);
  // Selecting the words of an option picks nothing; a plain click picks it, and answers nothing either.
  const options = () => card.all("option");
  globalThis.getSelection = () => ({ toString: () => "djinn wish allow" });
  try {
    options()[1].props.onClick();
  } finally {
    delete globalThis.getSelection;
  }
  assert.equal(options()[1].props["aria-pressed"], false);
  options()[1].props.onClick();
  assert.equal(options()[1].props["aria-pressed"], true);
  // Space on a focused option picks it, as on a button.
  const option0 = options()[0].props;
  option0.onKeyDown({
    key: " ",
    target: "self",
    currentTarget: "self",
    preventDefault: () => {},
  });
  assert.equal(options()[0].props["aria-pressed"], true);
  await card.settle();
  assert.deepEqual(answered, []);
  // Enter on a focused option picks it too, and answers nothing (W574).
  options()[1].props.onKeyDown({
    key: "Enter",
    target: "self",
    currentTarget: "self",
    preventDefault: () => {},
  });
  assert.equal(options()[1].props["aria-pressed"], true);
  // Elsewhere on the card, Enter alone answers nothing; Ctrl+Enter rubs the lamp.
  const article = { tagName: "ARTICLE", closest: () => null };
  card.one("question-card").onKeyDown({
    key: "Enter",
    target: article,
    preventDefault: () => {},
  });
  await card.settle();
  assert.deepEqual(answered, []);
  card.one("question-card").onKeyDown({
    key: "Enter",
    ctrlKey: true,
    target: article,
    preventDefault: () => {},
  });
  await card.settle();
  assert.deepEqual(answered, [s.Choice.B]);
});

test("a wish's reports are notes for agents, with no section nor count of their own, and its question links the one it names", async () => {
  const wishId = "01a130b1-398a-7072-be60-ce928cb7241e";
  const dew = wish(wishId, "Dew", s.WishState.ACTIVE, 1);
  const transport = s.createRouterTransport(({ service }) => {
    service(s.WishService, { list: () => ({ wishes: [dew] }) });
    service(s.TaskService, {
      list: () => ({
        tasks: [
          {
            id: "t1",
            wishId,
            code: "W1",
            title: "Study the payroll API",
            status: s.TaskStatus.DONE,
          },
        ],
      }),
    });
    service(s.QuestionService, {
      list: () => ({
        questions: [
          {
            id: "q4",
            wishId,
            code: "Q04",
            text: s.dew.QUESTION,
            options: s.dew.options(wishId),
            context: s.dew.CONTEXT,
            recommendation: s.dew.RECOMMENDATION,
            rounds: [],
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
            taskId: "t1",
            kind: "report",
            title: s.dew.REPORT_TITLE,
            content: s.dew.REPORT,
          },
          {
            id: "b2",
            wishId,
            kind: "hand-off",
            title: "Lexicon",
            content: "x",
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
    h(s.DjinnProvider, { djinn }, h(s.WishView, { wish: dew, onToast() {} })),
  );
  // No section for the reports: neither its heading nor a card, and the report is not shown on the wish.
  assert.doesNotMatch(
    html,
    /wish-reports|id="reports"|report-card|Open the report/,
  );
  assert.doesNotMatch(html, /<h2>Reports/);
  assert.doesNotMatch(html, /What is left to know/);
  // The question names it: its card links it.
  assert.match(
    html,
    /question-report-link.*Report: Payroll for acme: feasibility/,
  );
  // The For agents tab holds it with the other blocks.
  assert.match(
    html,
    /id="view-tab-agents" aria-selected="false" class="discreet">For agents<span class="count">2<\/span>/,
  );
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
  assert.doesNotMatch(
    told,
    /goes to the Djinn|The Djinn is not there to tell|question-hint/,
  );
  const open = props(true, {});
  assert.match(
    open,
    /The Djinn is not there to tell: your answer goes to the workers and waits in the wish&#x27;s brief\./,
  );
  const answer = { answer: { choice: s.Choice.A, note: "" } };
  assert.doesNotMatch(props(false, answer), /The Djinn is not there to tell/);
  assert.match(
    props(true, answer),
    /<p class="no-lead">The Djinn is not there to tell: the answer waits in the wish&#x27;s brief\.<\/p>/,
  );
  const waitingLead = {
    answer: { choice: s.Choice.A, note: "" },
    waitingLead: true,
  };
  const waitingHtml = props(false, waitingLead);
  assert.match(
    waitingHtml,
    /<p class="no-lead">The Djinn is not running to receive your answer: it gets it once it starts\.<\/p>/,
  );
  assert.match(
    waitingHtml,
    /<span class="status-badge tone-waiting">.*Waiting for the Djinn<\/span>/,
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
  assert.match(html, /1 question being investigated by the Djinn/);
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

test("a task shows its tokens and one price: the reported cost, else ≈ the estimate; its hover tells which", () => {
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
  // A reported cost is the price, without ≈; its hover says it was reported, with the tokens in full.
  assert.match(
    claude,
    /<span class="task-usage" title="input 4 · output 409 · cache read 38\.2K · cache written 12\.8K\n\$0\.003 reported">51\.4K tokens · \$0\.003<\/span>/,
  );
  assert.doesNotMatch(claude, /estimated|≈/);

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
  // An estimate reads ≈; its hover gives the table's date.
  assert.match(
    codex,
    /<span class="task-usage" title="input 1\.2K · output 300 · cache read 5K\n\$0\.018 estimated, prices of 2026-01-01">6\.5K tokens · ≈ \$0\.018<\/span>/,
  );
  assert.doesNotMatch(codex, /reported/);

  const unpriced = s.renderToStaticMarkup(
    h(s.WishTask, {
      task: {
        id: "t3",
        code: "W3",
        title: "Test unpriced",
        status: s.TaskStatus.DONE,
        model: "fixture-unpriced",
        usage: usage(100, 200, 0, 0, 0),
      },
      onStop() {},
    }),
  );
  assert.match(unpriced, />300 tokens<\/span>/);
  assert.doesNotMatch(unpriced, />300 tokens · /);
  assert.match(unpriced, /title="[^"]*no price in the table[^"]*"/);
  assert.doesNotMatch(unpriced, /\$/);

  const antigravity = s.renderToStaticMarkup(
    h(s.WishTask, {
      task: {
        id: "t4",
        code: "W4",
        title: "Test antigravity",
        status: s.TaskStatus.DONE,
        provider: s.Provider.ANTIGRAVITY,
        model: "gemini-3.8-flash-high",
        usage: usage(1000, 200, 0, 0, 0),
      },
      onStop() {},
    }),
  );
  // Antigravity's estimate is not its bill: the hover says so, the card shows the figure alone.
  assert.match(
    antigravity,
    /title="[^"]*estimated, prices of 2026-01-01 \(an estimate at API prices, not the bill\)"/,
  );
  assert.match(antigravity, />1\.2K tokens · ≈ \$[0-9.]+<\/span>/);

  // When usage unit is switched to tokens, costs are hidden
  s.setUsageUnit("tokens");
  try {
    const claudeTokens = s.renderToStaticMarkup(
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
    assert.match(claudeTokens, /51\.4K tokens/);
    assert.doesNotMatch(claudeTokens, /\$/);
    assert.doesNotMatch(claudeTokens, /estimated/);
  } finally {
    s.setUsageUnit("money");
  }
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

test("a task card shows its model: planned forecasts it, running shows it, finished keeps it; watchers and azimas show none", () => {
  s.clearModelAliases();
  const card = (task, project) =>
    s.renderToStaticMarkup(h(s.WishTask, { task, project, onStop() {} }));

  // --- Running tasks ---
  const claudeRunning = card({
    id: "t1",
    code: "W1",
    title: "Trim the wick",
    status: s.TaskStatus.RUNNING,
    model: "claude-sonnet-5-5-20250929",
  });
  assert.match(
    claudeRunning,
    /<span class="task-model" title="claude-sonnet-5-5-20250929">sonnet 5\.5<\/span>/,
  );

  const questionWorkerRunning = card({
    id: "t2",
    code: "W2",
    title: "Investigate Q01",
    status: s.TaskStatus.RUNNING,
    provider: s.Provider.ANTIGRAVITY,
    model: "gemini-3.8-flash-high",
    question: "q1",
  });
  assert.match(
    questionWorkerRunning,
    /<span class="task-model" title="gemini-3.8-flash-high">gemini 3\.8 flash<\/span>/,
  );

  const correctionWorkerRunning = card({
    id: "t3",
    code: "W3",
    title: "Correct integration failure",
    status: s.TaskStatus.RUNNING,
    provider: s.Provider.CODEX,
    model: "gpt-5.5",
    correction: { attempts: 1 },
  });
  assert.match(
    correctionWorkerRunning,
    /<span class="task-model" title="gpt-5.5">gpt-5\.5<\/span>/,
  );

  // Running task with empty model does not show badge
  const noModelRunning = card({
    id: "t4",
    code: "W4",
    title: "No model",
    status: s.TaskStatus.RUNNING,
    model: "",
  });
  assert.doesNotMatch(noModelRunning, /task-model/);

  // --- Planned / waiting tasks ---
  // Planned task with no model defaults to provider default (Claude -> sonnet 5.5 · default)
  const plannedDefaultClaude = card({
    id: "t5",
    code: "W5",
    title: "Planned Claude task",
    status: s.TaskStatus.PENDING,
  });
  assert.match(
    plannedDefaultClaude,
    /<span class="task-model" title="claude-sonnet-5-5">sonnet 5\.5 · default<\/span>/,
  );

  // Planned task with Antigravity provider defaults to gemini 3.8 flash · default
  const plannedDefaultAgy = card({
    id: "t6",
    code: "W6",
    title: "Planned Antigravity task",
    status: s.TaskStatus.PENDING,
    provider: s.Provider.ANTIGRAVITY,
  });
  assert.match(
    plannedDefaultAgy,
    /<span class="task-model" title="gemini-3.8-flash-high">gemini 3\.8 flash · default<\/span>/,
  );

  // Planned task using model from project settings
  const plannedProjectModel = card(
    {
      id: "t7",
      code: "W7",
      title: "Planned task with project settings",
      status: s.TaskStatus.PENDING,
      projectId: "p1",
    },
    { id: "p1", name: "Djinn", settings: { model: "claude-sonnet-4-5" } },
  );
  assert.match(
    plannedProjectModel,
    /<span class="task-model" title="claude-sonnet-4-5">sonnet 4\.5<\/span>/,
  );

  // Planned task with unknown alias shows "alias · version unknown"
  const plannedUnknownAlias = card({
    id: "t8",
    code: "W8",
    title: "Planned task with unknown alias",
    status: s.TaskStatus.PENDING,
    model: "haiku",
  });
  assert.match(
    plannedUnknownAlias,
    /<span class="task-model" title="haiku">haiku · version unknown<\/span>/,
  );

  // Planned task with known alias (learned or registered) resolves to version and shows "· alias"
  s.recordModelAlias("haiku", "claude-haiku-4-5");
  const plannedKnownAlias = card({
    id: "t9",
    code: "W9",
    title: "Planned task with known alias",
    status: s.TaskStatus.PENDING,
    model: "haiku",
  });
  assert.match(
    plannedKnownAlias,
    /<span class="task-model" title="claude-haiku-4-5">haiku 4\.5 · alias<\/span>/,
  );

  // Planned task with explicit model
  const plannedExplicit = card({
    id: "t10",
    code: "W10",
    title: "Planned task with explicit model",
    status: s.TaskStatus.PENDING,
    model: "claude-sonnet-4-5",
  });
  assert.match(
    plannedExplicit,
    /<span class="task-model" title="claude-sonnet-4-5">sonnet 4\.5<\/span>/,
  );

  // --- Finished tasks ---
  // Finished task (DONE, FAILED, STOPPED, INTERRUPTED) keeps showing recorded model
  const finishedDone = card({
    id: "t11",
    code: "W11",
    title: "Done task",
    status: s.TaskStatus.DONE,
    model: "claude-sonnet-5-5-20250929",
  });
  assert.match(
    finishedDone,
    /<span class="task-model" title="claude-sonnet-5-5-20250929">sonnet 5\.5<\/span>/,
  );

  const finishedFailed = card({
    id: "t12",
    code: "W12",
    title: "Failed task",
    status: s.TaskStatus.FAILED,
    model: "gpt-5.5",
  });
  assert.match(
    finishedFailed,
    /<span class="task-model" title="gpt-5.5">gpt-5\.5<\/span>/,
  );

  // Finished task across multiple models across resumes: badge shows last model, tooltip shows all
  const multiModelFinished = card({
    id: "t13",
    code: "W13",
    title: "Task run across models",
    status: s.TaskStatus.DONE,
    model: "gemini-3.8-flash-high",
    models: ["claude-sonnet-4-5", "gemini-3.8-flash-high"],
  });
  assert.match(
    multiModelFinished,
    /<span class="task-model" title="claude-sonnet-4-5, gemini-3.8-flash-high">gemini 3\.8 flash<\/span>/,
  );

  // --- Watchers and Azimas show none ---
  const watcherRunning = card({
    id: "t14",
    code: "W14",
    title: "Watch checks",
    status: s.TaskStatus.RUNNING,
    provider: s.Provider.WATCH,
    model: "claude-sonnet-5-5",
  });
  assert.doesNotMatch(watcherRunning, /task-model/);

  const watcherPlanned = card({
    id: "t15",
    code: "W15",
    title: "Watch planned",
    status: s.TaskStatus.PENDING,
    provider: s.Provider.WATCH,
  });
  assert.doesNotMatch(watcherPlanned, /task-model/);

  const azimaCard = card({
    id: "t16",
    code: "T1",
    title: "Azima plan",
    status: s.TaskStatus.PENDING,
    kind: s.TaskKind.AZIMA,
  });
  assert.doesNotMatch(azimaCard, /task-model/);
});

test("a task card waiting for branch to be green again shows compact reason", () => {
  const fullReason =
    "waits for feat/wails-go to be green again (push checks red at 12345678: test)";
  assert.equal(s.isGreenWait(fullReason), true);
  assert.equal(s.isGreenWait("waits for W1 (running)"), false);
  assert.equal(
    s.compactGreenWait(fullReason),
    "waits for feat/wails-go to be green again",
  );

  const markup = s.renderToStaticMarkup(
    h(s.WishTask, {
      task: {
        id: "t1",
        code: "W1",
        title: "Waiting task",
        status: s.TaskStatus.PENDING,
        waitReason: fullReason,
      },
      onStop() {},
    }),
  );
  assert.match(
    markup,
    /<span title="waits for feat\/wails-go to be green again \(push checks red at 12345678: test\)">waits for feat\/wails-go to be green again<\/span>/,
  );
});

test("a task card offers Start anyway from machine_wait, not from its reason's text", () => {
  const reason =
    "the machine is under pressure: the system reports memory pressure";
  const card = (task) =>
    s.renderToStaticMarkup(
      h(s.WishTask, {
        task: {
          id: "t1",
          code: "W1",
          title: "Waiting task",
          status: s.TaskStatus.PENDING,
          waitReason: reason,
          ...task,
        },
        onStop() {},
        onStart() {},
      }),
    );
  const held = card({ machineWait: true });
  assert.match(held, /class="wish-task-note machine-wait"/);
  assert.match(held, /<strong>Waits for the machine<\/strong>/);
  assert.match(held, /Start anyway/);
  // The same words, but held by more than the machine (a usage limit, a push hold, a task going first): no button.
  assert.doesNotMatch(card({ machineWait: false }), /Start anyway/);
  // Running: whatever it carries, nothing to start.
  assert.doesNotMatch(
    card({ machineWait: true, status: s.TaskStatus.RUNNING }),
    /Start anyway/,
  );
});

// Q273: the calls a worker's permissions refused are the lead's to handle (its ended event names them); the window
// shows no warning of them on the task's card.
test("a task card shows no warning of the calls its permissions refused: they go to the lead", () => {
  const card = (task) =>
    s.renderToStaticMarkup(
      h(s.WishTask, {
        task: {
          id: "t1",
          code: "W1",
          wishId: "w1",
          title: "Silae study",
          status: s.TaskStatus.DONE,
          ...task,
        },
        onStop() {},
      }),
    );
  const refused = card({
    refused: { count: 6, names: ["djinn", "ls", "git", "task", "gcloud"] },
  });
  assert.doesNotMatch(refused, /refused|wish allow|gcloud|with problems/);
  assert.equal(refused, card({}));
});

test("a task row reads on two lines: status, code and long title on the first, details on the second", () => {
  const longTitle =
    "Configure the multi-environment deployment pipeline with automated rollbacks and canary release verification across staging and production clusters";
  const task = {
    id: "t1",
    code: "W42",
    title: longTitle,
    status: s.TaskStatus.RUNNING,
    model: "claude-sonnet-5-5-20250929",
    startTime: { seconds: 1760000000n, nanos: 0 },
  };
  const markup = s.renderToStaticMarkup(
    h(s.WishTask, {
      task,
      project: { id: "p1", name: "Djinn Core" },
      onStop() {},
    }),
  );

  // 1. Two-line layout structure
  assert.match(markup, /class="wish-task-row has-actions"/);
  assert.match(markup, /<button class="wish-task-heading"/);
  assert.match(markup, /<span class="wish-task-first-line">/);
  assert.match(markup, /<span class="wish-task-second-line">/);

  // 2. First line: status badge, code, whole title without string truncation, and chevron
  const firstLine = markup.match(
    /<span class="wish-task-first-line">([\s\S]*?)<\/span><span class="wish-task-second-line">/,
  )?.[1];
  assert.ok(firstLine, "first line exists");
  assert.match(firstLine, /<span class="status-badge/);
  assert.match(firstLine, /<span class="agent-code">W42<\/span>/);
  assert.match(
    firstLine,
    new RegExp(`<strong class="task-title">${longTitle}</strong>`),
  );
  // Full title preserved, not truncated into characters + ellipsis in markup
  assert.doesNotMatch(firstLine, /<strong class="task-title">[^<]*…<\/strong>/);

  // Verify the order of elements in the first line: status -> code -> title
  const statusIdx = firstLine.indexOf('class="status-badge');
  const codeIdx = firstLine.indexOf('class="agent-code"');
  const titleIdx = firstLine.indexOf('class="task-title"');
  assert.ok(statusIdx < codeIdx, "status comes before code");
  assert.ok(codeIdx < titleIdx, "code comes before title");

  // 3. Second line: project, model, elapsed time in order (status badge moved to first line)
  const secondLine = markup.match(
    /<span class="wish-task-second-line">([\s\S]*?)<\/span><\/button>/,
  )?.[1];
  assert.ok(secondLine, "second line exists");
  assert.match(secondLine, /<span class="task-project">Djinn Core<\/span>/);
  assert.match(
    secondLine,
    /<span class="task-model" title="claude-sonnet-5-5-20250929">sonnet 5\.5<\/span>/,
  );
  assert.doesNotMatch(secondLine, /<span class="status-badge/);

  // Verify the order of elements in the second line: project -> model -> time
  const projectIdx = secondLine.indexOf('class="task-project"');
  const modelIdx = secondLine.indexOf('class="task-model"');
  const timeIdx = secondLine.indexOf('class="task-time"');
  assert.ok(projectIdx < modelIdx, "project comes before model");
  assert.ok(modelIdx < timeIdx, "model comes before time");

  // 4. Action buttons aligned in actions block outside the heading button
  assert.match(
    markup,
    /<span class="wish-task-actions">[\s\S]*?<\/span><\/div>/,
  );
  assert.match(markup, /aria-label="Stop the worker"/);
});

test("a task row and an azima row both start with their status badge: state -> code -> title", () => {
  // 1. Task row starts with its status badge before code and title
  const task = {
    id: "t1",
    code: "W42",
    title: "Configure the deployment pipeline",
    status: s.TaskStatus.RUNNING,
  };
  const taskMarkup = s.renderToStaticMarkup(
    h(s.WishTask, {
      task,
      project: { id: "p1", name: "Djinn Core" },
      onStop() {},
    }),
  );
  const taskFirstLine = taskMarkup.match(
    /<span class="wish-task-first-line">([\s\S]*?)<\/span><span class="wish-task-second-line">/,
  )?.[1];
  assert.ok(taskFirstLine, "task first line exists");
  const taskStatusIdx = taskFirstLine.indexOf('class="status-badge');
  const taskCodeIdx = taskFirstLine.indexOf('class="agent-code"');
  const taskTitleIdx = taskFirstLine.indexOf('class="task-title"');
  assert.ok(taskStatusIdx !== -1, "task status badge is present in first line");
  assert.ok(taskCodeIdx !== -1, "task agent code is present in first line");
  assert.ok(taskTitleIdx !== -1, "task title is present in first line");
  assert.ok(taskStatusIdx < taskCodeIdx, "task status comes before code");
  assert.ok(taskCodeIdx < taskTitleIdx, "task code comes before title");

  // 2. Azima row starts with its status badge before code and title
  const azima = {
    id: "a1",
    code: "T1",
    title: "Ship the lamp",
    kind: s.TaskKind.AZIMA,
    azima: { state: s.AzimaState.IN_PROGRESS, partsRunning: 1 },
  };
  const azimaMarkup = s.renderToStaticMarkup(
    h(s.AzimaCard, {
      azima,
      parts: [],
      tasks: new Map(),
      render() {
        return null;
      },
    }),
  );
  const azimaHeading = azimaMarkup.match(
    /<button[^>]*class="azima-heading"[^>]*>([\s\S]*?)<\/button>/,
  )?.[1];
  assert.ok(azimaHeading, "azima heading exists");
  const azimaStatusIdx = azimaHeading.indexOf('class="status-badge');
  const azimaCodeIdx = azimaHeading.indexOf('class="agent-code"');
  const azimaTitleIdx = azimaHeading.indexOf("<strong");
  assert.ok(azimaStatusIdx !== -1, "azima status badge is present in heading");
  assert.ok(azimaCodeIdx !== -1, "azima agent code is present in heading");
  assert.ok(azimaTitleIdx !== -1, "azima title is present in heading");
  assert.ok(azimaStatusIdx < azimaCodeIdx, "azima status comes before code");
  assert.ok(azimaCodeIdx < azimaTitleIdx, "azima code comes before title");
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
  assert.match(opened, /wish-task-diff-button/);
  assert.match(opened, /See the diff/);
  // A task with no worktree has no uncommitted files to show: the button says why it is off.
  assert.match(
    opened,
    /<button type="button" class="button secondary small wish-task-worktree-button" aria-haspopup="dialog" disabled="" title="This task has no worktree">See uncommitted files<\/button>/,
  );
  const withWorktree = s.renderToStaticMarkup(
    h(s.WishTask, {
      task: { ...task, worktree: "/tmp/w1" },
      focused: true,
      onStop() {},
    }),
  );
  assert.doesNotMatch(withWorktree, /disabled=""[^>]*>See uncommitted files/);
  // Logs and diff are deferred: containers are not rendered until toggled, the diff's drawer until a button opens it.
  assert.doesNotMatch(opened, /class="wish-task-events"/);
  assert.doesNotMatch(opened, /task-diff-view/);
  assert.doesNotMatch(opened, /diff-drawer/);
});

test("diff views render unified and split diffs with Chroma syntax tokens and hunk headers", () => {
  const hunks = [
    {
      header: "@@ -1,3 +1,4 @@",
      lines: [
        {
          kind: s.DiffLineKind.CONTEXT,
          oldLine: 1,
          newLine: 1,
          text: "package main",
          tokens: [
            { text: "package", category: "keyword" },
            { text: " main", category: "" },
          ],
        },
        {
          kind: s.DiffLineKind.DELETED,
          oldLine: 2,
          newLine: 0,
          text: "// old line",
          tokens: [{ text: "// old line", category: "comment" }],
        },
        {
          kind: s.DiffLineKind.ADDED,
          oldLine: 0,
          newLine: 2,
          text: "// new line",
          tokens: [{ text: "// new line", category: "comment" }],
        },
      ],
    },
  ];

  // 1. Unified view
  const unified = s.renderToStaticMarkup(h(s.UnifiedView, { hunks }));
  assert.match(unified, /diff-hunk-header/);
  assert.match(unified, /@@ -1,3 \+1,4 @@/);
  assert.match(unified, /line-context/);
  assert.match(unified, /line-deleted/);
  assert.match(unified, /line-added/);
  assert.match(unified, /tok-keyword/);
  assert.match(unified, /tok-comment/);
  assert.match(unified, />package</);
  assert.match(unified, />\+/);
  assert.match(unified, />-</);

  // 2. Split view
  const split = s.renderToStaticMarkup(h(s.SplitView, { hunks }));
  assert.match(split, /diff-split-table/);
  assert.match(split, /diff-split-row/);
  assert.match(split, /diff-split-side left/);
  assert.match(split, /diff-split-side right/);
  assert.match(split, /tok-keyword/);
  assert.match(split, /tok-comment/);
});

test("diff views render inline comments and a comment box; an ended task card offers to turn its waiting comments into work", () => {
  const hunks = [
    {
      header: "@@ -1,3 +1,4 @@",
      lines: [
        {
          kind: s.DiffLineKind.CONTEXT,
          oldLine: 1,
          newLine: 1,
          text: "package main",
        },
        {
          kind: s.DiffLineKind.ADDED,
          oldLine: 0,
          newLine: 2,
          text: '\tprintln("review this")',
        },
      ],
    },
  ];

  const comments = [
    {
      id: "c-1",
      file: "main.go",
      line: 2,
      endLine: 2,
      commit: "abcdef123456",
      text: "Please add a comment explaining this line.",
      resolved: false,
    },
    {
      id: "c-2",
      file: "main.go",
      line: 1,
      endLine: 2,
      commit: "abcdef123456",
      text: "Looks good overall.",
      resolved: true,
    },
  ];

  // 1. Unified view with comments
  const unified = s.renderToStaticMarkup(
    h(s.UnifiedView, { hunks, comments, filePath: "main.go" }),
  );
  assert.match(unified, /diff-comment-row/);
  assert.match(unified, /Please add a comment explaining this line\./);
  assert.match(unified, /Looks good overall\./);
  assert.match(unified, /diff-comment-resolved-badge/);
  assert.match(unified, /Resolved/);
  assert.match(unified, /diff-add-comment-btn/);

  // 2. CommentBox standalone
  const box = s.renderToStaticMarkup(
    h(s.CommentBox, {
      startLine: 2,
      endLine: 5,
      filePath: "main.go",
      onCancel() {},
    }),
  );
  assert.match(box, /diff-comment-box/);
  assert.match(box, /Comment on lines 2–5/);
  assert.match(box, /diff-comment-textarea/);
  assert.match(box, /diff-comment-btn-cancel/);
  assert.match(box, /diff-comment-btn-submit/);

  // A deleted line is in no version of the file a comment names: no comment button on it.
  const deleted = s.renderToStaticMarkup(
    h(s.UnifiedView, {
      hunks: [
        {
          header: "@@ -1 +0,0 @@",
          lines: [
            {
              kind: s.DiffLineKind.DELETED,
              oldLine: 1,
              newLine: 0,
              text: "gone",
            },
          ],
        },
      ],
      comments: [],
      filePath: "main.go",
    }),
  );
  assert.doesNotMatch(deleted, /diff-add-comment-btn/);

  // 3. The card of an ended task: the comments show in its diff; the card only offers to turn the waiting ones into
  // work, those that went to no worker and are not resolved.
  const card = (status, comments, onTurnComments = async () => {}) =>
    s.renderToStaticMarkup(
      h(s.WishTask, {
        task: {
          id: "t1",
          code: "W1",
          title: "Trim the wick",
          status,
          comments,
        },
        focused: true,
        onStop() {},
        onTurnComments,
      }),
    );
  const taskCard = card(s.TaskStatus.DONE, comments);
  assert.match(taskCard, /wish-task-turn-comments-button/);
  assert.match(taskCard, /Turn comments into work/);
  assert.doesNotMatch(taskCard, /Please add a comment explaining this line\./);
  const sent = comments.map((c) => ({ ...c, sentTo: "t2" }));
  assert.doesNotMatch(
    card(s.TaskStatus.DONE, sent),
    /wish-task-turn-comments-button/,
  );
  assert.doesNotMatch(
    card(s.TaskStatus.RUNNING, comments),
    /wish-task-turn-comments-button/,
  );
  assert.doesNotMatch(
    card(s.TaskStatus.CANCELLED, comments),
    /wish-task-turn-comments-button/,
  );
  assert.doesNotMatch(
    card(s.TaskStatus.DONE, comments, null),
    /wish-task-turn-comments-button/,
  );
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
  // Open, a running task links to its worker's row in the Hourglass, its CPU and memory over time.
  const open = (status) =>
    s.renderToStaticMarkup(
      h(
        s.DjinnProvider,
        { djinn: hourglassDjinn().djinn },
        h(s.WishTask, {
          task: {
            id: "t1",
            code: "W1",
            title: "Trim the wick",
            status,
            resources,
          },
          focused: true,
          onStop() {},
        }),
      ),
    );
  assert.match(
    open(s.TaskStatus.RUNNING),
    /<button type="button" class="text-link task-hourglass-open">Live in the Hourglass<\/button>/,
  );
  assert.doesNotMatch(open(s.TaskStatus.DONE), /task-hourglass-open/);
  assert.equal(s.memory(1536n), "1.5 kB");
});

test("a task card shows its model turns over its whole life, its run against the limit beside it, its tone warning past 80% of the run's; hover says total, run, limit", () => {
  const card = (status, turns) =>
    s.renderToStaticMarkup(
      h(s.WishTask, {
        task: { id: "t1", code: "W1", title: "Trim the wick", status, turns },
        onStop() {},
      }),
    );
  // A first run: its turns are its whole life's, shown once.
  const running = card(s.TaskStatus.RUNNING, {
    run: 37,
    limit: 500,
    total: 37,
  });
  assert.match(
    running,
    /<span class="task-turns" title="37 model turns in this run, of the 500 its seal allows a run"><svg[^>]*aria-label="model turns"[^>]*>.*?<\/svg>37\/500<\/span>/,
  );
  assert.doesNotMatch(running, /task-turns-run/);
  // Resumed: the whole life's 412 read first, this run's 37 against its 500 beside them, the tone still the run's.
  assert.match(
    card(s.TaskStatus.RUNNING, { run: 37, limit: 500, total: 412 }),
    /<span class="task-turns" title="412 model turns over its whole life · 37 model turns in this run, of the 500 its seal allows a run"><svg[^>]*aria-label="model turns"[^>]*>.*?<\/svg>412<span class="task-turns-run">37\/500<\/span><\/span>/,
  );
  // 80% is not past it.
  assert.match(
    card(s.TaskStatus.RUNNING, { run: 400, limit: 500, total: 400 }),
    /class="task-turns"/,
  );
  assert.match(
    card(s.TaskStatus.RUNNING, { run: 401, limit: 500, total: 450 }),
    /<span class="task-turns warning" title="450 model turns over its whole life · 401 model turns in this run, of the 500 its seal allows a run">.*?450<span class="task-turns-run">401\/500</,
  );
  assert.match(
    card(s.TaskStatus.DONE, { run: 12, limit: 500, total: 12 }),
    /title="12 model turns in its last run, of the 500 its seal allows a run">.*?12\/500</,
  );
  assert.doesNotMatch(card(s.TaskStatus.PENDING, undefined), /task-turns/);
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
  assert.match(byLead, /wish-task-closed">Closed by the Djinn, [^<]+<\/p>/);
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
    /Closed by the Djinn, [^<]+: continued in <button type="button" class="text-button agent-code task-link">W2<\/button><\/p>/,
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

test("a cancelled part leaves its azima's counters and shows among its finished parts, Cancelled", () => {
  const at = (seconds) => ({ seconds: BigInt(seconds), nanos: 0 });
  const work = (code, status, end) => ({
    id: code,
    code,
    title: `Task ${code}`,
    status,
    partOf: "T1",
    dependsOn: [],
    createTime: at(1),
    endTime: at(end),
  });
  const parts = [
    work("W1", s.TaskStatus.DONE, 10),
    work("W2", s.TaskStatus.CANCELLED, 20),
  ];
  const card = (counts) =>
    s.renderToStaticMarkup(
      h(s.AzimaCard, {
        azima: {
          id: "T1",
          code: "T1",
          title: "Ship the lamp",
          kind: s.TaskKind.AZIMA,
          status: s.TaskStatus.PENDING,
          dependsOn: [],
          azima: { state: s.AzimaState.AWAITING_PROOF, ready: true, ...counts },
        },
        parts,
        tasks: new Map(parts.map((x) => [x.id, x])),
        focus: "W2",
        render: (task) => h(s.WishTask, { key: task.id, task, onStop() {} }),
      }),
    );
  // The server's counts (internal/plan.FillAzimas), then the card's own when it has none: 1/1, nothing waiting.
  for (const html of [
    card({ parts: 1, partsDone: 1, partsRunning: 0 }),
    card({}),
  ]) {
    assert.match(html, />1\/1</);
    assert.match(html, /1 done, 0 running, 0 waiting/);
    assert.match(html, /status-badge tone-cancelled[^>]*>.*?Cancelled/);
  }
  // An azima whose only part is cancelled has nothing to count: no bar, never "0/1 … 1 waiting".
  const only = s.renderToStaticMarkup(
    h(s.AzimaCard, {
      azima: {
        id: "T2",
        code: "T2",
        title: "Trim the wick",
        kind: s.TaskKind.AZIMA,
        dependsOn: [],
        azima: { state: s.AzimaState.OPEN, ready: true },
      },
      parts: [{ ...parts[1], partOf: "T2" }],
      tasks: new Map(),
      render: () => null,
    }),
  );
  assert.doesNotMatch(only, /azima-progress/);
  assert.doesNotMatch(only, /waiting/);
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
  // What comes before an azima that waits for nothing is on its title's hover, not in its heading.
  assert.match(html, /<strong title="[^"]*\nafter T1">/);
  assert.doesNotMatch(html, />after T1</);
  assert.match(html, /1\/3/);
  assert.match(html, /In progress/);
  // No azima is ever said to wait for you.
  assert.doesNotMatch(html, /Waits for your answer/);
  assert.doesNotMatch(html, /tone-waiting/);

  // Nor does the flying carpet: an azima neither waits for you nor moves.
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
        model: "fixture-unpriced",
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

test("a merge sits under the original task it serves, folded and marked Merge; the others it serves link to it", () => {
  const at = (seconds) => ({ seconds: BigInt(seconds), nanos: 0 });
  const work = (code, status, extra) => ({
    id: code,
    code,
    title: `Task ${code}`,
    status,
    partOf: "T1",
    dependsOn: [],
    createTime: at(1),
    endTime: status === s.TaskStatus.DONE ? at(10) : undefined,
    ...extra,
  });
  const correction = (code, status, taskIds, extra) =>
    work(code, status, {
      title: `Merge: Task W401 (W401) — conflict in docs/agent-protocol.md`,
      correction: { failure: { taskIds }, attempt: 1 },
      ...extra,
    });
  const tasks = [
    {
      id: "T1",
      code: "T1",
      title: "The plan",
      kind: s.TaskKind.AZIMA,
      status: s.TaskStatus.PENDING,
      dependsOn: [],
      createTime: at(1),
      azima: { state: s.AzimaState.IN_PROGRESS, ready: true },
    },
    work("W401", s.TaskStatus.DONE, {
      integration: { state: s.IntegrationState.CONFLICT },
    }),
    work("W403", s.TaskStatus.DONE),
    work("W405", s.TaskStatus.PENDING),
    // W419 corrected W401; W422 corrects W419 and W403: both serve W401 first.
    correction("W419", s.TaskStatus.DONE, ["W401"]),
    correction("W422", s.TaskStatus.WAITING, ["W419", "W403"]),
    // A push correction serves no task: it stays a peer, marked.
    correction("W430", s.TaskStatus.RUNNING, [], { partOf: "" }),
  ];
  const byId = new Map(tasks.map((task) => [task.id, task]));
  const nest = s.mergeNest(tasks);
  assert.deepEqual(
    [...nest.host],
    [
      ["W419", "W401"],
      ["W422", "W401"],
    ],
  );
  const sections = (focus = "") => {
    const card = (task) => {
      const merges = nest.under.get(task.id);
      return h(s.WishTask, {
        key: task.id,
        task,
        focused: focus === task.id,
        merges,
        nested: merges?.map(card),
        mergeOpen: nest.host.get(focus) === task.id,
        alsoMerged: nest.links.get(task.id),
        onStop() {},
        async onSend() {},
        async onDone() {},
      });
    };
    const groups = s.azimaGroups(tasks);
    return s.renderToStaticMarkup(
      h(s.TaskSections, {
        moving: s.movingTasks(tasks),
        azimas: groups,
        fold: `merge-${focus}`,
        renderAzima: ({ azima, parts }) =>
          h(s.AzimaCard, {
            key: azima.id,
            azima,
            parts,
            tasks: byId,
            render: card,
            focus,
            nest,
          }),
        render: card,
      }),
    );
  };
  const codes = (html) =>
    [...html.matchAll(/<span class="agent-code">([TW]\d+)</g)].map((m) => m[1]);
  const html = sections();
  // The merges are no peers of the azima's work: W401, finished, moves while its merge waits; W403, finished, folds.
  assert.deepEqual(codes(html), ["W430", "T1", "W401", "W405"]);
  assert.match(
    html,
    /<button type="button" class="wish-task-merges-toggle" aria-expanded="false" title="W419 Merge: Task W401[^"]*\nW422 Merge: [^"]*">.*?<span class="merge-label">Merge<\/span><span class="agent-code">W419, W422<\/span>/,
  );
  // The push correction is marked where it stands.
  assert.match(
    html,
    /<span class="merge-label">Merge<\/span><span class="agent-code">W430<\/span>/,
  );
  // Brought into sight, W422 unfolds under W401, marked.
  const shown = sections("W422");
  assert.deepEqual(codes(shown), [
    "W430",
    "T1",
    "W401",
    "W419",
    "W422",
    "W405",
  ]);
  assert.match(
    shown,
    /<span class="merge-label">Merge<\/span><span class="agent-code">W422<\/span><span class="task-title-wrap">/,
  );
  // W403, which it serves too, links to W401.
  assert.match(
    sections("W403"),
    /wish-task-merged-in"><button type="button" class="text-button" title="W422 also brings this work in; it shows under W401"><span class="merge-label">Merge<\/span><span class="agent-code">W422<\/span>/,
  );
  // Counted with their original: four tasks of work, the push correction one of them.
  assert.equal(s.workCount(tasks), 4);
  // The attention bar puts W422's line on W401.
  const wish = { id: "w", code: "D1", title: "Wish" };
  const waiting = s.waitingTasks(wish, { tasks, questions: [] });
  const [line] = s.attentionOf([], waiting, []);
  assert.equal(line.code, "W401");
  assert.match(line.text, /^Merge W422 · /);
  assert.equal(line.target, "waiting-W422");
});

test("the flying carpet merges the active wishes: their questions, the blocking one first, each with its wish", async () => {
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
  assert.match(html, /<h1>Flying carpet<\/h1>/);
  // Its live graphs are in the Hourglass (W542): none here.
  assert.doesNotMatch(html, /consumption-panel|resource-chart/);
  // W538: the Djinn is at hand as on a wish: Talk to the Djinn in the header opens its terminal, on no wish, and the
  // pill at the bottom writes to it, Cmd/Ctrl+I.
  assert.match(
    html,
    /<div class="topbar-actions"><div class="split-button"><button class="button secondary small" title="Opens the Djinn&#x27;s terminal"><svg[^]*?<span>Talk to the Djinn<\/span><\/button>/,
  );
  assert.doesNotMatch(html, /focused on this wish/);
  assert.match(
    html,
    /<div class="lead-message">[^]*?aria-keyshortcuts="[^"]*I"[^]*?<span>Write to the Djinn<\/span><\/button>/,
  );
  // The blocking question of the second wish comes before the free one of the first, each marked with its wish.
  const blocking = html.indexOf("May W2 pour?");
  const free = html.indexOf("Brass or glass?");
  assert.ok(blocking > 0 && free > blocking);
  assert.match(html, /Blocks W2/);
  assert.match(
    html,
    /class="wish-origin" title="Find the oil"><b>2<\/b>Find the oil/,
  );
  // What waits; the tasks and the decisions of both wishes in their own tabs: the done task part of no azima counts
  // with them, as in its wish's (W507).
  assert.match(html, /W2 waits for your answer to Q01 before it may edit\./);
  assert.match(
    html,
    /id="view-tab-tasks"[^>]*>Tasks<span class="count">3<\/span>/,
  );
  assert.doesNotMatch(html, /Taste the oils/);
  assert.match(
    html,
    /id="view-tab-decisions"[^>]*>Decisions<span class="count">1<\/span>/,
  );
  assert.doesNotMatch(html, /Tonight\?/);
  // What each wish spent: the first its price, reported, then its tokens; the second, with no price at all, its tokens
  // and why.
  assert.match(
    html,
    /<span class="spent-price" title="\$0\.42 reported">\$0\.42<\/span><span>input 10 · output 400 · cache read 38K · cache written 12K<\/span>/,
  );
  assert.doesNotMatch(html, /estimated/);
  assert.match(html, /input 1\.2K · output 300 · cache read 5K/);
  assert.match(html, /<small>1 task without a price in the table<\/small>/);
});

test("what a wish spent shows one price, ≈ when an estimate is in it; the hover splits it, in money only", () => {
  const spent = s.spent([
    // Claude reports its cost: it is the price, whatever the table says.
    { usage: usage(10, 400, 38000, 12000, 0.42) },
    // Codex reports tokens: the table prices them (1200 × 5 + 300 × 30 + 5000 × 0.5) / 1M = $0.0175.
    {
      provider: s.Provider.CODEX,
      model: "gpt-5.5",
      usage: usage(1200, 300, 5000, 0, 0),
    },
    // An Antigravity estimate is at API prices, not the bill.
    {
      provider: s.Provider.ANTIGRAVITY,
      model: "gemini-3.8-flash-high",
      usage: usage(1000, 200, 0, 0, 0),
    },
    // A model the table does not know: no price.
    { model: "fixture-unpriced", usage: usage(100, 200, 0, 0, 0) },
  ]);
  assert.equal(spent.costUsd, 0.42);
  assert.equal(spent.priced, 2);
  assert.equal(spent.withoutPrice, 1);
  const total = spent.costUsd + spent.estimatedCostUsd;
  const money = (n) =>
    new Intl.NumberFormat("en", {
      style: "currency",
      currency: "USD",
      maximumFractionDigits: n < 1 ? 3 : 2,
    })
      .format(n)
      .replace(/[$.]/g, "\\$&");
  const html = s.renderToStaticMarkup(h(s.SpentLine, { spent }));
  const title = html.match(
    /<span class="spent-price" title="([^"]*)">([^<]*)</,
  );
  assert.ok(title, html);
  // One figure: the reported cost plus the estimate of the tasks that reported none, read ≈.
  assert.match(title[2], new RegExp(`^≈ ${money(total)}$`));
  const split = title[1].split("\n");
  assert.equal(split[0], "$0.42 reported");
  assert.match(
    split[1],
    new RegExp(
      `^${money(spent.estimatedCostUsd)} estimated, prices of 2026-01-01 \\(an estimate at API prices, not the bill\\)$`,
    ),
  );
  assert.equal(split[2], "1 task without a price in the table");
  // Several providers: each one's price, in the hover too.
  assert.ok(split.includes("claude: $0.42"), split.join(" | "));
  assert.ok(split.includes("codex: ≈ $0.018"), split.join(" | "));
  assert.ok(split.some((line) => line.startsWith("antigravity: ≈ $")));
  // Nothing of the split shows outside the hover; the tokens read as they did.
  const shown = html.replace(/title="[^"]*"/g, "");
  assert.doesNotMatch(shown, /reported|estimated|without a price|not the bill/);
  assert.match(
    shown,
    /<span>input 2\.3K · output 1\.1K · cache read 43K · cache written 12K<\/span>/,
  );
  assert.doesNotMatch(shown, /spent-providers/);

  // In tokens, nothing changes: the total, the detail, each provider's count, and no price.
  s.setUsageUnit("tokens");
  try {
    const counted = s.renderToStaticMarkup(h(s.SpentLine, { spent }));
    assert.match(counted, /<span class="spent-total">58\.4K tokens<\/span>/);
    assert.match(counted, /class="spent-providers"/);
    assert.doesNotMatch(counted, /\$|spent-price/);
  } finally {
    s.setUsageUnit("money");
  }
});

test("the flying carpet hides its empty sections", async () => {
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
  // The djinn's mark heads it; the brass Update sits beside the title, the notes a quiet link after it, then the ×.
  assert.match(
    release,
    /^<div class="update-banner" role="status"><section class="update-banner-part"><div class="update-banner-head"><div class="update-banner-title" title="v1.0.0 → v2.0.0"><span class="brand small"><span class="brand-mark"><img/,
  );
  assert.match(
    release,
    /A new version of Djinn is ready<\/span><\/div><div class="update-banner-actions"><button type="button" class="update-banner-primary">Update<\/button><a [^>]*>Release notes<\/a><button type="button" class="update-banner-close" aria-label="Dismiss" title="Dismiss">×<\/button><\/div>/,
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

test("the Tilasms tab lists each tilasm newest first with its code, title, author, date and what it cites, and opens one in a sandboxed frame", () => {
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
  const recent = tilasm(
    "01a1223a-ae45-728f-8c37-c005eee91edd",
    "L03",
    "Latest sketch",
    {
      updateTime: stamp("2026-10-09T13:00:00Z"),
    },
  );
  const props = {
    tilasms: [flows, model, recent],
    count: 3,
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
    /class="tilasm-drop ".*Drop an \.html file, a folder with an index\.html, or a \.zip — or click to choose a file/,
  );
  assert.match(
    html,
    /<input type="file" accept="\.html,\.htm,\.zip,text\/html,application\/zip"/,
  );
  // Sorted from newest to oldest: L03 (13:00), then L01 (v3 at 12:00), then L02 (10:00).
  assert.ok(html.indexOf("L03") < html.indexOf("L01"));
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

  // The drop zone reacts on dragover and forwards files picked from the file input.
  const picked = [];
  const view = live(s.TilasmList, {
    ...props,
    onDrop: (entries) => picked.push(entries),
  });
  view.one("tilasms").onDragOver({
    dataTransfer: { types: ["Files"] },
    preventDefault() {},
  });
  assert.match(view.html(), /class="tilasm-drop dropping"/);
  view.one("tilasms").onDragLeave({
    currentTarget: { contains: () => false },
    relatedTarget: null,
  });
  assert.doesNotMatch(view.html(), /dropping/);
  const input = view
    .all("input")
    .find((node) => node.props.type === "file").props;
  input.onChange({
    target: {
      files: [new File(["<h1>Lamp</h1>"], "lamp.html", { type: "text/html" })],
      value: "lamp.html",
    },
  });
  assert.equal(picked.length, 1);
  assert.equal(picked[0][0].name, "lamp.html");

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

test("a folder, an .html file or a .zip dropped on the Tilasms tab is read with its files, hidden files left out", async () => {
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
  const htmlFile = await s.readDrop([
    file("diagram.html", "<title>Lamp</title><p>diagram</p>"),
  ]);
  assert.equal(htmlFile.name, "diagram");
  assert.deepEqual(
    htmlFile.files.map((f) => [f.path, new TextDecoder().decode(f.content)]),
    [["index.html", "<title>Lamp</title><p>diagram</p>"]],
  );
  const htmFile = await s.readDrop(
    s.entriesOf({
      items: [],
      files: [new File(["<p>notes</p>"], "notes.HTM", { type: "text/html" })],
    }),
  );
  assert.equal(htmFile.name, "notes");
  assert.deepEqual(
    htmFile.files.map((f) => [f.path, new TextDecoder().decode(f.content)]),
    [["index.html", "<p>notes</p>"]],
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

test("a fenced svg or standalone svg block renders safely in Markdown and AgentBlocks", () => {
  // 1. MarkdownBody with fenced ```svg
  const mdHtml = s.renderToStaticMarkup(
    h(s.MarkdownBody, {
      text: 'Diagram:\n\n```svg\n<svg viewBox="0 0 100 100"><circle cx="50" cy="50" r="40" /><script>alert(\'bad\')</script></svg>\n```',
    }),
  );
  assert.match(mdHtml, /<div class="dia-frame">/);
  assert.match(mdHtml, /<circle cx="50" cy="50" r="40"\s*\/?>/);
  assert.doesNotMatch(mdHtml, /<script/);
  assert.doesNotMatch(mdHtml, /alert/);

  // 2. sanitizeSvg security checks
  const dirty = `<svg viewBox="0 0 100 100" onload="alert('evil')" onclick="doEvil()">
    <script>alert('pwn')</script>
    <foreignObject><iframe src="https://attacker.com"></iframe></foreignObject>
    <a href="https://attacker.com" xlink:href="javascript:alert(1)"><text>Link</text></a>
    <a href="#safe-anchor"><rect width="10" height="10" /></a>
  </svg>`;
  const clean = s.sanitizeSvg(dirty);
  assert.doesNotMatch(clean, /<script/i);
  assert.doesNotMatch(clean, /alert/i);
  assert.doesNotMatch(clean, /onload/i);
  assert.doesNotMatch(clean, /onclick/i);
  assert.doesNotMatch(clean, /foreignObject/i);
  assert.doesNotMatch(clean, /iframe/i);
  assert.doesNotMatch(clean, /attacker\.com/i);
  assert.match(clean, /href="#safe-anchor"/);
  assert.match(clean, /<rect width="10" height="10"\s*\/?>/);

  // 3. SafeSvg component rendering
  const svgHtml = s.renderToStaticMarkup(h(s.SafeSvg, { source: clean }));
  assert.match(svgHtml, /<div class="dia-frame">/);
  assert.match(svgHtml, /<rect width="10" height="10"\s*\/?>/);
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
    "W2 waiting: Waiting to be committed",
    "W3 done: Committed into feat/x as 1a2b3c4d",
    "W4 failed: Conflict, not committed: W4 conflicts with feat/x in a.go, corrected by W9",
    "W5 failed: Red tests, not committed: test exited 1",
    "W6 running: Being committed into feat/x",
    "W8 waiting: Changes not committed, not merged, reviewed by W9",
    "W10 done: Committed into feat/x as 1a2b3c4d, reviewed by W9",
  ]);
  const badges = tasks.map((task) => {
    const m = card(task).match(
      /<span class="status-badge tone-([a-z]+)">[\s\S]*?<span>([^<]*)<\/span><\/span>/,
    );
    return m ? `${task.code} ${m[1]}: ${m[2]}` : `${task.code} none`;
  });
  assert.deepEqual(badges, [
    "W1 done: Done",
    "W2 running: Merging",
    "W3 done: Done",
    "W4 failed: Merge failed",
    "W5 failed: Merge failed",
    "W6 running: Merging",
    "W8 waiting: To review",
    "W10 done: Done",
  ]);
  // A task that waits for another's work to be committed says so; the task it waits for is a link to its card, which
  // stays in sight until its work is committed (settledTask).
  const next = {
    id: "W7",
    code: "W7",
    title: "Next",
    status: s.TaskStatus.PENDING,
    createTime: at(1),
    waitReason: "waits for W2 to be committed",
  };
  assert.match(
    card(next),
    /waits for <span class="agent-code">W2<\/span> to be committed/,
  );
  const linked = s.renderToStaticMarkup(
    h(s.WishTask, {
      task: next,
      codes: new Map([["t2", "W2"]]),
      onStop() {},
      async onSend() {},
    }),
  );
  assert.match(
    linked,
    /waits for <button type="button" class="text-button agent-code task-link">W2<\/button> to be committed/,
  );
});

test("work that merges stays in sight, shows as merging apart from the workers everywhere, then folds once committed", async () => {
  const wishId = "01a11833-a440-7479-a067-52615c91da72";
  const lamp = wish(wishId, "Ship the lamp", s.WishState.ACTIVE, 1);
  const at = (seconds) => ({ seconds: BigInt(seconds), nanos: 0 });
  const tasks = (state) => [
    {
      id: "t1",
      wishId,
      code: "T1",
      title: "Lay the ground",
      kind: s.TaskKind.AZIMA,
      status: s.TaskStatus.PENDING,
      dependsOn: [],
      proofNeeds: [],
      // No counts from the server: the card counts its parts as the server would.
      azima: { state: s.AzimaState.IN_PROGRESS, ready: true },
    },
    {
      id: "w1",
      wishId,
      code: "W1",
      title: "Dig",
      status: s.TaskStatus.DONE,
      partOf: "t1",
      dependsOn: [],
      createTime: at(1),
      endTime: at(10),
      integration: { state: s.IntegrationState.COMMITTED, branch: "feat/x" },
    },
    {
      id: "w8",
      wishId,
      code: "W8",
      title: "Pour",
      status: s.TaskStatus.DONE,
      partOf: "t1",
      dependsOn: [],
      createTime: at(2),
      endTime: at(20),
      integration: { state, branch: "feat/x" },
    },
    {
      id: "w9",
      wishId,
      code: "W9",
      title: "Build",
      status: s.TaskStatus.PENDING,
      partOf: "t1",
      dependsOn: ["w8"],
      createTime: at(3),
      waitReason: "waits for W8 to be committed",
    },
  ];
  const azimaCard = (list) => {
    const byId = new Map(list.map((x) => [x.id, x]));
    return s.renderToStaticMarkup(
      h(s.AzimaCard, {
        azima: list[0],
        parts: list.slice(1),
        tasks: byId,
        nest: s.mergeNest(list),
        render: (task) =>
          h(s.WishTask, {
            key: task.id,
            task,
            codes: new Map(list.map((x) => [x.id, x.code])),
            onStop() {},
          }),
      }),
    );
  };
  // W8 merges: among the moving parts, in sight, blue; only W1 is behind the fold. W9 links to it.
  const merging = tasks(s.IntegrationState.PENDING);
  const card = azimaCard(merging);
  const fold = card.indexOf('class="fold-line"');
  assert.ok(fold > 0);
  assert.match(card.slice(0, fold), /id="task-w8"/);
  assert.match(card.slice(0, fold), /id="task-w9"/);
  assert.doesNotMatch(card, /id="task-w1"/);
  assert.match(card, />Show 1 finished</);
  assert.match(card, /wish-task tone-running[^"]*" id="task-w8"/);
  assert.match(
    card,
    /waits for <button type="button" class="text-button agent-code task-link">W8<\/button> to be committed/,
  );
  // Its bar: W1 done, W8 running, W9 waiting.
  assert.match(card, /1 done, 1 running, 1 waiting/);
  assert.match(card, /class="azima-bar-running" style="width:33\.3/);
  // Committed, W8 folds with the finished, and runs no more.
  const committed = tasks(s.IntegrationState.COMMITTED);
  const after = azimaCard(committed);
  assert.doesNotMatch(after, /id="task-w8"/);
  assert.match(after, />Show the 2 finished</);
  assert.match(after, /2 done, 0 running, 1 waiting/);

  // The side panel and its icon rail show it merging, apart from the workers that run (W577: a merge waiting is no
  // worker, and the load bar never counted it); the wish is blue all the same, even folded to its icons.
  const sidebar = (list, collapsed) =>
    s.renderToStaticMarkup(
      h(s.WishSidebar, {
        wishes: [lamp],
        projects: [],
        counts: {
          [wishId]: { questions: 0, ...s.runningOf(list) },
        },
        onSelectPlan() {},
        selectedWishId: "",
        selectedProjectId: "",
        collapsed,
        onSelectWish() {},
        onSelectProject() {},
        onMove() {},
        onNewProject() {},
      }),
    );
  assert.match(
    sidebar(merging, false),
    /aria-label="1 merging: its work waits to merge, no worker runs"/,
  );
  assert.doesNotMatch(sidebar(merging, false), /aria-label="1 running"/);
  assert.match(sidebar(merging, true), /wish-tone tone-running/);
  assert.doesNotMatch(sidebar(committed, false), /running/);
  assert.doesNotMatch(sidebar(committed, true), /tone-running/);

  // The head of the wish and the flying carpet count it as running.
  const page = async (list) => {
    const transport = s.createRouterTransport(({ service }) => {
      service(s.WishService, { list: () => ({ wishes: [lamp] }) });
      service(s.TaskService, { list: () => ({ tasks: list }) });
      service(s.QuestionService, { list: () => ({ questions: [] }) });
      service(s.BlockService, { list: () => ({ blocks: [] }) });
    });
    const djinn = s.createDjinn(transport, 10);
    const close = djinn.store.open(wishId);
    await djinn.store.changed(wishId, [s.Change.WISH]);
    close();
    const view = s.renderToStaticMarkup(
      h(
        s.DjinnProvider,
        { djinn },
        h(s.WishView, { wish: lamp, onToast() {} }),
      ),
    );
    const plan = s.renderToStaticMarkup(
      h(
        s.DjinnProvider,
        { djinn },
        h(s.FlightPlan, { wishes: [lamp], onOpen() {}, onToast() {} }),
      ),
    );
    return { view, plan };
  };
  // Merging, no worker runs for it (W577): its own pill, the wish blue all the same.
  const now = await page(merging);
  assert.match(
    now.view,
    /count-pill tone-running" title="1 merging: its work waits to merge, no worker runs"/,
  );
  assert.doesNotMatch(now.view, /title="1 running"/);
  assert.match(now.plan, /plan-wish tone-running/);
  assert.match(now.plan, /<span>1 merging<\/span>/);
  assert.doesNotMatch(now.plan, /<span>1 running<\/span>/);
  const later = await page(committed);
  assert.doesNotMatch(later.view, /count-pill tone-running/);
  assert.doesNotMatch(later.plan, /plan-wish tone-running/);
});

test("a failed merge an agent fixes shows orange, naming its correction, everywhere; red once no correction runs", async () => {
  const wishId = "01a13c45-f3a8-7295-855a-73c0445d7c88";
  const lamp = wish(wishId, "Ship the lamp", s.WishState.ACTIVE, 1);
  const at = (seconds) => ({ seconds: BigInt(seconds), nanos: 0 });
  // W1's merge conflicted; W2 corrects it, in the state given.
  const tasks = (status) => [
    {
      id: "w1",
      wishId,
      code: "W1",
      title: "Pour",
      status: s.TaskStatus.DONE,
      dependsOn: [],
      createTime: at(1),
      endTime: at(10),
      integration: {
        state: s.IntegrationState.CONFLICT,
        branch: "feat/x",
        reason: "a.go conflicts",
        correctedBy: "w2",
        attempts: 1,
      },
    },
    {
      id: "w2",
      wishId,
      code: "W2",
      title: "Correct the merge of W1",
      status,
      dependsOn: [],
      createTime: at(11),
      correction: { attempt: 1, failure: { taskIds: ["w1"] } },
    },
  ];
  const fixing = tasks(s.TaskStatus.RUNNING);
  const failed = tasks(s.TaskStatus.FAILED);
  const card = (list) =>
    s.renderToStaticMarkup(
      h(s.WishTask, {
        task: list[0],
        codes: new Map(list.map((x) => [x.id, x.code])),
        fixedBy: s.fixerOf(list[0], list)?.code,
        onStop() {},
        async onSend() {},
      }),
    );
  // The card: orange with a wrench, its words and its work line naming W2, a link to its card.
  const now = card(fixing);
  assert.match(now, /wish-task tone-fixing/);
  assert.match(
    now,
    /<span class="status-badge tone-fixing">[\s\S]*?<span>Merge being fixed by W2<\/span><\/span>/,
  );
  assert.match(
    now,
    /work-fixing">Conflict, not committed: a\.go conflicts, being fixed by <button type="button" class="text-button agent-code task-link">W2<\/button><\/p>/,
  );
  assert.doesNotMatch(now, /tone-failed|Merge failed/);
  // W2 failed: no correction runs, red as before.
  const red = card(failed);
  assert.match(red, /wish-task tone-failed/);
  assert.match(red, /<span>Merge failed<\/span>/);
  assert.match(
    red,
    /work-failed">Conflict, not committed: a\.go conflicts, corrected by W2</,
  );

  // The side panel counts it apart, orange.
  const sidebar = (list) =>
    s.renderToStaticMarkup(
      h(s.WishSidebar, {
        wishes: [lamp],
        projects: [],
        counts: { [wishId]: { questions: 0, ...s.runningOf(list) } },
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
  const detail = "1 merge being fixed: an agent corrects it, nothing to do";
  assert.match(
    sidebar(fixing),
    new RegExp(`count-pill tone-fixing" title="${detail}"`),
  );
  assert.doesNotMatch(sidebar(failed), /tone-fixing/);

  // The head of the wish and the flying carpet.
  const page = async (list) => {
    const transport = s.createRouterTransport(({ service }) => {
      service(s.WishService, { list: () => ({ wishes: [lamp] }) });
      service(s.TaskService, { list: () => ({ tasks: list }) });
      service(s.QuestionService, { list: () => ({ questions: [] }) });
      service(s.BlockService, { list: () => ({ blocks: [] }) });
    });
    const djinn = s.createDjinn(transport, 10);
    const close = djinn.store.open(wishId);
    await djinn.store.changed(wishId, [s.Change.WISH]);
    close();
    const view = s.renderToStaticMarkup(
      h(
        s.DjinnProvider,
        { djinn },
        h(s.WishView, { wish: lamp, onToast() {} }),
      ),
    );
    const plan = s.renderToStaticMarkup(
      h(
        s.DjinnProvider,
        { djinn },
        h(s.FlightPlan, { wishes: [lamp], onOpen() {}, onToast() {} }),
      ),
    );
    return { view, plan };
  };
  const live = await page(fixing);
  assert.match(
    live.view,
    new RegExp(`count-pill tone-fixing" title="${detail}"`),
  );
  assert.match(live.view, /<b>1<\/b>being fixed<\/span>/);
  assert.match(
    live.plan,
    new RegExp(`count-pill tone-fixing" title="${detail}"`),
  );
  const dead = await page(failed);
  assert.doesNotMatch(dead.view, /tone-fixing/);
  assert.doesNotMatch(dead.plan, /tone-fixing/);
});

test("the update banner proposes to install a build committed, on one line, what changed unfolded on demand", () => {
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
    base: "",
    running: true,
    changesCount: 0,
    lacks: [],
    lacksCount: 0,
  };
  const banner = (
    phase = { kind: "idle" },
    dismissedBuilds = [],
    installing,
    open = [],
  ) =>
    s.renderToStaticMarkup(
      h(s.UpdateBannerView, {
        state: {
          current: "v1",
          ready: "",
          notResumed: [],
          notesUrl: "",
          builds: [build],
          installing,
        },
        phase,
        dismissed: false,
        dismissedBuilds,
        open,
        onInstall() {},
        onDismiss() {},
        onNotes() {},
      }),
    );
  const html = banner();
  // One line: the build named by its commit, branch and wish, the tasks it brings; its actions beside it, the
  // details folded.
  assert.match(
    html,
    /<span class="update-banner-text">Build <code>1a2b3c4d<\/code> of feat\/wails-go<span class="update-banner-quiet"> · Run Djinn on itself<\/span><span class="update-banner-quiet"> — W5, W6<\/span><\/span>/,
  );
  assert.match(
    html,
    /title="1a2b3c4d · feat\/wails-go · Run Djinn on itself · djinn\nW5, W6"/,
  );
  assert.match(html, /<span class="brand small"><span class="brand-mark"><img/);
  assert.match(
    html,
    /<div class="update-banner-actions"><button type="button" class="update-banner-primary">Install and restart<\/button><button type="button" class="update-banner-more" aria-expanded="false">Details<\/button><button type="button" class="update-banner-close" aria-label="Dismiss" title="Dismiss">×<\/button><\/div><\/div><\/section>/,
  );
  assert.doesNotMatch(html, /update-banner-body/);
  // Unfolded: what changed, each task with its worker's summary folded, then the commits folded.
  const unfolded = banner({ kind: "idle" }, [], undefined, [build.sha]);
  assert.match(
    unfolded,
    /aria-expanded="true">Less<\/button>.*<div class="update-banner-body"><strong>What changed<\/strong>/,
  );
  assert.match(
    unfolded,
    /<ul class="update-banner-tasks"><li><details class="update-banner-task"><summary><code>W5<\/code> Work of W5<\/summary><div class="update-banner-summary">To check: the banner shows the build\.<\/div><\/details><\/li><li><details class="update-banner-task"><summary><code>W6<\/code> Work of W6<\/summary><div class="update-banner-summary">Done: W6 finished\.<\/div><\/details><\/li><\/ul>/,
  );
  assert.match(
    unfolded,
    /<details class="update-banner-commits"><summary>commits<\/summary><ul><li>Work of W6<\/li><li>Work of W5<\/li><\/ul><\/details>/,
  );
  // While it installs, its buttons go; its details stay.
  const installing = banner({ kind: "installing", sha: build.sha });
  assert.match(installing, /Installing…/);
  assert.doesNotMatch(installing, /update-banner-primary|update-banner-close/);
  // Then it says where the install stands, as Djinn tells it, in this window or another: what it waits for, that it
  // builds, that Djinn restarts.
  const step = (step, waiting = "") =>
    banner({ kind: "idle" }, [], { sha: build.sha, step, waiting });
  const waits = step(s.InstallStep.WAITING, "gate install: held by W9 (Other)");
  assert.match(waits, /Waiting — gate install: held by W9 \(Other\)/);
  assert.doesNotMatch(waits, /update-banner-primary|update-banner-close/);
  assert.match(step(s.InstallStep.PREPARING), /Preparing the build…/);
  assert.match(step(s.InstallStep.BUILDING), /Building…/);
  assert.match(step(s.InstallStep.RESTARTING), /Restarting…/);
  // Dismissed, that build no longer shows.
  assert.equal(banner({ kind: "idle" }, [build.sha]), "");
  // A build that installed no newer Djinn says it is installed, and restarts nothing.
  assert.match(
    s.renderToStaticMarkup(
      h(s.UpdateBannerView, {
        state: {
          current: "v1",
          ready: "",
          notResumed: [],
          notesUrl: "",
          builds: [],
        },
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

test("the update banner names the build against the running Djinn: what it adds, what it lacks, the other branches folded", () => {
  const proposal = (branch, sha, wishTitle, extra = {}) => ({
    wishTitle,
    project: "djinn",
    branch,
    sha,
    tasks: [],
    changes: [],
    summaries: [],
    base: "abcdef0123",
    running: false,
    changesCount: 0,
    lacks: [],
    lacksCount: 0,
    ...extra,
  });
  const mine = proposal(
    "feat/wails-go",
    "aaaa1111bbbb",
    "Run Djinn on itself",
    {
      running: true,
      tasks: ["W466", "W467"],
      summaries: [
        { code: "W466", title: "The tasks tab", summary: "" },
        { code: "W467", title: "The diff", summary: "" },
      ],
      changes: ["ui: the diff", "ui: the tasks tab"],
      changesCount: 2,
    },
  );
  const side = proposal(
    "feat/clement-port",
    "e60f8fc00000",
    "Clément's branches",
    {
      tasks: ["W2"],
      summaries: [{ code: "W2", title: "Port Clément's work", summary: "" }],
      changes: ["port: Clément's work"],
      changesCount: 1,
      lacks: ["ui: the tasks tab", "ui: the diff", "harness: azimas"],
      lacksCount: 3,
    },
  );
  const other = proposal("feat/x", "cccc2222dddd", "", {
    changes: ["fix"],
    changesCount: 1,
  });
  const banner = (state, { dismissedBuilds = [], open = [] } = {}) =>
    s.renderToStaticMarkup(
      h(s.UpdateBannerView, {
        state: {
          current: "local-g abcdef0",
          ready: "",
          notResumed: [],
          notesUrl: "",
          builds: [],
          ...state,
        },
        phase: { kind: "idle" },
        dismissed: false,
        dismissedBuilds,
        open,
        onInstall() {},
        onDismiss() {},
        onNotes() {},
      }),
    );
  // The running Djinn's branch first, on its line; the other branches' builds folded under one line naming them.
  const html = banner({ builds: [mine, side, other] });
  assert.match(
    html,
    /Build <code>aaaa1111<\/code> of feat\/wails-go<span class="update-banner-quiet"> · Run Djinn on itself<\/span><span class="update-banner-quiet"> — adds 2 tasks<\/span>/,
  );
  assert.match(
    html,
    /Builds of 2 other branches: feat\/clement-port, feat\/x<\/span><\/div><div class="update-banner-actions"><button type="button" class="update-banner-more" aria-expanded="false">Details<\/button><\/div>/,
  );
  assert.doesNotMatch(html, /e60f8fc0/);
  assert.equal(html.match(/<img/g).length, 1);
  // Unfolded, each other branch on its line, with its own install and ×: what it adds, and that it is older than the
  // running Djinn.
  const others = banner({ builds: [mine, side, other] }, { open: ["others"] });
  assert.match(
    others,
    /<div class="update-banner-others"><section class="update-banner-part"><div class="update-banner-head"><div class="update-banner-title" title="e60f8fc0 · feat\/clement-port · Clément&#x27;s branches · djinn\nadds 1 task · older than the running Djinn on 3 commits"><span class="update-banner-text">Build <code>e60f8fc0<\/code> of feat\/clement-port<span class="update-banner-quiet"> · Clément&#x27;s branches<\/span><span class="update-banner-quiet"> — adds 1 task · older than the running Djinn on 3 commits<\/span><\/span><\/div><div class="update-banner-actions"><button type="button" class="update-banner-primary">Install and restart<\/button>/,
  );
  assert.match(others, /— adds 1 commit<\/span>/);
  assert.equal(others.match(/class="update-banner-close"/g).length, 3);
  // A build's details: the tasks merged since the running Djinn, the commits folded, and those it lacks folded.
  const details = banner({ builds: [side] }, { open: ["others", side.sha] });
  assert.match(
    details,
    /<strong>Since the running Djinn \(abcdef01\)<\/strong><ul class="update-banner-tasks"><li><div class="update-banner-task-line"><code>W2<\/code> Port Clément&#x27;s work<\/div><\/li><\/ul><details class="update-banner-commits"><summary>1 commit<\/summary><ul><li>port: Clément&#x27;s work<\/li><\/ul><\/details><details class="update-banner-commits update-banner-lacks"><summary>Older than the running Djinn on 3 commits<\/summary><ul><li>ui: the tasks tab<\/li>/,
  );
  // A dismissed build goes, the others stay; all dismissed, no banner.
  assert.doesNotMatch(
    banner({ builds: [mine, side] }, { dismissedBuilds: [mine.sha] }),
    /aaaa1111/,
  );
  assert.equal(
    banner({ builds: [mine, side] }, { dismissedBuilds: [mine.sha, side.sha] }),
    "",
  );

  // The newer Djinn ready is named the same way, with its own ×, dismissed by its version.
  const ready = banner({ ready: "local-ge60f8fc", readyBuild: side });
  assert.match(
    ready,
    /<div class="update-banner-title" title="local-g abcdef0 → local-ge60f8fc\ne60f8fc0 · feat\/clement-port · Clément&#x27;s branches · djinn\nadds 1 task · older than the running Djinn on 3 commits">/,
  );
  assert.match(
    ready,
    /New Djinn ready: <code>e60f8fc0<\/code> of feat\/clement-port<span class="update-banner-quiet"> · Clément&#x27;s branches<\/span><span class="update-banner-quiet"> — adds 1 task · older than the running Djinn on 3 commits<\/span><\/span><\/div><div class="update-banner-actions"><button type="button" class="update-banner-primary">Update<\/button><button type="button" class="update-banner-more" aria-expanded="false">Details<\/button><button type="button" class="update-banner-close" aria-label="Dismiss" title="Dismiss">×<\/button>/,
  );
  assert.equal(s.readyKey("local-ge60f8fc"), "ready:local-ge60f8fc");
  assert.equal(
    banner(
      { ready: "local-ge60f8fc", readyBuild: side },
      { dismissedBuilds: ["ready:local-ge60f8fc"] },
    ),
    "",
  );
  // Another version ready shows again.
  assert.match(
    banner(
      { ready: "local-g0123456", readyBuild: undefined },
      { dismissedBuilds: ["ready:local-ge60f8fc"] },
    ),
    /A new version of Djinn is ready<\/span>/,
  );
});

test("the update banner shows a build of the wish's moved branch as the update, a parallel branch as another (W527)", () => {
  const proposal = (branch, sha, wishTitle, extra = {}) => ({
    wishTitle,
    project: "djinn",
    branch,
    sha,
    tasks: [],
    changes: ["harness: no orphan"],
    summaries: [],
    base: "abcdef0123",
    running: false,
    continues: "",
    changesCount: 1,
    lacks: [],
    lacksCount: 0,
    ...extra,
  });
  // The wish moved its integration from feat/wails-go, which the running Djinn was built from, to feat/robustness,
  // cut at its tip: that build holds the running commit. feat/clement-port's does not.
  const moved = proposal("feat/robustness", "bbbb3333cccc", "Orchestrator", {
    continues: "feat/wails-go",
  });
  const parallel = proposal(
    "feat/clement-port",
    "e60f8fc00000",
    "Clément's branches",
    { lacks: ["ui: the diff"], lacksCount: 1 },
  );
  const html = s.renderToStaticMarkup(
    h(s.UpdateBannerView, {
      state: {
        current: "local-300b7fe",
        ready: "",
        notResumed: [],
        notesUrl: "",
        builds: [moved, parallel],
      },
      phase: { kind: "idle" },
      dismissed: false,
      onInstall() {},
      onDismiss() {},
      onNotes() {},
    }),
  );
  // The moved branch's build heads the banner, with the djinn's mark, as the update of the running line.
  assert.match(
    html,
    /<span class="brand small">.*<span class="update-banner-text">Build <code>bbbb3333<\/code> of feat\/robustness, continuing feat\/wails-go<span class="update-banner-quiet"> · Orchestrator<\/span><span class="update-banner-quiet"> — adds 1 commit<\/span><\/span><\/div><div class="update-banner-actions"><button type="button" class="update-banner-primary">Install and restart<\/button>/,
  );
  // Only the parallel branch is another branch, folded.
  assert.match(html, /A build of another branch: feat\/clement-port<\/span>/);
  assert.doesNotMatch(html, /another branch[^<]*feat\/robustness/);
  assert.doesNotMatch(html, /e60f8fc0/);
  assert.equal(html.match(/<img/g).length, 1);
});

test("the update banner says how an install failed, with its last lines, what installs by itself, and offers the restart of the newer Djinn it installed (W554)", () => {
  const line = {
    wishTitle: "Robustness",
    project: "djinn",
    branch: "feat/robustness",
    sha: "aaaa1111bbbb",
    tasks: [],
    changes: ["ui: the banner"],
    summaries: [],
    base: "abcdef0123",
    running: true,
    continues: "",
    changesCount: 1,
    lacks: [],
    lacksCount: 0,
    auto: true,
  };
  const banner = (state, phase = { kind: "idle" }, open = []) =>
    s.renderToStaticMarkup(
      h(s.UpdateBannerView, {
        state: {
          current: "local-abcdef0",
          ready: "",
          installed: false,
          notResumed: [],
          notesUrl: "",
          builds: [],
          ...state,
        },
        phase,
        dismissed: false,
        open,
        onInstall() {},
        onDismiss() {},
        onNotes() {},
      }),
    );
  const installing = (extra) => ({
    sha: line.sha,
    step: s.InstallStep.BUILDING,
    waiting: "",
    doing: "",
    auto: false,
    error: "",
    log: [],
    ...extra,
  });
  // Past its deadline: why, Try again in place of the spinner, the last lines its command wrote in its details; even
  // while the window still waits for its call's answer.
  const failed = installing({
    step: s.InstallStep.FAILED,
    error:
      "it did not end within 20m0s, at install: go build ./cmd/djinn: Djinn stopped it",
    log: ["task: [ui] npm run build", "task: [install] go build ./cmd/djinn"],
  });
  for (const phase of [
    { kind: "idle" },
    { kind: "installing", sha: line.sha, seen: true },
  ]) {
    const html = banner({ builds: [line], installing: failed }, phase, [
      line.sha,
    ]);
    assert.match(
      html,
      /Build aaaa1111 did not install: it did not end within 20m0s, at install: go build \.\/cmd\/djinn: Djinn stopped it<\/span>/,
    );
    assert.match(
      html,
      /<button type="button" class="update-banner-primary">Try again<\/button>/,
    );
    assert.doesNotMatch(html, /Installing…|Building…/);
    assert.match(
      html,
      /<pre class="update-banner-log">task: \[ui\] npm run build\ntask: \[install\] go build \.\/cmd\/djinn<\/pre>/,
    );
  }
  // A build of the running line installs by itself: it says so, and the step of its command that runs; it waits for
  // a free machine, saying why.
  assert.match(
    banner({
      builds: [line],
      installing: installing({ auto: true, doing: "ui: npm run build" }),
    }),
    /<span>Installing by itself: Building… \(ui: npm run build\)<\/span>/,
  );
  assert.match(
    banner({
      builds: [line],
      installing: installing({
        auto: true,
        step: s.InstallStep.WAITING,
        waiting: "gate test: held by W5",
      }),
    }),
    /<span>Installs by itself once free — gate test: held by W5<\/span>/,
  );
  // Installed: the newer Djinn is ready, with what it adds, and the click only restarts.
  const ready = banner({
    ready: "local-aaaa111",
    installed: true,
    readyBuild: { ...line, auto: false },
  });
  assert.match(
    ready,
    /Djinn local-aaaa111 is ready: <code>aaaa1111<\/code> of feat\/robustness<span class="update-banner-quiet"> · Robustness<\/span><span class="update-banner-quiet"> — adds 1 commit<\/span><\/span><\/div><div class="update-banner-actions"><button type="button" class="update-banner-primary">Restart<\/button>/,
  );
  assert.match(
    banner({ ready: "local-aaaa111", installed: true }),
    /Djinn local-aaaa111 is ready<\/span>.*>Restart<\/button>/,
  );
});

test("an azima whose work is done awaits its proof: its own label and tone, what it needs, the pill apart, and the flying carpet lists what a person can give", async () => {
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
  // What it needs is on its badge's hover and in the card opened, not in its heading.
  assert.doesNotMatch(card, /azima-needs|>a Mac</);
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
  // The flying carpet lists the proof a person can give among what waits for them, never as work; a Mac's is not.
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

test("Lead is split: the button resumes the one lead, focused on the wish, the arrow lists this machine's agents", () => {
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
  assert.match(closed, /<span>Talk to the Djinn<\/span><\/button>/);
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
    /<button role="menuitem" class="lead-agent current" aria-current="true"><span class="lead-agent-name">Claude<svg[^]*?The Djinn&#x27;s agent: resumes it, focused on this wish/,
  );
  assert.match(
    open,
    /<button role="menuitem" class="lead-agent" disabled=""><span class="lead-agent-name">Codex<\/span><span class="lead-agent-detail">codex is not installed on this machine<\/span>/,
  );
  assert.match(
    open,
    /<button role="menuitem" class="lead-agent"><span class="lead-agent-name">Antigravity<\/span><span class="lead-agent-detail">Starts the Djinn anew on this agent, from the brief<\/span>/,
  );
  // On the flying carpet, on no wish: neither the button nor the menu speaks of a wish.
  const carpet = s.renderToStaticMarkup(
    h(s.LeadButton, { ...props, focused: false, open: true }),
  );
  assert.match(
    carpet,
    /<button class="button secondary small" title="Opens the Djinn&#x27;s terminal">/,
  );
  assert.match(
    carpet,
    /<span class="lead-agent-name">Claude<svg[^]*?<span class="lead-agent-detail">The Djinn&#x27;s agent: resumes it<\/span>/,
  );
  assert.doesNotMatch(carpet, /this wish/);
  // The lead could not start, or ended: a dot on Lead, its tooltip saying why, until Lead starts it again.
  assert.doesNotMatch(closed, /lead-dot/);
  assert.equal(s.leadTrouble(undefined, ""), undefined);
  const failed = s.leadTrouble(undefined, "no project yet");
  assert.deepEqual(failed, {
    text: "The Djinn could not start: no project yet. Talk to the Djinn tries again",
    failed: true,
  });
  assert.equal(
    s.leadTrouble({ code: 2 }, "").text,
    "The Djinn ended (code 2): its tab keeps its last output. Talk to the Djinn starts it again",
  );
  assert.equal(s.leadTrouble({ code: 0 }, "").failed, false);
  const dotted = s.renderToStaticMarkup(
    h(s.LeadButton, { ...props, trouble: failed }),
  );
  assert.match(
    dotted,
    /<button class="button secondary small" title="The Djinn could not start: no project yet. Talk to the Djinn tries again">[^]*?<span>Talk to the Djinn<\/span><span class="lead-dot failed" aria-hidden="true"><\/span><\/button>/,
  );
  const quit = s.renderToStaticMarkup(
    h(s.LeadButton, { ...props, trouble: s.leadTrouble({ code: 0 }, "") }),
  );
  assert.match(quit, /<span class="lead-dot" aria-hidden="true">/);

  // The agents not yet known: the menu says it loads.
  const loading = s.renderToStaticMarkup(
    h(s.LeadMenu, { recorded, onPick() {} }),
  );
  assert.match(loading, /Loading…/);
  // Last, the way to set the agents up, when the window offers it.
  assert.doesNotMatch(open, /Set up the agents/);
  const setup = s.renderToStaticMarkup(
    h(s.LeadButton, { ...props, open: true, onSetup() {} }),
  );
  assert.match(
    setup,
    /Antigravity<\/span>[^]*<button role="menuitem" class="lead-agent"><span class="lead-agent-name"><svg[^]*?<\/svg>Set up the agents<\/span><\/button><\/div>/,
  );
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

test("the wish's title shows in the head and edits in place", () => {
  const titled = s.renderToStaticMarkup(
    h(s.WishTitle, {
      title: "Ship the lamp",
      onSave() {},
    }),
  );
  assert.match(
    titled,
    /<h1 class="wish-title" role="button" tabindex="0" title="Click to rename the wish">Ship the lamp<\/h1>/,
  );
  const editing = s.renderToStaticMarkup(
    h(s.WishTitle, {
      title: "Ship the lamp",
      editing: true,
      onSave() {},
    }),
  );
  assert.match(
    editing,
    /<input type="text" class="wish-title-edit" aria-label="Title" maxLength="500" autofocus="" value="Ship the lamp"\/>/,
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
      ["q3", "later", undefined],
      ["q6", "later", undefined],
      ["ready-w1", "ready", undefined],
    ],
  );
  const markup = s.renderToStaticMarkup(h(s.AttentionBar, { items }));
  assert.match(markup, /<button class="attention-item level-move"/);
  // A move with no link has no button beside its line.
  assert.doesNotMatch(markup, /move-link/);
  assert.match(
    markup,
    /<span class="attention-level"><svg[^>]*class="lucide lucide-circle-user-round[^>]*>.*<\/svg>Your move<\/span>/,
  );
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
  const items = s.attentionOf(
    [
      { wish: w1, item: q("q1"), blocking: [] },
      { wish: w1, item: q("q2", { waitingLead: true }), blocking: [] },
      { wish: w2, item: q("q3"), blocking: [] },
      { wish: w1, item: q("q4"), blocking: [] },
    ],
    [
      {
        wish: w1,
        item: {
          id: "t9",
          code: "W9",
          status: s.TaskStatus.FAILED,
          error: "",
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
    /<span class="attention-item level-later"><span class="attention-level">Can wait<\/span><b class="attention-code">Q4<\/b><span class="attention-text">q4<\/span><\/span>/,
  );

  // In a folded part: a decision older than the latest ones shows when it is the one in focus, as the bar asks.
  const decisions = Array.from({ length: 40 }, (_, i) => ({
    item: {
      id: `d${i}`,
      ref: `d${i}`,
      block: { title: `Decision ${i}`, content: "" },
      human: false,
      approved: false,
      by: "lead",
      icon: "*",
      tasks: [],
    },
  }));
  const log = (focus) =>
    s.renderToStaticMarkup(
      h(s.DecisionLog, { items: decisions, focus, onTask() {} }),
    );
  assert.doesNotMatch(log(""), /id="decision-d35"/);
  assert.match(log("d35"), /id="decision-d35"/);
});

// W507: the lead made a wish with no project and one research task; its worker ended done while its permissions
// refused 6 calls. The window showed no task, read the wish's rank as 3 questions, and proposed the grant.
test("regression W507: a wish without a project shows its task, its rank as a rank, and no grant", async () => {
  const wishId = "01a1317d-4d8c-7de3-b351-e54f2a70840f";
  // The lamp proposes no grant while the lead has not decided about a task's refused calls (internal/plan.Ready).
  const wispr = wish(
    wishId,
    "Install Wispr Flow on Linux",
    s.WishState.ACTIVE,
    3,
  );
  const at = (seconds) => ({ seconds: BigInt(seconds), nanos: 0 });
  const research = {
    id: "01a1317d-4e22-78bc-9bfa-daef6104ed3a",
    wishId,
    code: "W1",
    title: "Find a safe, real way to run Wispr Flow on this Linux machine",
    status: s.TaskStatus.DONE,
    createTime: at(1),
    endTime: at(30),
    refused: { count: 6, names: ["Read", "Glob"] },
  };
  const transport = s.createRouterTransport(({ service }) => {
    service(s.WishService, {
      list: () => ({ wishes: [wispr] }),
      watch: () => new ReadableStream(),
    });
    service(s.ProjectService, { list: () => ({ projects: [] }) });
    service(s.TaskService, {
      list: () => ({ tasks: [research], total: 1 }),
    });
    service(s.QuestionService, {
      list: () => ({ questions: [], total: 0 }),
    });
    service(s.BlockService, { list: () => ({ blocks: [], total: 0 }) });
  });
  const djinn = s.createDjinn(transport, 10);
  const close = djinn.store.open(wishId);
  await djinn.store.changed(wishId, [s.Change.WISH, s.Change.TASK]);
  close();
  const html = s.renderToStaticMarkup(
    h(s.DjinnProvider, { djinn }, h(s.WishView, { wish: wispr, onToast() {} })),
  );
  assert.match(html, /<span class="eyebrow">No project<\/span>/);
  // (b) The rank reads as a rank, and nothing counts questions: there are none.
  assert.match(html, /<span>Active · rank 3<\/span>/);
  assert.doesNotMatch(html, /questions? wait|to decide/);
  // (c) No grant until the lead decides about the refused calls, and nothing waits for you: they are the lead's
  // (Q273), never a warning in the window.
  assert.doesNotMatch(html, /My wish is granted|id="grant-/);
  assert.doesNotMatch(
    html,
    /waits? for you|with problems|refused|id="waiting-|tone-interrupted/,
  );
  // (a) The Tasks tab counts it.
  assert.match(
    html,
    /id="view-tab-tasks" aria-selected="false" class="">Tasks<span class="count">1<\/span>/,
  );

  // (a) In the Tasks tab, a task part of no azima shows, folded under Finished once done, a clean Done whether its
  // calls were refused or not.
  const clean = {
    ...research,
    id: "t2",
    code: "W2",
    title: "Read the vendor's notes",
    refused: undefined,
  };
  const tasks = [research, clean];
  const card = (task) =>
    h(s.WishTask, {
      key: task.id,
      task,
      onStop() {},
      async onSend() {},
      async onDone() {},
    });
  const tab = s.renderToStaticMarkup(
    h(s.TaskSections, {
      moving: s.movingTasks(tasks),
      finished: s.finishedTasks(tasks),
      render: card,
    }),
  );
  assert.match(tab, /Moving or waiting<span class="count">0<\/span>/);
  assert.match(
    tab,
    /<section class="wish-section tasks-finished" aria-label="Finished"><div class="section-title"><h2>Finished<span class="count">2<\/span><\/h2><\/div><button type="button" class="fold-line" aria-expanded="false">.*Show the 2 finished<\/button><\/section>/,
  );
  // A link brings them into sight: unfolded, both a clean Done, neither to mark done.
  const shown = s.renderToStaticMarkup(
    h(s.TaskSections, {
      moving: s.movingTasks(tasks),
      finished: s.finishedTasks(tasks),
      showFinished: true,
      render: card,
    }),
  );
  assert.match(
    shown,
    /tasks-finished.*class="wish-task tone-done[^"]*".*<span>Done<\/span>.*<span class="agent-code">W1<.*class="wish-task tone-done[^"]*".*<span>Done<\/span>.*<span class="agent-code">W2</,
  );
  assert.doesNotMatch(shown, /Done with problems|aria-label="Mark done"/);

  // (b) The side panel shows the rank as #3, beside no pill.
  const side = s.renderToStaticMarkup(
    h(s.WishSidebar, {
      wishes: [wispr],
      projects: [],
      counts: { [wishId]: { questions: 0, running: 0, watching: 0 } },
      selectedWishId: wishId,
      selectedProjectId: "",
      collapsed: false,
      onSelectWish() {},
      onSelectProject() {},
      onMove() {},
      onNewProject() {},
    }),
  );
  assert.match(side, /<small class="wish-rank">#3<\/small>/);
  assert.doesNotMatch(side, /count-pill/);
});

test("an open question that can wait stays visible in WishView action center and attention bar, never folded", async () => {
  const wishId = "01a11833-a440-7479-a067-52615c91da70";
  const lamp = wish(wishId, "Djinn sur Wails", s.WishState.ACTIVE, 1);
  const transport = s.createRouterTransport(({ service }) => {
    service(s.WishService, {
      list: () => ({ wishes: [lamp] }),
      watch: () => new ReadableStream(),
    });
    service(s.ProjectService, {
      list: () => ({ projects: [] }),
    });
    service(s.TaskService, {
      list: () => ({ tasks: [], total: 0 }),
    });
    service(s.QuestionService, {
      list: () => ({
        questions: [
          {
            id: "q86",
            code: "Q86",
            text: "The push checks of feat/wails-go stay red. What should Djinn do?",
            options: ["Check again", "Push unchecked", "Leave held"],
            before: "",
            wishId,
          },
        ],
        total: 1,
      }),
    });
    service(s.BlockService, {
      list: () => ({ blocks: [], total: 0 }),
    });
    service(s.InboxService, {
      list: () => ({ items: [] }),
      sources: () => ({ sources: [] }),
    });
  });
  const djinn = s.createDjinn(transport, 10);
  const close = djinn.store.open(wishId);
  await djinn.store.changed(wishId, [s.Change.QUESTION]);
  close();
  const html = s.renderToStaticMarkup(
    h(s.DjinnProvider, { djinn }, h(s.WishView, { wish: lamp, onToast() {} })),
  );

  // 1. Attention bar shows the can-wait question with level-later and "Can wait".
  assert.match(html, /class="attention-item level-later"/);
  assert.match(html, /<b class="attention-code">Q86<\/b>/);
  assert.match(html, /<span class="attention-level">Can wait<\/span>/);

  // 2. Action center displays the question in decisions-section with can-wait tone.
  assert.match(html, /class="action-center"/);
  assert.match(html, /class="decisions-section"/);
  assert.match(html, /question-card open can-wait/);
  assert.match(html, /<span class="question-id">Q86<\/span>/);
  assert.match(html, /status-badge tone-later[^>]*>.*<span>Can wait<\/span>/);
  assert.match(html, /The push checks of feat\/wails-go stay red/);

  // 3. Question options and actions are rendered, not folded out of sight.
  assert.match(html, /Check again/);
  assert.doesNotMatch(html, /question-fold/);
  assert.doesNotMatch(html, /question-card[^"]*folded/);
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

test("a merge git refuses for want of an identity waits, the git config to run said, no conflict", () => {
  // The reason Djinn writes (noIdentity, internal/harness/worktree.go), as W5 of PR #2's CI met it.
  const reason =
    "git has no identity to sign Djinn's merges and commits: run git config --global user.name \"Your Name\" and git config --global user.email you@example.com (or the same without --global, in the project's repository)";
  const said =
    "git has no identity to sign Djinn&#x27;s merges and commits: run git config --global user.name &quot;Your Name&quot; and git config --global user.email you@example.com (or the same without --global, in the project&#x27;s repository)";
  const card = s.renderToStaticMarkup(
    h(s.WishTask, {
      task: {
        id: "W1",
        code: "W1",
        title: "Task W1",
        status: s.TaskStatus.DONE,
        createTime: { seconds: 1n, nanos: 0 },
        endTime: { seconds: 2n, nanos: 0 },
        integration: {
          state: s.IntegrationState.PENDING,
          branch: "main",
          sha: "",
          reason,
          correctedBy: "",
          reviewedBy: "",
        },
      },
      onStop() {},
      async onSend() {},
    }),
  );
  assert.ok(
    card.includes(
      `<p class="wish-task-note wish-task-work work-waiting">Waiting to be committed: ${said}</p>`,
    ),
    card,
  );
  assert.doesNotMatch(card, /Conflict/);
  const head = s.renderToStaticMarkup(
    h(s.MainMerges, {
      mains: [{ projectId: "p1", held: reason }],
      projects: [{ id: "p1", name: "app" }],
    }),
  );
  assert.ok(head.includes(`The merge of main waits: ${said}`), head);
});

test("the project view lists the setup and the checks, when each runs, and how each last ran", () => {
  const none = s.renderToStaticMarkup(
    h(s.ProjectChecks, { setup: "", checks: [], runs: [] }),
  );
  assert.match(
    none,
    /No check: Djinn integrates this project&#x27;s work only into a wish&#x27;s own branch, untested/,
  );
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
      this.style = { setProperty() {} };
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
    querySelector() {
      return null;
    }
    // Motion measures the box of an element it lays out (the wish's pill, workspace-action.tsx).
    getBoundingClientRect() {
      return {
        x: 0,
        y: 0,
        top: 0,
        left: 0,
        right: 0,
        bottom: 0,
        width: 0,
        height: 0,
      };
    }
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

// A branch Djinn pushes, as ProjectService sends it in Project.syncs.
const projectSync = (branch, ahead, behind, extra = {}) => ({
  wishId: "w1",
  wishTitle: "Lamp",
  branch,
  remote: "origin",
  target: branch,
  ahead,
  behind,
  azimaId: "",
  azimaCode: "",
  azimaTitle: "",
  ...extra,
});

test("the side panel lists each branch of a project out of sync, one short line each, with a small push button", () => {
  const project = {
    id: "p1",
    name: "lamp",
    directory: "/tmp/lamp",
    git: true,
    syncs: [
      projectSync("feat/x", 3, 1),
      projectSync("feat/clean", 0, 0, { wishTitle: "Gong" }),
      projectSync("djinn/T27-djinn-stays-fast", 2, 0, {
        azimaId: "a27",
        azimaCode: "T27",
        azimaTitle: "Djinn stays fast",
      }),
    ],
  };
  const sidebar = (extra) =>
    s.renderToStaticMarkup(
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
        ...extra,
      }),
    );
  const html = sidebar();
  // The branch first, then its counts with their symbols; never the long sentence nor origin/ in sight.
  assert.match(
    html,
    /<div class="sync-line" title="Integration branch of the wish Lamp\n3 commits ahead of origin\/feat\/x, 1 behind"><span class="sync-branch">feat\/x<\/span><span class="sync-counts"><span class="sync-ahead">↑3<\/span><span class="sync-behind">↓1<\/span><\/span><button type="button" class="icon-button sync-push" title="Push feat\/x" aria-label="Push feat\/x">/,
  );
  assert.match(
    html,
    /<div class="sync-line" title="Branch of the azima T27 Djinn stays fast, wish Lamp\n2 commits ahead of origin\/djinn\/T27-djinn-stays-fast"><span class="sync-branch">djinn\/T27-djinn-stays-fast<\/span><span class="sync-counts"><span class="sync-ahead">↑2<\/span><\/span>/,
  );
  // In sync, a branch does not show.
  assert.doesNotMatch(html, /feat\/clean/);
  assert.equal(html.match(/class="sync-line"/g).length, 2);
  assert.doesNotMatch(html, /sync-fold/);
  assert.doesNotMatch(html, /button accent/);
  assert.doesNotMatch(html, />[^<]*origin\/[^<]*</);

  // Collapsed, or nothing out of sync: no line.
  assert.doesNotMatch(sidebar({ collapsed: true }), /sync-line/);
  assert.doesNotMatch(
    s.renderToStaticMarkup(
      h(s.WishSidebar, {
        wishes: [],
        projects: [{ ...project, syncs: [projectSync("feat/x", 0, 0)] }],
        selectedWishId: "",
        selectedProjectId: "",
        collapsed: false,
        onSelectWish() {},
        onSelectProject() {},
        onMove() {},
        onNewProject() {},
      }),
    ),
    /sync-lines/,
  );

  // Its button pushes that branch of that project.
  const pushed = [];
  const lines = live(s.WishSidebar, {
    wishes: [],
    projects: [project],
    selectedWishId: "",
    selectedProjectId: "",
    collapsed: false,
    onSelectWish() {},
    onSelectProject() {},
    onMove() {},
    onNewProject() {},
    onPush: (id, sync) => pushed.push(`${id} ${sync.wishId} ${sync.branch}`),
  }).one(s.SyncLines);
  lines.onPush(project.syncs[2]);
  assert.deepEqual(pushed, ["p1 w1 djinn/T27-djinn-stays-fast"]);
});

test("more than three branches out of sync fold behind the first two; while one pushes, every button waits and says why", async () => {
  const syncs = [
    projectSync("feat/a", 1, 0),
    projectSync("feat/b", 0, 2),
    projectSync("feat/in-sync", 0, 0),
    projectSync("feat/c", 4, 1),
    projectSync("feat/d", 5, 0),
  ];
  let release;
  const pushed = [];
  const lines = live(s.SyncLines, {
    syncs,
    onPush: (sync) => {
      pushed.push(sync.branch);
      return new Promise((resolve) => {
        release = resolve;
      });
    },
  });
  const branches = () => lines.all(s.SyncLine).map((x) => x.props.sync.branch);
  assert.deepEqual(branches(), ["feat/a", "feat/b"]);
  const fold = lines.one("sync-fold");
  assert.equal(fold["aria-expanded"], false);
  assert.match(lines.html(), /<span>2 more branches<\/span>/);
  fold.onClick({ stopPropagation() {} });
  assert.deepEqual(branches(), ["feat/a", "feat/b", "feat/c", "feat/d"]);
  assert.match(lines.html(), /<span>Fewer branches<\/span>/);

  // A push runs: its own button says so, the others that one runs; none pushes again.
  lines.all(s.SyncLine)[2].props.onPush();
  assert.deepEqual(pushed, ["feat/c"]);
  const busy = lines.all(s.SyncLine).map((x) => x.props.busy);
  assert.deepEqual(busy, [
    "A push is running: wait for it to end",
    "A push is running: wait for it to end",
    "Pushing feat/c…",
    "A push is running: wait for it to end",
  ]);
  lines.all(s.SyncLine)[0].props.onPush();
  assert.deepEqual(pushed, ["feat/c"]);
  release();
  await lines.settle();
  assert.deepEqual(
    lines.all(s.SyncLine).map((x) => x.props.busy),
    ["", "", "", ""],
  );

  // The line itself: its button disabled with the reason as its title.
  const waiting = s.renderToStaticMarkup(
    h(s.SyncLine, {
      sync: syncs[3],
      busy: "A push is running: wait for it to end",
      onPush() {},
    }),
  );
  assert.match(
    waiting,
    /<button type="button" class="icon-button sync-push" disabled="" title="A push is running: wait for it to end" aria-label="A push is running: wait for it to end">/,
  );
  // Three lines never fold.
  assert.doesNotMatch(
    s.renderToStaticMarkup(h(s.SyncLines, { syncs: syncs.slice(0, 4) })),
    /sync-fold/,
  );
});

// W489: a push shows on its sync line until it ends, as the server records it (ProjectSync.push), whoever started it.
const pushRun = (stage, extra = {}) => ({
  branch: "feat/x",
  stage,
  check: "",
  note: "",
  startTime: { seconds: 1760000000n, nanos: 0 },
  ...extra,
});

test("a push shows on its sync line: a spinner while its checks and git push run, a check mark once pushed, a cross with why when held", () => {
  const now = 1760000100000;
  const line = (sync, extra = {}) =>
    s.renderToStaticMarkup(h(s.SyncLine, { sync, now, onPush() {}, ...extra }));
  // Its checks run: a spinner in the button, disabled, the check on hover.
  const checking = line(
    projectSync("feat/x", 3, 0, {
      push: pushRun(s.PushStage.CHECKING, { check: "test" }),
    }),
  );
  assert.match(
    checking,
    /<button type="button" class="icon-button sync-push" disabled="" title="Push checks running: test" aria-label="Push checks running: test"><svg[^>]*sync-spinner/,
  );
  assert.match(checking, /↑3/);
  // git push runs.
  assert.match(
    line(projectSync("feat/x", 3, 0, { push: pushRun(s.PushStage.PUSHING) })),
    /disabled="" title="Pushing feat\/x to origin…"/,
  );
  // Pushed a moment ago: a check mark, the branch in sync, no button to push nothing.
  const pushedSync = projectSync("feat/x", 0, 0, {
    push: pushRun(s.PushStage.PUSHED, {
      endTime: { seconds: 1760000098n, nanos: 0 },
    }),
  });
  const pushed = line(pushedSync);
  assert.match(
    pushed,
    /<span class="sync-status pushed" role="img" title="Pushed to origin\/feat\/x" aria-label="Pushed to origin\/feat\/x">/,
  );
  assert.doesNotMatch(pushed, /sync-push|sync-spinner/);
  // The list shows it a few seconds, then lets it go.
  assert.match(
    s.renderToStaticMarkup(h(s.SyncLines, { syncs: [pushedSync], now })),
    /sync-status pushed/,
  );
  assert.equal(
    s.renderToStaticMarkup(
      h(s.SyncLines, { syncs: [pushedSync], now: now + 10000 }),
    ),
    "",
  );
  // Held: a cross, the check that failed and its first lines, the correction Djinn started; the button pushes again.
  const held = line(
    projectSync("feat/x", 3, 0, {
      push: pushRun(s.PushStage.HELD, {
        endTime: { seconds: 1760000090n, nanos: 0 },
        note: "test: test exited 1\n--- FAIL: TestLogin",
      }),
      correction: "W12",
    }),
  );
  assert.match(
    held,
    /<span class="sync-status held" role="img" title="Push held: its push checks are red\n?test: test exited 1\n?--- FAIL: TestLogin\n?Djinn started W12 to correct them"/,
  );
  assert.match(
    held,
    /<button type="button" class="icon-button sync-push" title="Push feat\/x"/,
  );
  // Refused: a cross with git's words.
  assert.match(
    line(
      projectSync("feat/x", 3, 0, {
        push: pushRun(s.PushStage.FAILED, {
          endTime: { seconds: 1760000090n, nanos: 0 },
          note: "origin refused the push: its branch has commits that this one does not",
        }),
      }),
    ),
    /class="sync-status failed" role="img" title="The push failed\n?origin refused the push: its branch has commits that this one does not"/,
  );
  // Behind only: nothing to push, no button.
  assert.doesNotMatch(line(projectSync("feat/x", 0, 2)), /sync-push/);
});

test("a push running in Djinn makes every button of the list wait, as a reload shows it", () => {
  const lines = live(s.SyncLines, {
    syncs: [
      projectSync("feat/a", 2, 0),
      projectSync("feat/x", 3, 0, {
        push: pushRun(s.PushStage.CHECKING, { check: "test" }),
      }),
    ],
    onPush() {},
  });
  assert.deepEqual(
    lines.all(s.SyncLine).map((x) => x.props.busy),
    ["A push is running: wait for it to end", ""],
  );
});

test("regression W489: a stale ↑3 pushed again says 'up to date' quietly on its line, and an error stays on its line", async () => {
  const pushes = [];
  let answer = { upToDate: true };
  const lines = live(s.SyncLines, {
    syncs: [projectSync("feat/wails-go", 3, 0)],
    onPush: (sync) => {
      pushes.push(sync.branch);
      return answer instanceof Error
        ? Promise.reject(answer)
        : Promise.resolve(answer);
    },
  });
  lines.one(s.SyncLine).onPush();
  assert.equal(lines.one(s.SyncLine).pushing, true);
  await lines.settle();
  const quiet = lines.one(s.SyncLine);
  assert.equal(quiet.pushing, false);
  assert.equal(quiet.outcome.upToDate, true);
  const html = s.renderToStaticMarkup(h(s.SyncLine, quiet));
  assert.match(
    html,
    /<span class="sync-note" title="Nothing to push: origin\/feat\/wails-go holds this branch already">up to date<\/span>/,
  );
  assert.doesNotMatch(html, /sync-status/);

  // A failure the server does not explain: a cross on the line, the error on hover; no throw for a toast.
  answer = new Error("repository /tmp/app has no remote");
  lines.one(s.SyncLine).onPush();
  await lines.settle();
  assert.deepEqual(pushes, ["feat/wails-go", "feat/wails-go"]);
  assert.match(
    s.renderToStaticMarkup(h(s.SyncLine, lines.one(s.SyncLine))),
    /class="sync-status failed" role="img" title="The push failed\n?repository \/tmp\/app has no remote"/,
  );
});

test("the project panel displays the push cadence setting, out-of-sync status, and push button", () => {
  const p1 = {
    id: "p1",
    name: "app",
    git: true,
    directory: "/tmp/app",
    remote: "git@github.com:org/app.git",
    push: s.ProjectPush.STANDARD,
    syncs: [projectSync("feat/x", 3, 0), projectSync("feat/y", 0, 0)],
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
  // The same short line as the side panel's: the branch, its counts, a small grey button.
  assert.match(
    html,
    /<div class="sync-line" title="Integration branch of the wish Lamp\n3 commits ahead of origin\/feat\/x"><span class="sync-branch">feat\/x<\/span><span class="sync-counts"><span class="sync-ahead">↑3<\/span><\/span><button type="button" class="icon-button sync-push" title="Push feat\/x"/,
  );
  assert.doesNotMatch(html, /feat\/y|button accent/);
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
    syncs: [],
  };
  const htmlOnDemand = s.renderToStaticMarkup(
    h(
      s.DjinnProvider,
      { djinn },
      h(s.ProjectPanel, { project: p2, onClose() {} }),
    ),
  );
  assert.match(htmlOnDemand, /Pushes only when you ask\./);
  assert.doesNotMatch(htmlOnDemand, /sync-line/);
  assert.match(
    htmlOnDemand,
    /<button type="button" class="">Standard<\/button><button type="button" class="active">On demand<\/button>/,
  );
});

// The watcher commands a developer allows in a project, in its panel: each with a button that takes it back, and a
// field that allows one more, kept in their own settings.
test("a project's panel lists the watcher commands you allow", () => {
  const html = s.renderToStaticMarkup(
    h(s.WatchCommands, {
      commands: ["go run ./.agents/skills/babysit-mr-empowill/mrwatch"],
      onAllow: async () => true,
      onRevoke() {},
    }),
  );
  assert.match(
    html,
    /<div class="setting-row"><div><code>go run \.\/\.agents\/skills\/babysit-mr-empowill\/mrwatch<\/code><\/div><button type="button" class="button secondary">Take back<\/button><\/div>/,
  );
  assert.match(html, /never in the repository/);
  assert.match(
    html,
    /<form class="folder-field"><input aria-label="Watcher command to allow"[^>]*\/><button type="submit" class="button secondary" disabled="">Allow<\/button><\/form>/,
  );
  const none = s.renderToStaticMarkup(
    h(s.WatchCommands, {
      commands: [],
      onAllow: async () => true,
      onRevoke() {},
    }),
  );
  assert.doesNotMatch(none, /Take back/);
});

// An opened azima, as the Tasks tab shows it: built from what the lamp sends, its parts as the tab renders any task.
function openedAzima(extra = {}, parts = []) {
  const at = (seconds) => ({ seconds: BigInt(seconds), nanos: 0 });
  const azima = {
    id: "T05",
    code: "T05",
    title: "Split the orchestrator",
    kind: s.TaskKind.AZIMA,
    status: s.TaskStatus.PENDING,
    dependsOn: [],
    createTime: at(1),
    azima: { state: s.AzimaState.OPEN, ready: true },
    ...extra,
  };
  const tasks = new Map([azima, ...parts].map((x) => [x.id, x]));
  return s.renderToStaticMarkup(
    h(s.AzimaCard, {
      azima,
      parts,
      tasks,
      focus: azima.id,
      render: (task) =>
        h("article", { key: task.id, className: "part" }, task.code),
    }),
  );
}

test("an azima's description is Markdown, rendered: opened, and as a draft", () => {
  const text = "Split the **orchestrator**:\n\n- the scheduler\n- the `runner`";
  const markdown = (html) => {
    assert.match(html, /<div class="ac-markdown">/);
    assert.match(html, /<strong>orchestrator<\/strong>/);
    assert.match(html, /<li>the scheduler<\/li>/);
    assert.match(html, /<code>runner<\/code>/);
    assert.doesNotMatch(html, /\*\*/);
  };
  markdown(openedAzima({ description: text }));
  markdown(
    s.renderToStaticMarkup(
      h(s.DraftAzimaCard, {
        azima: {
          id: "T31",
          code: "T31",
          title: "Spread the work",
          kind: s.TaskKind.AZIMA,
          status: s.TaskStatus.PENDING,
          draft: true,
          description: text,
          dependsOn: [],
          azima: { state: s.AzimaState.DRAFT },
        },
        focus: "T31",
      }),
    ),
  );
});

test("an azima's title renames in place from its card, open or draft: a pencil, then a field in its heading", () => {
  const azima = {
    id: "T36",
    code: "T36",
    title: "feat/x reaches main",
    kind: s.TaskKind.AZIMA,
    status: s.TaskStatus.PENDING,
    dependsOn: [],
    azima: { state: s.AzimaState.OPEN, ready: true },
  };
  const draft = {
    ...azima,
    draft: true,
    azima: { state: s.AzimaState.DRAFT },
  };
  const card = (Card, props) =>
    s.renderToStaticMarkup(
      h(Card, { tasks: new Map(), parts: [], render: () => null, ...props }),
    );
  for (const [Card, a] of [
    [s.AzimaCard, azima],
    [s.DraftAzimaCard, draft],
  ]) {
    // Without a way to save it, no pencil.
    assert.doesNotMatch(card(Card, { azima: a }), /azima-rename/);
    const shown = card(Card, { azima: a, onRename() {} });
    assert.match(
      shown,
      /<button type="button" class="icon-button azima-rename" title="Rename this azima" aria-label="Rename this azima">/,
    );
    assert.match(shown, /<strong[^>]*>feat\/x reaches main<\/strong>/);
    const editing = card(Card, { azima: a, onRename() {}, renaming: true });
    assert.match(
      editing,
      /<span class="agent-code">T36<\/span><input type="text" class="azima-title-edit" aria-label="Title of the azima" maxLength="500" autofocus="" value="feat\/x reaches main"\/>/,
    );
    // The field takes the heading's place, and the pencil's.
    assert.doesNotMatch(editing, /azima-rename|aria-expanded/);
  }
});

test("an opened azima: its description, then its proof, on the left; its parts on the right, one per row", () => {
  const at = (seconds) => ({ seconds: BigInt(seconds), nanos: 0 });
  const part = (code, status) => ({
    id: code,
    code,
    title: `Part ${code}`,
    status,
    dependsOn: [],
    createTime: at(1),
  });
  const parts = [
    part("W1", s.TaskStatus.RUNNING),
    part("W2", s.TaskStatus.DONE),
  ];
  const described = openedAzima({ description: "Split it in **two**." }, parts);
  // Left, the description; right, what moves, one per row, the finished folded under it.
  assert.match(
    described,
    /<div class="azima-body with-side"><div class="azima-side"><div class="azima-description"><div class="ac-markdown">[\s\S]*?<\/div><\/div><\/div><div class="azima-parts"><div class="azima-part-list"><article class="part">W1<\/article><\/div><button[^>]*class="fold-line"/,
  );
  assert.doesNotMatch(described, /card-grid|task-grid/);
  // Awaiting its proof: what it needs, under the description.
  const proving = openedAzima(
    {
      description: "Split it in **two**.",
      azima: { state: s.AzimaState.AWAITING_PROOF, ready: true },
      proofNeeds: [
        { box: "Runs on macOS", needs: "a Mac", provers: [s.Prover.MAC] },
      ],
    },
    parts,
  );
  assert.match(
    proving,
    /<div class="azima-side"><div class="azima-description">[\s\S]*?<div class="azima-proof">[\s\S]*?Runs on macOS[\s\S]*?<\/div><\/div><div class="azima-parts">/,
  );
  // With no description, its parts take the whole width.
  const bare = openedAzima({}, parts);
  assert.match(bare, /<div class="azima-body"><div class="azima-parts">/);
  assert.doesNotMatch(bare, /azima-side/);
});

test("draft azimas: fold, card with description, actions, and flying carpet", () => {
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

  // 1. Flying carpet: drafts are collected in drafts, NOT in moving, waiting, or azimas.
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

  // 3. Flying carpet azimas are sorted by azimaRank, then compareCodes
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

  // Progress: segmented bar and its counts (1/3); the parts by state and the time only on the bar's hover, never
  // said twice in the heading.
  assert.match(t2CardHtml, /<span class="azima-bar-done" style="width:33\.33/);
  assert.match(
    t2CardHtml,
    /<span class="azima-bar-running" style="width:33\.33/,
  );
  assert.match(
    t2CardHtml,
    /<span class="azima-progress" title="1 of 3 parts done\n1 done, 1 running, 1 waiting\n[^"]+ · Running since [^"]+">/i,
  );
  assert.match(t2CardHtml, /<span class="azima-progress-counts">1\/3<\/span>/);
  const t2Heading = t2CardHtml.match(
    /<button[^>]*class="azima-heading"[^>]*>[\s\S]*?<\/button>/,
  )[0];
  assert.doesNotMatch(t2Heading, /azima-progress-parts|task-time/);
  assert.doesNotMatch(t2Heading, />1 done, 1 running, 1 waiting</);

  // Parts one per row in .azima-parts, the whole width when the azima has no description.
  assert.match(
    t2CardHtml,
    /<div class="azima-body"><div class="azima-parts"><div class="azima-part-list">/,
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

test("the wish's head says where its work lands, and a click changes the branch", async () => {
  const projects = [{ id: "p1", name: "lamp", git: true }];
  const branches = [{ projectId: "p1", branch: "feat/wails-go" }];
  const noLoad = () => Promise.resolve({ branches: [], checkedOut: "" });

  // Per wish, one project: a chip with its integration branch, the project not named.
  const one = s.renderToStaticMarkup(
    h(s.WhereWorkLands, {
      branches,
      projects,
      perAzima: false,
      loadBranches: noLoad,
      onChange: () => Promise.resolve(),
    }),
  );
  assert.match(
    one,
    /<button type="button" class="wish-lands" aria-haspopup="dialog" aria-expanded="false" title="Djinn merges the wish&#x27;s finished work into feat\/wails-go, then pushes it\. Click to change the branch\.">/,
  );
  assert.match(one, /<span>Lands in <code>feat\/wails-go<\/code><\/span>/);
  assert.doesNotMatch(one, /<b>lamp<\/b>/);

  // Several projects: one chip each, the project named.
  const two = s.renderToStaticMarkup(
    h(s.WhereWorkLands, {
      branches: [...branches, { projectId: "p2", branch: "main" }],
      projects: [...projects, { id: "p2", name: "wick", git: true }],
      perAzima: false,
      loadBranches: noLoad,
      onChange: () => Promise.resolve(),
    }),
  );
  assert.match(two, /<b>lamp<\/b><span>Lands in <code>feat\/wails-go<\/code>/);
  assert.match(two, /<b>wick<\/b><span>Lands in <code>main<\/code>/);

  // Per azima: one chip that says so, not a button; the wish's branch for work of no azima on hover.
  const perAzima = s.renderToStaticMarkup(
    h(s.WhereWorkLands, {
      branches,
      projects,
      perAzima: true,
      loadBranches: noLoad,
      onChange: () => Promise.resolve(),
    }),
  );
  assert.match(
    perAzima,
    /<span class="wish-lands" title="Per azima: each azima has its own branch, djinn\/&lt;code&gt;-&lt;slug&gt;, cut from main, and its pull request lands it in main\.\nWork of no azima lands in feat\/wails-go\.">.*Each azima lands in main<\/span>/,
  );
  assert.doesNotMatch(perAzima, /<button/);

  // A click on the chip opens the picker of its project.
  const chip = live(s.WhereWorkLands, {
    branches,
    projects,
    perAzima: false,
    loadBranches: noLoad,
    onChange: () => Promise.resolve(),
  });
  assert.equal(chip.all(s.BranchPicker).length, 0);
  chip.one("wish-lands").onClick();
  assert.equal(chip.all(s.BranchPicker).length, 1);
  assert.equal(chip.one("wish-lands")["aria-expanded"], true);

  // The picker: the branches, the current one marked; a click on another sets it; a name none has makes it from the
  // current one; what is typed narrows the list.
  const picked = [];
  const picker = live(s.BranchPicker, {
    current: "feat/wails-go",
    branches: ["feat/wails-go", "main", "release/v1"],
    load: noLoad,
    onClose() {},
    onPick: (branch, create) => {
      picked.push([branch, create]);
      return Promise.resolve();
    },
  });
  const html = picker.html();
  assert.match(
    html,
    /role="dialog" aria-label="Where the wish&#x27;s work lands"/,
  );
  assert.match(
    html,
    /<button type="button" class="branch-option current" aria-current="true"><code>feat\/wails-go<\/code>/,
  );
  const option = (name) =>
    picker
      .all("branch-option")
      .find((b) =>
        [b.props.children].flat().some((c) => c?.props?.children === name),
      );
  option("main").props.onClick();
  await picker.settle();
  assert.deepEqual(picked, [["main", false]]);
  picker.one("input").onChange({ currentTarget: { value: "feat/lamp" } });
  assert.doesNotMatch(picker.html(), /<code>main<\/code>/);
  assert.match(
    picker.html(),
    /class="branch-option new"[^>]*>.*<span>New branch feat\/lamp from feat\/wails-go<\/span>/,
  );
  picker.all("new")[0].props.onClick();
  await picker.settle();
  assert.deepEqual(picked.at(-1), ["feat/lamp", true]);
  // An existing name typed whole: no new branch offered; Enter picks it.
  picker.one("input").onChange({ currentTarget: { value: "release/v1" } });
  assert.doesNotMatch(picker.html(), /branch-option new/);
  picker.one("input").onKeyDown({ key: "Enter", preventDefault() {} });
  await picker.settle();
  assert.deepEqual(picked.at(-1), ["release/v1", false]);
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
  assert.doesNotMatch(defaultCardHtml, /T27-djinn-stays-fast/);
  assert.doesNotMatch(defaultCardHtml, /azima-actions/);

  // 5. AzimaCard in per-azima mode with branch, the main it lands in, sync, push button, and PR
  let pushedAzima = null;
  let openedPrUrl = null;
  const azimaPerAzima = {
    ...taskT27,
    azima: {
      branch: "djinn/T27-djinn-stays-fast",
      baseBranch: "main",
      cutFrom: "0123456789abcdef0123456789abcdef01234567",
      sync: {
        ahead: 2,
        behind: 0,
        branch: "djinn/T27-djinn-stays-fast",
        remote: "origin",
        target: "djinn/T27-djinn-stays-fast",
      },
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

  // Closed, its heading says nothing of its branch, neither shown nor on hover: only its line out of sync names it.
  assert.match(perAzimaHtml, /<strong title="Djinn stays fast">/);
  assert.doesNotMatch(perAzimaHtml, /azima-landing/);
  assert.doesNotMatch(
    perAzimaHtml.match(
      /<button type="button" class="azima-heading".*?<\/button>/,
    )[0],
    /T27-djinn-stays-fast/,
  );

  // Opened, its branch, the commit of main it was cut from, and that it lands in main, across the card.
  const openedHtml = s.renderToStaticMarkup(
    h(s.AzimaCard, {
      azima: azimaPerAzima,
      parts: [],
      tasks: new Map(),
      render: () => null,
      isPerAzima: true,
      focus: "a27",
    }),
  );
  assert.match(
    openedHtml,
    /<div class="azima-body"><p class="azima-landing">.*?<code>djinn\/T27-djinn-stays-fast<\/code><span>cut from main at 01234567<\/span><span>lands in main<\/span><\/p>/,
  );
  // Not cut yet: it will be when its first part starts. Never stacked on another azima's branch.
  const notCutHtml = s.renderToStaticMarkup(
    h(s.AzimaCard, {
      azima: {
        ...taskT27,
        azima: {
          state: s.AzimaState.OPEN,
          branch: "djinn/T27-djinn-stays-fast",
        },
      },
      parts: [],
      tasks: new Map(),
      render: () => null,
      isPerAzima: true,
      focus: "a27",
    }),
  );
  assert.match(
    notCutHtml,
    /<span>cut from main when its first part starts<\/span><span>lands in main<\/span>/,
  );
  // Per wish, no branch of its own: no line.
  assert.doesNotMatch(
    s.renderToStaticMarkup(
      h(s.AzimaCard, {
        azima: azimaPerAzima,
        parts: [],
        tasks: new Map(),
        render: () => null,
        focus: "a27",
      }),
    ),
    /azima-landing/,
  );

  // Sync state and push button: the short line of the side panel.
  assert.match(
    perAzimaHtml,
    /<div class="sync-line" title="2 commits ahead of origin\/djinn\/T27-djinn-stays-fast"><span class="sync-branch">djinn\/T27-djinn-stays-fast<\/span><span class="sync-counts"><span class="sync-ahead">↑2<\/span><\/span><button type="button" class="icon-button sync-push" title="Push djinn\/T27-djinn-stays-fast"/,
  );
  assert.doesNotMatch(perAzimaHtml, /button accent/);

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
  const fakeEvent = { stopPropagation() {}, preventDefault() {} };
  liveAzimaCard.one(s.SyncLine).onPush();
  assert.equal(pushedAzima?.id, "a27");
  assert.equal(
    liveAzimaCard.one(s.SyncLine).busy,
    "Pushing djinn/T27-djinn-stays-fast…",
  );

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
  assert.doesNotMatch(cleanAndProposedHtml, /sync-line/);

  // Its project's syncs tell how its branch stands, over what the task recorded.
  const fromProjectHtml = s.renderToStaticMarkup(
    h(s.AzimaCard, {
      azima: { ...taskT27, azima: { branch: "djinn/T27-djinn-stays-fast" } },
      sync: projectSync("djinn/T27-djinn-stays-fast", 1, 3, {
        azimaId: "a27",
        azimaCode: "T27",
        azimaTitle: "Djinn stays fast",
      }),
      parts: [],
      tasks: new Map(),
      render: () => null,
      isPerAzima: true,
      onPush() {},
    }),
  );
  assert.match(
    fromProjectHtml,
    /<span class="sync-branch">djinn\/T27-djinn-stays-fast<\/span><span class="sync-counts"><span class="sync-ahead">↑1<\/span><span class="sync-behind">↓3<\/span><\/span>/,
  );

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

// The resources panel's fixture: the machine over 30 minutes, two workers (W1's title long), gates held, and the
// commands of two projects, djinn's in an active wish, another's in none.
const t0 = 1700000000n;
const t1 = 1700000400n; // asked -> granted (400s wait)
const t2 = 1700001200n; // granted -> released (800s held)
const t3 = 1700001800n; // end of the 30 min window
const at = (seconds) => ({ seconds, nanos: 0 });
const mib = 1024n * 1024n;
const longTitle =
  "Make the flying carpet's resources read at a glance: one title line per worker with its code, its agent and this whole title, which wraps rather than being cut, then the facts of its job beside larger charts";
const lintGate = {
  gate: "lint",
  command: "go tool task lint",
  project: "djinn",
  askedTime: at(t0),
  grantTime: at(t1),
  releaseTime: at(t2),
};
const resourceData = () => ({
  machine: {
    cores: 8,
    memoryTotalBytes: 16n * 1024n * mib,
    loadLimitShare: 0.8,
    loadLimitMemoryBytes: 12n * 1024n * mib,
    points: [
      {
        time: at(t0),
        cpuPercent: 45,
        djinnCpuPercent: 4,
        memoryBytes: 4n * 1024n * mib,
        djinnMemoryBytes: 1500n * mib,
        selfCpuPercent: 0.5,
        selfMemoryBytes: 140n * mib,
      },
      {
        time: at(t3),
        cpuPercent: 62,
        djinnCpuPercent: 5.2,
        memoryBytes: 5n * 1024n * mib,
        djinnMemoryBytes: 1800n * mib,
        selfCpuPercent: 0.7,
        selfMemoryBytes: 150n * mib,
      },
    ],
  },
  gates: [
    lintGate,
    { ...lintGate, gate: "test", command: "npm test", project: "old" },
  ],
  workers: [
    {
      taskId: "task-w1",
      wishId: "wish-1",
      code: "W1",
      title: longTitle,
      provider: s.Provider.CLAUDE,
      model: "claude-opus-5-5",
      peakCpuPercent: 85,
      peakMemoryBytes: 64n * mib,
      paused: false,
      points: [
        { time: at(t0), cpuPercent: 30, memoryBytes: 32n * mib },
        { time: at(t3), cpuPercent: 75.4, memoryBytes: 60n * mib },
      ],
      gates: [lintGate],
    },
    {
      taskId: "task-w2",
      wishId: "wish-1",
      code: "W2",
      title: "Light the wick and test",
      provider: s.Provider.CODEX,
      model: "o3-mini",
      peakCpuPercent: 140,
      peakMemoryBytes: 128n * mib,
      paused: true,
      points: [
        { time: at(t0), cpuPercent: 90, memoryBytes: 80n * mib },
        { time: at(t3), cpuPercent: 135.2, memoryBytes: 120n * mib },
      ],
      gates: [],
    },
  ],
  workerMeasure: "",
});
const resourceCost = (projectId, command, extra) => ({
  projectId,
  projectName: projectId === "p1" ? "djinn" : "old-project",
  command,
  runs: 0,
  asked: 0,
  seconds: 0,
  gate: "",
  p50WaitSeconds: 0,
  p50HeldSeconds: 0,
  recentRuns: [],
  ...extra,
});
const resourceCommands = [
  resourceCost("p1", "go tool task e2e -- e2e/wish-render.spec.ts", {
    runs: 3,
    asked: 4,
    seconds: 90,
    gate: "e2e",
    recentRuns: [{ waitSeconds: 20, heldSeconds: 91, gate: "e2e" }],
  }),
  resourceCost("p1", "go tool task e2e -- e2e/flight-plan.spec.ts", {
    runs: 1,
    asked: 1,
    seconds: 60,
    gate: "e2e",
    recentRuns: [{ waitSeconds: 0, heldSeconds: 61, gate: "e2e" }],
  }),
  resourceCost("p1", "go tool task e2e", {
    runs: 2,
    asked: 2,
    seconds: 900,
    gate: "e2e",
    recentRuns: [{ waitSeconds: 60, heldSeconds: 901, gate: "e2e" }],
  }),
  resourceCost("p1", "go tool task lint", {
    runs: 15,
    asked: 15,
    seconds: 40,
    gate: "lint",
    recentRuns: [{ waitSeconds: 2, heldSeconds: 41, gate: "lint" }],
  }),
  resourceCost("p2", "go tool task test-old-suite", {
    runs: 40,
    asked: 40,
    seconds: 600,
    gate: "test",
  }),
];
const resourceTask = {
  id: "task-w1",
  wishId: "wish-1",
  projectId: "p1",
  code: "W1",
  title: longTitle,
  status: s.TaskStatus.RUNNING,
  provider: s.Provider.CLAUDE,
  model: "claude-opus-5-5",
  startTime: at(t0),
  firstStartTime: at(t0),
  runs: [{ startTime: at(t0) }],
  turns: { run: 37, limit: 500, total: 412 },
  usage: { inputTokens: 10n, outputTokens: 2000n, costUsd: 1.25 },
};

test("the resources panel: the machine's charts, each worker's title line in full with its facts and charts, the commands of the active wishes' projects grouped under their gates", () => {
  const html = s.renderToStaticMarkup(
    h(s.ConsumptionPanel, {
      initialData: resourceData(),
      initialCommands: resourceCommands,
      tasks: new Map([["task-w1", resourceTask]]),
      projects: [
        { id: "p1", name: "djinn" },
        { id: "p2", name: "old-project" },
      ],
      projectIds: new Set(["p1"]),
    }),
  );

  // The header: the ranges, up to a week, the one shown pressed.
  assert.match(html, /class="consumption-panel-heading">Resource consumption</);
  assert.match(html, /aria-pressed="true">30 min<\/button>/);
  for (const range of ["10 min", "2 h", "24 h", "7 days"])
    assert.match(html, new RegExp(`aria-pressed="false">${range}</button>`));

  // The machine: CPU out of 100% and memory against the machine's, both labelled, the load limit too.
  assert.match(html, /8 cores · 16 GB/);
  assert.match(html, /now 62% · peak 62%/);
  assert.match(html, /5 GB of 16 GB/);
  assert.match(html, /class="resource-chart-ref-label"[^>]*>max 16 GB</);
  assert.match(html, /class="resource-chart-ref-label"[^>]*>load limit 80%</);
  assert.match(html, /class="resource-chart-ref-label"[^>]*>load limit 12 GB</);
  assert.match(html, /class="resource-chart-tick"[^>]*>100%</);
  // Djinn apart: with everything it started, dashed, and its own process alone, dotted, both named.
  assert.match(
    html,
    /Dashed: Djinn with everything it started \(window, the Djinn&#x27;s agent, workers, gates\)/,
  );
  assert.match(html, /Dotted: Djinn&#x27;s own process alone \(djinn up\)/);
  assert.match(html, /<path class="resource-chart-extra dash" d="M/);
  assert.match(html, /<path class="resource-chart-extra dot" d="M/);
  // Times under each chart: a few ticks of the clock.
  const ticks = [
    ...html.matchAll(
      /class="resource-chart-tick"[^>]*>(\d\d?:\d\d( [AP]M)?)</g,
    ),
  ];
  assert.ok(
    ticks.length >= 3 * 6,
    `time ticks under the six charts: ${ticks.length}`,
  );
  // The gates held, the project of no active wish left out.
  assert.match(
    html,
    /aria-label="Gate lint \(djinn\): go tool task lint\. Waited 6m40s, held 13m20s\."/,
  );
  assert.doesNotMatch(html, /Gate test \(old\)/);

  // A worker: the title line holds its code, its agent and the whole title, never cut.
  const w1 = html.slice(
    html.indexOf('aria-label="W1 '),
    html.indexOf('aria-label="W2 '),
  );
  assert.match(
    w1,
    /<h4 class="resource-worker-title"><span class="agent-code">W1<\/span><span class="provider-badge provider-claude">claude<\/span><span class="resource-worker-name">Make the flying carpet&#x27;s resources read at a glance: [^<]*larger charts<\/span>/,
  );
  assert.doesNotMatch(w1, /…<\/span>/);
  // Its link to the task in its wish: a small eye on the title line.
  assert.match(
    w1,
    /<button class="icon-button resource-worker-open" type="button" aria-label="Open W1 in its wish"><svg/,
  );
  // The facts of its job, from its task.
  assert.match(w1, /<dt>Project<\/dt><dd>djinn<\/dd>/);
  assert.match(w1, /<dt>Turns<\/dt><dd><span class="task-turns"/);
  // Its turns over its whole life first, its run against its limit beside them.
  assert.match(w1, /<\/svg>412<span class="task-turns-run">37\/500<\/span>/);
  assert.match(w1, /<dt>Spent<\/dt>/);
  assert.match(w1, /<dt>Gate<\/dt><dd>held 13m20s, once<\/dd>/);
  assert.match(w1, /<dt>CPU<\/dt><dd>now 75\.4% · peak 85%<\/dd>/);
  // Its CPU and memory over time, larger, with the gate it held behind.
  assert.equal(
    (w1.match(/<figure class="resource-chart cpu">/g) ?? []).length,
    1,
  );
  assert.equal(
    (w1.match(/<figure class="resource-chart memory">/g) ?? []).length,
    1,
  );
  assert.match(w1, /<svg class="resource-chart-svg" width="480" height="190"/);
  assert.match(w1, /class="resource-chart-gate"/);
  // One scale for every worker: W1's CPU axis reaches W2's 140% peak, its memory axis W2's 128 MB.
  assert.match(html, /Every worker&#x27;s charts share one scale/);
  const w1Cpu = w1.slice(w1.indexOf('<figure class="resource-chart cpu">'));
  assert.match(w1Cpu, /class="resource-chart-tick"[^>]*>150%</);
  const w1Memory = w1.slice(
    w1.indexOf('<figure class="resource-chart memory">'),
  );
  assert.match(w1Memory, /class="resource-chart-tick"[^>]*>192 MB</);
  assert.match(
    w1,
    /role="group" aria-roledescription="chart" aria-label="CPU \(100% is one core\): now 75\.4% · peak 85%"/,
  );
  // A paused worker says so; without its task, the facts the series give.
  const w2 = html.slice(html.indexOf('aria-label="W2 '));
  assert.match(
    w2,
    /class="resource-worker is-paused"|resource-worker-paused">paused</,
  );
  assert.match(w2, /<dt>CPU<\/dt><dd>now 135\.2% · peak 140%<\/dd>/);

  // Where the time goes: djinn's commands, grouped under their gates, the most time first; the old project's not.
  assert.match(html, /Where the time goes: commands and their gates/);
  assert.match(html, /A gate is a named lock/);
  // One section per project of an active wish, saying first where most of its time goes.
  assert.match(
    html,
    /<section class="resource-project" aria-label="Project djinn"><h5 class="resource-project-name">Project djinn<span class="resource-section-detail">45m30s of commands<\/span><\/h5><p class="resource-project-top">Most of its time: go tool task e2e \(gate e2e\), 66% of it\.<\/p>/,
  );
  assert.doesNotMatch(html, /old-project|test-old-suite/);
  const table = html.slice(html.indexOf('class="resource-commands-table"'));
  const rows = [
    ...table.matchAll(
      /<tr class="resource-(gate|command)-row">([\s\S]*?)<\/tr>/g,
    ),
  ].map(
    ([, kind, row]) =>
      `${kind}: ${row
        .replace(/<[^>]+>/g, " ")
        .replace(/\s+/g, " ")
        .trim()}`,
  );
  assert.deepEqual(rows, [
    // 2 runs of the whole suite and 4 of some specs, 7 asked: 1800 s + 330 s.
    "gate: gate e2e 7 6 35m30s 5m55s 3m 30s 35m36s 5m56s",
    "command: go tool task e2e 2 2 30m 66% 15m 2m 1m 30m2s 15m1s",
    "command: go tool task e2e … 2 sets of arguments: -- e2e/wish-render.spec.ts, -- e2e/flight-plan.spec.ts 5 4 5m30s 12% 1m22s 1m 15s 5m34s 1m23s",
    "gate: gate lint 15 15 10m 40s 30s 2s 10m15s 41s",
    "command: go tool task lint 15 15 10m 22% 40s 30s 2s 10m15s 41s",
  ]);
  assert.match(
    html,
    /<th scope="col" rowSpan="2" class="num-col">Asked<\/th><th scope="col" rowSpan="2" class="num-col">Ran<\/th><th scope="colgroup" colSpan="2" class="resource-col-group">Time<\/th><th scope="colgroup" colSpan="2" class="resource-col-group">Waiting for the gate<\/th><th scope="colgroup" colSpan="2" class="resource-col-group">Holding the gate<\/th>/,
  );
  assert.equal(
    (html.match(/>total<\/th><th scope="col" class="num-col">mean</g) ?? [])
      .length,
    3,
  );
});

test("a chart says the value under the pointer and at the arrow keys, and keeps it as new samples come", () => {
  const points = [
    { t: 0, v: 10 },
    { t: 60_000, v: 95 },
    { t: 120_000, v: 20 },
  ];
  const chart = live(s.ResourceChart, {
    title: "CPU",
    unit: "cpu",
    summary: "now 20%",
    points,
    tMin: 0,
    tMax: 120_000,
    top: 100,
  });
  const plot = () => chart.one("resource-chart-plot");
  assert.equal(plot().tabIndex, 0);
  assert.equal(plot().role, "group");
  assert.doesNotMatch(chart.html(), /resource-chart-readout/);
  // The pointer at the middle of the plot (56 px of axis, 410 px of plot): the peak at one minute.
  const box = { getBoundingClientRect: () => ({ left: 0 }) };
  plot().onPointerMove({ clientX: 56 + 205, currentTarget: box });
  let html = chart.html();
  assert.match(html, /class="resource-chart-cursor"/);
  assert.match(html, /<strong>CPU 95%<\/strong>/);
  assert.match(html, /aria-live="polite">[^<]*CPU 95%</);
  // The keys walk the samples.
  const key = (k) => plot().onKeyDown({ key: k, preventDefault() {} });
  key("ArrowRight");
  assert.match(chart.html(), /<strong>CPU 20%<\/strong>/);
  key("Home");
  assert.match(chart.html(), /<strong>CPU 10%<\/strong>/);
  key("ArrowRight");
  assert.match(chart.html(), /<strong>CPU 95%<\/strong>/);
  // A new sample: the chart grows, the point shown stays the one chosen.
  chart.update({
    points: [...points, { t: 180_000, v: 55 }],
    tMax: 180_000,
    summary: "now 55%",
  });
  html = chart.html();
  assert.match(html, /<strong>CPU 95%<\/strong>/);
  assert.match(html, /aria-label="CPU: now 55%"/);
  key("End");
  assert.match(chart.html(), /<strong>CPU 55%<\/strong>/);
  // The pointer leaves: the readout goes, the last sample marked.
  plot().onPointerLeave();
  html = chart.html();
  assert.doesNotMatch(html, /resource-chart-readout/);
  assert.match(html, /<circle class="resource-chart-dot"/);

  // Memory says GB and its share of the machine.
  const memory = live(s.ResourceChart, {
    title: "Memory",
    unit: "memory",
    summary: "",
    points: [{ t: 0, v: 4 * 1024 ** 3 }],
    tMin: 0,
    tMax: 60_000,
    top: 16 * 1024 ** 3,
    memoryTotal: 16 * 1024 ** 3,
  });
  memory
    .one("resource-chart-plot")
    .onKeyDown({ key: "End", preventDefault() {} });
  assert.match(
    memory.html(),
    /<strong>Memory 4 GB · 25% of the machine<\/strong>/,
  );
});

test("a worker's eye opens its task in its wish, through the window's focus", () => {
  const opened = [];
  const row = live(s.WorkerRow, {
    worker: resourceData().workers[0],
    memoryTotal: 0,
    tMin: 0,
    tMax: 1,
    onOpen: (wishId, taskId) => opened.push([wishId, taskId]),
  });
  const eye = row.one("resource-worker-open");
  assert.equal(eye.label, "Open W1 in its wish");
  eye.onClick();
  assert.deepEqual(opened, [["wish-1", "task-w1"]]);
  // The window's focus takes it to the Tasks tab, where the task's card shows.
  assert.equal(s.tabOf("task-task-w1"), "tasks");
});

test("the side panel's Hourglass sits at the bottom of the menu, after the wishes, projects and import, and stays with no active wish", (t) => {
  globalThis.__DJINN_MAC__ = false;
  t.after(() => delete globalThis.__DJINN_MAC__);
  const sidebar = (wishes, selected) =>
    s.renderToStaticMarkup(
      h(s.WishSidebar, {
        wishes,
        projects: [{ id: "p1", name: "lamp", directory: "/tmp/lamp" }],
        onSelectPlan() {},
        hourglassSelected: selected,
        onSelectHourglass() {},
        selectedWishId: "",
        selectedProjectId: "",
        collapsed: false,
        onSelectWish() {},
        onSelectProject() {},
        onMove() {},
        onNewProject() {},
        onImportWish() {},
      }),
    );
  const html = sidebar(
    [wish("w1", "Ship the lamp", s.WishState.ACTIVE, 1)],
    true,
  );
  const carpet = html.indexOf("plan-nav");
  const wishes = html.indexOf("Ship the lamp");
  const projects = html.indexOf("project-list");
  const importBtn = html.indexOf("import-nav");
  const glass = html.indexOf("hourglass-nav");
  assert.ok(
    carpet >= 0 &&
      wishes > carpet &&
      projects > wishes &&
      importBtn > projects &&
      glass > importBtn,
    html,
  );
  assert.match(
    html,
    /<button class="nav-item hourglass-nav selected" title="Hourglass \(Ctrl 2\)" aria-keyshortcuts="Control\+2"><svg[^>]*lucide-hourglass[^>]*>[^]*?<\/svg><span>Hourglass<\/span><\/button>/,
  );
  assert.match(
    html,
    /class="nav-item plan-nav " title="Flying carpet \(Ctrl 1\)" aria-keyshortcuts="Control\+1"/,
  );
  // No active wish: no flying carpet, the Hourglass still.
  const calm = sidebar(
    [wish("w1", "Trim the wick", s.WishState.PAUSED, 0)],
    false,
  );
  assert.doesNotMatch(calm, /plan-nav/);
  assert.match(
    calm,
    /class="nav-item hourglass-nav " title="Hourglass \(Ctrl 2\)"/,
  );
});

// Ctrl+1 and Ctrl+2 (Q254) are the key's place: the digit row, whatever it types (& and e acute on AZERTY), or the numpad;
// the platform's modifier alone, never with Shift or Alt.
test("a page's shortcut is the place of its digit key, on any layout, the numpad too", () => {
  const press = (code, key, more = {}) => ({
    code,
    key,
    ctrlKey: true,
    metaKey: false,
    altKey: false,
    shiftKey: false,
    ...more,
  });
  // AZERTY: Ctrl+& is Digit1, the flying carpet; Ctrl+(e acute) is Digit2, the Hourglass.
  assert.equal(s.pageDigit(press("Digit1", "&"), false), 1);
  assert.equal(s.pageDigit(press("Digit2", "\u00e9"), false), 2);
  assert.equal(s.pageDigit(press("Digit1", "1"), false), 1);
  assert.equal(s.pageDigit(press("Numpad2", "2"), false), 2);
  // NumLock off, the numpad's 1 is End: still its place.
  assert.equal(s.pageDigit(press("Numpad1", "End"), false), 1);
  // Shift, Alt (AltGr is Ctrl+Alt on Windows), Ctrl with Meta: nothing.
  assert.equal(s.pageDigit(press("Digit1", "1", { shiftKey: true }), false), 0);
  assert.equal(s.pageDigit(press("Digit2", "~", { altKey: true }), false), 0);
  assert.equal(s.pageDigit(press("Digit1", "&", { metaKey: true }), false), 0);
  // No modifier, another key, the digit 0: nothing.
  assert.equal(s.pageDigit(press("Digit1", "&", { ctrlKey: false }), false), 0);
  assert.equal(s.pageDigit(press("KeyA", "1"), false), 0);
  assert.equal(s.pageDigit(press("Digit0", "\u00e0"), false), 0);
  // On a Mac, ⌘ and not Ctrl.
  const cmd = { ctrlKey: false, metaKey: true };
  assert.equal(s.pageDigit(press("Digit2", "\u00e9", cmd), true), 2);
  assert.equal(s.pageDigit(press("Digit2", "\u00e9"), true), 0);
});

// The tooltips name the shortcut as the keyboard writes it: the digit until the layout is known, its character then.
test("a page's shortcut is named for the keyboard's layout once it is known", () => {
  globalThis.__DJINN_MAC__ = false;
  try {
    assert.deepEqual(s.pageShortcut(1, {}), {
      label: "Ctrl 1",
      aria: "Control+1",
    });
    const azerty = { Digit1: "&", Digit2: "\u00e9" };
    assert.equal(s.pageShortcut(1, azerty).label, "Ctrl &");
    assert.equal(s.pageShortcut(2, azerty).label, "Ctrl \u00e9");
    assert.equal(s.pageShortcut(2, azerty).aria, "Control+2");
    assert.equal(s.digitKey(3, azerty), "3");
  } finally {
    delete globalThis.__DJINN_MAC__;
  }
  globalThis.__DJINN_MAC__ = true;
  try {
    assert.equal(s.pageShortcut(2, { Digit2: "\u00e9" }).label, "⌘ \u00e9");
  } finally {
    delete globalThis.__DJINN_MAC__;
  }
  // A key typed teaches its character; with Shift, Caps Lock or AltGr it types another, and teaches nothing.
  const typed = (code, key, more = {}) => ({
    code,
    key,
    ctrlKey: false,
    metaKey: false,
    altKey: false,
    shiftKey: false,
    getModifierState: () => false,
    ...more,
  });
  s.learnDigitKey(typed("Digit4", "1", { shiftKey: true }));
  s.learnDigitKey(
    typed("Digit4", "4", { getModifierState: (m) => m === "CapsLock" }),
  );
  s.learnDigitKey(typed("Digit4", "{", { altKey: true }));
  s.learnDigitKey(typed("Numpad4", "4"));
  assert.equal(s.digitKey(4), "4");
  s.learnDigitKey(typed("Digit4", "'"));
  assert.equal(s.digitKey(4), "'");
  s.learnDigitKey(typed("Digit4", "4", { ctrlKey: true }));
  assert.equal(s.digitKey(4), "4");
});

// A djinn whose machine services count their reads: the series, the commands, the machine the status bar reads.
function hourglassDjinn() {
  const reads = { series: 0, commands: 0 };
  const transport = s.createRouterTransport(({ service }) => {
    service(s.MachineService, {
      series: () => {
        reads.series++;
        return resourceData();
      },
      show: () => ({
        machine: {
          cores: 8,
          workers: 4,
          running: 1,
          memoryTotalBytes: 16n * 1024n ** 3n,
          memoryAvailableBytes: 12n * 1024n ** 3n,
        },
      }),
    });
    service(s.GateService, { list: () => ({ gates: [] }) });
    service(s.CommandService, {
      list: () => {
        reads.commands++;
        return { costs: resourceCommands };
      },
    });
  });
  return { reads, djinn: s.createDjinn(transport, 10) };
}

test("the Hourglass: its sections, the machine first, and the flying carpet's line to it", async () => {
  const { djinn } = hourglassDjinn();
  const html = s.renderToStaticMarkup(
    h(s.DjinnProvider, { djinn }, h(s.Hourglass, { wishes: [] })),
  );
  assert.match(
    html,
    /<div class="breadcrumbs"><strong>Hourglass<\/strong><\/div>/,
  );
  assert.match(html, /<h1>Hourglass<\/h1>/);
  // One section per live metric, the machine first: the whole resources panel, with its range.
  const sections = [...html.matchAll(/class="hourglass-section"/g)];
  assert.equal(sections.length, 1);
  assert.match(
    html,
    /<div id="hourglass-machine" class="hourglass-section"><section class="consumption-panel" aria-label="Resource consumption">/,
  );
  assert.match(html, /aria-pressed="true">30 min<\/button>/);

  // The flying carpet keeps one line of the machine's load, read for the status bar, with a link to the Hourglass.
  await djinn.store.readMachine();
  const line = s.renderToStaticMarkup(
    h(s.DjinnProvider, { djinn }, h(s.HourglassLine)),
  );
  assert.equal(
    line.replace(/<[^>]+>/g, ""),
    "1 of 4 workers running, memory 4 GB · 25% of the machine. See it live in the Hourglass",
  );
  assert.match(
    line,
    /<button type="button" class="text-link hourglass-line-open">See it live in the Hourglass<\/button>/,
  );
});

test("leaving the Hourglass stops its reads and its timers", async () => {
  const container = setupMockDom();
  const { reads, djinn } = hourglassDjinn();
  const timers = () =>
    process.getActiveResourcesInfo().filter((r) => r === "Timeout").length;
  const before = timers();
  const root = s.createRoot(container);
  await s.act(async () => {
    root.render(
      h(s.DjinnProvider, { djinn }, h(s.HourglassSections, { wishes: [] })),
    );
  });
  await s.act(() => new Promise((resolve) => setImmediate(resolve)));
  // Shown: the machine's series and the commands are read, and read again by timers.
  const shown = timers();
  s.act(() => root.unmount());
  // The store's watch, stopped with the provider, ends its last wait of 10 ms.
  await new Promise((resolve) => setTimeout(resolve, 50));
  const left = timers();
  // React's scheduler keeps a port open: it holds the test's process no more.
  for (const handle of process._getActiveHandles())
    if (handle?.constructor?.name === "MessagePort") handle.unref();
  assert.ok(reads.series >= 1 && reads.commands >= 1, JSON.stringify(reads));
  assert.ok(shown > before, `timers while shown: ${shown}, before: ${before}`);
  // Left: no timer stays.
  assert.equal(left, before);
});

test("principal wish: rank 1 is principal by default, wish with principal: true overrides rank", () => {
  const w1 = wish("w1", "First wish", s.WishState.ACTIVE, 1);
  const w2 = wish("w2", "Second wish", s.WishState.ACTIVE, 2);
  const w3 = wish("w3", "Third wish", s.WishState.ACTIVE, 3);
  assert.equal(s.principalWish([w1, w2, w3])?.id, "w1");
  assert.equal(s.isPrincipal(w1, [w1, w2, w3]), true);
  assert.equal(s.isPrincipal(w2, [w1, w2, w3]), false);

  const w2Explicit = { ...w2, principal: true };
  assert.equal(s.principalWish([w1, w2Explicit, w3])?.id, "w2");
  assert.equal(s.isPrincipal(w1, [w1, w2Explicit, w3]), false);
  assert.equal(s.isPrincipal(w2Explicit, [w1, w2Explicit, w3]), true);

  const pausedW1 = { ...w1, state: s.WishState.PAUSED };
  assert.equal(s.principalWish([pausedW1, w2, w3])?.id, "w2");
});

test("flightPlan includes waitingLead questions", () => {
  const w = wish("w1", "First wish", s.WishState.ACTIVE, 1);
  const qWaiting = {
    id: "q-wait",
    code: "Q01",
    text: "Waiting for lead?",
    waitingLead: true,
    answer: { choice: s.Choice.A, note: "yes" },
  };
  const qOpen = {
    id: "q-open",
    code: "Q02",
    text: "Open question?",
  };
  const detail = {
    loaded: true,
    questions: [qWaiting, qOpen],
    tasks: [],
    blocks: [],
  };
  const plan = s.flightPlan([w], { [w.id]: detail });
  assert.equal(plan.questions.length, 1);
  assert.equal(plan.questions[0].item.id, "q-open");
  assert.equal(plan.waitingLead.length, 1);
  assert.equal(plan.waitingLead[0].item.id, "q-wait");
});

test("operating load gauge: click selects nearest notch, keyboard steps, colours per notch class", async () => {
  s.loadManager.setSnapshot({
    notch: s.LoadNotch.MEDIUM,
    auto: false,
    chosenNotch: s.LoadNotch.MEDIUM,
    engagedMemoryBytes: 12n * 1024n * 1024n * 1024n,
    workerMemoryBytes: 4n * 1024n * 1024n * 1024n,
    memoryTotalBytes: 32n * 1024n * 1024n * 1024n,
    memoryAvailableBytes: 24n * 1024n * 1024n * 1024n,
    workerForecastBytes: 3n * 1024n * 1024n * 1024n,
    bindingResource: "memory",
    cpuSlots: 8,
    memorySlots: 4,
    baseSlots: 8,
    runningWorkers: 2,
  });

  const screen = live(s.OperatingLoadSlider, {});

  // Initial render at Medium
  let slider = screen.one("operating-load-slider");
  assert.equal(slider["data-notch"], "medium");

  let input = screen.one("operating-load-input");
  assert.equal(input.role, "slider");
  assert.equal(input["aria-valuemin"], 1);
  assert.equal(input["aria-valuemax"], 5);
  assert.equal(input["aria-valuenow"], 2);
  assert.match(input["aria-valuetext"], /Medium, 50\s*%/);

  // Check 5 notch buttons rendered
  const buttons = screen.all("operating-load-notch");
  assert.equal(buttons.length, 5);
  const notchSlugs = buttons.map((b) => b.props["data-notch"]);
  assert.deepEqual(notchSlugs, [
    "minimal",
    "medium",
    "high",
    "max",
    "overclock",
  ]);

  // Medium button is active
  const mediumBtn = buttons.find((b) => b.props["data-notch"] === "medium");
  assert.match(mediumBtn.props.className, /active/);

  // 1. Click on the track selects nearest notch
  // Simulate click on track at ratio ~0.0 (near left edge) -> Minimal
  slider.onClick({
    clientX: 5,
    currentTarget: { getBoundingClientRect: () => ({ left: 0, width: 200 }) },
    stopPropagation() {},
  });
  slider = screen.one("operating-load-slider");
  assert.equal(slider["data-notch"], "minimal");
  input = screen.one("operating-load-input");
  assert.equal(input["aria-valuenow"], 1);
  assert.match(input["aria-valuetext"], /Minimal, 20\s*%/);

  // Simulate click on track at ratio ~0.95 (near right edge) -> Overclock
  slider.onClick({
    clientX: 195,
    currentTarget: { getBoundingClientRect: () => ({ left: 0, width: 200 }) },
    stopPropagation() {},
  });
  slider = screen.one("operating-load-slider");
  assert.equal(slider["data-notch"], "overclock");
  input = screen.one("operating-load-input");
  assert.equal(input["aria-valuenow"], 5);
  assert.match(input["aria-valuetext"], /Overclock, 150\s*%/);

  // Click on a notch button directly: click High button
  const highBtn = screen
    .all("operating-load-notch")
    .find((b) => b.props["data-notch"] === "high");
  highBtn.props.onClick({ stopPropagation() {} });
  slider = screen.one("operating-load-slider");
  assert.equal(slider["data-notch"], "high");
  input = screen.one("operating-load-input");
  assert.equal(input["aria-valuenow"], 3);
  assert.match(input["aria-valuetext"], /High, 80\s*%/);

  // 2. Keyboard steps: ArrowRight, ArrowLeft, ArrowUp, ArrowDown, Home, End
  // From High (index 2 / value 3), ArrowRight moves to Max (index 3 / value 4)
  let prevented = false;
  input.onKeyDown({
    key: "ArrowRight",
    preventDefault() {
      prevented = true;
    },
  });
  assert.equal(prevented, true);
  slider = screen.one("operating-load-slider");
  assert.equal(slider["data-notch"], "max");
  input = screen.one("operating-load-input");
  assert.equal(input["aria-valuenow"], 4);
  assert.match(input["aria-valuetext"], /Max, 100\s*%/);

  // From Max, ArrowDown moves to High
  prevented = false;
  input.onKeyDown({
    key: "ArrowDown",
    preventDefault() {
      prevented = true;
    },
  });
  assert.equal(prevented, true);
  slider = screen.one("operating-load-slider");
  assert.equal(slider["data-notch"], "high");

  // Home key jumps to Minimal (notch 0 / value 1)
  prevented = false;
  input.onKeyDown({
    key: "Home",
    preventDefault() {
      prevented = true;
    },
  });
  assert.equal(prevented, true);
  slider = screen.one("operating-load-slider");
  assert.equal(slider["data-notch"], "minimal");
  input = screen.one("operating-load-input");
  assert.equal(input["aria-valuenow"], 1);

  // End key jumps to Overclock (notch 4 / value 5)
  prevented = false;
  input.onKeyDown({
    key: "End",
    preventDefault() {
      prevented = true;
    },
  });
  assert.equal(prevented, true);
  slider = screen.one("operating-load-slider");
  assert.equal(slider["data-notch"], "overclock");
  input = screen.one("operating-load-input");
  assert.equal(input["aria-valuenow"], 5);

  // 3. Colours per notch class
  // Check that all 5 notches correctly reflect their active class and data-notch
  for (const notch of s.NOTCHES) {
    const slug = s.notchSlug(notch);
    await s.loadManager.setNotch(notch);
    slider = screen.one("operating-load-slider");
    assert.equal(slider["data-notch"], slug);
    const active = screen
      .all("operating-load-notch")
      .find((b) => b.props.className.includes("active"));
    assert.equal(active.props["data-notch"], slug);
  }
});

test("operating load forecast: binding resource (memory-bound vs cpu-bound) and consistent two-line numbers", async () => {
  // Case A: Memory-bound machine (e.g. 4 memory slots, 8 cpu slots)
  s.loadManager.setSnapshot({
    notch: s.LoadNotch.MEDIUM,
    auto: false,
    chosenNotch: s.LoadNotch.MEDIUM,
    engagedMemoryBytes: 12n * 1024n * 1024n * 1024n,
    workerMemoryBytes: 4n * 1024n * 1024n * 1024n,
    memoryTotalBytes: 32n * 1024n * 1024n * 1024n,
    memoryAvailableBytes: 24n * 1024n * 1024n * 1024n,
    workerForecastBytes: 3n * 1024n * 1024n * 1024n, // ~3 GiB
    bindingResource: "memory",
    cpuSlots: 8,
    memorySlots: 4,
    baseSlots: 4, // min(cpu, memory)
    runningWorkers: 2,
  });

  let screen = live(s.OperatingLoadForecast, {});
  let binding = screen.one("operating-load-forecast-binding");
  // At Medium (50% of 4 = 2 workers)
  assert.match(
    s.renderToStaticMarkup(binding.children),
    /limited by memory: 2 workers of ~3 GB/,
  );

  let rows = screen.all("operating-load-forecast-row");
  assert.equal(rows.length, 2);
  let cpuText = s.renderToStaticMarkup(rows[0].props.children);
  let memText = s.renderToStaticMarkup(rows[1].props.children);
  assert.match(cpuText, /CPU: 2 of 2 workers run/);
  assert.match(memText, /Memory: 11 GB of 24 GB/); // (24 - 2) * 0.5 = 11 GB engaged

  // Case B: CPU-bound machine (e.g. 4 cpu slots, 16 memory slots)
  s.loadManager.setSnapshot({
    notch: s.LoadNotch.HIGH,
    auto: false,
    chosenNotch: s.LoadNotch.HIGH,
    engagedMemoryBytes: 16n * 1024n * 1024n * 1024n,
    workerMemoryBytes: 3n * 1024n * 1024n * 1024n,
    memoryTotalBytes: 64n * 1024n * 1024n * 1024n,
    memoryAvailableBytes: 48n * 1024n * 1024n * 1024n,
    workerForecastBytes: 1536n * 1024n * 1024n, // ~1.5 GiB
    bindingResource: "cpu",
    cpuSlots: 4,
    memorySlots: 16,
    baseSlots: 4,
    runningWorkers: 1,
  });

  screen = live(s.OperatingLoadForecast, {});
  binding = screen.one("operating-load-forecast-binding");
  // At High (80% of 4 = 3 workers)
  assert.match(
    s.renderToStaticMarkup(binding.children),
    /limited by CPU: 3 workers of ~1\.5 GB/,
  );

  rows = screen.all("operating-load-forecast-row");
  assert.equal(rows.length, 2);
  cpuText = s.renderToStaticMarkup(rows[0].props.children);
  memText = s.renderToStaticMarkup(rows[1].props.children);
  assert.match(cpuText, /CPU: 1 of 3 workers run/);
  assert.match(memText, /Memory: 37 GB of 48 GB/); // (48 - 2) * 0.8 = 36.8 GB rounded to 37 GB
});

test("operating load: each notch shows what the scheduler lets run there, and what moving the bar does (W577)", async () => {
  const N = s.LoadNotch;
  // The server's slots per notch: at Medium the scheduler runs 3, where the old estimate (50 % of 8) said 4.
  const notchSlots = {
    [N.MINIMAL]: 2,
    [N.MEDIUM]: 3,
    [N.HIGH]: 6,
    [N.MAX]: 8,
    [N.OVERCLOCK]: 12,
  };
  const base = {
    notch: N.HIGH,
    auto: false,
    chosenNotch: N.HIGH,
    engagedMemoryBytes: 8n * 1024n * 1024n * 1024n,
    workerMemoryBytes: 6n * 1024n * 1024n * 1024n,
    memoryTotalBytes: 31n * 1024n * 1024n * 1024n,
    memoryAvailableBytes: 14n * 1024n * 1024n * 1024n,
    workerForecastBytes: 1536n * 1024n * 1024n,
    bindingResource: "cpu",
    cpuSlots: 8,
    memorySlots: 10,
    baseSlots: 8,
    slots: 6,
    notchSlots,
    runningWorkers: 6,
    pausedWorkers: 0,
    forcedWorkers: 0,
    lowerPauses: true,
  };
  const cpuRow = () => {
    const screen = live(s.OperatingLoadForecast, {});
    const row = screen.all("operating-load-forecast-row")[0];
    return {
      text: s.renderToStaticMarkup(row.props.children),
      title:
        s
          .renderToStaticMarkup(row.props.children)
          .match(/title="([^"]*)"/)?.[1] ?? "",
    };
  };
  const notchTitle = (slug) =>
    live(s.OperatingLoadSlider, {})
      .all("operating-load-notch")
      .find((b) => b.props["data-notch"] === slug).props.title;

  s.loadManager.setPreviewNotch(null);
  s.loadManager.setSnapshot(base);
  // Each notch's tooltip names the server's count, never an estimate of its own.
  for (const [slug, n] of [
    ["minimal", N.MINIMAL],
    ["medium", N.MEDIUM],
    ["high", N.HIGH],
    ["max", N.MAX],
    ["overclock", N.OVERCLOCK],
  ])
    assert.match(
      notchTitle(slug),
      new RegExp(`up to ${notchSlots[n]} workers`),
      slug,
    );
  // At High, 6 of 6 run; no worker beyond; the label says what lowering does.
  let row = cpuRow();
  assert.match(row.text, /CPU: 6 of 6 workers run/);
  assert.doesNotMatch(row.text, /operating-load-forecast-over/);
  assert.match(
    row.title,
    /Lowering the load pauses the newest workers above its limit/,
  );

  // Previewing Medium: 3 run there, the 3 newest pause.
  s.loadManager.setPreviewNotch(N.MEDIUM);
  row = cpuRow();
  assert.match(row.text, /CPU: 6 of 3 workers run/);
  assert.match(row.text, /data-over="pause"[^>]*> · here the 3 newest pause/);
  // Where lowering lets them finish (Auto, Windows), it says so.
  s.loadManager.setSnapshot({ lowerPauses: false });
  row = cpuRow();
  assert.match(
    row.text,
    /data-over="finish"[^>]*> · 3 above the limit finish their run/,
  );
  assert.match(row.title, /lets the workers above its limit finish their run/);
  s.loadManager.setPreviewNotch(null);

  // At Medium, 4 run: 1 started by hand, the other above the limit finishing its run (a gate, a correction).
  s.loadManager.setSnapshot({
    ...base,
    notch: N.MEDIUM,
    chosenNotch: N.MEDIUM,
    slots: 3,
    runningWorkers: 5,
    forcedWorkers: 1,
  });
  row = cpuRow();
  assert.match(row.text, /CPU: 5 of 3 workers run/);
  assert.match(row.text, /data-over="hand"[^>]*> · 1 started by hand/);
  assert.match(
    row.text,
    /data-over="finish"[^>]*> · 1 above the limit finishes its run/,
  );

  // Before the server says, the estimate stands in.
  s.loadManager.setSnapshot({ ...base, notchSlots: {}, runningWorkers: 0 });
  assert.match(notchTitle("medium"), /up to 4 workers/);
  s.loadManager.setSnapshot(base);
});

test("azima counters and states in the window are right before any azima is opened on a wish with more tasks than one page", async () => {
  const at = (seconds) => ({ seconds: BigInt(seconds), nanos: 0 });
  const wishId = "01a11833-a440-7479-a067-52615c91da71";
  const lamp = wish(wishId, "Large wish", s.WishState.ACTIVE, 1, {
    createTime: at(1),
  });

  const azima1 = {
    id: "A1",
    wishId,
    code: "T1",
    title: "Lay the ground",
    kind: s.TaskKind.AZIMA,
    status: s.TaskStatus.DONE,
    azima: {
      state: s.AzimaState.DONE,
      ready: true,
      parts: 180,
      partsDone: 180,
      partsRunning: 0,
    },
    dependsOn: [],
    createTime: at(1),
  };
  const azima2 = {
    id: "A2",
    wishId,
    code: "T2",
    title: "The orchestrator",
    kind: s.TaskKind.AZIMA,
    status: s.TaskStatus.RUNNING,
    azima: {
      state: s.AzimaState.IN_PROGRESS,
      ready: true,
      parts: 250,
      partsDone: 200,
      partsRunning: 5,
    },
    dependsOn: ["T1"],
    createTime: at(1),
  };

  // 1. AzimaCard rendered directly before any parts are loaded (parts: [])
  // A2 is in progress with 200/250 parts done, 5 running, 45 waiting
  const cardInProgress = s.renderToStaticMarkup(
    h(s.AzimaCard, {
      azima: azima2,
      parts: [],
      tasks: new Map([[azima2.id, azima2]]),
      render: () => null,
    }),
  );
  assert.equal(s.azimaFinished(azima2), false);
  assert.match(cardInProgress, /class="azima-progress"/);
  assert.match(cardInProgress, /200\/250<\/span>/);
  assert.match(
    cardInProgress,
    /class="azima-progress" title="200 of 250 parts done\n200 done, 5 running, 45 waiting"/,
  );
  assert.match(cardInProgress, /status-badge tone-running/);

  // A1 is done with 180/180 parts done, parts: []
  const cardDone = s.renderToStaticMarkup(
    h(s.AzimaCard, {
      azima: azima1,
      parts: [],
      tasks: new Map([[azima1.id, azima1]]),
      render: () => null,
    }),
  );
  assert.equal(s.azimaFinished(azima1), true);
  assert.match(cardDone, /class="azima-progress"/);
  assert.match(cardDone, /180\/180<\/span>/);
  assert.match(cardDone, /status-badge tone-done/);

  // 2. Full window WishView with only page 1 loaded from the server
  // (e.g. 50 tasks out of 430 total)
  const page1Tasks = [azima1, azima2];
  for (let i = 1; i <= 48; i++) {
    page1Tasks.push({
      id: `W${i}`,
      wishId,
      code: `W${i}`,
      title: `Task ${i}`,
      kind: s.TaskKind.WORKER,
      status: i <= 20 ? s.TaskStatus.DONE : s.TaskStatus.PENDING,
      partOf: "A2",
      dependsOn: [],
      createTime: at(1),
    });
  }

  const transport = s.createRouterTransport(({ service }) => {
    service(s.WishService, { list: () => ({ wishes: [lamp] }) });
    service(s.ProjectService, { list: () => ({ projects: [] }) });
    service(s.TaskService, {
      list: () => ({
        tasks: page1Tasks,
        total: 430,
        nextPageToken: "page-2",
      }),
    });
    service(s.QuestionService, { list: () => ({ questions: [] }) });
    service(s.BlockService, { list: () => ({ blocks: [] }) });
  });

  const djinn = s.createDjinn(transport, 10);
  await djinn.store.changed("", [s.Change.WISH, s.Change.PROJECT]);
  const close = djinn.store.open(wishId);
  await djinn.store.changed(wishId, [s.Change.TASK]);
  close();

  const view = s.renderToStaticMarkup(
    h(s.DjinnProvider, { djinn }, h(s.WishView, { wish: lamp, onToast() {} })),
  );

  // Azima pill: 1 done out of 2 azimas
  assert.match(
    view,
    /title="1 of 2 azimas done"[^>]*>.*?<b>1<\/b>\s*\/ 2 azimas<\/span>/,
  );
  // Work pill: 380 done out of 430 total tasks across the wish
  assert.match(
    view,
    /title="380 of 430 tasks done"[^>]*>.*?<b>380<\/b>\s*\/ 430 done<\/span>/,
  );

  // 3. Tasks tab / TaskSections: azimas partition into active and done using server state,
  // and AzimaCard in progress renders 200/250 right away before opening
  const groups = s.azimaGroups(page1Tasks);
  const active = groups.filter((x) => !s.azimaFinished(x.azima));
  const done = groups.filter((x) => s.azimaFinished(x.azima));
  assert.equal(active.length, 1);
  assert.equal(done.length, 1);
  assert.equal(active[0].azima.code, "T2");
  assert.equal(done[0].azima.code, "T1");

  const tasksTab = s.renderToStaticMarkup(
    h(s.TaskSections, {
      moving: [],
      azimas: active,
      doneAzimas: done,
      fold: wishId,
      showDone: false,
      renderAzima: ({ azima, parts }) =>
        h(s.AzimaCard, {
          key: azima.id,
          azima,
          parts,
          tasks: new Map(page1Tasks.map((t) => [t.id, t])),
          render: () => null,
        }),
      render: () => null,
    }),
  );
  assert.match(tasksTab, /200\/250<\/span>/);
});

test("a long diff loads its next chunk as the reader scrolls near its end, a hunk cut between chunks joined back", async () => {
  const line = (kind, n, text) => ({
    kind,
    oldLine: kind === s.DiffLineKind.ADDED ? 0 : n,
    newLine: kind === s.DiffLineKind.DELETED ? 0 : n,
    text,
    tokens: [],
  });
  const first = {
    path: "big.txt",
    totalLines: 9,
    offset: 0,
    nextOffset: 5,
    hunks: [
      {
        header: "@@ -1,4 +1,4 @@",
        lines: [
          line(s.DiffLineKind.CONTEXT, 1, "line one"),
          line(s.DiffLineKind.ADDED, 2, "line two"),
          line(s.DiffLineKind.ADDED, 3, "line three"),
        ],
      },
    ],
  };
  const second = {
    path: "big.txt",
    totalLines: 9,
    offset: 5,
    nextOffset: 0,
    hunks: [
      {
        header: "@@ -1,4 +1,4 @@",
        continued: true,
        lines: [line(s.DiffLineKind.ADDED, 4, "line four")],
      },
      {
        header: "@@ -20,1 +20,1 @@",
        lines: [line(s.DiffLineKind.ADDED, 20, "line twenty")],
      },
    ],
  };

  // appendChunk joins the hunk the second chunk carries on to the first chunk's last one.
  const joined = s.appendChunk(s.appendChunk(null, first), second);
  assert.deepEqual(
    joined.hunks.map((hk) => [hk.header, hk.lines.map((l) => l.text)]),
    [
      ["@@ -1,4 +1,4 @@", ["line one", "line two", "line three", "line four"]],
      ["@@ -20,1 +20,1 @@", ["line twenty"]],
    ],
  );
  assert.equal(joined.nextOffset, 0);

  const container = setupMockDom();
  const observers = [];
  const savedObserver = globalThis.IntersectionObserver;
  globalThis.IntersectionObserver = class {
    constructor(callback) {
      this.callback = callback;
      this.live = true;
      observers.push(this);
    }
    observe() {}
    disconnect() {
      this.live = false;
    }
  };
  const asked = [];
  const transport = s.createRouterTransport(({ service }) => {
    service(s.TaskService, {
      diff: (req) => {
        if (req.file === "") {
          return { files: [{ path: "big.txt", added: 5 }], head: "abc" };
        }
        asked.push(req.offset);
        return { file: req.offset === 0 ? first : second, head: "abc" };
      },
    });
  });
  const djinn = s.createDjinn(transport, 10);
  // React sets a lone text child as textContent, and clears a root's: children first.
  const text = (node) =>
    node.childNodes.length > 0
      ? node.childNodes.map(text).join("")
      : (node.nodeValue ?? node.textContent ?? "");
  const count = (node, cls) =>
    (node.attributes?.class?.split(" ").includes(cls) ? 1 : 0) +
    node.childNodes.reduce((n, c) => n + count(c, cls), 0);
  const settle = async () => {
    for (let i = 0; i < 5; i++) {
      await s.act(async () => {
        await new Promise((r) => setTimeout(r, 0));
      });
    }
  };

  const root = s.createRoot(container);
  try {
    s.act(() => {
      root.render(
        h(
          s.DjinnProvider,
          { djinn },
          h(s.TaskDiffView, { task: { id: "t1", comments: [] } }),
        ),
      );
    });
    await settle();
    assert.deepEqual(asked, [0], "the first chunk alone at first");
    let shown = text(container);
    assert.match(shown, /line three/);
    assert.doesNotMatch(shown, /line four/);
    assert.match(shown, /5 of 9 lines shown/);
    assert.equal(count(container, "diff-next-chunk"), 1);

    // The end of the diff nears the view: the window asks for the next chunk, once.
    const live = observers.filter((o) => o.live);
    assert.equal(live.length, 1);
    await s.act(async () => {
      live[0].callback([{ isIntersecting: true }]);
      live[0].callback([{ isIntersecting: true }]);
    });
    await settle();
    assert.deepEqual(asked, [0, 5]);
    shown = text(container);
    assert.match(shown, /line four/);
    assert.match(shown, /line twenty/);
    assert.equal(count(container, "diff-hunk-header"), 2);
    assert.equal(
      count(container, "diff-next-chunk"),
      0,
      "nothing more to load",
    );
  } finally {
    s.act(() => root.unmount());
    globalThis.IntersectionObserver = savedObserver;
    for (const handle of process._getActiveHandles()) {
      if (handle?.constructor?.name === "MessagePort") handle.unref();
    }
  }
});

test("the uncommitted diff asks for the worktree's changes, read only, and says when the worktree is clean or gone", async () => {
  const container = setupMockDom();
  const asked = [];
  let answer = {};
  const transport = s.createRouterTransport(({ service }) => {
    service(s.TaskService, {
      diff: (req) => {
        asked.push([req.file, req.uncommitted]);
        if (req.file !== "") {
          return {
            file: {
              path: "notes.md",
              totalLines: 6,
              hunks: [
                {
                  header: "@@ -0,0 +1 @@",
                  lines: [
                    {
                      kind: s.DiffLineKind.ADDED,
                      newLine: 1,
                      text: "a draft",
                      tokens: [],
                    },
                  ],
                },
              ],
            },
            base: "abc",
          };
        }
        return answer;
      },
    });
  });
  const djinn = s.createDjinn(transport, 10);
  const text = (node) =>
    node.childNodes.length > 0
      ? node.childNodes.map(text).join("")
      : (node.nodeValue ?? node.textContent ?? "");
  const count = (node, cls) =>
    (node.attributes?.class?.split(" ").includes(cls) ? 1 : 0) +
    node.childNodes.reduce((n, c) => n + count(c, cls), 0);
  const settle = async () => {
    for (let i = 0; i < 5; i++) {
      await s.act(async () => {
        await new Promise((r) => setTimeout(r, 0));
      });
    }
  };
  const root = s.createRoot(container);
  const show = async (key) => {
    s.act(() => {
      root.render(
        h(
          s.DjinnProvider,
          { djinn },
          h(s.TaskDiffView, {
            key,
            task: { id: "t1", comments: [] },
            uncommitted: true,
          }),
        ),
      );
    });
    await settle();
    return text(container);
  };
  try {
    answer = {
      files: [{ path: "notes.md", added: 1, status: s.DiffStatus.ADDED }],
      base: "abc",
    };
    const shown = await show("changed");
    assert.deepEqual(asked, [
      ["", true],
      ["notes.md", true],
    ]);
    assert.match(shown, /a draft/);
    assert.equal(count(container, "diff-line-row"), 1);
    // Work not committed has no commit a comment could name: no comment on it.
    assert.equal(count(container, "diff-add-comment-btn"), 0);

    answer = { files: [], base: "abc" };
    assert.match(
      await show("clean"),
      /The worktree is clean: nothing left uncommitted\./,
    );
    answer = { files: [], noWorktree: true };
    assert.match(await show("gone"), /This task has no worktree any more/);
  } finally {
    s.act(() => root.unmount());
    for (const handle of process._getActiveHandles()) {
      if (handle?.constructor?.name === "MessagePort") handle.unref();
    }
  }
});

test("the Review tab lists merged work, its merges under the task they serve, checks each, and counts what is left", async () => {
  const at = (seconds) => ({ seconds: BigInt(seconds), nanos: 0 });
  const merged = { state: s.IntegrationState.COMMITTED, sha: "abc" };
  const work = (code, extra) => ({
    id: code,
    code,
    wishId: "w",
    title: `Task ${code}`,
    status: s.TaskStatus.DONE,
    branch: `b-${code}`,
    dependsOn: [],
    createTime: at(1),
    integration: merged,
    ...extra,
  });
  const reviewed = (actor) => ({
    marks: [
      { kind: s.MarkKind.REVIEWED, actor, createTime: at(1_760_000_000) },
    ],
  });
  const tasks = [
    work("W1", {
      comments: [
        {
          id: "c1",
          file: "src/lamp.go",
          line: 3,
          endLine: 5,
          text: "Off by one",
        },
        {
          id: "c2",
          file: "src/lamp.go",
          line: 9,
          endLine: 9,
          text: "Done",
          resolved: true,
        },
      ],
    }),
    // Not merged: its tests failed; still running; an azima; a question worker, without a branch; a 0-commit task without a merge sha.
    work("W2", { integration: { state: s.IntegrationState.RED } }),
    work("W3", { status: s.TaskStatus.RUNNING, integration: undefined }),
    work("W4", { kind: s.TaskKind.AZIMA, branch: "" }),
    work("W5", { branch: "", integration: undefined }),
    // A correction of W1, merged: under W1.
    work("W6", { correction: { failure: { taskIds: ["W1"] }, attempt: 1 } }),
    // Merged and checked by the developer.
    work("W7", reviewed("local")),
    // A push correction serves no task: a row of its own.
    work("W8", { correction: { failure: { taskIds: [] }, attempt: 1 } }),
    work("W9", {
      integration: { state: s.IntegrationState.COMMITTED, sha: "" },
    }),
  ];
  const rows = s.reviewRows(tasks);
  assert.deepEqual(
    rows.map((r) => [r.task.code, r.merges.map((x) => x.code)]),
    [
      ["W1", ["W6"]],
      ["W7", []],
      ["W8", []],
    ],
  );
  assert.equal(s.reviewLeft(rows), 3);
  // W6 checked: W1's row still waits on W1 itself.
  const rowsChecked = s.reviewRows(
    tasks.map((x) => (x.code === "W6" ? { ...x, ...reviewed("local") } : x)),
  );
  assert.equal(s.reviewLeft(rowsChecked), 2);

  const html = s.renderToStaticMarkup(
    h(s.ReviewTab, {
      rows,
      fold: "w-review-test",
      codes: new Map(tasks.map((x) => [x.id, x.code])),
      onReview: async () => {},
    }),
  );
  // What is left first, the merge indented under the task it serves, marked; the comments counted; the checked folded.
  assert.match(
    html,
    /aria-checked="false"[^>]*aria-label="Mark W1 reviewed".*review-code">W1<.*Task W1<.*title="2 comments on its diff".*review-merges.*review-code">W6<\/span><span class="review-merge">Merge<\/span>.*review-code">W8</s,
  );
  assert.doesNotMatch(html, /review-code">W7</);
  assert.match(html, /Show 1 reviewed/);
  for (const code of ["W2", "W3", "W4", "W5", "W9"])
    assert.doesNotMatch(html, new RegExp(`review-code">${code}<`));

  // A click on the box checks the task; checked, it says who and when, and a click takes the mark off.
  const calls = [];
  const opened = [];
  const props = (task, open = false) => ({
    task,
    merge: false,
    open,
    codes: new Map(),
    onOpen: (id) => opened.push(id),
    onReview: async (x, remove) => calls.push([x.code, remove]),
  });
  const line = live(s.ReviewLine.type, props(tasks[0]));
  line.one("review-check").onClick();
  line.one("review-open").onClick();
  await line.settle();
  assert.deepEqual(calls, [["W1", false]]);
  assert.deepEqual(opened, ["W1"]);
  const checked = live(s.ReviewLine.type, props(tasks[6]));
  assert.match(
    checked.html(),
    /aria-checked="true"[^>]*aria-label="W7 reviewed: take the mark off"/,
  );
  assert.match(checked.html(), /class="review-by">you, /);
  checked.one("review-check").onClick();
  await checked.settle();
  assert.deepEqual(calls[1], ["W7", true]);
  // Opened: the comments on its lines, where they are, the resolved one struck; then its diff.
  const open = s.renderToStaticMarkup(h(s.ReviewLine, props(tasks[0], true)));
  assert.match(
    open,
    /review-comment-list"><li class=""><span class="review-where">src\/lamp.go:3-5<\/span><span>Off by one<\/span><\/li><li class="resolved"><span class="review-where">src\/lamp.go:9<\/span>.*task-diff-view/s,
  );

  // The tab sits after Tasks, with what is left to review.
  const tabs = s.renderToStaticMarkup(
    h(s.ViewTabs, {
      view: "review",
      main: "Wish",
      tasks: 8,
      review: 3,
      decisions: 0,
      onView() {},
    }),
  );
  assert.match(
    tabs,
    /id="view-tab-tasks".*id="view-tab-review" aria-selected="true" class="active">Review<span class="count">3<\/span>.*id="view-tab-decisions"/,
  );
  // The flying carpet has no Review tab.
  assert.doesNotMatch(
    s.renderToStaticMarkup(
      h(s.ViewTabs, {
        view: "main",
        main: "Flying carpet",
        tasks: 0,
        decisions: 0,
        onView() {},
      }),
    ),
    /view-tab-review/,
  );
});

test("MakeWish requires an active lead before a wish can be created", async () => {
  let resumed = 0;
  const page = live(s.MakeWish, {
    projects: [],
    active: 0,
    lead: undefined,
    onMade() {},
    onClose() {},
    onResumeLead: async () => {
      resumed++;
    },
  });

  // With no active lead, the creation screen shows the lead onboarding step and command.
  const withoutLeadHtml = page.html();
  assert.match(
    withoutLeadHtml,
    /No Djinn is running to receive this wish: start the Djinn first, or run djinn lead resume\./,
  );
  assert.match(withoutLeadHtml, /djinn lead resume/);
  assert.match(withoutLeadHtml, />Start the Djinn</);

  // Typing a wish keeps submission disabled until a lead is running.
  const findByProp = (pred) => {
    const found = [];
    const walk = (node) => {
      if (Array.isArray(node)) node.forEach(walk);
      else if (node && typeof node === "object" && node.props) {
        if (pred(node)) found.push(node);
        walk(node.props.children);
      }
    };
    walk(page.all("section")[0]);
    return found;
  };
  const composerNode = findByProp(
    (n) => typeof n.props.onChange === "function" && "invalid" in n.props,
  )[0];
  assert.ok(composerNode, "WishComposer mounted");
  composerNode.props.onChange("Build a flying carpet.", true);

  const submitButton = () =>
    findByProp((n) => n.props.type === "submit")[0].props;
  assert.equal(
    submitButton().disabled,
    true,
    "submit stays disabled while no lead is active",
  );

  // Starting the lead from New Wish runs the resume action, clears the warning, and unlocks submission.
  const resumeButton = page
    .all("button")
    .find((b) => b.props.children === "Start the Djinn");
  assert.ok(resumeButton, "Start the Djinn button rendered");
  resumeButton.props.onClick();
  await page.settle();
  assert.equal(resumed, 1);
  assert.doesNotMatch(page.html(), /No Djinn is running to receive this wish/);
  assert.equal(
    submitButton().disabled,
    false,
    "submit is enabled once the lead is started",
  );

  // When a lead is already active, no lead warning is shown and submission is enabled as soon as a prompt is typed.
  const activePage = live(s.MakeWish, {
    projects: [],
    active: 0,
    lead: {
      provider: s.Provider.CLAUDE_CODE,
      sessionId: "lead-session",
      directory: "/workspace/lamp",
    },
    onMade() {},
    onClose() {},
  });
  assert.doesNotMatch(
    activePage.html(),
    /No Djinn is running to receive this wish/,
  );
  assert.doesNotMatch(activePage.html(), /djinn lead resume/);

  // When the lead has exited with an error, MakeWish surfaces the exit trouble and blocks submission.
  const exitedPage = live(s.MakeWish, {
    projects: [],
    active: 0,
    lead: {
      provider: s.Provider.CLAUDE_CODE,
      sessionId: "lead-session",
      directory: "/workspace/lamp",
    },
    leadExit: { code: 1 },
    onMade() {},
    onClose() {},
  });
  assert.match(exitedPage.html(), /The Djinn ended \(code 1\)/);
  assert.match(exitedPage.html(), /djinn lead resume/);
});

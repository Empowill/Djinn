// The screens on the services, rendered to text from a store filled by in-memory services: what the tests of each
// component (tests/<component>.test.mjs) share. Run with `go tool task test-ui`. Node reports English: the texts
// asserted are the English ones.
import assert from "node:assert/strict";

import { bundle } from "./bundle.mjs";

// screens bundles the components the screens' tests render with React, Connect, the store and the plan's messages, one
// bundle for them all, and gives the bundle (s), its createElement (h) and live on it.
export async function screens() {
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
export { ProjectGear, WishSidebar } from "@/src/wish-sidebar.tsx";
export { SyncLine, SyncLines, syncTitle } from "@/src/project-sync.tsx";
export { WishQuestion, NoteCounter } from "@/src/wish-question.tsx";
export {
  AsleepQuestions,
  QuestionColumn,
  arrowTo,
  columnMove,
  heir,
  questionGroups,
  stepTo,
} from "@/src/question-column.tsx";
export { WaitingTasks, WishView } from "@/src/wish-view.tsx";
export { EmptyState, QuestionsEmpty, sessionPick } from "@/src/empty-art.tsx";
export { Machine } from "@/src/visuals.tsx";
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
  runningOf,
  fixerOf,
  answerOutcome,
  planKind,
  NOTE_MAX,
} from "@/src/data/format.ts";
export { FlightPlan } from "@/src/flight-plan.tsx";
export { glanceOf, forgeMark, failingTests } from "@/src/glance.tsx";
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
  compareRanks,
  draftAzimas,
  finishedTasks,
  flightPlan,
  holdsUp,
  mergeNest,
  movingTasks,
  waitingLeadQuestions,
  waitingTasks,
  workCount,
  spent,
} from "@/src/data/flight.ts";
export { AzimaCard, azimaFinished, DraftAzimaCard } from "@/src/azima.tsx";
export { RankedAzimaList } from "@/src/azima-rank.tsx";
export {
  FolderField,
  MakeWish,
  ShortcutField,
  SleepQuestionDialog,
  sleepRequests,
  sleepTasks,
  sleepUntil,
} from "@/src/wish-dialogs.tsx";
export {
  BranchChoice,
  ConfigChanges,
  NoticeLine,
  ProjectChecks,
  ProjectPanel,
  ProjectSettings,
  ReferenceBranch,
  SkillLine,
  WatchCommands,
} from "@/src/project-panel.tsx";
export {
  CIRCLE_MODELS,
  CIRCLE_ROWS,
  SummoningCircle,
  canonicalModelId,
} from "@/src/summoning-circle.tsx";
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
  seatName,
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
export { TilasmFrame, TilasmList, newestTilasm } from "@/src/tilasms.tsx";
export {
  TilasmOver,
  TilasmOverContext,
  giveBack,
} from "@/src/tilasm-over.tsx";
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
export { CommentsColumn, SendBar } from "@/src/tilasm-comments.tsx";
export {
  AnchorStatus,
  CommentState,
  SendChoice,
  TilasmHoldReason,
  TilasmService,
} from "@/gen/ts/plan/v1/tilasm_pb.ts";
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
export { Docs, GRIMOIRE } from "@/src/docs.tsx";
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
  OperatingLoadAuto,
  loadManager,
  NOTCHES,
  notchLabel,
  notchSlug,
  nearestNotch,
  stopAt,
} from "@/src/operating-load.tsx";
export { LoadAutoStep, LoadNotch } from "@/gen/ts/djinn/v1/load_pb.ts";
export { HeldGates } from "@/src/ifrit.tsx";
export * from "@/gen/ts/plan/v1/plan_pb.ts";`,
  );
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
  return {
    s,
    h: s.createElement,
    live: (component, props) => live(s, component, props),
  };
}

// live renders a component whose state lives between renders, so that a test types and clicks with no DOM: it calls
// the component with a dispatcher that keeps its useState, as React does, and walks the elements it returns. The
// components it reaches use no other hook.
function live(s, component, props) {
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

export const wish = (id, title, state, rank, extra = {}) => ({
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

export const usage = (input, output, read, write, costUsd) => ({
  inputTokens: BigInt(input),
  outputTokens: BigInt(output),
  cacheReadTokens: BigInt(read),
  cacheWriteTokens: BigInt(write),
  costUsd,
});

export const stamp = (iso) => ({
  seconds: BigInt(Date.parse(iso) / 1000),
  nanos: 0,
});

export const tilasm = (id, code, title, extra = {}) => ({
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

// A branch Djinn pushes, as ProjectService sends it in Project.syncs.
export const projectSync = (branch, ahead, behind, extra = {}) => ({
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

// setupMockDom gives React a document of its own to mount into, enough for createRoot and act.
export function setupMockDom() {
  globalThis.IS_REACT_ACT_ENVIRONMENT = true;

  class MockElement {
    constructor(nodeType, nodeName) {
      this.nodeType = nodeType;
      this.nodeName = nodeName;
      this.tagName = nodeName;
      this.childNodes = [];
      this.parentNode = null;
      this.attributes = {};
      this.value = "";
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
    focus() {}
    setSelectionRange() {}
    closest() {
      return null;
    }
    querySelector() {
      return null;
    }
    querySelectorAll() {
      return [];
    }
    contains() {
      return false;
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

// resources is the machine, its gates, its workers and the commands' costs, as MachineService and CommandService
// send them, and a djinn that serves them (the Hourglass, the resources panel, a task's resources).
export function resources(s) {
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
  return {
    t0,
    t1,
    t2,
    t3,
    at,
    mib,
    longTitle,
    lintGate,
    resourceData,
    resourceCost,
    resourceCommands,
    hourglassDjinn,
  };
}

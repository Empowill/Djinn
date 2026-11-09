// The Summoning Circle as a page (src/summoning-circle.tsx, W540): the agents, then every sign-in in the order of
// priority with what runs there, then the models by kind of worker, then the change of the open tasks; a drop on the
// order writes it whole. Run with `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { screens, setupMockDom } from "./screens.mjs";

const { s, h } = await screens();

const ready = 3; // ProviderState.READY

// environment is what UiService.GetEnvironment answers: Claude Code ready on its own sign-in with an account work,
// Codex ready on its own.
function environment() {
  return {
    providers: [
      {
        id: "claude",
        name: "Claude Code",
        available: true,
        command: "claude",
        state: ready,
        loginCommand: "claude auth login",
        paused: false,
        accounts: [
          {
            account: {
              name: "work",
              provider: s.Provider.CLAUDE,
              label: "Work",
              configDirectory: "/home/me/.claude-work",
              paused: false,
            },
            state: ready,
            loginCommand:
              "CLAUDE_CONFIG_DIR=/home/me/.claude-work claude auth login",
          },
        ],
      },
      {
        id: "codex",
        name: "Codex",
        available: true,
        command: "codex",
        state: ready,
        loginCommand: "codex login",
        paused: false,
        accounts: [],
      },
    ],
  };
}

// task is a task of a wish, as the store keeps it: its agent, its account and its status.
function task(provider, account, status, kind = s.TaskKind.WORK) {
  return { provider, account, status, kind };
}

// signIns are the sign-ins of the order, as the markup shows them, top to bottom.
function signIns(html) {
  return [...html.matchAll(/data-sign-in="([^"]+)"/g)].map((m) => m[1]);
}

// lineOf is the markup of a sign-in's line in the order, up to the next one.
function lineOf(html, key) {
  const start = html.indexOf(`data-sign-in="${key}"`);
  assert.ok(start > 0, `${key} in the order`);
  const next = html.indexOf("data-sign-in=", start + 1);
  return html.slice(
    start,
    next > 0 ? next : html.indexOf('class="sign-in-said"', start),
  );
}

test("the Circle is a page: the agents, then the ordered accounts with what runs there, then the models, then the bulk change", () => {
  const { RUNNING, RESUMING, DONE } = s.TaskStatus;
  const tasks = [
    task(s.Provider.CLAUDE, "work", RUNNING),
    task(s.Provider.CLAUDE, "work", RUNNING),
    task(s.Provider.CLAUDE, "work", RESUMING),
    task(s.Provider.UNSPECIFIED, "", RUNNING),
    task(s.Provider.CLAUDE, "", DONE),
    task(s.Provider.WATCH, "", RUNNING),
    task(s.Provider.CLAUDE, "", RUNNING, s.TaskKind.AZIMA),
  ];
  const html = s.renderToStaticMarkup(
    h(s.SummoningCircle, {
      initialRows: [],
      initialAgents: {
        env: environment(),
        checking: false,
        error: "",
        check() {},
        setPaused() {},
        order: ["claude/work", "codex", "claude"],
        usage: [
          {
            provider: s.Provider.CLAUDE,
            account: "",
            next: true,
            fiveHour: { usedPercent: 22 },
            sevenDay: { usedPercent: 41 },
          },
        ],
      },
      initialTasks: tasks,
    }),
  );

  // A page with its header, as the Grimoire: no dialog.
  assert.match(html, /^<div class="wish-view summoning-page">/);
  assert.doesNotMatch(html, /role="dialog"/);
  assert.match(html, /<h1>Summoning Circle<\/h1>/);

  // The agents, then the order, then the models by kind of worker, then the change of the open tasks.
  const at = [
    'data-agent="claude"',
    "Accounts · order of priority",
    "Models by worker kind",
    '<div class="setting-row summoning-row">',
    'class="config-box summoning-bulk"',
  ].map((part) => html.indexOf(part));
  assert.ok(
    at.every((i, n) => i > 0 && (n === 0 || i > at[n - 1])),
    at.join(),
  );

  // The sign-ins in the developer's order, each with its grip and rank.
  assert.deepEqual(signIns(html), ["claude/work", "codex", "claude"]);
  assert.match(lineOf(html, "claude/work"), /<span class="sign-in-rank">1</);
  // What runs on each: two workers and a task waiting on work, one worker on the own sign-in; no watcher, no azima.
  const work = lineOf(html, "claude/work");
  assert.match(work, />2 workers</);
  assert.match(work, />1 task waits</);
  const own = lineOf(html, "claude");
  assert.match(own, />1 worker</);
  assert.doesNotMatch(own, /sign-in-waiting/);
  assert.match(own, />Next Claude Code task here</);
  assert.match(own, /aria-label="Share of the 5-hour session used"/);
  assert.doesNotMatch(lineOf(html, "codex"), /sign-in-workers/);
});

test("loadOf counts the workers and the waiting tasks of each sign-in, a task of no agent counted on Claude Code", () => {
  const { RUNNING, RESUMING, PENDING } = s.TaskStatus;
  const load = s.loadOf([
    task(s.Provider.UNSPECIFIED, "", RUNNING),
    task(s.Provider.CLAUDE, "", RESUMING),
    task(s.Provider.CODEX, "gpt", RUNNING),
    task(s.Provider.CODEX, "gpt", PENDING),
  ]);
  assert.deepEqual(load.get(`${s.Provider.CLAUDE}/`), {
    workers: 1,
    waiting: 1,
  });
  assert.deepEqual(load.get(`${s.Provider.CODEX}/gpt`), {
    workers: 1,
    waiting: 0,
  });
  assert.equal(s.timeLeft(2.5 * 3600_000 + 1), "2 h 31");
  assert.equal(s.timeLeft(45 * 60_000), "45 min");
  assert.equal(s.timeLeft(-5), "0 min");
});

test("in the Circle, a drop of a sign-in sends the whole order to Djinn", async () => {
  const container = setupMockDom();
  const below = (el, out = []) => {
    for (const child of el.childNodes) {
      out.push(child);
      below(child, out);
    }
    return out;
  };
  const props = (el) =>
    el[Object.keys(el).find((k) => k.startsWith("__reactProps$"))];
  const settle = async () => {
    for (let i = 0; i < 4; i++)
      await s.act(() => new Promise((r) => setTimeout(r, 0)));
  };
  const byLabel = (start) =>
    below(container).find((el) =>
      String(el.attributes?.["aria-label"] ?? "").startsWith(start),
    );
  const rowOf = (key) =>
    below(container).find((el) => el.attributes?.["data-sign-in"] === key);
  const shown = () =>
    below(container)
      .map((el) => el.attributes?.["data-sign-in"])
      .filter(Boolean)
      .map(String);
  let order = ["claude", "claude/work", "codex"];
  const calls = [];
  const transport = s.createRouterTransport(({ service }) => {
    service(s.UiService, { getEnvironment: () => environment() });
    service(s.LeadService, {
      accounts: () => ({ order, pinned: false }),
      order: (req) => {
        calls.push(req.signIn);
        order = req.signIn;
        return { signIn: req.signIn };
      },
      getCircle: () => ({ rows: [] }),
    });
  });
  const djinn = s.createDjinn(transport, 10);
  const root = s.createRoot(container);
  try {
    await s.act(async () => {
      root.render(h(s.DjinnProvider, { djinn }, h(s.SummoningCircle, {})));
    });
    await settle();
    assert.deepEqual(shown(), ["claude", "claude/work", "codex"]);

    // Drag Codex by its grip over the top half of the first line, and drop it there: first, the whole order sent.
    const data = { setData() {}, setDragImage() {}, dropEffect: "" };
    await s.act(async () =>
      props(byLabel("Change the priority of Codex")).onDragStart({
        dataTransfer: data,
      }),
    );
    await s.act(async () =>
      props(rowOf("claude")).onDragOver({
        currentTarget: {
          getBoundingClientRect: () => ({ top: 0, height: 40 }),
        },
        clientY: 5,
        dataTransfer: data,
        preventDefault() {},
      }),
    );
    const list = below(container).find((el) =>
      String(el.attributes?.class ?? "")
        .split(" ")
        .includes("sign-in-order"),
    );
    await s.act(async () => props(list).onDrop({ preventDefault() {} }));
    await settle();
    assert.deepEqual(calls, [["codex", "claude", "claude/work"]]);
    assert.deepEqual(shown(), ["codex", "claude", "claude/work"]);
  } finally {
    s.act(() => root.unmount());
    await new Promise((r) => setTimeout(r, 30));
    for (const handle of process._getActiveHandles())
      if (handle?.constructor?.name === "MessagePort") handle.unref();
  }
});

test("the lead in its terminal: when the Djinn's row names another agent, the Circle starts the Djinn there on demand (W578)", async () => {
  const lead = s.WorkerKind.LEAD;
  const models = [
    {
      id: "claude-opus-5-5",
      label: "Claude Opus 5.5",
      provider: s.Provider.CLAUDE,
    },
    { id: "gpt-5.5", label: "GPT-5.5", provider: s.Provider.CODEX },
  ];
  const agents = {
    env: environment(),
    checking: false,
    error: "",
    check() {},
    setPaused() {},
  };
  const circle = (rowProvider, initialLead) =>
    s.renderToStaticMarkup(
      h(s.SummoningCircle, {
        initialRows: [
          {
            kind: lead,
            provider: rowProvider,
            model:
              rowProvider === s.Provider.CODEX ? "gpt-5.5" : "claude-opus-5-5",
          },
        ],
        initialModels: models,
        initialAgents: agents,
        initialTasks: [],
        initialLead,
      }),
    );
  const recordedClaude = { recorded: s.Provider.CLAUDE, driven: false };
  const other = circle(s.Provider.CODEX, recordedClaude);
  assert.match(
    other,
    /<div class="summoning-lead-start"><p>Talk to the Djinn resumes its session on Claude\.<\/p><button type="button" class="button secondary small" title="A new session of the Djinn starts on this agent from the brief, and becomes the Djinn">Start the Djinn on Codex<\/button><\/div>/,
  );
  // The same agent as the recorded session's, no session recorded, or the lead driven: nothing to start.
  for (const markup of [
    circle(s.Provider.CLAUDE, recordedClaude),
    circle(s.Provider.CODEX, { recorded: undefined, driven: false }),
    circle(s.Provider.CODEX, { recorded: s.Provider.CLAUDE, driven: true }),
  ])
    assert.doesNotMatch(markup, /summoning-lead-start|Start the Djinn/);

  // A click starts the Djinn on Codex through LeadService.Resume, and shows what Djinn said.
  const container = setupMockDom();
  const below = (el, out = []) => {
    for (const child of el.childNodes) {
      out.push(child);
      below(child, out);
    }
    return out;
  };
  const props = (el) =>
    el[Object.keys(el).find((k) => k.startsWith("__reactProps$"))];
  const settle = async () => {
    for (let i = 0; i < 4; i++)
      await s.act(() => new Promise((r) => setTimeout(r, 0)));
  };
  const text = (el) =>
    el.nodeType === 3
      ? (el.nodeValue ?? "")
      : el.childNodes.length
        ? el.childNodes.map(text).join("")
        : (el.textContent ?? "");
  const resumed = [];
  const transport = s.createRouterTransport(({ service }) => {
    service(s.UiService, { getEnvironment: () => environment() });
    service(s.LeadService, {
      accounts: () => ({ pinned: false }),
      resume: (req) => {
        resumed.push(req.provider);
        return { note: "Codex leads now, from the brief." };
      },
    });
  });
  const djinn = s.createDjinn(transport, 10);
  const root = s.createRoot(container);
  try {
    await s.act(async () => {
      root.render(
        h(
          s.DjinnProvider,
          { djinn },
          h(s.SummoningCircle, {
            initialRows: [
              { kind: lead, provider: s.Provider.CODEX, model: "gpt-5.5" },
            ],
            initialModels: models,
            initialAgents: agents,
            initialTasks: [],
            initialLead: recordedClaude,
          }),
        ),
      );
    });
    const start = below(container).find(
      (el) =>
        el.tagName?.toLowerCase() === "button" &&
        text(el) === "Start the Djinn on Codex",
    );
    assert.ok(start, "the button to start the Djinn on Codex");
    await s.act(async () => props(start).onClick());
    await settle();
    assert.deepEqual(resumed, [s.Provider.CODEX]);
    const note = below(container).find(
      (el) => String(el.attributes?.class ?? "") === "summoning-lead-note",
    );
    assert.equal(note && text(note), "Codex leads now, from the brief.");
  } finally {
    s.act(() => root.unmount());
    await new Promise((r) => setTimeout(r, 30));
    for (const handle of process._getActiveHandles())
      if (handle?.constructor?.name === "MessagePort") handle.unref();
  }
});

test("leadReady: a session of the Djinn can start once an agent that leads is installed and signed in (W578)", () => {
  const { MISSING, SIGNED_OUT, READY, UNKNOWN } = s.ProviderState;
  const env = (claude, codex, antigravity) => ({
    providers: [
      { id: "claude", state: claude },
      { id: "codex", state: codex },
      { id: "antigravity", state: antigravity },
    ],
  });
  assert.equal(s.leadReady(env(MISSING, SIGNED_OUT, MISSING)), false);
  assert.equal(s.leadReady(env(READY, MISSING, MISSING)), true);
  assert.equal(s.leadReady(env(MISSING, UNKNOWN, MISSING)), true);
  // Antigravity leads in its terminal, not driven by Djinn.
  assert.equal(s.leadReady(env(MISSING, SIGNED_OUT, READY)), true);
  assert.equal(s.leadReady(env(MISSING, SIGNED_OUT, READY), true), false);
  // No agent told yet: nothing warns.
  assert.equal(s.leadReady({ providers: [] }), true);
});

test("the memory brake's threshold: 5 % by default among the choices, off at 0, fixed by djinn up's flag (W28)", () => {
  assert.deepEqual(s.brakeChoices(5), [0, 3, 5, 8, 10, 15, 20]);
  assert.deepEqual(s.brakeChoices(12), [0, 3, 5, 8, 10, 12, 15, 20]);
  const brake = (initial) =>
    s.renderToStaticMarkup(h(s.BrakeSection, { initial }));
  const free = brake({ percent: 5, fixed: false });
  assert.match(free, /<h3 class="summoning-section-title">Memory brake<\/h3>/);
  assert.match(free, /<option value="0">Off<\/option>/);
  assert.match(free, /<option value="5" selected="">5% \(default\)<\/option>/);
  assert.match(free, /Applies at once and holds for the next launches/);
  // Fixed by djinn up's flag: the list is closed, and says why.
  const fixed = brake({ percent: 12, fixed: true });
  assert.match(fixed, /<option value="12" selected="">12%<\/option>/);
  assert.match(fixed, /<select[^>]*disabled=""/);
  assert.match(fixed, /Set for this run by djinn up --brake-memory/);
  // The Circle shows it last, after the change of the open tasks.
  const page = s.renderToStaticMarkup(
    h(s.SummoningCircle, {
      initialRows: [],
      initialModels: [],
      initialAgents: {
        env: environment(),
        checking: false,
        error: "",
        check() {},
        setPaused() {},
      },
      initialTasks: [],
      initialBrake: { percent: 5, fixed: false },
    }),
  );
  assert.ok(
    page.indexOf("Memory brake") > page.indexOf("summoning-bulk"),
    "the brake after the bulk change",
  );
});

test("in the Circle, a threshold chosen goes to Djinn at once (LoadService.SetBrake), and the list shows Djinn's answer", async () => {
  const container = setupMockDom();
  const below = (el, out = []) => {
    for (const child of el.childNodes) {
      out.push(child);
      below(child, out);
    }
    return out;
  };
  const props = (el) =>
    el[Object.keys(el).find((k) => k.startsWith("__reactProps$"))];
  const settle = async () => {
    for (let i = 0; i < 4; i++)
      await s.act(() => new Promise((r) => setTimeout(r, 0)));
  };
  const select = () =>
    below(container).find(
      (el) =>
        el.attributes?.["aria-label"] ===
        "Freeze under this share of memory available",
    );
  const sets = [];
  const transport = s.createRouterTransport(({ service }) => {
    service(s.LoadService, {
      get: () => ({ brakeMemoryPercent: 5, brakeMemoryFixed: false }),
      setBrake: (req) => {
        sets.push(req.percent);
        return { brakeMemoryPercent: req.percent };
      },
    });
  });
  const djinn = s.createDjinn(transport, 10);
  const root = s.createRoot(container);
  try {
    await s.act(async () => {
      root.render(h(s.DjinnProvider, { djinn }, h(s.BrakeSection, {})));
    });
    await settle();
    assert.equal(props(select()).value, "5");
    await s.act(async () =>
      props(select()).onChange({ target: { value: "0" } }),
    );
    await settle();
    assert.deepEqual(sets, [0]);
    assert.equal(props(select()).value, "0");
    assert.equal(props(select()).disabled, false);
  } finally {
    s.act(() => root.unmount());
    await new Promise((r) => setTimeout(r, 30));
    for (const handle of process._getActiveHandles())
      if (handle?.constructor?.name === "MessagePort") handle.unref();
  }
});

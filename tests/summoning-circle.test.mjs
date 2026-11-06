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

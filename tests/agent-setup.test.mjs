// The setup of the agents (src/agent-setup.tsx): each agent's line, its accounts under it, their pause, their usage
// and the line that adds one. Run with `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { screens, setupMockDom } from "./screens.mjs";

const { s, h } = await screens();

const ready = 3; // ProviderState.READY
const missing = 1; // ProviderState.MISSING

// environment is what UiService.GetEnvironment answers: Claude Code ready on its own sign-in, with a paused account
// work and an account home; Codex not installed; Antigravity installed, its own sign-in paused.
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
              paused: true,
            },
            state: ready,
            loginCommand:
              "CLAUDE_CONFIG_DIR=/home/me/.claude-work claude auth login",
          },
          {
            account: {
              name: "home",
              provider: s.Provider.CLAUDE,
              label: "",
              configDirectory: "/home/me/.claude-home",
              paused: false,
            },
            state: ready,
            loginCommand:
              "CLAUDE_CONFIG_DIR=/home/me/.claude-home claude auth login",
          },
        ],
      },
      {
        id: "codex",
        name: "Codex",
        available: false,
        command: "codex",
        state: missing,
        installCommand: "npm install -g @openai/codex",
        loginCommand: "codex login",
        paused: false,
        accounts: [],
      },
      {
        id: "antigravity",
        name: "Antigravity",
        available: true,
        command: "agy",
        state: 4,
        loginCommand: "agy",
        paused: true,
        accounts: [],
      },
    ],
  };
}

// line is the markup of one line of the panel: the agent's (data-agent) or an account's (data-account).
function line(html, attribute, value) {
  const start = html.indexOf(`${attribute}="${value}"`);
  assert.ok(start > 0, `${attribute}=${value} in ${html}`);
  return html.slice(
    start,
    html.indexOf("</div>", html.indexOf("</div>", start) + 1),
  );
}

// usageLine is the markup of the line under a sign-in's: its state and its quota (data-usage, agent or agent/account).
function usageLine(html, id) {
  const start = html.indexOf(`data-usage="${id}"`);
  assert.ok(start > 0, `data-usage=${id} in ${html}`);
  return html.slice(start, html.indexOf("</div>", start));
}

// timestamp is a proto timestamp at date.
function timestamp(date) {
  return { seconds: BigInt(Math.floor(date.getTime() / 1000)), nanos: 0 };
}

// panel is the panel's markup for env and the usage Djinn read.
function panel(env, usage) {
  return s.renderToStaticMarkup(
    h(s.AgentSetupPanel, {
      agents: {
        env,
        checking: false,
        error: "",
        check() {},
        setPaused() {},
        usage,
      },
    }),
  );
}

test("each sign-in shows its pause and the button that changes it: the agent's own, each account; none for an agent missing", () => {
  const html = s.renderToStaticMarkup(
    h(s.AgentSetupPanel, {
      agents: {
        env: environment(),
        checking: false,
        error: "",
        check() {},
        setPaused() {},
      },
    }),
  );
  const claude = line(html, "data-agent", "claude");
  assert.match(
    claude,
    /<button type="button" class="button secondary small" title="Pause the agent&#x27;s own sign-in\nDjinn starts no worker on this sign-in: the next ones run on another sign-in of the agent, or wait while each one is paused\. A worker running there finishes its turn\." aria-label="Pause the agent&#x27;s own sign-in" aria-pressed="false">/,
  );
  assert.doesNotMatch(claude, /Paused/);
  assert.doesNotMatch(usageLine(html, "claude"), /Paused/);
  const work = line(html, "data-account", "work");
  assert.match(
    usageLine(html, "claude/work"),
    /<span class="status-text"><span class="status-dot neutral"><\/span>Paused<\/span>/,
  );
  assert.match(
    work,
    /aria-label="Resume this account" aria-pressed="true">[^]*?Resume<\/button>/,
  );
  const home = line(html, "data-account", "home");
  assert.match(home, /aria-label="Pause this account" aria-pressed="false"/);
  assert.doesNotMatch(usageLine(html, "claude/home"), /Paused/);
  // Codex is not installed: nothing to pause. Antigravity, with no account folder, pauses its own sign-in.
  assert.doesNotMatch(line(html, "data-agent", "codex"), /aria-pressed/);
  assert.doesNotMatch(html, /data-usage="codex"/);
  const agy = line(html, "data-agent", "antigravity");
  assert.match(usageLine(html, "antigravity"), /Paused/);
  assert.match(agy, /aria-label="Resume the agent&#x27;s own sign-in"/);
});

test("each sign-in shows its state, active, paused or limited, its week's gauge, when the week ends and its 5 hours", () => {
  const now = Date.now();
  const hour = 3600_000;
  const env = environment();
  env.providers[0].accounts[0].account.paused = false;
  const usage = [
    {
      provider: s.Provider.CLAUDE,
      account: "",
      sevenDay: {
        usedPercent: 27.4,
        resetsTime: timestamp(new Date(now + 30 * hour)),
      },
      fiveHour: { usedPercent: 44 },
      readTime: timestamp(new Date(now)),
      source: s.UsageSource.PROBE,
      probeError: "",
      paused: false,
      next: true,
    },
    {
      provider: s.Provider.CLAUDE,
      account: "work",
      sevenDay: {
        usedPercent: 96,
        resetsTime: timestamp(new Date(now + 50 * hour)),
      },
      fiveHour: { usedPercent: 100 },
      readTime: timestamp(new Date(now)),
      source: s.UsageSource.WORKER,
      probeError: "",
      paused: false,
      limitedUntil: timestamp(new Date(now + 2 * hour)),
      next: false,
    },
    {
      provider: s.Provider.CLAUDE,
      account: "home",
      probeError: "signed out",
      paused: false,
      next: false,
    },
  ];
  const html = panel(env, usage);

  // The agent's own: active, its week at 27 %, ending in 30 hours, its 5 hours at 44 %, the next task's.
  const own = usageLine(html, "claude");
  assert.match(own, /data-state="active"/);
  assert.match(own, /<span class="status-dot green"><\/span>Active<\/span>/);
  assert.match(
    own,
    /<span class="usage-gauge" role="meter" aria-label="Share of the week used" aria-valuemin="0" aria-valuemax="100" aria-valuenow="27"><span style="width:27%"><\/span><\/span><span class="usage-percent">27 %<\/span>/,
  );
  assert.match(own, /ends [^<]+/);
  assert.match(own, /5 h 44 %/);
  assert.match(own, /title="Read at [^"]+ by Djinn&#x27;s probe/);
  assert.match(own, />Next task here</);

  // work: limited until its reset, its week full past 95 %.
  const work = usageLine(html, "claude/work");
  assert.match(work, /data-state="limited"/);
  assert.match(
    work,
    /<span class="status-dot blocked"><\/span>Limited until [^<]+<\/span>/,
  );
  assert.match(work, /class="usage-gauge full"/);
  assert.match(work, /title="Read at [^"]+ from a worker running here"/);
  assert.doesNotMatch(work, /Next task here/);

  // home: active, its quota not read, the probe's failure on hover.
  const home = usageLine(html, "claude/home");
  assert.match(home, /data-state="active"/);
  assert.match(home, /title="signed out">Quota could not be read</);
  assert.doesNotMatch(home, /usage-gauge/);

  // A pause wins over a limit; a sign-in not signed in has no state, only its quota.
  env.providers[0].accounts[1].account.paused = true;
  env.providers[0].accounts[0].state = 2; // ProviderState.SIGNED_OUT
  usage[2] = { ...usage[1], account: "home" };
  usage[1] = { ...usage[1], limitedUntil: undefined };
  const again = panel(env, usage);
  assert.match(usageLine(again, "claude/home"), /data-state="paused"/);
  assert.doesNotMatch(usageLine(again, "claude/home"), /Limited/);
  assert.doesNotMatch(usageLine(again, "claude/work"), /data-state=/);
  assert.match(usageLine(again, "claude/work"), /usage-gauge full/);

  // Djinn reads no Codex quota: a Codex account says its state alone, a Claude Code one that its quota is not read yet.
  const codex = environment();
  Object.assign(codex.providers[1], { available: true, state: ready });
  codex.providers[1].accounts = [
    {
      account: {
        name: "gpt",
        provider: s.Provider.CODEX,
        label: "",
        configDirectory: "/home/me/.codex-gpt",
        paused: false,
      },
      state: ready,
      loginCommand: "CODEX_HOME=/home/me/.codex-gpt codex login",
    },
  ];
  const unread = panel(codex, []);
  assert.match(usageLine(unread, "codex/gpt"), /data-state="active"/);
  assert.doesNotMatch(usageLine(unread, "codex/gpt"), /Quota/);
  assert.match(usageLine(unread, "claude/home"), />Quota not read yet</);
});

test("usageOf finds a sign-in's usage by its agent and its account, the agent's own by none", () => {
  const usage = [
    { provider: s.Provider.CLAUDE, account: "" },
    { provider: s.Provider.CLAUDE, account: "work" },
    { provider: s.Provider.CODEX, account: "work" },
  ];
  assert.equal(s.usageOf(usage, "claude", ""), usage[0]);
  assert.equal(s.usageOf(usage, "claude", "work"), usage[1]);
  assert.equal(s.usageOf(usage, "codex", "work"), usage[2]);
  assert.equal(s.usageOf(usage, "codex", ""), undefined);
  assert.equal(s.usageOf(undefined, "claude", ""), undefined);
});

test("withPaused changes one sign-in's pause and leaves the others as they were", () => {
  const env = environment();
  const own = s.withPaused(env, "claude", "", true);
  assert.equal(own.providers[0].paused, true);
  assert.equal(own.providers[0].accounts[0].account.paused, true);
  assert.equal(own.providers[2].paused, true);
  assert.equal(
    env.providers[0].paused,
    false,
    "the environment read stays as it was",
  );
  const work = s.withPaused(env, "claude", "work", false);
  assert.equal(work.providers[0].accounts[0].account.paused, false);
  assert.equal(work.providers[0].accounts[1].account.paused, false);
  assert.equal(work.providers[0].paused, false);
  assert.equal(work.providers[0].accounts[1], env.providers[0].accounts[1]);
});

test("the panel adds an account the way the command line does, and pauses and resumes sign-ins through the lead's service", async () => {
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
  const byLabel = (label) =>
    below(container).find((el) => el.attributes?.["aria-label"] === label);
  const calls = [];
  const transport = s.createRouterTransport(({ service }) => {
    service(s.LeadService, {
      addAccount: (req) => {
        calls.push(["add", req.name, req.provider, req.label]);
        return {};
      },
      pauseAccount: (req) => {
        calls.push(["pause", req.name, req.provider]);
        return { note: "" };
      },
      resumeAccount: (req) => {
        calls.push(["resume", req.name, req.provider]);
        return { note: "" };
      },
    });
  });
  const djinn = s.createDjinn(transport, 10);
  let checks = 0;
  function Panel() {
    const [env, setEnv] = s.useState(environment);
    return h(s.AgentSetupPanel, {
      agents: {
        env,
        checking: false,
        error: "",
        check() {
          checks++;
        },
        setPaused: (agentId, account, paused) =>
          setEnv((e) => s.withPaused(e, agentId, account, paused)),
      },
    });
  }
  const root = s.createRoot(container);
  try {
    await s.act(async () => {
      root.render(h(s.DjinnProvider, { djinn }, h(Panel)));
    });
    // Pause Claude Code's own sign-in, then resume the account work.
    await s.act(async () =>
      props(byLabel("Pause the agent's own sign-in")).onClick(),
    );
    await settle();
    await s.act(async () => props(byLabel("Resume this account")).onClick());
    await settle();
    assert.deepEqual(calls, [
      ["pause", "", s.Provider.CLAUDE],
      ["resume", "work", s.Provider.CLAUDE],
    ]);
    // Each line shows its new pause at once, without checking the agents again.
    const resumes = below(container).filter(
      (el) =>
        el.attributes?.["aria-label"] === "Resume the agent's own sign-in",
    );
    assert.equal(
      resumes.length,
      2,
      "Claude Code's and Antigravity's own sign-ins",
    );
    assert.ok(byLabel("Pause this account"));
    assert.equal(
      below(container).filter(
        (el) => el.attributes?.["aria-label"] === "Pause this account",
      ).length,
      2,
      "work and home",
    );
    assert.equal(checks, 0);

    // Add an account: its agent, its name, its label, as djinn lead add-account takes them; then the agents are checked
    // again, its line to come with its sign-in.
    await s.act(async () =>
      props(byLabel("The account's agent")).onChange({
        target: { value: String(s.Provider.CODEX) },
      }),
    );
    await s.act(async () =>
      props(byLabel("Name, such as work")).onChange({
        target: { value: " gpt " },
      }),
    );
    await s.act(async () =>
      props(byLabel("Label, such as Work")).onChange({
        target: { value: "GPT" },
      }),
    );
    const form = below(container).find((el) => el.nodeName === "FORM");
    await s.act(async () => props(form).onSubmit({ preventDefault() {} }));
    await settle();
    assert.deepEqual(calls.at(-1), ["add", "gpt", s.Provider.CODEX, "GPT"]);
    assert.equal(checks, 1);
  } finally {
    s.act(() => root.unmount());
    await new Promise((r) => setTimeout(r, 30));
    for (const handle of process._getActiveHandles())
      if (handle?.constructor?.name === "MessagePort") handle.unref();
  }
});

test("the panel reads each sign-in's usage from the lead's service with the agents, and again after a pause", async () => {
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
  let reads = 0;
  const transport = s.createRouterTransport(({ service }) => {
    service(s.UiService, {
      getEnvironment: () => environment(),
    });
    service(s.LeadService, {
      accounts: () => {
        reads++;
        return {
          usage: [
            {
              provider: s.Provider.CLAUDE,
              account: "home",
              sevenDay: { usedPercent: 61 },
              next: true,
            },
          ],
        };
      },
      pauseAccount: () => ({ note: "" }),
    });
  });
  const djinn = s.createDjinn(transport, 10);
  function Panel() {
    return h(s.AgentSetupPanel, { agents: s.useAgents() });
  }
  const root = s.createRoot(container);
  try {
    await s.act(async () => {
      root.render(h(s.DjinnProvider, { djinn }, h(Panel)));
    });
    await settle();
    assert.equal(reads, 1);
    const meter = below(container).find(
      (el) => el.attributes?.role === "meter",
    );
    assert.ok(meter, "home's week gauge");
    assert.equal(String(meter.attributes["aria-valuenow"]), "61");
    const home = below(container).find(
      (el) => el.attributes?.["data-usage"] === "claude/home",
    );
    assert.equal(home.attributes["data-state"], "active");
    // A pause may change the sign-in the next task takes: the usage is read again.
    await s.act(async () =>
      props(
        below(container).find(
          (el) => el.attributes?.["aria-label"] === "Pause this account",
        ),
      ).onClick(),
    );
    await settle();
    assert.equal(reads, 2);
  } finally {
    s.act(() => root.unmount());
    await new Promise((r) => setTimeout(r, 30));
    for (const handle of process._getActiveHandles())
      if (handle?.constructor?.name === "MessagePort") handle.unref();
  }
});

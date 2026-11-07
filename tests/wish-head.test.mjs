// A wish's head (src/wish-head.tsx): its title and description, its lead, its pushes, where its work lands. Run with
// `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { screens } from "./screens.mjs";

const { s, h, live } = await screens();

test("Lead is one button: Talk to the Djinn resumes the one lead, focused on the wish; no agent selector beside it (W578)", () => {
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
  const props = { onLead() {} };

  // Talk to the Djinn alone: the agents, the models and the accounts are chosen in the Summoning Circle.
  const closed = s.renderToStaticMarkup(h(s.LeadButton, props));
  assert.equal(
    closed.replace(/<svg[^]*?<\/svg>/, "<svg/>"),
    '<button class="button secondary small" title="Opens the Djinn&#x27;s terminal, focused on this wish"><svg/><span>Talk to the Djinn</span></button>',
  );
  assert.doesNotMatch(
    closed,
    /Choose the agent|aria-haspopup|role="menu"|Set up the agents/,
  );
  // On the flying carpet, on no wish: the button does not speak of a wish.
  const carpet = s.renderToStaticMarkup(
    h(s.LeadButton, { ...props, focused: false }),
  );
  assert.match(
    carpet,
    /<button class="button secondary small" title="Opens the Djinn&#x27;s terminal">/,
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

test("the wish's head says a push its checks hold, why on hover, each branch's", () => {
  const held = (holds) =>
    s.renderToStaticMarkup(
      h(s.LastPushes, {
        pushes: [{ projectId: "p1", refused: "", holds }],
        projects: [{ id: "p1", name: "app" }],
      }),
    );
  assert.match(
    held({
      "feat/qol": { held: "at 1a2b3c4d, test: go tool task test exited 1" },
    }),
    /<span class="wish-push-refused" title="at 1a2b3c4d, test: go tool task test exited 1">The push is held: its checks are red<\/span>/,
  );
  // Each branch held keeps its hold (W37): its title names its branch once there are several.
  const two = held({
    "djinn/T2-second": { held: "at 2b3c4d5e, lint: exited 1" },
    "djinn/T1-first": { held: "at 1a2b3c4d, test: exited 1" },
  });
  assert.match(
    two,
    /title="djinn\/T1-first\n\nat 1a2b3c4d, test: exited 1">The push is held: its checks are red<\/span><span class="wish-push-refused" title="djinn\/T2-second\n\nat 2b3c4d5e, lint: exited 1">/,
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

  // WishModels in the wish header opens the bulk model switcher scoped to this wish.
  const modelsChip = live(s.WishModels, { wishId: "w1" });
  assert.equal(modelsChip.all("wish-models-picker").length, 0);
  assert.equal(modelsChip.one("wish-models")["aria-expanded"], false);
  modelsChip.one("wish-models").onClick();
  assert.equal(modelsChip.one("wish-models")["aria-expanded"], true);
  assert.equal(modelsChip.all("wish-models-picker").length, 1);
  assert.equal(modelsChip.one(s.UpdateModelsSection).wishId, "w1");
  assert.match(
    modelsChip.html(),
    /<div class="branch-picker wish-models-picker" role="dialog" aria-label="Change open tasks in one gesture"><div class="update-models-section">/,
  );
});

test("seatName names a seat as the attention bar says it: the agent, then its account's label", () => {
  const accounts = [
    {
      name: "work",
      provider: s.Provider.CLAUDE,
      label: "Work",
      configDirectory: "/home/d/.claude-work",
    },
    {
      name: "gpt",
      provider: s.Provider.CODEX,
      label: "",
      configDirectory: "/home/d/.codex-gpt",
    },
  ];
  assert.equal(
    s.seatName({ provider: s.Provider.CODEX, account: "gpt" }, accounts),
    "Codex · gpt",
  );
  assert.equal(
    s.seatName({ provider: s.Provider.CLAUDE, account: "work" }, accounts),
    "Claude Code · Work",
  );
  assert.equal(
    s.seatName({ provider: s.Provider.CLAUDE, account: "" }, accounts),
    "Claude Code",
  );
});

test("driven, Lead has neither Talk to the Djinn nor a seat's chip: its batch and journal alone, the seat chosen in the Circle (W578)", () => {
  const markup = s.renderToStaticMarkup(
    h(s.LeadButton, {
      trouble: { text: "The Djinn could not start: boom", failed: true },
      drive: {},
      onLead() {},
    }),
  );
  assert.equal(markup, "");
  assert.doesNotMatch(markup, /Talk to the Djinn|lead-dot|lead-seat|menu/);
});

test("next to Lead, Djinn shows the running batch (since when, events, cost) and a failed batch's retry", () => {
  const nowSec = BigInt(Math.floor(Date.now() / 1000) - 12);
  const running = {
    id: "b-run",
    state: s.LeadBatchState.RUNNING,
    startTime: { seconds: nowSec, nanos: 0 },
    provider: s.Provider.CLAUDE,
    model: "claude-opus-4-6",
    events: ["task W1 DONE", "question Q2 ANSWERED"],
    attempt: 1,
    usage: {
      inputTokens: 1200n,
      outputTokens: 300n,
      costUsd: 0.05,
    },
  };
  const runMarkup = s.renderToStaticMarkup(
    h(s.LeadBatchStatus, { batch: running }),
  );
  assert.match(runMarkup, /class="lead-batch running"/);
  assert.match(runMarkup, /since /);
  assert.match(runMarkup, /2 events/);
  assert.match(runMarkup, /\$0\.05/);
  assert.match(runMarkup, /task W1 DONE\nquestion Q2 ANSWERED/);

  const failed = {
    id: "b-fail",
    state: s.LeadBatchState.FAILED,
    startTime: { seconds: nowSec - 30n, nanos: 0 },
    endTime: { seconds: nowSec - 5n, nanos: 0 },
    provider: s.Provider.CLAUDE,
    model: "claude-sonnet-5-5",
    events: ["wish Created: Lamp"],
    error: "rate limit exceeded",
    attempt: 2,
    usage: {
      inputTokens: 100000n,
      outputTokens: 10000n,
      costUsd: 0,
    },
  };
  const failMarkup = s.renderToStaticMarkup(
    h(s.LeadBatchStatus, { batch: failed }),
  );
  assert.match(failMarkup, /class="lead-batch failed"/);
  // The failure says what it is; Retry is on its line in the lead's journal on the side (Decision Q115 B).
  assert.match(failMarkup, /try 2 failed<\/span>/);
  assert.doesNotMatch(failMarkup, /Talk to the Djinn/);
  assert.match(failMarkup, /≈ \$0\.30/);
  assert.match(failMarkup, /rate limit exceeded/);
});

test("next to Lead, the journal lists past batches (model, session, launches, last sentence, cost, retry)", () => {
  const nowSec = BigInt(Math.floor(Date.now() / 1000) - 60);
  const batches = [
    {
      id: "b2",
      state: s.LeadBatchState.DONE,
      startTime: { seconds: nowSec, nanos: 0 },
      endTime: { seconds: nowSec + 10n, nanos: 0 },
      provider: s.Provider.CLAUDE,
      model: "claude-opus-4-6",
      sessionId: "abcdef1234567890",
      events: ["task W1 DONE"],
      commands: ["task spawn", "wish update"],
      note: "Merged W1 and spawned W2 for the window.",
      attempt: 1,
      usage: {
        inputTokens: 2000n,
        outputTokens: 400n,
        costUsd: 0.08,
      },
    },
    {
      id: "b1",
      state: s.LeadBatchState.FAILED,
      startTime: { seconds: nowSec - 120n, nanos: 0 },
      endTime: { seconds: nowSec - 100n, nanos: 0 },
      provider: s.Provider.CODEX,
      model: "gpt-5.5",
      sessionId: "9876543210fedcba",
      events: ["wish Created"],
      commands: [],
      error: "connection reset",
      attempt: 3,
      usage: {
        inputTokens: 100000n,
        outputTokens: 0n,
        costUsd: 0,
      },
    },
  ];

  const journalMarkup = s.renderToStaticMarkup(h(s.LeadJournal, { batches }));
  assert.match(journalMarkup, /claude-opus-4-6/);
  assert.match(journalMarkup, /abcdef12/);
  assert.match(
    journalMarkup,
    /<code>task spawn<\/code><code>wish update<\/code>/,
  );
  assert.match(journalMarkup, /Merged W1 and spawned W2 for the window\./);
  assert.match(journalMarkup, /\$0\.08/);
  assert.match(journalMarkup, /gpt-5\.5/);
  assert.match(journalMarkup, /98765432/);
  assert.match(journalMarkup, /try 3/);
  assert.match(journalMarkup, /failed/);
  assert.match(journalMarkup, /connection reset/);
  assert.match(journalMarkup, /≈ \$0\.50/);

  // Inside LeadButton: shows the running/failed batch chip and the Journal button next to Lead.
  const buttonMarkup = s.renderToStaticMarkup(
    h(s.LeadButton, {
      journalOpen: true,
      drive: {
        batch: { ...batches[0], state: s.LeadBatchState.RUNNING },
        journal: batches,
      },
      onLead() {},
    }),
  );
  assert.match(buttonMarkup, /class="lead-batch running"/);
  assert.match(
    buttonMarkup,
    /class="button secondary small lead-journal-button"/,
  );
  assert.match(buttonMarkup, /aria-label="Lead journal"/);
  assert.match(buttonMarkup, /<span>2<\/span>/);
  assert.match(buttonMarkup, /class="lead-journal-popover"/);
});

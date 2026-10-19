// A task's card (src/wish-task.tsx): its status, model, usage, prompt, resources, its marks and its buttons. Run with
// `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { resources, screens, usage } from "./screens.mjs";

const { s, h } = await screens();
const { hourglassDjinn } = resources(s);

test("a worker the scheduler flagged to relaunch shows a mark beside its code, with why and the command (W188)", () => {
  const card = (status, health) =>
    s.renderToStaticMarkup(
      h(s.WishTask, {
        task: {
          id: "01a14710-0169-7580-8e84-704c8214cdb5",
          code: "W1",
          title: "Trim the wick",
          status,
          provider: s.Provider.CLAUDE,
          health,
        },
        onStop() {},
        async onSend() {},
      }),
    );
  const stalled = card(s.TaskStatus.RUNNING, {
    state: s.HealthState.STALLED,
    reason: "silent 12 min, CPU 1%",
  });
  assert.match(
    stalled,
    /<span class="agent-code">W1<\/span><span class="health-mark stalled" role="img" aria-label="Stalled" title="Stalled: silent 12 min, CPU 1%. The lead relaunches it with: djinn task continue 01a14710-0169-7580-8e84-704c8214cdb5">/,
  );
  const retriable = card(s.TaskStatus.FAILED, {
    state: s.HealthState.RETRIABLE,
    reason: "a crash: claude ended before the end of its turn",
  });
  assert.match(
    retriable,
    /class="health-mark retriable" role="img" aria-label="Can be relaunched" title="Can be relaunched: a crash: claude ended before the end of its turn\./,
  );
  // Waiting for its limit, past a limit Djinn resumes it from, or well: no mark.
  for (const health of [
    { state: s.HealthState.LIMITED, reason: "the account's session limit" },
    { state: s.HealthState.LIMIT_LIFTED, reason: "its account changed" },
    undefined,
  ]) {
    assert.doesNotMatch(card(s.TaskStatus.RESUMING, health), /health-mark/);
  }
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

test("a task to run or running is made urgent from its card, its mark taken off the same way; not an ended one (W234)", () => {
  const card = (task, onUrge) =>
    s.renderToStaticMarkup(
      h(s.WishTask, {
        task: {
          id: "t1",
          code: "W1",
          title: "Trim the wick",
          provider: s.Provider.CLAUDE,
          ...task,
        },
        onStop() {},
        async onSend() {},
        onUrge,
      }),
    );
  const urge = () => {};
  for (const status of [
    s.TaskStatus.PENDING,
    s.TaskStatus.WAITING,
    s.TaskStatus.PAUSED,
    s.TaskStatus.RUNNING,
  ]) {
    assert.match(
      card({ status }, urge),
      /<button class="icon-button " title="Make it urgent for your quick feedback: [^"]+" aria-label="Urgent" aria-pressed="false"><svg[^>]*lucide-zap/,
      s.TaskStatus[status],
    );
    // Urgent, a repair or for feedback: the toggle is pressed, and takes the mark off.
    for (const feedback of [false, true])
      assert.match(
        card({ status, urgent: true, feedback }, urge),
        /<button class="icon-button active" title="Take its urgent mark off: it waits its turn again" aria-label="Urgent" aria-pressed="true">/,
      );
  }
  for (const task of [
    { status: s.TaskStatus.DONE },
    { status: s.TaskStatus.FAILED, urgent: true },
    { status: s.TaskStatus.RUNNING, provider: s.Provider.WATCH },
    { status: s.TaskStatus.RUNNING, kind: s.TaskKind.INVESTIGATION },
  ])
    assert.doesNotMatch(card(task, urge), /aria-label="Urgent"/);
  // Without onUrge, no toggle.
  assert.doesNotMatch(
    card({ status: s.TaskStatus.RUNNING }, undefined),
    /aria-label="Urgent"/,
  );
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
    /<button type="button" class="button secondary small wish-task-worktree-button" disabled="" title="This task has no worktree">See uncommitted files<\/button>/,
  );
  const withWorktree = s.renderToStaticMarkup(
    h(s.WishTask, {
      task: { ...task, worktree: "/tmp/w1" },
      focused: true,
      onStop() {},
    }),
  );
  assert.doesNotMatch(withWorktree, /disabled=""[^>]*>See uncommitted files/);
  // Logs and diff are deferred: containers are not rendered until toggled, the diff's review until a button opens it.
  assert.doesNotMatch(opened, /class="wish-task-events"/);
  assert.doesNotMatch(opened, /task-diff-view/);
  assert.doesNotMatch(opened, /task-review/);
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

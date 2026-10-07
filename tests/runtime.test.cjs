"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");

const runtime = require("../electron/runtime.cjs");
const { taskFixture } = require("./workflow-fixture.cjs");

test("large context and human steering cannot crowd out current exit criteria", () => {
  const prompt = runtime.composeRunPrompt({
    taskId: "context-budget",
    provider: "codex",
    cwd: "/tmp/project",
    mode: "execute",
    prompt: "Historical context ".repeat(4500),
    stepId: "implementation",
    guidance: [
      { id: "latest", text: "Priorité humaine ".repeat(400) },
      { id: "second", text: "Autre précision ".repeat(400) },
    ],
    step: {
      id: "implementation",
      type: "implementation",
      title: "Implémenter",
      objective: "Tester le ticket demandé",
      status: "running",
      exitCriteria: ["CURRENT_EXIT_CRITERION"],
      expectedArtifacts: [],
      skills: [],
    },
  });
  assert.ok(prompt.length <= runtime.MAX_COMPOSED_PROMPT_LENGTH);
  assert.match(prompt, /Priorité humaine/);
  assert.match(prompt, /CURRENT_EXIT_CRITERION/);
  assert.match(prompt, /exactly one step_result/);
});

test("builds constrained provider arguments", () => {
  assert.deepEqual(
    runtime.buildProviderArgs("codex", "make a plan", "o4-mini"),
    [
      "exec",
      "--json",
      "--sandbox",
      "workspace-write",
      "--skip-git-repo-check",
      "--model",
      "o4-mini",
      "--",
      "make a plan",
    ],
  );
  const claudeArgs = runtime.buildProviderArgs(
    "claude",
    "review this",
    "sonnet",
  );
  assert.deepEqual(claudeArgs, [
    "-p",
    "--output-format",
    "stream-json",
    "--verbose",
    "--model",
    "sonnet",
    "--",
    "review this",
  ]);
  assert.equal(claudeArgs.includes("--dangerously-skip-permissions"), false);
  const claudePlanArgs = runtime.buildProviderArgs(
    "claude",
    "inspect only",
    undefined,
    "plan",
  );
  assert.equal(claudePlanArgs.includes("--append-system-prompt"), true);
  assert.equal(
    claudePlanArgs.includes("--dangerously-skip-permissions"),
    false,
  );
  const claudeReviewArgs = runtime.buildProviderArgs(
    "claude",
    "review only",
    undefined,
    "review",
  );
  assert.equal(claudeReviewArgs.includes("Read,Glob,Grep"), true);
  assert.equal(claudeReviewArgs.includes("workspace-write"), false);
  assert.deepEqual(
    runtime.buildProviderArgs("codex", "inspect only", undefined, "plan"),
    [
      "exec",
      "--json",
      "--sandbox",
      "read-only",
      "--skip-git-repo-check",
      "--",
      "inspect only",
    ],
  );
  const planInvocation = runtime.buildProviderInvocation({
    taskId: "plan-task",
    provider: "codex",
    cwd: "/tmp/project",
    prompt: "Inspect only",
    mode: "plan",
  });
  assert.equal(planInvocation.args.includes("read-only"), true);
});

test("validates attached image bytes and builds provider-specific image input", () => {
  const png = `data:image/png;base64,${Buffer.from("\x89PNG\r\n\x1a\n", "binary").toString("base64")}`;
  const input = runtime.validateRunInput({
    taskId: "task-images",
    provider: "codex",
    cwd: "/tmp/project",
    prompt: "Review this screenshot",
    images: [{ id: "screen-1", title: "Screen", dataUrl: png }],
  });
  assert.equal(input.images[0].mediaType, "image/png");
  const codex = runtime.buildProviderInvocation(input, {
    imagePaths: ["/tmp/run/screen-1.png"],
  });
  const imageIndex = codex.args.indexOf("--image");
  assert.deepEqual(codex.args.slice(imageIndex, imageIndex + 2), [
    "--image",
    "/tmp/run/screen-1.png",
  ]);
  assert.equal(codex.args.at(-2), "--");
  assert.equal(codex.stdinText, undefined);

  const claudeInput = runtime.validateRunInput({
    ...input,
    provider: "claude",
  });
  const claude = runtime.buildProviderInvocation(claudeInput);
  assert.deepEqual(claude.args, [
    "-p",
    "--input-format",
    "stream-json",
    "--output-format",
    "stream-json",
    "--verbose",
  ]);
  const message = JSON.parse(claude.stdinText);
  assert.equal(message.parent_tool_use_id, null);
  assert.equal(message.session_id, "");
  assert.equal(message.message.content[1].source.media_type, "image/png");
  assert.throws(
    () =>
      runtime.validateRunInput({
        ...input,
        images: [{ id: "bad", dataUrl: "data:image/png;base64,/9j=" }],
      }),
    /image/,
  );
});

test("composes explicit mode and resume context without launching anything", () => {
  const prompt = runtime.composeRunPrompt({
    taskId: "task-1",
    provider: "codex",
    cwd: "/tmp/project",
    prompt: "Continue the implementation",
    mode: "review",
    decisions: ["Keep the existing API"],
    priorSummaries: ["The first pass added the parser"],
  });
  assert.match(prompt, /review mode/);
  assert.match(prompt, /Keep the existing API/);
  assert.match(prompt, /first pass added the parser/);
  assert.match(prompt, /DJINN_EVENT:/);
});

test("validates bounded concurrency and carries human guidance into prompts", () => {
  const input = runtime.validateRunInput({
    taskId: "task-guidance",
    provider: "codex",
    cwd: "/tmp/project",
    prompt: "Inspect the project",
    mode: "plan",
    concurrency: 3,
    guidance: [{ id: "g1", text: "Focus on keyboard navigation." }],
  });
  assert.equal(input.concurrency, 3);
  assert.match(runtime.composeRunPrompt(input), /keyboard navigation/);
  const targeted = runtime.validateRunInput({
    taskId: "task-targeted-guidance",
    provider: "codex",
    cwd: "/tmp/project",
    prompt: "Inspect the project",
    guidance: [
      { id: "g-target", text: "Check the worker output.", agentId: "worker-2" },
    ],
  });
  assert.deepEqual(targeted.guidance, [
    { id: "g-target", text: "Check the worker output.", agentId: "worker-2" },
  ]);
  assert.match(
    runtime.composeRunPrompt(targeted),
    /\[agent worker-2\] Check the worker output/,
  );
  assert.deepEqual(
    runtime.validateSteerInput({
      runId: "run-1",
      id: "g2",
      text: "Keep the copy concise.",
    }),
    {
      runId: "run-1",
      id: "g2",
      text: "Keep the copy concise.",
    },
  );
  assert.deepEqual(
    runtime.validateSteerInput({
      runId: "run-1",
      id: "g3",
      text: "Only the review agent needs this.",
      agentId: "review-agent",
    }),
    {
      runId: "run-1",
      id: "g3",
      text: "Only the review agent needs this.",
      agentId: "review-agent",
    },
  );
  assert.throws(
    () =>
      runtime.validateRunInput({
        taskId: "bad-concurrency",
        provider: "codex",
        cwd: "/tmp",
        prompt: "x",
        concurrency: 17,
      }),
    /concurrency/,
  );
  assert.throws(
    () => runtime.validateSteerInput({ runId: "run-1", id: "g2", text: "" }),
    /text/,
  );
});

test("validates separate CPU and memory resource bounds and enforces capacity", () => {
  const capacity = { cpu: 4, memoryMb: 16384 };
  const agents = Array.from({ length: 4 }, (_, index) => ({
    id: `memory-${index + 1}`,
    prompt: "Inspect one area",
    readOnly: true,
    resources: { memoryMb: 4096 },
  }));
  const input = runtime.validateRunInput({
    taskId: "task-resources",
    provider: "codex",
    cwd: "/tmp/project",
    prompt: "Inspect the project",
    mode: "review",
    concurrency: 4,
    resourcePolicy: { mode: "fixed", capacity },
    agents,
  });
  assert.deepEqual(input.resourcePolicy.capacity, { ...capacity, labels: [] });
  const plan = runtime.buildExecutionPlan(input);
  assert.deepEqual(
    plan.waves.map((wave) => wave.agents.map((agent) => agent.id)),
    [["memory-1", "memory-2", "memory-3", "memory-4"]],
  );

  assert.throws(
    () =>
      runtime.validateRunInput({
        taskId: "bad-memory-capacity",
        provider: "codex",
        cwd: "/tmp/project",
        prompt: "Inspect the project",
        resourcePolicy: {
          capacity: { cpu: 4, memoryMb: Number.MAX_SAFE_INTEGER + 1 },
        },
      }),
    /memoryMb/,
  );
  assert.throws(
    () =>
      runtime.validateRunInput({
        taskId: "bad-cpu-capacity",
        provider: "codex",
        cwd: "/tmp/project",
        prompt: "Inspect the project",
        resourcePolicy: { capacity: { cpu: 1025, memoryMb: 16384 } },
      }),
    /cpu/,
  );
  assert.throws(
    () =>
      runtime.buildExecutionPlan({
        taskId: "too-large-memory",
        provider: "codex",
        cwd: "/tmp/project",
        prompt: "Inspect the project",
        mode: "review",
        resourcePolicy: { mode: "fixed", capacity },
        agents: [
          { id: "large", prompt: "Inspect", resources: { memoryMb: 16385 } },
        ],
      }),
    /memoryMb/,
  );
});

test("parses valid DJINN_EVENT markers and leaves surrounding text intact", () => {
  const parsed = runtime.parseDjinnEvents(
    'Before DJINN_EVENT:{"type":"question","data":{"id":"q1","title":"Choose"}} after',
  );
  assert.equal(parsed.events.length, 1);
  assert.deepEqual(parsed.events[0], {
    type: "question",
    data: { id: "q1", title: "Choose" },
  });
  assert.equal(parsed.text, "Before  after");
});

test("ignores malformed and unsupported DJINN_EVENT markers as model text", () => {
  const unsupported = runtime.parseDjinnEvents(
    'DJINN_EVENT:{"type":"status","data":{"status":"running"}}',
  );
  assert.deepEqual(unsupported.events, []);
  assert.match(unsupported.text, /DJINN_EVENT/);

  const malformed = runtime.parseDjinnEvents('DJINN_EVENT:{"type":"note"');
  assert.deepEqual(malformed.events, []);
  assert.match(malformed.text, /DJINN_EVENT/);
  const invalidArtifact = runtime.parseDjinnEvents(
    'DJINN_EVENT:{"type":"artifact","data":{"type":"shell","content":"x"}}',
  );
  assert.deepEqual(invalidArtifact.events, []);
});

test("normalizes Codex JSONL and Claude stream-json records", () => {
  const codex = runtime.parseProviderLine(
    "codex",
    JSON.stringify({
      type: "thread.started",
      thread_id: "thread-1",
    }),
  );
  assert.equal(codex[0].type, "status");
  assert.equal(codex[0].data.providerRunId, "thread-1");

  const tool = runtime.parseProviderLine(
    "codex",
    JSON.stringify({
      type: "item.completed",
      item: {
        type: "command_execution",
        id: "tool-1",
        command: "npm test",
        aggregated_output: "ok",
      },
    }),
  );
  assert.equal(tool[0].type, "tool");
  assert.equal(tool[0].data.callId, "tool-1");
  assert.equal(tool[0].data.output, "ok");

  const claude = runtime.parseProviderLine(
    "claude",
    JSON.stringify({
      type: "assistant",
      message: {
        content: [
          {
            type: "text",
            text: 'Done DJINN_EVENT:{"type":"note","data":{"title":"Ready"}}',
          },
        ],
      },
    }),
  );
  assert.equal(claude[0].type, "note");
  assert.equal(claude[0].data.title, "Ready");
  assert.equal(claude[1].type, "text");
  assert.equal(claude[1].data.text, "Done");
});

test("accepts portable session arrays and the single-task compatibility wrapper", () => {
  const task = taskFixture();
  const expected = runtime.validateTask(task);
  assert.deepEqual(
    runtime.validateSession({ version: 1, tasks: [task] }).tasks,
    [expected],
  );
  assert.deepEqual(
    runtime.validateSession({ format: "djinn-session", version: 1, task })
      .tasks,
    [expected],
  );
  const json = runtime.serializeSession({ version: 1, tasks: [task] });
  assert.deepEqual(runtime.parseSessionText(json).tasks, [expected]);
});

test("preserves UI state fields while validating persisted state", () => {
  const state = runtime.validateState({
    version: 1,
    selectedId: "task-1",
    settings: { provider: "codex" },
    tasks: [taskFixture({ title: "Keep this" })],
  });
  assert.equal(state.selectedId, "task-1");
  assert.equal(state.settings.provider, "codex");
  assert.equal(state.version, 2);
  assert.equal(state.tasks[0].title, "Keep this");
  assert.throws(
    () => runtime.validateState({ version: 1, tasks: "invalid" }),
    /tasks/,
  );
});

test("rejects invalid or oversized imported sessions before any execution", () => {
  assert.throws(
    () =>
      runtime.parseSessionText(
        JSON.stringify({ format: "djinn-session", version: 3, tasks: [] }),
      ),
    /version/,
  );
  assert.throws(
    () => runtime.validateSession({ version: 1, tasks: "run this command" }),
    /tasks/,
  );
  assert.throws(() => runtime.validateSession({ version: 1 }), /tasks or task/);
  const session = runtime.validateSession({
    version: 1,
    tasks: [taskFixture({ command: "rm -rf /", title: "Imported data" })],
  });
  assert.equal(session.tasks[0].title, "Imported data");
  assert.equal(session.tasks[0].command, undefined);
});

test("validates run paths, provider, and explicit agent launch limits", () => {
  const input = runtime.validateRunInput({
    taskId: "task-1",
    provider: "claude",
    cwd: "/tmp/project",
    prompt: "Do the work",
    agents: [
      { id: "a1", name: "Research", role: "research", prompt: "Find context" },
      { id: "a2", name: "Review", role: "review", prompt: "Check the result" },
      { id: "a3", name: "Polish", role: "polish", prompt: "Polish the result" },
    ],
  });
  assert.equal(input.agents.length, 3);
  assert.throws(
    () =>
      runtime.validateRunInput({
        taskId: "task-1",
        provider: "codex",
        cwd: "relative",
        prompt: "x",
      }),
    /absolute/,
  );
  assert.throws(
    () =>
      runtime.validateRunInput({
        taskId: "task-1",
        provider: "unknown",
        cwd: "/tmp",
        prompt: "x",
      }),
    /provider/,
  );
  assert.throws(
    () =>
      runtime.validateRunInput({
        taskId: "task-1",
        provider: "codex",
        cwd: "/tmp",
        prompt: "x",
        agents: Array.from({ length: 17 }, (_, i) => ({
          id: `a${i}`,
          prompt: "x",
        })),
      }),
    /At most 16/,
  );
});

test("builds a sequential worker-first integration plan", () => {
  const plan = runtime.buildExecutionPlan({
    taskId: "task-1",
    provider: "codex",
    cwd: "/tmp/project",
    prompt: "Integrate the work",
    mode: "execute",
    agents: [
      {
        id: "research",
        name: "Research",
        role: "research",
        prompt: "Collect evidence",
      },
      {
        id: "review",
        name: "Review",
        role: "review",
        prompt: "Check the evidence",
      },
    ],
  });
  assert.equal(plan.sequential, true);
  assert.deepEqual(
    plan.stages.map((stage) => stage.id),
    ["research", "review", "lead"],
  );
  assert.equal(plan.stages.at(-1).kind, "lead");
});

test("builds bounded inspector waves and requires declared ownership for writers", () => {
  const agents = [
    { id: "a1", name: "One", role: "one", prompt: "Inspect one" },
    { id: "a2", name: "Two", role: "two", prompt: "Inspect two" },
    { id: "a3", name: "Three", role: "three", prompt: "Inspect three" },
  ];
  const review = runtime.buildExecutionPlan(
    {
      taskId: "review-task",
      provider: "codex",
      cwd: "/tmp",
      prompt: "Review",
      mode: "review",
      concurrency: 2,
      agents,
    },
    { git: { branch: "main", worktree: "/tmp" } },
  );
  assert.equal(review.parallel, true);
  assert.equal(review.concurrency, 2);
  assert.deepEqual(
    review.waves.map((wave) => wave.agents.map((agent) => agent.id)),
    [["a1", "a2"], ["a3"]],
  );
  assert.deepEqual(review.git, { branch: "main", worktree: "/tmp" });

  const execute = runtime.buildExecutionPlan({
    taskId: "execute-task",
    provider: "codex",
    cwd: "/tmp",
    prompt: "Execute",
    mode: "execute",
    concurrency: 3,
    agents: agents.map((agent) => ({ ...agent, writeScope: ["*"] })),
  });
  assert.equal(execute.concurrency, 1);
  assert.equal(execute.parallel, false);
  assert.equal(execute.sequential, true);
});

test("validates scoped next-step proposals, keeps them lead-only, and never auto-progresses", () => {
  const marker =
    "DJINN_EVENT:" +
    JSON.stringify({
      type: "next_step",
      data: {
        type: "specification",
        title: "Formaliser le contrat",
        objective: "Écrire les règles vérifiables",
        reason: "La discussion a isolé une décision manquante",
      },
    });
  const parsed = runtime.parseDjinnEvents(marker);
  assert.equal(parsed.events.length, 1);
  assert.equal(parsed.events[0].type, "next_step");
  assert.equal(parsed.events[0].data.type, "specification");
  assert.throws(
    () =>
      runtime.validateProtocolData("next_step", {
        type: "prototype",
        title: "x",
        objective: "y",
      }),
    /reason/,
  );
  const scoped = runtime.scopeProviderEvent(
    {
      kind: "lead",
      runId: "run-1",
      stepId: "step-1",
      workflowMode: "flexible",
    },
    parsed.events[0],
  );
  assert.equal(scoped.data.stepId, "step-1");
  assert.equal(scoped.data.runId, "run-1");
  assert.equal(
    runtime.scopeProviderEvent(
      { kind: "agent", agentId: "worker", runId: "run-1", stepId: "step-1" },
      parsed.events[0],
    ),
    null,
  );
});

test("binds the saved workflow mode and canonical project sources into the prompt", () => {
  const t = {
    ...taskFixture(),
    project: "/tmp",
    model: "configured",
    steps: runtime.workflow.createDefaultSteps("task-1"),
    activeStepId: "task-1:step:1",
    selectedStepId: "task-1:step:1",
    workflowMode: "flexible",
    projectSnapshot: {
      id: "project-1",
      name: "Projet",
      directory: "/tmp",
      conventions: "Respecter les décisions humaines.",
      locations: {},
      workflows: [],
      sourcesOfTruth: [
        {
          id: "ctx",
          title: "Contexte",
          path: "docs/CONTEXT.md",
          description: "Source canonique",
        },
      ],
      updatedAt: "2026-10-06T00:00:00.000Z",
      capturedAt: "2026-10-06T00:00:00.000Z",
    },
  };
  const input = runtime.validateRunInput({
    taskId: t.id,
    stepId: t.activeStepId,
    provider: t.provider,
    cwd: t.project,
    model: t.model,
    prompt: "Préparer la discussion",
    mode: "plan",
  });
  const bound = runtime.bindRunToTask(input, t);
  assert.equal(bound.workflowMode, "flexible");
  const prompt = runtime.composeRunPrompt(bound);
  assert.match(prompt, /flexible workflow/);
  assert.match(prompt, /docs\/CONTEXT\.md/);
  assert.match(prompt, /Human-edited supports/);
});

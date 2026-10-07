"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const protocol = require("../electron/permission-protocol.cjs");
const runtime = require("../electron/runtime.cjs");

const context = {
  taskId: "task-1",
  runId: "run-1",
  stepId: "step-1",
  agentId: "worker-1",
  agentName: "Worker",
  cwd: "/tmp/project",
};

test("normalizes Codex command approvals without exposing native ids", () => {
  const normalized = protocol.normalizeNativeRequest({
    provider: "codex",
    method: "item/commandExecution/requestApproval",
    nativeId: 41,
    params: {
      threadId: "thread-1",
      turnId: "turn-1",
      itemId: "item-1",
      startedAtMs: Date.now(),
      command: "npm test",
      cwd: "/tmp/project",
      reason: "The test command needs to run.",
      availableDecisions: ["accept", "acceptForSession", "decline"],
    },
    context,
  });
  assert.equal(normalized.request.provider, "codex");
  assert.equal(normalized.request.method, "item/commandExecution/requestApproval");
  assert.equal(normalized.request.command, "npm test");
  assert.equal(normalized.request.canAcceptForSession, true);
  assert.notEqual(normalized.request.id, "41");
  assert.equal(normalized.native.id, "41");
});

test("builds least-privilege Codex responses and native user-input answers", () => {
  const approval = protocol.normalizeNativeRequest({
    provider: "codex",
    method: "item/permissions/requestApproval",
    nativeId: "native-permission",
    params: {
      threadId: "thread-1",
      turnId: "turn-1",
      itemId: "item-1",
      startedAtMs: Date.now(),
      cwd: "/tmp/project",
      permissions: {
        fileSystem: {
          entries: [{ access: "write", path: { type: "path", path: "/tmp/project/src" } }],
        },
        network: { enabled: false },
      },
    },
    context,
  });
  const entry = { ...approval, provider: "codex", method: "item/permissions/requestApproval" };
  assert.deepEqual(protocol.buildNativeResponse(entry, "accept"), {
    permissions: approval.native.params.permissions,
    scope: "turn",
  });
  assert.deepEqual(protocol.buildNativeResponse(entry, "decline"), {
    permissions: { fileSystem: { entries: [] }, network: { enabled: false } },
    scope: "turn",
  });

  const userInput = protocol.normalizeNativeRequest({
    provider: "codex",
    method: "item/tool/requestUserInput",
    nativeId: "input-1",
    params: {
      threadId: "thread-1",
      turnId: "turn-1",
      itemId: "item-2",
      questions: [
        {
          id: "choice",
          header: "Choice",
          question: "Which check?",
          options: [{ label: "Fast", description: "Run the fast check." }],
        },
      ],
      isBlocking: true,
    },
    context,
  });
  assert.deepEqual(
    protocol.buildNativeResponse(
      { ...userInput, provider: "codex", method: "item/tool/requestUserInput" },
      "accept",
      { choice: "Fast" },
    ),
    { answers: { choice: { answers: ["Fast"] } } },
  );
  assert.deepEqual(
    protocol.buildNativeResponse(
      { ...userInput, provider: "codex", method: "item/tool/requestUserInput" },
      "decline",
      { choice: "Fast" },
    ),
    { answers: { choice: { answers: [] } } },
  );
});

test("shows exact filesystem and network scope on permission cards", () => {
  const normalized = protocol.normalizeNativeRequest({
    provider: "codex",
    method: "item/permissions/requestApproval",
    nativeId: "scope-1",
    params: {
      permissions: {
        fileSystem: {
          entries: [
            { access: "write", path: "/workspace/src" },
            {
              access: "read",
              path: { type: "special", value: { kind: "project_roots", subpath: "src" } },
            },
          ],
        },
        network: { enabled: true, allowedHosts: ["registry.npmjs.org"] },
      },
      networkApprovalContext: { protocol: "https" },
    },
    context,
  });
  assert.equal(normalized.request.title, "Accorder l'accès réseau et aux fichiers");
  assert.match(normalized.request.reason, /écriture : \/workspace\/src/);
  assert.match(normalized.request.reason, /lecture : <project_roots\/src>/);
  assert.match(normalized.request.reason, /registry\.npmjs\.org/);
  assert.match(normalized.request.reason, /https/);
  assert.deepEqual(
    protocol.buildNativeResponse(
      { ...normalized, provider: "codex", method: "item/permissions/requestApproval" },
      "accept",
    ),
    {
      permissions: normalized.native.params.permissions,
      scope: "turn",
    },
  );
});

test("parses Claude control requests and responds on the same request id", () => {
  const line = JSON.stringify({
    type: "control_request",
    request_id: "claude-request-1",
    request: {
      subtype: "can_use_tool",
      tool_name: "Bash",
      input: { command: "npm test", description: "Run checks" },
      permission_suggestions: [{ type: "addRules", rules: ["Bash(npm test)"] }],
    },
  });
  const parsed = protocol.parseClaudeControlRequest(line);
  assert.equal(parsed.nativeId, "claude-request-1");
  const normalized = protocol.normalizeNativeRequest({
    provider: "claude",
    method: "control_request",
    nativeId: parsed.nativeId,
    params: parsed.params,
    context,
  });
  assert.equal(normalized.request.command, "npm test");
  assert.equal(normalized.request.canAcceptForSession, true);
  assert.deepEqual(
    protocol.buildNativeResponse(
      { ...normalized, provider: "claude", method: "control_request" },
      "acceptForSession",
    ),
    {
      type: "control_response",
      response: {
        subtype: "success",
        request_id: "claude-request-1",
        response: {
          behavior: "allow",
          updatedInput: { command: "npm test", description: "Run checks" },
          updatedPermissions: [
            {
              type: "addRules",
              rules: ["Bash(npm test)"],
              destination: "session",
            },
          ],
        },
      },
    },
  );
});

test("does not offer Claude session grants for broad or unknown suggestions", () => {
  for (const permission_suggestions of [
    [{ type: "setMode", mode: "bypassPermissions", destination: "userSettings" }],
    [{ type: "addRules", rules: ["Bash(*)"], destination: "projectSettings" }],
    [{ type: "futureGrant", value: "anything", destination: "localSettings" }],
  ]) {
    const normalized = protocol.normalizeNativeRequest({
      provider: "claude",
      method: "control_request",
      nativeId: "claude-session-reject",
      params: {
        type: "control_request",
        request_id: "claude-session-reject",
        request: {
          subtype: "can_use_tool",
          tool_name: "Bash",
          input: { command: "npm test" },
          permission_suggestions,
        },
      },
      context,
    });
    assert.equal(normalized.request.canAcceptForSession, false);
    assert.equal(
      protocol.buildNativeResponse(
        { ...normalized, provider: "claude", method: "control_request" },
        "acceptForSession",
      ),
      null,
    );
  }
  const normalized = protocol.normalizeNativeRequest({
    provider: "claude",
    method: "control_request",
    nativeId: "claude-session-sanitize",
    params: {
      type: "control_request",
      request_id: "claude-session-sanitize",
      request: {
        subtype: "can_use_tool",
        tool_name: "Bash",
        input: { command: "npm test" },
        permission_suggestions: [
          { type: "addRules", rules: ["Bash(npm test)"], destination: "userSettings" },
        ],
      },
    },
    context,
  });
  const response = protocol.buildNativeResponse(
    { ...normalized, provider: "claude", method: "control_request" },
    "acceptForSession",
  );
  assert.deepEqual(response.response.response.updatedPermissions, [
    { type: "addRules", rules: ["Bash(npm test)"], destination: "session" },
  ]);
});

test("maps Claude AskUserQuestion answers back to the provider question text", () => {
  const normalized = protocol.normalizeNativeRequest({
    provider: "claude",
    method: "control_request",
    nativeId: "claude-question-1",
    params: {
      type: "control_request",
      request_id: "claude-question-1",
      request: {
        subtype: "can_use_tool",
        tool_name: "AskUserQuestion",
        input: {
          questions: [
            {
              question: "Which check should run?",
              header: "Check",
              options: [
                { label: "Fast", description: "Run a fast check." },
                { label: "Full", description: "Run the full check." },
              ],
            },
          ],
        },
      },
    },
    context,
  });
  assert.equal(normalized.request.questions[0].id, "question-1");
  const response = protocol.buildNativeResponse(
    { ...normalized, provider: "claude", method: "control_request" },
    "accept",
    { "question-1": "Fast" },
  );
  assert.deepEqual(response.response.response.updatedInput.answers, {
    "Which check should run?": "Fast",
  });
});

test("only surfaces supported MCP form schemas and preserves scalar types", () => {
  const normalized = protocol.normalizeNativeRequest({
    provider: "codex",
    method: "mcpServer/elicitation/request",
    nativeId: "mcp-1",
    params: {
      mode: "form",
      serverName: "checks",
      message: "Choose the threshold",
      requestedSchema: {
        type: "object",
        properties: {
          threshold: { type: "integer", title: "Threshold" },
          enabled: { type: "boolean", title: "Enable" },
        },
      },
    },
    context,
  });
  assert.equal(normalized.request.questions.length, 2);
  const response = protocol.buildNativeResponse(
    { ...normalized, provider: "codex", method: "mcpServer/elicitation/request" },
    "accept",
    { threshold: "3", enabled: "true" },
  );
  assert.deepEqual(response, {
    action: "accept",
    content: { threshold: 3, enabled: true },
    _meta: null,
  });
  assert.equal(
    protocol.normalizeNativeRequest({
      provider: "codex",
      method: "mcpServer/elicitation/request",
      nativeId: "mcp-unknown",
      params: {
        mode: "form",
        serverName: "checks",
        requestedSchema: {
          type: "object",
          properties: { nested: { type: "object" } },
        },
      },
      context,
    }),
    null,
  );
});

test("unknown Claude control messages are not treated as approvals", () => {
  assert.equal(
    protocol.parseClaudeControlRequest(
      JSON.stringify({ type: "control_request", request_id: "x", request: null }),
    ),
    null,
  );
  assert.equal(
    protocol.normalizeNativeRequest({
      provider: "claude",
      method: "control_request",
      nativeId: "x",
      params: {
        type: "control_request",
        request_id: "x",
        request: { subtype: "set_model", model: "sonnet" },
      },
      context,
    }),
    null,
  );
});

test("native mission prompts keep review writable and bound old context", () => {
  const input = {
    taskId: "prompt-task",
    provider: "codex",
    cwd: "/tmp/project",
    prompt: "P".repeat(100_000),
    mode: "review",
    stepId: "review-step",
    step: {
      id: "review-step",
      type: "review",
      title: "Review",
      objective: "Check the implementation",
      status: "running",
      exitCriteria: ["Checks pass"],
      expectedArtifacts: [],
      skills: [],
    },
    priorSummaries: ["H".repeat(8_000)],
  };
  const composed = runtime.composeRunPrompt(input);
  assert.ok(composed.length <= runtime.MAX_COMPOSED_PROMPT_LENGTH);
  assert.match(composed, /Native provider permission cards/);
  assert.match(composed, /step_result/);
  assert.match(
    runtime.buildProviderArgs("codex", "review", undefined, "review").join(" "),
    /workspace-write/,
  );
  const claude = runtime.buildProviderInvocation(
    { ...input, provider: "claude", prompt: "Review" },
    { nativePermissions: true },
  );
  assert.equal(claude.keepStdinOpen, true);
  assert.equal(claude.args.includes("--permission-prompts"), true);
  assert.equal(claude.args.includes("manual"), true);
});

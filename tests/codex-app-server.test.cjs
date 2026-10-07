"use strict";
const test = require("node:test"),
  assert = require("node:assert/strict");
const { EventEmitter } = require("node:events");
const { CodexAppServer } = require("../electron/codex-app-server.cjs");
function fixture({ resumeHistoryBytes = 0, maxRecordBytes } = {}) {
  const child = new EventEmitter(),
    requests = [];
  for (const stream of ["stdout", "stderr", "stdin"]) {
    child[stream] = new EventEmitter();
    child[stream].setEncoding = () => {};
  }
  const emit = (message) =>
    child.stdout.emit("data", JSON.stringify(message) + "\n");
  child.stdin.write = (line) => {
    const msg = JSON.parse(line);
    requests.push(msg);
    if (msg.method && msg.id !== undefined)
      queueMicrotask(() =>
        emit({
          id: msg.id,
          result:
            msg.method === "thread/start"
              ? { thread: { id: "thread-1" } }
              : msg.method === "thread/resume"
                ? {
                    thread: {
                      id: msg.params.threadId,
                      turns: msg.params.excludeTurns
                        ? []
                        : [
                            {
                              items: [{ text: "x".repeat(resumeHistoryBytes) }],
                            },
                          ],
                    },
                  }
                : msg.method === "turn/start"
                  ? { turn: { id: "turn-1" } }
                  : msg.method === "turn/steer"
                    ? { turnId: "turn-1" }
                    : {},
        }),
      );
  };
  child.stdin.end = () => {};
  child.kill = () => {};
  const server = new CodexAppServer(child, {
    requestTimeout: 1000,
    ...(maxRecordBytes === undefined ? {} : { maxRecordBytes }),
  });
  return { server, child, requests, emit };
}
const input = {
  cwd: "/tmp/project",
  mode: "execute",
  model: "configured",
  prompt: "Work",
};
test("app-server preserves model, sandbox and history, and steers expected active turn", async () => {
  const f = fixture();
  try {
    const turn = await f.server.run("worker", input, () => {});
    await f.server.steer("worker", "Targeted human message", "human-id");
    assert.deepEqual(f.requests.find((r) => r.method === "turn/steer").params, {
      threadId: "thread-1",
      expectedTurnId: "turn-1",
      clientUserMessageId: "human-id",
      input: [
        { type: "text", text: "Targeted human message", text_elements: [] },
      ],
    });
    const started = f.requests.find((r) => r.method === "turn/start").params;
    assert.equal(started.model, "configured");
    assert.equal(started.sandboxPolicy.networkAccess, false);
    assert.deepEqual(started.sandboxPolicy.writableRoots, ["/tmp/project"]);
    assert.equal(started.approvalPolicy, "on-request");
    f.emit({
      method: "turn/completed",
      params: {
        threadId: "thread-1",
        turn: { id: "turn-1", status: "completed" },
      },
    });
    assert.equal((await turn.completed).status, "completed");
    const next = await f.server.run(
      "worker",
      { ...input, mode: "review" },
      () => {},
    );
    assert.equal(
      f.requests.filter((r) => r.method === "thread/start").length,
      1,
    );
    assert.equal(
      f.requests.filter((r) => r.method === "turn/start").at(-1).params
        .sandboxPolicy.type,
      "workspaceWrite",
    );
    f.emit({
      method: "turn/completed",
      params: {
        threadId: "thread-1",
        turn: { id: "turn-1", status: "completed" },
      },
    });
    await next.completed;
  } finally {
    f.server.fail(new Error("Fixture closed"));
  }
});

test("app-server registers dynamic tools and preserves item/tool/call responses", async () => {
  const f = fixture();
  const requests = [];
  const dynamicTools = [
    {
      type: "function",
      name: "publish_question",
      description: "Publish a question",
      inputSchema: { type: "object" },
    },
  ];
  f.server.on("request", (method, id, params) =>
    requests.push({ method, id, params }),
  );
  try {
    const turn = await f.server.run(
      "native-tools",
      input,
      () => {},
      { dynamicTools },
    );
    assert.deepEqual(
      f.requests.find((request) => request.method === "thread/start").params
        .dynamicTools,
      dynamicTools,
    );
    f.emit({
      id: "tool-call-1",
      method: "item/tool/call",
      params: {
        threadId: turn.threadId,
        turnId: turn.turnId,
        callId: "call-1",
        namespace: null,
        tool: "publish_question",
        arguments: { title: "Choose" },
      },
    });
    await new Promise((resolve) => setImmediate(resolve));
    assert.deepEqual(requests.at(-1), {
      method: "item/tool/call",
      id: "tool-call-1",
      params: {
        threadId: turn.threadId,
        turnId: turn.turnId,
        callId: "call-1",
        namespace: null,
        tool: "publish_question",
        arguments: { title: "Choose" },
      },
    });
    assert.equal(
      f.server.respond("tool-call-1", {
        success: true,
        contentItems: [{ type: "inputText", text: '{"ok":true}' }],
      }),
      true,
    );
    assert.deepEqual(f.requests.at(-1), {
      id: "tool-call-1",
      result: {
        success: true,
        contentItems: [{ type: "inputText", text: '{"ok":true}' }],
      },
    });
    f.emit({
      method: "turn/completed",
      params: {
        threadId: turn.threadId,
        turn: { id: turn.turnId, status: "completed" },
      },
    });
    await turn.completed;
  } finally {
    f.server.fail(new Error("Fixture closed"));
  }
});
test("warning is diagnostic; native permission expansion is declined; explicit failure ends turn", async () => {
  const f = fixture(),
    diagnostics = [];
  f.server.on("diagnostic", (v) => diagnostics.push(v));
  try {
    const turn = await f.server.run(
      "chief",
      { ...input, mode: "plan" },
      () => {},
    );
    f.child.stderr.emit("data", "WARN codex_skills icon");
    assert.equal(f.server.closed, false);
    f.emit({
      id: "approval-1",
      method: "item/commandExecution/requestApproval",
      params: { threadId: "thread-1" },
    });
    assert.deepEqual(f.requests.at(-1), {
      id: "approval-1",
      result: { decision: "decline" },
    });
    f.emit({
      method: "turn/completed",
      params: {
        threadId: "thread-1",
        turn: {
          id: "turn-1",
          status: "failed",
          error: { message: "Explicit failure" },
        },
      },
    });
    assert.equal((await turn.completed).status, "failed");
    assert.equal(diagnostics.length, 2);
  } finally {
    f.server.fail(new Error("Fixture closed"));
  }
});

test("holds native approval requests and responds with the original JSON-RPC id", async () => {
  const f = fixture();
  const requests = [];
  f.server.on("request", (method, id, params) =>
    requests.push({ method, id, params }),
  );
  try {
    const turn = await f.server.run("chief", { ...input, mode: "review" }, () => {});
    f.emit({
      id: 9001,
      method: "item/commandExecution/requestApproval",
      params: { threadId: "thread-1", command: "npm test" },
    });
    await new Promise((resolve) => setImmediate(resolve));
    assert.deepEqual(requests.at(-1), {
      method: "item/commandExecution/requestApproval",
      id: 9001,
      params: { threadId: "thread-1", command: "npm test" },
    });
    assert.equal(f.requests.some((request) => request.id === 9001), false);
    assert.equal(f.server.respond("9001", { decision: "accept" }), true);
    assert.deepEqual(f.requests.at(-1), {
      id: 9001,
      result: { decision: "accept" },
    });
    f.emit({
      method: "turn/completed",
      params: {
        threadId: "thread-1",
        turn: { id: "turn-1", status: "completed" },
      },
    });
    assert.equal((await turn.completed).status, "completed");
  } finally {
    f.server.fail(new Error("Fixture closed"));
  }
});
test("saved native conversation resumes explicitly and transport loss resolves active turns", async () => {
  const f = fixture();
  const turn = await f.server.run("chief", input, () => {}, {
    savedThreadId: "saved-thread",
  });
  assert.equal(
    f.requests.find((r) => r.method === "thread/resume").params.threadId,
    "saved-thread",
  );
  f.child.emit("close", 1);
  assert.equal((await turn.completed).status, "failed");
  await assert.rejects(
    f.server.steer("chief", "Message", "lost"),
    /active turn/,
  );
});

test("ambiguous transport termination waits for process close before resolving a writer", async () => {
  const f = fixture();
  const turn = await f.server.run("worker", input, () => {});
  const kills = [];
  f.child.kill = (signal) => kills.push(signal);
  let resolved = false;
  const aborted = f.server
    .abortTransport(new Error("Lost acknowledgement"))
    .then(() => {
      resolved = true;
    });
  let completed = false;
  turn.completed.then(() => {
    completed = true;
  });
  await new Promise((r) => setImmediate(r));
  assert.deepEqual(kills, ["SIGTERM"]);
  assert.equal(resolved, false);
  assert.equal(completed, false);
  f.child.emit("close", 143);
  await aborted;
  assert.equal((await turn.completed).status, "failed");
});

test("late completion from an interrupted turn cannot resolve its replacement early", async () => {
  const f = fixture();
  try {
    const turn = await f.server.run("worker", input, () => {});
    let settled = false;
    turn.completed.then(() => {
      settled = true;
    });
    f.emit({
      method: "turn/completed",
      params: {
        threadId: "thread-1",
        turn: { id: "stale-turn", status: "interrupted" },
      },
    });
    await new Promise((resolve) => setImmediate(resolve));
    assert.equal(settled, false);
    assert.equal(f.server.turns.has("thread-1"), true);
    f.emit({
      method: "turn/completed",
      params: {
        threadId: "thread-1",
        turn: { id: "turn-1", status: "completed" },
      },
    });
    assert.equal((await turn.completed).status, "completed");
  } finally {
    f.server.fail(new Error("Fixture closed"));
  }
});

test("long saved conversations resume without downloading full history or closing Codex", async () => {
  const f = fixture({ resumeHistoryBytes: 1_200_000 });
  f.child.kill = () => f.child.emit("close", 0);
  try {
    const turn = await f.server.run("chief", input, () => {}, {
      savedThreadId: "saved-large-thread",
    });
    const resume = f.requests.find((r) => r.method === "thread/resume").params;
    assert.equal(resume.threadId, "saved-large-thread");
    assert.equal(resume.excludeTurns, true);
    assert.equal(f.server.closed, false);
    assert.equal(turn.threadId, "saved-large-thread");
    f.emit({
      method: "turn/completed",
      params: {
        threadId: turn.threadId,
        turn: { id: turn.turnId, status: "completed" },
      },
    });
    assert.equal((await turn.completed).status, "completed");
  } finally {
    f.server.fail(new Error("Fixture closed"));
  }
});

test("transport abort retains its cause when Codex exits cleanly", async () => {
  const f = fixture();
  const turn = await f.server.run("chief", input, () => {});
  f.child.kill = () => f.child.emit("close", 0);
  await f.server.abortTransport(
    new Error("Codex app-server record exceeds the output limit"),
  );
  assert.equal(
    (await turn.completed).error.message,
    "Codex app-server record exceeds the output limit",
  );
});

test("large fragmented native records preserve notifications and allow the active turn to finish", async () => {
  const f = fixture(),
    notifications = [];
  f.child.kill = () => f.child.emit("close", 0);
  try {
    const turn = await f.server.run("chief", input, (method, params) =>
      notifications.push({ method, params }),
    );
    const output = "résultat 🧞\n".repeat(120_000);
    const line =
      JSON.stringify({
        method: "item/completed",
        params: {
          threadId: turn.threadId,
          item: {
            id: "large-tool",
            type: "commandExecution",
            aggregatedOutput: output,
          },
        },
      }) + "\n";
    for (let i = 0; i < line.length; i += 60_000)
      f.child.stdout.emit("data", line.slice(i, i + 60_000));
    assert.equal(f.server.closed, false);
    assert.equal(notifications[0]?.params.item.aggregatedOutput, output);
    // The final turn can also contain all of its completed items.
    f.emit({
      method: "turn/completed",
      params: {
        threadId: turn.threadId,
        turn: {
          id: turn.turnId,
          status: "completed",
          items: [{ text: output }],
        },
      },
    });
    assert.equal((await turn.completed).status, "completed");
    assert.equal(f.server.closed, false);
  } finally {
    f.server.fail(new Error("Fixture closed"));
  }
});

test("a batch larger than the record limit is read as independent JSON-RPC lines", async () => {
  const f = fixture(),
    notifications = [];
  f.child.kill = () => f.child.emit("close", 0);
  try {
    const turn = await f.server.run("chief", input, (method, params) =>
      notifications.push({ method, params }),
    );
    const line =
      JSON.stringify({
        method: "item/agentMessage/delta",
        params: {
          threadId: turn.threadId,
          itemId: "message",
          delta: "x".repeat(600_000),
        },
      }) + "\n";
    // Even a batch exceeding the default 32 MiB limit is safe when each
    // individual record fits. End on a partial next record to exercise carry.
    const completion = JSON.stringify({
      method: "turn/completed",
      params: {
        threadId: turn.threadId,
        turn: { id: turn.turnId, status: "completed" },
      },
    });
    f.child.stdout.emit("data", line.repeat(60) + completion.slice(0, 30));
    assert.equal(f.server.closed, false);
    assert.equal(notifications.length, 60);
    f.child.stdout.emit("data", completion.slice(30) + "\n");
    assert.equal((await turn.completed).status, "completed");
  } finally {
    f.server.fail(new Error("Fixture closed"));
  }
});

for (const newline of ["", "\n"]) {
  test(`an oversized UTF-8 record (${newline ? "complete" : "partial"}) still waits for actual process close`, async () => {
    const f = fixture({ maxRecordBytes: 512 }),
      kills = [];
    try {
      const turn = await f.server.run("chief", input, () => {});
      f.child.kill = (signal) => kills.push(signal);
      let completed = false;
      turn.completed.then(() => {
        completed = true;
      });
      const prefix = '{"method":"unused","params":{"text":"';
      f.child.stdout.emit("data", prefix);
      // Characters fit, bytes do not: four bytes per emoji.
      f.child.stdout.emit("data", "🧞".repeat(130) + '"}}' + newline);
      await new Promise((resolve) => setImmediate(resolve));
      assert.deepEqual(kills, ["SIGTERM"]);
      assert.equal(completed, false);
      f.child.emit("close", 0);
      assert.equal(
        (await turn.completed).error.message,
        "Codex app-server record exceeds the output limit",
      );
    } finally {
      f.server.fail(new Error("Fixture closed"));
    }
  });
}

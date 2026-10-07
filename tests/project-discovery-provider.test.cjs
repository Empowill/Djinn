"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const fsp = fs.promises;
const os = require("node:os");
const path = require("node:path");
const { EventEmitter } = require("node:events");

const { analyzeProject } = require("../electron/project-discovery.cjs");

async function projectFixture() {
  const directory = await fsp.mkdtemp(
    path.join(os.tmpdir(), "djinn-analysis-"),
  );
  await fsp.writeFile(
    path.join(directory, "README.md"),
    "# Analysis fixture\n",
  );
  await fsp.writeFile(
    path.join(directory, "package.json"),
    JSON.stringify({
      name: "analysis-fixture",
      scripts: { test: "node --test" },
    }),
  );
  return directory;
}

function fakeChild() {
  const child = new EventEmitter();
  for (const stream of ["stdout", "stderr", "stdin"]) {
    child[stream] = new EventEmitter();
    child[stream].setEncoding = () => undefined;
  }
  child.stdin.write = () => undefined;
  child.stdin.end = () => undefined;
  child.exitCode = null;
  child.signalCode = null;
  child.kills = [];
  child.kill = (signal) => {
    child.kills.push(signal);
    if (child.signalCode) return true;
    child.signalCode = signal;
    queueMicrotask(() => child.emit("close", null, signal));
    return true;
  };
  return child;
}

function artifactContent(summary = "Agent setup") {
  return JSON.stringify({
    summary,
    workflows: [],
    sourcesOfTruth: [],
    locations: {},
    conventions: "Observed from the fixture.",
    notes: ["Agent suggestion for review."],
  });
}

test("Claude analysis uses the configured model and read-only tools, then accepts only project-setup", async () => {
  const directory = await projectFixture();
  const calls = [];
  const child = fakeChild();
  const result = await analyzeProject(
    { directory, provider: "claude", model: "configured-claude" },
    {
      command: "/configured/claude",
      env: { PATH: "/configured/bin", DJINN_TEST: "1" },
      spawn(command, args, options) {
        calls.push({ command, args, options });
        queueMicrotask(() => {
          child.stdout.emit(
            "data",
            `${JSON.stringify({
              type: "assistant",
              message: {
                content: [
                  {
                    type: "text",
                    text:
                      "DJINN_EVENT:" +
                      JSON.stringify({
                        type: "artifact",
                        data: {
                          id: "project-setup",
                          content: artifactContent(),
                        },
                      }),
                  },
                ],
              },
            })}\n`,
          );
          child.exitCode = 0;
          child.emit("close", 0, null);
        });
        return child;
      },
    },
  );

  assert.equal(result.analysis.provider, "claude");
  assert.equal(result.analysis.status, "agent");
  assert.equal(result.summary, "Agent setup");
  assert.equal(calls.length, 1);
  assert.equal(calls[0].command, "/configured/claude");
  assert.equal(calls[0].options.cwd, directory);
  assert.equal(calls[0].options.env.DJINN_TEST, "1");
  assert.equal(calls[0].options.shell, false);
  assert.ok(calls[0].args.includes("--tools"));
  assert.ok(calls[0].args.includes("Read,Glob,Grep"));
  assert.ok(calls[0].args.includes("--permission-mode"));
  assert.ok(calls[0].args.includes("plan"));
  assert.deepEqual(
    calls[0].args.slice(
      calls[0].args.indexOf("--model"),
      calls[0].args.indexOf("--model") + 2,
    ),
    ["--model", "configured-claude"],
  );
  assert.ok(child.kills.length >= 1);
});

test("Codex analysis uses app-server read-only sandbox and preserves the configured model", async () => {
  const directory = await projectFixture();
  const calls = [];
  const requests = [];
  const child = fakeChild();
  const emit = (message) =>
    child.stdout.emit("data", `${JSON.stringify(message)}\n`);
  child.stdin.write = (line) => {
    const request = JSON.parse(line);
    requests.push(request);
    if (request.method === "initialize")
      return queueMicrotask(() => emit({ id: request.id, result: {} }));
    if (request.method === "thread/start")
      return queueMicrotask(() =>
        emit({
          id: request.id,
          result: { thread: { id: "discovery-thread" } },
        }),
      );
    if (request.method === "turn/start") {
      queueMicrotask(() => {
        emit({ id: request.id, result: { turn: { id: "discovery-turn" } } });
        emit({
          method: "item/completed",
          params: {
            threadId: "discovery-thread",
            item: {
              id: "message-1",
              type: "agentMessage",
              text:
                "DJINN_EVENT:" +
                JSON.stringify({
                  type: "artifact",
                  data: {
                    id: "project-setup",
                    content: artifactContent("Codex setup"),
                  },
                }),
            },
          },
        });
        emit({
          method: "turn/completed",
          params: {
            threadId: "discovery-thread",
            turn: { id: "discovery-turn", status: "completed" },
          },
        });
      });
    }
  };
  const result = await analyzeProject(
    { directory, provider: "codex", model: "configured-codex" },
    {
      command: "/configured/codex",
      env: { PATH: "/configured/bin" },
      spawn(command, args, options) {
        calls.push({ command, args, options });
        return child;
      },
    },
  );

  assert.equal(result.analysis.provider, "codex");
  assert.equal(result.analysis.status, "agent");
  assert.equal(result.summary, "Codex setup");
  assert.deepEqual(calls[0].args, ["app-server", "--listen", "stdio://"]);
  assert.equal(calls[0].options.shell, false);
  const threadStart = requests.find(
    (request) => request.method === "thread/start",
  );
  const turnStart = requests.find((request) => request.method === "turn/start");
  assert.equal(threadStart.params.model, "configured-codex");
  assert.equal(threadStart.params.sandbox, "read-only");
  assert.equal(turnStart.params.model, "configured-codex");
  assert.equal(turnStart.params.sandboxPolicy.type, "readOnly");
  assert.equal(turnStart.params.sandboxPolicy.networkAccess, false);
  assert.equal(threadStart.params.approvalPolicy, "never");
});

test("invalid provider output remains an explicit fallback", async () => {
  const directory = await projectFixture();
  const child = fakeChild();
  const result = await analyzeProject(
    { directory, provider: "claude", model: "configured-claude" },
    {
      command: "/configured/claude",
      spawn() {
        queueMicrotask(() => {
          child.stdout.emit(
            "data",
            `${JSON.stringify({ type: "assistant", message: { content: [{ type: "text", text: "not an artifact" }] } })}\n`,
          );
          child.exitCode = 0;
          child.emit("close", 0, null);
        });
        return child;
      },
    },
  );
  assert.equal(result.analysis.provider, "claude");
  assert.equal(result.analysis.status, "fallback");
  assert.match(result.analysis.detail, /invalid_agent_output|project-setup/);
});

test("cancellation rejects with AbortError and does not become a fallback", async () => {
  const directory = await projectFixture();
  const controller = new AbortController();
  const child = fakeChild();
  const promise = analyzeProject(
    { directory, provider: "claude", model: "configured-claude" },
    {
      command: "/configured/claude",
      signal: controller.signal,
      spawn() {
        queueMicrotask(() => controller.abort());
        return child;
      },
    },
  );
  await assert.rejects(promise, (error) => error.name === "AbortError");
  assert.ok(child.kills.length >= 1);
});

test("agent interpretations preserve collected evidence and reject invented repository locations", async () => {
  const directory = await projectFixture();
  for (const invented of [false, true]) {
    const child = fakeChild();
    const payload = {
      summary: "Analyse",
      evidence: [
        { path: "README.md", kind: "automation", excerpt: "INVENTED FACT" },
      ],
      ...(invented ? { locations: { source: "missing-directory" } } : {}),
    };
    const result = await analyzeProject(
      { directory, provider: "claude" },
      {
        command: "/fixture/claude",
        spawn() {
          queueMicrotask(() => {
            const marker =
              "DJINN_EVENT:" +
              JSON.stringify({
                type: "artifact",
                data: { id: "project-setup", content: JSON.stringify(payload) },
              });
            child.stdout.emit(
              "data",
              JSON.stringify({
                type: "assistant",
                message: { content: [{ type: "text", text: marker }] },
              }) + "\n",
            );
            child.exitCode = 0;
            child.emit("close", 0);
          });
          return child;
        },
      },
    );
    assert.equal(result.analysis.status, invented ? "fallback" : "agent");
    assert.equal(
      result.evidence.find((e) => e.path === "README.md").kind,
      "documentation",
    );
    assert.doesNotMatch(JSON.stringify(result.evidence), /INVENTED FACT/);
    assert.equal(result.locations.source, undefined);
  }
});

test("both scan harnesses escalate cancellation when the provider ignores SIGTERM", async () => {
  for (const provider of ["claude", "codex"]) {
    const directory = await projectFixture(),
      child = fakeChild(),
      controller = new AbortController();
    const emit = (message) =>
      child.stdout.emit("data", JSON.stringify(message) + "\n");
    child.kill = (signal) => {
      child.kills.push(signal);
      if (signal === "SIGKILL") {
        child.signalCode = signal;
        child.emit("close", null, signal);
      }
      return true;
    };
    child.stdin.write = (line) => {
      const request = JSON.parse(line);
      if (request.method === "initialize")
        queueMicrotask(() => emit({ id: request.id, result: {} }));
      if (request.method === "thread/start")
        queueMicrotask(() =>
          emit({ id: request.id, result: { thread: { id: "thread" } } }),
        );
      if (request.method === "turn/start")
        queueMicrotask(() => {
          emit({ id: request.id, result: { turn: { id: "turn" } } });
          controller.abort();
        });
    };
    const keepAlive = setTimeout(() => {}, 5000);
    try {
      await assert.rejects(
        analyzeProject(
          { directory, provider },
          {
            command: "/fixture/provider",
            signal: controller.signal,
            spawn() {
              if (provider === "claude")
                queueMicrotask(() => controller.abort());
              return child;
            },
          },
        ),
        (error) => error.name === "AbortError",
      );
      assert.ok(child.kills.includes("SIGTERM"));
      assert.ok(child.kills.includes("SIGKILL"));
      assert.equal(child.signalCode, "SIGKILL");
    } finally {
      clearTimeout(keepAlive);
    }
  }
});

test("large repositories use the same complete bounded context under Claude and Codex", async () => {
  const directory = await projectFixture();
  await fsp.mkdir(path.join(directory, "docs"));
  await Promise.all(
    Array.from({ length: 85 }, (_, i) =>
      fsp.writeFile(
        path.join(directory, "docs", `process-${i}.md`),
        `# Workflow ${i}\n${"prototype review ".repeat(150)}`,
      ),
    ),
  );
  const prompts = [];
  for (const provider of ["claude", "codex"]) {
    const child = fakeChild();
    const marker =
      "DJINN_EVENT:" +
      JSON.stringify({
        type: "artifact",
        data: { id: "project-setup", content: artifactContent() },
      });
    const emit = (message) =>
      child.stdout.emit("data", JSON.stringify(message) + "\n");
    child.stdin.write = (line) => {
      const request = JSON.parse(line);
      if (request.method === "initialize")
        queueMicrotask(() => emit({ id: request.id, result: {} }));
      if (request.method === "thread/start")
        queueMicrotask(() =>
          emit({ id: request.id, result: { thread: { id: "thread" } } }),
        );
      if (request.method === "turn/start") {
        prompts.push(request.params.input[0].text);
        queueMicrotask(() => {
          emit({ id: request.id, result: { turn: { id: "turn" } } });
          emit({
            method: "item/completed",
            params: {
              threadId: "thread",
              item: { id: "message", type: "agentMessage", text: marker },
            },
          });
          emit({
            method: "turn/completed",
            params: {
              threadId: "thread",
              turn: { id: "turn", status: "completed" },
            },
          });
        });
      }
    };
    const result = await analyzeProject(
      { directory, provider, history: Array(12).fill("h".repeat(2000)) },
      {
        command: "/fixture/provider",
        spawn(command, args) {
          if (provider === "claude") {
            prompts.push(args.at(-1));
            queueMicrotask(() => {
              emit({
                type: "assistant",
                message: { content: [{ type: "text", text: marker }] },
              });
              child.exitCode = 0;
              child.emit("close", 0);
            });
          }
          return child;
        },
      },
    );
    assert.equal(result.analysis.status, "agent");
    assert.equal(result.evidence.length, 80);
    assert.equal(
      result.evidence.find((entry) => entry.path.startsWith("docs/")).excerpt
        .length,
      1600,
    );
  }
  const normalized = prompts.map((prompt) => {
    assert.ok(prompt.length <= 100000);
    const jsonText = prompt
      .split("Local scan report:\n")[1]
      .split("\nLocal Djinn mission summaries")[0];
    const context = JSON.parse(jsonText);
    assert.match(context.notes.join(" "), /limite de contexte/);
    assert.ok(context.evidence.every((entry) => entry.excerpt.length <= 800));
    assert.equal(context.workflows, undefined);
    return {
      ...context,
      sourcesOfTruth: context.sourcesOfTruth.map((s) => ({ ...s, id: "UUID" })),
    };
  });
  assert.equal(normalized.length, 2);
  assert.deepEqual(normalized[0], normalized[1]);
});

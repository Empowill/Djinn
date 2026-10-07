"use strict";
const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { createRequire, Module } = require("node:module");
const { EventEmitter } = require("node:events");
const runtime = require("../electron/runtime.cjs");
const { taskFixture } = require("./workflow-fixture.cjs");

// Exercise the real native pipeline and process lifecycle with controlled fake
// processes; no provider, Electron window, or external process is launched.
function harness({
  codex = false,
  rejectSteer = false,
  rejectInterrupt = false,
  threadTurns = null,
} = {}) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-native-"));
  const events = [],
    processes = [],
    allProcesses = [],
    kills = [],
    turns = [],
    requests = [];
  const realRequire = createRequire(
    path.resolve(__dirname, "../electron/main.cjs"),
  );
  const appEvents = new Map();
  let quitCalls = 0;
  const app = {
    on(name, callback) { appEvents.set(name, callback); },
    whenReady: () => new Promise(() => {}),
    getPath: () => root,
    quit() { quitCalls++; },
    setPath() {},
  };
  class FakeWindow extends EventEmitter {
    constructor() {
      super();
      this.webContents = new EventEmitter();
      this.webContents.mainFrame = { routingId: 1 };
      this.webContents.setWindowOpenHandler = (handler) => {
        this.openHandler = handler;
      };
    }
    loadURL() {
      return Promise.resolve();
    }
    loadFile() {
      return Promise.resolve();
    }
    isDestroyed() {
      return false;
    }
  }
  const electron = {
    app,
    BrowserWindow: FakeWindow,
    ipcMain: {},
    shell: {},
    dialog: {},
    Notification: {},
  };
  const fakeProcess = Object.create(process);
  fakeProcess.kill = (pid) => {
    kills.push(pid);
  };
  const spawn = (command, args, options) => {
    const child = new EventEmitter();
    child.pid = 800000 + allProcesses.length;
    for (const stream of ["stdout", "stderr", "stdin"]) {
      child[stream] = new EventEmitter();
      child[stream].setEncoding = () => {};
      child[stream].end = () => {};
    }
    // Native Claude stream-json runs keep stdin open so the permission
    // broker can answer control_request frames. Capture the initial user
    // frame as the prompt used by assertions; Codex app-server keeps its
    // existing JSON-RPC write path below.
    child.stdin.writes = [];
      child.stdin.write = (line) => {
        child.stdin.writes.push(line);
        try {
          const frame = JSON.parse(String(line));
          if (frame?.type === "user") {
          const content = frame.message?.content;
          child.prompt = Array.isArray(content)
            ? content
                .filter((item) => item?.type === "text")
                .map((item) => item.text)
                .join("\n")
            : typeof content === "string"
              ? content
              : "";
            if (
              child.args.includes("--tools") &&
              child.prompt.startsWith(
                "You are Djinn, the mission lead supervising",
              )
            ) {
              const index = processes.indexOf(child);
              if (index >= 0) processes.splice(index, 1);
              setImmediate(() => child.close());
            }
          }
      } catch {
        // The fixture only inspects valid stream-json frames.
      }
      return true;
    };
    child.kill = () => {
      kills.push(child.pid);
    };
    child.args = args;
    child.options = options;
    child.close = (code = 0) => {
      child.closed = true;
      child.emit("close", code, null);
    };
    allProcesses.push(child);
    if (
      args.includes("--tools") &&
      args.some(
        (a) =>
          typeof a === "string" &&
          a.startsWith("You are Djinn, the mission lead supervising"),
      )
    )
      setImmediate(() => child.close());
    else processes.push(child);
    if (args[0] === "app-server") {
      let threadNumber = 0;
      const emit = (value) =>
        child.stdout.emit("data", JSON.stringify(value) + "\n");
      child.stdin.write = (line) => {
        const request = JSON.parse(line);
        requests.push(request);
        if (request.id === undefined) return;
        setImmediate(() => {
          if (request.method === "turn/steer" && rejectSteer) {
            emit({
              id: request.id,
              error: {
                code: -32601,
                message: "Steering unsupported by fixture provider",
              },
            });
            return;
          }
          if (request.method === "turn/interrupt" && rejectInterrupt) {
            emit({
              id: request.id,
              error: {
                code: -32601,
                message: "Interruption unsupported by fixture provider",
              },
            });
            return;
          }
          let result = {};
          if (request.method === "thread/start")
            result = { thread: { id: `thread-${++threadNumber}` } };
          if (request.method === "thread/resume")
            result = { thread: { id: request.params.threadId } };
          if (request.method === "thread/turns/list" && threadTurns) {
            const data =
              typeof threadTurns === "function"
                ? threadTurns(request.params)
                : threadTurns;
            result = { data, nextCursor: null, backwardsCursor: null };
          }
          if (request.method === "turn/start") {
            const turn = {
              id: `turn-${turns.length + 1}`,
              threadId: request.params.threadId,
              params: request.params,
              delta(text) {
                emit({
                  method: "item/agentMessage/delta",
                  params: {
                    threadId: turn.threadId,
                    itemId: "message-" + turn.id,
                    delta: text,
                  },
                });
              },
              message(text) {
                emit({
                  method: "item/completed",
                  params: {
                    threadId: turn.threadId,
                    item: {
                      id: "question-" + turn.id,
                      type: "agentMessage",
                      text,
                    },
                  },
                });
              },
              complete(status = "completed", text = "Done.") {
                if (turn.done) return;
                turn.done = true;
                emit({
                  method: "item/completed",
                  params: {
                    threadId: turn.threadId,
                    item: {
                      id: "message-" + turn.id,
                      type: "agentMessage",
                      text,
                    },
                  },
                });
                emit({
                  method: "turn/completed",
                  params: {
                    threadId: turn.threadId,
                    turn: { id: turn.id, status },
                  },
                });
              },
            };
            turns.push(turn);
            result = { turn: { id: turn.id, status: "inProgress" } };
            emit({
              method: "turn/started",
              params: { threadId: turn.threadId, turn: result.turn },
            });
          }
          if (request.method === "turn/steer")
            result = { turnId: request.params.expectedTurnId };
          emit({ id: request.id, result });
          if (request.method === "turn/interrupt")
            turns
              .find((t) => t.id === request.params.turnId)
              ?.complete("interrupted");
          const latest = turns.at(-1);
          if (
            request.method === "turn/start" &&
            latest.params.input[0].text.includes("read-only chief fixture")
          )
            latest.complete();
        });
      };
    }
    return child;
  };
  const source =
    fs
      .readFileSync(path.resolve(__dirname, "../electron/main.cjs"), "utf8")
      .replace(
        "function sendEvent(event) {",
        "function sendEvent(event) { testEvents.push(event); return;",
      )
      .replaceAll(
        "const command = resolveProviderCommand(spec);",
        "const command = 'fixture-provider';",
      ) +
    "\nmodule.exports.testDiscoverProject = discoverProject; module.exports.testCancelProjectDiscovery = cancelProjectDiscovery; module.exports.testSaveState = saveState; module.exports.testLoadState = loadState; module.exports.testRuntimeSnapshot = getRuntimeSnapshot; module.exports.testActiveRuns = () => activeRuns; module.exports.testCreateWindow = createMainWindow; module.exports.testVisualization = (source) => visualizationRegistry.create(source); module.exports.testStepResultSatisfiesExitCriteria = stepResultSatisfiesExitCriteria;";
  const compiled = new Module(path.resolve(__dirname, "../electron/main.cjs"));
  compiled.testBindings = { testProcess: fakeProcess, testEvents: events };
  compiled.require = (name) =>
    name === "electron"
      ? electron
      : name === "node:child_process"
        ? { spawn, spawnSync: () => ({ status: 1 }) }
        : realRequire(name);
  compiled._compile(
    "const { testProcess: process, testEvents } = module.testBindings;\n" +
      source,
    path.resolve(__dirname, "../electron/main.cjs"),
  );
  const api = compiled.exports;
  const input = {
    taskId: "native",
    provider: codex ? "codex" : "claude",
    model: "configured-model",
    cwd: root,
    prompt: "Preserve existing files.",
    mode: "execute",
  };
  return {
    root,
    events,
    processes,
    allProcesses,
    kills,
    turns,
    requests,
    api,
    appEvents,
    get quitCalls() { return quitCalls; },
    input,
    async dispose() {
      for (const event of events.filter(
        (e) =>
          e.type === "status" && e.data.status === "running" && !e.data.scope,
      ))
        await api.cancelRun(event.runId);
      for (const t of turns) if (!t.done) t.complete("interrupted");
      for (const child of allProcesses) if (!child.closed) child.close();
      await tick();
      await api.flushMissionJournal();
      fs.rmSync(root, { recursive: true, force: true });
    },
  };
}
const tick = () => new Promise((resolve) => setImmediate(resolve));
const childPrompt = (child) => child.prompt || child.args.at(-1) || "";
const isSupervisorProcess = (child) =>
  child?.args?.includes("--tools") &&
  (child.prompt?.startsWith(
    "You are Djinn, the mission lead supervising",
  ) ||
    child.args.some(
      (arg) =>
        typeof arg === "string" &&
        arg.startsWith("You are Djinn, the mission lead supervising"),
    ));
async function until(predicate) {
  const deadline = Date.now() + 2000;
  while (Date.now() < deadline) {
    if (predicate()) return;
    await new Promise((resolve) => setTimeout(resolve, 1));
  }
  assert.ok(predicate(), "expected native lifecycle state");
}
const worker = {
  id: "worker",
  name: "Socle",
  role: "Runtime",
  prompt: "Only edit your assigned files.",
  readOnly: false,
  writeScope: ["*"],
};

test("auto-preview requires evidence for every configured stage criterion", async () => {
  const h = harness({ codex: true });
  try {
    const base = {
      stepId: "step-1",
      validatedInput: {
        step: { exitCriteria: ["Checks pass", "Preview responds"] },
      },
      stepResult: {
        status: "ready",
        criteria: [{ criterion: "Checks pass", met: true, evidence: "npm test" }],
      },
    };
    assert.equal(h.api.testStepResultSatisfiesExitCriteria(base), false);
    assert.equal(
      h.api.testStepResultSatisfiesExitCriteria({
        ...base,
        stepResult: {
          ...base.stepResult,
          criteria: [
            ...base.stepResult.criteria,
            { criterion: "Preview responds", met: true, evidence: "HTTP 200" },
          ],
        },
      }),
      true,
    );
    assert.equal(
      h.api.testStepResultSatisfiesExitCriteria({
        ...base,
        stepResult: {
          ...base.stepResult,
          criteria: [
            ...base.stepResult.criteria,
            { criterion: "Preview responds", met: true, evidence: "" },
          ],
        },
      }),
      false,
    );
  } finally {
    await h.dispose();
  }
});

test("lead proposals run independent scoped workers concurrently and pass results to dependents", async () => {
  const h = harness({ codex: true });
  try {
    const run = await h.api.startRun({ ...h.input, concurrency: 3, agents: [] });
    await until(() => h.turns.length === 1);
    const definitions = [
      { id: "design", name: "Design", role: "UI", prompt: "Work on UI.", writeScope: ["src/ui"], readOnly: false },
      { id: "logic", name: "Logic", role: "Logic", prompt: "Work on logic.", writeScope: ["src/logic"], readOnly: false },
      { id: "check", name: "Check", role: "Review", prompt: "Check both reports.", readOnly: true, dependsOn: ["design", "logic"] },
    ];
    h.turns[0].complete("completed", definitions.map(data => "DJINN_EVENT:" + JSON.stringify({ type: "agent", data })).join("\n"));
    await until(() => h.turns.length >= 3);
    assert.notEqual(h.turns[1].done, true);
    assert.notEqual(h.turns[2].done, true);
    h.turns[1].complete("completed", "Design proof delivered.");
    h.turns[2].complete("completed", "Logic proof delivered.");
    await until(() => h.turns.length === 4);
    assert.equal(h.turns[3].params.sandboxPolicy.type, "readOnly");
    assert.match(h.turns[3].params.input[0].text, /Design proof delivered/);
    assert.match(h.turns[3].params.input[0].text, /Logic proof delivered/);
    h.turns[3].complete("completed", "Review proof delivered.");
    await until(() => h.turns.length === 5);
    h.turns[4].complete();
    await until(() => h.events.some(e => e.runId === run.runId && e.type === "status" && e.data.status === "completed"));
    await h.api.flushMissionJournal();
    const page = await h.api.getMissionJournalPage("native", 0, 200);
    assert.ok(page.events.some(e => e.type === "agent" && e.data.id === "check"));
    let prevented = false;
    h.appEvents.get("before-quit")({ preventDefault() { prevented = true; } });
    assert.equal(prevented, true);
    await until(() => h.quitCalls === 1);
  } finally { await h.dispose(); }
});

test("native visualization frames cannot navigate themselves or escape to another document", async () => {
  const h = harness();
  try {
    const window = h.api.testCreateWindow();
    const { url } = h.api.testVisualization("<h1>Local</h1>");
    const navigate = (details) => {
      let prevented = false;
      window.webContents.emit("will-frame-navigate", {
        ...details,
        preventDefault() {
          prevented = true;
        },
      });
      return prevented;
    };
    const child = { routingId: 2, url };
    for (const target of [
      "https://example.com",
      "http://127.0.0.1:4317/",
      "file:///tmp/escape.html",
      "data:text/html,escape",
      "blob:null/escape",
      "about:blank",
      url,
    ])
      assert.equal(
        navigate({
          url: target,
          isMainFrame: false,
          frame: child,
          initiator: child,
        }),
        true,
        target,
      );
    assert.equal(
      navigate({
        url,
        isMainFrame: false,
        frame: { routingId: 2, url: "about:blank" },
        initiator: window.webContents.mainFrame,
      }),
      false,
      "parent may load a registered isolated support",
    );
    assert.equal(
      navigate({
        url: "djinn-visualization://unknown/",
        isMainFrame: false,
        frame: child,
        initiator: window.webContents.mainFrame,
      }),
      true,
    );
    assert.equal(
      navigate({
        url: url.replace("djinn-visualization:", "https:"),
        isMainFrame: false,
        frame: child,
        initiator: window.webContents.mainFrame,
      }),
      true,
      "a registered hostname cannot authorize a network URL",
    );
    assert.equal(
      navigate({ url: "https://example.com", isMainFrame: true }),
      true,
    );
    assert.equal(window.openHandler().action, "deny");
  } finally {
    await h.dispose();
  }
});

test("provider stderr warning followed by output completes worker normally", async () => {
  const h = harness();
  try {
    const run = await h.api.startRun({ ...h.input, agents: [worker] });
    await until(() => h.processes.length === 1);
    h.processes[0].stderr.emit(
      "data",
      "WARN codex_skills: missing icon for skill\n",
    );
    h.processes[0].stdout.emit(
      "data",
      JSON.stringify({
        type: "item.completed",
        item: { type: "agent_message", text: "Work continued." },
      }) + "\n",
    );
    h.processes[0].close();
    await until(() => h.processes.length === 2);
    h.processes[1].close();
    await until(() =>
      h.events.some(
        (e) => e.runId === run.runId && e.data.status === "completed",
      ),
    );
    assert.equal(
      h.events.some((e) => e.type === "error"),
      false,
    );
    assert.ok(
      h.events.some(
        (e) =>
          e.type === "note" &&
          e.data.severity === "warning" &&
          e.data.agentId === "worker",
      ),
    );
    assert.ok(
      h.events.some(
        (e) =>
          e.type === "agent" &&
          e.data.id === "worker" &&
          e.data.status === "done",
      ),
    );
    assert.ok(
      h.events.some(
        (e) =>
          e.data.waitingForAgents === true && e.data.activeAgentId === "worker",
      ),
    );
    assert.ok(
      h.events.some(
        (e) =>
          e.type === "status" &&
          e.runId === run.runId &&
          e.data.activeAgentId === "worker" &&
          e.data.activeTask === worker.role,
      ),
      "native activity retains the worker task",
    );
  } finally {
    await h.dispose();
  }
});

test("targeted guidance interrupts then resumes the same worker after close without concurrent writes", async () => {
  const h = harness();
  try {
    const run = await h.api.startRun({ ...h.input, agents: [worker] });
    await until(() => h.processes.length === 1);
    h.processes[0].stdout.emit(
      "data",
      JSON.stringify({
        type: "item.completed",
        item: { type: "agent_message", text: "Existing implementation kept." },
      }) + "\n",
    );
    const receipt = await h.api.steerRun({
      runId: run.runId,
      id: "g1",
      text: "Add the targeted check.",
      agentId: "worker",
    });
    assert.equal(receipt.status, "transmitted");
    assert.ok(h.kills.length);
    await tick();
    assert.equal(h.processes.length, 1, "replacement waits for actual close");
    await assert.rejects(h.api.startRun(h.input), /active/);
    h.processes[0].close();
    await until(() => h.processes.length === 2);
    const prompt = childPrompt(h.processes[1]);
    assert.match(prompt, /Add the targeted check/);
    assert.match(prompt, /Existing implementation kept/);
    assert.equal(h.processes[1].options.cwd, h.root);
    assert.ok(h.processes[1].args.includes("configured-model"));
    const passes = h.events.filter(
      (e) => e.type === "agent" && e.data.lifecycle === "agent_started",
    );
    assert.notEqual(passes[0].data.runId, passes[1].data.runId);
    assert.ok(
      h.events.some(
        (e) =>
          e.type === "guidance" &&
          e.data.id === "g1" &&
          e.data.status === "consumed",
      ),
    );
    assert.equal(
      h.events.some(
        (e) =>
          e.runId === run.runId &&
          ["completed", "cancelled", "error"].includes(e.data.status),
      ),
      false,
    );
    h.processes[1].close();
    await until(() => h.processes.length === 3);
    h.processes[2].close();
  } finally {
    await h.dispose();
  }
});

test("guidance to a finished worker pauses lead integration and revisits only that worker", async () => {
  const h = harness();
  try {
    const run = await h.api.startRun({ ...h.input, agents: [worker] });
    await until(() => h.processes.length === 1);
    h.processes[0].close();
    await until(() => h.processes.length === 2);
    await h.api.steerRun({
      runId: run.runId,
      id: "g2",
      text: "Worker specific correction.",
      agentId: "worker",
    });
    assert.equal(h.processes.length, 2);
    h.processes[1].close();
    await until(() => h.processes.length === 3);
    assert.match(
      childPrompt(h.processes[2]),
      /delegated Djinn agent named Socle/,
    );
    assert.match(childPrompt(h.processes[2]), /Worker specific correction/);
    h.processes[2].close();
    await until(() => h.processes.length === 4);
    h.processes[3].close();
  } finally {
    await h.dispose();
  }
});

test("unknown agent guidance has a reliable prevented receipt and never reaches another agent", async () => {
  const h = harness();
  try {
    const run = await h.api.startRun(h.input);
    await until(() => h.processes.length === 1);
    const receipt = await h.api.steerRun({
      runId: run.runId,
      id: "bad-target",
      text: "Do not reroute.",
      agentId: "missing",
    });
    assert.equal(receipt.status, "prevented");
    assert.equal(receipt.reason, "unknown_agent");
    assert.equal(h.kills.length, 0);
    h.processes[0].close();
  } finally {
    await h.dispose();
  }
});

test("blocking question requires all answers; saved answer permits same-stage native reprise", async () => {
  const h = harness();
  try {
    const steps = runtime.workflow.createDefaultSteps("native");
    const task = {
      ...taskFixture(),
      id: "native",
      project: h.root,
      provider: "claude",
      model: "configured-model",
      steps,
      activeStepId: steps[0].id,
      selectedStepId: steps[0].id,
    };
    await h.api.testSaveState({ version: 2, tasks: [task], projects: [] });
    const input = { ...h.input, mode: "plan", stepId: steps[0].id };
    const run = await h.api.startRun(input);
    await until(() => h.processes.length === 1);
    h.processes[0].stdout.emit(
      "data",
      'DJINN_EVENT:{"type":"question","data":{"id":"block","title":"Choose","blocking":true}}\n',
    );
    h.processes[0].close();
    await until(() =>
      h.events.some(
        (e) => e.runId === run.runId && e.data.status === "completed",
      ),
    );
    task.steps[0].status = "blocked";
    task.questions.push({
      id: "block",
      title: "Choose",
      context: "",
      recommendation: "",
      options: [],
      blocking: true,
      unlocks: "resume",
      stepId: steps[0].id,
    });
    await h.api.testSaveState({ version: 2, tasks: [task], projects: [] });
    await assert.rejects(h.api.startRun(input), /blocking answers/);
    task.questions.at(-1).answer = "Proceed";
    await h.api.testSaveState({ version: 2, tasks: [task], projects: [] });
    await h.api.startRun(input);
    await until(() => h.processes.length === 2);
    assert.ok(
      h.events
        .filter((e) => e.taskId === "native")
        .every((e) => e.stepId === steps[0].id),
    );
    h.processes[1].close();
  } finally {
    await h.dispose();
  }
});

test("simultaneous IPC starts reserve one writer after asynchronous validation", async () => {
  const h = harness();
  try {
    const starts = await Promise.allSettled([
      h.api.startRun(h.input),
      h.api.startRun(h.input),
    ]);
    assert.equal(starts.filter((s) => s.status === "fulfilled").length, 1);
    assert.equal(starts.filter((s) => s.status === "rejected").length, 1);
    await until(() => h.processes.length === 1);
    h.processes[0].close();
  } finally {
    await h.dispose();
  }
});

test("durable Codex chief replies while the worker continues, using separate protected threads", async () => {
  const h = harness({ codex: true });
  try {
    const run = await h.api.startRun({ ...h.input, agents: [worker] });
    await until(() => h.turns.length === 1);
    assert.equal(
      h.turns.some((t) => t.params.sandboxPolicy.type === "readOnly"),
      false,
      "worker startup does not create an idle supervisor turn",
    );
    const initial = await h.api.steerRun({
      runId: run.runId,
      id: "status-start",
      text: "Prépare une réponse sur l'état du worker.",
    });
    assert.equal(initial.status, "transmitted");
    await until(() => h.turns.length === 2);
    assert.equal(h.processes.length, 1);
    const chief = h.turns.find(
      (t) => t.params.sandboxPolicy.type === "readOnly",
    );
    const working = h.turns.find(
      (t) => t.params.sandboxPolicy.type === "workspaceWrite",
    );
    chief.complete("completed", "Je supervise le worker.");
    await tick();
    const receipt = await h.api.steerRun({
      runId: run.runId,
      id: "status-msg",
      text: "Où en est le worker ?",
    });
    assert.equal(receipt.delivery, "app_server");
    await until(() => h.turns.length === 3);
    const answer = h.turns[2];
    assert.equal(
      answer.threadId,
      chief.threadId,
      "chief thread retains its conversation",
    );
    assert.equal(answer.params.sandboxPolicy.type, "readOnly");
    answer.complete("completed", "Le worker poursuit sa tâche.");
    await tick();
    assert.equal(
      working.done,
      undefined,
      "conversation does not interrupt the writer",
    );
    assert.equal(
      h.requests.filter((r) => r.method === "turn/interrupt").length,
      0,
    );
    await h.api.steerRun({
      runId: run.runId,
      id: "worker-msg",
      text: "Ajoute la vérification ciblée.",
      agentId: "worker",
    });
    assert.ok(
      h.requests.some(
        (r) =>
          r.method === "turn/steer" && r.params.threadId === working.threadId,
      ),
    );
    assert.ok(
      h.events.some(
        (e) =>
          e.type === "guidance" &&
          e.data.id === "worker-msg" &&
          e.data.status === "consumed",
      ),
    );
    assert.equal(
      h.turns.length,
      3,
      "targeted worker steering does not create another supervisor turn",
    );
    working.complete("completed", "Code et preuves conservés.");
    await until(() => h.turns.length === 4);
    const integration = h.turns[3];
    assert.equal(integration.params.sandboxPolicy.type, "workspaceWrite");
    for (const t of h.turns) {
      assert.equal(t.params.approvalPolicy, "on-request");
      assert.equal(t.params.sandboxPolicy.networkAccess, false);
      assert.equal(t.params.model, "configured-model");
    }
    integration.complete();
    await until(() =>
      h.events.some(
        (e) => e.runId === run.runId && e.data.status === "completed",
      ),
    );
    assert.equal(
      h.events.some((e) => e.type === "error"),
      false,
    );
  } finally {
    await h.dispose();
  }
});

test("supervisor blocking question interrupts all active turns and releases the stage before answers", async () => {
  const h = harness({ codex: true });
  try {
    const run = await h.api.startRun({ ...h.input, agents: [worker] });
    await until(() => h.turns.length === 1);
    await h.api.steerRun({
      runId: run.runId,
      id: "start-supervisor-block",
      text: "Reste disponible pour le suivi.",
    });
    await until(() => h.turns.length === 2);
    const chief = h.turns.find(
      (t) => t.params.sandboxPolicy.type === "readOnly",
    );
    chief.message(
      'DJINN_EVENT:{"type":"question","data":{"id":"supervisor-block","title":"Choisir","blocking":true}}\n',
    );
    await until(() => h.turns.every((t) => t.done));
    await until(() =>
      h.events.some(
        (e) =>
          e.runId === run.runId &&
          !e.data.scope &&
          e.data.status === "completed",
      ),
    );
    assert.equal(
      h.turns.length,
      2,
      "no integration writer starts while blocked",
    );
    assert.equal(
      h.requests.filter((r) => r.method === "turn/interrupt").length,
      2,
    );
    assert.equal(
      h.events.some((e) => e.type === "error"),
      false,
    );
    assert.ok(
      h.events.some(
        (e) =>
          e.type === "agent" &&
          e.data.id === "worker" &&
          e.data.status === "blocked",
      ),
    );
    assert.equal(h.api.testRuntimeSnapshot().runs.length, 0);
  } finally {
    await h.dispose();
  }
});

test("Codex guidance to a finished worker interrupts integration and revisits that worker before another writer", async () => {
  const h = harness({ codex: true });
  try {
    const run = await h.api.startRun({ ...h.input, agents: [worker] });
    await until(() => h.turns.length === 1);
    const working = h.turns[0];
    working.complete("completed", "Existing worker output.");
    await until(() => h.turns.length === 2);
    const integration = h.turns[1];
    const receipt = await h.api.steerRun({
      runId: run.runId,
      id: "codex-revisit",
      text: "Worker targeted correction.",
      agentId: "worker",
    });
    assert.equal(receipt.status, "transmitted");
    await until(() => h.turns.length === 3);
    assert.equal(
      integration.done,
      true,
      "integration relinquishes write permission before the worker resumes",
    );
    const correction = h.turns[2];
    assert.equal(correction.threadId, working.threadId);
    assert.match(correction.params.input[0].text, /Worker targeted correction/);
    assert.match(correction.params.input[0].text, /Existing worker output/);
    assert.equal(
      h.events.some((e) => e.type === "error"),
      false,
    );
    correction.complete();
    await until(() => h.turns.length === 4);
    h.turns[3].complete();
    await until(() =>
      h.events.some(
        (e) =>
          e.type === "status" &&
          e.runId === run.runId &&
          e.data.status === "completed",
      ),
    );
    assert.ok(
      h.events.some(
        (e) =>
          e.type === "guidance" &&
          e.data.id === "codex-revisit" &&
          e.data.status === "consumed",
      ),
    );
  } finally {
    await h.dispose();
  }
});

test("prevented guidance is excluded from the next lead prompt and cannot be consumed", async () => {
  const h = harness();
  try {
    const run = await h.api.startRun({ ...h.input, agents: [worker] });
    await until(() => h.processes.length === 1);
    const receipt = await h.api.steerRun({
      runId: run.runId,
      id: "prevented",
      text: "Unknown recipient private instruction.",
      agentId: "missing",
    });
    assert.equal(receipt.status, "prevented");
    h.processes[0].close();
    await until(() => h.processes.length === 2);
    assert.doesNotMatch(
      childPrompt(h.processes[1]),
      /Unknown recipient private instruction/,
    );
    assert.equal(
      h.events.some(
        (e) =>
          e.type === "guidance" &&
          e.data.id === "prevented" &&
          e.data.status === "consumed",
      ),
      false,
    );
    h.processes[1].close();
  } finally {
    await h.dispose();
  }
});

test("Codex rejected direct steering falls back to a controlled same-worker reprise", async () => {
  const h = harness({ codex: true, rejectSteer: true });
  try {
    const run = await h.api.startRun({ ...h.input, agents: [worker] });
    await until(() => h.turns.length === 1);
    const working = h.turns[0];
    const receipt = await h.api.steerRun({
      runId: run.runId,
      id: "fallback",
      text: "Preserve my changes and retry.",
      agentId: "worker",
    });
    assert.equal(receipt.status, "transmitted");
    assert.equal(receipt.delivery, "controlled_restart");
    await until(() => h.turns.length === 2);
    assert.equal(working.done, true);
    const correction = h.turns.find(
      (t) => t !== working && t.threadId === working.threadId,
    );
    assert.ok(correction);
    assert.match(
      correction.params.input[0].text,
      /Preserve my changes and retry/,
    );
    assert.equal(
      h.events.some((e) => e.type === "error"),
      false,
    );
    assert.ok(
      h.events.some(
        (e) =>
          e.type === "guidance" &&
          e.data.id === "fallback" &&
          e.data.status === "consumed",
      ),
    );
    for (const t of h.turns.filter((t) => !t.done)) t.complete();
    await until(() => h.turns.length === 3);
    h.turns[2].complete();
    await until(() =>
      h.events.some(
        (e) => e.runId === run.runId && e.data.status === "completed",
      ),
    );
  } finally {
    await h.dispose();
  }
});

test("a rejected parent interruption blocks controlled reprise until explicit transport cancellation", async () => {
  const h = harness({ codex: true, rejectSteer: true, rejectInterrupt: true });
  try {
    const run = await h.api.startRun({ ...h.input, agents: [worker] });
    await until(() => h.turns.length === 1);
    const working = h.turns[0];
    const receipt = await h.api.steerRun({ runId: run.runId, id: "parent-reject-reprise", text: "Retry this worker.", agentId: "worker" });
    assert.equal(receipt.delivery, "controlled_restart");
    await until(() => h.api.testActiveRuns().get(run.runId)?.observedRestartBlocked);
    assert.equal(h.turns.filter(turn => turn.threadId === working.threadId).length, 1);
    assert.equal(h.api.testActiveRuns().has(run.runId), true);
    await h.api.cancelRun(run.runId);
    await until(() => h.kills.some(pid => Math.abs(pid) === h.allProcesses[0].pid));
    assert.equal(h.api.testActiveRuns().has(run.runId), true, "the rejected parent still owns the project until process close");
    h.allProcesses[0].close();
    await until(() => !h.api.testActiveRuns().has(run.runId));
    assert.equal(h.turns.filter(turn => turn.threadId === working.threadId).length, 1);
  } finally { await h.dispose(); }
});

test("controlled Codex reprise blocks instead of overlapping an unconfirmed observed child", async () => {
  const h = harness({ codex: true, rejectSteer: true });
  const notify = (method, params) =>
    h.allProcesses[0].stdout.emit(
      "data",
      JSON.stringify({ method, params }) + "\n",
    );
  try {
    const run = await h.api.startRun({ ...h.input, agents: [worker] });
    await until(() => h.turns.length === 1);
    const working = h.turns[0];
    const leadThread = working.threadId;
    notify("item/completed", {
      threadId: leadThread,
      item: {
        type: "collabAgentToolCall",
        id: "restart-child-call",
        tool: "spawnAgent",
        status: "completed",
        senderThreadId: leadThread,
        receiverThreadIds: ["restart-child"],
        agentsStates: {
          "restart-child": { status: "running", message: null },
        },
      },
    });
    notify("turn/started", {
      threadId: "restart-child",
      turn: { id: "restart-child-turn", status: "inProgress" },
    });
    const receipt = await h.api.steerRun({
      runId: run.runId,
      id: "restart-with-observed-child",
      text: "Preserve my changes and retry.",
      agentId: "worker",
    });
    assert.equal(receipt.delivery, "controlled_restart");
    assert.deepEqual(
      [...h.api.testActiveRuns().get(run.runId).pendingObservedCancellation],
      ["codex:restart-child"],
    );
    await until(() =>
      h.requests.some(
        (request) =>
          request.method === "turn/interrupt" &&
          request.params.threadId === "restart-child" &&
          request.params.turnId === "restart-child-turn",
      ),
    );
    await until(() =>
      h.events.some(
        (event) => event.data.title === "Reprise contrôlée empêchée",
      ),
    );
    assert.equal(
      h.turns.filter((turn) => turn.threadId === working.threadId).length,
      1,
      "the replacement worker turn waits for confirmed child closure",
    );
    assert.equal(h.api.testActiveRuns().get(run.runId).blocked, true);
    notify("turn/completed", {
      threadId: "restart-child",
      turn: { id: "restart-child-turn", status: "interrupted" },
    });
    const cancelReceipt = await h.api.cancelRun(run.runId);
    assert.equal(cancelReceipt.cancelled, true, JSON.stringify({
      active: [...h.api.testActiveRuns().keys()],
      statuses: h.events
        .filter((event) => event.runId === run.runId && event.type === "status")
        .map((event) => event.data.status),
    }));
    await until(() =>
      h.events.some(
        (event) => event.runId === run.runId && event.data.status === "cancelled",
      ),
    );
  } finally {
    await h.dispose();
  }
});

test("Codex app-server stderr warning keeps its worker active and permits successful completion", async () => {
  const h = harness({ codex: true });
  try {
    const run = await h.api.startRun({ ...h.input, agents: [worker] });
    await until(() => h.turns.length === 1);
    h.processes[0].stderr.emit(
      "data",
      "WARN codex_skills: missing icon for skill\n",
    );
    assert.ok(
      h.events.some(
        (e) =>
          e.type === "note" &&
          e.data.severity === "warning" &&
          /codex_skills/.test(e.data.detail),
      ),
    );
    assert.equal(
      h.events.some((e) => e.type === "error" || e.data.status === "error"),
      false,
    );
    assert.ok(
      h.events
        .at(-1)
        .data.activeAgents.some(
          (a) => a.id === "worker" && a.task === worker.role,
        ),
    );
    for (const t of h.turns) t.complete("completed", "Output after warning.");
    await until(() => h.turns.length === 2);
    h.turns[1].complete();
    await until(() =>
      h.events.some(
        (e) => e.runId === run.runId && e.data.status === "completed",
      ),
    );
    assert.ok(
      h.events.some(
        (e) =>
          e.type === "agent" &&
          e.data.id === "worker" &&
          e.data.status === "done",
      ),
    );
    assert.equal(
      h.events.some((e) => e.type === "error"),
      false,
    );
  } finally {
    await h.dispose();
  }
});

test("known provider startup diagnostics never become agent errors in either harness", async () => {
  for (const provider of ["claude", "codex"]) {
    const h = harness({ codex: provider === "codex" });
    try {
      const run = await h.api.startRun({
        ...h.input,
        provider,
        agents: [worker],
      });
      if (provider === "claude") {
        await until(() => h.processes.length === 1);
        h.processes[0].stdout.emit(
          "data",
          JSON.stringify({
            type: "error",
            message: "Reading additional input from stdin...",
          }) + "\n",
        );
        assert.ok(
          h.events.some(
            (e) =>
              e.type === "note" &&
              e.data.severity === "warning" &&
              /Reading additional input/.test(e.data.detail),
          ),
        );
        assert.equal(
          h.events.some((e) => e.type === "error"),
          false,
        );
        h.processes[0].close();
        await until(() => h.processes.length === 2);
        h.processes[1].close();
      } else {
        await until(() => h.turns.length === 1);
        const workerTurn = h.turns[0];
        h.processes[0].stdout.emit(
          "data",
          JSON.stringify({
            method: "error",
            params: {
              threadId: workerTurn.threadId,
              willRetry: false,
              error: { message: "Reading additional input from stdin..." },
            },
          }) + "\n",
        );
        assert.equal(
          h.events.some((e) => e.type === "error"),
          false,
        );
        for (const turn of h.turns) turn.complete();
        await until(() => h.turns.length === 2);
        h.turns[1].complete();
      }
      await until(() =>
        h.events.some(
          (e) => e.runId === run.runId && e.data.status === "completed",
        ),
      );
      assert.equal(
        h.events.some((e) => e.type === "error"),
        false,
        `${provider} startup diagnostics must remain warnings`,
      );
    } finally {
      await h.dispose();
    }
  }
});

test("human steering reaches the live orchestrator directly in Claude and Codex harnesses", async () => {
  for (const provider of ["claude", "codex"]) {
    const h = harness({ codex: provider === "codex" });
    try {
      const run = await h.api.startRun({
        ...h.input,
        provider,
        agents: [worker],
      });
      if (provider === "codex") {
        await until(() => h.turns.length === 1);
        assert.equal(
          h.turns.some(
            (turn) => turn.params.sandboxPolicy.type === "readOnly",
          ),
          false,
          "Codex does not start a supervisor before steering",
        );
        const receipt = await h.api.steerRun({
          runId: run.runId,
          id: `chief-${provider}`,
          text: "Réponds directement à cette indication.",
        });
        assert.ok(["transmitted", "consumed"].includes(receipt.status));
        await until(() => h.turns.length === 2);
        const chief = h.turns.find(
          (turn) => turn.params.sandboxPolicy.type === "readOnly",
        );
        assert.ok(chief, "human steering starts the Codex supervisor on demand");
        assert.match(chief.params.input[0].text, /Réponds directement/);
        assert.equal(
          h.requests.filter(
            (request) =>
              request.method === "turn/steer" &&
              request.params.clientUserMessageId === `chief-${provider}`,
          ).length,
          0,
          "the first supervisor pass receives steering in its prompt",
        );
      } else {
        await until(() => h.processes.length === 1);
        assert.equal(
          h.allProcesses.filter(isSupervisorProcess).length,
          0,
          "Claude does not start a supervisor before steering",
        );
        const receipt = await h.api.steerRun({
          runId: run.runId,
          id: `chief-${provider}`,
          text: "Réponds directement à cette indication.",
        });
        assert.ok(["transmitted", "consumed"].includes(receipt.status));
        await until(() => h.allProcesses.filter(isSupervisorProcess).length === 1);
        assert.match(
          childPrompt(h.allProcesses.filter(isSupervisorProcess).at(-1)),
          /Réponds directement à cette indication/,
        );
      }
    } finally {
      await h.dispose();
    }
  }
});

test("targeted worker steering is also visible to the orchestrator in both harnesses", async () => {
  for (const provider of ["claude", "codex"]) {
    const h = harness({ codex: provider === "codex" });
    try {
      const run = await h.api.startRun({
        ...h.input,
        provider,
        agents: [worker],
      });
      const text = "Le worker doit conserver cette contrainte.";
      if (provider === "codex") {
        await until(() => h.turns.length === 1);
        const workerTurn = h.turns[0];
        const receipt = await h.api.steerRun({
          runId: run.runId,
          id: `worker-${provider}`,
          text,
          agentId: "worker",
        });
        assert.ok(["transmitted", "consumed"].includes(receipt.status));
        await until(
          () =>
            h.requests.filter(
              (request) =>
                request.method === "turn/steer" &&
                request.params.clientUserMessageId === `worker-${provider}`,
            ).length === 1,
        );
        const deliveries = h.requests.filter(
          (request) =>
            request.method === "turn/steer" &&
            request.params.clientUserMessageId === `worker-${provider}`,
        );
        assert.deepEqual(
          new Set(deliveries.map((request) => request.params.threadId)),
          new Set([workerTurn.threadId]),
        );
        assert.equal(
          h.turns.some(
            (turn) => turn.params.sandboxPolicy.type === "readOnly",
          ),
          false,
          "targeted worker steering does not start an unrelated supervisor",
        );
      } else {
        await until(() => h.processes.length === 1);
        assert.equal(h.allProcesses.filter(isSupervisorProcess).length, 0);
        const receipt = await h.api.steerRun({
          runId: run.runId,
          id: `worker-${provider}`,
          text,
          agentId: "worker",
        });
        assert.equal(receipt.status, "transmitted");
        const previousWorker = h.processes[0];
        previousWorker.close();
        await until(
          () => h.processes.length === 2 && h.processes[1] !== previousWorker,
        );
        assert.match(
          childPrompt(h.processes[1]),
          /Le worker doit conserver/,
        );
        assert.equal(h.allProcesses.filter(isSupervisorProcess).length, 0);
      }
    } finally {
      await h.dispose();
    }
  }
});

test("long worker reports are bounded before the Claude or Codex chief validates its input", async () => {
  for (const provider of ["claude", "codex"]) {
    const h = harness({ codex: provider === "codex" });
    try {
      const run = await h.api.startRun({
        ...h.input,
        provider,
        agents: [worker],
      });
      const report = "preuve ".repeat(2_000);
      if (provider === "claude") {
        await until(() => h.processes.length === 1);
        h.processes[0].stdout.emit(
          "data",
          JSON.stringify({
            type: "assistant",
            message: { content: [{ type: "text", text: report }] },
          }) + "\n",
        );
        h.processes[0].close();
        await until(() => h.processes.length === 2);
        assert.equal(
          h.events.some(
            (event) =>
              event.type === "error" &&
              /workerSummaries\[0\]/.test(event.data.message || ""),
          ),
          false,
        );
        h.processes[1].close();
      } else {
        await until(() => h.turns.length === 1);
        const workerTurn = h.turns[0];
        workerTurn.complete("completed", report);
        await until(() => h.turns.length === 2);
        h.turns[1].complete("completed", "Intégration terminée.");
      }
      await until(() =>
        h.events.some(
          (event) =>
            event.runId === run.runId && event.data.status === "completed",
        ),
      );
      assert.equal(
        h.events.some(
          (event) =>
            event.type === "error" &&
            /workerSummaries\[0\]/.test(event.data.message || ""),
        ),
        false,
        `${provider} chief received a bounded worker summary`,
      );
    } finally {
      await h.dispose();
    }
  }
});

test("flexible lead passes accept next_step proposals while fixed passes ignore them", async () => {
  for (const workflowMode of ["flexible", "fixed"]) {
    for (const provider of ["claude", "codex"]) {
      const h = harness({ codex: provider === "codex" });
      try {
        const steps = runtime.workflow.createDefaultSteps("native");
        const projectSnapshot = {
          id: "project:native",
          name: "Fixture",
          directory: h.root,
          conventions: "",
          locations: {},
          workflows: [],
          updatedAt: "2026-10-06T08:00:00.000Z",
          capturedAt: "2026-10-06T08:00:00.000Z",
        };
        const project = {
          id: projectSnapshot.id,
          name: projectSnapshot.name,
          directory: projectSnapshot.directory,
          conventions: projectSnapshot.conventions,
          locations: projectSnapshot.locations,
          workflows: projectSnapshot.workflows,
          updatedAt: projectSnapshot.updatedAt,
        };
        const task = {
          ...taskFixture(),
          id: "native",
          project: h.root,
          projectId: projectSnapshot.id,
          projectSnapshot,
          provider,
          model: "configured-model",
          workflowMode,
          steps,
          activeStepId: steps[0].id,
          selectedStepId: steps[0].id,
        };
        await h.api.testSaveState({
          version: 2,
          tasks: [task],
          projects: [project],
        });
        const run = await h.api.startRun({
          ...h.input,
          provider,
          mode: "plan",
          stepId: steps[0].id,
        });
        const marker =
          'DJINN_EVENT:{"type":"next_step","data":{"type":"specification","title":"Clarifier","objective":"Décrire le contrat","reason":"La décision reste ouverte."}}';
        if (provider === "codex") {
          await until(() => h.turns.length === 1);
          h.turns[0].complete("completed", marker);
        } else {
          await until(() => h.processes.length === 1);
          h.processes[0].stdout.emit(
            "data",
            JSON.stringify({
              type: "assistant",
              message: { content: [{ type: "text", text: marker }] },
            }) + "\n",
          );
          h.processes[0].close();
        }
        await until(() =>
          h.events.some(
            (event) =>
              event.runId === run.runId && event.data.status === "completed",
          ),
        );
        assert.equal(
          h.events.filter((event) => event.type === "next_step").length,
          workflowMode === "flexible" ? 1 : 0,
          `${provider}/${workflowMode} workflow next_step policy`,
        );
      } finally {
        await h.dispose();
      }
    }
  }
});

test("a real Codex provider failure is reported once instead of duplicating the thread error", async () => {
  const h = harness({ codex: true });
  try {
    const run = await h.api.startRun(h.input);
    await until(() => h.turns.length === 1);
    h.processes[0].stdout.emit(
      "data",
      JSON.stringify({
        method: "error",
        params: {
          threadId: h.turns[0].threadId,
          willRetry: false,
          error: { message: "The configured model is unavailable." },
        },
      }) + "\n",
    );
    h.turns[0].complete("failed", "The configured model is unavailable.");
    await until(() =>
      h.events.some((e) => e.runId === run.runId && e.data.status === "error"),
    );
    const errors = h.events.filter(
      (e) => e.taskId === "native" && e.type === "error",
    );
    assert.equal(errors.length, 1);
    assert.match(errors[0].data.message, /configured model/);
  } finally {
    await h.dispose();
  }
});

test("a closed Codex server releases the mission and resumes its saved conversation on a fresh connection", async () => {
  const h = harness({ codex: true });
  try {
    const first = await h.api.startRun(h.input);
    await until(() => h.turns.length === 1);
    const session = h.events.find((e) => e.data.providerSession);
    assert.ok(session);
    h.processes[0].close(0);
    await until(() =>
      h.events.some(
        (e) => e.runId === first.runId && e.data.status === "error",
      ),
    );
    const second = await h.api.startRun({
      ...h.input,
      providerSessions: {
        [session.data.sessionKey]: session.data.providerThreadId,
      },
    });
    await until(() => h.turns.length === 2);
    assert.equal(h.processes.length, 2);
    assert.equal(h.turns[1].threadId, h.turns[0].threadId);
    const resume = h.requests.find((r) => r.method === "thread/resume");
    assert.equal(resume.params.excludeTurns, true);
    h.turns[1].complete();
    await until(() =>
      h.events.some(
        (e) => e.runId === second.runId && e.data.status === "completed",
      ),
    );
  } finally {
    await h.dispose();
  }
});

test("an unversioned saved Codex session migrates once to a native-tools thread", async () => {
  const h = harness({ codex: true });
  try {
    const run = await h.api.startRun({
      ...h.input,
      providerSessions: { "native:lead": "legacy-thread" },
    });
    await until(() => h.turns.length === 1);
    const starts = h.requests.filter((request) => request.method === "thread/start");
    const resumes = h.requests.filter((request) => request.method === "thread/resume");
    assert.equal(starts.length, 1);
    assert.equal(resumes.length, 0);
    assert.deepEqual(
      starts[0].params.dynamicTools.map((tool) => tool.name),
      runtime.nativeToolDefinitions().map((tool) => tool.name),
    );
    const turnStart = h.requests.find((request) => request.method === "turn/start");
    assert.match(
      turnStart.params.input[0].text,
      /Native interaction session migration/,
    );
    h.turns[0].complete();
    await until(() =>
      h.events.some(
        (event) => event.runId === run.runId && event.data.status === "completed",
      ),
    );
  } finally {
    await h.dispose();
  }
});

test("an observed Codex child can use the legacy DJINN_EVENT fallback", async () => {
  const h = harness({ codex: true });
  try {
    const run = await h.api.startRun(h.input);
    await until(() => h.turns.length === 1);
    const leadTurn = h.turns[0];
    const emit = (message) =>
      h.allProcesses[0].stdout.emit("data", JSON.stringify(message) + "\n");
    emit({
      method: "thread/started",
      params: {
        thread: {
          id: "observed-child",
          parentThreadId: leadTurn.threadId,
          agentNickname: "Observed worker",
          status: { type: "active" },
        },
      },
    });
    await until(() =>
      h.events.some(
        (event) => event.type === "agent" && event.data.id === "codex:observed-child",
      ),
    );
    emit({
      method: "item/completed",
      params: {
        threadId: "observed-child",
        item: {
          id: "observed-message",
          type: "agentMessage",
          text: 'DJINN_EVENT:{"type":"report","data":{"status":"ready","summary":"Child checked the fixture","completed":["fixture"],"remaining":[],"evidence":["native-orchestration"]}}',
        },
      },
    });
    await until(() =>
      h.events.some(
        (event) =>
          event.type === "report" &&
          event.data.agentId === "codex:observed-child" &&
          event.data.parentRunId === run.runId,
      ),
    );
    leadTurn.complete();
    await until(() =>
      h.events.some(
        (event) => event.runId === run.runId && event.data.status === "completed",
      ),
    );
  } finally {
    await h.dispose();
  }
});

test("managed native tools acknowledge only after durable interaction and action storage", async () => {
  const h = harness({ codex: true });
  const emitToolCall = (turn, id, tool, argumentsValue) =>
    h.allProcesses[0].stdout.emit(
      "data",
      JSON.stringify({
        id,
        method: "item/tool/call",
        params: {
          threadId: turn.threadId,
          turnId: turn.id,
          namespace: null,
          tool,
          arguments: argumentsValue,
        },
      }) + "\n",
    );
  try {
    const run = await h.api.startRun(h.input);
    await until(() => h.turns.length === 1);
    const turn = h.turns[0];
    emitToolCall(turn, "native-question", "publish_question", {
      title: "Need a choice",
      context: "The independent work can continue.",
      blocking: false,
      options: ["Keep going"],
    });
    await until(() =>
      h.events.some(
        (event) => event.type === "question" && event.data.title === "Need a choice",
      ),
    );
    await until(() =>
      h.requests.some((request) => request.id === "native-question"),
    );
    const questionResponse = h.requests
      .filter((request) => request.id === "native-question")
      .at(-1);
    assert.equal(questionResponse.result.success, true);
    let interactions = await h.api.getMissionInteractions("native");
    assert.ok(
      interactions.events.some(
        (event) => event.type === "question" && event.data.title === "Need a choice",
      ),
    );

    emitToolCall(turn, "native-action", "publish_test_action", {
      id: "fixture-action",
      kind: "manual",
      title: "Run the fixture check",
      workItemId: "work-1",
      target: "worker-a",
    });
    await until(() =>
      h.events.some(
        (event) => event.type === "action" && event.data.id === "fixture-action",
      ),
    );
    await until(() => h.requests.some((request) => request.id === "native-action"));
    const actionResponse = h.requests
      .filter((request) => request.id === "native-action")
      .at(-1);
    assert.equal(actionResponse.result.success, true);
    interactions = await h.api.getMissionInteractions("native");
    assert.ok(
      interactions.events.some(
        (event) => event.type === "action" && event.data.id === "fixture-action",
      ),
    );
    turn.complete();
    await until(() =>
      h.events.some(
        (event) => event.runId === run.runId && event.data.status === "completed",
      ),
    );
  } finally {
    await h.dispose();
  }
});

test("a native worker question blocks only its own passage", async () => {
  const h = harness({ codex: true });
  const agents = [
    { ...worker, id: "worker-a", name: "Worker A", writeScope: ["a.txt"] },
    { ...worker, id: "worker-b", name: "Worker B", writeScope: ["b.txt"] },
  ];
  const emitToolCall = (turn) =>
    h.allProcesses[0].stdout.emit(
      "data",
      JSON.stringify({
        id: "worker-question",
        method: "item/tool/call",
        params: {
          threadId: turn.threadId,
          turnId: turn.id,
          namespace: null,
          tool: "publish_question",
          arguments: {
            title: "Worker input",
            context: "Only this worker is waiting.",
            blocking: true,
          },
        },
      }) + "\n",
    );
  try {
    const run = await h.api.startRun({
      ...h.input,
      concurrency: 2,
      agents,
    });
    await until(() => h.turns.length === 2);
    const first = h.turns.find((turn) =>
      turn.params.input[0].text.includes("Worker A"),
    );
    const second = h.turns.find((turn) => turn !== first);
    assert.ok(first);
    assert.ok(second);
    emitToolCall(first);
    await until(() =>
      h.events.some(
        (event) =>
          event.type === "question" &&
          event.data.title === "Worker input" &&
          event.data.blockingScope === "agent",
      ),
    );
    await until(() => h.requests.some((request) => request.id === "worker-question"));
    assert.equal(h.requests.filter((request) => request.method === "turn/interrupt").length, 1);
    assert.equal(second.done, undefined);
    assert.equal(
      h.events.some(
        (event) => event.runId === run.runId && event.data.status === "error",
      ),
      false,
    );
    second.complete();
    await until(() => h.turns.length === 3);
    h.turns[2].complete();
    await until(() =>
      h.events.some(
        (event) => event.runId === run.runId && event.data.status === "completed",
      ),
    );
  } finally {
    await h.dispose();
  }
});

test("large native Codex records keep the mission alive through successful completion", async () => {
  const h = harness({ codex: true });
  try {
    const run = await h.api.startRun(h.input);
    await until(() => h.turns.length === 1);
    const turn = h.turns[0];
    const diff =
      JSON.stringify({
        method: "turn/diff/updated",
        params: {
          threadId: turn.threadId,
          turnId: turn.id,
          diff: "+updated\n".repeat(150_000),
        },
      }) + "\n";
    for (let i = 0; i < diff.length; i += 64_000)
      h.processes[0].stdout.emit("data", diff.slice(i, i + 64_000));
    turn.complete("completed", "L’implémentation est terminée.");
    await until(() =>
      h.events.some(
        (e) =>
          e.runId === run.runId &&
          e.type === "status" &&
          e.data.status === "completed",
      ),
    );
    assert.equal(
      h.events.some((e) => e.type === "error"),
      false,
    );
    assert.equal(h.kills.length, 0);
    assert.ok(
      h.events.some(
        (e) => e.type === "text" && e.data.text.includes("terminée"),
      ),
    );
  } finally {
    await h.dispose();
  }
});

test("Codex streaming updates activity and finalizes one native message identity", async () => {
  const h = harness({ codex: true });
  try {
    await h.api.startRun(h.input);
    await until(() => h.turns.length === 1);
    h.turns[0].delta("Travail en cours");
    assert.ok(
      h.events.some(
        (e) =>
          e.type === "text" &&
          e.data.streaming &&
          e.data.text === "Travail en cours",
      ),
    );
    h.turns[0].complete(
      "completed",
      'Travail terminé.\nDJINN_EVENT:{"type":"note","data":{"title":"Preuve","detail":"Vérifiée"}}',
    );
    await tick();
    const messages = h.events.filter(
      (e) => e.type === "text" && e.data.streaming,
    );
    assert.equal(messages[0].data.messageId, messages.at(-1).data.messageId);
    assert.equal(messages.at(-1).data.final, true);
    assert.equal(messages.at(-1).data.text.trim(), "Travail terminé.");
    assert.ok(
      h.events.some((e) => e.type === "note" && e.data.title === "Preuve"),
    );
    assert.ok(!messages.at(-1).data.text.includes("DJINN_EVENT:"));
  } finally {
    await h.dispose();
  }
});
test("native cancellation retains the writer until the actual turn is interrupted", async () => {
  const h = harness();
  try {
    const run = await h.api.startRun(h.input);
    await until(() => h.processes.length === 1);
    await h.api.cancelRun(run.runId);
    assert.ok(h.events.some((e) => e.data.status === "stopping"));
    assert.ok(
      !h.events.some(
        (e) => e.runId === run.runId && e.data.status === "cancelled",
      ),
    );
    await assert.rejects(h.api.startRun(h.input), /active|owns/);
    h.processes[0].close();
    await until(() =>
      h.events.some(
        (e) => e.runId === run.runId && e.data.status === "cancelled",
      ),
    );
  } finally {
    await h.dispose();
  }
});

test("native cancellation interrupts observed Codex child turns and waits for closure", async () => {
  const h = harness({ codex: true });
  const notify = (method, params) =>
    h.allProcesses[0].stdout.emit(
      "data",
      JSON.stringify({ method, params }) + "\n",
    );
  try {
    const run = await h.api.startRun({ ...h.input, agents: [] });
    await until(() => h.turns.length === 1);
    const leadThread = h.turns[0].threadId;
    notify("item/completed", {
      threadId: leadThread,
      item: {
        type: "collabAgentToolCall",
        id: "cancel-child-call",
        tool: "spawnAgent",
        status: "completed",
        senderThreadId: leadThread,
        receiverThreadIds: ["cancel-child"],
        agentsStates: {
          "cancel-child": { status: "running", message: null },
        },
      },
    });
    notify("turn/started", {
      threadId: "cancel-child",
      turn: { id: "cancel-child-turn", status: "inProgress" },
    });
    assert.ok(
      h.api
        .testRuntimeSnapshot()
        .runs[0].observedAgents.some(
          (agent) => agent.providerThreadId === "cancel-child",
        ),
    );
    const cancellation = h.api.cancelRun(run.runId);
    await until(() =>
      h.requests.some(
        (request) =>
          request.method === "turn/interrupt" &&
          request.params.threadId === "cancel-child" &&
          request.params.turnId === "cancel-child-turn",
      ),
    );
    assert.equal(
      h.events.some(
        (event) => event.runId === run.runId && event.data.status === "cancelled",
      ),
      false,
      "root cancellation waits for the observed child turn",
    );
    notify("turn/completed", {
      threadId: "cancel-child",
      turn: { id: "cancel-child-turn", status: "interrupted" },
    });
    await cancellation;
    await until(() =>
      h.events.some(
        (event) => event.runId === run.runId && event.data.status === "cancelled",
      ),
    );
    assert.ok(
      h.events.some(
        (event) =>
          event.type === "agent" &&
          event.data.id === "codex:cancel-child" &&
          event.data.live === false &&
          event.data.status === "blocked",
      ),
    );
  } finally {
    await h.dispose();
  }
});

test("native root cancellation finds the task app-server after its lead pass retires", async () => {
  const h = harness({ codex: true });
  const notify = (method, params) =>
    h.allProcesses[0].stdout.emit(
      "data",
      JSON.stringify({ method, params }) + "\n",
    );
  try {
    const run = await h.api.startRun({ ...h.input, agents: [] });
    await until(() => h.turns.length === 1);
    const leadThread = h.turns[0].threadId;
    notify("item/completed", {
      threadId: leadThread,
      item: {
        type: "collabAgentToolCall",
        id: "retired-pass-child-call",
        tool: "spawnAgent",
        status: "completed",
        senderThreadId: leadThread,
        receiverThreadIds: ["retired-pass-child"],
        agentsStates: {
          "retired-pass-child": { status: "running", message: null },
        },
      },
    });
    notify("turn/started", {
      threadId: "retired-pass-child",
      turn: { id: "retired-pass-child-turn", status: "inProgress" },
    });
    const activeRuns = h.api.testActiveRuns();
    const root = activeRuns.get(run.runId);
    const leadPass = [...activeRuns.values()].find(
      (candidate) => candidate.parentRunId === run.runId,
    );
    assert.ok(root && leadPass);
    activeRuns.delete(leadPass.runId);
    root.children.clear();
    root.currentChild = null;
    h.api.cancelRun(run.runId);
    await until(() =>
      h.requests.some(
        (request) =>
          request.method === "turn/interrupt" &&
          request.params.threadId === "retired-pass-child" &&
          request.params.turnId === "retired-pass-child-turn",
      ),
    );
    notify("turn/completed", {
      threadId: "retired-pass-child",
      turn: { id: "retired-pass-child-turn", status: "interrupted" },
    });
    await until(() =>
      h.events.some(
        (event) => event.runId === run.runId && event.data.status === "cancelled",
      ),
    );
  } finally {
    await h.dispose();
  }
});

test("native cancellation bounds an acknowledged child interruption without inventing its status", async () => {
  const h = harness({ codex: true });
  const notify = (method, params) =>
    h.allProcesses[0].stdout.emit(
      "data",
      JSON.stringify({ method, params }) + "\n",
    );
  try {
    const run = await h.api.startRun({ ...h.input, agents: [] });
    await until(() => h.turns.length === 1);
    const leadThread = h.turns[0].threadId;
    notify("item/completed", {
      threadId: leadThread,
      item: {
        type: "collabAgentToolCall",
        id: "timeout-child-call",
        tool: "spawnAgent",
        status: "completed",
        senderThreadId: leadThread,
        receiverThreadIds: ["timeout-child"],
        agentsStates: {
          "timeout-child": { status: "running", message: null },
        },
      },
    });
    notify("turn/started", {
      threadId: "timeout-child",
      turn: { id: "timeout-child-turn", status: "inProgress" },
    });
    h.api.cancelRun(run.runId);
    await until(() =>
      h.requests.some(
        (request) =>
          request.method === "turn/interrupt" &&
          request.params.threadId === "timeout-child" &&
          request.params.turnId === "timeout-child-turn",
      ),
    );
    await until(() =>
      h.kills.some((pid) => Math.abs(pid) === h.allProcesses[0].pid),
    );
    assert.equal(
      h.api.testActiveRuns().has(run.runId),
      true,
      "project ownership stays reserved until the app-server actually closes",
    );
    h.allProcesses[0].close();
    await until(() =>
      h.events.some(
        (event) => event.runId === run.runId && event.data.status === "cancelled",
      ),
    );
    assert.ok(
      h.events.some(
        (event) =>
          event.type === "note" &&
          event.data.title === "Interruption du sous-agent Codex non confirmée" &&
          /fermeture/.test(event.data.detail || ""),
      ),
    );
    assert.ok(
      h.events.some(
        (event) =>
          event.type === "agent" &&
          event.data.id === "codex:timeout-child" &&
          event.data.live === false &&
          event.data.status === "running",
      ),
      "the last observed child status is preserved while local ownership closes",
    );
  } finally {
    await h.dispose();
  }
});

test("native cancellation closes the app-server after a rejected child interruption", async () => {
  const h = harness({ codex: true, rejectInterrupt: true });
  const notify = (method, params) =>
    h.allProcesses[0].stdout.emit(
      "data",
      JSON.stringify({ method, params }) + "\n",
    );
  try {
    const run = await h.api.startRun({ ...h.input, agents: [] });
    await until(() => h.turns.length === 1);
    const leadThread = h.turns[0].threadId;
    notify("item/completed", {
      threadId: leadThread,
      item: {
        type: "collabAgentToolCall",
        id: "rejected-interrupt-call",
        tool: "spawnAgent",
        status: "completed",
        senderThreadId: leadThread,
        receiverThreadIds: ["rejected-interrupt-child"],
        agentsStates: {
          "rejected-interrupt-child": { status: "running", message: null },
        },
      },
    });
    notify("turn/started", {
      threadId: "rejected-interrupt-child",
      turn: { id: "rejected-interrupt-child-turn", status: "inProgress" },
    });
    h.api.cancelRun(run.runId);
    await until(() =>
      h.requests.some(
        (request) =>
          request.method === "turn/interrupt" &&
          request.params.threadId === "rejected-interrupt-child" &&
          request.params.turnId === "rejected-interrupt-child-turn",
      ),
    );
    await until(() =>
      h.events.some(
        (event) =>
          event.type === "note" &&
          event.data.title === "Interruption du sous-agent Codex non confirmée" &&
          /accusé/.test(event.data.detail || ""),
      ),
    );
    await until(() =>
      h.kills.some((pid) => Math.abs(pid) === h.allProcesses[0].pid),
    );
    assert.equal(
      h.api.testActiveRuns().has(run.runId),
      true,
      "a rejected child interrupt keeps project ownership until transport close",
    );
    h.allProcesses[0].close();
    await until(() =>
      h.events.some(
        (event) => event.runId === run.runId && event.data.status === "cancelled",
      ),
    );
  } finally {
    await h.dispose();
  }
});

test("native cancellation reports an observed child without a resolvable active turn", async () => {
  const h = harness({ codex: true });
  const notify = (method, params) =>
    h.allProcesses[0].stdout.emit(
      "data",
      JSON.stringify({ method, params }) + "\n",
    );
  try {
    const run = await h.api.startRun({ ...h.input, agents: [] });
    await until(() => h.turns.length === 1);
    const leadThread = h.turns[0].threadId;
    notify("item/completed", {
      threadId: leadThread,
      item: {
        type: "collabAgentToolCall",
        id: "missing-turn-call",
        tool: "spawnAgent",
        status: "completed",
        senderThreadId: leadThread,
        receiverThreadIds: ["missing-turn-child"],
        agentsStates: {
          "missing-turn-child": { status: "running", message: null },
        },
      },
    });
    const cancellation = h.api.cancelRun(run.runId);
    await until(() =>
      h.requests.some(
        (request) =>
          request.method === "thread/turns/list" &&
          request.params.threadId === "missing-turn-child" &&
          request.params.limit === 1 &&
          request.params.sortDirection === "desc" &&
          request.params.itemsView === "notLoaded",
      ),
    );
    await cancellation;
    await until(() =>
      h.kills.some((pid) => Math.abs(pid) === h.allProcesses[0].pid),
    );
    h.allProcesses[0].close();
    await until(() =>
      h.events.some(
        (event) => event.runId === run.runId && event.data.status === "cancelled",
      ),
    );
    assert.equal(
      h.requests.some(
        (request) =>
          request.method === "thread/read" ||
          request.method === "turn/interrupt" &&
          request.params.threadId === "missing-turn-child",
      ),
      false,
      "no unbounded history read or interruption is claimed without an active child turn handle",
    );
    assert.ok(
      h.events.some(
        (event) =>
          event.type === "note" &&
          event.data.title === "Interruption du sous-agent Codex non confirmée",
      ),
    );
    assert.equal(
      h.events.some(
        (event) =>
          event.type === "agent" &&
          event.data.id === "codex:missing-turn-child" &&
          event.data.live === false &&
          event.data.status === "running",
      ),
      true,
      "local ownership closes while the last unresolved child status is preserved",
    );
  } finally {
    await h.dispose();
  }
});

test("native blocking pause waits for an observed Codex child turn to close", async () => {
  const h = harness({ codex: true });
  const notify = (method, params) =>
    h.allProcesses[0].stdout.emit(
      "data",
      JSON.stringify({ method, params }) + "\n",
    );
  try {
    const run = await h.api.startRun({ ...h.input, agents: [] });
    await until(() => h.turns.length === 1);
    const leadThread = h.turns[0].threadId;
    notify("item/completed", {
      threadId: leadThread,
      item: {
        type: "collabAgentToolCall",
        id: "pause-child-call",
        tool: "spawnAgent",
        status: "completed",
        senderThreadId: leadThread,
        receiverThreadIds: ["pause-child"],
        agentsStates: {
          "pause-child": { status: "running", message: null },
        },
      },
    });
    notify("turn/started", {
      threadId: "pause-child",
      turn: { id: "pause-child-turn", status: "inProgress" },
    });
    h.turns[0].message(
      'DJINN_EVENT:{"type":"question","data":{"id":"pause-question","title":"Choisir","blocking":true}}',
    );
    await until(() =>
      h.requests.some(
        (request) =>
          request.method === "turn/interrupt" &&
          request.params.threadId === "pause-child" &&
          request.params.turnId === "pause-child-turn",
      ),
    );
    assert.equal(
      h.events.some(
        (event) => event.runId === run.runId && event.data.status === "completed",
      ),
      false,
      "blocking pause waits for the observed child turn",
    );
    notify("turn/completed", {
      threadId: "pause-child",
      turn: { id: "pause-child-turn", status: "interrupted" },
    });
    await until(() =>
      h.events.some(
        (event) => event.runId === run.runId && event.data.status === "completed",
      ),
    );
    assert.ok(
      h.events.some(
        (event) =>
          event.type === "agent" &&
          event.data.id === "codex:pause-child" &&
          event.data.live === false &&
          event.data.status === "blocked",
      ),
    );
  } finally {
    await h.dispose();
  }
});

test("native cancellation resolves a missing child turn through bounded pagination", async () => {
  const h = harness({
    codex: true,
    threadTurns: [{ id: "listed-child-turn", status: "inProgress" }],
  });
  const notify = (method, params) =>
    h.allProcesses[0].stdout.emit(
      "data",
      JSON.stringify({ method, params }) + "\n",
    );
  try {
    const run = await h.api.startRun({ ...h.input, agents: [] });
    await until(() => h.turns.length === 1);
    const leadThread = h.turns[0].threadId;
    notify("item/completed", {
      threadId: leadThread,
      item: {
        type: "collabAgentToolCall",
        id: "listed-turn-call",
        tool: "spawnAgent",
        status: "completed",
        senderThreadId: leadThread,
        receiverThreadIds: ["listed-turn-child"],
        agentsStates: {
          "listed-turn-child": { status: "running", message: null },
        },
      },
    });
    const cancellation = h.api.cancelRun(run.runId);
    await until(() =>
      h.requests.some(
        (request) =>
          request.method === "thread/turns/list" &&
          request.params.threadId === "listed-turn-child",
      ),
    );
    await until(() =>
      h.requests.some(
        (request) =>
          request.method === "turn/interrupt" &&
          request.params.threadId === "listed-turn-child" &&
          request.params.turnId === "listed-child-turn",
      ),
    );
    assert.equal(
      h.requests.some((request) => request.method === "thread/read"),
      false,
    );
    notify("turn/completed", {
      threadId: "listed-turn-child",
      turn: { id: "listed-child-turn", status: "interrupted" },
    });
    await cancellation;
    await until(() =>
      h.events.some(
        (event) => event.runId === run.runId && event.data.status === "cancelled",
      ),
    );
  } finally {
    await h.dispose();
  }
});

test("native migration preserves recoverable v1 bytes and remains idempotent", async () => {
  const h = harness();
  try {
    const original = JSON.stringify({
      version: 1,
      tasks: [{ ...taskFixture(), project: h.root }],
    });
    fs.writeFileSync(path.join(h.root, "state.json"), original);
    const migrated = await h.api.testLoadState();
    assert.equal(migrated.version, 2);
    assert.equal(migrated.projects.length, 1);
    assert.equal(migrated.tasks[0].steps.length, 4);
    assert.equal(
      fs.readFileSync(path.join(h.root, "state.json.v1.backup"), "utf8"),
      original,
    );
    assert.deepEqual(await h.api.testLoadState(), migrated);
    assert.equal(
      JSON.parse(fs.readFileSync(path.join(h.root, "state.json"), "utf8"))
        .version,
      2,
    );
  } finally {
    await h.dispose();
  }
});
test("invalid persisted bytes are preserved instead of silently resetting missions", async () => {
  const h = harness();
  try {
    const original = "{invalid";
    fs.writeFileSync(path.join(h.root, "state.json"), original);
    await assert.rejects(h.api.testLoadState(), /malformed/);
    assert.equal(
      fs.readFileSync(path.join(h.root, "state.json"), "utf8"),
      original,
    );
  } finally {
    await h.dispose();
  }
});
test("a human rename saved during a Codex turn prevents a later provider title overwrite", async () => {
  const h = harness({ codex: true });
  try {
    const steps = runtime.workflow.createDefaultSteps("native");
    const task = {
      ...taskFixture(),
      id: "native",
      project: h.root,
      model: "configured-model",
      steps,
      activeStepId: steps[0].id,
      selectedStepId: steps[0].id,
      titleSource: "placeholder",
    };
    const state = { version: 2, tasks: [task], projects: [] };
    await h.api.testSaveState(state);
    await h.api.startRun({ ...h.input, mode: "plan", stepId: steps[0].id });
    await until(() => h.turns.length === 1);
    task.title = "Mon titre";
    task.titleSource = "human";
    task.titleEditedAt = new Date().toISOString();
    await h.api.testSaveState(state);
    h.turns[0].complete(
      "completed",
      'DJINN_EVENT:{"type":"mission_metadata","data":{"title":"Titre tardif"}}',
    );
    await tick();
    assert.ok(!h.events.some((e) => e.type === "mission_metadata"));
    assert.equal((await h.api.testLoadState()).tasks[0].title, "Mon titre");
  } finally {
    await h.dispose();
  }
});

test("native disjoint writers start together, conflicts wait locally and freed slots refill immediately", async () => {
  const h = harness({ codex: true });
  try {
    const agents = [
      { ...worker, id: "one", name: "One", writeScope: ["one.txt"] },
      { ...worker, id: "shared", name: "Shared", writeScope: ["one.txt"] },
      { ...worker, id: "two", name: "Two", writeScope: ["two.txt"] },
      { ...worker, id: "three", name: "Three", writeScope: ["three.txt"] },
    ];
    const run = await h.api.startRun({ ...h.input, concurrency: 2, agents });
    await until(() => h.turns.length === 2);
    const one = h.turns.find((t) =>
      t.params.sandboxPolicy.writableRoots?.includes(
        path.join(fs.realpathSync(h.root), "one.txt"),
      ),
    );
    const two = h.turns.find((t) =>
      t.params.sandboxPolicy.writableRoots?.includes(
        path.join(fs.realpathSync(h.root), "two.txt"),
      ),
    );
    assert.ok(one && two);
    assert.ok(!one.done && !two.done);
    assert.ok(
      h.events.some(
        (e) =>
          e.type === "agent" &&
          e.data.id === "shared" &&
          e.data.waitKind === "ownership_conflict" &&
          e.data.waitingForAgentIds.includes("one"),
      ),
    );
    two.complete("completed", "Second finished");
    await until(() =>
      h.turns.some((t) =>
        t.params.sandboxPolicy.writableRoots?.includes(
          path.join(fs.realpathSync(h.root), "three.txt"),
        ),
      ),
    );
    assert.equal(
      one.done,
      undefined,
      "independent third starts while first is still active",
    );
    const three = h.turns.find((t) =>
      t.params.sandboxPolicy.writableRoots?.includes(
        path.join(fs.realpathSync(h.root), "three.txt"),
      ),
    );
    one.complete();
    await until(
      () =>
        h.turns.filter((t) =>
          t.params.sandboxPolicy.writableRoots?.includes(
            path.join(fs.realpathSync(h.root), "one.txt"),
          ),
        ).length === 2,
    );
    const shared = h.turns
      .filter((t) =>
        t.params.sandboxPolicy.writableRoots?.includes(
          path.join(fs.realpathSync(h.root), "one.txt"),
        ),
      )
      .at(-1);
    assert.ok(one.done, "shared file has one writer at a time");
    shared.complete();
    three.complete();
    await until(() =>
      h.turns.some((t) =>
        t.params.sandboxPolicy.writableRoots?.includes(h.root),
      ),
    );
    h.turns
      .find((t) => t.params.sandboxPolicy.writableRoots?.includes(h.root))
      .complete();
    await until(() =>
      h.events.some(
        (e) => e.runId === run.runId && e.data.status === "completed",
      ),
    );
  } finally {
    await h.dispose();
  }
});
test("native dependency waits for the contract while independent scope executes, then integrates alone", async () => {
  const h = harness({ codex: true });
  try {
    const agents = [
      {
        ...worker,
        id: "contract",
        name: "Contract",
        writeScope: ["contract.ts"],
      },
      {
        ...worker,
        id: "consumer",
        name: "Consumer",
        writeScope: ["consumer.ts"],
        dependsOn: ["contract"],
      },
      {
        ...worker,
        id: "independent",
        name: "Independent",
        writeScope: ["independent.ts"],
      },
    ];
    await h.api.startRun({ ...h.input, concurrency: 3, agents });
    await until(() => h.turns.length === 2);
    const writer = (scope) =>
      h.turns.find((t) =>
        t.params.sandboxPolicy.writableRoots?.includes(
          path.join(fs.realpathSync(h.root), scope),
        ),
      );
    assert.ok(writer("contract.ts"));
    assert.ok(writer("independent.ts"));
    assert.equal(writer("consumer.ts"), undefined);
    assert.ok(
      h.events.some(
        (e) => e.data.id === "consumer" && e.data.waitKind === "dependency",
      ),
    );
    writer("contract.ts").complete();
    await until(() => writer("consumer.ts"));
    assert.equal(writer("independent.ts").done, undefined);
    writer("consumer.ts").complete();
    await tick();
    assert.ok(
      !h.turns.some((t) =>
        t.params.sandboxPolicy.writableRoots?.includes(h.root),
      ),
      "chief integration waits for actual writers",
    );
    writer("independent.ts").complete();
    await until(() =>
      h.turns.some((t) =>
        t.params.sandboxPolicy.writableRoots?.includes(h.root),
      ),
    );
    h.turns.at(-1).complete();
  } finally {
    await h.dispose();
  }
});
test("runtime reconnect snapshots replay only existing native ownership and stable event IDs", async () => {
  const h = harness({ codex: true });
  try {
    assert.equal(h.api.testRuntimeSnapshot().runs.length, 0);
    const run = await h.api.startRun(h.input);
    await until(() => h.turns.length === 1);
    h.turns[0].delta("Progress");
    const snapshot = h.api.testRuntimeSnapshot();
    assert.equal(snapshot.runs.length, 1);
    assert.equal(snapshot.runs[0].runId, run.runId);
    assert.equal(snapshot.runs[0].phase, "lead");
    assert.equal(snapshot.runs[0].activeAgents[0].id, "lead");
    assert.equal(
      snapshot.runs[0].activeAgents[0].runId,
      h.events.find((e) => e.data.agentId === "lead").runId,
    );
    assert.ok(
      snapshot.runs[0].events.every((e) => typeof e.eventId === "string"),
    );
    const count = h.turns.length;
    assert.deepEqual(
      h.api.testRuntimeSnapshot().runs[0].events.map((e) => e.eventId),
      snapshot.runs[0].events.map((e) => e.eventId),
    );
    assert.equal(h.turns.length, count);
    h.turns[0].complete();
    await until(() => h.api.testRuntimeSnapshot().runs.length === 0);
  } finally {
    await h.dispose();
  }
});
test("runtime reconnect cache stays bounded by event count and serialized characters", async () => {
  const h = harness({ codex: true });
  const notify = (method, params) =>
    h.allProcesses[0].stdout.emit(
      "data",
      JSON.stringify({ method, params }) + "\n",
    );
  const cacheSize = (events) =>
    events.reduce((sum, event) => sum + JSON.stringify(event).length, 0);
  try {
    await h.api.startRun(h.input);
    await until(() => h.turns.length === 1);
    const threadId = h.turns[0].threadId;
    for (let index = 0; index < 300; index++)
      notify("item/completed", {
        threadId,
        item: {
          id: `bounded-message-${index}`,
          type: "agentMessage",
          text: `Event ${index}`,
        },
      });
    let events = h.api.testRuntimeSnapshot().runs[0].events;
    assert.equal(events.length, 250);
    assert.ok(cacheSize(events) <= 2_000_000);
    for (let index = 0; index < 30; index++)
      notify("item/completed", {
        threadId,
        item: {
          id: `large-message-${index}`,
          type: "agentMessage",
          text: "x".repeat(100_000),
        },
      });
    events = h.api.testRuntimeSnapshot().runs[0].events;
    assert.ok(events.length <= 250);
    assert.ok(cacheSize(events) <= 2_000_000);
    h.turns[0].complete();
  } finally {
    await h.dispose();
  }
});
test("native project ownership excludes parent and descendant pipelines until actual completion", async () => {
  const h = harness();
  try {
    const nested = path.join(h.root, "nested");
    fs.mkdirSync(nested);
    await h.api.startRun(h.input);
    await until(() => h.processes.length === 1);
    await assert.rejects(
      h.api.startRun({ ...h.input, taskId: "another", cwd: nested }),
      /active/,
    );
    h.processes[0].close();
    await until(() => h.api.testRuntimeSnapshot().runs.length === 0);
    await h.api.startRun({ ...h.input, taskId: "another", cwd: nested });
    await until(() => h.processes.length === 2);
    await assert.rejects(h.api.startRun(h.input), /active/);
    h.processes[1].close();
  } finally {
    await h.dispose();
  }
});
test("finished-worker steering fills an available scoped slot while its independent peer continues", async () => {
  const h = harness({ codex: true });
  try {
    const run = await h.api.startRun({
      ...h.input,
      concurrency: 2,
      agents: [
        { ...worker, id: "one", writeScope: ["one.ts"] },
        { ...worker, id: "two", writeScope: ["two.ts"] },
      ],
    });
    await until(() => h.turns.length === 2);
    const writers = h.turns.filter(
      (t) => t.params.sandboxPolicy.type === "workspaceWrite",
    );
    const one = writers.find((t) =>
      t.params.sandboxPolicy.writableRoots.includes(
        path.join(fs.realpathSync(h.root), "one.ts"),
      ),
    );
    const two = writers.find((t) => t !== one);
    one.complete();
    await until(() =>
      h.events.some(
        (e) =>
          e.type === "agent" && e.data.id === "one" && e.data.status === "done",
      ),
    );
    await h.api.steerRun({
      runId: run.runId,
      id: "retry-independent",
      agentId: "one",
      text: "Targeted correction while peer continues.",
    });
    await until(() =>
      h.turns.some((t) => t !== one && t.threadId === one.threadId),
    );
    const retry = h.turns.find((t) => t !== one && t.threadId === one.threadId);
    assert.equal(two.done, undefined);
    assert.match(
      retry.params.input[0].text,
      /Targeted correction while peer continues/,
    );
    assert.equal(
      h.events.some(
        (e) =>
          e.data.id === "retry-independent" && e.data.status === "consumed",
      ),
      true,
    );
    for (const turn of h.turns.filter((t) => !t.done)) turn.complete();
    await until(() =>
      h.turns.some((t) =>
        t.params.sandboxPolicy.writableRoots?.includes(h.root),
      ),
    );
    h.turns.at(-1).complete();
  } finally {
    await h.dispose();
  }
});
test("read-only inspector in an execute stage retains native step scope and cannot gain write permissions", async () => {
  const h = harness({ codex: true });
  try {
    const steps = runtime.workflow.createDefaultSteps("native");
    steps[0] = {
      ...steps[0],
      status: "completed",
      approvedBy: "human",
      approvedAt: "2026-10-06T08:00:00Z",
    };
    const task = {
      ...taskFixture(),
      id: "native",
      project: h.root,
      provider: "codex",
      model: "configured-model",
      phase: "execution",
      steps,
      activeStepId: steps[1].id,
      selectedStepId: steps[1].id,
      configuration: { ...taskFixture().configuration, concurrency: 2 },
    };
    await h.api.testSaveState({ version: 2, tasks: [task], projects: [] });
    await h.api.startRun({
      ...h.input,
      stepId: steps[1].id,
      concurrency: 2,
      agents: [
        { ...worker, id: "writer", writeScope: ["owned.ts"] },
        { ...worker, id: "inspector", readOnly: true, writeScope: ["*"] },
      ],
    });
    await until(() => h.turns.length === 2);
    const inspecting = h.turns.find((t) =>
      t.params.input[0].text.includes("Ownership: Read-only inspection"),
    );
    assert.ok(inspecting);
    assert.equal(inspecting.params.sandboxPolicy.type, "readOnly");
    assert.ok(
      h.turns.some((t) => t.params.sandboxPolicy.type === "workspaceWrite"),
    );
    assert.ok(
      h.events
        .filter((e) => e.taskId === "native")
        .every((e) => e.stepId === steps[1].id),
    );
    for (const t of h.turns) t.complete();
    await until(() => h.turns.length === 3);
    h.turns[2].complete();
  } finally {
    await h.dispose();
  }
});

test("automatic qualification remains read-only and restarts with the selected permissions under both harnesses", async () => {
  for (const provider of ["claude", "codex"]) {
    for (const chosenType of ["specification", "implementation"]) {
      const h = harness({ codex: provider === "codex" });
      try {
        const initial = runtime.workflow.createDiscussionStep(
          "native",
          "discussion",
        );
        let task = {
          ...taskFixture(),
          id: "native",
          project: h.root,
          provider,
          workflowMode: "flexible",
          steps: [initial],
          activeStepId: initial.id,
          selectedStepId: initial.id,
        };
        await h.api.testSaveState({ version: 2, tasks: [task], projects: [] });
        const routing = await h.api.startRun({
          ...h.input,
          provider,
          mode: "plan",
          stepId: initial.id,
        });
        const proposal = {
          type: chosenType,
          title: "Discussion adaptée",
          objective: "Répondre à l’intention",
          reason: "Intent explicite",
        };
        const marker =
          "DJINN_EVENT:" +
          JSON.stringify({ type: "discussion_type", data: proposal });
        if (provider === "codex") {
          await until(() => h.turns.length === 1);
          const thread = h.requests.find((r) => r.method === "thread/start");
          assert.equal(thread.params.sandbox, "read-only");
          h.turns[0].complete("completed", marker);
        } else {
          await until(() => h.processes.length === 1);
          assert.ok(h.processes[0].args.includes("plan"));
          h.processes[0].stdout.emit(
            "data",
            JSON.stringify({
              type: "assistant",
              message: { content: [{ type: "text", text: marker }] },
            }) + "\n",
          );
          h.processes[0].close();
        }
        await until(() =>
          h.events.some(
            (e) => e.runId === routing.runId && e.data.status === "completed",
          ),
        );
        const classification = h.events.find(
          (e) => e.type === "discussion_type",
        );
        assert.ok(
          classification,
          `${provider}/${chosenType}: lead classification accepted`,
        );
        assert.equal(classification.stepId, initial.id);
        task = runtime.workflow.classifyDiscussion(task, {
          ...proposal,
          stepId: initial.id,
        });
        await h.api.testSaveState({ version: 2, tasks: [task], projects: [] });
        const mode = runtime.workflow.stepMode(chosenType);
        const selected = await h.api.startRun({
          ...h.input,
          provider,
          mode,
          stepId: initial.id,
        });
        assert.notEqual(selected.runId, routing.runId);
        if (provider === "codex") {
          await until(() => h.turns.length === 2);
          const threads = h.requests.filter((r) => r.method === "thread/start");
          assert.equal(
            threads.at(-1).params.sandbox,
            chosenType === "implementation" ? "workspace-write" : "read-only",
          );
          h.turns[1].complete();
        } else {
          await until(() => h.processes.length === 2);
          assert.ok(
            h.processes[1].args.includes(
              mode === "execute" ? "manual" : "plan",
            ),
          );
          h.processes[1].close();
        }
        await until(() =>
          h.events.some(
            (e) => e.runId === selected.runId && e.data.status === "completed",
          ),
        );
        assert.equal(h.events.filter((e) => e.type === "error").length, 0);
      } finally {
        await h.dispose();
      }
    }
  }
});

test("a fresh mission accepts one complete workflow_defined qualification from both harnesses", async () => {
  for (const provider of ["claude", "codex"]) {
    const h = harness({ codex: provider === "codex" });
    try {
      const initial = runtime.workflow.createDiscussionStep(
        "native-workflow",
        "discussion",
      );
      const task = {
        ...taskFixture(),
        id: "native-workflow",
        project: h.root,
        provider,
        workflowMode: "flexible",
        workflowOrigin: "agent",
        steps: [initial],
        activeStepId: initial.id,
        selectedStepId: initial.id,
      };
      await h.api.testSaveState({ version: 2, tasks: [task], projects: [] });
      const routing = await h.api.startRun({
        ...h.input,
        taskId: "native-workflow",
        provider,
        mode: "plan",
        stepId: initial.id,
      });
      const definition = {
        reason: "L'intention demande une spécification avant toute écriture.",
        steps: [
          {
            type: "specification",
            title: "Spécifier",
            objective: "Décrire le résultat attendu.",
          },
          {
            type: "review",
            title: "Vérifier",
            objective: "Contrôler les preuves.",
          },
        ],
      };
      const marker =
        "DJINN_EVENT:" +
        JSON.stringify({ type: "workflow_defined", data: definition });
      if (provider === "codex") {
        await until(() => h.turns.length === 1);
        const thread = h.requests.find((r) => r.method === "thread/start");
        assert.equal(thread.params.sandbox, "read-only");
        h.turns[0].complete("completed", marker);
      } else {
        await until(() => h.processes.length === 1);
        assert.ok(h.processes[0].args.includes("plan"));
        h.processes[0].stdout.emit(
          "data",
          JSON.stringify({
            type: "assistant",
            message: { content: [{ type: "text", text: marker }] },
          }) + "\n",
        );
        h.processes[0].close();
      }
      await until(() =>
        h.events.some(
          (e) => e.runId === routing.runId && e.data.status === "completed",
        ),
      );
      const defined = h.events.find((e) => e.type === "workflow_defined");
      assert.ok(defined, `${provider}: complete timeline accepted`);
      assert.equal(defined.stepId, initial.id);
      assert.equal(defined.data.steps.length, 2);
      assert.equal(
        h.events.some((e) => e.type === "discussion_type"),
        false,
      );
      assert.equal(
        h.events.some((e) => e.type === "artifact" || e.type === "action"),
        false,
      );
    } finally {
      await h.dispose();
    }
  }
});

test("project discovery IPC validates inputs and scopes history to the selected repository under both harnesses", async () => {
  for (const provider of ["claude", "codex"]) {
    const h = harness({ codex: provider === "codex" });
    try {
      const input = {
        directory: h.root,
        provider,
        scanId: "setup-fixture",
        includeHistory: true,
        model: "configured-model",
      };
      await assert.rejects(
        h.api.testDiscoverProject({ ...input, scanId: "invalid\n" }),
      );
      await assert.rejects(
        h.api.testDiscoverProject({ ...input, provider: "unknown" }),
      );
      await assert.rejects(h.api.testDiscoverProject({ ...input, model: {} }));
      await assert.rejects(
        h.api.testDiscoverProject({ ...input, includeHistory: "yes" }),
      );
      await h.api.testSaveState({
        version: 2,
        projects: [],
        tasks: [
          taskFixture({
            id: "history-local",
            steps: runtime.workflow.createDefaultSteps("history-local"),
            activeStepId:
              runtime.workflow.createDefaultSteps("history-local")[0].id,
            project: h.root,
            provider,
            brief: "Utiliser les conventions déjà approuvées",
          }),
          taskFixture({
            id: "history-other",
            steps: runtime.workflow.createDefaultSteps("history-other"),
            activeStepId:
              runtime.workflow.createDefaultSteps("history-other")[0].id,
            project: "/tmp/unrelated-fixture",
            brief: "Ne pas partager ce projet",
          }),
        ],
      });
      const pending = h.api.testDiscoverProject(input);
      const marker =
        "DJINN_EVENT:" +
        JSON.stringify({
          type: "artifact",
          data: {
            id: "project-setup",
            content: JSON.stringify({
              summary: "Suggestions vérifiées par l’agent",
            }),
          },
        });
      if (provider === "codex") {
        await until(() => h.turns.length === 1);
        h.allProcesses[0].kill = () => {
          h.allProcesses[0].exitCode = 0;
          h.allProcesses[0].close();
        };
        const request = h.requests.find((r) => r.method === "turn/start");
        assert.equal(request.params.sandboxPolicy.type, "readOnly");
        assert.match(
          request.params.input[0].text,
          /conventions déjà approuvées/,
        );
        assert.doesNotMatch(
          request.params.input[0].text,
          /Ne pas partager ce projet/,
        );
        h.turns[0].complete("completed", marker);
      } else {
        await until(() => h.processes.length === 1);
        assert.ok(h.processes[0].args.includes("Read,Glob,Grep"));
        h.processes[0].stdout.emit(
          "data",
          JSON.stringify({
            type: "assistant",
            message: { content: [{ type: "text", text: marker }] },
          }) + "\n",
        );
        h.processes[0].exitCode = 0;
        h.processes[0].close();
      }
      const report = await pending;
      assert.equal(report.analysis.status, "agent");
      assert.equal(report.analysis.provider, provider);
      assert.equal(report.historyCount, 1);
      assert.equal(report.directory, fs.realpathSync(h.root));
      assert.equal(report.summary, "Suggestions vérifiées par l’agent");
      assert.deepEqual(await h.api.testCancelProjectDiscovery(input.scanId), {
        cancelled: false,
      });
    } finally {
      await h.dispose();
    }
  }
});

test("plan and review readers start during the current lead turn and integrate their reports", async () => {
  for (const mode of ["plan", "review"]) {
    const h = harness({ codex: true });
    try {
      const run = await h.api.startRun({ ...h.input, mode, concurrency: 2, agents: [] });
      await until(() => h.turns.length === 1);
      const proposals = ["reader-a", "reader-b"].map(id => ({ id, name: id, role: "Inspection", prompt: "Inspect only", readOnly: true, writeScope: ["src"] }));
      h.turns[0].message(proposals.map(data => "DJINN_EVENT:" + JSON.stringify({ type: "agent", data })).join("\n"));
      await until(() => h.turns.length === 3);
      assert.equal(h.turns[0].done, undefined, "readers start before the lead completes");
      assert.equal(h.turns[1].params.sandboxPolicy.type, "readOnly");
      assert.equal(h.turns[2].params.sandboxPolicy.type, "readOnly");
      h.turns[0].complete();
      h.turns[1].complete("completed", "Reader A proof");
      h.turns[2].complete("completed", "Reader B proof");
      await until(() => h.turns.length === 4);
      assert.match(h.turns[3].params.input[0].text, /Reader A proof/);
      assert.match(h.turns[3].params.input[0].text, /Reader B proof/);
      h.turns[3].complete();
      await until(() => h.events.some(e => e.runId === run.runId && e.type === "status" && e.data.status === "completed"));
    } finally { await h.dispose(); }
  }
});

test("current-pass readers obey resource exclusion and cancellation waits for actual closure", async () => {
  const h = harness({ codex: true });
  try {
    const run = await h.api.startRun({ ...h.input, mode: "plan", concurrency: 2, agents: [] });
    await until(() => h.turns.length === 1);
    const definitions = ["reader-a", "reader-b"].map(id => ({ id, name: id, role: "Inspection", prompt: "Inspect only", readOnly: true, resources: { exclusive: ["gpu"] } }));
    h.turns[0].message(definitions.map(data => "DJINN_EVENT:" + JSON.stringify({ type: "agent", data })).join("\n"));
    await until(() => h.turns.length === 2);
    assert.equal(h.turns.length, 2, "the second exclusive reader waits");
    const cancel = h.api.cancelRun(run.runId);
    await cancel;
    await until(() => h.events.some(e => e.runId === run.runId && e.type === "status" && e.data.status === "cancelled"));
    assert.ok(h.turns.every(t => t.done), "readers close before root cancellation is complete");
    assert.equal(h.turns.length, 2, "queued reader was not started after cancellation");
  } finally { await h.dispose(); }
});

test("provider collaboration emits observed cards/messages without duplicate launches across passages", async () => {
  const h = harness({ codex: true });
  const notify = (method, params) => h.allProcesses[0].stdout.emit("data", JSON.stringify({ method, params }) + "\n");
  try {
    const first = await h.api.startRun({ ...h.input, agents: [] });
    await until(() => h.turns.length === 1);
    const leadThread = h.turns[0].threadId;
    const item = { type: "collabAgentToolCall", id: "collab-1", tool: "spawnAgent", status: "completed", senderThreadId: leadThread, receiverThreadIds: ["observed-child"], model: "child-model", agentsStates: { "observed-child": { status: "running", message: null } } };
    notify("item/completed", { threadId: leadThread, item });
    notify("item/completed", { threadId: leadThread, item });
    const cards = h.events.filter(e => e.type === "agent" && e.data.origin === "codex");
    assert.equal(cards.length, 1);
    assert.equal(cards[0].data.id, "codex:observed-child");
    assert.equal(cards[0].data.parentAgentId, "lead");
    assert.equal(cards[0].data.model, "child-model");
    assert.equal(h.turns.length, 1, "observed child was not launched by Djinn");
    notify("item/completed", { threadId: "observed-child", item: { type: "agentMessage", id: "child-message", text: "Observed proof" } });
    assert.ok(h.events.some(e => e.type === "text" && e.data.agentId === "codex:observed-child" && e.data.text === "Observed proof"));
    assert.equal(h.api.testRuntimeSnapshot().runs[0].observedAgents.length, 1);
    notify("turn/completed", { threadId: "observed-child", turn: { id: "child-turn", status: "completed" } });
    h.turns[0].complete();
    await until(() => h.events.some(e => e.runId === first.runId && e.type === "status" && e.data.status === "completed"));
    assert.ok(h.events.some(e => e.type === "agent" && e.data.id === "codex:observed-child" && e.data.live === false));
    const second = await h.api.startRun({ ...h.input, agents: [] });
    await until(() => h.turns.length === 2);
    notify("item/started", { threadId: h.turns[1].threadId, item: { type: "subAgentActivity", id: "activity-2", agentThreadId: "second-child", agentPath: "/root/inspect", kind: "started" } });
    assert.ok(h.events.some(e => e.runId === second.runId && e.type === "agent" && e.data.id === "codex:second-child"));
    assert.equal(h.api.testRuntimeSnapshot().runs[0].observedAgents.length, 1, "snapshot excludes the old passage");
    h.turns[1].complete();
  } finally { await h.dispose(); }
});

test("collaboration directed to a planned worker enriches its existing card", async () => {
  const h = harness({ codex: true });
  try {
    await h.api.startRun({ ...h.input, agents: [{ ...worker, readOnly: true }] });
    await until(() => h.turns.length === 1);
    const actualWorker = h.turns[0];
    assert.ok(actualWorker);
    h.allProcesses[0].stdout.emit("data", JSON.stringify({ method: "item/completed", params: { threadId: actualWorker.threadId, item: { type: "collabAgentToolCall", id: "managed-target", tool: "sendInput", status: "completed", senderThreadId: actualWorker.threadId, receiverThreadIds: [actualWorker.threadId], agentsStates: { [actualWorker.threadId]: { status: "running", message: null } } } } }) + "\n");
    const updates = h.events.filter(e => e.type === "agent" && e.data.providerThreadId === actualWorker.threadId);
    assert.ok(updates.length > 0);
    assert.ok(updates.every(e => e.data.id === "worker" && e.data.origin !== "codex"));
    assert.equal(h.api.testRuntimeSnapshot().runs[0].observedAgents.length, 0);
    assert.equal(h.turns.length, 1);
    actualWorker.complete();
    await until(() => h.turns.length === 2);
    h.turns[1].complete();
  } finally { await h.dispose(); }
});

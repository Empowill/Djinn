"use strict";
const test = require("node:test"), assert = require("node:assert/strict");
const { CodexAgentObserver } = require("../electron/codex-agent-observer.cjs");
// Synthetic examples conforming to the locally generated CLI schema; these
// are not presented as captured live provider traffic.
function harness() {
  const events = [];
  const observer = new CodexAgentObserver({ acceptsParent: id => id === "lead-thread" || Boolean(observer.get(id)), onAgent: a => events.push(a) });
  return { events, observer };
}
const spawn = { type: "collabAgentToolCall", id: "spawn-call", tool: "spawnAgent", status: "completed", senderThreadId: "lead-thread", receiverThreadIds: ["child-1", "child-2"], model: "worker-model", prompt: "Inspect", reasoningEffort: "max", agentsStates: { "child-1": { status: "running", message: null }, "child-2": { status: "pendingInit", message: null } } };
test("one stable card per child, no card for sender, and tool completion is not child completion", () => {
  const h = harness();
  h.observer.observe("turn/started", { threadId: "lead-thread", turn: { id: "lead-turn" } });
  h.observer.observe("item/completed", { threadId: "lead-thread", item: spawn });
  assert.equal(h.events.length, 2);
  assert.equal(h.observer.get("lead-thread"), undefined);
  assert.equal(h.observer.get("child-1").status, "running");
  assert.equal(h.observer.get("child-2").status, "queued");
  h.observer.observe("item/completed", { threadId: "lead-thread", item: spawn });
  assert.equal(h.events.length, 2, "repeated records are deduplicated");
  h.observer.observe("item/completed", { threadId: "lead-thread", item: { ...spawn, tool: "wait", model: "parent-model", agentsStates: { "child-1": { status: "completed", message: "Result" }, "child-2": { status: "errored", message: "Failure" } } } });
  assert.equal(h.observer.get("child-1").status, "done");
  assert.equal(h.observer.get("child-1").summary, "Result");
  assert.equal(h.observer.get("child-1").model, "worker-model");
  assert.equal(h.observer.get("child-2").status, "error");
  assert.equal(h.observer.records().length, 2);
});
test("subAgentActivity and child thread/turn records merge without inventing models", () => {
  const h = harness();
  h.observer.observe("item/started", { threadId: "lead-thread", item: { type: "subAgentActivity", id: "activity", agentThreadId: "child-1", agentPath: "/root/inspect", kind: "started" } });
  assert.equal(h.observer.get("child-1").name, "inspect");
  assert.equal(h.observer.get("child-1").model, undefined);
  h.observer.observe("thread/started", { thread: { id: "child-1", parentThreadId: "lead-thread", agentNickname: "Ada", agentRole: "Inspection", model: "local-model", status: { type: "idle" } } });
  assert.equal(h.observer.get("child-1").id, "codex:child-1");
  assert.equal(h.observer.get("child-1").name, "Ada");
  assert.equal(h.observer.get("child-1").status, "queued");
  h.observer.observe("turn/started", { threadId: "child-1", turn: { id: "child-turn" } });
  assert.equal(h.observer.get("child-1").status, "running");
  assert.equal(h.observer.get("child-1").turnId, "child-turn");
  h.observer.observe("turn/completed", { threadId: "child-1", turn: { id: "child-turn", status: "completed" } });
  assert.equal(h.observer.get("child-1").status, "done");
  assert.equal(h.observer.records().length, 1);
  h.observer.observe("item/completed", { threadId: "outside", item: { ...spawn, senderThreadId: "outside", receiverThreadIds: ["foreign"], agentsStates: {} } });
  assert.equal(h.observer.get("foreign"), undefined);
});

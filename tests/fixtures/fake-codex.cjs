#!/usr/bin/env node
"use strict";

const args = process.argv.slice(2);

if (args.includes("--version")) {
  process.stdout.write("codex-fixture 0.1.0\n");
  process.exit(0);
}

if (args[0] === "login" && args[1] === "status") {
  process.stdout.write('{"loggedIn":true,"fixture":true}\n');
  process.exit(0);
}

if (args[0] === "login") {
  process.stdout.write('{"loggedIn":true,"fixture":true}\n');
  process.exit(0);
}

if (args[0] === "app-server") {
  const readline = require("node:readline");
  const reader = readline.createInterface({ input: process.stdin });
  const send = (value) => process.stdout.write(JSON.stringify(value) + "\n");
  let threadCount = 0,
    turnCount = 0;
  const turns = new Map();
  reader.on("line", (line) => {
    const request = JSON.parse(line),
      params = request.params || {};
    if (request.id === undefined) return;
    if (process.env.DJINN_FIXTURE_REQUESTS)
      require("node:fs").appendFileSync(
        process.env.DJINN_FIXTURE_REQUESTS,
        JSON.stringify({ method: request.method, model: params.model }) + "\n",
      );
    let result = {};
    if (request.method === "model/list")
      result = {
        data: [
          {
            id: "fixture-model",
            model: "fixture-model",
            displayName: "Fixture Model",
            description: "Modèle de test local",
            isDefault: true,
          },
          {
            id: "fixture-fast",
            model: "fixture-fast",
            displayName: "Fixture Fast",
            description: "Choix secondaire",
          },
        ],
        nextCursor: null,
      };
    if (request.method === "thread/start")
      result = { thread: { id: `fixture-thread-${++threadCount}` } };
    if (request.method === "thread/resume")
      result = { thread: { id: params.threadId } };
    if (request.method === "turn/start") {
      const turn = { id: `fixture-turn-${++turnCount}`, status: "inProgress" };
      turns.set(params.threadId, turn);
      result = { turn };
      send({ id: request.id, result });
      send({
        method: "turn/started",
        params: { threadId: params.threadId, turn },
      });
      send({
        method: "item/started",
        params: {
          threadId: params.threadId,
          item: {
            id: "fixture-command",
            type: "commandExecution",
            command: "fixture-check",
          },
        },
      });
      const preview = params.input.some(
        (i) => i.type === "text" && i.text.includes("DJINN_SMOKE_AUTOPREVIEW"),
      );
      const content = preview
        ? 'Implementation complete.\nDJINN_EVENT:{"type":"action","data":{"id":"preview-check","kind":"manual","title":"Vérifier le résultat"}}'
        : 'The fixture provider is ready.\nDJINN_EVENT:{"type":"question","data":{"id":"FX01","title":"Fixture decision","context":"Fake provider.","options":[],"blocking":false}}\nDJINN_EVENT:{"type":"artifact","data":{"id":"fixture-artifact","title":"Fixture report","type":"document","content":"# Fixture report"}}';
      send({
        method: "item/agentMessage/delta",
        params: {
          threadId: params.threadId,
          itemId: turn.id + "-message",
          delta: "Fixture running",
        },
      });
      send({
        method: "item/completed",
        params: {
          threadId: params.threadId,
          item: {
            id: turn.id + "-message",
            type: "agentMessage",
            text: content,
          },
        },
      });
      if (preview) {
        turns.delete(params.threadId);
        send({
          method: "turn/completed",
          params: {
            threadId: params.threadId,
            turn: { ...turn, status: "completed" },
          },
        });
      }
      return;
    }
    if (request.method === "turn/interrupt") {
      const turn = turns.get(params.threadId);
      turns.delete(params.threadId);
      send({ id: request.id, result });
      if (turn)
        send({
          method: "turn/completed",
          params: {
            threadId: params.threadId,
            turn: { ...turn, status: "interrupted" },
          },
        });
      return;
    }
    if (request.method === "turn/steer")
      result = { turnId: params.expectedTurnId };
    send({ id: request.id, result });
  });
  return;
}

if (args[0] !== "exec") {
  process.stderr.write(`unsupported fixture invocation: ${args.join(" ")}\n`);
  process.exit(2);
}

const emit = (value) => process.stdout.write(`${JSON.stringify(value)}\n`);

if (args.some((arg) => arg.includes("DJINN_SMOKE_AUTOPREVIEW"))) {
  emit({ type: "thread.started", thread_id: "fixture-preview" });
  process.stdout.write(
    'DJINN_EVENT:{"type":"action","data":{"id":"preview-check","kind":"manual","title":"Vérifier le résultat"}}\n',
  );
  emit({
    type: "item.completed",
    item: { type: "agent_message", text: "Implementation complete." },
  });
  emit({
    type: "turn.completed",
    usage: { input_tokens: 0, output_tokens: 0 },
  });
  process.exit(0);
}

emit({ type: "thread.started", thread_id: "fixture-thread-001" });
emit({
  type: "item.started",
  item: {
    type: "command_execution",
    id: "fixture-command-001",
    command: "printf fixture-check",
  },
});
process.stdout.write(
  'DJINN_EVENT:{"type":"question","data":{"id":"FX01","title":"Fixture decision","context":"A question from the fake provider.","recommendation":"Keep the fixture deterministic.","options":[{"id":"a","label":"Keep it","description":"Use the fixture."},{"id":"b","label":"Change it","description":"Use another fixture."}],"blocking":false,"unlocks":"Fixture continues.","agentId":"lead","theme":"Smoke"}}\n',
);
process.stdout.write(
  'DJINN_EVENT:{"type":"artifact","data":{"id":"fixture-artifact","title":"Fixture report","type":"document","content":"# Fixture report\\n\\nThe fake provider emitted this artifact."}}\n',
);
emit({
  type: "item.completed",
  item: {
    type: "command_execution",
    id: "fixture-command-001",
    command: "printf fixture-check",
    aggregated_output: "fixture-check",
  },
});
emit({
  type: "item.completed",
  item: {
    type: "agent_message",
    text: "The fixture provider is ready for the smoke test.",
  },
});

// Keep a run alive until the harness calls cancelRun so cancellation exercises
// the process-group path instead of racing a naturally completed child.
const keepAlive = setInterval(() => undefined, 1_000);
const stop = () => {
  clearInterval(keepAlive);
  process.exit(143);
};
process.once("SIGTERM", stop);
process.once("SIGINT", stop);

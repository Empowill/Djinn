#!/usr/bin/env node
"use strict";
const args = process.argv.slice(2);
if (args.includes("--version")) {
  console.log("codex-permission-fixture 1");
  process.exit(0);
}
if (args[0] === "login") {
  console.log('{"loggedIn":true}');
  process.exit(0);
}
if (args[0] !== "app-server") process.exit(2);
const fs = require("node:fs");
const send = (value) => process.stdout.write(JSON.stringify(value) + "\n");
let thread = 0,
  turn = 0;
const approvals = new Map();
const nativeCalls = new Map();
const structured = process.env.DJINN_SMOKE_STRUCTURED === "1";
function finish(pending) {
  send({ method: "item/completed", params: { threadId: pending.threadId, item: {
    id: "message-" + pending.turnId, type: "agentMessage",
    text: 'DJINN_EVENT:{"type":"step_result","data":{"status":"ready","summary":"Autorisation traitée dans le même passage.","completed":["Diagnostic local vérifié"],"remaining":["Recette humaine du ticket B"],"evidence":["Fixture isolée, sans fournisseur payant"]}}',
  } } });
  send({ method: "turn/completed", params: { threadId: pending.threadId, turn: { id: pending.turnId, status: "completed" } } });
}
function publishNext(pending, index = 0) {
  const calls = [
    ["publish_question", { id: "structured-question", title: "Qui peut modifier la date ?", context: "La date devient définitive après validation. Faut-il autoriser les RH et l’évaluateur principal à la modifier avant validation ?", options: [], blocking: false }],
    ["publish_artifact", { id: "structured-report", type: "markdown", title: "Compte rendu enregistré", content: "# Compte rendu\n\nLa recette B est indépendante. La correction A continue.\n\n" + "Preuve conservée\n".repeat(1600) + "Fin du rapport complet" }],
    ["update_task", { id: "ticket-b", title: "Ticket B · Recette indépendante", status: "ready", ticket: "B" }],
  ];
  if (index >= calls.length) return finish(pending);
  const id = 12000 + index;
  nativeCalls.set(id, { pending, index });
  send({ id, method: "item/tool/call", params: { threadId: pending.threadId, turnId: pending.turnId, callId: "structured-call-" + index, tool: calls[index][0], arguments: calls[index][1] } });
}
require("node:readline")
  .createInterface({ input: process.stdin })
  .on("line", (line) => {
    const request = JSON.parse(line),
      params = request.params || {};
    if (request.id === undefined) return;
    fs.appendFileSync(
      process.env.DJINN_PERMISSION_TRACE,
      JSON.stringify(request) + "\n",
    );
    if (!request.method) {
      const native = nativeCalls.get(request.id);
      if (native) {
        nativeCalls.delete(request.id);
        if (!request.result?.success) throw new Error("Structured tool failed: " + JSON.stringify(request));
        publishNext(native.pending, native.index + 1);
        return;
      }
      const pending = approvals.get(request.id);
      if (pending) {
        approvals.delete(request.id);
        if (structured && pending.turnId === "permission-turn-1") publishNext(pending);
        else finish(pending);
      }
      return;
    }
    let result = {};
    if (request.method === "model/list")
      result = {
        data: [
          {
            id: "fixture-model",
            model: "fixture-model",
            displayName: "Fixture",
            isDefault: true,
          },
        ],
        nextCursor: null,
      };
    if (request.method === "thread/start")
      result = { thread: { id: "permission-thread-" + ++thread } };
    if (request.method === "thread/resume")
      result = { thread: { id: params.threadId } };
    if (request.method === "turn/start") {
      const turnId = "permission-turn-" + ++turn;
      send({
        id: request.id,
        result: { turn: { id: turnId, status: "inProgress" } },
      });
      send({
        method: "turn/started",
        params: {
          threadId: params.threadId,
          turn: { id: turnId, status: "inProgress" },
        },
      });
      if (structured && JSON.stringify(params).includes("RH et évaluateur principal avant validation.") && !JSON.stringify(params).includes("Cancellation smoke")) {
        finish({ threadId: params.threadId, turnId });
        return;
      }
      const id = 9000 + turn;
      approvals.set(id, { threadId: params.threadId, turnId });
      send({
        id,
        method: "item/commandExecution/requestApproval",
        params: {
          threadId: params.threadId,
          turnId,
          itemId: "command-" + turn,
          command: "printf djinn-isolated-test",
          cwd: params.cwd,
          reason: "Test isolé de la demande native",
          availableDecisions: ["accept", "acceptForSession", "decline"],
        },
      });
      return;
    }
    send({ id: request.id, result });
  });

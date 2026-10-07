"use strict";
// Contract verified against the installed Codex CLI's generate-ts output.
// Observing a provider child never queues or launches a Djinn worker.
const text = (value, max = 8000) => typeof value === "string" ? value.slice(0, max) : "";
const state = (value, fallback) => ({ pendingInit: "queued", running: "running", completed: "done", errored: "error", interrupted: "blocked", shutdown: "blocked", notFound: "blocked" }[value] || fallback);
const activityLabels = { started: "Démarre", interacted: "Échange avec le parent", interrupted: "Interrompu", completed: "Terminé", spawnAgent: "Créé par Codex", sendInput: "Indication reçue", wait: "État observé", closeAgent: "Fermeture demandée", sendMessage: "Message transmis", followupTask: "Nouvelle tâche transmise", interruptAgent: "Interruption demandée", listAgents: "État observé", resumeAgent: "Reprise demandée" };
const lifecycle = status => status === "done" ? "agent_completed" : ["blocked", "error"].includes(status) ? "agent_blocked" : "agent_started";
const stableProviderAgentId = (provider, threadId) => text(threadId, 240) ? `${provider}:${text(threadId, 240)}` : null;

class CodexAgentObserver {
  constructor({ provider = "codex", onAgent, acceptsParent = () => true } = {}) {
    this.provider = provider;
    this.onAgent = onAgent || (() => {});
    this.acceptsParent = acceptsParent;
    this.agents = new Map();
  }
  get(threadId) { return this.agents.get(threadId); }
  records() { return [...this.agents.values()].map(({ signature, ...record }) => record); }
  update(threadId, patch) {
    if (!text(threadId, 240)) return null;
    const old = this.agents.get(threadId);
    const parentId = patch.parentProviderThreadId || old?.parentProviderThreadId;
    if (!old && (!parentId || !this.acceptsParent(parentId))) return null;
    const record = {
      id: stableProviderAgentId(this.provider, threadId), provider: this.provider,
      providerThreadId: threadId, origin: "codex", status: "queued",
      ...old, ...Object.fromEntries(Object.entries(patch).filter(([, value]) => value !== undefined)),
    };
    record.lifecycle = lifecycle(record.status);
    delete record.signature;
    const signature = JSON.stringify(record);
    if (old?.signature === signature) return record;
    this.agents.set(threadId, { ...record, signature });
    this.onAgent(record);
    return record;
  }
  observe(method, params = {}) {
    const item = params.item;
    const results = [];
    const add = (threadId, patch) => { const record = this.update(threadId, patch); if (record) results.push(record); };
    if (["item/started", "item/completed"].includes(method) && item?.type === "collabAgentToolCall") {
      // The tool status is not the child status. Spawn completion proves only
      // creation; agentsStates carries the observed state of each recipient.
      const recipients = new Set([...(item.receiverThreadIds || []), ...Object.keys(item.agentsStates || {})]);
      for (const threadId of recipients) {
        const observed = item.agentsStates?.[threadId];
        const old = this.get(threadId);
        add(threadId, {
          parentProviderThreadId: old?.parentProviderThreadId || text(item.senderThreadId, 256),
          status: state(observed?.status, old?.status || "queued"),
          activity: activityLabels[item.tool] || "Activité de collaboration",
          summary: observed?.message ? text(observed.message) : old?.summary,
          // A requested spawn model is provider evidence; other tool calls
          // must not replace a child's model with the sender's model.
          model: item.tool === "spawnAgent" && text(item.model) ? text(item.model) : old?.model,
        });
      }
    } else if (["item/started", "item/completed"].includes(method) && item?.type === "subAgentActivity") {
      const threadId = item.agentThreadId;
      const old = this.get(threadId);
      add(threadId, {
        parentProviderThreadId: old?.parentProviderThreadId || text(params.threadId, 256),
        name: text(item.agentPath, 256).split("/").filter(Boolean).at(-1) || old?.name,
        status: state(item.kind, item.kind === "started" ? "running" : old?.status || "running"),
        activity: activityLabels[item.kind] || "Activité observée",
      });
    } else if (method === "thread/started" && params.thread?.parentThreadId) {
      const thread = params.thread;
      add(thread.id, {
        parentProviderThreadId: thread.parentThreadId,
        name: text(thread.agentNickname || thread.name, 256) || undefined,
        role: text(thread.agentRole, 256) || undefined,
        model: text(thread.model, 256) || undefined,
        // A thread loaded in idle state is not proof of task completion.
        status: thread.status?.type === "active" ? "running" : "queued",
        activity: "Thread enfant observé",
      });
    } else if (this.get(params.threadId)) {
      if (method === "turn/started") add(params.threadId, {
        status: "running",
        activity: "Travaille",
        turnId: text(params.turn?.id || params.turnId, 256) || undefined,
      });
      if (method === "turn/completed") add(params.threadId, {
        status: params.turn?.status === "completed" ? "done" : params.turn?.status === "failed" ? "error" : "blocked",
        activity: params.turn?.status === "completed" ? "Tour terminé" : "Tour interrompu ou en échec",
        turnId: text(params.turn?.id || params.turnId, 256) || undefined,
      });
    }
    return results;
  }
}
module.exports = { CodexAgentObserver, stableProviderAgentId };

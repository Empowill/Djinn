"use strict";
const { EventEmitter } = require("node:events");

// Native records may contain a full tool result, diff or completed turn. This
// bounds one UTF-8 JSON-RPC record, not a pipe chunk or the entire conversation.
const MAX_RECORD_BYTES = 32 * 1024 * 1024;

// Persistent, local JSON-RPC transport. Credentials/provider configuration are
// inherited by the configured Codex binary; no model or auth substitution.
class CodexAppServer extends EventEmitter {
  constructor(
    child,
    {
      requestTimeout = 30000,
      maxRecordBytes = MAX_RECORD_BYTES,
      terminate = (signal) => child.kill(signal),
    } = {},
  ) {
    super();
    this.child = child;
    this.terminate = terminate;
    this.pending = new Map();
    this.turns = new Map();
    this.threads = new Map();
    this.nextId = 1;
    this.requestTimeout = requestTimeout;
    this.closed = false;
    let parts = [],
      recordBytes = 0;
    child.stdout.setEncoding("utf8");
    child.stdout.on("data", (chunk) => {
      if (this.closed || this.aborting) return;
      let start = 0;
      while (start < chunk.length) {
        const index = chunk.indexOf("\n", start);
        const part = chunk.slice(start, index < 0 ? chunk.length : index);
        recordBytes += Buffer.byteLength(part, "utf8");
        if (recordBytes > maxRecordBytes) {
          parts = [];
          recordBytes = 0;
          void this.abortTransport(
            new Error("Codex app-server record exceeds the output limit"),
          );
          return;
        }
        if (part) parts.push(part);
        if (index < 0) break;
        const line = parts.join("");
        parts = [];
        recordBytes = 0;
        if (line.trim()) {
          try {
            this.receive(JSON.parse(line));
          } catch {
            this.emit("diagnostic", line.slice(0, 4000));
          }
        }
        if (this.closed || this.aborting) return;
        start = index + 1;
      }
    });
    child.stderr.setEncoding("utf8");
    child.stderr.on("data", (chunk) =>
      this.emit("diagnostic", String(chunk).slice(0, 8000)),
    );
    child.stdin.on("error", (error) => void this.abortTransport(error));
    child.on("error", (error) =>
      child.pid ? void this.abortTransport(error) : this.fail(error),
    );
    child.on("close", (code) =>
      this.fail(
        this.abortError ||
          new Error(`Codex app-server closed (${code ?? "unknown"})`),
      ),
    );
    this.ready = this.request("initialize", {
      clientInfo: { name: "djinn", title: "Djinn", version: "0.2.1" },
    }).then(() => this.send({ method: "initialized" }));
    this.ready.catch(() => {});
  }
  send(message) {
    if (this.closed) throw new Error("Codex app-server is unavailable");
    this.child.stdin.write(JSON.stringify(message) + "\n");
  }
  request(method, params) {
    const id = this.nextId++;
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id);
        reject(new Error(`Codex ${method} acknowledgement timed out`));
      }, this.requestTimeout);
      this.pending.set(id, { resolve, reject, timer });
      try {
        this.send({ id, method, params });
      } catch (error) {
        clearTimeout(timer);
        this.pending.delete(id);
        reject(error);
      }
    });
  }
  receive(message) {
    if (message.id !== undefined && !message.method) {
      const pending = this.pending.get(message.id);
      if (!pending) return;
      this.pending.delete(message.id);
      clearTimeout(pending.timer);
      if (message.error)
        pending.reject(
          new Error(message.error.message || "Codex request failed"),
        );
      else pending.resolve(message.result);
      return;
    }
    if (message.id !== undefined && message.method) {
      // Djinn never silently grants expanded permissions or runs client tools.
      const approval = message.method.endsWith("/requestApproval");
      this.send(
        approval
          ? { id: message.id, result: { decision: "decline" } }
          : {
              id: message.id,
              error: {
                code: -32601,
                message: "This client does not support this request",
              },
            },
      );
      this.emit("diagnostic", `Native request prevented: ${message.method}`);
      return;
    }
    const params = message.params || {};
    const entry = this.turns.get(params.threadId);
    if (entry) {
      if (message.method === "turn/completed") {
        // A restarted turn may share a thread with a late notification from
        // the interrupted turn. Do not let that stale completion resolve the
        // replacement passage early.
        if (entry.turnId && params.turn?.id && entry.turnId !== params.turn.id)
          return;
      }
      entry.onNotification(message.method, params);
      if (message.method === "turn/started") entry.turnId = params.turn.id;
      if (message.method === "turn/completed") {
        this.turns.delete(params.threadId);
        entry.resolve(params.turn);
      }
    }
    this.emit("notification", message.method, params);
  }
  async run(
    key,
    input,
    onNotification,
    { savedThreadId, imagePaths = [] } = {},
  ) {
    await this.ready;
    let threadId = this.threads.get(key);
    const config = {
      cwd: input.cwd,
      approvalPolicy: "never",
      sandbox: input.mode === "execute" ? "workspace-write" : "read-only",
      ...(input.model ? { model: input.model } : {}),
    };
    if (!threadId) {
      const response = savedThreadId
        ? await this.request("thread/resume", {
            ...config,
            threadId: savedThreadId,
            // Codex retains the history natively. Downloading every turn can
            // overflow the bounded transport when a long mission is resumed.
            excludeTurns: true,
          })
        : await this.request("thread/start", config);
      threadId = response.thread.id;
      this.threads.set(key, threadId);
    }
    if (this.turns.has(threadId))
      throw new Error("Codex thread already has an active turn");
    let resolve;
    const completed = new Promise((done) => {
      resolve = done;
    });
    const entry = { threadId, turnId: null, onNotification, resolve };
    this.turns.set(threadId, entry);
    const sandboxPolicy =
      input.mode === "execute"
        ? {
            type: "workspaceWrite",
            writableRoots: input.writableRoots?.length
              ? input.writableRoots
              : [input.cwd],
            networkAccess: false,
            excludeTmpdirEnvVar: false,
            excludeSlashTmp: false,
          }
        : { type: "readOnly", networkAccess: false };
    try {
      const response = await this.request("turn/start", {
        threadId,
        input: [
          { type: "text", text: input.prompt, text_elements: [] },
          ...imagePaths.map((p) => ({ type: "localImage", path: p })),
        ],
        cwd: input.cwd,
        approvalPolicy: "never",
        sandboxPolicy,
        ...(input.model ? { model: input.model } : {}),
      });
      entry.turnId ||= response.turn.id;
      if (entry.interruptRequested && this.turns.get(threadId) === entry)
        await this.interrupt(key);
      return { threadId, turnId: entry.turnId, completed };
    } catch (error) {
      // A lost turn/start acknowledgement is ambiguous: the provider may be
      // writing already. Terminate the transport and wait for actual close
      // before reporting failure or allowing another writer.
      if (this.turns.get(threadId) === entry) await this.abortTransport(error);
      resolve({ status: "failed", error: { message: error.message } });
      throw error;
    }
  }
  async steer(key, text, id) {
    const threadId = this.threads.get(key),
      entry = this.turns.get(threadId);
    if (!entry?.turnId) throw new Error("Codex recipient has no active turn");
    return this.request("turn/steer", {
      threadId,
      expectedTurnId: entry.turnId,
      clientUserMessageId: id,
      input: [{ type: "text", text, text_elements: [] }],
    });
  }
  async interrupt(key) {
    const threadId = this.threads.get(key),
      entry = this.turns.get(threadId);
    if (!entry) return;
    entry.interruptRequested = true;
    if (entry.turnId)
      await this.request("turn/interrupt", { threadId, turnId: entry.turnId });
  }
  abortTransport(error) {
    if (this.closed) return Promise.resolve();
    if (this.aborting) return this.aborting;
    // The close listener runs before the abort waiter. Retain the reason even
    // when Codex handles SIGTERM gracefully and exits with code zero.
    this.abortError = error;
    this.aborting = new Promise((resolve) => {
      const force = setTimeout(() => {
        try {
          this.terminate("SIGKILL");
        } catch {}
      }, 1500);
      force.unref?.();
      this.child.once("close", () => {
        clearTimeout(force);
        this.fail(error);
        resolve();
      });
      try {
        this.terminate("SIGTERM");
      } catch {
        if (!this.child.pid) {
          clearTimeout(force);
          this.fail(error);
          resolve();
        }
      }
    });
    return this.aborting;
  }
  fail(error) {
    if (this.closed) return;
    this.closed = true;
    for (const pending of this.pending.values()) {
      clearTimeout(pending.timer);
      pending.reject(error);
    }
    this.pending.clear();
    for (const entry of this.turns.values())
      entry.resolve({ status: "failed", error: { message: error.message } });
    this.turns.clear();
    this.emit("closed", error);
  }
  close() {
    if (!this.closed) {
      this.child.stdin.end();
      this.terminate("SIGTERM");
    }
  }
}
module.exports = { CodexAppServer };

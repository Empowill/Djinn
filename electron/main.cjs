"use strict";

const {
  app,
  BrowserWindow,
  dialog,
  ipcMain,
  Notification,
  shell,
  protocol,
} = require("electron");
const fs = require("node:fs");
const fsp = fs.promises;
const path = require("node:path");
const os = require("node:os");
const crypto = require("node:crypto");
const { spawn, spawnSync } = require("node:child_process");

const runtime = require("./runtime.cjs");
const {
  compactStateHistory,
  MAX_TOOL_DETAIL_BUDGET,
  MAX_TOOL_EVENT_DETAIL_LENGTH,
} = require("./state-history.cjs");
const actionRuntime = require("./actions.cjs");
const { writeMissionContext } = require("./mission-context.cjs");
const projectDiscovery = require("./project-discovery.cjs");
const { projectHistory } = require("./project-history.cjs");
const projectScans = new Map();
const { CodexAppServer } = require("./codex-app-server.cjs");
const { CodexAgentObserver } = require("./codex-agent-observer.cjs");
const permissionProtocol = require("./permission-protocol.cjs");
const codexServers = new Map();
const { discoverCodexModels, claudeModels } = require("./provider-models.cjs");
const modelCatalogCache = new Map();
const scheduler = require("./scheduler.cjs");
const { MissionJournal } = require("./mission-journal.cjs");
const { StructuredInteractionStore } = require("./structured-interaction-store.cjs");
const { inspectTestEnvironment } = require("./test-environment.cjs");
let missionJournal;
const getMissionJournal = () =>
  (missionJournal ||= MissionJournal.forUserData(app.getPath("userData")));
let structuredInteractionStore;
const getStructuredInteractionStore = () =>
  (structuredInteractionStore ||= new StructuredInteractionStore(
    path.join(app.getPath("userData"), "mission-structured"),
  ));
const pendingNotificationClicks = new Map();
const getMissionJournalPage = (taskId, cursor = 0, limit = 200) =>
  getMissionJournal().readPage(taskId, { cursor, limit });
const getMissionInteractions = (taskId) =>
  getStructuredInteractionStore().read(taskId);
const {
  VisualizationRegistry,
  policy: visualizationPolicy,
} = require("./visualization.cjs");
const visualizationRegistry = new VisualizationRegistry();
protocol?.registerSchemesAsPrivileged([
  {
    scheme: "djinn-visualization",
    privileges: { standard: true, secure: true, supportFetchAPI: true },
  },
]);

const MAX_OUTPUT_LINE = 1_000_000;
const MAX_CAPTURED_STATUS_OUTPUT = 64_000;
const MAX_RECONNECT_EVENTS = 250;
const MAX_RECONNECT_CHARACTERS = 2_000_000;
const MAX_PERSISTED_STATE_READ_BYTES = 32_000_000;
const HISTORY_COMPACTION_BACKUP_SUFFIX = ".before-history-compaction.backup";
const MAX_OBSERVED_TURN_LOOKUP = 1;
const PROCESS_KILL_GRACE_MS = 1_500;
const NOTIFICATION_SHOW_TIMEOUT_MS = 2_000;
// Some provider versions write informational startup diagnostics as protocol
// error records. They must remain visible as warnings, but cannot poison the
// run's providerError flag or turn a successful pass into an error.
const NON_FATAL_PROVIDER_DIAGNOSTICS = Object.freeze([
  /^Reading additional input from stdin\.{0,3}$/i,
  /\bWARN\s+codex_[\w:.-]+:/i,
]);
const MAX_WORKER_SUMMARY_FOR_PROMPT = 7_600;
const COMMON_PROVIDER_BIN_DIRS = Object.freeze([
  "/opt/homebrew/bin",
  "/usr/local/bin",
  path.join(os.homedir(), ".local", "bin"),
  path.join(os.homedir(), ".npm-global", "bin"),
  path.join(os.homedir(), ".bun", "bin"),
]);
const CODEX_APP_COMMANDS = Object.freeze([
  "/Applications/ChatGPT.app/Contents/Resources/codex-cli/CodexCLI.app/Contents/MacOS/codex",
  "/Applications/Codex.app/Contents/Resources/codex",
]);

const PROVIDER_SPECS = Object.freeze({
  codex: Object.freeze({
    id: "codex",
    name: "Codex",
    command: "codex",
    authArgs: ["login", "status"],
    loginArgs: ["login"],
  }),
  claude: Object.freeze({
    id: "claude",
    name: "Claude",
    command: "claude",
    authArgs: ["auth", "status"],
    loginArgs: ["auth", "login"],
  }),
});

let mainWindow = null;
let ipcRegistered = false;
let stateLoadPromise = null;
const activeRuns = new Map();
const activeTaskRuns = new Map();
const activeCwdRuns = new Map();
// Native provider requests are held here until the renderer makes an
// explicit, request-scoped decision. The native request id and payload stay
// in this process; the renderer sees only the bounded public card.
const pendingPermissions = new Map();
// A reconnect can happen after the bounded live journal has evicted an older
// event. Keep the latest durable native interaction by logical identity so a
// renderer snapshot can recover questions, work items, reports and step
// results without replaying the full mission lifecycle.
const structuredInteractionProjections = new Map();
const STRUCTURED_PROJECTION_TYPES = Object.freeze([
  "question",
  "artifact",
  "action",
  "work_item",
  "report",
  "step_result",
]);
const MAX_STRUCTURED_PROJECTION_EVENTS = 256;
const MAX_STRUCTURED_PROJECTION_BYTES = 12_000_000;
const MAX_STRUCTURED_PROJECTION_STRING = 16_000;
const NATIVE_INTERACTION = Symbol("djinn.nativeInteraction");
// `thread/resume` cannot install dynamicTools. Version the durable session
// key so a mission saved by an older build is resumed once in a fresh thread
// that has the native tool contract, while the old providerSessions entry is
// retained for history and diagnostics.
const CODEX_NATIVE_SESSION_VERSION = "native-tools-v1";

function compactStructuredValue(value, depth = 0) {
  if (typeof value === "string")
    return value.length > MAX_STRUCTURED_PROJECTION_STRING
      ? `${value.slice(0, MAX_STRUCTURED_PROJECTION_STRING)}…`
      : value;
  if (Array.isArray(value))
    return value
      .slice(0, 100)
      .map((entry) => compactStructuredValue(entry, depth + 1));
  if (!value || typeof value !== "object" || depth > 6) return value;
  return Object.fromEntries(
    Object.entries(value).map(([key, entry]) => [
      key,
      compactStructuredValue(entry, depth + 1),
    ]),
  );
}
// Keep native notifications strongly reachable until Electron reports that
// they have closed. Without this set, short-lived notification objects can be
// collected before the platform displays them or delivers a click event.
const activeNotifications = new Set();

function providerPathEntries() {
  const inherited =
    typeof process.env.PATH === "string"
      ? process.env.PATH.split(path.delimiter)
      : [];
  return [
    ...new Set([...inherited, ...COMMON_PROVIDER_BIN_DIRS].filter(Boolean)),
  ];
}

function childEnvironment() {
  return {
    ...process.env,
    PATH: providerPathEntries().join(path.delimiter),
    NO_COLOR: "1",
  };
}

function executablePath(candidate) {
  try {
    fs.accessSync(candidate, fs.constants.X_OK);
    return candidate;
  } catch {
    return null;
  }
}

function providerCommandCandidates(spec) {
  const candidates = [];
  if (path.isAbsolute(spec.command)) {
    candidates.push(spec.command);
  } else {
    for (const directory of providerPathEntries()) {
      candidates.push(path.join(directory, spec.command));
      if (process.platform === "win32") {
        for (const extension of (process.env.PATHEXT || ".EXE;.CMD;.BAT").split(
          ";",
        )) {
          if (extension)
            candidates.push(
              path.join(directory, `${spec.command}${extension.toLowerCase()}`),
            );
        }
      }
    }
  }
  if (spec.id === "codex") candidates.push(...CODEX_APP_COMMANDS);
  candidates.push(spec.command);
  return [...new Set(candidates)];
}

function resolveProviderCommand(spec) {
  for (const candidate of providerCommandCandidates(spec)) {
    if (path.isAbsolute(candidate)) {
      const resolved = executablePath(candidate);
      if (resolved) return resolved;
    }
  }
  return spec.command;
}

function runGitProbe(args, cwd) {
  try {
    const result = spawnSync("git", args, {
      cwd,
      shell: false,
      windowsHide: true,
      stdio: ["ignore", "pipe", "ignore"],
      encoding: "utf8",
      timeout: 2_000,
    });
    if (result.error || result.status !== 0) return null;
    const value = String(result.stdout || "").trim();
    return value || null;
  } catch {
    return null;
  }
}

function detectGitContext(cwd) {
  const worktree = runGitProbe(["rev-parse", "--show-toplevel"], cwd);
  const branch = runGitProbe(["branch", "--show-current"], cwd);
  if (!worktree && !branch) return null;
  return { worktree, branch };
}

function makeError(code, message, details) {
  const error = new Error(message);
  error.code = code;
  if (details !== undefined) error.details = details;
  return error;
}

function serializeError(error) {
  return {
    code: typeof error?.code === "string" ? error.code : "internal_error",
    message: error instanceof Error ? error.message : String(error),
  };
}

function randomId(prefix = "") {
  return `${prefix}${crypto.randomUUID()}`;
}

function defaultState() {
  return { version: runtime.SESSION_VERSION, tasks: [] };
}

function configureUserDataPath() {
  let configured = process.env.DJINN_USER_DATA;
  try {
    const commandLinePath = app.commandLine?.getSwitchValue("user-data-dir");
    if (commandLinePath) configured = commandLinePath;
  } catch {
    // Test doubles may not expose commandLine before app readiness.
  }
  if (!configured) return;
  if (
    typeof configured !== "string" ||
    configured.length > runtime.MAX_PATH_LENGTH ||
    configured.includes("\u0000")
  ) {
    throw makeError(
      "invalid_path",
      "DJINN_USER_DATA must be a short absolute path",
    );
  }
  const target = path.resolve(configured);
  if (!path.isAbsolute(configured))
    throw makeError("invalid_path", "DJINN_USER_DATA must be absolute");
  fs.mkdirSync(target, { recursive: true, mode: 0o700 });
  app.setPath("userData", target);
}

function focusMainWindow() {
  if (!mainWindow) return;
  if (
    typeof mainWindow.isDestroyed === "function" &&
    mainWindow.isDestroyed()
  )
    return;
  if (
    typeof mainWindow.isMinimized === "function" &&
    mainWindow.isMinimized() &&
    typeof mainWindow.restore === "function"
  )
    mainWindow.restore();
  if (typeof mainWindow.show === "function") mainWindow.show();
  if (typeof mainWindow.focus === "function") mainWindow.focus();
}

function acquireSingleInstanceLock() {
  // Older test doubles (and older Electron shells used by local tooling) may
  // not expose this API. A real Electron build always does, so preserving a
  // successful fallback keeps the native bootstrap testable without weakening
  // the production lock.
  if (typeof app.requestSingleInstanceLock !== "function") return true;
  const acquired = app.requestSingleInstanceLock();
  if (!acquired) {
    if (typeof app.quit === "function") app.quit();
    return false;
  }
  app.on("second-instance", focusMainWindow);
  return true;
}

function statePath() {
  return path.join(app.getPath("userData"), "state.json");
}

async function atomicWriteText(filePath, content) {
  const directory = path.dirname(filePath);
  await fsp.mkdir(directory, { recursive: true, mode: 0o700 });
  const temporaryPath = path.join(
    directory,
    `.${path.basename(filePath)}.${process.pid}.${crypto.randomUUID()}.tmp`,
  );
  try {
    await fsp.writeFile(temporaryPath, content, {
      encoding: "utf8",
      mode: 0o600,
    });
    await fsp.rename(temporaryPath, filePath);
  } catch (error) {
    await fsp.rm(temporaryPath, { force: true }).catch(() => undefined);
    throw error;
  }
}

async function preserveHistoryCompactionBackup(filePath) {
  await fsp
    .copyFile(
      filePath,
      `${filePath}${HISTORY_COMPACTION_BACKUP_SUFFIX}`,
      fs.constants.COPYFILE_EXCL,
    )
    .catch((error) => {
      if (error.code !== "EEXIST") throw error;
    });
}

function compactAndValidateState(value, restored) {
  const compacted = compactStateHistory(value, {
    maxDetailLength: MAX_TOOL_EVENT_DETAIL_LENGTH,
    detailBudget: MAX_TOOL_DETAIL_BUDGET,
  });
  const state = runtime.validateState(compacted, restored);
  const serialized = JSON.stringify(state);
  const content = `${serialized}\n`;
  if (
    content.length > runtime.MAX_SESSION_LENGTH ||
    Buffer.byteLength(content, "utf8") > runtime.MAX_SESSION_LENGTH
  ) {
    throw makeError(
      "input_too_large",
      `state exceeds the ${runtime.MAX_SESSION_LENGTH} character limit after history compaction`,
    );
  }
  return { state, content };
}

async function readPersistentState() {
  const filePath = statePath();
  try {
    const stats = await fsp.stat(filePath);
    if (stats.size > MAX_PERSISTED_STATE_READ_BYTES) {
      throw makeError(
        "input_too_large",
        `Saved state exceeds the ${MAX_PERSISTED_STATE_READ_BYTES} byte read limit`,
      );
    }
    const text = await fsp.readFile(filePath, "utf8");
    const value = JSON.parse(text);
    const { state, content } = compactAndValidateState(value, true);
    if (content !== text) {
      // Validate the complete compacted state before replacing anything and
      // retain exact recoverable bytes before the first history rewrite.
      await preserveHistoryCompactionBackup(filePath);
      if (value.version === 1) {
        await fsp
          .copyFile(
            filePath,
            `${filePath}.v1.backup`,
            fs.constants.COPYFILE_EXCL,
          )
          .catch((error) => {
            if (error.code !== "EEXIST") throw error;
          });
      }
      await atomicWriteText(filePath, content);
    }
    return state;
  } catch (error) {
    if (error?.code === "ENOENT") return null;
    if (
      error?.code === "invalid_session" ||
      error?.code === "invalid_state" ||
      error?.code === "invalid_state_version" ||
      error?.code === "input_too_large" ||
      error?.code === "invalid_input"
    ) {
      throw error; // Preserve invalid persisted bytes; the renderer suspends autosave.
    }
    if (error instanceof SyntaxError)
      throw makeError(
        "invalid_state",
        "The saved workspace is malformed; automatic saving is suspended",
      );
    throw error;
  }
}

async function loadState() {
  if (!stateLoadPromise) {
    stateLoadPromise = readPersistentState().catch((error) => {
      stateLoadPromise = null;
      throw error;
    });
  }
  return stateLoadPromise;
}

async function saveState(value) {
  const { state, content } = compactAndValidateState(value, false);
  const filePath = statePath();
  try {
    await fsp.stat(filePath);
    await preserveHistoryCompactionBackup(filePath);
  } catch (error) {
    if (error.code !== "ENOENT") throw error;
  }
  await atomicWriteText(filePath, content);
  stateLoadPromise = Promise.resolve(state);
  for (const task of state.tasks) {
    const run = activeRuns.get(activeTaskRuns.get(task.id));
    if (run) {
      run.titleSource = task.titleSource;
      run.titleEditedAt = task.titleEditedAt;
      run.hasBlockingQuestion =
        run.blockingStopRequested ||
        task.questions.some(
          (q) =>
            q.blocking &&
            q.blockingScope !== "agent" &&
            !q.answer?.trim() &&
            (!q.stepId || q.stepId === run.stepId),
        );
    }
  }
  return { saved: true, version: runtime.SESSION_VERSION };
}

function currentDevUrl() {
  const configured = process.env.DJINN_DEV_URL;
  if (!configured) return null;
  try {
    const parsed = new URL(configured);
    if (!["http:", "https:"].includes(parsed.protocol) || !parsed.hostname)
      return null;
    return parsed.toString();
  } catch {
    return null;
  }
}

function rendererFilePath() {
  return path.resolve(__dirname, "..", "dist", "index.html");
}

function isAllowedRendererUrl(rawUrl) {
  if (typeof rawUrl !== "string") return false;
  try {
    const parsed = new URL(rawUrl);
    const devUrl = currentDevUrl();
    if (devUrl && ["http:", "https:"].includes(parsed.protocol)) {
      return parsed.origin === new URL(devUrl).origin;
    }
    if (parsed.protocol === "file:") {
      return (
        path.resolve(decodeURIComponent(parsed.pathname)) === rendererFilePath()
      );
    }
  } catch {
    return false;
  }
  return false;
}

function assertTrustedSender(event) {
  const sender = event?.sender;
  const senderFrame = event?.senderFrame;
  if (
    !mainWindow ||
    mainWindow.isDestroyed() ||
    sender !== mainWindow.webContents
  ) {
    throw makeError("forbidden", "IPC sender is not the Djinn window");
  }
  if (
    !senderFrame ||
    !sender.mainFrame ||
    senderFrame.routingId !== sender.mainFrame.routingId
  ) {
    throw makeError(
      "forbidden",
      "IPC calls must originate from the top-level frame",
    );
  }
  if (!isAllowedRendererUrl(senderFrame.url)) {
    throw makeError(
      "forbidden",
      "IPC sender URL is not an allowed Djinn renderer",
    );
  }
}

function registerHandler(channel, handler) {
  ipcMain.handle(channel, async (event, ...args) => {
    try {
      assertTrustedSender(event);
      return await handler(...args);
    } catch (error) {
      return { ok: false, error: serializeError(error) };
    }
  });
}

function sendEvent(event) {
  if (
    !mainWindow ||
    mainWindow.isDestroyed() ||
    mainWindow.webContents.isDestroyed()
  )
    return;
  try {
    mainWindow.webContents.send("djinn:event", event);
  } catch {
    // Closing a window can race with a child process emitting its final event.
  }
}

// Action state is deliberately native and strongly held for the lifetime of
// the app. Renderer reloads reconcile it through getActions rather than
// trusting a renderer-supplied command or lifecycle status.
const runScopes = new Map();
const actionScopes = new Map();
function scopeAction(action, taskId, runId) {
  const key = `${taskId}:${action.id}`;
  const scope = runScopes.get(runId) || actionScopes.get(key);
  if (scope) actionScopes.set(key, scope);
  return { ...action, ...(scope || {}) };
}

function structuredProjectionKey(envelope) {
  const data =
    envelope?.data && typeof envelope.data === "object" ? envelope.data : {};
  if (envelope.type === "step_result")
    return String(
      data.stepId || envelope.stepId || data.runId || envelope.runId || envelope.eventId,
    );
  if (
    envelope.type === "question" ||
    envelope.type === "artifact" ||
    envelope.type === "work_item" ||
    envelope.type === "report"
  )
    return String(
      data.id || data.workItemId || data.runId || envelope.runId || envelope.eventId,
    );
  return String(envelope.eventId);
}

function recordStructuredProjection(envelope) {
  if (
    !envelope?.taskId ||
    !STRUCTURED_PROJECTION_TYPES.includes(envelope.type)
  )
    return;
  let projection = structuredInteractionProjections.get(envelope.taskId);
  if (!projection) {
    projection = new Map();
    structuredInteractionProjections.set(envelope.taskId, projection);
  }
  const key = `${envelope.type}:${structuredProjectionKey(envelope)}`;
  const compactData =
    envelope.data && typeof envelope.data === "object"
      ? compactStructuredValue(envelope.data)
      : envelope.data;
  if (
    envelope.type === "artifact" &&
    envelope.data &&
    typeof envelope.data === "object" &&
    typeof envelope.data.content === "string"
  )
    compactData.content = envelope.data.content;
  const compact = {
    ...envelope,
    data: compactData,
  };
  projection.delete(key);
  projection.set(key, compact);
  const projectionBytes = () =>
    [...projection.values()].reduce((sum, item) => sum + JSON.stringify(item).length, 0);
  while (
    projection.size > MAX_STRUCTURED_PROJECTION_EVENTS ||
    projectionBytes() > MAX_STRUCTURED_PROJECTION_BYTES
  )
    projection.delete(projection.keys().next().value);
}

function persistStructuredProjection(envelope) {
  if (
    !envelope?.taskId ||
    !STRUCTURED_PROJECTION_TYPES.includes(envelope.type)
  )
    return Promise.resolve();
  return getStructuredInteractionStore().upsert(envelope.taskId, envelope);
}

function structuredProjectionForTask(taskId) {
  if (typeof taskId !== "string") return [];
  const projection = structuredInteractionProjections.get(taskId);
  if (!projection) return [];
  return [...projection.values()].map((envelope) => ({
    ...envelope,
    data:
      envelope.data && typeof envelope.data === "object"
        ? { ...envelope.data }
        : envelope.data,
  }));
}

const actionRegistry = new actionRuntime.ActionRegistry({
  openExternal: (url) => shell.openExternal(url),
  environment: childEnvironment(),
  onUpdate: (action, taskId, meta = {}) => {
    sendEvent({
      runId: meta.runId || null,
      taskId: taskId || null,
      stepId: scopeAction(action, taskId, meta.runId).stepId,
      type: "action",
      timestamp: new Date().toISOString(),
      data: scopeAction(action, taskId, meta.runId),
    });
  },
});

function publishEvent(run, type, data, { durable = false } = {}) {
  if (!runtime.PUBLIC_EVENT_TYPES.includes(type)) return;
  const eventData = data && typeof data === "object" ? { ...data } : data;
  if (eventData && typeof eventData === "object")
    eventData.stepId = run?.stepId;
  if (run?.supervisor && eventData && typeof eventData === "object")
    eventData.supervisor = true;
  if (
    run?.agentId &&
    eventData &&
    typeof eventData === "object" &&
    !Array.isArray(eventData)
  ) {
    eventData.agentId = run.agentId;
    eventData.parentRunId = run.parentRunId;
    eventData.scope = "agent";
  }
  if (run && type !== "guidance") {
    const root = run.parent || run;
    root.lastActivityAt = new Date().toISOString();
    if (eventData && typeof eventData === "object") {
      eventData.lastActivityAt = root.lastActivityAt;
      eventData.activeAgentId ??=
        run.agentId || (root.phase === "workers" ? undefined : "lead");
      eventData.activeAgents = [...root.children]
        .filter((pass) => !pass.finished)
        .map((pass) => ({
          id: pass.agentId,
          task: pass.agent?.role,
          runId: pass.runId,
        }));
      eventData.activeTask ??=
        run.agent?.role ||
        root.agentDefinitions?.find(
          (agent) => agent.id === eventData.activeAgentId,
        )?.role;
    }
  }
  const envelope = {
    eventId: randomId("event-"),
    runId: run?.runId || null,
    taskId: run?.taskId || null,
    stepId: run?.stepId,
    type,
    timestamp: new Date().toISOString(),
    data: eventData,
  };
  const root = run?.parent || run;
  if (root?.kind === "lead") {
    appendReconnectEvent(root, envelope, type, eventData);
  }
  sendEvent(envelope);
  let journalWrite;
  try {
    journalWrite = envelope.taskId
      ? getMissionJournal().append(envelope.taskId, envelope)
      : Promise.resolve();
  } catch (error) {
    journalWrite = Promise.reject(error);
  }
  const persisted = journalWrite.then(() => {
    recordStructuredProjection(envelope);
    return persistStructuredProjection(envelope).then(() => envelope);
  });
  Object.defineProperty(envelope, "_persisted", {
    value: persisted,
    enumerable: false,
  });
  if (durable) return persisted;
  void persisted.catch((error) => {
    sendEvent({
      ...envelope,
      eventId: randomId("journal-error-"),
      type: "note",
      data: {
        title: "Journal natif non enregistré",
        detail: error.message,
      },
    });
  });
  return envelope;
}

function appendReconnectEvent(root, envelope, type, eventData) {
  root.journal ||= [];
  if (type === "text" && eventData?.streaming)
    root.journal = root.journal.filter(
      (e) =>
        !(
          e.runId === envelope.runId &&
          e.data?.messageId === eventData.messageId
        ),
    );
  root.journal.push(envelope);
  root.journalSize = root.journal.reduce(
    (sum, e) => sum + JSON.stringify(e).length,
    0,
  );
  while (
    root.journal.length > MAX_RECONNECT_EVENTS ||
    root.journalSize > MAX_RECONNECT_CHARACTERS
  ) {
    const removed = root.journal.shift();
    root.journalSize -= JSON.stringify(removed).length;
  }
  if (root.journalSize < 0) root.journalSize = 0;
}

function permissionPublicEvent(run, type, request, extra = {}) {
  const value = { request, ...request, ...extra };
  // Keep a flat copy for older renderer snapshots while the request object is
  // the canonical shape used by the current bridge.
  publishEvent(run, type, value);
}

function permissionContext(run, agent = null) {
  const root = run?.parent || run;
  return {
    taskId: root?.taskId || run?.taskId || "",
    runId: run?.runId || root?.runId || "",
    stepId: run?.stepId || root?.stepId,
    agentId: agent?.id || run?.agentId || "lead",
    agentName:
      agent?.name || run?.agent?.name || (run?.supervisor ? "Chef" : undefined),
    cwd: run?.cwd || root?.cwd,
  };
}

function livePermissionRun(entry) {
  const run = entry?.run;
  const root = run?.parent || run;
  if (
    !run ||
    run.finished ||
    root?.finished ||
    run.cancelRequested ||
    root?.cancelRequested ||
    run.questionStopRequested
  )
    return false;
  if (root?.runId && activeRuns.get(root.runId) !== root) return false;
  return true;
}

function forgetPermission(entry) {
  if (!entry) return;
  pendingPermissions.delete(entry.request.id);
  entry.run?.pendingPermissions?.delete(entry.request.id);
  maybeCloseClaudeInput(entry.run);
}

function maybeCloseClaudeInput(run) {
  if (
    !run?.providerCompleted ||
    !run.stdinOpen ||
    run.pendingPermissions?.size ||
    !run.child?.stdin ||
    run.child.stdin.destroyed
  )
    return false;
  run.stdinOpen = false;
  try {
    run.child.stdin.end();
    return true;
  } catch {
    return false;
  }
}

function writePermissionResponse(entry, decision, answers) {
  const payload = permissionProtocol.buildNativeResponse(
    entry,
    decision,
    answers,
  );
  if (!payload) return false;
  if (entry.provider === "codex") {
    return Boolean(entry.transport?.respond(entry.native.id, payload));
  }
  if (entry.provider === "claude") {
    const child = entry.run?.child;
    if (!child?.stdin || child.stdin.destroyed || entry.run.finished)
      return false;
    child.stdin.write(`${JSON.stringify(payload)}\n`);
    return true;
  }
  return false;
}

function declineCodexRequest(transport, nativeId, method) {
  if (permissionProtocol.isCodexRequestMethod(method))
    return transport?.respond(
      nativeId,
      permissionProtocol.buildCodexDeclineResponse(method),
    );
  return transport?.respond(nativeId, null, {
    error: {
      code: -32601,
      message: `Djinn does not support native request ${method}`,
    },
  });
}

function registerPermissionRequest(
  run,
  provider,
  method,
  nativeId,
  params,
  transport,
  agent = null,
) {
  const root = run?.parent || run;
  if (!run || !root || run.finished || root.finished || root.cancelRequested) {
    if (provider === "codex") declineCodexRequest(transport, nativeId, method);
    return null;
  }
  const duplicate = [...pendingPermissions.values()].find(
    (entry) =>
      entry.provider === provider &&
      (provider === "claude"
        ? entry.run === run
        : entry.transport === transport) &&
      String(entry.native.id) === String(nativeId),
  );
  // Providers may replay a control frame while the UI is reconnecting. Keep
  // one card and one native waiter; a second card could race the same wire id.
  if (duplicate) return duplicate.request;
  const normalized = permissionProtocol.normalizeNativeRequest({
    provider,
    method,
    nativeId,
    params,
    context: permissionContext(run, agent),
  });
  if (!normalized) {
    if (provider === "codex") {
      declineCodexRequest(transport, nativeId, method);
    } else if (provider === "claude" && run.child?.stdin)
      run.child.stdin.write(
        `${JSON.stringify({
          type: "control_response",
          response: {
            subtype: "success",
            request_id: String(nativeId),
            response: { behavior: "deny", message: "Unsupported request." },
          },
        })}\n`,
      );
    publishEvent(run, "note", {
      title: "Unsupported native request declined",
      detail: String(method).slice(0, 512),
      severity: "warning",
    });
    return null;
  }
  const request = normalized.request;
  const entry = {
    request,
    native: normalized.native,
    provider,
    method,
    run,
    transport,
    agent,
  };
  pendingPermissions.set(request.id, entry);
  run.pendingPermissions ||= new Set();
  run.pendingPermissions.add(request.id);
  permissionPublicEvent(run, "permission_requested", request);
  return request;
}

function resolvePermissionEntry(entry, decision, answers, reason) {
  if (!entry || !pendingPermissions.has(entry.request.id)) return false;
  const request = entry.request;
  if (decision === "acceptForSession" && !request.canAcceptForSession)
    throw makeError(
      "invalid_permission",
      "This native request does not support session approval",
    );
  if (!livePermissionRun(entry)) {
    cancelPermissionEntry(entry, reason || "run_closed");
    return false;
  }
  let sent = false;
  try {
    sent = writePermissionResponse(entry, decision, answers);
  } catch (error) {
    publishEvent(entry.run, "note", {
      title: "Native permission response failed",
      detail: error.message,
      severity: "error",
    });
  }
  if (!sent) return false;
  request.status = decision === "decline" ? "declined" : "accepted";
  request.updatedAt = new Date().toISOString();
  forgetPermission(entry);
  permissionPublicEvent(entry.run, "permission_resolved", request, {
    decision,
    ...(reason ? { reason } : {}),
  });
  return true;
}

function cancelPermissionEntry(entry, reason = "cancelled") {
  if (!entry || !pendingPermissions.has(entry.request.id)) return false;
  try {
    // Decline is the least privileged protocol response and lets a live
    // provider finish its turn before the enclosing run is closed.
    writePermissionResponse(entry, "decline");
  } catch {
    // Process close is authoritative when a provider has already gone away.
  }
  entry.request.status = "cancelled";
  entry.request.updatedAt = new Date().toISOString();
  forgetPermission(entry);
  permissionPublicEvent(entry.run, "permission_resolved", entry.request, {
    decision: "decline",
    reason,
  });
  return true;
}

function cancelPermissionsForRun(run, reason = "run_closed") {
  const root = run?.parent || run;
  // Closing one delegated passage must not cancel a sibling's native card.
  // Only the root owns the whole run tree; a child owns requests routed to
  // that child (including a provider child explicitly bound to its passage).
  for (const entry of [...pendingPermissions.values()]) {
    const belongsToRun =
      entry.run === run ||
      (run === root && (entry.run === root || entry.run?.parent === root));
    if (belongsToRun) cancelPermissionEntry(entry, reason);
  }
}

function getPendingPermissions(taskId) {
  return [...pendingPermissions.values()]
    .filter(
      (entry) =>
        entry.request.status === "pending" &&
        (taskId === undefined || entry.request.taskId === taskId) &&
        livePermissionRun(entry),
    )
    .sort((a, b) => a.request.createdAt.localeCompare(b.request.createdAt))
    .map((entry) => ({ ...entry.request }));
}

async function respondPermission(input) {
  if (!input || typeof input !== "object" || Array.isArray(input))
    throw makeError(
      "invalid_permission",
      "Permission response must be an object",
    );
  const taskId = typeof input.taskId === "string" ? input.taskId : "";
  const requestId = typeof input.requestId === "string" ? input.requestId : "";
  const decision = input.decision;
  if (
    !taskId ||
    !requestId ||
    !["accept", "acceptForSession", "decline"].includes(decision)
  )
    throw makeError("invalid_permission", "Invalid native permission response");
  if (
    input.answers !== undefined &&
    (!input.answers ||
      typeof input.answers !== "object" ||
      Array.isArray(input.answers))
  )
    throw makeError(
      "invalid_permission",
      "Permission answers must be an object",
    );
  const entry = pendingPermissions.get(requestId);
  if (!entry || entry.request.taskId !== taskId) return { resolved: false };
  return {
    resolved: resolvePermissionEntry(entry, decision, input.answers),
  };
}

function publishLoginEvent(run, status, data = {}) {
  const eventData = {
    provider: run.provider,
    status,
    ...data,
  };
  if (!eventData.url && typeof eventData.text === "string") {
    const match = eventData.text.match(/https?:\/\/[^\s<>"']+/i);
    if (match) {
      try {
        const url = new URL(match[0].replace(/[),.;]+$/, ""));
        if (["http:", "https:"].includes(url.protocol))
          eventData.url = url.toString();
      } catch {
        // Authentication output is still useful as text when a URL is malformed.
      }
    }
  }
  sendEvent({
    runId: run.runId,
    taskId: null,
    type: "auth",
    timestamp: new Date().toISOString(),
    data: eventData,
  });
}

function emitNormalizedProviderEvent(run, event) {
  if (!event || !runtime.PUBLIC_EVENT_TYPES.includes(event.type)) return;
  const nativeQuestion =
    event.type === "question" && event.data?.[NATIVE_INTERACTION] === true;
  event = runtime.scopeProviderEvent(run, event);
  if (!event) return;
  const data =
    event.data && typeof event.data === "object"
      ? { ...event.data }
      : event.data;
  if (data && typeof data === "object" && data.providerRunId) {
    run.providerRunId = String(data.providerRunId);
  }
  if (
    run.supervisor &&
    event.type === "note" &&
    typeof data?.forwardToAgentId === "string" &&
    typeof data?.guidanceId === "string"
  ) {
    const root = run.parent;
    const original = root.guidance.find((g) => g.id === data.guidanceId);
    if (
      original &&
      !original.agentId &&
      root.agentDefinitions.some((a) => a.id === data.forwardToAgentId)
    ) {
      const forwardedId = `${original.id}:forward:${data.forwardToAgentId}`;
      if (!root.guidance.some((g) => g.id === forwardedId))
        void steerRun({
          runId: root.runId,
          id: forwardedId,
          text: original.text,
          agentId: data.forwardToAgentId,
        }).catch((error) =>
          publishEvent(root, "note", {
            title: "Routage empêché",
            detail: error.message,
          }),
        );
    }
  }
  if (event.type === "action") {
    if (!run.taskId) return;
    try {
      actionRegistry.register(
        run.taskId,
        {
          ...data,
          ...(run.agentId && !data.agentId ? { agentId: run.agentId } : {}),
        },
        { projectRoot: run.cwd, runId: run.runId },
      );
    } catch (error) {
      publishEvent(run, "error", { message: error.message, phase: "action" });
    }
    return;
  }
  if (event.type === "agent" && queueProposedAgent(run, data)) return;
  if (event.type === "error") {
    const message = String(data?.message || data?.detail || "").trim();
    if (
      NON_FATAL_PROVIDER_DIAGNOSTICS.some((pattern) => pattern.test(message))
    ) {
      publishEvent(run, "note", {
        title: "Diagnostic du fournisseur",
        detail: message,
        provider: run.provider,
        stream: data?.stream || "stdout",
        severity: "warning",
      });
      return;
    }
    run.providerError = true;
  }
  if (event.type === "text" && data && typeof data.text === "string") {
    run.outputSummary = `${run.outputSummary}\n${data.text}`
      .trim()
      .slice(-8_000);
  }
  if (event.type === "step_result" && data && typeof data === "object") {
    // Keep the native completion proof with the provider passage. The root
    // lead copies it only after that passage closes successfully, so an
    // interrupted/restarted pass cannot accidentally unlock auto-preview.
    run.stepResult = {
      ...data,
      stepId: run.stepId,
      runId: run.runId,
    };
  }
  if (
    run.agentId &&
    run.agentId !== "lead" &&
    ["note", "artifact", "tool", "report", "work_item"].includes(event.type)
  ) {
    const report =
      event.type === "artifact"
        ? `Support: ${data?.title || data?.id || "support"} (${data?.type || "document"})`
        : event.type === "report"
          ? `${data?.summary || ""}\nDone: ${(data?.completed || []).join(", ")}\nRemaining: ${(data?.remaining || []).join(", ")}`
          : event.type === "work_item"
            ? `${data?.title || data?.id || "work item"} [${data?.status || "pending"}]\n${data?.detail || ""}`
        : `${data?.title || ""}\n${data?.detail || data?.message || ""}`;
    run.outputSummary = `${run.outputSummary}\n${report}`.trim().slice(-8_000);
  }
  if (event.type === "question" && data?.blocking !== false) {
    const root = run.parent || run;
    const observedActor = run.kind === "observed-pass";
    const questionScope = observedActor
      ? "agent"
      : nativeQuestion
        ? data.blockingScope || (nativeToolIsLead(run) ? "mission" : "agent")
        : "mission";
    if (questionScope === "agent") {
      // A worker question pauses only its own passage. Observed provider
      // children have no managed Djinn run, so interrupt their exact observed
      // turn through the bounded observer path instead of the parent turn.
      if (observedActor) {
        run.questionStopRequested = true;
        const observed = root.observedAgents?.get(run.agentId);
        if (observed) {
          void interruptObservedCodexAgent(root, run.codexServer, observed);
          if (!run.blockedEventSent) {
            run.blockedEventSent = true;
            emitAgentStatus(root, run.agent, "blocked", {
              runId: run.runId,
              lifecycle: "agent_blocked",
              questionId: data.id,
            });
          }
        }
      } else {
        run.blocked = true;
        if (!run.blockedEventSent && run.parent) {
          run.blockedEventSent = true;
          emitAgentStatus(root, run.agent, "blocked", {
            runId: run.runId,
            lifecycle: "agent_blocked",
            questionId: data.id,
          });
        }
        stopRunForBlockingQuestion(run);
      }
    } else {
      // Legacy text questions retain mission-wide blocking. Native lead
      // questions default here as well; an explicit native agent scope takes
      // the branch above.
      root.hasBlockingQuestion = true;
      root.blockingStopRequested = true;
      if (run.parent && (run.supervisor || run.agentId !== "lead")) {
        run.blocked = true;
        if (!run.blockedEventSent) {
          run.blockedEventSent = true;
          emitAgentStatus(root, run.agent, "blocked", {
            runId: run.runId,
            lifecycle: "agent_blocked",
            questionId: data.id,
          });
        }
        stopWorkersForBlockingQuestion(root, data.id);
      } else {
        stopRunForBlockingQuestion(run);
      }
    }
  }
  if (event.type === "status" && data && typeof data === "object") {
    // Provider lifecycle records are useful metadata, while UI lifecycle status
    // remains one of running/completed/cancelled/error.
    if (data.phase === "provider_started") {
      publishEvent(run, "note", {
        title: "Provider session started",
        providerRunId: run.providerRunId,
      });
      return;
    }
    if (data.phase === "provider_completed") {
      run.providerCompleted = true;
      maybeCloseClaudeInput(run);
      publishEvent(run, "note", {
        title: "Provider session completed",
        providerRunId: run.providerRunId,
      });
      return;
    }
  }
  return publishEvent(run, event.type, data);
}

function appendOutput(run, stream, chunk) {
  if (run.finished) return;
  const text =
    typeof chunk === "string" ? chunk : Buffer.from(chunk).toString("utf8");
  run.buffers[stream] = `${run.buffers[stream] || ""}${text}`;
  let newlineIndex = run.buffers[stream].indexOf("\n");
  while (newlineIndex >= 0) {
    const line = run.buffers[stream].slice(0, newlineIndex).replace(/\r$/, "");
    run.buffers[stream] = run.buffers[stream].slice(newlineIndex + 1);
    handleOutputLine(run, stream, line);
    newlineIndex = run.buffers[stream].indexOf("\n");
  }
  if (run.buffers[stream].length > MAX_OUTPUT_LINE) {
    const line = run.buffers[stream].slice(0, MAX_OUTPUT_LINE);
    run.buffers[stream] = "";
    handleOutputLine(run, stream, `${line}…`);
  }
}

function flushOutput(run, stream) {
  if (run.finished) return;
  const line = run.buffers[stream];
  run.buffers[stream] = "";
  if (line) handleOutputLine(run, stream, line.replace(/\r$/, ""));
}

function handleOutputLine(run, stream, line) {
  if (run.finished || !line) return;
  if (stream === "stdout" && run.provider === "claude") {
    const control = permissionProtocol.parseClaudeControlRequest(line);
    if (control) {
      registerPermissionRequest(
        run,
        "claude",
        "control_request",
        control.nativeId,
        control.params,
        null,
      );
      return;
    }
    // Host responses are not normally echoed by Claude. If a provider build
    // mirrors them, keep the control frame out of user-facing model text.
    try {
      const parsed = JSON.parse(line);
      if (parsed?.type === "control_response") return;
    } catch {
      // Continue with ordinary provider parsing for non-JSON output.
    }
  }
  try {
    const events =
      stream === "stdout"
        ? runtime.parseProviderLine(run.provider, line)
        : runtime.parseStderrLine(run.provider, line);
    for (const event of events) emitNormalizedProviderEvent(run, event);
  } catch (error) {
    publishEvent(run, "error", { message: error.message, stream });
  }
}

function providerSpec(provider) {
  runtime.validateProvider(provider);
  return PROVIDER_SPECS[provider];
}

function spawnCapture(command, args, timeoutMs = 4_000) {
  return new Promise((resolve) => {
    let child;
    let settled = false;
    let timedOut = false;
    let stdout = "";
    let stderr = "";
    const finish = (result) => {
      if (settled) return;
      settled = true;
      resolve({ ...result, stdout, stderr, timedOut });
    };
    try {
      child = spawn(command, args, {
        shell: false,
        windowsHide: true,
        stdio: ["ignore", "pipe", "pipe"],
        env: childEnvironment(),
      });
    } catch (error) {
      finish({ spawned: false, code: null, signal: null, error });
      return;
    }
    const timer = setTimeout(() => {
      timedOut = true;
      try {
        child.kill("SIGTERM");
      } catch {
        // The process may have exited between the timer and kill.
      }
    }, timeoutMs);
    const collect = (target, chunk) => {
      if (target === "stdout")
        stdout = `${stdout}${chunk}`.slice(-MAX_CAPTURED_STATUS_OUTPUT);
      else stderr = `${stderr}${chunk}`.slice(-MAX_CAPTURED_STATUS_OUTPUT);
    };
    child.stdout?.setEncoding("utf8");
    child.stderr?.setEncoding("utf8");
    child.stdout?.on("data", (chunk) => collect("stdout", chunk));
    child.stderr?.on("data", (chunk) => collect("stderr", chunk));
    child.once("error", (error) => {
      clearTimeout(timer);
      finish({ spawned: false, code: null, signal: null, error });
    });
    child.once("close", (code, signal) => {
      clearTimeout(timer);
      finish({ spawned: true, code, signal, error: null });
    });
  });
}

function parseVersion(output) {
  const line = String(output || "")
    .split(/\r?\n/)
    .map((value) => value.trim())
    .find(Boolean);
  return line ? line.slice(0, 256) : null;
}

function parseAuthenticationStatus(result) {
  const output = `${result.stdout || ""}\n${result.stderr || ""}`.trim();
  if (!output) return null;
  const inspectJson = (value) => {
    const values = [];
    const visit = (current, depth = 0) => {
      if (depth > 4 || current === null || current === undefined) return;
      if (typeof current === "object") {
        for (const [key, child] of Object.entries(current)) {
          if (/loggedin|authenticated|isloggedin|isauthenticated/i.test(key))
            values.push(child);
          visit(child, depth + 1);
        }
      }
    };
    visit(value);
    if (values.some((value) => value === true)) return true;
    if (values.some((value) => value === false)) return false;
    return null;
  };
  try {
    const parsed = JSON.parse(output);
    const status = inspectJson(parsed);
    if (status !== null) return status;
  } catch {
    // Human-readable output or JSONL is handled below.
  }
  for (const line of output.split(/\r?\n/)) {
    try {
      const parsed = JSON.parse(line);
      const status = inspectJson(parsed);
      if (status !== null) return status;
    } catch {
      // Human-readable status output is handled by the expressions below.
    }
  }
  const jsonFalse =
    /(?:loggedIn|isLoggedIn|authenticated|isAuthenticated)\s*["']?\s*:\s*false/i;
  const jsonTrue =
    /(?:loggedIn|isLoggedIn|authenticated|isAuthenticated)\s*["']?\s*:\s*true/i;
  if (jsonFalse.test(output)) return false;
  if (jsonTrue.test(output)) return true;
  const negative =
    /not\s+(?:logged|signed)\s*in|unauthenticated|no\s+(?:active\s+)?session|logged\s*out|not\s+authenticated/i;
  const positive =
    /logged\s*in|signed\s*in|authenticated|active\s+session|auth(?:enticated|ed)/i;
  if (negative.test(output)) return false;
  if (positive.test(output)) return true;
  return null;
}

async function detectProvider(spec) {
  const command = resolveProviderCommand(spec);
  const versionResult = await spawnCapture(command, ["--version"]);
  if (!versionResult.spawned) {
    return {
      id: spec.id,
      name: spec.name,
      available: false,
      authenticated: null,
      version: null,
      command,
    };
  }
  const authResult = await spawnCapture(command, spec.authArgs);
  return {
    id: spec.id,
    name: spec.name,
    available: true,
    authenticated: parseAuthenticationStatus(authResult),
    version: parseVersion(versionResult.stdout || versionResult.stderr),
    command,
  };
}

async function getEnvironment() {
  const providers = await Promise.all(
    Object.values(PROVIDER_SPECS).map(detectProvider),
  );
  let appVersion = "0.1.0";
  try {
    appVersion = app.getVersion();
  } catch {
    // Keep a useful value in test harnesses before Electron app is ready.
  }
  return { platform: process.platform, providers, appVersion };
}

async function getProviderModels(provider, refresh = false) {
  const spec = providerSpec(provider);
  const cached = modelCatalogCache.get(provider);
  if (!refresh && cached && Date.now() - cached.at < cached.ttl)
    return cached.promise;
  // Coalesce concurrent dialogs, including refresh while discovery is running.
  if (cached?.pending) return cached.promise;
  const entry = { at: Date.now(), ttl: 300000, pending: true };
  entry.promise = (async () => {
    const source = provider === "codex" ? "cli" : "t3-manifest";
    try {
      const command = resolveProviderCommand(spec);
      let models;
      if (provider === "codex")
        models = await discoverCodexModels(command, childEnvironment());
      else {
        const result = await spawnCapture(command, ["--version"]);
        if (!result.spawned || result.code !== 0 || result.timedOut)
          throw new Error(
            "Claude Code indisponible. Vérifiez la connexion du fournisseur.",
          );
        models = claudeModels(result.stdout || result.stderr);
      }
      if (!models.length)
        throw new Error(
          "Aucun modèle disponible. Vérifiez la connexion du fournisseur.",
        );
      return { provider, source, models };
    } catch (error) {
      entry.ttl = 5000;
      return {
        provider,
        source,
        models: [],
        error: String(error.message || error).slice(0, 500),
      };
    } finally {
      entry.pending = false;
    }
  })();
  modelCatalogCache.set(provider, entry);
  return entry.promise;
}

function createRun(input, kind = "lead", parentRunId = null, agent = null) {
  let resolveCompletion;
  const completion = new Promise((resolve) => {
    resolveCompletion = resolve;
  });
  return {
    runId: randomId(kind === "login" ? "login-" : ""),
    startedAt: new Date().toISOString(),
    journal: [],
    journalSize: 0,
    taskId: input?.taskId || null,
    stepId: input?.stepId,
    stepType: input?.step?.type,
    titleSource: input?.titleSource,
    titleEditedAt: input?.titleEditedAt,
    provider: input?.provider || null,
    cwd: input?.cwd || process.cwd(),
    workflowMode: input?.workflowMode,
    workflowOrigin: input?.workflowOrigin,
    resourcePolicy: input?.resourcePolicy,
    initialWorkflowProposal: input?.initialWorkflowProposal,
    workflowPolicy: input?.projectSnapshot?.workflowPolicy,
    kind,
    parentRunId,
    parent: null,
    currentChild: null,
    children: new Set(),
    agentId: agent?.id || null,
    agent,
    child: null,
    finished: false,
    cancelRequested: false,
    providerRunId: null,
    providerCompleted: false,
    stdinOpen: false,
    buffers: { stdout: "", stderr: "" },
    killTimer: null,
    completion,
    resolveCompletion,
    queuedAgents: [],
    outputSummary: "",
    executionPlan: null,
    mode: input?.mode || "execute",
    imageDir: null,
    hasBlockingQuestion: false,
    questionStopRequested: false,
    blocked: false,
    blockedEventSent: false,
    providerError: false,
    restartRequested: false,
    pendingAgents: new Set(),
    pendingPermissions: new Set(),
    // A lead can discover more work after the initial worker queue has
    // drained. Keep one serialized drain promise so a burst of provider
    // proposals cannot launch the same reader twice or race integration.
    targetedAgentDrain: null,
    agentDefinitions: input?.agents || [],
    workerSummaries: [],
    observedAgents: new Map(),
    observedCancellationAttempts: new Set(),
    observedCancellationUnavailable: new Set(),
    pendingObservedCancellation: new Set(),
    observedCancellationWaiters: new Set(),
    observedCancellationFailed: new Set(),
    observedCancellationTurnIds: new Map(),
    observedRestartWait: null,
    pendingFinish: null,
    observedCancellationTimer: null,
    observedCancellationTimedOut: false,
    observedCancellationClosing: false,
    observedCancellationShutdownFailed: false,
    observedCancellationShutdown: null,
    guidance: Array.isArray(input?.guidance)
      ? input.guidance.map((entry) => ({ ...entry, status: "queued" }))
      : [],
  };
}

function killProcessGroup(run, force = false) {
  const child = run.child;
  if (!child?.pid || child.pid === process.pid) return;
  if (process.platform === "win32") {
    if (!force) {
      try {
        child.kill("SIGTERM");
      } catch {
        // Fall through to the forced tree kill after the grace period.
      }
      return;
    }
    const args = ["/pid", String(child.pid), "/T", "/F"];
    const killer = spawn("taskkill", args, {
      shell: false,
      windowsHide: true,
      stdio: "ignore",
    });
    killer.once("error", () => undefined);
    return;
  }
  const signal = force ? "SIGKILL" : "SIGTERM";
  try {
    process.kill(-child.pid, signal);
  } catch {
    try {
      process.kill(child.pid, signal);
    } catch {
      // The process already exited.
    }
  }
}

function stopRunForBlockingQuestion(run) {
  if (!run || run.finished || run.questionStopRequested || run.cancelRequested)
    return false;
  run.questionStopRequested = true;
  if (run.codexServer) {
    interruptObservedCodexAgents(run);
    void run.codexServer.interrupt(run.codexKey).catch((error) =>
      publishEvent(run, "note", {
        title: "Interruption indisponible",
        detail: error.message,
      }),
    );
    return true;
  }
  if (!run.child) {
    finishRun(run, "completed", {
      message: "Run stopped while waiting for an answer.",
    });
    return true;
  }
  killProcessGroup(run);
  run.killTimer = setTimeout(() => {
    killProcessGroup(run, true);
    run.killTimer = null;
    // The directory remains locked until close confirms process termination.
  }, PROCESS_KILL_GRACE_MS);
  return true;
}

function stopWorkersForBlockingQuestion(root, questionId) {
  for (const candidate of [...activeRuns.values()]) {
    if (candidate.parent === root && !candidate.finished) {
      candidate.blocked = true;
      if (!candidate.blockedEventSent) {
        candidate.blockedEventSent = true;
        emitAgentStatus(root, candidate.agent, "blocked", {
          runId: candidate.runId,
          lifecycle: "agent_blocked",
          questionId,
        });
      }
      stopRunForBlockingQuestion(candidate);
    }
  }
}

async function publishLoginCompletion(run, status, details) {
  const data = {
    message:
      details.message ||
      (status === "completed"
        ? "Provider login completed."
        : status === "cancelled"
          ? "Provider login cancelled."
          : "Provider login failed."),
    exitCode: details.exitCode,
    signal: details.signal,
  };
  if (status === "completed") {
    try {
      data.providers = (await getEnvironment()).providers;
    } catch {
      // The completion event still reaches the renderer if a status refresh fails.
    }
  }
  publishLoginEvent(run, status, data);
}

function stepResultSatisfiesExitCriteria(run) {
  if (!run?.stepId) return true;
  const step = run.validatedInput?.step || run.step;
  const expected = Array.isArray(step?.exitCriteria)
    ? step.exitCriteria
        .map((criterion) => String(criterion || "").trim())
        .filter(Boolean)
    : [];
  const result = run.stepResult;
  if (!result || result.status !== "ready") return false;
  const criteria = Array.isArray(result.criteria) ? result.criteria : [];
  // Every reported criterion needs affirmative proof. A ready status without
  // evidence is insufficient to unlock a runnable preview.
  if (
    criteria.some(
      (criterion) =>
        criterion?.met !== true ||
        typeof criterion?.evidence !== "string" ||
        !criterion.evidence.trim(),
    )
  )
    return false;
  return expected.every((criterion) =>
    criteria.some(
      (reported) =>
        typeof reported?.criterion === "string" &&
        reported.criterion.trim() === criterion &&
        reported.met === true &&
        typeof reported.evidence === "string" &&
        reported.evidence.trim(),
    ),
  );
}

function nativeToolResponse(success, value) {
  let text;
  try {
    text = JSON.stringify(value);
  } catch {
    text = JSON.stringify({ error: { code: "serialization_error", message: "Tool result is not JSON serializable" } });
  }
  return {
    success: Boolean(success),
    contentItems: [
      {
        type: "inputText",
        text: String(text || "{}").slice(0, 64_000),
      },
    ],
  };
}

function nativeToolFailure(error, code = "invalid_tool_call") {
  return nativeToolResponse(false, {
    error: {
      code: typeof error?.code === "string" ? error.code : code,
      message: String(error?.message || error || "Native tool call rejected").slice(
        0,
        4_000,
      ),
    },
  });
}

function nativeToolScope(root, run, data, field = "agentId") {
  const result = { ...data };
  const ownAgentId = run.agentId && run.agentId !== "lead" ? run.agentId : null;
  if (ownAgentId) {
    if (result[field] !== undefined && result[field] !== ownAgentId)
      throw makeError(
        "invalid_scope",
        `Native tool scope belongs to agent ${ownAgentId}`,
      );
    result[field] = ownAgentId;
    return result;
  }
  if (result[field] !== undefined) {
    const known = new Set([
      "lead",
      ...(root.agentDefinitions || []).map((agent) => agent.id),
      ...[...(root.observedAgents?.values() || [])].map((agent) => agent.id),
    ]);
    if (!known.has(result[field]))
      throw makeError("invalid_scope", `Unknown native agent scope ${result[field]}`);
  }
  return result;
}

function observedNativePublicationRun(root, target, observed) {
  const parent = target?.parent || root;
  const sourceRunId = target?.runId || root.runId;
  const observedId = String(observed.id).slice(0, 256);
  return {
    ...(target || root),
    kind: "observed-pass",
    runId: `${sourceRunId}:observed:${observedId}`.slice(0, 256),
    // Renderer ownership is rooted at the mission run. Keep the managed
    // passage only in the local sourceRun reference used for summary
    // propagation; a child gate must never have to know that internal id.
    parentRunId: root.runId,
    parent,
    taskId: root.taskId,
    stepId: target?.stepId || parent.stepId || root.stepId,
    agentId: observedId,
    agent: {
      id: observedId,
      name: observed.name || observed.role || "Sous-agent Codex",
      role: observed.role || "Sous-agent Codex",
      readOnly: true,
    },
    provider: "codex",
    supervisor: false,
    finished: false,
    cancelRequested: false,
    blocked: false,
    blockedEventSent: false,
    outputSummary: "",
    children: new Set(),
  };
}

function propagateObservedNativeSummary(target, observed, eventType, data) {
  if (!target || target.finished || !observed || !data) return;
  const summary =
    eventType === "report"
      ? `${data.summary || ""}\nDone: ${(data.completed || []).join(", ")}\nRemaining: ${(data.remaining || []).join(", ")}`
      : eventType === "artifact"
        ? `Support: ${data.title || data.id || "support"} (${data.type || "document"})`
        : eventType === "work_item"
          ? `${data.title || data.id || "work item"} [${data.status || "pending"}]\n${data.detail || ""}`
          : `${data.title || ""}\n${data.detail || data.message || ""}`;
  const prefix = `[${observed.name || observed.id}]`;
  target.outputSummary = `${target.outputSummary || ""}\n${prefix} ${summary}`
    .trim()
    .slice(-8_000);
  if (eventType === "report") {
    target.observedReports ||= [];
    target.observedReports.push({
      ...data,
      agentId: observed.id,
      providerThreadId: observed.providerThreadId,
    });
    if (target.observedReports.length > 32) target.observedReports.shift();
  }
}

function nativeToolContext(root, server, params) {
  if (!root || root.finished || root.cancelRequested)
    throw makeError("invalid_scope", "The mission is no longer active");
  if (!params || typeof params !== "object" || Array.isArray(params))
    throw makeError("invalid_tool_call", "Native tool parameters are invalid");
  const threadId = typeof params.threadId === "string" ? params.threadId : "";
  const turnId = typeof params.turnId === "string" ? params.turnId : "";
  if (!threadId || !turnId)
    throw makeError("invalid_scope", "Native tool thread or turn is missing");
  const current = activeRuns.get(activeTaskRuns.get(root.taskId)) || root;
  const requestContext = codexPermissionContextForRequest(
    root,
    server,
    current,
    params,
  );
  if (!requestContext)
    throw makeError("invalid_scope", "Native tool thread is outside this mission");
  const { target, observed } = requestContext;
  const binding = server.djinnAgentBindings?.get(threadId);
  const managed = codexRunForThread(root, server, threadId);
  if (observed && !managed && !binding) {
    if (!observedAgentIsActive(observed))
      throw makeError("invalid_scope", "Observed Codex agent is no longer active");
    if (observed.turnId && String(observed.turnId) !== turnId)
      throw makeError("invalid_scope", "Native tool turn is no longer active");
    const liveTurnId = server.turns?.get(threadId)?.turnId || observed.turnId;
    if (!liveTurnId || String(liveTurnId) !== turnId)
      throw makeError("invalid_scope", "Native tool turn is no longer active");
    const run = observedNativePublicationRun(root, target, observed);
    return {
      root,
      run,
      sourceRun: target,
      observed,
      binding: null,
      threadId,
      turnId,
    };
  }
  if (!binding || binding.rootRunId !== root.runId || !binding.runId)
    throw makeError("invalid_scope", "Native tool thread is outside this mission");
  const run = activeRuns.get(binding.runId);
  if (!run || run.finished || (run.parent || run) !== root)
    throw makeError("invalid_scope", "Native tool passage is no longer active");
  const turn = server.turns?.get(threadId);
  if (!turn || !turn.turnId || String(turn.turnId) !== turnId)
    throw makeError("invalid_scope", "Native tool turn is no longer active");
  return { root, run, sourceRun: run, observed: null, binding, threadId, turnId };
}

function nativeToolArguments(value) {
  let data = value;
  if (typeof data === "string") {
    if (data.length > runtime.MAX_NATIVE_TOOL_ARGUMENTS_LENGTH)
      throw makeError("input_too_large", "Native tool arguments are too large");
    try {
      data = JSON.parse(data);
    } catch {
      throw makeError("invalid_tool_call", "Native tool arguments are not valid JSON");
    }
  }
  if (!data || typeof data !== "object" || Array.isArray(data))
    throw makeError("invalid_tool_call", "Native tool arguments must be an object");
  try {
    if (JSON.stringify(data).length > runtime.MAX_NATIVE_TOOL_ARGUMENTS_LENGTH)
      throw makeError("input_too_large", "Native tool arguments are too large");
  } catch (error) {
    if (error?.code === "input_too_large") throw error;
    throw makeError("invalid_tool_call", "Native tool arguments are not serializable");
  }
  return data;
}

function nativeToolIsLead(run) {
  return Boolean(
    run?.kind === "lead-pass" &&
      run.agentId === "lead" &&
      !run.supervisor &&
      run.parent?.kind === "lead",
  );
}

async function handleNativeToolCall(root, server, nativeId, params) {
  let context;
  try {
    context = nativeToolContext(root, server, params);
    const tool = typeof params.tool === "string" ? params.tool : "";
    if (!runtime.NATIVE_TOOL_NAMES.includes(tool))
      throw makeError("unknown_tool", `Unknown native tool ${tool || "(empty)"}`);
    const input = nativeToolArguments(params.arguments);
    const { run, sourceRun, observed } = context;
    const publishNative = async (eventType, eventData) => {
      const envelope = emitNormalizedProviderEvent(run, {
        type: eventType,
        data: eventData,
      });
      if (!envelope)
        throw makeError(
          "invalid_scope",
          `Native ${eventType} is not allowed in the current stage scope`,
        );
      if (observed && sourceRun && sourceRun !== run)
        propagateObservedNativeSummary(sourceRun, observed, eventType, envelope.data);
      await getMissionJournal().flush(root.taskId);
      if (envelope?._persisted) await envelope._persisted;
      return envelope;
    };
    let eventType;
    let eventData;
    let response;
    if (tool === "publish_question") {
      eventType = "question";
      eventData = runtime.validateNativeQuestionData(input);
      eventData.blocking ??= true;
      eventData = nativeToolScope(root, run, eventData);
      eventData.blockingScope =
        run.kind === "observed-pass"
          ? "agent"
          : eventData.blockingScope ||
            (nativeToolIsLead(run) ? "mission" : "agent");
      Object.defineProperty(eventData, NATIVE_INTERACTION, {
        value: true,
        enumerable: false,
      });
      if (!eventData.id) {
        run.nativeQuestionSequence = (run.nativeQuestionSequence || 0) + 1;
        eventData.id = `${run.runId}:question:${run.nativeQuestionSequence}`.slice(
          0,
          256,
        );
      }
      await publishNative(eventType, eventData);
      response = {
        ok: true,
        event: eventType,
        questionId: eventData.id,
        blockingScope: eventData.blockingScope,
      };
    } else if (tool === "publish_step_report") {
      if (!nativeToolIsLead(run))
        throw makeError(
          "invalid_scope",
          "Only the native lead may publish a step report",
        );
      eventType = "step_result";
      eventData = runtime.validateProtocolData(
        "step_result",
        runtime.validateNativeReportData(input, { step: true }),
      );
      await publishNative(eventType, eventData);
      response = { ok: true, event: eventType, stepId: run.stepId };
    } else if (tool === "publish_report") {
      if (!run.agentId || run.agentId === "lead" || run.supervisor)
        throw makeError(
          "invalid_scope",
          "Only a bounded worker may publish a worker report",
        );
      eventType = "report";
      eventData = runtime.validateNativeReportData(input);
      eventData = nativeToolScope(root, run, eventData);
      if (!eventData.id) {
        run.nativeReportSequence = (run.nativeReportSequence || 0) + 1;
        eventData.id = `${run.runId}:report:${run.nativeReportSequence}`.slice(
          0,
          256,
        );
      }
      await publishNative(eventType, eventData);
      response = { ok: true, event: eventType, reportId: eventData.id };
    } else if (tool === "update_task") {
      eventType = "work_item";
      eventData = runtime.validateProtocolData("work_item", input);
      eventData = nativeToolScope(root, run, eventData);
      // publishEvent stamps the authoring agent into agentId. Preserve the
      // lead's explicit worker assignment separately so the work item keeps
      // its target after that author stamp is applied.
      if (eventData.agentId) eventData.assignedAgentId = eventData.agentId;
      await publishNative(eventType, eventData);
      response = { ok: true, event: eventType, workItemId: eventData.id };
    } else if (tool === "publish_artifact") {
      eventType = "artifact";
      eventData = runtime.validateNativeArtifactData(input);
      eventData = nativeToolScope(root, run, eventData);
      if (!eventData.id) {
        run.nativeArtifactSequence = (run.nativeArtifactSequence || 0) + 1;
        eventData.id = `${run.runId}:artifact:${run.nativeArtifactSequence}`.slice(
          0,
          256,
        );
      }
      await publishNative(eventType, eventData);
      response = { ok: true, event: eventType, artifactId: eventData.id };
    } else if (tool === "inspect_test_environment") {
      // This diagnostic intentionally returns only a tool result on success;
      // validation/inspection failures are durable error events. It cannot
      // execute a provider-supplied command.
      try {
        const environment = await inspectTestEnvironment(run.cwd, input);
        response = { ok: true, event: "test_environment", ...environment };
      } catch (error) {
        await publishNative("error", {
          message: String(error?.message || error).slice(0, 4_000),
          phase: "test_environment",
        });
        throw error;
      }
    } else if (tool === "publish_test_action") {
      if (run.supervisor)
        throw makeError(
          "invalid_scope",
          "A supervisor cannot publish executable test actions",
        );
      const nativeAction = runtime.validateNativeTestActionData(input);
      let actionData = nativeToolScope(root, run, nativeAction);
      // The action registry treats an explicit id as idempotent and returns
      // the existing recipe. A new native proposal must therefore get a
      // bounded version id rather than silently reusing stale test details.
      if (
        actionData.id &&
        actionRegistry
          .getActions(root.taskId)
          .some((candidate) => candidate.id === actionData.id)
      ) {
        run.nativeActionSequence = (run.nativeActionSequence || 0) + 1;
        actionData = {
          ...actionData,
          id: `${actionData.id}:${run.runId}:${run.nativeActionSequence}`.slice(
            0,
            256,
          ),
        };
      }
      const action = actionRegistry.register(root.taskId, actionData, {
        projectRoot: run.cwd,
        runId: run.runId,
      });
      // ActionRegistry emits the live UI update synchronously, but its
      // in-memory recipe is not the mission journal. Persist the scoped action
      // before acknowledging the native tool so a renderer/app reload can
      // recover the recipe independently of the registry process lifetime.
      await publishEvent(
        run,
        "action",
        scopeAction(action, root.taskId, run.runId),
        { durable: true },
      );
      if (action.workItemId || action.target) {
        // Action registration emits the executable recipe synchronously. Keep
        // a durable link note as well so a renderer reload can associate it
        // with the corresponding work item without re-running the action.
        await publishEvent(
          run,
          "note",
          {
            title: "Test action linked",
            detail: `${action.workItemId ? `work item ${action.workItemId}` : ""}${action.workItemId && action.target ? " · " : ""}${action.target || ""}`.slice(
              0,
              4_000,
            ),
            workItemId: action.workItemId,
            target: action.target,
            actionId: action.id,
          },
          { durable: true },
        );
        await getMissionJournal().flush(root.taskId);
      }
      response = { ok: true, event: "action", actionId: action.id };
    }
    return nativeToolResponse(true, response);
  } catch (error) {
    return nativeToolFailure(error);
  }
}

function finishRun(run, status, details = {}) {
  if (run.finished) return;
  if (run.observedCancellationClosing || run.observedCancellationShutdownFailed)
    return;
  cancelPermissionsForRun(
    run,
    status === "cancelled" ? "run_cancelled" : "run_closed",
  );
  const explicitStopRequested = Boolean(
    run.cancelRequested ||
    run.questionStopRequested ||
    run.hasBlockingQuestion ||
    run.blockingStopRequested,
  );
  if (
    run.kind === "lead" &&
    ["cancelled", "completed"].includes(status) &&
    explicitStopRequested &&
    !run.observedCancellationTimedOut &&
    (run.pendingObservedCancellation?.size ||
      run.observedCancellationFailed?.size)
  ) {
    run.pendingFinish ||= { status, details };
    if (!run.observedCancellationTimer) {
      run.observedCancellationTimer = setTimeout(() => {
        run.observedCancellationTimer = null;
        run.observedCancellationTimedOut = true;
        for (const id of run.pendingObservedCancellation) {
          run.observedCancellationFailed.add(id);
          const record = run.observedAgents?.get(id);
          if (record)
            observedCancellationNote(
              run,
              record,
              `aucune notification de fermeture reçue après ${PROCESS_KILL_GRACE_MS} ms`,
            );
        }
        run.pendingObservedCancellation.clear();
        notifyObservedCancellationWaiters(run);
        run.observedCancellationClosing = true;
        const pending = run.pendingFinish || { status, details };
        const close = closeCancelledCodexServer(run);
        if (!close) {
          run.observedCancellationClosing = false;
          run.pendingFinish = null;
          finishRun(run, pending.status, pending.details);
          return;
        }
        void close.then(
          () => {
            run.observedCancellationClosing = false;
            run.pendingFinish = null;
            finishRun(run, pending.status, pending.details);
          },
          (error) => {
            run.observedCancellationClosing = false;
            run.observedCancellationShutdownFailed = true;
            run.pendingFinish = null;
            run.blocked = true;
            publishEvent(run, "note", {
              title: "Fermeture Codex non confirmée",
              detail: String(error?.message || error).slice(0, 2_000),
              severity: "error",
            });
            publishEvent(run, "status", {
              status: "blocked",
              phase: run.phase,
              waitingForAgents: true,
              activeAgentId: "lead",
            });
          },
        );
      }, PROCESS_KILL_GRACE_MS);
      run.observedCancellationTimer.unref?.();
    }
    return;
  }
  if (run.observedCancellationTimer) {
    clearTimeout(run.observedCancellationTimer);
    run.observedCancellationTimer = null;
  }
  run.pendingFinish = null;
  if (run.kind === "lead") {
    for (const observed of run.observedAgents?.values() || []) {
      const { signature, parentProviderThreadId, turnId, ...record } = observed;
      record.live = false;
      publishEvent(run, "agent", record);
    }
  }
  run.finished = true;
  if (run.killTimer) {
    clearTimeout(run.killTimer);
    run.killTimer = null;
  }
  activeRuns.delete(run.runId);
  if (
    run.kind === "lead" &&
    run.taskId &&
    activeTaskRuns.get(run.taskId) === run.runId
  ) {
    activeTaskRuns.delete(run.taskId);
  }
  if (
    run.kind === "lead" &&
    run.cwd &&
    activeCwdRuns.get(run.cwd) === run.runId
  ) {
    activeCwdRuns.delete(run.cwd);
  }
  if (run.parent?.currentChild === run) run.parent.currentChild = null;
  if (run.parent?.children) run.parent.children.delete(run);
  if (
    run.parent?.kind === "lead" &&
    run.parent.cancelRequested &&
    !run.parent.finished &&
    ![...(run.parent.children || [])].some((child) => !child.finished)
  ) {
    finishRun(run.parent, "cancelled", {
      message: "Run cancelled after its active passes closed.",
    });
  }
  void cleanupRunImages(run);

  if (run.kind === "lead") {
    for (const entry of run.guidance || [])
      if (entry.status !== "consumed" && entry.status !== "prevented") {
        entry.status = "prevented";
        publishEvent(run, "guidance", {
          ...entry,
          reason: run.hasBlockingQuestion
            ? "blocking_answers_required"
            : status === "cancelled"
              ? "run_cancelled"
              : "provider_not_started",
        });
      }
  }
  if (run.kind === "login") {
    void publishLoginCompletion(run, status, details);
    run.resolveCompletion({ status, ...details });
    return;
  }
  const interrupted =
    run.restartRequested &&
    !run.cancelRequested &&
    !run.questionStopRequested &&
    !run.parent?.cancelRequested;
  if (interrupted) {
    run.resolveCompletion({ status: "interrupted", ...details });
    return;
  }
  if (status === "error" && !run.providerError) {
    publishEvent(run, "error", {
      message: details.message || "Provider process failed",
      exitCode: details.exitCode,
      signal: details.signal,
    });
  }
  if (status !== "cancelled" || !run.cancelNoticeSent) {
    publishEvent(run, "status", {
      status,
      provider: run.provider,
      providerRunId: run.providerRunId,
      exitCode: details.exitCode,
      signal: details.signal,
      scope: run.agentId ? "agent" : undefined,
    });
  }
  if (run.agentId && run.parentRunId && !run.supervisor) {
    publishEvent({ ...run, runId: run.parentRunId, agentId: null }, "agent", {
      id: run.agentId,
      name: run.agent?.name,
      role: run.agent?.role,
      writeScope: run.agent?.writeScope,
      dependsOn: run.agent?.dependsOn,
      readOnly: run.agent?.readOnly,
      isolation: run.agent?.isolation,
      resources: run.agent?.resources,
      status: run.blocked
        ? "blocked"
        : status === "completed"
          ? "done"
          : status === "cancelled"
            ? "queued"
            : "error",
      lifecycle:
        run.blocked || status === "cancelled"
          ? "agent_blocked"
          : "agent_completed",
      runId: run.runId,
      ...(run.branch ? { branch: run.branch } : {}),
      ...(run.worktree ? { worktree: run.worktree } : {}),
      progress: run.blocked || status !== "completed" ? undefined : 100,
      summary:
        run.outputSummary ||
        details.message ||
        (status === "completed" ? "Completed" : status),
    });
  }
  const workerStatuses = Array.isArray(run.workerSummaries)
    ? run.workerSummaries.map((worker) => worker.status)
    : [];
  const stepResultReady = stepResultSatisfiesExitCriteria(run);
  const canAutoStartProjectServer =
    run.kind === "lead" &&
    run.mode === "execute" &&
    status === "completed" &&
    !run.hasBlockingQuestion &&
    !run.cancelRequested &&
    !run.providerError &&
    stepResultReady &&
    workerStatuses.every((workerStatus) =>
      ["completed", "done"].includes(workerStatus),
    );
  if (canAutoStartProjectServer && run.taskId && run.cwd) {
    void actionRegistry
      .maybeAutoStart(run.taskId, run.cwd, {
        runId: run.runId,
        mode: run.mode,
        runKind: run.kind,
        status,
        hasBlockingQuestion: run.hasBlockingQuestion,
        cancelRequested: run.cancelRequested,
        workerStatuses,
      })
      .catch((error) => {
        // Auto-preview is an end-of-run affordance. Its failure must not turn a
        // completed implementation run into a task error; expose the bounded
        // failure as a note/action-card update instead.
        try {
          actionRegistry.register(
            run.taskId,
            {
              kind: "manual",
              title: "Vérifier l’aperçu du projet",
              detail: String(error?.message || error).slice(0, 2_000),
            },
            { projectRoot: run.cwd, runId: run.runId },
          );
        } catch {
          publishEvent(run, "note", {
            title: "Aperçu du projet indisponible",
            detail: String(error?.message || error).slice(0, 2_000),
          });
        }
      });
  }
  if (
    run.supervisor &&
    run.parent &&
    !run.parent.finished &&
    run.parent.phase === "workers"
  )
    publishEvent(run.parent, "status", {
      status: "running",
      phase: "workers",
      waitingForAgents: true,
      leadActivity: "supervises",
    });
  run.resolveCompletion({ status, ...details });
}

function codexServerForCancellation(run) {
  const root = run?.parent || run;
  const candidates = [
    run,
    root,
    run?.currentChild,
    ...(run?.children || []),
    ...(root?.children || []),
  ];
  for (const candidate of candidates) {
    const server = candidate?.codexServer;
    if (server && !server.closed) return server;
  }
  const server = root?.taskId ? codexServers.get(root.taskId) : null;
  return server && !server.closed ? server : null;
}

function closeCancelledCodexServer(root) {
  if (!root?.cancelRequested || !root.taskId) return null;
  if (root.observedCancellationShutdown)
    return root.observedCancellationShutdown;
  const server = codexServerForCancellation(root);
  if (!server) return null;
  const shared = [...activeRuns.values()].some((candidate) => {
    const candidateRoot = candidate.parent || candidate;
    return (
      candidateRoot !== root &&
      !candidateRoot.finished &&
      candidateRoot.taskId === root.taskId &&
      candidate.codexServer === server
    );
  });
  if (shared) return null;
  let shutdown;
  try {
    if (typeof server.abortTransport === "function") {
      shutdown = server.abortTransport(
        new Error("Codex app-server stopped after mission cancellation"),
      );
    } else {
      server.close();
    }
  } catch (error) {
    shutdown = Promise.reject(error);
  }
  root.observedCancellationShutdown = Promise.resolve(shutdown);
  return root.observedCancellationShutdown;
}

function forceCancelledCodexShutdown(root, message) {
  if (!root?.cancelRequested || root.finished) return Promise.resolve();
  if (root.observedCancellationClosing)
    return root.observedCancellationShutdown;
  root.observedCancellationClosing = true;
  root.observedCancellationTimedOut = true;
  const shutdown = closeCancelledCodexServer(root);
  if (!shutdown) {
    root.observedCancellationClosing = false;
    blockObservedRestart(root);
    return Promise.resolve();
  }
  return shutdown.then(
    () => {
      root.pendingObservedCancellation.clear();
      root.observedCancellationClosing = false;
      for (const pass of [...(root.children || [])])
        finishRun(pass, "cancelled", { message });
      finishRun(root, "cancelled", { message });
    },
    (error) => {
      root.observedCancellationClosing = false;
      root.observedCancellationShutdownFailed = true;
      blockObservedRestart(root);
      publishEvent(root, "note", {
        title: "Fermeture Codex non confirmée",
        detail: error.message,
      });
    },
  );
}

function cancelOneRun(run) {
  if (!run || run.finished) return false;
  const wasRequested = run.cancelRequested;
  run.cancelRequested = true;
  run.questionStopRequested = false;
  if (!wasRequested && run.kind !== "login") {
    publishEvent(run, "status", {
      status: "stopping",
      provider: run.provider,
      scope: run.agentId ? "agent" : undefined,
    });
  }
  const server = codexServerForCancellation(run);
  if (server) {
    interruptObservedCodexAgents(run, server);
    if (run.codexServer === server && run.codexKey) {
      void server.interrupt(run.codexKey).catch((error) => {
        publishEvent(run, "note", {
          title: "Interruption indisponible",
          detail: error.message,
        });
        void forceCancelledCodexShutdown(run.parent || run, error.message);
      });
      return true;
    }
  }
  const activeChildren = [...(run.children || [])].filter(
    (child) => !child.finished,
  );
  if (!run.child && activeChildren.length) {
    for (const child of activeChildren) cancelOneRun(child);
    return true;
  }
  if (!run.child && run.currentChild && !run.currentChild.finished) {
    cancelOneRun(run.currentChild);
    return true;
  }
  if (!run.child) {
    finishRun(run, "cancelled", {
      message: "Run cancelled before provider start.",
    });
    return true;
  }
  if (run.killTimer) return true;
  killProcessGroup(run);
  run.killTimer = setTimeout(() => {
    killProcessGroup(run, true);
    run.killTimer = null;
    // Do not release the writer before close, even after forced termination.
  }, PROCESS_KILL_GRACE_MS);
  return true;
}

function wireProcess(run, child) {
  run.child = child;
  child.stdout?.setEncoding("utf8");
  child.stderr?.setEncoding("utf8");
  child.stdout?.on("data", (chunk) => appendOutput(run, "stdout", chunk));
  child.stderr?.on("data", (chunk) => appendOutput(run, "stderr", chunk));
  child.once("error", (error) => {
    if (!run.finished) {
      finishRun(
        run,
        run.cancelRequested
          ? "cancelled"
          : run.questionStopRequested
            ? "completed"
            : "error",
        { message: error.message },
      );
    }
  });
  child.once("close", (code, signal) => {
    if (run.killTimer) {
      clearTimeout(run.killTimer);
      run.killTimer = null;
    }
    if (run.finished) return;
    flushOutput(run, "stdout");
    flushOutput(run, "stderr");
    if (run.restartRequested) killProcessGroup(run, true);
    const status = run.cancelRequested
      ? "cancelled"
      : run.questionStopRequested
        ? "completed"
        : code === 0 && !run.providerError
          ? "completed"
          : "error";
    finishRun(run, status, {
      exitCode: code,
      signal,
      message:
        status === "error"
          ? run.providerError
            ? "Provider reported an explicit failure"
            : `Provider exited with code ${code ?? "unknown"}`
          : undefined,
    });
  });
  if (run.cancelRequested) cancelOneRun(run);
}

function codexRunForThread(root, server, threadId) {
  if (!threadId) return null;
  for (const candidate of activeRuns.values()) {
    if (
      candidate === root ||
      (candidate.parent !== root && candidate.parent?.parent !== root)
    )
      continue;
    if (candidate.codexServer !== server) continue;
    if (
      candidate.providerRunId === threadId ||
      (candidate.codexKey &&
        server.threads?.get(candidate.codexKey) === threadId)
    )
      return candidate;
  }
  return null;
}

function codexRunForObservedThread(root, server, threadId) {
  const seen = new Set();
  let currentThreadId = threadId;
  while (currentThreadId && !seen.has(currentThreadId)) {
    seen.add(currentThreadId);
    const managed = codexRunForThread(root, server, currentThreadId);
    if (managed) return managed;
    const observed = server.codexAgentObserver?.get(currentThreadId);
    currentThreadId =
      observed?.parentProviderThreadId || observed?.parentThreadId || null;
  }
  return null;
}

function codexPermissionContextForRequest(root, server, current, params) {
  const threadId =
    typeof params?.threadId === "string" && params.threadId.length <= 256
      ? params.threadId
      : null;
  if (!threadId) return null;
  const binding = server.djinnAgentBindings?.get(threadId);
  const boundRun = binding?.runId ? activeRuns.get(binding.runId) : null;
  // A binding survives provider history reuse. If its Djinn passage has
  // closed, a late request from that thread must never fall through to the
  // current mission pass.
  if (binding && (!boundRun || boundRun.finished || boundRun.cancelRequested))
    return null;
  const observed = server.codexAgentObserver?.get(threadId);
  const managed = codexRunForThread(current, server, threadId);
  const target =
    boundRun || managed || codexRunForObservedThread(root, server, threadId);
  if (!target) {
    // Unknown provider threads are not request-scoped to this live mission.
    // Declining keeps stale/foreign requests from becoming current-run cards.
    return null;
  }
  if (observed && !managed && !observedAgentIsActive(observed)) return null;
  const liveTurnId = server.turns?.get(threadId)?.turnId || observed?.turnId;
  if (
    params?.turnId !== undefined &&
    liveTurnId &&
    String(params.turnId) !== String(liveTurnId)
  )
    return null;
  return { target, observed, binding };
}

function publishObservedCodexAgent(root, server, observed) {
  if (!root || root.finished || !observed?.id) return;
  const managed = server.djinnAgentBindings?.get(observed.providerThreadId);
  if (managed?.rootRunId === root.runId) {
    // A provider record for a planned worker enriches the existing card;
    // the Djinn passage remains authoritative for its lifecycle.
    publishEvent(root, "agent", {
      id: managed.agentId,
      name: managed.name,
      role: managed.role,
      provider: "codex",
      providerThreadId: observed.providerThreadId,
      model: observed.model || managed.model,
      activity: observed.activity,
    });
    return;
  }
  const parentRun =
    codexRunForThread(root, server, observed.parentProviderThreadId) ||
    codexRunForThread(root, server, observed.parentThreadId);
  const previous = root.observedAgents.get(observed.id) || {};
  const parentBinding = server.djinnAgentBindings?.get(
    observed.parentProviderThreadId,
  );
  const parentObserved = root.observedAgents.get(
    `codex:${observed.parentProviderThreadId}`,
  );
  const eventData = {
    id: observed.id,
    name: observed.name || observed.role || "Sous-agent Codex",
    role: observed.role || "Sous-agent Codex",
    status: observed.status || "running",
    lifecycle: observed.lifecycle,
    summary: observed.summary || undefined,
    origin: "codex",
    live: true,
    provider: "codex",
    providerThreadId: observed.providerThreadId,
    activity: observed.activity,
    ...(observed.providerCallId
      ? { providerCallId: observed.providerCallId }
      : {}),
    parentAgentId:
      parentRun?.agentId ||
      (parentBinding?.rootRunId === root.runId
        ? parentBinding.agentId
        : parentObserved?.id),
    runId: root.runId,
    stepId: root.stepId,
    ...(observed.model ? { model: observed.model } : {}),
  };
  for (const [key, value] of Object.entries(eventData))
    if (value === undefined || value === "") delete eventData[key];
  const comparable = JSON.stringify(eventData);
  if (previous.signature === comparable) {
    if (observed.turnId && previous.turnId !== observed.turnId)
      previous.turnId = observed.turnId;
    if (!observedAgentIsActive(observed)) {
      root.observedCancellationFailed.delete(observed.id);
      root.observedCancellationTurnIds.delete(observed.id);
      settleObservedCancellation(root, observed.id);
    }
    if (root.cancelRequested || root.observedRestartBlocked)
      void interruptObservedCodexAgent(root, server, previous);
    return;
  }
  root.observedAgents.set(observed.id, {
    ...eventData,
    ...(observed.parentProviderThreadId
      ? { parentProviderThreadId: observed.parentProviderThreadId }
      : {}),
    ...(observed.turnId ? { turnId: observed.turnId } : {}),
    signature: comparable,
  });
  publishEvent(root, "agent", eventData);
  if (!observedAgentIsActive(observed)) {
    root.observedCancellationFailed.delete(observed.id);
    root.observedCancellationTurnIds.delete(observed.id);
    settleObservedCancellation(root, observed.id);
  }
  if (root.cancelRequested || root.observedRestartBlocked)
    void interruptObservedCodexAgent(
      root,
      server,
      root.observedAgents.get(observed.id),
    );
}

function observedAgentIsActive(record) {
  return (
    record?.live !== false &&
    !["done", "error", "blocked"].includes(record.status)
  );
}

function observedAgentBelongsTo(run, record) {
  if (run.kind === "lead") return true;
  return (
    record?.parentAgentId === run.agentId ||
    record?.parentProviderThreadId === run.providerRunId
  );
}

function observedCancellationNote(root, record, detail) {
  if (!record?.id || root.observedCancellationUnavailable.has(record.id))
    return;
  root.observedCancellationUnavailable.add(record.id);
  publishEvent(root, "note", {
    title: "Interruption du sous-agent Codex non confirmée",
    detail: `${record.id}: ${detail}`.slice(0, 2_000),
    severity: "warning",
  });
}

function activeTurnIdFromTurnPage(response) {
  const turns = Array.isArray(response?.data) ? response.data : [];
  const active = [...turns].reverse().find((turn) => {
    const status =
      typeof turn?.status === "string" ? turn.status : turn?.status?.type;
    return [
      "active",
      "inProgress",
      "in_progress",
      "running",
      "started",
    ].includes(status);
  });
  const turnId = active?.id || active?.turnId;
  return typeof turnId === "string" && turnId.length <= 256 ? turnId : null;
}

function observedCancellationWaitConfirmed(root, waiter) {
  return [...waiter.ids].every(
    (id) => !root.pendingObservedCancellation.has(id),
  );
}

function notifyObservedCancellationWaiters(root) {
  for (const waiter of root.observedCancellationWaiters || []) {
    if (!observedCancellationWaitConfirmed(root, waiter)) continue;
    clearTimeout(waiter.timer);
    root.observedCancellationWaiters.delete(waiter);
    waiter.resolve(
      ![...waiter.ids].some((id) => root.observedCancellationFailed.has(id)),
    );
  }
}

function waitForObservedCancellations(root, ids) {
  const pendingIds = [...new Set(ids)].filter((id) =>
    root.pendingObservedCancellation.has(id),
  );
  if (!pendingIds.length)
    return Promise.resolve(
      !ids.some((id) => root.observedCancellationFailed.has(id)),
    );
  return new Promise((resolve) => {
    const waiter = {
      ids: new Set(pendingIds),
      resolve,
      timer: null,
    };
    waiter.timer = setTimeout(() => {
      root.observedCancellationWaiters.delete(waiter);
      for (const id of waiter.ids) {
        if (!root.pendingObservedCancellation.has(id)) continue;
        root.observedCancellationFailed.add(id);
        const record = root.observedAgents?.get(id);
        if (record)
          observedCancellationNote(
            root,
            record,
            `fermeture du tour non confirmée après ${PROCESS_KILL_GRACE_MS} ms`,
          );
      }
      resolve(false);
    }, PROCESS_KILL_GRACE_MS);
    root.observedCancellationWaiters.add(waiter);
    notifyObservedCancellationWaiters(root);
  });
}

function blockObservedRestart(root, pass) {
  root.observedRestartBlocked = true;
  root.blocked = true;
  publishEvent(root, "note", {
    title: "Reprise contrôlée empêchée",
    detail:
      "Un sous-agent Codex observé n’a pas confirmé sa fermeture ; aucune nouvelle passe ne sera lancée pour éviter un chevauchement.",
    severity: "warning",
    runId: pass?.runId,
  });
  publishEvent(root, "status", {
    status: "blocked",
    phase: root.phase,
    waitingForAgents: true,
    activeAgentId: pass?.agentId || "lead",
  });
}

function settleObservedCancellation(root, id) {
  if (!root.pendingObservedCancellation?.delete(id)) return;
  notifyObservedCancellationWaiters(root);
  if (!root.pendingObservedCancellation.size && root.pendingFinish) {
    const pending = root.pendingFinish;
    root.pendingFinish = null;
    finishRun(root, pending.status, pending.details);
  }
}

async function interruptObservedCodexAgent(root, server, record) {
  if (!root || root.finished || !observedAgentIsActive(record)) return false;
  if (root.observedCancellationAttempts.has(record.id)) {
    const attemptedTurnId = root.observedCancellationTurnIds.get(record.id);
    if (!record.turnId || attemptedTurnId === record.turnId) return true;
    root.observedCancellationAttempts.delete(record.id);
  }
  root.observedCancellationAttempts.add(record.id);
  root.observedCancellationFailed.delete(record.id);
  root.pendingObservedCancellation.add(record.id);
  const threadId = record.providerThreadId;
  if (!threadId || typeof server?.request !== "function") {
    root.observedCancellationFailed.add(record.id);
    root.observedCancellationAttempts.delete(record.id);
    root.observedCancellationTurnIds.delete(record.id);
    observedCancellationNote(
      root,
      record,
      "le serveur app-server ou le threadId n’est pas disponible",
    );
    return false;
  }
  let turnId = record.turnId;
  if (!turnId) {
    try {
      const response = await server.request("thread/turns/list", {
        threadId,
        limit: MAX_OBSERVED_TURN_LOOKUP,
        sortDirection: "desc",
        itemsView: "notLoaded",
      });
      turnId = activeTurnIdFromTurnPage(response);
    } catch (error) {
      root.observedCancellationFailed.add(record.id);
      root.observedCancellationAttempts.delete(record.id);
      root.observedCancellationTurnIds.delete(record.id);
      observedCancellationNote(
        root,
        record,
        `lecture bornée du dernier tour impossible: ${error.message}`,
      );
      return false;
    }
    if (!turnId) {
      root.observedCancellationFailed.add(record.id);
      root.observedCancellationAttempts.delete(record.id);
      root.observedCancellationTurnIds.delete(record.id);
      observedCancellationNote(
        root,
        record,
        "aucun tour actif n’a été retourné par thread/turns/list",
      );
      return false;
    }
    record.turnId = turnId;
  }
  if (!observedAgentIsActive(record)) {
    settleObservedCancellation(root, record.id);
    return false;
  }
  root.observedCancellationTurnIds.set(record.id, turnId);
  try {
    await server.request("turn/interrupt", { threadId, turnId });
    return true;
  } catch (error) {
    root.observedCancellationFailed.add(record.id);
    root.observedCancellationAttempts.delete(record.id);
    root.observedCancellationTurnIds.delete(record.id);
    observedCancellationNote(
      root,
      record,
      `accusé turn/interrupt refusé: ${error.message}`,
    );
    return false;
  }
}

function interruptObservedCodexAgents(run, serverOverride = null) {
  const root = run.parent || run;
  const server = serverOverride || codexServerForCancellation(run);
  if (!server) return [];
  const ids = [];
  for (const record of root.observedAgents?.values() || []) {
    if (!observedAgentIsActive(record) || !observedAgentBelongsTo(run, record))
      continue;
    ids.push(record.id);
    void interruptObservedCodexAgent(root, server, record);
  }
  return ids;
}

function codexServerFor(run) {
  const root = run.parent || run;
  let server = codexServers.get(root.taskId);
  if (!server || server.closed) {
    const command = resolveProviderCommand(providerSpec("codex"));
    const child = spawn(command, ["app-server", "--listen", "stdio://"], {
      cwd: root.cwd,
      detached: process.platform !== "win32",
      shell: false,
      windowsHide: true,
      stdio: ["pipe", "pipe", "pipe"],
      env: childEnvironment(),
    });
    server = new CodexAppServer(child, {
      terminate: (signal) => killProcessGroup({ child }, signal === "SIGKILL"),
    });
    codexServers.set(root.taskId, server);
    server.on("diagnostic", (detail) => {
      const current = activeRuns.get(activeTaskRuns.get(root.taskId));
      if (current)
        publishEvent(current, "note", {
          title: "Diagnostic Codex",
          detail,
          severity: "warning",
          stream: "stderr",
        });
    });
    server.on("request", (method, nativeId, params) => {
      if (method === "item/tool/call") {
        const current = activeRuns.get(activeTaskRuns.get(root.taskId));
        if (!current) {
          server.respond(
            nativeId,
            nativeToolFailure(
              makeError("invalid_scope", "The mission is no longer active"),
            ),
          );
          return;
        }
        void handleNativeToolCall(root, server, nativeId, params)
          .then((response) => server.respond(nativeId, response))
          .catch((error) =>
            server.respond(nativeId, nativeToolFailure(error)),
          );
        return;
      }
      const current = activeRuns.get(activeTaskRuns.get(root.taskId));
      if (!current) {
        declineCodexRequest(server, nativeId, method);
        return;
      }
      const context = codexPermissionContextForRequest(
        root,
        server,
        current,
        params,
      );
      if (!context) {
        declineCodexRequest(server, nativeId, method);
        return;
      }
      const { target, observed, binding } = context;
      const agent = observed
        ? {
            id: observed.id,
            name: observed.name || observed.role || "Sous-agent Codex",
          }
        : binding
          ? { id: binding.agentId, name: binding.name }
          : null;
      registerPermissionRequest(
        target,
        "codex",
        method,
        nativeId,
        params,
        server,
        agent,
      );
    });
    server.on("requestCancelled", (nativeId) => {
      for (const entry of [...pendingPermissions.values()])
        if (
          entry.provider === "codex" &&
          entry.transport === server &&
          String(entry.native.id) === String(nativeId)
        )
          cancelPermissionEntry(entry, "provider_closed");
    });
    server.on("closed", () => {
      for (const entry of [...pendingPermissions.values()])
        if (entry.provider === "codex" && entry.transport === server)
          cancelPermissionEntry(entry, "provider_closed");
    });
    // CodexAppServer intentionally emits every notification, including child
    // collaboration threads which are absent from `turns`. Observe that
    // broadcast here instead of changing the transport's turn routing.
    server.on("notification", (method, params) => {
      server.codexAgentObserver?.observe(method, params);
      const current = activeRuns.get(activeTaskRuns.get(root.taskId));
      const observed = server.codexAgentObserver?.get(params.threadId);
      if (!current || !observed || !current.observedAgents.has(observed.id))
        return;
      if (
        method === "item/agentMessage/delta" ||
        (method === "item/completed" && params.item?.type === "agentMessage")
      ) {
        const messageId = textProviderId(params.itemId || params.item?.id);
        const key = `${params.threadId}:${messageId}`;
        let value =
          method === "item/agentMessage/delta"
            ? (server.observedMessages.get(key) || "") + (params.delta || "")
            : params.item.text || "";
        if (method === "item/completed")
          value = parseObservedCodexMessage(root, server, observed, value);
        server.observedMessages.set(key, value.slice(-60000));
        publishEvent(current, "text", {
          agentId: observed.id,
          scope: "agent",
          parentRunId: current.runId,
          messageId: textProviderId(`${params.threadId}:${messageId}`),
          streaming: true,
          final: method === "item/completed",
          text: value.slice(-60000),
        });
      }
    });
  }
  if (server.observerRootId !== root.runId) {
    server.observerRootId = root.runId;
    server.djinnAgentBindings ||= new Map();
    server.observedMessages = new Map();
    server.codexAgentObserver = new CodexAgentObserver({
      acceptsParent: (threadId) =>
        server.djinnAgentBindings.get(threadId)?.rootRunId === root.runId ||
        root.observedAgents.has(`codex:${threadId}`),
      onAgent: (observed) => publishObservedCodexAgent(root, server, observed),
    });
  }
  return server;
}

function textProviderId(value) {
  return typeof value === "string" ? value.slice(0, 256) : "message";
}

function parseObservedCodexMessage(root, server, observed, text) {
  const sourceRun = codexRunForObservedThread(
    root,
    server,
    observed.providerThreadId,
  );
  if (!sourceRun || sourceRun.finished) return text;
  const publicationRun = observedNativePublicationRun(root, sourceRun, observed);
  const parsed = runtime.parseProviderLine("codex", text || "");
  for (const event of parsed.filter((candidate) => candidate.type !== "text"))
    emitNormalizedProviderEvent(publicationRun, event);
  return parsed
    .filter((candidate) => candidate.type === "text")
    .map((candidate) => candidate.data?.text || "")
    .join("\n");
}

async function runCodexPass(run, input, imagePaths) {
  activeRuns.set(run.runId, run);
  const root = run.parent || run;
  let server;
  const messages = new Map();
  const lane = run.supervisor ? "supervisor" : run.agentId || "lead";
  const legacyKey = `${run.stepId || "legacy"}${input.step?.type === "discussion" ? ":routing" : ""}:${lane}`;
  // Keep one bounded provider conversation for a mission lane across workflow
  // stages. The prompt still carries only the current stage and bounded
  // summaries; native session approvals therefore retain their intended
  // session scope without granting a project-wide blanket.
  const missionKey = `${root.taskId || root.runId}:${lane}${input.step?.type === "discussion" ? ":routing" : ""}`;
  const key = (run.codexKey = `${missionKey}:${CODEX_NATIVE_SESSION_VERSION}`);
  const savedNativeThreadId = input.providerSessions?.[key];
  const migratedThreadId =
    savedNativeThreadId ||
    input.providerSessions?.[missionKey] ||
    input.providerSessions?.[legacyKey];
  const needsNativeSessionMigration =
    !savedNativeThreadId && Boolean(migratedThreadId);
  const composedPrompt = runtime.composeRunPrompt(input);
  const migrationNote =
    "\n\nNative interaction session migration: continue from the mission context, user decisions, prior summaries, and current workflow supplied above. The previous provider session is retained as history but cannot provide native Djinn tools; use the tools registered on this fresh session.";
  const prompt = needsNativeSessionMigration
    ? `${composedPrompt.slice(0, Math.max(0, runtime.MAX_COMPOSED_PROMPT_LENGTH - migrationNote.length))}${migrationNote}`
    : composedPrompt;
  publishEvent(run, "status", {
    status: "running",
    provider: "codex",
    mode: input.mode,
    activity: run.supervisor
      ? "responds"
      : run.agentId === "lead"
        ? "integrates"
        : "works",
  });
  try {
    server = run.codexServer = codexServerFor(run);
    const turn = await server.run(
      key,
      { ...input, prompt },
      (method, params) => {
        if (run.finished) return;
        if (params.threadId)
          server.djinnAgentBindings.set(params.threadId, {
            rootRunId: root.runId,
            agentId: run.agentId || "lead",
            runId: run.runId,
            name: run.agent?.name || "Chef",
            role: run.agent?.role || "Intégration",
            model: input.model,
          });
        if (method === "item/agentMessage/delta") {
          const id = String(params.itemId || "message").slice(0, 256);
          const message = messages.get(id) || { text: "", at: 0 };
          message.text = (message.text + (params.delta || "")).slice(-60000);
          messages.set(id, message);
          if (Date.now() - message.at >= 200) {
            message.at = Date.now();
            publishEvent(run, "text", {
              messageId: id,
              streaming: true,
              text: message.text.split("DJINN_EVENT:")[0],
            });
          }
        } else if (
          method === "item/completed" &&
          params.item?.type === "agentMessage"
        ) {
          const events = runtime.parseProviderLine(
            "codex",
            params.item.text || "",
          );
          const text = events
            .filter((e) => e.type === "text")
            .map((e) => e.data.text || "")
            .join("\n");
          for (const event of events.filter((e) => e.type !== "text"))
            emitNormalizedProviderEvent(run, event);
          run.outputSummary = (run.outputSummary + "\n" + text).slice(-8000);
          publishEvent(run, "text", {
            messageId: String(params.item.id || "message").slice(0, 256),
            streaming: true,
            final: true,
            text,
          });
        } else if (
          ["item/started", "item/completed"].includes(method) &&
          params.item?.type !== "agentMessage"
        ) {
          emitNormalizedProviderEvent(run, {
            type: "tool",
            data: {
              name: params.item?.type || "Codex",
              status: method === "item/started" ? "started" : "completed",
              callId: params.item?.id,
              input: params.item?.command,
              output: params.item?.aggregatedOutput,
            },
          });
        } else if (method === "error" && params.willRetry !== true)
          emitNormalizedProviderEvent(run, {
            type: "error",
            data: { message: params.error?.message || "Codex turn failed" },
          });
      },
      {
        // Resuming an old key would silently drop dynamicTools because the
        // app-server schema only accepts them on thread/start. The versioned
        // key therefore resumes only an already equipped thread; an old key
        // triggers one fresh start with the bounded context above.
        savedThreadId: savedNativeThreadId,
        imagePaths,
        dynamicTools: runtime.nativeToolDefinitions(),
      },
    );
    run.providerRunId = turn.threadId;
    root.providerSessions ||= { ...(input.providerSessions || {}) };
    root.providerSessions[key] = turn.threadId;
    publishEvent(run, "note", {
      title: "Session Codex conservée",
      providerThreadId: turn.threadId,
      sessionKey: key,
      providerSession: true,
    });
    consumeGuidance(root, input.guidance, run.agentId);
    for (const entry of root.guidance || []) {
      if (
        entry.status !== "transmitted" ||
        (entry.agentId && entry.agentId !== run.agentId) ||
        (!entry.agentId && run.agentId !== "lead")
      )
        continue;
      if ((input.guidance || []).some((g) => g.id === entry.id)) continue;
      try {
        await server.steer(key, entry.text, entry.id);
        consumeGuidance(root, [entry], run.agentId);
      } catch (error) {
        if (!server.turns.has(server.threads.get(key)))
          scheduleAgentRevisit(root, run.agentId);
        else {
          entry.delivery = "controlled_restart";
          requestPassRestart(run);
          publishEvent(root, "guidance", entry);
          publishEvent(run, "note", {
            title: "Transmission directe indisponible — reprise contrôlée",
            detail: error.message,
          });
        }
        break;
      }
    }
    if (
      run.cancelRequested ||
      root.cancelRequested ||
      run.questionStopRequested ||
      run.restartRequested
    )
      await server.interrupt(key);
    const result = await turn.completed;
    if (
      run.restartRequested &&
      !run.cancelRequested &&
      !root.cancelRequested &&
      run.observedRestartWait &&
      !(await run.observedRestartWait)
    ) {
      blockObservedRestart(root, run);
      finishRun(run, "error", {
        message: "Observed Codex child interruption was not confirmed.",
      });
      return;
    }
    finishRun(
      run,
      run.cancelRequested || root.cancelRequested
        ? "cancelled"
        : run.questionStopRequested
          ? "completed"
          : result.status === "completed"
            ? "completed"
            : result.status === "interrupted" && run.restartRequested
              ? "completed"
              : "error",
      { message: result.error?.message },
    );
  } catch (error) {
    if (root.cancelRequested) {
      await forceCancelledCodexShutdown(root, error.message);
      return;
    }
    if (run.restartRequested) {
      if (run.observedRestartWait) await run.observedRestartWait;
      blockObservedRestart(root, run);
      // The original turn may still be alive. Keep the passage and directory
      // reservation until explicit cancellation or its confirmed completion.
      publishEvent(run, "note", {
        title: "Reprise empêchée",
        detail: error.message,
      });
      return;
    }
    finishRun(run, root.cancelRequested ? "cancelled" : "error", {
      message: error.message,
    });
  }
}

async function supervise(parent, input, humanEntry) {
  // Supervision uses its own persistent read-only thread while the writer works.
  if (parent.finished || parent.cancelRequested || parent.hasBlockingQuestion)
    return;
  if (parent.provider !== "codex" && parent.provider !== "claude") return;
  if (parent.supervisorPass && !parent.supervisorPass.finished) {
    const pass = parent.supervisorPass;
    if (humanEntry && pass.codexServer) {
      try {
        await pass.codexServer.steer(
          pass.codexKey,
          humanEntry.text,
          humanEntry.id,
        );
        consumeGuidance(parent, [humanEntry], "lead");
      } catch {
        parent.pendingSupervisorMessages ||= [];
        parent.pendingSupervisorMessages.push(humanEntry);
      }
    } else if (humanEntry) {
      parent.pendingSupervisorMessages ||= [];
      parent.pendingSupervisorMessages.push(humanEntry);
      requestPassRestart(pass);
    }
    return;
  }
  const passInput = {
    ...input,
    mode: "plan",
    step: undefined,
    agents: [],
    images: [],
    guidance: humanEntry ? [humanEntry] : [],
    prompt: `You are Djinn, the mission lead supervising bounded workers. Speak French. Read-only supervision: do not edit files, launch agents, approve results, or change workflow stages. Respond promptly to the human, confirm understanding and your next action. To forward an un-targeted human instruction to an existing worker, emit a note with guidanceId matching the received instruction and forwardToAgentId matching that existing worker. The native runtime forwards the original human text; you cannot launch another agent or change its scope. Use only observed native events below; distinguish silence from a blocker. Workers keep ownership of their files. Existing mission context:\n${input.prompt.slice(0, 60000)}\nObserved activity:\n${JSON.stringify(parent.workerSummaries || []).slice(-12000)}\nCurrently active agents:\n${JSON.stringify([...parent.children].filter((p) => !p.finished && !p.supervisor).map((p) => ({ id: p.agentId, task: p.agent?.role, latestOutput: p.outputSummary.slice(-1500) }))).slice(0, 12000)}\n${humanEntry ? `Human message (${humanEntry.id}): ${humanEntry.text}` : "Introduce your supervision briefly, then remain available for later messages. Do not invent worker progress."}`,
  };
  const pass = createRun(passInput, "lead-pass", parent.runId, {
    id: "lead",
    name: "Djinn",
    role: "Supervision et dialogue",
  });
  pass.parent = parent;
  pass.supervisor = true;
  parent.supervisorPass = pass;
  parent.children.add(pass);
  publishEvent(parent, "status", {
    status: "running",
    phase: "workers",
    waitingForAgents: true,
    leadActivity: "responds",
  });
  if (parent.provider === "codex") await runCodexPass(pass, passInput, []);
  else {
    const invocation = runtime.buildProviderInvocation(passInput, {
      nativePermissions: true,
    });
    const separator = invocation.args.indexOf("--");
    if (!invocation.args.includes("--tools"))
      invocation.args.splice(
        separator < 0 ? invocation.args.length : separator,
        0,
        "--tools",
        "Read,Glob,Grep",
      );
    spawnProviderRun(pass, invocation);
    if (pass.child && !pass.finished)
      consumeGuidance(parent, passInput.guidance, "lead");
    await pass.completion;
  }
  if (
    parent.pendingSupervisorMessages?.length &&
    !parent.finished &&
    !parent.cancelRequested &&
    !parent.hasBlockingQuestion
  ) {
    const queued = parent.pendingSupervisorMessages.splice(0);
    for (const entry of queued) {
      if (
        parent.finished ||
        parent.cancelRequested ||
        parent.hasBlockingQuestion
      )
        break;
      await supervise(parent, input, entry);
    }
  }
}

function spawnProviderRun(run, invocation) {
  if (run.finished || run.cancelRequested || run.questionStopRequested) {
    if (!run.finished) {
      finishRun(run, run.cancelRequested ? "cancelled" : "completed", {
        message: run.cancelRequested
          ? "Run cancelled before provider start."
          : "Run stopped while waiting for an answer.",
      });
    }
    return;
  }
  activeRuns.set(run.runId, run);
  publishEvent(run, "status", {
    status: "running",
    provider: run.provider,
    mode: run.mode,
  });
  let child;
  try {
    const spec = providerSpec(run.provider);
    const command = resolveProviderCommand(spec);
    child = spawn(command, invocation.args, {
      cwd: invocation.cwd,
      shell: false,
      detached: process.platform !== "win32",
      windowsHide: true,
      stdio: invocation.stdinText
        ? ["pipe", "pipe", "pipe"]
        : ["ignore", "pipe", "pipe"],
      env: childEnvironment(),
    });
    if (invocation.stdinText && child.stdin) {
      run.stdinOpen = Boolean(invocation.keepStdinOpen);
      child.stdin.once("error", (error) => {
        if (!run.finished)
          publishEvent(run, "error", {
            message: `Provider image input failed: ${error.message}`,
          });
      });
      child.stdin.write(invocation.stdinText);
      // Native Claude permission control requests arrive while the process
      // is alive and require a response on this same stdin stream. Image-only
      // discovery invocations retain the historical one-shot close.
      if (!invocation.keepStdinOpen) {
        child.stdin.end();
        run.stdinOpen = false;
      }
    }
  } catch (error) {
    finishRun(run, "error", { message: error.message });
    return;
  }
  wireProcess(run, child);
  if (run.restartRequested) requestPassRestart(run);
}

async function ensureDirectory(directory) {
  try {
    const stats = await fsp.stat(directory);
    if (!stats.isDirectory())
      throw makeError("invalid_path", "cwd is not a directory");
  } catch (error) {
    if (error?.code === "ENOENT")
      throw makeError("invalid_path", "cwd does not exist");
    throw error;
  }
}

const IMAGE_EXTENSIONS = Object.freeze({
  "image/png": ".png",
  "image/jpeg": ".jpg",
  "image/webp": ".webp",
});

async function materializeRunImages(run, input) {
  if (!input.images?.length) return [];
  const runsRoot = path.join(app.getPath("userData"), "runs");
  const runRoot = path.join(runsRoot, run.runId);
  const imageDir = path.join(runRoot, "images");
  await fsp.mkdir(imageDir, { recursive: true, mode: 0o700 });
  const imagePaths = [];
  try {
    for (const [index, image] of input.images.entries()) {
      const baseName =
        (image.id || `image-${index + 1}`)
          .replace(/[^a-zA-Z0-9._-]/g, "_")
          .replace(/^\.+$/, "image") || `image-${index + 1}`;
      const extension = IMAGE_EXTENSIONS[image.mediaType];
      if (!extension)
        throw makeError("invalid_image", "Unsupported image type");
      const filePath = path.join(
        imageDir,
        `${String(index + 1).padStart(2, "0")}-${baseName}${extension}`,
      );
      await fsp.writeFile(filePath, Buffer.from(image.base64, "base64"), {
        mode: 0o600,
        flag: "wx",
      });
      imagePaths.push(filePath);
    }
  } catch (error) {
    await fsp
      .rm(runRoot, { recursive: true, force: true })
      .catch(() => undefined);
    throw error;
  }
  run.imageDir = runRoot;
  return imagePaths;
}

async function cleanupRunImages(run) {
  if (!run?.imageDir) return;
  const imageDir = run.imageDir;
  run.imageDir = null;
  await fsp
    .rm(imageDir, { recursive: true, force: true })
    .catch(() => undefined);
}

function emitAgentStatus(parent, agent, status, extra = {}) {
  const git = parent.executionPlan?.git || {};
  const eventData = {
    id: agent.id,
    name: agent.name,
    role: agent.role,
    status,
    writeScope: agent.writeScope,
    dependsOn: agent.dependsOn,
    readOnly: agent.readOnly,
    isolation: agent.isolation,
    resources: agent.resources,
    prompt: agent.prompt,
    ...extra,
  };
  const branch = extra.branch ?? git.branch;
  const worktree = extra.worktree ?? git.worktree;
  if (branch) eventData.branch = branch;
  if (worktree) eventData.worktree = worktree;
  publishEvent(parent, "agent", eventData);
}

/**
 * A lead may discover an independent worker while integrating. The proposal
 * is still validated and scheduled by the native queue; provider output never
 * gets to spawn a process by itself. Plan discoveries remain readers; review
 * and execute discoveries retain the explicit readOnly request so a narrowly
 * scoped correction can use the native approval bridge.
 */
function queueProposedAgent(run, data) {
  const root = run?.parent || run;
  const mode = root?.validatedInput?.mode || run?.mode;
  if (
    !root ||
    (run?.agentId && run.agentId !== "lead") ||
    run?.supervisor ||
    run?.kind !== "lead-pass" ||
    !["plan", "execute", "review"].includes(mode)
  )
    return false;
  // Informational lifecycle records from lead tools are not spawn proposals.
  if (!data?.prompt?.trim()) return false;
  try {
    const candidate = runtime.validateRunAgent(
      {
        ...data,
        // Plan is read-only. Review may make an explicitly requested local
        // correction, so preserve the proposal's readOnly setting there and
        // let the native provider approval card gate the write.
        readOnly: mode === "plan" ? true : data.readOnly,
        status: undefined,
      },
      root.agentDefinitions.length,
    );
    if (candidate.id === "lead")
      throw makeError("invalid_agent", "lead is reserved for the native chief");
    if (root.agentDefinitions.some((agent) => agent.id === candidate.id))
      throw makeError(
        "invalid_agent",
        `Agent ${candidate.id} is already known`,
      );
    const prepared = scheduler.prepareAgents(root.cwd, [candidate])[0];
    scheduler.validateDependencies([...root.agentDefinitions, prepared]);
    root.agentDefinitions.push(prepared);
    root.pendingAgents.add(prepared.id);
    emitAgentStatus(root, prepared, "queued", {
      lifecycle: "agent_queued",
      proposed: true,
      readOnly: mode === "plan" ? true : prepared.readOnly,
      waitReason: "Proposition indépendante reçue; vérification des ressources",
      waitingForAgentIds: prepared.dependsOn || [],
    });
    root.queueWake?.();
    // Once the initial queue has drained there is no waiter to wake. Start a
    // serialized targeted drain now so plan/review readers belong to this
    // passage, while the lead can continue its own read-only synthesis.
    root.revisitLead = true;
    if (
      !root.queueWake &&
      root.validatedInput &&
      root.workerSummaries &&
      !root.targetedAgentDrain
    ) {
      void drainTargetedAgents(
        root,
        root.validatedInput,
        root.workerSummaries,
      ).catch((error) =>
        publishEvent(root, "note", {
          title: "Démarrage du lecteur empêché",
          detail: String(error?.message || error).slice(0, 2_000),
          proposed: true,
          severity: "warning",
        }),
      );
    }
    return true;
  } catch (error) {
    publishEvent(run, "note", {
      title: "Proposition de sous-agent refusée",
      detail: String(error?.message || error).slice(0, 2_000),
      proposed: true,
      severity: "warning",
    });
    return false;
  }
}

function guidanceForPrompt(parent, agentId = null) {
  if (!parent.guidance?.length) return undefined;
  const isLead = agentId === null || agentId === undefined;
  let budget = 16000;
  return parent.guidance
    .filter(
      (entry) =>
        entry.status !== "prevented" &&
        (!entry.agentId || isLead || entry.agentId === agentId),
    )
    .slice(-32)
    .reverse()
    .filter((entry) => {
      if (entry.text.length > budget) return false;
      budget -= entry.text.length;
      return true;
    })
    .reverse()
    .map((entry) => ({
      id: entry.id,
      text: entry.text,
      ...(entry.agentId ? { agentId: entry.agentId } : {}),
    }));
}

function workerSummaryForPrompt(summary) {
  const value = String(summary || "");
  if (value.length <= MAX_WORKER_SUMMARY_FOR_PROMPT) return value;
  return `${value.slice(0, MAX_WORKER_SUMMARY_FOR_PROMPT - 80)}\n[Résumé tronqué par Djinn.]`;
}

function consumeGuidance(parent, guidance, consumerAgentId = null) {
  if (!guidance?.length) return;
  const now = new Date().toISOString();
  const consumed = new Map(guidance.map((entry) => [entry.id, entry]));
  for (const entry of parent.guidance || []) {
    const promptEntry = consumed.get(entry.id);
    if (
      !promptEntry ||
      entry.status === "consumed" ||
      entry.status === "prevented"
    )
      continue;
    // Targeted messages remain queued until their own agent receives them.
    // The lead sees them as context without marking another agent as recipient.
    if (entry.agentId && entry.agentId !== (consumerAgentId || "lead"))
      continue;
    entry.status = "consumed";
    entry.appliedAt = now;
    publishEvent(parent, "guidance", {
      id: entry.id,
      text: entry.text,
      status: "consumed",
      appliedAt: now,
      delivery: entry.delivery,
      ...(entry.agentId ? { agentId: entry.agentId } : {}),
    });
  }
}

async function runAgentPass(parent, validatedInput, agent) {
  const childInput = {
    ...validatedInput,
    cwd: agent.isolation === "worktree" ? agent.worktree : validatedInput.cwd,
    prompt: `You are a delegated Djinn agent named ${agent.name}. Role: ${agent.role}.\nOwnership: ${agent.readOnly ? "Read-only inspection" : agent.writeScope?.length ? agent.writeScope.join(", ") : "Exclusive project writer"}. Do not write outside your ownership. You are not alone in this repository: preserve others' edits and coordinate shared contracts through the chief. Report blocked dependencies explicitly; perform independent work within your scope first.\n\n${agent.prompt}`,
    mode: agent.readOnly ? "plan" : validatedInput.mode,
    step: agent.readOnly ? undefined : validatedInput.step,
    writableRoots: agent.writePaths,
    agents: [],
    workerSummaries: undefined,
    guidance: guidanceForPrompt(parent, agent.id),
  };
  const child = createRun(childInput, "agent", parent.runId, agent);
  runScopes.set(child.runId, { stepId: parent.stepId, runId: child.runId });
  child.parent = parent;
  child.mode = childInput.mode;
  child.branch = parent.executionPlan?.git?.branch || null;
  child.worktree =
    agent.isolation === "worktree"
      ? agent.worktree
      : parent.executionPlan?.git?.worktree || null;
  const previousChild = parent.currentChild;
  parent.children.add(child);
  parent.currentChild = child;
  parent.phase = "workers";
  publishEvent(parent, "status", {
    status: "running",
    phase: "workers",
    waitingForAgents: true,
    activeAgentId: agent.id,
    activeTask: agent.role,
  });
  emitAgentStatus(parent, agent, "running", {
    runId: child.runId,
    lifecycle: "agent_started",
    waitReason: "",
    waitingForAgentIds: [],
  });
  let completionResult = { status: "error" };
  try {
    const imagePaths = await materializeRunImages(child, childInput);
    const invocation = runtime.buildProviderInvocation(childInput, {
      imagePaths,
      nativePermissions: true,
    });
    if (parent.cancelRequested) child.cancelRequested = true;
    if (child.provider === "codex")
      void runCodexPass(child, childInput, imagePaths);
    else spawnProviderRun(child, invocation);
    if (child.child && !child.finished)
      consumeGuidance(parent, childInput.guidance, agent.id);
    completionResult = await child.completion;
  } catch (error) {
    if (!child.finished) finishRun(child, "error", { message: error.message });
    completionResult = await child.completion;
  } finally {
    if (parent.currentChild === child) parent.currentChild = previousChild;
  }
  return {
    id: agent.id,
    name: agent.name,
    status: child.blocked
      ? "blocked"
      : completionResult.status ||
        (child.cancelRequested ? "cancelled" : "error"),
    runId: child.runId,
    branch: child.branch,
    worktree: child.worktree,
    interrupted: completionResult.status === "interrupted",
    summary:
      child.outputSummary ||
      (child.cancelRequested ? "Cancelled" : "No report emitted."),
  };
}

async function runAgent(parent, input, agent) {
  let result;
  const priorSummaries = [...(input.priorSummaries || [])];
  do {
    result = await runAgentPass(parent, { ...input, priorSummaries }, agent);
    if (result.interrupted)
      priorSummaries.push(
        `Interrupted ${agent.name} pass: ${result.summary}`.slice(-8000),
      );
  } while (
    result.interrupted &&
    !parent.observedRestartBlocked &&
    !parent.cancelRequested &&
    !parent.hasBlockingQuestion
  );
  return result;
}

async function drainTargetedAgents(parent, input, summaries) {
  if (parent.targetedAgentDrain) return parent.targetedAgentDrain;
  if (
    !parent.pendingAgents.size ||
    parent.cancelRequested ||
    parent.hasBlockingQuestion
  )
    return;
  const drain = (async () => {
    const results = await runAgentQueue(
      parent,
      { ...input, agents: [] },
      summaries,
    );
    summaries.push(...results);
    return results;
  })();
  parent.targetedAgentDrain = drain;
  try {
    return await drain;
  } finally {
    if (parent.targetedAgentDrain === drain) parent.targetedAgentDrain = null;
  }
}

async function awaitTargetedAgentDrain(parent) {
  const drain = parent?.targetedAgentDrain;
  if (drain) {
    try {
      await drain;
    } catch {
      // The parent completion carries the provider/queue error. A cancelled
      // reader must still close before the project reservation is released.
    }
  }
  const activeChildren = [...(parent?.children || [])].filter(
    (child) =>
      !child.finished &&
      !child.supervisor &&
      (child.agent?.readOnly || child.mode !== "execute"),
  );
  if (activeChildren.length)
    await Promise.all(activeChildren.map((child) => child.completion));
}

async function runAgentQueue(parent, validatedInput, priorResults = []) {
  const summaries = [],
    completed = new Map(priorResults.map((result) => [result.id, result])),
    active = new Map();
  const concurrency = Math.min(
    validatedInput.concurrency || 1,
    runtime.MAX_CONCURRENCY,
  );
  const pending = [...validatedInput.agents];
  const lastReasons = new Map();
  function waiting(agent, reason) {
    const key = JSON.stringify(reason);
    if (lastReasons.get(agent.id) === key) return;
    lastReasons.set(agent.id, key);
    const names = reason.agents
      .map((id) => parent.agentDefinitions.find((a) => a.id === id)?.name || id)
      .join(", ");
    const messages = {
      dependency: `Attend le résultat de ${names}`,
      dependency_failed: `Prérequis non terminé : ${names}`,
      ownership_conflict: `Périmètre partagé avec ${names}`,
      concurrency_limit: `Limite de ${concurrency} agents actifs atteinte`,
      resource_capacity: `Capacité de ressource atteinte${reason.resource ? ` (${reason.resource})` : ""}`,
      resource_unavailable: `Ressource indisponible : ${(reason.resources || []).join(", ") || "capacité du système"}`,
      blocking_answers_required: "Attend vos réponses bloquantes",
      cancelled: "Mission interrompue avant le démarrage",
    };
    emitAgentStatus(
      parent,
      agent,
      reason.kind === "dependency_failed" ? "blocked" : "queued",
      {
        lifecycle: "agent_queued",
        waitReason: messages[reason.kind],
        waitKind: reason.kind,
        waitingForAgentIds: reason.agents,
        summary: messages[reason.kind],
      },
    );
  }
  while (pending.length || active.size || parent.pendingAgents.size) {
    for (const id of [...parent.pendingAgents]) {
      if (active.has(id) || pending.some((a) => a.id === id)) continue;
      const agent = parent.agentDefinitions.find((a) => a.id === id);
      parent.pendingAgents.delete(id);
      if (agent) {
        pending.push(agent);
        completed.delete(id);
      }
    }
    if (!parent.cancelRequested && !parent.hasBlockingQuestion) {
      for (const agent of [...pending]) {
        const reason = scheduler.reasonFor(
          agent,
          [...active.values()].map((e) => e.agent),
          completed,
          concurrency,
          validatedInput.mode,
          parent.executionPlan?.capacity || undefined,
        );
        if (reason) {
          waiting(agent, reason);
          if (reason.kind === "dependency_failed") {
            pending.splice(pending.indexOf(agent), 1);
            const result = {
              id: agent.id,
              name: agent.name,
              status: "blocked",
              summary: `Prerequisite failed: ${reason.agents.join(", ")}`,
            };
            completed.set(agent.id, result);
            summaries.push(result);
          }
          continue;
        }
        pending.splice(pending.indexOf(agent), 1);
        parent.pendingAgents.delete(agent.id);
        const work = { agent };
        active.set(agent.id, work);
        const dependencySummaries = (agent.dependsOn || [])
          .map((id) => completed.get(id))
          .filter(Boolean)
          .map((summary) =>
            workerSummaryForPrompt(
              `${summary.name || summary.id} (${summary.status}): ${summary.summary || "Aucun compte rendu."}`,
            ),
          );
        const workerInput = {
          ...validatedInput,
          priorSummaries: [
            ...(validatedInput.priorSummaries || []),
            ...priorResults
              .filter((result) => result.id === agent.id)
              .slice(-1)
              .map((result) =>
                workerSummaryForPrompt(
                  `${result.name} (${result.status}): ${result.summary}`,
                ),
              ),
            ...dependencySummaries,
          ],
        };
        work.promise = runAgent(parent, workerInput, agent).then(
          (result) => ({ id: agent.id, result }),
          (error) => ({
            id: agent.id,
            result: {
              id: agent.id,
              name: agent.name,
              status: "error",
              summary: error.message,
            },
          }),
        );
      }
    }
    if (!active.size) {
      if (!pending.length) break;
      const kind = parent.cancelRequested
        ? "cancelled"
        : parent.hasBlockingQuestion
          ? "blocking_answers_required"
          : "dependency_failed";
      for (const agent of pending)
        waiting(agent, { kind, agents: agent.dependsOn || [] });
      break;
    }
    const wake = new Promise((resolve) => {
      parent.queueWake = () => resolve({ wake: true });
    });
    const outcome = await Promise.race(
      [...active.values()].map((e) => e.promise).concat(wake),
    );
    parent.queueWake = null;
    if (outcome.wake) continue;
    const { id, result } = outcome;
    active.delete(id);
    completed.set(id, result);
    summaries.push(result);
    // Freed slots start independent work immediately; no wave-wide barrier.
  }
  return summaries;
}

async function runPipeline(parent, validatedInput) {
  try {
    parent.validatedInput = validatedInput;
    // Keep the lead reachable through steerRun. A provider turn is needed
    // only when the human addresses it or the workers need integration.
    const workerSummaries = await runAgentQueue(parent, validatedInput);
    if (parent.supervisorPass && !parent.supervisorPass.finished)
      await parent.supervisorPass.completion;
    parent.workerSummaries = workerSummaries;
    if (parent.observedRestartBlocked) return;
    if (parent.finished) return;
    if (parent.cancelRequested) {
      finishRun(parent, "cancelled", {
        message: "Run cancelled before integration.",
      });
      return;
    }
    if (parent.hasBlockingQuestion) {
      finishRun(parent, "completed", {
        message: "Run is waiting for an answer before integration.",
      });
      return;
    }
    await drainTargetedAgents(parent, validatedInput, workerSummaries);
    let result;
    const leadSummaries = [];
    do {
      await drainTargetedAgents(parent, validatedInput, workerSummaries);
      if (parent.cancelRequested || parent.hasBlockingQuestion) break;
      parent.revisitLead = false;
      parent.phase = "lead";
      publishEvent(parent, "status", {
        status: "running",
        phase: "lead",
        waitingForAgents: false,
        activeAgentId: "lead",
        activeTask: validatedInput.step?.objective || "Intégration",
      });
      const leadInput = {
        ...validatedInput,
        agents: [],
        workerSummaries: workerSummaries.map((worker) =>
          workerSummaryForPrompt(
            `${worker.name} (${worker.status}): ${worker.summary}`,
          ),
        ),
        priorSummaries: [
          ...(validatedInput.priorSummaries || []),
          ...leadSummaries,
        ],
        guidance: guidanceForPrompt(parent),
      };
      const pass = createRun(leadInput, "lead-pass", parent.runId, {
        id: "lead",
        name: "Chef",
        role: validatedInput.step?.objective || "Intégration",
      });
      pass.parent = parent;
      parent.children.add(pass);
      parent.currentChild = pass;
      runScopes.set(pass.runId, { stepId: parent.stepId, runId: pass.runId });
      const imagePaths = await materializeRunImages(pass, leadInput);
      const invocation = runtime.buildProviderInvocation(leadInput, {
        imagePaths,
        nativePermissions: true,
      });
      if (parent.cancelRequested) pass.cancelRequested = true;
      if (pass.provider === "codex")
        void runCodexPass(pass, leadInput, imagePaths);
      else spawnProviderRun(pass, invocation);
      if (pass.child && !pass.finished)
        consumeGuidance(parent, leadInput.guidance);
      result = await pass.completion;
      parent.stepResult =
        result.status === "completed" ? pass.stepResult : undefined;
      if (parent.observedRestartBlocked) return;
      parent.outputSummary = pass.outputSummary;
      parent.providerError ||= pass.providerError;
      // A proposal can arrive while the lead turn is still active. Await its
      // current-pass drain before deciding whether integration is complete;
      // this also preserves the real close/interrupt boundary on cancellation
      // and blocking questions.
      if (
        parent.pendingAgents.size &&
        !parent.cancelRequested &&
        !parent.hasBlockingQuestion
      )
        await drainTargetedAgents(parent, validatedInput, workerSummaries);
      await awaitTargetedAgentDrain(parent);
      if (result.status === "interrupted")
        leadSummaries.push(
          `Interrupted lead pass: ${pass.outputSummary || "Preserve the existing files and continue."}`.slice(
            -8000,
          ),
        );
    } while (
      (result?.status === "interrupted" ||
        parent.revisitLead ||
        parent.pendingAgents.size > 0) &&
      !parent.cancelRequested &&
      !parent.hasBlockingQuestion
    );
    parent.workerSummaries = workerSummaries;
    await awaitTargetedAgentDrain(parent);
    finishRun(
      parent,
      parent.cancelRequested
        ? "cancelled"
        : parent.hasBlockingQuestion
          ? "completed"
          : result?.status || "completed",
      result || {},
    );
  } catch (error) {
    if (!parent.finished) {
      if (parent.observedRestartBlocked) return;
      const unfinished = [...parent.children].filter(
        (child) => !child.finished,
      );
      for (const child of unfinished) cancelOneRun(child);
      await Promise.all(unfinished.map((child) => child.completion));
      await awaitTargetedAgentDrain(parent);
      finishRun(parent, parent.cancelRequested ? "cancelled" : "error", {
        message: error.message,
        phase: parent.phase || "workers",
      });
    }
  }
}

async function startRun(input) {
  let validated = runtime.validateRunInput(input);
  if (validated.agents.some((agent) => agent.id === "lead"))
    throw makeError("invalid_agent", "lead is reserved for the native lead");
  if (activeTaskRuns.has(validated.taskId)) {
    throw makeError(
      "run_active",
      `Task ${validated.taskId} already has an active run`,
    );
  }
  const state = await loadState();
  const task = state?.tasks.find((t) => t.id === validated.taskId);
  if (task) validated = runtime.bindRunToTask(validated, task);
  else if (validated.stepId)
    throw makeError(
      "invalid_step",
      "Save the mission before starting a workflow stage",
    );
  if (validated.projectSnapshot)
    runtime.validateProjectDirectory(validated.projectSnapshot);
  await ensureDirectory(validated.cwd);
  const cwdKey = await fsp.realpath(validated.cwd);
  const directoryOwned = () =>
    [...activeCwdRuns.keys()].some((cwd) =>
      scheduler.pathsOverlap(cwd, cwdKey),
    );
  if (directoryOwned()) {
    throw makeError(
      "run_active",
      `Project directory ${cwdKey} already has an active run`,
    );
  }
  // Recheck after asynchronous state/path validation: concurrent IPC starts must not race.
  if (activeTaskRuns.has(validated.taskId) || directoryOwned())
    throw makeError(
      "run_active",
      "A run already owns this mission or directory",
    );
  validated.agents = scheduler.prepareAgents(cwdKey, validated.agents);
  let contextWarning;
  if (task) {
    try {
      const contextIndex = await writeMissionContext(
        task,
        app.getPath("userData"),
      );
      validated.prompt = `Full saved mission context is available on demand at ${contextIndex}. Read this index and the relevant full text supports when an excerpt is insufficient, especially before assessing exit criteria. Preserve current human instructions and acquired decisions.\n\n${validated.prompt}`;
    } catch (error) {
      contextWarning = error.message;
    }
    // Archiving adds an asynchronous boundary: preserve exclusive ownership.
    if (activeTaskRuns.has(validated.taskId) || directoryOwned())
      throw makeError(
        "run_active",
        "A run already owns this mission or directory",
      );
  }
  const run = createRun(validated, "lead");
  runScopes.set(run.runId, { stepId: run.stepId, runId: run.runId });
  run.cwd = cwdKey;
  run.mode = validated.mode;
  run.phase = validated.agents.length > 0 ? "workers" : "lead";
  run.executionPlan = runtime.buildExecutionPlan(validated, {
    git: detectGitContext(cwdKey),
    preparedAgents: validated.agents,
    capacity:
      validated.resourcePolicy?.capacity ||
      (validated.resourcePolicy?.mode === "adaptive"
        ? scheduler.systemCapacity()
        : undefined),
  });
  activeRuns.set(run.runId, run);
  activeTaskRuns.set(validated.taskId, run.runId);
  activeCwdRuns.set(cwdKey, run.runId);
  if (contextWarning)
    publishEvent(run, "note", {
      title: "Supports complets indisponibles",
      detail: `${contextWarning}. Les extraits restent disponibles ; signalez tout critère qui nécessite un document complet.`,
    });
  publishEvent(run, "status", {
    status: "running",
    provider: run.provider,
    phase: run.phase,
    waitingForAgents: run.phase === "workers",
    activeAgentId: run.phase === "lead" ? "lead" : undefined,
    executionPlan: run.executionPlan,
  });
  // Validated lead proposals use the same ownership/dependency scheduler.
  void runPipeline(run, validated);
  return { runId: run.runId };
}

function getRuntimeSnapshot() {
  // Only live native ownership is evidence. A stored state/run ID never
  // starts a provider or makes an external Codex conversation appear active.
  return {
    capturedAt: new Date().toISOString(),
    notificationClicks: [...pendingNotificationClicks.values()],
    permissions: getPendingPermissions(),
    runs: [...activeTaskRuns.values()]
      .map((id) => activeRuns.get(id))
      .filter((run) => run && !run.finished)
      .map((run) => ({
        taskId: run.taskId,
        runId: run.runId,
        stepId: run.stepId,
        mode: run.mode,
        startedAt: run.startedAt,
        status: run.cancelRequested ? "stopping" : "running",
        phase: run.phase,
        lastActivityAt: run.lastActivityAt,
        activeAgents: [...run.children]
          .filter((pass) => !pass.finished)
          .map((pass) => ({
            id: pass.agentId,
            name: pass.agent?.name || "Chef",
            task: pass.agent?.role || "Intégration",
            runId: pass.runId,
            readOnly: Boolean(
              pass.supervisor ||
              pass.agent?.readOnly === true ||
              pass.mode === "plan",
            ),
          })),
        observedAgents: [...(run.observedAgents?.values() || [])].map(
          ({ signature, parentProviderThreadId, turnId, ...agent }) => agent,
        ),
        structuredEvents: structuredProjectionForTask(run.taskId),
        events: [...run.journal],
      })),
  };
}

async function cancelRun(runId) {
  if (typeof runId !== "string" || runId.length > 256) {
    return { cancelled: false, runId: null, reason: "invalid_run_id" };
  }
  const run = activeRuns.get(runId);
  if (run) {
    if (run.parent) run.parent.cancelRequested = true;
    let cancelled = cancelOneRun(run);
    for (const child of [...activeRuns.values()].filter(
      (candidate) => candidate.parentRunId === runId,
    )) {
      cancelled = cancelOneRun(child) || cancelled;
    }
    return { cancelled, runId };
  }
  const children = [...activeRuns.values()].filter(
    (candidate) => candidate.parentRunId === runId,
  );
  let cancelled = false;
  for (const child of children) {
    if (child.parent) child.parent.cancelRequested = true;
    cancelled = cancelOneRun(child) || cancelled;
  }
  return { cancelled, runId, reason: cancelled ? undefined : "not_found" };
}

function requestPassRestart(pass) {
  if (
    !pass ||
    pass.finished ||
    pass.cancelRequested ||
    pass.questionStopRequested ||
    pass.restartRequested
  )
    return false;
  pass.restartRequested = true;
  const root = pass.parent || pass;
  const server = pass.codexServer || codexServerForCancellation(pass);
  const observedIds = interruptObservedCodexAgents(pass, server);
  pass.observedRestartWait = waitForObservedCancellations(root, observedIds);
  if (pass.codexServer) {
    void pass.codexServer.interrupt(pass.codexKey).catch((error) => {
      blockObservedRestart(root, pass);
      publishEvent(pass, "note", {
        title: "Reprise empêchée",
        detail: error.message,
      });
    });
    return true;
  }
  if (!pass.child || pass.killTimer) return true;
  killProcessGroup(pass);
  pass.killTimer = setTimeout(() => {
    // Wait for close before releasing the writer or spawning its replacement.
    killProcessGroup(pass, true);
    pass.killTimer = null;
  }, PROCESS_KILL_GRACE_MS);
  return true;
}

function scheduleAgentRevisit(run, agentId) {
  if (!agentId || agentId === "lead") {
    run.revisitLead = true;
    return;
  }
  run.pendingAgents.add(agentId);
  // Integration must relinquish the writer before a finished worker resumes.
  // The parent keeps the directory reservation throughout this reprise.
  for (const child of run.children)
    if (!child.finished && !child.supervisor && child.agentId === "lead")
      requestPassRestart(child);
}

async function steerRun(input) {
  const validated = runtime.validateSteerInput(input);
  let run = activeRuns.get(validated.runId);
  if (run?.parent) run = run.parent;
  if (!run || run.kind !== "lead" || run.finished) {
    throw makeError("run_inactive", "The requested run is no longer active");
  }
  if (run.guidance.some((entry) => entry.id === validated.id)) {
    throw makeError(
      "duplicate_guidance",
      `Guidance ${validated.id} is already queued`,
    );
  }
  // Already acknowledged history does not consume the live delivery queue.
  if (run.guidance.length >= runtime.MAX_GUIDANCE)
    run.guidance = run.guidance.filter(
      (entry) => !["consumed", "prevented"].includes(entry.status),
    );
  if (run.guidance.length >= runtime.MAX_GUIDANCE) {
    throw makeError(
      "too_many_guidance",
      `At most ${runtime.MAX_GUIDANCE} guidance entries may be queued`,
    );
  }
  const target = validated.agentId;
  const knownTarget =
    !target ||
    target === "lead" ||
    run.agentDefinitions.some((agent) => agent.id === target);
  const reason = !knownTarget
    ? "unknown_agent"
    : run.cancelRequested
      ? "run_cancelled"
      : run.hasBlockingQuestion
        ? "blocking_answers_required"
        : undefined;
  const entry = {
    ...validated,
    status: reason ? "prevented" : "transmitted",
    queuedAt: new Date().toISOString(),
    ...(reason ? { reason } : { delivery: "controlled_restart" }),
  };
  run.guidance.push(entry);
  publishEvent(run, "guidance", entry);
  if (reason) return { id: entry.id, status: entry.status, reason };

  const active = [...run.children].filter(
    (child) => !child.finished && !child.supervisor,
  );
  const recipients = active.filter((child) =>
    target ? child.agentId === target : child.agentId === "lead",
  );
  if (run.provider === "codex") {
    entry.delivery = "app_server";
    try {
      if (recipients.length) {
        for (const pass of recipients) {
          if (
            pass.codexServer?.turns.get(
              pass.codexServer.threads.get(pass.codexKey),
            )?.turnId
          ) {
            try {
              await pass.codexServer.steer(pass.codexKey, entry.text, entry.id);
              consumeGuidance(run, [entry], pass.agentId);
            } catch (error) {
              if (
                !pass.codexServer.turns.has(
                  pass.codexServer.threads.get(pass.codexKey),
                )
              ) {
                scheduleAgentRevisit(run, pass.agentId);
              } else {
                entry.delivery = "controlled_restart";
                requestPassRestart(pass);
                publishEvent(run, "guidance", entry);
                publishEvent(run, "note", {
                  title:
                    "Transmission directe indisponible — reprise contrôlée",
                  detail: error.message,
                });
              }
            }
          }
        }
      } else if (target && target !== "lead") scheduleAgentRevisit(run, target);
      else if (run.phase === "lead") run.revisitLead = true;
      // A targeted worker message does not need another chief turn.
      if (run.phase === "workers" && (!target || target === "lead"))
        void supervise(run, run.validatedInput, entry).catch((error) =>
          publishEvent(run, "note", {
            title: "Dialogue chef indisponible",
            detail: error.message,
          }),
        );
    } catch (error) {
      entry.status = "prevented";
      entry.reason = error.message;
      publishEvent(run, "guidance", entry);
      return { id: entry.id, status: entry.status, reason: entry.reason };
    }
  } else if (recipients.length) {
    for (const child of recipients) requestPassRestart(child);
  } else if (target && target !== "lead") {
    scheduleAgentRevisit(run, target);
  }
  if (
    run.provider === "claude" &&
    run.phase === "workers" &&
    (!target || target === "lead")
  )
    void supervise(run, run.validatedInput, entry).catch((error) =>
      publishEvent(run, "note", {
        title: "Dialogue chef indisponible",
        detail: error.message,
      }),
    );
  if (target && target !== "lead" && run.pendingAgents.has(target)) {
    if (run.phase === "lead") {
      run.revisitLead = true;
      for (const pass of active.filter((p) => p.agentId === "lead"))
        requestPassRestart(pass);
    }
    run.queueWake?.();
  }
  publishEvent(run, "note", {
    title:
      run.provider === "codex"
        ? target && target !== "lead"
          ? "Indication adressée à l’agent"
          : "Indication adressée au chef"
        : recipients.length
          ? "Indication transmise — reprise contrôlée"
          : "Indication transmise — passage planifié",
    detail: target
      ? `Destinataire : ${target}. Le contexte et les fichiers existants sont conservés.`
      : "Les agents actifs reprennent avec cette indication et leurs sorties précédentes.",
    guidanceId: entry.id,
    targetAgentId: target,
  });
  return { id: entry.id, status: entry.status, delivery: entry.delivery };
}

async function getActions(taskId) {
  return actionRegistry
    .getActions(taskId)
    .map((action) => scopeAction(action, taskId));
}

async function performAction(input) {
  const action = await actionRegistry.performAction(input);
  return scopeAction(action, input.taskId);
}

function validateNotificationInput(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw makeError(
      "invalid_notification",
      "Question notification must be an object",
    );
  }
  const read = (field, max) => {
    const result = value[field];
    if (
      typeof result !== "string" ||
      result.trim().length === 0 ||
      result.length > max ||
      result.includes("\u0000")
    ) {
      throw makeError("invalid_notification", `${field} is invalid`);
    }
    return result;
  };
  return {
    taskId: read("taskId", runtime.MAX_TASK_ID_LENGTH || 256),
    questionId: read("questionId", 256),
    title: read("title", 1_000),
    body: read("body", runtime.MAX_EVENT_TEXT_LENGTH || 100_000),
  };
}

function notificationErrorMessage(error) {
  const message =
    error instanceof Error
      ? error.message
      : error && typeof error.message === "string"
        ? error.message
        : error === undefined || error === null
          ? ""
          : String(error);
  return message.slice(0, 500);
}

async function notifyQuestion(value) {
  const question = validateNotificationInput(value);
  if (typeof Notification !== "function")
    return { shown: false, reason: "unsupported" };
  let notification = null;
  try {
    if (
      typeof Notification.isSupported === "function" &&
      !Notification.isSupported()
    ) {
      return { shown: false, reason: "unsupported" };
    }
    notification = new Notification({
      title: question.title,
      body: question.body,
    });
    if (
      !notification ||
      typeof notification.show !== "function" ||
      typeof notification.on !== "function"
    ) {
      return { shown: false, reason: "unsupported" };
    }
    activeNotifications.add(notification);
    return await new Promise((resolve) => {
      let settled = false;
      let timer = null;
      const remove = (event, listener) => {
        try {
          notification.removeListener?.(event, listener);
        } catch {
          // A platform notification can disappear while listeners are removed.
        }
      };
      const release = () => {
        activeNotifications.delete(notification);
        if (timer) clearTimeout(timer);
        timer = null;
        remove("show", onShow);
        remove("failed", onFailed);
        remove("close", onClose);
        remove("click", onClick);
      };
      const finish = (shown, reason, error) => {
        if (settled) return;
        settled = true;
        release();
        const result = { shown };
        if (reason) result.reason = reason;
        const message = notificationErrorMessage(error);
        if (message) result.message = message;
        resolve(result);
      };
      const onShow = () => {
        if (settled) return;
        settled = true;
        if (timer) clearTimeout(timer);
        timer = null;
        remove("show", onShow);
        remove("failed", onFailed);
        // Keep the close and click listeners, and the strong reference, until
        // Electron reports that the native notification has closed.
        resolve({ shown: true });
      };
      const onFailed = (error) => finish(false, "failed", error);
      const onClose = () => {
        if (!settled) finish(false, "closed");
        else release();
      };
      const onClick = () => {
        if (mainWindow && !mainWindow.isDestroyed()) {
          if (mainWindow.isMinimized()) mainWindow.restore();
          mainWindow.show();
          mainWindow.focus();
        }
        const click = {
          eventId: randomId("notification-click-"),
          runId: null,
          taskId: question.taskId,
          type: "notification_clicked",
          timestamp: new Date().toISOString(),
          data: { questionId: question.questionId },
        };
        pendingNotificationClicks.set(question.taskId, click);
        sendEvent(click);
        void getMissionJournal()
          .append(question.taskId, click)
          .catch(() => undefined);
      };
      notification.on("show", onShow);
      notification.on("failed", onFailed);
      notification.on("close", onClose);
      notification.on("click", onClick);
      timer = setTimeout(
        () => finish(false, "timeout"),
        NOTIFICATION_SHOW_TIMEOUT_MS,
      );
      try {
        notification.show();
      } catch (error) {
        finish(false, "error", error);
      }
    });
  } catch (error) {
    if (notification) activeNotifications.delete(notification);
    return {
      shown: false,
      reason: "error",
      message: notificationErrorMessage(error),
    };
  }
}

async function loginProvider(provider) {
  const spec = providerSpec(provider);
  const command = resolveProviderCommand(spec);
  const run = createRun({ provider: spec.id }, "login");
  run.provider = spec.id;
  activeRuns.set(run.runId, run);
  publishLoginEvent(run, "running", {
    message: `Starting ${spec.name} login.`,
    operation: "login",
  });
  let child;
  try {
    child = spawn(command, spec.loginArgs, {
      shell: false,
      detached: process.platform !== "win32",
      windowsHide: true,
      stdio: ["ignore", "pipe", "pipe"],
      env: childEnvironment(),
    });
  } catch (error) {
    finishRun(run, "error", { message: error.message });
    return { runId: run.runId };
  }
  run.child = child;
  child.stdout?.setEncoding("utf8");
  child.stderr?.setEncoding("utf8");
  child.stdout?.on("data", (chunk) => {
    if (!run.finished) {
      publishLoginEvent(run, "running", {
        stream: "stdout",
        text: String(chunk).slice(0, MAX_OUTPUT_LINE),
      });
    }
  });
  child.stderr?.on("data", (chunk) => {
    if (!run.finished) {
      publishLoginEvent(run, "running", {
        stream: "stderr",
        text: String(chunk).slice(0, MAX_OUTPUT_LINE),
      });
    }
  });
  child.once("error", (error) => {
    if (!run.finished) finishRun(run, "error", { message: error.message });
  });
  child.once("close", (code, signal) => {
    if (run.killTimer) {
      clearTimeout(run.killTimer);
      run.killTimer = null;
    }
    if (run.finished) return;
    finishRun(run, code === 0 ? "completed" : "error", {
      exitCode: code,
      signal,
      message:
        code === 0 ? undefined : `Login exited with code ${code ?? "unknown"}`,
    });
  });
  return { runId: run.runId };
}

async function selectDirectory() {
  if (!mainWindow || mainWindow.isDestroyed())
    throw makeError("window_unavailable", "Djinn window is unavailable");
  const result = await dialog.showOpenDialog(mainWindow, {
    title: "Choose a project directory",
    properties: ["openDirectory", "createDirectory"],
  });
  if (result.canceled || !result.filePaths?.[0]) return null;
  await ensureDirectory(result.filePaths[0]);
  return result.filePaths[0];
}

async function exportSession(value) {
  const normalized = runtime.createPortableSession(value);
  const session =
    value && typeof value === "object" && value.task !== undefined
      ? {
          format: runtime.SESSION_FORMAT,
          version: runtime.SESSION_VERSION,
          exportedAt: normalized.exportedAt,
          projects: normalized.projects,
          task: normalized.tasks[0],
        }
      : normalized;
  const json = `${JSON.stringify(session, null, 2)}\n`;
  const filename = `djinn-${new Date().toISOString().replace(/[:.]/g, "-")}.json`;
  if (!mainWindow || mainWindow.isDestroyed())
    throw makeError("window_unavailable", "Djinn window is unavailable");
  const result = await dialog.showSaveDialog(mainWindow, {
    title: "Export Djinn session",
    defaultPath: path.join(app.getPath("documents"), filename),
    filters: [{ name: "Djinn session", extensions: ["json"] }],
  });
  if (result.canceled || !result.filePath) return null;
  await atomicWriteText(result.filePath, json);
  return {
    json,
    session,
    filename: path.basename(result.filePath),
    path: result.filePath,
  };
}

async function importSession() {
  if (!mainWindow || mainWindow.isDestroyed())
    throw makeError("window_unavailable", "Djinn window is unavailable");
  const result = await dialog.showOpenDialog(mainWindow, {
    title: "Import Djinn session",
    properties: ["openFile"],
    filters: [{ name: "Djinn session", extensions: ["json"] }],
  });
  if (result.canceled || !result.filePaths?.[0]) return null;
  const filePath = result.filePaths[0];
  const stats = await fsp.stat(filePath);
  if (stats.size > runtime.MAX_SESSION_LENGTH) {
    throw makeError("input_too_large", "Session file is too large");
  }
  const text = await fsp.readFile(filePath, "utf8");
  const session = runtime.parseSessionText(text);
  // The returned value is inert data. No command, agent, or provider field is
  // interpreted during import; the user must explicitly start a run later.
  const raw = JSON.parse(text);
  if (raw && raw.task !== undefined) {
    return {
      format: runtime.SESSION_FORMAT,
      version: runtime.SESSION_VERSION,
      projects: session.projects,
      task: session.tasks[0],
    };
  }
  return {
    format: runtime.SESSION_FORMAT,
    version: runtime.SESSION_VERSION,
    projects: session.projects,
    tasks: session.tasks,
  };
}

function sanitizeArtifactName(name) {
  if (
    typeof name !== "string" ||
    name.length === 0 ||
    name.length > 256 ||
    name.includes("\u0000")
  ) {
    throw makeError("invalid_artifact", "Artifact name is invalid");
  }
  if (
    name === "." ||
    name === ".." ||
    name.includes("/") ||
    name.includes("\\")
  ) {
    throw makeError(
      "invalid_artifact",
      "Artifact name must be a single file name",
    );
  }
  const normalized = name.replace(/[^a-zA-Z0-9._-]/g, "_");
  if (!normalized || normalized === "." || normalized === "..") {
    throw makeError("invalid_artifact", "Artifact name is invalid");
  }
  return normalized;
}

async function saveArtifact(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw makeError("invalid_artifact", "Artifact must be an object");
  }
  const name = sanitizeArtifactName(value.name);
  if (typeof value.content !== "string")
    throw makeError("invalid_artifact", "Artifact content must be text");
  if (value.content.length > runtime.MAX_ARTIFACT_CONTENT_LENGTH) {
    throw makeError("input_too_large", "Artifact content is too large");
  }
  if (!mainWindow || mainWindow.isDestroyed())
    throw makeError("window_unavailable", "Djinn window is unavailable");
  const result = await dialog.showSaveDialog(mainWindow, {
    title: "Save Djinn artifact",
    defaultPath: path.join(app.getPath("documents"), name),
    filters: [
      {
        name: "Artifact",
        extensions: [path.extname(name).replace(/^\./, "") || "txt"],
      },
    ],
  });
  if (result.canceled || !result.filePath) return null;
  const filePath = result.filePath;
  await atomicWriteText(filePath, value.content);
  return {
    name: path.basename(filePath),
    path: filePath,
    bytes: Buffer.byteLength(value.content, "utf8"),
  };
}

async function openExternal(rawUrl) {
  if (typeof rawUrl !== "string" || rawUrl.length > 2_048) {
    throw makeError(
      "invalid_url",
      "Only a short HTTP or HTTPS URL may be opened",
    );
  }
  let parsed;
  try {
    parsed = new URL(rawUrl);
  } catch {
    throw makeError("invalid_url", "URL is invalid");
  }
  if (
    !["http:", "https:"].includes(parsed.protocol) ||
    !parsed.hostname ||
    parsed.username ||
    parsed.password
  ) {
    throw makeError(
      "invalid_url",
      "Only HTTP and HTTPS URLs without credentials may be opened",
    );
  }
  await shell.openExternal(parsed.toString());
  return { opened: true, url: parsed.toString() };
}

function discoveryScanId(value) {
  if (typeof value !== "string" || !/^[a-zA-Z0-9:_-]{1,128}$/.test(value))
    throw makeError("invalid_scan", "Identifiant d’analyse invalide.");
  return value;
}
async function discoverProject(input) {
  if (!input || typeof input !== "object" || Array.isArray(input))
    throw makeError("invalid_scan", "Choisissez un dossier de projet.");
  const scanId = discoveryScanId(input.scanId);
  const provider = runtime.validateProvider(input.provider);
  if (
    typeof input.directory !== "string" ||
    !path.isAbsolute(input.directory) ||
    input.directory.length > 4096 ||
    /[\0\r\n]/.test(input.directory)
  )
    throw makeError(
      "invalid_path",
      "Le dossier doit être un chemin absolu valide.",
    );
  if (
    input.model !== undefined &&
    (typeof input.model !== "string" ||
      input.model.length > 256 ||
      /[\0\r\n]/.test(input.model))
  )
    throw makeError("invalid_model", "Modèle invalide.");
  if (
    input.includeHistory !== undefined &&
    typeof input.includeHistory !== "boolean"
  )
    throw makeError("invalid_scan", "Choix d’historique invalide.");
  if (projectScans.has(scanId) || projectScans.size >= 2)
    throw makeError(
      "scan_busy",
      "Une analyse est déjà en cours. Attendez sa fin ou annulez-la.",
    );
  const controller = new AbortController();
  projectScans.set(scanId, controller);
  try {
    const directory = await fsp.realpath(input.directory);
    if (!(await fsp.stat(directory)).isDirectory())
      throw makeError("invalid_path", "Le dossier est introuvable.");
    const state = input.includeHistory !== false ? await loadState() : null;
    return await projectDiscovery.analyzeProject(
      {
        directory,
        provider,
        model: input.model || undefined,
        history: projectHistory(state, directory),
      },
      {
        command: resolveProviderCommand(providerSpec(provider)),
        env: childEnvironment(),
        signal: controller.signal,
        spawn,
      },
    );
  } finally {
    projectScans.delete(scanId);
  }
}
async function cancelProjectDiscovery(rawScanId) {
  const scanId = discoveryScanId(rawScanId),
    controller = projectScans.get(scanId);
  controller?.abort();
  return { cancelled: Boolean(controller) };
}

function registerIpcHandlers() {
  if (ipcRegistered) return;
  ipcRegistered = true;
  registerHandler("djinn:discover-project", discoverProject);
  registerHandler("djinn:cancel-project-discovery", cancelProjectDiscovery);
  registerHandler("djinn:get-environment", getEnvironment);
  registerHandler("djinn:get-provider-models", getProviderModels);
  registerHandler("djinn:get-runtime-snapshot", getRuntimeSnapshot);
  registerHandler("djinn:get-pending-permissions", getPendingPermissions);
  registerHandler("djinn:get-mission-journal-page", getMissionJournalPage);
  registerHandler("djinn:get-mission-interactions", getMissionInteractions);
  registerHandler("djinn:select-directory", selectDirectory);
  registerHandler("djinn:load-state", loadState);
  registerHandler("djinn:save-state", saveState);
  registerHandler("djinn:export-session", exportSession);
  registerHandler("djinn:import-session", importSession);
  registerHandler("djinn:render-visualization", (source) =>
    visualizationRegistry.create(source),
  );
  registerHandler("djinn:validate-project", runtime.validateProjectDirectory);
  registerHandler("djinn:start-run", startRun);
  registerHandler("djinn:cancel-run", cancelRun);
  registerHandler("djinn:steer-run", steerRun);
  registerHandler("djinn:get-actions", getActions);
  registerHandler("djinn:perform-action", performAction);
  registerHandler("djinn:login-provider", loginProvider);
  registerHandler("djinn:open-external", openExternal);
  registerHandler("djinn:save-artifact", saveArtifact);
  registerHandler("djinn:notify-question", notifyQuestion);
  registerHandler("djinn:respond-permission", respondPermission);
}

function createMainWindow() {
  const window = new BrowserWindow({
    width: 1440,
    height: 940,
    minWidth: 980,
    minHeight: 680,
    show: false,
    backgroundColor: "#09090b",
    webPreferences: {
      preload: path.join(__dirname, "preload.cjs"),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
      webSecurity: true,
    },
  });
  mainWindow = window;
  window.webContents.setWindowOpenHandler(() => ({ action: "deny" }));
  window.webContents.on("will-navigate", (event, url) => {
    if (!isAllowedRendererUrl(url || event.url)) event.preventDefault();
  });
  window.webContents.on("will-frame-navigate", (event) => {
    // Supports may execute local scripts but cannot navigate out of their
    // isolated document. Only the renderer may load a registered support.
    const main = window.webContents.mainFrame;
    const fromParent =
      event.initiator &&
      event.initiator.routingId === main.routingId &&
      event.initiator.processId === main.processId;
    if (event.isMainFrame) {
      if ((event.initiator && !fromParent) || !isAllowedRendererUrl(event.url))
        event.preventDefault();
      return;
    }
    const initialDocument =
      !event.initiator &&
      event.frame &&
      (!event.frame.url || event.frame.url === "about:blank");
    if (
      (!fromParent && !initialDocument) ||
      typeof event.url !== "string" ||
      !event.url.startsWith("djinn-visualization://") ||
      !visualizationRegistry.get(event.url)
    )
      event.preventDefault();
  });
  window.webContents.on("will-redirect", (event) => {
    if (!event.isMainFrame || !isAllowedRendererUrl(event.url))
      event.preventDefault();
  });
  window.webContents.on("will-attach-webview", (event) => {
    event.preventDefault();
  });
  window.once("ready-to-show", () => window.show());
  window.on("closed", () => {
    if (mainWindow === window) mainWindow = null;
  });
  const devUrl = currentDevUrl();
  const loadPromise = devUrl
    ? window.loadURL(devUrl)
    : window.loadFile(rendererFilePath());
  loadPromise.catch((error) => {
    sendEvent({
      runId: null,
      taskId: null,
      type: "error",
      timestamp: new Date().toISOString(),
      data: { message: `Unable to load Djinn renderer: ${error.message}` },
    });
  });
  return window;
}

async function bootstrap() {
  configureUserDataPath();
  if (!acquireSingleInstanceLock()) return;
  await app.whenReady();
  registerIpcHandlers();
  protocol.handle("djinn-visualization", (request) => {
    const html = visualizationRegistry.get(request.url);
    return new Response(html || "Visualization unavailable", {
      status: html ? 200 : 404,
      headers: {
        "Content-Type": "text/html; charset=utf-8",
        "Content-Security-Policy": visualizationPolicy,
      },
    });
  });
  // The renderer reports invalid persisted state and suspends autosave;
  // keep its window available even when recovery is needed.
  createMainWindow();
  app.on("activate", () => {
    if (BrowserWindow.getAllWindows().length === 0) createMainWindow();
  });
}

app.on("window-all-closed", () => {
  if (process.platform !== "darwin") app.quit();
});

let journalFlushedForQuit = false;
app.on("before-quit", (event) => {
  for (const controller of projectScans.values()) controller.abort();
  for (const run of [...activeRuns.values()]) cancelOneRun(run);
  for (const server of codexServers.values()) server.close();
  actionRegistry.killAllImmediately();
  if (missionJournal && !journalFlushedForQuit) {
    event.preventDefault();
    void Promise.all([
      missionJournal.flush(),
      structuredInteractionStore?.flush(),
    ])
      .catch((error) =>
        console.error("[djinn] journal flush failed:", error.message),
      )
      .finally(() => {
        journalFlushedForQuit = true;
        app.quit();
      });
  }
});

void bootstrap().catch((error) => {
  // Electron has no renderer to report to if startup itself fails.
  console.error("[djinn] startup failed:", error);
  app.quit();
});

module.exports = {
  // Kept small and pure enough for a future Electron harness; runtime helpers
  // remain in runtime.cjs so unit tests never need to initialize Electron.
  isAllowedRendererUrl,
  providerPathEntries,
  resolveProviderCommand,
  childEnvironment,
  detectGitContext,
  startRun,
  cancelRun,
  steerRun,
  getPendingPermissions,
  respondPermission,
  notifyQuestion,
  guidanceForPrompt,
  consumeGuidance,
  activeNotifications,
  NOTIFICATION_SHOW_TIMEOUT_MS,
  validateNotificationInput,
  parseAuthenticationStatus,
  sanitizeArtifactName,
  acquireSingleInstanceLock,
  focusMainWindow,
  actionRegistry,
  getActions,
  performAction,
  actionRuntime,
  handleNativeToolCall,
  structuredProjectionForTask,
  getMissionInteractions,
  CodexAgentObserver,
  getMissionJournalPage,
  flushMissionJournal: () => missionJournal?.flush(),
};

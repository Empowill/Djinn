"use strict";

const path = require("node:path");
const fs = require("node:fs");
const workflow = require("./workflow.cjs");
const sessionValidation = require("./session-validation.cjs");

const SESSION_FORMAT = "djinn-session";
const SESSION_VERSION = 2;
const PROVIDERS = Object.freeze(["codex", "claude"]);
const RUN_MODES = Object.freeze(["plan", "execute", "review"]);
const PROTOCOL_EVENT_TYPES = Object.freeze([
  "question",
  "artifact",
  "agent",
  "work_item",
  "report",
  "phase",
  "note",
  "action",
  "mission_metadata",
  "discussion_type",
  "workflow_defined",
  "workflow_amended",
  "next_step",
  "step_result",
]);
const PUBLIC_EVENT_TYPES = Object.freeze([
  "text",
  "tool",
  ...PROTOCOL_EVENT_TYPES,
  // Native provider requests are emitted by the broker, not parsed from a
  // provider DJINN_EVENT payload. They still use the public event envelope so
  // the renderer and reconnect journal receive the request lifecycle.
  "permission_requested",
  "permission_resolved",
  "status",
  "error",
  "guidance",
]);

// These tools are registered with Codex's native app-server on a fresh
// thread. Their arguments are validated again by the main process before any
// event or action is persisted. The text protocol remains available for
// Claude and older Codex clients.
const NATIVE_TOOL_NAMES = Object.freeze([
  "publish_question",
  "publish_step_report",
  "update_task",
  "publish_test_action",
  "publish_report",
  "publish_artifact",
  "inspect_test_environment",
]);

const scheduler = require("./scheduler.cjs");
const MAX_PROMPT_LENGTH = 120_000;
const MAX_COMPOSED_PROMPT_LENGTH = 40_000;
const MAX_PATH_LENGTH = 4_096;
const MAX_MODEL_LENGTH = 256;
const MAX_TASK_ID_LENGTH = 256;
const MAX_TASK_TITLE_LENGTH = 1_000;
const MAX_EVENT_TEXT_LENGTH = 100_000;
const MAX_ARTIFACT_CONTENT_LENGTH = 10_000_000;
const MAX_SESSION_LENGTH = 12_000_000;
const MAX_AGENTS = workflow.MAX_SUB_AGENTS;
const MAX_CONCURRENCY = MAX_AGENTS;
const MAX_AGENT_PROMPT_LENGTH = 120_000;
const MAX_RESOURCE_POLICY_LENGTH = 128;
const MAX_CPU = 1024;
const MAX_MEMORY_MB = Number.MAX_SAFE_INTEGER;
const MAX_IMAGES = 5;
const MAX_IMAGE_BYTES = 4 * 1024 * 1024;
const MAX_TOTAL_IMAGE_BYTES = 12 * 1024 * 1024;
const MAX_IMAGE_DATA_URL_LENGTH = 8_000_000;
const MAX_GUIDANCE = 32;
const MAX_GUIDANCE_TEXT_LENGTH = 12_000;
const ACTION_KINDS = Object.freeze(["server", "link", "manual"]);
const MAX_ACTION_TITLE_LENGTH = 1_000;
const MAX_ACTION_DETAIL_LENGTH = 100_000;
const MAX_ACTION_URL_LENGTH = 2_048;
const MAX_ACTION_DIRECTORY_LENGTH = 1_024;
const MAX_ACTION_SCRIPT_LENGTH = 128;
const MAX_NATIVE_LIST_ENTRIES = 100;
const MAX_NATIVE_TOOL_ARGUMENTS_LENGTH = 12_000_000;
const ACTION_SCRIPT_PATTERN =
  /^(?:dev|start|serve|preview)(?:[:._-][A-Za-z0-9][A-Za-z0-9._-]{0,63})?$/;

class DjinnRuntimeError extends Error {
  constructor(code, message, details) {
    super(message);
    this.name = "DjinnRuntimeError";
    this.code = code;
    if (details !== undefined) this.details = details;
  }
}

function runtimeError(code, message, details) {
  return new DjinnRuntimeError(code, message, details);
}

function isRecord(value) {
  if (value === null || typeof value !== "object" || Array.isArray(value)) {
    return false;
  }
  const prototype = Object.getPrototypeOf(value);
  return prototype === Object.prototype || prototype === null;
}

function cloneJson(value) {
  if (typeof structuredClone === "function") {
    return structuredClone(value);
  }
  return JSON.parse(JSON.stringify(value));
}

function boundedString(value, name, max, { allowEmpty = true } = {}) {
  if (typeof value !== "string") {
    throw runtimeError("invalid_input", `${name} must be a string`);
  }
  if (!allowEmpty && value.trim().length === 0) {
    throw runtimeError("invalid_input", `${name} must not be empty`);
  }
  if (value.length > max) {
    throw runtimeError(
      "input_too_large",
      `${name} exceeds the ${max} character limit`,
    );
  }
  if (value.includes("\u0000")) {
    throw runtimeError("invalid_input", `${name} contains a NUL character`);
  }
  return value;
}

function boundedIdentifier(value, name, max = MAX_TASK_ID_LENGTH) {
  const result = boundedString(value, name, max, { allowEmpty: false }).trim();
  if (result.length === 0) {
    throw runtimeError("invalid_input", `${name} must not be empty`);
  }
  return result;
}

function decodeImageDataUrl(value, label) {
  const dataUrl = boundedString(value, label, MAX_IMAGE_DATA_URL_LENGTH, {
    allowEmpty: false,
  });
  const match =
    /^data:(image\/(?:png|jpeg|webp));base64,([A-Za-z0-9+/]+={0,2})$/.exec(
      dataUrl,
    );
  if (!match || match[2].length % 4 === 1) {
    throw runtimeError(
      "invalid_image",
      `${label} must be a base64 PNG, JPEG, or WebP data URL`,
    );
  }
  const [, mediaType, encoded] = match;
  const bytes = Buffer.from(encoded, "base64");
  if (bytes.length === 0 || bytes.length > MAX_IMAGE_BYTES) {
    throw runtimeError(
      "input_too_large",
      `${label} must be at most ${MAX_IMAGE_BYTES} bytes`,
    );
  }
  const canonical = bytes.toString("base64").replace(/=+$/, "");
  if (canonical !== encoded.replace(/=+$/, "")) {
    throw runtimeError(
      "invalid_image",
      `${label} contains invalid base64 data`,
    );
  }
  const isPng =
    mediaType === "image/png" &&
    bytes.subarray(0, 8).equals(Buffer.from("\x89PNG\r\n\x1a\n", "binary"));
  const isJpeg =
    mediaType === "image/jpeg" &&
    bytes.subarray(0, 3).equals(Buffer.from([0xff, 0xd8, 0xff]));
  const isWebp =
    mediaType === "image/webp" &&
    bytes.subarray(0, 4).toString("ascii") === "RIFF" &&
    bytes.subarray(8, 12).toString("ascii") === "WEBP";
  if (!isPng && !isJpeg && !isWebp) {
    throw runtimeError(
      "invalid_image",
      `${label} does not match its declared image type`,
    );
  }
  return { dataUrl, mediaType, base64: encoded, byteLength: bytes.length };
}

function validateRunImages(images) {
  if (images === undefined) return [];
  if (!Array.isArray(images))
    throw runtimeError("invalid_image", "images must be an array");
  if (images.length > MAX_IMAGES)
    throw runtimeError(
      "too_many_images",
      `At most ${MAX_IMAGES} images may be attached`,
    );
  let totalBytes = 0;
  const result = images.map((image, index) => {
    if (!isRecord(image))
      throw runtimeError("invalid_image", `images[${index}] must be an object`);
    const id = boundedIdentifier(
      image.id ?? `image-${index + 1}`,
      `images[${index}].id`,
      256,
    );
    const title = boundedString(
      image.title ?? id,
      `images[${index}].title`,
      1_000,
      { allowEmpty: false },
    );
    const decoded = decodeImageDataUrl(
      image.dataUrl,
      `images[${index}].dataUrl`,
    );
    totalBytes += decoded.byteLength;
    if (totalBytes > MAX_TOTAL_IMAGE_BYTES) {
      throw runtimeError(
        "input_too_large",
        `Attached images must total at most ${MAX_TOTAL_IMAGE_BYTES} bytes`,
      );
    }
    return { id, title, ...decoded };
  });
  return result;
}

function validateProvider(provider) {
  if (!PROVIDERS.includes(provider)) {
    throw runtimeError(
      "invalid_provider",
      `Unsupported provider: ${String(provider)}`,
    );
  }
  return provider;
}

function validateMode(mode) {
  if (mode === undefined || mode === null) return "execute";
  if (!RUN_MODES.includes(mode)) {
    throw runtimeError("invalid_mode", `Unsupported run mode: ${String(mode)}`);
  }
  return mode;
}

function validateConcurrency(concurrency) {
  if (concurrency === undefined || concurrency === null) return 1;
  if (
    !Number.isInteger(concurrency) ||
    concurrency < 1 ||
    concurrency > MAX_CONCURRENCY
  ) {
    throw runtimeError(
      "invalid_concurrency",
      `concurrency must be an integer from 1 to ${MAX_CONCURRENCY}`,
    );
  }
  return concurrency;
}

function validateResourcePolicy(value) {
  if (value === undefined || value === null) return undefined;
  if (!isRecord(value))
    throw runtimeError("invalid_resources", "resourcePolicy must be an object");
  const mode = value.mode === undefined ? "fixed" : value.mode;
  if (!["fixed", "adaptive"].includes(mode))
    throw runtimeError(
      "invalid_resources",
      `Unsupported resource policy: ${String(mode)}`,
    );
  let capacity;
  if (value.capacity !== undefined) {
    if (!isRecord(value.capacity))
      throw runtimeError(
        "invalid_resources",
        "resourcePolicy.capacity must be an object",
      );
    const positive = (raw, field, maximum) => {
      if (
        typeof raw !== "number" ||
        !Number.isFinite(raw) ||
        raw <= 0 ||
        raw > maximum
      )
        throw runtimeError(
          "invalid_resources",
          `${field} must be a positive number at most ${maximum}`,
        );
      return raw;
    };
    capacity = {
      cpu: positive(value.capacity.cpu, "resourcePolicy.capacity.cpu", MAX_CPU),
      memoryMb: positive(
        value.capacity.memoryMb,
        "resourcePolicy.capacity.memoryMb",
        MAX_MEMORY_MB,
      ),
      labels: scheduler
        .validateResourceProfile({ labels: value.capacity.labels })
        .labels.slice(0, MAX_RESOURCE_POLICY_LENGTH),
    };
  }
  return { mode, ...(capacity ? { capacity } : {}) };
}

function validateTask(task, label = "task", restored = true) {
  try {
    return sessionValidation.validateTask(task, restored);
  } catch (error) {
    throw runtimeError("invalid_session", `${label}: ${error.message}`);
  }
}

function validateProject(value) {
  try {
    return workflow.validateProject(value);
  } catch (error) {
    throw runtimeError("invalid_project", error.message);
  }
}

function validateProjectDirectory(value) {
  const project = validateProject(value);
  if (!project.directory)
    throw runtimeError(
      "invalid_path",
      "Associate this project with a local directory first",
    );
  let root;
  try {
    root = fs.realpathSync(project.directory);
    if (!fs.statSync(root).isDirectory()) throw new Error("not a directory");
    for (const location of Object.values(project.locations)) {
      const parts = location.replaceAll("\\", "/").split("/");
      let candidate = root;
      for (const part of parts) {
        candidate = path.join(candidate, part);
        try {
          fs.lstatSync(candidate);
          candidate = fs.realpathSync(candidate); // A dangling symlink is not a safe missing directory.
        } catch (error) {
          if (error.code !== "ENOENT") throw error;
          try {
            if (fs.lstatSync(candidate).isSymbolicLink())
              throw new Error("Unresolved symbolic link in project location");
          } catch (linkError) {
            if (linkError.code !== "ENOENT") throw linkError;
          }
        }
        const relative = path.relative(root, candidate);
        if (
          relative === ".." ||
          relative.startsWith(`..${path.sep}`) ||
          path.isAbsolute(relative)
        )
          throw new Error("Location leaves project root");
      }
    }
  } catch (error) {
    throw runtimeError("invalid_path", `Project directory: ${error.message}`);
  }
  return { ...project, directory: root };
}

function enforceJsonSize(value, label, max = MAX_SESSION_LENGTH) {
  let serialized;
  try {
    serialized = JSON.stringify(value);
  } catch {
    throw runtimeError(
      "invalid_input",
      `${label} must contain JSON-compatible data`,
    );
  }
  if (serialized.length > max) {
    throw runtimeError(
      "input_too_large",
      `${label} exceeds the ${max} character limit`,
    );
  }
}

/**
 * Normalize all supported portable session shapes to a single, harmless
 * in-memory representation. Importing this value never executes any field.
 */
function validateSession(value, restored = true) {
  if (!isRecord(value)) {
    throw runtimeError("invalid_session", "Session must be an object");
  }
  if (![1, SESSION_VERSION].includes(value.version)) {
    throw runtimeError(
      "invalid_session_version",
      `Session version must be ${SESSION_VERSION}`,
    );
  }
  if (value.format !== undefined && value.format !== SESSION_FORMAT) {
    throw runtimeError(
      "invalid_session_format",
      `Session format must be ${SESSION_FORMAT}`,
    );
  }

  let rawTasks;
  if (Array.isArray(value.tasks)) {
    rawTasks = value.tasks;
  } else if (value.task !== undefined) {
    rawTasks = [value.task];
  } else {
    throw runtimeError("invalid_session", "Session must contain tasks or task");
  }
  if (rawTasks.length > 1_000) {
    throw runtimeError("input_too_large", "Session has too many tasks");
  }

  const state = validateState(
    {
      version: value.version,
      tasks: rawTasks,
      ...(value.projects !== undefined ? { projects: value.projects } : {}),
    },
    restored,
  );
  const tasks = state.tasks;
  enforceJsonSize(tasks, "session");
  return {
    format: SESSION_FORMAT,
    version: SESSION_VERSION,
    projects: state.projects,
    tasks,
  };
}

function validateState(value, restored = true) {
  if (!isRecord(value))
    throw runtimeError("invalid_state", "State must be an object");
  if (![1, SESSION_VERSION].includes(value.version))
    throw runtimeError(
      "invalid_state_version",
      `State version must be 1 or ${SESSION_VERSION}`,
    );
  if (!Array.isArray(value.tasks) || value.tasks.length > 1000)
    throw runtimeError(
      "invalid_state",
      "State.tasks must be an array with at most 1000 entries",
    );
  enforceJsonSize(value, "state");
  try {
    return sessionValidation.validateState(value, restored);
  } catch (error) {
    throw runtimeError("invalid_state", error.message);
  }
}

function createPortableSession(value) {
  const normalized = validateSession(value, false);
  return {
    format: SESSION_FORMAT,
    version: SESSION_VERSION,
    exportedAt: new Date().toISOString(),
    projects: normalized.projects,
    tasks: normalized.tasks,
  };
}

function serializeSession(value) {
  const session = createPortableSession(value);
  return `${JSON.stringify(session, null, 2)}\n`;
}

function parseSessionText(text) {
  boundedString(text, "session text", MAX_SESSION_LENGTH, {
    allowEmpty: false,
  });
  let value;
  try {
    value = JSON.parse(text);
  } catch (error) {
    throw runtimeError(
      "invalid_session_json",
      "Session file is not valid JSON",
      {
        cause: error instanceof Error ? error.message : String(error),
      },
    );
  }
  return validateSession(value);
}

function validateRunAgent(agent, index = 0) {
  if (!isRecord(agent)) {
    throw runtimeError("invalid_agent", `agents[${index}] must be an object`);
  }
  const isolation = scheduler.validateIsolation(agent.isolation);
  const worktree =
    agent.worktree === undefined
      ? undefined
      : boundedString(
          agent.worktree,
          `agents[${index}].worktree`,
          MAX_PATH_LENGTH,
          {
            allowEmpty: false,
          },
        );
  if (isolation === "worktree" && (!worktree || !path.isAbsolute(worktree)))
    throw runtimeError(
      "invalid_agent",
      `agents[${index}].worktree must be an absolute path for worktree isolation`,
    );
  return {
    id: boundedIdentifier(agent.id, `agents[${index}].id`),
    name: boundedString(agent.name ?? agent.id, `agents[${index}].name`, 256, {
      allowEmpty: false,
    }),
    role: boundedString(
      agent.role ?? "delegated agent",
      `agents[${index}].role`,
      1_000,
      {
        allowEmpty: false,
      },
    ),
    writeScope: scheduler.validateWriteScope(agent.writeScope),
    isolation,
    worktree,
    resources: scheduler.validateResourceProfile(agent.resources),
    readOnly:
      agent.readOnly === undefined
        ? undefined
        : (() => {
            if (typeof agent.readOnly !== "boolean")
              throw runtimeError("invalid_agent", "readOnly must be boolean");
            return agent.readOnly;
          })(),
    dependsOn:
      agent.dependsOn === undefined
        ? undefined
        : (() => {
            if (
              !Array.isArray(agent.dependsOn) ||
              agent.dependsOn.length > MAX_AGENTS
            )
              throw runtimeError("invalid_agent", "dependsOn must be bounded");
            return [
              ...new Set(
                agent.dependsOn.map((id) =>
                  boundedIdentifier(id, "agent.dependsOn"),
                ),
              ),
            ];
          })(),
    prompt: boundedString(
      agent.prompt,
      `agents[${index}].prompt`,
      MAX_AGENT_PROMPT_LENGTH,
      {
        allowEmpty: false,
      },
    ),
  };
}

function validateGuidance(guidance) {
  if (guidance === undefined) return [];
  if (!Array.isArray(guidance))
    throw runtimeError("invalid_guidance", "guidance must be an array");
  if (guidance.length > MAX_GUIDANCE) {
    throw runtimeError(
      "too_many_guidance",
      `At most ${MAX_GUIDANCE} guidance entries may be attached`,
    );
  }
  const ids = new Set();
  return guidance.map((entry, index) => {
    if (!isRecord(entry))
      throw runtimeError(
        "invalid_guidance",
        `guidance[${index}] must be an object`,
      );
    const id = boundedIdentifier(entry.id, `guidance[${index}].id`, 256);
    if (ids.has(id))
      throw runtimeError("invalid_guidance", `guidance id ${id} is duplicated`);
    ids.add(id);
    const result = {
      id,
      text: boundedString(
        entry.text,
        `guidance[${index}].text`,
        MAX_GUIDANCE_TEXT_LENGTH,
        {
          allowEmpty: false,
        },
      ),
    };
    if (entry.agentId !== undefined && entry.agentId !== null) {
      result.agentId = boundedIdentifier(
        entry.agentId,
        `guidance[${index}].agentId`,
        256,
      );
    }
    return result;
  });
}

function validateSteerInput(value) {
  if (!isRecord(value))
    throw runtimeError("invalid_guidance", "Steering input must be an object");
  const result = {
    runId: boundedIdentifier(value.runId, "runId", 256),
    id: boundedIdentifier(value.id, "guidance.id", 256),
    text: boundedString(value.text, "guidance.text", MAX_GUIDANCE_TEXT_LENGTH, {
      allowEmpty: false,
    }),
  };
  if (value.agentId !== undefined && value.agentId !== null) {
    result.agentId = boundedIdentifier(value.agentId, "guidance.agentId", 256);
  }
  return result;
}

function validateRunInput(input) {
  if (!isRecord(input)) {
    throw runtimeError("invalid_input", "Run input must be an object");
  }
  const result = {
    taskId: boundedIdentifier(input.taskId, "taskId"),
    provider: validateProvider(input.provider),
    cwd: boundedString(input.cwd, "cwd", MAX_PATH_LENGTH, {
      allowEmpty: false,
    }),
    prompt: boundedString(input.prompt, "prompt", MAX_PROMPT_LENGTH, {
      allowEmpty: false,
    }),
    mode: validateMode(input.mode),
    concurrency: validateConcurrency(input.concurrency),
  };
  if (input.resourcePolicy !== undefined)
    result.resourcePolicy = validateResourcePolicy(input.resourcePolicy);
  if (input.providerSessions !== undefined) {
    if (
      !isRecord(input.providerSessions) ||
      Object.keys(input.providerSessions).length > 200
    )
      throw runtimeError(
        "invalid_input",
        "providerSessions must be a bounded map",
      );
    result.providerSessions = Object.fromEntries(
      Object.entries(input.providerSessions).map(([key, value]) => [
        boundedIdentifier(key, "session.key", 512),
        boundedIdentifier(value, "session.threadId", 256),
      ]),
    );
  }
  if (input.stepId !== undefined)
    result.stepId = boundedIdentifier(input.stepId, "stepId");
  if (input.workflowMode !== undefined) {
    if (!["flexible", "fixed"].includes(input.workflowMode))
      throw runtimeError(
        "invalid_workflow",
        `Unsupported workflow mode: ${String(input.workflowMode)}`,
      );
    result.workflowMode = input.workflowMode;
  }
  if (input.workflowOrigin !== undefined) {
    if (!["agent", "legacy"].includes(input.workflowOrigin))
      throw runtimeError(
        "invalid_workflow",
        `Unsupported workflow origin: ${String(input.workflowOrigin)}`,
      );
    result.workflowOrigin = input.workflowOrigin;
  }
  if (input.initialStepProposal !== undefined) {
    if (!isRecord(input.initialStepProposal))
      throw runtimeError(
        "invalid_input",
        "initialStepProposal must be an object",
      );
    const proposal = validateProtocolData(
      "discussion_type",
      input.initialStepProposal,
    );
    result.initialStepProposal = {
      ...proposal,
      stepId: boundedIdentifier(
        input.initialStepProposal.stepId,
        "initialStepProposal.stepId",
      ),
    };
  }
  if (input.initialWorkflowProposal !== undefined) {
    if (!isRecord(input.initialWorkflowProposal))
      throw runtimeError(
        "invalid_input",
        "initialWorkflowProposal must be an object",
      );
    const proposal = validateWorkflowDefinition(
      input.initialWorkflowProposal,
      "initialWorkflowProposal",
    );
    result.initialWorkflowProposal = {
      ...proposal,
      stepId: boundedIdentifier(
        input.initialWorkflowProposal.stepId,
        "initialWorkflowProposal.stepId",
      ),
    };
  }
  if (input.workflowProposal !== undefined) {
    if (!isRecord(input.workflowProposal))
      throw runtimeError("invalid_input", "workflowProposal must be an object");
    const proposal = validateWorkflowDefinition(
      input.workflowProposal,
      "workflowProposal",
    );
    result.workflowProposal = {
      ...proposal,
      stepId: boundedIdentifier(
        input.workflowProposal.stepId,
        "workflowProposal.stepId",
      ),
    };
  }
  if (input.step !== undefined) {
    result.step = workflow.validateStep(input.step);
    if (result.stepId !== result.step.id)
      throw runtimeError("invalid_step", "stepId must match the run step");
    if (result.mode !== workflow.stepMode(result.step.type))
      throw runtimeError(
        "invalid_mode",
        "Step type determines the provider mode",
      );
  }
  if (input.projectSnapshot !== undefined)
    result.projectSnapshot = workflow.validateProjectSnapshot(
      input.projectSnapshot,
    );
  if (input.previousSteps !== undefined) {
    if (!Array.isArray(input.previousSteps) || input.previousSteps.length > 100)
      throw runtimeError(
        "invalid_input",
        "previousSteps must be a bounded array",
      );
    result.previousSteps = input.previousSteps.map((step) => ({
      id: boundedIdentifier(step.id, "previousSteps.id"),
      title: boundedString(step.title, "previousSteps.title", 1000),
      summary: boundedString(step.summary, "previousSteps.summary", 8000),
    }));
  }
  if (input.images !== undefined)
    result.images = validateRunImages(input.images);
  if (input.guidance !== undefined)
    result.guidance = validateGuidance(input.guidance);
  if (!path.isAbsolute(result.cwd)) {
    throw runtimeError("invalid_path", "cwd must be an absolute path");
  }
  if (input.model !== undefined && input.model !== null) {
    result.model = boundedString(input.model, "model", MAX_MODEL_LENGTH, {
      allowEmpty: false,
    });
  }
  if (input.agents !== undefined) {
    if (!Array.isArray(input.agents)) {
      throw runtimeError("invalid_agent", "agents must be an array");
    }
    if (input.agents.length > MAX_AGENTS) {
      throw runtimeError(
        "too_many_agents",
        `At most ${MAX_AGENTS} agents may be launched`,
      );
    }
    result.agents = input.agents.map(validateRunAgent);
    if (
      new Set(result.agents.map((agent) => agent.id)).size !==
      result.agents.length
    )
      throw runtimeError(
        "invalid_agent",
        "Duplicate agent identities cannot share a provider thread",
      );
  } else {
    result.agents = [];
  }

  scheduler.validateDependencies(result.agents);
  if (input.decisions !== undefined) {
    if (!Array.isArray(input.decisions)) {
      throw runtimeError("invalid_input", "decisions must be an array");
    }
    result.decisions = input.decisions.slice(0, 100).map((decision, index) =>
      boundedString(decision, `decisions[${index}]`, 4_000, {
        allowEmpty: false,
      }),
    );
  }
  if (input.priorSummaries !== undefined) {
    if (!Array.isArray(input.priorSummaries)) {
      throw runtimeError("invalid_input", "priorSummaries must be an array");
    }
    result.priorSummaries = input.priorSummaries
      .slice(0, 100)
      .map((summary, index) =>
        boundedString(summary, `priorSummaries[${index}]`, 8_000, {
          allowEmpty: false,
        }),
      );
  }
  if (input.workerSummaries !== undefined) {
    if (!Array.isArray(input.workerSummaries)) {
      throw runtimeError("invalid_input", "workerSummaries must be an array");
    }
    result.workerSummaries = input.workerSummaries
      .slice(0, MAX_AGENTS)
      .map((summary, index) =>
        boundedString(summary, `workerSummaries[${index}]`, 8_000, {
          allowEmpty: false,
        }),
      );
  }
  return result;
}

function scopeProviderEvent(run, event) {
  const lead =
    (run.kind === "lead" && !run.agentId && !run.parentRunId) ||
    (run.kind === "lead-pass" &&
      run.agentId === "lead" &&
      run.parent?.kind === "lead");
  const titleOwner = run.parent || run;
  if (
    event.type === "mission_metadata" &&
    (!lead || titleOwner.titleSource === "human" || titleOwner.titleEditedAt)
  )
    return null;
  if (event.type === "agent" && !lead) return null;
  if (event.type === "step_result" && !lead) return null;
  const workflowMode = run.workflowMode || run.validatedInput?.workflowMode;
  const workflowPolicy =
    run.workflowPolicy || run.validatedInput?.projectSnapshot?.workflowPolicy;
  const workflowOrigin =
    run.workflowOrigin ||
    run.validatedInput?.workflowOrigin ||
    run.parent?.validatedInput?.workflowOrigin;
  const stepType =
    run.stepType || run.step?.type || run.validatedInput?.step?.type;
  const initialStepProposal =
    run.initialStepProposal || run.validatedInput?.initialStepProposal;
  const initialWorkflowProposal =
    run.initialWorkflowProposal ||
    run.validatedInput?.initialWorkflowProposal ||
    run.parent?.validatedInput?.initialWorkflowProposal;
  if (event.type === "discussion_type") {
    if (
      !lead ||
      workflowMode !== "flexible" ||
      workflowOrigin === "agent" ||
      workflowPolicy === "enforced" ||
      stepType !== "discussion" ||
      initialStepProposal
    )
      return null;
  }
  if (event.type === "workflow_defined") {
    if (
      !lead ||
      workflowMode !== "flexible" ||
      workflowOrigin !== "agent" ||
      workflowPolicy === "enforced" ||
      stepType !== "discussion" ||
      initialStepProposal ||
      initialWorkflowProposal
    )
      return null;
  }
  if (event.type === "next_step") {
    if (!lead || workflowMode !== "flexible" || workflowPolicy === "enforced")
      return null;
  }
  if (
    event.type === "workflow_amended" &&
    (!lead ||
      workflowMode !== "flexible" ||
      workflowPolicy === "enforced" ||
      stepType === "discussion")
  )
    return null;
  if (
    stepType === "discussion" &&
    workflowMode === "flexible" &&
    !initialStepProposal &&
    !initialWorkflowProposal &&
    [
      "artifact",
      "action",
      "agent",
      "phase",
      "next_step",
      "workflow_amended",
    ].includes(event.type)
  )
    return null;
  const data = { ...event.data, stepId: run.stepId, runId: run.runId };
  if (event.type === "agent") {
    data.status = "queued";
    data.proposed = true;
  } // Actual agent lifecycle is emitted only by native orchestration.
  if (run.agentId) data.agentId = run.agentId;
  if (event.type === "phase" && run.stepId)
    return {
      type: "note",
      data: {
        stepId: run.stepId,
        runId: run.runId,
        title: "Proposition de progression",
        detail: JSON.stringify(event.data),
        progressionProposal: true,
      },
    };
  return { type: event.type, data };
}

function bindRunToTask(input, task) {
  const stepId = input.stepId || task.activeStepId;
  const step = task.steps?.find((s) => s.id === stepId);
  if (!step || !workflow.canStartStep({ ...task, status: "idle" }, stepId))
    throw runtimeError(
      "invalid_step",
      "This stage cannot start before human validation and blocking answers",
    );
  if (path.resolve(task.project) !== path.resolve(input.cwd))
    throw runtimeError(
      "invalid_path",
      "Run directory must match the mission project",
    );
  if (
    input.provider !== task.provider ||
    ((task.model || "") !== "" && (input.model || "") !== task.model)
  )
    throw runtimeError(
      "invalid_provider",
      "Run must use the mission configured provider and model",
    );
  const mode = workflow.stepMode(step.type);
  if (input.mode !== mode)
    throw runtimeError(
      "invalid_mode",
      "Step type determines the provider mode",
    );
  const configuredConcurrency = validateConcurrency(
    task.configuration?.concurrency,
  );
  return {
    ...input,
    concurrency: Math.min(input.concurrency, configuredConcurrency),
    providerSessions: task.providerSessions,
    stepId,
    step,
    workflowMode: task.workflowMode,
    workflowOrigin: task.workflowOrigin,
    initialStepProposal: task.initialStepProposal,
    initialWorkflowProposal: task.initialWorkflowProposal,
    workflowProposal: task.workflowProposal,
    titleSource: task.titleSource,
    titleEditedAt: task.titleEditedAt,
    projectSnapshot: task.projectSnapshot,
    previousSteps: task.steps
      .filter((s) => s.status === "completed")
      .map((s) => ({
        id: s.id,
        title: s.title,
        summary: (s.summary || "").slice(0, 8000),
      })),
  };
}

function protocolInstructions(mode, provider = "claude") {
  const instructions = [
    `You are running inside Djinn in ${mode} mode.`,
    provider === "codex"
      ? "Codex app-server native structured tools are the source of truth: call publish_question, update_task, publish_report, publish_step_report, publish_artifact, publish_test_action, and inspect_test_environment directly when those tools apply. Use inspect_test_environment for bounded local diagnostics before asking for routine escalation; it reads package scripts, runs only a fixed local docker ps probe, and checks explicitly supplied loopback URLs. Do not encode those interactions as DJINN_EVENT text. Native questions require a non-empty context or question, non-blocking questions use blocking:false, and a worker question should keep blockingScope:agent so independent workers can continue; the lead may use blockingScope:mission when the whole stage must wait. Native reports require status, summary, completed, remaining, and evidence. Use DJINN_EVENT only for note, metadata, workflow, or other legacy events without a native tool; Claude/text fallback remains normalized by Djinn."
      : "For UI updates, emit one single-line JSON object prefixed with DJINN_EVENT:. Claude has no Djinn native structured tool bridge, so use the normalized text protocol for questions, work items, reports, actions, artifacts, and lifecycle updates.",
    "Allowed event types are question, artifact, agent, work_item, report, phase, note, action, mission_metadata, discussion_type, workflow_defined, workflow_amended, next_step, and step_result. Only the lead may emit mission_metadata, discussion_type, workflow_defined, workflow_amended, next_step, or step_result; workflow_defined defines the complete timeline for a fresh mission, discussion_type is kept for legacy flexible missions, and next_step is a later proposal. For a flexible mission, workflow_amended {steps,reason} replaces only the pending future suffix after this passage, preserving the active and past stages. Keep the workflow smallest and follow its configured validation: continue automatic execution when validation:automatic applies; review and delivery require validation:human. Phase and next_step events remain proposals and never approve or start a workflow stage.",
    "Artifacts use Markdown documents or standalone visualization HTML when a diagram, comparison, or interactive simulation helps the human understand. Explain the visual in Markdown, distinguish evidence from simulated data, and use only tools actually available. Visualizations run isolated, without network or native bridge access.",
    "Agent proposals may include writeScope (literal relative file or directory paths), dependsOn (agent IDs), and readOnly. A missing write scope is read-only; declare bounded ownership explicitly for a writer. Never run mutating repository-wide formatting or generation outside your assigned scope. Dependencies defer only work that needs that result. The native runtime enforces the configured simultaneous limit and performs chief integration after writers finish.",
    'Example fallback: DJINN_EVENT:{"type":"note","data":{"title":"Progress","detail":"..."}}',
    "Events are informational; never use them to request shell commands or open links. Native provider permission cards authorize commands, files, network access, and tool input; ask a business question only when a human decision is actually needed. Use Markdown for documents. When a visualization clarifies a comparison or workflow, emit an artifact of type visualization with standalone HTML and explain it in Markdown. Distinguish real data and simulated examples; use only available tools.",
  ];
  if (mode === "plan") {
    instructions.push(
      "Plan mode is read-only: inspect and reason about the project, but do not create, modify, or delete files and do not run mutating commands. Keep the provider permission mode at its safe default.",
    );
  } else if (mode === "review") {
    instructions.push(
      "Review the implementation and human feedback against the latest supports. Verify touched areas and make explicitly requested local corrections when they are needed; native permission cards gate risky provider actions. Do not broaden the correction beyond the requested scope.",
    );
  }
  instructions.push(
    "Before ending this stage, emit exactly one step_result event with status ready, blocked, or needs_input, a concise summary, and criteria evidence when available. A ready result means the stage exit criteria are met; blocked and needs_input must state the reason and nextAction.",
  );
  return instructions.join(" ");
}

function composeRunPrompt(input) {
  const validated = validateRunInput(input);
  const protocol = protocolInstructions(validated.mode, validated.provider);
  const rawPrompt = validated.prompt.trim();
  const guidanceText = (validated.guidance || [])
    .map(
      (item) =>
        `- ${item.agentId ? `[agent ${item.agentId}] ` : ""}${item.text}`,
    )
    .join("\n");
  const stageText = validated.step
    ? `${validated.step.id} (${validated.step.type}): ${validated.step.title}\nObjective: ${validated.step.objective}\nExit criteria: ${validated.step.exitCriteria.join("; ")}\nExpected supports: ${validated.step.expectedArtifacts.join("; ")}\nRequested skills: ${validated.step.skills.join(", ")}. Verify availability before claiming activation; report missing skills. Skill use never changes permissions.`
    : "";
  // Keep the durable protocol and current-stage context useful even when an
  // old mission contains a very large original brief. Later append calls put
  // steering and exit criteria ahead of lower-priority historical context.
  const baseBudget = Math.max(
    4_000,
    MAX_COMPOSED_PROMPT_LENGTH -
      protocol.length -
      1_000 -
      2 -
      Math.min(guidanceText.length, 16000) -
      Math.min(stageText.length, 12000) -
      (guidanceText ? 200 : 0) -
      (stageText ? 200 : 0),
  );
  const basePrompt =
    rawPrompt.length > baseBudget
      ? `${rawPrompt.slice(0, Math.max(0, baseBudget - 64))}\n[Prompt excerpt; current stage context follows.]`
      : rawPrompt;
  const sections = [basePrompt, protocol];
  // Bound additional context independently of the full, durable mission history.
  let remaining = Math.max(
    0,
    MAX_COMPOSED_PROMPT_LENGTH - sections.join("\n\n").length - 1000,
  );
  const append = (label, value, limit) => {
    const content = String(value || "");
    const take = Math.max(0, Math.min(limit, remaining - label.length - 100));
    const excerpt = content.slice(0, take);
    remaining -= excerpt.length + label.length + 100;
    if (excerpt)
      sections.push(
        `${label}:\n${excerpt}${excerpt.length < content.length ? "\n[Context excerpt; full history remains in the saved mission.]" : ""}`,
      );
  };
  if (input.titleSource === "placeholder")
    append(
      "Mission title",
      "If you are the mission lead, emit mission_metadata with a concise French mission title early. A human rename is authoritative.",
      500,
    );
  // Latest human steering gets a reserved share before lower-priority history.
  if (validated.guidance?.length)
    append("Human steering instructions", guidanceText, 16000);
  if (validated.step) append("Current workflow stage", stageText, 12000);
  const workflowMode = validated.workflowMode || "fixed";
  append(
    "Workflow policy",
    validated.workflowOrigin === "agent" &&
      validated.step?.type === "discussion"
      ? "This is a fresh mission. The project has no preselected workflow: define the smallest useful timeline from the user's intent before project work. Keep this routing pass concise and end after recording the timeline; provider permissions are handled by native approval cards."
      : validated.workflowOrigin === "agent"
        ? "This mission timeline was selected during the initial qualification. Follow the current stage and its permissions, preserve the selected later stages, and honor only the configured validation between stages."
        : workflowMode === "flexible"
          ? "This mission uses a flexible workflow. The lead chooses the smallest useful continuation after the current stage. Emit a next_step event only when another stage is needed; it is a human proposal and never adds or starts a stage automatically. The mission may end after this stage without code or a prototype."
          : "This mission uses its saved fixed workflow. Follow the existing timeline and do not emit next_step proposals.",
    3000,
  );
  append(
    "Orchestration policy",
    JSON.stringify(
      validated.resourcePolicy || {
        mode: "fixed",
        note: "Use declared ownership and dependencies; no resource assumption is imposed.",
      },
    ),
    4000,
  );
  if (validated.workflowProposal)
    append(
      "Selected mission timeline",
      `${JSON.stringify(validated.workflowProposal.steps)}\nRationale: ${validated.workflowProposal.reason}`,
      16000,
    );
  if (validated.step?.type === "discussion")
    append(
      "Automatic discussion routing",
      validated.workflowOrigin === "agent"
        ? "This is the unclassified first discussion of a fresh mission. Decide the complete, smallest useful timeline from the user's intent and current supports, then emit exactly one workflow_defined event with {steps:[{type,title,objective}],reason}. Use at most 8 stages; allowed types are exploration, reflection, specification, prototype, implementation, review and delivery. Keep this routing pass concise: do not emit normal artifacts, code, prototype work, actions, agents, next_step or phase events here. The orchestrator applies the timeline and starts its first stage with that stage's permissions. A specification may be the final stage and never implies code or a prototype. Ask a blocking question only if the timeline cannot be chosen from the intent."
        : "This is the unclassified first discussion of a flexible mission. Decide the smallest useful concrete stage from the human goal and current supports, then emit exactly one discussion_type event with {type,title,objective,reason}. Allowed types are exploration, reflection, specification, prototype, and implementation; do not choose review or delivery before a reviewed result exists. Stop after routing: do not emit normal artifacts, code, prototype work, actions, agents, next_step, or phase events in this pass. The orchestrator will apply the decision and start the selected stage separately.",
      5000,
    );
  if (validated.step?.type === "specification")
    append(
      "Specification guidance",
      "This is a specification stage and therefore plan/read-only work. Produce and refine the specification and its evidence without editing project source, writing code, or creating a prototype. If the specification resolves the goal, finish without proposing another stage; otherwise, in a flexible workflow, propose only the smallest next stage needed.",
      4000,
    );
  if (validated.projectSnapshot)
    append(
      "Project conventions",
      `Snapshot ${validated.projectSnapshot.capturedAt}; complement repository and human instructions, report conflicts.\n${validated.projectSnapshot.conventions}\nRelative target locations: ${JSON.stringify(validated.projectSnapshot.locations)}. Targets do not change the project root.`,
      16000,
    );
  if (validated.projectSnapshot?.sourcesOfTruth?.length)
    append(
      "Canonical project sources",
      `${JSON.stringify(validated.projectSnapshot.sourcesOfTruth)}\nTreat these relative paths as canonical project context. Human-edited supports and decisions have priority over agent proposals; preserve human edits and surface conflicts for review.`,
      12000,
    );
  if (validated.decisions?.length)
    append("Decisions from the user", validated.decisions.join("\n"), 12000);
  if (validated.workerSummaries?.length)
    append(
      "Reports from delegated workers",
      validated.workerSummaries.join("\n"),
      16000,
    );
  if (validated.priorSummaries?.length)
    append("Prior run summaries", validated.priorSummaries.join("\n"), 12000);
  if (validated.previousSteps?.length)
    append(
      "Approved earlier stages",
      validated.previousSteps.map((s) => `${s.title}: ${s.summary}`).join("\n"),
      12000,
    );
  if (
    validated.step?.type === "discussion" &&
    validated.workflowOrigin === "agent"
  )
    sections.push(
      "Final routing contract: this fresh mission requires one workflow_defined event containing the complete timeline before any project action. Do not emit legacy routing, artifacts, actions, agents, next_step or phase events during qualification. The event must contain 1 to 8 stages with type, title and objective, and a non-empty reason.",
    );
  return sections.join("\n\n");
}

function validateImagePaths(imagePaths) {
  if (imagePaths === undefined) return [];
  if (!Array.isArray(imagePaths) || imagePaths.length > MAX_IMAGES) {
    throw runtimeError(
      "invalid_image",
      `imagePaths must contain at most ${MAX_IMAGES} paths`,
    );
  }
  return imagePaths.map((imagePath, index) => {
    const value = boundedString(
      imagePath,
      `imagePaths[${index}]`,
      MAX_PATH_LENGTH,
      { allowEmpty: false },
    );
    if (!path.isAbsolute(value))
      throw runtimeError("invalid_path", "image paths must be absolute");
    return value;
  });
}

function buildProviderArgs(
  provider,
  prompt,
  model,
  mode,
  imagePaths = [],
  useStdin = false,
  nativePermissions = false,
) {
  validateProvider(provider);
  const safePrompt = boundedString(
    prompt,
    "prompt",
    MAX_PROMPT_LENGTH + 20_000,
    {
      allowEmpty: false,
    },
  );
  const safeImagePaths = validateImagePaths(imagePaths);
  const args =
    provider === "codex"
      ? [
          "exec",
          "--json",
          "--sandbox",
          mode === "plan" ? "read-only" : "workspace-write",
          "--skip-git-repo-check",
        ]
      : useStdin
        ? [
            "-p",
            "--input-format",
            "stream-json",
            "--output-format",
            "stream-json",
            "--verbose",
            ...(nativePermissions ? ["--permission-prompts", "host"] : []),
          ]
        : ["-p", "--output-format", "stream-json", "--verbose"];
  if (
    provider === "claude" &&
    (mode === "plan" || (!nativePermissions && mode === "review"))
  ) {
    args.push(
      "--tools",
      "Read,Glob,Grep",
      "--append-system-prompt",
      `${mode === "plan" ? "Plan" : "Review"} mode is read-only. Inspect and reason about the project, but do not create, modify, or delete files and do not run mutating commands.`,
    );
  }
  if (provider === "claude" && nativePermissions) {
    args.push("--permission-mode", mode === "plan" ? "plan" : "manual");
  } else if (provider === "claude" && mode !== undefined && !useStdin) {
    args.push(
      "--permission-mode",
      mode === "plan" || mode === "review" ? "plan" : "acceptEdits",
    );
  }
  if (model !== undefined && model !== null) {
    args.push(
      "--model",
      boundedString(model, "model", MAX_MODEL_LENGTH, { allowEmpty: false }),
    );
  }
  if (provider === "codex") {
    for (const imagePath of safeImagePaths) args.push("--image", imagePath);
    args.push("--", safePrompt);
  } else if (!useStdin) {
    args.push("--", safePrompt);
  }
  return args;
}

function buildClaudeStreamInput(prompt, images) {
  const content = [{ type: "text", text: prompt }];
  for (const image of images || []) {
    content.push({
      type: "image",
      source: {
        type: "base64",
        media_type: image.mediaType,
        data: image.base64,
      },
    });
  }
  return `${JSON.stringify({
    type: "user",
    message: { role: "user", content },
    parent_tool_use_id: null,
    session_id: "",
  })}\n`;
}

function buildProviderInvocation(input, options = {}) {
  const validated = validateRunInput(input);
  const imagePaths = validateImagePaths(options.imagePaths);
  const nativePermissions = options.nativePermissions === true;
  const useStdin =
    validated.provider === "claude" &&
    (nativePermissions || (validated.images?.length || 0) > 0);
  const composedPrompt = composeRunPrompt(validated);
  return {
    command: validated.provider,
    args: buildProviderArgs(
      validated.provider,
      composedPrompt,
      validated.model,
      validated.mode,
      imagePaths,
      useStdin,
      nativePermissions,
    ),
    cwd: validated.cwd,
    mode: validated.mode,
    shell: false,
    stdinText: useStdin
      ? buildClaudeStreamInput(composedPrompt, validated.images)
      : undefined,
    keepStdinOpen: nativePermissions,
    nativePermissions,
  };
}

function buildExecutionPlan(input, options = {}) {
  const validated = validateRunInput(input);
  const configuredConcurrency = Math.min(
    validated.concurrency,
    MAX_CONCURRENCY,
  );
  const resourcePolicy = validated.resourcePolicy;
  const capacity =
    options.capacity ||
    resourcePolicy?.capacity ||
    (resourcePolicy?.mode === "adaptive"
      ? scheduler.systemCapacity()
      : undefined);
  const batches = scheduler.buildWaves(
    options.preparedAgents || validated.agents,
    configuredConcurrency,
    validated.mode,
    capacity ? { capacity } : undefined,
  );
  const effectiveConcurrency = Math.max(
    1,
    ...batches.map((batch) => batch.length),
  );
  const workerStages = validated.agents.map((agent) => ({
    id: agent.id,
    kind: "worker",
    name: agent.name,
    role: agent.role,
    lifecycle: "agent",
  }));
  const waves = batches.map((batch, index) => ({
    id: `wave-${index + 1}`,
    kind: "worker-wave",
    concurrency: batch.length,
    agents: batch.map((a) => ({
      ...workerStages.find((s) => s.id === a.id),
      writeScope: a.writeScope,
      dependsOn: a.dependsOn,
      readOnly: a.readOnly,
    })),
  }));
  const git =
    options.git && typeof options.git === "object"
      ? {
          branch:
            typeof options.git.branch === "string" ? options.git.branch : null,
          worktree:
            typeof options.git.worktree === "string"
              ? options.git.worktree
              : null,
        }
      : null;
  return {
    taskId: validated.taskId,
    mode: validated.mode,
    requestedConcurrency: validated.concurrency,
    configuredConcurrency,
    scheduling: "ownership",
    resourcePolicy: resourcePolicy || { mode: "fixed" },
    capacity: capacity || null,
    concurrency: effectiveConcurrency,
    parallel: effectiveConcurrency > 1 && workerStages.length > 1,
    sequential: effectiveConcurrency === 1 || workerStages.length < 2,
    git,
    stages: [
      ...workerStages,
      { id: "lead", kind: "lead", name: "Djinn", role: "integration" },
    ],
    waves,
  };
}

/**
 * Validate the small action vocabulary exposed by the model protocol. The
 * model may identify an existing package script, but it cannot supply a
 * command, arguments, shell, environment, or lifecycle status. Native code
 * resolves the script from package.json before anything is launched.
 */
function validateActionProposal(value) {
  if (!isRecord(value)) {
    throw runtimeError("invalid_event", "action event data must be an object");
  }
  const forbidden = [
    "command",
    "args",
    "cwd",
    "shell",
    "env",
    "operation",
    "status",
    "createdAt",
    "updatedAt",
    "error",
  ];
  for (const field of forbidden) {
    if (Object.prototype.hasOwnProperty.call(value, field)) {
      throw runtimeError("invalid_event", `action.${field} is not accepted`);
    }
  }
  if (!ACTION_KINDS.includes(value.kind)) {
    throw runtimeError(
      "invalid_event",
      `Unknown action kind: ${String(value.kind)}`,
    );
  }
  const result = {
    kind: value.kind,
    title: boundedString(value.title, "action.title", MAX_ACTION_TITLE_LENGTH, {
      allowEmpty: false,
    }),
  };
  if (value.id !== undefined) {
    result.id = boundedIdentifier(value.id, "action.id", 256);
  }
  if (value.detail !== undefined) {
    result.detail = boundedString(
      value.detail,
      "action.detail",
      MAX_ACTION_DETAIL_LENGTH,
    );
  }
  if (value.testInstructions !== undefined) {
    if (
      !Array.isArray(value.testInstructions) ||
      value.testInstructions.length > 24
    )
      throw runtimeError(
        "invalid_event",
        "action.testInstructions must contain at most 24 entries",
      );
    result.testInstructions = value.testInstructions.map((instruction, index) =>
      boundedString(
        instruction,
        `action.testInstructions[${index}]`,
        4_000,
        { allowEmpty: false },
      ),
    );
  }
  if (value.expectedResult !== undefined)
    result.expectedResult = boundedString(
      value.expectedResult,
      "action.expectedResult",
      4_000,
      { allowEmpty: false },
    );
  if (value.agentId !== undefined && value.agentId !== null) {
    result.agentId = boundedIdentifier(value.agentId, "action.agentId", 256);
  }
  if (value.workItemId !== undefined)
    result.workItemId = boundedIdentifier(value.workItemId, "action.workItemId", 256);
  if (value.target !== undefined)
    result.target = boundedString(value.target, "action.target", MAX_ACTION_TITLE_LENGTH, {
      allowEmpty: false,
    });
  if (
    value.directory !== undefined &&
    value.directory !== null &&
    value.directory !== ""
  ) {
    const directory = boundedString(
      value.directory,
      "action.directory",
      MAX_ACTION_DIRECTORY_LENGTH,
      {
        allowEmpty: false,
      },
    );
    if (path.isAbsolute(directory) || /^[A-Za-z]:[\\/]/.test(directory)) {
      throw runtimeError(
        "invalid_event",
        "action.directory must be relative to the project",
      );
    }
    const normalized = directory.replaceAll("\\", "/");
    if (normalized.split("/").some((part) => part === ".." || part === ".")) {
      throw runtimeError(
        "invalid_event",
        "action.directory contains dot segments",
      );
    }
    result.directory = normalized.split("/").filter(Boolean).join("/");
    if (!result.directory)
      throw runtimeError("invalid_event", "action.directory must not be empty");
  }
  if (value.script !== undefined && value.script !== null) {
    const script = boundedString(
      value.script,
      "action.script",
      MAX_ACTION_SCRIPT_LENGTH,
      {
        allowEmpty: false,
      },
    ).trim();
    if (!ACTION_SCRIPT_PATTERN.test(script)) {
      throw runtimeError(
        "invalid_event",
        "action.script must name a recognized development script",
      );
    }
    result.script = script;
  }
  if (value.url !== undefined && value.url !== null) {
    const url = boundedString(value.url, "action.url", MAX_ACTION_URL_LENGTH, {
      allowEmpty: false,
    });
    let parsed;
    try {
      parsed = new URL(url);
    } catch {
      throw runtimeError("invalid_event", "action.url is invalid");
    }
    if (
      !["http:", "https:"].includes(parsed.protocol) ||
      !parsed.hostname ||
      parsed.username ||
      parsed.password
    ) {
      throw runtimeError(
        "invalid_event",
        "action.url must be HTTP(S) without credentials",
      );
    }
    result.url = parsed.toString();
  }
  if (result.kind === "link" && !result.url) {
    throw runtimeError("invalid_event", "Link actions require a URL");
  }
  if (result.kind !== "server" && result.script !== undefined) {
    throw runtimeError(
      "invalid_event",
      "Only server actions may name a script",
    );
  }
  if (result.kind === "manual" && result.url !== undefined) {
    throw runtimeError("invalid_event", "Manual actions cannot carry a URL");
  }
  return result;
}

function truncateEventText(value) {
  if (typeof value !== "string") return "";
  return value.length > MAX_EVENT_TEXT_LENGTH
    ? `${value.slice(0, MAX_EVENT_TEXT_LENGTH)}…`
    : value;
}

function validateWorkflowDefinition(data, field = "workflow_defined") {
  if (!isRecord(data))
    throw runtimeError(
      "invalid_event",
      `${field} event data must be an object`,
    );
  if (
    !Array.isArray(data.steps) ||
    data.steps.length < 1 ||
    data.steps.length > 8
  )
    throw runtimeError(
      "invalid_event",
      `${field}.steps must contain between 1 and 8 stages`,
    );
  const allowed = workflow.STEP_TYPES.filter(
    (stepType) => stepType !== "discussion",
  );
  const steps = data.steps.map((value, index) => {
    if (!isRecord(value))
      throw runtimeError(
        "invalid_event",
        `${field}.steps[${index}] must be an object`,
      );
    if (!allowed.includes(value.type))
      throw runtimeError(
        "invalid_event",
        `Unknown ${field} type: ${String(value.type)}`,
      );
    const title = boundedString(
      value.title,
      `${field}.steps[${index}].title`,
      MAX_TASK_TITLE_LENGTH,
      { allowEmpty: false },
    );
    const objective = boundedString(
      value.objective,
      `${field}.steps[${index}].objective`,
      MAX_EVENT_TEXT_LENGTH,
      { allowEmpty: false },
    );
    if (
      value.validation !== undefined &&
      !["human", "automatic"].includes(value.validation)
    )
      throw runtimeError(
        "invalid_event",
        `${field}.steps[${index}].validation is invalid`,
      );
    if (
      (value.type === "review" || value.type === "delivery") &&
      value.validation === "automatic"
    )
      throw runtimeError(
        "invalid_event",
        "Review and delivery require human validation",
      );
    const list = (input, name) => {
      if (input === undefined) return [];
      if (!Array.isArray(input) || input.length > 100)
        throw runtimeError(
          "invalid_event",
          `${field}.${name} must be an array`,
        );
      return input.map((item, itemIndex) =>
        boundedString(item, `${field}.${name}[${itemIndex}]`, 4_000),
      );
    };
    return {
      type: value.type,
      title,
      objective,
      ...(value.validation === undefined
        ? {}
        : { validation: value.validation }),
      exitCriteria: list(value.exitCriteria, `steps[${index}].exitCriteria`),
      expectedArtifacts: list(
        value.expectedArtifacts,
        `steps[${index}].expectedArtifacts`,
      ),
      skills: list(value.skills, `steps[${index}].skills`),
    };
  });
  return {
    steps,
    reason: boundedString(
      data.reason,
      `${field}.reason`,
      MAX_EVENT_TEXT_LENGTH,
      {
        allowEmpty: false,
      },
    ),
  };
}

const NATIVE_TOOL_DEFINITIONS = Object.freeze([
  {
    type: "function",
    name: "publish_question",
    description:
      "Publish one decision question in the current Djinn mission. Use blocking false when independent work may continue.",
    inputSchema: {
      type: "object",
      properties: {
        id: { type: "string" },
        title: { type: "string", minLength: 1 },
        context: { type: "string", minLength: 1 },
        question: {
          type: "string",
          minLength: 1,
          description: "Legacy alias for context.",
        },
        recommendation: { type: "string" },
        unlocks: { type: "string" },
        blocking: { type: "boolean" },
        blockingScope: { enum: ["agent", "mission"] },
        workItemId: { type: "string" },
        theme: { type: "string" },
        agentId: { type: "string" },
        options: {
          type: "array",
          maxItems: MAX_NATIVE_LIST_ENTRIES,
          items: {
            oneOf: [
              { type: "string" },
              {
                type: "object",
                properties: {
                  id: { type: "string" },
                  label: { type: "string" },
                  description: { type: "string" },
                },
                required: ["label"],
              },
            ],
          },
        },
      },
      required: ["title"],
      allOf: [
        {
          anyOf: [
            { required: ["context"] },
            { required: ["question"] },
          ],
        },
      ],
    },
  },
  {
    type: "function",
    name: "publish_step_report",
    description:
      "Publish the lead's structured result for the current workflow stage. Include completed, remaining and evidence for clear progress.",
    inputSchema: {
      type: "object",
      properties: {
        status: { enum: ["ready", "blocked", "needs_input"] },
        summary: { type: "string" },
        completed: { type: "array", items: { type: "string" }, maxItems: 100 },
        done: {
          type: "array",
          items: { type: "string" },
          maxItems: 100,
          description: "Legacy alias for completed.",
        },
        remaining: { type: "array", items: { type: "string" }, maxItems: 100 },
        evidence: { type: "array", items: { type: "string" }, maxItems: 100 },
        criteria: { type: "array", maxItems: 100 },
        reason: { type: "string" },
        nextAction: { type: "string" },
        blocked: { type: "boolean" },
      },
      required: ["status", "summary", "remaining", "evidence"],
      allOf: [{ anyOf: [{ required: ["completed"] }, { required: ["done"] }] }],
    },
  },
  {
    type: "function",
    name: "publish_report",
    description:
      "Publish a structured contribution from a worker. Include completed, remaining and evidence; the native scope supplies the worker identity.",
    inputSchema: {
      type: "object",
      properties: {
        id: { type: "string" },
        status: { enum: ["ready", "blocked", "needs_input"] },
        summary: { type: "string" },
        completed: { type: "array", items: { type: "string" }, maxItems: 100 },
        done: { type: "array", items: { type: "string" }, maxItems: 100 },
        remaining: { type: "array", items: { type: "string" }, maxItems: 100 },
        evidence: { type: "array", items: { type: "string" }, maxItems: 100 },
        reason: { type: "string" },
        nextAction: { type: "string" },
        blocked: { type: "boolean" },
      },
      required: ["status", "summary", "remaining", "evidence"],
      allOf: [{ anyOf: [{ required: ["completed"] }, { required: ["done"] }] }],
    },
  },
  {
    type: "function",
    name: "update_task",
    description:
      "Update one bounded mission work item. This records progress only; it cannot launch commands or change ownership.",
    inputSchema: {
      type: "object",
      properties: {
        id: { type: "string" },
        title: { type: "string" },
        status: {
          enum: ["pending", "running", "blocked", "ready", "done"],
        },
        detail: { type: "string" },
        agentId: { type: "string" },
        ticket: { type: "string" },
        worktree: { type: "string" },
        branch: { type: "string" },
      },
      required: ["id", "title", "status"],
    },
  },
  {
    type: "function",
    name: "publish_test_action",
    description:
      "Register a bounded test or preview action in Djinn's native action registry. The registry resolves the command from package metadata.",
    inputSchema: {
      type: "object",
      properties: {
        id: { type: "string" },
        kind: { enum: ["server", "link", "manual"] },
        title: { type: "string" },
        detail: { type: "string" },
        directory: { type: "string" },
        script: { type: "string" },
        url: { type: "string" },
        testInstructions: {
          type: "array",
          items: { type: "string" },
          maxItems: 24,
        },
        expectedResult: { type: "string" },
        workItemId: { type: "string" },
        target: { type: "string" },
      },
      required: ["kind", "title"],
    },
  },
  {
    type: "function",
    name: "publish_artifact",
    description:
      "Publish one bounded artifact for the current mission. Use document for Markdown content; markdown is accepted as a legacy alias and is stored as document.",
    inputSchema: {
      type: "object",
      properties: {
        id: { type: "string" },
        title: { type: "string" },
        type: {
          enum: [
            "diagram",
            "wireframe",
            "document",
            "markdown",
            "code",
            "screenshot",
            "visualization",
          ],
        },
        content: { type: "string" },
        agentId: { type: "string" },
      },
      required: ["title", "type", "content"],
    },
  },
  {
    type: "function",
    name: "inspect_test_environment",
    description:
      "Inspect the bounded local test environment before requesting routine permission. Reads package scripts, checks local containers with a fixed docker ps probe, and probes only explicitly supplied loopback roots; it never runs model-provided commands.",
    inputSchema: {
      type: "object",
      properties: {
        directory: {
          type: "string",
          maxLength: MAX_PATH_LENGTH,
          description: "Optional project-relative directory to inspect.",
        },
        urls: {
          type: "array",
          maxItems: 8,
          items: { type: "string", maxLength: MAX_ACTION_URL_LENGTH },
          description: "Optional loopback server roots to probe.",
        },
      },
    },
  },
]);

function nativeToolDefinitions() {
  return cloneJson(NATIVE_TOOL_DEFINITIONS);
}

function boundedNativeList(value, name, max = MAX_NATIVE_LIST_ENTRIES) {
  if (value === undefined) return undefined;
  if (!Array.isArray(value) || value.length > max)
    throw runtimeError(
      "invalid_event",
      `${name} must be an array with at most ${max} entries`,
    );
  return value.map((entry, index) =>
    boundedString(entry, `${name}[${index}]`, MAX_EVENT_TEXT_LENGTH, {
      allowEmpty: false,
    }),
  );
}

function optionIdentifier(label, index) {
  const base = String(label || "")
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/gi, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 128);
  return boundedIdentifier(base || `option-${index + 1}`, "question.option.id", 256);
}

function validateQuestionData(data) {
  const result = {};
  const titleValue = data.title ?? data.question;
  result.title = boundedString(titleValue, "question.title", MAX_TASK_TITLE_LENGTH, {
    allowEmpty: false,
  });
  if (data.id !== undefined)
    result.id = boundedIdentifier(data.id, "question.id", 256);
  const contextValue = data.context ?? data.question;
  if (contextValue !== undefined)
    result.context = boundedString(
      contextValue,
      "question.context",
      MAX_EVENT_TEXT_LENGTH,
    );
  for (const field of ["recommendation", "unlocks", "agentId", "theme", "workItemId"]) {
    if (data[field] !== undefined)
      result[field] = boundedString(
        data[field],
        `question.${field}`,
        field === "agentId" ? 256 : MAX_EVENT_TEXT_LENGTH,
      );
  }
  if (data.blocking !== undefined) {
    result.blocking = data.blocking;
    if (typeof result.blocking !== "boolean")
      throw runtimeError("invalid_event", "question.blocking must be a boolean");
  }
  if (data.blockingScope !== undefined) {
    if (!["agent", "mission"].includes(data.blockingScope))
      throw runtimeError(
        "invalid_event",
        "question.blockingScope must be agent or mission",
      );
    result.blockingScope = data.blockingScope;
  }
  if (data.options !== undefined) {
    if (!Array.isArray(data.options) || data.options.length > MAX_NATIVE_LIST_ENTRIES)
      throw runtimeError(
        "invalid_event",
        "question.options must be an array with at most 100 entries",
      );
    const ids = new Set();
    result.options = data.options.map((option, index) => {
      const value =
        typeof option === "string"
          ? { label: option }
          : isRecord(option)
            ? option
            : null;
      if (!value)
        throw runtimeError(
          "invalid_event",
          `question.options[${index}] must be a string or object`,
        );
      const label = boundedString(
        value.label,
        `question.options[${index}].label`,
        1_000,
        { allowEmpty: false },
      );
      const id = boundedIdentifier(
        value.id ?? optionIdentifier(label, index),
        `question.options[${index}].id`,
        256,
      );
      if (ids.has(id))
        throw runtimeError("invalid_event", `question option id ${id} is duplicated`);
      ids.add(id);
      return {
        id,
        label,
        description:
          value.description === undefined
            ? ""
            : boundedString(
                value.description,
                `question.options[${index}].description`,
                4_000,
              ),
      };
    });
  }
  return result;
}

function validateNativeQuestionData(data) {
  const context = data.context ?? data.question;
  if (
    typeof context !== "string" ||
    context.trim().length === 0
  )
    throw runtimeError(
      "invalid_event",
      "publish_question requires a non-empty context or question",
    );
  return validateQuestionData(data);
}

function validateReportData(data, { step = false } = {}) {
  const statuses = ["ready", "blocked", "needs_input"];
  const blocked = data.blocked === true;
  const status = data.status === undefined ? (blocked ? "blocked" : "ready") : data.status;
  if (!statuses.includes(status))
    throw runtimeError("invalid_event", `Unknown report status: ${String(status)}`);
  const result = {
    status,
    summary: boundedString(data.summary, "report.summary", MAX_EVENT_TEXT_LENGTH, {
      allowEmpty: false,
    }),
  };
  if (data.id !== undefined) result.id = boundedIdentifier(data.id, "report.id", 256);
  const completed = data.completed ?? data.done;
  const completedList = boundedNativeList(completed, "report.completed");
  const remaining = boundedNativeList(data.remaining, "report.remaining");
  const evidence = boundedNativeList(data.evidence, "report.evidence");
  if (completedList !== undefined) result.completed = completedList;
  if (remaining !== undefined) result.remaining = remaining;
  if (evidence !== undefined) result.evidence = evidence;
  if (data.reason !== undefined)
    result.reason = boundedString(data.reason, "report.reason", MAX_EVENT_TEXT_LENGTH);
  if (data.nextAction !== undefined)
    result.nextAction = boundedString(
      data.nextAction,
      "report.nextAction",
      MAX_EVENT_TEXT_LENGTH,
    );
  if (step && data.criteria !== undefined) {
    if (!Array.isArray(data.criteria) || data.criteria.length > MAX_NATIVE_LIST_ENTRIES)
      throw runtimeError("invalid_event", "step_result.criteria must be an array with at most 100 entries");
    result.criteria = data.criteria.map((criterion, index) => {
      if (!isRecord(criterion))
        throw runtimeError("invalid_event", `step_result.criteria[${index}] must be an object`);
      if (typeof criterion.met !== "boolean")
        throw runtimeError("invalid_event", `step_result.criteria[${index}].met must be a boolean`);
      return {
        criterion: boundedString(
          criterion.criterion,
          `step_result.criteria[${index}].criterion`,
          4_000,
          { allowEmpty: false },
        ),
        met: criterion.met,
        ...(criterion.evidence === undefined
          ? {}
          : {
              evidence: boundedString(
                criterion.evidence,
                `step_result.criteria[${index}].evidence`,
                MAX_EVENT_TEXT_LENGTH,
              ),
            }),
      };
    });
  }
  return result;
}

function validateNativeReportData(data, { step = false } = {}) {
  for (const field of ["status", "summary", "remaining", "evidence"])
    if (data[field] === undefined)
      throw runtimeError(
        "invalid_event",
        `publish_${step ? "step_" : ""}report requires ${field}`,
      );
  if (data.completed === undefined && data.done === undefined)
    throw runtimeError(
      "invalid_event",
      `publish_${step ? "step_" : ""}report requires completed`,
    );
  return validateReportData(data, { step });
}

function validateWorkItemData(data) {
  const statuses = ["pending", "running", "blocked", "ready", "done"];
  if (!statuses.includes(data.status))
    throw runtimeError("invalid_event", `Unknown work_item status: ${String(data.status)}`);
  const result = {
    id: boundedIdentifier(data.id, "work_item.id", 256),
    title: boundedString(data.title, "work_item.title", MAX_TASK_TITLE_LENGTH, {
      allowEmpty: false,
    }),
    status: data.status,
  };
  for (const field of ["detail", "agentId", "ticket", "worktree", "branch"]) {
    if (data[field] !== undefined)
      result[field] = boundedString(
        data[field],
        `work_item.${field}`,
        field === "detail" ? MAX_EVENT_TEXT_LENGTH : MAX_PATH_LENGTH,
      );
  }
  return result;
}

function validateNativeTestActionData(data) {
  const result = validateActionProposal({
    ...data,
    ...(data.kind === undefined ? { kind: "manual" } : {}),
  });
  for (const field of ["workItemId", "target"]) {
    if (data[field] !== undefined)
      result[field] = boundedString(
        data[field],
        `publish_test_action.${field}`,
        1_000,
        { allowEmpty: false },
      );
  }
  return result;
}

function validateNativeArtifactData(data) {
  if (data.type === undefined)
    throw runtimeError("invalid_event", "publish_artifact requires type");
  if (data.content === undefined)
    throw runtimeError("invalid_event", "publish_artifact requires content");
  const result = validateProtocolData("artifact", data);
  if (result.type === undefined)
    throw runtimeError("invalid_event", "publish_artifact requires type");
  if (result.content === undefined)
    throw runtimeError("invalid_event", "publish_artifact requires content");
  return result;
}

function validateProtocolData(type, data) {
  if (!isRecord(data)) {
    throw runtimeError("invalid_event", `${type} event data must be an object`);
  }
  if (type === "action") return validateActionProposal(data);
  if (type === "question") return validateQuestionData(data);
  if (type === "work_item") return validateWorkItemData(data);
  if (type === "report") return validateReportData(data);
  if (type === "discussion_type") {
    const allowed = workflow.STEP_TYPES.filter(
      (stepType) =>
        stepType !== "discussion" &&
        stepType !== "review" &&
        stepType !== "delivery",
    );
    if (!allowed.includes(data.type))
      throw runtimeError(
        "invalid_event",
        `Unknown discussion_type type: ${String(data.type)}`,
      );
    return {
      type: data.type,
      title: boundedString(
        data.title,
        "discussion_type.title",
        MAX_TASK_TITLE_LENGTH,
        { allowEmpty: false },
      ),
      objective: boundedString(
        data.objective,
        "discussion_type.objective",
        MAX_EVENT_TEXT_LENGTH,
      ),
      reason: boundedString(
        data.reason,
        "discussion_type.reason",
        MAX_EVENT_TEXT_LENGTH,
        { allowEmpty: false },
      ),
    };
  }
  if (type === "workflow_defined") return validateWorkflowDefinition(data);
  if (type === "workflow_amended") {
    // A future suffix can be empty when the mission needs no additional work.
    if (Array.isArray(data.steps) && data.steps.length === 0)
      return {
        steps: [],
        reason: boundedString(
          data.reason,
          "workflow_amended.reason",
          MAX_EVENT_TEXT_LENGTH,
          { allowEmpty: false },
        ),
      };
    return validateWorkflowDefinition(data, "workflow_amended");
  }
  if (type === "next_step") {
    if (!workflow.STEP_TYPES.includes(data.type) || data.type === "discussion")
      throw runtimeError(
        "invalid_event",
        `Unknown next_step type: ${String(data.type)}`,
      );
    return {
      type: data.type,
      title: boundedString(
        data.title,
        "next_step.title",
        MAX_TASK_TITLE_LENGTH,
        {
          allowEmpty: false,
        },
      ),
      objective: boundedString(
        data.objective,
        "next_step.objective",
        MAX_EVENT_TEXT_LENGTH,
      ),
      reason: boundedString(
        data.reason,
        "next_step.reason",
        MAX_EVENT_TEXT_LENGTH,
        {
          allowEmpty: false,
        },
      ),
    };
  }
  if (type === "mission_metadata")
    return {
      title: boundedString(
        data.title,
        "mission_metadata.title",
        MAX_TASK_TITLE_LENGTH,
        { allowEmpty: false },
      ),
    };
  if (type === "step_result") {
    return validateReportData(data, { step: true });
  }
  const result = cloneJson(data);
  if (result.id !== undefined)
    result.id = boundedIdentifier(result.id, `${type}.id`);
  if (result.title !== undefined) {
    result.title = boundedString(
      result.title,
      `${type}.title`,
      MAX_TASK_TITLE_LENGTH,
    );
  }
  if (result.detail !== undefined) {
    result.detail = boundedString(
      result.detail,
      `${type}.detail`,
      MAX_EVENT_TEXT_LENGTH,
    );
  }
  if (result.content !== undefined) {
    result.content = boundedString(
      result.content,
      `${type}.content`,
      MAX_ARTIFACT_CONTENT_LENGTH,
    );
  }
  if (result.name !== undefined)
    result.name = boundedString(result.name, `${type}.name`, 256);
  for (const field of [
    "context",
    "recommendation",
    "unlocks",
    "agentId",
    "model",
    "summary",
  ]) {
    if (result[field] !== undefined) {
      result[field] = boundedString(
        result[field],
        `${type}.${field}`,
        MAX_EVENT_TEXT_LENGTH,
      );
    }
  }
  if (result.role !== undefined)
    result.role = boundedString(result.role, `${type}.role`, 1_000);
  if (result.options !== undefined) {
    if (!Array.isArray(result.options) || result.options.length > 100) {
      throw runtimeError(
        "invalid_event",
        `${type}.options must be an array with at most 100 entries`,
      );
    }
    result.options = result.options.map((option, index) => {
      if (!isRecord(option)) {
        throw runtimeError(
          "invalid_event",
          `${type}.options[${index}] must be an object`,
        );
      }
      const normalized = { ...option };
      if (normalized.id !== undefined) {
        normalized.id = boundedIdentifier(
          normalized.id,
          `${type}.options[${index}].id`,
          256,
        );
      }
      if (normalized.label !== undefined) {
        normalized.label = boundedString(
          normalized.label,
          `${type}.options[${index}].label`,
          1_000,
        );
      }
      if (normalized.description !== undefined) {
        normalized.description = boundedString(
          normalized.description,
          `${type}.options[${index}].description`,
          4_000,
        );
      }
      return normalized;
    });
  }
  if (result.blocking !== undefined && typeof result.blocking !== "boolean") {
    throw runtimeError("invalid_event", `${type}.blocking must be a boolean`);
  }
  if (type === "phase" && result.phase !== undefined) {
    const phases = ["brief", "execution", "review", "delivery"];
    if (!phases.includes(result.phase)) {
      throw runtimeError(
        "invalid_event",
        `Unknown phase: ${String(result.phase)}`,
      );
    }
  }
  if (type === "artifact" && result.type !== undefined) {
    // "markdown" was used by early native tool clients. Keep the durable
    // artifact vocabulary small while accepting that safe alias.
    if (result.type === "markdown") result.type = "document";
    const artifactTypes = [
      "diagram",
      "wireframe",
      "document",
      "code",
      "screenshot",
      "visualization",
    ];
    if (!artifactTypes.includes(result.type)) {
      throw runtimeError(
        "invalid_event",
        `Unknown artifact type: ${String(result.type)}`,
      );
    }
  }
  if (type === "agent" && result.status !== undefined) {
    const statuses = ["queued", "running", "blocked", "done", "error"];
    if (!statuses.includes(result.status)) {
      throw runtimeError(
        "invalid_event",
        `Unknown agent status: ${String(result.status)}`,
      );
    }
  }
  return result;
}

function normalizeProtocolEvent(value) {
  if (!isRecord(value)) return null;
  const type = value.type;
  if (!PROTOCOL_EVENT_TYPES.includes(type)) return null;
  const data = value.data === undefined ? { ...value } : value.data;
  if (value.data === undefined) delete data.type;
  try {
    return { type, data: validateProtocolData(type, data) };
  } catch {
    return null;
  }
}

/** Parse one or more DJINN_EVENT objects from model output. */
function parseDjinnEvents(text) {
  if (typeof text !== "string" || text.length === 0)
    return { events: [], text: text || "", diagnostics: [] };
  const events = [];
  const diagnostics = [];
  let serializedProviderRecord = false;
  try {
    serializedProviderRecord = isRecord(JSON.parse(text));
  } catch {
    // Plain model text is the normal path; only a complete JSON record needs
    // the escaped-marker guard below.
  }
  let remainder = text;
  const marker = /DJINN_EVENT:\s*(\{[^\n]*\})/g;
  let match;
  while ((match = marker.exec(text)) !== null) {
    // A provider JSON record can contain a serialized assistant message whose
    // nested DJINN_EVENT marker is escaped. Leave that record intact; the
    // provider-specific normalizer will parse the decoded text and validate it
    // on the second pass.
    if (serializedProviderRecord && match[0].includes('\\"')) continue;
    try {
      const raw = JSON.parse(match[1]);
      const event = normalizeProtocolEvent(raw);
      if (!event) {
        diagnostics.push({
          message: "Unsupported or invalid DJINN_EVENT marker",
          marker: match[0].slice(0, 4_000),
        });
        continue;
      }
      events.push(event);
      remainder = remainder.replace(match[0], "");
    } catch (error) {
      diagnostics.push({
        message: `Malformed DJINN_EVENT marker: ${String(error.message || error)}`,
        marker: match[0].slice(0, 4_000),
      });
      // Keep malformed markers as ordinary model text. No model output is executable.
    }
  }
  // The bounded marker expression intentionally does not match an incomplete
  // JSON object. Still surface that condition as a warning while preserving
  // the original provider text for the transcript.
  const markerCount = serializedProviderRecord
    ? 0
    : [...text.matchAll(/DJINN_EVENT:/g)].length;
  if (markerCount > events.length + diagnostics.length) {
    diagnostics.push({
      message: "Incomplete DJINN_EVENT marker",
      marker: text.slice(
        text.indexOf("DJINN_EVENT:"),
        Math.min(text.length, text.indexOf("DJINN_EVENT:") + 4_000),
      ),
    });
  }
  return { events, text: remainder.trim(), diagnostics };
}

function textEvent(text, provider, extra = {}) {
  return {
    type: "text",
    data: {
      text: truncateEventText(String(text ?? "")),
      provider,
      ...extra,
    },
  };
}

function toolEvent(data, provider, extra = {}) {
  return {
    type: "tool",
    data: {
      provider,
      ...data,
      ...extra,
    },
  };
}

function statusEvent(data, provider, extra = {}) {
  return {
    type: "status",
    data: {
      provider,
      ...data,
      ...extra,
    },
  };
}

function errorEvent(message, provider, extra = {}) {
  return {
    type: "error",
    data: {
      provider,
      message: truncateEventText(String(message ?? "Unknown provider error")),
      ...extra,
    },
  };
}

function protocolDiagnosticEvent(detail, provider) {
  return {
    type: "note",
    data: {
      title: "Invalid Djinn event marker",
      detail: truncateEventText(String(detail || "Invalid DJINN_EVENT marker")),
      provider,
      stream: "stdout",
      severity: "warning",
      protocolDiagnostic: true,
    },
  };
}

function extractContentBlocks(value) {
  if (Array.isArray(value)) return value;
  if (isRecord(value) && Array.isArray(value.content)) return value.content;
  return [];
}

function normalizeCodexRecord(record) {
  const type = record.type;
  if (type === "thread.started") {
    return [
      statusEvent(
        {
          status: "running",
          phase: "provider_started",
          providerRunId: record.thread_id,
        },
        "codex",
      ),
    ];
  }
  if (type === "error" || type === "turn.failed") {
    return [
      errorEvent(
        record.message || record.error || JSON.stringify(record),
        "codex",
      ),
    ];
  }
  if (type === "turn.completed") {
    return [
      statusEvent(
        {
          status: "completed",
          phase: "provider_completed",
          providerRunId: record.thread_id,
        },
        "codex",
      ),
    ];
  }
  if (
    type === "item.started" ||
    type === "item.updated" ||
    type === "item.completed"
  ) {
    const item = isRecord(record.item) ? record.item : record;
    const itemType = item.type || "";
    if (
      itemType === "agent_message" ||
      itemType === "assistant_message" ||
      itemType === "message"
    ) {
      const text = item.text ?? item.message ?? item.content;
      return text === undefined
        ? []
        : [textEvent(text, "codex", { providerRunId: record.thread_id })];
    }
    if (itemType.includes("command") || itemType.includes("tool")) {
      return [
        toolEvent(
          {
            name: item.name || itemType,
            callId: item.id,
            status:
              type === "item.completed"
                ? "completed"
                : type === "item.started"
                  ? "started"
                  : "updated",
            input: item.command ? { command: item.command } : item.input,
            output: item.aggregated_output ?? item.output,
          },
          "codex",
          { providerRunId: record.thread_id },
        ),
      ];
    }
    if (item.text !== undefined) {
      return [
        textEvent(item.text, "codex", { providerRunId: record.thread_id }),
      ];
    }
  }
  if (record.text !== undefined || record.message !== undefined) {
    return [
      textEvent(record.text ?? record.message, "codex", {
        providerRunId: record.thread_id,
      }),
    ];
  }
  return [textEvent(JSON.stringify(record), "codex")];
}

function normalizeClaudeRecord(record) {
  if (record.type === "system") {
    return [
      statusEvent(
        {
          status: "running",
          phase: "provider_started",
          providerRunId: record.session_id,
          subtype: record.subtype,
        },
        "claude",
      ),
    ];
  }
  if (record.type === "error") {
    const message =
      record.error?.message ||
      record.message ||
      JSON.stringify(record.error || record);
    return [
      errorEvent(message, "claude", { providerRunId: record.session_id }),
    ];
  }
  if (record.type === "result") {
    const events = [];
    if (record.result)
      events.push(
        textEvent(record.result, "claude", {
          providerRunId: record.session_id,
        }),
      );
    events.push(
      statusEvent(
        {
          status: "completed",
          phase: "provider_completed",
          providerRunId: record.session_id,
        },
        "claude",
      ),
    );
    return events;
  }
  if (record.type === "tool_use") {
    return [
      toolEvent(
        {
          name: record.name || "tool",
          callId: record.id,
          status: "started",
          input: record.input,
        },
        "claude",
        { providerRunId: record.session_id },
      ),
    ];
  }
  if (record.type === "tool_result") {
    return [
      toolEvent(
        {
          name: record.name || "tool",
          callId: record.tool_use_id || record.id,
          status: "completed",
          output: record.content ?? record.output,
        },
        "claude",
        { providerRunId: record.session_id },
      ),
    ];
  }

  const blocks = extractContentBlocks(record.message || record.content);
  if (blocks.length > 0) {
    const events = [];
    for (const block of blocks) {
      if (!isRecord(block)) continue;
      if (block.type === "text" && block.text !== undefined) {
        events.push(
          textEvent(block.text, "claude", { providerRunId: record.session_id }),
        );
      } else if (block.type === "tool_use") {
        events.push(
          toolEvent(
            {
              name: block.name || "tool",
              callId: block.id,
              status: "started",
              input: block.input,
            },
            "claude",
            { providerRunId: record.session_id },
          ),
        );
      } else if (block.type === "tool_result") {
        events.push(
          toolEvent(
            {
              name: block.name || "tool",
              callId: block.tool_use_id || block.id,
              status: "completed",
              output: block.content ?? block.output,
            },
            "claude",
            { providerRunId: record.session_id },
          ),
        );
      }
    }
    if (events.length > 0) return events;
  }
  if (record.text !== undefined || record.message !== undefined) {
    return [
      textEvent(record.text ?? record.message, "claude", {
        providerRunId: record.session_id,
      }),
    ];
  }
  return [textEvent(JSON.stringify(record), "claude")];
}

function parseProviderLine(provider, line) {
  validateProvider(provider);
  if (typeof line !== "string" || line.trim().length === 0) return [];
  const parsedProtocol = parseDjinnEvents(line);
  const events = parsedProtocol.events.slice();
  for (const diagnostic of parsedProtocol.diagnostics || [])
    events.push(protocolDiagnosticEvent(diagnostic.message, provider));
  const remaining = parsedProtocol.text;
  if (!remaining) return events;

  let record;
  try {
    record = JSON.parse(remaining);
  } catch {
    events.push(textEvent(remaining, provider));
    return events;
  }
  if (!isRecord(record)) {
    events.push(textEvent(remaining, provider));
    return events;
  }
  const providerEvents =
    provider === "codex"
      ? normalizeCodexRecord(record)
      : normalizeClaudeRecord(record);
  return events.concat(providerEvents.flatMap(expandEmbeddedProtocolEvents));
}

function expandEmbeddedProtocolEvents(event) {
  if (event?.type !== "text" || typeof event.data?.text !== "string")
    return [event];
  const parsed = parseDjinnEvents(event.data.text);
  if (parsed.events.length === 0 && !parsed.diagnostics?.length) return [event];
  const result = (parsed.events || []).slice();
  for (const diagnostic of parsed.diagnostics || [])
    result.push(protocolDiagnosticEvent(diagnostic.message, event.data.provider));
  if (parsed.text)
    result.push({ ...event, data: { ...event.data, text: parsed.text } });
  return result;
}

function parseStderrLine(provider, line) {
  validateProvider(provider);
  if (typeof line !== "string" || line.trim().length === 0) return [];
  // stderr is a diagnostic channel, not a lifecycle failure. Explicit provider
  // error records on stdout and the process exit status remain authoritative.
  return [
    {
      type: "note",
      data: {
        title: "Diagnostic du fournisseur",
        detail: line.trim(),
        provider,
        stream: "stderr",
        severity: "warning",
      },
    },
  ];
}

function parseProviderOutput(provider, output) {
  validateProvider(provider);
  boundedString(output, "provider output", MAX_SESSION_LENGTH);
  return output
    .split(/\r?\n/)
    .flatMap((line) => parseProviderLine(provider, line));
}

module.exports = {
  DjinnRuntimeError,
  SESSION_FORMAT,
  SESSION_VERSION,
  PROVIDERS,
  RUN_MODES,
  PROTOCOL_EVENT_TYPES,
  PUBLIC_EVENT_TYPES,
  NATIVE_TOOL_NAMES,
  nativeToolDefinitions,
  MAX_PROMPT_LENGTH,
  MAX_COMPOSED_PROMPT_LENGTH,
  MAX_PATH_LENGTH,
  MAX_TASK_ID_LENGTH,
  MAX_EVENT_TEXT_LENGTH,
  MAX_NATIVE_TOOL_ARGUMENTS_LENGTH,
  MAX_AGENTS,
  MAX_CONCURRENCY,
  MAX_GUIDANCE,
  MAX_GUIDANCE_TEXT_LENGTH,
  MAX_RESOURCE_POLICY_LENGTH,
  ACTION_KINDS,
  MAX_ACTION_TITLE_LENGTH,
  MAX_ACTION_DETAIL_LENGTH,
  MAX_ACTION_URL_LENGTH,
  MAX_ACTION_DIRECTORY_LENGTH,
  validateActionProposal,
  validateNativeTestActionData,
  validateNativeArtifactData,
  validateNativeQuestionData,
  validateNativeReportData,
  validateReportData,
  MAX_IMAGES,
  MAX_IMAGE_BYTES,
  MAX_TOTAL_IMAGE_BYTES,
  MAX_IMAGE_DATA_URL_LENGTH,
  MAX_ARTIFACT_CONTENT_LENGTH,
  MAX_SESSION_LENGTH,
  validateProvider,
  validateTask,
  validateProject,
  validateProjectDirectory,
  workflow,
  validateSession,
  validateState,
  createPortableSession,
  serializeSession,
  parseSessionText,
  validateRunAgent,
  validateResourcePolicy,
  validateGuidance,
  validateSteerInput,
  validateRunImages,
  validateRunInput,
  bindRunToTask,
  scopeProviderEvent,
  composeRunPrompt,
  buildProviderArgs,
  buildClaudeStreamInput,
  buildProviderInvocation,
  buildExecutionPlan,
  validateProtocolData,
  normalizeProtocolEvent,
  parseDjinnEvents,
  parseProviderLine,
  parseStderrLine,
  parseProviderOutput,
};

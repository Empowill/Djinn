"use strict";

const crypto = require("node:crypto");

const MAX_ID = 256;
const MAX_METHOD = 1_000;
const MAX_TITLE = 1_000;
const MAX_REASON = 12_000;
const MAX_COMMAND = 24_000;
const MAX_CWD = 4_096;
const MAX_QUESTION = 12_000;
const MAX_OPTION_LABEL = 1_000;
const MAX_OPTION_DESCRIPTION = 4_000;
const MAX_PATHS = 100;
const MAX_QUESTIONS = 24;
const MAX_OPTIONS = 20;

const CODEX_APPROVAL_METHODS = new Set([
  "item/commandExecution/requestApproval",
  "item/fileChange/requestApproval",
  "item/permissions/requestApproval",
  "applyPatchApproval",
  "execCommandApproval",
]);
const CODEX_INPUT_METHOD = "item/tool/requestUserInput";
const CODEX_ELICITATION_METHOD = "mcpServer/elicitation/request";

function isRecord(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function text(value, fallback = "", max = MAX_REASON) {
  if (typeof value !== "string") return fallback;
  const clean = value.replaceAll("\u0000", "�");
  if (clean.length <= max) return clean;
  if (max <= 1) return clean.slice(0, max);
  return `${clean.slice(0, max - 1)}…`;
}

function idString(value, max = MAX_ID) {
  if (typeof value === "string" && value.length > 0) return text(value, "", max);
  if (typeof value === "number" && Number.isFinite(value)) return String(value);
  return null;
}

function opaqueId() {
  return `permission-${crypto.randomUUID()}`;
}

function specialPathDescription(value) {
  if (!isRecord(value)) return null;
  const kind = typeof value.kind === "string" ? value.kind : "";
  const directPath = typeof value.path === "string" ? value.path : "";
  const subpath = typeof value.subpath === "string" ? value.subpath : "";
  if (!kind && !directPath) return null;
  const label = kind === "unknown" && directPath ? directPath : kind;
  return `<${label}${subpath ? `/${subpath}` : ""}>`;
}

function pathFromValue(value) {
  if (typeof value === "string") return value;
  if (!isRecord(value)) return null;
  if (typeof value.path === "string") return value.path;
  if (isRecord(value.path)) {
    if (typeof value.path.path === "string") return value.path.path;
    if (value.path.type === "special")
      return specialPathDescription(value.path.value);
    if (
      value.path.type === "glob_pattern" &&
      typeof value.path.pattern === "string"
    )
      return `<glob:${value.path.pattern}>`;
  }
  if (value.type === "special") return specialPathDescription(value.value);
  if (value.type === "glob_pattern" && typeof value.pattern === "string")
    return `<glob:${value.pattern}>`;
  if (typeof value.file_path === "string") return value.file_path;
  if (typeof value.filePath === "string") return value.filePath;
  if (typeof value.pathName === "string") return value.pathName;
  return null;
}

function collectPaths(...values) {
  const paths = [];
  const add = (value) => {
    if (paths.length >= MAX_PATHS) return;
    if (Array.isArray(value)) {
      value.forEach(add);
      return;
    }
    const candidate = pathFromValue(value);
    if (candidate && !paths.includes(candidate)) paths.push(text(candidate, "", MAX_CWD));
  };
  values.forEach(add);
  return paths;
}

function collectCodexPermissionPaths(params) {
  const permissions = params?.permissions || params?.additionalPermissions;
  const fileSystem = permissions?.fileSystem;
  return collectPaths(
    fileSystem?.read,
    fileSystem?.write,
    fileSystem?.entries,
    params?.commandActions,
    params?.fileChanges && Object.keys(params.fileChanges),
    params?.grantRoot,
  );
}

function strings(value) {
  if (typeof value === "string" && value.trim()) return [value.trim()];
  if (Array.isArray(value)) return value.flatMap(strings);
  return [];
}

function networkScope(params) {
  const network = params?.permissions?.network || params?.network;
  const context = params?.networkApprovalContext;
  const hosts = [
    ...strings(network?.allowedHosts),
    ...strings(network?.allowed_hosts),
    ...strings(network?.hosts),
    ...strings(context?.allowedHosts),
    ...strings(context?.host),
    ...strings(context?.hostname),
  ];
  const protocols = [
    ...strings(network?.allowedProtocols),
    ...strings(network?.protocols),
    ...strings(context?.protocol),
    ...strings(context?.protocols),
  ];
  const enabled =
    network?.enabled === true ||
    hosts.length > 0 ||
    protocols.length > 0 ||
    (typeof context === "string" && context.trim().length > 0);
  if (!enabled) return null;
  const uniqueHosts = [...new Set(hosts)].slice(0, MAX_PATHS);
  const uniqueProtocols = [...new Set(protocols)].slice(0, MAX_PATHS);
  const details = [];
  if (uniqueHosts.length) details.push(`hôtes : ${uniqueHosts.join(", ")}`);
  if (uniqueProtocols.length)
    details.push(`protocoles : ${uniqueProtocols.join(", ")}`);
  if (!details.length && typeof context === "string" && context.trim())
    details.push(context.trim());
  return `Accès réseau demandé${details.length ? ` (${details.join(" ; ")})` : ""}`;
}

function filesystemScope(params) {
  const fileSystem =
    params?.permissions?.fileSystem || params?.additionalPermissions?.fileSystem;
  if (!isRecord(fileSystem)) return null;
  const details = [];
  const add = (access, value) => {
    if (details.length >= MAX_PATHS) return;
    if (Array.isArray(value)) {
      value.forEach((entry) => add(access, entry));
      return;
    }
    if (isRecord(value) && Array.isArray(value.entries)) {
      value.entries.forEach((entry) => add(access, entry));
      return;
    }
    const candidate = pathFromValue(value);
    const special =
      isRecord(value) &&
      (value.special || value.specialRoot || value.root || value.scope);
    const target = candidate || (typeof special === "string" ? special : "");
    if (!target) return;
    const entryAccess =
      isRecord(value) && value.access !== undefined
        ? strings(value.access).join("/")
        : access;
    const accessLabel =
      { read: "lecture", write: "écriture", readWrite: "lecture/écriture" }[
        entryAccess
      ] || entryAccess;
    details.push(`${accessLabel} : ${text(target, "", MAX_CWD)}`);
  };
  add("lecture", fileSystem.read);
  add("écriture", fileSystem.write);
  add("accès", fileSystem.entries);
  add("accès", fileSystem.specialRoots);
  return details.length
    ? `Accès fichiers demandé (${details.join(" ; ")})`
    : null;
}

function permissionScopeReason(params) {
  return [filesystemScope(params), networkScope(params)]
    .filter(Boolean)
    .join(" · ");
}

function reasonWithScope(reason, params) {
  const scope = permissionScopeReason(params);
  const base = text(reason, "", MAX_REASON);
  if (!scope) return base || undefined;
  return text([base, scope].filter(Boolean).join("\n"), "", MAX_REASON);
}

function hasNetworkScope(params) {
  return Boolean(networkScope(params));
}

function hasFilesystemScope(params) {
  return Boolean(filesystemScope(params));
}

const CLAUDE_SESSION_DESTINATIONS = new Set([
  "session",
  "userSettings",
  "projectSettings",
  "localSettings",
]);

function normalizeClaudePermissionSuggestions(value) {
  if (!Array.isArray(value) || !value.length)
    return { suggestions: [], compatible: false };
  const suggestions = [];
  for (const suggestion of value) {
    if (!isRecord(suggestion)) return { suggestions: [], compatible: false };
    const destination = suggestion.destination;
    if (
      destination !== undefined &&
      (typeof destination !== "string" ||
        !CLAUDE_SESSION_DESTINATIONS.has(destination))
    )
      return { suggestions: [], compatible: false };
    if (suggestion.type === "addRules") {
      if (
        !Array.isArray(suggestion.rules) ||
        !suggestion.rules.length ||
        suggestion.rules.length > MAX_OPTIONS
      )
        return { suggestions: [], compatible: false };
      const rules = suggestion.rules.map((rule) =>
        typeof rule === "string" ? rule.trim() : "",
      );
      // A session grant is useful only for a concrete tool rule. Wildcards,
      // empty rules, and arbitrary patterns could silently become a blanket
      // provider grant when the user clicks the session action.
      if (
        rules.some(
          (rule) =>
            !rule ||
            rule.length > MAX_COMMAND ||
            rule.includes("*") ||
            !/^[A-Za-z][A-Za-z0-9_:-]*\([^\n\r()]+\)$/.test(rule),
        )
      )
        return { suggestions: [], compatible: false };
      suggestions.push({
        type: "addRules",
        rules,
        destination: "session",
      });
      continue;
    }
    if (suggestion.type === "addDirectories") {
      if (
        !Array.isArray(suggestion.directories) ||
        !suggestion.directories.length ||
        suggestion.directories.length > MAX_PATHS
      )
        return { suggestions: [], compatible: false };
      const directories = suggestion.directories.map((directory) =>
        typeof directory === "string" ? directory.trim() : "",
      );
      if (
        directories.some(
          (directory) =>
            !directory ||
            directory.length > MAX_CWD ||
            directory.includes("*") ||
            directory.includes("\0") ||
            ["/", "\\", ".", "~"].includes(directory),
        )
      )
        return { suggestions: [], compatible: false };
      suggestions.push({
        type: "addDirectories",
        directories,
        destination: "session",
      });
      continue;
    }
    // setMode (especially bypassPermissions) and future suggestion kinds are
    // deliberately unsupported for a session card.
    return { suggestions: [], compatible: false };
  }
  return { suggestions, compatible: suggestions.length > 0 };
}

function normalizeOptions(options) {
  if (!Array.isArray(options)) return undefined;
  return options.slice(0, MAX_OPTIONS).flatMap((option) => {
    if (!isRecord(option)) return [];
    const label = text(option.label, "", MAX_OPTION_LABEL);
    const description = text(option.description, "", MAX_OPTION_DESCRIPTION);
    if (!label) return [];
    return [{ label, description }];
  });
}

function normalizeQuestions(questions) {
  if (!Array.isArray(questions)) return undefined;
  const result = questions.slice(0, MAX_QUESTIONS).flatMap((question, index) => {
    if (!isRecord(question)) return [];
    const id = idString(question.id) || `question-${index + 1}`;
    const value = text(question.question || question.prompt, "", MAX_QUESTION);
    if (!value) return [];
    return [
      {
        id,
        question: value,
        ...(normalizeOptions(question.options)
          ? { options: normalizeOptions(question.options) }
          : {}),
      },
    ];
  });
  return result.length ? result : undefined;
}

function requestedSchemaQuestions(schema) {
  if (!isRecord(schema) || !isRecord(schema.properties)) return undefined;
  const required = new Set(Array.isArray(schema.required) ? schema.required : []);
  const supportedTypes = new Set(["string", "number", "integer", "boolean"]);
  const questions = Object.entries(schema.properties)
    .slice(0, MAX_QUESTIONS)
    .map(([id, definition]) => {
      const field = isRecord(definition) ? definition : {};
      if (field.type && !supportedTypes.has(field.type)) return null;
      if (field.type === undefined && !Array.isArray(field.enum)) return null;
      let options;
      if (Array.isArray(field.enum))
        options = field.enum.slice(0, MAX_OPTIONS).map((value) => ({
          label: text(value, String(value), MAX_OPTION_LABEL),
          description: "",
        }));
      return {
        id: text(id, "question", MAX_ID),
        question: text(field.title || field.description, id, MAX_QUESTION),
        ...(options ? { options } : {}),
        ...(required.has(id) ? {} : { optional: true }),
      };
    });
  if (questions.some((question) => question === null)) return undefined;
  return questions.length ? questions : undefined;
}

function baseRequest({ provider, method, nativeId, params, context = {} }) {
  const nativeRequestId = idString(nativeId);
  const request = {
    id: text(context.id, "", MAX_ID) || opaqueId(),
    taskId: text(context.taskId, "", 256),
    runId: text(context.runId, "", 256),
    ...(context.stepId ? { stepId: text(context.stepId, "", 256) } : {}),
    agentId: text(context.agentId || "lead", "lead", 256),
    ...(context.agentName ? { agentName: text(context.agentName, "", 256) } : {}),
    provider,
    method: text(method, "", MAX_METHOD),
    title: "Autorisation requise",
    status: "pending",
    createdAt: context.createdAt || new Date().toISOString(),
    canAcceptForSession: false,
  };
  const native = { id: nativeRequestId, method, params };
  return { request, native };
}

function normalizeCodexRequest(method, nativeId, params, context) {
  if (
    !CODEX_APPROVAL_METHODS.has(method) &&
    method !== CODEX_INPUT_METHOD &&
    method !== CODEX_ELICITATION_METHOD
  )
    return null;
  const result = baseRequest({
    provider: "codex",
    method,
    nativeId,
    params,
    context,
  });
  const { request, native } = result;
  request.reason = reasonWithScope(params?.reason, params);
  request.cwd = text(params?.cwd, "", MAX_CWD) || text(context.cwd, "", MAX_CWD) || undefined;
  request.paths = collectCodexPermissionPaths(params);

  if (method === "item/commandExecution/requestApproval") {
    const kind = text(params?.kind, "commande", 128);
    const kindLabel = {
      command: "la commande",
      read: "la lecture",
      write: "l'écriture",
      network: "l'accès réseau",
    }[kind] || kind;
    request.title = text(`Autoriser ${kindLabel}`, "Autorisation requise", MAX_TITLE);
    request.command = text(params?.command, "", MAX_COMMAND) || undefined;
    request.canAcceptForSession =
      params?.availableDecisions?.includes?.("acceptForSession") ?? true;
  } else if (method === "item/fileChange/requestApproval") {
    request.title = "Autoriser les modifications de fichiers";
    request.canAcceptForSession = true;
  } else if (method === "item/permissions/requestApproval") {
    request.title = hasNetworkScope(params)
      ? hasFilesystemScope(params)
        ? "Accorder l'accès réseau et aux fichiers"
        : "Autoriser l'accès réseau"
      : "Accorder les permissions supplémentaires";
    request.canAcceptForSession = true;
  } else if (method === "execCommandApproval") {
    request.title = "Autoriser l'exécution de la commande";
    request.command = Array.isArray(params?.command)
      ? text(params.command.map((value) => text(value, String(value), MAX_CWD)).join(" "), "", MAX_COMMAND) || undefined
      : text(params?.command, "", MAX_COMMAND) || undefined;
    request.canAcceptForSession = true;
  } else if (method === "applyPatchApproval") {
    request.title = "Autoriser les modifications de fichiers";
    request.canAcceptForSession = true;
  } else if (method === CODEX_INPUT_METHOD) {
    request.title = "Réponse demandée par Codex";
    request.questions = normalizeQuestions(params?.questions);
  } else if (method === CODEX_ELICITATION_METHOD) {
    if (!['form', 'openai/form', 'openaiForm'].includes(params?.mode)) return null;
    request.title = `Réponse demandée par ${text(params?.serverName, "le serveur MCP", 256)}`;
    request.reason = reasonWithScope(
      text(params?.message, "", MAX_REASON) ||
        text(params?.description, "", MAX_REASON) ||
        request.reason,
      params,
    );
    request.questions =
      normalizeQuestions(params?.questions) ||
      requestedSchemaQuestions(params?.requestedSchema);
    if (!request.questions?.length) return null;
  }
  return { request, native };
}

function normalizeClaudeRequest(nativeId, controlRequest, context) {
  if (!isRecord(controlRequest)) return null;
  const request = controlRequest.request;
  if (!isRecord(request) || request.subtype !== "can_use_tool") return null;
  const result = baseRequest({
    provider: "claude",
    method: "control_request",
    nativeId,
    params: controlRequest,
    context,
  });
  const { request: publicRequest, native } = result;
  const input = isRecord(request.input) ? request.input : {};
  const toolName = text(request.tool_name, "tool", 256);
  publicRequest.title = `Autoriser ${toolName}`;
  publicRequest.command =
    text(input.command, "", MAX_COMMAND) || text(input.cmd, "", MAX_COMMAND) || undefined;
  publicRequest.cwd = text(input.cwd, "", MAX_CWD) || text(context.cwd, "", MAX_CWD) || undefined;
  publicRequest.reason = reasonWithScope(
    text(request.reason, "", MAX_REASON) || text(request.description, "", MAX_REASON),
    { ...request, ...input },
  );
  publicRequest.paths = collectPaths(
    input.file_path,
    input.filePath,
    input.path,
    input.paths,
    request.permission_suggestions,
  );
  publicRequest.questions = normalizeQuestions(
    input.questions || request.questions,
  );
  // Claude can return permission_suggestions for a session-scoped grant. Only
  // concrete tool rules/directories are compatible; setMode and unknown
  // suggestions must never expose a session action that could widen all tools.
  const sessionSuggestions = normalizeClaudePermissionSuggestions(
    request.permission_suggestions,
  );
  publicRequest.canAcceptForSession = sessionSuggestions.compatible;
  native.claudeRequest = request;
  native.sessionSuggestions = sessionSuggestions.suggestions;
  native.controlRequest = controlRequest;
  return { request: publicRequest, native };
}

function normalizeNativeRequest({
  provider,
  method,
  nativeId,
  params,
  context,
}) {
  if (provider === "codex")
    return normalizeCodexRequest(method, nativeId, params, context);
  if (provider === "claude")
    return normalizeClaudeRequest(nativeId, params, context);
  return null;
}

function parseClaudeControlRequest(line) {
  if (typeof line !== "string" || !line.trim()) return null;
  let value;
  try {
    value = JSON.parse(line);
  } catch {
    return null;
  }
  if (value?.type !== "control_request") return null;
  if (!idString(value.request_id)) return null;
  if (!isRecord(value.request)) return null;
  return { nativeId: idString(value.request_id), params: value };
}

function answersFor(answers) {
  if (!isRecord(answers)) return {};
  return Object.fromEntries(
    Object.entries(answers)
      .slice(0, MAX_QUESTIONS)
      .flatMap(([key, value]) => {
        if (typeof value !== "string") return [];
        return [[key.slice(0, MAX_ID), text(value, "", MAX_QUESTION)]];
      }),
  );
}

function codexPermissionResponse(entry, decision, answers) {
  const method = entry.method;
  const requested = entry.native.params || {};
  if (method === CODEX_INPUT_METHOD) {
    const values = decision === "decline" ? {} : answersFor(answers);
    return {
      answers: Object.fromEntries(
        (entry.request.questions || []).map((question) => [
          question.id,
          { answers: values[question.id] === undefined ? [] : [values[question.id]] },
        ]),
      ),
    };
  }
  if (method === CODEX_ELICITATION_METHOD) {
    if (decision === "decline") return { action: "decline", content: null };
    const values = answersFor(answers);
    const schema = requestedSchemaQuestions(entry.native.params?.requestedSchema)
      ? entry.native.params?.requestedSchema
      : null;
    const properties = isRecord(schema?.properties) ? schema.properties : {};
    const content = {};
    for (const question of entry.request.questions || []) {
      const value = values[question.id];
      if (value === undefined) continue;
      const definition = isRecord(properties[question.id]) ? properties[question.id] : {};
      if (Array.isArray(definition.enum)) {
        const match = definition.enum.find((candidate) => String(candidate) === value);
        if (match !== undefined) content[question.id] = match;
        continue;
      }
      if (definition.type === "number" || definition.type === "integer") {
        const numeric = Number(value);
        if (Number.isFinite(numeric) &&
            (definition.type !== "integer" || Number.isInteger(numeric)))
          content[question.id] = numeric;
        continue;
      }
      if (definition.type === "boolean") {
        if (value === "true" || value === "false") content[question.id] = value === "true";
        continue;
      }
      content[question.id] = value;
    }
    return { action: "accept", content, _meta: null };
  }
  if (method === "item/permissions/requestApproval") {
    if (decision === "decline")
      return {
        permissions: { fileSystem: { entries: [] }, network: { enabled: false } },
        scope: "turn",
      };
    return {
      permissions: requested.permissions || { fileSystem: { entries: [] }, network: { enabled: false } },
      scope: decision === "acceptForSession" ? "session" : "turn",
    };
  }
  if (method === "applyPatchApproval" || method === "execCommandApproval") {
    return {
      decision:
        decision === "decline"
          ? { denied: { rejection: "Declined in Djinn." } }
          : decision === "acceptForSession"
            ? "approved_for_session"
            : "approved",
    };
  }
  return { decision: decision === "decline" ? "decline" : decision };
}

function claudePermissionResponse(entry, decision, answers) {
  const nativeRequest = entry.native.claudeRequest || {};
  const requestId = entry.native.id;
  if (decision === "decline") {
    return {
      type: "control_response",
      response: {
        subtype: "success",
        request_id: requestId,
        response: { behavior: "deny", message: "Declined in Djinn." },
      },
    };
  }
  const response = {
    behavior: "allow",
    ...(isRecord(nativeRequest.input)
      ? { updatedInput: nativeRequest.input }
      : {}),
  };
  const values = answersFor(answers);
  if (Object.keys(values).length > 0) {
    const originalInput = isRecord(nativeRequest.input)
      ? nativeRequest.input
      : {};
    const questions = Array.isArray(originalInput.questions)
      ? originalInput.questions
      : [];
    // AskUserQuestion validates answers by the original question text, while
    // the Djinn card uses bounded opaque question ids. Translate back to the
    // provider schema instead of putting ids alongside tool input fields.
    const keyedAnswers = {};
    for (const question of entry.request.questions || []) {
      const answer = values[question.id];
      if (answer === undefined) continue;
      const index = (entry.request.questions || []).indexOf(question);
      const original = questions[index];
      const key =
        typeof original?.question === "string" && original.question.length > 0
          ? original.question
          : question.question;
      keyedAnswers[key] = answer;
    }
    response.updatedInput = {
      ...originalInput,
      ...(Object.keys(keyedAnswers).length ? { answers: keyedAnswers } : {}),
    };
  }
  if (decision === "acceptForSession") {
    const sessionSuggestions =
      Array.isArray(entry.native.sessionSuggestions) &&
      entry.native.sessionSuggestions.length
        ? entry.native.sessionSuggestions
        : normalizeClaudePermissionSuggestions(
              nativeRequest.permission_suggestions,
            ).suggestions;
    if (!sessionSuggestions.length) return null;
    // The user-facing session action is ephemeral to this Djinn run. Every
    // compatible provider suggestion is explicitly rewritten to the session
    // destination, so a hostile user/project settings destination cannot
    // persist a grant outside the live provider passage.
    response.updatedPermissions = sessionSuggestions.map((suggestion) => ({
      ...suggestion,
      destination: "session",
    }));
  }
  return {
    type: "control_response",
    response: { subtype: "success", request_id: requestId, response },
  };
}

function buildNativeResponse(entry, decision, answers) {
  if (!entry || !["accept", "acceptForSession", "decline"].includes(decision))
    return null;
  if (entry.provider === "codex")
    return codexPermissionResponse(entry, decision, answers);
  if (entry.provider === "claude")
    return claudePermissionResponse(entry, decision, answers);
  return null;
}

function buildCodexDeclineResponse(method) {
  if (method === CODEX_INPUT_METHOD) return { answers: {} };
  if (method === CODEX_ELICITATION_METHOD)
    return { action: "decline", content: null };
  if (method === "item/permissions/requestApproval")
    return {
      permissions: { fileSystem: { entries: [] }, network: { enabled: false } },
      scope: "turn",
    };
  if (method === "applyPatchApproval" || method === "execCommandApproval")
    return {
      decision: {
        denied: { rejection: "Request is no longer active in Djinn." },
      },
    };
  return { decision: "decline" };
}

function isCodexRequestMethod(method) {
  return CODEX_APPROVAL_METHODS.has(method) ||
    method === CODEX_INPUT_METHOD ||
    method === CODEX_ELICITATION_METHOD;
}

module.exports = {
  CODEX_APPROVAL_METHODS,
  CODEX_INPUT_METHOD,
  CODEX_ELICITATION_METHOD,
  normalizeQuestions,
  normalizeClaudePermissionSuggestions,
  normalizeNativeRequest,
  parseClaudeControlRequest,
  buildNativeResponse,
  buildCodexDeclineResponse,
  isCodexRequestMethod,
  answersFor,
};

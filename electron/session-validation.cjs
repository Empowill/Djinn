// Generated from src/session-validation.ts. Keep both in sync.
"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
exports.validateAction = validateAction;
exports.validatePermission = validatePermission;
exports.validateStepResult = validateStepResult;
exports.validateWorkItem = validateWorkItem;
exports.validateMissionReport = validateMissionReport;
exports.validateTask = validateTask;
exports.validateState = validateState;
const workflow_1 = require("./workflow.cjs");
const i18n_1 = require("./i18n.cjs");
// A function rather than a direct call: validateTask names its record `t`.
const codexWaitReason = () => (0, i18n_1.t)("validation.codex_state_unconfirmed");
const invalid = (field) => {
    throw new Error((0, i18n_1.t)("workflow.invalid_format", { field }));
};
function record(value, field) {
    if (!value || typeof value !== "object" || Array.isArray(value))
        return invalid(field);
    return value;
}
function string(value, field, fallback) {
    if (value === undefined && fallback !== undefined)
        return fallback;
    if (typeof value !== "string")
        return invalid(field);
    return value;
}
function id(value, field) {
    const result = string(value, field);
    if (!result.trim())
        return invalid(field);
    return result;
}
function optionalString(value, field) {
    return value === undefined ? undefined : string(value, field);
}
function boolean(value, field, fallback) {
    if (value === undefined && fallback !== undefined)
        return fallback;
    if (typeof value !== "boolean")
        return invalid(field);
    return value;
}
function number(value, field, min, max) {
    if (typeof value !== "number" ||
        !Number.isFinite(value) ||
        value < min ||
        value > max)
        return invalid(field);
    return value;
}
function date(value, field) {
    const result = string(value, field);
    if (!result || !Number.isFinite(Date.parse(result)))
        return invalid(field);
    return result;
}
function optionalDate(value, field) {
    return value === undefined ? undefined : date(value, field);
}
function choice(value, options, field) {
    if (typeof value !== "string" || !options.includes(value))
        return invalid(field);
    return value;
}
function collection(value, field, parse) {
    if (!Array.isArray(value))
        return invalid(field);
    const result = value.map((entry, index) => parse(record(entry, `${field}.${index}`), `${field}.${index}`));
    if (new Set(result.map((entry) => entry.id)).size !== result.length)
        return invalid(`${field}.ids`);
    return result;
}
function validateAction(value, restored = false) {
    const a = record(value, "action");
    const kind = choice(a.kind, ["server", "link", "manual"], "action.kind");
    let status = choice(a.status, ["pending", "running", "ready", "done", "error", "stopped"], "action.status");
    const url = optionalString(a.url, "action.url");
    const directory = optionalString(a.directory, "action.directory");
    if (directory &&
        (directory.length > 4096 ||
            directory.includes("\0") ||
            /^(?:[\\/]|[A-Za-z]:)/.test(directory) ||
            directory
                .replaceAll("\\", "/")
                .split("/")
                .some((p) => p === ".." || p === ".")))
        return invalid("action.directory");
    if (url) {
        let parsed;
        try {
            parsed = new URL(url);
        }
        catch {
            return invalid("action.url");
        }
        if (!["http:", "https:"].includes(parsed.protocol) ||
            parsed.username ||
            parsed.password)
            return invalid("action.url");
    }
    if (restored && kind === "server" && ["running", "ready"].includes(status))
        status = "stopped";
    const action = {
        stepId: optionalString(a.stepId, "action.stepId"),
        runId: optionalString(a.runId, "action.runId"),
        id: id(a.id, "action.id"),
        kind,
        status,
        title: string(a.title, "action.title"),
        detail: optionalString(a.detail, "action.detail"),
        createdAt: date(a.createdAt, "action.createdAt"),
        updatedAt: date(a.updatedAt, "action.updatedAt"),
        agentId: optionalString(a.agentId, "action.agentId"),
        workItemId: optionalString(a.workItemId, "action.workItemId"),
        target: optionalString(a.target, "action.target"),
        url: restored && kind === "server" ? undefined : url,
        script: optionalString(a.script, "action.script"),
        directory,
        error: optionalString(a.error, "action.error"),
        testStartedAt: optionalDate(a.testStartedAt, "action.testStartedAt"),
    };
    if (a.testInstructions !== undefined) {
        if (!Array.isArray(a.testInstructions) || a.testInstructions.length > 24)
            return invalid("action.testInstructions");
        action.testInstructions = a.testInstructions.map((v) => boundedText(v, "action.testInstructions", 4000));
    }
    if (a.expectedResult !== undefined)
        action.expectedResult = boundedText(a.expectedResult, "action.expectedResult", 4000);
    if (a.testResult !== undefined) {
        const result = record(a.testResult, "action.testResult");
        action.testResult = {
            status: choice(result.status, ["passed", "problem", "deferred"], "action.testResult.status"),
            recordedAt: date(result.recordedAt, "action.testResult.recordedAt"),
            detail: result.detail === undefined
                ? undefined
                : boundedText(result.detail, "action.testResult.detail", 12000),
        };
    }
    if (action.id.length > 256 ||
        action.title.length > 1000 ||
        (action.detail?.length || 0) > 100000 ||
        (action.error?.length || 0) > 12000 ||
        (action.url?.length || 0) > 4096 ||
        (action.script?.length || 0) > 256)
        return invalid("action.length");
    return action;
}
function boundedText(value, field, limit = 4000) {
    const result = string(value, field);
    if (result.length > limit || result.includes("\0"))
        return invalid(field);
    return result;
}
function validatePermission(value, restored = false) {
    const p = record(value, "permission");
    const status = choice(p.status, ["pending", "accepted", "declined", "cancelled"], "permission.status");
    const result = {
        id: id(p.id, "permission.id"),
        taskId: id(p.taskId, "permission.taskId"),
        runId: id(p.runId, "permission.runId"),
        stepId: optionalString(p.stepId, "permission.stepId"),
        agentId: id(p.agentId, "permission.agentId"),
        agentName: optionalString(p.agentName, "permission.agentName"),
        provider: choice(p.provider, ["codex", "claude"], "permission.provider"),
        method: boundedText(p.method, "permission.method", 1000),
        title: boundedText(p.title, "permission.title", 1000),
        reason: p.reason === undefined
            ? undefined
            : boundedText(p.reason, "permission.reason", 12000),
        command: p.command === undefined
            ? undefined
            : boundedText(p.command, "permission.command", 24000),
        cwd: p.cwd === undefined
            ? undefined
            : boundedText(p.cwd, "permission.cwd", 4096),
        status: restored && status === "pending" ? "cancelled" : status,
        createdAt: date(p.createdAt, "permission.createdAt"),
        updatedAt: optionalDate(p.updatedAt, "permission.updatedAt"),
        canAcceptForSession: boolean(p.canAcceptForSession, "permission.canAcceptForSession", false),
    };
    if ([
        result.id,
        result.taskId,
        result.runId,
        result.agentId,
        result.stepId || "",
    ].some((v) => v.length > 256 || v.includes("\0")))
        return invalid("permission.identity");
    if (p.paths !== undefined) {
        if (!Array.isArray(p.paths) || p.paths.length > 100)
            return invalid("permission.paths");
        result.paths = p.paths.map((v) => boundedText(v, "permission.paths", 4096));
    }
    if (p.questions !== undefined) {
        if (!Array.isArray(p.questions) || p.questions.length > 24)
            return invalid("permission.questions");
        result.questions = p.questions.map((value) => {
            const q = record(value, "permission.question");
            const options = q.options;
            if (options !== undefined &&
                (!Array.isArray(options) || options.length > 20))
                return invalid("permission.options");
            return {
                id: boundedText(q.id, "permission.question.id", 256),
                question: boundedText(q.question, "permission.question", 12000),
                ...(q.optional === true ? { optional: true } : {}),
                options: options === undefined
                    ? undefined
                    : options.map((value) => {
                        const o = record(value, "permission.option");
                        return {
                            label: boundedText(o.label, "permission.option.label", 1000),
                            description: boundedText(o.description, "permission.option.description", 4000),
                        };
                    }),
            };
        });
        if (new Set(result.questions.map((q) => q.id)).size !==
            result.questions.length)
            return invalid("permission.questions.ids");
    }
    return result;
}
function validateStepResult(value) {
    return (0, workflow_1.validateStepReport)(value);
}
function validateWorkItem(value) {
    const item = record(value, "workItem");
    return {
        id: boundedText(id(item.id, "workItem.id"), "workItem.id", 256),
        title: boundedText(item.title, "workItem.title", 1000),
        status: choice(item.status, ["pending", "running", "blocked", "ready", "done"], "workItem.status"),
        updatedAt: date(item.updatedAt, "workItem.updatedAt"),
        ...Object.fromEntries(["agentId", "stepId", "runId", "ticket", "worktree", "branch", "detail"]
            .filter((field) => item[field] !== undefined)
            .map((field) => [
            field,
            boundedText(item[field], `workItem.${field}`, field === "detail" ? 4000 : 4096),
        ])),
    };
}
function validateMissionReport(value) {
    const item = record(value, "report");
    return {
        ...validateStepResult(item),
        id: boundedText(id(item.id, "report.id"), "report.id", 256),
        agentId: optionalString(item.agentId, "report.agentId"),
        updatedAt: date(item.updatedAt, "report.updatedAt"),
    };
}
function validateTask(value, restored = true) {
    const t = record(value, "mission");
    const configuration = record(t.configuration, "configuration");
    if (!Array.isArray(configuration.deliverables))
        return invalid("configuration.deliverables");
    const concurrency = number(configuration.concurrency, "configuration.concurrency", 1, workflow_1.MAX_SUB_AGENTS);
    if (!Number.isInteger(concurrency))
        return invalid("configuration.concurrency");
    const questions = collection(t.questions, "questions", (q, p) => ({
        stepId: optionalString(q.stepId, `${p}.stepId`),
        runId: optionalString(q.runId, `${p}.runId`),
        id: id(q.id, `${p}.id`),
        title: string(q.title, `${p}.title`),
        context: string(q.context, `${p}.context`),
        recommendation: string(q.recommendation, `${p}.recommendation`),
        blocking: boolean(q.blocking, `${p}.blocking`),
        blockingScope: q.blockingScope === undefined
            ? undefined
            : choice(q.blockingScope, ["agent", "mission"], `${p}.blockingScope`),
        workItemId: optionalString(q.workItemId, `${p}.workItemId`),
        unlocks: string(q.unlocks, `${p}.unlocks`),
        agentId: optionalString(q.agentId, `${p}.agentId`),
        answer: optionalString(q.answer, `${p}.answer`),
        answeredAt: optionalDate(q.answeredAt, `${p}.answeredAt`),
        theme: optionalString(q.theme, `${p}.theme`),
        options: collection(q.options, `${p}.options`, (o, op) => ({
            id: id(o.id, `${op}.id`),
            label: string(o.label, `${op}.label`),
            description: string(o.description, `${op}.description`, ""),
        })),
    }));
    const scopeList = (value, field) => {
        if (value === undefined)
            return undefined;
        if (!Array.isArray(value) || value.length > 200)
            return invalid(field);
        return value.map((v) => {
            const result = string(v, field)
                .trim()
                .replaceAll("\\", "/")
                .replace(/\/+$/, "");
            if (!result ||
                result.length > 4096 ||
                /[\0\r\n]/.test(result) ||
                (result !== "*" &&
                    (/^(?:[\/]|[A-Za-z]:)/.test(result) ||
                        /[*?\[\]{}]/.test(result) ||
                        result.split("/").some((s) => !s || s === ".." || s === "."))))
                return invalid(field);
            return result;
        });
    };
    const idList = (value, field) => {
        if (value === undefined)
            return undefined;
        if (!Array.isArray(value) || value.length > 200)
            return invalid(field);
        return value.map((v) => id(v, field));
    };
    const resourceProfile = (value, field) => {
        if (value === undefined || value === null)
            return undefined;
        const resources = record(value, field);
        const positive = (raw, resourceField, maximum = 1024) => {
            if (raw === undefined)
                return undefined;
            if (typeof raw !== "number" ||
                !Number.isFinite(raw) ||
                raw <= 0 ||
                raw > maximum)
                return invalid(resourceField);
            return raw;
        };
        const labels = (raw, resourceField) => {
            if (raw === undefined)
                return undefined;
            if (!Array.isArray(raw) || raw.length > 64)
                return invalid(resourceField);
            const result = raw.map((entry) => {
                if (typeof entry !== "string" ||
                    !entry.trim() ||
                    entry.length > 128 ||
                    /[\0\r\n]/.test(entry))
                    return invalid(resourceField);
                return entry.trim();
            });
            return [...new Set(result)];
        };
        const cpu = positive(resources.cpu ?? resources.cpuCores, `${field}.cpu`);
        const memoryMb = positive(resources.memoryMb ?? resources.memory, `${field}.memoryMb`, Number.MAX_SAFE_INTEGER);
        const resourceLabels = labels(resources.labels, `${field}.labels`);
        const requires = labels(resources.requires, `${field}.requires`);
        const excludes = labels(resources.excludes, `${field}.excludes`);
        const exclusive = labels(resources.exclusive, `${field}.exclusive`);
        return {
            ...(cpu === undefined ? {} : { cpu }),
            ...(memoryMb === undefined ? {} : { memoryMb }),
            ...(resourceLabels === undefined ? {} : { labels: resourceLabels }),
            ...(requires === undefined ? {} : { requires }),
            ...(excludes === undefined ? {} : { excludes }),
            ...(exclusive === undefined ? {} : { exclusive }),
        };
    };
    const agents = collection(t.agents, "agents", (a, p) => ({
        stepId: optionalString(a.stepId, `${p}.stepId`),
        runId: optionalString(a.runId, `${p}.runId`),
        id: id(a.id, `${p}.id`),
        name: string(a.name, `${p}.name`),
        origin: a.origin === undefined
            ? undefined
            : choice(a.origin, ["codex"], `${p}.origin`),
        live: a.live === undefined ? undefined : boolean(a.live, `${p}.live`),
        provider: a.provider === undefined
            ? undefined
            : choice(a.provider, ["codex", "claude"], `${p}.provider`),
        providerThreadId: optionalString(a.providerThreadId, `${p}.providerThreadId`),
        parentAgentId: optionalString(a.parentAgentId, `${p}.parentAgentId`),
        activity: optionalString(a.activity, `${p}.activity`),
        writeScope: scopeList(a.writeScope, `${p}.writeScope`),
        dependsOn: idList(a.dependsOn, `${p}.dependsOn`),
        isolation: a.isolation === undefined
            ? undefined
            : choice(a.isolation, ["shared", "worktree"], `${p}.isolation`),
        resources: resourceProfile(a.resources, `${p}.resources`),
        readOnly: a.readOnly === undefined
            ? undefined
            : boolean(a.readOnly, `${p}.readOnly`),
        waitReason: optionalString(a.waitReason, `${p}.waitReason`),
        waitingForAgentIds: idList(a.waitingForAgentIds, `${p}.waitingForAgentIds`),
        role: string(a.role, `${p}.role`),
        model: string(a.model, `${p}.model`, ""),
        status: choice(a.status, ["queued", "running", "blocked", "done", "error"], `${p}.status`),
        summary: string(a.summary, `${p}.summary`),
        progress: number(a.progress, `${p}.progress`, 0, 100),
        prompt: optionalString(a.prompt, `${p}.prompt`),
        worktree: optionalString(a.worktree, `${p}.worktree`),
        branch: optionalString(a.branch, `${p}.branch`),
    })).map((a) => restored && a.status === "running"
        ? a.origin === "codex"
            ? {
                ...a,
                status: "blocked",
                live: false,
                waitReason: codexWaitReason(),
            }
            : { ...a, status: "queued" }
        : a);
    const events = collection(t.events, "events", (e, p) => ({
        stepId: optionalString(e.stepId, `${p}.stepId`),
        id: id(e.id, `${p}.id`),
        time: date(e.time, `${p}.time`),
        type: choice(e.type, [
            "note",
            "agent",
            "decision",
            "tool",
            "error",
            "phase",
            "review",
        ], `${p}.type`),
        title: string(e.title, `${p}.title`),
        detail: string(e.detail, `${p}.detail`),
        agentId: optionalString(e.agentId, `${p}.agentId`),
        lifecycle: e.lifecycle === undefined
            ? undefined
            : choice(e.lifecycle, ["started", "completed", "blocked"], `${p}.lifecycle`),
        runId: optionalString(e.runId, `${p}.runId`),
        worktree: optionalString(e.worktree, `${p}.worktree`),
        branch: optionalString(e.branch, `${p}.branch`),
        actor: e.actor === undefined
            ? undefined
            : choice(e.actor, ["human", "agent"], `${p}.actor`),
        interventionId: optionalString(e.interventionId, `${p}.interventionId`),
    }));
    const artifacts = collection(t.artifacts, "artifacts", (a, p) => ({
        stepId: optionalString(a.stepId, `${p}.stepId`),
        runId: optionalString(a.runId, `${p}.runId`),
        id: id(a.id, `${p}.id`),
        title: string(a.title, `${p}.title`),
        type: choice(a.type, [
            "diagram",
            "wireframe",
            "document",
            "code",
            "screenshot",
            "visualization",
        ], `${p}.type`),
        content: string(a.content, `${p}.content`),
        updatedAt: date(a.updatedAt, `${p}.updatedAt`),
        sourceOfTruth: a.sourceOfTruth === undefined
            ? undefined
            : boolean(a.sourceOfTruth, `${p}.sourceOfTruth`),
        revision: a.revision === undefined
            ? undefined
            : number(a.revision, `${p}.revision`, 1, 1000000),
        baseRevision: a.baseRevision === undefined
            ? undefined
            : number(a.baseRevision, `${p}.baseRevision`, 1, 1000000),
        editedBy: a.editedBy === undefined
            ? undefined
            : choice(a.editedBy, ["human", "agent"], `${p}.editedBy`),
        needsRevalidation: a.needsRevalidation === undefined
            ? undefined
            : boolean(a.needsRevalidation, `${p}.needsRevalidation`),
        revisions: a.revisions === undefined
            ? undefined
            : (() => {
                if (!Array.isArray(a.revisions))
                    return invalid(`${p}.revisions`);
                return a.revisions.map((entry, index) => {
                    const r = record(entry, `${p}.revisions.${index}`);
                    return {
                        revision: number(r.revision, `${p}.revision`, 1, 1000000),
                        content: string(r.content, `${p}.content`),
                        updatedAt: date(r.updatedAt, `${p}.updatedAt`),
                        editedBy: choice(r.editedBy, ["human", "agent"], `${p}.editedBy`),
                    };
                });
            })(),
    }));
    const feedback = collection(t.feedback, "feedback", (f, p) => ({
        stepId: optionalString(f.stepId, `${p}.stepId`),
        runId: optionalString(f.runId, `${p}.runId`),
        id: id(f.id, `${p}.id`),
        artifactId: id(f.artifactId, `${p}.artifactId`),
        text: string(f.text, `${p}.text`),
        x: number(f.x, `${p}.x`, 0, 100),
        y: number(f.y, `${p}.y`, 0, 100),
        resolved: boolean(f.resolved, `${p}.resolved`),
        createdAt: optionalDate(f.createdAt, `${p}.createdAt`),
    }));
    if (feedback.some((f) => !artifacts.some((a) => a.id === f.artifactId)))
        return invalid("feedback.artifactId");
    const status = choice(t.status, ["idle", "running", "waiting", "paused", "done", "error"], "status");
    const project = string(t.project, "project");
    if (project.includes("\0") ||
        project.includes("\n") ||
        project.includes("\r") ||
        (project && !/^(?:\/|[A-Za-z]:[\\/]|\\\\)/.test(project)))
        return invalid("project");
    optionalString(t.runId, "runId");
    const normalized = {
        id: id(t.id, "id"),
        title: string(t.title, "title"),
        brief: string(t.brief, "brief"),
        project,
        provider: choice(t.provider, ["codex", "claude"], "provider"),
        model: string(t.model, "model", ""),
        phase: choice(t.phase, ["brief", "execution", "review", "delivery"], "phase"),
        status: restored && status === "running" ? "paused" : status,
        runId: restored ? undefined : optionalString(t.runId, "runId"),
        createdAt: date(t.createdAt, "createdAt"),
        questions,
        agents,
        events,
        artifacts,
        feedback,
        actions: t.actions === undefined
            ? []
            : collection(t.actions, "actions", (a) => validateAction(a, restored)),
        instructions: t.instructions === undefined
            ? []
            : collection(t.instructions, "instructions", (i, p) => ({
                stepId: optionalString(i.stepId, `${p}.stepId`),
                runId: optionalString(i.runId, `${p}.runId`),
                id: id(i.id, `${p}.id`),
                text: string(i.text, `${p}.text`),
                agentId: optionalString(i.agentId, `${p}.agentId`),
                time: date(i.time, `${p}.time`),
                appliedAt: optionalDate(i.appliedAt, `${p}.appliedAt`),
                status: i.status === undefined
                    ? undefined
                    : choice(i.status, ["queued", "transmitted", "consumed", "prevented"], `${p}.status`),
                reason: optionalString(i.reason, `${p}.reason`),
            })),
        demo: boolean(t.demo, "demo", false),
        planCompleted: boolean(t.planCompleted, "planCompleted", false),
        reviewApprovedAt: optionalDate(t.reviewApprovedAt, "reviewApprovedAt"),
        runMode: t.runMode === undefined
            ? undefined
            : choice(t.runMode, ["plan", "execute", "review"], "runMode"),
        configuration: {
            prototype: string(configuration.prototype, "configuration.prototype"),
            review: string(configuration.review, "configuration.review"),
            deliverables: configuration.deliverables.map((entry, index) => string(entry, `configuration.deliverables.${index}`)),
            concurrency,
        },
    };
    if (t.agentHistory !== undefined) {
        const history = record(t.agentHistory, "agentHistory");
        if (Object.keys(history).length > 100)
            return invalid("agentHistory");
        normalized.agentHistory = {};
        for (const [stepId, agents] of Object.entries(history)) {
            if (!Array.isArray(t.steps) ||
                !t.steps.some((s) => record(s, "step").id === stepId))
                return invalid("agentHistory.stepId");
            normalized.agentHistory[stepId] = validateTask({ ...t, agentHistory: undefined, agents }, restored).agents;
        }
    }
    if (t.permissions !== undefined) {
        if (!Array.isArray(t.permissions) || t.permissions.length > 200)
            return invalid("permissions");
        normalized.permissions = t.permissions.map((p) => validatePermission(p, restored));
        if (normalized.permissions.some((p) => p.taskId !== normalized.id) ||
            new Set(normalized.permissions.map((p) => p.id)).size !==
                normalized.permissions.length)
            return invalid("permissions.identity");
    }
    if (t.stepResult !== undefined)
        normalized.stepResult = validateStepResult(t.stepResult);
    for (const field of ["workItems", "reports"]) {
        if (t[field] === undefined)
            continue;
        if (!Array.isArray(t[field]) || t[field].length > 200)
            return invalid(field);
        Object.assign(normalized, {
            [field]: collection(t[field], field, (value) => field === "workItems"
                ? validateWorkItem(value)
                : validateMissionReport(value)),
        });
    }
    if (t.providerSessions !== undefined) {
        const sessions = record(t.providerSessions, "providerSessions");
        if (Object.keys(sessions).length > 200)
            return invalid("providerSessions");
        normalized.providerSessions = Object.fromEntries(Object.entries(sessions).map(([key, value]) => {
            if (key.length > 512 ||
                ["__proto__", "constructor", "prototype"].includes(key))
                return invalid("providerSessions.key");
            const threadId = id(value, "providerSessions.threadId");
            if (threadId.length > 256)
                return invalid("providerSessions.threadId");
            return [key, threadId];
        }));
    }
    if (t.runtimeEventIds !== undefined) {
        if (!Array.isArray(t.runtimeEventIds) || t.runtimeEventIds.length > 100000)
            return invalid("runtimeEventIds");
        normalized.runtimeEventIds = t.runtimeEventIds.map((v) => {
            const value = id(v, "runtimeEventIds");
            if (value.length > 256)
                return invalid("runtimeEventIds");
            return value;
        });
    }
    // Legacy records keep no invented step scope; v2 records retain their exact identities.
    const workflowFields = Object.fromEntries([
        "steps",
        "activeStepId",
        "selectedStepId",
        "projectId",
        "projectSnapshot",
        "progressionPolicy",
        "workflowMode",
        "workflowOrigin",
        "nextStepProposal",
        "initialStepProposal",
        "initialWorkflowProposal",
        "workflowProposal",
        "workflowAmendment",
        "titleSource",
        "titleGeneratedAt",
        "titleEditedAt",
        "legacyHistory",
    ]
        .filter((key) => t[key] !== undefined)
        .map((key) => [key, t[key]]));
    return (0, workflow_1.validateTaskWorkflow)({ ...normalized, ...workflowFields }, restored);
}
function validateState(value, restored = true) {
    const s = record(value, "espace");
    if (![1, 2].includes(s.version) || !Array.isArray(s.tasks))
        return invalid("espace.version/tasks");
    if (s.version === 2 &&
        s.tasks.some((task) => !task || typeof task !== "object" || !Array.isArray(task.steps)))
        return invalid("espace.tasks.steps");
    const tasks = s.tasks.map((task) => validateTask(task, restored));
    if (new Set(tasks.map((t) => t.id)).size !== tasks.length)
        return invalid("espace.tasks.ids");
    const settings = s.settings === undefined ? {} : record(s.settings, "preferences");
    const selectedId = string(s.selectedId, "selectedId", tasks[0]?.id || "");
    const projects = s.projects === undefined
        ? []
        : collection(s.projects, "projects", (p) => (0, workflow_1.validateProject)(p));
    for (const task of tasks) {
        if (task.projectSnapshot &&
            !projects.some((p) => p.id === task.projectId)) {
            if (s.version === 2 && s.projects !== undefined)
                return invalid("projectId");
            const { capturedAt: _capturedAt, ...project } = task.projectSnapshot;
            projects.push(project);
        }
        if (task.projectId && !projects.some((p) => p.id === task.projectId))
            return invalid("projectId");
    }
    return {
        version: 2,
        projects,
        tasks,
        selectedId: tasks.some((t) => t.id === selectedId)
            ? selectedId
            : tasks[0]?.id || "",
        settings: {
            provider: settings.provider === undefined
                ? "codex"
                : choice(settings.provider, ["codex", "claude"], "preferences.provider"),
            model: string(settings.model, "preferences.model", ""),
            reduceMotion: boolean(settings.reduceMotion, "preferences.reduceMotion", false),
            sound: boolean(settings.sound, "preferences.sound", false),
        },
    };
}

import type { Task, NativeRuntimeRun } from "./types";
import { t } from "./i18n";

export function reconnectNativeRun(
  task: Task,
  run: NativeRuntimeRun,
): Task | undefined {
  const stage = task.steps?.find((s) => s.id === run.stepId);
  if (task.id !== run.taskId || !stage) return undefined;
  const agents = [...task.agents];
  for (const observed of run.observedAgents || []) {
    if (observed.origin !== "codex") continue;
    const index = agents.findIndex((a) => a.id === observed.id);
    const old = agents[index];
    const record = {
      ...old,
      ...observed,
      model: observed.model || old?.model || "",
      stepId: stage.id,
      runId: observed.runId || run.runId,
      progress: observed.status === "done" ? 100 : old?.progress || 0,
      summary:
        observed.summary || old?.summary || t("wish_view.codex_activity"),
    };
    if (index < 0) agents.push(record);
    else agents[index] = record;
  }
  for (const active of run.activeAgents) {
    const index = agents.findIndex((a) => a.id === active.id);
    const old = agents[index];
    const agent = {
      ...old,
      id: active.id,
      name: active.name,
      role: active.task,
      model:
        active.model ??
        old?.model ??
        (active.origin === "codex" ? "" : task.model || task.provider),
      status: active.status || ("running" as const),
      summary: old?.summary || active.task,
      progress: old?.progress || 0,
      stepId: stage.id,
      runId: active.runId,
      readOnly: active.readOnly,
      origin: active.origin || old?.origin,
      provider: active.provider || old?.provider,
      providerThreadId: active.providerThreadId || old?.providerThreadId,
      parentAgentId: active.parentAgentId || old?.parentAgentId,
      activity: active.activity || old?.activity,
      waitReason: undefined,
      waitingForAgentIds: [],
    };
    if (index < 0) agents.push(agent);
    else agents[index] = agent;
  }
  return {
    ...task,
    runId: run.runId,
    runMode: run.mode,
    status:
      run.status === "stopping"
        ? "paused"
        : task.questions.some(
              (q) => q.stepId === stage.id && q.blocking && !q.answer,
            )
          ? "waiting"
          : "running",
    activeStepId: stage.id,
    agents,
    steps: task.steps?.map((s) =>
      s.id === stage.id
        ? {
            ...s,
            status: run.status === "stopping" ? "paused" : "running",
            startedAt: s.startedAt || run.startedAt,
          }
        : s,
    ),
    activity: {
      lead: run.phase === "workers" ? "supervises" : "integrates",
      activeAgents: run.activeAgents,
      lastActivityAt: run.lastActivityAt,
    },
  };
}
export function stepView(
  task: Task,
  stepId = task.selectedStepId || task.activeStepId,
): Task {
  if (!stepId) return task;
  const belongs = (item: { stepId?: string }) => item.stepId === stepId;
  return {
    ...task,
    questions: task.questions.filter(
      (item) => belongs(item) || (stepId === task.activeStepId && !item.answer),
    ),
    agents: (stepId === task.activeStepId
      ? task.agents
      : task.agentHistory?.[stepId] || []
    ).filter((a) => !a.stepId || belongs(a)),
    events: task.events.filter(belongs),
    artifacts: task.artifacts.filter(belongs),
    feedback: task.feedback.filter(belongs),
    // An independent recipe stays usable while later stages and sibling agents run.
    actions: task.actions?.filter(
      (item) =>
        belongs(item) ||
        (stepId === task.activeStepId &&
          item.status !== "done" &&
          item.testResult?.status !== "passed"),
    ),
    instructions: task.instructions?.filter(belongs),
  };
}
export function mergeStepView(previous: Task, edited: Task): Task {
  const scope = edited.selectedStepId || edited.activeStepId;
  const next = { ...previous, ...edited };
  for (const key of [
    "questions",
    "events",
    "artifacts",
    "feedback",
    "actions",
    "instructions",
  ] as const) {
    const editedIds = new Set((edited[key] || []).map((i) => i.id));
    const retained = (previous[key] || []).filter(
      (item) => item.stepId !== scope || !editedIds.has(item.id),
    );
    const updated = (edited[key] || []).map((item) => {
      const live = (previous[key] || []).find((i) => i.id === item.id);
      return {
        ...item,
        ...((key === "events" || key === "instructions") && live ? live : {}),
        stepId: item.stepId || scope,
      };
    });
    Object.assign(next, { [key]: [...retained, ...updated] });
  }
  if (scope !== previous.activeStepId) {
    next.agents = previous.agents;
    next.agentHistory = {
      ...previous.agentHistory,
      [scope || ""]: edited.agents,
    };
  }
  next.runId = previous.runId;
  next.activity = previous.activity;
  next.providerSessions = previous.providerSessions;
  next.runtimeEventIds = previous.runtimeEventIds;
  next.permissions = previous.permissions;
  next.stepResult = previous.stepResult;
  if (previous.runId) {
    next.status = previous.status;
    next.steps = previous.steps?.map((s) => ({
      ...s,
      needsRevalidation:
        s.needsRevalidation ||
        edited.steps?.find((e) => e.id === s.id)?.needsRevalidation,
    }));
    next.agents = next.agents.map((a) => {
      const live = previous.agents.find((p) => p.id === a.id);
      return live
        ? {
            ...a,
            status: live.status,
            progress: live.progress,
            runId: live.runId,
            summary: live.summary,
          }
        : a;
    });
  }
  return next;
}

export function legacyView(task: Task): Task {
  const legacy = (item: { stepId?: string }) => !item.stepId;
  return {
    ...task,
    questions: task.questions.filter(legacy),
    agents: task.agents.filter(legacy),
    events: task.events.filter(legacy),
    artifacts: task.artifacts.filter(legacy),
    feedback: task.feedback.filter(legacy),
    actions: task.actions?.filter(legacy),
    instructions: task.instructions?.filter(legacy),
  };
}

import type { RuntimeEvent, Task } from "./types";
import { validateTask, validateWorkItem, validateMissionReport, validateStepResult, validateAction } from "./session-validation";

/** Recover acknowledged publications without replaying lifecycle or starting work. */
export function restoreMissionInteractions(task: Task, events: RuntimeEvent[]): Task {
  let next = task;
  for (const event of events) {
    if (event.taskId !== task.id || !event.data || typeof event.data !== "object") continue;
    const data = event.data as Record<string, unknown>;
    const stepId = event.stepId || (typeof data.stepId === "string" ? data.stepId : undefined);
    if (stepId && !task.steps?.some((step) => step.id === stepId)) continue;
    const scope = { stepId, runId: event.runId || undefined };
    const rootRunId = typeof data.parentRunId === "string" ? data.parentRunId : event.runId || undefined;
    // A superseded pass must not replace the current stage's live result.
    if (stepId === task.activeStepId && task.runId && rootRunId !== task.runId) continue;
    try {
      if (event.type === "work_item") {
        const item = validateWorkItem({ ...data, agentId: data.assignedAgentId || data.agentId, ...scope, updatedAt: event.timestamp });
        const prior = next.workItems?.find((old) => old.id === item.id);
        if (!prior || Date.parse(item.updatedAt) > Date.parse(prior.updatedAt)) next = { ...next, workItems: [...(next.workItems || []).filter((old) => old.id !== item.id), item].slice(-200) };
      } else if (event.type === "report") {
        const report = validateMissionReport({ ...data, ...scope, updatedAt: event.timestamp });
        const prior = next.reports?.find((old) => old.id === report.id);
        if (!prior || Date.parse(report.updatedAt) > Date.parse(prior.updatedAt)) next = { ...next, reports: [...(next.reports || []).filter((old) => old.id !== report.id), report].slice(-200) };
      } else if (event.type === "question") {
        if (next.questions.some((question) => question.id === data.id)) continue;
        next = validateTask({ ...next, questions: [...next.questions, {
          ...data, ...scope, context: data.context || data.question || "", recommendation: data.recommendation || "", options: data.options || [], blocking: data.blocking !== false, unlocks: data.unlocks || "",
        }] }, false);
      } else if (event.type === "action") {
        const action = validateAction({ ...data, ...scope }, true);
        if (!next.actions?.some((prior) => prior.id === action.id)) next = { ...next, actions: [...(next.actions || []), action].slice(-100) };
      } else if (event.type === "artifact") {
        const prior = next.artifacts.find((artifact) => artifact.id === data.id);
        if (prior && (prior.editedBy === "human" || Date.parse(prior.updatedAt) >= Date.parse(event.timestamp))) continue;
        next = validateTask({ ...next, artifacts: [...next.artifacts.filter((artifact) => artifact.id !== data.id), {
          ...data, ...scope, type: data.type === "markdown" ? "document" : data.type, updatedAt: event.timestamp,
        }] }, false);
      } else if (event.type === "step_result" && (!data.agentId || data.agentId === "lead")) {
        const result = validateStepResult({ ...data, stepId, runId: rootRunId, reportedAt: event.timestamp });
        const step = next.steps?.find((step) => step.id === stepId);
        if (!step || (step.report && (!step.report.reportedAt || Date.parse(step.report.reportedAt) >= Date.parse(event.timestamp))) || (step.approvedAt && Date.parse(event.timestamp) > Date.parse(step.approvedAt))) continue;
        next = { ...next, steps: next.steps?.map((step) => step.id === stepId ? { ...step, report: result } : step), ...(stepId === next.activeStepId && (!next.stepResult?.reportedAt || Date.parse(event.timestamp) > Date.parse(next.stepResult.reportedAt)) ? { stepResult: result } : {}) };
      }
    } catch {
      // Invalid old publications remain in the journal; never compromise the saved mission.
    }
  }
  return next;
}

/** Compatibility for complete reports that older runtimes left as plain text. */
export function restoreLegacyReports(task: Task): Task {
  const publications: RuntimeEvent[] = [];
  for (const entry of task.events) {
    // Older native text events recorded the agent/run but omitted actor.
    // Explicit human notes and anonymous quotations are never publications.
    const nativeOrigin = entry.actor === "agent" ||
      (!entry.actor && Boolean(entry.agentId) && Boolean(entry.runId));
    if (!nativeOrigin || entry.type !== "note") continue;
    for (const line of entry.detail.split("\n")) {
      if (!line.trim().startsWith("DJINN_EVENT:")) continue;
      try {
        const value = JSON.parse(line.trim().slice("DJINN_EVENT:".length));
        if (value.type !== "artifact" || !value.data || typeof value.data !== "object") continue;
        publications.push({ taskId: task.id, stepId: entry.stepId, runId: entry.runId || "", timestamp: entry.time, type: "artifact", data: value.data });
      } catch { /* Partial streaming frames cannot be recovered. */ }
    }
  }
  return restoreMissionInteractions(task, publications);
}

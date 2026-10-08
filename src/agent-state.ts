import type { Agent, Task } from "./types";
import { t } from "./i18n";

/** Runtime lifecycle is preserved; unresolved interactions explain its visible state. */
export function agentDisplayState(task: Task, agent: Agent) {
  const permission = task.permissions?.find(
    (request) => request.status === "pending" && request.agentId === agent.id,
  );
  const question = task.questions.find(
    (question) =>
      question.blocking &&
      !question.answer &&
      (question.agentId || "lead") === agent.id,
  );
  const labels = {
    queued: t("agent_state.queued"),
    running: t("agent_state.running"),
    blocked: t("agent_state.blocked"),
    done: t("agent_state.done"),
    error: t("agent_state.error"),
  };
  return {
    status: permission || question ? ("blocked" as const) : agent.status,
    label: permission
      ? t("agent_state.awaiting_permission")
      : question
        ? t("agent_state.awaiting_answer")
        : labels[agent.status],
    detail:
      permission?.title ||
      question?.title ||
      agent.waitReason ||
      agent.activity ||
      agent.summary,
    permission,
    question,
  };
}

import type { Agent, Task } from "./types";

/** Runtime lifecycle is preserved; unresolved interactions explain its visible state. */
export function agentDisplayState(task: Task, agent: Agent) {
  const permission = task.permissions?.find((request) => request.status === "pending" && request.agentId === agent.id);
  const question = task.questions.find((question) => question.blocking && !question.answer && (question.agentId || "lead") === agent.id);
  const labels = { queued: "En attente", running: "En cours", blocked: "Bloqué", done: "Terminé", error: "Erreur" };
  return {
    status: permission || question ? "blocked" as const : agent.status,
    label: permission ? "En attente d’autorisation" : question ? "En attente de réponse" : labels[agent.status],
    detail: permission?.title || question?.title || agent.waitReason || agent.activity || agent.summary,
    permission,
    question,
  };
}

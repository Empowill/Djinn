import type { AppState, StepStatus, Task } from "./types";

export type StepCompletionAlert = {
  taskId: string;
  stepId: string;
  questionId: string;
  title: string;
  body: string;
};

/** Observe live transitions; boot/import never replays historical completions. */
export class StepCompletionTracker {
  private previous = new Map<string, Map<string, StepStatus>>();

  sync(tasks: Task[]): StepCompletionAlert[] {
    const alerts: StepCompletionAlert[] = [];
    const next = new Map<string, Map<string, StepStatus>>();
    for (const task of tasks) {
      const previous = this.previous.get(task.id);
      next.set(
        task.id,
        new Map(task.steps?.map((step) => [step.id, step.status])),
      );
      if (task.demo || !previous) continue;
      for (const step of task.steps || []) {
        if (
          previous.get(step.id) !== "running" ||
          !["completed", "awaiting_human"].includes(step.status)
        )
          continue;
        // The native notification channel accepts bounded target identifiers.
        const questionId = `step:${step.id}`;
        if (questionId.length > 256) continue;
        alerts.push({
          taskId: task.id,
          stepId: step.id,
          questionId,
          title: "Étape terminée",
          body: `${task.title} — ${step.title}${step.status === "awaiting_human" ? " · Résultat à valider" : ""}`.slice(
            0,
            1000,
          ),
        });
      }
    }
    this.previous = next;
    return alerts;
  }
}

export function selectCompletedStep(
  state: AppState,
  taskId: string,
  stepId: string,
): AppState {
  const task = state.tasks.find((task) => task.id === taskId);
  if (!task) return state;
  const step = task.steps?.find(
    (step) => step.id === stepId && step.status !== "pending",
  );
  return {
    ...state,
    selectedId: task.id,
    tasks: step
      ? state.tasks.map((item) =>
          item.id === taskId ? { ...item, selectedStepId: step.id } : item,
        )
      : state.tasks,
  };
}

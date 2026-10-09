// Small readings of the protos for the screens: dates, states, answers. They compute nothing the lamp decides.
import { type Timestamp, timestampDate } from "@bufbuild/protobuf/wkt";

import {
  Allowance,
  Choice,
  type Project,
  type Question,
  type Task,
  TaskStatus,
  type Wish,
  WishState,
} from "../../gen/ts/plan/v1/plan_pb";
import { type TextKey, language, t } from "../i18n";

// A djinn grants three wishes at a time, never more. The lamp refuses a fourth; the page only says so beforehand.
export const MAX_ACTIVE = 3;

export function date(ts?: Timestamp): Date | undefined {
  return ts ? timestampDate(ts) : undefined;
}

// when says a time the way the page does: the day and the hour.
export function when(ts?: Timestamp): string {
  const d = date(ts);
  if (!d) return "";
  return d.toLocaleString(language, {
    day: "numeric",
    month: "short",
    hour: "2-digit",
    minute: "2-digit",
  });
}

// A wish stored before states is active.
export function isActive(wish: Wish): boolean {
  return (
    wish.state === WishState.ACTIVE || wish.state === WishState.UNSPECIFIED
  );
}

export function wishStateText(wish: Wish): string {
  if (wish.state === WishState.GRANTED) return t("wish.state_granted");
  if (wish.state === WishState.PAUSED) return t("wish.state_paused");
  return t("wish.state_active", { rank: wish.rank });
}

const taskStatusKeys: Record<TaskStatus, TextKey> = {
  [TaskStatus.UNSPECIFIED]: "task.status_pending",
  [TaskStatus.PENDING]: "task.status_pending",
  [TaskStatus.RUNNING]: "task.status_running",
  [TaskStatus.DONE]: "task.status_done",
  [TaskStatus.FAILED]: "task.status_failed",
  [TaskStatus.STOPPED]: "task.status_stopped",
  [TaskStatus.INTERRUPTED]: "task.status_interrupted",
  [TaskStatus.WAITING]: "task.status_waiting",
  [TaskStatus.PAUSED]: "task.status_paused",
  [TaskStatus.RESUMING]: "task.status_resuming",
};

// taskStatusText names where a task stands: a task Djinn resumes once its provider's limit resets waits for the
// limit; one cut short and resumed as another task (forkedAs) says which.
export function taskStatusText(
  task: Pick<Task, "status" | "resumeAfter">,
  forkedAs = "",
): string {
  if (task.status === TaskStatus.RESUMING && task.resumeAfter)
    return t("task.status_limit");
  if (task.status === TaskStatus.INTERRUPTED && forkedAs)
    return t("task.status_forked", { task: forkedAs });
  return t(taskStatusKeys[task.status]);
}

// taskTone is the class of a task's dot, as the sidebar's mission dots are styled. A task cut short and resumed as
// another one (forkedAs) is no error any more.
export function taskTone(status: TaskStatus, forkedAs = ""): string {
  if (status === TaskStatus.INTERRUPTED && forkedAs) return "idle";
  switch (status) {
    case TaskStatus.RUNNING:
    case TaskStatus.RESUMING:
      return "running";
    case TaskStatus.DONE:
      return "done";
    case TaskStatus.FAILED:
    case TaskStatus.INTERRUPTED:
      return "error";
    case TaskStatus.WAITING:
      return "waiting";
    default:
      return "idle";
  }
}

export function taskFinished(status: TaskStatus): boolean {
  return (
    status === TaskStatus.DONE ||
    status === TaskStatus.FAILED ||
    status === TaskStatus.STOPPED ||
    status === TaskStatus.INTERRUPTED
  );
}

// The letter of an option: A for the first one.
export function letter(index: number): string {
  return String.fromCharCode(65 + index);
}

// The choice that answers with an option, by its index.
export function choiceOf(index: number): Choice {
  return Choice.A + index;
}

// answerText says what was chosen: yes, or the option's letter and text.
export function answerText(question: Question): string {
  const choice = question.answer?.choice ?? Choice.UNSPECIFIED;
  if (choice === Choice.UNSPECIFIED) return "";
  if (choice === Choice.YES) return t("question.yes");
  const index = choice - Choice.A;
  const option = question.options[index];
  return option ? `${letter(index)} · ${option}` : letter(index);
}

export function isOpen(question: Question): boolean {
  return !question.answer;
}

export function allowanceOf(wish: Wish, projectId: string): Allowance {
  const found = wish.allowances.find(
    (a) => a.projectId.toLowerCase() === projectId.toLowerCase(),
  );
  return found?.allowance || Allowance.NONE;
}

export function projectsOf(wish: Wish, projects: Project[]): Project[] {
  return wish.projectIds
    .map((id) => projects.find((p) => p.id.toLowerCase() === id.toLowerCase()))
    .filter((p): p is Project => !!p);
}

// usd says a cost in dollars, to the cent.
export function usd(cost: number): string {
  return new Intl.NumberFormat(language, {
    style: "currency",
    currency: "USD",
    maximumFractionDigits: cost < 1 ? 3 : 2,
  }).format(cost);
}

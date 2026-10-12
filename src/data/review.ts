// What a wish's Review tab lists: its merged work, each task checked once its diff was read (MarkKind.REVIEWED, kept on
// the task through MarkService.Put). The corrections and reviews Djinn started sit under the task they serve, as the
// Tasks tab groups them (mergeNest).
import {
  IntegrationState,
  type Mark,
  MarkKind,
  type Task,
  TaskKind,
  TaskStatus,
} from "../../gen/ts/plan/v1/plan_pb";
import { mergeNest } from "./flight";
import { markOf } from "./format";

// A row of the Review tab: a task, and the merges that serve it, both to review.
export interface ReviewRow {
  task: Task;
  merges: Task[];
}

// toReview tells merged work: a task, not an azima, done, whose branch is in its wish's integration branch.
export function toReview(task: Task): boolean {
  return (
    task.kind !== TaskKind.AZIMA &&
    task.status === TaskStatus.DONE &&
    task.integration?.state === IntegrationState.COMMITTED &&
    task.branch !== ""
  );
}

// reviewedMark is the task's reviewed mark: who checked it, when; none until it was.
export function reviewedMark(task: Task): Mark | undefined {
  return markOf(task, MarkKind.REVIEWED);
}

// reviewRows are the rows of the Review tab, in the order of the tasks: each merge to review under the task it
// serves, when that task is to review too; else in a row of its own.
export function reviewRows(tasks: readonly Task[]): ReviewRow[] {
  const nest = mergeNest(tasks);
  const listed = new Set(tasks.filter(toReview).map((x) => x.id));
  const rows: ReviewRow[] = [];
  for (const task of tasks) {
    if (!listed.has(task.id)) continue;
    const host = nest.host.get(task.id);
    if (host && listed.has(host)) continue;
    const merges = (nest.under.get(task.id) ?? []).filter((x) =>
      listed.has(x.id),
    );
    rows.push({ task, merges });
  }
  return rows;
}

// rowReviewed tells a row all checked: its task and its merges.
export function rowReviewed(row: ReviewRow): boolean {
  return !!reviewedMark(row.task) && row.merges.every((x) => reviewedMark(x));
}

// reviewLeft is how many tasks of the rows are not checked yet, merges counted.
export function reviewLeft(rows: readonly ReviewRow[]): number {
  let left = 0;
  for (const row of rows)
    for (const task of [row.task, ...row.merges])
      if (!reviewedMark(task)) left++;
  return left;
}

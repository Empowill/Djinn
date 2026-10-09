// The flight plan of the active wishes, merged from what the store read of each: one list of questions, of what
// waits, of what runs, of the decisions. Every item keeps its wish. The order is the page's (the Go page of a
// wish sorts the same way); what the lamp decides (a wish's rank, whether it is ready) comes from the lamp.
import {
  type Question,
  type Task,
  TaskStatus,
  type Usage,
  type Wish,
} from "../../gen/ts/plan/v1/plan_pb";
import { type Decision, decisionsOf, later } from "./decisions";
import { investigating, isOpen, waitsForYou, watcherRuns } from "./format";
import type { WishDetail } from "./store";

export interface Item<T> {
  wish: Wish;
  item: T;
}

export interface OpenQuestion extends Item<Question> {
  // The codes of the tasks that wait for this answer: such a question comes first.
  blocking: string[];
}

// Something that waits for the user and is not a question: a worker that waits for an answer, one cut short.
export interface Waiting extends Item<Task> {
  // The code of the question the task waits for, when it is open.
  question: string;
}

export interface FlightPlan {
  // The questions that wait for your answer.
  questions: OpenQuestion[];
  // The questions you asked to investigate: they wait for the lead.
  investigating: OpenQuestion[];
  waiting: Waiting[];
  // The wishes Djinn proposes to grant.
  ready: Wish[];
  running: Item<Task>[];
  // The tasks that move or wait, by status, then those done or stopped, the latest ended first: the Tasks tab.
  moving: Item<Task>[];
  finished: Item<Task>[];
  // Every decision of the wishes, the latest first: the Decisions tab.
  decisions: Item<Decision>[];
  // Read for every wish shown.
  loaded: boolean;
}

// blocking maps a question to the codes of the tasks that wait for its answer.
export function blocking(tasks: readonly Task[]): Map<string, string[]> {
  const out = new Map<string, string[]>();
  for (const task of tasks) {
    if (task.status !== TaskStatus.WAITING || !task.editQuestionId) continue;
    out.set(task.editQuestionId, [
      ...(out.get(task.editQuestionId) ?? []),
      task.code,
    ]);
  }
  return out;
}

const time = (ts?: { seconds: bigint; nanos: number }) =>
  ts ? Number(ts.seconds) * 1000 + ts.nanos / 1e6 : 0;

// finishedTask tells a finished task: done, stopped on request, or cut short and taken over by a fork among tasks, as
// the wish's page and brief say. Every other one still moves, or waits for someone.
export function finishedTask(task: Task, tasks: readonly Task[] = []): boolean {
  return (
    task.status === TaskStatus.DONE ||
    task.status === TaskStatus.STOPPED ||
    (task.status === TaskStatus.INTERRUPTED && forkedAs(task, tasks) !== "")
  );
}

// newestEnded orders finished tasks the latest ended first, then the latest created, as the wish's page and brief do.
export function newestEnded(a: Task, b: Task): number {
  return (
    time(b.endTime) - time(a.endTime) || time(b.createTime) - time(a.createTime)
  );
}

// finishedTasks are the tasks at the bottom of the Tasks tab, the latest ended first.
export function finishedTasks(tasks: readonly Task[]): Task[] {
  return tasks.filter((x) => finishedTask(x, tasks)).sort(newestEnded);
}

// The order of the tasks that move or wait: what runs or broke first (running, cut short, failed, resuming), then
// what waits (waiting, paused, a watcher that watches), the planned ones last. A status this page does not name yet
// comes before the planned ones.
const motion = [
  TaskStatus.RUNNING,
  TaskStatus.INTERRUPTED,
  TaskStatus.FAILED,
  TaskStatus.RESUMING,
  TaskStatus.WAITING,
  TaskStatus.PAUSED,
];
const motionRank = (task: Task) => {
  if (
    task.status === TaskStatus.PENDING ||
    task.status === TaskStatus.UNSPECIFIED
  )
    return motion.length + 2;
  // A running watcher watches: it waits with the paused ones.
  if (watcherRuns(task)) return motion.length;
  const i = motion.indexOf(task.status);
  return i < 0 ? motion.length + 1 : i;
};

// byMotion orders the tasks that move or wait by status; a sort keeps the order within a status.
export function byMotion(a: Task, b: Task): number {
  return motionRank(a) - motionRank(b);
}

// movingTasks are the tasks at the top of the Tasks tab: what moves or waits for someone, by status.
export function movingTasks(tasks: readonly Task[]): Task[] {
  return tasks.filter((x) => !finishedTask(x, tasks)).sort(byMotion);
}

// openQuestions are a wish's questions that wait for your answer, the blocking ones first, then in the order they were
// asked. Those being investigated wait for the lead: investigatingQuestions.
export function openQuestions(wish: Wish, detail: WishDetail): OpenQuestion[] {
  return questionsWhere(wish, detail, waitsForYou);
}

export function investigatingQuestions(
  wish: Wish,
  detail: WishDetail,
): OpenQuestion[] {
  return questionsWhere(wish, detail, investigating);
}

function questionsWhere(
  wish: Wish,
  detail: WishDetail,
  keep: (q: Question) => boolean,
): OpenQuestion[] {
  const blocks = blocking(detail.tasks);
  return detail.questions
    .filter(keep)
    .map((item) => ({ wish, item, blocking: blocks.get(item.id) ?? [] }))
    .sort((a, b) => Number(!a.blocking.length) - Number(!b.blocking.length));
}

// forkedAs is the code of the task that took over one cut short: a task of its wish forked from its session; "" when
// none did. It is the lamp's rule (internal/render.ForkedAs).
export function forkedAs(task: Task, tasks: readonly Task[]): string {
  return (
    tasks.find(
      (other) =>
        other.id !== task.id &&
        other.wishId === task.wishId &&
        other.forkOf !== "" &&
        other.forkOf === task.code,
    )?.code ?? ""
  );
}

// waitingTasks are a wish's tasks that wait for the user: for an answer before they edit, or cut short by a stop and
// not resumed. A task Djinn resumes by itself (RESUMING), or one resumed as another task, is not the user's move.
export function waitingTasks(wish: Wish, detail: WishDetail): Waiting[] {
  const questions = new Map(detail.questions.map((q) => [q.id, q]));
  return detail.tasks
    .filter(
      (task) =>
        task.status === TaskStatus.WAITING ||
        (task.status === TaskStatus.INTERRUPTED &&
          !forkedAs(task, detail.tasks)),
    )
    .map((item) => {
      const q = questions.get(item.editQuestionId);
      return { wish, item, question: q && isOpen(q) ? q.code : "" };
    });
}

// flightPlan merges the flight plans of the wishes given, in their order (the active ones, by rank).
export function flightPlan(
  wishes: readonly Wish[],
  details: Readonly<Record<string, WishDetail | undefined>>,
): FlightPlan {
  const plan: FlightPlan = {
    questions: [],
    investigating: [],
    waiting: [],
    ready: [],
    running: [],
    moving: [],
    finished: [],
    decisions: [],
    loaded: true,
  };
  for (const wish of wishes) {
    const detail = details[wish.id];
    if (!detail?.loaded) plan.loaded = false;
    if (wish.ready) plan.ready.push(wish);
    if (!detail) continue;
    plan.questions.push(...openQuestions(wish, detail));
    plan.investigating.push(...investigatingQuestions(wish, detail));
    plan.waiting.push(...waitingTasks(wish, detail));
    for (const item of detail.tasks) {
      if (
        item.status === TaskStatus.RUNNING ||
        item.status === TaskStatus.RESUMING
      )
        plan.running.push({ wish, item });
      if (finishedTask(item, detail.tasks)) plan.finished.push({ wish, item });
      else plan.moving.push({ wish, item });
    }
    for (const item of decisionsOf(
      detail.questions,
      detail.blocks,
      detail.tasks,
    ))
      plan.decisions.push({ wish, item });
  }
  // Blocking first across every wish; otherwise each keeps its wish's rank and its own order.
  plan.questions.sort(
    (a, b) => Number(!a.blocking.length) - Number(!b.blocking.length),
  );
  plan.decisions.sort((a, b) => later(a.item.at, b.item.at));
  plan.moving.sort((a, b) => byMotion(a.item, b.item));
  plan.finished.sort((a, b) => newestEnded(a.item, b.item));
  return plan;
}

// Spent is what tasks spent, summed: tokens always, the cost only of the tasks whose agent gives one (Codex and
// Antigravity give tokens only). Nothing is guessed.
export interface Spent {
  inputTokens: bigint;
  outputTokens: bigint;
  cacheReadTokens: bigint;
  cacheWriteTokens: bigint;
  costUsd: number;
  // The tasks that spent tokens without a cost.
  withoutCost: number;
  // The tasks that spent something.
  tasks: number;
}

// n reads a count of tokens; a message built by hand (a test) may leave it out.
const n = (value?: bigint) => value ?? 0n;

export function tokensOf(usage?: Usage): bigint {
  if (!usage) return 0n;
  return (
    n(usage.inputTokens) +
    n(usage.outputTokens) +
    n(usage.cacheReadTokens) +
    n(usage.cacheWriteTokens)
  );
}

export function spent(tasks: readonly Task[]): Spent {
  const out: Spent = {
    inputTokens: 0n,
    outputTokens: 0n,
    cacheReadTokens: 0n,
    cacheWriteTokens: 0n,
    costUsd: 0,
    withoutCost: 0,
    tasks: 0,
  };
  for (const { usage } of tasks) {
    if (!usage || (tokensOf(usage) === 0n && !usage.costUsd)) continue;
    out.tasks++;
    out.inputTokens += n(usage.inputTokens);
    out.outputTokens += n(usage.outputTokens);
    out.cacheReadTokens += n(usage.cacheReadTokens);
    out.cacheWriteTokens += n(usage.cacheWriteTokens);
    if (usage.costUsd > 0) out.costUsd += usage.costUsd;
    else out.withoutCost++;
  }
  return out;
}

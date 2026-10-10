// The flight plan of the active wishes, merged from what the store read of each: one list of questions, of what
// waits, of what runs, of the decisions. Every item keeps its wish. The order is the page's (the Go page of a
// wish sorts the same way); what the lamp decides (a wish's rank, whether it is ready) comes from the lamp.
import {
  AzimaState,
  type ProofNeed,
  Prover,
  type Question,
  type Task,
  TaskKind,
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
// urgency is how much an open question holds up, as the lamp computes it (internal/render.UrgencyOf): 0 blocking, a
// task waits for it; 1 needed before something, its before words; 2 it can wait.
export function urgency(question: OpenQuestion): number {
  if (question.blocking.length) return 0;
  return question.item.before ? 1 : 2;
}

// byUrgency orders open questions: blocking, then before X, then can wait. A sort keeps the order within a level.
export function byUrgency(a: OpenQuestion, b: OpenQuestion): number {
  return urgency(a) - urgency(b);
}

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
  // The tasks that move or wait, by status, then those done or stopped, the latest ended first: the Tasks tab. Work
  // part of an azima is in its azima's group, between them.
  moving: Item<Task>[];
  azimas: Item<AzimaGroup>[];
  drafts: Item<Task>[];
  // The proofs a person can give, of the azimas whose work is done: they wait for the person, never as work.
  proofs: Item<Proof>[];
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

// finishedTask tells a finished task: done, stopped on request, or cut short for good, as the wish's page and brief
// say. Djinn resumes by itself every task it can (resuming), so one left cut short (taken over by a fork, imported
// from another machine, its worktree gone) is history. Every other one still moves, or waits for someone.
export function finishedTask(task: Task): boolean {
  return (
    task.status === TaskStatus.DONE ||
    task.status === TaskStatus.STOPPED ||
    task.status === TaskStatus.INTERRUPTED
  );
}

// newestEnded orders finished tasks the latest ended first, then the latest created, as the wish's page and brief do.
export function newestEnded(a: Task, b: Task): number {
  return (
    time(b.endTime) - time(a.endTime) || time(b.createTime) - time(a.createTime)
  );
}

// isAzima tells an azima: a task of the plan that no worker runs. Work is part of one; it never waits for anyone.
export function isAzima(task: Task): boolean {
  return task.kind === TaskKind.AZIMA;
}

// isDraftAzima tells a draft azima: not ready to spawn parts into, not counted in progress.
export function isDraftAzima(task: Task): boolean {
  return (
    isAzima(task) && (task.draft || task.azima?.state === AzimaState.DRAFT)
  );
}

// loose are the tasks the Tasks tab lists on their own: work part of no azima among tasks.
function loose(tasks: readonly Task[]): Task[] {
  const azimas = new Set(tasks.filter(isAzima).map((x) => x.id));
  return tasks.filter((x) => !isAzima(x) && !azimas.has(x.partOf));
}

// azimasDone is how many azimas are done, how many await their proof (their work done), and how many there are:
// their progress, as the work's is in tasks.
export function azimasDone(tasks: readonly Task[]): {
  done: number;
  proof: number;
  count: number;
} {
  const azimas = tasks.filter((x) => isAzima(x) && !isDraftAzima(x));
  const done = azimas.filter(
    (x) => x.azima?.state === AzimaState.DONE || x.status === TaskStatus.DONE,
  ).length;
  const proof = azimas.filter(
    (x) =>
      x.azima?.state === AzimaState.AWAITING_PROOF &&
      x.status !== TaskStatus.DONE,
  ).length;
  return { done, proof, count: azimas.length };
}

// Proof is a box of an azima's plan file that waits for a proof a person can give.
export interface Proof {
  azima: Task;
  need: ProofNeed;
}

// givenByAPerson tells a proof a person can give: its needs name a person or a review (internal/plan.GivenByAPerson).
export function givenByAPerson(need: ProofNeed): boolean {
  return need.provers.some((p) => p === Prover.PERSON || p === Prover.REVIEW);
}

// awaitedProofs are the proofs a person can give, of the azimas that await theirs, by code.
export function awaitedProofs(tasks: readonly Task[]): Proof[] {
  return tasks
    .filter(
      (x) =>
        isAzima(x) &&
        x.azima?.state === AzimaState.AWAITING_PROOF &&
        x.status !== TaskStatus.DONE,
    )
    .sort((a, b) => compareCodes(a.code, b.code))
    .flatMap((azima) =>
      azima.proofNeeds.filter(givenByAPerson).map((need) => ({ azima, need })),
    );
}

// workCount is how many tasks of work there are: the azimas are the plan, not work.
export function workCount(tasks: readonly Task[]): number {
  return tasks.filter((x) => !isAzima(x)).length;
}

// finishedTasks are the tasks at the bottom of the Tasks tab, the latest ended first: work part of no azima.
export function finishedTasks(tasks: readonly Task[]): Task[] {
  return loose(tasks)
    .filter((x) => finishedTask(x))
    .sort(newestEnded);
}

// AzimaGroup is an azima and the work part of it: what moves or waits by status first, then what is finished, the
// latest ended first.
export interface AzimaGroup {
  azima: Task;
  parts: Task[];
}

// compareCodes orders codes as a person reads them, T2 before T10: the lamp's order (internal/plan.CompareCodes).
export function compareCodes(a: string, b: string): number {
  const split = (code: string): [string, number] => {
    const m = /^(\D*)(\d+)$/.exec(code);
    return m ? [m[1].toUpperCase(), Number(m[2])] : [code.toUpperCase(), -1];
  };
  const [pa, na] = split(a);
  const [pb, nb] = split(b);
  return pa < pb ? -1 : pa > pb ? 1 : na - nb || (a < b ? -1 : a > b ? 1 : 0);
}

// azimaRank orders the azimas as the brief does: the ready ones first, those under way before the open ones, then
// the blocked ones, then those awaiting their proof, the done ones last. Where an azima stands is the lamp's
// (Task.azima).
function azimaRank(task: Task): number {
  const state = task.azima?.state ?? AzimaState.OPEN;
  if (state === AzimaState.DONE || task.status === TaskStatus.DONE) return 4;
  if (state === AzimaState.AWAITING_PROOF) return 3;
  if (task.azima && !task.azima.ready) return 2;
  return state === AzimaState.IN_PROGRESS ? 0 : 1;
}

// azimaGroups are the azimas among tasks with their parts, in the order of azimaRank, then by code.
export function azimaGroups(tasks: readonly Task[]): AzimaGroup[] {
  return tasks
    .filter((x) => isAzima(x) && !isDraftAzima(x))
    .sort((a, b) => azimaRank(a) - azimaRank(b) || compareCodes(a.code, b.code))
    .map((azima) => {
      const parts = tasks.filter((x) => x.partOf === azima.id);
      return {
        azima,
        parts: [
          ...parts.filter((x) => !finishedTask(x)).sort(byMotion),
          ...parts.filter((x) => finishedTask(x)).sort(newestEnded),
        ],
      };
    });
}

// draftAzimas are the draft azimas among tasks, ordered by code.
export function draftAzimas(tasks: readonly Task[]): Task[] {
  return tasks
    .filter(isDraftAzima)
    .sort((a, b) => compareCodes(a.code, b.code));
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

// movingTasks are the tasks at the top of the Tasks tab: what moves or waits for someone, by status; work part of no
// azima.
export function movingTasks(tasks: readonly Task[]): Task[] {
  return loose(tasks)
    .filter((x) => !finishedTask(x))
    .sort(byMotion);
}

// openQuestions are a wish's questions that wait for your answer, the blocking ones first, then those needed before
// something, then those that can wait; each level in the order they were asked. Those being investigated wait for the
// lead: investigatingQuestions.
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
    .sort(byUrgency);
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
      // Only a worker that asks something waits for the user: Djinn resumes the ones it cut short by itself.
      (task) => task.status === TaskStatus.WAITING,
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
    azimas: [],
    drafts: [],
    proofs: [],
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
    }
    for (const item of movingTasks(detail.tasks))
      plan.moving.push({ wish, item });
    for (const item of finishedTasks(detail.tasks))
      plan.finished.push({ wish, item });
    for (const item of azimaGroups(detail.tasks))
      plan.azimas.push({ wish, item });
    for (const item of draftAzimas(detail.tasks))
      plan.drafts.push({ wish, item });
    for (const item of awaitedProofs(detail.tasks))
      plan.proofs.push({ wish, item });
    for (const item of decisionsOf(
      detail.questions,
      detail.blocks,
      detail.tasks,
    ))
      plan.decisions.push({ wish, item });
  }
  // Blocking, then before X, then can wait across every wish; within a level each keeps its wish's rank and its order.
  plan.questions.sort(byUrgency);
  plan.decisions.sort((a, b) => later(a.item.at, b.item.at));
  plan.moving.sort((a, b) => byMotion(a.item, b.item));
  plan.finished.sort((a, b) => newestEnded(a.item, b.item));
  plan.drafts.sort((a, b) => compareCodes(a.item.code, b.item.code));
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

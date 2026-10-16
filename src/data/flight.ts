// The flying carpet of the active wishes, merged from what the store read of each: one list of questions, of what
// waits, of what runs, of the decisions. Every item keeps its wish. The order is the page's (the Go page of a
// wish sorts the same way), but for the open questions, which the window lists as they were asked (byAsked) where the
// page puts the most urgent first; what the lamp decides (a wish's rank, whether it is ready) comes from the lamp.
// The flying carpet was called the flight plan: this file, flightPlan and FlightPlan keep that name.
import {
  AzimaState,
  type Block,
  IntegrationState,
  type ProofNeed,
  Prover,
  Provider,
  type Question,
  type Task,
  TaskKind,
  TaskStatus,
  type Wish,
} from "../../gen/ts/plan/v1/plan_pb";
import { type Decision, decisionsOf, later } from "./decisions";
import {
  investigating,
  isOpen,
  isTaskMerging,
  waitsForYou,
  watcherRuns,
} from "./format";
import { estimateCost, getPriceTable } from "./prices";
import { reportOf, reportsOf } from "./reports";
import type { WishDetail } from "./store";

export interface Item<T> {
  wish: Wish;
  item: T;
}

export interface OpenQuestion extends Item<Question> {
  // The codes of the tasks that wait for this answer: such a question comes first.
  blocking: string[];
  // The report of its wish it names (src/data/reports.ts), which its card links.
  report?: Block;
}

export const EMPTY_BLOCKING: string[] = [];

// urgency is how much an open question holds up, as the lamp computes it (internal/render.UrgencyOf): 0 blocking, a
// task waits for it; 1 a move only the developer can make; 2 needed before something, its before words; 3 it can wait.
export function urgency(question: OpenQuestion): number {
  if (question.blocking.length) return 0;
  if (question.item.move && question.item.before) return 1;
  return question.item.before ? 2 : CAN_WAIT;
}

// The urgency of a question that can wait: no task waits for it, and it names nothing it is needed before, a move
// included (Q13).
export const CAN_WAIT = 3;

// holdsUp tells an open question that keeps its wish from moving forward and cannot wait, as its card says: a task
// waits for it, or it is needed before something, a move of the developer's or not. Only these are lines of the bar
// of what waits for you (attention.tsx); one that can wait shows in the questions column alone.
export function holdsUp(question: OpenQuestion): boolean {
  return urgency(question) < CAN_WAIT;
}

// placeOf is where a question stands among the others: when it was asked, or when you put it off for later if that
// came after (Question.laterTime), as Djinn's page and brief place it (render.Place).
export function placeOf(question: Question): number {
  return Math.max(time(question.createTime), time(question.laterTime));
}

// byAsked orders questions as they were asked, the oldest first: a new one always comes below, and a revision, which
// keeps its creation time, never moves one. One put off for later goes below those asked before, and stays there.
// The window lists them so (AGENTS.md, Interface); its urgency shows on each.
export function byAsked(a: Item<Question>, b: Item<Question>): number {
  return (
    placeOf(a.item) - placeOf(b.item) ||
    (a.item.id < b.item.id ? -1 : a.item.id > b.item.id ? 1 : 0)
  );
}

// Something that waits for the user and is not a question: a worker that waits for an answer before it edits.
export interface Waiting extends Item<Task> {
  // The code of the question the task waits for, when it is open.
  question: string;
  // For a merge, the original task it sits under (mergeNest): the line shows as that task's.
  host?: Task;
}

export interface FlightPlan {
  // The questions that wait for your answer.
  questions: OpenQuestion[];
  // The questions answered that wait for a lead.
  waitingLead: OpenQuestion[];
  // The questions you asked to investigate: they wait for the lead.
  investigating: OpenQuestion[];
  waiting: Waiting[];
  // The wishes Djinn proposes to grant.
  ready: Wish[];
  // What runs: workers at work, and work that merges (isTaskMerging).
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

// finishedTask tells a finished task: done, stopped on request, cancelled, or cut short for good, as the wish's page and brief
// say. Djinn resumes by itself every task it can (resuming), so one left cut short (taken over by a fork, imported
// from another machine, its worktree gone) is history. Every other one still moves, or waits for someone.
export function finishedTask(task: Task): boolean {
  if (isAzima(task)) return task.azima?.state === AzimaState.DONE;
  return (
    task.status === TaskStatus.DONE ||
    task.status === TaskStatus.STOPPED ||
    task.status === TaskStatus.INTERRUPTED ||
    task.status === TaskStatus.CANCELLED
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

// loose are the tasks the Tasks tab lists on their own: work part of no azima among tasks, merges nested under the
// task they serve left out (mergeNest).
export function loose(tasks: readonly Task[]): Task[] {
  const azimas = new Set(tasks.filter(isAzima).map((x) => x.id));
  const nested = mergeNest(tasks).host;
  return tasks.filter(
    (x) => !isAzima(x) && !azimas.has(x.partOf) && !nested.has(x.id),
  );
}

// isMerge tells a worker Djinn starts by itself to bring work into an integration branch: a correction (its merge
// conflicted, or its tests or push checks failed) or a review of work left not committed (internal/plan.IsMerge).
export function isMerge(task: Task): boolean {
  return !!task.correction || !!task.review;
}

// mergeOf are the tasks a merge brings the work of, by id.
function mergeOf(task: Task): readonly string[] {
  return task.correction?.failure?.taskIds ?? task.review?.taskIds ?? [];
}

// mergeServes are the original tasks a merge serves: the tasks it brings the work of, earlier merges among them
// followed back to tasks that are no merges, in order and once each (internal/plan.MergeServes).
export function mergeServes(
  task: Task,
  byId: ReadonlyMap<string, Task>,
): Task[] {
  const out: Task[] = [];
  const seen = new Set([task.id]);
  const walk = (ids: readonly string[]) => {
    for (const id of ids) {
      const s = byId.get(id);
      if (seen.has(id) || !s) continue;
      seen.add(id);
      if (isMerge(s)) walk(mergeOf(s));
      else out.push(s);
    }
  };
  walk(mergeOf(task));
  return out;
}

// MergeNest says where the merges among a wish's tasks show: each under the first original task it serves in the
// same azima (or, like it, in none), not as a peer of that azima's work; linked from the other tasks it serves. A
// merge that serves none there (a push or main branch correction) stays a peer, marked as a merge.
export interface MergeNest {
  // The task each nested merge sits under, by the merge's id.
  host: ReadonlyMap<string, string>;
  // The merges nested under a task, by its id, in their order among the tasks.
  under: ReadonlyMap<string, Task[]>;
  // The merges that serve a task but sit under another one, its host, by the task's id.
  links: ReadonlyMap<string, { merge: Task; host: Task }[]>;
}

const nests = new WeakMap<readonly Task[], MergeNest>();

// mergeNest is where the merges among tasks show (MergeNest); the same list gives the same answer.
export function mergeNest(tasks: readonly Task[]): MergeNest {
  const known = nests.get(tasks);
  if (known) return known;
  const byId = new Map(tasks.map((x) => [x.id, x]));
  const host = new Map<string, string>();
  const under = new Map<string, Task[]>();
  const links = new Map<string, { merge: Task; host: Task }[]>();
  for (const x of tasks) {
    if (!isMerge(x)) continue;
    const served = mergeServes(x, byId);
    const first = served.find((s) => s.partOf === x.partOf);
    if (!first) continue;
    host.set(x.id, first.id);
    under.set(first.id, [...(under.get(first.id) ?? []), x]);
    for (const s of served)
      if (s !== first)
        links.set(s.id, [
          ...(links.get(s.id) ?? []),
          { merge: x, host: first },
        ]);
  }
  const nest = { host, under, links };
  nests.set(tasks, nest);
  return nest;
}

// workOnItsWay tells a done task whose work is not in its wish's integration branch yet: waiting for its batch,
// merging, in conflict, red, or left not committed. Something still moves or waits on it. Work committed, a task with
// no work to merge (no branch, cancelled) or whose integration was cleared has none on its way.
export function workOnItsWay(task: Task): boolean {
  if (task.status !== TaskStatus.DONE) return false;
  const s = task.integration?.state;
  return (
    s === IntegrationState.PENDING ||
    s === IntegrationState.INTEGRATING ||
    s === IntegrationState.CONFLICT ||
    s === IntegrationState.RED ||
    s === IntegrationState.UNCOMMITTED
  );
}

// ended tells a finished task whose work is in, or has none to bring in (workOnItsWay).
const ended = (task: Task) => finishedTask(task) && !workOnItsWay(task);

// settledTask tells a finished task whose work is committed, and whose merges are settled too: the Tasks tab and an
// azima fold it with the finished. A task whose work merges, or failed to, or whose merge still runs moves with it:
// one that waits for it to be committed points at a card in sight.
export function settledTask(task: Task, nest: MergeNest): boolean {
  return ended(task) && (nest.under.get(task.id) ?? []).every(ended);
}

// isTaskMergingOrFailed tells whether a done task's work is merging or failed to merge.
export function isTaskMergingOrFailed(task: Task): boolean {
  if (task.status !== TaskStatus.DONE) return false;
  const s = task.integration?.state;
  return (
    s === IntegrationState.PENDING ||
    s === IntegrationState.INTEGRATING ||
    s === IntegrationState.CONFLICT ||
    s === IntegrationState.RED
  );
}

// azimaHasMergingOrFailed tells whether any task part of the azima (recursively) is merging or merge-failed.
export function azimaHasMergingOrFailed(
  azimaId: string,
  tasks: readonly Task[],
): boolean {
  const parts = tasks.filter((t) => t.partOf === azimaId);
  for (const part of parts) {
    if (isAzima(part)) {
      if (azimaHasMergingOrFailed(part.id, tasks)) return true;
    } else if (isTaskMergingOrFailed(part)) {
      return true;
    }
  }
  return false;
}

// azimasDone is how many azimas are done, how many await their proof (their work done), and how many there are:
// their progress, as the work's is in tasks.
export function azimasDone(tasks: readonly Task[]): {
  done: number;
  proof: number;
  count: number;
} {
  const azimas = tasks.filter((x) => isAzima(x) && !isDraftAzima(x));
  const done = azimas.filter((x) => x.azima?.state === AzimaState.DONE).length;
  const proof = azimas.filter(
    (x) => x.azima?.state === AzimaState.AWAITING_PROOF,
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
    .filter((x) => isAzima(x) && x.azima?.state === AzimaState.AWAITING_PROOF)
    .sort((a, b) => compareCodes(a.code, b.code))
    .flatMap((azima) =>
      azima.proofNeeds.filter(givenByAPerson).map((need) => ({ azima, need })),
    );
}

// workCount is how many tasks of work there are: the azimas are the plan, not work; a merge counts with the task it
// serves; a cancelled task is no work left (the server's Task.azima.parts leaves it out too).
export function workCount(tasks: readonly Task[]): number {
  const azimas = tasks.filter((x) => isAzima(x) && !isDraftAzima(x));
  const hasServerParts = azimas.some((a) => (a.azima?.parts ?? 0) > 0);
  const nested = mergeNest(tasks).host;
  if (hasServerParts) {
    const serverParts = azimas.reduce((n, a) => n + (a.azima?.parts ?? 0), 0);
    const looseTasks = tasks.filter(
      (x) =>
        !isAzima(x) &&
        !x.partOf &&
        !nested.has(x.id) &&
        x.status !== TaskStatus.CANCELLED,
    ).length;
    return serverParts + looseTasks;
  }
  return tasks.filter(
    (x) =>
      !isAzima(x) && !nested.has(x.id) && x.status !== TaskStatus.CANCELLED,
  ).length;
}

// workDoneCount is how many work tasks are done: using server azima.partsDone when available.
export function workDoneCount(tasks: readonly Task[]): number {
  const azimas = tasks.filter((x) => isAzima(x) && !isDraftAzima(x));
  const hasServerParts = azimas.some((a) => (a.azima?.parts ?? 0) > 0);
  if (hasServerParts) {
    const serverDone = azimas.reduce(
      (n, a) => n + (a.azima?.partsDone ?? 0),
      0,
    );
    const nested = mergeNest(tasks).host;
    const looseDone = tasks.filter(
      (x) =>
        x.status === TaskStatus.DONE &&
        !isAzima(x) &&
        !x.partOf &&
        !nested.has(x.id),
    ).length;
    return serverDone + looseDone;
  }
  const nested = mergeNest(tasks).host;
  return tasks.filter(
    (task) =>
      task.status === TaskStatus.DONE && !isAzima(task) && !nested.has(task.id),
  ).length;
}

// finishedTasks are the tasks at the bottom of the Tasks tab, the latest ended first: work part of no azima.
export function finishedTasks(tasks: readonly Task[]): Task[] {
  const nest = mergeNest(tasks);
  return loose(tasks)
    .filter((x) => settledTask(x, nest))
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

// azimaRank orders the azimas in a stable order between renders and between days:
// 0: azimas that move (a worker running on one of its parts, in progress, or watching)
// 1: azimas waiting for the developer (awaiting proof / to validate)
// 2: ready azimas not started (open and ready to begin)
// 3: blocked azimas (waiting for unfinished dependencies)
// 4: drafts (not ready to spawn parts into)
// 5: done azimas (finished, folded at the end)
// Within each group, azimas are ordered strictly by the plan's code order (compareCodes),
// never by time or by a count that changes every minute.
export function azimaRank(task: Task): number {
  if (isDraftAzima(task) || task.azima?.state === AzimaState.DRAFT) return 4;
  const state = task.azima?.state ?? AzimaState.OPEN;
  if (state === AzimaState.DONE) return 5;
  if (
    (task.azima?.partsRunning ?? 0) > 0 ||
    state === AzimaState.IN_PROGRESS ||
    state === AzimaState.WATCHING
  )
    return 0;
  if (state === AzimaState.AWAITING_PROOF) return 1;
  if (task.azima && !task.azima.ready) return 3;
  return 2;
}

// azimaGroups are the azimas among tasks with their parts, in the order of azimaRank, then by code.
export function azimaGroups(tasks: readonly Task[]): AzimaGroup[] {
  return tasks
    .filter((x) => isAzima(x) && !isDraftAzima(x))
    .sort((a, b) => azimaRank(a) - azimaRank(b) || compareCodes(a.code, b.code))
    .map((azima) => {
      const nest = mergeNest(tasks);
      const parts = tasks.filter(
        (x) => x.partOf === azima.id && !nest.host.has(x.id),
      );
      return {
        azima,
        parts: [
          ...parts.filter((x) => !settledTask(x, nest)).sort(byMotion),
          ...parts.filter((x) => settledTask(x, nest)).sort(newestEnded),
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
  const nest = mergeNest(tasks);
  return loose(tasks)
    .filter((x) => !settledTask(x, nest))
    .sort(byMotion);
}

// openQuestions are a wish's questions that wait for your answer, in the order they were asked (byAsked). Those being
// investigated wait for the lead: investigatingQuestions.
export function openQuestions(wish: Wish, detail: WishDetail): OpenQuestion[] {
  return questionsWhere(wish, detail, waitsForYou);
}

export function investigatingQuestions(
  wish: Wish,
  detail: WishDetail,
): OpenQuestion[] {
  return questionsWhere(wish, detail, investigating);
}

// waitingLeadQuestions are a wish's answered questions waiting for a lead to start.
export function waitingLeadQuestions(
  wish: Wish,
  detail: WishDetail,
): OpenQuestion[] {
  return questionsWhere(wish, detail, (q) => Boolean(q.waitingLead));
}

function questionsWhere(
  wish: Wish,
  detail: WishDetail,
  keep: (q: Question) => boolean,
): OpenQuestion[] {
  const blocks = blocking(detail.tasks);
  const reports = reportsOf(detail.blocks);
  return detail.questions
    .filter(keep)
    .map((item) => {
      const report = reportOf(item, reports);
      return {
        wish,
        item,
        blocking: blocks.get(item.id) ?? EMPTY_BLOCKING,
        ...(report && { report }),
      };
    })
    .sort(byAsked);
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

// waitingTasks are a wish's tasks that wait for the user: for an answer before they edit. A failed worker never does,
// whatever made it fail, refused calls included: it is the lead's, who takes it up again, closes it, or asks the user a
// question for a right only they grant (Q273, W96: W19, an investigator failed on refused commands, waited for you
// with nothing you could do).
export function waitingTasks(wish: Wish, detail: WishDetail): Waiting[] {
  const questions = new Map(detail.questions.map((q) => [q.id, q]));
  return detail.tasks
    .filter((task) => task.status === TaskStatus.WAITING)
    .map((item) => {
      const q = questions.get(item.editQuestionId);
      const id = mergeNest(detail.tasks).host.get(item.id);
      const host = id ? detail.tasks.find((x) => x.id === id) : undefined;
      return {
        wish,
        item,
        question: q && isOpen(q) ? q.code : "",
        ...(host && { host }),
      };
    });
}

// flightPlan merges the flying carpets of the wishes given, in their order (the active ones, by rank).
export function flightPlan(
  wishes: readonly Wish[],
  details: Readonly<Record<string, WishDetail | undefined>>,
): FlightPlan {
  const plan: FlightPlan = {
    questions: [],
    waitingLead: [],
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
    plan.waitingLead.push(...waitingLeadQuestions(wish, detail));
    plan.investigating.push(...investigatingQuestions(wish, detail));
    plan.waiting.push(...waitingTasks(wish, detail));
    for (const item of detail.tasks) {
      if (
        item.status === TaskStatus.RUNNING ||
        item.status === TaskStatus.RESUMING ||
        isTaskMerging(item)
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
  // The questions stay by wish, in the wishes' rank, each wish's in the order they were asked: nothing sorts them
  // across wishes, so a new one never comes above the others of its wish.
  plan.decisions.sort((a, b) => later(a.item.at, b.item.at));
  plan.moving.sort((a, b) => byMotion(a.item, b.item));
  plan.azimas.sort(
    (a, b) =>
      azimaRank(a.item.azima) - azimaRank(b.item.azima) ||
      compareCodes(a.item.azima.code, b.item.azima.code),
  );
  plan.finished.sort((a, b) => newestEnded(a.item, b.item));
  plan.drafts.sort((a, b) => compareCodes(a.item.code, b.item.code));
  return plan;
}

export interface ProviderSpent {
  provider: Provider;
  inputTokens: bigint;
  outputTokens: bigint;
  cacheReadTokens: bigint;
  cacheWriteTokens: bigint;
  thinkingTokens: bigint;
  costUsd: number;
  estimatedCostUsd: number;
  withoutCost: number;
  priced: number;
  withoutPrice: number;
  tasks: number;
}

// Spent is what tasks spent, summed: tokens always, and the cost: what the agents reported, and for the tasks with no
// reported cost, their tokens at the table's prices. The two parts add up to the price (costUsd + estimatedCostUsd).
export interface Spent {
  inputTokens: bigint;
  outputTokens: bigint;
  cacheReadTokens: bigint;
  cacheWriteTokens: bigint;
  thinkingTokens: bigint;
  costUsd: number;
  // The estimate of the tasks without a reported cost.
  estimatedCostUsd: number;
  tableDate: string;
  // The tasks that spent tokens without a cost.
  withoutCost: number;
  // The tasks without a cost the table priced.
  priced: number;
  // The tasks without a cost nor a price in the table.
  withoutPrice: number;
  // The tasks that spent something.
  tasks: number;
  // Totals per provider
  byProvider: ProviderSpent[];
}

// n reads a count of tokens; a message built by hand (a test) may leave it out.
const n = (value?: bigint) => value ?? 0n;

export type TokenCounts = {
  inputTokens?: bigint;
  outputTokens?: bigint;
  cacheReadTokens?: bigint;
  cacheWriteTokens?: bigint;
  thinkingTokens?: bigint;
};

export function tokensOf(usage?: TokenCounts): bigint {
  if (!usage) return 0n;
  const inTok = n(usage.inputTokens);
  const outTok = n(usage.outputTokens);
  const thinkTok = n(usage.thinkingTokens);
  const readTok = n(usage.cacheReadTokens);
  const writeTok = n(usage.cacheWriteTokens);
  const totalOut = outTok >= thinkTok ? outTok : thinkTok;
  return inTok + totalOut + readTok + writeTok;
}

export function spent(tasks: readonly Task[]): Spent {
  const table = getPriceTable();
  const out: Spent = {
    inputTokens: 0n,
    outputTokens: 0n,
    cacheReadTokens: 0n,
    cacheWriteTokens: 0n,
    thinkingTokens: 0n,
    costUsd: 0,
    estimatedCostUsd: 0,
    tableDate: table.date,
    withoutCost: 0,
    priced: 0,
    withoutPrice: 0,
    tasks: 0,
    byProvider: [],
  };
  const providerMap = new Map<Provider, ProviderSpent>();

  for (const task of tasks) {
    const { usage, provider, model } = task;
    if (!usage || (tokensOf(usage) === 0n && !usage.costUsd)) continue;
    out.tasks++;
    const inTok = n(usage.inputTokens);
    const outTok = n(usage.outputTokens);
    const readTok = n(usage.cacheReadTokens);
    const writeTok = n(usage.cacheWriteTokens);
    const thinkTok = n(usage.thinkingTokens);

    out.inputTokens += inTok;
    out.outputTokens += outTok;
    out.cacheReadTokens += readTok;
    out.cacheWriteTokens += writeTok;
    out.thinkingTokens += thinkTok;

    // A reported cost is the price; the table prices only the tasks without one.
    const reported = usage.costUsd > 0;
    const est = estimateCost(usage, model, provider);
    if (reported) out.costUsd += usage.costUsd;
    else {
      out.withoutCost++;
      if (est.hasPrice) {
        out.priced++;
        out.estimatedCostUsd += est.costUsd;
      } else out.withoutPrice++;
    }

    const prov = provider ?? Provider.UNSPECIFIED;
    let pSpent = providerMap.get(prov);
    if (!pSpent) {
      pSpent = {
        provider: prov,
        inputTokens: 0n,
        outputTokens: 0n,
        cacheReadTokens: 0n,
        cacheWriteTokens: 0n,
        thinkingTokens: 0n,
        costUsd: 0,
        estimatedCostUsd: 0,
        withoutCost: 0,
        priced: 0,
        withoutPrice: 0,
        tasks: 0,
      };
      providerMap.set(prov, pSpent);
    }
    pSpent.tasks++;
    pSpent.inputTokens += inTok;
    pSpent.outputTokens += outTok;
    pSpent.cacheReadTokens += readTok;
    pSpent.cacheWriteTokens += writeTok;
    pSpent.thinkingTokens += thinkTok;
    if (reported) pSpent.costUsd += usage.costUsd;
    else {
      pSpent.withoutCost++;
      if (est.hasPrice) {
        pSpent.priced++;
        pSpent.estimatedCostUsd += est.costUsd;
      } else pSpent.withoutPrice++;
    }
  }

  out.byProvider = Array.from(providerMap.values());
  return out;
}

const taskMaps = new WeakMap<readonly Task[], ReadonlyMap<string, Task>>();
const EMPTY_TASKS: Task[] = [];

// taskMapOf returns a map of tasks by id, cached by the tasks array reference.
export function taskMapOf(
  tasks: readonly Task[] = EMPTY_TASKS,
): ReadonlyMap<string, Task> {
  let m = taskMaps.get(tasks);
  if (!m) {
    m = new Map(tasks.map((task) => [task.id, task]));
    taskMaps.set(tasks, m);
  }
  return m;
}

const codeMaps = new WeakMap<readonly Task[], ReadonlyMap<string, string>>();

// codeMapOf returns a map of task codes by id, cached by the tasks array reference.
export function codeMapOf(
  tasks: readonly Task[] = EMPTY_TASKS,
): ReadonlyMap<string, string> {
  let m = codeMaps.get(tasks);
  if (!m) {
    m = new Map(tasks.map((task) => [task.id, task.code]));
    codeMaps.set(tasks, m);
  }
  return m;
}

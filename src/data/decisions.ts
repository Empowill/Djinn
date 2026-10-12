// The decision log of a wish, read from the protos: its answered questions and its blocks of kind decision, the latest
// first, each with who took it and the tasks that name it (Task.decision). The Go page and the brief read the same
// (internal/render/decisions.go).
import type { Timestamp } from "@bufbuild/protobuf/wkt";

import {
  type Block,
  type Mark,
  MarkKind,
  type Question,
  type Task,
} from "../../gen/ts/plan/v1/plan_pb";

// The emoji of a decision that names none: an answered question, a decision block.
export const QUESTION_ICON = "💬";
export const BLOCK_ICON = "📌";

// What Decision.by says of a block the wish's lead wrote.
export const BY_LEAD = "lead";

export interface Decision {
  // The question's or the block's id.
  id: string;
  // What a task names to come from it: the question's code, or the block's id.
  ref: string;
  question?: Question;
  block?: Block;
  // When it was taken: the answer, or the block's creation.
  at?: Timestamp;
  // The developer took it: an answered question, or a block the developer approved.
  human: boolean;
  // By an approval: the recommendation on a question, a block as it is.
  approved: boolean;
  // The agent that took a block: BY_LEAD, or the code of the task the block is about. Empty when human.
  by: string;
  // Its emoji: its own, or the default of its kind.
  icon: string;
  // The tasks that name it as their decision.
  tasks: Task[];
}

// isDecisionBlock tells a block of kind decision, case ignored.
export function isDecisionBlock(block: Pick<Block, "kind">): boolean {
  return block.kind.toLowerCase() === "decision";
}

const approved = (marks?: Mark[]) =>
  (marks ?? []).some((m) => m.kind === MarkKind.APPROVED);

// later orders two times, the later first.
export const later = (a?: Timestamp, b?: Timestamp) =>
  Number(b?.seconds ?? 0n) - Number(a?.seconds ?? 0n) ||
  (b?.nanos ?? 0) - (a?.nanos ?? 0);

const questionDecisions = new WeakMap<Question, Decision>();
const blockDecisions = new WeakMap<Block, Decision>();

const sameTasks = (a: readonly Task[], b: readonly Task[]) =>
  a.length === b.length && a.every((t, i) => t === b[i]);

// decisionsOf are the decisions of a wish, the latest first.
export function decisionsOf(
  questions: readonly Question[],
  blocks: readonly Block[],
  tasks: readonly Task[],
): Decision[] {
  const led = (ref: string) =>
    tasks.filter((task) => task.decision?.toUpperCase() === ref.toUpperCase());
  const out: Decision[] = [];
  for (const q of questions) {
    if (!q.answer) continue;
    const matching = led(q.code);
    const cached = questionDecisions.get(q);
    if (cached && sameTasks(cached.tasks, matching)) {
      out.push(cached);
      continue;
    }
    const d: Decision = {
      id: q.id,
      ref: q.code,
      question: q,
      at: q.answer.createTime,
      human: true,
      approved: approved(q.marks),
      by: "",
      icon: q.icon || QUESTION_ICON,
      tasks: matching,
    };
    questionDecisions.set(q, d);
    out.push(d);
  }
  for (const b of blocks) {
    if (!isDecisionBlock(b)) continue;
    const human = approved(b.marks);
    const by = human
      ? ""
      : tasks.find((task) => task.id === b.taskId)?.code || BY_LEAD;
    const matching = led(b.id);
    const cached = blockDecisions.get(b);
    if (cached && cached.by === by && sameTasks(cached.tasks, matching)) {
      out.push(cached);
      continue;
    }
    const d: Decision = {
      id: b.id,
      ref: b.id,
      block: b,
      at: b.createTime,
      human,
      approved: human,
      by,
      icon: b.icon || BLOCK_ICON,
      tasks: matching,
    };
    blockDecisions.set(b, d);
    out.push(d);
  }
  // Stable: two decisions of one time keep their order.
  return out.sort((a, b) => later(a.at, b.at));
}

// decisionOf is the decision a task comes from, among a wish's decisions.
export function decisionOf(
  task: Pick<Task, "decision">,
  decisions: readonly Decision[],
): Decision | undefined {
  const ref = task.decision?.toUpperCase();
  return ref ? decisions.find((d) => d.ref.toUpperCase() === ref) : undefined;
}

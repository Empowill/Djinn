// Small readings of the protos for the screens: dates, states, answers. They compute nothing the lamp decides.
import { type Timestamp, timestampDate } from "@bufbuild/protobuf/wkt";

import {
  Allowance,
  Choice,
  type KeptWorktree,
  type Mark,
  MarkKind,
  type Project,
  Provider,
  type Question,
  RoundKind,
  type Task,
  TaskStatus,
  type Wish,
  WishState,
} from "../../gen/ts/plan/v1/plan_pb";
import { useEffect, useState } from "react";

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

// span says a length of time as a counter, two units at most: 45s, 1m1s, 1h1m, 3d4h.
export function span(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000));
  const m = Math.floor(s / 60);
  const h = Math.floor(m / 60);
  if (s < 60) return t("time.s", { s });
  if (m < 60) return t("time.ms", { m, s: s % 60 });
  if (h < 24) return t("time.hm", { h, m: m % 60 });
  return t("time.dh", { d: Math.floor(h / 24), h: h % 24 });
}

// taskTime is how long a task runs, from its start until now, or how long it ran once ended; "" before it starts.
// Its title says when it started, and when it ended.
export function taskTime(
  task: Task,
  now: number,
): { text: string; title: string } {
  const start = date(task.startTime);
  if (!start) return { text: "", title: "" };
  const end = taskFinished(task.status) ? date(task.endTime) : undefined;
  if (taskFinished(task.status) && !end) return { text: "", title: "" };
  const text = span((end?.getTime() ?? now) - start.getTime());
  const title = end
    ? t("task.ran", { from: when(task.startTime), to: when(task.endTime) })
    : t("task.running_since", { when: when(task.startTime) });
  return { text, title };
}

// useNow is the time now, read again every period while on: a duration shown stays current.
export function useNow(on: boolean, period = 1_000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!on) return;
    setNow(Date.now());
    const id = setInterval(() => setNow(Date.now()), period);
    return () => clearInterval(id);
  }, [on, period]);
  return now;
}

// A wish stored before states is active.
export function isActive(wish: Wish): boolean {
  return (
    wish.state === WishState.ACTIVE || wish.state === WishState.UNSPECIFIED
  );
}

// noLead tells a wish with no lead session: djinn tells no lead its answers, they wait in the wish's brief
// (plan.ErrNoLead).
export function noLead(wish: Pick<Wish, "lead">): boolean {
  return !wish.lead?.sessionId;
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
// limit; one cut short and resumed as another task (forkedAs) says which; a running watcher watches.
export function taskStatusText(
  task: Pick<Task, "status" | "resumeAfter"> & Partial<Pick<Task, "provider">>,
  forkedAs = "",
): string {
  if (watcherRuns(task)) return t("task.status_watching");
  if (task.status === TaskStatus.RESUMING && task.resumeAfter)
    return t("task.status_limit");
  if (task.status === TaskStatus.INTERRUPTED && forkedAs)
    return t("task.status_forked", { task: forkedAs });
  return t(taskStatusKeys[task.status]);
}

// Tone is a state in the window's status language: each has its colour, its icon and its word (src/status.tsx).
export type Tone =
  | "done"
  | "running"
  | "waiting"
  | "investigating"
  | "planned"
  | "failed"
  | "interrupted"
  | "stopped"
  | "paused"
  | "watching"
  | "human"
  | "proof"
  // An open question nothing waits for: it can wait.
  | "later";

// taskTone is a task's state in that language. A task Djinn resumes runs; one waiting for its provider's limit to
// reset holds still, as a paused one; one cut short and resumed as another task (forkedAs) is stopped, no alarm.
// None of them is the person's move. A running watcher has its own tone: it sleeps until its command prints.
export function taskTone(
  task: Pick<Task, "status" | "resumeAfter"> & Partial<Pick<Task, "provider">>,
  forkedAs = "",
): Tone {
  const status = task.status;
  if (watcherRuns(task)) return "watching";
  if (status === TaskStatus.RESUMING && task.resumeAfter) return "paused";
  if (status === TaskStatus.INTERRUPTED && forkedAs) return "stopped";
  switch (status) {
    case TaskStatus.RUNNING:
    case TaskStatus.RESUMING:
      return "running";
    case TaskStatus.DONE:
      return "done";
    case TaskStatus.FAILED:
      return "failed";
    case TaskStatus.INTERRUPTED:
      return "interrupted";
    case TaskStatus.STOPPED:
      return "stopped";
    case TaskStatus.WAITING:
      return "waiting";
    case TaskStatus.PAUSED:
      return "paused";
    default:
      return "planned";
  }
}

// watcherRuns tells whether the task is a watcher whose command runs: a command, no agent (Provider.WATCH).
export function watcherRuns(
  task: Pick<Task, "status"> & Partial<Pick<Task, "provider">>,
): boolean {
  return task.provider === Provider.WATCH && task.status === TaskStatus.RUNNING;
}

// Running counts a wish's tasks whose process runs, apart: workers at work, and watchers that wait for their command to
// print. A watcher is not work in progress: "1 running" for a PR watched alone said a worker ran.
export interface Running {
  running: number;
  watching: number;
}

export function runningOf(
  tasks: (Pick<Task, "status"> & Partial<Pick<Task, "provider">>)[],
): Running {
  const live = tasks.filter((task) => task.status === TaskStatus.RUNNING);
  const watching = live.filter(watcherRuns).length;
  return { running: live.length - watching, watching };
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

// deletedText says a wish is deleted, and which worktrees of its tasks stay on disk, and why: commits the project's
// current branch does not have, changes not committed, or what Git said.
export function deletedText(title: string, kept: KeptWorktree[]): string {
  const lines = kept.map((k) => {
    const why = k.error
      ? [k.error]
      : [
          ...(k.commits > 0
            ? [t("wish.kept_commits", { count: k.commits, base: k.base })]
            : []),
          ...(k.changed ? [t("wish.kept_changed")] : []),
        ];
    return t("wish.kept_worktree", {
      code: k.taskCode,
      branch: k.branch,
      why: why.join(", "),
    });
  });
  return [t("wish.deleted_toast", { title }), ...lines].join(" ");
}

// usd says a cost in dollars, to the cent.
export function usd(cost: number): string {
  return new Intl.NumberFormat(language, {
    style: "currency",
    currency: "USD",
    maximumFractionDigits: cost < 1 ? 3 : 2,
  }).format(cost);
}

// investigating tells an open question whose last round asks to investigate: it waits for the lead, not for you. The
// lamp reads it the same way (plan.Investigating).
export function investigating(question: Question): boolean {
  const last = question.rounds?.at(-1);
  return !question.answer && last?.kind === RoundKind.ENLIGHTEN;
}

// waitsForYou tells an open question that waits for your answer.
export function waitsForYou(question: Question): boolean {
  return isOpen(question) && !investigating(question);
}

// markOf is the mark of a kind on a question or a block, if it has one.
export function markOf(
  item: { marks?: Mark[] },
  kind: MarkKind,
): Mark | undefined {
  return item.marks?.find((m) => m.kind === kind);
}

const firstWord = (text: string) => /^\s*(\p{L}*)/u.exec(text)?.[1] ?? "";

// recommendedChoice is the option a question's recommendation names, for a one-click answer, as the lamp reads it
// (plan.Recommended in internal/plan/marks.go): yes without options; else a letter first ("B: …", "**B**, because…"),
// or the first word of one option only.
export function recommendedChoice(question: Question): Choice | undefined {
  const options = question.options;
  if (!options.length) return Choice.YES;
  let text = question.recommendation.replace(/^[\s*_#>`]+/, "");
  text = text.replace(/^[Oo]ption /, "");
  const index = text.charCodeAt(0) - 65;
  if (index >= 0 && index < Math.min(options.length, 4)) {
    const rest = text.slice(1).replace(/^ +/, "");
    if (!rest || ":.)*,;—–-(".includes(rest[0])) return choiceOf(index);
  }
  const word = firstWord(text).toLowerCase();
  if (!word) return undefined;
  const found = options
    .map((o, i) => (firstWord(o).toLowerCase() === word ? i : -1))
    .filter((i) => i >= 0);
  return found.length === 1 ? choiceOf(found[0]) : undefined;
}

// wishTone is a wish's state in the status language: what it needs first.
export function wishTone(
  wish: Wish,
  questions: number,
  running: number,
  watching = 0,
): Tone {
  if (wish.state === WishState.GRANTED) return "done";
  if (wish.state === WishState.PAUSED) return "paused";
  if (questions > 0 || wish.ready) return "waiting";
  if (running > 0) return "running";
  if (watching > 0) return "watching";
  return "planned";
}

// shortModel turns a provider's model identifier into a short, readable label:
// "claude-sonnet-5-5" -> "sonnet 5.5", "gemini-3.8-flash-high" -> "gemini 3.8 flash", "gpt-5.5" -> "gpt-5.5".
export function shortModel(model: string): string {
  if (!model) return "";
  let m = model.trim();
  // Strip trailing snapshot date e.g. -20250929
  m = m.replace(/-\d{8}$/, "");
  // Strip provider / registry prefix e.g. anthropic/ or openai/
  m = m.replace(/^[a-z0-9-._]+\//, "");
  // Strip endpoint prefix e.g. us.anthropic.
  m = m.replace(/^[a-z0-9-._]+\.(?=claude|gemini|gpt)/, "");

  // Claude: claude-(sonnet|opus|haiku)-(X-Y|X.Y) -> $1 X.Y
  const claudeMatch = /^claude-(sonnet|opus|haiku)-(\d+)[.-](\d+)$/.exec(m);
  if (claudeMatch) {
    return `${claudeMatch[1]} ${claudeMatch[2]}.${claudeMatch[3]}`;
  }
  // Claude: claude-(X-Y|X.Y)-(sonnet|opus|haiku) -> $3 X.Y
  const claudeOldMatch = /^claude-(\d+)[.-](\d+)-(sonnet|opus|haiku)$/.exec(m);
  if (claudeOldMatch) {
    return `${claudeOldMatch[3]} ${claudeOldMatch[1]}.${claudeOldMatch[2]}`;
  }
  // Claude single tier: claude-(sonnet|opus|haiku) -> $1
  const claudeTier = /^claude-(sonnet|opus|haiku)$/.exec(m);
  if (claudeTier) {
    return claudeTier[1];
  }

  // Gemini: gemini-X.Y-tier(-level)? -> gemini X.Y tier
  // e.g. gemini-3.8-flash-high -> gemini 3.8 flash, gemini-3.8-flash-medium -> gemini 3.8 flash
  // Strip -high, -medium, -low
  if (m.startsWith("gemini-")) {
    return m.replace(/-(high|medium|low)$/, "").replace(/-/g, " ");
  }

  // Codex / GPT: gpt-X.Y-codex -> gpt-X.Y, gpt-5.5 -> gpt-5.5
  if (m.startsWith("gpt-")) {
    return m.replace(/-(codex|preview)$/, "");
  }

  return m;
}

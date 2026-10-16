import {
  durationMs,
  type Timestamp,
  timestampDate,
  timestampMs,
} from "@bufbuild/protobuf/wkt";

import {
  Allowance,
  AzimaState,
  Choice,
  Closer,
  IfritState,
  IntegrationState,
  type KeptWorktree,
  type Lead,
  type Mark,
  MarkKind,
  type PlannedTask,
  type Project,
  type ProjectSync,
  Provider,
  PushStrategy,
  type Question,
  RoundKind,
  type Task,
  TaskKind,
  TaskRole,
  TaskStatus,
  type Wish,
  WishState,
} from "../../gen/ts/plan/v1/plan_pb";
import { useEffect, useState } from "react";

import { type TextKey, language, t } from "../i18n";
import { onStillness, still } from "../stillness";
import { ifritLit, ifritOut, ifritWord, isIfrit } from "./ifrit";

// A djinn grants three wishes at a time, never more. The lamp refuses a fourth; the page only says so beforehand.
export const MAX_ACTIVE = 3;

// firstLine is a text's first line of words, trimmed to 140 characters.
export function firstLine(text: string): string {
  const line =
    text
      .split("\n")
      .map((l) => l.replace(/^[#>*+\-\s]+/, "").trim())
      .find(Boolean) ?? "";
  return line.length > 140 ? `${line.slice(0, 139)}…` : line;
}

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

// askedWhen says when a question was asked, when it was last revised and when you put it off for later: the day and
// month in figures, the hour (asked 16/10 at 10:24 · revised at 10:31 · put off at 10:40), a later day said only when
// it is another. "" when it has no time.
export function askedWhen(question: Question): string {
  const asked = date(question.createTime);
  if (!asked) return "";
  const day = (d: Date) =>
    d.toLocaleDateString(language, { day: "2-digit", month: "2-digit" });
  const hour = (d: Date) =>
    d.toLocaleTimeString(language, { hour: "2-digit", minute: "2-digit" });
  const out = t("question.asked_at", { day: day(asked), time: hour(asked) });
  const last = [...(question.rounds ?? [])]
    .reverse()
    .find((r) => r.kind === RoundKind.REVISE);
  const revised = question.revision > 0 ? date(last?.createTime) : undefined;
  const later = date(question.laterTime);
  const words = (d: Date, at: TextKey, on: TextKey) =>
    d.toDateString() === asked.toDateString()
      ? t(at, { time: hour(d) })
      : t(on, { day: day(d), time: hour(d) });
  return [
    out,
    revised && words(revised, "question.revised_at", "question.revised_on"),
    later && words(later, "question.later_at", "question.later_on"),
  ]
    .filter(Boolean)
    .join(" · ");
}

// span says a length of time as a counter, two units at most: 45s, 1m1s, 1h1m, 3d4h.
export function span(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000));
  const m = Math.floor(s / 60);
  const h = Math.floor(m / 60);
  if (s < 60) return t("time.s", { s });
  if (m < 60)
    return s % 60 === 0 ? t("time.m", { m }) : t("time.ms", { m, s: s % 60 });
  if (h < 24)
    return m % 60 === 0 ? t("time.h", { h }) : t("time.hm", { h, m: m % 60 });
  return h % 24 === 0
    ? t("time.d", { d: Math.floor(h / 24) })
    : t("time.dh", { d: Math.floor(h / 24), h: h % 24 });
}

// taskRunDurations returns the durations in milliseconds of each of the task's runs.
export function taskRunDurations(task: Task, now: number): number[] {
  if (task.runs && task.runs.length > 0) {
    return task.runs.map((r) => {
      const rStart = r.startTime ? timestampMs(r.startTime) : 0;
      let rEnd: number;
      if (r.endTime) {
        rEnd = timestampMs(r.endTime);
      } else if (task.status === TaskStatus.RUNNING) {
        rEnd = now;
      } else if (task.endTime) {
        rEnd = timestampMs(task.endTime);
      } else {
        rEnd = rStart;
      }
      return Math.max(0, rEnd - rStart);
    });
  }
  if (task.startTime) {
    const rStart = timestampMs(task.startTime);
    let rEnd: number;
    if (taskFinished(task.status) || task.status === TaskStatus.PAUSED) {
      rEnd = task.endTime ? timestampMs(task.endTime) : rStart;
    } else if (task.status === TaskStatus.RUNNING) {
      rEnd = now;
    } else {
      rEnd = rStart;
    }
    return [Math.max(0, rEnd - rStart)];
  }
  if (task.running) {
    return [durationMs(task.running)];
  }
  return [];
}

// taskTime returns the card line and tooltip for a task.
// Card line: "<N> runs · <time> running · <time> waiting gate · <time> paused", each part shown only when not zero.
// Tooltip: the runs listed and wait times ("3 runs: 12m, 8m, 15m; paused 4m; waited 9m for a slot").
export function taskTime(
  task: Task,
  now: number,
): { text: string; title: string } {
  const firstStart = task.firstStartTime
    ? timestampMs(task.firstStartTime)
    : task.startTime
      ? timestampMs(task.startTime)
      : 0;
  if (!firstStart && (!task.runs || task.runs.length === 0)) {
    return { text: "", title: "" };
  }

  const runDurs = taskRunDurations(task, now);
  const runsCount =
    task.runs && task.runs.length > 0
      ? task.runs.length
      : firstStart > 0
        ? 1
        : 0;

  let totalRunMs = runDurs.reduce((a, b) => a + b, 0);
  if (totalRunMs === 0 && task.running) {
    totalRunMs =
      durationMs(task.running) +
      (task.waitingGate ? durationMs(task.waitingGate) : 0);
  }

  const waitingGateMs = task.waitingGate ? durationMs(task.waitingGate) : 0;
  const runningMs = Math.max(0, totalRunMs - waitingGateMs);

  let pausedMs: number;
  if (task.status !== TaskStatus.RUNNING && task.paused) {
    pausedMs = durationMs(task.paused);
  } else {
    const finalEnd =
      taskFinished(task.status) && task.endTime
        ? timestampMs(task.endTime)
        : now;
    const elapsedMs =
      firstStart > 0 && finalEnd > firstStart ? finalEnd - firstStart : 0;
    pausedMs = Math.max(0, elapsedMs - totalRunMs);
  }

  const lineParts: string[] = [];
  if (runsCount > 0) {
    lineParts.push(t("task.runs", { count: runsCount }));
  }
  if (runningMs > 0) {
    lineParts.push(t("task.time_running", { time: span(runningMs) }));
  }
  if (waitingGateMs > 0) {
    lineParts.push(t("task.time_waiting_gate", { time: span(waitingGateMs) }));
  }
  if (pausedMs > 0) {
    lineParts.push(t("task.time_paused", { time: span(pausedMs) }));
  }
  const text = lineParts.join(" · ");

  const tooltipParts: string[] = [];
  if (runDurs.length > 0) {
    const runsLabel = t("task.runs", { count: runsCount });
    tooltipParts.push(`${runsLabel}: ${runDurs.map(span).join(", ")}`);
  }
  if (pausedMs > 0) {
    tooltipParts.push(t("task.tooltip_paused", { time: span(pausedMs) }));
  }
  const waitBeforeStartMs = task.waitingBeforeStart
    ? durationMs(task.waitingBeforeStart)
    : 0;
  if (waitBeforeStartMs > 0) {
    const isDep =
      (task.dependsOn && task.dependsOn.length > 0) ||
      Boolean(task.waitReason && task.waitReason.includes("waits for"));
    tooltipParts.push(
      t(isDep ? "task.time_waited_deps" : "task.time_waited_slot", {
        time: span(waitBeforeStartMs),
      }),
    );
  }
  const activity = taskActivity(task, now);
  if (activity) {
    tooltipParts.push(activity);
  }
  const title = tooltipParts.join("; ");

  return { text, title };
}

// Silence limit (a task's seal): 15 minutes before the Lamplighter checks an idle running worker.
// Past half this limit (7.5 min), the UI highlights silence.
export const SILENCE_LIMIT_MS = 15 * 60 * 1000;
export const SILENCE_HOVER_MS = SILENCE_LIMIT_MS / 2;

// taskActivity returns the compact activity string for a running task on hover:
// "active <time> ago" or "silent for <N> min" when past half the silence limit.
export function taskActivity(task: Task, now: number): string | null {
  if (task.status !== TaskStatus.RUNNING) return null;
  const last = task.lastActivityTime
    ? timestampMs(task.lastActivityTime)
    : task.startTime
      ? timestampMs(task.startTime)
      : 0;
  if (!last) return null;
  const elapsed = Math.max(0, now - last);
  // A listening ifrit is silent by design: it waits for your next message.
  if (
    elapsed >= SILENCE_HOVER_MS &&
    task.ifrit?.state !== IfritState.LISTENING
  ) {
    const mins = Math.max(1, Math.round(elapsed / 60000));
    return t("task.silent_for", { count: mins });
  }
  return t("task.active_ago", { time: span(elapsed) });
}

// azimaTime is how long an azima runs or ran: summing its parts' running time.
export function azimaTime(
  azima: Task,
  parts: readonly Task[],
  now: number,
): { text: string; title: string } {
  let totalRunningMs = 0;
  if (parts.length > 0) {
    for (const p of parts) {
      if (p.kind === TaskKind.AZIMA) continue;
      const durs = taskRunDurations(p, now);
      const runMs = durs.reduce((a, b) => a + b, 0);
      const gateMs = p.waitingGate ? durationMs(p.waitingGate) : 0;
      totalRunningMs += Math.max(0, runMs - gateMs);
    }
  } else if (azima.running) {
    totalRunningMs = durationMs(azima.running);
  } else {
    const durs = taskRunDurations(azima, now);
    const runMs = durs.reduce((a, b) => a + b, 0);
    const gateMs = azima.waitingGate ? durationMs(azima.waitingGate) : 0;
    totalRunningMs = Math.max(0, runMs - gateMs);
  }
  if (totalRunningMs === 0) return { text: "", title: "" };
  const text = span(totalRunningMs);

  const allTasks = [azima, ...parts];
  const starts = allTasks
    .map((t) => ({ task: t, date: date(t.firstStartTime ?? t.startTime) }))
    .filter((x): x is { task: Task; date: Date } => !!x.date);
  if (starts.length === 0) return { text, title: "" };
  starts.sort((a, b) => a.date.getTime() - b.date.getTime());
  const firstStart = starts[0];

  const finished =
    azima.azima?.state === AzimaState.DONE || taskFinished(azima.status);
  if (finished) {
    const ends = allTasks
      .map((t) => ({ task: t, date: date(t.endTime) }))
      .filter((x): x is { task: Task; date: Date } => !!x.date);
    if (ends.length > 0) {
      ends.sort((a, b) => b.date.getTime() - a.date.getTime());
      const lastEnd = ends[0];
      return {
        text,
        title: t("task.ran", {
          from: when(
            firstStart.task.firstStartTime ?? firstStart.task.startTime,
          ),
          to: when(lastEnd.task.endTime),
        }),
      };
    }
  }

  return {
    text,
    title: t("task.running_since", {
      when: when(firstStart.task.firstStartTime ?? firstStart.task.startTime),
    }),
  };
}

// useNow is the time now, read again every period while on: a duration shown stays current. A window at rest
// (stillness.ts) stops the clock, and reads the time again as it wakes.
export function useNow(on: boolean, period = 1_000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!on) return;
    let id: ReturnType<typeof setInterval> | undefined;
    const tick = () => {
      clearInterval(id);
      id = undefined;
      setNow(Date.now());
      if (!still()) id = setInterval(() => setNow(Date.now()), period);
    };
    tick();
    const stop = onStillness(tick);
    return () => {
      stop();
      clearInterval(id);
    };
  }, [on, period]);
  return now;
}

// A wish stored before states is active.
export function isActive(wish: Wish): boolean {
  return (
    wish.state === WishState.ACTIVE || wish.state === WishState.UNSPECIFIED
  );
}

// hasLead tells whether Djinn has a resumable lead session or workspace.
export function hasLead(lead?: Lead | null): boolean {
  if (!lead || !lead.directory) return false;
  return Boolean(lead.sessionId || lead.provider === Provider.ANTIGRAVITY);
}

// noLead tells whether Djinn has no lead session: djinn tells no lead its answers, they wait in the wish's brief
// (plan.ErrNoLead).
export function noLead(lead?: Lead | null): boolean {
  return !hasLead(lead);
}

// principalWish returns the wish explicitly marked as principal, or the first active wish by rank.
export function principalWish(allWishes: readonly Wish[]): Wish | undefined {
  const activeWishes = allWishes.filter(isActive);
  const explicit = activeWishes.find((w) => w.principal);
  if (explicit) return explicit;
  const sorted = [...activeWishes].sort(
    (a, b) => (a.rank || 999) - (b.rank || 999),
  );
  return sorted[0];
}

// isPrincipal tells whether wish is the principal wish among all wishes.
export function isPrincipal(wish: Wish, allWishes: readonly Wish[]): boolean {
  return isActive(wish) && principalWish(allWishes)?.id === wish.id;
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
  [TaskStatus.CANCELLED]: "task.status_cancelled",
};

// taskStatusText names where a task stands: a task Djinn resumes once its provider's limit resets waits for the
// limit; one cut short and resumed as another task (forkedAs) says which; a running watcher watches, and a running
// investigator investigates.
// fixedBy is the code of the correction task that fixes its failed merge while it works (fixerOf): the merge shows
// being fixed, not failed.
export function taskStatusText(
  task: Pick<Task, "status" | "resumeAfter"> &
    Partial<Pick<Task, "provider" | "integration" | "role" | "kind" | "ifrit">>,
  forkedAs = "",
  fixedBy = "",
): string {
  if (watcherRuns(task)) return t("task.status_watching");
  if (ifritLit(task) || ifritOut(task)) return ifritWord(task);
  if (investigatorRuns(task)) return t("task.status_investigating");
  if (task.status === TaskStatus.RESUMING && task.resumeAfter)
    return t("task.status_limit");
  if (task.status === TaskStatus.INTERRUPTED && forkedAs)
    return t("task.status_forked", { task: forkedAs });
  if (task.status === TaskStatus.DONE) {
    switch (task.integration?.state) {
      case IntegrationState.PENDING:
      case IntegrationState.INTEGRATING:
        return t("task.status_merging");
      case IntegrationState.CONFLICT:
      case IntegrationState.RED:
        return fixedBy
          ? t("task.status_fixing", { task: fixedBy })
          : t("task.status_merge_failed");
      case IntegrationState.UNCOMMITTED:
        return t("task.status_to_review");
      default:
        return t("task.status_done");
    }
  }
  return t(taskStatusKeys[task.status]);
}

// cancelledText is what a cancelled task's badge says on hover, compact: who cancelled it, and why.
export function cancelledText(
  task: Partial<Pick<Task, "cancelled">>,
): string | undefined {
  const c = task.cancelled;
  if (!c) return undefined;
  const who = t(
    c.actor === Closer.DEVELOPER
      ? "task.cancelled_by_you"
      : "task.cancelled_by_lead",
  );
  const note = c.note.trim();
  return note ? `${who}: ${note}` : who;
}

// Tone is a state in the window's status language: each has its colour, its icon and its word (src/status.tsx).
export type Tone =
  | "done"
  | "running"
  | "waiting"
  | "investigating"
  | "planned"
  | "failed"
  // A merge that failed and that a correction task fixes: Djinn handles it, nothing to do (fixerOf).
  | "fixing"
  | "interrupted"
  | "stopped"
  // Cancelled: its work was discarded on purpose, never done.
  | "cancelled"
  | "paused"
  | "watching"
  | "human"
  | "proof"
  // An open question nothing waits for: it can wait.
  | "later"
  | "withdrawn"
  | "accent"
  // A lit ifrit (src/data/ifrit.ts): vermilion, with a flame; it may hold gates others wait for, and it calls you.
  | "ifrit"
  // An ifrit put out: the grey of what stopped, its flame outlined; a message lights it again.
  | "out";

// taskTone is a task's state in that language. A task Djinn resumes runs; one waiting for its provider's limit to
// reset holds still, as a paused one; one cut short and resumed as another task (forkedAs) is stopped, no alarm.
// None of them is the person's move. A running watcher has its own tone: it sleeps until its command prints. An
// investigator at work is violet, as Enlighten me that started it (investigatorRuns), not blue as the work. A merge
// that failed is red, unless a correction task fixes it while it works (fixedBy, fixerOf): orange, Djinn handles it.
export function taskTone(
  task: Pick<Task, "status" | "resumeAfter"> &
    Partial<Pick<Task, "provider" | "integration" | "role" | "kind" | "ifrit">>,
  forkedAs = "",
  fixedBy = "",
): Tone {
  const status = task.status;
  if (watcherRuns(task)) return "watching";
  if (ifritLit(task)) return "ifrit";
  if (ifritOut(task)) return "out";
  if (status === TaskStatus.RESUMING && task.resumeAfter) return "paused";
  if (status === TaskStatus.INTERRUPTED && forkedAs) return "stopped";
  switch (status) {
    case TaskStatus.RUNNING:
    case TaskStatus.RESUMING:
      return task.role === TaskRole.INVESTIGATOR ? "investigating" : "running";
    case TaskStatus.DONE:
      switch (task.integration?.state) {
        case IntegrationState.PENDING:
        case IntegrationState.INTEGRATING:
          return "running";
        case IntegrationState.CONFLICT:
        case IntegrationState.RED:
          return fixedBy ? "fixing" : "failed";
        case IntegrationState.UNCOMMITTED:
          return "waiting";
        default:
          return "done";
      }
    case TaskStatus.FAILED:
      return "failed";
    case TaskStatus.INTERRUPTED:
      return "interrupted";
    case TaskStatus.STOPPED:
      return "stopped";
    case TaskStatus.CANCELLED:
      return "cancelled";
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

// investigatorRuns tells whether the task is an investigator at work: the worker Djinn starts on a question after
// Enlighten me (TaskRole.INVESTIGATOR).
export function investigatorRuns(
  task: Pick<Task, "status"> & Partial<Pick<Task, "role">>,
): boolean {
  return (
    task.role === TaskRole.INVESTIGATOR && task.status === TaskStatus.RUNNING
  );
}

// isTaskMerging tells whether a done task's work is merging: waiting for its batch, or in it. It shows blue, as work
// that runs (taskTone), and counts with it (runningOf).
export function isTaskMerging(
  task: Pick<Task, "status"> & Partial<Pick<Task, "integration">>,
): boolean {
  if (task.status !== TaskStatus.DONE) return false;
  const s = task.integration?.state;
  return s === IntegrationState.PENDING || s === IntegrationState.INTEGRATING;
}

// mergeFailed tells a done task whose work failed to merge into its wish's integration branch: a conflict, or red
// checks.
function mergeFailed(
  task: Pick<Task, "status"> & Partial<Pick<Task, "integration">>,
): boolean {
  const s = task.integration?.state;
  return (
    task.status === TaskStatus.DONE &&
    (s === IntegrationState.CONFLICT || s === IntegrationState.RED)
  );
}

// fixerOf is the correction task that fixes a done task's failed merge while it works, among the wish's tasks: Djinn
// handles the failure, the window shows it orange. None, and the failure stays red, when no correction is under way:
// none was started, it failed, was stopped, cancelled or dropped, or the attempts were spent and Djinn asked the
// person (questionId). A correction done whose own merge failed in turn fixes it while a correction fixes that one.
type Fixable = Pick<Task, "status"> & Partial<Pick<Task, "id" | "integration">>;

export function fixerOf<T extends Fixable>(
  task: Fixable,
  tasks: readonly T[],
): T | undefined {
  const seen = new Set<string>();
  const of = (x: Fixable): T | undefined => {
    if (!mergeFailed(x)) return undefined;
    const id = x.integration?.correctedBy ?? "";
    if (!id || x.integration?.questionId || seen.has(id)) return undefined;
    seen.add(id);
    const fixer = tasks.find((other) => other.id === id);
    switch (fixer?.status) {
      case undefined:
      case TaskStatus.FAILED:
      case TaskStatus.STOPPED:
      case TaskStatus.CANCELLED:
      case TaskStatus.INTERRUPTED:
        return undefined;
      case TaskStatus.DONE:
        // Done with no work recorded, Djinn drops it as a correction; done with its merge failed, it waits for its own.
        if (!fixer.integration) return undefined;
        return !mergeFailed(fixer) || of(fixer) ? fixer : undefined;
      default:
        return fixer;
    }
  };
  return of(task);
}

// Running counts a wish's tasks whose process runs, apart: workers at work, investigators at work on a question after
// Enlighten me (investigatorRuns), and watchers that wait for their command to print. A watcher is not work in
// progress: "1 running" for a PR watched alone said a worker ran. Work that merges (isTaskMerging) is counted apart: no
// worker runs for it, and the load bar's count of workers never had it (W577: 6 workers and 2 merges waiting read as "8
// running" at a level that runs 6). Djinn is at work on it all the same: a wish's tone counts it as running. Work whose
// merge failed and that a correction fixes (fixerOf) is counted apart too: its correction runs, and counts with the
// running. A lit ifrit is counted apart as well (ifritLit): it stays lit while you are there, at work or listening.
export interface Running {
  running: number;
  investigating: number;
  watching: number;
  merging: number;
  fixing: number;
  ifrits: number;
}

export function runningOf(
  tasks: (Pick<Task, "status"> &
    Partial<
      Pick<Task, "id" | "provider" | "integration" | "role" | "kind" | "ifrit">
    >)[],
): Running {
  const ifrits = tasks.filter(ifritLit).length;
  const live = tasks.filter(
    (task) => task.status === TaskStatus.RUNNING && !isIfrit(task),
  );
  const watching = live.filter(watcherRuns).length;
  const investigating = live.filter(investigatorRuns).length;
  const merging = tasks.filter(isTaskMerging).length;
  const fixing = tasks.filter(
    (task) => mergeFailed(task) && fixerOf(task, tasks),
  ).length;
  return {
    running: live.length - watching - investigating,
    investigating,
    watching,
    merging,
    fixing,
    ifrits,
  };
}

export function taskFinished(status: TaskStatus): boolean {
  return (
    status === TaskStatus.DONE ||
    status === TaskStatus.FAILED ||
    status === TaskStatus.STOPPED ||
    status === TaskStatus.INTERRUPTED ||
    status === TaskStatus.CANCELLED
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

// planOf is the task a question's option launches once picked (decision Q25), set by the asker; none for an option
// without a plan. A message built by hand (a test) may leave the list out.
export function planOf(
  question: Question,
  choice: Choice | undefined,
): PlannedTask | undefined {
  if (choice === undefined || choice === Choice.UNSPECIFIED) return undefined;
  return (question.plans ?? []).find((p) => p.option === choice);
}

// launchedPlan is the task an answer launched from its option's plan, by Djinn or the lead: its identifier, once
// launched.
export function launchedPlan(question: Question): PlannedTask | undefined {
  const plan = planOf(question, question.answer?.choice);
  return plan?.taskId ? plan : undefined;
}

export function isOpen(question: Question): boolean {
  return !question.answer && !question.withdrawal;
}

export function isWithdrawn(question: Question): boolean {
  return !!question.withdrawal;
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
  return (
    !question.answer &&
    !question.withdrawal &&
    last?.kind === RoundKind.ENLIGHTEN
  );
}

// A version a question had before its last revision.
export interface QuestionVersion {
  // 1 for the question as it was asked.
  number: number;
  // When it was asked, or revised into this version.
  time?: Timestamp;
  context: string;
  options: string[];
  recommendation: string;
  // What you asked to investigate about it, in order.
  asked: string[];
}

// questionVersions are a question's earlier versions, the first asked first, as its rounds keep them: each revision's
// round holds the version it replaced. The question itself is its current version, never among them.
export function questionVersions(question: Question): QuestionVersion[] {
  const out: QuestionVersion[] = [];
  let time = question.createTime;
  let asked: string[] = [];
  for (const round of question.rounds ?? []) {
    if (round.kind === RoundKind.ENLIGHTEN) {
      if (round.note.trim()) asked.push(round.note.trim());
    } else if (round.kind === RoundKind.REVISE) {
      out.push({
        number: out.length + 1,
        time,
        context: round.context,
        options: round.options,
        recommendation: round.recommendation,
        asked,
      });
      time = round.createTime;
      asked = [];
    }
  }
  return out;
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

// wishTone is a wish's state in the status language: what it needs first. A lit ifrit comes before what waits: it is
// there for you, and calls you.
export function wishTone(
  wish: Wish,
  questions: number,
  running: number,
  watching = 0,
  investigating = 0,
  ifrits = 0,
): Tone {
  if (wish.state === WishState.GRANTED) return "done";
  if (wish.state === WishState.PAUSED) return "paused";
  if (ifrits > 0) return "ifrit";
  if (questions > 0 || wish.ready) return "waiting";
  if (running > 0) return "running";
  if (investigating > 0) return "investigating";
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

// defaultModel is the model a provider runs when no model was requested; empty when it has none.
export function defaultModel(provider?: Provider): string {
  switch (provider) {
    case Provider.CLAUDE:
    case Provider.UNSPECIFIED:
    case undefined:
      return "claude-sonnet-5-5";
    case Provider.ANTIGRAVITY:
      return "gemini-3.8-flash-high";
    case Provider.CODEX:
      return "gpt-5.5";
    default:
      return "";
  }
}

// In-memory alias registry: alias -> model id (e.g. "sonnet" -> "claude-sonnet-5-5")
const modelAliases = new Map<string, string>();

export function isAlias(model: string): boolean {
  if (!model) return false;
  const trimmed = model.trim();
  return !/\d/.test(trimmed);
}

export function normalizeAlias(alias: string): string {
  let a = alias.trim().toLowerCase();
  a = a.replace(/^[a-z0-9-._]+\//, "");
  a = a.replace(/^claude-/, "");
  return a;
}

export function recordModelAlias(alias: string, model: string): void {
  if (!alias || !model) return;
  const a = normalizeAlias(alias);
  modelAliases.set(a, model.trim());
  modelAliases.set(alias.trim().toLowerCase(), model.trim());
}

export function resolveModelAlias(alias: string): string | undefined {
  if (!alias) return undefined;
  const a = normalizeAlias(alias);
  return modelAliases.get(a) ?? modelAliases.get(alias.trim().toLowerCase());
}

export function clearModelAliases(): void {
  modelAliases.clear();
}

export function learnAliasesFromModel(model: string): void {
  if (!model || isAlias(model)) return;
  const m = model.toLowerCase();
  for (const tier of ["sonnet", "opus", "haiku"]) {
    if (m.includes(tier)) {
      recordModelAlias(tier, model);
      recordModelAlias(`claude-${tier}`, model);
    }
  }
}

export interface TaskModelDisplay {
  label: string;
  title: string;
}

// taskModelDisplay computes the model badge to show on a task row:
// - watchers and azimas show none.
// - running and paused tasks show the model they run.
// - finished tasks keep showing the model they ran (the last one if several, all in tooltip).
// - planned and waiting tasks forecast the model they will run (requested, settings, or provider default).
export function taskModelDisplay(
  task: Task,
  project?:
    Project | { settings?: { model?: string; provider?: Provider } } | null,
): TaskModelDisplay | null {
  if (
    task.provider === Provider.WATCH ||
    watcherRuns(task) ||
    task.kind === TaskKind.AZIMA ||
    task.azima != null
  ) {
    return null;
  }

  if (task.status === TaskStatus.RUNNING || task.status === TaskStatus.PAUSED) {
    if (!task.model) return null;
    if (!isAlias(task.model)) {
      learnAliasesFromModel(task.model);
    }
    return {
      label: shortModel(task.model),
      title: task.model,
    };
  }

  if (taskFinished(task.status)) {
    const modelsList: string[] = [];
    if (task.models && task.models.length > 0) {
      modelsList.push(...task.models);
    }
    if (task.model && !modelsList.includes(task.model)) {
      modelsList.push(task.model);
    }
    const distinct: string[] = [];
    for (const m of modelsList) {
      const trimmed = m.trim();
      if (trimmed && !distinct.includes(trimmed)) {
        distinct.push(trimmed);
        if (!isAlias(trimmed)) {
          learnAliasesFromModel(trimmed);
        }
      }
    }
    const lastModel =
      distinct.length > 0
        ? distinct[distinct.length - 1]
        : task.model?.trim() || "";
    if (!lastModel) return null;
    return {
      label: shortModel(lastModel),
      title: distinct.length > 1 ? distinct.join(", ") : lastModel,
    };
  }

  // Planned or waiting tasks
  const providerDef = defaultModel(task.provider);
  let requested = task.model?.trim() || "";
  let isDefault = false;

  const projectSettings =
    project && "settings" in project
      ? (project as { settings?: { model?: string; provider?: Provider } })
          .settings
      : undefined;

  if (!requested) {
    if (
      projectSettings?.model?.trim() &&
      (!projectSettings.provider || projectSettings.provider === task.provider)
    ) {
      requested = projectSettings.model.trim();
    } else if (providerDef) {
      requested = providerDef;
      isDefault = true;
    }
  } else if (
    requested === providerDef &&
    (!projectSettings?.model ||
      projectSettings.model.trim() !== requested ||
      (projectSettings.provider && projectSettings.provider !== task.provider))
  ) {
    isDefault = true;
  }

  if (!requested) return null;

  if (isDefault) {
    return {
      label: `${shortModel(requested)} · default`,
      title: requested,
    };
  }

  if (isAlias(requested)) {
    const resolved = resolveModelAlias(requested);
    if (resolved) {
      return {
        label: `${shortModel(resolved)} · alias`,
        title: resolved,
      };
    }
    return {
      label: `${shortModel(requested)} · version unknown`,
      title: requested,
    };
  }

  return {
    label: shortModel(requested),
    title: requested,
  };
}

// syncDescription says how the integration branch stands against its remote target.
export function syncDescription(sync?: ProjectSync): string {
  if (!sync) return "";
  const remote = sync.remote || "origin";
  const target = sync.target || sync.branch || "";
  if (sync.ahead > 0 && sync.behind > 0) {
    return t("project.sync_ahead_behind", {
      ahead: sync.ahead,
      behind: sync.behind,
      remote,
      target,
    });
  }
  if (sync.ahead > 0) {
    return t("project.sync_ahead", {
      count: sync.ahead,
      remote,
      target,
    });
  }
  if (sync.behind > 0) {
    return t("project.sync_behind", {
      count: sync.behind,
      remote,
      target,
    });
  }
  return "";
}

// slug keeps lower-case letters and digits, joined by single dashes, at most n characters cut on a dash.
export function slug(s: string, n = 40): string {
  const folded = s
    .normalize("NFD")
    .replace(/[\u0300-\u036f]/g, "")
    .toLowerCase();
  let res = "";
  let dash = false;
  for (let i = 0; i < folded.length; i++) {
    const ch = folded[i];
    if ((ch >= "a" && ch <= "z") || (ch >= "0" && ch <= "9")) {
      if (dash && res.length > 0) {
        res += "-";
      }
      res += ch;
      dash = false;
    } else {
      dash = true;
    }
  }
  if (res.length > n) {
    res = res.slice(0, n);
    const lastDash = res.lastIndexOf("-");
    if (lastDash > 0) {
      res = res.slice(0, lastDash);
    }
  }
  return res.replace(/^-+|-+$/g, "");
}

// azimaBranchName gives the integration branch of an azima: djinn/<code-slug> or djinn/<code>.
export function azimaBranchName(
  azima?: { code?: string; title?: string } | null,
): string {
  if (!azima || !azima.code) return "";
  const s = slug(azima.title || "", 40);
  if (s) {
    return `djinn/${azima.code}-${s}`;
  }
  return `djinn/${azima.code}`;
}

// effectivePushStrategy resolves the push strategy for a wish, falling back to its projects or WISH.
export function effectivePushStrategy(
  wish?: Wish,
  projects?: readonly { pushStrategy?: PushStrategy }[],
): PushStrategy {
  if (wish?.pushStrategy) {
    return wish.pushStrategy;
  }
  for (const p of projects ?? []) {
    if (p.pushStrategy) {
      return p.pushStrategy;
    }
  }
  return PushStrategy.WISH;
}

// gitReason is a reason Djinn wrote, in the window's language when it is git refusing to sign a merge for want of a
// name and an email (noIdentity, internal/harness/worktree.go): the git config to run. Any other stays as written.
export function gitReason(reason: string): string {
  return reason.startsWith("git has no identity")
    ? t("work.no_identity")
    : reason;
}

// What the window reads of an ifrit (T51): a worker that stays lit while the developer is there, its process waiting on
// its input between two messages. Whether it is lit, the word of where it stands (never its colour alone), how long
// before it goes out once Djinn warned, the ports of its serves as links, the gates it holds, and the gate another task
// waits for while an ifrit holds it.
import { type Timestamp, timestampMs } from "@bufbuild/protobuf/wkt";

import type { Gate } from "../../gen/ts/machine/v1/machine_pb";
import {
  IfritState,
  ServeState,
  type Task,
  TaskKind,
  TaskStatus,
} from "../../gen/ts/plan/v1/plan_pb";
import { language, t } from "../i18n";

type Ifrit = Pick<Task, "status"> & Partial<Pick<Task, "kind" | "ifrit">>;

const ms = (ts?: Timestamp) => (ts ? timestampMs(ts) : 0);

export function isIfrit(task: Partial<Pick<Task, "kind">>): boolean {
  return task.kind === TaskKind.IFRIT;
}

// ifritLit tells an ifrit whose worker runs: at work on a turn, or listening between two.
export function ifritLit(task: Ifrit): boolean {
  return (
    isIfrit(task) &&
    (task.status === TaskStatus.RUNNING ||
      task.status === TaskStatus.RESUMING) &&
    task.ifrit?.state !== IfritState.OUT
  );
}

// ifritOut tells an ifrit Djinn put out, the developer gone or Djinn restarted: a message lights it again. One done,
// stopped by hand or cancelled is no longer out: it ended.
export function ifritOut(task: Ifrit): boolean {
  return (
    isIfrit(task) &&
    task.ifrit?.state === IfritState.OUT &&
    !ifritLit(task) &&
    task.status !== TaskStatus.DONE &&
    task.status !== TaskStatus.CANCELLED
  );
}

// IfritPhase is where an ifrit stands, as its word says it: at work on a turn, lit and listening, going out soon once
// Djinn warned, or out.
export type IfritPhase = "working" | "listening" | "warned" | "out";

// ifritWarned tells a lit ifrit Djinn warned it puts out soon: its warning time has come, its out time not yet.
export function ifritWarned(task: Ifrit, now: number): boolean {
  const warn = ms(task.ifrit?.warnTime);
  return ifritLit(task) && warn > 0 && warn <= now;
}

export function ifritPhase(task: Ifrit, now: number): IfritPhase | undefined {
  if (ifritOut(task)) return "out";
  if (!ifritLit(task)) return undefined;
  if (ifritWarned(task, now)) return "warned";
  return task.ifrit?.state === IfritState.LISTENING ? "listening" : "working";
}

// ifritMinutesLeft is how many minutes, rounded up, before Djinn puts a lit ifrit out; 0 once due or unknown.
export function ifritMinutesLeft(task: Ifrit, now: number): number {
  const out = ms(task.ifrit?.outTime);
  return out > now ? Math.ceil((out - now) / 60_000) : 0;
}

// ifritWord is the word of where an ifrit stands: lit, at work, going out in 4 min, out. Without a clock (now 0) it
// never counts down: what a row says that does not tick.
export function ifritWord(task: Ifrit, now = 0): string {
  switch (ifritPhase(task, now)) {
    case "out":
      return t("ifrit.state_out");
    case "warned":
      return t("ifrit.state_warned", {
        count: Math.max(1, ifritMinutesLeft(task, now)),
      });
    case "listening":
      return t("ifrit.state_lit");
    default:
      return t("ifrit.state_working");
  }
}

// IfritLink is a port one of its serves listens on, as a link: http://localhost:5173/, named front.
export interface IfritLink {
  url: string;
  // What it shows: localhost:5173.
  host: string;
  // Its name, as declared, else its serve's.
  label: string;
  // It answered at the last check: one that does not is greyed.
  open: boolean;
}

// ifritLinks lists the ports of its serves that run, as links, in the order they were started and declared.
export function ifritLinks(task: Partial<Pick<Task, "ifrit">>): IfritLink[] {
  const out: IfritLink[] = [];
  for (const serve of task.ifrit?.serves ?? []) {
    if (
      serve.state !== ServeState.RUNNING &&
      serve.state !== ServeState.STARTING
    )
      continue;
    for (const port of serve.ports) {
      if (out.some((l) => l.host === `localhost:${port.number}`)) continue;
      out.push({
        url: `http://localhost:${port.number}/`,
        host: `localhost:${port.number}`,
        label: port.label || serve.name,
        open: port.open,
      });
    }
  }
  return out;
}

// ifritGates names the gates a lit ifrit holds: those Djinn lists held by its task, and those its running serves hold.
export function ifritGates(
  task: Pick<Task, "id"> & Partial<Pick<Task, "ifrit">>,
  gates: readonly Gate[],
): string[] {
  const out = gates
    .filter((g) => g.holder && g.holderTaskId === task.id)
    .map((g) => g.name);
  for (const serve of task.ifrit?.serves ?? [])
    if (
      serve.gate &&
      (serve.state === ServeState.RUNNING ||
        serve.state === ServeState.STARTING) &&
      !out.includes(serve.gate.toLowerCase())
    )
      out.push(serve.gate.toLowerCase());
  return out.sort();
}

// IfritWait is a gate a task waits for while an ifrit holds it: the ifrit gives it back when it goes out, at its
// expire time at the latest, or when it ends.
export interface IfritWait {
  gate: string;
  // Who holds it, as Djinn names them: "W12 (Front of dolmen): task run-back".
  holder: string;
  // When it is given back at the latest: the hour, "" when Djinn does not say.
  release: string;
}

// ifritWaitOf is the gate held by an ifrit that the task waits for, if any. Djinn names each waiter by its code and
// title, then what it runs (gate.Gates, Describe).
export function ifritWaitOf(
  task: Pick<Task, "code" | "title">,
  gates: readonly Gate[],
): IfritWait | undefined {
  const who = `${task.code} (${task.title})`;
  const gate = gates.find(
    (g) =>
      g.ifrit &&
      g.holder &&
      g.waiting.some((w) => w === who || w.startsWith(`${who}:`)),
  );
  if (!gate) return undefined;
  const release = ms(gate.expireTime);
  return {
    gate: gate.name,
    holder: gate.holder,
    release: release
      ? new Date(release).toLocaleTimeString(language, {
          hour: "2-digit",
          minute: "2-digit",
        })
      : "",
  };
}

// litIfrits lists the lit ifrits among items, the one with the latest activity first: the one the developer works with.
export function litIfrits<T extends { item: Task }>(items: readonly T[]): T[] {
  return items
    .filter(({ item }) => ifritLit(item))
    .sort(
      (a, b) =>
        ms(b.item.lastActivityTime) - ms(a.item.lastActivityTime) ||
        (a.item.id < b.item.id ? -1 : a.item.id > b.item.id ? 1 : 0),
    );
}

// ifritsLit counts the lit ifrits among tasks.
export function ifritsLit(tasks: readonly Task[]): number {
  return tasks.filter(ifritLit).length;
}
